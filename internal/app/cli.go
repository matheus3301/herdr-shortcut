package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/matheus3301/herdr-shortcut/internal/buildinfo"
	"github.com/matheus3301/herdr-shortcut/internal/herdr"
	"github.com/matheus3301/herdr-shortcut/internal/tui"
)

const usage = `herdr-shortcut ` + buildinfo.Version + ` — Shortcut task picker and coding-agent launcher for Herdr

Usage:
  herdr-shortcut <command>

Commands:
  open       Open or focus the Shortcut task-picker popup (Herdr plugin action)
  tui        Run the interactive task picker (Herdr pane entrypoint)
  doctor     Validate configuration, credentials, Shortcut access, and Herdr
  version    Print the version
  help       Show this help

Flags:
  -h, --help       Show this help
  -v, --version    Print the version

Configuration is loaded from $HERDR_PLUGIN_CONFIG_DIR/config.toml,
$XDG_CONFIG_HOME/herdr-shortcut/config.toml, or
$HOME/.config/herdr-shortcut/config.toml. The Shortcut token comes from the
SHORTCUT_API_TOKEN environment variable or a configured token_command.
`

// Main dispatches a command and returns the process exit code.
func Main(env Environment) int {
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}
	if len(env.Args) == 0 {
		fmt.Fprint(env.Stderr, usage)
		return 2
	}
	cmd := env.Args[0]
	rest := env.Args[1:]
	switch cmd {
	case "help", "--help", "-h":
		fmt.Fprint(env.Stdout, usage)
		return 0
	case "version", "--version", "-v":
		if code, handled := checkArity(env, cmd, rest); handled {
			return code
		}
		fmt.Fprintf(env.Stdout, "%s %s\n", buildinfo.Name, buildinfo.Version)
		return 0
	case "open":
		if code, handled := checkArity(env, cmd, rest); handled {
			return code
		}
		if err := cmdOpen(env); err != nil {
			fmt.Fprintf(env.Stderr, "herdr-shortcut open: %v\n", err)
			return 1
		}
		return 0
	case "tui":
		if code, handled := checkArity(env, cmd, rest); handled {
			return code
		}
		return runTUICommand(env)
	case "doctor":
		if code, handled := checkArity(env, cmd, rest); handled {
			return code
		}
		return runDoctor(env)
	default:
		fmt.Fprintf(env.Stderr, "herdr-shortcut: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

// checkArity enforces that a subcommand takes no positional arguments and
// handles a local -h/--help flag. It returns (exitCode, handled): when handled
// is true the caller should return exitCode immediately.
func checkArity(env Environment, cmd string, rest []string) (int, bool) {
	for _, a := range rest {
		if a == "-h" || a == "--help" {
			fmt.Fprint(env.Stdout, usage)
			return 0, true
		}
	}
	if len(rest) > 0 {
		fmt.Fprintf(env.Stderr, "herdr-shortcut %s takes no arguments (got %q)\n\n%s", cmd, strings.Join(rest, " "), usage)
		return 2, true
	}
	return 0, false
}

// cmdOpen invokes `plugin pane open` through HERDR_BIN_PATH. A popup targets the
// active pane, so Herdr rejects workspace_id/target_pane_id — the only context
// carried directly is the working directory (focused pane, else workspace). The
// popup regenerates its own plugin context (pointing at the popup pane), so the
// immutable original invocation context (workspace id, workspace cwd, focused
// pane cwd) is forwarded via custom --env variables the popup then prefers.
func cmdOpen(env Environment) error {
	ctxInfo, err := herdr.ContextFromEnv(env.Getenv)
	if err != nil {
		return err
	}
	cwd := ctxInfo.FocusedPaneCwd
	if cwd == "" {
		cwd = ctxInfo.WorkspaceCwd
	}
	return newHerdr(env).OpenPane(context.Background(), herdr.OpenPaneParams{
		PluginID:   PluginID,
		Entrypoint: PaneEntrypoint,
		Placement:  "popup",
		Cwd:        cwd,
		Env:        ctxInfo.ActionEnv(),
	})
}

func runTUICommand(env Environment) int {
	a, err := newApp(env)
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-shortcut: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Discover the supported harness kinds up front; a discovery failure is an
	// actionable error, never a silent fallback.
	kinds, err := a.herdr.AgentKinds(ctx)
	if err != nil {
		fmt.Fprintf(env.Stderr, "herdr-shortcut: could not discover agent kinds from Herdr: %v\n", err)
		return 1
	}
	a.kinds = kinds

	// The configured default kind must be one this Herdr actually reports.
	// Validate it immediately after discovery: an unsupported default is a hard
	// error, never silently replaced with the first kind or any other fallback.
	if err := a.validateKind(a.cfg.Agent.DefaultKind); err != nil {
		fmt.Fprintf(env.Stderr, "herdr-shortcut: %v\n", err)
		return 1
	}

	run := env.RunTUI
	if run == nil {
		run = tui.Run
	}
	if _, err := run(a.tuiDeps(ctx)); err != nil {
		fmt.Fprintf(env.Stderr, "herdr-shortcut: %v\n", err)
		return 1
	}
	return 0
}

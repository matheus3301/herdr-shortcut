package app

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/matheus3301/herdr-shortcut/internal/config"
)

const doctorTimeout = 30 * time.Second

// minHerdrVersion is the minimum Herdr version the plugin supports.
const minHerdrVersion = "0.7.5"

// runDoctor validates configuration, token resolution, Shortcut authentication,
// Herdr capability (>= 0.7.5 with the plugin command), the active workspace, the
// discovered agent-harness kinds (and that the configured default is among them),
// and configured repositories. It never prints secret values and never reports
// overall success from configuration alone: every required capability must be
// independently verified.
func runDoctor(env Environment) int {
	a, err := newApp(env)
	if err != nil {
		fmt.Fprintf(env.Stderr, "[fail] Configuration: %v\n", err)
		return 1
	}
	out := env.Stdout
	ok := true
	report := func(status, msg string) { fmt.Fprintf(out, "%-7s %s\n", status, msg) }
	markFail := func() { ok = false }

	source := a.cfg.SourcePath
	if source == "" {
		source = "built-in defaults (no config file)"
	}
	report("[ok]", "Configuration: valid — "+source)

	ctx, cancel := context.WithTimeout(context.Background(), doctorTimeout)
	defer cancel()

	token, tokenSrc, tErr := a.tokenResolver().ResolveToken(ctx, a.cfg.Shortcut.TokenCommand)
	if tErr != nil {
		report("[fail]", "Shortcut token: "+tErr.Error())
		markFail()
	} else {
		report("[ok]", "Shortcut token: resolved via "+string(tokenSrc))
		if !a.checkShortcutAuth(ctx, token, report) {
			markFail()
		}
	}

	if !a.checkHerdr(ctx, report) {
		markFail()
	}
	if !a.checkKinds(ctx, report) {
		markFail()
	}
	if !a.checkRepositories(report) {
		markFail()
	}

	if ok {
		fmt.Fprintln(out, "\nAll checks passed.")
		return 0
	}
	fmt.Fprintln(out, "\nSome checks failed; see above.")
	return 1
}

func (a *App) checkShortcutAuth(ctx context.Context, token string, report func(string, string)) bool {
	client, err := a.buildClient(token)
	if err != nil {
		report("[fail]", "Shortcut client: "+err.Error())
		return false
	}
	member, err := client.CurrentMember(ctx)
	if err != nil {
		report("[fail]", "Shortcut authentication: "+err.Error())
		return false
	}
	report("[ok]", "Shortcut authentication: @"+member.MentionName)
	return true
}

// checkHerdr verifies the Herdr binary is present, is at least the minimum
// version, exposes the plugin command, and that an active workspace can be
// resolved. All are required.
func (a *App) checkHerdr(ctx context.Context, report func(string, string)) bool {
	ok := true
	version, err := a.herdr.Version(ctx)
	if err != nil {
		report("[fail]", "Herdr: not available via "+a.herdr.Bin()+" ("+err.Error()+")")
		return false
	}
	if !versionAtLeast(version, minHerdrVersion) {
		report("[fail]", fmt.Sprintf("Herdr: version %s is older than the required %s", version, minHerdrVersion))
		ok = false
	} else {
		report("[ok]", "Herdr: version "+version)
	}
	if !a.herdr.PluginCommandAvailable(ctx) {
		report("[fail]", "Herdr: the 'plugin' command is missing; install the official Herdr "+minHerdrVersion)
		ok = false
	} else {
		report("[ok]", "Herdr: plugin command available")
	}

	// The workspace requires a running Herdr; resolving it confirms integration.
	if a.ctxInfo.WorkspaceID != "" {
		report("[ok]", "Herdr workspace: "+a.ctxInfo.WorkspaceID+" (from plugin context)")
	} else if ws, werr := a.herdr.ActiveWorkspaceID(ctx); werr == nil {
		report("[ok]", "Herdr workspace: "+ws+" (active)")
	} else {
		report("[fail]", "Herdr workspace: none available — run inside a Herdr session ("+werr.Error()+")")
		ok = false
	}
	return ok
}

// checkKinds discovers the supported agent-harness kinds and confirms the
// configured default kind is among them. It is non-destructive (no agent is
// started).
func (a *App) checkKinds(ctx context.Context, report func(string, string)) bool {
	kinds, err := a.herdr.AgentKinds(ctx)
	if err != nil {
		report("[fail]", "Agent kinds: discovery failed: "+err.Error())
		return false
	}
	report("[ok]", "Agent kinds: "+strings.Join(kinds, ", "))
	for _, k := range kinds {
		if k == a.cfg.Agent.DefaultKind {
			report("[ok]", "Default kind: "+a.cfg.Agent.DefaultKind+" (supported)")
			return true
		}
	}
	report("[fail]", fmt.Sprintf("Default kind %q is not among the supported kinds", a.cfg.Agent.DefaultKind))
	return false
}

func (a *App) checkRepositories(report func(string, string)) bool {
	ok := true
	if len(a.cfg.Repositories) == 0 {
		report("[ok]", "Repositories: none configured (dialog uses Herdr cwd and custom paths)")
		return true
	}
	for _, r := range a.cfg.Repositories {
		path, err := config.ExpandPath(r.Path, a.env.Getenv)
		if err != nil {
			report("[fail]", fmt.Sprintf("Repository %q: %v", r.Name, err))
			ok = false
			continue
		}
		info, err := os.Stat(path)
		switch {
		case err != nil:
			report("[fail]", fmt.Sprintf("Repository %q: %v", r.Name, err))
			ok = false
		case !info.IsDir():
			report("[fail]", fmt.Sprintf("Repository %q: not a directory: %s", r.Name, path))
			ok = false
		default:
			report("[ok]", fmt.Sprintf("Repository %q: %s", r.Name, path))
		}
	}
	return ok
}

// versionAtLeast reports whether dotted numeric version v >= min.
func versionAtLeast(v, min string) bool {
	pv, pm := parseSemver(v), parseSemver(min)
	for i := 0; i < 3; i++ {
		if pv[i] != pm[i] {
			return pv[i] > pm[i]
		}
	}
	return true
}

func parseSemver(s string) [3]int {
	var out [3]int
	parts := strings.SplitN(s, ".", 3)
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(strings.TrimFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' }))
		out[i] = n
	}
	return out
}

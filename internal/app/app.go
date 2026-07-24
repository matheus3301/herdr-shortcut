// Package app wires configuration, the Shortcut client, the prompt renderer,
// the Herdr adapter, and the TUI into the herdr-shortcut command-line surface.
package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/matheus3301/herdr-shortcut/internal/browser"
	"github.com/matheus3301/herdr-shortcut/internal/config"
	"github.com/matheus3301/herdr-shortcut/internal/herdr"
	"github.com/matheus3301/herdr-shortcut/internal/prompt"
	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
	"github.com/matheus3301/herdr-shortcut/internal/tmpl"
	"github.com/matheus3301/herdr-shortcut/internal/tui"
)

// Plugin identifiers. These must match herdr-plugin.toml (enforced by tests).
const (
	PluginID       = "matheus3301.shortcut"
	PaneEntrypoint = "tasks"
	ActionOpen     = "open"
)

// Environment holds process inputs and injectable dependencies so the whole CLI
// is testable without a real network, Herdr session, or coding-agent account.
type Environment struct {
	Getenv func(string) string
	Args   []string
	Stdout io.Writer
	Stderr io.Writer

	// Optional injectables (defaults are used when nil).
	HTTPClient  *http.Client
	HerdrRunner herdr.Runner
	TokenRunner config.CommandRunner
	Now         func() time.Time
	// Sleep is an injectable context-aware delay used while a newly created pane
	// finishes shell startup. It defaults to a real timer.
	Sleep   func(context.Context, time.Duration) error
	Browser *browser.Opener
	// RunTUI runs the interactive program; defaults to tui.Run.
	RunTUI func(tui.Deps) (tui.Model, error)
	// DirValidator revalidates/canonicalizes a selected cwd immediately before
	// tab creation; defaults to canonicalDir (real filesystem).
	DirValidator func(path string) (string, error)
}

func (e Environment) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

func (e Environment) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// App is a fully constructed application for config-backed commands.
type App struct {
	env     Environment
	cfg     config.Config
	herdr   *herdr.Herdr
	ctxInfo herdr.InvocationContext
	prompt  *prompt.Prompt
	// kinds are the agent-harness kinds discovered from the installed Herdr
	// binary; populated before the picker runs.
	kinds []string
}

// newApp loads configuration and builds the shared dependencies. Configuration
// and template errors surface here so config-backed commands fail early.
func newApp(env Environment) (*App, error) {
	cfg, err := config.Load(env.Getenv)
	if err != nil {
		return nil, err
	}
	compiled, err := prompt.Compile(cfg.Agent.PromptTemplate)
	if err != nil {
		return nil, fmt.Errorf("agent.prompt_template: %w", err)
	}
	ctxInfo, err := herdr.ContextFromEnv(env.Getenv)
	if err != nil {
		return nil, err
	}
	return &App{
		env:     env,
		cfg:     cfg,
		herdr:   newHerdr(env),
		ctxInfo: ctxInfo,
		prompt:  compiled,
	}, nil
}

func newHerdr(env Environment) *herdr.Herdr {
	return herdr.New(env.getenv("HERDR_BIN_PATH"), env.HerdrRunner)
}

func (a *App) tokenResolver() config.TokenResolver {
	return config.TokenResolver{Getenv: a.env.Getenv, Runner: a.env.TokenRunner}
}

func (a *App) buildClient(token string) (*shortcut.Client, error) {
	return shortcut.New(shortcut.Options{
		BaseURL:    a.cfg.Shortcut.APIBaseURL,
		Token:      token,
		HTTPClient: a.env.HTTPClient,
		Timeout:    a.cfg.Shortcut.RequestTimeout,
	})
}

func (a *App) browserOpener() browser.Opener {
	if a.env.Browser != nil {
		return *a.env.Browser
	}
	return browser.Opener{}
}

// expandedRepositories returns the configured repositories with paths expanded
// for display and selection in the dialog.
func (a *App) expandedRepositories() []tui.Repository {
	repos := make([]tui.Repository, 0, len(a.cfg.Repositories))
	for _, r := range a.cfg.Repositories {
		path, err := config.ExpandPath(r.Path, a.env.Getenv)
		if err != nil {
			continue
		}
		repos = append(repos, tui.Repository{Name: r.Name, Path: path})
	}
	return repos
}

// renderIDKindTemplate renders a validated {id}/{kind} template.
func renderIDKindTemplate(tplText string, id int64, kind string) string {
	t, err := tmpl.Parse(tplText, []string{"id", "kind"})
	if err != nil {
		return ""
	}
	return t.Render(map[string]string{"id": strconv.FormatInt(id, 10), "kind": kind})
}

func (a *App) tabLabel(id int64, kind string) string {
	return renderIDKindTemplate(a.cfg.Agent.TabLabelTemplate, id, kind)
}

func (a *App) agentBaseName(id int64, kind string) string {
	return herdr.SanitizeAgentName(renderIDKindTemplate(a.cfg.Agent.NameTemplate, id, kind))
}

// promptData builds prompt input from a fresh Story, its resolved state, and the
// selected harness kind.
func promptData(s shortcut.Story, state shortcut.StoryState, kind string) prompt.Data {
	return prompt.Data{
		ID:          int(s.ID),
		Name:        s.Name,
		URL:         s.AppURL,
		Description: s.Description,
		StoryType:   s.StoryType,
		State:       state.Name,
		Labels:      s.LabelNames(),
		Estimate:    s.Estimate,
		Deadline:    s.Deadline,
		BranchName:  s.BranchName(),
		TeamID:      s.Team(),
		EpicID:      s.EpicID,
		Kind:        kind,
	}
}

// tuiDeps assembles the TUI dependencies bound to this app and context.
func (a *App) tuiDeps(ctx context.Context) tui.Deps {
	opener := a.browserOpener()
	return tui.Deps{
		Load:           a.load,
		Launch:         a.launch,
		Resume:         a.resume,
		OpenURL:        opener.Open,
		Clipboard:      defaultClipboard,
		Now:            a.env.now,
		Context:        ctx,
		Kinds:          a.kinds,
		DefaultKind:    a.cfg.Agent.DefaultKind,
		Repositories:   a.expandedRepositories(),
		FocusedPaneCwd: a.ctxInfo.FocusedPaneCwd,
		WorkspaceCwd:   a.ctxInfo.WorkspaceCwd,
		TabLabel:       func(s shortcut.ResolvedStory, kind string) string { return a.tabLabel(s.ID, kind) },
		AgentName:      func(s shortcut.ResolvedStory, kind string) string { return a.agentBaseName(s.ID, kind) },
	}
}

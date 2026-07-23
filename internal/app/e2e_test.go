package app

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/matheus3301/herdr-shortcut/internal/herdr"
	"github.com/matheus3301/herdr-shortcut/internal/tui"
)

func init() { lipgloss.SetColorProfile(termenv.Ascii) }

// drive updates a model with msg and, when the update returns a simple message
// command (load/launch), executes it once and applies the resulting message.
// Tick-based commands (launch success linger) are not executed.
func drive(t *testing.T, model tea.Model, msg tea.Msg) tea.Model {
	t.Helper()
	next, cmd := model.Update(msg)
	if cmd != nil {
		if out := cmd(); out != nil {
			next2, _ := next.Update(out)
			return next2
		}
	}
	return next
}

// discoveringHerdr is a fake Herdr runner that reports kindsLine via bare
// `herdr agent` (exit 2 on stderr, as real Herdr does) and records every argv.
func discoveringHerdr(kindsLine string, rec *[][]string) herdr.Runner {
	return func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		*rec = append(*rec, args)
		switch {
		case len(args) == 1 && args[0] == "agent":
			return herdr.CommandResult{Stderr: []byte("herdr agent — start an agent\n  " + kindsLine + "\n"), ExitCode: 2}, nil
		case len(args) >= 1 && args[0] == "--version":
			return herdr.CommandResult{Stdout: []byte("herdr 0.7.5"), ExitCode: 0}, nil
		case len(args) >= 2 && args[0] == "workspace" && args[1] == "list":
			return okEnvelope(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"ws1","focused":true}]}}`), nil
		case len(args) >= 2 && args[0] == "agent" && args[1] == "list":
			return okEnvelope(`{"result":{"type":"agent_list","agents":[]}}`), nil
		case len(args) >= 2 && args[0] == "tab" && args[1] == "create":
			return okEnvelope(`{"result":{"type":"tab_created","tab":{"tab_id":"tab-1"},"root_pane":{"pane_id":"pane-1"}}}`), nil
		case len(args) >= 2 && args[0] == "agent" && args[1] == "start":
			return agentStartedEnvelope(args), nil
		case len(args) >= 2 && args[0] == "agent" && args[1] == "prompt":
			return agentPromptedEnvelope(args), nil
		default:
			return okEnvelope(`{"result":{"type":"ok"}}`), nil
		}
	}
}

// discoverInto runs the real discovery path so a.kinds comes from Herdr, never
// from a direct injection.
func discoverInto(t *testing.T, a *App) {
	t.Helper()
	kinds, err := a.herdr.AgentKinds(context.Background())
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	a.kinds = kinds
}

// TestEndToEndThroughTUI wires the real TUI model to the real app load/launch
// closures backed by a fake Shortcut server and a fake Herdr runner, discovers
// the kinds through Herdr, then drives list load -> mouse Story select -> cwd
// select -> launch, asserting the exact `agent start`/`agent prompt` calls.
func TestEndToEndThroughTUI(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()

	repoDir := t.TempDir() // a real, valid cwd for the focused pane
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-e2e-secret"}), discoveringHerdr("kinds: claude|codex|gemini", &rec))
	a.env.DirValidator = nil // exercise real cwd revalidation on repoDir
	a.ctxInfo = herdr.InvocationContext{WorkspaceID: "ws1", FocusedPaneCwd: repoDir}
	discoverInto(t, a)

	var model tea.Model = tui.New(a.tuiDeps(context.Background()))

	// Init -> triggerLoad -> startLoad -> loadResult (real a.load against fake API).
	if cmd := model.Init(); cmd != nil {
		model = drive(t, model, cmd())
	}
	model = drive(t, model, tea.WindowSizeMsg{Width: 100, Height: 30})

	m := model.(tui.Model)
	if !m.HasStories() {
		t.Fatalf("stories did not load through the TUI")
	}

	// Mouse-select the second row (sorted IDs are [7, 42]) -> Story SC-42.
	model = drive(t, model, tea.MouseMsg{X: 4, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = model.(tui.Model)
	if !m.DialogOpen() {
		t.Fatal("clicking a row should open the launch dialog")
	}

	// The default cwd choice is the focused pane (repoDir). Launch with Enter.
	model = drive(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	// Verify the exact Herdr command sequence. The default kind (claude) is used
	// and the name/label templates include {kind}.
	startCall := findCall(rec, "agent", "start")
	wantStart := []string{"agent", "start", "sc-42-claude", "--kind", "claude", "--pane", "pane-1"}
	if !reflect.DeepEqual(startCall, wantStart) {
		t.Errorf("agent start argv =\n %v\nwant\n %v\n(all calls: %v)", startCall, wantStart, rec)
	}
	promptCall := findCall(rec, "agent", "prompt")
	if len(promptCall) != 4 || promptCall[2] != "sc-42-claude" {
		t.Fatalf("agent prompt argv = %v", promptCall)
	}
	// The cwd is symlink-resolved before launch (SPEC requirement), so compare
	// against the resolved form of the selected repository directory.
	wantCwd, err := filepath.EvalSymlinks(repoDir)
	if err != nil {
		wantCwd = repoDir
	}
	tabCall := findCall(rec, "tab", "create")
	if len(tabCall) == 0 || !slices.Contains(tabCall, wantCwd) {
		t.Errorf("tab create should use the resolved selected cwd %q: %v", wantCwd, tabCall)
	}
}

// TestEndToEndEveryKindThroughDiscovery drives the full flow — Herdr discovery,
// TUI load, keyboard Story select, harness selection in the dialog, cwd select,
// and launch — for every v0.7.5 kind plus a future, unknown-but-valid kind,
// asserting the exact `agent start --kind <selected>` and `agent prompt` calls.
// The kinds come from discovery, never from a direct app injection.
func TestEndToEndEveryKindThroughDiscovery(t *testing.T) {
	t.Parallel()
	allKinds := append(append([]string{}, v075Kinds...), "future-harness_9")
	kindsLine := "kinds: " + strings.Join(allKinds, "|")
	for i, kind := range allKinds {
		i, kind := i, kind
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			srv := freshStoryServer(t)
			defer srv.Close()
			repoDir := t.TempDir()
			var rec [][]string
			a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-e2e-secret"}), discoveringHerdr(kindsLine, &rec))
			a.env.DirValidator = nil
			a.ctxInfo = herdr.InvocationContext{WorkspaceID: "ws1", FocusedPaneCwd: repoDir}
			discoverInto(t, a)
			if len(a.kinds) != len(allKinds) {
				t.Fatalf("discovered %d kinds, want %d", len(a.kinds), len(allKinds))
			}

			var model tea.Model = tui.New(a.tuiDeps(context.Background()))
			if cmd := model.Init(); cmd != nil {
				model = drive(t, model, cmd())
			}
			model = drive(t, model, tea.WindowSizeMsg{Width: 100, Height: 40})
			if !model.(tui.Model).HasStories() {
				t.Fatal("stories did not load")
			}

			// Keyboard: move to the SC-42 row (sorted [7, 42]) and open the dialog.
			model = drive(t, model, tea.KeyMsg{Type: tea.KeyDown})
			model = drive(t, model, tea.KeyMsg{Type: tea.KeyEnter})
			if !model.(tui.Model).DialogOpen() {
				t.Fatal("Enter should open the dialog")
			}

			// Focus the harness region and navigate to this kind's row.
			model = drive(t, model, tea.KeyMsg{Type: tea.KeyTab})
			for j := 0; j < len(allKinds); j++ {
				model = drive(t, model, tea.KeyMsg{Type: tea.KeyUp}) // clamp to index 0
			}
			for j := 0; j < i; j++ {
				model = drive(t, model, tea.KeyMsg{Type: tea.KeyDown})
			}

			// Launch with Enter (default cwd choice is the focused pane, repoDir).
			model = drive(t, model, tea.KeyMsg{Type: tea.KeyEnter})

			startCall := findCall(rec, "agent", "start")
			wantStart := []string{"agent", "start", "sc-42-" + kind, "--kind", kind, "--pane", "pane-1"}
			if !reflect.DeepEqual(startCall, wantStart) {
				t.Errorf("agent start argv =\n %v\nwant\n %v", startCall, wantStart)
			}
			promptCall := findCall(rec, "agent", "prompt")
			if len(promptCall) != 4 || promptCall[2] != "sc-42-"+kind {
				t.Fatalf("agent prompt argv = %v", promptCall)
			}
			if !strings.Contains(promptCall[3], "Agent harness: "+kind) {
				t.Errorf("prompt should record the selected harness %q:\n%s", kind, promptCall[3])
			}
			wantCwd, err := filepath.EvalSymlinks(repoDir)
			if err != nil {
				wantCwd = repoDir
			}
			if tabCall := findCall(rec, "tab", "create"); !slices.Contains(tabCall, wantCwd) {
				t.Errorf("tab create should use the resolved cwd %q: %v", wantCwd, tabCall)
			}
		})
	}
}

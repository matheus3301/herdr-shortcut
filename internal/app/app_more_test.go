package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/matheus3301/herdr-shortcut/internal/config"
	"github.com/matheus3301/herdr-shortcut/internal/herdr"
	"github.com/matheus3301/herdr-shortcut/internal/prompt"
	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
	"github.com/matheus3301/herdr-shortcut/internal/tui"
)

func writeBadConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[shortcut]\npage_size = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadSuccess(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&[][]string{}))
	res := a.load(context.Background())
	if res.Err != nil {
		t.Fatalf("load error: %v", res.Err)
	}
	if res.Member != "matheus" {
		t.Errorf("member = %q", res.Member)
	}
	if len(res.Stories) != 2 {
		t.Fatalf("stories = %d, want 2", len(res.Stories))
	}
	// Resolved state names come from the workflows.
	if res.Stories[0].State.Name != "In Dev" {
		t.Errorf("state not resolved: %+v", res.Stories[0].State)
	}
}

func TestLoadMissingTokenIsCredentials(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	a := newLaunchApp(srv.URL, envFrom(nil), recordingHerdr(&[][]string{}))
	res := a.load(context.Background())
	if _, ok := res.Err.(tui.CredentialsError); !ok {
		t.Fatalf("expected CredentialsError, got %v", res.Err)
	}
}

func TestExpandedRepositories(t *testing.T) {
	t.Parallel()
	a := &App{
		env: Environment{Getenv: envFrom(map[string]string{"HOME": "/home/u"})},
		cfg: config.Config{Repositories: []config.Repository{
			{Name: "Home repo", Path: "~/src/app"},
			{Name: "Abs", Path: "/opt/x"},
		}},
	}
	repos := a.expandedRepositories()
	if len(repos) != 2 {
		t.Fatalf("repos = %d", len(repos))
	}
	if repos[0].Path != "/home/u/src/app" {
		t.Errorf("expand failed: %q", repos[0].Path)
	}
}

func TestTuiDepsWiring(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	a := &App{
		env:     Environment{Getenv: envFrom(nil)},
		cfg:     cfg,
		ctxInfo: herdr.InvocationContext{FocusedPaneCwd: "/repo/f", WorkspaceCwd: "/repo/w"},
	}
	deps := a.tuiDeps(context.Background())
	if deps.FocusedPaneCwd != "/repo/f" || deps.WorkspaceCwd != "/repo/w" {
		t.Errorf("cwd wiring wrong: %+v", deps)
	}
	if deps.DefaultKind != "claude" {
		t.Errorf("DefaultKind = %q", deps.DefaultKind)
	}
	story := shortcut.ResolvedStory{Story: shortcut.Story{ID: 5}}
	if got := deps.TabLabel(story, "codex"); got != "SC-5 codex" {
		t.Errorf("TabLabel = %q", got)
	}
	if got := deps.AgentName(story, "codex"); got != "sc-5-codex" {
		t.Errorf("AgentName = %q", got)
	}
}

func TestRunTUICommand(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	cfgDir := writeConfig(t, srv.URL)
	called := false
	var rec [][]string
	var out bytes.Buffer
	env := Environment{
		Args:   []string{"tui"},
		Stdout: &out,
		Stderr: &out,
		Getenv: envFrom(map[string]string{
			"HERDR_PLUGIN_CONFIG_DIR": cfgDir,
			"SHORTCUT_API_TOKEN":      "t",
		}),
		HerdrRunner: recordingHerdr(&rec),
		RunTUI: func(deps tui.Deps) (tui.Model, error) {
			called = true
			if deps.Load == nil || deps.Launch == nil {
				t.Error("deps not wired")
			}
			if len(deps.Kinds) == 0 || deps.DefaultKind == "" {
				t.Error("discovered kinds not wired into deps")
			}
			return tui.Model{}, nil
		},
	}
	if code := Main(env); code != 0 {
		t.Fatalf("tui exit = %d: %s", code, out.String())
	}
	if !called {
		t.Error("RunTUI was not invoked")
	}
}

func TestFreshStoryUnknownStateNotStale(t *testing.T) {
	t.Parallel()
	// Workflows contain state 10 only; the fresh Story references state 777.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/workflows":
			fmt.Fprint(w, `[{"id":1,"name":"Eng","states":[{"id":10,"name":"In Dev","type":"started","position":1}]}]`)
		case "/api/v3/stories/42":
			fmt.Fprint(w, `{"id":42,"name":"Fresh","app_url":"https://app.shortcut.com/o/story/42/x","story_type":"feature","workflow_state_id":777}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&rec))
	// The stale list state must NOT be used for the fresh, now-unknown state.
	listStory := shortcut.ResolvedStory{Story: shortcut.Story{ID: 42}, State: shortcut.StoryState{Name: "Stale Started", Type: "started"}}
	res := a.launch(context.Background(), listStory, "/repo", "claude")
	if res.Err != nil {
		t.Fatalf("launch: %v", res.Err)
	}
	prompt := findCall(rec, "agent", "prompt")[3]
	if !strings.Contains(prompt, shortcut.UnknownStateName) {
		t.Errorf("prompt should use Unknown state:\n%s", prompt)
	}
	if strings.Contains(prompt, "Stale Started") {
		t.Errorf("prompt used stale list state:\n%s", prompt)
	}
}

// storyOnlyServer serves the fresh Story and workflows for launch tests.
func launchStory() shortcut.ResolvedStory {
	return shortcut.ResolvedStory{Story: shortcut.Story{ID: 42}}
}

func TestTabCreateTimeoutIsResourceCreatedNotEarly(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	tabCreates := 0
	runner := func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		switch {
		case args[0] == "agent" && args[1] == "list":
			return okEnvelope(`{"result":{"type":"agent_list","agents":[]}}`), nil
		case args[0] == "tab" && args[1] == "create":
			tabCreates++
			return herdr.CommandResult{}, context.DeadlineExceeded // ambiguous: maybe created
		default:
			return okEnvelope(`{"result":{"type":"ok"}}`), nil
		}
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err == nil {
		t.Fatal("expected an error from the tab-create timeout")
	}
	if res.Stage != tui.StageTabCreated {
		t.Errorf("a tab-create timeout must be StageTabCreated (potentially created), got %v", res.Stage)
	}
	if tabCreates != 1 {
		t.Errorf("must not auto-create another tab; creates=%d", tabCreates)
	}
}

func TestLaunchRejectsEmptyPrompt(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&rec))
	compiled, _ := prompt.Compile("   ") // renders to whitespace only
	a.prompt = compiled
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err == nil || !strings.Contains(res.Err.Error(), "prompt is empty") {
		t.Fatalf("expected empty-prompt rejection, got %v", res.Err)
	}
	if findCall(rec, "tab", "create") != nil {
		t.Error("no tab must be created for an empty prompt")
	}
	if res.Stage != tui.StageFailedEarly {
		t.Errorf("stage = %v, want StageFailedEarly", res.Stage)
	}
}

func TestLaunchRejectsControlBearingNativeArg(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&rec))
	a.cfg.Agent.ArgsByKind = map[string][]string{"claude": {"--flag", "bad\x00nul"}}
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err == nil || !strings.Contains(res.Err.Error(), "control character") {
		t.Fatalf("expected control-arg rejection, got %v", res.Err)
	}
	if findCall(rec, "tab", "create") != nil {
		t.Error("no tab must be created for a control-bearing native arg")
	}
}

func TestClipboardCommandsPerOS(t *testing.T) {
	t.Parallel()
	if cmds := clipboardCommands("darwin"); len(cmds) == 0 || cmds[0].name != "pbcopy" {
		t.Errorf("darwin clipboard = %+v", cmds)
	}
	if cmds := clipboardCommands("linux"); len(cmds) == 0 {
		t.Error("linux should have clipboard candidates")
	}
	if cmds := clipboardCommands("plan9"); len(cmds) != 0 {
		t.Errorf("unsupported OS should have no clipboard command: %+v", cmds)
	}
}

func TestCanonicalDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := canonicalDir(dir)
	if err != nil {
		t.Fatalf("canonicalDir(%q): %v", dir, err)
	}
	if resolved, _ := filepath.EvalSymlinks(dir); got != resolved {
		t.Errorf("canonicalDir = %q, want %q", got, resolved)
	}
	if _, err := canonicalDir(""); err == nil {
		t.Error("empty path should error")
	}
	if _, err := canonicalDir(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing path should error")
	}
	f := filepath.Join(dir, "file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalDir(f); err == nil {
		t.Error("a file should be rejected as not-a-directory")
	}
}

func TestWorkflowRefreshFailureUsesUnknownStateNotStale(t *testing.T) {
	t.Parallel()
	// Workflows fail (404, non-retryable so the test is fast); the Story fetch
	// succeeds. The prompt must show Unknown state, never the stale list state.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/workflows":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"gone"}`)
		case "/api/v3/stories/42":
			fmt.Fprint(w, `{"id":42,"name":"Fresh","app_url":"https://app.shortcut.com/o/story/42/x","story_type":"feature","workflow_state_id":10}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&rec))
	listStory := shortcut.ResolvedStory{Story: shortcut.Story{ID: 42}, State: shortcut.StoryState{Name: "Stale Started", Type: "started"}}
	res := a.launch(context.Background(), listStory, "/repo", "claude")
	if res.Err != nil {
		t.Fatalf("launch should still succeed when workflows fail: %v", res.Err)
	}
	prompt := findCall(rec, "agent", "prompt")[3]
	if !strings.Contains(prompt, shortcut.UnknownStateName) {
		t.Errorf("workflow-refresh failure should render Unknown state:\n%s", prompt)
	}
	if strings.Contains(prompt, "Stale Started") {
		t.Errorf("must not fall back to the stale list state:\n%s", prompt)
	}
}

// v075Kinds is the set Herdr v0.7.5 reports; the table below also exercises a
// future, unknown-but-valid kind produced by discovery.
var v075Kinds = []string{
	"pi", "claude", "codex", "gemini", "cursor", "devin", "agy", "cline", "omp",
	"mastracode", "opencode", "copilot", "kimi", "kiro", "droid", "amp", "grok",
	"hermes", "kilo", "qodercli", "maki",
}

func TestLaunchEveryDiscoveredKind(t *testing.T) {
	t.Parallel()
	// Include a future, unknown-but-valid kind so dynamic support is exercised.
	kinds := append(append([]string{}, v075Kinds...), "future-harness_9")
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			srv := freshStoryServer(t)
			defer srv.Close()
			var rec [][]string
			a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&rec))
			a.kinds = kinds // discovery reports this kind
			// Per-kind args must be scoped to the selected kind only; the sentinel
			// kind's args must never appear.
			a.cfg.Agent.ArgsByKind = map[string][]string{
				kind:            {"--flag-for-" + kind},
				"sentinelother": {"--must-not-appear"},
			}
			res := a.launch(context.Background(), launchStory(), "/repo", kind)
			if res.Err != nil {
				t.Fatalf("launch(%s): %v", kind, res.Err)
			}
			start := findCall(rec, "agent", "start")
			want := []string{"agent", "start", "sc-42-" + kind, "--kind", kind, "--pane", "pane-1", "--", "--flag-for-" + kind}
			if !reflect.DeepEqual(start, want) {
				t.Fatalf("agent start argv =\n %v\nwant\n %v", start, want)
			}
			if strings.Contains(strings.Join(start, " "), "--must-not-appear") {
				t.Errorf("another kind's args leaked into %s: %v", kind, start)
			}
			// The prompt records the selected harness.
			if p := findCall(rec, "agent", "prompt"); p == nil || !strings.Contains(p[3], "Agent harness: "+kind) {
				t.Errorf("prompt should record harness %q: %v", kind, p)
			}
		})
	}
}

func TestLaunchRejectsUndiscoveredKind(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&rec))
	a.kinds = []string{"claude", "codex"}
	res := a.launch(context.Background(), launchStory(), "/repo", "gemini") // not discovered
	if res.Err == nil {
		t.Fatal("expected error for a kind not reported by Herdr")
	}
	if findCall(rec, "tab", "create") != nil {
		t.Error("no tab must be created for an unsupported kind")
	}
}

func TestWorkspaceFrozenWhenContextLacksOne(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	runner := func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		rec = append(rec, args)
		switch {
		case args[0] == "workspace" && args[1] == "list":
			return okEnvelope(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","focused":false},{"workspace_id":"w9","focused":true}]}}`), nil
		case args[0] == "agent" && args[1] == "list":
			return okEnvelope(`{"result":{"type":"agent_list","agents":[]}}`), nil
		case args[0] == "tab" && args[1] == "create":
			return okEnvelope(`{"result":{"type":"tab_created","tab":{"tab_id":"t"},"root_pane":{"pane_id":"p"}}}`), nil
		case args[0] == "agent" && args[1] == "start":
			return agentStartedEnvelope(args), nil
		default:
			return agentPromptedEnvelope(args), nil
		}
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	a.ctxInfo = herdr.InvocationContext{} // no workspace in context
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err != nil {
		t.Fatalf("launch: %v", res.Err)
	}
	tab := findCall(rec, "tab", "create")
	if !slices.Contains(tab, "--workspace") || !slices.Contains(tab, "w9") {
		t.Errorf("tab create should use the frozen active workspace w9: %v", tab)
	}
}

func TestNoActiveWorkspaceFailsBeforeTabCreate(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	runner := func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		rec = append(rec, args)
		if args[0] == "workspace" && args[1] == "list" {
			return okEnvelope(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","focused":false}]}}`), nil
		}
		return okEnvelope(`{"result":{"type":"ok"}}`), nil
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	a.ctxInfo = herdr.InvocationContext{}
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err == nil {
		t.Fatal("expected error when no active workspace")
	}
	if findCall(rec, "tab", "create") != nil {
		t.Error("no tab must be created when the workspace cannot be resolved")
	}
	if res.Stage != tui.StageFailedEarly {
		t.Errorf("stage = %v, want StageFailedEarly", res.Stage)
	}
}

func TestReconcileAmbiguousStartSucceeds(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	starts, lists := 0, 0
	runner := func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		switch {
		case args[0] == "agent" && args[1] == "list":
			lists++
			// Initial name generation sees no agents; the reconcile list (after the
			// ambiguous start) shows the agent actually came up on pane-1, matching
			// name+kind+pane and reporting a positive lifecycle signal.
			if lists == 1 {
				return okEnvelope(`{"result":{"type":"agent_list","agents":[]}}`), nil
			}
			return okEnvelope(`{"result":{"type":"agent_list","agents":[{"name":"sc-42-claude","agent":"claude","pane_id":"pane-1","interactive_ready":true}]}}`), nil
		case args[0] == "tab" && args[1] == "create":
			return okEnvelope(`{"result":{"type":"tab_created","tab":{"tab_id":"tab-1"},"root_pane":{"pane_id":"pane-1"}}}`), nil
		case args[0] == "agent" && args[1] == "start":
			starts++
			return herdr.CommandResult{Stderr: []byte(`{"error":{"code":"timeout","message":"timed out waiting for agent startup"}}`), ExitCode: 1}, nil
		default:
			return agentPromptedEnvelope(args), nil
		}
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err != nil {
		t.Fatalf("reconciled ambiguous start should succeed: %v", res.Err)
	}
	if starts != 1 {
		t.Errorf("must not blindly restart; starts=%d", starts)
	}
	if res.Stage != tui.StageComplete {
		t.Errorf("stage = %v, want complete", res.Stage)
	}
}

func TestTransientPaneBusyWaitsForShellStartup(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	base := recordingHerdr(&rec)
	startCalls := 0
	runner := func(ctx context.Context, bin string, args []string) (herdr.CommandResult, error) {
		if len(args) >= 2 && args[0] == "agent" && args[1] == "start" {
			rec = append(rec, args)
			startCalls++
			if startCalls <= 3 {
				return herdr.CommandResult{Stderr: []byte(`{"error":{"code":"agent_pane_busy","message":"not an available shell"}}`), ExitCode: 1}, nil
			}
			return agentStartedEnvelope(args), nil
		}
		return base(ctx, bin, args)
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	sleeps := 0
	a.env.Sleep = func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err != nil {
		t.Fatalf("launch should wait through transient pane busy errors: %v", res.Err)
	}
	if res.Stage != tui.StageComplete {
		t.Fatalf("stage = %v, want complete", res.Stage)
	}
	if startCalls != 4 || sleeps != 3 {
		t.Fatalf("start calls=%d sleeps=%d, want 4 and 3", startCalls, sleeps)
	}
}

func TestPermanentPaneBusyStopsAfterReadinessWindow(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	base := recordingHerdr(&rec)
	startCalls := 0
	runner := func(ctx context.Context, bin string, args []string) (herdr.CommandResult, error) {
		if len(args) >= 2 && args[0] == "agent" && args[1] == "start" {
			rec = append(rec, args)
			startCalls++
			return herdr.CommandResult{Stderr: []byte(`{"error":{"code":"agent_pane_busy","message":"not an available shell"}}`), ExitCode: 1}, nil
		}
		return base(ctx, bin, args)
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	sleeps := 0
	a.env.Sleep = func(context.Context, time.Duration) error {
		sleeps++
		return nil
	}
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err == nil || !strings.Contains(res.Err.Error(), "did not become available") {
		t.Fatalf("expected bounded shell-readiness error, got %v", res.Err)
	}
	if res.Stage != tui.StageTabCreated {
		t.Fatalf("stage = %v, want tab-created recovery", res.Stage)
	}
	if startCalls != agentPaneReadyMaxAttempts || sleeps != agentPaneReadyMaxAttempts-1 {
		t.Fatalf("start calls=%d sleeps=%d, want %d and %d", startCalls, sleeps, agentPaneReadyMaxAttempts, agentPaneReadyMaxAttempts-1)
	}
}

func TestPaneBusyCancellationStopsRetriesAndPrompt(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	base := recordingHerdr(&rec)
	startCalls, promptCalls := 0, 0
	runner := func(ctx context.Context, bin string, args []string) (herdr.CommandResult, error) {
		if len(args) >= 2 && args[0] == "agent" && args[1] == "start" {
			rec = append(rec, args)
			startCalls++
			return herdr.CommandResult{Stderr: []byte(`{"error":{"code":"agent_pane_busy","message":"not an available shell"}}`), ExitCode: 1}, nil
		}
		if len(args) >= 2 && args[0] == "agent" && args[1] == "prompt" {
			promptCalls++
		}
		return base(ctx, bin, args)
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	a.env.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("launch error = %v, want context cancellation", res.Err)
	}
	if startCalls != 1 || promptCalls != 0 {
		t.Fatalf("start calls=%d prompt calls=%d, want 1 and 0", startCalls, promptCalls)
	}
}

func TestNameCollisionRegenerates(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var startNames []string
	lists := 0
	runner := func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		switch {
		case args[0] == "agent" && args[1] == "list":
			lists++
			// Initial gen sees no agents (so the base name sc-42 is chosen), but by
			// the time we start, another agent has raced in and taken sc-42.
			if lists == 1 {
				return okEnvelope(`{"result":{"type":"agent_list","agents":[]}}`), nil
			}
			return okEnvelope(`{"result":{"type":"agent_list","agents":[{"name":"sc-42-claude","agent":"codex","pane_id":"other"}]}}`), nil
		case args[0] == "tab" && args[1] == "create":
			return okEnvelope(`{"result":{"type":"tab_created","tab":{"tab_id":"tab-1"},"root_pane":{"pane_id":"pane-1"}}}`), nil
		case args[0] == "agent" && args[1] == "start":
			name := args[2]
			startNames = append(startNames, name)
			if name == "sc-42-claude" {
				return herdr.CommandResult{Stderr: []byte(`{"error":{"code":"agent_name_taken","message":"agent name sc-42-claude is already used"}}`), ExitCode: 1}, nil
			}
			return agentStartedEnvelope(args), nil
		default:
			return agentPromptedEnvelope(args), nil
		}
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err != nil {
		t.Fatalf("collision should regenerate and succeed: %v", res.Err)
	}
	if res.AgentName == "sc-42-claude" {
		t.Errorf("agent name should have been regenerated, got %q", res.AgentName)
	}
	if len(startNames) != 2 || startNames[0] != "sc-42-claude" {
		t.Errorf("expected a retry with a fresh name, starts=%v", startNames)
	}
}

func TestCollisionRetryUsesUnsuffixedBase(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	lists := 0
	var startNames []string
	runner := func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		switch {
		case args[0] == "agent" && args[1] == "list":
			lists++
			switch lists {
			case 1: // name generation: sc-42-claude is taken, so the name becomes -2
				return okEnvelope(`{"result":{"type":"agent_list","agents":[{"name":"sc-42-claude","agent":"codex","pane_id":"x"}]}}`), nil
			case 2: // reconcile probe: the -2 start is not live on pane-1
				return okEnvelope(`{"result":{"type":"agent_list","agents":[]}}`), nil
			default: // fresh-name generation must avoid -2 as well
				return okEnvelope(`{"result":{"type":"agent_list","agents":[{"name":"sc-42-claude","agent":"codex","pane_id":"x"},{"name":"sc-42-claude-2","agent":"codex","pane_id":"y"}]}}`), nil
			}
		case args[0] == "tab" && args[1] == "create":
			return okEnvelope(`{"result":{"type":"tab_created","tab":{"tab_id":"tab-1"},"root_pane":{"pane_id":"pane-1"}}}`), nil
		case args[0] == "agent" && args[1] == "start":
			startNames = append(startNames, args[2])
			if args[2] == "sc-42-claude-2" {
				return herdr.CommandResult{Stderr: []byte(`{"error":{"code":"agent_name_taken","message":"taken"}}`), ExitCode: 1}, nil
			}
			return agentStartedEnvelope(args), nil
		default:
			return agentPromptedEnvelope(args), nil
		}
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err != nil {
		t.Fatalf("launch: %v", res.Err)
	}
	// The retry regenerates from the unsuffixed base "sc-42-claude" (giving -3),
	// never doubly-suffixed "sc-42-claude-2-2".
	if res.AgentName != "sc-42-claude-3" {
		t.Errorf("collision retry name = %q, want sc-42-claude-3", res.AgentName)
	}
	if len(startNames) != 2 || startNames[0] != "sc-42-claude-2" || startNames[1] != "sc-42-claude-3" {
		t.Errorf("start names = %v, want [sc-42-claude-2 sc-42-claude-3]", startNames)
	}
}

func TestFailedRetryReturnsSecondError(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	startCalls := 0
	runner := func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		switch {
		case args[0] == "agent" && args[1] == "list":
			return okEnvelope(`{"result":{"type":"agent_list","agents":[]}}`), nil
		case args[0] == "tab" && args[1] == "create":
			return okEnvelope(`{"result":{"type":"tab_created","tab":{"tab_id":"tab-1"},"root_pane":{"pane_id":"pane-1"}}}`), nil
		case args[0] == "agent" && args[1] == "start":
			startCalls++
			if args[2] == "sc-42-claude" {
				return herdr.CommandResult{Stderr: []byte(`{"error":{"code":"agent_name_taken","message":"first-collision-reason"}}`), ExitCode: 1}, nil
			}
			return herdr.CommandResult{Stderr: []byte(`{"error":{"code":"agent_pane_busy","message":"second-failure-reason"}}`), ExitCode: 1}, nil
		default:
			return agentPromptedEnvelope(args), nil
		}
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err == nil {
		t.Fatal("expected the failed retry to surface an error")
	}
	// The SECOND (retry) error is returned, not the first collision error.
	if !strings.Contains(res.Err.Error(), "second-failure-reason") {
		t.Errorf("expected the second error, got %v", res.Err)
	}
	if strings.Contains(res.Err.Error(), "first-collision-reason") {
		t.Errorf("must not return the first error: %v", res.Err)
	}
	if res.Stage != tui.StageTabCreated {
		t.Errorf("stage = %v, want StageTabCreated", res.Stage)
	}
	if startCalls != agentPaneReadyMaxAttempts {
		t.Errorf("start calls = %d, want one shared budget of %d", startCalls, agentPaneReadyMaxAttempts)
	}
}

func TestIncompleteTabIsResourceCreatedNotEarly(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	tabCreates := 0
	runner := func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		switch {
		case args[0] == "agent" && args[1] == "list":
			return okEnvelope(`{"result":{"type":"agent_list","agents":[]}}`), nil
		case args[0] == "tab" && args[1] == "create":
			tabCreates++
			// tab_created but missing the root pane id: ambiguous, a tab may exist.
			return okEnvelope(`{"result":{"type":"tab_created","tab":{"tab_id":"tab-9"}}}`), nil
		default:
			return okEnvelope(`{"result":{"type":"ok"}}`), nil
		}
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err == nil {
		t.Fatal("expected an error for the incomplete tab response")
	}
	// Ambiguous tab creation must be treated as potentially-created, never a clean
	// early failure, and the partial tab id must be preserved.
	if res.Stage != tui.StageTabCreated {
		t.Errorf("stage = %v, want StageTabCreated (potentially resource-created)", res.Stage)
	}
	if res.TabID != "tab-9" {
		t.Errorf("partial tab id not preserved: %+v", res)
	}
	if tabCreates != 1 {
		t.Errorf("must not auto-create another tab; tab creates = %d", tabCreates)
	}
}

func TestLaunchRevalidatesCwdBeforeTabCreate(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&rec))
	a.env.DirValidator = nil // exercise the real canonicalDir revalidation
	missing := filepath.Join(t.TempDir(), "gone")
	res := a.launch(context.Background(), launchStory(), missing, "claude")
	if res.Err == nil {
		t.Fatal("expected a working-directory error before tab creation")
	}
	if res.Stage != tui.StageFailedEarly {
		t.Errorf("stage = %v, want StageFailedEarly (no resource created)", res.Stage)
	}
	if findCall(rec, "tab", "create") != nil {
		t.Error("no tab must be created when the cwd fails revalidation")
	}
}

func TestResumeFromPromptFailureReusesTab(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&rec))
	prev := tui.LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-42", Stage: tui.StageAgentStarted}
	res := a.resume(context.Background(), prev, shortcut.ResolvedStory{Story: shortcut.Story{ID: 42}}, "claude")
	if res.Err != nil {
		t.Fatalf("resume error: %v", res.Err)
	}
	if findCall(rec, "tab", "create") != nil {
		t.Error("resume from prompt failure must NOT create a new tab")
	}
	if findCall(rec, "agent", "start") != nil {
		t.Error("resume from prompt failure must NOT start the agent again")
	}
	if findCall(rec, "agent", "prompt") == nil {
		t.Error("resume should re-submit the prompt")
	}
	if res.Stage != tui.StageComplete {
		t.Errorf("resume stage = %v, want complete", res.Stage)
	}
}

func TestResumeFromStartFailureReusesTab(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), recordingHerdr(&rec))
	prev := tui.LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-42", Stage: tui.StageTabCreated}
	res := a.resume(context.Background(), prev, shortcut.ResolvedStory{Story: shortcut.Story{ID: 42}}, "claude")
	if res.Err != nil {
		t.Fatalf("resume error: %v", res.Err)
	}
	if findCall(rec, "tab", "create") != nil {
		t.Error("resume must NOT create a new tab")
	}
	start := findCall(rec, "agent", "start")
	if start == nil || !slices.Contains(start, "pane-1") {
		t.Errorf("resume should start the agent in the existing pane: %v", start)
	}
	if findCall(rec, "agent", "prompt") == nil {
		t.Error("resume should submit the prompt")
	}
}

func TestDoctorFailsWhenHerdrUnavailable(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	cfgDir := writeConfig(t, srv.URL)
	var out bytes.Buffer
	// Runner where `herdr --version` fails (binary broken/absent).
	runner := func(_ context.Context, _ string, args []string) (herdr.CommandResult, error) {
		if len(args) >= 1 && args[0] == "--version" {
			return herdr.CommandResult{}, fmt.Errorf("exec: herdr not found")
		}
		return okEnvelope(`{"result":{"type":"ok"}}`), nil
	}
	env := Environment{
		Args: []string{"doctor"}, Stdout: &out, Stderr: &out,
		Getenv:      envFrom(map[string]string{"HERDR_PLUGIN_CONFIG_DIR": cfgDir, "SHORTCUT_API_TOKEN": "tok-test-secret"}),
		HerdrRunner: runner,
	}
	if code := Main(env); code == 0 {
		t.Errorf("doctor must fail when Herdr is unavailable:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Herdr") {
		t.Errorf("expected a Herdr failure line:\n%s", out.String())
	}
}

func TestDoctorFailsWhenDefaultKindUnsupported(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	// Config sets a default_kind that Herdr does not report.
	dir := t.TempDir()
	content := "[shortcut]\napi_base_url = \"" + srv.URL + "/api/v3\"\n[agent]\ndefault_kind = \"nonesuch\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var rec [][]string
	var out bytes.Buffer
	env := Environment{
		Args: []string{"doctor"}, Stdout: &out, Stderr: &out,
		Getenv:      envFrom(map[string]string{"HERDR_PLUGIN_CONFIG_DIR": dir, "SHORTCUT_API_TOKEN": "tok-test-secret", "HERDR_PLUGIN_CONTEXT_JSON": `{"workspace_id":"ws1"}`}),
		HerdrRunner: recordingHerdr(&rec),
	}
	if code := Main(env); code == 0 {
		t.Errorf("doctor must fail when the default kind is unsupported:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "nonesuch") {
		t.Errorf("expected a default-kind failure line:\n%s", out.String())
	}
}

func TestSubcommandArityAndLocalHelp(t *testing.T) {
	t.Parallel()
	// Extra positional arg is rejected.
	var out bytes.Buffer
	if code := Main(Environment{Args: []string{"open", "extra"}, Stdout: &out, Stderr: &out, Getenv: envFrom(nil)}); code != 2 {
		t.Errorf("open extra arg should exit 2, got %d", code)
	}
	if !strings.Contains(out.String(), "takes no arguments") {
		t.Errorf("expected arity error:\n%s", out.String())
	}
	// Local --help prints usage and exits 0.
	for _, cmd := range []string{"open", "tui", "doctor", "version"} {
		var b bytes.Buffer
		code := Main(Environment{Args: []string{cmd, "--help"}, Stdout: &b, Stderr: &b, Getenv: envFrom(nil)})
		if code != 0 || !strings.Contains(b.String(), "Usage:") {
			t.Errorf("%s --help: code=%d out=%q", cmd, code, b.String())
		}
	}
	// version with an extra arg is rejected.
	var v bytes.Buffer
	if code := Main(Environment{Args: []string{"version", "junk"}, Stdout: &v, Stderr: &v, Getenv: envFrom(nil)}); code != 2 {
		t.Errorf("version junk should exit 2, got %d", code)
	}
}

func TestRunTUIFailsWhenDefaultKindUnsupported(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	// recordingHerdr reports claude|codex|gemini; the configured default is none
	// of them, so the picker must not run.
	dir := t.TempDir()
	content := "[shortcut]\napi_base_url = \"" + srv.URL + "/api/v3\"\n[agent]\ndefault_kind = \"nonesuch\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	env := Environment{
		Args:        []string{"tui"},
		Stdout:      &out,
		Stderr:      &out,
		Getenv:      envFrom(map[string]string{"HERDR_PLUGIN_CONFIG_DIR": dir, "SHORTCUT_API_TOKEN": "tok-test-secret"}),
		HerdrRunner: recordingHerdr(&[][]string{}),
		RunTUI: func(tui.Deps) (tui.Model, error) {
			t.Fatal("TUI must not run when the default kind is unsupported")
			return tui.Model{}, nil
		},
	}
	if code := Main(env); code == 0 {
		t.Errorf("tui must fail when the default kind is unsupported:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "nonesuch") {
		t.Errorf("expected a default-kind error mentioning the kind:\n%s", out.String())
	}
}

func TestRunTUICommandConfigError(t *testing.T) {
	t.Parallel()
	dir := writeBadConfig(t)
	var out bytes.Buffer
	env := Environment{
		Args:   []string{"tui"},
		Stdout: &out,
		Stderr: &out,
		Getenv: envFrom(map[string]string{"HERDR_PLUGIN_CONFIG_DIR": dir}),
		RunTUI: func(tui.Deps) (tui.Model, error) {
			t.Fatal("should not run TUI on config error")
			return tui.Model{}, nil
		},
	}
	if code := Main(env); code == 0 {
		t.Error("expected nonzero exit on invalid config")
	}
	if !strings.Contains(out.String(), "page_size") {
		t.Errorf("expected config error in stderr: %s", out.String())
	}
}

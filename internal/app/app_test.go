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
	"strings"
	"testing"

	"github.com/matheus3301/herdr-shortcut/internal/config"
	"github.com/matheus3301/herdr-shortcut/internal/herdr"
	"github.com/matheus3301/herdr-shortcut/internal/prompt"
	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
	"github.com/matheus3301/herdr-shortcut/internal/tui"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func okEnvelope(json string) herdr.CommandResult {
	return herdr.CommandResult{Stdout: []byte(json), ExitCode: 0}
}

func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func agentStartedEnvelope(args []string) herdr.CommandResult {
	return okEnvelope(fmt.Sprintf(
		`{"result":{"type":"agent_started","agent":{"name":%q,"agent":%q,"pane_id":%q}}}`,
		args[2], argValue(args, "--kind"), argValue(args, "--pane"),
	))
}

func agentPromptedEnvelope(args []string) herdr.CommandResult {
	return okEnvelope(fmt.Sprintf(`{"result":{"type":"agent_prompted","agent":{"name":%q}}}`, args[2]))
}

// recordingHerdr returns a fake Herdr runner that records argv and serves canned
// success envelopes for the launch/open commands.
func recordingHerdr(rec *[][]string) herdr.Runner {
	return func(ctx context.Context, bin string, args []string) (herdr.CommandResult, error) {
		*rec = append(*rec, args)
		switch {
		case len(args) >= 1 && args[0] == "--version":
			return herdr.CommandResult{Stdout: []byte("herdr 0.7.5"), ExitCode: 0}, nil
		case len(args) == 1 && args[0] == "agent":
			return herdr.CommandResult{Stdout: []byte("kinds: claude|codex|gemini\n"), ExitCode: 0}, nil
		case len(args) >= 2 && args[0] == "plugin" && args[1] == "--help":
			return herdr.CommandResult{ExitCode: 0}, nil
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

func findCall(rec [][]string, prefix ...string) []string {
	for _, c := range rec {
		if len(c) < len(prefix) {
			continue
		}
		match := true
		for i, p := range prefix {
			if c[i] != p {
				match = false
				break
			}
		}
		if match {
			return c
		}
	}
	return nil
}

func TestVersionCommand(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"version", "--version", "-v"} {
		var out bytes.Buffer
		code := Main(Environment{Args: []string{arg}, Stdout: &out, Stderr: &out, Getenv: envFrom(nil)})
		if code != 0 {
			t.Errorf("%s exit = %d", arg, code)
		}
		if !strings.Contains(out.String(), "0.1.0") {
			t.Errorf("%s output = %q", arg, out.String())
		}
	}
}

func TestHelpCommand(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"help", "--help", "-h"} {
		var out bytes.Buffer
		code := Main(Environment{Args: []string{arg}, Stdout: &out, Stderr: &out, Getenv: envFrom(nil)})
		if code != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Errorf("%s: code=%d out=%q", arg, code, out.String())
		}
	}
}

func TestUnknownAndNoCommand(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if code := Main(Environment{Args: []string{"bogus"}, Stdout: &out, Stderr: &out, Getenv: envFrom(nil)}); code != 2 {
		t.Errorf("unknown command exit = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "unknown command") {
		t.Errorf("expected unknown-command message: %q", out.String())
	}
	var out2 bytes.Buffer
	if code := Main(Environment{Args: nil, Stdout: &out2, Stderr: &out2, Getenv: envFrom(nil)}); code != 2 {
		t.Errorf("no command exit = %d, want 2", code)
	}
}

func TestOpenCommandPopupArgv(t *testing.T) {
	t.Parallel()
	var rec [][]string
	env := Environment{
		Args:   []string{"open"},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Getenv: envFrom(map[string]string{
			"HERDR_BIN_PATH":            "/opt/herdr",
			"HERDR_PLUGIN_CONTEXT_JSON": `{"workspace_id":"ws1","focused_pane_id":"pane0","focused_pane_cwd":"/repo/x"}`,
		}),
		HerdrRunner: recordingHerdr(&rec),
	}
	if code := Main(env); code != 0 {
		t.Fatalf("open exit = %d", code)
	}
	// A popup targets the active pane: no --workspace / --target-pane, only cwd,
	// plus the immutable original context forwarded via custom --env variables.
	want := []string{
		"plugin", "pane", "open", "--plugin", "matheus3301.shortcut", "--entrypoint", "tasks", "--placement", "popup", "--cwd", "/repo/x",
		"--env", "HERDR_SHORTCUT_ACTION_FOCUSED_PANE_CWD=/repo/x",
		"--env", "HERDR_SHORTCUT_ACTION_WORKSPACE_ID=ws1",
	}
	got := findCall(rec, "plugin", "pane", "open")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("open argv =\n %v\nwant\n %v", got, want)
	}
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "--workspace ") || strings.Contains(joined, "--target-pane") {
		t.Errorf("popup open must not pass --workspace/--target-pane: %v", got)
	}
}

// freshStoryServer serves the workflows and a fresh Story for launch tests.
func freshStoryServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/workflows":
			fmt.Fprint(w, `[{"id":1,"name":"Eng","states":[{"id":10,"name":"In Dev","type":"started","position":1}]}]`)
		case r.URL.Path == "/api/v3/stories/42":
			fmt.Fprint(w, `{"id":42,"name":"Fresh title","description":"Fresh description body","app_url":"https://app.shortcut.com/o/story/42/x","story_type":"feature","workflow_state_id":10,"labels":[{"id":1,"name":"api"}],"completed":false,"archived":false}`)
		case r.URL.Path == "/api/v3/member":
			fmt.Fprint(w, `{"id":"u","mention_name":"matheus","name":"M"}`)
		case r.URL.Path == "/api/v3/search/stories":
			fmt.Fprint(w, `{"total":2,"data":[{"id":42,"name":"Fresh title","app_url":"https://app.shortcut.com/o/story/42/x","story_type":"feature","workflow_state_id":10},{"id":7,"name":"Second","app_url":"https://app.shortcut.com/o/story/7/y","story_type":"bug","workflow_state_id":10}],"next":null}`)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
}

func newLaunchApp(srvURL string, getenv func(string) string, runner herdr.Runner) *App {
	cfg := config.Default()
	cfg.Shortcut.APIBaseURL = srvURL + "/api/v3"
	compiled, _ := prompt.Compile(cfg.Agent.PromptTemplate)
	return &App{
		// Launch tests use synthetic cwds like "/repo"; a passthrough validator
		// keeps them without touching the real filesystem. Tests that exercise the
		// real revalidation set env.DirValidator to nil (or their own).
		env:     Environment{Getenv: getenv, HerdrRunner: runner, DirValidator: func(p string) (string, error) { return p, nil }},
		cfg:     cfg,
		herdr:   herdr.New("herdr", runner),
		ctxInfo: herdr.InvocationContext{WorkspaceID: "ws1"},
		prompt:  compiled,
		kinds:   []string{"claude", "codex", "gemini"},
	}
}

func TestLaunchEndToEnd(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()

	var rec [][]string
	getenv := envFrom(map[string]string{"SHORTCUT_API_TOKEN": "secrettoken"})
	a := newLaunchApp(srv.URL, getenv, recordingHerdr(&rec))

	// The list story is intentionally stale to prove fresh data is fetched.
	listStory := shortcut.ResolvedStory{
		Story: shortcut.Story{ID: 42, Name: "Stale title", Description: "stale"},
		State: shortcut.StoryState{Name: "Stale state", Type: "unstarted"},
	}
	res := a.launch(context.Background(), listStory, "/repo/x", "claude")
	if res.Err != nil {
		t.Fatalf("launch error: %v", res.Err)
	}
	// The default name/label templates include {kind}.
	if res.TabID != "tab-1" || res.PaneID != "pane-1" || res.AgentName != "sc-42-claude" {
		t.Errorf("launch result = %+v", res)
	}

	// Verify the exact Herdr command sequence and argv.
	if findCall(rec, "agent", "list") == nil {
		t.Error("expected an `agent list` call for unique naming")
	}
	tabCall := findCall(rec, "tab", "create")
	wantTab := []string{"tab", "create", "--workspace", "ws1", "--cwd", "/repo/x", "--label", "SC-42 claude", "--focus", "--env", "SHORTCUT_API_TOKEN="}
	if !reflect.DeepEqual(tabCall, wantTab) {
		t.Errorf("tab create argv =\n %v\nwant\n %v", tabCall, wantTab)
	}
	startCall := findCall(rec, "agent", "start")
	wantStart := []string{"agent", "start", "sc-42-claude", "--kind", "claude", "--pane", "pane-1"}
	if !reflect.DeepEqual(startCall, wantStart) {
		t.Errorf("agent start argv =\n %v\nwant\n %v", startCall, wantStart)
	}
	promptCall := findCall(rec, "agent", "prompt")
	if len(promptCall) != 4 || promptCall[2] != "sc-42-claude" {
		t.Fatalf("agent prompt argv = %v", promptCall)
	}
	// The prompt must use fresh Story details, the resolved fresh state, and kind.
	promptText := promptCall[3]
	for _, want := range []string{"SC-42", "Fresh title", "Fresh description body", "In Dev", "feature", "Agent harness: claude"} {
		if !strings.Contains(promptText, want) {
			t.Errorf("prompt missing %q:\n%s", want, promptText)
		}
	}
	if strings.Contains(promptText, "Stale") {
		t.Errorf("prompt used stale list data:\n%s", promptText)
	}
	// The token must never appear in any recorded Herdr argv.
	for _, c := range rec {
		if strings.Contains(strings.Join(c, " "), "secrettoken") {
			t.Fatalf("token leaked into Herdr command: %v", c)
		}
	}
}

func TestLaunchRedactsTokenFromPrompt(t *testing.T) {
	t.Parallel()
	const token = "sc-super-secret-token-value"
	// The fresh Story maliciously echoes the token in its description; the
	// rendered prompt must redact it (defense in depth).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/workflows":
			fmt.Fprint(w, `[{"id":1,"name":"Eng","states":[{"id":10,"name":"In Dev","type":"started","position":1}]}]`)
		case "/api/v3/stories/42":
			fmt.Fprintf(w, `{"id":42,"name":"Fresh","description":"leaked %s here","app_url":"https://app.shortcut.com/o/story/42/x","story_type":"feature","workflow_state_id":10}`, token)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	var rec [][]string
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": token}), recordingHerdr(&rec))
	res := a.launch(context.Background(), launchStory(), "/repo", "claude")
	if res.Err != nil {
		t.Fatalf("launch: %v", res.Err)
	}
	promptCall := findCall(rec, "agent", "prompt")
	if promptCall == nil {
		t.Fatal("no prompt call recorded")
	}
	if strings.Contains(promptCall[3], token) {
		t.Errorf("token leaked into the prompt:\n%s", promptCall[3])
	}
	if !strings.Contains(promptCall[3], "[redacted]") {
		t.Errorf("expected the token to be redacted:\n%s", promptCall[3])
	}
}

func TestLaunchPromptFailureKeepsTabAndReportsRecovery(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()

	var rec [][]string
	runner := func(ctx context.Context, bin string, args []string) (herdr.CommandResult, error) {
		rec = append(rec, args)
		if len(args) >= 2 && args[0] == "agent" && args[1] == "prompt" {
			return herdr.CommandResult{Stderr: []byte(`{"error":{"code":"agent_prompt_stalled","message":"stalled"}}`), ExitCode: 1}, nil
		}
		return recordingHerdr(&[][]string{})(ctx, bin, args)
	}
	a := newLaunchApp(srv.URL, envFrom(map[string]string{"SHORTCUT_API_TOKEN": "tok-test-secret"}), runner)
	res := a.launch(context.Background(), shortcut.ResolvedStory{Story: shortcut.Story{ID: 42}}, "/repo/x", "claude")
	if res.Err == nil {
		t.Fatal("expected prompt failure error")
	}
	if res.TabID != "tab-1" || res.PaneID != "pane-1" || res.AgentName != "sc-42-claude" {
		t.Errorf("failure result should still carry tab/pane/agent ids: %+v", res)
	}
	if res.Recovery == "" || !strings.Contains(res.Recovery, "agent prompt 'sc-42-claude'") {
		t.Errorf("expected recovery command, got %q", res.Recovery)
	}
	if res.Stage != tui.StageAgentStarted {
		t.Errorf("prompt failure stage = %v, want StageAgentStarted", res.Stage)
	}
}

func TestLaunchMissingTokenIsCredentialsError(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	a := newLaunchApp(srv.URL, envFrom(nil), recordingHerdr(&[][]string{}))
	res := a.launch(context.Background(), shortcut.ResolvedStory{Story: shortcut.Story{ID: 42}}, "/repo/x", "claude")
	var credErr tui.CredentialsError
	if !errors.As(res.Err, &credErr) {
		t.Fatalf("expected CredentialsError, got %v", res.Err)
	}
}

func writeConfig(t *testing.T, baseURL string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	content := "[shortcut]\napi_base_url = \"" + baseURL + "/api/v3\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDoctorSuccess(t *testing.T) {
	t.Parallel()
	srv := freshStoryServer(t)
	defer srv.Close()
	cfgDir := writeConfig(t, srv.URL)
	var rec [][]string
	var out bytes.Buffer
	env := Environment{
		Args:   []string{"doctor"},
		Stdout: &out,
		Stderr: &out,
		Getenv: envFrom(map[string]string{
			"HERDR_PLUGIN_CONFIG_DIR":   cfgDir,
			"SHORTCUT_API_TOKEN":        "secrettoken",
			"HERDR_BIN_PATH":            "/opt/herdr",
			"HERDR_PLUGIN_CONTEXT_JSON": `{"workspace_id":"ws1"}`,
		}),
		HerdrRunner: recordingHerdr(&rec),
	}
	code := Main(env)
	if code != 0 {
		t.Fatalf("doctor exit = %d\n%s", code, out.String())
	}
	report := out.String()
	for _, want := range []string{"Configuration: valid", "Shortcut token: resolved", "Shortcut authentication: @matheus", "Herdr: version 0.7.5", "plugin command available", "Herdr workspace", "Agent kinds: claude, codex, gemini", "Default kind: claude (supported)"} {
		if !strings.Contains(report, want) {
			t.Errorf("doctor report missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "secrettoken") {
		t.Fatalf("doctor leaked the token:\n%s", report)
	}
}

func TestDoctorAuthFailureHidesToken(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"unauthorized"}`)
	}))
	defer srv.Close()
	cfgDir := writeConfig(t, srv.URL)
	var rec [][]string
	var out bytes.Buffer
	env := Environment{
		Args:   []string{"doctor"},
		Stdout: &out,
		Stderr: &out,
		Getenv: envFrom(map[string]string{
			"HERDR_PLUGIN_CONFIG_DIR": cfgDir,
			"SHORTCUT_API_TOKEN":      "supersecretvalue",
		}),
		HerdrRunner: recordingHerdr(&rec),
	}
	if code := Main(env); code == 0 {
		t.Errorf("doctor should fail on auth error")
	}
	if strings.Contains(out.String(), "supersecretvalue") {
		t.Fatalf("doctor leaked the token:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "401") {
		t.Errorf("expected 401 in report:\n%s", out.String())
	}
}

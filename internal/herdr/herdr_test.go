package herdr

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// TestMain re-execs this binary as a fake Herdr for DefaultRunner tests.
func TestMain(m *testing.M) {
	if len(os.Args) >= 2 && os.Args[1] == "__herdr_helper__" {
		mode := ""
		if len(os.Args) >= 3 {
			mode = os.Args[2]
		}
		switch mode {
		case "ok":
			_, _ = os.Stdout.WriteString(`{"id":"x","result":{"type":"ok"}}`)
			os.Exit(0)
		case "err":
			_, _ = os.Stderr.WriteString(`{"id":"x","error":{"code":"not_found","message":"pane missing"}}`)
			os.Exit(1)
		case "usage":
			_, _ = os.Stderr.WriteString("error: unknown flag --bogus")
			os.Exit(2)
		case "envcheck":
			if v, ok := os.LookupEnv("SHORTCUT_API_TOKEN"); ok {
				_, _ = os.Stdout.WriteString("TOKEN=" + v)
			} else {
				_, _ = os.Stdout.WriteString("TOKEN=UNSET")
			}
			os.Exit(0)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type scriptedRunner struct {
	mu    sync.Mutex
	calls [][]string
	fn    func(args []string) (CommandResult, error)
}

func (s *scriptedRunner) run(ctx context.Context, bin string, args []string) (CommandResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, args)
	s.mu.Unlock()
	return s.fn(args)
}

func (s *scriptedRunner) lastCall() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

func okResult(json string) func([]string) (CommandResult, error) {
	return func([]string) (CommandResult, error) {
		return CommandResult{Stdout: []byte(json), ExitCode: 0}, nil
	}
}

func TestCreateTabArgvAndParsing(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"id":"cli:tab:create","result":{"type":"tab_created","tab":{"tab_id":"t1"},"root_pane":{"pane_id":"p1"}}}`)}
	h := New("/bin/herdr", sr.run)
	res, err := h.CreateTab(context.Background(), "ws1", "/tmp/work", "SC-1", true)
	if err != nil {
		t.Fatalf("CreateTab: %v", err)
	}
	if res.TabID != "t1" || res.PaneID != "p1" {
		t.Errorf("result = %+v", res)
	}
	want := []string{"tab", "create", "--workspace", "ws1", "--cwd", "/tmp/work", "--label", "SC-1", "--focus", "--env", "SHORTCUT_API_TOKEN="}
	if !reflect.DeepEqual(sr.lastCall(), want) {
		t.Errorf("argv = %v, want %v", sr.lastCall(), want)
	}
}

func TestCreateTabScrubsToken(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"tab_created","tab":{"tab_id":"t"},"root_pane":{"pane_id":"p"}}}`)}
	h := New("", sr.run)
	if _, err := h.CreateTab(context.Background(), "ws", "/x", "l", true); err != nil {
		t.Fatalf("CreateTab: %v", err)
	}
	// The token must always be scrubbed from the new tab's environment so neither
	// the root shell nor the harness can inherit it.
	got := sr.lastCall()
	var sawEnv bool
	for i := 0; i+1 < len(got); i++ {
		if got[i] == "--env" && got[i+1] == "SHORTCUT_API_TOKEN=" {
			sawEnv = true
		}
	}
	if !sawEnv {
		t.Errorf("tab create must include `--env SHORTCUT_API_TOKEN=`: %v", got)
	}
}

func TestCreateTabNoFocus(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"tab_created","tab":{"tab_id":"t"},"root_pane":{"pane_id":"p"}}}`)}
	h := New("", sr.run)
	if _, err := h.CreateTab(context.Background(), "", "", "", false); err != nil {
		t.Fatalf("CreateTab: %v", err)
	}
	got := sr.lastCall()
	want := []string{"tab", "create", "--no-focus", "--env", "SHORTCUT_API_TOKEN="}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
	// Empty workspace/cwd/label flags omitted.
	if strings.Contains(strings.Join(got, " "), "--workspace") {
		t.Errorf("empty workspace should be omitted: %v", got)
	}
}

func TestCreateTabIncompleteResponse(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"tab_created","tab":{"tab_id":"t1"}}}`)}
	h := New("", sr.run)
	_, err := h.CreateTab(context.Background(), "ws", "/x", "l", true)
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("expected incomplete-response error, got %v", err)
	}
}

func TestStartAgentArgvWithSeparator(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"agent_started","agent":{"name":"sc-1","agent":"claude","pane_id":"p1"}}}`)}
	h := New("", sr.run)
	if err := h.StartAgent(context.Background(), "sc-1", "claude", "p1", []string{"-m", "opus"}); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	want := []string{"agent", "start", "sc-1", "--kind", "claude", "--pane", "p1", "--", "-m", "opus"}
	if !reflect.DeepEqual(sr.lastCall(), want) {
		t.Errorf("argv = %v, want %v", sr.lastCall(), want)
	}
}

func TestStartAgentNoExtraArgs(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"agent_started","agent":{"name":"sc-1","agent":"claude","pane_id":"p1"}}}`)}
	h := New("", sr.run)
	if err := h.StartAgent(context.Background(), "sc-1", "claude", "p1", nil); err != nil {
		t.Fatalf("StartAgent: %v", err)
	}
	if strings.Contains(strings.Join(sr.lastCall(), " "), "--") && sr.lastCall()[len(sr.lastCall())-1] == "--" {
		t.Errorf("no trailing -- expected when no extra args: %v", sr.lastCall())
	}
}

func TestSubmitPromptArgv(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"agent_prompted","agent":{"name":"sc-1"}}}`)}
	h := New("", sr.run)
	prompt := "Work on SC-1\nmultiline"
	if err := h.SubmitPrompt(context.Background(), "sc-1", prompt); err != nil {
		t.Fatalf("SubmitPrompt: %v", err)
	}
	want := []string{"agent", "prompt", "sc-1", prompt}
	if !reflect.DeepEqual(sr.lastCall(), want) {
		t.Errorf("argv = %v, want %v", sr.lastCall(), want)
	}
}

func TestOpenPanePopupDropsWorkspaceAndTargetPane(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"ok"}}`)}
	h := New("", sr.run)
	// Even though workspace/target-pane are supplied, a popup must not forward
	// them (Herdr rejects them for popup/overlay).
	err := h.OpenPane(context.Background(), OpenPaneParams{
		PluginID: "matheus3301.shortcut", Entrypoint: "tasks", Placement: "popup",
		WorkspaceID: "ws", TargetPaneID: "pane0", Cwd: "/repo",
	})
	if err != nil {
		t.Fatalf("OpenPane: %v", err)
	}
	want := []string{"plugin", "pane", "open", "--plugin", "matheus3301.shortcut", "--entrypoint", "tasks", "--placement", "popup", "--cwd", "/repo"}
	if !reflect.DeepEqual(sr.lastCall(), want) {
		t.Errorf("popup argv = %v, want %v", sr.lastCall(), want)
	}
	joined := strings.Join(sr.lastCall(), " ")
	if strings.Contains(joined, "--workspace") || strings.Contains(joined, "--target-pane") {
		t.Errorf("popup must not include --workspace/--target-pane: %v", sr.lastCall())
	}
}

func TestOpenPaneWrongResultTypeRejected(t *testing.T) {
	t.Parallel()
	// A popup that somehow returns a tiled result type must be rejected.
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"plugin_pane_opened","plugin_pane":{}}}`)}
	h := New("", sr.run)
	err := h.OpenPane(context.Background(), OpenPaneParams{PluginID: "p", Entrypoint: "tasks", Placement: "popup"})
	if err == nil || !strings.Contains(err.Error(), "unexpected result type") {
		t.Fatalf("expected result-type mismatch error, got %v", err)
	}
}

func TestOpenPaneOverlayExpectsPluginPaneOpened(t *testing.T) {
	t.Parallel()
	// An overlay creates a pane and returns plugin_pane_opened (NOT a bare ok).
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"plugin_pane_opened","plugin_pane":{}}}`)}
	h := New("", sr.run)
	if err := h.OpenPane(context.Background(), OpenPaneParams{PluginID: "p", Entrypoint: "tasks", Placement: "overlay"}); err != nil {
		t.Fatalf("overlay expecting plugin_pane_opened: %v", err)
	}
	// A bare ok from an overlay is a mismatch.
	sr2 := &scriptedRunner{fn: okResult(`{"result":{"type":"ok"}}`)}
	h2 := New("", sr2.run)
	if err := h2.OpenPane(context.Background(), OpenPaneParams{PluginID: "p", Entrypoint: "tasks", Placement: "overlay"}); err == nil {
		t.Fatal("overlay returning bare ok must be rejected")
	}
}

func TestStartAgentValidatesIdentity(t *testing.T) {
	t.Parallel()
	// A returned agent whose name/kind/pane differ from the request is rejected.
	cases := []string{
		`{"result":{"type":"agent_started","agent":{"name":"other","agent":"claude","pane_id":"p1"}}}`,
		`{"result":{"type":"agent_started","agent":{"name":"sc-1","agent":"codex","pane_id":"p1"}}}`,
		`{"result":{"type":"agent_started","agent":{"name":"sc-1","agent":"claude","pane_id":"other"}}}`,
	}
	for _, body := range cases {
		sr := &scriptedRunner{fn: okResult(body)}
		h := New("", sr.run)
		if err := h.StartAgent(context.Background(), "sc-1", "claude", "p1", nil); err == nil {
			t.Errorf("expected identity mismatch error for %s", body)
		}
	}
	// A complete matching identity is accepted; missing identity is rejected.
	for _, body := range []string{
		`{"result":{"type":"agent_started","agent":{"name":"sc-1","agent":"claude","pane_id":"p1"}}}`,
	} {
		sr := &scriptedRunner{fn: okResult(body)}
		h := New("", sr.run)
		if err := h.StartAgent(context.Background(), "sc-1", "claude", "p1", nil); err != nil {
			t.Errorf("matching identity should pass for %s: %v", body, err)
		}
	}
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"agent_started","agent":{}}}`)}
	if err := New("", sr.run).StartAgent(context.Background(), "sc-1", "claude", "p1", nil); err == nil {
		t.Fatal("missing start identity should fail")
	}
}

func TestSubmitPromptValidatesName(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"agent_prompted","agent":{"name":"other"}}}`)}
	h := New("", sr.run)
	if err := h.SubmitPrompt(context.Background(), "sc-1", "hi"); err == nil {
		t.Fatal("expected prompt identity mismatch error")
	}
	sr2 := &scriptedRunner{fn: okResult(`{"result":{"type":"agent_prompted","agent":{"name":"sc-1"}}}`)}
	h2 := New("", sr2.run)
	if err := h2.SubmitPrompt(context.Background(), "sc-1", "hi"); err != nil {
		t.Fatalf("matching prompt name should pass: %v", err)
	}
	sr3 := &scriptedRunner{fn: okResult(`{"result":{"type":"agent_prompted","agent":{}}}`)}
	if err := New("", sr3.run).SubmitPrompt(context.Background(), "sc-1", "hi"); err == nil {
		t.Fatal("missing prompt identity should fail")
	}
}

func TestIsAmbiguousTab(t *testing.T) {
	t.Parallel()
	// Timeout/cancellation while creating a tab is ambiguous (maybe created).
	sr := &scriptedRunner{fn: func([]string) (CommandResult, error) {
		return CommandResult{}, context.DeadlineExceeded
	}}
	h := New("", sr.run)
	_, err := h.CreateTab(context.Background(), "ws", "/x", "l", true)
	if !IsAmbiguousTab(err) {
		t.Errorf("timeout should be an ambiguous tab: %v", err)
	}
	// A successful (exit 0) but wrong-typed response is ambiguous.
	sr2 := &scriptedRunner{fn: okResult(`{"result":{"type":"surprise"}}`)}
	if _, err := New("", sr2.run).CreateTab(context.Background(), "ws", "/x", "l", true); !IsAmbiguousTab(err) {
		t.Errorf("wrong result type should be an ambiguous tab: %v", err)
	}
	// A successful (exit 0) but malformed body is ambiguous.
	sr3 := &scriptedRunner{fn: okResult(`not json`)}
	if _, err := New("", sr3.run).CreateTab(context.Background(), "ws", "/x", "l", true); !IsAmbiguousTab(err) {
		t.Errorf("malformed body should be an ambiguous tab: %v", err)
	}
	// A clean server error (exit 1 envelope) is NOT ambiguous.
	sr4 := &scriptedRunner{fn: func([]string) (CommandResult, error) {
		return CommandResult{Stderr: []byte(`{"error":{"code":"bad_cwd","message":"no"}}`), ExitCode: 1}, nil
	}}
	if _, err := New("", sr4.run).CreateTab(context.Background(), "ws", "/x", "l", true); IsAmbiguousTab(err) {
		t.Errorf("a clean server error must not be ambiguous: %v", err)
	}
	// Plain exit 1 can mean the socket response was lost after server-side
	// creation; unlike a structured rejection, it is ambiguous.
	sr5 := &scriptedRunner{fn: func([]string) (CommandResult, error) {
		return CommandResult{Stderr: []byte("connection closed before response"), ExitCode: 1}, nil
	}}
	if _, err := New("", sr5.run).CreateTab(context.Background(), "ws", "/x", "l", true); !IsAmbiguousTab(err) {
		t.Errorf("plain exit-1 transport failure should be ambiguous: %v", err)
	}
}

func TestActiveWorkspaceID(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","focused":false},{"workspace_id":"w2","focused":true}]}}`)}
	h := New("", sr.run)
	id, err := h.ActiveWorkspaceID(context.Background())
	if err != nil {
		t.Fatalf("ActiveWorkspaceID: %v", err)
	}
	if id != "w2" {
		t.Errorf("active workspace = %q, want w2", id)
	}
}

func TestActiveWorkspaceNone(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"workspace_list","workspaces":[{"workspace_id":"w1","focused":false}]}}`)}
	h := New("", sr.run)
	if _, err := h.ActiveWorkspaceID(context.Background()); err == nil {
		t.Fatal("expected error when no workspace is focused")
	}
}

func TestResultTypeMismatchRejected(t *testing.T) {
	t.Parallel()
	// tab create returning the wrong discriminator must fail.
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"agent_started","tab":{"tab_id":"t"},"root_pane":{"pane_id":"p"}}}`)}
	h := New("", sr.run)
	if _, err := h.CreateTab(context.Background(), "ws", "/x", "l", true); err == nil {
		t.Fatal("expected result-type mismatch error")
	}
}

func TestHerdrErrorMessageBounded(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("x", 5000)
	sr := &scriptedRunner{fn: func([]string) (CommandResult, error) {
		return CommandResult{Stderr: []byte(`{"error":{"code":"boom","message":"` + huge + `"}}`), ExitCode: 1}, nil
	}}
	h := New("", sr.run)
	err := h.SubmitPrompt(context.Background(), "sc-1", "hi")
	var he *HerdrError
	if !errors.As(err, &he) {
		t.Fatalf("expected HerdrError, got %v", err)
	}
	if len(he.Message) > 600 {
		t.Errorf("error message not bounded: %d chars", len(he.Message))
	}
}

func TestListAgentsAndNames(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult(`{"result":{"type":"agent_list","agents":[{"name":"sc-1","agent":"claude","pane_id":"p"},{"agent":"codex","pane_id":"q"}]}}`)}
	h := New("", sr.run)
	names, err := h.LiveAgentNames(context.Background())
	if err != nil {
		t.Fatalf("LiveAgentNames: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"sc-1"}) {
		t.Errorf("names = %v (unnamed agents should be excluded)", names)
	}
}

func TestErrorEnvelopeExit1(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: func([]string) (CommandResult, error) {
		return CommandResult{Stderr: []byte(`{"id":"x","error":{"code":"not_found","message":"pane not found"}}`), ExitCode: 1}, nil
	}}
	h := New("", sr.run)
	_, err := h.CreateTab(context.Background(), "ws", "/x", "l", true)
	var he *HerdrError
	if !errors.As(err, &he) {
		t.Fatalf("expected HerdrError, got %v", err)
	}
	if he.Code != "not_found" || !strings.Contains(he.Message, "pane not found") {
		t.Errorf("parsed error = %+v", he)
	}
}

func TestUsageErrorExit2(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: func([]string) (CommandResult, error) {
		return CommandResult{Stderr: []byte("error: unknown flag --bogus"), ExitCode: 2}, nil
	}}
	h := New("", sr.run)
	err := h.StartAgent(context.Background(), "sc-1", "claude", "p", nil)
	var he *HerdrError
	if !errors.As(err, &he) {
		t.Fatalf("expected HerdrError, got %v", err)
	}
	if he.ExitCode != 2 || !strings.Contains(he.Message, "unknown flag") || he.Code != "" {
		t.Errorf("usage error = %+v", he)
	}
}

func TestMalformedStdout(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: okResult("this is not json")}
	h := New("", sr.run)
	err := h.SubmitPrompt(context.Background(), "sc-1", "hi")
	if err == nil || !strings.Contains(err.Error(), "non-JSON") {
		t.Fatalf("expected non-JSON error, got %v", err)
	}
}

func TestRunnerStartFailure(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: func([]string) (CommandResult, error) {
		return CommandResult{}, errors.New("exec boom")
	}}
	h := New("", sr.run)
	err := h.SubmitPrompt(context.Background(), "sc-1", "hi")
	if err == nil || !strings.Contains(err.Error(), "exec boom") {
		t.Fatalf("expected exec failure error, got %v", err)
	}
}

func TestSanitizeAgentName(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"sc-42", "sc-42"},
		{"SC-42", "sc-42"},
		{"42", "s42"},
		{"", "sc"},
		{"feature/foo bar!", "feature-foo-bar-"},
		{strings.Repeat("a", 40), strings.Repeat("a", 32)},
	}
	for _, tc := range tests {
		if got := SanitizeAgentName(tc.in); got != tc.want {
			t.Errorf("SanitizeAgentName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUniqueAgentName(t *testing.T) {
	t.Parallel()
	if got := UniqueAgentName("sc-1", nil); got != "sc-1" {
		t.Errorf("no collision = %q", got)
	}
	if got := UniqueAgentName("sc-1", []string{"sc-1"}); got != "sc-1-2" {
		t.Errorf("one collision = %q", got)
	}
	if got := UniqueAgentName("sc-1", []string{"sc-1", "sc-1-2"}); got != "sc-1-3" {
		t.Errorf("two collisions = %q", got)
	}
	// 32-char base with a collision must stay within the limit.
	base := strings.Repeat("a", 32)
	got := UniqueAgentName(base, []string{base})
	if len(got) > MaxAgentNameLen {
		t.Errorf("unique name exceeds limit: %q (%d)", got, len(got))
	}
	if !strings.HasSuffix(got, "-2") {
		t.Errorf("expected -2 suffix, got %q", got)
	}
}

func TestAgentKindsDiscovery(t *testing.T) {
	t.Parallel()
	// The full v0.7.5 kinds line, embedded amid unrelated help lines.
	v075 := "pi|claude|codex|gemini|cursor|devin|agy|cline|omp|mastracode|opencode|copilot|kimi|kiro|droid|amp|grok|hermes|kilo|qodercli|maki"
	out := "herdr agent commands:\n  herdr agent list\n  herdr agent start <name> --kind KIND\n  kinds: " + v075 + "\n"
	sr := &scriptedRunner{fn: func(args []string) (CommandResult, error) {
		if len(args) == 1 && args[0] == "agent" {
			return CommandResult{Stdout: []byte(out), ExitCode: 0}, nil
		}
		return CommandResult{ExitCode: 1}, nil
	}}
	h := New("", sr.run)
	kinds, err := h.AgentKinds(context.Background())
	if err != nil {
		t.Fatalf("AgentKinds: %v", err)
	}
	want := strings.Split(v075, "|")
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %v\nwant %v", kinds, want)
	}
	if kinds[0] != "pi" || kinds[len(kinds)-1] != "maki" {
		t.Errorf("order not preserved: %v", kinds)
	}
}

func TestAgentKindsFromStderrWithUsageExit(t *testing.T) {
	t.Parallel()
	// Real Herdr prints the usage listing (with the kinds line) to stderr and
	// exits 2; discovery must still succeed.
	help := "herdr agent commands:\n  herdr agent list\n  kinds: claude|codex|gemini\n"
	sr := &scriptedRunner{fn: func(args []string) (CommandResult, error) {
		if len(args) == 1 && args[0] == "agent" {
			return CommandResult{Stderr: []byte(help), ExitCode: 2}, nil
		}
		return CommandResult{ExitCode: 1}, nil
	}}
	h := New("", sr.run)
	kinds, err := h.AgentKinds(context.Background())
	if err != nil {
		t.Fatalf("AgentKinds (stderr/exit2): %v", err)
	}
	if !reflect.DeepEqual(kinds, []string{"claude", "codex", "gemini"}) {
		t.Errorf("kinds = %v", kinds)
	}
}

func TestFindKindsLine(t *testing.T) {
	t.Parallel()
	line, ok := findKindsLine("noise\n  kinds: claude | codex \nmore noise")
	if !ok || line != "claude | codex" {
		t.Errorf("findKindsLine = %q, %v", line, ok)
	}
	if _, ok := findKindsLine("just help text\nno kinds here"); ok {
		t.Error("expected no kinds line")
	}
}

func TestParseKinds(t *testing.T) {
	t.Parallel()
	// Future/unknown-but-valid kind, dedup of valid duplicates, whitespace.
	got, err := parseKinds("claude | codex |claude| new-harness_9")
	if err != nil {
		t.Fatalf("parseKinds valid: %v", err)
	}
	want := []string{"claude", "codex", "new-harness_9"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseKinds = %v, want %v", got, want)
	}
	// Any invalid component rejects the whole authoritative line — it must never
	// be silently narrowed to the valid subset.
	if _, err := parseKinds("claude | BAD!! | codex"); err == nil {
		t.Error("expected rejection of an invalid kind component")
	}
	// Any empty component (e.g. a stray or trailing pipe) also rejects the line.
	if _, err := parseKinds("claude | | codex"); err == nil {
		t.Error("expected rejection of an empty kind component")
	}
	if _, err := parseKinds("claude|codex|"); err == nil {
		t.Error("expected rejection of a trailing empty component")
	}
	if _, err := parseKinds("   "); err == nil {
		t.Error("expected rejection of an empty kinds list")
	}
}

func TestAgentKindsNoFallbackOnFailure(t *testing.T) {
	t.Parallel()
	// Discovery output without a kinds line must error, never fall back.
	sr := &scriptedRunner{fn: func([]string) (CommandResult, error) {
		return CommandResult{Stdout: []byte("herdr agent commands:\n  herdr agent list\n"), ExitCode: 0}, nil
	}}
	h := New("", sr.run)
	if _, err := h.AgentKinds(context.Background()); err == nil {
		t.Fatal("expected discovery error when no kinds line is present")
	}
}

func TestAgentKindsRejectsInvalidComponent(t *testing.T) {
	t.Parallel()
	// A kinds line containing an invalid component must fail discovery entirely,
	// never return the valid subset.
	sr := &scriptedRunner{fn: func(args []string) (CommandResult, error) {
		if len(args) == 1 && args[0] == "agent" {
			return CommandResult{Stderr: []byte("  kinds: claude|codex|BAD!!|gemini\n"), ExitCode: 2}, nil
		}
		return CommandResult{ExitCode: 1}, nil
	}}
	h := New("", sr.run)
	if _, err := h.AgentKinds(context.Background()); err == nil {
		t.Fatal("expected discovery to reject an invalid kind component, not narrow it")
	}
}

func TestIsValidKind(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"claude", "codex", "new-harness_9", "a"} {
		if !IsValidKind(ok) {
			t.Errorf("IsValidKind(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "9lead", "Upper", "has space", "bad!", "-lead"} {
		if IsValidKind(bad) {
			t.Errorf("IsValidKind(%q) = true", bad)
		}
	}
}

func TestParseVersion(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"herdr 0.7.5":            "0.7.5",
		"herdr v0.7.5":           "0.7.5",
		"0.8.10 (build abc)":     "0.8.10",
		"no version here":        "",
		"herdr 0.7 (incomplete)": "",
	}
	for in, want := range cases {
		if got := parseVersion(in); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersionAndPluginCapability(t *testing.T) {
	t.Parallel()
	sr := &scriptedRunner{fn: func(args []string) (CommandResult, error) {
		if len(args) >= 1 && args[0] == "--version" {
			return CommandResult{Stdout: []byte("herdr 0.7.5"), ExitCode: 0}, nil
		}
		return CommandResult{ExitCode: 0}, nil // plugin --help
	}}
	h := New("", sr.run)
	v, err := h.Version(context.Background())
	if err != nil || v != "0.7.5" {
		t.Fatalf("Version = %q, %v", v, err)
	}
	if !h.PluginCommandAvailable(context.Background()) {
		t.Error("plugin command should be available")
	}
}

func TestParseContext(t *testing.T) {
	t.Parallel()
	c, err := ParseContext(`{"workspace_id":"ws1","tab_id":"t1","focused_pane_id":"p1","focused_pane_cwd":"/repo","workspace_cwd":"/ws"}`)
	if err != nil {
		t.Fatalf("ParseContext: %v", err)
	}
	if c.WorkspaceID != "ws1" || c.FocusedPaneCwd != "/repo" || c.WorkspaceCwd != "/ws" {
		t.Errorf("context = %+v", c)
	}
	// Empty is valid.
	if _, err := ParseContext(""); err != nil {
		t.Errorf("empty context should not error: %v", err)
	}
	// Malformed is an error.
	if _, err := ParseContext("{bad"); err == nil {
		t.Error("expected error for malformed context JSON")
	}
}

func TestContextForwardedActionEnvPreferred(t *testing.T) {
	t.Parallel()
	// The popup regenerates its own context (pointing at the popup pane); the
	// forwarded originals must override it for workspace and cwd selection.
	env := map[string]string{
		"HERDR_PLUGIN_CONTEXT_JSON":              `{"workspace_id":"popup-ws","focused_pane_cwd":"/popup/pane","workspace_cwd":"/popup/ws"}`,
		"HERDR_SHORTCUT_ACTION_WORKSPACE_ID":     "orig-ws",
		"HERDR_SHORTCUT_ACTION_FOCUSED_PANE_CWD": "/orig/pane",
		"HERDR_SHORTCUT_ACTION_WORKSPACE_CWD":    "/orig/ws",
	}
	c, err := ContextFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("ContextFromEnv: %v", err)
	}
	if c.WorkspaceID != "orig-ws" || c.FocusedPaneCwd != "/orig/pane" || c.WorkspaceCwd != "/orig/ws" {
		t.Errorf("forwarded originals not preferred: %+v", c)
	}
}

func TestActionEnvOmitsEmpty(t *testing.T) {
	t.Parallel()
	c := InvocationContext{WorkspaceID: "w", FocusedPaneCwd: "/f"} // WorkspaceCwd empty
	env := c.ActionEnv()
	if env[EnvActionWorkspaceID] != "w" || env[EnvActionFocusedPaneCwd] != "/f" {
		t.Errorf("ActionEnv = %v", env)
	}
	if _, ok := env[EnvActionWorkspaceCwd]; ok {
		t.Errorf("empty workspace cwd should be omitted: %v", env)
	}
}

func TestContextFromEnvFallback(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"HERDR_WORKSPACE_ID": "ws-env",
		"HERDR_PANE_ID":      "pane-env",
	}
	c, err := ContextFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("ContextFromEnv: %v", err)
	}
	if c.WorkspaceID != "ws-env" || c.FocusedPaneID != "pane-env" {
		t.Errorf("env fallback context = %+v", c)
	}
}

func TestDefaultRunnerRealExec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Success envelope on stdout, exit 0.
	res, err := DefaultRunner(ctx, os.Args[0], []string{"__herdr_helper__", "ok"})
	if err != nil {
		t.Fatalf("DefaultRunner ok: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(string(res.Stdout), `"type":"ok"`) {
		t.Errorf("ok result = %+v", res)
	}
	// Error envelope on stderr, exit 1.
	res, err = DefaultRunner(ctx, os.Args[0], []string{"__herdr_helper__", "err"})
	if err != nil {
		t.Fatalf("DefaultRunner err: %v", err)
	}
	if res.ExitCode != 1 || !strings.Contains(string(res.Stderr), "not_found") {
		t.Errorf("err result = %+v", res)
	}
	// Usage error, exit 2.
	res, err = DefaultRunner(ctx, os.Args[0], []string{"__herdr_helper__", "usage"})
	if err != nil {
		t.Fatalf("DefaultRunner usage: %v", err)
	}
	if res.ExitCode != 2 {
		t.Errorf("usage exit code = %d", res.ExitCode)
	}
}

func TestDefaultRunnerScrubsToken(t *testing.T) {
	// Not parallel: mutates the process environment.
	t.Setenv("SHORTCUT_API_TOKEN", "super-secret")
	res, err := DefaultRunner(context.Background(), os.Args[0], []string{"__herdr_helper__", "envcheck"})
	if err != nil {
		t.Fatalf("DefaultRunner: %v", err)
	}
	if got := string(res.Stdout); got != "TOKEN=UNSET" {
		t.Fatalf("child saw the token: %q", got)
	}
}

func TestDefaultRunnerViaAdapter(t *testing.T) {
	t.Parallel()
	// Exercise the full adapter path with the real runner using the "ok" helper.
	h := New(os.Args[0], nil)
	raw, err := h.run(context.Background(), []string{"__herdr_helper__", "ok"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(string(raw), `"type":"ok"`) {
		t.Errorf("raw result = %s", raw)
	}
}

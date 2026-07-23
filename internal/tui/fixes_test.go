package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
)

// manyKinds returns up to n distinct valid harness kinds for dialog tests.
func manyKinds(n int) []string {
	base := []string{
		"pi", "claude", "codex", "gemini", "cursor", "devin", "agy", "cline", "omp",
		"mastracode", "opencode", "copilot", "kimi", "kiro", "droid", "amp", "grok",
		"hermes", "kilo", "qodercli", "maki",
	}
	if n <= len(base) {
		return base[:n]
	}
	return base
}

// --- F6: mouse wheel scrolls the focused dialog region; q quits the dialog ---

func TestWheelScrollsFocusedHarnessRegion(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(3))
	deps.Kinds = manyKinds(21)
	// Short terminal so the harness window is much smaller than 21 kinds.
	m := loaded(t, deps, 60, 12)
	m, _ = step(t, m, key("enter"))                 // open dialog (cwd region focused)
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyTab}) // focus the harness region
	if !m.dialog.focusHarness {
		t.Fatal("Tab should focus the harness region")
	}
	// Wheel down enough to reach the last kind, proving every kind is reachable.
	for i := 0; i < 40; i++ {
		m, _ = step(t, m, wheel(false))
	}
	if m.dialog.kindSel != len(deps.Kinds)-1 {
		t.Errorf("wheel should scroll the harness region to the last kind; kindSel=%d", m.dialog.kindSel)
	}
	if last := deps.Kinds[len(deps.Kinds)-1]; !strings.Contains(m.View(), last) {
		t.Errorf("last kind %q should be reachable/visible by wheel:\n%s", last, m.View())
	}
	// Wheel up returns toward the top.
	for i := 0; i < 40; i++ {
		m, _ = step(t, m, wheel(true))
	}
	if m.dialog.kindSel != 0 {
		t.Errorf("wheel up should return to the first kind; kindSel=%d", m.dialog.kindSel)
	}
}

func TestWheelScrollsFocusedCwdRegion(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(3))
	repos := make([]Repository, 20)
	for i := range repos {
		repos[i] = Repository{Name: "repo", Path: "/r/" + string(rune('a'+i))}
	}
	deps.Repositories = repos
	m := loaded(t, deps, 60, 12)
	m, _ = step(t, m, key("enter")) // dialog opens with the cwd region focused
	if m.dialog.focusHarness {
		t.Fatal("dialog should start on the cwd region")
	}
	start := m.dialog.cwdSel
	m, _ = step(t, m, wheel(false))
	if m.dialog.cwdSel <= start {
		t.Errorf("wheel down should scroll the cwd region; cwdSel=%d", m.dialog.cwdSel)
	}
}

func TestQQuitsDialogWhenNotEditing(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m, _ = step(t, m, key("enter")) // open dialog (cwd region, custom not selected)
	m, cmd := step(t, m, key("q"))
	if !m.quitting || cmd == nil {
		t.Errorf("q should quit from a non-editing dialog region (quitting=%v)", m.quitting)
	}
}

func TestQTypesIntoCustomPathWhenEditing(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m, _ = step(t, m, key("enter")) // open dialog
	// Move the cwd selection to the last choice (Custom path).
	for i := 0; i < len(m.dialog.choices); i++ {
		m, _ = step(t, m, key("down"))
	}
	if !m.dialog.customFocused() {
		t.Fatal("expected the custom-path input to be focused")
	}
	m, _ = step(t, m, key("q"))
	if m.quitting {
		t.Fatal("q must not quit while editing the custom path")
	}
	if !strings.Contains(m.dialog.custom.value(), "q") {
		t.Errorf("q should be typed into the custom path: %q", m.dialog.custom.value())
	}
}

// --- F8: sanitize untrusted input; display-cell-aware text input ---

func TestOneLineDropsUnsafeFormatting(t *testing.T) {
	t.Parallel()
	// 'a', RLO (Cf), 'b', line separator (Zl), 'c', ZWJ (Cf), 'd', BOM (Cf),
	// 'e', tab, 'f'.
	in := "a‮b c‍d\ufeffe\tf"
	got := oneLine(in)
	for _, bad := range []rune{'‮', ' ', '‍', '\ufeff'} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("oneLine kept unsafe rune %U: %q", bad, got)
		}
	}
	for _, want := range []string{"a", "b", "e", "f"} {
		if !strings.Contains(got, want) {
			t.Errorf("oneLine dropped safe content %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\t") {
		t.Errorf("tab should be collapsed to a space: %q", got)
	}
}

func TestTextInputRejectsUnsafeRunes(t *testing.T) {
	t.Parallel()
	var ti textInput
	// 'a', RLO, line sep, tab, newline, BEL, zero-width space, 'b'.
	for _, r := range []rune{'a', '‮', ' ', '\t', '\n', 0x07, '​', 'b'} {
		ti.insert(r)
	}
	if ti.value() != "ab" {
		t.Errorf("insert kept unsafe runes: %q", ti.value())
	}
}

func TestTextInputRenderIsCellAware(t *testing.T) {
	t.Parallel()
	var ti textInput
	ti.setValue(strings.Repeat("你", 20)) // 20 wide glyphs = 40 display cells
	got := ti.render(10, true)
	if displayWidth(got) > 10 {
		t.Errorf("render exceeded the field width: %d cells (%q)", displayWidth(got), got)
	}
	// Combining marks stay attached to their base rather than inflating width.
	ti.setValue(strings.Repeat("á", 8)) // 8 base 'a's + combining acutes = 8 cells
	if w := displayWidth(ti.render(4, false)); w > 4 {
		t.Errorf("combining marks widened render: %d cells", w)
	}
}

// --- F9: exact, copyable recovery command; preserve state; confirm resubmit ---

func failingDeps(stage LaunchStage, recovery string) Deps {
	deps := okDeps(makeStories(1))
	deps.Launch = func(context.Context, shortcut.ResolvedStory, string, string) LaunchResult {
		return LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-1", Stage: stage, Recovery: recovery, Err: errors.New("boom")}
	}
	return deps
}

func TestRecoveryShownExactlyWithoutWhitespaceCollapse(t *testing.T) {
	t.Parallel()
	// Multiple spaces inside the quoted argument must survive to the screen.
	recovery := "herdr agent prompt 'sc-1' 'a   b'"
	m := loaded(t, failingDeps(StageAgentStarted, recovery), 120, 30)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd())
	if !strings.Contains(m.View(), "a   b") {
		t.Errorf("recovery command whitespace was collapsed:\n%s", m.View())
	}
}

func TestRecoveryCopyToClipboard(t *testing.T) {
	t.Parallel()
	recovery := "herdr agent prompt 'sc-1' 'do   the   thing'"
	var copied string
	deps := failingDeps(StageTabCreated, recovery)
	deps.Clipboard = func(s string) error { copied = s; return nil }
	m := loaded(t, deps, 120, 30)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd()) // failure with recovery
	m, ccmd := step(t, m, key("c"))
	if ccmd == nil {
		t.Fatal("c should issue a clipboard copy command")
	}
	m, _ = step(t, m, ccmd())
	if copied != recovery {
		t.Errorf("clipboard got %q, want the exact recovery %q", copied, recovery)
	}
	if !strings.Contains(m.View(), "Copied") {
		t.Errorf("expected copy feedback:\n%s", m.View())
	}
}

func TestRecoveryHorizontalScroll(t *testing.T) {
	t.Parallel()
	recovery := "herdr agent prompt 'sc-1' '" + strings.Repeat("x", 200) + "'"
	m := loaded(t, failingDeps(StageTabCreated, recovery), 40, 20)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd())
	if m.recoveryHScroll != 0 {
		t.Fatalf("scroll should start at 0, got %d", m.recoveryHScroll)
	}
	m, _ = step(t, m, key("right"))
	if m.recoveryHScroll != 1 {
		t.Errorf("right should advance the recovery horizontal scroll, got %d", m.recoveryHScroll)
	}
	// Scrolling far right is clamped, never past the command.
	for i := 0; i < 500; i++ {
		m, _ = step(t, m, key("right"))
	}
	if m.recoveryHScroll > m.maxRecoveryWidth() {
		t.Errorf("horizontal scroll not clamped: %d > %d", m.recoveryHScroll, m.maxRecoveryWidth())
	}
	m, _ = step(t, m, key("left"))
	if m.recoveryHScroll < 0 {
		t.Error("horizontal scroll went negative")
	}
}

func TestPartialLaunchPreservedAcrossEscAndResumed(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	var launches, resumes int
	deps.Launch = func(context.Context, shortcut.ResolvedStory, string, string) LaunchResult {
		launches++
		return LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-1", Stage: StageTabCreated, Err: errors.New("start failed"), Recovery: "cmd"}
	}
	deps.Resume = func(context.Context, LaunchResult, shortcut.ResolvedStory, string) LaunchResult {
		resumes++
		return LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-1", Stage: StageComplete}
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter")) // open dialog
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd()) // failure (StageTabCreated) -> pending preserved
	m, _ = step(t, m, key("esc"))
	if m.launchResult != nil {
		t.Fatal("Esc should dismiss the failure overlay")
	}
	if m.dialog == nil {
		t.Fatal("the dialog should still be available after Esc")
	}
	// Launching the same story/kind again must resume, not create a new tab.
	m, cmd2 := step(t, m, key("enter"))
	if cmd2 == nil {
		t.Fatal("expected a launch/resume command")
	}
	m, _ = step(t, m, cmd2())
	if resumes != 1 {
		t.Errorf("re-launch of the same story/kind should resume once, got %d", resumes)
	}
	if launches != 1 {
		t.Errorf("a duplicate fresh launch must not run; launches=%d", launches)
	}
}

// --- C2: quit keys ignored while a launch is in flight ---

func TestQuitKeysIgnoredWhileLaunching(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(1)), 100, 30)
	m, _ = step(t, m, key("enter"))    // open dialog
	m, cmd := step(t, m, key("enter")) // launch -> launching=true (cmd not run)
	if !m.launching || cmd == nil {
		t.Fatal("expected launching state")
	}
	for _, k := range []string{"ctrl+c", "q", "esc"} {
		mm, c := step(t, m, key(k))
		if mm.quitting || c != nil {
			t.Errorf("%q must be ignored while launching (quitting=%v)", k, mm.quitting)
		}
	}
}

// --- C3: pending includes cwd; strict match; block mismatch; confirm resubmit ---

func TestPendingBlocksMismatchedCwdLaunch(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	deps.Repositories = []Repository{{Name: "A", Path: "/repo/a"}, {Name: "B", Path: "/repo/b"}}
	deps.FocusedPaneCwd, deps.WorkspaceCwd = "", ""
	var launches, resumes int
	deps.Launch = func(context.Context, shortcut.ResolvedStory, string, string) LaunchResult {
		launches++
		return LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-1", Stage: StageTabCreated, Err: errors.New("start failed"), Recovery: "cmd"}
	}
	deps.Resume = func(context.Context, LaunchResult, shortcut.ResolvedStory, string) LaunchResult {
		resumes++
		return LaunchResult{Stage: StageComplete}
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter"))    // dialog; cwd choice 0 = /repo/a
	m, cmd := step(t, m, key("enter")) // launch with /repo/a
	m, _ = step(t, m, cmd())           // fail -> pending{cwd=/repo/a}
	m, _ = step(t, m, key("esc"))      // back to the dialog
	m, _ = step(t, m, key("down"))     // select /repo/b
	if m.dialog.choices[m.dialog.cwdSel].path != "/repo/b" {
		t.Fatalf("expected /repo/b selected, got %+v", m.dialog.choices[m.dialog.cwdSel])
	}
	m, cmd2 := step(t, m, key("enter")) // launch with a DIFFERENT cwd
	if cmd2 != nil {
		t.Error("a cwd-mismatched launch must be blocked (no command)")
	}
	if launches != 1 || resumes != 0 {
		t.Errorf("mismatched launch must not start or resume; launches=%d resumes=%d", launches, resumes)
	}
	if m.dialog == nil || !strings.Contains(m.dialog.errText, "pending") {
		t.Errorf("expected an actionable pending message, got %q", m.dialog.errText)
	}
}

func TestStageAgentStartedPendingViaDialogConfirms(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	var resumes int
	deps.Launch = func(context.Context, shortcut.ResolvedStory, string, string) LaunchResult {
		return LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-1", Stage: StageAgentStarted, Err: errors.New("prompt failed"), Recovery: "cmd"}
	}
	deps.Resume = func(context.Context, LaunchResult, shortcut.ResolvedStory, string) LaunchResult {
		resumes++
		return LaunchResult{Stage: StageComplete}
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd())      // StageAgentStarted failure -> pending
	m, _ = step(t, m, key("esc")) // back to the dialog
	m, cmd2 := step(t, m, key("enter"))
	if cmd2 != nil {
		t.Error("must not auto-resume a StageAgentStarted pending reached via the dialog")
	}
	if resumes != 0 {
		t.Errorf("resume must not run before confirmation; resumes=%d", resumes)
	}
	if !m.confirmResubmit || !strings.Contains(m.View(), "Resubmit the prompt?") {
		t.Errorf("expected the resubmit confirmation, got confirm=%v", m.confirmResubmit)
	}
	m, cmd3 := step(t, m, key("y"))
	if cmd3 == nil {
		t.Fatal("confirming should issue the resume command")
	}
	m, _ = step(t, m, cmd3())
	if resumes != 1 {
		t.Errorf("confirmed resubmit should resume once; resumes=%d", resumes)
	}
}

// --- C5: no-data error / credentials screen flattens and scrolls at min height ---

func TestCredentialsScreenScrollsAtMinHeight(t *testing.T) {
	t.Parallel()
	deps := okDeps(nil)
	deps.Load = func(context.Context) LoadResult {
		return LoadResult{Err: CredentialsError{Cause: errors.New("no token")}}
	}
	// Minimum height (forces vertical scrolling) with a width that fits the exact
	// commands, so the complete instructions are recoverable by scrolling.
	m := loaded(t, deps, 80, minHeight)
	if !m.blockingError() {
		t.Fatal("expected a blocking credentials error")
	}
	// Never overflows the height.
	if got := strings.Count(m.View(), "\n") + 1; got != minHeight {
		t.Errorf("no-data screen has %d rows, want %d", got, minHeight)
	}
	// Every line is flattened (no embedded newlines) so scrolling is exact.
	for _, ln := range m.noDataLines() {
		if strings.Contains(ln, "\n") {
			t.Errorf("no-data line not flattened: %q", ln)
		}
	}
	// The complete, exact setup instructions are reachable by scrolling.
	seen := m.View()
	for i := 0; i < len(m.noDataLines()); i++ {
		m, _ = step(t, m, key("down"))
		seen += "\n" + m.View()
	}
	for _, want := range []string{"SHORTCUT_API_TOKEN", "token_command", "api-tokens"} {
		if !strings.Contains(seen, want) {
			t.Errorf("setup instruction %q not reachable by scrolling", want)
		}
	}
}

func TestNoDataErrorScreenFitsAtMinHeight(t *testing.T) {
	t.Parallel()
	deps := okDeps(nil)
	deps.Load = func(context.Context) LoadResult {
		return LoadResult{Err: &shortcut.APIError{StatusCode: 500, Message: strings.Repeat("verylongerror ", 30)}}
	}
	m := loaded(t, deps, minWidth, minHeight)
	if !m.blockingError() {
		t.Fatal("expected a blocking load error")
	}
	if got := strings.Count(m.View(), "\n") + 1; got != minHeight {
		t.Errorf("error screen has %d rows, want %d (footer must not overflow)", got, minHeight)
	}
}

// --- F13: states fit or scroll at minimum dimensions without overlap ---

func TestDialogFitsAtSmallHeightsWithoutOverlap(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(3))
	deps.Kinds = manyKinds(21)
	for h := minHeight; h <= 20; h++ {
		for _, withErr := range []bool{false, true} {
			m := loaded(t, deps, minWidth, h)
			m, _ = step(t, m, key("enter"))
			if withErr {
				m.dialog.errText = "invalid directory"
			}
			dr := m.dialogLayout()
			buttonRow := h - 1
			end := buttonRow
			if withErr {
				if dr.errorRow < 0 || dr.errorRow >= buttonRow {
					t.Fatalf("h=%d error row %d must sit above the buttons at %d", h, dr.errorRow, buttonRow)
				}
				end = dr.errorRow
			}
			if dr.harnessTop+dr.harnessCount > dr.cwdLabelRow {
				t.Errorf("h=%d harness rows overlap the cwd label", h)
			}
			if dr.cwdTop+dr.cwdCount > end {
				t.Errorf("h=%d cwd rows overlap the error/buttons (cwdTop=%d count=%d end=%d)", h, dr.cwdTop, dr.cwdCount, end)
			}
			if dr.harnessCount < 1 || dr.cwdCount < 1 {
				t.Errorf("h=%d must show at least one harness and one cwd row: %+v", h, dr)
			}
			if got := strings.Count(m.View(), "\n") + 1; got != h {
				t.Errorf("h=%d dialog View has %d rows, want %d", h, got, h)
			}
		}
	}
}

func TestResultAndHelpFitAtMinHeight(t *testing.T) {
	t.Parallel()
	// Help scrolls within the minimum height.
	m := loaded(t, okDeps(makeStories(3)), minWidth, minHeight)
	m, _ = step(t, m, key("?"))
	if got := strings.Count(m.View(), "\n") + 1; got > minHeight {
		t.Errorf("help overflows min height: %d rows", got)
	}
	// A long failure result scrolls within the minimum height.
	m2 := loaded(t, failingDeps(StageAgentStarted, strings.Repeat("y", 400)), minWidth, minHeight)
	m2, _ = step(t, m2, key("enter"))
	m2, cmd := step(t, m2, key("enter"))
	m2, _ = step(t, m2, cmd())
	if got := strings.Count(m2.View(), "\n") + 1; got > minHeight {
		t.Errorf("failure result overflows min height: %d rows", got)
	}
}

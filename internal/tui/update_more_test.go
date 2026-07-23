package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
)

func TestSearchModeNavigation(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(5)), 100, 30)
	m, _ = step(t, m, key("/"))
	// Down/up move the list selection while searching.
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("down in search should move selection, cursor=%d", m.cursor)
	}
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("up in search should move selection, cursor=%d", m.cursor)
	}
	// Type, then left/right/backspace edit the query.
	m, _ = step(t, m, key("story"))
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.search.value() != "stor" {
		t.Errorf("search value = %q", m.search.value())
	}
	// Enter leaves search focus but keeps the filter.
	m, _ = step(t, m, key("enter"))
	if m.searchFocused {
		t.Error("enter should leave search focus")
	}
}

func TestPageKeys(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(100)), 100, 20)
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if m.cursor == 0 {
		t.Error("pgdown should move the cursor down a page")
	}
	top := m.cursor
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.cursor >= top {
		t.Error("pgup should move the cursor up")
	}
	// Home/End keys.
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.cursor != len(m.filtered)-1 {
		t.Errorf("End should select last, cursor=%d", m.cursor)
	}
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyHome})
	if m.cursor != 0 {
		t.Errorf("Home should select first, cursor=%d", m.cursor)
	}
}

func TestHelpToggle(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m, _ = step(t, m, key("?"))
	if !m.showHelp || !strings.Contains(m.View(), "Keyboard & Mouse") {
		t.Errorf("? should show help")
	}
	// Any key dismisses help.
	m, _ = step(t, m, key("x"))
	if m.showHelp {
		t.Error("a key should dismiss help")
	}
}

func TestDialogKeyboardNavAndCancel(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(2)), 100, 30)
	m, _ = step(t, m, key("enter")) // open dialog (cwd region focused)
	start := m.dialog.cwdSel
	m, _ = step(t, m, key("j"))
	if m.dialog.cwdSel != start+1 {
		t.Errorf("j should move the cwd selection")
	}
	m, _ = step(t, m, key("k"))
	if m.dialog.cwdSel != start {
		t.Errorf("k should move the cwd selection back")
	}
	// Tab switches to the harness region; down changes the kind.
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if !m.dialog.focusHarness {
		t.Error("Tab should focus the harness region")
	}
	k0 := m.dialog.kindSel
	m, _ = step(t, m, key("j"))
	if m.dialog.kindSel != k0+1 {
		t.Error("j should change the selected kind when the harness region is focused")
	}
	// Esc closes the dialog.
	m, _ = step(t, m, key("esc"))
	if m.dialog != nil {
		t.Error("esc should close the dialog")
	}
}

func TestLaunchSuccessKeyExits(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(1)), 100, 30)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd()) // success result
	if !m.Launched() {
		t.Fatal("expected launch success")
	}
	// A key on the success screen quits.
	m2, qcmd := step(t, m, key("x"))
	if !m2.quitting || qcmd == nil {
		t.Error("a key on the success screen should quit")
	}
}

func TestRetryFromStartFailureResumesWithoutDuplicating(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	launchCalls, resumeCalls := 0, 0
	deps.Launch = func(context.Context, shortcut.ResolvedStory, string, string) LaunchResult {
		launchCalls++
		return LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-1", Stage: StageTabCreated, Err: errors.New("start failed")}
	}
	deps.Resume = func(_ context.Context, prev LaunchResult, _ shortcut.ResolvedStory, _ string) LaunchResult {
		resumeCalls++
		if prev.TabID != "tab-1" {
			t.Errorf("resume should receive the prior tab, got %q", prev.TabID)
		}
		return LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-1", Stage: StageComplete}
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd()) // start failure (StageTabCreated)
	if !strings.Contains(m.View(), "tab-1") {
		t.Errorf("failure view should show the tab id:\n%s", m.View())
	}
	m, cmd = step(t, m, key("r"))
	if cmd == nil {
		t.Fatal("start-failure retry should resume")
	}
	m, _ = step(t, m, cmd())
	if launchCalls != 1 || resumeCalls != 1 || !m.Launched() {
		t.Errorf("start-failure retry: launch=%d resume=%d launched=%v", launchCalls, resumeCalls, m.Launched())
	}
}

func TestPromptFailureDoesNotAutoResubmit(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	resumeCalls := 0
	deps.Launch = func(context.Context, shortcut.ResolvedStory, string, string) LaunchResult {
		return LaunchResult{TabID: "tab-1", PaneID: "pane-1", AgentName: "sc-1", Stage: StageAgentStarted, Recovery: "herdr agent prompt 'sc-1' '...'", Err: errors.New("prompt write failed")}
	}
	deps.Resume = func(context.Context, LaunchResult, shortcut.ResolvedStory, string) LaunchResult {
		resumeCalls++
		return LaunchResult{Stage: StageComplete}
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd()) // prompt failure (StageAgentStarted)
	// The failure view explains the prompt is not auto-resubmitted.
	if !strings.Contains(m.View(), "not resubmitted automatically") {
		t.Errorf("failure view should explain no auto-resubmit:\n%s", m.View())
	}
	// Pressing Enter/r must NOT auto-resubmit; it asks for explicit confirmation.
	m, cmd2 := step(t, m, key("r"))
	if cmd2 != nil {
		t.Error("pressing r must not resubmit without confirmation")
	}
	if resumeCalls != 0 {
		t.Errorf("Resume must not run before confirmation; got %d", resumeCalls)
	}
	if !strings.Contains(m.View(), "Resubmit the prompt?") {
		t.Errorf("expected the resubmit confirmation prompt:\n%s", m.View())
	}
	// Declining cancels the resubmit and returns to the failure view.
	m, _ = step(t, m, key("n"))
	if resumeCalls != 0 || strings.Contains(m.View(), "Resubmit the prompt?") {
		t.Errorf("declining must cancel the resubmit; calls=%d view=%s", resumeCalls, m.View())
	}
	// Confirming (r then y) resumes explicitly.
	m, _ = step(t, m, key("r"))
	m, cmd3 := step(t, m, key("y"))
	if cmd3 == nil {
		t.Fatal("confirming should issue the resume command")
	}
	m, _ = step(t, m, cmd3())
	if resumeCalls != 1 {
		t.Errorf("confirmed resubmit should call Resume once; got %d", resumeCalls)
	}
}

func TestRetryAfterEarlyFailureDoesFullLaunch(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	launchCalls := 0
	deps.Launch = func(context.Context, shortcut.ResolvedStory, string, string) LaunchResult {
		launchCalls++
		if launchCalls == 1 {
			return LaunchResult{Stage: StageFailedEarly, Err: errors.New("tab create failed")}
		}
		return LaunchResult{TabID: "t", AgentName: "sc-1", Stage: StageComplete}
	}
	deps.Resume = func(context.Context, LaunchResult, shortcut.ResolvedStory, string) LaunchResult {
		t.Fatal("early failure must retry via Launch, not Resume")
		return LaunchResult{}
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd()) // early failure
	m, cmd = step(t, m, key("r"))
	m, _ = step(t, m, cmd())
	if launchCalls != 2 || !m.Launched() {
		t.Errorf("early-failure retry should re-launch fully; calls=%d launched=%v", launchCalls, m.Launched())
	}
}

func TestSyncLoadingFirstFrame(t *testing.T) {
	t.Parallel()
	m := New(okDeps(makeStories(1)))
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	// Before any load completes, the model is already in the loading state.
	if !m.loading || !strings.Contains(m.View(), "Loading") {
		t.Errorf("first frame should show loading synchronously:\n%s", m.View())
	}
}

func TestCtrlCQuitsFromDialogAndHelp(t *testing.T) {
	t.Parallel()
	// From the dialog.
	m := loaded(t, okDeps(makeStories(1)), 100, 30)
	m, _ = step(t, m, key("enter"))
	m2, cmd := step(t, m, key("ctrl+c"))
	if !m2.quitting || cmd == nil {
		t.Error("ctrl+c should quit from the dialog")
	}
	// From help.
	h := loaded(t, okDeps(makeStories(1)), 100, 30)
	h, _ = step(t, h, key("?"))
	h2, cmd := step(t, h, key("ctrl+c"))
	if !h2.quitting || cmd == nil {
		t.Error("ctrl+c should quit from help")
	}
}

func TestQuittingIgnoresLateResults(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m, _ = step(t, m, key("q")) // quitting = true
	// A late load result arriving after quit must be ignored.
	m2, _ := step(t, m, loadResultMsg{gen: m.loadGen, res: LoadResult{Member: "late", Stories: makeStories(9)}})
	if m2.member == "late" || len(m2.stories) == 9 {
		t.Error("results must be ignored while quitting")
	}
}

func TestSearchHomeEndDelete(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(1)), 100, 30)
	m, _ = step(t, m, key("/"))
	m, _ = step(t, m, key("abcd"))
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyHome})
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyDelete}) // removes 'a'
	if m.search.value() != "bcd" {
		t.Errorf("Home+Delete failed: %q", m.search.value())
	}
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyEnd})
	m, _ = step(t, m, key("z"))
	if m.search.value() != "bcdz" {
		t.Errorf("End+type failed: %q", m.search.value())
	}
}

func TestHelpScrollsWhenTall(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(1)), 60, 8) // short terminal
	m, _ = step(t, m, key("?"))
	if len(m.helpLines()) <= m.height {
		t.Skip("help fits; scrolling not exercised at this size")
	}
	before := m.overlayScroll
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.overlayScroll <= before {
		t.Errorf("Down should scroll help; offset %d -> %d", before, m.overlayScroll)
	}
	// The rendered help never exceeds the terminal height.
	if got := strings.Count(m.View(), "\n") + 1; got != m.height {
		t.Errorf("help view = %d lines, want %d", got, m.height)
	}
	// A non-scroll key dismisses help.
	m, _ = step(t, m, key("x"))
	if m.showHelp {
		t.Error("a non-scroll key should dismiss help")
	}
}

func TestDialogHarnessSelectionChangesLaunchedKind(t *testing.T) {
	t.Parallel()
	var gotKind string
	deps := okDeps(makeStories(1)) // Kinds: claude, codex, gemini
	deps.Launch = func(_ context.Context, _ shortcut.ResolvedStory, _, kind string) LaunchResult {
		gotKind = kind
		return LaunchResult{TabID: "t", AgentName: "a", Stage: StageComplete}
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter")) // open dialog; default kind claude, cwd focused
	// Preview initially reflects the default kind.
	if !strings.Contains(m.View(), "sc-1-claude") {
		t.Errorf("preview should show default kind:\n%s", m.View())
	}
	// Tab to the harness region and move to gemini (claude -> codex -> gemini).
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.dialog.selectedKind() != "gemini" {
		t.Fatalf("selected kind = %q, want gemini", m.dialog.selectedKind())
	}
	// Preview updates live with the selected kind.
	if !strings.Contains(m.View(), "sc-1-gemini") {
		t.Errorf("preview should update to the selected kind:\n%s", m.View())
	}
	m, cmd := step(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter should launch")
	}
	m, _ = step(t, m, cmd())
	if gotKind != "gemini" {
		t.Errorf("launched kind = %q, want gemini", gotKind)
	}
}

func TestDialogMouseSelectsHarnessKind(t *testing.T) {
	t.Parallel()
	var gotKind string
	deps := okDeps(makeStories(1))
	deps.Launch = func(_ context.Context, _ shortcut.ResolvedStory, _, kind string) LaunchResult {
		gotKind = kind
		return LaunchResult{TabID: "t", AgentName: "a", Stage: StageComplete}
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter"))
	lay := m.computeLayout()
	// Click the second harness row (codex).
	m, _ = step(t, m, clickAt(3, lay.dialog.harnessTop+1))
	if m.dialog.selectedKind() != "codex" || !m.dialog.focusHarness {
		t.Fatalf("clicking harness row should select codex, got %q focus=%v", m.dialog.selectedKind(), m.dialog.focusHarness)
	}
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd())
	if gotKind != "codex" {
		t.Errorf("launched kind = %q, want codex", gotKind)
	}
}

func TestListEscQuitsWhenNoFilter(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m2, cmd := step(t, m, key("esc"))
	if !m2.quitting || cmd == nil {
		t.Error("esc at main level with no filter should quit")
	}
}

func TestCtrlCAlwaysQuits(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	// From search mode.
	m, _ = step(t, m, key("/"))
	m2, cmd := step(t, m, key("ctrl+c"))
	if !m2.quitting || cmd == nil {
		t.Error("ctrl+c should quit from search mode")
	}
}

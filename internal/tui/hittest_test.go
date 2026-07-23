package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMouseClickSelectsRow(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(10)), 100, 30)
	lay := m.computeLayout()
	// Click the third visible row.
	m, _ = step(t, m, clickAt(5, lay.listTop+2))
	if m.dialog == nil {
		t.Fatal("click on a row should open the dialog")
	}
	if m.dialog.story.ID != 3 {
		t.Errorf("clicked story = %d, want 3", m.dialog.story.ID)
	}
}

func TestMouseClickWithScrollOffset(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(50)), 100, 30)
	m.offset = 10 // scrolled down
	lay := m.computeLayout()
	m, _ = step(t, m, clickAt(3, lay.listTop+4))
	if m.dialog == nil {
		t.Fatal("expected dialog")
	}
	// index = offset(10) + 4 = 14 -> story ID 15
	if m.dialog.story.ID != 15 {
		t.Errorf("clicked story = %d, want 15", m.dialog.story.ID)
	}
}

func TestMouseClickHeaderDoesNothing(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(10)), 100, 30)
	for _, y := range []int{0, 1, 2} { // title, search, column header
		m2, _ := step(t, m, clickAt(5, y))
		if m2.dialog != nil {
			t.Errorf("click on header row %d should do nothing", y)
		}
	}
}

func TestMouseClickBelowListDoesNothing(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	lay := m.computeLayout()
	// Only 3 stories, so rows below listTop+3 are empty.
	m2, _ := step(t, m, clickAt(5, lay.listTop+5))
	if m2.dialog != nil {
		t.Error("click below the last story should do nothing")
	}
	// Click on the footer row.
	m3, _ := step(t, m, clickAt(5, m.height-1))
	if m3.dialog != nil {
		t.Error("click on footer should do nothing")
	}
}

func TestMouseHitTestingAfterResize(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(40)), 120, 40)
	// Resize to a shorter terminal; layout must recompute.
	m, _ = step(t, m, keyResize(80, 12))
	lay := m.computeLayout()
	if lay.listHeight != 12-headerRows-footerRows {
		t.Fatalf("listHeight = %d after resize", lay.listHeight)
	}
	// A click at the last visible row selects the expected story.
	m, _ = step(t, m, clickAt(2, lay.listTop+lay.visible-1))
	if m.dialog == nil {
		t.Fatal("expected dialog after in-range click")
	}
	wantID := int64(m.offset + lay.visible) // 1-based ID = index+1
	if m.dialog.story.ID != wantID {
		t.Errorf("clicked story = %d, want %d", m.dialog.story.ID, wantID)
	}
}

func TestHitTestingUnaffectedByMaliciousStoryText(t *testing.T) {
	t.Parallel()
	stories := makeStories(4)
	// A hostile name with embedded newlines and a raw ANSI escape must not add
	// rows or shift the geometry used for hit testing.
	stories[1].Name = "evil\nSECOND\x1b[31mLINE\r\nTHIRD"
	stories[1].State.Name = "In\nProgress"
	m := loaded(t, okDeps(stories), 100, 20)

	// The rendered view must have exactly `height` lines (no extra rows).
	if got := strings.Count(m.View(), "\n") + 1; got != 20 {
		t.Errorf("view has %d lines, want 20 (malicious text leaked rows)", got)
	}

	lay := m.computeLayout()
	// Clicking the hostile row (index 1) still selects that exact story.
	m, _ = step(t, m, clickAt(5, lay.listTop+1))
	if m.dialog == nil || m.dialog.story.ID != 2 {
		t.Fatalf("hit testing shifted by malicious text: %+v", m.dialog)
	}
}

func TestMouseWheelScroll(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(50)), 100, 30)
	if m.offset != 0 {
		t.Fatal("offset should start at 0")
	}
	m, _ = step(t, m, wheel(false)) // wheel down
	if m.offset != 3 {
		t.Errorf("wheel down offset = %d, want 3", m.offset)
	}
	m, _ = step(t, m, wheel(true)) // wheel up
	if m.offset != 0 {
		t.Errorf("wheel up offset = %d, want 0", m.offset)
	}
}

func TestMouseWheelClampsAtEnds(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(4)), 100, 30) // fewer than a page
	m, _ = step(t, m, wheel(false))
	if m.offset != 0 {
		t.Errorf("offset must stay 0 when everything fits; got %d", m.offset)
	}
}

func TestMouseDialogButtonHitTesting(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m, _ = step(t, m, key("enter")) // open dialog
	lay := m.computeLayout()

	// Click the Launch button.
	lb := lay.dialog.launchBtn
	m2, cmd := step(t, m, clickAt(lb.x0, lb.y))
	if cmd == nil {
		t.Error("clicking Launch should issue a launch command")
	}
	_ = m2

	// Click the Cancel button closes the dialog.
	cb := lay.dialog.cancelBtn
	m3, _ := step(t, m, clickAt(cb.x0+1, cb.y))
	if m3.dialog != nil {
		t.Error("clicking Cancel should close the dialog")
	}

	// Click a cwd choice row selects it and focuses the cwd region.
	m4, _ := step(t, m, clickAt(2, lay.dialog.cwdTop+1))
	if m4.dialog == nil || m4.dialog.cwdSel != 1 || m4.dialog.focusHarness {
		t.Errorf("clicking cwd row should select cwd index 1, got %+v", m4.dialog)
	}

	// Click a harness row selects that kind and focuses the harness region.
	m5, _ := step(t, m, clickAt(2, lay.dialog.harnessTop+1))
	if m5.dialog == nil || m5.dialog.kindSel != 1 || !m5.dialog.focusHarness {
		t.Errorf("clicking harness row should select kind index 1, got %+v", m5.dialog)
	}
}

func TestDialogFitsShortTerminalWithVisibleButtons(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	deps.FocusedPaneCwd = "/a"
	deps.WorkspaceCwd = "/b"
	deps.Repositories = []Repository{{Name: "R1", Path: "/r1"}, {Name: "R2", Path: "/r2"}, {Name: "R3", Path: "/r3"}, {Name: "R4", Path: "/r4"}}
	m := loaded(t, deps, 80, 14) // short: choices must scroll
	m, _ = step(t, m, key("enter"))
	// The dialog view is exactly the terminal height.
	if got := strings.Count(m.View(), "\n") + 1; got != 14 {
		t.Fatalf("dialog view = %d lines, want 14", got)
	}
	lay := m.computeLayout()
	// The Launch/Cancel buttons sit on the last row and are hittable.
	if lay.dialog.launchBtn.y != 13 {
		t.Errorf("launch button row = %d, want 13 (bottom)", lay.dialog.launchBtn.y)
	}
	m2, cmd := step(t, m, clickAt(lay.dialog.launchBtn.x0, lay.dialog.launchBtn.y))
	if cmd == nil {
		t.Error("Launch button must be clickable on a short terminal")
	}
	_ = m2
}

func TestDialogScrolledHarnessHitTesting(t *testing.T) {
	t.Parallel()
	// Many kinds force the harness window to scroll.
	deps := okDeps(makeStories(1))
	deps.Kinds = []string{"pi", "claude", "codex", "gemini", "cursor", "devin", "amp", "grok"}
	deps.DefaultKind = "claude"
	m := loaded(t, deps, 80, 16)
	m, _ = step(t, m, key("enter"))
	// Focus the harness region and move to the last kind so it scrolls.
	m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	for i := 0; i < len(m.dialog.kinds)-1; i++ {
		m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	lay := m.computeLayout()
	if lay.dialog.harnessOffset == 0 {
		t.Fatalf("expected the harness window to have scrolled; offset=0")
	}
	// Clicking the first visible harness row selects harnessOffset, not index 0.
	m2, _ := step(t, m, clickAt(3, lay.dialog.harnessTop))
	if m2.dialog.kindSel != lay.dialog.harnessOffset {
		t.Errorf("scrolled harness click selected %d, want %d", m2.dialog.kindSel, lay.dialog.harnessOffset)
	}
}

func TestMouseDialogClickOutsideDoesNothing(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m, _ = step(t, m, key("enter"))
	beforeK, beforeC := m.dialog.kindSel, m.dialog.cwdSel
	// Click far outside any region.
	m2, cmd := step(t, m, clickAt(90, 25))
	if cmd != nil {
		t.Error("click outside regions should not issue a command")
	}
	if m2.dialog == nil || m2.dialog.kindSel != beforeK || m2.dialog.cwdSel != beforeC {
		t.Error("click outside should not change dialog state")
	}
}

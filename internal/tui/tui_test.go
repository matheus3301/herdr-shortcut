package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
)

func init() {
	// Deterministic, ANSI-free rendering for substring assertions.
	lipgloss.SetColorProfile(termenv.Ascii)
}

var fixedNow = time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

func makeStories(n int) []shortcut.ResolvedStory {
	out := make([]shortcut.ResolvedStory, n)
	for i := range out {
		id := int64(i + 1)
		out[i] = shortcut.ResolvedStory{
			Story: shortcut.Story{
				ID:        id,
				Name:      fmt.Sprintf("Story number %d", id),
				StoryType: "feature",
				AppURL:    fmt.Sprintf("https://app.shortcut.com/o/story/%d/s", id),
			},
			State: shortcut.StoryState{Name: "In Progress", Type: "started", Known: true},
		}
	}
	return out
}

func okDeps(stories []shortcut.ResolvedStory) Deps {
	return Deps{
		Load: func(context.Context) LoadResult { return LoadResult{Member: "matheus", Stories: stories} },
		Launch: func(_ context.Context, _ shortcut.ResolvedStory, _, kind string) LaunchResult {
			return LaunchResult{TabID: "t1", PaneID: "p1", AgentName: "sc-1-" + kind}
		},
		OpenURL:     func(context.Context, string) error { return nil },
		StatDir:     func(p string) (string, error) { return p, nil },
		Now:         func() time.Time { return fixedNow },
		Kinds:       []string{"claude", "codex", "gemini"},
		DefaultKind: "claude",
		TabLabel: func(s shortcut.ResolvedStory, kind string) string {
			return fmt.Sprintf("SC-%d %s", s.ID, kind)
		},
		AgentName: func(s shortcut.ResolvedStory, kind string) string {
			return fmt.Sprintf("sc-%d-%s", s.ID, kind)
		},
		Repositories:   []Repository{{Name: "Main", Path: "/repo/main"}},
		FocusedPaneCwd: "/repo/focused",
		WorkspaceCwd:   "/repo/ws",
	}
}

func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}

// sized returns a model that has received its window size.
func sized(t *testing.T, deps Deps, w, h int) Model {
	t.Helper()
	m := New(deps)
	m, _ = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

// loaded returns a model whose initial load has completed synchronously.
func loaded(t *testing.T, deps Deps, w, h int) Model {
	t.Helper()
	m := sized(t, deps, w, h)
	m, cmd := step(t, m, triggerLoadMsg{})
	if cmd == nil {
		t.Fatal("expected a load command")
	}
	m, _ = step(t, m, cmd())
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func clickAt(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func keyResize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

func wheel(up bool) tea.MouseMsg {
	btn := tea.MouseButtonWheelDown
	if up {
		btn = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{Action: tea.MouseActionPress, Button: btn}
}

func TestInitialLoadingState(t *testing.T) {
	t.Parallel()
	m := sized(t, okDeps(makeStories(3)), 100, 30)
	m, _ = step(t, m, triggerLoadMsg{}) // starts load, does not complete
	if !m.loading {
		t.Fatal("expected loading state")
	}
	if !strings.Contains(m.View(), "Loading your Shortcut tasks") {
		t.Errorf("loading view:\n%s", m.View())
	}
}

func TestLoadedListView(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	v := m.View()
	for _, want := range []string{"Shortcut / My tasks", "@matheus", "3 results", "Story number 1", "SC-1"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
}

func TestEmptyQueue(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(nil), 100, 30)
	if !strings.Contains(m.View(), "No active Stories") {
		t.Errorf("expected empty message:\n%s", m.View())
	}
}

func TestLoadErrorAndRetry(t *testing.T) {
	t.Parallel()
	deps := okDeps(nil)
	apiErr := &shortcut.APIError{StatusCode: 401, Message: "auth failed"}
	deps.Load = func(context.Context) LoadResult { return LoadResult{Err: apiErr} }
	m := loaded(t, deps, 100, 30)
	v := m.View()
	if !strings.Contains(v, "Authentication failed") || !strings.Contains(v, "retry") {
		t.Errorf("expected auth error + retry:\n%s", v)
	}
}

func TestCredentialsMissingState(t *testing.T) {
	t.Parallel()
	deps := okDeps(nil)
	deps.Load = func(context.Context) LoadResult {
		return LoadResult{Err: CredentialsError{Cause: errors.New("no token")}}
	}
	m := loaded(t, deps, 100, 30)
	v := m.View()
	if !strings.Contains(v, "SHORTCUT_API_TOKEN") || !strings.Contains(v, "token_command") {
		t.Errorf("expected credential setup commands:\n%s", v)
	}
}

func TestRefreshPreservesList(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m, cmd := step(t, m, key("r"))
	if !m.refreshing {
		t.Error("expected refreshing state")
	}
	if !strings.Contains(m.View(), "Story number 1") {
		t.Error("list should be preserved while refreshing")
	}
	// Complete the refresh.
	m, _ = step(t, m, cmd())
	if m.refreshing {
		t.Error("refresh should have finished")
	}
}

func TestFilter(t *testing.T) {
	t.Parallel()
	stories := makeStories(3)
	stories[1].Name = "Fix the login bug"
	stories[1].StoryType = "bug"
	m := loaded(t, okDeps(stories), 100, 30)
	m, _ = step(t, m, key("/"))
	if !m.searchFocused {
		t.Fatal("expected search focus")
	}
	m, _ = step(t, m, key("bug"))
	if len(m.filtered) != 1 {
		t.Fatalf("expected 1 filtered story, got %d", len(m.filtered))
	}
	m, _ = step(t, m, key("zzz"))
	if len(m.filtered) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(m.filtered))
	}
	if !strings.Contains(m.View(), "No Stories match") {
		t.Errorf("expected no-match message:\n%s", m.View())
	}
	// Esc clears the filter.
	m, _ = step(t, m, key("esc"))
	if len(m.filtered) != 3 {
		t.Errorf("esc should clear filter, got %d", len(m.filtered))
	}
}

func TestSelectionKeys(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(5)), 100, 30)
	if m.cursor != 0 {
		t.Fatal("cursor should start at 0")
	}
	m, _ = step(t, m, key("j"))
	m, _ = step(t, m, key("j"))
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want 2", m.cursor)
	}
	m, _ = step(t, m, key("G"))
	if m.cursor != 4 {
		t.Errorf("G should go to last, cursor = %d", m.cursor)
	}
	m, _ = step(t, m, key("g"))
	if m.cursor != 0 {
		t.Errorf("g should go to first, cursor = %d", m.cursor)
	}
}

func TestEnterOpensDialog(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m, _ = step(t, m, key("j")) // select story 2
	m, _ = step(t, m, key("enter"))
	if m.dialog == nil {
		t.Fatal("expected dialog to open")
	}
	if m.dialog.story.ID != 2 {
		t.Errorf("dialog story = %d, want 2", m.dialog.story.ID)
	}
	v := m.View()
	for _, want := range []string{"Launch agent", "Agent harness:", "claude", "Working directory:", "Focused pane", "Workspace", "Main", "Custom path", "Cancel", "SC-2", "sc-2-claude"} {
		if !strings.Contains(v, want) {
			t.Errorf("dialog view missing %q:\n%s", want, v)
		}
	}
}

func TestDialogChoicesDeduped(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	deps.FocusedPaneCwd = "/same"
	deps.WorkspaceCwd = "/same" // duplicate should be dropped
	deps.Repositories = nil
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter"))
	// Choices: /same (once) + Custom = 2
	if len(m.dialog.choices) != 2 {
		t.Fatalf("expected 2 deduped choices, got %d: %+v", len(m.dialog.choices), m.dialog.choices)
	}
}

func TestDialogCustomPathValidation(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	deps.StatDir = func(p string) (string, error) {
		if p == "/good" {
			return "/good", nil
		}
		return "", errors.New("path does not exist")
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter"))
	// Navigate to the custom choice (last).
	for range m.dialog.choices {
		m, _ = step(t, m, key("down"))
	}
	if !m.dialog.customFocused() {
		t.Fatal("expected custom choice selected")
	}
	m, _ = step(t, m, key("/bad"))
	m, cmd := step(t, m, key("enter"))
	if cmd != nil {
		t.Fatal("invalid path must not launch")
	}
	if m.dialog == nil || m.dialog.errText == "" {
		t.Fatalf("expected inline error, dialog=%+v", m.dialog)
	}
	// Fix the path.
	for range "/bad" {
		m, _ = step(t, m, key("backspace"))
	}
	m, _ = step(t, m, key("/good"))
	m, cmd = step(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("valid path should launch")
	}
}

func TestLaunchSuccess(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(2)), 100, 30)
	m, _ = step(t, m, key("enter"))    // open dialog
	m, cmd := step(t, m, key("enter")) // launch
	if cmd == nil {
		t.Fatal("expected launch command")
	}
	if !m.launching {
		t.Error("expected launching state")
	}
	if !strings.Contains(m.View(), "Launching claude") {
		t.Errorf("expected launching view:\n%s", m.View())
	}
	m, _ = step(t, m, cmd()) // launch result
	if !m.Launched() {
		t.Fatal("expected successful launch")
	}
	if !strings.Contains(m.View(), "launched") {
		t.Errorf("expected success view:\n%s", m.View())
	}
}

func TestLaunchFailureAndRetry(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(2))
	calls := 0
	deps.Launch = func(context.Context, shortcut.ResolvedStory, string, string) LaunchResult {
		calls++
		if calls == 1 {
			return LaunchResult{Err: errors.New("tab create failed"), Recovery: "herdr agent prompt sc-1 ..."}
		}
		return LaunchResult{TabID: "t1", AgentName: "sc-1"}
	}
	m := loaded(t, deps, 100, 30)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	m, _ = step(t, m, cmd()) // failure
	v := m.View()
	if !strings.Contains(v, "Launch failed") || !strings.Contains(v, "Recover manually") {
		t.Errorf("expected failure view with recovery:\n%s", v)
	}
	if m.dialog == nil {
		t.Error("selected story/dialog should be preserved after failure")
	}
	// Retry succeeds.
	m, cmd = step(t, m, key("r"))
	if cmd == nil {
		t.Fatal("retry should issue a launch command")
	}
	m, _ = step(t, m, cmd())
	if !m.Launched() {
		t.Errorf("retry should succeed; calls=%d", calls)
	}
}

func TestDuplicateLaunchPrevented(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(1)), 100, 30)
	m, _ = step(t, m, key("enter"))
	m, cmd := step(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("first launch should issue a command")
	}
	// Second launch attempt while launching must be a no-op.
	_, cmd2 := step(t, m, key("enter"))
	if cmd2 != nil {
		t.Error("duplicate launch should be prevented while one is in progress")
	}
}

func TestResizeTooSmallAndBack(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 10, Height: 4})
	if !strings.Contains(m.View(), "Too small") {
		t.Errorf("expected too-small message:\n%s", m.View())
	}
	m, _ = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if !strings.Contains(m.View(), "Story number 1") {
		t.Errorf("expected list after resize back:\n%s", m.View())
	}
}

func TestOpenBrowser(t *testing.T) {
	t.Parallel()
	var opened string
	deps := okDeps(makeStories(2))
	deps.OpenURL = func(_ context.Context, u string) error { opened = u; return nil }
	m := loaded(t, deps, 100, 30)
	_, cmd := step(t, m, key("o"))
	if cmd == nil {
		t.Fatal("expected open command")
	}
	if msg := cmd(); msg != nil {
		if _, ok := msg.(openResultMsg); !ok {
			t.Fatalf("unexpected msg type %T", msg)
		}
	}
	if opened != "https://app.shortcut.com/o/story/1/s" {
		t.Errorf("opened URL = %q", opened)
	}
}

func TestOpenBrowserError(t *testing.T) {
	t.Parallel()
	deps := okDeps(makeStories(1))
	deps.OpenURL = func(context.Context, string) error { return errors.New("no browser") }
	m := loaded(t, deps, 100, 30)
	m, cmd := step(t, m, key("o"))
	m2, _ := step(t, m, cmd())
	if m2.openErr == "" {
		t.Error("expected openErr to be set")
	}
	if !strings.Contains(m2.View(), "no browser") {
		t.Errorf("footer should show open error:\n%s", m2.View())
	}
}

func TestQuit(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(1)), 100, 30)
	m2, cmd := step(t, m, key("q"))
	if !m2.quitting || cmd == nil {
		t.Error("q should quit")
	}
}

func TestLateLoadResponseIgnored(t *testing.T) {
	t.Parallel()
	m := loaded(t, okDeps(makeStories(3)), 100, 30)
	// A stale response from an earlier generation must be ignored.
	stale := loadResultMsg{gen: m.loadGen - 1, res: LoadResult{Member: "stale", Stories: nil}}
	m2, _ := step(t, m, stale)
	if m2.member != "matheus" || len(m2.stories) != 3 {
		t.Errorf("stale response should be ignored: member=%q stories=%d", m2.member, len(m2.stories))
	}
}

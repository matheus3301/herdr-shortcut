// Package tui implements the Bubble Tea task-picker UI: story list, local
// filtering, launch dialog, and mouse hit testing derived from the rendered
// geometry. All network and launch work is performed through injected
// dependencies as Bubble Tea commands so the Update loop never blocks.
package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
)

// Repository is a configured working-directory choice shown in the dialog.
type Repository struct {
	Name string
	Path string // already expanded by the caller
}

// LoadResult is returned by Deps.Load.
type LoadResult struct {
	Member  string
	Stories []shortcut.ResolvedStory
	Err     error
}

// LaunchStage records how far a launch progressed, so a retry resumes from the
// failed step instead of creating a duplicate tab or agent.
type LaunchStage int

const (
	// StageFailedEarly means the failure happened before (or at) tab creation, so
	// no Herdr resources exist and a full retry is safe.
	StageFailedEarly LaunchStage = iota
	// StageTabCreated means the tab exists but the agent was not started.
	StageTabCreated
	// StageAgentStarted means the agent started but the prompt was not submitted.
	StageAgentStarted
	// StageComplete means the launch succeeded.
	StageComplete
)

// LaunchResult is returned by Deps.Launch and Deps.Resume.
type LaunchResult struct {
	TabID     string
	PaneID    string
	AgentName string
	// Recovery is a copyable, credential-free command to finish a launch whose
	// prompt submission failed.
	Recovery string
	Stage    LaunchStage
	Err      error
}

// CredentialsError signals that no Shortcut token could be resolved. The TUI
// renders a tailored setup screen for it.
type CredentialsError struct{ Cause error }

func (e CredentialsError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "missing Shortcut credentials"
}

func (e CredentialsError) Unwrap() error { return e.Cause }

// Deps are the injected behaviors the model orchestrates. All functions must be
// safe to call from a goroutine.
type Deps struct {
	// Load resolves the member and Stories (already resolved and sorted).
	Load func(ctx context.Context) LoadResult
	// Launch runs the full launch algorithm for a Story in cwd with the selected
	// harness kind.
	Launch func(ctx context.Context, story shortcut.ResolvedStory, cwd, kind string) LaunchResult
	// Resume re-attempts only the remaining steps of a launch that already
	// created a tab (or started the agent), avoiding duplicate resources.
	Resume func(ctx context.Context, prev LaunchResult, story shortcut.ResolvedStory, kind string) LaunchResult
	// OpenURL opens a Story URL in the browser. It honors ctx for cancellation.
	OpenURL func(ctx context.Context, rawURL string) error
	// Clipboard copies text (e.g. a recovery command) to the system clipboard.
	// Optional; when nil the copy action reports that copying is unavailable.
	Clipboard func(text string) error
	// StatDir validates that path exists and is a directory, returning the
	// resolved path.
	StatDir func(path string) (string, error)
	// Now is an injectable clock.
	Now func() time.Time
	// Context is the base context for cancellation; defaults to Background.
	Context context.Context

	// Kinds are the agent-harness kinds discovered from the installed Herdr
	// binary. DefaultKind is the initially selected kind.
	Kinds       []string
	DefaultKind string

	// TabLabel and AgentName render the previewed launch identifiers for a Story
	// and the currently selected kind.
	TabLabel  func(story shortcut.ResolvedStory, kind string) string
	AgentName func(story shortcut.ResolvedStory, kind string) string

	// Dialog cwd sources.
	Repositories   []Repository
	FocusedPaneCwd string
	WorkspaceCwd   string
}

// Model is the Bubble Tea model.
type Model struct {
	deps    Deps
	styles  styles
	baseCtx context.Context

	width, height int
	gotSize       bool

	// data lifecycle
	loading    bool
	refreshing bool
	loadErr    error
	member     string
	stories    []shortcut.ResolvedStory
	filtered   []int
	updatedAt  time.Time
	loadGen    int
	loadCancel context.CancelFunc

	// selection / viewport
	cursor int // index into filtered
	offset int // first visible filtered index

	// search
	searchFocused bool
	search        textInput

	// dialog
	dialog *dialogState

	// launch
	launching    bool
	launchGen    int
	launchCancel context.CancelFunc
	launchErr    error
	launchResult *LaunchResult
	launchStory  shortcut.ResolvedStory
	launchCwd    string
	launchKind   string
	// pending preserves a partial launch (a tab/agent already created) so a retry
	// — from the failure screen or a later dialog launch of the same story/kind —
	// resumes instead of creating duplicate Herdr resources. It survives Esc.
	pending *pendingLaunch
	// recoveryHScroll horizontally scrolls the copyable recovery command so it is
	// shown exactly (never wrapped or whitespace-collapsed).
	recoveryHScroll int
	// confirmResubmit gates re-submitting a prompt that may already have been
	// delivered: it is never resubmitted without explicit confirmation.
	confirmResubmit bool
	copyNote        string // transient feedback after a clipboard copy

	// misc
	showHelp      bool
	openErr       string
	openGen       int
	openCancel    context.CancelFunc // cancels the in-flight browser open (serialized)
	overlayScroll int                // vertical scroll for help / result overlays
	quitting      bool
}

type dialogState struct {
	story shortcut.ResolvedStory
	// Harness selection.
	kinds   []string
	kindSel int
	// Working-directory selection.
	choices []cwdChoice
	cwdSel  int
	custom  textInput
	// focusHarness selects which region the up/down keys navigate.
	focusHarness bool
	errText      string
}

// selectedKind returns the currently selected harness kind.
func (d *dialogState) selectedKind() string {
	if d.kindSel < 0 || d.kindSel >= len(d.kinds) {
		return ""
	}
	return d.kinds[d.kindSel]
}

// customFocused reports whether the custom-path input is the active cwd choice.
func (d *dialogState) customFocused() bool {
	return !d.focusHarness && d.cwdSel >= 0 && d.cwdSel < len(d.choices) && d.choices[d.cwdSel].custom
}

type cwdChoice struct {
	label  string
	path   string
	custom bool
}

// pendingLaunch is a partially completed launch retained so a retry resumes the
// existing tab/agent instead of creating duplicates. A resume is allowed only
// when the Story, kind, and canonical cwd all match.
type pendingLaunch struct {
	result LaunchResult
	story  shortcut.ResolvedStory
	kind   string
	cwd    string // canonical cwd the pending tab was created with
}

// matches reports whether a new launch selection targets this pending launch.
func (p *pendingLaunch) matches(storyID int64, kind, cwd string) bool {
	return p != nil && p.story.ID == storyID && p.kind == kind && p.cwd == cwd
}

// Message types.
type (
	triggerLoadMsg struct{}
	loadResultMsg  struct {
		gen int
		res LoadResult
	}
	launchResultMsg struct {
		gen int
		res LaunchResult
	}
	openResultMsg struct {
		gen int
		err error
	}
	quitMsg struct{}
)

// New builds a Model from deps, applying safe defaults.
func New(deps Deps) Model {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	base := deps.Context
	if base == nil {
		base = context.Background()
	}
	return Model{
		deps:    deps,
		styles:  newStyles(),
		baseCtx: base,
		// Loading starts synchronously so the very first frame shows the loading
		// state rather than an empty screen.
		loading: true,
	}
}

// Init starts the first load.
func (m Model) Init() tea.Cmd {
	return func() tea.Msg { return triggerLoadMsg{} }
}

// now returns the current time via the injected clock.
func (m Model) now() time.Time { return m.deps.Now() }

// startLoad cancels any in-flight load and begins a new one, preserving the
// existing list while refreshing.
func (m *Model) startLoad() tea.Cmd {
	if m.loadCancel != nil {
		m.loadCancel()
	}
	m.loadGen++
	gen := m.loadGen
	ctx, cancel := context.WithCancel(m.baseCtx)
	m.loadCancel = cancel
	if len(m.stories) == 0 {
		m.loading = true
	} else {
		m.refreshing = true
	}
	m.openErr = ""
	load := m.deps.Load
	return func() tea.Msg {
		return loadResultMsg{gen: gen, res: load(ctx)}
	}
}

// refreshFiltered recomputes the filtered index set and clamps the cursor and
// viewport offset to valid ranges.
func (m *Model) refreshFiltered() {
	m.filtered = applyFilter(m.stories, m.search.value())
	m.clampCursor()
}

func (m *Model) clampCursor() {
	if len(m.filtered) == 0 {
		m.cursor = 0
		m.offset = 0
		return
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureCursorVisible()
}

// ensureCursorVisible adjusts the viewport offset so the cursor is on screen.
func (m *Model) ensureCursorVisible() {
	lay := m.computeLayout()
	if lay.listHeight <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+lay.listHeight {
		m.offset = m.cursor - lay.listHeight + 1
	}
	maxOffset := len(m.filtered) - lay.listHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// selectedStory returns the currently selected resolved story, if any.
func (m Model) selectedStory() (shortcut.ResolvedStory, bool) {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return shortcut.ResolvedStory{}, false
	}
	return m.stories[m.filtered[m.cursor]], true
}

// buildChoices assembles the deduplicated cwd choices for the dialog.
func (m Model) buildChoices() []cwdChoice {
	var choices []cwdChoice
	seen := make(map[string]bool)
	add := func(label, path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		choices = append(choices, cwdChoice{label: label, path: path})
	}
	add("Focused pane", m.deps.FocusedPaneCwd)
	add("Workspace", m.deps.WorkspaceCwd)
	for _, r := range m.deps.Repositories {
		add(r.Name, r.Path)
	}
	choices = append(choices, cwdChoice{label: "Custom path…", custom: true})
	return choices
}

// dialogKinds returns the harness kinds discovered from the installed Herdr
// binary. The app validates discovery (non-empty, default supported) before the
// picker runs, so this never fabricates a fallback list: an empty result means
// no harness rows rather than a silently invented default.
func (m Model) dialogKinds() []string {
	return m.deps.Kinds
}

// openDialog opens the launch dialog for the selected story, pre-selecting the
// configured default harness kind.
func (m *Model) openDialog() {
	story, ok := m.selectedStory()
	if !ok {
		return
	}
	kinds := m.dialogKinds()
	kindSel := 0
	for i, k := range kinds {
		if k == m.deps.DefaultKind {
			kindSel = i
			break
		}
	}
	m.dialog = &dialogState{
		story:        story,
		kinds:        kinds,
		kindSel:      kindSel,
		choices:      m.buildChoices(),
		focusHarness: false, // start on the cwd region; the default kind is set
	}
	m.searchFocused = false
}

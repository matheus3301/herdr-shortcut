package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
)

// launchSuccessLinger is how long the success screen shows before the popup
// exits, leaving the new tab focused.
const launchSuccessLinger = 800 * time.Millisecond

// errClipboardUnavailable is reported when no clipboard integration is wired.
var errClipboardUnavailable = errors.New("clipboard is unavailable")

// Update handles all messages. Network and launch work runs as commands so the
// loop never blocks.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.gotSize = true
		m.clampCursor()
		return m, nil
	case triggerLoadMsg:
		cmd := m.startLoad()
		return m, cmd
	case loadResultMsg:
		return m.onLoadResult(msg), nil
	case launchResultMsg:
		return m.onLaunchResult(msg)
	case openResultMsg:
		// Ignore stale or post-shutdown browser results.
		if m.quitting || msg.gen != m.openGen {
			return m, nil
		}
		if msg.err != nil {
			m.openErr = msg.err.Error()
		} else {
			m.openErr = ""
		}
		return m, nil
	case copyResultMsg:
		if m.quitting {
			return m, nil
		}
		if msg.err != nil {
			m.copyNote = "Copy failed: " + msg.err.Error()
		} else {
			m.copyNote = "Copied the recovery command to the clipboard."
		}
		return m, nil
	case quitMsg:
		return m.quit()
	case tea.KeyMsg:
		return m.onKey(msg)
	case tea.MouseMsg:
		return m.onMouse(msg)
	}
	return m, nil
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	if m.loadCancel != nil {
		m.loadCancel()
	}
	if m.launchCancel != nil {
		m.launchCancel()
	}
	if m.openCancel != nil {
		m.openCancel()
	}
	m.quitting = true
	return m, tea.Quit
}

func (m Model) onLoadResult(msg loadResultMsg) Model {
	if m.quitting || msg.gen != m.loadGen {
		return m // ignore late or post-shutdown response
	}
	m.loading = false
	m.refreshing = false
	if msg.res.Err != nil {
		m.loadErr = msg.res.Err
		return m
	}
	m.loadErr = nil
	m.member = msg.res.Member
	m.stories = msg.res.Stories
	m.updatedAt = m.now()
	m.refreshFiltered()
	return m
}

func (m Model) onLaunchResult(msg launchResultMsg) (tea.Model, tea.Cmd) {
	if m.quitting || msg.gen != m.launchGen {
		return m, nil // ignore late or post-shutdown response
	}
	m.launching = false
	res := msg.res
	m.launchResult = &res
	m.overlayScroll = 0
	m.recoveryHScroll = 0
	m.confirmResubmit = false
	m.copyNote = ""
	switch {
	case res.Err == nil:
		m.pending = nil // success: no partial resources remain
		return m, tea.Tick(launchSuccessLinger, func(time.Time) tea.Msg { return quitMsg{} })
	case res.Stage != StageFailedEarly:
		// A tab (and maybe an agent) exists; remember it (with the canonical cwd)
		// so a later retry — from this screen or a subsequent dialog launch of the
		// same Story, kind, and cwd — resumes instead of creating duplicates.
		m.pending = &pendingLaunch{result: res, story: m.launchStory, kind: m.launchKind, cwd: m.launchCwd}
	default:
		m.pending = nil // failed before any resource was created
	}
	return m, nil
}

// copyResultMsg reports the outcome of a clipboard copy.
type copyResultMsg struct{ err error }

// copyRecoveryCmd copies the exact recovery command to the clipboard.
func (m *Model) copyRecoveryCmd() tea.Cmd {
	if m.launchResult == nil || m.launchResult.Recovery == "" {
		return nil
	}
	if m.deps.Clipboard == nil {
		return func() tea.Msg { return copyResultMsg{err: errClipboardUnavailable} }
	}
	text := m.launchResult.Recovery
	cp := m.deps.Clipboard
	return func() tea.Msg { return copyResultMsg{err: cp(text)} }
}

// beginLaunch sets up launch state and returns a command that runs op, guarding
// against duplicate concurrent launches.
func (m *Model) beginLaunch(op func(ctx context.Context) LaunchResult) tea.Cmd {
	if m.launching {
		return nil
	}
	if m.launchCancel != nil {
		m.launchCancel()
	}
	m.launchGen++
	gen := m.launchGen
	ctx, cancel := context.WithCancel(m.baseCtx)
	m.launchCancel = cancel
	m.launching = true
	m.launchErr = nil
	m.launchResult = nil
	return func() tea.Msg {
		return launchResultMsg{gen: gen, res: op(ctx)}
	}
}

// startLaunch begins a fresh launch, creating a tab and starting the selected
// harness kind.
func (m *Model) startLaunch(story shortcut.ResolvedStory, cwd, kind string) tea.Cmd {
	m.launchStory = story
	m.launchCwd = cwd
	m.launchKind = kind
	launch := m.deps.Launch
	return m.beginLaunch(func(ctx context.Context) LaunchResult {
		return launch(ctx, story, cwd, kind)
	})
}

// resumeLaunch re-attempts only the remaining steps of a launch that already
// created a tab, so a retry never duplicates the tab or the agent.
func (m *Model) resumeLaunch(prev LaunchResult, story shortcut.ResolvedStory, kind string) tea.Cmd {
	if m.deps.Resume == nil {
		return m.startLaunch(story, m.launchCwd, kind)
	}
	resume := m.deps.Resume
	return m.beginLaunch(func(ctx context.Context) LaunchResult {
		return resume(ctx, prev, story, kind)
	})
}

func (m Model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While a launch command is in flight, ignore EVERY key — including Ctrl+C and
	// quit keys. Cancelling now could orphan a just-created tab or agent and lose
	// the recovery state; the launch result will arrive and re-enable input.
	if m.launching {
		return m, nil
	}
	// Ctrl+C quits from every other mode, including help and the launch overlays.
	if msg.Type == tea.KeyCtrlC {
		return m.quit()
	}
	if m.showHelp {
		if m.scrollOverlay(msg, len(m.helpLines())) {
			return m, nil
		}
		// Any other key dismisses help.
		m.showHelp = false
		m.overlayScroll = 0
		return m, nil
	}
	if m.launchResult != nil {
		if m.scrollOverlay(msg, len(m.resultLines())) {
			return m, nil
		}
		return m.onLaunchResultKey(msg)
	}
	if m.dialog != nil {
		return m.onDialogKey(msg)
	}
	if m.searchFocused {
		return m.onSearchKey(msg)
	}
	if m.blockingError() {
		// The no-data error/credentials screen scrolls vertically; other keys
		// (r/q/?/esc) fall through to the list handler.
		if m.scrollOverlay(msg, len(m.noDataLines())) {
			return m, nil
		}
	}
	return m.onListKey(msg)
}

func (m Model) onListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m.quit()
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "g", "home":
		m.cursor = 0
		m.ensureCursorVisible()
	case "G", "end":
		if n := len(m.filtered); n > 0 {
			m.cursor = n - 1
			m.ensureCursorVisible()
		}
	case "pgdown":
		m.pageCursor(1)
	case "pgup":
		m.pageCursor(-1)
	case "/":
		m.searchFocused = true
	case "enter":
		if _, ok := m.selectedStory(); ok {
			m.openDialog()
		}
	case "o":
		cmd := m.openBrowserCmd()
		return m, cmd
	case "r":
		cmd := m.startLoad()
		return m, cmd
	case "?":
		m.showHelp = true
		m.overlayScroll = 0
	case "esc":
		if m.search.value() != "" {
			m.search.clear()
			m.refreshFiltered()
			return m, nil
		}
		return m.quit()
	}
	return m, nil
}

func (m Model) onSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m.quit()
	case tea.KeyEnter:
		m.searchFocused = false
	case tea.KeyEsc:
		m.searchFocused = false
		m.search.clear()
		m.refreshFiltered()
	case tea.KeyUp:
		m.moveCursor(-1)
	case tea.KeyDown:
		m.moveCursor(1)
	case tea.KeyLeft:
		m.search.left()
	case tea.KeyRight:
		m.search.right()
	case tea.KeyHome:
		m.search.home()
	case tea.KeyEnd:
		m.search.end()
	case tea.KeyDelete:
		m.search.deleteForward()
		m.refreshFiltered()
	case tea.KeyBackspace:
		m.search.backspace()
		m.refreshFiltered()
	case tea.KeySpace:
		m.search.insert(' ')
		m.refreshFiltered()
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			m.search.insert(r)
		}
		m.refreshFiltered()
	}
	return m, nil
}

func (m Model) onDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.dialog
	switch msg.Type {
	case tea.KeyCtrlC:
		return m.quit()
	case tea.KeyEsc:
		m.dialog = nil
		return m, nil
	case tea.KeyEnter:
		return m.launchFromDialog()
	case tea.KeyTab, tea.KeyShiftTab:
		// Switch between the harness and working-directory regions.
		d.focusHarness = !d.focusHarness
		d.errText = ""
		return m, nil
	case tea.KeyUp:
		m.dialogMove(-1)
		return m, nil
	case tea.KeyDown:
		m.dialogMove(1)
		return m, nil
	}
	// Editing keys apply only to the custom-path input when it is focused.
	if d.customFocused() {
		switch msg.Type {
		case tea.KeyBackspace:
			d.custom.backspace()
		case tea.KeyDelete:
			d.custom.deleteForward()
		case tea.KeyLeft:
			d.custom.left()
		case tea.KeyRight:
			d.custom.right()
		case tea.KeyHome:
			d.custom.home()
		case tea.KeyEnd:
			d.custom.end()
		case tea.KeySpace:
			d.custom.insert(' ')
		case tea.KeyRunes:
			for _, r := range msg.Runes {
				d.custom.insert(r)
			}
		}
		return m, nil
	}
	// These apply only outside the custom-path editor (handled above), so q can
	// safely quit from a non-editing dialog region.
	switch msg.String() {
	case "q":
		return m.quit()
	case "j":
		m.dialogMove(1)
	case "k":
		m.dialogMove(-1)
	}
	return m, nil
}

func (m Model) onLaunchResultKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.launchResult.Err == nil {
		// Success screen: any key exits.
		return m.quit()
	}
	if m.confirmResubmit {
		return m.onResubmitConfirmKey(msg)
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m.quit()
	case "c":
		return m, m.copyRecoveryCmd()
	case "left", "h":
		if m.recoveryHScroll > 0 {
			m.recoveryHScroll--
		}
		return m, nil
	case "right", "l":
		m.scrollRecovery(1)
		return m, nil
	case "enter", "r":
		prev := *m.launchResult
		switch prev.Stage {
		case StageFailedEarly:
			// No Herdr resources were created; a full retry is safe.
			m.launchResult = nil
			cmd := m.startLaunch(m.launchStory, m.launchCwd, m.launchKind)
			return m, cmd
		case StageTabCreated:
			// The tab exists but the agent never started; resume without
			// creating a new tab (the prompt has not been submitted yet).
			m.launchResult = nil
			cmd := m.resumeLaunch(prev, m.launchStory, m.launchKind)
			return m, cmd
		default:
			// StageAgentStarted: the prompt may already have been delivered, so it
			// is never auto-resubmitted. Ask for explicit confirmation instead of a
			// silent no-op.
			m.confirmResubmit = true
			return m, nil
		}
	case "esc":
		// Return to the dialog (still open) or list, but PRESERVE the partial
		// launch (m.pending) so a later launch of the same story/kind resumes
		// rather than creating a duplicate tab.
		m.launchResult = nil
	}
	return m, nil
}

// onResubmitConfirmKey handles the explicit confirmation for resubmitting a
// prompt that may already have been delivered.
func (m Model) onResubmitConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m.quit()
	case "c":
		return m, m.copyRecoveryCmd()
	case "y":
		prev := *m.launchResult
		m.confirmResubmit = false
		m.launchResult = nil
		return m, m.resumeLaunch(prev, m.launchStory, m.launchKind)
	case "n", "esc":
		m.confirmResubmit = false
	}
	return m, nil
}

// scrollRecovery advances the recovery horizontal scroll, clamped so it never
// runs past the widest command line.
func (m *Model) scrollRecovery(delta int) {
	avail := m.width - 3
	if avail < 1 {
		avail = 1
	}
	maxOff := m.maxRecoveryWidth() - avail
	if maxOff < 0 {
		maxOff = 0
	}
	m.recoveryHScroll += delta
	if m.recoveryHScroll > maxOff {
		m.recoveryHScroll = maxOff
	}
	if m.recoveryHScroll < 0 {
		m.recoveryHScroll = 0
	}
}

func (m Model) launchFromDialog() (tea.Model, tea.Cmd) {
	if m.launching {
		return m, nil
	}
	d := m.dialog
	if d.cwdSel < 0 || d.cwdSel >= len(d.choices) {
		return m, nil
	}
	choice := d.choices[d.cwdSel]
	var path string
	if choice.custom {
		path = strings.TrimSpace(d.custom.value())
		if path == "" {
			d.errText = "Enter a directory path."
			return m, nil
		}
	} else {
		path = choice.path
	}
	resolved, err := m.statDir(path)
	if err != nil {
		d.errText = err.Error()
		return m, nil
	}
	kind := d.selectedKind()
	if p := m.pending; p != nil {
		switch {
		case p.matches(d.story.ID, kind, resolved):
			// Same Story, kind, and cwd as the partial launch: resume the existing
			// tab/agent instead of creating duplicates.
			if p.result.Stage == StageAgentStarted {
				// The prompt may already have been delivered; require explicit
				// confirmation before resubmitting, even reached through the dialog.
				m.launchStory, m.launchKind, m.launchCwd = p.story, p.kind, p.cwd
				res := p.result
				m.launchResult = &res
				m.confirmResubmit = true
				m.dialog = nil
				return m, nil
			}
			return m, m.resumeLaunch(p.result, d.story, kind)
		default:
			// A different launch is still pending; never overwrite or silently
			// replace it. Block with an actionable message.
			d.errText = fmt.Sprintf("Finish the pending launch of SC-%d (%s) first — reopen it and retry, or quit to discard it.", p.story.ID, p.kind)
			return m, nil
		}
	}
	return m, m.startLaunch(d.story, resolved, kind)
}

func (m Model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.launching {
		return m, nil
	}
	// Wheel scrolling routes to whichever region is active, so every list row,
	// every harness kind, every cwd choice (including Custom path), and the
	// help/result overlays stay mouse-reachable even in short terminals.
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.wheelScroll(-1)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.wheelScroll(1)
		return m, nil
	}
	if m.showHelp {
		return m, nil
	}
	lay := m.computeLayout()
	if lay.tooSmall {
		return m, nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if m.launchResult != nil {
		return m, nil
	}
	if m.dialog != nil {
		return m.onDialogClick(msg.X, msg.Y, lay)
	}
	return m.onListClick(msg.X, msg.Y, lay)
}

// wheelScroll routes a wheel tick (dir = ±1) to the active scrollable region.
func (m *Model) wheelScroll(dir int) {
	switch {
	case m.showHelp:
		m.adjustOverlayScroll(dir*3, len(m.helpLines()))
	case m.launchResult != nil:
		m.adjustOverlayScroll(dir*3, len(m.resultLines()))
	case m.dialog != nil:
		// Scroll the focused harness/cwd region by moving its selection, which
		// drives the region's scroll window.
		m.dialogMove(dir)
	default:
		m.scroll(dir * 3)
	}
}

func (m Model) onListClick(x, y int, lay layoutInfo) (tea.Model, tea.Cmd) {
	// The row hit region is the visible list rectangle: x in [0,width), y in the
	// list viewport.
	if x < 0 || x >= m.width {
		return m, nil
	}
	if y >= lay.listTop && y < lay.listTop+lay.visible {
		idx := m.offset + (y - lay.listTop)
		if idx >= 0 && idx < len(m.filtered) {
			m.cursor = idx
			m.ensureCursorVisible()
			m.openDialog()
		}
	}
	return m, nil
}

func (m Model) onDialogClick(x, y int, lay layoutInfo) (tea.Model, tea.Cmd) {
	dr := lay.dialog
	if dr.launchBtn.hit(x, y) {
		return m.launchFromDialog()
	}
	if dr.cancelBtn.hit(x, y) {
		m.dialog = nil
		return m, nil
	}
	if x < 0 || x >= m.width {
		return m, nil
	}
	// Harness rows: map the visible window back through its scroll offset.
	if y >= dr.harnessTop && y < dr.harnessTop+dr.harnessCount {
		idx := dr.harnessOffset + (y - dr.harnessTop)
		if idx >= 0 && idx < len(m.dialog.kinds) {
			m.dialog.kindSel = idx
			m.dialog.focusHarness = true
			m.dialog.errText = ""
		}
		return m, nil
	}
	// Working-directory rows.
	if y >= dr.cwdTop && y < dr.cwdTop+dr.cwdCount {
		idx := dr.cwdOffset + (y - dr.cwdTop)
		if idx >= 0 && idx < len(m.dialog.choices) {
			m.dialog.cwdSel = idx
			m.dialog.focusHarness = false
			m.dialog.errText = ""
		}
	}
	return m, nil
}

// --- selection / viewport helpers ---

func (m *Model) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.ensureCursorVisible()
}

func (m *Model) pageCursor(dir int) {
	step := m.computeLayout().listHeight
	if step < 1 {
		step = 1
	}
	m.moveCursor(dir * step)
}

func (m *Model) scroll(delta int) {
	lay := m.computeLayout()
	if lay.listHeight <= 0 {
		return
	}
	maxOffset := len(m.filtered) - lay.listHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	m.offset += delta
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.cursor < m.offset {
		m.cursor = m.offset
	}
	if m.cursor >= m.offset+lay.listHeight {
		m.cursor = m.offset + lay.listHeight - 1
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// scrollOverlay adjusts the overlay scroll offset for Up/Down/PgUp/PgDn and
// reports whether it handled the key. total is the full content line count.
func (m *Model) scrollOverlay(msg tea.KeyMsg, total int) bool {
	page := m.height - 1
	if page < 1 {
		page = 1
	}
	switch msg.Type {
	case tea.KeyUp:
		m.adjustOverlayScroll(-1, total)
	case tea.KeyDown:
		m.adjustOverlayScroll(1, total)
	case tea.KeyPgUp:
		m.adjustOverlayScroll(-page, total)
	case tea.KeyPgDown:
		m.adjustOverlayScroll(page, total)
	default:
		return false
	}
	return true
}

// adjustOverlayScroll moves the overlay scroll offset by delta, clamped so it
// never scrolls past the content.
func (m *Model) adjustOverlayScroll(delta, total int) {
	m.overlayScroll += delta
	maxOff := total - (m.height - 1)
	if maxOff < 0 {
		maxOff = 0
	}
	if m.overlayScroll > maxOff {
		m.overlayScroll = maxOff
	}
	if m.overlayScroll < 0 {
		m.overlayScroll = 0
	}
}

// dialogMove moves the selection within the currently focused region.
func (m *Model) dialogMove(delta int) {
	d := m.dialog
	if d == nil {
		return
	}
	if d.focusHarness {
		if len(d.kinds) == 0 {
			return
		}
		d.kindSel = clampInt(d.kindSel+delta, 0, len(d.kinds)-1)
	} else {
		if len(d.choices) == 0 {
			return
		}
		d.cwdSel = clampInt(d.cwdSel+delta, 0, len(d.choices)-1)
	}
	d.errText = ""
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m Model) statDir(path string) (string, error) {
	if m.deps.StatDir != nil {
		return m.deps.StatDir(path)
	}
	return defaultStatDir(path)
}

func (m *Model) openBrowserCmd() tea.Cmd {
	story, ok := m.selectedStory()
	if !ok || m.deps.OpenURL == nil {
		return nil
	}
	// Serialize opens: cancel any in-flight open so only the latest runs, and use
	// a generation id to ignore stale results.
	if m.openCancel != nil {
		m.openCancel()
	}
	m.openGen++
	gen := m.openGen
	ctx, cancel := context.WithCancel(m.baseCtx)
	m.openCancel = cancel
	url := story.AppURL
	open := m.deps.OpenURL
	return func() tea.Msg {
		defer cancel()
		return openResultMsg{gen: gen, err: open(ctx, url)}
	}
}

package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
)

const (
	headerRows = 3
	footerRows = 1
)

// layoutInfo is the geometry shared by rendering and mouse hit testing so both
// agree on where each interactive element lives.
type layoutInfo struct {
	tooSmall   bool
	listTop    int
	listHeight int
	visible    int
	dialog     dialogRegions
}

type dialogRegions struct {
	previewRow      int // row of the Tab/Agent preview, or -1 when dropped
	harnessLabelRow int
	harnessTop      int // first screen row of the harness window
	harnessCount    int // number of kinds visible in the window
	harnessOffset   int // index of the first visible kind
	cwdLabelRow     int
	cwdTop          int // first screen row of the cwd window
	cwdCount        int // number of cwd choices visible in the window
	cwdOffset       int // index of the first visible cwd choice
	errorRow        int // row of the inline error, or -1 when no error is shown
	launchBtn       buttonRegion
	cancelBtn       buttonRegion
}

// scrollOffset returns the window offset that keeps sel visible within a window
// of the given size over count items.
func scrollOffset(sel, count, window int) int {
	if window <= 0 || count <= window {
		return 0
	}
	off := 0
	if sel >= window {
		off = sel - window + 1
	}
	if off > count-window {
		off = count - window
	}
	if off < 0 {
		off = 0
	}
	return off
}

type buttonRegion struct{ y, x0, x1 int }

func (b buttonRegion) hit(x, y int) bool {
	return b.x1 > b.x0 && y == b.y && x >= b.x0 && x < b.x1
}

const (
	launchLabel = "[ Launch agent ]"
	cancelLabel = "[ Cancel ]"
	buttonGap   = 3
)

// computeLayout derives the current geometry from model state. It is the single
// source of truth for both View and mouse handling.
func (m Model) computeLayout() layoutInfo {
	var li layoutInfo
	if m.width < minWidth || m.height < minHeight {
		li.tooSmall = true
		return li
	}
	if m.dialog != nil {
		li.dialog = m.dialogLayout()
		return li
	}
	li.listTop = headerRows
	li.listHeight = m.height - headerRows - footerRows
	if li.listHeight < 0 {
		li.listHeight = 0
	}
	remaining := len(m.filtered) - m.offset
	if remaining < 0 {
		remaining = 0
	}
	li.visible = min(li.listHeight, remaining)
	return li
}

// dialogLayout computes the dialog geometry. A fixed header (top) and the
// Launch/Cancel buttons (bottom) are always visible; the harness and cwd lists
// each scroll within their own window in the region between, so the dialog fits
// any terminal height at or above the minimum without overlapping rows. The
// inline error takes its own row only when present, so no content is hidden
// beneath it.
func (m Model) dialogLayout() dialogRegions {
	d := m.dialog
	nk, nc := len(d.kinds), len(d.choices)
	header, previewRow := dialogHeader(m.height)
	buttonRow := m.height - 1

	// Content occupies [header, contentEnd). The error, when shown, claims its own
	// row just above the buttons instead of overlaying a list row.
	contentEnd := buttonRow
	errorRow := -1
	if d.errText != "" {
		errorRow = buttonRow - 1
		contentEnd = errorRow
	}

	// Content rows hold two section labels plus the two scrollable windows.
	avail := contentEnd - header
	if avail < 4 {
		avail = 4 // guaranteed by minHeight; keeps the math non-overlapping
	}
	windows := avail - 2
	hWin := (windows + 1) / 2
	if hWin > nk {
		hWin = nk
	}
	if hWin < 1 {
		hWin = 1
	}
	cWin := windows - hWin
	if cWin < 1 {
		cWin = 1
	}
	if cWin > nc {
		cWin = nc
	}
	// Give any slack the cwd list does not need back to the harness list.
	if slack := (windows - hWin) - cWin; slack > 0 && hWin+slack <= nk {
		hWin += slack
	}

	harnessLabelRow := header
	harnessTop := header + 1
	cwdLabelRow := harnessTop + hWin
	cwdTop := cwdLabelRow + 1
	lw := displayWidth(launchLabel)
	cw := displayWidth(cancelLabel)
	return dialogRegions{
		previewRow:      previewRow,
		harnessLabelRow: harnessLabelRow,
		harnessTop:      harnessTop,
		harnessCount:    hWin,
		harnessOffset:   scrollOffset(d.kindSel, nk, hWin),
		cwdLabelRow:     cwdLabelRow,
		cwdTop:          cwdTop,
		cwdCount:        cWin,
		cwdOffset:       scrollOffset(d.cwdSel, nc, cWin),
		errorRow:        errorRow,
		launchBtn:       buttonRegion{y: buttonRow, x0: 0, x1: lw},
		cancelBtn:       buttonRegion{y: buttonRow, x0: lw + buttonGap, x1: lw + buttonGap + cw},
	}
}

// dialogHeader returns the number of fixed header rows and the preview row (or
// -1 when the Tab/Agent preview is dropped). It shrinks the header as the
// terminal gets shorter so the two selection regions, the error, and the buttons
// always fit without overlap.
func dialogHeader(height int) (rows, previewRow int) {
	switch {
	case height < 12:
		return 2, -1 // title, story only — drop the preview to save rows
	case height < 16:
		return 3, 2 // + preview
	default:
		return 5, 3 // + blank spacing around the preview
	}
}

// View renders the current state.
func (m Model) View() string {
	if m.quitting && m.launchResult == nil {
		return ""
	}
	if !m.gotSize {
		return "Loading…"
	}
	lay := m.computeLayout()
	if lay.tooSmall {
		return m.renderTooSmall()
	}
	if m.showHelp {
		return m.renderHelp()
	}
	if m.launching {
		return m.renderLaunching()
	}
	if m.launchResult != nil {
		return m.renderLaunchResult()
	}
	if m.dialog != nil {
		return m.renderDialog()
	}
	if m.blockingError() {
		// A failed initial load (or missing credentials) with no list to show: a
		// full-screen, vertically scrollable state so the complete, exact setup
		// instructions are reachable even at the minimum height.
		return m.renderNoData()
	}
	return m.renderList(lay)
}

// blockingError reports whether the whole screen is a load failure with no list
// to show (excluding an in-progress (re)load, which shows the loading state).
func (m Model) blockingError() bool {
	return len(m.stories) == 0 && m.loadErr != nil && !m.loading
}

// noDataLines is the full, flattened content for the no-data error/credentials
// screen. Every wrapped line is a separate element so renderScroll can scroll it
// exactly, exposing all setup instructions.
func (m Model) noDataLines() []string {
	member := "@?"
	if m.member != "" {
		member = "@" + oneLine(m.member)
	}
	head := []string{
		m.styles.title.Render("Shortcut / My tasks"),
		m.styles.subtle.Render(member),
		"",
	}
	body := m.renderLoadError() // returns the credentials help for a CredentialsError
	tail := []string{
		"",
		m.styles.footer.Render("r retry · q quit · ? help · ↑↓ scroll"),
	}
	return flattenLines(append(append(head, body...), tail...))
}

func (m Model) renderNoData() string { return m.renderScroll(m.noDataLines()) }

func (m Model) renderTooSmall() string {
	// Short lines that fit even a very narrow terminal, routed through renderScroll
	// so the message fits (or scrolls) rather than overflowing a 1-row screen.
	return m.renderScroll([]string{
		"Too small.",
		fmt.Sprintf("Min %d×%d.", minWidth, minHeight),
	})
}

func (m Model) helpLines() []string {
	return []string{
		m.styles.title.Render("Shortcut — Keyboard & Mouse"),
		"",
		"  j / k, ↓ / ↑     Move selection",
		"  g / G, Home/End  First / last",
		"  PgDn / PgUp      Page the list",
		"  /                Filter (Esc clears)",
		"  Enter            Open launch dialog",
		"  o                Open Story in browser",
		"  r                Refresh from Shortcut",
		"  ?                Toggle this help",
		"  q / Ctrl+C       Quit",
		"",
		"  Mouse: wheel scrolls, click a row to launch,",
		"  click a cwd or a dialog button in the dialog.",
		"",
		m.styles.subtle.Render("↑/↓/PgUp/PgDn scroll · press any other key to return."),
	}
}

func (m Model) renderHelp() string { return m.renderScroll(m.helpLines()) }

// renderScroll fits lines to the terminal height, showing a vertical window
// with a position indicator when the content is taller than the screen.
func (m Model) renderScroll(lines []string) string {
	if m.height <= 0 {
		return ""
	}
	if len(lines) <= m.height {
		out := make([]string, 0, m.height)
		for _, l := range lines {
			out = append(out, truncate(l, m.width))
		}
		for len(out) < m.height {
			out = append(out, "")
		}
		return strings.Join(out, "\n")
	}
	view := m.height - 1 // reserve one row for the indicator
	off := m.overlayScroll
	if off > len(lines)-view {
		off = len(lines) - view
	}
	if off < 0 {
		off = 0
	}
	out := make([]string, 0, m.height)
	for _, l := range lines[off : off+view] {
		out = append(out, truncate(l, m.width))
	}
	out = append(out, m.styles.subtle.Render(truncate(fmt.Sprintf("  lines %d–%d of %d · ↑/↓/PgUp/PgDn", off+1, off+view, len(lines)), m.width)))
	return strings.Join(out, "\n")
}

// agentLabel names the harness in user-facing text, using the selected kind when
// known and a generic term otherwise. It is never Claude-specific.
func agentLabel(kind string) string {
	if strings.TrimSpace(kind) == "" {
		return "agent"
	}
	return oneLine(kind)
}

func (m Model) launchingLines() []string {
	story := m.launchStory
	return []string{
		m.styles.title.Render("Launching " + agentLabel(m.launchKind)),
		"",
		fmt.Sprintf("  SC-%d  %s", story.ID, oneLine(story.Name)),
		"",
		m.styles.accent.Render("  Creating tab and starting the agent…"),
	}
}

func (m Model) renderLaunching() string { return m.renderScroll(m.launchingLines()) }

func (m Model) resultLines() []string {
	r := m.launchResult
	if r.Err == nil {
		return []string{
			m.styles.successText.Render("✓ " + agentLabel(m.launchKind) + " launched"),
			"",
			fmt.Sprintf("  Agent:  %s", oneLine(r.AgentName)),
			fmt.Sprintf("  Tab:    %s", oneLine(r.TabID)),
			fmt.Sprintf("  Pane:   %s", oneLine(r.PaneID)),
			"",
			m.styles.subtle.Render("  Closing…"),
		}
	}
	if m.confirmResubmit {
		return m.resubmitConfirmLines()
	}
	lines := []string{
		m.styles.errorText.Render("✗ Launch failed"),
		"",
		indentWrap(oneLine(r.Err.Error()), m.width, 2),
	}
	// After tab creation the tab is kept; report its IDs so the user can find it.
	if r.TabID != "" {
		lines = append(lines,
			"",
			m.styles.subtle.Render(indentWrap(fmt.Sprintf("The tab was created and left open — tab %s, pane %s, agent %s.", oneLine(r.TabID), oneLine(r.PaneID), oneLine(r.AgentName)), m.width, 2)),
		)
	}
	lines = append(lines, m.recoverySection(r.Recovery)...)
	if m.copyNote != "" {
		lines = append(lines, "", m.styles.subtle.Render("  "+oneLine(m.copyNote)))
	}
	var hint string
	if r.Stage == StageAgentStarted {
		hint = "The prompt may already have been delivered; it is not resubmitted automatically. c copy · Enter resubmit (confirm) · ←/→ scroll · Esc back · q quit"
	} else {
		hint = "Enter/r retry · c copy · ←/→ scroll · Esc back · q quit"
	}
	// indentWrap may itself produce multiple lines; split so scrolling is exact.
	lines = append(lines, "")
	lines = append(lines, strings.Split(m.styles.subtle.Render(indentWrap(hint, m.width, 2)), "\n")...)
	return flattenLines(lines)
}

// recoverySection renders the copyable recovery command through a horizontal
// viewport so it is shown EXACTLY: no wrapping, no whitespace collapsing, and no
// lossy truncation. Long or multi-line commands are scrolled with ←/→, and `c`
// copies the exact command to the clipboard.
func (m Model) recoverySection(recovery string) []string {
	if recovery == "" {
		return nil
	}
	lines := []string{"", m.styles.subtle.Render("  Recover manually (c to copy):")}
	avail := m.width - 2
	if avail < 1 {
		avail = 1
	}
	for _, cmdLine := range strings.Split(recovery, "\n") {
		lead := "  "
		if m.recoveryHScroll > 0 {
			lead = "‹ "
		}
		body := hSlice(cmdLine, m.recoveryHScroll, avail-1)
		if displayWidth(cmdLine) > m.recoveryHScroll+avail-1 {
			body += "›"
		}
		lines = append(lines, lead+body)
	}
	return lines
}

// resubmitConfirmLines renders the explicit confirmation shown before a prompt
// that may already have been delivered is resubmitted.
func (m Model) resubmitConfirmLines() []string {
	r := m.launchResult
	lines := []string{
		m.styles.errorText.Render("⚠ Resubmit the prompt?"),
		"",
		indentWrap("The agent started but the prompt submission failed, so the prompt MAY already have been delivered. Resubmitting could duplicate it.", m.width, 2),
		"",
		m.styles.subtle.Render("  y resubmit · n/Esc cancel · c copy · q quit"),
	}
	lines = append(lines, m.recoverySection(r.Recovery)...)
	return flattenLines(lines)
}

// maxRecoveryWidth is the display width of the widest recovery command line, used
// to clamp horizontal scrolling.
func (m Model) maxRecoveryWidth() int {
	if m.launchResult == nil {
		return 0
	}
	w := 0
	for _, l := range strings.Split(m.launchResult.Recovery, "\n") {
		if d := displayWidth(l); d > w {
			w = d
		}
	}
	return w
}

func (m Model) renderLaunchResult() string { return m.renderScroll(m.resultLines()) }

// flattenLines splits any multi-line entries so each slice element is one line,
// which keeps scroll math exact.
func flattenLines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, l := range in {
		out = append(out, strings.Split(l, "\n")...)
	}
	return out
}

func (m Model) renderList(lay layoutInfo) string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteByte('\n')
	b.WriteString(m.renderSearchLine())
	b.WriteByte('\n')

	idW := m.widestID()
	cols := computeColumns(m.width, idW)
	b.WriteString(m.styles.header.Render(padRight(m.renderColumnHeader(cols), m.width)))
	b.WriteByte('\n')

	body := m.renderListBody(lay, cols)
	b.WriteString(body)
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m Model) renderHeader() string {
	title := m.styles.title.Render("Shortcut / My tasks")
	var status string
	switch {
	case m.loading:
		status = "Loading…"
	case m.refreshing:
		status = "Refreshing…"
	case m.loadErr != nil && len(m.stories) > 0:
		if k := classifyError(m.loadErr); k != "" {
			status = "⚠ " + k
		} else {
			status = "⚠ refresh failed"
		}
	default:
		status = "updated " + humanAge(m.now(), m.updatedAt) + " ago"
	}
	member := "@?"
	if m.member != "" {
		member = "@" + oneLine(m.member)
	}
	info := fmt.Sprintf("%s · %d results · %s", member, len(m.filtered), status)
	return joinEnds(title, m.styles.subtle.Render(info), m.width)
}

func (m Model) renderSearchLine() string {
	label := "/ "
	var body string
	if m.searchFocused {
		body = m.search.render(m.width-len(label), true)
		return m.styles.accent.Render(label) + body
	}
	if m.search.value() != "" {
		body = m.search.render(m.width-len(label), false)
		return m.styles.subtle.Render(label) + body
	}
	return m.styles.subtle.Render("/ filter  (press / to search)")
}

func (m Model) renderColumnHeader(cols columnSet) string {
	var parts []string
	parts = append(parts, padRight("ID", cols.idW))
	if cols.typeW > 0 {
		parts = append(parts, padRight("TYPE", cols.typeW))
	}
	if cols.stateW > 0 {
		parts = append(parts, padRight("STATE", cols.stateW))
	}
	parts = append(parts, padRight("TITLE", cols.titleW))
	if cols.estimateW > 0 {
		parts = append(parts, padLeft("EST", cols.estimateW))
	}
	if cols.updatedW > 0 {
		parts = append(parts, padLeft("AGE", cols.updatedW))
	}
	if cols.deadlineW > 0 {
		parts = append(parts, padRight("DUE", cols.deadlineW))
	}
	return strings.Join(parts, strings.Repeat(" ", gap))
}

func (m Model) renderListBody(lay layoutInfo, cols columnSet) string {
	var lines []string
	// Special empty/error states occupy the list body.
	switch {
	case m.loading && len(m.stories) == 0:
		lines = m.centeredBox(lay.listHeight, m.styles.accent.Render("Loading your Shortcut tasks…"))
	// A load error with no stories is handled full-screen by renderNoData (see
	// View/blockingError), so it never reaches the list body.
	case len(m.stories) == 0:
		lines = m.centeredBox(lay.listHeight, m.styles.subtle.Render("No active Stories assigned to you. Press r to refresh."))
	case len(m.filtered) == 0:
		lines = m.centeredBox(lay.listHeight, m.styles.subtle.Render("No Stories match the filter. Press Esc to clear."))
	default:
		for i := 0; i < lay.visible; i++ {
			idx := m.offset + i
			story := m.stories[m.filtered[idx]]
			selected := idx == m.cursor
			lines = append(lines, m.renderStoryRow(story, cols, selected))
		}
		// Pad remaining list rows to keep the footer anchored.
		for len(lines) < lay.listHeight {
			lines = append(lines, "")
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m Model) renderStoryRow(s shortcut.ResolvedStory, cols columnSet, selected bool) string {
	var parts []string
	parts = append(parts, padRight("SC-"+strconv.FormatInt(s.ID, 10), cols.idW))
	if cols.typeW > 0 {
		parts = append(parts, padRight(shortType(oneLine(s.StoryType)), cols.typeW))
	}
	if cols.stateW > 0 {
		state := truncate(oneLine(s.State.Name), cols.stateW)
		if !selected {
			state = m.styles.stateStyle(s.State.Type).Render(state)
		}
		parts = append(parts, padRight(state, cols.stateW))
	}
	parts = append(parts, padRight(oneLine(s.Name), cols.titleW))
	if cols.estimateW > 0 {
		est := "·"
		if s.Estimate != nil {
			est = "~" + strconv.FormatInt(*s.Estimate, 10)
		}
		parts = append(parts, padLeft(est, cols.estimateW))
	}
	if cols.updatedW > 0 {
		age := "·"
		if s.UpdatedAt != nil {
			age = humanAge(m.now(), *s.UpdatedAt)
		}
		parts = append(parts, padLeft(age, cols.updatedW))
	}
	if cols.deadlineW > 0 {
		due := "·"
		if s.Deadline != nil {
			due = s.Deadline.UTC().Format("2006-01-02")
		}
		parts = append(parts, padRight(due, cols.deadlineW))
	}
	row := strings.Join(parts, strings.Repeat(" ", gap))
	row = padRight(row, m.width)
	if selected {
		return m.styles.selected.Render(row)
	}
	return row
}

func (m Model) renderLoadError() []string {
	msg := "Could not load Stories."
	hint := "Press r to retry."
	if credErr := asCredentialsError(m.loadErr); credErr != nil {
		return m.credentialsHelp()
	}
	if k := classifyError(m.loadErr); k != "" {
		msg = k
	}
	return []string{
		m.styles.errorText.Render("⚠ " + msg),
		"",
		m.styles.subtle.Render(wrapText(oneLine(m.loadErr.Error()), m.width-4)),
		"",
		m.styles.subtle.Render(hint),
	}
}

func (m Model) credentialsHelp() []string {
	return []string{
		m.styles.errorText.Render("⚠ No Shortcut API token found"),
		"",
		m.styles.subtle.Render("Set a token, then press r to retry:"),
		"",
		"  export SHORTCUT_API_TOKEN=…",
		m.styles.subtle.Render("  (create one at https://app.shortcut.com/settings/account/api-tokens)"),
		"",
		m.styles.subtle.Render("Or configure shortcut.token_command in config.toml."),
	}
}

func (m Model) renderFooter() string {
	// Error banners take priority and carry detail so failures are never silent.
	if m.openErr != "" {
		return m.styles.errorText.Render(truncate("⚠ could not open browser: "+oneLine(m.openErr), m.width))
	}
	if m.loadErr != nil && len(m.stories) > 0 {
		return m.styles.errorText.Render(truncate("⚠ refresh failed: "+oneLine(m.loadErr.Error())+" · press r to retry", m.width))
	}
	var hints string
	switch {
	case m.searchFocused:
		hints = "type to filter · Enter/Esc done"
	case m.loadErr != nil && len(m.stories) == 0:
		hints = "r retry · q quit · ? help"
	default:
		hints = "↑↓ move · Enter launch · / filter · o open · r refresh · ? help · q quit"
	}
	return m.styles.footer.Render(truncate(hints, m.width))
}

func (m Model) renderDialog() string {
	d := m.dialog
	dr := m.dialogLayout()
	kind := d.selectedKind()

	// Pre-size to the exact height so geometry is stable and never overflows.
	lines := make([]string, m.height)
	set := func(row int, s string) {
		if row >= 0 && row < m.height {
			lines[row] = truncate(s, m.width)
		}
	}

	set(0, m.styles.title.Render("Launch agent"))
	set(1, fmt.Sprintf("SC-%d  %s", d.story.ID, oneLine(d.story.Name)))
	if dr.previewRow >= 0 && m.deps.TabLabel != nil && m.deps.AgentName != nil {
		set(dr.previewRow, m.styles.subtle.Render(fmt.Sprintf("Tab: %s    Agent: %s",
			oneLine(m.deps.TabLabel(d.story, kind)), oneLine(m.deps.AgentName(d.story, kind)))))
	}

	set(dr.harnessLabelRow, m.sectionLabel("Agent harness:", d.focusHarness, dr.harnessOffset+dr.harnessCount, len(d.kinds)))
	for i := 0; i < dr.harnessCount; i++ {
		idx := dr.harnessOffset + i
		if idx >= len(d.kinds) {
			break
		}
		set(dr.harnessTop+i, m.renderKindRow(idx))
	}

	set(dr.cwdLabelRow, m.sectionLabel("Working directory:", !d.focusHarness, dr.cwdOffset+dr.cwdCount, len(d.choices)))
	for i := 0; i < dr.cwdCount; i++ {
		idx := dr.cwdOffset + i
		if idx >= len(d.choices) {
			break
		}
		set(dr.cwdTop+i, m.renderChoice(idx, d.choices[idx]))
	}

	if dr.errorRow >= 0 {
		set(dr.errorRow, m.styles.errorText.Render("⚠ "+oneLine(d.errText)))
	}
	set(m.height-1, m.renderButtons())
	return strings.Join(lines, "\n")
}

// sectionLabel renders a dialog region label, marking the focused region and
// showing a scroll indicator when the region's list is windowed.
func (m Model) sectionLabel(text string, focused bool, shown, total int) string {
	if total > shown {
		text = fmt.Sprintf("%s (%d/%d ↑↓)", text, shown, total)
	}
	if focused {
		return m.styles.accent.Render("▎ " + text)
	}
	return m.styles.subtle.Render("  " + text)
}

func (m Model) renderKindRow(i int) string {
	d := m.dialog
	selected := i == d.kindSel
	content := "  " + d.kinds[i]
	if selected {
		content = "▸ " + d.kinds[i]
	}
	if selected && d.focusHarness {
		return m.styles.selected.Render(padRight(content, m.width))
	}
	if selected {
		return m.styles.accent.Render(content)
	}
	return content
}

func (m Model) renderChoice(i int, c cwdChoice) string {
	d := m.dialog
	selected := i == d.cwdSel
	focused := selected && !d.focusHarness
	marker := "  "
	if selected {
		marker = "▸ "
	}
	var content string
	switch {
	case c.custom && focused:
		content = marker + "Custom path…: " + d.custom.render(max(m.width-20, 4), true)
	case c.custom && d.custom.value() != "":
		content = marker + "Custom path…: " + oneLine(d.custom.value())
	case c.custom:
		content = marker + "Custom path…"
	default:
		content = fmt.Sprintf("%s%s  %s", marker, oneLine(c.label), oneLine(c.path))
	}
	if focused {
		return m.styles.selected.Render(padRight(content, m.width))
	}
	if selected {
		return m.styles.accent.Render(content)
	}
	return content
}

func (m Model) renderButtons() string {
	launch := m.styles.buttonPri.Render(launchLabel)
	cancel := m.styles.button.Render(cancelLabel)
	return launch + strings.Repeat(" ", buttonGap) + cancel
}

// widestID returns the display width of the widest "SC-<id>" among stories.
func (m Model) widestID() int {
	w := 3
	for _, s := range m.stories {
		l := len("SC-" + strconv.FormatInt(s.ID, 10))
		if l > w {
			w = l
		}
	}
	return w
}

func (m Model) centeredBox(height int, content ...string) []string {
	lines := make([]string, 0, height)
	top := (height - len(content)) / 2
	if top < 0 {
		top = 0
	}
	for i := 0; i < top; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, content...)
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines[:max(height, 0)]
}

// joinEnds places left and right on one line padded to width. Widths are
// measured with lipgloss so embedded styling does not distort spacing.
func joinEnds(left, right string, width int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	if lw+rw+1 > width {
		return truncate(left, width)
	}
	return left + strings.Repeat(" ", width-lw-rw) + right
}

// indentWrap wraps s to the available width and prefixes every line with indent
// spaces, so long content (like a recovery command) stays fully visible.
func indentWrap(s string, width, indent int) string {
	pad := strings.Repeat(" ", indent)
	wrapped := wrapText(s, width-indent)
	lines := strings.Split(wrapped, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// wrapText soft-wraps text to width, joining lines with newlines.
func wrapText(s string, width int) string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			switch {
			case line == "":
				line = word
			case displayWidth(line)+1+displayWidth(word) <= width:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

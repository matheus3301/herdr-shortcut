package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Minimum usable terminal dimensions. Below these the UI renders a message
// rather than a broken or negative-dimension layout.
const (
	minWidth  = 34
	minHeight = 8
	minTitle  = 8
)

// displayWidth returns the terminal cell width of s, ignoring ANSI escapes.
func displayWidth(s string) int { return ansi.StringWidth(s) }

// truncate shortens s to display width w (ANSI-aware), adding an ellipsis when
// cut and always resetting any open styles.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// padRight truncates then right-pads s to display width w (ANSI-aware).
func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = truncate(s, w)
	if gap := w - displayWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// padLeft truncates then left-pads s to display width w (ANSI-aware).
func padLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = truncate(s, w)
	if gap := w - displayWidth(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

// oneLine collapses newlines and tabs to spaces and drops other control,
// format, and line/paragraph-separator characters (including raw ANSI escape
// bytes, bidi overrides, zero-width joiners, and U+2028/U+2029), so API-derived
// or pasted text cannot span multiple rows, reorder the display, or inject
// terminal control sequences. It is applied to every untrusted string before it
// participates in layout or hit testing.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r) || isUnsafeFormatting(r):
			return -1
		default:
			return r
		}
	}, s)
}

// isUnsafeFormatting reports whether r is a Unicode format (Cf) or line/paragraph
// separator (Zl/Zp) character. These are not "control" characters but can still
// hide content, reorder text (bidi), or start a new line in a terminal.
func isUnsafeFormatting(r rune) bool {
	return unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// hSlice returns the display-cell window of s beginning at cell offset `off` and
// spanning at most `width` cells, without wrapping or collapsing whitespace. It
// is a horizontal viewport: the caller scrolls `off` to reveal the rest, so
// nothing is permanently lost or altered. Wide glyphs that would straddle an
// edge are dropped to keep column alignment.
func hSlice(s string, off, width int) string {
	if width <= 0 || off < 0 {
		return ""
	}
	var b strings.Builder
	pos := 0     // cell position of the next rune
	emitted := 0 // cells written so far
	for _, r := range s {
		rw := displayWidth(string(r))
		if rw == 0 {
			// Zero-width (combining) rune: keep it only while inside the window.
			if pos >= off && pos < off+width {
				b.WriteRune(r)
			}
			continue
		}
		if pos+rw <= off {
			pos += rw
			continue // entirely before the window
		}
		if pos < off {
			pos += rw // straddles the left edge: drop to keep alignment
			continue
		}
		if emitted+rw > width {
			break // would exceed the window
		}
		b.WriteRune(r)
		emitted += rw
		pos += rw
	}
	return b.String()
}

// humanAge renders an approximate age of t relative to now.
func humanAge(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
}

// shortType abbreviates a Shortcut story type for the compact type column.
func shortType(t string) string {
	switch t {
	case "feature":
		return "feat"
	case "chore":
		return "chore"
	case "bug":
		return "bug"
	default:
		if t == "" {
			return "-"
		}
		return t
	}
}

// styles holds the palette. Shortcut purple is the accent, with strong
// selected-row contrast and per-state colors.
type styles struct {
	title       lipgloss.Style
	subtle      lipgloss.Style
	accent      lipgloss.Style
	selected    lipgloss.Style
	header      lipgloss.Style
	footer      lipgloss.Style
	errorText   lipgloss.Style
	successText lipgloss.Style
	stateStyles map[string]lipgloss.Style
	button      lipgloss.Style
	buttonPri   lipgloss.Style
}

func newStyles() styles {
	purple := lipgloss.Color("#8B5CF6")
	subtle := lipgloss.Color("#8A8A8A")
	return styles{
		title:       lipgloss.NewStyle().Bold(true).Foreground(purple),
		subtle:      lipgloss.NewStyle().Foreground(subtle),
		accent:      lipgloss.NewStyle().Foreground(purple),
		selected:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(purple),
		header:      lipgloss.NewStyle().Foreground(subtle),
		footer:      lipgloss.NewStyle().Foreground(subtle),
		errorText:   lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171")).Bold(true),
		successText: lipgloss.NewStyle().Foreground(lipgloss.Color("#34D399")).Bold(true),
		button:      lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")),
		buttonPri:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(purple),
		stateStyles: map[string]lipgloss.Style{
			"started":   lipgloss.NewStyle().Foreground(lipgloss.Color("#34D399")),
			"unstarted": lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24")),
			"backlog":   lipgloss.NewStyle().Foreground(lipgloss.Color("#60A5FA")),
			"done":      lipgloss.NewStyle().Foreground(subtle),
		},
	}
}

func (s styles) stateStyle(stateType string) lipgloss.Style {
	if st, ok := s.stateStyles[stateType]; ok {
		return st
	}
	return s.subtle
}

// columnSet describes which list columns are shown at the current width and how
// wide each is. Optional columns are dropped before the title is truncated.
type columnSet struct {
	idW       int
	typeW     int // 0 = hidden
	stateW    int // 0 = hidden
	updatedW  int // 0 = hidden
	estimateW int // 0 = hidden
	deadlineW int // 0 = hidden
	titleW    int
}

// gap is the single space between columns.
const gap = 1

// computeColumns allocates column widths for a given content width and the
// widest ID present, dropping optional columns (deadline, estimate, updated,
// type, state — in that order) until the title fits its minimum.
func computeColumns(width, idW int) columnSet {
	if idW < 3 {
		idW = 3
	}
	if idW > 9 {
		idW = 9
	}
	cs := columnSet{
		idW:       idW,
		typeW:     5,
		stateW:    14,
		updatedW:  4,
		estimateW: 4,
		deadlineW: 10,
	}
	// Drop order: last entry dropped first.
	type opt struct{ w *int }
	dropOrder := []*int{&cs.deadlineW, &cs.estimateW, &cs.updatedW, &cs.typeW, &cs.stateW}

	fits := func() bool {
		used := cs.idW
		for _, w := range []int{cs.typeW, cs.stateW, cs.updatedW, cs.estimateW, cs.deadlineW} {
			if w > 0 {
				used += gap + w
			}
		}
		cs.titleW = width - used - gap // gap before title
		return cs.titleW >= minTitle
	}

	for _, w := range dropOrder {
		if fits() {
			break
		}
		*w = 0
	}
	fits() // recompute titleW with final column set
	if cs.titleW < 1 {
		cs.titleW = 1
	}
	return cs
}

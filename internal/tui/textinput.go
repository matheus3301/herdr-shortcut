package tui

import (
	"strings"
	"unicode"
)

// textInput is a minimal single-line rune editor. It avoids an external
// dependency and keeps editing behavior fully deterministic for tests.
type textInput struct {
	runes  []rune
	cursor int // rune index in [0, len(runes)]
}

func (t *textInput) value() string { return string(t.runes) }

func (t *textInput) setValue(s string) {
	t.runes = []rune(s)
	t.cursor = len(t.runes)
}

func (t *textInput) clear() {
	t.runes = nil
	t.cursor = 0
}

func (t *textInput) insert(r rune) {
	// Reject newlines/tabs and every Unicode control, format, and line/paragraph
	// separator so pasted search or path input cannot span rows, hide content, or
	// inject terminal control sequences.
	if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
		return
	}
	t.runes = append(t.runes, 0)
	copy(t.runes[t.cursor+1:], t.runes[t.cursor:])
	t.runes[t.cursor] = r
	t.cursor++
}

func (t *textInput) insertString(s string) {
	for _, r := range s {
		t.insert(r)
	}
}

func (t *textInput) backspace() {
	if t.cursor == 0 {
		return
	}
	t.runes = append(t.runes[:t.cursor-1], t.runes[t.cursor:]...)
	t.cursor--
}

func (t *textInput) deleteForward() {
	if t.cursor >= len(t.runes) {
		return
	}
	t.runes = append(t.runes[:t.cursor], t.runes[t.cursor+1:]...)
}

func (t *textInput) left() {
	if t.cursor > 0 {
		t.cursor--
	}
}

func (t *textInput) right() {
	if t.cursor < len(t.runes) {
		t.cursor++
	}
}

func (t *textInput) home() { t.cursor = 0 }
func (t *textInput) end()  { t.cursor = len(t.runes) }

// render returns the input text with a cursor caret. When showCursor is set the
// text is windowed so the caret is always visible, even for input longer than
// width. Windowing is measured in terminal display cells (ANSI-aware), so wide
// glyphs and zero-width combining marks never overflow the field or split.
func (t *textInput) render(width int, showCursor bool) string {
	if width <= 0 {
		return ""
	}
	if !showCursor {
		return truncate(string(t.runes), width)
	}
	// Reserve one cell for the caret.
	avail := width - 1
	if avail < 1 {
		avail = 1
	}
	// Expand left from the cursor, accumulating display width, so the text before
	// the caret fits the budget (right-anchoring the caret when scrolled).
	start := t.cursor
	used := 0
	for start > 0 {
		w := displayWidth(string(t.runes[start-1]))
		if used+w > avail {
			break
		}
		used += w
		start--
	}
	// Then fill the remaining cells after the caret.
	end := t.cursor
	rem := avail - used
	for end < len(t.runes) {
		w := displayWidth(string(t.runes[end]))
		if w > rem {
			break
		}
		rem -= w
		end++
	}
	var b strings.Builder
	b.WriteString(string(t.runes[start:t.cursor]))
	b.WriteString("▏")
	b.WriteString(string(t.runes[t.cursor:end]))
	return b.String()
}

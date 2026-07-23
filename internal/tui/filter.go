package tui

import (
	"strconv"
	"strings"

	"github.com/matheus3301/herdr-shortcut/internal/shortcut"
)

// filterText builds the lowercase haystack used for local filtering of a Story:
// its ID (as "sc-42" and "42"), title, state, type, and label names.
func filterText(s shortcut.ResolvedStory) string {
	var b strings.Builder
	id := strconv.FormatInt(s.ID, 10)
	b.WriteString("sc-")
	b.WriteString(id)
	b.WriteByte(' ')
	b.WriteString(id)
	b.WriteByte(' ')
	b.WriteString(oneLine(s.Name))
	b.WriteByte(' ')
	b.WriteString(oneLine(s.State.Name))
	b.WriteByte(' ')
	b.WriteString(oneLine(s.StoryType))
	for _, l := range s.Labels {
		b.WriteByte(' ')
		b.WriteString(oneLine(l.Name))
	}
	return strings.ToLower(b.String())
}

// matchesFilter reports whether the Story matches every whitespace-separated
// term in query (AND semantics, case-insensitive substring).
func matchesFilter(s shortcut.ResolvedStory, query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return true
	}
	hay := filterText(s)
	for _, term := range strings.Fields(query) {
		if !strings.Contains(hay, term) {
			return false
		}
	}
	return true
}

// applyFilter returns the indices of stories matching query, preserving order.
func applyFilter(stories []shortcut.ResolvedStory, query string) []int {
	out := make([]int, 0, len(stories))
	for i, s := range stories {
		if matchesFilter(s, query) {
			out = append(out, i)
		}
	}
	return out
}

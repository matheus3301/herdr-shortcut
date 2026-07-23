// Package prompt renders the deterministic launch prompt handed to the selected
// coding-agent harness.
//
// The renderer takes typed Story data, normalizes control characters that could
// manipulate a terminal, caps each field and the final prompt to documented safe
// limits, and substitutes a validated placeholder template. It never emits Go
// formatting artifacts for missing values and never depends on a shell.
package prompt

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/matheus3301/herdr-shortcut/internal/tmpl"
)

// Documented safe limits. These are part of the public contract and are covered
// by tests and documentation.
const (
	// MaxFieldLen bounds every short, single-value field.
	MaxFieldLen = 1024
	// MaxDescriptionLen bounds the Story description, which may be long Markdown.
	MaxDescriptionLen = 8000
	// MaxPromptLen bounds the fully rendered prompt.
	MaxPromptLen = 16000
	// TruncationMarker is appended when a value is shortened.
	TruncationMarker = " …[truncated]"
)

// DefaultTemplate is the documented default launch prompt. Keep this in sync
// with config.example.toml and the README (enforced by tests).
const DefaultTemplate = `Work on Shortcut Story SC-{id}: {name}
URL: {url}
Agent harness: {kind}
Type: {story_type}
State: {state}
Labels: {labels}
Suggested branch: {branch_name}

Description:
{description}

Inspect this repository and its instructions before changing anything. Implement
the Story end to end with the smallest correct change, add or update tests, run
the relevant verification, and summarize the result. Do not change the Shortcut
Story itself unless the user explicitly asks you to.
`

// placeholders is the documented placeholder set (SPEC section 11).
var placeholders = []string{
	"id", "name", "url", "description", "story_type", "state", "labels",
	"estimate", "deadline", "branch_name", "team_id", "epic_id", "kind",
}

// Placeholders returns the documented placeholder names, in a stable order.
func Placeholders() []string {
	out := make([]string, len(placeholders))
	copy(out, placeholders)
	return out
}

// Data is the typed input for a single Story. Nil pointers and empty strings
// render as "none" or the empty string rather than Go formatting artifacts.
type Data struct {
	ID          int
	Name        string
	URL         string
	Description string
	StoryType   string
	State       string
	Labels      []string
	Estimate    *int64
	Deadline    *time.Time
	BranchName  string
	TeamID      string // Shortcut group (team) UUID; may be empty
	EpicID      *int64
	Kind        string // selected agent-harness kind
}

// Prompt is a compiled, reusable prompt template.
type Prompt struct {
	tpl *tmpl.Template
}

// Compile validates text against the documented placeholder set. Template
// errors are configuration errors and are surfaced before any launch.
func Compile(text string) (*Prompt, error) {
	t, err := tmpl.Parse(text, placeholders)
	if err != nil {
		return nil, fmt.Errorf("prompt template: %w", err)
	}
	return &Prompt{tpl: t}, nil
}

// Render produces the final prompt for d. The result is safe to pass as a
// single argv value: control characters are normalized and lengths are bounded.
func (p *Prompt) Render(d Data) string {
	values := map[string]string{
		"id": strconv.Itoa(d.ID),
		// Single-line fields collapse any embedded newlines so they cannot forge
		// extra prompt lines; only the description preserves meaningful multiline.
		"name":        capField(sanitizeLine(d.Name), MaxFieldLen),
		"url":         capField(sanitizeLine(d.URL), MaxFieldLen),
		"description": capField(sanitize(d.Description), MaxDescriptionLen),
		"story_type":  capField(sanitizeLine(d.StoryType), MaxFieldLen),
		"state":       capField(sanitizeLine(d.State), MaxFieldLen),
		"labels":      capField(sanitizeLine(joinLabels(d.Labels)), MaxFieldLen),
		"estimate":    formatIntPtr(d.Estimate),
		"deadline":    formatDeadline(d.Deadline),
		"branch_name": capField(sanitizeLine(d.BranchName), MaxFieldLen),
		"team_id":     capField(sanitizeLine(orNone(d.TeamID)), MaxFieldLen),
		"epic_id":     formatIntPtr(d.EpicID),
		"kind":        capField(sanitizeLine(d.Kind), MaxFieldLen),
	}
	// Sanitize the fully rendered prompt so control characters in the template
	// literals themselves are also normalized, then cap the total length.
	return capField(sanitize(p.tpl.Render(values)), MaxPromptLen)
}

func joinLabels(labels []string) string {
	cleaned := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l != "" {
			cleaned = append(cleaned, l)
		}
	}
	if len(cleaned) == 0 {
		return "none"
	}
	return strings.Join(cleaned, ", ")
}

func formatIntPtr(v *int64) string {
	if v == nil {
		return "none"
	}
	return strconv.FormatInt(*v, 10)
}

func formatDeadline(t *time.Time) string {
	if t == nil {
		return "none"
	}
	return t.UTC().Format("2006-01-02")
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

// Redact replaces every occurrence of secret in s with a placeholder. It is a
// defense-in-depth guard so the resolved token can never appear in the final
// rendered prompt, even if it somehow reached Story data.
func Redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

// isUnsafeRune reports whether r is a control (Cc), format (Cf), or
// line/paragraph-separator (Zl/Zp) character. These can inject terminal escapes,
// hide or reorder text (bidi), or forge lines, so they are dropped from prompts.
func isUnsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// sanitizeLine renders a value as a single line: newlines and tabs become
// spaces and all other control/format/separator characters are dropped, so a
// single-line field cannot forge extra lines or inject terminal control
// sequences.
func sanitizeLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case isUnsafeRune(r):
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// sanitize normalizes line endings to "\n" and drops control, format, and
// line/paragraph-separator characters other than newline and tab, so a Story
// cannot inject ANSI escapes, bidi overrides, or U+2028/U+2029 line breaks
// through the prompt.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if isUnsafeRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// capField shortens s to at most maxLen runes, appending a clear truncation
// marker while keeping the total length within maxLen.
func capField(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	marker := []rune(TruncationMarker)
	if len(marker) >= maxLen {
		// Degenerate cap smaller than the marker: return a hard-truncated slice.
		return string(runes[:maxLen])
	}
	keep := maxLen - len(marker)
	return string(runes[:keep]) + TruncationMarker
}

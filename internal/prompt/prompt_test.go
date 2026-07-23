package prompt

import (
	"strings"
	"testing"
	"time"
)

func est(v int64) *int64 { return &v }

func TestRedact(t *testing.T) {
	t.Parallel()
	if got := Redact("token abc123 here", "abc123"); got != "token [redacted] here" {
		t.Errorf("Redact = %q", got)
	}
	// An empty secret is a no-op (never turns the whole string into placeholders).
	if got := Redact("unchanged", ""); got != "unchanged" {
		t.Errorf("empty secret should be a no-op: %q", got)
	}
}

func TestSanitizeDropsFormatAndSeparators(t *testing.T) {
	t.Parallel()
	p, err := Compile("{name}\n{description}")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// Name with a bidi override (Cf) and BOM (Cf); description with a Unicode line
	// separator (Zl) and a paragraph separator (Zp).
	out := p.Render(Data{
		Name:        "safe\u202ename\ufeff",
		Description: "one\u2028two\u2029three",
	})
	for _, bad := range []string{"\u202e", "\ufeff", "\u2028", "\u2029"} {
		if strings.Contains(out, bad) {
			t.Errorf("prompt kept unsafe rune %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "safename") {
		t.Errorf("name content dropped: %q", out)
	}
}

func TestDefaultRenderIncludesCoreFields(t *testing.T) {
	t.Parallel()
	p, err := Compile(DefaultTemplate)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	deadline := time.Date(2026, 8, 1, 15, 4, 5, 0, time.UTC)
	out := p.Render(Data{
		ID:          123,
		Name:        "Add retry logic",
		URL:         "https://app.shortcut.com/org/story/123",
		Description: "Line one\nLine two",
		StoryType:   "feature",
		State:       "In Progress",
		Labels:      []string{"backend", "urgent"},
		Estimate:    est(3),
		Deadline:    &deadline,
		BranchName:  "matheus/sc-123-add-retry",
		TeamID:      "team-uuid",
		EpicID:      est(7),
	})
	for _, want := range []string{
		"Work on Shortcut Story SC-123: Add retry logic",
		"URL: https://app.shortcut.com/org/story/123",
		"Type: feature",
		"State: In Progress",
		"Labels: backend, urgent",
		"Suggested branch: matheus/sc-123-add-retry",
		"Line one\nLine two",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered prompt missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderMissingValues(t *testing.T) {
	t.Parallel()
	// Custom template exercising every optional field's "none" rendering.
	p, err := Compile("labels={labels} estimate={estimate} deadline={deadline} team={team_id} epic={epic_id} desc=[{description}]")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out := p.Render(Data{ID: 1})
	for _, want := range []string{
		"labels=none",
		"estimate=none",
		"deadline=none",
		"team=none",
		"epic=none",
		"desc=[]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "%!") || strings.Contains(out, "<nil>") {
		t.Errorf("prompt contains Go formatting artifacts: %q", out)
	}
}

func TestRenderNormalizesControlCharacters(t *testing.T) {
	t.Parallel()
	p, err := Compile("[{description}]")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	// ANSI escape, bell, carriage return, and a null byte.
	desc := "safe\x1b[31mred\x07\r\nnext\x00end\ttab"
	out := p.Render(Data{Description: desc})
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("ESC not stripped: %q", out)
	}
	if strings.ContainsRune(out, '\x07') || strings.ContainsRune(out, '\x00') {
		t.Errorf("control chars not stripped: %q", out)
	}
	if strings.ContainsRune(out, '\r') {
		t.Errorf("carriage return not normalized: %q", out)
	}
	if !strings.Contains(out, "safe[31mred\nnext") {
		t.Errorf("unexpected normalization result: %q", out)
	}
	if !strings.Contains(out, "end\ttab") {
		t.Errorf("tab should be preserved: %q", out)
	}
}

func TestRenderTruncatesFieldsAndPrompt(t *testing.T) {
	t.Parallel()
	p, err := Compile("{description}")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	long := strings.Repeat("x", MaxDescriptionLen+500)
	out := p.Render(Data{Description: long})
	if len([]rune(out)) > MaxDescriptionLen {
		t.Errorf("description not capped: len=%d", len([]rune(out)))
	}
	if !strings.HasSuffix(out, TruncationMarker) {
		t.Errorf("expected truncation marker, got suffix %q", out[len(out)-20:])
	}
}

func TestRenderCapsFinalPrompt(t *testing.T) {
	t.Parallel()
	// A template with many description copies to blow past the prompt cap.
	p, err := Compile(strings.Repeat("{name}", 40))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out := p.Render(Data{Name: strings.Repeat("y", MaxFieldLen)})
	if len([]rune(out)) > MaxPromptLen {
		t.Errorf("final prompt not capped: len=%d", len([]rune(out)))
	}
}

func TestCompileInvalidTemplate(t *testing.T) {
	t.Parallel()
	for _, tc := range []string{"{not_a_field}", "unclosed {id", "{ID}"} {
		if _, err := Compile(tc); err == nil {
			t.Errorf("Compile(%q) expected error", tc)
		}
	}
}

func TestDefaultTemplateCompiles(t *testing.T) {
	t.Parallel()
	if _, err := Compile(DefaultTemplate); err != nil {
		t.Fatalf("default template must compile: %v", err)
	}
}

func TestSingleLineFieldsCollapseNewlines(t *testing.T) {
	t.Parallel()
	p, err := Compile("name=[{name}] labels=[{labels}]\ndesc=[{description}]")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out := p.Render(Data{
		Name:        "line1\nline2\x1b[31m",
		Labels:      []string{"a\nb", "c"},
		Description: "para1\npara2",
	})
	if !strings.Contains(out, "name=[line1 line2[31m]") {
		t.Errorf("name should be single-line and ESC-stripped: %q", out)
	}
	if !strings.Contains(out, "labels=[a b, c]") {
		t.Errorf("labels should be single-line: %q", out)
	}
	// Description keeps its newline.
	if !strings.Contains(out, "desc=[para1\npara2]") {
		t.Errorf("description should preserve multiline: %q", out)
	}
}

func TestFinalTemplateLiteralsSanitized(t *testing.T) {
	t.Parallel()
	// A control character in the template literal itself must be normalized.
	p, err := Compile("before\x07after {id}")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	out := p.Render(Data{ID: 5})
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("bell in template literal not sanitized: %q", out)
	}
}

func TestDeadlineFormatting(t *testing.T) {
	t.Parallel()
	p, err := Compile("{deadline}")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	dl := time.Date(2026, 12, 31, 23, 59, 0, 0, time.FixedZone("x", 3600))
	out := p.Render(Data{Deadline: &dl})
	if out != "2026-12-31" {
		t.Errorf("deadline = %q, want 2026-12-31 (UTC date)", out)
	}
}

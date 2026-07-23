package childenv

import (
	"slices"
	"testing"
)

func TestSanitizedStripsToken(t *testing.T) {
	t.Parallel()
	in := []string{"PATH=/bin", "SHORTCUT_API_TOKEN=secret", "HOME=/home/u", "SHORTCUT_API_TOKEN_OTHER=keep"}
	out := Sanitized(in)
	for _, kv := range out {
		if kv == "SHORTCUT_API_TOKEN=secret" {
			t.Fatalf("token not stripped: %v", out)
		}
	}
	if !slices.Contains(out, "PATH=/bin") || !slices.Contains(out, "HOME=/home/u") {
		t.Errorf("non-sensitive vars dropped: %v", out)
	}
	// A different variable that merely shares a prefix must be kept.
	if !slices.Contains(out, "SHORTCUT_API_TOKEN_OTHER=keep") {
		t.Errorf("prefix-similar var wrongly stripped: %v", out)
	}
	if len(out) != 3 {
		t.Errorf("expected 3 vars, got %d: %v", len(out), out)
	}
}

func TestSanitizedEmpty(t *testing.T) {
	t.Parallel()
	if got := Sanitized(nil); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

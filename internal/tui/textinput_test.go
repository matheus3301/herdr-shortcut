package tui

import (
	"os"
	"testing"
)

func TestTextInputEditing(t *testing.T) {
	t.Parallel()
	var ti textInput
	ti.insertString("helo")
	if ti.value() != "helo" {
		t.Fatalf("value = %q", ti.value())
	}
	// Move left one and insert 'l' -> "hello".
	ti.left()
	ti.insert('l')
	if ti.value() != "hello" {
		t.Fatalf("after insert: %q", ti.value())
	}
	// Home, delete forward removes 'h'.
	ti.home()
	ti.deleteForward()
	if ti.value() != "ello" {
		t.Fatalf("after deleteForward: %q", ti.value())
	}
	// End, backspace removes 'o'.
	ti.end()
	ti.backspace()
	if ti.value() != "ell" {
		t.Fatalf("after backspace: %q", ti.value())
	}
	// right at end is a no-op; left/home/end bounds hold.
	ti.right()
	ti.right()
	ti.setValue("world")
	if ti.value() != "world" || ti.cursor != 5 {
		t.Fatalf("setValue: %q cursor=%d", ti.value(), ti.cursor)
	}
	ti.clear()
	if ti.value() != "" || ti.cursor != 0 {
		t.Fatalf("clear failed: %q", ti.value())
	}
	// Control characters are ignored on insert.
	ti.insert('\n')
	ti.insert('\t')
	if ti.value() != "" {
		t.Fatalf("control chars should be ignored: %q", ti.value())
	}
}

func TestTextInputRender(t *testing.T) {
	t.Parallel()
	var ti textInput
	ti.setValue("abc")
	if got := ti.render(10, false); got != "abc" {
		t.Errorf("render without cursor = %q", got)
	}
	withCaret := ti.render(10, true)
	if withCaret == "abc" {
		t.Errorf("render with cursor should include a caret, got %q", withCaret)
	}
}

func TestDefaultStatDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := defaultStatDir(dir); err != nil {
		t.Errorf("existing dir should validate: %v", err)
	}
	if _, err := defaultStatDir(dir + "/does-not-exist"); err == nil {
		t.Error("missing path should error")
	}
	if _, err := defaultStatDir(""); err == nil {
		t.Error("empty path should error")
	}
	// A file (not a directory) is rejected.
	f := dir + "/file"
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := defaultStatDir(f); err == nil {
		t.Error("a file should be rejected")
	}
}

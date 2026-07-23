package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestHumanAge(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "now"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{2 * 24 * time.Hour, "2d"},
		{90 * 24 * time.Hour, "3mo"},
	}
	for _, tc := range tests {
		if got := humanAge(now, now.Add(-tc.ago)); got != tc.want {
			t.Errorf("humanAge(-%s) = %q, want %q", tc.ago, got, tc.want)
		}
	}
	// Future timestamps clamp to "now" rather than going negative.
	if got := humanAge(now, now.Add(time.Hour)); got != "now" {
		t.Errorf("future age = %q", got)
	}
}

func TestShortType(t *testing.T) {
	t.Parallel()
	cases := map[string]string{"feature": "feat", "chore": "chore", "bug": "bug", "": "-", "epic": "epic"}
	for in, want := range cases {
		if got := shortType(in); got != want {
			t.Errorf("shortType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComputeColumnsDropsOptionalBeforeTitle(t *testing.T) {
	t.Parallel()
	// Wide: everything present.
	wide := computeColumns(120, 6)
	if wide.deadlineW == 0 || wide.estimateW == 0 || wide.updatedW == 0 {
		t.Errorf("wide layout should keep optional columns: %+v", wide)
	}
	if wide.titleW < minTitle {
		t.Errorf("title too small at wide width: %d", wide.titleW)
	}
	// Narrow: optional columns dropped, title preserved at/above minimum.
	narrow := computeColumns(40, 6)
	if narrow.titleW < 1 {
		t.Errorf("title width must stay positive: %d", narrow.titleW)
	}
	if narrow.deadlineW != 0 {
		t.Errorf("deadline should be dropped first at narrow width: %+v", narrow)
	}
}

func TestOneLine(t *testing.T) {
	t.Parallel()
	if got := oneLine("a\nb\tc\rd"); got != "a b c d" {
		t.Errorf("oneLine = %q", got)
	}
	// ESC (a control char) is dropped; the remaining CSI text stays as literal.
	if got := oneLine("x\x1b[31my\x07z"); got != "x[31myz" {
		t.Errorf("oneLine control strip = %q", got)
	}
}

func TestANSIAwareWidth(t *testing.T) {
	t.Parallel()
	styled := lipgloss.NewStyle().Bold(true).Render("hello")
	// The styled string carries ANSI, but its display width is 5.
	if displayWidth(styled) != 5 {
		t.Errorf("displayWidth(styled) = %d, want 5", displayWidth(styled))
	}
	// Padding a styled cell to width 8 adds exactly 3 spaces (display-aware).
	padded := padRight(styled, 8)
	if displayWidth(padded) != 8 {
		t.Errorf("padded display width = %d, want 8", displayWidth(padded))
	}
}

func TestTruncateAndPad(t *testing.T) {
	t.Parallel()
	if got := truncate("hello world", 5); got != "hell…" {
		t.Errorf("truncate = %q", got)
	}
	if got := padRight("hi", 5); got != "hi   " {
		t.Errorf("padRight = %q", got)
	}
	if got := padLeft("hi", 5); got != "   hi" {
		t.Errorf("padLeft = %q", got)
	}
	if truncate("x", 0) != "" || padRight("x", 0) != "" || padLeft("x", 0) != "" {
		t.Error("zero width should yield empty string")
	}
}

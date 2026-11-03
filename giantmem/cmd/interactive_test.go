package cmd

import (
	"strings"
	"testing"
)

func TestFmtStamp(t *testing.T) {
	if got := fmtStamp("20261103_081333"); got != "2026-11-03 08:13" {
		t.Fatalf("session stamp: %q", got)
	}
	if got := fmtStamp("garbage"); got != "garbage" {
		t.Fatalf("passthrough: %q", got)
	}
}

func TestClip(t *testing.T) {
	if got := clip("a\n  b\tc", 10); got != "a b c" {
		t.Fatalf("whitespace fold: %q", got)
	}
	if got := clip("héllo wörld", 8); got != "héllo..." {
		t.Fatalf("rune-safe cut: %q", got)
	}
}

func TestRenderTable(t *testing.T) {
	out := renderTable([]string{"type", "n"}, []map[string]any{{"type": "research", "n": 3}, {"type": nil, "n": 10}})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "type") || !strings.Contains(lines[2], "NULL") {
		t.Fatalf("table:\n%s", out)
	}
	if strings.Index(lines[1], "3") != strings.Index(lines[0], "n") {
		t.Fatalf("columns not aligned:\n%s", out)
	}
}

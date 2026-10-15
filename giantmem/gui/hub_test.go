package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileHead(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "q.py")
	os.WriteFile(text, []byte(strings.Repeat("x", 100)), 0o644)
	bin := filepath.Join(dir, "b.bin")
	os.WriteFile(bin, []byte{'a', 0, 'b'}, 0o644)
	a := &App{}

	h, err := a.ReadFileHead(text, 40)
	if err != nil || len(h.Content) != 40 || !h.Truncated || h.Size != 100 || h.Binary {
		t.Fatalf("truncated read = %+v, %v", h, err)
	}
	h, _ = a.ReadFileHead(text, 0)
	if len(h.Content) != 100 || h.Truncated {
		t.Fatalf("full read = %+v", h)
	}
	h, _ = a.ReadFileHead(bin, 0)
	if !h.Binary || h.Content != "" {
		t.Fatalf("binary read = %+v", h)
	}
	if _, err := a.ReadFileHead(filepath.Join(dir, "nope"), 0); err == nil {
		t.Fatal("missing file should error")
	}
}

func TestOnURLOpenBeforeFrontend(t *testing.T) {
	a := &App{}
	a.onURLOpen("giantmem://session/32f0acaa-3fea/")
	if got := a.TakePendingLink(); got != (DeepLink{Kind: "session", SessionID: "32f0acaa-3fea"}) {
		t.Fatalf("pending = %+v", got)
	}
	if got := a.TakePendingLink(); got != (DeepLink{}) {
		t.Fatalf("pending not cleared: %+v", got)
	}
	a.onURLOpen("giantmem://day/2026-10-14")
	if got := a.TakePendingLink(); got != (DeepLink{Kind: "day", Day: "2026-10-14"}) {
		t.Fatalf("day pending = %+v", got)
	}
	for raw, want := range map[string]DeepLink{
		"giantmem://tool/abc/main/0":                   {Kind: "tool", SessionID: "abc", Agent: "main", Seq: 0},
		"giantmem://tool/abc/a1b2/17":                  {Kind: "tool", SessionID: "abc", Agent: "a1b2", Seq: 17},
		"giantmem://file/abc/scratchpad/cdp.mjs":       {Kind: "file", SessionID: "abc", File: "scratchpad/cdp.mjs"},
		"giantmem://file/abc/scratchpad/my%20notes.md": {Kind: "file", SessionID: "abc", File: "scratchpad/my notes.md"},
	} {
		a.onURLOpen(raw)
		if got := a.TakePendingLink(); got != want {
			t.Fatalf("%s = %+v, want %+v", raw, got, want)
		}
	}
	for _, bad := range []string{
		"giantmem://tool/abc/main",
		"giantmem://tool/abc/main/x",
		"giantmem://tool/abc/main/-1",
		"giantmem://tool//main/3",
		"giantmem://file/abc",
		"giantmem://file/abc/%zz",
		"https://example.com/session/x",
		"giantmem://session/",
		"giantmem://session/a/b",
		"giantmem://day/",
		"giantmem://day/2026-13-01",
		"giantmem://day/today",
		"giantmem://nope/x",
	} {
		a.onURLOpen(bad)
		if got := a.TakePendingLink(); got != (DeepLink{}) {
			t.Fatalf("%s should be ignored, got %+v", bad, got)
		}
	}
}

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
	if got := a.TakePendingSession(); got != "32f0acaa-3fea" {
		t.Fatalf("pending = %q", got)
	}
	if got := a.TakePendingSession(); got != "" {
		t.Fatalf("pending not cleared: %q", got)
	}
	a.onURLOpen("https://example.com/session/x")
	a.onURLOpen("giantmem://session/")
	if got := a.TakePendingSession(); got != "" {
		t.Fatalf("non-session urls should be ignored, got %q", got)
	}
}

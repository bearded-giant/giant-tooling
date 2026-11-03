package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDescribeMCPPrettyJSON(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "mcpServers": {
    "giantmem-search": {
      "command": "/home/x/.local/bin/giantmem",
      "args": [
        "mcp",
        "serve"
      ]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := describeMCP(home)
	if !got.Registered || !got.Healthy || got.Command != "/home/x/.local/bin/giantmem" || got.Args != "mcp serve" {
		t.Fatalf("got %+v", got)
	}

	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"permissions": {"allow": ["mcp__giantmem-search__find_mr"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := describeMCP(home); got.Registered {
		t.Fatalf("a permission rule is not a registration: %+v", got)
	}
}

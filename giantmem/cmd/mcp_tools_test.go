package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFeaturesJSONFlatShape(t *testing.T) {
	p := filepath.Join(t.TempDir(), "features.json")
	body := `{"zeta": {"status": "pending", "branch": "-"}, "alpha": {"status": "in_progress", "branch": "alpha"}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readFeaturesJSON(p)
	if len(got) != 2 {
		t.Fatalf("got %d features, want 2: %v", len(got), got)
	}
	if got[0]["name"] != "alpha" || got[0]["status"] != "in_progress" || got[1]["name"] != "zeta" {
		t.Fatalf("want alpha then zeta with fields kept, got %v", got)
	}
}

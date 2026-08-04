package archiver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

func TestMoveFeature(t *testing.T) {
	base := t.TempDir()
	t.Setenv("GIANTMEM_ARCHIVE_BASE", base)
	d, err := db.OpenLiveOrCreate(filepath.Join(base, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	d.Close()

	src := t.TempDir()
	dest := t.TempDir()
	srcWS := filepath.Join(src, ".giantmem")
	destWS := filepath.Join(dest, ".giantmem")
	featDir := filepath.Join(srcWS, "features", "widget")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// dest has a .giantmem but no features.json — exercises the create path
	if err := os.MkdirAll(destWS, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featDir, "proposal.md"), []byte("# widget\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcWS, "features", "features.json"),
		[]byte(`{"widget": {"name": "widget", "status": "in_progress"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcWS, "features", "_index.md"),
		[]byte("# Feature Index\n\n| Feature | Status |\n|---|---|\n| [widget](widget/) | in_progress |\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// dry run touches nothing
	if _, err := MoveFeature(src, dest, "widget", true); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !dirExists(featDir) {
		t.Fatal("dry run moved the dir")
	}

	res, err := MoveFeature(src, dest, "widget", false)
	if err != nil {
		t.Fatal(err)
	}
	if dirExists(featDir) {
		t.Fatal("source feature dir still exists")
	}
	destFeat := filepath.Join(destWS, "features", "widget")
	if b, _ := os.ReadFile(filepath.Join(destFeat, "proposal.md")); string(b) != "# widget\n" {
		t.Fatalf("dest proposal.md = %q", b)
	}
	if res.Files != 1 {
		t.Fatalf("Files = %d, want 1", res.Files)
	}

	srcEntries, err := readFeaturesJSON(filepath.Join(srcWS, "features", "features.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := srcEntries["widget"]; ok {
		t.Fatal("widget still in source features.json")
	}
	destEntries, err := readFeaturesJSON(filepath.Join(destWS, "features", "features.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := destEntries["widget"]["status"].(string); st != "in_progress" {
		t.Fatalf("dest status = %q", st)
	}
	idx, _ := os.ReadFile(filepath.Join(srcWS, "features", "_index.md"))
	if string(idx) != "# Feature Index\n\n| Feature | Status |\n|---|---|\n" {
		t.Fatalf("_index.md row not dropped: %q", idx)
	}

	// second move of the same name must refuse (collision at dest)
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := MoveFeature(src, dest, "widget", false); err == nil {
		t.Fatal("expected collision error")
	}
}

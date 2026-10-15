package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

type bundleFile struct {
	path, kind string
	body       []byte
}

func writeBundle(t *testing.T, base, slug, sid string, manifest map[string]any, files []bundleFile) string {
	t.Helper()
	dir := filepath.Join(base, "bundles", slug, sid)
	var entries []map[string]any
	for _, f := range files {
		p := filepath.Join(dir, f.path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, f.body, 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, map[string]any{"path": f.path, "size": len(f.body), "kind": f.kind})
	}
	manifest["files"] = entries
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "bundle.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func collectBundle(t *testing.T, opts EmitOptions) map[string]Doc {
	t.Helper()
	src := &sessionBundleSource{cfg: SourceConfig{Name: "session-bundle", Kind: "builtin", Enabled: true}}
	docCh, errCh := src.Emit(context.Background(), opts)
	out := map[string]Doc{}
	for d := range docCh {
		out[d.Metadata["rel"]] = d
	}
	for e := range errCh {
		if e != nil {
			t.Fatalf("emit error: %v", e)
		}
	}
	return out
}

func setupBundles(t *testing.T) (base, alphaDir string) {
	base = t.TempDir()
	alphaDir = writeBundle(t, base, "-proj-alpha", "sid-alpha",
		map[string]any{"session_id": "uuid-alpha", "cwd": "/work/alpha", "first_prompt": "fix the flaky query"},
		[]bundleFile{
			{"scratchpad/query.py", "script", []byte("print('hi')\n")},
			{"tasks/run.output", "log", []byte("ok\n")},
			{"claude/workflows/wf.js", "workflow", []byte("export default 1\n")},
			{"claude/transcript.jsonl", "transcript", []byte("{}\n")},
			{"scratchpad/thing.xyz", "other", []byte("x\n")},
			{"claude/subagents/agent.md", "doc", []byte("sub\n")},
			{"scratchpad/big.log", "log", bytes.Repeat([]byte("a"), bundleMaxFileSize+1)},
			{"scratchpad/blob.dat", "data", []byte("ab\x00cd")},
		})
	writeBundle(t, base, "-proj-beta", "sid-beta",
		map[string]any{"cwd": "/work/beta", "first_prompt": "beta prompt"},
		[]bundleFile{{"scratchpad/notes.md", "doc", []byte("beta notes\n")}})
	bad := filepath.Join(base, "bundles", "-proj-gamma", "sid-gamma")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "bundle.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	return base, alphaDir
}

func TestSessionBundleSource_EmitsIndexableFiles(t *testing.T) {
	base, alphaDir := setupBundles(t)
	docs := collectBundle(t, EmitOptions{ArchiveBase: base})

	want := []string{"scratchpad/query.py", "tasks/run.output", "claude/workflows/wf.js", "scratchpad/notes.md"}
	if len(docs) != len(want) {
		t.Fatalf("docs = %d (%v), want %d", len(docs), docs, len(want))
	}
	for _, rel := range want {
		if _, ok := docs[rel]; !ok {
			t.Fatalf("missing %s", rel)
		}
	}

	d := docs["scratchpad/query.py"]
	if d.SourceType != "bundle" || d.DirType != "script" || d.SessionID != "uuid-alpha" ||
		d.Project != "proj-alpha" || d.Cwd != "/work/alpha" || !d.IsLatest {
		t.Fatalf("unexpected doc: %+v", d)
	}
	if d.Filepath != filepath.Join(alphaDir, "scratchpad", "query.py") || d.Metadata["bundle"] != alphaDir {
		t.Fatalf("bad paths: %+v", d)
	}
	if !strings.HasPrefix(d.Content, "scratchpad/query.py\nfix the flaky query\n\nprint('hi')") {
		t.Fatalf("content prefix wrong: %q", d.Content)
	}
	if len(d.Timestamp) != 15 || d.Timestamp[8] != '_' {
		t.Fatalf("timestamp should be 20060102_150405, got %q", d.Timestamp)
	}
	if docs["tasks/run.output"].DirType != "log" || docs["claude/workflows/wf.js"].DirType != "workflow" {
		t.Fatalf("dir types wrong")
	}

	// no session_id in the manifest falls back to the bundle dir name
	if b := docs["scratchpad/notes.md"]; b.SessionID != "sid-beta" || b.Project != "proj-beta" {
		t.Fatalf("beta doc: %+v", b)
	}
}

func TestSessionBundleSource_ProjectFilter(t *testing.T) {
	base, _ := setupBundles(t)
	docs := collectBundle(t, EmitOptions{ArchiveBase: base, Project: "ALPHA"})
	if len(docs) != 3 {
		t.Fatalf("docs = %d, want 3 alpha docs", len(docs))
	}
	for _, d := range docs {
		if d.Project != "proj-alpha" {
			t.Fatalf("filter leaked %+v", d)
		}
	}
}

func TestSessionBundleSource_Incremental(t *testing.T) {
	base, alphaDir := setupBundles(t)
	d, err := db.Open(filepath.Join(t.TempDir(), "archives.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	indexed := filepath.Join(alphaDir, "scratchpad", "query.py")
	if _, err := d.Exec(`INSERT INTO documents (project, timestamp, source_type, filepath, filename, indexed_at)
		VALUES ('proj-alpha', '20261015_080000', 'bundle', ?, 'query.py', datetime('now', '+1 hour'))`, indexed); err != nil {
		t.Fatal(err)
	}

	docs := collectBundle(t, EmitOptions{ArchiveBase: base, DB: d})
	if _, ok := docs["scratchpad/query.py"]; ok {
		t.Fatalf("file indexed after its mtime should be skipped")
	}
	if len(docs) != 3 {
		t.Fatalf("docs = %d, want 3", len(docs))
	}

	docs = collectBundle(t, EmitOptions{ArchiveBase: base, DB: d, Force: true})
	if _, ok := docs["scratchpad/query.py"]; !ok {
		t.Fatalf("Force should re-emit")
	}
}

func TestSessionBundleSource_SkipsRepoCopies(t *testing.T) {
	base := t.TempDir()
	writeBundle(t, base, "-proj-copy", "sid-copy",
		map[string]any{"session_id": "uuid-copy"},
		[]bundleFile{
			{"scratchpad/tree/pyproject.toml", "data", []byte("[project]\n")},
			{"scratchpad/tree/.gitignore", "other", []byte(".venv\n")},
			{"scratchpad/tree/app/main.py", "script", []byte("print(1)\n")},
			{"scratchpad/bisect/abc123/go.mod", "other", []byte("module x\n")},
			{"scratchpad/bisect/abc123/.gitignore", "other", []byte("bin\n")},
			{"scratchpad/bisect/abc123/main.go", "script", []byte("package main\n")},
			// a harness with a package.json but no .gitignore is session work
			{"scratchpad/verify/package.json", "data", []byte("{}\n")},
			{"scratchpad/verify/v.js", "script", []byte("1\n")},
			// the scratchpad root itself is never a copy
			{"scratchpad/package.json", "data", []byte("{}\n")},
			{"scratchpad/.gitignore", "other", []byte("x\n")},
			{"scratchpad/probe.py", "script", []byte("2\n")},
		})
	docs := collectBundle(t, EmitOptions{ArchiveBase: base})
	want := []string{"scratchpad/verify/package.json", "scratchpad/verify/v.js", "scratchpad/package.json", "scratchpad/probe.py"}
	if len(docs) != len(want) {
		t.Fatalf("docs = %d (%v), want %v", len(docs), docs, want)
	}
	for _, rel := range want {
		if _, ok := docs[rel]; !ok {
			t.Fatalf("missing %s", rel)
		}
	}
}

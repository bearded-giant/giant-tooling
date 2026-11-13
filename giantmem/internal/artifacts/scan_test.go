package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func symlinkedWorkspace(t *testing.T) (repo, link string) {
	t.Helper()
	dir := t.TempDir()
	real := filepath.Join(dir, "archive", "ws")
	feat := filepath.Join(real, "features", "foo")
	if err := os.MkdirAll(feat, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ntype: proposal\nfeature: foo\nstatus: ready\n---\n# Proposal\n"
	if err := os.WriteFile(filepath.Join(feat, "proposal.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(repo, ".giantmem")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	return repo, link
}

func TestScanFollowsSymlinkedWorkspace(t *testing.T) {
	repo, link := symlinkedWorkspace(t)
	idx, err := Scan(link)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Artifacts) != 1 {
		t.Fatalf("want 1 artifact through the symlinked .giantmem, got %d", len(idx.Artifacts))
	}
	if got := idx.Artifacts[0].Path; got != "features/foo/proposal.md" {
		t.Fatalf("path = %q, want features/foo/proposal.md", got)
	}
	if idx.Worktree != repo {
		t.Fatalf("worktree = %q, want %q", idx.Worktree, repo)
	}
}

func TestWalkFindsSymlinkedWorkspace(t *testing.T) {
	repo, link := symlinkedWorkspace(t)
	var out []string
	walkForWorkspaces(filepath.Dir(repo), 3, map[string]bool{}, &out)
	for _, ws := range out {
		if ws == link {
			return
		}
	}
	t.Fatalf("crawl missed %s, found %v", link, out)
}

func TestWalkSkipsDotDirMirrors(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"repo/.giantmem", "repo/.mdlive/history/.giantmem", "wt/.bare/x/.giantmem"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var out []string
	walkForWorkspaces(root, 4, map[string]bool{}, &out)
	if len(out) != 1 || out[0] != filepath.Join(root, "repo", ".giantmem") {
		t.Fatalf("want only repo/.giantmem, got %v", out)
	}
}

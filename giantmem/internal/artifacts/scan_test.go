package artifacts

import (
	"os"
	"os/exec"
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

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDetectRepoBranchBareWorktreeUsesContainerName(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	gitRun(t, root, "init", "-q", src)
	gitRun(t, src, "commit", "-q", "--allow-empty", "-m", "init")
	base := filepath.Join(root, "proj-wt")
	gitRun(t, root, "clone", "-q", "--bare", src, filepath.Join(base, ".bare"))
	gitRun(t, filepath.Join(base, ".bare"), "worktree", "add", "-q", "-b", "feat-x", "../feat-x", "main")

	repo, branch := DetectRepoBranch(filepath.Join(base, "feat-x", ".giantmem"))
	if repo != "proj-wt" || branch != "feat-x" {
		t.Fatalf("got (%q, %q), want (proj-wt, feat-x)", repo, branch)
	}

	repo, branch = DetectRepoBranch(filepath.Join(src, ".giantmem"))
	if repo != "src" || branch != "main" {
		t.Fatalf("plain repo: got (%q, %q), want (src, main)", repo, branch)
	}
}

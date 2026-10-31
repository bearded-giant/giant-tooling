package ingest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionProject(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "dev", "claude-code-config")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	os.MkdirAll(filepath.Join(repo, "hooks"), 0o755)
	wt := filepath.Join(home, "dev", "data-eng", "recharge-sidekick-wt", "context-memory")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: ../.bare/worktrees/context-memory"), 0o644)

	projects := filepath.Join(home, "projects")
	write := func(slug, body string) string {
		p := filepath.Join(projects, slug, "s.jsonl")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	rec := func(cwd string) string {
		return `{"type":"summary"}` + "\n" + `{"type":"user","cwd":"` + cwd + `","message":{"content":"hi"}}` + "\n"
	}
	cases := []struct {
		name, slug, body, want string
	}{
		{"plain repo", "-Users-x-dev-claude-code-config", rec(repo), "claude-code-config"},
		{"repo subdir", "-Users-x-dev-claude-code-config-hooks", rec(filepath.Join(repo, "hooks")), "claude-code-config"},
		{"worktree", "-Users-x-dev-data-eng-recharge-sidekick-wt-context-memory", rec(wt), "recharge-sidekick-wt-context-memory"},
		{"removed worktree", "-Users-x-dev-ai-inv-wt-gone", rec("/nowhere/dev/ai/inv-wt/gone"), "inv-wt-gone"},
		{"removed repo", "-Users-x-dev-python-recharge-auth", rec("/nowhere/dev/python/recharge-auth"), "recharge-auth"},
		{"no cwd", "-Users-x-dev-foo-bar", `{"type":"user","message":{"content":"hi"}}` + "\n", "dev/foo/bar"},
	}
	for _, c := range cases {
		p := write(c.slug, c.body)
		if got := SessionProject(p, projects, ""); got != c.want {
			t.Errorf("%s: SessionProject = %q, want %q", c.name, got, c.want)
		}
	}
}

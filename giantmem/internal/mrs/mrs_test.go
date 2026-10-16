package mrs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

const (
	authMR  = "https://gitlab.rechargeapps.net/identity/recharge-auth/-/merge_requests/62"
	invMR   = "https://gitlab.rechargeapps.net/ai-tooling/investigator-agent/-/merge_requests/62"
	tfMR    = "https://gitlab.rechargeapps.net/engineering/infrastructure/terraform/-/merge_requests/4177"
	ghPR    = "https://github.com/o/r/pull/62"
	sOpen   = "974061b2-0000-0000-0000-000000000000"
	sTouch  = "4c72c55a-0000-0000-0000-000000000000"
	sOther  = "f8943589-0000-0000-0000-000000000000"
	authDir = "/dev/recharge-auth-wt/data-hardening"
)

func fixture(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "archives.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []struct{ id, project, cwd string }{
		{sOpen, "dev/python/recharge-auth-wt-data-hardening", authDir},
		{sTouch, "dev/python/recharge-auth-wt-data-hardening", authDir},
		{sOther, "dev/python/recharge-auth-wt-better-redis-search", "/dev/recharge-auth-wt/better-redis-search"},
	} {
		exec(`INSERT INTO documents (project, timestamp, source_type, filepath, filename, session_id, cwd, indexed_at)
              VALUES (?, '20261015_080000', 'session', ?, 'x.jsonl', ?, ?, datetime('now'))`, s.project, "/p/"+s.id+".jsonl", s.id, s.cwd)
	}
	links := []struct{ sid, kind, target, label, ts, role, cwd string }{
		{sTouch, "mr", authMR, "recharge-auth!62", "2026-10-14T23:00:00Z", "touched", authDir},
		{sOpen, "mr", authMR, "recharge-auth!62", "2026-10-15T00:00:00Z", "opened", authDir},
		{sOpen, "mr", tfMR, "terraform!4177", "2026-10-15T00:03:00Z", "opened", "/dev/infra/terraform"},
		{sOther, "mr", invMR, "investigator-agent!62", "2026-10-16T00:56:00Z", "touched", ""},
		{sOther, "mr", ghPR, "r#62", "2026-09-01T00:00:00Z", "opened", ""},
		{sOther, "mr_ref", "https://gitlab.rechargeapps.net/identity/recharge-auth/-/merge_requests/45", "recharge-auth!45", "2026-10-16T00:00:00Z", "", ""},
	}
	for _, l := range links {
		exec(`INSERT INTO session_links (session_id, kind, target, label, ts, role, cwd) VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''))`,
			l.sid, l.kind, l.target, l.label, l.ts, l.role, l.cwd)
	}
	return d
}

func TestMatcher(t *testing.T) {
	cases := []struct {
		ref  string
		want []string
	}{
		{"!62", []string{authMR, invMR}},
		{"#62", []string{ghPR}},
		{"62", []string{authMR, invMR, ghPR}},
		{"recharge-auth!62", []string{authMR}},
		{"identity/recharge-auth!62", []string{authMR}},
		{"auth!62", nil},
		{"r#62", []string{ghPR}},
		{"terraform!4177", []string{tfMR}},
		{authMR + "/diffs", []string{authMR}},
	}
	all := []string{authMR, invMR, tfMR, ghPR}
	for _, c := range cases {
		m, err := Matcher(c.ref)
		if err != nil {
			t.Fatalf("%s: %v", c.ref, err)
		}
		var got []string
		for _, u := range all {
			if m(u) {
				got = append(got, u)
			}
		}
		if len(got) != len(c.want) {
			t.Errorf("%s matched %v, want %v", c.ref, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s matched %v, want %v", c.ref, got, c.want)
			}
		}
	}
	if _, err := Matcher("nope"); err == nil {
		t.Error("bad ref accepted")
	}
}

func TestFind(t *testing.T) {
	d := fixture(t)
	list, err := Find(d, Filter{Ref: "!62"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].URL != invMR || list[1].URL != authMR {
		t.Fatalf("got %+v", list)
	}
	auth := list[1]
	if auth.Opener.SessionID != sOpen || auth.Opener.Role != "opened" || len(auth.Sessions) != 2 {
		t.Errorf("opener = %+v, sessions = %d", auth.Opener, len(auth.Sessions))
	}
	if auth.Opener.Resume != "cd "+authDir+" && claude --resume "+sOpen {
		t.Errorf("resume = %q", auth.Opener.Resume)
	}
	if inv := list[0]; inv.Opener.SessionID != sOther || inv.Opener.Role != "touched" || inv.Opener.Cwd != "/dev/recharge-auth-wt/better-redis-search" {
		t.Errorf("first-touched opener = %+v", inv.Opener)
	}

	tf, _ := Find(d, Filter{Ref: "terraform!4177"})
	if len(tf) != 1 || tf[0].Opener.Cwd != authDir || tf[0].Opener.RanIn != "/dev/infra/terraform" {
		t.Errorf("terraform opener = %+v", tf[0].Opener)
	}

	recent, _ := Find(d, Filter{Since: "2026-10-01T00:00:00Z"})
	if len(recent) != 3 {
		t.Errorf("since kept %d, want 3 (gh PR is older)", len(recent))
	}
	byProject, _ := Find(d, Filter{Project: "terraform"})
	if len(byProject) != 1 || byProject[0].URL != tfMR {
		t.Errorf("project terraform = %+v", byProject)
	}
	bySession, _ := Find(d, Filter{Project: "better-redis-search"})
	if len(bySession) != 2 {
		t.Errorf("project better-redis-search kept %d, want 2", len(bySession))
	}
}

func TestRefreshSkipsTerminalAndCaches(t *testing.T) {
	d := fixture(t)
	saveStatus(d, tfMR, &Status{State: "merged", Title: "bucket", CheckedAt: "2026-10-15T00:00:00Z"})
	var calls atomic.Int32
	orig := Fetcher
	t.Cleanup(func() { Fetcher = orig })
	Fetcher = func(_ context.Context, u string) (*Status, error) {
		calls.Add(1)
		if u == ghPR {
			return nil, errors.New("gh: not logged in")
		}
		return &Status{State: "open", Verdict: "GREEN", Level: "ok", SourceBranch: "b"}, nil
	}
	list, _ := Find(d, Filter{})
	Refresh(context.Background(), d, list, true)
	if n := calls.Load(); n != 3 {
		t.Errorf("fetched %d, want 3 (merged one is cached)", n)
	}
	got := map[string]*Status{}
	for _, m := range list {
		got[m.URL] = m.Status
	}
	if s := got[tfMR]; !s.Cached || s.State != "merged" {
		t.Errorf("terraform = %+v", s)
	}
	if s := got[ghPR]; s.Verdict != "?" || s.Error == "" {
		t.Errorf("failed fetch = %+v", s)
	}
	if s := got[authMR]; s.Cached || s.Show() != "GREEN" {
		t.Errorf("auth = %+v", s)
	}
	if c := loadCache(d); c[authMR].Verdict != "GREEN" || len(c) != 3 {
		t.Errorf("cache = %+v", c)
	}

	Refresh(context.Background(), d, list, false)
	if n := calls.Load(); n != 3 {
		t.Errorf("no-live refresh fetched")
	}
}

func TestGitlabStatus(t *testing.T) {
	cases := []struct{ body, show, level string }{
		{`{"state":"merged"}`, "merged", ""},
		{`{"state":"locked"}`, "closed", ""},
		{`{"state":"opened","detailed_merge_status":"conflict","head_pipeline":{"status":"success"}}`, "conflicts", "bad"},
		{`{"state":"opened","head_pipeline":{"status":"failed"}}`, "pipe:failed", "bad"},
		{`{"state":"opened","detailed_merge_status":"need_rebase","head_pipeline":{"status":"success"}}`, "rebase", "bad"},
		{`{"state":"opened","head_pipeline":{"status":"running"}}`, "pipe:running", "wait"},
		{`{"state":"opened"}`, "no pipe", "wait"},
		{`{"state":"opened","sha":"b","head_pipeline":{"status":"success","sha":"a"}}`, "pipe:stale", "wait"},
		{`{"state":"opened","draft":true,"head_pipeline":{"status":"success"}}`, "draft", "wait"},
		{`{"state":"opened","detailed_merge_status":"mergeable","head_pipeline":{"status":"success"}}`, "GREEN", "ok"},
	}
	for _, c := range cases {
		s, err := gitlabStatus([]byte(c.body))
		if err != nil || s.Show() != c.show || s.Level != c.level {
			t.Errorf("%s = %+v %v, want %s/%s", c.body, s, err, c.show, c.level)
		}
	}
}

func TestGithubStatus(t *testing.T) {
	cases := []struct{ body, show string }{
		{`{"state":"closed","merged":true}`, "merged"},
		{`{"state":"closed"}`, "closed"},
		{`{"state":"open","draft":true,"mergeable_state":"clean"}`, "draft"},
		{`{"state":"open","mergeable_state":"dirty"}`, "conflicts"},
		{`{"state":"open","mergeable_state":"clean","head":{"ref":"b"}}`, "GREEN"},
		{`{"state":"open"}`, "pending"},
	}
	for _, c := range cases {
		if s, err := githubStatus([]byte(c.body)); err != nil || s.Show() != c.show {
			t.Errorf("%s = %+v %v, want %s", c.body, s, err, c.show)
		}
	}
}

func TestRemoteKey(t *testing.T) {
	for in, want := range map[string]string{
		"git@gitlab.rechargeapps.net:identity/recharge-auth.git":      "gitlab.rechargeapps.net/identity/recharge-auth",
		"https://gitlab.rechargeapps.net/identity/Recharge-Auth.git/": "gitlab.rechargeapps.net/identity/recharge-auth",
		"ssh://git@github.com:22/o/r":                                 "github.com/o/r",
	} {
		if got := remoteKey(in); got != want {
			t.Errorf("remoteKey(%q) = %q, want %q", in, got, want)
		}
	}
}

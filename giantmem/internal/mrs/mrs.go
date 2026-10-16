package mrs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Session struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	TS        string `json:"ts"`
	Cwd       string `json:"cwd,omitempty"`    // where the session was launched; resume needs it
	RanIn     string `json:"ran_in,omitempty"` // shell cwd when it linked the MR, when that differs
	Project   string `json:"project,omitempty"`
	Resume    string `json:"resume,omitempty"`
}

type Status struct {
	State        string `json:"state"` // open | merged | closed
	Verdict      string `json:"verdict,omitempty"`
	Level        string `json:"level,omitempty"`
	Title        string `json:"title,omitempty"`
	SourceBranch string `json:"source_branch,omitempty"`
	CheckedAt    string `json:"checked_at,omitempty"`
	Cached       bool   `json:"cached,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Show is the one-word status a table cell gets.
func (s *Status) Show() string {
	switch {
	case s == nil:
		return "?"
	case s.State != "open":
		return s.State
	case s.Verdict != "":
		return s.Verdict
	}
	return "open"
}

func (s *Status) Terminal() bool { return s != nil && (s.State == "merged" || s.State == "closed") }

type MR struct {
	URL      string    `json:"url"`
	Label    string    `json:"label"`
	Opener   *Session  `json:"opener,omitempty"`
	Sessions []Session `json:"sessions"`
	Status   *Status   `json:"status,omitempty"`
	Checkout []string  `json:"checkout,omitempty"`
	LastTS   string    `json:"last_ts"`
}

type Filter struct {
	Ref     string
	Project string
	Since   string // RFC3339; MRs whose newest link is older are dropped
}

var (
	urlRe = regexp.MustCompile(`^https://([^/]+)/(.+?)(?:/-/merge_requests|/pull)/(\d+)`)
	refRe = regexp.MustCompile(`^(?:(.*?)([!#]))?(\d+)$`)
)

type parsed struct {
	host, project, num string
	github             bool
}

func parseURL(u string) (parsed, bool) {
	m := urlRe.FindStringSubmatch(u)
	if m == nil {
		return parsed{}, false
	}
	return parsed{host: m[1], project: m[2], num: m[3], github: m[1] == "github.com"}, true
}

// Matcher returns a predicate for a URL, `repo!N`, `group/repo!N`, `repo#N`, or bare `!N` / `#N` / `N`.
func Matcher(ref string) (func(string) bool, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "https://") {
		want, ok := parseURL(ref)
		if !ok {
			return nil, fmt.Errorf("not an MR/PR url: %s", ref)
		}
		return func(u string) bool { p, ok := parseURL(u); return ok && p == want }, nil
	}
	m := refRe.FindStringSubmatch(ref)
	if m == nil {
		return nil, fmt.Errorf("bad ref %q: want a URL, repo!N, repo#N, or !N", ref)
	}
	repo, sigil, num := strings.ToLower(strings.Trim(m[1], "/")), m[2], m[3]
	return func(u string) bool {
		p, ok := parseURL(u)
		if !ok || p.num != num || (sigil == "!" && p.github) || (sigil == "#" && !p.github) {
			return false
		}
		proj := strings.ToLower(p.project)
		return repo == "" || proj == repo || strings.HasSuffix(proj, "/"+repo)
	}, nil
}

// Find groups each MR's session links; the opener is the earliest 'opened' link, else the earliest link.
func Find(db *sql.DB, f Filter) ([]MR, error) {
	match := func(string) bool { return true }
	if f.Ref != "" {
		var err error
		if match, err = Matcher(f.Ref); err != nil {
			return nil, err
		}
	}
	rows, err := db.Query(`
        SELECT l.target, COALESCE(l.label,''), l.session_id, COALESCE(l.role,'touched'), COALESCE(l.ts,''),
               COALESCE(d.cwd, l.cwd, ''), COALESCE(l.cwd, ''), COALESCE(d.project,'')
          FROM session_links l
          LEFT JOIN documents d ON d.session_id = l.session_id AND d.source_type = 'session'
         WHERE l.kind = 'mr' AND l.role IS NOT NULL
         ORDER BY l.target, l.ts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byURL := map[string]*MR{}
	var order []string
	for rows.Next() {
		var u, label string
		var s Session
		if err := rows.Scan(&u, &label, &s.SessionID, &s.Role, &s.TS, &s.Cwd, &s.RanIn, &s.Project); err != nil {
			return nil, err
		}
		if s.RanIn == s.Cwd {
			s.RanIn = ""
		}
		if !match(u) {
			continue
		}
		m := byURL[u]
		if m == nil {
			m = &MR{URL: u, Label: label}
			byURL[u] = m
			order = append(order, u)
		}
		if s.Cwd != "" {
			s.Resume = fmt.Sprintf("cd %s && claude --resume %s", s.Cwd, s.SessionID)
		}
		m.Sessions = append(m.Sessions, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	proj := strings.ToLower(f.Project)
	var out []MR
	for _, u := range order {
		m := byURL[u]
		for i := range m.Sessions {
			s := &m.Sessions[i]
			if s.TS > m.LastTS {
				m.LastTS = s.TS
			}
			if s.Role == "opened" && (m.Opener == nil || m.Opener.Role != "opened") {
				m.Opener = s
			}
		}
		if m.Opener == nil {
			m.Opener = &m.Sessions[0]
		}
		if f.Since != "" && m.LastTS < f.Since {
			continue
		}
		if proj != "" && !m.touchesProject(proj) {
			continue
		}
		out = append(out, *m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastTS > out[j].LastTS })
	return out, nil
}

func (m *MR) touchesProject(proj string) bool {
	if p, ok := parseURL(m.URL); ok && strings.Contains(strings.ToLower(path.Base(p.project)), proj) {
		return true
	}
	for _, s := range m.Sessions {
		if strings.Contains(strings.ToLower(s.Project), proj) || strings.Contains(strings.ToLower(s.Cwd), proj) ||
			strings.Contains(strings.ToLower(s.RanIn), proj) {
			return true
		}
	}
	return false
}

// Fetcher reads one MR's live state; tests swap it.
var Fetcher = fetch

// Refresh fills Status: merged/closed come from the cache, open ones are fetched when live.
func Refresh(ctx context.Context, db *sql.DB, list []MR, live bool) {
	cache := loadCache(db)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range list {
		m := &list[i]
		if c, ok := cache[m.URL]; ok {
			c.Cached = true
			m.Status = &c
			if c.Terminal() || !live {
				continue
			}
		}
		if !live {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st, err := Fetcher(ctx, m.URL)
			if err != nil {
				if m.Status != nil {
					m.Status.Error = err.Error()
				} else {
					m.Status = &Status{State: "open", Verdict: "?", Error: err.Error()}
				}
				return
			}
			st.CheckedAt = time.Now().UTC().Format(time.RFC3339)
			m.Status = st
		}()
	}
	wg.Wait()
	for _, m := range list {
		if m.Status != nil && !m.Status.Cached && m.Status.Error == "" {
			saveStatus(db, m.URL, m.Status)
		}
	}
}

func loadCache(db *sql.DB) map[string]Status {
	out := map[string]Status{}
	rows, err := db.Query(`SELECT url, COALESCE(title,''), state, COALESCE(verdict,''), COALESCE(level,''),
                                  COALESCE(source_branch,''), checked_at FROM mr_state`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		var s Status
		if rows.Scan(&u, &s.Title, &s.State, &s.Verdict, &s.Level, &s.SourceBranch, &s.CheckedAt) == nil {
			out[u] = s
		}
	}
	return out
}

func saveStatus(db *sql.DB, u string, s *Status) {
	_, _ = db.Exec(`INSERT OR REPLACE INTO mr_state (url, title, state, verdict, level, source_branch, checked_at)
                    VALUES (?, ?, ?, ?, ?, ?, ?)`, u, s.Title, s.State, s.Verdict, s.Level, s.SourceBranch, s.CheckedAt)
}

func fetch(ctx context.Context, u string) (*Status, error) {
	p, ok := parseURL(u)
	if !ok {
		return nil, fmt.Errorf("not an MR/PR url: %s", u)
	}
	if p.github {
		raw, err := run(ctx, "gh", "api", fmt.Sprintf("repos/%s/pulls/%s", p.project, p.num))
		if err != nil {
			return nil, err
		}
		return githubStatus(raw)
	}
	raw, err := run(ctx, "glab", "api", "--hostname", p.host,
		fmt.Sprintf("projects/%s/merge_requests/%s", url.PathEscape(p.project), p.num))
	if err != nil {
		return nil, err
	}
	return gitlabStatus(raw)
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%s: %s", name, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

var running = map[string]bool{"created": true, "pending": true, "running": true, "preparing": true,
	"waiting_for_resource": true, "scheduled": true}

// gitlabStatus mirrors the claude-code-config statusline verdict (hooks/mr_status.py).
func gitlabStatus(raw []byte) (*Status, error) {
	var mr struct {
		State        string `json:"state"`
		Title        string `json:"title"`
		SourceBranch string `json:"source_branch"`
		SHA          string `json:"sha"`
		Draft        bool   `json:"draft"`
		HasConflicts bool   `json:"has_conflicts"`
		Merge        string `json:"detailed_merge_status"`
		Pipe         *struct {
			Status string `json:"status"`
			SHA    string `json:"sha"`
		} `json:"head_pipeline"`
	}
	if err := json.Unmarshal(raw, &mr); err != nil {
		return nil, err
	}
	s := &Status{Title: mr.Title, SourceBranch: mr.SourceBranch}
	switch mr.State {
	case "merged", "closed":
		s.State = mr.State
		return s, nil
	case "locked":
		s.State = "closed"
		return s, nil
	}
	s.State = "open"
	var pipe, pipeSHA string
	if mr.Pipe != nil {
		pipe, pipeSHA = mr.Pipe.Status, mr.Pipe.SHA
	}
	switch {
	case mr.HasConflicts || mr.Merge == "conflict":
		s.Verdict, s.Level = "conflicts", "bad"
	case pipe == "failed" || pipe == "canceled":
		s.Verdict, s.Level = "pipe:failed", "bad"
	case mr.Merge == "need_rebase":
		s.Verdict, s.Level = "rebase", "bad"
	case running[pipe]:
		s.Verdict, s.Level = "pipe:running", "wait"
	case pipe == "":
		s.Verdict, s.Level = "no pipe", "wait"
	case pipeSHA != "" && pipeSHA != mr.SHA:
		s.Verdict, s.Level = "pipe:stale", "wait"
	case mr.Draft || mr.Merge == "draft_status":
		s.Verdict, s.Level = "draft", "wait"
	case mr.Merge == "discussions_not_resolved":
		s.Verdict, s.Level = "threads", "wait"
	case mr.Merge == "not_approved" || mr.Merge == "requested_changes":
		s.Verdict, s.Level = "approval", "wait"
	case mr.Merge == "mergeable":
		s.Verdict, s.Level = "GREEN", "ok"
	default:
		s.Verdict, s.Level = cmpOr(mr.Merge, pipe), "wait"
	}
	return s, nil
}

func githubStatus(raw []byte) (*Status, error) {
	var pr struct {
		State          string `json:"state"`
		Merged         bool   `json:"merged"`
		Draft          bool   `json:"draft"`
		Title          string `json:"title"`
		MergeableState string `json:"mergeable_state"`
		Head           struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, err
	}
	s := &Status{Title: pr.Title, SourceBranch: pr.Head.Ref}
	switch {
	case pr.Merged:
		s.State = "merged"
		return s, nil
	case pr.State == "closed":
		s.State = "closed"
		return s, nil
	}
	s.State = "open"
	switch {
	case pr.Draft:
		s.Verdict, s.Level = "draft", "wait"
	case pr.MergeableState == "dirty":
		s.Verdict, s.Level = "conflicts", "bad"
	case pr.MergeableState == "behind":
		s.Verdict, s.Level = "rebase", "bad"
	case pr.MergeableState == "unstable":
		s.Verdict, s.Level = "checks:failed", "bad"
	case pr.MergeableState == "blocked":
		s.Verdict, s.Level = "blocked", "wait"
	case pr.MergeableState == "clean" || pr.MergeableState == "has_hooks":
		s.Verdict, s.Level = "GREEN", "ok"
	default:
		s.Verdict, s.Level = cmpOr(pr.MergeableState, "pending"), "wait"
	}
	return s, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Checkouts finds local worktrees on each MR's source branch, searching every repo a session ran in.
func Checkouts(ctx context.Context, db *sql.DB, list []MR) {
	need := false
	for _, m := range list {
		if m.Status != nil && m.Status.SourceBranch != "" && !m.Status.Terminal() {
			need = true
		}
	}
	if !need {
		return
	}
	repos := localRepos(ctx, db)
	trees := map[string][][2]string{}
	for i := range list {
		m := &list[i]
		p, ok := parseURL(m.URL)
		if !ok || m.Status == nil || m.Status.SourceBranch == "" || m.Status.Terminal() {
			continue
		}
		key := strings.ToLower(p.host + "/" + p.project)
		for _, common := range repos[key] {
			if _, ok := trees[common]; !ok {
				trees[common] = worktrees(ctx, common)
			}
			for _, t := range trees[common] {
				if t[1] == m.Status.SourceBranch {
					m.Checkout = append(m.Checkout, t[0])
				}
			}
		}
	}
}

// localRepos maps host/project to the git common dirs of checkouts found under session cwds.
func localRepos(ctx context.Context, db *sql.DB) map[string][]string {
	rows, err := db.Query(`SELECT DISTINCT cwd FROM documents WHERE source_type = 'session' AND cwd IS NOT NULL AND cwd <> ''
                           UNION SELECT DISTINCT cwd FROM session_links WHERE cwd IS NOT NULL AND cwd <> ''`)
	if err != nil {
		return nil
	}
	var cwds []string
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			if fi, err := os.Stat(c); err == nil && fi.IsDir() {
				cwds = append(cwds, c)
			}
		}
	}
	rows.Close()
	var mu sync.Mutex
	commons := map[string]bool{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, c := range cwds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out, err := run(ctx, "git", "-C", c, "rev-parse", "--path-format=absolute", "--git-common-dir")
			if err == nil {
				mu.Lock()
				commons[strings.TrimSpace(string(out))] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	out := map[string][]string{}
	for common := range commons {
		raw, err := run(ctx, "git", "--git-dir", common, "config", "--get", "remote.origin.url")
		if err != nil {
			continue
		}
		if key := remoteKey(strings.TrimSpace(string(raw))); key != "" {
			out[key] = append(out[key], common)
		}
	}
	return out
}

var remoteRe = regexp.MustCompile(`^(?:[a-z+]+://)?(?:[^@/]+@)?([^/:]+)(?::\d+)?[:/](.+?)(?:\.git)?/?$`)

func remoteKey(remote string) string {
	m := remoteRe.FindStringSubmatch(remote)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1] + "/" + m[2])
}

func worktrees(ctx context.Context, common string) [][2]string {
	raw, err := run(ctx, "git", "--git-dir", common, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var out [][2]string
	var wt string
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			wt = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			out = append(out, [2]string{wt, strings.TrimPrefix(line, "branch refs/heads/")})
		}
	}
	return out
}

// Number returns the iid/PR number of an MR URL, or 0.
func Number(u string) int {
	p, ok := parseURL(u)
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(p.num)
	return n
}

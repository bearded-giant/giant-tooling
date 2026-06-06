package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
)

type App struct {
	ctx     context.Context
	live    *sql.DB
	archive *sql.DB
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	base := archiveBase()
	if live, err := db.Open(filepath.Join(base, "live.db")); err == nil {
		a.live = live
	} else {
		fmt.Fprintf(os.Stderr, "gui: open live.db: %v\n", err)
	}
	if archive, err := db.Open(filepath.Join(base, "archives.db")); err == nil {
		a.archive = archive
	} else {
		fmt.Fprintf(os.Stderr, "gui: open archives.db: %v\n", err)
	}
}

func (a *App) shutdown(ctx context.Context) {
	if a.live != nil {
		a.live.Close()
		a.live = nil
	}
	if a.archive != nil {
		a.archive.Close()
		a.archive = nil
	}
}

// ListArtifacts returns artifact rows filtered + sorted. limit<=0 means no limit.
// Frontend sees a JS-side artifacts.ListFilter object (Type/Status/Lifecycle as
// arrays; Scope/Repo/Branch/Feature/Domain as strings).
func (a *App) ListArtifacts(filter artifacts.ListFilter, sortBy string, limit int) ([]artifacts.Artifact, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	return artifacts.ListArtifacts(a.live, filter, sortBy, limit)
}

// FacetCountsResult bundles the facet maps returned by
// artifacts.FacetCountsExt so Wails can ship them as one JS object.
type FacetCountsResult struct {
	ByType      map[string]int `json:"byType"`
	ByLifecycle map[string]int `json:"byLifecycle"`
	ByStatus    map[string]int `json:"byStatus"`
	ByFeature   map[string]int `json:"byFeature"`
	ByRepo      map[string]int `json:"byRepo"`
}

func (a *App) FacetCounts() (FacetCountsResult, error) {
	if a.live == nil {
		return FacetCountsResult{}, fmt.Errorf("live db not open")
	}
	t, l, s, f, r, err := artifacts.FacetCountsExt(a.live)
	if err != nil {
		return FacetCountsResult{}, err
	}
	return FacetCountsResult{
		ByType:      t,
		ByLifecycle: l,
		ByStatus:    s,
		ByFeature:   f,
		ByRepo:      r,
	}, nil
}

// SearchHybrid runs the blended ranker. Candidates come from the artifacts
// projection (filtered if filter is non-empty); the query vector is resolved
// via the daemon's `embed` RPC so the GUI never loads an embedding model.
// When the daemon is down, vector score collapses to zero — FTS/recency/access
// still rank the result set.
func (a *App) SearchHybrid(query string, filter artifacts.ListFilter, limit int) ([]search.HybridResult, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	candidates, err := artifacts.ListArtifacts(a.live, filter, "", 0)
	if err != nil {
		return nil, err
	}
	queryVec, _ := daemonEmbed(query)
	return search.Hybrid(a.live, query, queryVec, candidates, search.DefaultHybridWeights(), limit)
}

// SearchFTS runs the FTS5 query path across archives.db + live.db (either may
// be nil — search.Run scopes to whichever is open). Returns ranked hits with
// snippets, suitable for the session viewer's row list.
func (a *App) SearchFTS(params search.Params) ([]search.Hit, error) {
	return search.Run(a.archive, a.live, params)
}

// FeatureRow describes one (repo, feature) pair with its artifact count and
// a sample worktree, so the sidebar can group features under their owning
// repo and still distinguish between worktree branches that share a feature.
type FeatureRow struct {
	Repo     string `json:"repo"`
	Feature  string `json:"feature"`
	Count    int    `json:"count"`
	Worktree string `json:"worktree,omitempty"`
}

// FeaturesByRepo returns every (repo, feature) pair from the artifacts table
// with its count and one sample worktree. Ordered by repo asc, then count
// desc. Features with empty name are omitted (those rows are repo-scoped
// artifacts like plans/current.md that don't belong to a feature folder).
func (a *App) FeaturesByRepo() ([]FeatureRow, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	rows, err := a.live.Query(
		`SELECT repo, feature, COUNT(*) AS n, MAX(worktree) AS wt
           FROM artifacts
          WHERE feature <> ''
          GROUP BY repo, feature
          ORDER BY repo ASC, n DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeatureRow
	for rows.Next() {
		var r FeatureRow
		if err := rows.Scan(&r.Repo, &r.Feature, &r.Count, &r.Worktree); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SessionFilter scopes ListSessions and SessionFacets. Each non-empty field
// is ANDed into the WHERE clause. DateBucket accepts one of:
//   "today" / "yesterday" / "7d" / "30d" / "older"
// matching the same buckets the sidebar renders.
type SessionFilter struct {
	Project    string `json:"project,omitempty"`
	DirType    string `json:"dirType,omitempty"`
	Topic      string `json:"topic,omitempty"`
	DateBucket string `json:"dateBucket,omitempty"`
}

// dateBucketWhere translates a SessionFilter.DateBucket label into a SQL
// fragment + bound args using SQLite's datetime('now') anchor.
func dateBucketWhere(bucket string) (string, []any) {
	switch bucket {
	case "today":
		return ` AND date(timestamp) = date('now', 'localtime')`, nil
	case "yesterday":
		return ` AND date(timestamp) = date('now', '-1 day', 'localtime')`, nil
	case "7d":
		return ` AND timestamp >= datetime('now', '-7 days') AND date(timestamp) < date('now', '-1 day', 'localtime')`, nil
	case "30d":
		return ` AND timestamp >= datetime('now', '-30 days') AND timestamp < datetime('now', '-7 days')`, nil
	case "older":
		return ` AND timestamp < datetime('now', '-30 days')`, nil
	}
	return "", nil
}

// ListSessions returns the most recent session rows from archives.db without
// running an FTS5 MATCH — used when the search input is empty (FTS5 errors on
// empty queries). All filter fields are optional and AND-combined.
func (a *App) ListSessions(filter SessionFilter, limit int) ([]search.Hit, error) {
	if a.archive == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT
            COALESCE(project,''), COALESCE(timestamp,''), COALESCE(source_type,''),
            COALESCE(dir_type,''), filepath, filename,
            COALESCE(is_latest,0), COALESCE(session_id,''),
            COALESCE(cwd,''), COALESCE(topic,'')
          FROM documents
          WHERE source_type = 'session'`
	args := []any{}
	if filter.Project != "" {
		q += ` AND (project = ? OR canonical_project = ?)`
		args = append(args, filter.Project, filter.Project)
	}
	if filter.DirType != "" {
		q += ` AND dir_type = ?`
		args = append(args, filter.DirType)
	}
	if filter.Topic != "" {
		q += ` AND topic = ?`
		args = append(args, filter.Topic)
	}
	if frag, _ := dateBucketWhere(filter.DateBucket); frag != "" {
		q += frag
	}
	q += ` ORDER BY timestamp DESC LIMIT ?`
	args = append(args, limit)

	rows, err := a.archive.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []search.Hit
	for rows.Next() {
		var h search.Hit
		var isLatest int
		if err := rows.Scan(
			&h.Project, &h.Timestamp, &h.SourceType, &h.DirType,
			&h.Filepath, &h.Filename, &isLatest, &h.SessionID,
			&h.Cwd, &h.Topic,
		); err != nil {
			return nil, err
		}
		h.IsLatest = isLatest != 0
		h.Source = "archive"
		out = append(out, h)
	}
	return out, rows.Err()
}

// ToolUseHit describes one tool_use occurrence inside a session JSONL: where
// it lives, which tool ran, a short rendering of the input, and the paired
// tool_result body when one is present. Used by the GUI's tool-use search.
type ToolUseHit struct {
	SessionPath  string `json:"sessionPath"`
	SessionID    string `json:"sessionId"`
	Project      string `json:"project,omitempty"`
	Timestamp    string `json:"timestamp,omitempty"`
	TurnIndex    int    `json:"turnIndex"`
	ToolName     string `json:"toolName"`
	InputSummary string `json:"inputSummary"`
	InputJSON    string `json:"inputJSON"`
	Output       string `json:"output,omitempty"`
	OutputClip   string `json:"outputClip,omitempty"`
	IsError      bool   `json:"isError,omitempty"`
}

// ToolUseFilter scopes SearchToolUses. Query is a case-insensitive substring
// matched against the tool input JSON and the paired tool_result body. Empty
// fields are ignored. Limit caps the result count (default 200).
type ToolUseFilter struct {
	Query     string `json:"query,omitempty"`
	ToolName  string `json:"toolName,omitempty"`
	Project   string `json:"project,omitempty"`
	UseFTSPre bool   `json:"useFTSPre,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// SearchToolUses scans session JSONL files for tool_use blocks that match the
// filter. When UseFTSPre is true we narrow the candidate session list via
// archives.db FTS first; otherwise we scan every session row in documents.
// This is the heavy path — we open and stream-parse each candidate jsonl —
// but for typical 'find every kubectl' queries the FTS pre-filter trims the
// fan-out to dozens of files.
func (a *App) SearchToolUses(filter ToolUseFilter) ([]ToolUseHit, error) {
	if a.archive == nil {
		return nil, nil
	}
	if filter.Limit <= 0 {
		filter.Limit = 200
	}
	type row struct {
		path, sessionID, project, timestamp string
	}
	var rows []row
	q := filter.Query
	if filter.UseFTSPre && strings.TrimSpace(q) != "" {
		fts := search.SanitizeFTSQuery(q)
		stmt := `SELECT d.filepath, COALESCE(d.session_id,''), COALESCE(d.project,''), COALESCE(d.timestamp,'')
                   FROM documents d
                   JOIN documents_fts f ON f.rowid = d.id
                  WHERE d.source_type = 'session'
                    AND documents_fts MATCH ?`
		args := []any{fts}
		if filter.Project != "" {
			stmt += ` AND (d.project = ? OR d.canonical_project = ?)`
			args = append(args, filter.Project, filter.Project)
		}
		stmt += ` ORDER BY d.timestamp DESC`
		r, err := a.archive.Query(stmt, args...)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		for r.Next() {
			var rr row
			if err := r.Scan(&rr.path, &rr.sessionID, &rr.project, &rr.timestamp); err != nil {
				return nil, err
			}
			rows = append(rows, rr)
		}
	} else {
		stmt := `SELECT filepath, COALESCE(session_id,''), COALESCE(project,''), COALESCE(timestamp,'')
                   FROM documents WHERE source_type = 'session'`
		args := []any{}
		if filter.Project != "" {
			stmt += ` AND (project = ? OR canonical_project = ?)`
			args = append(args, filter.Project, filter.Project)
		}
		stmt += ` ORDER BY timestamp DESC`
		r, err := a.archive.Query(stmt, args...)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		for r.Next() {
			var rr row
			if err := r.Scan(&rr.path, &rr.sessionID, &rr.project, &rr.timestamp); err != nil {
				return nil, err
			}
			rows = append(rows, rr)
		}
	}
	needle := strings.ToLower(strings.TrimSpace(filter.Query))
	wantTool := filter.ToolName
	var out []ToolUseHit
	for _, rr := range rows {
		hits, err := scanToolUses(rr.path, rr.sessionID, rr.project, rr.timestamp, wantTool, needle, filter.Limit-len(out))
		if err != nil {
			continue
		}
		out = append(out, hits...)
		if len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

// scanToolUses walks one jsonl file, pairs tool_use blocks with their later
// tool_result by id, and emits ToolUseHit rows for blocks matching wantTool
// (when non-empty) and needle (case-insensitive substring on input json +
// result body). Stops once remaining hits are exhausted.
func scanToolUses(path, sessionID, project, timestamp, wantTool, needle string, remaining int) ([]ToolUseHit, error) {
	if remaining <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	type pending struct {
		hit       ToolUseHit
		matchesIn bool
	}
	byID := map[string]*pending{}
	var out []ToolUseHit

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	turn := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}
		turn++
		msg, _ := obj["message"].(map[string]any)
		ts, _ := obj["timestamp"].(string)
		content := msg["content"]
		blocks, ok := content.([]any)
		if !ok {
			continue
		}
		for _, b := range blocks {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch bm["type"] {
			case "tool_use":
				name, _ := bm["name"].(string)
				if wantTool != "" && wantTool != name {
					continue
				}
				input := bm["input"]
				inputJSON, _ := json.Marshal(input)
				summary := summarizeToolInput(name, input)
				matches := true
				if needle != "" {
					hay := strings.ToLower(string(inputJSON) + "\n" + summary)
					matches = strings.Contains(hay, needle)
				}
				id, _ := bm["id"].(string)
				p := &pending{
					matchesIn: matches,
					hit: ToolUseHit{
						SessionPath:  path,
						SessionID:    sessionID,
						Project:      project,
						Timestamp:    firstNonEmpty(ts, timestamp),
						TurnIndex:    turn,
						ToolName:     name,
						InputSummary: summary,
						InputJSON:    string(inputJSON),
					},
				}
				if id != "" {
					byID[id] = p
				} else if matches {
					out = append(out, p.hit)
					if len(out) >= remaining {
						return out, nil
					}
				}
			case "tool_result":
				id, _ := bm["tool_use_id"].(string)
				p, ok := byID[id]
				if !ok {
					continue
				}
				body := stringifyToolResult(bm["content"])
				isErr, _ := bm["is_error"].(bool)
				p.hit.Output = body
				p.hit.OutputClip = clipText(body, 240)
				p.hit.IsError = isErr
				if !p.matchesIn && needle != "" {
					if strings.Contains(strings.ToLower(body), needle) {
						p.matchesIn = true
					}
				}
				if p.matchesIn {
					out = append(out, p.hit)
					delete(byID, id)
					if len(out) >= remaining {
						return out, nil
					}
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	// flush remaining pending entries that matched on input but never paired
	// with a result.
	ordered := make([]*pending, 0, len(byID))
	for _, p := range byID {
		if p.matchesIn {
			ordered = append(ordered, p)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].hit.TurnIndex < ordered[j].hit.TurnIndex })
	for _, p := range ordered {
		out = append(out, p.hit)
		if len(out) >= remaining {
			break
		}
	}
	return out, nil
}

func summarizeToolInput(name string, input any) string {
	im, _ := input.(map[string]any)
	if im == nil {
		return ""
	}
	get := func(k string) string {
		s, _ := im[k].(string)
		return s
	}
	switch name {
	case "Bash":
		return clipText(firstNonEmpty(get("description"), get("command")), 160)
	case "Read", "Write", "Edit":
		return clipText(get("file_path"), 160)
	case "Glob":
		return clipText(get("pattern"), 160)
	case "Grep":
		return clipText(strings.TrimSpace(get("pattern")+" in "+get("path")), 160)
	case "WebFetch", "WebSearch":
		return clipText(firstNonEmpty(get("url"), get("query")), 160)
	case "Skill":
		return clipText(get("skill"), 80)
	case "Agent":
		return clipText(firstNonEmpty(get("description"), get("subagent_type")), 120)
	case "TodoWrite":
		if arr, ok := im["todos"].([]any); ok {
			return fmt.Sprintf("%d todos", len(arr))
		}
	}
	for k, v := range im {
		if s, ok := v.(string); ok && s != "" {
			return clipText(k+": "+s, 160)
		}
	}
	return ""
}

func stringifyToolResult(c any) string {
	switch v := c.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		var parts []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
				continue
			}
			if m, ok := x.(map[string]any); ok {
				if t, _ := m["text"].(string); t != "" {
					parts = append(parts, t)
					continue
				}
			}
			b, _ := json.Marshal(x)
			parts = append(parts, string(b))
		}
		return strings.Join(parts, "\n")
	}
	b, _ := json.Marshal(c)
	return string(b)
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func firstNonEmpty(strs ...string) string {
	for _, s := range strs {
		if s != "" {
			return s
		}
	}
	return ""
}

// SessionFacetCounts mirrors FacetCountsResult but groups sessions by their
// archives.db dimensions: project, dir_type, topic, and a coarse date bucket.
type SessionFacetCounts struct {
	ByProject map[string]int `json:"byProject"`
	ByDirType map[string]int `json:"byDirType"`
	ByTopic   map[string]int `json:"byTopic"`
	ByDate    map[string]int `json:"byDate"`
}

// SessionFacets returns counts per project / dir_type / topic / date-bucket
// for source_type='session' rows. Used to render the sessions-tab sidebar.
func (a *App) SessionFacets() (SessionFacetCounts, error) {
	out := SessionFacetCounts{
		ByProject: map[string]int{},
		ByDirType: map[string]int{},
		ByTopic:   map[string]int{},
		ByDate:    map[string]int{},
	}
	if a.archive == nil {
		return out, nil
	}
	groups := []struct {
		col string
		dst map[string]int
	}{
		{"COALESCE(canonical_project, project, '')", out.ByProject},
		{"COALESCE(dir_type,'')", out.ByDirType},
		{"COALESCE(topic,'')", out.ByTopic},
	}
	for _, g := range groups {
		rows, err := a.archive.Query(fmt.Sprintf(
			`SELECT %s, COUNT(*) FROM documents WHERE source_type='session' GROUP BY %s`,
			g.col, g.col,
		))
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return out, err
			}
			g.dst[k] = n
		}
		rows.Close()
	}
	// Date buckets in a single pass via CASE WHEN.
	dateQ := `SELECT
                  CASE
                    WHEN date(timestamp) = date('now','localtime') THEN 'today'
                    WHEN date(timestamp) = date('now','-1 day','localtime') THEN 'yesterday'
                    WHEN timestamp >= datetime('now','-7 days') THEN '7d'
                    WHEN timestamp >= datetime('now','-30 days') THEN '30d'
                    ELSE 'older'
                  END AS bucket,
                  COUNT(*)
              FROM documents
              WHERE source_type='session'
              GROUP BY bucket`
	rows, err := a.archive.Query(dateQ)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return out, err
		}
		out.ByDate[k] = n
	}
	return out, rows.Err()
}

// GetArtifact returns one artifact row by ID. Errors when nothing matches.
func (a *App) GetArtifact(id string) (artifacts.Artifact, error) {
	if a.live == nil {
		return artifacts.Artifact{}, fmt.Errorf("live db not open")
	}
	rows, err := artifacts.ListArtifacts(a.live, artifacts.ListFilter{}, "", 0)
	if err != nil {
		return artifacts.Artifact{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return r, nil
		}
	}
	return artifacts.Artifact{}, fmt.Errorf("artifact not found: %s", id)
}

// GetArtifactBody returns the raw markdown of one artifact, resolved via the
// stored worktree + .giantmem/ + path. Empty string when the file is missing.
func (a *App) GetArtifactBody(id string) (string, error) {
	art, err := a.GetArtifact(id)
	if err != nil {
		return "", err
	}
	if art.Worktree == "" || art.Path == "" {
		return "", fmt.Errorf("artifact has no path: %s", id)
	}
	abs := filepath.Join(art.Worktree, ".giantmem", art.Path)
	body, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ReadFile returns the raw bytes of any file as a string. Used by the session
// viewer to render Claude transcript JSONL given a Hit.Filepath. No path
// sandboxing yet — GUI is single-user localhost.
func (a *App) ReadFile(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// RepoActivity describes one (project, worktree) bucket: how many .giantmem
// docs it holds and when any of them was last touched. Powers the GUI
// activity tab's rolling-log view — newest project at the top.
type RepoActivity struct {
	Project      string `json:"project"`
	WorktreePath string `json:"worktreePath"`
	DocCount     int    `json:"docCount"`
	Mtime        int64  `json:"mtime"`
}

// FileActivity is one row in the per-project expand panel. Mirrors what
// `giantmem recent docs -p <project>` returns from the CLI.
type FileActivity struct {
	Path    string `json:"path"`
	Project string `json:"project"`
	Feature string `json:"feature,omitempty"`
	DirType string `json:"dirType"`
	Mtime   int64  `json:"mtime"`
}

// RecentRepos returns the N most-recently-touched projects (live.db),
// ordered by last mtime desc. limit<=0 → 30.
func (a *App) RecentRepos(limit int) ([]RepoActivity, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	if limit <= 0 {
		limit = 30
	}
	rows, err := a.live.Query(`
        SELECT project, COALESCE(worktree_path,''),
               COUNT(*) AS docs, MAX(mtime) AS m
          FROM live_docs
         WHERE worktree_path IS NOT NULL AND worktree_path <> ''
         GROUP BY worktree_path
         ORDER BY m DESC
         LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RepoActivity
	for rows.Next() {
		var r RepoActivity
		if err := rows.Scan(&r.Project, &r.WorktreePath, &r.DocCount, &r.Mtime); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentFiles returns the N most-recently-touched files for one worktree.
// Caller passes the worktree_path (not the project) so split repo/wt rows
// stay separate. limit<=0 → 50.
func (a *App) RecentFiles(worktreePath string, limit int) ([]FileActivity, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	if worktreePath == "" {
		return nil, fmt.Errorf("worktreePath required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := a.live.Query(`
        SELECT path, project, COALESCE(feature,''), COALESCE(dir_type,''), mtime
          FROM live_docs
         WHERE worktree_path = ?
         ORDER BY mtime DESC
         LIMIT ?`, worktreePath, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileActivity
	for rows.Next() {
		var r FileActivity
		if err := rows.Scan(&r.Path, &r.Project, &r.Feature, &r.DirType, &r.Mtime); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LiveMtime returns the live.db file mtime as unix seconds. Frontend polls
// this on a 5s interval; when it changes, the GUI bumps reloadKey and re-runs
// all queries — that's how the GUI tracks daemon-side reconciles and peer
// PostToolUse writes without an explicit subscribe RPC.
//
// Returns 0 on stat error (file missing, etc.) so a polling caller can no-op
// rather than thrash on transient failures.
func (a *App) LiveMtime() int64 {
	base := archiveBase()
	st, err := os.Stat(filepath.Join(base, "live.db"))
	if err != nil {
		return 0
	}
	return st.ModTime().Unix()
}

// daemonEmbed asks the running giantmemd to embed text with its real backend.
// Returns (vec, true) on success; (nil, false) when the daemon is down so
// callers can degrade gracefully. GUI never loads its own embedder.
func daemonEmbed(text string) ([]float32, bool) {
	if text == "" {
		return nil, false
	}
	sock := daemon.DefaultSocketPath()
	if !daemon.SocketAlive(sock, 250*time.Millisecond) {
		return nil, false
	}
	cli := daemon.NewClient(sock, 5*time.Second)
	var out daemon.EmbedResult
	if err := cli.Call("embed", &daemon.EmbedParams{Text: text}, &out); err != nil {
		return nil, false
	}
	if len(out.Vec) == 0 {
		return nil, false
	}
	return out.Vec, true
}

func archiveBase() string {
	if v := os.Getenv("GIANTMEM_ARCHIVE_BASE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "giantmem_archive")
}

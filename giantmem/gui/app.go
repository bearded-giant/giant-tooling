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

// Version is the GUI build version. Default mirrors wails.json
// info.productVersion; the Makefile overrides it via
// -ldflags "-X main.Version=..." for dev builds (release tag plus
// git short-sha and optional .dirty marker).
var Version = "0.2.0"

// Version exposes the GUI build version to the frontend.
func (a *App) Version() string {
	return Version
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
func (a *App) ListArtifacts(filter artifacts.ListFilter, sortBy string, limit int, since, until string) ([]artifacts.Artifact, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	if since != "" {
		t, err := search.ParseSince(since)
		if err != nil {
			return nil, err
		}
		filter.Since = t
	}
	if until != "" {
		t, err := search.ParseUntil(until)
		if err != nil {
			return nil, err
		}
		filter.Until = t
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
func (a *App) SearchHybrid(query string, filter artifacts.ListFilter, limit int, since, until string) ([]search.HybridResult, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	if since != "" {
		t, err := search.ParseSince(since)
		if err != nil {
			return nil, err
		}
		filter.Since = t
	}
	if until != "" {
		t, err := search.ParseUntil(until)
		if err != nil {
			return nil, err
		}
		filter.Until = t
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
// is ANDed into the WHERE clause. Since/Until are date-range specs (duration
// like "7d", a bare date "2006-01-02", or RFC3339) shared with the other views.
type SessionFilter struct {
	Project string `json:"project,omitempty"`
	DirType string `json:"dirType,omitempty"`
	Topic   string `json:"topic,omitempty"`
	Since   string `json:"since,omitempty"`
	Until   string `json:"until,omitempty"`
}

// docTimeBounds resolves since/until specs to the compact documents.timestamp
// layout (YYYYMMDD_HHMMSS) for lexical comparison. Empty spec → empty bound.
func docTimeBounds(since, until string) (lo, hi string, err error) {
	if since != "" {
		t, e := search.ParseSince(since)
		if e != nil {
			return "", "", e
		}
		lo = t.Format("20060102_150405")
	}
	if until != "" {
		t, e := search.ParseUntil(until)
		if e != nil {
			return "", "", e
		}
		hi = t.Format("20060102_150405")
	}
	return lo, hi, nil
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
	lo, hi, err := docTimeBounds(filter.Since, filter.Until)
	if err != nil {
		return nil, err
	}
	if lo != "" {
		q += ` AND timestamp >= ?`
		args = append(args, lo)
	}
	if hi != "" {
		q += ` AND timestamp < ?`
		args = append(args, hi)
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
	Since     string `json:"since,omitempty"`
	Until     string `json:"until,omitempty"`
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
	var sinceT, untilT time.Time
	if filter.Since != "" {
		t, err := search.ParseSince(filter.Since)
		if err != nil {
			return nil, err
		}
		sinceT = t
	}
	if filter.Until != "" {
		t, err := search.ParseUntil(filter.Until)
		if err != nil {
			return nil, err
		}
		untilT = t
	}
	// no SQL time-prune: documents.timestamp is session-representative, so a
	// session that starts before `since` may still hold in-range tool_uses.
	// filter per-hit inside the scan. ponytail: full scan for sparse narrow
	// ranges; add a documents.timestamp early-break if this gets slow.
	var out []ToolUseHit
	for _, rr := range rows {
		hits, err := scanToolUses(rr.path, rr.sessionID, rr.project, rr.timestamp, wantTool, needle, sinceT, untilT, filter.Limit-len(out))
		if err != nil {
			continue
		}
		out = append(out, hits...)
		if len(out) >= filter.Limit {
			break
		}
	}
	// recent first across all scanned sessions (ISO ts sorts chronologically)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp > out[j].Timestamp })
	return out, nil
}

// scanToolUses walks one jsonl file, pairs tool_use blocks with their later
// tool_result by id, and emits ToolUseHit rows for blocks matching wantTool
// (when non-empty) and needle (case-insensitive substring on input json +
// result body). Stops once remaining hits are exhausted.
func scanToolUses(path, sessionID, project, timestamp, wantTool, needle string, since, until time.Time, remaining int) ([]ToolUseHit, error) {
	if remaining <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	inRange := func(tsStr string) bool {
		if since.IsZero() && until.IsZero() {
			return true
		}
		mt, err := time.Parse(time.RFC3339, tsStr)
		if err != nil {
			return true // compact-format fallback / unparseable — don't drop
		}
		if !since.IsZero() && mt.Before(since) {
			return false
		}
		if !until.IsZero() && !mt.Before(until) {
			return false
		}
		return true
	}

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
				} else if matches && inRange(p.hit.Timestamp) {
					out = append(out, p.hit)
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
				if p.matchesIn && inRange(p.hit.Timestamp) {
					out = append(out, p.hit)
					delete(byID, id)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	// unpaired tool_use blocks (no tool_result yet) that still matched on input
	for _, p := range byID {
		if p.matchesIn && inRange(p.hit.Timestamp) {
			out = append(out, p.hit)
		}
	}
	// newest turn first, then cap — a >remaining session yields its most recent
	// hits, not its oldest.
	sort.SliceStable(out, func(i, j int) bool { return out[i].TurnIndex > out[j].TurnIndex })
	if len(out) > remaining {
		out = out[:remaining]
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

// GetArtifactBody returns the raw markdown of one artifact. Reads disk when the
// worktree is live, else the durable copy in live_docs.content — so a removed
// worktree no longer means a lost doc.
func (a *App) GetArtifactBody(id string) (string, error) {
	art, err := a.GetArtifact(id)
	if err != nil {
		return "", err
	}
	return artifacts.Body(a.live, art)
}

// BrowseRow is one live_docs file for the tree sidebar. Flat — the frontend
// nests by repo/feature/rel. Dead marks a worktree that no longer exists on
// disk; the body still serves from live_docs.content.
type BrowseRow struct {
	Path      string `json:"path"`
	Rel       string `json:"rel"`
	Repo      string `json:"repo"`
	Feature   string `json:"feature"`
	Worktree  string `json:"worktree"`
	Type      string `json:"type"`
	Mtime     int64  `json:"mtime"`
	SessionID string `json:"sessionId"`
	Dead      bool   `json:"dead"`
}

// BrowseTree returns every .giantmem/ file known to live_docs, typed via the
// same classifier as the artifacts projection (empty type = infra/unknown).
func (a *App) BrowseTree() ([]BrowseRow, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	rows, err := a.live.Query(
		`SELECT path, project, COALESCE(feature,''), COALESCE(worktree_path,''),
                COALESCE(session_id,''), mtime
           FROM live_docs
          WHERE instr(path, '/.giantmem/') > 0
          ORDER BY project, path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	deadCache := map[string]bool{}
	isDead := func(wt string) bool {
		if wt == "" {
			return false
		}
		d, ok := deadCache[wt]
		if !ok {
			_, err := os.Stat(wt)
			d = err != nil
			deadCache[wt] = d
		}
		return d
	}

	var out []BrowseRow
	for rows.Next() {
		var r BrowseRow
		if err := rows.Scan(&r.Path, &r.Repo, &r.Feature, &r.Worktree, &r.SessionID, &r.Mtime); err != nil {
			return nil, err
		}
		rel, ok := artifacts.RelFromLivePath(r.Path)
		if !ok {
			continue
		}
		r.Rel = rel
		if cls, ok := artifacts.Classify(rel); ok {
			r.Type = cls.Type
			if r.Feature == "" {
				r.Feature = cls.Feature
			}
		}
		r.Dead = isDead(r.Worktree)
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetLiveBody returns any live_docs file's body — disk first, DB fallback.
// Works for files that never classified as typed artifacts.
func (a *App) GetLiveBody(path string) (string, error) {
	if a.live == nil {
		return "", fmt.Errorf("live db not open")
	}
	return artifacts.BodyByPath(a.live, path)
}

// SessionPathByID resolves a session id to its transcript jsonl path so the
// browse view can jump file -> owning session.
func (a *App) SessionPathByID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("empty session id")
	}
	if a.archive != nil {
		var p string
		err := a.archive.QueryRow(
			`SELECT filepath FROM documents
              WHERE source_type='session' AND session_id=?
              ORDER BY timestamp DESC LIMIT 1`, id).Scan(&p)
		if err == nil && p != "" {
			return p, nil
		}
	}
	if a.live != nil {
		var p string
		err := a.live.QueryRow(
			`SELECT COALESCE(jsonl_path,'') FROM active_sessions WHERE id=?`, id).Scan(&p)
		if err == nil && p != "" {
			return p, nil
		}
	}
	return "", fmt.Errorf("no transcript for session %s", id)
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

// ActivityCounts is the small headline-tile bundle: total live_docs across
// all repos, total session JSONLs in archives.db, live_docs written today
// (local), and count of active (status=in_progress) features. Used by the
// activity tab sidebar — one round-trip, four numbers.
type ActivityCounts struct {
	LiveDocs      int `json:"liveDocs"`
	Sessions      int `json:"sessions"`
	WritesToday   int `json:"writesToday"`
	ActiveFeatures int `json:"activeFeatures"`
}

func (a *App) ActivityCounts() (ActivityCounts, error) {
	var c ActivityCounts
	if a.live != nil {
		_ = a.live.QueryRow("SELECT COUNT(*) FROM live_docs").Scan(&c.LiveDocs)
		// writes today: ingested_at is RFC3339 in UTC; compare via date()
		// "today" = local-time day. ingested_at is stored as RFC3339 UTC; cast
		// to localtime before comparing dates so the tile doesn't roll over
		// at 17:00 PDT (00:00 UTC).
		_ = a.live.QueryRow(
			`SELECT COUNT(*) FROM live_docs
              WHERE date(ingested_at, 'localtime') = date('now', 'localtime')`,
		).Scan(&c.WritesToday)
		// active features: distinct (project, feature) where any artifact has status=in_progress.
		// artifacts table mirrors live_docs frontmatter status.
		_ = a.live.QueryRow(
			`SELECT COUNT(DISTINCT repo || '/' || feature)
               FROM artifacts
              WHERE status = 'in_progress' AND feature <> ''`,
		).Scan(&c.ActiveFeatures)
	}
	if a.archive != nil {
		_ = a.archive.QueryRow(
			"SELECT COUNT(*) FROM documents WHERE source_type = 'session'",
		).Scan(&c.Sessions)
	}
	return c, nil
}

// SparklinePoint is one (date, count) tuple for a per-project mini bar chart.
// Dates are YYYY-MM-DD in local time, ascending. Missing days are filled
// with count=0 server-side so the frontend can render a fixed-width bar
// without gap-filling.
type SparklinePoint struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// ProjectSparkline returns last `days` days of doc-write counts for one
// worktree. Days <=0 → 7. Caller passes the worktree_path so split repo/wt
// rows stay distinct (matches RecentRepos grouping).
func (a *App) ProjectSparkline(worktreePath string, days int) ([]SparklinePoint, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	if worktreePath == "" {
		return nil, fmt.Errorf("worktreePath required")
	}
	if days <= 0 {
		days = 7
	}
	rows, err := a.live.Query(`
        SELECT date(mtime, 'unixepoch', 'localtime') AS d, COUNT(*)
          FROM live_docs
         WHERE worktree_path = ?
           AND mtime >= strftime('%s', 'now', ?, 'start of day')
         GROUP BY d
         ORDER BY d ASC`, worktreePath, fmt.Sprintf("-%d days", days-1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]int{}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		found[d] = n
	}
	// Fill missing days. Compute date strings client-side; sqlite date math
	// already aligned to 'start of day' so simple Time math works.
	out := make([]SparklinePoint, 0, days)
	now := time.Now()
	for i := days - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		out = append(out, SparklinePoint{Day: day, Count: found[day]})
	}
	return out, nil
}

// HeatmapCell is one (worktree, day, count) tuple. The frontend pivots into
// a grid: rows = worktrees, cols = days (ascending).
type HeatmapCell struct {
	WorktreePath string `json:"worktreePath"`
	Project      string `json:"project"`
	Day          string `json:"day"`
	Count        int    `json:"count"`
}

// ProjectHeatmap returns cells for the top-N worktrees (by total writes in
// the window) across the last `days` days. days<=0 → 14, topN<=0 → 10.
// Server-side fills zero-count cells so the frontend just renders a grid.
func (a *App) ProjectHeatmap(days, topN int) ([]HeatmapCell, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	if days <= 0 {
		days = 14
	}
	if topN <= 0 {
		topN = 10
	}
	since := fmt.Sprintf("-%d days", days-1)

	// pick top-N worktrees by total writes in window
	topRows, err := a.live.Query(`
        SELECT worktree_path, MAX(project), COUNT(*) AS n
          FROM live_docs
         WHERE worktree_path IS NOT NULL AND worktree_path <> ''
           AND mtime >= strftime('%s', 'now', ?, 'start of day')
         GROUP BY worktree_path
         ORDER BY n DESC
         LIMIT ?`, since, topN)
	if err != nil {
		return nil, err
	}
	type wtInfo struct{ project string }
	wts := map[string]wtInfo{}
	var wtOrder []string
	for topRows.Next() {
		var wt, proj string
		var n int
		if err := topRows.Scan(&wt, &proj, &n); err != nil {
			topRows.Close()
			return nil, err
		}
		wts[wt] = wtInfo{project: proj}
		wtOrder = append(wtOrder, wt)
	}
	topRows.Close()
	if len(wtOrder) == 0 {
		return nil, nil
	}

	// counts per (wt, day) for those worktrees
	placeholders := make([]string, len(wtOrder))
	args := []any{since}
	for i, wt := range wtOrder {
		placeholders[i] = "?"
		args = append(args, wt)
	}
	q := fmt.Sprintf(`
        SELECT worktree_path, date(mtime,'unixepoch','localtime') AS d, COUNT(*)
          FROM live_docs
         WHERE mtime >= strftime('%%s','now',?,'start of day')
           AND worktree_path IN (%s)
         GROUP BY worktree_path, d`, strings.Join(placeholders, ","))
	rows, err := a.live.Query(q, args...)
	if err != nil {
		return nil, err
	}
	type key struct{ wt, day string }
	found := map[key]int{}
	for rows.Next() {
		var wt, d string
		var n int
		if err := rows.Scan(&wt, &d, &n); err != nil {
			rows.Close()
			return nil, err
		}
		found[key{wt, d}] = n
	}
	rows.Close()

	// fill grid
	out := make([]HeatmapCell, 0, len(wtOrder)*days)
	now := time.Now()
	for _, wt := range wtOrder {
		for i := days - 1; i >= 0; i-- {
			day := now.AddDate(0, 0, -i).Format("2006-01-02")
			out = append(out, HeatmapCell{
				WorktreePath: wt,
				Project:      wts[wt].project,
				Day:          day,
				Count:        found[key{wt, day}],
			})
		}
	}
	return out, nil
}

// GetPref / SetPref give the frontend a Go-backed key/value store so UI
// preferences (sidebar width, last tab, etc.) survive across launches even
// when WKWebView's localStorage is non-persistent. File-backed at
// ~/.config/giantmem/gui-prefs.json. Atomic write via temp+rename.
func (a *App) GetPref(key string) (string, error) {
	m, err := readPrefs()
	if err != nil {
		return "", err
	}
	return m[key], nil
}

func (a *App) SetPref(key, value string) error {
	m, err := readPrefs()
	if err != nil {
		return err
	}
	if value == "" {
		delete(m, key)
	} else {
		m[key] = value
	}
	return writePrefs(m)
}

func prefsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "giantmem", "gui-prefs.json")
}

func readPrefs() (map[string]string, error) {
	p := prefsPath()
	body, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	m := map[string]string{}
	if len(body) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return map[string]string{}, nil
	}
	return m, nil
}

func writePrefs(m map[string]string) error {
	p := prefsPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
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
	var newest int64
	// watch both dbs (session sweep writes archives.db, doc edits write live.db)
	// AND their -wal sidecars: in WAL mode writes land in <db>-wal and only reach
	// the main file on checkpoint, so the main mtime freezes for hours between.
	for _, name := range []string{
		"live.db", "live.db-wal",
		"archives.db", "archives.db-wal",
	} {
		if st, err := os.Stat(filepath.Join(base, name)); err == nil {
			if m := st.ModTime().Unix(); m > newest {
				newest = m
			}
		}
	}
	return newest
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

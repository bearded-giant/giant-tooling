// Package search wraps the FTS5 query logic over archives.db + live.db so it
// can be called from both the CLI and the daemon RPC handler.
package search

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Params is a structured search query. All fields except Query are optional.
type Params struct {
	Query       string
	Project     string
	DirType     string
	SourceType  string
	Feature     string
	Latest      bool
	LiveOnly    bool
	ArchiveOnly bool
	Since       string
	Until       string
	Limit       int
	IncludeFull bool
}

// Hit is one search result row, suitable for JSON-RPC and CLI rendering.
type Hit struct {
	Score      float64 `json:"score"`
	Source     string  `json:"source"`
	Project    string  `json:"project"`
	Timestamp  string  `json:"timestamp,omitempty"`
	DirType    string  `json:"dir_type,omitempty"`
	Feature    string  `json:"feature,omitempty"`
	Filepath   string  `json:"filepath"`
	Filename   string  `json:"filename"`
	IsLatest   bool    `json:"is_latest,omitempty"`
	SourceType string  `json:"source_type,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
	Cwd        string  `json:"cwd,omitempty"`
	Topic      string  `json:"topic,omitempty"`
	Snippet    string  `json:"snippet,omitempty"`
}

// Run executes a query across archive + live (or scoped to one) and merges
// results. Caller passes already-open DB handles; either may be nil.
func Run(archive, live *sql.DB, p Params) ([]Hit, error) {
	if p.Limit <= 0 {
		p.Limit = 20
	}
	p.Query = SanitizeFTSQuery(p.Query)
	scope := "both"
	if p.LiveOnly {
		scope = "live"
	}
	if p.ArchiveOnly {
		scope = "archive"
	}
	if p.SourceType == "session" {
		scope = "archive"
	}

	var hits []Hit
	livePaths := map[string]bool{}
	if (scope == "live" || scope == "both") && live != nil {
		h, err := QueryLive(live, p)
		if err == nil {
			for _, x := range h {
				livePaths[x.Filepath] = true
			}
			hits = append(hits, h...)
		} else if scope == "live" {
			return nil, err
		}
	}
	if (scope == "archive" || scope == "both") && archive != nil {
		h, err := QueryArchive(archive, p)
		if err == nil {
			for _, x := range h {
				if livePaths[x.Filepath] {
					continue
				}
				hits = append(hits, x)
			}
		} else if scope == "archive" {
			return nil, err
		}
	}

	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score < hits[j].Score })
	if len(hits) > p.Limit {
		hits = hits[:p.Limit]
	}
	return hits, nil
}

// QueryArchive runs the archives.db search.
func QueryArchive(d *sql.DB, p Params) ([]Hit, error) {
	conds := []string{"documents_fts MATCH ?"}
	args := []any{p.Query}
	if p.Project != "" {
		conds = append(conds, "d.project LIKE ?")
		args = append(args, "%"+p.Project+"%")
	}
	if p.DirType != "" {
		conds = append(conds, "d.dir_type = ?")
		args = append(args, p.DirType)
	}
	if p.SourceType != "" {
		conds = append(conds, "d.source_type = ?")
		args = append(args, p.SourceType)
	}
	if p.Latest {
		conds = append(conds, "d.is_latest = 1")
	}
	if p.Since != "" {
		t, err := ParseSince(p.Since)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "d.timestamp >= ?")
		args = append(args, t.Format("20060102_150405"))
	}
	if p.Until != "" {
		t, err := ParseUntil(p.Until)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "d.timestamp < ?")
		args = append(args, t.Format("20060102_150405"))
	}
	snippet := "''"
	if p.IncludeFull {
		snippet = "snippet(documents_fts, 0, '<', '>', '...', 12)"
	}
	q := fmt.Sprintf(`
        SELECT bm25(documents_fts), d.project, d.timestamp, COALESCE(d.dir_type,''),
               d.filepath, d.filename, d.is_latest, d.source_type,
               COALESCE(d.session_id,''), COALESCE(d.cwd,''), COALESCE(d.topic,''),
               %s
          FROM documents_fts
          JOIN documents d ON d.id = documents_fts.rowid
         WHERE %s
         ORDER BY bm25(documents_fts)
         LIMIT ?`, snippet, strings.Join(conds, " AND "))
	args = append(args, p.Limit)
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		var isLatest int
		if err := rows.Scan(&h.Score, &h.Project, &h.Timestamp, &h.DirType, &h.Filepath,
			&h.Filename, &isLatest, &h.SourceType, &h.SessionID, &h.Cwd, &h.Topic, &h.Snippet); err != nil {
			return nil, err
		}
		h.Source = "archive"
		h.IsLatest = isLatest == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// QueryLive runs the live.db search.
func QueryLive(d *sql.DB, p Params) ([]Hit, error) {
	conds := []string{"live_docs_fts MATCH ?"}
	args := []any{p.Query}
	if p.Project != "" {
		conds = append(conds, "ld.project LIKE ?")
		args = append(args, "%"+p.Project+"%")
	}
	if p.DirType != "" {
		conds = append(conds, "ld.dir_type = ?")
		args = append(args, p.DirType)
	}
	if p.Feature != "" {
		conds = append(conds, "ld.feature = ?")
		args = append(args, p.Feature)
	}
	if p.Since != "" {
		t, err := ParseSince(p.Since)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "ld.mtime >= ?")
		args = append(args, t.Unix())
	}
	if p.Until != "" {
		t, err := ParseUntil(p.Until)
		if err != nil {
			return nil, err
		}
		conds = append(conds, "ld.mtime < ?")
		args = append(args, t.Unix())
	}
	snippet := "''"
	if p.IncludeFull {
		snippet = "snippet(live_docs_fts, 4, '<', '>', '...', 12)"
	}
	q := fmt.Sprintf(`
        SELECT bm25(live_docs_fts), ld.project, COALESCE(ld.dir_type,''),
               COALESCE(ld.feature,''), ld.path, COALESCE(ld.session_id,''),
               %s
          FROM live_docs_fts
          JOIN live_docs ld ON ld.rowid = live_docs_fts.rowid
         WHERE %s
         ORDER BY bm25(live_docs_fts)
         LIMIT ?`, snippet, strings.Join(conds, " AND "))
	args = append(args, p.Limit)
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Score, &h.Project, &h.DirType, &h.Feature, &h.Filepath, &h.SessionID, &h.Snippet); err != nil {
			return nil, err
		}
		h.Source = "live"
		h.SourceType = "live"
		out = append(out, h)
	}
	return out, rows.Err()
}

func parseSinceUntil(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if dateOnlyRE.MatchString(s) {
		if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
			return t, nil
		}
	}
	dur, err := parseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad time spec %q: must be duration, date, or RFC3339", s)
	}
	return time.Now().Add(-dur), nil
}

var dateOnlyRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ParseSince resolves a since-bound: a duration ("7d","4h"), an RFC3339
// timestamp, or a bare local date "2006-01-02" (start of that day).
func ParseSince(s string) (time.Time, error) { return parseSinceUntil(s) }

// ParseUntil resolves an until-bound. A bare local date means "through the end
// of that day": it resolves to 00:00 local of the NEXT day so callers using
// `col < until` include every row on the named day.
func ParseUntil(s string) (time.Time, error) {
	t, err := parseSinceUntil(s)
	if err != nil {
		return time.Time{}, err
	}
	if dateOnlyRE.MatchString(strings.TrimSpace(s)) {
		t = t.AddDate(0, 0, 1)
	}
	return t, nil
}

// parseDuration accepts durations Go's time.ParseDuration handles plus "d".
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		head := strings.TrimSuffix(s, "d")
		var n int
		if _, err := fmt.Sscanf(head, "%d", &n); err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// ftsOperatorRE detects whether the user is already speaking FTS5 query
// language (operators, column qualifiers, prefix matches, phrases, parens,
// negation). When any of these appear we pass the query through untouched so
// power users keep full control.
var ftsOperatorRE = regexp.MustCompile(`(?:^|\s)(?:AND|OR|NOT|NEAR)(?:\s|$)|[()"*^:]`)

// SanitizeFTSQuery makes plain-text queries safe for FTS5 MATCH. FTS5 treats
// `-` as NOT and bare words like `and` as column references, so an input like
// `hub-and-spoke` parses as `hub NOT and NOT spoke` and errors out. We split
// on whitespace and wrap each token as a quoted phrase, which forces FTS5 to
// tokenize internally and drops punctuation. Queries that already contain
// FTS5 operators are returned as-is so advanced users can still write
// `(hub OR spoke) NOT legacy` and so on.
func SanitizeFTSQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return q
	}
	if ftsOperatorRE.MatchString(q) {
		return q
	}
	fields := strings.Fields(q)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		out = append(out, `"`+f+`"`)
	}
	return strings.Join(out, " ")
}

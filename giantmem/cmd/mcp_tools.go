package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bryangrimes/gm/internal/db"
	"github.com/mark3labs/mcp-go/mcp"
)

// ----- list_sessions ---------------------------------------------------------

type listSessionsArgs struct {
	Project string  `json:"project"`
	Limit   float64 `json:"limit"`
}

type sessionRow struct {
	SessionID string `json:"session_id"`
	Project   string `json:"project"`
	Cwd       string `json:"cwd,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Timestamp string `json:"timestamp"`
	Filepath  string `json:"jsonl_path"`
}

func listSessionsHandler(ctx context.Context, _ mcp.CallToolRequest, args listSessionsArgs) (*mcp.CallToolResult, error) {
	limit := int(args.Limit)
	if limit <= 0 {
		limit = 20
	}
	d, err := db.Open(archiveDBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer d.Close()

	q := `SELECT COALESCE(session_id,''), project, COALESCE(cwd,''),
                 COALESCE(topic,''), timestamp, filepath
            FROM documents
           WHERE source_type = 'session'`
	var qargs []any
	if args.Project != "" {
		q += " AND project LIKE ?"
		qargs = append(qargs, "%"+args.Project+"%")
	}
	q += " ORDER BY timestamp DESC LIMIT ?"
	qargs = append(qargs, limit)

	rows, err := d.Query(q, qargs...)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer rows.Close()
	var out []sessionRow
	for rows.Next() {
		var r sessionRow
		if err := rows.Scan(&r.SessionID, &r.Project, &r.Cwd, &r.Topic, &r.Timestamp, &r.Filepath); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out = append(out, r)
	}
	return jsonResult(map[string]any{"sessions": out, "total": len(out)})
}

// ----- get_session_summary ---------------------------------------------------

type getSessionSummaryArgs struct {
	IDPrefix string `json:"id_prefix"`
}

type sessionSummary struct {
	SessionID  string `json:"session_id"`
	Project    string `json:"project"`
	Cwd        string `json:"cwd,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Timestamp  string `json:"timestamp"`
	Filepath   string `json:"jsonl_path"`
	IndexedAt  string `json:"indexed_at,omitempty"`
	FileSize   int64  `json:"file_size_bytes"`
}

func getSessionSummaryHandler(ctx context.Context, _ mcp.CallToolRequest, args getSessionSummaryArgs) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(args.IDPrefix) == "" {
		return mcp.NewToolResultError("id_prefix is required"), nil
	}
	d, err := db.Open(archiveDBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer d.Close()

	q := `SELECT COALESCE(session_id,''), project, COALESCE(cwd,''),
                 COALESCE(topic,''), timestamp, filepath, indexed_at
            FROM documents
           WHERE source_type = 'session'
             AND (session_id LIKE ? OR filepath LIKE ?)
           ORDER BY timestamp DESC LIMIT 5`
	rows, err := d.Query(q, args.IDPrefix+"%", "%"+args.IDPrefix+"%")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer rows.Close()

	var matches []sessionSummary
	for rows.Next() {
		var r sessionSummary
		if err := rows.Scan(&r.SessionID, &r.Project, &r.Cwd, &r.Topic, &r.Timestamp, &r.Filepath, &r.IndexedAt); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		matches = append(matches, r)
	}
	if len(matches) == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("no session matching %q", args.IDPrefix)), nil
	}
	if len(matches) > 1 {
		// return all matches with note
		return jsonResult(map[string]any{
			"matches":   matches,
			"ambiguous": true,
			"note":      "prefix matches multiple sessions; use a longer prefix",
		})
	}
	r := matches[0]
	if st, err := os.Stat(r.Filepath); err == nil {
		r.FileSize = st.Size()
	}
	return jsonResult(r)
}

// ----- recent_writes ---------------------------------------------------------

type recentWritesArgs struct {
	Project string  `json:"project"`
	Since   string  `json:"since"`
	Limit   float64 `json:"limit"`
}

type liveDocRow struct {
	Path        string `json:"path"`
	Project     string `json:"project"`
	Feature     string `json:"feature,omitempty"`
	DirType     string `json:"dir_type,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Mtime       int64  `json:"mtime"`
	IngestedAt  string `json:"ingested_at"`
	GitSHA      string `json:"git_sha,omitempty"`
}

func recentWritesHandler(ctx context.Context, _ mcp.CallToolRequest, args recentWritesArgs) (*mcp.CallToolResult, error) {
	limit := int(args.Limit)
	if limit <= 0 {
		limit = 30
	}
	since := args.Since
	if since == "" {
		since = "24h"
	}
	dur, err := parseDuration(since)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("bad 'since' value %q", since)), nil
	}
	cutoff := time.Now().Add(-dur).Unix()

	d, err := db.Open(liveDBPath())
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("open live.db: %v", err)), nil
	}
	defer d.Close()

	q := `SELECT path, project, COALESCE(feature,''), COALESCE(dir_type,''),
                 COALESCE(session_id,''), mtime, ingested_at, COALESCE(git_sha,'')
            FROM live_docs
           WHERE mtime >= ?`
	qargs := []any{cutoff}
	if args.Project != "" {
		q += " AND project LIKE ?"
		qargs = append(qargs, "%"+args.Project+"%")
	}
	q += " ORDER BY mtime DESC LIMIT ?"
	qargs = append(qargs, limit)

	rows, err := d.Query(q, qargs...)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer rows.Close()

	var out []liveDocRow
	for rows.Next() {
		var r liveDocRow
		if err := rows.Scan(&r.Path, &r.Project, &r.Feature, &r.DirType, &r.SessionID, &r.Mtime, &r.IngestedAt, &r.GitSHA); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out = append(out, r)
	}
	return jsonResult(map[string]any{
		"writes": out,
		"total":  len(out),
		"since":  since,
	})
}

// ----- feature_status --------------------------------------------------------

type featureStatusArgs struct {
	Project string `json:"project"`
}

type projectFeatures struct {
	Project      string                   `json:"project"`
	WorktreePath string                   `json:"worktree_path"`
	Features     []map[string]any         `json:"features"`
}

func featureStatusHandler(ctx context.Context, _ mcp.CallToolRequest, args featureStatusArgs) (*mcp.CallToolResult, error) {
	d, err := db.Open(liveDBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer d.Close()

	q := `SELECT DISTINCT project, worktree_path FROM live_docs WHERE worktree_path IS NOT NULL`
	var qargs []any
	if args.Project != "" {
		q += " AND project LIKE ?"
		qargs = append(qargs, "%"+args.Project+"%")
	}
	rows, err := d.Query(q, qargs...)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer rows.Close()

	var out []projectFeatures
	seen := map[string]bool{}
	for rows.Next() {
		var proj, wt string
		if err := rows.Scan(&proj, &wt); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if wt == "" || seen[wt] {
			continue
		}
		seen[wt] = true
		feats := readFeaturesJSON(filepath.Join(wt, ".giantmem", "features", "features.json"))
		out = append(out, projectFeatures{
			Project:      proj,
			WorktreePath: wt,
			Features:     feats,
		})
	}
	return jsonResult(map[string]any{"projects": out})
}

func readFeaturesJSON(path string) []map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// dict shape: {features: {name: {...}}}
	var asDict struct {
		Features map[string]map[string]any `json:"features"`
	}
	if err := json.Unmarshal(raw, &asDict); err == nil && asDict.Features != nil {
		var out []map[string]any
		for name, f := range asDict.Features {
			f["name"] = name
			out = append(out, f)
		}
		return out
	}
	// list shape: {features: [{...}, ...]}
	var asList struct {
		Features []map[string]any `json:"features"`
	}
	if err := json.Unmarshal(raw, &asList); err == nil {
		return asList.Features
	}
	return nil
}

// ----- workspace_tree --------------------------------------------------------

type workspaceTreeArgs struct {
	Project      string `json:"project"`
	WorktreePath string `json:"worktree_path"`
}

func workspaceTreeHandler(ctx context.Context, _ mcp.CallToolRequest, args workspaceTreeArgs) (*mcp.CallToolResult, error) {
	if args.WorktreePath != "" {
		return jsonResult(scanWorkspaceTreeOnDisk(args.WorktreePath))
	}
	d, err := db.Open(liveDBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer d.Close()

	q := `SELECT project, COALESCE(feature,'_none_'), COALESCE(dir_type,'root'), COUNT(*)
            FROM live_docs`
	var qargs []any
	if args.Project != "" {
		q += " WHERE project LIKE ?"
		qargs = append(qargs, "%"+args.Project+"%")
	}
	q += " GROUP BY project, feature, dir_type ORDER BY project, feature, dir_type"
	rows, err := d.Query(q, qargs...)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer rows.Close()

	type bucket struct {
		Project string `json:"project"`
		Feature string `json:"feature,omitempty"`
		DirType string `json:"dir_type"`
		Count   int    `json:"count"`
	}
	var out []bucket
	for rows.Next() {
		var b bucket
		if err := rows.Scan(&b.Project, &b.Feature, &b.DirType, &b.Count); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if b.Feature == "_none_" {
			b.Feature = ""
		}
		out = append(out, b)
	}
	return jsonResult(map[string]any{"buckets": out})
}

func scanWorkspaceTreeOnDisk(worktree string) map[string]any {
	gm := filepath.Join(worktree, ".giantmem")
	if _, err := os.Stat(gm); err != nil {
		return map[string]any{"error": "no .giantmem at " + worktree}
	}
	counts := map[string]int{}
	filepath.WalkDir(gm, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(gm, p)
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		if len(parts) == 0 {
			return nil
		}
		counts[parts[0]]++
		return nil
	})
	return map[string]any{
		"worktree":    worktree,
		"giantmem":    gm,
		"file_counts": counts,
	}
}

// ----- helpers ---------------------------------------------------------------

func jsonResult(v any) (*mcp.CallToolResult, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}

var durationRe = regexp.MustCompile(`^(\d+)([smhdw])$`)

func parseDuration(s string) (time.Duration, error) {
	m := durationRe.FindStringSubmatch(s)
	if m == nil {
		// fall back to time.ParseDuration for things like "2h30m"
		return time.ParseDuration(s)
	}
	n, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "s":
		return time.Duration(n) * time.Second, nil
	case "m":
		return time.Duration(n) * time.Minute, nil
	case "h":
		return time.Duration(n) * time.Hour, nil
	case "d":
		return time.Duration(n) * 24 * time.Hour, nil
	case "w":
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("bad duration: %s", s)
}

// keep sql import live for future tools
var _ = (*sql.DB)(nil)

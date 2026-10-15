// Package hub answers per-session questions across archives.db and the
// session bundles: tool calls, links, archived files, subagents, and days.
// The CLI, the MCP server, and the GUI all read through it.
package hub

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
)

type ToolUse struct {
	SessionID  string `json:"session_id"`
	AgentID    string `json:"agent_id,omitempty"`
	Project    string `json:"project"`
	Seq        int    `json:"seq"`
	TS         string `json:"ts,omitempty"`
	Tool       string `json:"tool"`
	Summary    string `json:"summary"`
	Input      string `json:"input,omitempty"`
	ResultHead string `json:"result_head,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
}

type Link struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Label  string `json:"label,omitempty"`
	TS     string `json:"ts,omitempty"`
}

type BundleFile struct {
	Path    string `json:"path"`
	AbsPath string `json:"abs_path"`
	Size    int64  `json:"size"`
	Kind    string `json:"kind"`
}

type Bundle struct {
	SessionID   string       `json:"session_id"`
	Cwd         string       `json:"cwd,omitempty"`
	Branch      string       `json:"branch,omitempty"`
	FirstPrompt string       `json:"first_prompt,omitempty"`
	Updated     string       `json:"updated,omitempty"`
	Resume      string       `json:"resume,omitempty"`
	Files       []BundleFile `json:"files"`
	Dir         string       `json:"dir"`
}

type Agent struct {
	AgentID     string `json:"agent_id"`
	AgentType   string `json:"agent_type,omitempty"`
	Description string `json:"description,omitempty"`
	Calls       int    `json:"calls"`
	Errors      int    `json:"errors"`
	FirstTS     string `json:"first_ts,omitempty"`
}

type DaySession struct {
	SessionID   string `json:"session_id"`
	Project     string `json:"project"`
	Topic       string `json:"topic,omitempty"`
	Transcript  string `json:"transcript"`
	FirstTS     string `json:"first_ts"`
	LastTS      string `json:"last_ts"`
	Calls       int    `json:"calls"`
	Errors      int    `json:"errors"`
	Links       int    `json:"links"`
	FirstPrompt string `json:"first_prompt,omitempty"`
	Files       int    `json:"files"`
}

// ToolQuery scopes QueryToolUses. Agent: "" all, "main" main thread only, else
// one subagent id. Since/Until compare against the RFC3339 UTC ts column.
type ToolQuery struct {
	SessionID string
	Match     string
	Tool      string
	Project   string
	Agent     string
	Errors    bool
	Since     string
	Until     string
	Limit     int
	WithIO    bool
}

func QueryToolUses(d *sql.DB, q ToolQuery) ([]ToolUse, error) {
	from := "tool_uses t"
	var conds []string
	var args []any
	if q.Match != "" {
		from = "tool_uses_fts f JOIN tool_uses t ON t.id = f.rowid"
		conds = append(conds, "tool_uses_fts MATCH ?")
		args = append(args, search.SanitizeFTSQuery(q.Match))
	}
	add := func(cond string, v any) {
		conds = append(conds, cond)
		args = append(args, v)
	}
	if q.SessionID != "" {
		add("t.session_id = ?", q.SessionID)
	}
	if q.Tool != "" {
		add("t.tool LIKE ?", q.Tool)
	}
	if q.Project != "" {
		add("t.project LIKE ?", "%"+q.Project+"%")
	}
	switch q.Agent {
	case "":
	case "main":
		conds = append(conds, "t.agent_id = ''")
	default:
		add("t.agent_id = ?", q.Agent)
	}
	if q.Errors {
		conds = append(conds, "t.is_error = 1")
	}
	if q.Since != "" {
		add("t.ts >= ?", q.Since)
	}
	if q.Until != "" {
		add("t.ts < ?", q.Until)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	order := "t.ts DESC, t.seq DESC"
	switch {
	case q.SessionID != "":
		order = "t.agent_id, t.seq"
	case q.Match != "":
		order = "bm25(tool_uses_fts), t.ts DESC"
	}
	io := "'', ''"
	if q.WithIO {
		io = "t.input, COALESCE(t.result_head,'')"
	}
	sqlq := fmt.Sprintf(`SELECT t.session_id, t.agent_id, t.project, t.seq, COALESCE(t.ts,''),
                                t.tool, t.summary, t.is_error, %s
                           FROM %s %s ORDER BY %s`, io, from, where, order)
	if q.Limit > 0 {
		sqlq += " LIMIT ?"
		args = append(args, q.Limit)
	}
	rows, err := d.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolUse
	for rows.Next() {
		var r ToolUse
		var isErr int
		if err := rows.Scan(&r.SessionID, &r.AgentID, &r.Project, &r.Seq, &r.TS, &r.Tool, &r.Summary, &isErr, &r.Input, &r.ResultHead); err != nil {
			return nil, err
		}
		r.IsError = isErr == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

func QueryLinks(d *sql.DB, sessionID string) ([]Link, error) {
	rows, err := d.Query(`SELECT kind, target, COALESCE(label,''), COALESCE(ts,'')
                            FROM session_links WHERE session_id = ? ORDER BY ts, kind`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Link
	for rows.Next() {
		var r Link
		if err := rows.Scan(&r.Kind, &r.Target, &r.Label, &r.TS); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// bundleDir globs for the session's bundle; the slug is not stored with the session row.
func bundleDir(archiveBase, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(archiveBase, "bundles", "*", sessionID, "bundle.json"))
	if len(matches) == 0 {
		return ""
	}
	return filepath.Dir(matches[0])
}

func LoadBundle(archiveBase, sessionID string) (*Bundle, error) {
	dir := bundleDir(archiveBase, sessionID)
	if dir == "" {
		return nil, fmt.Errorf("no bundle for session %s", sessionID)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "bundle.json"))
	if err != nil {
		return nil, err
	}
	var b Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	b.Dir = dir
	for i := range b.Files {
		b.Files[i].AbsPath = filepath.Join(dir, b.Files[i].Path)
	}
	return &b, nil
}

// Agents summarizes a session's subagents from tool_uses, labelled from the
// harness meta.json beside each transcript (or its bundle copy).
func Agents(d *sql.DB, archiveBase, sessionID string) ([]Agent, error) {
	rows, err := d.Query(`SELECT agent_id, COUNT(*), SUM(is_error), MIN(COALESCE(ts,''))
                            FROM tool_uses WHERE session_id = ? AND agent_id != ''
                           GROUP BY agent_id ORDER BY MIN(COALESCE(ts,'')), agent_id`, sessionID)
	if err != nil {
		return nil, err
	}
	var out []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.AgentID, &a.Calls, &a.Errors, &a.FirstTS); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, a)
	}
	rows.Close()
	paths := map[string]string{}
	if prow, err := d.Query(`SELECT filepath FROM documents WHERE source_type = 'subagent' AND session_id = ?`, sessionID); err == nil {
		for prow.Next() {
			var p string
			if prow.Scan(&p) == nil {
				paths[strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "agent-"), ".jsonl")] = p
			}
		}
		prow.Close()
	}
	bdir := bundleDir(archiveBase, sessionID)
	for i := range out {
		var metas []string
		if p, ok := paths[out[i].AgentID]; ok {
			metas = append(metas, strings.TrimSuffix(p, ".jsonl")+".meta.json")
		}
		if bdir != "" {
			metas = append(metas, filepath.Join(bdir, "claude", "subagents", "agent-"+out[i].AgentID+".meta.json"))
		}
		for _, m := range metas {
			raw, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			var meta struct {
				AgentType   string `json:"agentType"`
				Description string `json:"description"`
			}
			if json.Unmarshal(raw, &meta) == nil {
				out[i].AgentType, out[i].Description = meta.AgentType, meta.Description
				break
			}
		}
	}
	return out, nil
}

// Day lists sessions with tool activity in [since, until), RFC3339 UTC bounds.
func Day(d *sql.DB, archiveBase, since, until string) ([]DaySession, error) {
	rows, err := d.Query(`
        SELECT t.session_id, MIN(t.ts), MAX(t.ts), COUNT(*), SUM(t.is_error),
               COALESCE(doc.project, MAX(t.project)), COALESCE(doc.topic,''), COALESCE(doc.filepath,''),
               (SELECT COUNT(*) FROM session_links l WHERE l.session_id = t.session_id)
          FROM tool_uses t
          LEFT JOIN documents doc ON doc.session_id = t.session_id AND doc.source_type = 'session'
         WHERE t.ts >= ? AND t.ts < ?
         GROUP BY t.session_id
         ORDER BY MIN(t.ts)`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DaySession
	for rows.Next() {
		var s DaySession
		if err := rows.Scan(&s.SessionID, &s.FirstTS, &s.LastTS, &s.Calls, &s.Errors, &s.Project, &s.Topic, &s.Transcript, &s.Links); err != nil {
			return nil, err
		}
		if b, err := LoadBundle(archiveBase, s.SessionID); err == nil {
			s.FirstPrompt = b.FirstPrompt
			s.Files = len(b.Files)
		}
		s.Transcript = TranscriptPath(archiveBase, s.Transcript, s.SessionID)
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].FirstTS < out[j].FirstTS })
	return out, rows.Err()
}

// TranscriptPath returns jsonlPath when it still exists, else the bundle's
// snapshot: Claude Code prunes old transcripts, the bundle keeps a copy.
func TranscriptPath(archiveBase, jsonlPath, sessionID string) string {
	if jsonlPath != "" {
		if _, err := os.Stat(jsonlPath); err == nil {
			return jsonlPath
		}
	}
	if sessionID == "" && jsonlPath != "" {
		sessionID = strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")
	}
	if dir := bundleDir(archiveBase, sessionID); dir != "" {
		snap := filepath.Join(dir, "claude", "transcript.jsonl")
		if _, err := os.Stat(snap); err == nil {
			return snap
		}
	}
	return jsonlPath
}

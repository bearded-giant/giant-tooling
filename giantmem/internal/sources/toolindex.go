package sources

import (
	"database/sql"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/sessions"
)

// IndexToolCalls replaces one transcript's tool_uses rows and merges its links.
// The main thread and each subagent own their rows by (session_id, agent_id).
func IndexToolCalls(db *sql.DB, d Doc, vaultRoot string) error {
	agentID := d.Metadata["agent_id"]
	calls, links, err := sessions.ExtractToolCalls(d.Filepath, vaultRoot)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM tool_uses WHERE session_id = ? AND agent_id = ?`, d.SessionID, agentID); err != nil {
		return err
	}
	for _, c := range calls {
		isErr := 0
		if c.IsError {
			isErr = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO tool_uses (session_id, agent_id, project, seq, ts, tool, summary, input, is_error, result_head)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			d.SessionID, agentID, d.Project, c.Seq, nilIfEmpty(c.TS), c.Tool, c.Summary, c.Input, isErr, nilIfEmpty(c.ResultHead),
		); err != nil {
			return err
		}
	}
	for _, l := range links {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO session_links (session_id, kind, target, label, ts) VALUES (?, ?, ?, ?, ?)`,
			d.SessionID, l.Kind, l.Target, nilIfEmpty(l.Label), nilIfEmpty(l.TS),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

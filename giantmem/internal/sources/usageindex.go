package sources

import (
	"database/sql"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/sessions"
)

// IndexUsage replaces one transcript's usage rows. A subagent transcript has no
// credential_org line, so its rows take the parent thread's org nearest in time.
func IndexUsage(db *sql.DB, d Doc) error {
	agentID := d.Metadata["agent_id"]
	rows, err := sessions.ExtractUsage(d.Filepath)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM usage WHERE session_id = ? AND agent_id = ?`, d.SessionID, agentID); err != nil {
		return err
	}
	for _, r := range rows {
		org := r.Org
		if org == "" && agentID != "" {
			org = parentOrg(tx, d.SessionID, r.TS)
		}
		// a forked session copies its parent's lines, so the message id dedupes across transcripts
		if _, err := tx.Exec(
			`INSERT INTO usage (message_id, session_id, agent_id, ts, day, org, model, input, cache_5m, cache_1h, cache_read, output)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
             ON CONFLICT(message_id) DO UPDATE SET
                 session_id = excluded.session_id, agent_id = excluded.agent_id, ts = excluded.ts, day = excluded.day,
                 org = excluded.org, model = excluded.model, input = excluded.input, cache_5m = excluded.cache_5m,
                 cache_1h = excluded.cache_1h, cache_read = excluded.cache_read, output = excluded.output`,
			r.MessageID, d.SessionID, agentID, r.TS, localDay(r.TS), org, r.Model,
			r.Input, r.Cache5m, r.Cache1h, r.CacheRead, r.Output,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func parentOrg(tx *sql.Tx, sessionID, ts string) string {
	var org string
	_ = tx.QueryRow(
		`SELECT org FROM usage WHERE session_id = ? AND agent_id = '' AND org <> ''
         ORDER BY abs(julianday(ts) - julianday(?)) LIMIT 1`, sessionID, ts,
	).Scan(&org)
	return org
}

func localDay(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		if len(ts) >= 10 {
			return ts[:10]
		}
		return ts
	}
	return t.In(time.Local).Format("2006-01-02")
}

// Package sessioninfo wraps the archive session queries so they can be called
// from both the daemon handler and the CLI direct-open path.
package sessioninfo

import "database/sql"

// Row is one session document row.
type Row struct {
	ID        string `json:"id"`
	JSONLPath string `json:"jsonl_path"`
	Project   string `json:"project"`
	Cwd       string `json:"cwd,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Timestamp string `json:"timestamp"`
}

// List returns recent sessions ordered by timestamp DESC.
// project is a LIKE filter (empty = all projects). limit <= 0 defaults to 20.
func List(archive *sql.DB, project string, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT COALESCE(session_id,''), filepath, project, COALESCE(cwd,''),
               COALESCE(topic,''), timestamp
          FROM documents
         WHERE source_type = 'session'`
	var args []any
	if project != "" {
		q += ` AND project LIKE ?`
		args = append(args, "%"+project+"%")
	}
	q += ` ORDER BY timestamp DESC LIMIT ?`
	args = append(args, limit)

	rows, err := archive.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.JSONLPath, &r.Project, &r.Cwd, &r.Topic, &r.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Find runs an FTS5 search over session transcripts. limit <= 0 defaults to 20.
func Find(archive *sql.DB, query string, limit int) ([]Row, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT bm25(documents_fts), COALESCE(d.session_id,''), d.filepath, d.project,
               COALESCE(d.cwd,''), COALESCE(d.topic,''), d.timestamp
          FROM documents_fts
          JOIN documents d ON d.id = documents_fts.rowid
         WHERE documents_fts MATCH ?
           AND d.source_type = 'session'
         ORDER BY bm25(documents_fts)
         LIMIT ?`
	rows, err := archive.Query(q, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var score float64
		if err := rows.Scan(&score, &r.ID, &r.JSONLPath, &r.Project, &r.Cwd, &r.Topic, &r.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

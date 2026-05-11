// Package timelineinfo wraps the archive timeline query so it can be called
// from both the daemon handler and the CLI direct-open path.
package timelineinfo

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Row is one document row from the timeline query.
type Row struct {
	Project    string `json:"project"`
	SourceType string `json:"source_type"`
	Timestamp  string `json:"timestamp"`
}

// Query returns rows in the given window. days controls how many days back to
// look; project and source are optional LIKE / equality filters.
func Query(archive *sql.DB, days int, project, source string) ([]Row, error) {
	if days <= 0 {
		days = 14
	}
	end := time.Now()
	start := end.AddDate(0, 0, -days+1)
	since := start.Truncate(24 * time.Hour).Format("20060102_150405")

	conds := []string{`timestamp >= ?`}
	qargs := []any{since}
	if project != "" {
		conds = append(conds, "project LIKE ?")
		qargs = append(qargs, "%"+project+"%")
	}
	if source != "" {
		conds = append(conds, "source_type = ?")
		qargs = append(qargs, source)
	}
	q := fmt.Sprintf(
		`SELECT project, source_type, timestamp FROM documents WHERE %s`,
		strings.Join(conds, " AND "),
	)
	rows, err := archive.Query(q, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.Project, &r.SourceType, &r.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

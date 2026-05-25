package artifacts

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// LogAccess writes one row to the artifact_access table. Caller picks
// the (query, rank) shape per design.md Decision 2:
//   - direct show: query=NULL, rank=NULL
//   - list/find result row: query=filter-summary, rank=1-based position
//   - reindex (auto): MUST NOT log (don't pollute access_log)
//
// Pass an empty query string + rank=0 for the direct-show case.
func LogAccess(db *sql.DB, artifactID, query string, rank int) error {
	if db == nil || artifactID == "" {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var (
		qArg  any
		rArg  any
	)
	if query == "" {
		qArg = nil
	} else {
		qArg = query
	}
	if rank <= 0 {
		rArg = nil
	} else {
		rArg = rank
	}
	_, err := db.Exec(
		`INSERT INTO artifact_access(artifact_id, query, rank, accessed_at) VALUES (?, ?, ?, ?)`,
		artifactID, qArg, rArg, now,
	)
	return err
}

// LogAccesses inserts many rows in one tx — used when a list operation
// returns multiple rows. ranks slice MUST be 1-based and the same
// length as ids.
func LogAccesses(db *sql.DB, ids []string, ranks []int, query string) error {
	if db == nil || len(ids) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	stmt, err := tx.Prepare(
		`INSERT INTO artifact_access(artifact_id, query, rank, accessed_at) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, id := range ids {
		if id == "" {
			continue
		}
		var qArg any
		if query != "" {
			qArg = query
		}
		var rArg any
		if i < len(ranks) && ranks[i] > 0 {
			rArg = ranks[i]
		}
		if _, err := stmt.Exec(id, qArg, rArg, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AccessCount returns the number of access_log rows for artifactID
// since `since`. since==zero returns lifetime count.
func AccessCount(db *sql.DB, artifactID string, since time.Time) (int, error) {
	if db == nil {
		return 0, nil
	}
	var (
		n   int
		err error
	)
	if since.IsZero() {
		err = db.QueryRow(
			`SELECT COUNT(*) FROM artifact_access WHERE artifact_id = ?`,
			artifactID,
		).Scan(&n)
	} else {
		err = db.QueryRow(
			`SELECT COUNT(*) FROM artifact_access WHERE artifact_id = ? AND accessed_at >= ?`,
			artifactID, since.UTC().Format(time.RFC3339),
		).Scan(&n)
	}
	return n, err
}

// AccessCounts returns a map of artifact_id -> count for a window. One
// query rather than per-id round-trips.
func AccessCounts(db *sql.DB, since time.Time) (map[string]int, error) {
	if db == nil {
		return map[string]int{}, nil
	}
	out := map[string]int{}
	var (
		rows *sql.Rows
		err  error
	)
	if since.IsZero() {
		rows, err = db.Query(`SELECT artifact_id, COUNT(*) FROM artifact_access GROUP BY artifact_id`)
	} else {
		rows, err = db.Query(
			`SELECT artifact_id, COUNT(*) FROM artifact_access WHERE accessed_at >= ? GROUP BY artifact_id`,
			since.UTC().Format(time.RFC3339),
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// TopAccessed returns the top-N artifact_ids by count in the window.
func TopAccessed(db *sql.DB, since time.Time, limit int) ([]AccessSummary, error) {
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}
	var (
		rows *sql.Rows
		err  error
	)
	if since.IsZero() {
		rows, err = db.Query(
			`SELECT artifact_id, COUNT(*) AS n FROM artifact_access
             GROUP BY artifact_id ORDER BY n DESC LIMIT ?`, limit,
		)
	} else {
		rows, err = db.Query(
			`SELECT artifact_id, COUNT(*) AS n FROM artifact_access
             WHERE accessed_at >= ?
             GROUP BY artifact_id ORDER BY n DESC LIMIT ?`,
			since.UTC().Format(time.RFC3339), limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessSummary{}
	for rows.Next() {
		var s AccessSummary
		if err := rows.Scan(&s.ArtifactID, &s.Count); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AccessSummary is the row shape for TopAccessed.
type AccessSummary struct {
	ArtifactID string `json:"id"`
	Count      int    `json:"access_count"`
}

// PruneAccessLog deletes rows older than `before` and returns the number
// of rows removed.
func PruneAccessLog(db *sql.DB, before time.Time) (int64, error) {
	if db == nil {
		return 0, nil
	}
	res, err := db.Exec(
		`DELETE FROM artifact_access WHERE accessed_at < ?`,
		before.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// AccessFilterSummary turns CLI filter flags into a stable string used as
// the access_log.query value, so callers can later reason about what
// surfaced an artifact.
//
// Pass the artifact subcommand's filter slice/string fields; empty values
// are dropped. Returns "" when no filters were active.
func AccessFilterSummary(pairs map[string]string) string {
	if len(pairs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(pairs))
	for k, v := range pairs {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

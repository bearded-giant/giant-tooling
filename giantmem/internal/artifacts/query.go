package artifacts

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ListFilter selects rows from the artifacts projection table. Empty fields are
// ignored. Multi-value fields (Type, Status, Lifecycle) match any of the given
// values.
type ListFilter struct {
	Type      []string
	Status    []string
	Lifecycle []string
	Scope     string
	Repo      string
	Branch    string
	Feature   string
	Domain    string
	Since     time.Time `json:"-"`
	Until     time.Time `json:"-"`
}

// TableHasRows reports whether the artifacts projection has been populated.
// Callers use this to prefer the SQL read path and fall back to a filesystem
// Scan when the table is empty (first run, before any reconcile).
func TableHasRows(live *sql.DB) bool {
	if live == nil {
		return false
	}
	var n int
	if err := live.QueryRow(`SELECT COUNT(*) FROM artifacts`).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// ListArtifacts reads the projection table with the given filters, joining the
// 30-day access count and embedding presence. sortBy is a whitelisted column
// (defaults to a stable repo/type/feature/id ordering). limit<=0 means no limit.
func ListArtifacts(live *sql.DB, f ListFilter, sortBy string, limit int) ([]Artifact, error) {
	if live == nil {
		return nil, nil
	}

	var where []string
	var args []any
	addIn := func(col string, vals []string) {
		vals = expandCSV(vals)
		if len(vals) == 0 {
			return
		}
		ph := make([]string, len(vals))
		for i, v := range vals {
			ph[i] = "?"
			args = append(args, v)
		}
		where = append(where, fmt.Sprintf("a.%s IN (%s)", col, strings.Join(ph, ",")))
	}
	addEq := func(col, val string) {
		if val == "" {
			return
		}
		where = append(where, fmt.Sprintf("a.%s = ?", col))
		args = append(args, val)
	}

	addIn("type", f.Type)
	addIn("status", f.Status)
	addIn("lifecycle", f.Lifecycle)
	addEq("scope", f.Scope)
	addEq("branch", f.Branch)
	addEq("feature", f.Feature)
	addEq("domain", f.Domain)
	if f.Repo != "" && f.Repo != "all" && f.Repo != "current" {
		addEq("repo", f.Repo)
	}
	// artifacts.updated is date-only (YYYY-MM-DD); compare lexically. Until is
	// pre-resolved to next-day 00:00 by search.ParseUntil, so `< untilDate`
	// includes the whole named day.
	if !f.Since.IsZero() {
		where = append(where, "a.updated >= ?")
		args = append(args, f.Since.Format("2006-01-02"))
	}
	if !f.Until.IsZero() {
		where = append(where, "a.updated < ?")
		args = append(args, f.Until.Format("2006-01-02"))
	}

	q := `SELECT a.id, a.type, a.feature, a.domain, a.name, a.status, a.lifecycle,
                 a.scope, a.repo, a.branch, a.path, a.worktree, a.size, a.created,
                 a.updated, a.has_front,
                 COALESCE(ac.cnt, 0) AS access_count,
                 CASE WHEN em.artifact_id IS NULL THEN 0 ELSE 1 END AS has_vec
          FROM artifacts a
          LEFT JOIN (
              SELECT artifact_id, COUNT(*) AS cnt FROM artifact_access
              WHERE accessed_at > datetime('now','-30 day')
              GROUP BY artifact_id
          ) ac ON ac.artifact_id = a.id
          LEFT JOIN artifact_embedding_meta em ON em.artifact_id = a.id`
	if len(where) > 0 {
		q += "\n          WHERE " + strings.Join(where, " AND ")
	}
	q += "\n          ORDER BY " + orderBy(sortBy)
	if limit > 0 {
		q += "\n          LIMIT ?"
		args = append(args, limit)
	}

	rows, err := live.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Artifact
	for rows.Next() {
		var a Artifact
		var hasFront, hasVec int
		if err := rows.Scan(&a.ID, &a.Type, &a.Feature, &a.Domain, &a.Name, &a.Status,
			&a.Lifecycle, &a.Scope, &a.Repo, &a.Branch, &a.Path, &a.Worktree, &a.Size,
			&a.Created, &a.Updated, &hasFront, &a.AccessCount, &hasVec); err != nil {
			return nil, err
		}
		a.HasFront = hasFront != 0
		a.HasVec = hasVec != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

// FacetCounts returns per-value row counts for the type, lifecycle, status,
// feature, and repo columns in one pass each — used to render filter
// sidebars. Feature and repo are excluded from the standard 3-facet
// inputs because their cardinality varies wildly but they make great
// secondary filters.
func FacetCounts(live *sql.DB) (byType, byLifecycle, byStatus map[string]int, err error) {
	if live == nil {
		return map[string]int{}, map[string]int{}, map[string]int{}, nil
	}
	byType, err = groupCount(live, "type")
	if err != nil {
		return
	}
	byLifecycle, err = groupCount(live, "lifecycle")
	if err != nil {
		return
	}
	byStatus, err = groupCount(live, "status")
	return
}

// FacetCountsExt returns the standard three facet maps plus byFeature and
// byRepo for richer filter sidebars. Empty values map to "" keys.
func FacetCountsExt(live *sql.DB) (byType, byLifecycle, byStatus, byFeature, byRepo map[string]int, err error) {
	if live == nil {
		empty := map[string]int{}
		return empty, empty, empty, empty, empty, nil
	}
	byType, err = groupCount(live, "type")
	if err != nil {
		return
	}
	byLifecycle, err = groupCount(live, "lifecycle")
	if err != nil {
		return
	}
	byStatus, err = groupCount(live, "status")
	if err != nil {
		return
	}
	byFeature, err = groupCount(live, "feature")
	if err != nil {
		return
	}
	byRepo, err = groupCount(live, "repo")
	return
}

func groupCount(live *sql.DB, col string) (map[string]int, error) {
	rows, err := live.Query(fmt.Sprintf(
		`SELECT COALESCE(%s,''), COUNT(*) FROM artifacts GROUP BY %s`, col, col))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// orderBy whitelists sort columns to avoid SQL injection via sortBy.
func orderBy(sortBy string) string {
	switch sortBy {
	case "updated":
		return "a.updated DESC, a.id"
	case "created":
		return "a.created DESC, a.id"
	case "access":
		return "access_count DESC, a.id"
	case "type":
		return "a.type, a.feature, a.id"
	default:
		return "a.repo, a.type, a.feature, a.id"
	}
}

func expandCSV(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	var out []string
	for _, raw := range in {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

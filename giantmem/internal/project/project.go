package project

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

type IndexInfo struct {
	Name        string   `json:"name"`
	Docs        int      `json:"docs"`
	Artifacts   int      `json:"artifacts"`
	ArchiveDocs int      `json:"archiveDocs"`
	Worktrees   []string `json:"worktrees"`
	Gone        bool     `json:"gone"`
}

type Deleted struct {
	LiveDocs    int `json:"liveDocs"`
	Artifacts   int `json:"artifacts"`
	Embeddings  int `json:"embeddings"`
	AccessRows  int `json:"accessRows"`
	Sessions    int `json:"sessions"`
	ArchiveDocs int `json:"archiveDocs"`
}

// List returns every distinct live_docs.project with counts. archive may be
// nil; archive doc counts are then 0.
func List(live, archive *sql.DB) ([]IndexInfo, error) {
	if live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	rows, err := live.Query(
		`SELECT project, COALESCE(worktree_path,''), COUNT(*)
           FROM live_docs GROUP BY project, worktree_path ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byName := map[string]*IndexInfo{}
	var order []string
	for rows.Next() {
		var name, wt string
		var n int
		if err := rows.Scan(&name, &wt, &n); err != nil {
			return nil, err
		}
		info := byName[name]
		if info == nil {
			info = &IndexInfo{Name: name, Gone: true}
			byName[name] = info
			order = append(order, name)
		}
		info.Docs += n
		// mirrors GUI: gone only when every non-empty worktree is missing
		alive := wt == ""
		if wt != "" {
			info.Worktrees = append(info.Worktrees, wt)
			if _, err := os.Stat(wt); err == nil {
				alive = true
			}
		}
		if alive {
			info.Gone = false
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, name := range order {
		info := byName[name]
		ids, err := artifactIDs(live, name, info.Worktrees)
		if err != nil {
			return nil, err
		}
		info.Artifacts = len(ids)
		if archive != nil {
			_ = archive.QueryRow(
				`SELECT COUNT(*) FROM documents WHERE project = ? OR COALESCE(canonical_project,'') = ?`,
				name, name).Scan(&info.ArchiveDocs)
		}
	}

	out := make([]IndexInfo, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

// Delete removes a project from the live index: live_docs (fts via trigger),
// active_sessions, artifacts and their embeddings/access rows. With
// purgeArchive it also drops the project's archives.db documents. archive may
// be nil unless purgeArchive is set.
func Delete(live, archive *sql.DB, name string, purgeArchive bool) (Deleted, error) {
	var d Deleted
	if live == nil {
		return d, fmt.Errorf("live db not open")
	}
	if purgeArchive && archive == nil {
		return d, fmt.Errorf("archive db not open")
	}

	var worktrees []string
	rows, err := live.Query(
		`SELECT DISTINCT COALESCE(worktree_path,'') FROM live_docs WHERE project = ?`, name)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var wt string
		if err := rows.Scan(&wt); err != nil {
			rows.Close()
			return d, err
		}
		if wt != "" {
			worktrees = append(worktrees, wt)
		}
	}
	rows.Close()

	ids, err := artifactIDs(live, name, worktrees)
	if err != nil {
		return d, err
	}

	hasEmbeddings, err := tableExists(live, "artifact_embedding_meta")
	if err != nil {
		return d, err
	}

	tx, err := live.Begin()
	if err != nil {
		return d, err
	}
	defer tx.Rollback()

	for _, id := range ids {
		if n, err := execCount(tx, `DELETE FROM artifact_access WHERE artifact_id = ?`, id); err != nil {
			return d, err
		} else {
			d.AccessRows += n
		}
		if hasEmbeddings {
			var rowid int64
			err := tx.QueryRow(`SELECT rowid FROM artifact_embedding_meta WHERE artifact_id = ?`, id).Scan(&rowid)
			switch err {
			case nil:
				if _, err := tx.Exec(`DELETE FROM artifact_embeddings WHERE rowid = ?`, rowid); err != nil {
					return d, err
				}
				if _, err := tx.Exec(`DELETE FROM artifact_embedding_meta WHERE artifact_id = ?`, id); err != nil {
					return d, err
				}
				d.Embeddings++
			case sql.ErrNoRows:
			default:
				return d, err
			}
		}
		if _, err := tx.Exec(`DELETE FROM artifacts WHERE id = ?`, id); err != nil {
			return d, err
		}
	}
	d.Artifacts = len(ids)

	if n, err := execCount(tx, `DELETE FROM live_docs WHERE project = ?`, name); err != nil {
		return d, err
	} else {
		d.LiveDocs = n
	}
	if n, err := execCount(tx, `DELETE FROM active_sessions WHERE project = ?`, name); err != nil {
		return d, err
	} else {
		d.Sessions = n
	}
	if err := tx.Commit(); err != nil {
		return d, err
	}

	if purgeArchive {
		n, err := purgeArchiveDocs(archive, name)
		if err != nil {
			return d, fmt.Errorf("live index deleted, archive purge failed: %w", err)
		}
		d.ArchiveDocs = n
	}
	return d, nil
}

// artifactIDs resolves the project's artifacts table rows: worktree match
// first (artifact repo may be the canonical name, not this project bucket),
// plus repo-name match for rows with no worktree.
func artifactIDs(live *sql.DB, name string, worktrees []string) ([]string, error) {
	ok, err := tableExists(live, "artifacts")
	if err != nil || !ok {
		return nil, err
	}
	q := `SELECT id FROM artifacts WHERE (repo = ? AND (worktree IS NULL OR worktree = ''))`
	args := []any{name}
	if len(worktrees) > 0 {
		q += ` OR worktree IN (?` + strings.Repeat(",?", len(worktrees)-1) + `)`
		for _, wt := range worktrees {
			args = append(args, wt)
		}
	}
	rows, err := live.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// documents_fts is standalone (rowid == documents.id) so both must go in one tx.
func purgeArchiveDocs(archive *sql.DB, name string) (int, error) {
	// sessions facets group by canonical_project, so a purge must match both
	rows, err := archive.Query(
		`SELECT id FROM documents WHERE project = ? OR COALESCE(canonical_project,'') = ?`, name, name)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := archive.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec(`DELETE FROM documents_fts WHERE rowid = ?`, id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM documents WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	return len(ids), tx.Commit()
}

func tableExists(d *sql.DB, name string) (bool, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}

func execCount(tx *sql.Tx, q string, args ...any) (int, error) {
	res, err := tx.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

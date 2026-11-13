package artifacts

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
)

// giantmemMarker splits an absolute live_docs path into the workspace-relative
// portion Classify understands. live_index.py stores absolute paths; everything
// after this marker is what Scan would have produced walking the .giantmem dir.
const giantmemMarker = "/.giantmem/"

// RelFromLivePath returns the workspace-relative path for a live_docs.path, or
// ("", false) when the path is not under a .giantmem/ directory (e.g. harness
// memory files, which are recall-only and never typed artifacts).
func RelFromLivePath(abs string) (string, bool) {
	abs = filepath.ToSlash(abs)
	i := strings.LastIndex(abs, giantmemMarker)
	if i < 0 {
		return "", false
	}
	rel := abs[i+len(giantmemMarker):]
	if rel == "" {
		return "", false
	}
	return rel, true
}

// DeriveFromLiveDoc builds an Artifact from a live_docs row with NO filesystem
// access. relPath is workspace-relative (post-.giantmem/). content is the full
// file body as mirrored into live_docs. Frontmatter, when present, overrides
// the path-inferred classification exactly as FS Scan does.
//
// Returns ok=false when relPath does not classify as a known artifact.
func DeriveFromLiveDoc(relPath, content, repo, branch, worktree string) (Artifact, bool) {
	cls, ok := Classify(relPath)
	if !ok {
		return Artifact{}, false
	}
	a := Artifact{
		Type:     cls.Type,
		Feature:  cls.Feature,
		Domain:   cls.Domain,
		Name:     cls.Name,
		Status:   "ready",
		Path:     filepath.ToSlash(relPath),
		Repo:     repo,
		Branch:   branch,
		Worktree: worktree,
		Size:     int64(len(content)),
	}

	ext := strings.ToLower(filepath.Ext(relPath))
	if ext == ".json" {
		applyJSONFrontmatterBytes(&a, []byte(content))
	} else if fm, _, has := ParseFrontmatter(content); has {
		a.HasFront = true
		applyFrontmatterMap(&a, fm)
	}

	if a.Type == "tasks" {
		if status, ok := taskStatusFromContent(content); ok {
			a.Status = status
		}
	}
	if a.Lifecycle == "" {
		a.Lifecycle = defaultLifecycle(relPath)
	}
	a.ID = projectedID(a)
	return a, true
}

// projectedID is the artifacts-table/embedding key. BuildID alone is unique only
// within one workspace's artifacts.json; the projection is cross-repo, so the
// same relative path (e.g. plans/current.md -> repo:plan:current) in different
// repos would collide on an id-only PK. Prefixing the repo keeps them distinct.
// Branch-worktree siblings share a repo label, so they intentionally collapse
// newest-wins (per-repo granularity).
func projectedID(a Artifact) string {
	base := BuildID(a)
	if a.Repo == "" {
		return base
	}
	return a.Repo + "/" + base
}

// TableStats reports what one ReconcileTable pass changed.
type TableStats struct {
	Scanned   int `json:"scanned"`
	Upserted  int `json:"upserted"`
	Removed   int `json:"removed"`
	Canonical int `json:"canonical_backfilled"`
}

// Cache holds each live_docs row's derive result between passes. Zero value is
// ready; one pass at a time.
type Cache struct {
	rows    map[string]CachedRow
	written map[string]Artifact
}

// CachedRow is a live_docs row's derive result, keyed by live_docs.path.
type CachedRow struct {
	key      string
	Mtime    int64
	OK       bool
	A        Artifact
	BodyHash string
}

// Rows returns the rows the last pass saw.
func (c *Cache) Rows() map[string]CachedRow { return c.rows }

// BodyHash is sha256 hex of a frontmatter-stripped body; embedding meta compares against it.
func BodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// ReconcileTable projects every .giantmem/ row in live_docs into the artifacts
// table. Pure derive (no FS): reuses live_docs.content. Idempotent. Also
// backfills empty live_docs.canonical_project so repo-grouping works.
func ReconcileTable(live *sql.DB, archiveBase string) (TableStats, error) {
	return ReconcileTableCached(live, archiveBase, &Cache{})
}

// ReconcileTableCached is ReconcileTable reading content only for rows whose
// ingested_at, mtime, size, project or worktree moved since c last saw them.
func ReconcileTableCached(live *sql.DB, archiveBase string, c *Cache) (TableStats, error) {
	var st TableStats
	if live == nil {
		return st, nil
	}

	// octet_length reads the record header, so this scan never loads content
	rows, err := live.Query(
		`SELECT path, project, worktree_path, COALESCE(canonical_project,''), mtime,
                COALESCE(ingested_at,''), octet_length(content)
         FROM live_docs WHERE instr(path, ?) > 0`, giantmemMarker)
	if err != nil {
		return st, err
	}
	type rowMeta struct {
		path, rel, project, worktree, canonical, key string
		mtime                                        int64
	}
	var metas []rowMeta
	for rows.Next() {
		var m rowMeta
		var ingested string
		var size int64
		if err := rows.Scan(&m.path, &m.project, &m.worktree, &m.canonical, &m.mtime, &ingested, &size); err != nil {
			rows.Close()
			return st, err
		}
		var ok bool
		if m.rel, ok = RelFromLivePath(m.path); !ok {
			continue
		}
		m.key = fmt.Sprintf("%s|%d|%d|%s|%s", ingested, m.mtime, size, m.project, m.worktree)
		metas = append(metas, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return st, err
	}
	rows.Close()

	next := make(map[string]CachedRow, len(metas))
	var misses []rowMeta
	for _, m := range metas {
		if r, ok := c.rows[m.path]; ok && r.key == m.key {
			next[m.path] = r
		} else {
			misses = append(misses, m)
		}
	}
	if len(misses) > 0 {
		rtx, err := live.Begin()
		if err != nil {
			return st, err
		}
		get, err := rtx.Prepare(`SELECT content FROM live_docs WHERE path = ?`)
		if err != nil {
			rtx.Rollback()
			return st, err
		}
		for _, m := range misses {
			var content string
			if err := get.QueryRow(m.path).Scan(&content); err != nil {
				if err == sql.ErrNoRows {
					continue
				}
				get.Close()
				rtx.Rollback()
				return st, err
			}
			r := CachedRow{key: m.key, Mtime: m.mtime}
			r.A, r.OK = DeriveFromLiveDoc(m.rel, content, m.project, "", m.worktree)
			if r.OK {
				if r.A.Updated == "" && m.mtime > 0 {
					r.A.Updated = time.Unix(m.mtime, 0).UTC().Format("2006-01-02")
				}
				_, body, _ := ParseFrontmatter(content)
				r.BodyHash = BodyHash(body)
			}
			next[m.path] = r
		}
		get.Close()
		rtx.Rollback()
	}
	c.rows = next

	type pending struct {
		a         Artifact
		path      string
		canonical string
		project   string
		mtime     int64
	}
	var items []pending
	for _, m := range metas {
		r, ok := next[m.path]
		if !ok {
			continue
		}
		st.Scanned++
		if !r.OK {
			continue
		}
		items = append(items, pending{a: r.A, path: m.path, canonical: m.canonical, project: m.project, mtime: m.mtime})
	}

	// Branch/worktree siblings derive the SAME projectedID but carry different
	// bodies/worktrees. Upserting every sibling each pass makes them overwrite
	// one another (last-wins) — a real write that dirties live.db and respins the
	// daemon's fsnotify reconcile into an infinite loop. Collapse to one winner
	// per id first. Deterministic: newest mtime, tie-break highest path.
	winners := make(map[string]pending, len(items))
	for _, it := range items {
		cur, ok := winners[it.a.ID]
		if !ok || it.mtime > cur.mtime || (it.mtime == cur.mtime && it.path > cur.path) {
			winners[it.a.ID] = it
		}
	}

	byPath := make(map[string]string, len(winners))
	for id, it := range winners {
		byPath[it.a.Repo+"\x00"+it.a.Path] = id
	}
	if err := carryRenamedIDs(live, byPath); err != nil {
		return st, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	derived := make(map[string]struct{}, len(winners))
	// one txn: ~12k autocommit no-op upserts spent most of a pass in lock syscalls
	tx, err := live.Begin()
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	written := make(map[string]Artifact, len(winners))
	for _, it := range winners {
		derived[it.a.ID] = struct{}{}
		written[it.a.ID] = it.a
		// this engine is the only artifacts writer, so an unchanged winner is already on disk
		if prev, ok := c.written[it.a.ID]; ok && prev == it.a {
			continue
		}
		res, err := upsertArtifact(tx, it.a, now)
		if err != nil {
			return st, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			st.Upserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	c.written = written

	// canonical_project is a per-path live_docs column, independent of the id
	// collapse — backfill every source row, not just winners. The WHERE guard
	// skips already-filled rows so this converges after one pass and never feeds
	// the daemon loop.
	for _, it := range items {
		if it.canonical != "" {
			continue
		}
		canon := project.Canonicalize(it.project, archiveBase)
		if canon == "" {
			continue
		}
		res, err := live.Exec(
			`UPDATE live_docs SET canonical_project=? WHERE path=? AND COALESCE(canonical_project,'')=''`,
			canon, it.path)
		if err != nil {
			return st, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			st.Canonical++
		}
	}

	removed, err := deleteOrphans(live, derived)
	if err != nil {
		return st, err
	}
	st.Removed = removed
	return st, nil
}

func upsertArtifact(live interface {
	Exec(string, ...any) (sql.Result, error)
}, a Artifact, now string) (sql.Result, error) {
	updated := a.Updated
	hasFront := 0
	if a.HasFront {
		hasFront = 1
	}
	return live.Exec(
		`INSERT INTO artifacts
           (id, type, feature, domain, name, status, lifecycle, scope, repo,
            branch, path, worktree, size, created, updated, has_front, indexed_at,
            notion, notion_synced, notion_row)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
         ON CONFLICT(id) DO UPDATE SET
            type=excluded.type, feature=excluded.feature, domain=excluded.domain,
            name=excluded.name, status=excluded.status, lifecycle=excluded.lifecycle,
            scope=excluded.scope, repo=excluded.repo, branch=excluded.branch,
            path=excluded.path, worktree=excluded.worktree, size=excluded.size,
            created=excluded.created, updated=excluded.updated,
            has_front=excluded.has_front, indexed_at=excluded.indexed_at,
            notion=excluded.notion, notion_synced=excluded.notion_synced,
            notion_row=excluded.notion_row
         WHERE type IS NOT excluded.type OR feature IS NOT excluded.feature
            OR domain IS NOT excluded.domain OR name IS NOT excluded.name
            OR status IS NOT excluded.status OR lifecycle IS NOT excluded.lifecycle
            OR scope IS NOT excluded.scope OR repo IS NOT excluded.repo
            OR branch IS NOT excluded.branch OR path IS NOT excluded.path
            OR worktree IS NOT excluded.worktree OR size IS NOT excluded.size
            OR created IS NOT excluded.created OR updated IS NOT excluded.updated
            OR has_front IS NOT excluded.has_front
            OR notion IS NOT excluded.notion OR notion_synced IS NOT excluded.notion_synced
            OR notion_row IS NOT excluded.notion_row`,
		a.ID, a.Type, a.Feature, a.Domain, a.Name, a.Status, a.Lifecycle, a.Scope,
		a.Repo, a.Branch, a.Path, a.Worktree, a.Size, a.Created, updated, hasFront, now,
		a.Notion, a.NotionSynced, a.NotionRow,
	)
}

// carryRenamedIDs re-keys vectors and access history when a file's id changes
// (a taxonomy rename), so the embed pass skips it instead of re-embedding.
func carryRenamedIDs(live *sql.DB, byPath map[string]string) error {
	current := make(map[string]bool, len(byPath))
	for _, id := range byPath {
		current[id] = true
	}
	rows, err := live.Query(`SELECT id, repo, path FROM artifacts`)
	if err != nil {
		return err
	}
	moves := map[string]string{}
	for rows.Next() {
		var id, repo, path string
		if err := rows.Scan(&id, &repo, &path); err != nil {
			rows.Close()
			return err
		}
		if to, ok := byPath[repo+"\x00"+path]; ok && to != id && !current[id] {
			moves[id] = to
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(moves) == 0 {
		return nil
	}
	tx, err := live.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for from, to := range moves {
		if _, err := tx.Exec(`UPDATE OR IGNORE artifact_embedding_meta SET artifact_id=? WHERE artifact_id=?`, to, from); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE artifact_access SET artifact_id=? WHERE artifact_id=?`, to, from); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// deleteOrphans removes artifacts rows whose id is no longer derivable from
// live_docs (file deleted). Diffs id-sets only — no content read.
func deleteOrphans(live *sql.DB, keep map[string]struct{}) (int, error) {
	rows, err := live.Query(`SELECT id FROM artifacts`)
	if err != nil {
		return 0, err
	}
	var orphans []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		if _, ok := keep[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, id := range orphans {
		if _, err := live.Exec(`DELETE FROM artifacts WHERE id=?`, id); err != nil {
			return 0, err
		}
	}
	return len(orphans), nil
}

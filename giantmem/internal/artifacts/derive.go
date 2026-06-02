package artifacts

import (
	"database/sql"
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

// ReconcileTable projects every .giantmem/ row in live_docs into the artifacts
// table. Pure derive (no FS): reuses live_docs.content. Idempotent. Also
// backfills empty live_docs.canonical_project so repo-grouping works.
//
// Steady state is cheap: derive is string parsing over a few hundred rows.
// Short batched transactions keep it friendly to concurrent peer writes (the
// PostToolUse hook) under WAL + busy_timeout.
func ReconcileTable(live *sql.DB, archiveBase string) (TableStats, error) {
	var st TableStats
	if live == nil {
		return st, nil
	}

	branchByWorktree, err := sessionBranches(live)
	if err != nil {
		return st, err
	}

	rows, err := live.Query(
		`SELECT path, content, project, worktree_path, COALESCE(canonical_project,''), mtime
         FROM live_docs WHERE instr(path, ?) > 0`, giantmemMarker)
	if err != nil {
		return st, err
	}

	type pending struct {
		a         Artifact
		path      string
		canonical string
		project   string
		mtime     int64
	}
	var items []pending
	for rows.Next() {
		var absPath, content, proj, worktree, canonical string
		var mtime int64
		if err := rows.Scan(&absPath, &content, &proj, &worktree, &canonical, &mtime); err != nil {
			rows.Close()
			return st, err
		}
		rel, ok := RelFromLivePath(absPath)
		if !ok {
			continue
		}
		st.Scanned++
		a, ok := DeriveFromLiveDoc(rel, content, proj, branchByWorktree[worktree], worktree)
		if !ok {
			continue
		}
		if a.Updated == "" && mtime > 0 {
			a.Updated = time.Unix(mtime, 0).UTC().Format("2006-01-02")
		}
		items = append(items, pending{a: a, path: absPath, canonical: canonical, project: proj, mtime: mtime})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return st, err
	}
	rows.Close()

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

	now := time.Now().UTC().Format(time.RFC3339)
	derived := make(map[string]struct{}, len(winners))
	for _, it := range winners {
		res, err := upsertArtifact(live, it.a, now)
		if err != nil {
			return st, err
		}
		derived[it.a.ID] = struct{}{}
		if n, _ := res.RowsAffected(); n > 0 {
			st.Upserted++
		}
	}

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

// sessionBranches maps worktree_path -> branch from active_sessions, preferring
// the most-recently-seen session. Pure SQL, no git/FS — live_docs itself has no
// branch column, so this is how the projection learns branch.
func sessionBranches(live *sql.DB) (map[string]string, error) {
	out := map[string]string{}
	rows, err := live.Query(
		`SELECT worktree_path, branch, COALESCE(last_seen,'') FROM active_sessions
         WHERE COALESCE(worktree_path,'') != '' AND COALESCE(branch,'') != ''`)
	if err != nil {
		// active_sessions always exists post-v1; treat any error as "no branches".
		return out, nil
	}
	defer rows.Close()
	seen := map[string]string{}
	for rows.Next() {
		var wt, branch, lastSeen string
		if err := rows.Scan(&wt, &branch, &lastSeen); err != nil {
			return out, err
		}
		if prev, ok := seen[wt]; !ok || lastSeen > prev {
			seen[wt] = lastSeen
			out[wt] = branch
		}
	}
	return out, rows.Err()
}

func upsertArtifact(live *sql.DB, a Artifact, now string) (sql.Result, error) {
	updated := a.Updated
	hasFront := 0
	if a.HasFront {
		hasFront = 1
	}
	return live.Exec(
		`INSERT INTO artifacts
           (id, type, feature, domain, name, status, lifecycle, scope, repo,
            branch, path, worktree, size, created, updated, has_front, indexed_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
         ON CONFLICT(id) DO UPDATE SET
            type=excluded.type, feature=excluded.feature, domain=excluded.domain,
            name=excluded.name, status=excluded.status, lifecycle=excluded.lifecycle,
            scope=excluded.scope, repo=excluded.repo, branch=excluded.branch,
            path=excluded.path, worktree=excluded.worktree, size=excluded.size,
            created=excluded.created, updated=excluded.updated,
            has_front=excluded.has_front, indexed_at=excluded.indexed_at
         WHERE type IS NOT excluded.type OR feature IS NOT excluded.feature
            OR domain IS NOT excluded.domain OR name IS NOT excluded.name
            OR status IS NOT excluded.status OR lifecycle IS NOT excluded.lifecycle
            OR scope IS NOT excluded.scope OR repo IS NOT excluded.repo
            OR branch IS NOT excluded.branch OR path IS NOT excluded.path
            OR worktree IS NOT excluded.worktree OR size IS NOT excluded.size
            OR created IS NOT excluded.created OR updated IS NOT excluded.updated
            OR has_front IS NOT excluded.has_front`,
		a.ID, a.Type, a.Feature, a.Domain, a.Name, a.Status, a.Lifecycle, a.Scope,
		a.Repo, a.Branch, a.Path, a.Worktree, a.Size, a.Created, updated, hasFront, now,
	)
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

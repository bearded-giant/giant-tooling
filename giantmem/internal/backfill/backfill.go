// Package backfill scans every .giantmem/ workspace on disk and upserts ALL
// non-empty files into live_docs. Closes the gap left by the PostToolUse
// hook, which only sees .md edits made by Claude — files touched by vim,
// git pull, scripts, etc. never reach live_docs otherwise.
//
// Idempotent: a row is skipped when stored mtime >= file mtime AND stored
// content length matches the file size. Safe to run concurrently with the
// projection reconciler (WAL, short txns).
package backfill

import (
	"database/sql"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
)

const maxFileSize = 5_000_000

// extraRootsMarker opts a repo into indexing files OUTSIDE its .giantmem/. Its
// presence at <worktree>/.giantmem/.index-roots is the per-repo gate — without
// it, only the .giantmem/ tree is walked. Lines are globs relative to the
// worktree root (see matchesAny). These rows land in live_docs (FTS-searchable)
// but never project into artifacts or embeddings — those stay .giantmem-typed.
const extraRootsMarker = ".index-roots"

// Stats reports what one Run pass changed.
type Stats struct {
	Workspaces int `json:"workspaces"`
	Scanned    int `json:"scanned"`
	Upserted   int `json:"upserted"`
	Skipped    int `json:"skipped"`
	Empty      int `json:"empty"`
	TooLarge   int `json:"too_large"`
	Errors     int `json:"errors"`
}

// Run discovers every .giantmem/ under the configured dev roots (via
// artifacts.DiscoverWorkspaces) and upserts every non-empty file into
// live_docs. maxDepth bounds the discovery walk; pass 0 for the default.
func Run(db *sql.DB, archiveBase string, maxDepth int) (Stats, error) {
	var st Stats
	workspaces := artifacts.DiscoverWorkspaces(maxDepth)
	st.Workspaces = len(workspaces)
	for _, ws := range workspaces {
		walkWorkspace(db, archiveBase, ws, &st)
	}
	return st, nil
}

// RunOnWorkspace backfills a single .giantmem/ root. Useful for tests and
// targeted re-indexing.
func RunOnWorkspace(db *sql.DB, archiveBase, workspace string) (Stats, error) {
	var st Stats
	st.Workspaces = 1
	walkWorkspace(db, archiveBase, workspace, &st)
	return st, nil
}

func walkWorkspace(db *sql.DB, archiveBase, ws string, st *Stats) {
	worktreePath := filepath.Dir(ws)
	info := project.Detect(worktreePath, archiveBase)
	featureFromJSON := project.FeatureFromGiantmem(worktreePath)
	sha := gitSha(worktreePath)
	now := time.Now().UTC().Format(time.RFC3339)

	_ = filepath.WalkDir(ws, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p == ws {
				return nil
			}
			// skip hidden + heavy dirs but stay inside .giantmem/ — dot-dirs
			// like .mdlive hold tool mirrors, not docs
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" || name == "venv" {
				return fs.SkipDir
			}
			return nil
		}
		if name == ".giantmem-index" || name == ".DS_Store" || strings.HasPrefix(name, ".") {
			return nil
		}
		feature := featureFromPath(p)
		if feature == "" {
			feature = featureFromJSON
		}
		upsertFile(db, p, d, info.Project, worktreePath, feature, dirTypeFromPath(p), sha, now, st)
		return nil
	})

	walkExtraRoots(db, worktreePath, info.Project, sha, now, st)
}

// walkExtraRoots indexes repo files outside .giantmem/ when the worktree opts
// in via .giantmem/.index-roots. ponytail: full-repo walk gated behind that
// marker; skip-list below is the ceiling — narrow it with patterns, not code.
func walkExtraRoots(db *sql.DB, worktreePath, proj, sha, now string, st *Stats) {
	data, err := os.ReadFile(filepath.Join(worktreePath, ".giantmem", extraRootsMarker))
	if err != nil {
		return
	}
	patterns := parsePatterns(data)
	if len(patterns) == 0 {
		return
	}

	_ = filepath.WalkDir(worktreePath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			// dotdirs (incl. .giantmem — already walked — .git, .obsidian) + heavy dirs
			if p != worktreePath && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" || name == "venv") {
				return fs.SkipDir
			}
			return nil
		}
		if name == ".DS_Store" || strings.HasPrefix(name, ".") {
			return nil
		}
		rel, rerr := filepath.Rel(worktreePath, p)
		if rerr != nil {
			return nil
		}
		if !matchesAny(patterns, filepath.ToSlash(rel), name) {
			return nil
		}
		upsertFile(db, p, d, proj, worktreePath, "", "repo-doc", sha, now, st)
		return nil
	})
}

func parsePatterns(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// matchesAny reports whether rel (worktree-relative, slash) or its basename
// matches a pattern. Trailing "/" = directory prefix; no "/" = basename glob
// (repo-wide); otherwise filepath.Match on the relative path.
func matchesAny(patterns []string, rel, base string) bool {
	for _, pat := range patterns {
		switch {
		case strings.HasSuffix(pat, "/"):
			if strings.HasPrefix(rel, pat) {
				return true
			}
		case !strings.Contains(pat, "/"):
			if ok, _ := filepath.Match(pat, base); ok {
				return true
			}
		default:
			if ok, _ := filepath.Match(pat, rel); ok {
				return true
			}
		}
	}
	return false
}

func upsertFile(db *sql.DB, p string, d fs.DirEntry, proj, worktreePath, feature, dirType, sha, now string, st *Stats) {
	fi, ierr := d.Info()
	if ierr != nil {
		st.Errors++
		return
	}
	st.Scanned++
	if fi.Size() == 0 {
		st.Empty++
		return
	}
	if fi.Size() > maxFileSize {
		st.TooLarge++
		return
	}
	// fast-path: skip when stored mtime matches AND content byte length
	// matches. octet_length (not length) — sqlite length() counts chars,
	// which mismatches fi.Size() for utf-8 multi-byte content and would
	// re-upsert every pass.
	var existingMtime int64
	var existingLen int
	mtime := fi.ModTime().Unix()
	err := db.QueryRow(
		"SELECT mtime, octet_length(content) FROM live_docs WHERE path = ?",
		p,
	).Scan(&existingMtime, &existingLen)
	if err == nil && existingMtime >= mtime && int64(existingLen) == fi.Size() {
		st.Skipped++
		return
	}

	body, rerr := os.ReadFile(p)
	if rerr != nil {
		st.Errors++
		return
	}

	_, xerr := db.Exec(`
            INSERT INTO live_docs (path, project, worktree_path, feature, dir_type,
                session_id, git_sha, mtime, ingested_at, content)
            VALUES (?, ?, ?, ?, ?, '', ?, ?, ?, ?)
            ON CONFLICT(path) DO UPDATE SET
                project=excluded.project,
                worktree_path=excluded.worktree_path,
                feature=excluded.feature,
                dir_type=excluded.dir_type,
                git_sha=excluded.git_sha,
                mtime=excluded.mtime,
                ingested_at=excluded.ingested_at,
                content=excluded.content
        `, p, proj, worktreePath, feature, dirType, sha, mtime, now, string(body))
	if xerr != nil {
		st.Errors++
		return
	}
	st.Upserted++
}

func featureFromPath(p string) string {
	p = filepath.ToSlash(p)
	idx := strings.Index(p, "/.giantmem/features/")
	if idx < 0 {
		return ""
	}
	rest := p[idx+len("/.giantmem/features/"):]
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

func dirTypeFromPath(p string) string {
	p = filepath.ToSlash(p)
	idx := strings.LastIndex(p, "/.giantmem/")
	if idx < 0 {
		return ""
	}
	rest := p[idx+len("/.giantmem/"):]
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "root"
	}
	head := parts[0]
	if strings.Contains(head, ".") {
		return "root"
	}
	return head
}

func gitSha(worktreePath string) string {
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

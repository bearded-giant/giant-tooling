package ingest

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
)

var (
	skipFiles = map[string]bool{".giantmem-index": true, ".DS_Store": true}
	skipDirs  = map[string]bool{".git": true}
)

// Options controls an ingest run.
type Options struct {
	ArchiveBase    string // ~/giantmem_archive
	ClaudeProjects string // ~/.claude/projects
	Project        string // optional project filter
	SessionsOnly   bool
	WorkspacesOnly bool
	Force          bool
}

// Stats summarizes a run.
type Stats struct {
	WorkspaceCount int
	WorkspaceErr   int
	SessionCount   int
	SessionErr     int
}

// EnsureSchema brings the documents schema up to date (idempotent).
func EnsureSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS documents (
            id INTEGER PRIMARY KEY,
            project TEXT NOT NULL,
            timestamp TEXT NOT NULL,
            source_type TEXT NOT NULL DEFAULT 'workspace',
            dir_type TEXT,
            filepath TEXT NOT NULL UNIQUE,
            filename TEXT NOT NULL,
            is_latest INTEGER DEFAULT 0,
            session_id TEXT,
            topic TEXT,
            indexed_at TEXT NOT NULL,
            cwd TEXT
        )`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
            content,
            tokenize='porter unicode61'
        )`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	// add cwd column to legacy schema if missing
	rows, err := db.Query("PRAGMA table_info(documents)")
	if err != nil {
		return err
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	if !cols["cwd"] {
		if _, err := db.Exec("ALTER TABLE documents ADD COLUMN cwd TEXT"); err != nil {
			return err
		}
	}
	return nil
}

// Run executes an ingest pass per Options. Returns stats.
func Run(db *sql.DB, opt Options) (Stats, error) {
	var st Stats
	if err := EnsureSchema(db); err != nil {
		return st, err
	}

	doWorkspaces := !opt.SessionsOnly
	doSessions := !opt.WorkspacesOnly

	if doWorkspaces {
		if _, err := os.Stat(opt.ArchiveBase); err != nil {
			if !doSessions {
				return st, fmt.Errorf("archive base not found: %s", opt.ArchiveBase)
			}
			doWorkspaces = false
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if doWorkspaces {
		scanRoot := opt.ArchiveBase
		if opt.Project != "" {
			scanRoot = filepath.Join(opt.ArchiveBase, opt.Project)
			if _, err := os.Stat(scanRoot); err != nil {
				if !doSessions {
					return st, fmt.Errorf("project not found in archive: %s", opt.Project)
				}
				goto sessions
			}
		}

		// drop existing non-session rows for the scope being rebuilt
		var deleteCond string
		var deleteArgs []any
		if opt.Project != "" {
			deleteCond = "project = ? AND source_type != 'session'"
			deleteArgs = []any{opt.Project}
		} else {
			deleteCond = "source_type != 'session'"
		}
		idRows, err := db.Query("SELECT id FROM documents WHERE "+deleteCond, deleteArgs...)
		if err != nil {
			return st, err
		}
		var oldIDs []int64
		for idRows.Next() {
			var id int64
			if err := idRows.Scan(&id); err != nil {
				idRows.Close()
				return st, err
			}
			oldIDs = append(oldIDs, id)
		}
		idRows.Close()
		tx, err := db.Begin()
		if err != nil {
			return st, err
		}
		for _, id := range oldIDs {
			if _, err := tx.Exec("DELETE FROM documents_fts WHERE rowid = ?", id); err != nil {
				tx.Rollback()
				return st, err
			}
			if _, err := tx.Exec("DELETE FROM documents WHERE id = ?", id); err != nil {
				tx.Rollback()
				return st, err
			}
		}
		if err := tx.Commit(); err != nil {
			return st, err
		}

		latest := resolveLatestTimestamps(opt.ArchiveBase)
		ws, errs := ingestWorkspaces(db, opt.ArchiveBase, scanRoot, latest, now)
		st.WorkspaceCount = ws
		st.WorkspaceErr = errs
	}

sessions:
	if doSessions {
		s, errs, err := ingestSessions(db, opt.ClaudeProjects, opt.Project, opt.Force, now)
		if err != nil {
			return st, err
		}
		st.SessionCount = s
		st.SessionErr = errs
	}

	return st, nil
}

// ----- workspace pass -----

func ingestWorkspaces(db *sql.DB, archiveBase, scanRoot string, latest map[string]bool, now string) (int, int) {
	var count, errs int
	walk := func(target string, fn func(p string, d fs.DirEntry, parsed *ParsedPath) bool) {
		filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if skipFiles[d.Name()] {
				return nil
			}
			parsed, ok := ParseArchivePath(p, archiveBase)
			if !ok {
				return nil
			}
			if !fn(p, d, parsed) {
				errs++
			}
			return nil
		})
	}

	// .md files
	walk(scanRoot, func(p string, d fs.DirEntry, parsed *ParsedPath) bool {
		if !strings.HasSuffix(p, ".md") {
			return true
		}
		ts := filepath.Join(archiveBase, parsed.Project, parsed.Timestamp)
		isLatest := 0
		if latest[ts] {
			isLatest = 1
		}
		ok := ingestFile(db, p, parsed, "workspace", "", "", isLatest, now, "")
		if ok {
			count++
		}
		return ok
	})

	// domains/*.json
	walk(scanRoot, func(p string, d fs.DirEntry, parsed *ParsedPath) bool {
		if !strings.HasSuffix(p, ".json") {
			return true
		}
		if !strings.Contains(p, string(filepath.Separator)+"domains"+string(filepath.Separator)) {
			return true
		}
		if strings.HasPrefix(d.Name(), ".") {
			return true
		}
		ts := filepath.Join(archiveBase, parsed.Project, parsed.Timestamp)
		isLatest := 0
		if latest[ts] {
			isLatest = 1
		}
		// flatten domain JSON for FTS
		content, err := flattenDomainJSON(p)
		if err != nil {
			return false
		}
		ok := ingestFile(db, p, parsed, "domain", "", "", isLatest, now, content)
		if ok {
			count++
		}
		return ok
	})

	// filebox/* (non-md, non-hidden)
	walk(scanRoot, func(p string, d fs.DirEntry, parsed *ParsedPath) bool {
		if strings.HasSuffix(p, ".md") {
			return true
		}
		if !strings.Contains(p, string(filepath.Separator)+"filebox"+string(filepath.Separator)) {
			return true
		}
		if strings.HasPrefix(d.Name(), ".") {
			return true
		}
		ts := filepath.Join(archiveBase, parsed.Project, parsed.Timestamp)
		isLatest := 0
		if latest[ts] {
			isLatest = 1
		}
		ok := ingestFile(db, p, parsed, "workspace", "", "", isLatest, now, "")
		if ok {
			count++
		}
		return ok
	})

	return count, errs
}

// ResolveLatestTimestamps maps absolute timestamp dirs that "latest" symlinks
// resolve to. Exported for use by source plugins.
func ResolveLatestTimestamps(archiveBase string) map[string]bool {
	return resolveLatestTimestamps(archiveBase)
}

func resolveLatestTimestamps(archiveBase string) map[string]bool {
	out := map[string]bool{}
	filepath.WalkDir(archiveBase, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == "latest" {
			info, err := os.Lstat(p)
			if err != nil {
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 {
				resolved, err := filepath.EvalSymlinks(p)
				if err == nil {
					if st, err := os.Stat(resolved); err == nil && st.IsDir() {
						out[resolved] = true
					}
				}
			}
		}
		return nil
	})
	return out
}

// ----- sessions pass -----

func ingestSessions(db *sql.DB, projectsDir, projectFilter string, force bool, now string) (int, int, error) {
	if _, err := os.Stat(projectsDir); err != nil {
		return 0, 0, nil
	}

	existing := map[string]string{}
	if !force {
		rows, err := db.Query("SELECT filepath, indexed_at FROM documents WHERE source_type = 'session'")
		if err == nil {
			for rows.Next() {
				var fp, ia string
				if err := rows.Scan(&fp, &ia); err == nil {
					existing[fp] = ia
				}
			}
			rows.Close()
		}
	}

	var count, errs int
	err := filepath.WalkDir(projectsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if strings.Contains(p, "subagents") {
			return nil
		}
		projectName := SessionProjectName(p, projectsDir)
		if projectFilter != "" && !strings.Contains(strings.ToLower(projectName), strings.ToLower(projectFilter)) {
			return nil
		}
		mtime, err := fileMtime(p)
		if err != nil {
			return nil
		}
		if !force {
			if ia, ok := existing[p]; ok {
				if t, err := time.Parse(time.RFC3339, ia); err == nil {
					if !mtime.After(t) {
						return nil
					}
				}
				// mtime newer: delete old row before re-ingest
				var oldID int64
				if err := db.QueryRow("SELECT id FROM documents WHERE filepath = ?", p).Scan(&oldID); err == nil {
					db.Exec("DELETE FROM documents_fts WHERE rowid = ?", oldID)
					db.Exec("DELETE FROM documents WHERE id = ?", oldID)
				}
			}
		}
		extract, err := ExtractSessionText(p)
		if err != nil || extract == nil {
			return nil
		}
		topic := DetectTopic(extract.Text)
		ts := mtime.Format("20060102_150405")
		parsed := &ParsedPath{Project: projectName, Timestamp: ts}
		if ok := ingestFile(db, p, parsed, "session", extract.SessionID, topic, 0, now, extract.Text); ok {
			// also store cwd
			if extract.Cwd != "" {
				db.Exec("UPDATE documents SET cwd = ? WHERE filepath = ?", extract.Cwd, p)
			}
			count++
		} else {
			errs++
		}
		return nil
	})
	return count, errs, err
}

// ----- shared upsert -----

// ingestFile inserts a row into documents + documents_fts. Returns true on
// success. contentOverride: indexable text. Empty = read from file (with
// filename prepended for friendly matches).
func ingestFile(db *sql.DB, fp string, parsed *ParsedPath, sourceType, sessionID, topic string, isLatest int, now, contentOverride string) bool {
	content := contentOverride
	if content == "" {
		raw, err := os.ReadFile(fp)
		if err != nil {
			return false
		}
		content = filepath.Base(fp) + "\n" + string(raw)
	}
	canonical := canonicalProjectFor(parsed.Project)
	res, err := db.Exec(
		`INSERT INTO documents
            (project, timestamp, source_type, dir_type, filepath, filename,
             is_latest, session_id, topic, indexed_at, canonical_project)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		parsed.Project, parsed.Timestamp, sourceType, nilIfEmpty(parsed.DirType),
		fp, filepath.Base(fp), isLatest, nilIfEmpty(sessionID), nilIfEmpty(topic), now,
		canonical,
	)
	if err != nil {
		return false
	}
	id, err := res.LastInsertId()
	if err != nil {
		return false
	}
	if _, err := db.Exec("INSERT INTO documents_fts (rowid, content) VALUES (?, ?)", id, content); err != nil {
		return false
	}
	return true
}

func canonicalProjectFor(name string) string {
	home, _ := os.UserHomeDir()
	base := os.Getenv("GIANTMEM_ARCHIVE_BASE")
	if base == "" {
		base = filepath.Join(home, "giantmem_archive")
	}
	return project.Canonicalize(name, base)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// FlattenDomainJSON exposes flattenDomainJSON for source plugins.
func FlattenDomainJSON(path string) (string, error) {
	return flattenDomainJSON(path)
}

// flattenDomainJSON emits FTS-friendly text from a domain JSON file.
func flattenDomainJSON(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		// fall back to raw content
		return filepath.Base(path) + "\n" + string(raw), nil
	}
	var lines []string
	getStr := func(k string) string {
		v, _ := data[k].(string)
		return v
	}
	lines = append(lines, "domain: "+getStr("domain"))
	lines = append(lines, "description: "+getStr("description"))

	if arr, ok := data["explored_for_features"].([]any); ok {
		for _, v := range arr {
			if s, _ := v.(string); s != "" {
				lines = append(lines, "feature: "+s)
			}
		}
	}
	if arr, ok := data["entry_points"].([]any); ok {
		for _, v := range arr {
			if m, ok := v.(map[string]any); ok {
				p, _ := m["path"].(string)
				t, _ := m["type"].(string)
				d, _ := m["description"].(string)
				lines = append(lines, fmt.Sprintf("entry_point: %s (%s) %s", p, t, d))
			}
		}
	}
	if arr, ok := data["key_files"].([]any); ok {
		for _, v := range arr {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			p, _ := m["path"].(string)
			pur, _ := m["purpose"].(string)
			lines = append(lines, fmt.Sprintf("key_file: %s -- %s", p, pur))
			for _, key := range []string{"exports", "patterns", "dependencies"} {
				if items, ok := m[key].([]any); ok {
					for _, item := range items {
						if s, _ := item.(string); s != "" {
							switch key {
							case "exports":
								lines = append(lines, "  export: "+s)
							case "patterns":
								lines = append(lines, "  pattern: "+s)
							case "dependencies":
								lines = append(lines, "  depends_on: "+s)
							}
						}
					}
				}
			}
		}
	}
	if arch, ok := data["architecture"].(map[string]any); ok {
		if layers, ok := arch["layers"].([]any); ok {
			var ls []string
			for _, l := range layers {
				if s, _ := l.(string); s != "" {
					ls = append(ls, s)
				}
			}
			if len(ls) > 0 {
				lines = append(lines, "layers: "+strings.Join(ls, " -> "))
			}
		}
		if flow, _ := arch["data_flow"].(string); flow != "" {
			lines = append(lines, "data_flow: "+flow)
		}
		for _, key := range []string{"patterns", "key_decisions"} {
			if items, ok := arch[key].([]any); ok {
				for _, v := range items {
					if s, _ := v.(string); s != "" {
						switch key {
						case "patterns":
							lines = append(lines, "architecture_pattern: "+s)
						case "key_decisions":
							lines = append(lines, "key_decision: "+s)
						}
					}
				}
			}
		}
	}
	if dm, ok := data["data_models"].(map[string]any); ok {
		if items, ok := dm["tables"].([]any); ok {
			for _, v := range items {
				if s, _ := v.(string); s != "" {
					lines = append(lines, "table: "+s)
				}
			}
		}
		if items, ok := dm["cache_keys"].([]any); ok {
			for _, v := range items {
				if s, _ := v.(string); s != "" {
					lines = append(lines, "cache_key: "+s)
				}
			}
		}
	}
	if deps, ok := data["dependencies"].(map[string]any); ok {
		for _, key := range []string{"internal", "external"} {
			if items, ok := deps[key].([]any); ok {
				for _, v := range items {
					if s, _ := v.(string); s != "" {
						lines = append(lines, key+"_dep: "+s)
					}
				}
			}
		}
	}
	if items, ok := data["gotchas"].([]any); ok {
		for _, v := range items {
			if s, _ := v.(string); s != "" {
				lines = append(lines, "gotcha: "+s)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

package db

import (
	"database/sql"
)

// EnsureArchive ensures the archives.db schema has all expected columns.
// archives.db is created/maintained by giantmem-search.py; we only add columns
// the gm CLI needs (cwd for session resume).
func EnsureArchive(d *sql.DB) error {
	cols, err := tableColumns(d, "documents")
	if err != nil {
		return err
	}
	if _, has := cols["cwd"]; !has {
		if _, err := d.Exec("ALTER TABLE documents ADD COLUMN cwd TEXT"); err != nil {
			return err
		}
	}
	return nil
}

// EnsureLive creates the live.db schema if missing.
func EnsureLive(d *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS live_docs (
            path TEXT PRIMARY KEY,
            project TEXT NOT NULL,
            worktree_path TEXT,
            feature TEXT,
            dir_type TEXT,
            session_id TEXT,
            git_sha TEXT,
            mtime INTEGER NOT NULL,
            ingested_at TEXT NOT NULL,
            content TEXT NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS idx_live_project ON live_docs(project)`,
		`CREATE INDEX IF NOT EXISTS idx_live_session ON live_docs(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_live_feature ON live_docs(feature)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS live_docs_fts USING fts5(
            path, project, feature, dir_type, content,
            tokenize='porter unicode61',
            content='live_docs', content_rowid='rowid'
        )`,
		// triggers to keep FTS in sync
		`CREATE TRIGGER IF NOT EXISTS live_docs_ai AFTER INSERT ON live_docs BEGIN
            INSERT INTO live_docs_fts(rowid, path, project, feature, dir_type, content)
            VALUES (new.rowid, new.path, new.project, COALESCE(new.feature,''), COALESCE(new.dir_type,''), new.content);
        END`,
		`CREATE TRIGGER IF NOT EXISTS live_docs_ad AFTER DELETE ON live_docs BEGIN
            INSERT INTO live_docs_fts(live_docs_fts, rowid, path, project, feature, dir_type, content)
            VALUES ('delete', old.rowid, old.path, old.project, COALESCE(old.feature,''), COALESCE(old.dir_type,''), old.content);
        END`,
		`CREATE TRIGGER IF NOT EXISTS live_docs_au AFTER UPDATE ON live_docs BEGIN
            INSERT INTO live_docs_fts(live_docs_fts, rowid, path, project, feature, dir_type, content)
            VALUES ('delete', old.rowid, old.path, old.project, COALESCE(old.feature,''), COALESCE(old.dir_type,''), old.content);
            INSERT INTO live_docs_fts(rowid, path, project, feature, dir_type, content)
            VALUES (new.rowid, new.path, new.project, COALESCE(new.feature,''), COALESCE(new.dir_type,''), new.content);
        END`,
		`CREATE TABLE IF NOT EXISTS active_sessions (
            id TEXT PRIMARY KEY,
            jsonl_path TEXT,
            project TEXT,
            worktree_path TEXT,
            branch TEXT,
            cwd TEXT,
            started_at TEXT,
            last_seen TEXT
        )`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func tableColumns(d *sql.DB, table string) (map[string]struct{}, error) {
	rows, err := d.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = struct{}{}
	}
	return out, rows.Err()
}

// OpenLiveOrCreate opens live.db, creates+initializes if missing.
func OpenLiveOrCreate(path string) (*sql.DB, error) {
	d, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, err
	}
	if err := EnsureLive(d); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

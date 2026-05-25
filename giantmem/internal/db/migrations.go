package db

import (
	"database/sql"
	"fmt"
	"os"
)

// Migration is one schema step. Apply must be idempotent enough to be re-run
// from a partial state (use IF NOT EXISTS, etc.) but each migration runs in a
// transaction so partial failures roll back.
type Migration struct {
	Version int
	Name    string
	Apply   func(*sql.Tx) error
}

// archiveMigrations is the canonical list for archives.db.
var archiveMigrations = []Migration{
	{
		Version: 1,
		Name:    "initial documents + fts",
		Apply: func(tx *sql.Tx) error {
			_, err := tx.Exec(`
                CREATE TABLE IF NOT EXISTS documents (
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
                    indexed_at TEXT NOT NULL
                );
                CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
                    content, tokenize='porter unicode61'
                );
            `)
			return err
		},
	},
	{
		Version: 2,
		Name:    "add cwd column",
		Apply: func(tx *sql.Tx) error {
			cols, err := txTableColumns(tx, "documents")
			if err != nil {
				return err
			}
			if _, has := cols["cwd"]; has {
				return nil
			}
			_, err = tx.Exec(`ALTER TABLE documents ADD COLUMN cwd TEXT`)
			return err
		},
	},
	{
		Version: 3,
		Name:    "add canonical_project column",
		Apply: func(tx *sql.Tx) error {
			cols, err := txTableColumns(tx, "documents")
			if err != nil {
				return err
			}
			if _, has := cols["canonical_project"]; has {
				return nil
			}
			_, err = tx.Exec(`ALTER TABLE documents ADD COLUMN canonical_project TEXT`)
			if err != nil {
				return err
			}
			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_documents_canonical ON documents(canonical_project)`)
			return err
		},
	},
}

// liveMigrations is the canonical list for live.db.
var liveMigrations = []Migration{
	{
		Version: 1,
		Name:    "initial live_docs + fts + active_sessions",
		Apply: func(tx *sql.Tx) error {
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
				if _, err := tx.Exec(s); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 2,
		Name:    "add canonical_project column",
		Apply: func(tx *sql.Tx) error {
			cols, err := txTableColumns(tx, "live_docs")
			if err != nil {
				return err
			}
			if _, has := cols["canonical_project"]; has {
				return nil
			}
			_, err = tx.Exec(`ALTER TABLE live_docs ADD COLUMN canonical_project TEXT`)
			if err != nil {
				return err
			}
			_, err = tx.Exec(`CREATE INDEX IF NOT EXISTS idx_live_canonical ON live_docs(canonical_project)`)
			return err
		},
	},
	{
		Version: 3,
		Name:    "scoped-memory: scopes + artifact_access",
		Apply: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS scopes (
                    scope_id TEXT PRIMARY KEY,
                    description TEXT,
                    tags TEXT,
                    repos TEXT,
                    updated_at TEXT
                )`,
				`CREATE TABLE IF NOT EXISTS artifact_access (
                    id INTEGER PRIMARY KEY,
                    artifact_id TEXT NOT NULL,
                    query TEXT,
                    rank INTEGER,
                    accessed_at TEXT NOT NULL
                )`,
				`CREATE INDEX IF NOT EXISTS idx_artifact_access_id ON artifact_access(artifact_id)`,
				`CREATE INDEX IF NOT EXISTS idx_artifact_access_ts ON artifact_access(accessed_at)`,
			}
			for _, s := range stmts {
				if _, err := tx.Exec(s); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 4,
		Name:    "scoped-memory: artifact_embeddings vec0 + meta",
		Apply: func(tx *sql.Tx) error {
			dim := embeddingDimFromEnv()
			stmts := []string{
				`CREATE VIRTUAL TABLE IF NOT EXISTS artifact_embeddings USING vec0(
                    embedding FLOAT[` + dim + `]
                )`,
				`CREATE TABLE IF NOT EXISTS artifact_embedding_meta (
                    artifact_id TEXT PRIMARY KEY,
                    rowid INTEGER NOT NULL,
                    body_hash TEXT NOT NULL,
                    dim INTEGER NOT NULL,
                    model TEXT NOT NULL,
                    updated_at TEXT NOT NULL
                )`,
				`CREATE INDEX IF NOT EXISTS idx_artifact_embedding_meta_rowid ON artifact_embedding_meta(rowid)`,
			}
			for _, s := range stmts {
				if _, err := tx.Exec(s); err != nil {
					return err
				}
			}
			return nil
		},
	},
}

// embeddingDimFromEnv returns the vec0 dimension as a string, honoring
// GIANTMEM_EMBED_DIM. Default 768 (bge-base-en-v1.5).
func embeddingDimFromEnv() string {
	v := os.Getenv("GIANTMEM_EMBED_DIM")
	if v == "" {
		return "768"
	}
	return v
}

// MigrateArchive brings archives.db up to the latest version.
func MigrateArchive(d *sql.DB) error {
	return migrate(d, archiveMigrations, "archives.db")
}

// MigrateLive brings live.db up to the latest version.
func MigrateLive(d *sql.DB) error {
	return migrate(d, liveMigrations, "live.db")
}

// SchemaVersion returns the current user_version of a db.
func SchemaVersion(d *sql.DB) (int, error) {
	var v int
	err := d.QueryRow("PRAGMA user_version").Scan(&v)
	return v, err
}

func migrate(d *sql.DB, migrations []Migration, label string) error {
	current, err := SchemaVersion(d)
	if err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	for _, m := range migrations {
		if m.Version <= current {
			continue
		}
		tx, err := d.Begin()
		if err != nil {
			return err
		}
		if err := m.Apply(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s migration v%d (%s): %w", label, m.Version, m.Name, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%s: applied migration v%d (%s)\n", label, m.Version, m.Name)
		current = m.Version
	}
	return nil
}

func txTableColumns(tx *sql.Tx, table string) (map[string]struct{}, error) {
	rows, err := tx.Query("PRAGMA table_info(" + table + ")")
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

package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

// Open opens the named SQLite db with WAL + busy timeout, then runs any pending
// migrations to bring it up to head. If the file is missing, it's created.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	d, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, err
	}
	if err := migrateFor(d, path); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return d, nil
}

// migrateFor picks the right migration list based on the db filename.
func migrateFor(d *sql.DB, path string) error {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "archives.db":
		return MigrateArchive(d)
	case "live.db":
		return MigrateLive(d)
	}
	// unknown db: do nothing — caller manages schema
	return nil
}

// OpenOrInit kept for compatibility; Open now creates if missing.
func OpenOrInit(path string) (*sql.DB, bool, error) {
	_, err := os.Stat(path)
	exists := err == nil
	d, err := Open(path)
	return d, exists, err
}

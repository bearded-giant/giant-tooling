package db

import "database/sql"

// Backwards-compatible shims. Migrations now run inside Open().
// These delegate to the migration framework so existing call sites keep working.

func EnsureArchive(d *sql.DB) error { return MigrateArchive(d) }
func EnsureLive(d *sql.DB) error    { return MigrateLive(d) }

// OpenLiveOrCreate is preserved for callers; behaves the same as Open() for live.db.
func OpenLiveOrCreate(path string) (*sql.DB, error) {
	return Open(path)
}

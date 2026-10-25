package projection

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
)

// Gate skips Reconcile when live_docs is unchanged since the last good pass.
type Gate struct {
	Resync  time.Duration
	lastFP  string
	lastRun time.Time
}

// Fingerprint moves on any insert, delete, mtime or byte-length change to the
// rows Reconcile reads. octet_length reads the record header, not the content.
func Fingerprint(live *sql.DB) (string, error) {
	var n, maxID, mtimes, size int64
	var ingested sql.NullString
	err := live.QueryRow(
		`SELECT count(*), COALESCE(max(rowid), 0), COALESCE(sum(mtime), 0),
                max(ingested_at), COALESCE(sum(octet_length(content)), 0)
         FROM live_docs WHERE instr(path, ?) > 0`, "/.giantmem/",
	).Scan(&n, &maxID, &mtimes, &ingested, &size)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d|%d|%d|%s|%d", n, maxID, mtimes, ingested.String, size), nil
}

// Run reconciles unless the fingerprint matches the last successful pass. A
// same-second, same-length edit slips past the fingerprint, so Resync forces a
// full pass anyway.
func (g *Gate) Run(live *sql.DB, archiveBase string, embedder search.Embedder) (Stats, bool, error) {
	fp, ferr := Fingerprint(live)
	if ferr == nil && fp == g.lastFP && time.Since(g.lastRun) < g.Resync {
		return Stats{}, false, nil
	}
	st, err := Reconcile(live, archiveBase, embedder)
	if err != nil || ferr != nil {
		g.lastFP = ""
		return st, true, err
	}
	g.lastFP, g.lastRun = fp, time.Now()
	return st, true, nil
}

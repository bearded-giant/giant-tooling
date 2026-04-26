package db

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("database not found at %s: %w", path, err)
	}
	d, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// OpenOrInit opens path; if missing, returns nil and the caller should init schema.
func OpenOrInit(path string) (*sql.DB, bool, error) {
	exists := true
	if _, err := os.Stat(path); err != nil {
		exists = false
	}
	d, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, exists, err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, exists, err
	}
	return d, exists, nil
}

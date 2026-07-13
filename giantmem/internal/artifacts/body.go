package artifacts

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// LiveAbsPath reconstructs the live_docs.path key for an artifact: the absolute
// path live_index.py stored when mirroring the file into live.db.
func LiveAbsPath(a Artifact) string {
	if a.Worktree == "" || a.Path == "" {
		return ""
	}
	return filepath.Join(a.Worktree, ".giantmem", a.Path)
}

// Body returns an artifact's raw body. Disk first (freshest for a live
// worktree), then the copy mirrored in live_docs.content, which survives
// worktree removal because live_docs is never pruned. Errors only when neither
// source has it.
func Body(live *sql.DB, a Artifact) (string, error) {
	abs := LiveAbsPath(a)
	if abs == "" {
		return "", fmt.Errorf("artifact has no path: %s", a.ID)
	}
	return BodyByPath(live, abs)
}

// BodyByPath is Body keyed directly on a live_docs.path — for files that never
// classified as typed artifacts (WORKSPACE.md, ad-hoc outputs).
func BodyByPath(live *sql.DB, abs string) (string, error) {
	if raw, err := os.ReadFile(abs); err == nil {
		return string(raw), nil
	}
	if live != nil {
		var content string
		if err := live.QueryRow(`SELECT content FROM live_docs WHERE path=?`, abs).Scan(&content); err == nil {
			return content, nil
		}
	}
	return "", fmt.Errorf("no body: not on disk (%s) and no live_docs row", abs)
}

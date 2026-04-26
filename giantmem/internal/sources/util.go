package sources

import (
	"os"
	"path/filepath"
)

func readFile(p string) string {
	raw, err := os.ReadFile(p)
	if err != nil {
		return filepath.Base(p)
	}
	return filepath.Base(p) + "\n" + string(raw)
}

func archiveBase() string {
	if v := os.Getenv("GIANTMEM_ARCHIVE_BASE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "giantmem_archive")
}

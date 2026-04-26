package health

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// IgnoreSet holds parsed `.giantmem-ignore` directives for a workspace.
type IgnoreSet struct {
	StaleOK  bool
	OrphanOK bool
	Patterns []string
}

// LoadIgnoreFor reads the workspace's `.giantmem-ignore` (parent of the
// `.giantmem/` dir) plus the global ignore at ~/.config/giantmem/global-ignore.
// Either may be missing; returns a usable empty set in that case.
func LoadIgnoreFor(giantmemDir string) IgnoreSet {
	worktree := filepath.Dir(giantmemDir)
	files := []string{
		filepath.Join(worktree, ".giantmem-ignore"),
		globalIgnorePath(),
	}
	var out IgnoreSet
	for _, f := range files {
		readIgnoreInto(f, &out)
	}
	return out
}

func globalIgnorePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "giantmem", "global-ignore")
}

func readIgnoreInto(path string, out *IgnoreSet) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			low := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "#")))
			switch low {
			case "stale-ok":
				out.StaleOK = true
			case "orphan-ok":
				out.OrphanOK = true
			}
			continue
		}
		out.Patterns = append(out.Patterns, line)
	}
}

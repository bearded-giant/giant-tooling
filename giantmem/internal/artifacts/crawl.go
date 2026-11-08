package artifacts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/workspaces"
)

var discoverCache struct {
	sync.Mutex
	key string
	at  time.Time
	out []string
}

// DiscoverWorkspaces returns absolute paths to every `.giantmem/` directory
// under workspaces.Roots() ($GIANTMEM_DEV_ROOTS or $HOME), depth-bounded
// (default workspaces.DefaultDepth).
func DiscoverWorkspaces(maxDepth int) []string {
	if maxDepth <= 0 {
		maxDepth = workspaces.DefaultDepth
	}
	roots := workspaces.Roots()
	key := fmt.Sprintf("%d\x00%s", maxDepth, strings.Join(roots, ":"))
	discoverCache.Lock()
	defer discoverCache.Unlock()
	// the $HOME walk costs ~1s and some callers resolve once per artifact row
	if discoverCache.key == key && time.Since(discoverCache.at) < 30*time.Second {
		return append([]string(nil), discoverCache.out...)
	}
	dirs := workspaces.Find(roots, maxDepth)
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = filepath.Join(d, ".giantmem")
	}
	discoverCache.key, discoverCache.at, discoverCache.out = key, time.Now(), out
	return append([]string(nil), out...)
}

// LoadOrScan returns a workspace's Index. Prefers the on-disk artifacts.json
// when present and fresh-enough; falls back to scanning. Used by cross-repo
// crawl to avoid re-scanning every workspace on every call.
func LoadOrScan(workspace string) (*Index, error) {
	idx, err := Load(workspace)
	if err != nil {
		return nil, err
	}
	if idx != nil && len(idx.Artifacts) > 0 {
		return idx, nil
	}
	return Scan(workspace)
}

// CrawlAll discovers every .giantmem/ under the configured dev roots and
// returns merged artifact rows annotated with their source workspace.
func CrawlAll(maxDepth int) ([]Artifact, []string, error) {
	workspaces := DiscoverWorkspaces(maxDepth)
	var all []Artifact
	for _, ws := range workspaces {
		idx, err := LoadOrScan(ws)
		if err != nil || idx == nil {
			continue
		}
		for _, a := range idx.Artifacts {
			all = append(all, a)
		}
	}
	return all, workspaces, nil
}

// DiscoverArchives returns every .../latest/ workspace under the archive
// base. The archive layout is {base}/{project}/{ts}/... with a `latest`
// symlink pointing at the most recent timestamp directory.
func DiscoverArchives(archiveBase string) []string {
	if archiveBase == "" {
		home, _ := os.UserHomeDir()
		archiveBase = filepath.Join(home, "giantmem_archive")
	}
	entries, err := os.ReadDir(archiveBase)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		latest := filepath.Join(archiveBase, e.Name(), "latest")
		st, err := os.Stat(latest)
		if err != nil || !st.IsDir() {
			continue
		}
		// Treat the latest snapshot as a workspace iff it looks like .giantmem/
		// (contains a `features` or `context` dir).
		if dirExists(filepath.Join(latest, "features")) || dirExists(filepath.Join(latest, "context")) {
			out = append(out, latest)
		}
	}
	sort.Strings(out)
	return out
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// CrawlEverything = live workspaces + archive latest. Archive entries are
// tagged with .Archived=true so callers can render or filter them.
func CrawlEverything(maxDepth int, archiveBase string) ([]Artifact, []string, []string, error) {
	live, liveDirs, err := CrawlAll(maxDepth)
	if err != nil {
		return nil, nil, nil, err
	}
	archives := DiscoverArchives(archiveBase)

	all := live
	for _, ws := range archives {
		idx, err := Scan(ws) // archives may not have artifacts.json yet
		if err != nil || idx == nil {
			continue
		}
		for _, a := range idx.Artifacts {
			a.Repo = filepath.Base(filepath.Dir(ws)) + " (archived)"
			all = append(all, a)
		}
	}
	return all, liveDirs, archives, nil
}

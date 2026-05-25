package artifacts

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoverWorkspaces returns absolute paths to every `.giantmem/` directory
// found under the configured dev roots. Default roots: $GIANTMEM_DEV_ROOTS
// (colon-separated) or $HOME/dev.
//
// Walk is depth-bounded (default 4) — typical layout is ~/dev/{lang}/repo/...,
// occasionally with `-wt` worktree siblings. We never follow symlinks.
func DiscoverWorkspaces(maxDepth int) []string {
	if maxDepth <= 0 {
		maxDepth = 4
	}
	roots := discoveryRoots()

	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		walkForWorkspaces(root, maxDepth, seen, &out)
	}
	sort.Strings(out)
	return out
}

func discoveryRoots() []string {
	if env := os.Getenv("GIANTMEM_DEV_ROOTS"); env != "" {
		var roots []string
		for _, p := range strings.Split(env, ":") {
			p = strings.TrimSpace(p)
			if p != "" {
				roots = append(roots, p)
			}
		}
		return roots
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, "dev")}
}

func walkForWorkspaces(root string, maxDepth int, seen map[string]bool, out *[]string) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return
	}
	rootDepth := strings.Count(absRoot, string(os.PathSeparator))

	_ = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		depth := strings.Count(p, string(os.PathSeparator)) - rootDepth
		if depth > maxDepth {
			return fs.SkipDir
		}
		base := d.Name()
		if base == ".git" || base == "node_modules" || base == "venv" || base == ".venv" || base == "__pycache__" {
			return fs.SkipDir
		}
		if base == ".giantmem" {
			abs, _ := filepath.Abs(p)
			if !seen[abs] {
				seen[abs] = true
				*out = append(*out, abs)
			}
			return fs.SkipDir
		}
		return nil
	})
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

// Package workspaces finds every .giantmem/ directory under the dev roots and
// groups them into projects: worktree containers (a dir holding .bare), git
// repos, and _other for workspaces outside any repo.
package workspaces

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultDepth = 7

var pruned = map[string]bool{
	"Library":          true,
	"OrbStack":         true,
	"google-cloud-sdk": true,
	"cache":            true,
	"giantmem-vault":   true,
	"giantmem_archive": true,
	"node_modules":     true,
	"target":           true,
	"vendor":           true,
	"venv":             true,
	"__pycache__":      true,
}

type Kind string

const (
	Container Kind = "container"
	Repo      Kind = "repo"
	Other     Kind = "other"
)

const (
	OtherName = "_other"
	RootLabel = "(root)"
)

type Member struct {
	Dir   string `json:"dir"`
	Label string `json:"label"`
}

type Project struct {
	Name    string   `json:"name"`
	Root    string   `json:"root"`
	Kind    Kind     `json:"kind"`
	Members []Member `json:"members"`
}

// Roots is $GIANTMEM_DEV_ROOTS (colon-separated) or $HOME.
func Roots() []string {
	if env := os.Getenv("GIANTMEM_DEV_ROOTS"); env != "" {
		var roots []string
		for _, p := range strings.Split(env, ":") {
			if p = strings.TrimSpace(p); p != "" {
				roots = append(roots, p)
			}
		}
		return roots
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{home}
}

// Find returns every directory holding a .giantmem/ under roots, sorted. Hidden
// and pruned dirs are skipped; symlinks are never followed.
func Find(roots []string, maxDepth int) []string {
	if maxDepth <= 0 {
		maxDepth = DefaultDepth
	}
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		base := strings.Count(abs, string(os.PathSeparator))
		_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() && p != abs {
					return fs.SkipDir
				}
				return nil
			}
			name := d.Name()
			if name == ".giantmem" && p != abs {
				if d.IsDir() || isDir(p) {
					add(filepath.Dir(p))
				}
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if p != abs && (pruned[name] || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			if strings.Count(p, string(os.PathSeparator))-base >= maxDepth {
				// the depth cap still lets this dir's own .giantmem count
				if isDir(filepath.Join(p, ".giantmem")) {
					add(p)
				}
				return fs.SkipDir
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// Group buckets workspace dirs into projects. Dirs need not exist on disk: a
// removed worktree still lands under its container when the container is there.
func Group(home string, dirs []string) []Project {
	byRoot := map[string]*Project{}
	var other *Project
	for _, ws := range dirs {
		root, name, kind := projectOf(home, ws)
		if kind == Other {
			if other == nil {
				other = &Project{Name: OtherName, Root: home, Kind: Other}
			}
			other.Members = append(other.Members, Member{Dir: ws, Label: otherLabel(home, ws)})
			continue
		}
		p := byRoot[root]
		if p == nil {
			p = &Project{Name: name, Root: root, Kind: kind}
			byRoot[root] = p
		}
		label := RootLabel
		if ws != root {
			label = filepath.ToSlash(rel(root, ws))
		}
		p.Members = append(p.Members, Member{Dir: ws, Label: label})
	}

	names := map[string]int{}
	for _, p := range byRoot {
		names[p.Name]++
	}
	out := make([]Project, 0, len(byRoot)+1)
	for _, p := range byRoot {
		if names[p.Name] > 1 {
			p.Name += " (" + strings.ReplaceAll(filepath.ToSlash(rel(home, p.Root)), "/", "-") + ")"
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if other != nil {
		out = append(out, *other)
	}
	for i := range out {
		m := out[i].Members
		sort.Slice(m, func(a, b int) bool { return m[a].Dir < m[b].Dir })
	}
	return out
}

func projectOf(home, ws string) (string, string, Kind) {
	repo := ""
	for a := ws; a != home && a != filepath.Dir(a); a = filepath.Dir(a) {
		if isDir(filepath.Join(a, ".bare")) {
			return a, strings.TrimSuffix(filepath.Base(a), "-wt"), Container
		}
		if repo == "" {
			if _, err := os.Stat(filepath.Join(a, ".git")); err == nil {
				repo = a
			}
		}
	}
	if repo != "" {
		return repo, filepath.Base(repo), Repo
	}
	return "", "", Other
}

func otherLabel(home, ws string) string {
	if ws == home {
		return "~"
	}
	if r := rel(home, ws); !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return ws
}

func rel(base, p string) string {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return r
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

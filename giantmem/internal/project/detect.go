package project

import (
	"os"
	"path/filepath"
	"strings"
)

// Info describes the project context for a path.
type Info struct {
	Project      string // canonical project name (consolidated to -wt when applicable)
	WorktreePath string // worktree root (real path)
	GitRoot      string // .git location's containing dir (worktree-aware)
	IsBare       bool   // true if part of a bare-with-worktrees layout
}

// Detect returns project info for cwd. archiveBase is consulted to apply the
// -wt sibling consolidation rule.
func Detect(cwd, archiveBase string) Info {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	root, gitFile, gitIsFile := findGitRoot(abs)
	if root == "" {
		// fallback: use immediate dir name
		return Info{Project: filepath.Base(abs), WorktreePath: abs}
	}

	info := Info{WorktreePath: root, GitRoot: root}

	if gitIsFile {
		// worktree: .git is a file pointing into the bare repo's worktrees/
		info.IsBare = true
		// project name = parent of worktree root
		// e.g. ~/dev/ai/chat-orchestrator-wt/main → "chat-orchestrator-wt"
		info.Project = filepath.Base(filepath.Dir(root))
	} else {
		// regular repo: project = repo dir name
		// e.g. ~/dev/ai/chat-orchestrator → "chat-orchestrator"
		info.Project = filepath.Base(root)

		// consolidation rule: if a sibling "-wt" project exists in archiveBase,
		// use that name. lets us merge plain repo data into the worktree-style
		// bucket when both exist.
		if archiveBase != "" {
			candidate := info.Project + "-wt"
			if dirExists(filepath.Join(archiveBase, candidate)) {
				info.Project = candidate
			}
		}
	}

	_ = gitFile
	return info
}

// findGitRoot walks up from start. returns (rootDir, gitPath, gitIsFile).
// rootDir is empty if no .git found.
func findGitRoot(start string) (string, string, bool) {
	cur := start
	for {
		gitPath := filepath.Join(cur, ".git")
		if st, err := os.Stat(gitPath); err == nil {
			return cur, gitPath, !st.IsDir()
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", "", false
		}
		cur = parent
	}
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// FeatureFromGiantmem returns the active feature name (status=in_progress) for
// the workspace rooted at worktreePath, or "" if none. Reads features.json.
func FeatureFromGiantmem(worktreePath string) string {
	candidates := []string{
		filepath.Join(worktreePath, ".giantmem", "features", "features.json"),
	}
	for _, p := range candidates {
		name := readActiveFeature(p)
		if name != "" {
			return name
		}
	}
	return ""
}

// readActiveFeature parses features.json (supports two known shapes) and
// returns the name of the in_progress feature, if any.
func readActiveFeature(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// crude but tolerant: find the first feature object whose status is
	// in_progress and return its name.
	// shape A: {"features": {"name": {...}, ...}} (dict-keyed)
	// shape B: {"features": [{"name": "...", ...}, ...]} (list)
	// we parse manually with the std json lib via a small helper.
	return findInProgress(b)
}

func findInProgress(b []byte) string {
	// extremely small json parser dependency-free path: use encoding/json
	// via a generic decoder.
	type genericFeature struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	type rootDict struct {
		Features map[string]genericFeature `json:"features"`
	}
	type rootList struct {
		Features []genericFeature `json:"features"`
	}

	// try dict shape
	if name := tryDict(b); name != "" {
		return name
	}
	// try list shape
	if name := tryList(b); name != "" {
		return name
	}
	return ""
}

func tryDict(b []byte) string {
	type feat struct {
		Status string `json:"status"`
	}
	type root struct {
		Features map[string]feat `json:"features"`
	}
	var r root
	if err := jsonUnmarshal(b, &r); err != nil {
		return ""
	}
	for name, f := range r.Features {
		if strings.EqualFold(f.Status, "in_progress") {
			return name
		}
	}
	return ""
}

func tryList(b []byte) string {
	type feat struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	type root struct {
		Features []feat `json:"features"`
	}
	var r root
	if err := jsonUnmarshal(b, &r); err != nil {
		return ""
	}
	for _, f := range r.Features {
		if strings.EqualFold(f.Status, "in_progress") {
			return f.Name
		}
	}
	return ""
}

package artifacts

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// FindWorkspace returns the nearest .giantmem directory walking up from
// startDir. Returns ("", false) when none found within 8 levels.
func FindWorkspace(startDir string) (string, bool) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", false
	}
	if filepath.Base(abs) == ".giantmem" {
		return abs, true
	}
	cur := abs
	for i := 0; i < 8; i++ {
		cand := filepath.Join(cur, ".giantmem")
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return "", false
}

// DetectRepoBranch returns (repo, branch) for the workspace by inspecting
// the parent git repo. Falls back to the parent dir name and "unknown".
func DetectRepoBranch(workspace string) (string, string) {
	repoRoot := workspace
	if filepath.Base(workspace) == ".giantmem" {
		repoRoot = filepath.Dir(workspace)
	}
	repo := filepath.Base(repoRoot)
	branch := "unknown"
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if out, err := cmd.Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	return repo, branch
}

// Scan walks workspace and builds an Index of all classifiable artifacts.
// Frontmatter on each file (when present) overrides path-inferred values for
// Type, Status, Feature, Domain. Tasks files auto-derive status from the
// fraction of checked checkboxes.
func Scan(workspace string) (*Index, error) {
	repo, branch := DetectRepoBranch(workspace)
	idx := &Index{
		Version:   IndexVersion,
		Repo:      repo,
		Worktree:  filepath.Dir(workspace),
		Branch:    branch,
		IndexedAt: time.Now().UTC().Format(time.RFC3339),
	}

	err := filepath.WalkDir(workspace, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".giantmem" {
				return nil
			}
			if strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			if name == "filebox" || name == "history" {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(workspace, p)
		if err != nil {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".md" && ext != ".json" && ext != ".yaml" && ext != ".yml" {
			return nil
		}
		cls, ok := Classify(rel)
		if !ok {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		mtime := info.ModTime().UTC().Format("2006-01-02")

		a := Artifact{
			Type:     cls.Type,
			Feature:  cls.Feature,
			Domain:   cls.Domain,
			Name:     cls.Name,
			Status:   "ready",
			Path:     filepath.ToSlash(rel),
			Repo:     repo,
			Branch:   branch,
			Worktree: idx.Worktree,
			Size:     info.Size(),
			Updated:  mtime,
		}

		if ext == ".md" {
			applyMarkdownFrontmatter(&a, p)
		} else if ext == ".json" {
			applyJSONFrontmatter(&a, p)
		}

		if a.Type == "tasks" {
			if status, ok := taskStatusFromFile(p); ok {
				a.Status = status
			}
		}

		if a.Lifecycle == "" {
			a.Lifecycle = defaultLifecycle(rel)
		}

		a.ID = BuildID(a)
		idx.Artifacts = append(idx.Artifacts, a)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return idx, nil
}

func applyMarkdownFrontmatter(a *Artifact, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	fm, _, ok := ParseFrontmatterBytes(raw)
	if !ok {
		return
	}
	a.HasFront = true
	applyFrontmatterMap(a, fm)
}

// applyFrontmatterMap overlays parsed YAML frontmatter onto an Artifact.
// Shared by FS Scan (applyMarkdownFrontmatter) and live-db DeriveFromLiveDoc so
// the two indexers never disagree on how frontmatter overrides path inference.
func applyFrontmatterMap(a *Artifact, fm map[string]string) {
	if v, ok := fm["type"]; ok && ValidType(v) {
		a.Type = v
	}
	if v, ok := fm["feature"]; ok && v != "" {
		a.Feature = v
	}
	if v, ok := fm["domain"]; ok && v != "" {
		a.Domain = v
	}
	if v, ok := fm["name"]; ok && v != "" {
		a.Name = v
	}
	if v, ok := fm["status"]; ok && v != "" {
		a.Status = v
	}
	if v, ok := fm["created"]; ok && v != "" {
		a.Created = v
	}
	if v, ok := fm["updated"]; ok && v != "" {
		a.Updated = v
	}
	if v, ok := fm["scope"]; ok && v != "" {
		a.Scope = v
	}
	if v, ok := fm["lifecycle"]; ok && validLifecycle(v) {
		a.Lifecycle = v
	}
}

func applyJSONFrontmatter(a *Artifact, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	applyJSONFrontmatterBytes(a, raw)
}

func applyJSONFrontmatterBytes(a *Artifact, raw []byte) {
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return
	}
	a.HasFront = true
	if v, ok := data["type"].(string); ok && ValidType(v) {
		a.Type = v
	}
	if v, ok := data["feature"].(string); ok && v != "" {
		a.Feature = v
	}
	if v, ok := data["domain"].(string); ok && v != "" {
		a.Domain = v
	}
	if v, ok := data["status"].(string); ok && v != "" {
		a.Status = v
	}
	if v, ok := data["scope"].(string); ok && v != "" {
		a.Scope = v
	}
	if v, ok := data["lifecycle"].(string); ok && validLifecycle(v) {
		a.Lifecycle = v
	}
}

var checkboxRe = regexp.MustCompile(`(?m)^\s*[-*]\s*\[([ xX])\]\s+`)

// taskStatusFromFile counts checked vs total checkboxes and returns
// draft (0%) / ready (0<x<100%) / done (100%). Returns ok=false when no
// checkboxes were found, leaving the file's own status untouched.
func taskStatusFromFile(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return taskStatusFromContent(string(raw))
}

// taskStatusFromContent derives draft/ready/done from checkbox completion in a
// tasks-file body. Mirrors taskStatusFromFile for the live-db derive path,
// where the content is already in memory.
func taskStatusFromContent(content string) (string, bool) {
	matches := checkboxRe.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return "", false
	}
	total := len(matches)
	done := 0
	for _, m := range matches {
		if len(m) > 1 && (m[1] == "x" || m[1] == "X") {
			done++
		}
	}
	if done == 0 {
		return "draft", true
	}
	if done == total {
		return "done", true
	}
	return "ready", true
}

// IndexPath returns the canonical location of the live artifacts.json for a
// workspace directory.
func IndexPath(workspace string) string {
	return filepath.Join(workspace, "artifacts.json")
}

// Save writes the Index to its canonical location with stable JSON formatting.
func Save(workspace string, idx *Index) error {
	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return os.WriteFile(IndexPath(workspace), out, 0o644)
}

// Load reads the on-disk Index for a workspace. Returns (nil, nil) when no
// index file exists yet — callers should treat that as "scan needed".
func Load(workspace string) (*Index, error) {
	raw, err := os.ReadFile(IndexPath(workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("parse index: %w", err)
	}
	return &idx, nil
}

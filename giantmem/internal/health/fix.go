package health

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Fixer applies a remediation for a single Finding. Returns:
//   ok=true if the issue was resolved
//   skipped=true if not applicable (e.g. requires interactive prompt and --auto wasn't set)
//   err on actual failure
type FixResult struct {
	Category string
	Path     string
	Message  string
	Skipped  bool
	Note     string
	Err      error
}

// FixOptions configures fix behavior.
type FixOptions struct {
	Categories map[string]bool // empty = all
	Auto       bool            // skip prompts; assume yes for orphan archive
	DryRun     bool
}

// Fix iterates findings and applies fixers. Returns per-finding results.
func Fix(findings []Finding, opt FixOptions) []FixResult {
	var out []FixResult
	for _, f := range findings {
		if len(opt.Categories) > 0 && !opt.Categories[f.Category] {
			continue
		}
		out = append(out, fixOne(f, opt))
	}
	return out
}

func fixOne(f Finding, opt FixOptions) FixResult {
	switch f.Category {
	case "symlink":
		return fixSymlink(f, opt)
	case "drift":
		return fixDrift(f, opt)
	case "worktree":
		return fixWorktree(f, opt)
	case "mcp":
		return FixResult{Category: f.Category, Path: f.Path, Skipped: true,
			Note: "manual fix: edit ~/.claude/settings.json mcpServers.giantmem-search.command to giantmem"}
	case "hook":
		return FixResult{Category: f.Category, Path: f.Path, Skipped: true,
			Note: "manual fix: add PostToolUse entry calling ~/.claude/hooks/live_index.py"}
	case "db":
		return FixResult{Category: f.Category, Path: f.Path, Skipped: true,
			Note: "DB integrity errors require manual recovery; see hint"}
	case "orphan":
		if !opt.Auto {
			return FixResult{Category: f.Category, Path: f.Path, Skipped: true,
				Note: "use --auto to archive automatically, or run giantmem archive run --project <name> " + f.Path}
		}
		return fixOrphan(f, opt)
	case "stale":
		return FixResult{Category: f.Category, Path: f.Path, Skipped: true,
			Note: "stale workspaces are info-only; add `# stale-ok` to .giantmem-ignore to silence"}
	}
	return FixResult{Category: f.Category, Path: f.Path, Skipped: true, Note: "no fixer for category"}
}

// fixSymlink rebinds <project>/latest to the newest existing timestamp dir.
func fixSymlink(f Finding, opt FixOptions) FixResult {
	link := f.Path
	projectDir := filepath.Dir(link)
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return FixResult{Category: f.Category, Path: f.Path, Err: err}
	}
	var ts []string
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) == 15 && e.Name()[8] == '_' {
			full := filepath.Join(projectDir, e.Name())
			if st, err := os.Stat(full); err == nil && st.IsDir() {
				ts = append(ts, e.Name())
			}
		}
	}
	if len(ts) == 0 {
		return FixResult{Category: f.Category, Path: f.Path, Note: "no timestamp dirs to bind"}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ts)))
	target := ts[0]
	if opt.DryRun {
		return FixResult{Category: f.Category, Path: f.Path, Note: fmt.Sprintf("would rebind to %s", target)}
	}
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		return FixResult{Category: f.Category, Path: f.Path, Err: err}
	}
	return FixResult{Category: f.Category, Path: f.Path, Note: "rebound to " + target}
}

// fixDrift kicks ingest for the missing/stale project.
func fixDrift(f Finding, opt FixOptions) FixResult {
	project := filepath.Base(f.Path)
	if opt.DryRun {
		return FixResult{Category: f.Category, Path: f.Path, Note: "would: giantmem ingest --project " + project}
	}
	cmd := exec.Command("giantmem", "ingest", "--workspaces-only", "--project", project)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return FixResult{Category: f.Category, Path: f.Path, Err: err}
	}
	return FixResult{Category: f.Category, Path: f.Path, Note: "ingested " + project}
}

// fixWorktree runs `git worktree prune` at the bare root.
func fixWorktree(f Finding, opt FixOptions) FixResult {
	// extract bare-root from hint: "git -C <root> worktree prune"
	hint := f.Hint
	parts := strings.Fields(hint)
	var bareRoot string
	for i, p := range parts {
		if p == "-C" && i+1 < len(parts) {
			bareRoot = parts[i+1]
			break
		}
	}
	if bareRoot == "" {
		return FixResult{Category: f.Category, Path: f.Path, Skipped: true, Note: "could not parse bare root from hint"}
	}
	if opt.DryRun {
		return FixResult{Category: f.Category, Path: f.Path, Note: "would: git -C " + bareRoot + " worktree prune"}
	}
	cmd := exec.Command("git", "-C", bareRoot, "worktree", "prune")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return FixResult{Category: f.Category, Path: f.Path, Err: err}
	}
	return FixResult{Category: f.Category, Path: f.Path, Note: "pruned"}
}

// fixOrphan archives the orphan .giantmem/ via the giantmem CLI.
func fixOrphan(f Finding, opt FixOptions) FixResult {
	if opt.DryRun {
		return FixResult{Category: f.Category, Path: f.Path, Note: "would archive " + f.Path}
	}
	parent := filepath.Dir(f.Path)
	cmd := exec.Command("giantmem", "archive", "run", "--no-reinit", "--project", filepath.Base(parent), f.Path)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return FixResult{Category: f.Category, Path: f.Path, Err: err}
	}
	return FixResult{Category: f.Category, Path: f.Path, Note: "archived"}
}

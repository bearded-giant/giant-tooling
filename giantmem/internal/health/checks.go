package health

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

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

// Severity levels.
const (
	SevError = "error"
	SevWarn  = "warn"
	SevInfo  = "info"
)

// Finding is a single doctor result.
type Finding struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// Options for a doctor run.
type Options struct {
	ArchiveBase string
	LiveDB      string
	ArchiveDB   string
	HomeDir     string
	Roots       []string // dirs to scan for orphan .giantmem (default ~/dev)
	StaleDays   int
}

// Run executes all checks and returns findings.
func Run(opt Options) []Finding {
	var out []Finding
	out = append(out, checkSettings(opt)...)
	out = append(out, checkDBIntegrity(opt)...)
	out = append(out, checkLatestSymlinks(opt)...)
	out = append(out, checkOrphanGiantmem(opt)...)
	out = append(out, checkOrphanWorktrees(opt)...)
	out = append(out, checkStaleWorkspaces(opt)...)
	out = append(out, checkArchiveDrift(opt)...)
	return out
}

// settings.json checks --------------------------------------------------------

var settingsHookRe = regexp.MustCompile(`live_index\.py`)
var settingsMCPRe = regexp.MustCompile(`giantmem`)

func checkSettings(opt Options) []Finding {
	var out []Finding
	settingsPath := filepath.Join(opt.HomeDir, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		out = append(out, Finding{
			Severity: SevError,
			Category: "settings",
			Message:  "cannot read ~/.claude/settings.json",
			Path:     settingsPath,
			Hint:     err.Error(),
		})
		return out
	}
	if !settingsHookRe.Match(raw) {
		out = append(out, Finding{
			Severity: SevError,
			Category: "hook",
			Message:  "PostToolUse hook for live_index.py not wired into settings.json",
			Path:     settingsPath,
			Hint:     "add the PostToolUse entry that calls ~/.claude/hooks/live_index.py",
		})
	}
	var parsed struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil {
		entry, ok := parsed.MCPServers["giantmem-search"]
		if !ok {
			out = append(out, Finding{
				Severity: SevWarn,
				Category: "mcp",
				Message:  "MCP server 'giantmem-search' not registered",
				Path:     settingsPath,
				Hint:     `add { "command": "giantmem", "args": ["mcp","serve"] } under mcpServers`,
			})
		} else if !strings.Contains(entry.Command, "giantmem") {
			out = append(out, Finding{
				Severity: SevError,
				Category: "mcp",
				Message:  fmt.Sprintf("MCP entry points at %q, not the giantmem binary", entry.Command),
				Path:     settingsPath,
				Hint:     "update mcpServers.giantmem-search.command to giantmem",
			})
		}
	}
	return out
}

// DB integrity ---------------------------------------------------------------

func checkDBIntegrity(opt Options) []Finding {
	var out []Finding
	for _, p := range []string{opt.ArchiveDB, opt.LiveDB} {
		if _, err := os.Stat(p); err != nil {
			continue // missing DB is its own concern, not corruption
		}
		d, err := db.Open(p)
		if err != nil {
			out = append(out, Finding{
				Severity: SevError,
				Category: "db",
				Message:  "cannot open db",
				Path:     p,
				Hint:     err.Error(),
			})
			continue
		}
		var result string
		err = d.QueryRow("PRAGMA integrity_check").Scan(&result)
		d.Close()
		if err != nil {
			out = append(out, Finding{
				Severity: SevError,
				Category: "db",
				Message:  "integrity_check query failed",
				Path:     p,
				Hint:     err.Error(),
			})
			continue
		}
		if result != "ok" {
			out = append(out, Finding{
				Severity: SevError,
				Category: "db",
				Message:  "integrity_check returned: " + result,
				Path:     p,
				Hint:     "consider rebuilding via giantmem ingest --force",
			})
		}
	}
	return out
}

// Broken `latest` symlinks ---------------------------------------------------

func checkLatestSymlinks(opt Options) []Finding {
	var out []Finding
	if _, err := os.Stat(opt.ArchiveBase); err != nil {
		return out
	}
	filepath.WalkDir(opt.ArchiveBase, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() != "latest" {
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			out = append(out, Finding{Severity: SevError, Category: "symlink", Message: "cannot read symlink", Path: p, Hint: err.Error()})
			return nil
		}
		resolved := filepath.Join(filepath.Dir(p), target)
		if _, err := os.Stat(resolved); err != nil {
			out = append(out, Finding{
				Severity: SevError,
				Category: "symlink",
				Message:  fmt.Sprintf("latest -> %s does not exist", target),
				Path:     p,
				Hint:     "rerun giantmem archive run, or manually rebind the symlink",
			})
		}
		return nil
	})
	return out
}

// Orphan .giantmem/ (no .git in path) ----------------------------------------

func checkOrphanGiantmem(opt Options) []Finding {
	var out []Finding
	for _, root := range opt.Roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			name := d.Name()
			if name == "node_modules" || name == ".git" || name == ".venv" || name == "venv" {
				return fs.SkipDir
			}
			if name != ".giantmem" {
				return nil
			}
			ig := LoadIgnoreFor(p)
			if ig.OrphanOK {
				return fs.SkipDir
			}
			if !hasGitAncestor(filepath.Dir(p), root) {
				out = append(out, Finding{
					Severity: SevWarn,
					Category: "orphan",
					Message:  ".giantmem/ exists but no .git in any ancestor (worktree removed?)",
					Path:     p,
					Hint:     "run: giantmem archive run --project <name> " + p + "  to capture before deleting (or add `# orphan-ok` to .giantmem-ignore)",
				})
			}
			return fs.SkipDir
		})
	}
	return out
}

func hasGitAncestor(start, root string) bool {
	cur := start
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur || !strings.HasPrefix(cur, root) {
			return false
		}
		cur = parent
	}
}

// Orphan worktrees (git lists them but path missing) -------------------------

func checkOrphanWorktrees(opt Options) []Finding {
	var out []Finding
	for _, root := range opt.Roots {
		// walk one level deep finding bare-with-worktrees layouts (.bare sibling)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		var stack []string
		for _, e := range entries {
			if e.IsDir() {
				stack = append(stack, filepath.Join(root, e.Name()))
			}
		}
		for len(stack) > 0 {
			d := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if isBareWorktreeRoot(d) {
				out = append(out, scanBareWorktreeOrphans(d)...)
				continue
			}
			// recurse one more level
			subs, err := os.ReadDir(d)
			if err != nil {
				continue
			}
			depth := strings.Count(strings.TrimPrefix(d, root), string(filepath.Separator))
			if depth >= 3 {
				continue
			}
			for _, s := range subs {
				if s.IsDir() && !strings.HasPrefix(s.Name(), ".") {
					stack = append(stack, filepath.Join(d, s.Name()))
				}
			}
		}
	}
	return out
}

func isBareWorktreeRoot(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".bare"))
	return err == nil && st.IsDir()
}

func scanBareWorktreeOrphans(bareRoot string) []Finding {
	var out []Finding
	cmd := exec.Command("git", "-C", bareRoot, "worktree", "list", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		path := strings.TrimPrefix(line, "worktree ")
		if _, err := os.Stat(path); err != nil {
			out = append(out, Finding{
				Severity: SevWarn,
				Category: "worktree",
				Message:  "git tracks worktree but directory is gone",
				Path:     path,
				Hint:     "git -C " + bareRoot + " worktree prune",
			})
		}
	}
	return out
}

// Stale workspaces (newest md > N days old) ----------------------------------

func checkStaleWorkspaces(opt Options) []Finding {
	cutoff := time.Now().AddDate(0, 0, -opt.StaleDays)
	var out []Finding
	for _, root := range opt.Roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			name := d.Name()
			if name == "node_modules" || name == ".git" || name == ".venv" || name == "venv" {
				return fs.SkipDir
			}
			if name != ".giantmem" {
				return nil
			}
			ig := LoadIgnoreFor(p)
			if ig.StaleOK {
				return fs.SkipDir
			}
			latest := newestMD(p)
			if !latest.IsZero() && latest.Before(cutoff) {
				days := int(time.Since(latest).Hours() / 24)
				out = append(out, Finding{
					Severity: SevInfo,
					Category: "stale",
					Message:  fmt.Sprintf("workspace inactive for %d days", days),
					Path:     p,
					Hint:     "consider giantmem archive run from its parent dir (or add `# stale-ok` to .giantmem-ignore)",
				})
			}
			return fs.SkipDir
		})
	}
	return out
}

func newestMD(root string) time.Time {
	var newest time.Time
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}

// archives.db drift ----------------------------------------------------------
// Detect: live workspaces with files newer than archives.db's most recent
// indexed_at for that project. Indicates ingest is overdue.

func checkArchiveDrift(opt Options) []Finding {
	var out []Finding
	if _, err := os.Stat(opt.ArchiveDB); err != nil {
		return out
	}
	d, err := db.Open(opt.ArchiveDB)
	if err != nil {
		return out
	}
	defer d.Close()
	rows, err := d.Query(`SELECT project, MAX(indexed_at) FROM documents WHERE source_type != 'session' GROUP BY project`)
	if err != nil {
		return out
	}
	indexed := map[string]time.Time{}
	for rows.Next() {
		var proj, ia string
		if err := rows.Scan(&proj, &ia); err != nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, ia)
		if err != nil {
			continue
		}
		indexed[proj] = t
	}
	rows.Close()

	// for each archived project dir, compare its newest md mtime to indexed time
	if _, err := os.Stat(opt.ArchiveBase); err != nil {
		return out
	}
	entries, _ := os.ReadDir(opt.ArchiveBase)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		proj := e.Name()
		t, ok := indexed[proj]
		if !ok {
			out = append(out, Finding{
				Severity: SevWarn,
				Category: "drift",
				Message:  fmt.Sprintf("archive project %q has no rows in archives.db", proj),
				Path:     filepath.Join(opt.ArchiveBase, proj),
				Hint:     "giantmem ingest --project " + proj,
			})
			continue
		}
		newest := newestMD(filepath.Join(opt.ArchiveBase, proj))
		if !newest.IsZero() && newest.After(t.Add(time.Hour)) {
			out = append(out, Finding{
				Severity: SevWarn,
				Category: "drift",
				Message:  fmt.Sprintf("project %q has files newer than last ingest (%s vs indexed %s)", proj, newest.Format(time.RFC3339), t.Format(time.RFC3339)),
				Path:     filepath.Join(opt.ArchiveBase, proj),
				Hint:     "giantmem ingest --project " + proj,
			})
		}
	}
	return out
}

// Summary helpers ------------------------------------------------------------

// Summary counts findings by severity.
type Summary struct {
	Errors int `json:"errors"`
	Warns  int `json:"warns"`
	Infos  int `json:"infos"`
	Total  int `json:"total"`
}

// Summarize returns counts.
func Summarize(findings []Finding) Summary {
	var s Summary
	for _, f := range findings {
		s.Total++
		switch f.Severity {
		case SevError:
			s.Errors++
		case SevWarn:
			s.Warns++
		case SevInfo:
			s.Infos++
		}
	}
	return s
}

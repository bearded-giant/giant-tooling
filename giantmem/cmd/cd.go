package cmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	cdRefresh bool
	cdRoots   []string
	cdNoFzf   bool
)

var cdCmd = &cobra.Command{
	Use:   "cd <pattern>",
	Short: "Print best-match worktree path for cd-ing into. Use shell wrapper gj() to actually cd.",
	Long: `Fuzzy-jump to a worktree across all bare-with-worktrees layouts under the
given roots (default ~/dev). Prints the path; pair with the gj() shell function
that giantmem worktree shell-init emits.

Match priority:
  1. exact basename of worktree dir
  2. <project>/<branch> substring (e.g. "orch/main")
  3. branch name substring across all worktrees

Multi-match opens fzf if available, or prints all candidates with --no-fzf.

Worktree list is cached at ~/.cache/giantmem/worktrees.json. Use --refresh to
rebuild the cache.`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := strings.Join(args, " ")
		home, _ := os.UserHomeDir()
		if len(cdRoots) == 0 {
			cdRoots = []string{filepath.Join(home, "dev")}
		}
		entries, err := loadOrBuildCache(home, cdRoots, cdRefresh)
		if err != nil {
			return err
		}
		matches := matchEntries(entries, pattern)
		if len(matches) == 0 {
			return fmt.Errorf("no worktree matches %q", pattern)
		}
		if len(matches) == 1 {
			fmt.Println(matches[0].Path)
			return nil
		}
		// multi-match
		if !cdNoFzf {
			if path, ok := fzfPick(matches); ok {
				fmt.Println(path)
				return nil
			}
		}
		// fzf unavailable / cancelled / --no-fzf: print all
		for _, m := range matches {
			fmt.Println(m.Path)
		}
		if cdNoFzf {
			return nil
		}
		return fmt.Errorf("multiple matches; install fzf or use --no-fzf and pick one")
	},
}

type wtCacheEntry struct {
	Path     string `json:"path"`
	Project  string `json:"project"`
	Branch   string `json:"branch"`
	Basename string `json:"basename"`
}

type wtCache struct {
	BuiltAt string         `json:"built_at"`
	Roots   []string       `json:"roots"`
	Entries []wtCacheEntry `json:"entries"`
}

func cachePath(home string) string {
	return filepath.Join(home, ".cache", "giantmem", "worktrees.json")
}

func loadOrBuildCache(home string, roots []string, force bool) ([]wtCacheEntry, error) {
	cp := cachePath(home)
	if !force {
		if raw, err := os.ReadFile(cp); err == nil {
			var c wtCache
			if err := json.Unmarshal(raw, &c); err == nil {
				// stale if older than 6h
				if t, err := time.Parse(time.RFC3339, c.BuiltAt); err == nil && time.Since(t) < 6*time.Hour {
					return c.Entries, nil
				}
			}
		}
	}
	entries := scanWorktrees(roots)
	c := wtCache{
		BuiltAt: time.Now().UTC().Format(time.RFC3339),
		Roots:   roots,
		Entries: entries,
	}
	if err := os.MkdirAll(filepath.Dir(cp), 0o755); err == nil {
		if data, err := json.MarshalIndent(c, "", "  "); err == nil {
			os.WriteFile(cp, data, 0o644)
		}
	}
	return entries, nil
}

// scanWorktrees finds bare-with-worktrees layouts under roots and emits one
// entry per checked-out branch. Also includes regular repos (single entry per
// repo).
func scanWorktrees(roots []string) []wtCacheEntry {
	seen := map[string]bool{}
	var out []wtCacheEntry

	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			name := d.Name()
			if name == "node_modules" || name == ".venv" || name == "venv" {
				return fs.SkipDir
			}
			if name == ".git" || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			// detect bare-with-worktrees: has .bare sibling
			if _, err := os.Stat(filepath.Join(p, ".bare")); err == nil {
				out = append(out, listBareWorktrees(p)...)
				return fs.SkipDir // each worktree gets descended into via list output
			}
			// detect regular repo
			if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
				if !seen[p] {
					seen[p] = true
					out = append(out, wtCacheEntry{
						Path:     p,
						Project:  filepath.Base(p),
						Branch:   "",
						Basename: filepath.Base(p),
					})
				}
				return fs.SkipDir
			}
			// shallow recursion: don't descend more than 4 levels
			depth := strings.Count(strings.TrimPrefix(p, root), string(filepath.Separator))
			if depth >= 4 {
				return fs.SkipDir
			}
			return nil
		})
	}
	// dedupe by path
	dedup := []wtCacheEntry{}
	for _, e := range out {
		if seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		dedup = append(dedup, e)
	}
	sort.Slice(dedup, func(i, j int) bool { return dedup[i].Path < dedup[j].Path })
	return dedup
}

func listBareWorktrees(bareRoot string) []wtCacheEntry {
	// point git at the actual bare dir; running from the parent fails because
	// it has no .git of its own.
	cmd := exec.Command("git", "-C", filepath.Join(bareRoot, ".bare"), "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var entries []wtCacheEntry
	var cur wtCacheEntry
	flush := func() {
		if cur.Path == "" {
			return
		}
		// skip the .bare entry itself
		if !strings.HasSuffix(cur.Path, ".bare") {
			cur.Project = filepath.Base(bareRoot)
			cur.Basename = filepath.Base(cur.Path)
			entries = append(entries, cur)
		}
		cur = wtCacheEntry{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	flush()
	return entries
}

// matchEntries ranks entries by match strength.
func matchEntries(entries []wtCacheEntry, pattern string) []wtCacheEntry {
	low := strings.ToLower(pattern)
	type scored struct {
		entry wtCacheEntry
		score int
	}
	var hits []scored
	for _, e := range entries {
		base := strings.ToLower(e.Basename)
		proj := strings.ToLower(e.Project)
		branch := strings.ToLower(e.Branch)
		key := proj + "/" + branch
		score := 0
		switch {
		case base == low:
			score = 100
		case proj == low:
			score = 90
		case branch == low:
			score = 85
		case strings.Contains(key, low):
			score = 70
		case strings.Contains(branch, low):
			score = 60
		case strings.Contains(proj, low):
			score = 50
		case strings.Contains(base, low):
			score = 40
		}
		if score > 0 {
			hits = append(hits, scored{e, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].entry.Path < hits[j].entry.Path
	})
	// when top match is a strong exact hit, return only it
	if len(hits) > 0 && hits[0].score >= 90 && (len(hits) == 1 || hits[1].score < hits[0].score) {
		return []wtCacheEntry{hits[0].entry}
	}
	out := make([]wtCacheEntry, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.entry)
	}
	return out
}

func fzfPick(entries []wtCacheEntry) (string, bool) {
	fzf, err := exec.LookPath("fzf")
	if err != nil {
		return "", false
	}
	var input strings.Builder
	for _, e := range entries {
		input.WriteString(fmt.Sprintf("%s\t%s\t%s\n", e.Path, e.Project, e.Branch))
	}
	cmd := exec.Command(fzf,
		"--delimiter", "\t",
		"--with-nth", "2,3,1",
		"--header", "pick worktree",
	)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return "", false
	}
	fields := strings.Split(line, "\t")
	return fields[0], true
}

func init() {
	cdCmd.Flags().BoolVar(&cdRefresh, "refresh", false, "rebuild the worktree cache before matching")
	cdCmd.Flags().StringSliceVar(&cdRoots, "root", nil, "roots to scan (default ~/dev)")
	cdCmd.Flags().BoolVar(&cdNoFzf, "no-fzf", false, "print all candidates instead of opening fzf")
	rootCmd.AddCommand(cdCmd)
}

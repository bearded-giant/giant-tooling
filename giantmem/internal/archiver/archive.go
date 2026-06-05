package archiver

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/ingest"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
)

var TimestampRe = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}$`)

// IngestProject re-indexes the project's archive rows into archives.db using
// the native Go ingester. Runs in a background goroutine.
func IngestProject(_unused, projectName string) {
	go func() {
		home, _ := os.UserHomeDir()
		archiveBase := os.Getenv("GIANTMEM_ARCHIVE_BASE")
		if archiveBase == "" {
			archiveBase = filepath.Join(home, "giantmem_archive")
		}
		dbPath := filepath.Join(archiveBase, "archives.db")
		d, err := db.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: ingest open db: %v\n", err)
			return
		}
		defer d.Close()
		_, err = ingest.Run(d, ingest.Options{
			ArchiveBase:    archiveBase,
			ClaudeProjects: filepath.Join(home, ".claude", "projects"),
			Project:        projectName,
			WorkspacesOnly: true,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: ingest failed: %v\n", err)
		}
	}()
}

// Run archives src into archiveBase/<project>/<ts>/.
func Run(src, archiveBase, projectOverride string, dryRun, reinit bool) (string, error) {
	if src == "" {
		if dirExists(filepath.Join(cwd(), ".giantmem")) {
			src = filepath.Join(cwd(), ".giantmem")
		} else if dirExists(filepath.Join(cwd(), "scratch")) {
			src = filepath.Join(cwd(), "scratch")
		} else {
			return "", fmt.Errorf("no .giantmem directory in current dir")
		}
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	if !dirExists(abs) {
		return "", fmt.Errorf("not a directory: %s", abs)
	}

	projectName := projectOverride
	if projectName == "" {
		info := project.Detect(filepath.Dir(abs), archiveBase)
		projectName = info.Project
	}

	projectDir := filepath.Join(archiveBase, projectName)
	ts := time.Now().Format("20060102_150405")
	destDir := filepath.Join(projectDir, ts)

	size := dirSize(abs)
	fmt.Printf("Archive: %s (%s)\n", abs, humanSize(size))
	fmt.Printf("     to: %s\n", destDir)

	if dryRun {
		fmt.Println("(dry run, not moved)")
		return destDir, nil
	}

	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(abs, destDir); err != nil {
		return "", fmt.Errorf("rename: %w", err)
	}

	if err := buildLegacyIndex(destDir); err != nil {
		fmt.Fprintf(os.Stderr, "warn: legacy index: %v\n", err)
	}
	if err := updateLatest(projectDir, ts); err != nil {
		fmt.Fprintf(os.Stderr, "warn: latest symlink: %v\n", err)
	}

	if err := pruneLiveDocsUnder(abs); err != nil {
		fmt.Fprintf(os.Stderr, "warn: prune live.db: %v\n", err)
	}

	// archives.db workspace ingest deprecated — live.db is now authoritative
	// for .giantmem/ content (backfill walker covers every file). Cold archive
	// dirs at {project}/{ts}/ stay for filesystem-level recovery only.

	if reinit {
		parent := filepath.Dir(abs)
		if err := reinitWorkspace(parent); err != nil {
			fmt.Fprintf(os.Stderr, "warn: re-init: %v\n", err)
		}
	}
	fmt.Printf("Archived to %s (latest -> %s)\n", destDir, ts)
	return destDir, nil
}

// List shows archives.
func List(archiveBase, projectName string) error {
	if !dirExists(archiveBase) {
		return fmt.Errorf("archive base not found: %s", archiveBase)
	}
	if projectName != "" {
		dir := filepath.Join(archiveBase, projectName)
		if !dirExists(dir) {
			return fmt.Errorf("project not found: %s", projectName)
		}
		fmt.Printf("Archives in %s:\n", dir)
		return listProject(dir)
	}
	fmt.Printf("Archives in %s:\n", archiveBase)
	return listAllProjects(archiveBase)
}

func listAllProjects(base string) error {
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	type row struct {
		name  string
		count int
	}
	var rows []row
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		c := countTimestamps(filepath.Join(base, e.Name()))
		rows = append(rows, row{e.Name(), c})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for _, r := range rows {
		fmt.Printf("  %s: %d archive(s)\n", r.name, r.count)
	}
	return nil
}

func listProject(projectDir string) error {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return err
	}
	latestTarget := readLatest(projectDir)
	for _, e := range entries {
		if !e.IsDir() || !TimestampRe.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(projectDir, e.Name())
		size := dirSize(path)
		marker := ""
		if e.Name() == latestTarget {
			marker = " <- latest"
		}
		fmt.Printf("  %s (%s)%s\n", e.Name(), humanSize(size), marker)
	}
	return nil
}

// Open opens the archive in Finder (macOS).
func Open(archiveBase, projectName, ts string) error {
	target := filepath.Join(archiveBase, projectName)
	if ts != "" {
		target = filepath.Join(target, ts)
	} else {
		latest := filepath.Join(target, "latest")
		if _, err := os.Lstat(latest); err == nil {
			target = latest
		}
	}
	if !dirExists(target) {
		return fmt.Errorf("not found: %s", target)
	}
	fmt.Printf("Opening: %s\n", target)
	return exec.Command("open", target).Run()
}

// Dedup moves older duplicate files (same relative path) into _review/.
func Dedup(archiveBase, projectName string, dryRun bool) error {
	projectDir := filepath.Join(archiveBase, projectName)
	if !dirExists(projectDir) {
		return fmt.Errorf("project not found: %s", projectName)
	}
	tsDirs, err := timestampDirs(projectDir)
	if err != nil {
		return err
	}
	if len(tsDirs) < 2 {
		fmt.Printf("need at least 2 archives to dedup (found %d)\n", len(tsDirs))
		return nil
	}
	sort.Slice(tsDirs, func(i, j int) bool { return tsDirs[i] > tsDirs[j] })

	reviewDir := filepath.Join(projectDir, "_review")
	seen := map[string]bool{}
	moved := 0

	for _, tsDir := range tsDirs {
		full := filepath.Join(projectDir, tsDir)
		err := filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(full, p)
			if err != nil {
				return nil
			}
			if strings.HasPrefix(rel, "features/") || rel == ".giantmem-index" {
				return nil
			}
			if seen[rel] {
				if dryRun {
					fmt.Printf("  [dup] %s/%s\n", tsDir, rel)
				} else {
					dest := filepath.Join(reviewDir, tsDir, rel)
					if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
						return nil
					}
					if err := os.Rename(p, dest); err != nil {
						return nil
					}
				}
				moved++
			} else {
				seen[rel] = true
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	if dryRun {
		fmt.Printf("Found %d older duplicate(s). Run without --dry-run to move.\n", moved)
		return nil
	}
	if moved == 0 {
		fmt.Println("No duplicates found")
		return nil
	}
	fmt.Printf("Moved %d duplicate(s) to %s\n", moved, reviewDir)
	fmt.Printf("Review and delete when satisfied: rm -rf %s\n", reviewDir)
	return nil
}

// StaleResult describes a workspace that may be archive-eligible.
type StaleResult struct {
	Path         string
	Project      string
	WorktreePath string
	Worktree     string
	LastWrite    time.Time
	AgeDays      int
	Size         int64
}

// Stale scans roots for live `.giantmem/` directories whose newest md
// modification is older than minAgeDays.
func Stale(roots []string, archiveBase string, minAgeDays int) ([]StaleResult, error) {
	cutoff := time.Now().AddDate(0, 0, -minAgeDays)
	var out []StaleResult
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
			latest := newestMD(p)
			if latest.IsZero() || latest.After(cutoff) {
				return fs.SkipDir
			}
			info := project.Detect(filepath.Dir(p), archiveBase)
			wt := "ok"
			if !dirExists(info.WorktreePath) {
				wt = "missing"
			}
			out = append(out, StaleResult{
				Path:         p,
				Project:      info.Project,
				WorktreePath: info.WorktreePath,
				Worktree:     wt,
				LastWrite:    latest,
				AgeDays:      int(time.Since(latest).Hours() / 24),
				Size:         dirSize(p),
			})
			return fs.SkipDir
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastWrite.Before(out[j].LastWrite) })
	return out, nil
}

// helpers ------------------------------------------------------------------

func cwd() string {
	d, _ := os.Getwd()
	return d
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func dirSize(p string) int64 {
	var total int64
	filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func humanSize(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%dB", n)
	}
	units := []string{"K", "M", "G", "T"}
	val := float64(n) / k
	u := 0
	for val >= k && u < len(units)-1 {
		val /= k
		u++
	}
	return fmt.Sprintf("%.1f%s", val, units[u])
}

func countTimestamps(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	c := 0
	for _, e := range entries {
		if e.IsDir() && TimestampRe.MatchString(e.Name()) {
			c++
		}
	}
	return c
}

func timestampDirs(projectDir string) ([]string, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && TimestampRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func readLatest(projectDir string) string {
	link := filepath.Join(projectDir, "latest")
	target, err := os.Readlink(link)
	if err != nil {
		return ""
	}
	return target
}

func updateLatest(projectDir, ts string) error {
	link := filepath.Join(projectDir, "latest")
	_ = os.Remove(link)
	return os.Symlink(ts, link)
}

func buildLegacyIndex(archiveDir string) error {
	indexPath := filepath.Join(archiveDir, ".giantmem-index")
	rg, err := exec.LookPath("rg")
	if err != nil {
		return nil
	}
	out, err := os.Create(indexPath)
	if err != nil {
		return err
	}
	defer out.Close()
	cmd := exec.Command(rg, "-n", "--no-ignore", "--glob", "*.md", "--glob", "domains/*.json", ".", archiveDir)
	cmd.Stdout = out
	cmd.Stderr = nil
	_ = cmd.Run()
	return nil
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

func reinitWorkspace(dir string) error {
	lib := WorkspaceLibPath()
	if _, err := os.Stat(lib); err != nil {
		return fmt.Errorf("workspace-lib not found at %s", lib)
	}
	script := fmt.Sprintf(`source %q && workspace_init %q "$(basename %q)"`, lib, dir, dir)
	cmd := exec.Command("bash", "-c", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// pruneLiveDocsUnder removes live_docs rows whose path is under the (now-moved)
// workspace dir. Triggers cascade FTS row cleanup.
func pruneLiveDocsUnder(archivedPath string) error {
	home, _ := os.UserHomeDir()
	base := os.Getenv("GIANTMEM_ARCHIVE_BASE")
	if base == "" {
		base = filepath.Join(home, "giantmem_archive")
	}
	livePath := filepath.Join(base, "live.db")
	if _, err := os.Stat(livePath); err != nil {
		return nil
	}
	d, err := db.Open(livePath)
	if err != nil {
		return err
	}
	defer d.Close()
	worktree := filepath.Dir(archivedPath)
	res, err := d.Exec(
		`DELETE FROM live_docs WHERE worktree_path = ? OR path LIKE ?`,
		worktree, archivedPath+string(filepath.Separator)+"%",
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		fmt.Fprintf(os.Stderr, "pruned %d live.db rows under %s\n", n, archivedPath)
	}
	return nil
}

// WorkspaceLibPath returns the location of workspace-lib.sh.
func WorkspaceLibPath() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".claude", "lib", "workspace", "workspace-lib.sh"),
		filepath.Join(home, "dev", "giant-tooling", "workspace", "workspace-lib.sh"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

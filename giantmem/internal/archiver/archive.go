package archiver

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/backfill"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
)

var TimestampRe = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}$`)

// RunAll wipes the entire .giantmem/ at src and reinits a fresh one in place,
// AFTER verifying every file is mirrored in live.db. The live_docs rows are
// kept — live.db is the durable archive (protected by the db-backup cron).
// Replaces the legacy mv-to-archive behavior; no FS snapshot is taken.
func RunAll(src, archiveBase string, dryRun, reinit bool) error {
	if src == "" {
		if dirExists(filepath.Join(cwd(), ".giantmem")) {
			src = filepath.Join(cwd(), ".giantmem")
		} else if dirExists(filepath.Join(cwd(), "scratch")) {
			src = filepath.Join(cwd(), "scratch")
		} else {
			return fmt.Errorf("no .giantmem directory in current dir")
		}
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if !dirExists(abs) {
		return fmt.Errorf("not a directory: %s", abs)
	}

	size := dirSize(abs)
	fmt.Printf("Wipe: %s (%s)\n", abs, humanSize(size))

	if dryRun {
		fmt.Println("(dry run, nothing removed)")
		return nil
	}

	captured, missing, verr := captureAndVerify(abs)
	if verr != nil {
		return fmt.Errorf("verify live.db capture: %w", verr)
	}
	if len(missing) > 0 {
		return fmt.Errorf("refusing to wipe: %d of %d file(s) not captured in live.db (e.g. %s)",
			len(missing), captured+len(missing), missing[0])
	}
	fmt.Printf("verified %d file(s) in live.db; removing dir (rows kept)\n", captured)
	if err := os.RemoveAll(abs); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	if reinit {
		parent := filepath.Dir(abs)
		if err := reinitWorkspace(parent); err != nil {
			fmt.Fprintf(os.Stderr, "warn: re-init: %v\n", err)
		}
	}
	fmt.Printf("Wiped %s\n", abs)
	_ = archiveBase
	return nil
}

// FeatureResult reports one feature's archive outcome.
type FeatureResult struct {
	Name     string
	Status   string // before
	Action   string // "archived", "skipped", "error"
	Reason   string
	Captured int // files verified present in live.db before delete
}

// ArchiveFeature archives a single feature: verifies its files are mirrored in
// live.db, removes its dir (live_docs rows kept as the durable record), and
// patches features.json to status=archived.
func ArchiveFeature(workspaceDir, name string, force, dryRun bool) (FeatureResult, error) {
	res := FeatureResult{Name: name}
	ws, err := resolveWorkspace(workspaceDir)
	if err != nil {
		return res, err
	}
	featDir := filepath.Join(ws, "features", name)
	if !dirExists(featDir) {
		return res, fmt.Errorf("feature dir not found: %s", featDir)
	}

	featuresJSON := filepath.Join(ws, "features", "features.json")
	entries, err := readFeaturesJSON(featuresJSON)
	if err != nil {
		return res, fmt.Errorf("read features.json: %w", err)
	}
	meta, ok := entries[name]
	if !ok {
		return res, fmt.Errorf("feature %q not in features.json", name)
	}
	statusBefore, _ := meta["status"].(string)
	res.Status = statusBefore

	if !strings.EqualFold(statusBefore, "complete") && !force {
		res.Action = "skipped"
		res.Reason = fmt.Sprintf("status=%s (use --force)", statusBefore)
		return res, nil
	}
	if strings.EqualFold(statusBefore, "archived") {
		res.Action = "skipped"
		res.Reason = "already archived"
		return res, nil
	}

	if dryRun {
		res.Action = "would-archive"
		return res, nil
	}

	captured, missing, verr := captureAndVerify(featDir)
	if verr != nil {
		res.Action = "error"
		res.Reason = fmt.Sprintf("verify live.db: %v", verr)
		return res, verr
	}
	if len(missing) > 0 {
		res.Action = "error"
		res.Reason = fmt.Sprintf("%d file(s) not captured in live.db (e.g. %s)", len(missing), filepath.Base(missing[0]))
		return res, fmt.Errorf("%s", res.Reason)
	}
	res.Captured = captured

	if err := os.RemoveAll(featDir); err != nil {
		res.Action = "error"
		res.Reason = err.Error()
		return res, err
	}

	meta["status"] = "archived"
	meta["archived"] = time.Now().UTC().Format("2006-01-02")
	entries[name] = meta
	if err := writeFeaturesJSON(featuresJSON, entries); err != nil {
		res.Action = "error"
		res.Reason = fmt.Sprintf("write features.json: %v", err)
		return res, err
	}
	res.Action = "archived"
	return res, nil
}

// ArchiveCompleted archives every status=complete feature (or all
// non-archived when force=true).
func ArchiveCompleted(workspaceDir string, force, dryRun bool) ([]FeatureResult, error) {
	ws, err := resolveWorkspace(workspaceDir)
	if err != nil {
		return nil, err
	}
	featuresJSON := filepath.Join(ws, "features", "features.json")
	entries, err := readFeaturesJSON(featuresJSON)
	if err != nil {
		return nil, fmt.Errorf("read features.json: %w", err)
	}

	var names []string
	for name, meta := range entries {
		status, _ := meta["status"].(string)
		if strings.EqualFold(status, "archived") {
			continue
		}
		if !force && !strings.EqualFold(status, "complete") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	var out []FeatureResult
	for _, n := range names {
		r, err := ArchiveFeature(ws, n, force, dryRun)
		if err != nil {
			r.Action = "error"
			r.Reason = err.Error()
		}
		out = append(out, r)
	}
	return out, nil
}

// resolveWorkspace returns the abs path to the .giantmem dir for the given
// workspace (which may be the worktree, the .giantmem itself, or empty=cwd).
func resolveWorkspace(workspaceDir string) (string, error) {
	if workspaceDir == "" {
		workspaceDir = cwd()
	}
	abs, err := filepath.Abs(workspaceDir)
	if err != nil {
		return "", err
	}
	if filepath.Base(abs) == ".giantmem" && dirExists(abs) {
		return abs, nil
	}
	ws := filepath.Join(abs, ".giantmem")
	if !dirExists(ws) {
		return "", fmt.Errorf("no .giantmem at %s", ws)
	}
	return ws, nil
}

// readFeaturesJSON parses the flat-dict shape: {"<name>": {...}, ...}
func readFeaturesJSON(path string) (map[string]map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func writeFeaturesJSON(path string, entries map[string]map[string]any) error {
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
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

// captureAndVerify backfills the enclosing .giantmem into live.db, then confirms
// every non-empty file under dirPath is mirrored there (content-length match).
// The caller MUST NOT delete on disk when missing is non-empty — those files
// have no durable copy and would be lost. Returns the count verified and the
// list still uncaptured (e.g. files over backfill's 5MB cap).
func captureAndVerify(dirPath string) (captured int, missing []string, err error) {
	base := archiveBaseDir()
	livePath := filepath.Join(base, "live.db")
	if _, serr := os.Stat(livePath); serr != nil {
		return 0, nil, fmt.Errorf("live.db not found at %s", livePath)
	}
	d, oerr := db.Open(livePath)
	if oerr != nil {
		return 0, nil, oerr
	}
	defer d.Close()

	if ws := enclosingGiantmem(dirPath); ws != "" {
		if _, berr := backfill.RunOnWorkspace(d, base, ws); berr != nil {
			fmt.Fprintf(os.Stderr, "warn: backfill before verify: %v\n", berr)
		}
	}

	_ = filepath.WalkDir(dirPath, func(p string, de fs.DirEntry, werr error) error {
		if werr != nil || de.IsDir() {
			return nil
		}
		name := de.Name()
		if name == ".giantmem-index" || name == ".DS_Store" || strings.HasPrefix(name, ".") {
			return nil
		}
		info, ierr := de.Info()
		if ierr != nil || info.Size() == 0 {
			return nil
		}
		var n int64
		if scanErr := d.QueryRow(
			"SELECT octet_length(content) FROM live_docs WHERE path = ?", p,
		).Scan(&n); scanErr != nil || n != info.Size() {
			missing = append(missing, p)
			return nil
		}
		captured++
		return nil
	})
	return captured, missing, nil
}

func archiveBaseDir() string {
	base := os.Getenv("GIANTMEM_ARCHIVE_BASE")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "giantmem_archive")
	}
	return base
}

// enclosingGiantmem returns the .giantmem dir at or above p, or "" if none.
func enclosingGiantmem(p string) string {
	p = filepath.Clean(p)
	if filepath.Base(p) == ".giantmem" {
		return p
	}
	marker := string(filepath.Separator) + ".giantmem" + string(filepath.Separator)
	if i := strings.LastIndex(p, marker); i >= 0 {
		return p[:i] + string(filepath.Separator) + ".giantmem"
	}
	return ""
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

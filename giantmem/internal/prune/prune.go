package prune

import (
	"bufio"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed prune.sh
var scriptFS embed.FS

// Bands are the age cutoffs the planner reports, in days. 0 = no age bound.
var Bands = []int{0, 30, 90, 180, 365}

type Band struct {
	Days  int   `json:"days"`
	Docs  int   `json:"docs"`
	Bytes int64 `json:"bytes"`
}

type Bucket struct {
	Repo   string `json:"repo"`
	Bands  []Band `json:"bands"`
	Oldest int64  `json:"oldest"`
	Newest int64  `json:"newest"`
}

type Options struct {
	Repos      []string `json:"repos"`
	OlderThan  string   `json:"olderThan"`
	Vacuum     bool     `json:"vacuum"`
	DB         string   `json:"db"`
	VacuumOnly bool     `json:"vacuumOnly"`
	StopDaemon bool     `json:"stopDaemon"`
}

const sampleRows = 200

// Buckets reports per-repo doc counts per age band plus an estimated byte
// footprint. Counts are exact and index-only (idx_live_prune); bytes are
// content-length averaged over a random sample per repo, because summing
// length(content) across the whole table reads every blob (~30s on a 13G db).
func Buckets(d *sql.DB) ([]Bucket, error) {
	var sel []string
	for _, days := range Bands {
		if days == 0 {
			sel = append(sel, "COUNT(*)")
			continue
		}
		sel = append(sel, fmt.Sprintf("SUM(mtime < strftime('%%s','now','-%d days'))", days))
	}
	byRepo := map[string]*Bucket{}
	scan := func(q string) error {
		rows, err := d.Query(q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var repo sql.NullString
			var oldest, newest sql.NullInt64
			counts := make([]sql.NullInt64, len(Bands))
			dest := []any{&repo, &oldest, &newest}
			for i := range counts {
				dest = append(dest, &counts[i])
			}
			if err := rows.Scan(dest...); err != nil {
				return err
			}
			name := strings.TrimSpace(repo.String)
			if name == "" {
				continue
			}
			b := byRepo[name]
			if b == nil {
				b = &Bucket{Repo: name, Bands: make([]Band, len(Bands))}
				for i, days := range Bands {
					b.Bands[i].Days = days
				}
				byRepo[name] = b
			}
			for i := range counts {
				b.Bands[i].Docs += int(counts[i].Int64)
			}
			if oldest.Valid && (b.Oldest == 0 || oldest.Int64 < b.Oldest) {
				b.Oldest = oldest.Int64
			}
			if newest.Int64 > b.Newest {
				b.Newest = newest.Int64
			}
		}
		return rows.Err()
	}

	cols := "MIN(mtime), MAX(mtime), " + strings.Join(sel, ", ")
	if err := scan(`SELECT canonical_project, ` + cols + `
		FROM live_docs WHERE canonical_project IS NOT NULL AND canonical_project <> ''
		GROUP BY canonical_project`); err != nil {
		return nil, err
	}
	// rows the canonicalizer never labeled fall back to their raw project so
	// they stay selectable (the script matches either column).
	if err := scan(`SELECT project, ` + cols + `
		FROM live_docs WHERE canonical_project IS NULL OR canonical_project = ''
		GROUP BY project`); err != nil {
		return nil, err
	}

	out := make([]Bucket, 0, len(byRepo))
	for _, b := range byRepo {
		avg := sampleAvgLen(d, b.Repo)
		for i := range b.Bands {
			b.Bands[i].Bytes = int64(avg * float64(b.Bands[i].Docs))
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bands[0].Bytes != out[j].Bands[0].Bytes {
			return out[i].Bands[0].Bytes > out[j].Bands[0].Bytes
		}
		return out[i].Repo < out[j].Repo
	})
	return out, nil
}

func sampleAvgLen(d *sql.DB, repo string) float64 {
	var avg sql.NullFloat64
	err := d.QueryRow(`SELECT AVG(length(content)) FROM (
			SELECT content FROM live_docs
			WHERE canonical_project = ?1 OR project = ?1
			ORDER BY random() LIMIT ?2)`, repo, sampleRows).Scan(&avg)
	if err != nil {
		return 0
	}
	return avg.Float64
}

// Run executes the prune script once per selected repo (or once with no repo
// filter when none are selected), streaming its log lines to onLine. dryRun
// stops before any export or delete.
func Run(archiveBase string, opts Options, dryRun bool, onLine func(string)) error {
	script, err := writeScript()
	if err != nil {
		return err
	}
	defer os.Remove(script)

	if opts.StopDaemon {
		// VACUUM needs an exclusive lock and the daemon holds live.db open for
		// its whole life, so reclaiming anything means bouncing it.
		if err := daemonCmd("stop", onLine); err != nil {
			onLine("WARN could not stop giantmemd: " + err.Error())
		}
		defer func() {
			if err := daemonCmd("start", onLine); err != nil {
				onLine("WARN could not restart giantmemd: " + err.Error() + " -- run: giantmem daemon start")
			}
		}()
	}

	if opts.VacuumOnly {
		args := []string{script, "--vacuum-only"}
		if opts.DB != "" {
			args = append(args, "--db", opts.DB)
		}
		return runOne(archiveBase, args, onLine)
	}

	targets := opts.Repos
	if len(targets) == 0 {
		targets = []string{""}
	}
	if opts.OlderThan == "" && len(opts.Repos) == 0 {
		return fmt.Errorf("refusing to prune: pick at least one repo or an age cutoff")
	}
	for _, repo := range targets {
		args := []string{script}
		if opts.DB != "" {
			args = append(args, "--db", opts.DB)
		}
		if opts.OlderThan != "" {
			args = append(args, "--older-than", opts.OlderThan)
		}
		if repo != "" {
			args = append(args, "--repo", repo)
		}
		if !opts.Vacuum {
			args = append(args, "--no-vacuum")
		}
		if !dryRun {
			args = append(args, "--yes")
		}
		if err := runOne(archiveBase, args, onLine); err != nil {
			return fmt.Errorf("prune %s: %w", repoLabel(repo), err)
		}
	}
	return nil
}

// daemonCmd runs `giantmem daemon <verb>`, preferring the binary on PATH and
// falling back to the usual install dir (a bundled .app has a bare PATH).
func daemonCmd(verb string, onLine func(string)) error {
	bin, err := exec.LookPath("giantmem")
	if err != nil {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".local", "bin", "giantmem")
		if _, serr := os.Stat(bin); serr != nil {
			return fmt.Errorf("giantmem binary not found")
		}
	}
	out, err := exec.Command(bin, "daemon", verb).CombinedOutput()
	if onLine != nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) != "" {
				onLine(line)
			}
		}
	}
	return err
}

func repoLabel(repo string) string {
	if repo == "" {
		return "(all repos)"
	}
	return repo
}

func runOne(archiveBase string, args []string, onLine func(string)) error {
	cmd := exec.Command("bash", args...)
	if archiveBase != "" {
		cmd.Env = append(os.Environ(), "GIANTMEM_ARCHIVE_BASE="+archiveBase)
	}
	// one pipe for both streams: the script logs to stderr but prints its
	// dry-run table to stdout, and the caller wants them interleaved.
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return err
	}
	pw.Close()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")
		if strings.TrimSpace(line) != "" && onLine != nil {
			onLine(line)
		}
	}
	pr.Close()
	return cmd.Wait()
}

func writeScript() (string, error) {
	body, err := scriptFS.ReadFile("prune.sh")
	if err != nil {
		return "", err
	}
	path := filepath.Join(os.TempDir(), "giantmem-db-prune.sh")
	if err := os.WriteFile(path, body, 0o700); err != nil {
		return "", err
	}
	return path, nil
}

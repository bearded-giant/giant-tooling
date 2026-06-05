package cmd

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/backfill"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Build / update giantmem databases",
	Long:  "Manage live.db and archives.db: init, ingest sessions, migrate project names, rebuild live index.",
}

var (
	migrateDryRun     bool
	migrateCanonical  bool
	sessionsForce     bool
	liveRoots         []string
	backfillWorkspace string
)

var indexInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create live.db and ensure archives.db has expected columns",
	RunE: func(cmd *cobra.Command, args []string) error {
		// archives.db: only if it exists, ensure cwd column. don't create -- it's
		// owned by giantmem-search.py for now.
		if _, err := os.Stat(archiveDBPath()); err == nil {
			a, err := db.Open(archiveDBPath())
			if err != nil {
				return err
			}
			if err := db.EnsureArchive(a); err != nil {
				a.Close()
				return err
			}
			a.Close()
			fmt.Printf("archives.db: ensured cwd column at %s\n", archiveDBPath())
		} else {
			fmt.Printf("archives.db: not found at %s (skipping)\n", archiveDBPath())
		}

		l, err := db.OpenLiveOrCreate(liveDBPath())
		if err != nil {
			return err
		}
		defer l.Close()
		fmt.Printf("live.db: ready at %s\n", liveDBPath())
		return nil
	},
}

var indexMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Consolidate project rows: foo -> foo-wt when archive has foo-wt",
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()

		rows, err := a.Query("SELECT DISTINCT project FROM documents")
		if err != nil {
			return err
		}
		projects := []string{}
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return err
			}
			projects = append(projects, p)
		}
		rows.Close()

		// build set
		set := make(map[string]bool)
		for _, p := range projects {
			set[p] = true
		}

		var moves []struct{ from, to string }
		for _, p := range projects {
			if strings.HasSuffix(p, "-wt") {
				continue
			}
			// candidate matches: name-wt, or last segment + -wt for prefixed names
			base := filepath.Base(p)
			cand := base + "-wt"
			if set[cand] {
				moves = append(moves, struct{ from, to string }{p, cand})
				continue
			}
			// prefixed: dev/ai/chat-orchestrator -> dev/ai/chat-orchestrator-wt
			cand2 := p + "-wt"
			if set[cand2] {
				moves = append(moves, struct{ from, to string }{p, cand2})
			}
		}

		if len(moves) == 0 {
			fmt.Println("nothing to migrate")
			return nil
		}
		for _, m := range moves {
			fmt.Printf("  %s -> %s\n", m.from, m.to)
		}
		if migrateDryRun {
			fmt.Println("(dry run, no changes)")
			return nil
		}
		tx, err := a.Begin()
		if err != nil {
			return err
		}
		for _, m := range moves {
			if _, err := tx.Exec("UPDATE documents SET project = ? WHERE project = ?", m.to, m.from); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		fmt.Printf("migrated %d project(s)\n", len(moves))

		if migrateCanonical {
			n, err := backfillCanonical(a)
			if err != nil {
				return err
			}
			fmt.Printf("canonicalized %d row(s) in archives.db\n", n)

			l, err := db.Open(liveDBPath())
			if err == nil {
				defer l.Close()
				ln, err := backfillCanonicalLive(l)
				if err != nil {
					return err
				}
				fmt.Printf("canonicalized %d row(s) in live.db\n", ln)
			}
		}
		return nil
	},
}

// backfillCanonical writes canonical_project for every documents row.
func backfillCanonical(a *sql.DB) (int, error) {
	rows, err := a.Query(`SELECT DISTINCT project FROM documents`)
	if err != nil {
		return 0, err
	}
	var projects []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		projects = append(projects, p)
	}
	rows.Close()

	tx, err := a.Begin()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, p := range projects {
		canon := canonicalProjectName(p)
		res, err := tx.Exec(
			`UPDATE documents SET canonical_project = ? WHERE project = ?`,
			canon, p,
		)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, tx.Commit()
}

// backfillCanonicalLive does the same for live.db.
func backfillCanonicalLive(l *sql.DB) (int, error) {
	rows, err := l.Query(`SELECT DISTINCT project FROM live_docs`)
	if err != nil {
		return 0, err
	}
	var projects []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		projects = append(projects, p)
	}
	rows.Close()

	tx, err := l.Begin()
	if err != nil {
		return 0, err
	}
	total := 0
	for _, p := range projects {
		canon := canonicalProjectName(p)
		res, err := tx.Exec(
			`UPDATE live_docs SET canonical_project = ? WHERE project = ?`,
			canon, p,
		)
		if err != nil {
			tx.Rollback()
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, tx.Commit()
}

func canonicalProjectName(name string) string {
	return project.Canonicalize(name, archiveBasePath())
}

var indexSessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Backfill cwd + ensure session rows from ~/.claude/projects/**/*.jsonl",
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		if err := db.EnsureArchive(a); err != nil {
			return err
		}

		home, _ := os.UserHomeDir()
		root := filepath.Join(home, ".claude", "projects")
		updated := 0
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			if strings.Contains(path, "subagents") {
				return nil
			}
			cwd := extractCwd(path)
			if cwd == "" {
				return nil
			}
			res, err := a.Exec(
				"UPDATE documents SET cwd = ? WHERE filepath = ? AND source_type = 'session' AND (cwd IS NULL OR cwd = '')",
				cwd, path,
			)
			if err != nil {
				return nil
			}
			n, _ := res.RowsAffected()
			updated += int(n)
			return nil
		})
		fmt.Printf("updated cwd on %d session row(s)\n", updated)
		fmt.Println("(run giantmem-search.py ingest to add new sessions)")
		return nil
	},
}

func extractCwd(jsonlPath string) string {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	limit := 50
	for sc.Scan() && limit > 0 {
		limit--
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		if v, ok := m["cwd"].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

var indexLiveCmd = &cobra.Command{
	Use:   "live [root...]",
	Short: "Rebuild live.db from .giantmem trees under given roots",
	RunE: func(cmd *cobra.Command, args []string) error {
		roots := args
		if len(roots) == 0 {
			home, _ := os.UserHomeDir()
			roots = []string{filepath.Join(home, "dev")}
		}
		l, err := db.OpenLiveOrCreate(liveDBPath())
		if err != nil {
			return err
		}
		defer l.Close()

		count := 0
		for _, root := range roots {
			n, err := scanLive(l, root)
			if err != nil {
				return err
			}
			count += n
		}
		fmt.Printf("indexed %d live doc(s)\n", count)
		return nil
	},
}

func scanLive(l *sql.DB, root string) (int, error) {
	count := 0
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := l.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`
        INSERT INTO live_docs (path, project, worktree_path, feature, dir_type,
            session_id, git_sha, mtime, ingested_at, content)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(path) DO UPDATE SET
            project=excluded.project,
            worktree_path=excluded.worktree_path,
            feature=excluded.feature,
            dir_type=excluded.dir_type,
            session_id=excluded.session_id,
            git_sha=excluded.git_sha,
            mtime=excluded.mtime,
            ingested_at=excluded.ingested_at,
            content=excluded.content`)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == ".git" || name == ".venv" || name == "venv" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		if !strings.Contains(p, "/.giantmem/") {
			return nil
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		info := project.Detect(filepath.Dir(p), archiveBasePath())
		feature := featureFromPath(p)
		dirType := dirTypeFromPath(p)
		_, err = stmt.Exec(p, info.Project, info.WorktreePath, feature, dirType,
			"", "", st.ModTime().Unix(), now, string(content))
		if err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func featureFromPath(p string) string {
	// .giantmem/features/<name>/...
	idx := strings.Index(p, "/.giantmem/features/")
	if idx < 0 {
		return ""
	}
	rest := p[idx+len("/.giantmem/features/"):]
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

func dirTypeFromPath(p string) string {
	// .giantmem/<dir_type>/...
	idx := strings.Index(p, "/.giantmem/")
	if idx < 0 {
		return ""
	}
	rest := p[idx+len("/.giantmem/"):]
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 {
		return "root"
	}
	if !strings.Contains(parts[0], ".") {
		return parts[0]
	}
	return "root"
}

var indexBackfillCmd = &cobra.Command{
	Use:   "backfill",
	Short: "Crawl .giantmem/ on disk and upsert all non-empty files into live.db",
	Long: `Default: walks ` + "`$GIANTMEM_DEV_ROOTS`" + ` (or ~/dev) and upserts every
.giantmem/ workspace's non-empty files (any extension, max 5MB) into live_docs.

` + "`--workspace <path>`" + ` scopes the walk to one workspace — the .giantmem/
directory you pass, or a worktree root whose .giantmem subdir we'll find.
Used by the worktree-remove flow to flush a workspace into live.db just
before the .giantmem/ directory gets deleted.

Idempotent: a file is re-read only when its mtime is newer than the stored
row OR its size differs. Same code path the daemon runs at startup.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		l, err := db.OpenLiveOrCreate(liveDBPath())
		if err != nil {
			return err
		}
		defer l.Close()
		var st backfill.Stats
		if backfillWorkspace != "" {
			ws := backfillWorkspace
			// caller may pass either the workspace root (.../.giantmem) or the
			// worktree root that contains it; normalize.
			if fi, ferr := os.Stat(ws); ferr == nil && fi.IsDir() {
				if filepath.Base(ws) != ".giantmem" {
					candidate := filepath.Join(ws, ".giantmem")
					if cfi, cerr := os.Stat(candidate); cerr == nil && cfi.IsDir() {
						ws = candidate
					}
				}
			}
			st, err = backfill.RunOnWorkspace(l, archiveBasePath(), ws)
		} else {
			st, err = backfill.Run(l, archiveBasePath(), 0)
		}
		if err != nil {
			return err
		}
		fmt.Printf("backfill: workspaces=%d scanned=%d upserted=%d skipped=%d empty=%d too_large=%d errors=%d\n",
			st.Workspaces, st.Scanned, st.Upserted, st.Skipped, st.Empty, st.TooLarge, st.Errors)
		return nil
	},
}

func init() {
	indexMigrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "show planned changes only")
	indexMigrateCmd.Flags().BoolVar(&migrateCanonical, "canonicalize", false, "also backfill canonical_project on existing rows")
	indexSessionsCmd.Flags().BoolVar(&sessionsForce, "force", false, "re-extract cwd even if set")
	indexBackfillCmd.Flags().StringVar(&backfillWorkspace, "workspace", "", "scope to a single .giantmem (or its parent worktree); default = all roots")

	indexCmd.AddCommand(indexInitCmd)
	indexCmd.AddCommand(indexMigrateCmd)
	indexCmd.AddCommand(indexSessionsCmd)
	indexCmd.AddCommand(indexLiveCmd)
	indexCmd.AddCommand(indexBackfillCmd)
	rootCmd.AddCommand(indexCmd)
}

package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
	"github.com/spf13/cobra"
)

// completeProjects suggests project names from archives.db + live.db.
func completeProjects(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	seen := map[string]bool{}
	if a, err := db.Open(archiveDBPath()); err == nil {
		rows, err := a.Query(`SELECT DISTINCT COALESCE(canonical_project, project) FROM documents`)
		if err == nil {
			for rows.Next() {
				var p string
				if err := rows.Scan(&p); err == nil {
					seen[p] = true
				}
			}
			rows.Close()
		}
		a.Close()
	}
	if l, err := db.Open(liveDBPath()); err == nil {
		rows, err := l.Query(`SELECT DISTINCT COALESCE(canonical_project, project) FROM live_docs`)
		if err == nil {
			for rows.Next() {
				var p string
				if err := rows.Scan(&p); err == nil {
					seen[p] = true
				}
			}
			rows.Close()
		}
		l.Close()
	}
	var out []string
	for p := range seen {
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(toComplete)) {
			out = append(out, p)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeSessionIDs suggests session id-prefixes (8 chars).
func completeSessionIDs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	a, err := db.Open(archiveDBPath())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer a.Close()
	rows, err := a.Query(`SELECT DISTINCT session_id FROM documents WHERE source_type='session' AND session_id IS NOT NULL ORDER BY timestamp DESC LIMIT 200`)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			continue
		}
		if sid == "" {
			continue
		}
		short := sid[:8]
		if strings.HasPrefix(short, toComplete) {
			out = append(out, short)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeArchiveProjects suggests dirs under archive base.
func completeArchiveProjects(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	entries, err := os.ReadDir(archiveBasePath())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		if strings.HasPrefix(e.Name(), toComplete) {
			out = append(out, e.Name())
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeFeatures suggests active feature names from worktrees under ~/dev.
func completeFeatures(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, "dev")
	seen := map[string]bool{}
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() == "node_modules" || d.Name() == ".venv" || d.Name() == "venv" {
			return filepath.SkipDir
		}
		if d.Name() != ".giantmem" {
			return nil
		}
		feat := project.FeatureFromGiantmem(filepath.Dir(p))
		if feat != "" {
			seen[feat] = true
		}
		return filepath.SkipDir
	})
	var out []string
	for f := range seen {
		if strings.HasPrefix(strings.ToLower(f), strings.ToLower(toComplete)) {
			out = append(out, f)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	// register completion functions on existing flags/args
	for _, f := range []*cobra.Command{findCmd, sessListCmd, sessFindCmd, statusCmd, recentWritesCompletionTarget()} {
		if f != nil {
			_ = f.RegisterFlagCompletionFunc("project", completeProjects)
		}
	}
	for _, f := range []*cobra.Command{findCmd, captureCmd, tailCmd} {
		_ = f.RegisterFlagCompletionFunc("feature", completeFeatures)
	}
	// session id positional
	for _, c := range []*cobra.Command{sessShowCmd, sessResumeCmd, sessExportCmd} {
		c.ValidArgsFunction = completeSessionIDs
	}
	// session diff: two session ids
	sessDiffCmd.ValidArgsFunction = completeSessionIDs
	// archive open / dedup project arg
	archiveOpenCmd.ValidArgsFunction = completeArchiveProjects
	archiveDedupCmd.ValidArgsFunction = completeArchiveProjects
	archiveListCmd.ValidArgsFunction = completeArchiveProjects
}

// recentWritesCompletionTarget returns nil because the MCP tool isn't a Cobra cmd;
// kept here to keep the loop simple if future commands need wiring.
func recentWritesCompletionTarget() *cobra.Command { return nil }

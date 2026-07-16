package cmd

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/project"
	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "List and manage indexed projects",
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every project in the live index with doc/artifact counts",
	RunE:  runProjectList,
}

var projectDeleteCmd = &cobra.Command{
	Use:   "delete <project>",
	Short: "Remove a project from the live index (live_docs, artifacts, embeddings, sessions)",
	Long: `Removes every live-index row for the project: live_docs (+fts via trigger),
active_sessions, artifacts and their embeddings/access rows.

archives.db documents are kept unless --purge-archive is passed, so long-term
search still finds the project's history after a live-index delete.

Prompts y/N unless --yes.`,
	Args: cobra.ExactArgs(1),
	RunE: runProjectDelete,
}

var (
	projectJSON         bool
	projectGoneOnly     bool
	projectDeleteYes    bool
	projectPurgeArchive bool
)

func init() {
	projectListCmd.Flags().BoolVar(&projectJSON, "json", false, "JSON output")
	projectListCmd.Flags().BoolVar(&projectGoneOnly, "gone", false, "only projects whose worktrees no longer exist on disk")
	projectDeleteCmd.Flags().BoolVar(&projectDeleteYes, "yes", false, "skip confirmation prompt")
	projectDeleteCmd.Flags().BoolVar(&projectPurgeArchive, "purge-archive", false, "also delete the project's archives.db documents")
	projectDeleteCmd.Flags().BoolVar(&projectJSON, "json", false, "JSON output")
	projectCmd.AddCommand(projectListCmd, projectDeleteCmd)
	rootCmd.AddCommand(projectCmd)
}

func runProjectList(cmd *cobra.Command, args []string) error {
	live, err := db.Open(liveDBPath())
	if err != nil {
		return err
	}
	defer live.Close()
	archive, err := db.Open(archiveDBPath())
	if err == nil {
		defer archive.Close()
	} else {
		archive = nil
	}

	infos, err := project.List(live, archive)
	if err != nil {
		return err
	}
	if projectGoneOnly {
		var gone []project.IndexInfo
		for _, p := range infos {
			if p.Gone {
				gone = append(gone, p)
			}
		}
		infos = gone
	}

	if projectJSON {
		return output.JSON(infos)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tDOCS\tARTIFACTS\tARCHIVE\tGONE\tWORKTREE")
	for _, p := range infos {
		gone := ""
		if p.Gone {
			gone = "gone"
		}
		wt := ""
		if len(p.Worktrees) > 0 {
			wt = p.Worktrees[0]
			if len(p.Worktrees) > 1 {
				wt = fmt.Sprintf("%s (+%d)", wt, len(p.Worktrees)-1)
			}
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\t%s\n", p.Name, p.Docs, p.Artifacts, p.ArchiveDocs, gone, wt)
	}
	w.Flush()
	fmt.Printf("\n%d projects\n", len(infos))
	return nil
}

func runProjectDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	live, err := db.Open(liveDBPath())
	if err != nil {
		return err
	}
	defer live.Close()
	var archive *sql.DB
	if a, err := db.Open(archiveDBPath()); err == nil {
		archive = a
		defer archive.Close()
	} else if projectPurgeArchive {
		return fmt.Errorf("open archives.db: %w", err)
	}

	infos, err := project.List(live, archive)
	if err != nil {
		return err
	}
	var target *project.IndexInfo
	for i := range infos {
		if infos[i].Name == name {
			target = &infos[i]
			break
		}
	}
	if target == nil {
		var archCount int
		if archive != nil {
			_ = archive.QueryRow(
				`SELECT COUNT(*) FROM documents WHERE project = ? OR COALESCE(canonical_project,'') = ?`,
				name, name).Scan(&archCount)
		}
		if archCount > 0 && projectPurgeArchive {
			// archive-only project (sessions ingested, no live_docs) — purge is still valid
			target = &project.IndexInfo{Name: name, ArchiveDocs: archCount}
		} else if archCount > 0 {
			return fmt.Errorf("no project %q in live index, but %d archive docs exist — use --purge-archive to remove them", name, archCount)
		} else {
			return fmt.Errorf("no project %q in live index (giantmem project list)", name)
		}
	}

	if !projectDeleteYes {
		scope := fmt.Sprintf("%d docs, %d artifacts from live index", target.Docs, target.Artifacts)
		if projectPurgeArchive {
			scope += fmt.Sprintf(" + %d archive docs", target.ArchiveDocs)
		}
		fmt.Printf("delete project %q (%s)? [y/N] ", name, scope)
		ans, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
			fmt.Println("aborted")
			return nil
		}
	}

	d, err := project.Delete(live, archive, name, projectPurgeArchive)
	if err != nil {
		return err
	}
	if projectJSON {
		return output.JSON(d)
	}
	fmt.Printf("deleted %q: %d live docs, %d artifacts, %d embeddings, %d access rows, %d sessions",
		name, d.LiveDocs, d.Artifacts, d.Embeddings, d.AccessRows, d.Sessions)
	if projectPurgeArchive {
		fmt.Printf(", %d archive docs", d.ArchiveDocs)
	}
	fmt.Println()
	return nil
}

package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/primeinfo"
	"github.com/spf13/cobra"
)

var (
	primeJSON      bool
	primeRecentN   int
	primeSessionsN int
	primeHistoryN  int
	primeNoDaemon  bool
)

var primeCmd = &cobra.Command{
	Use:   "prime [path]",
	Short: "Emit a context primer for a workspace (active feature, recent docs/sessions/history)",
	Long: `Designed for Claude Code SessionStart hooks. Walks up from cwd (or the
given path), detects project, reads features.json, queries live.db for the
project's recent docs, archives.db for recent sessions, and the .giantmem/history
log if present.

If giantmemd is running the query runs over the daemon's already-open DB handles.
Pass --no-daemon to open DBs directly.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		if len(args) > 0 {
			cwd = args[0]
		}
		p, err := dispatchPrime(cwd)
		if err != nil {
			return err
		}
		if primeJSON {
			return output.JSON(p)
		}
		printPrimeText(p)
		return nil
	},
}

// dispatchPrime tries the daemon first, falls back to direct DB open.
func dispatchPrime(cwd string) (*primeinfo.Payload, error) {
	if !primeNoDaemon && os.Getenv("GIANTMEM_NO_DAEMON") == "" {
		sock := daemon.DefaultSocketPath()
		if daemon.SocketAlive(sock, 250*time.Millisecond) {
			cli := daemon.NewClient(sock, 5*time.Second)
			var p primeinfo.Payload
			err := cli.Call("prime", &daemon.PrimeParams{
				Cwd:      cwd,
				RecentN:  primeRecentN,
				SessionN: primeSessionsN,
				HistoryN: primeHistoryN,
			}, &p)
			if err == nil {
				return &p, nil
			}
			if !daemon.IsSchemaDrift(err) {
				fmt.Fprintf(os.Stderr, "daemon error, falling back: %v\n", err)
			}
		}
	}
	return primeDirect(cwd)
}

func primeDirect(cwd string) (*primeinfo.Payload, error) {
	var archive, live *sql.DB
	if d, err := db.Open(archiveDBPath()); err == nil {
		archive = d
		defer archive.Close()
	}
	if d, err := db.Open(liveDBPath()); err == nil {
		live = d
		defer live.Close()
	}
	return primeinfo.Build(archive, live, cwd, archiveBasePath(), primeRecentN, primeSessionsN, primeHistoryN)
}

func printPrimeText(p *primeinfo.Payload) {
	fmt.Printf("project: %s\n", p.Project)
	fmt.Printf("worktree: %s\n", p.WorktreePath)
	if p.ActiveFeature != "" {
		fmt.Printf("active feature: %s\n", p.ActiveFeature)
	}
	if len(p.RecentDocs) > 0 {
		fmt.Println("\nrecent docs:")
		for _, d := range p.RecentDocs {
			fmt.Printf("  %s  %s\n", d.DirType, d.Path)
		}
	}
	if len(p.RecentSessions) > 0 {
		fmt.Println("\nrecent sessions:")
		for _, s := range p.RecentSessions {
			id := s.SessionID
			if len(id) > 8 {
				id = id[:8]
			}
			fmt.Printf("  %s  %s  %s\n", id, s.Topic, s.Timestamp)
		}
	}
	if len(p.HistoryTail) > 0 {
		fmt.Println("\nhistory tail:")
		for _, l := range p.HistoryTail {
			if l == "" {
				continue
			}
			fmt.Printf("  %s\n", l)
		}
	}
}

func init() {
	primeCmd.Flags().BoolVar(&primeJSON, "json", false, "JSON output (for hooks)")
	primeCmd.Flags().IntVar(&primeRecentN, "recent", 3, "max recent live docs")
	primeCmd.Flags().IntVar(&primeSessionsN, "sessions", 2, "max recent sessions")
	primeCmd.Flags().IntVar(&primeHistoryN, "history", 5, "max history.md tail lines")
	primeCmd.Flags().BoolVar(&primeNoDaemon, "no-daemon", false, "skip giantmemd; open DBs directly")
	rootCmd.AddCommand(primeCmd)
}

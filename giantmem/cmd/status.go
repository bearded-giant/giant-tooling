package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/statusinfo"
	"github.com/spf13/cobra"
)

func jsonMarshal(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

var (
	statusJSON      bool
	statusStaleD    int
	statusRoot      string
	statusProject   string
	statusWriteFile string
	statusNoDaemon  bool
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "One-shot snapshot for statuslines and quick checks",
	Long: `Returns active_feature for cwd, live_docs written today, last ingest time,
and a count of stale workspaces. Designed to be cheap (<50ms) for statusline use.

Defaults to the current dir. Pass --root <path> to query a different worktree.

If giantmemd is running the query runs over the daemon's already-open DB handles.
Pass --no-daemon to open DBs directly.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, _ := os.Getwd()
		if statusRoot != "" {
			cwd = statusRoot
		}
		s := dispatchStatus(cwd)
		if statusWriteFile != "" {
			data, err := jsonMarshal(s)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(statusWriteFile), 0o755); err != nil {
				return err
			}
			return os.WriteFile(statusWriteFile, data, 0o644)
		}
		if statusJSON {
			return output.JSON(s)
		}
		printStatus(s)
		return nil
	},
}

func jsonMarshalIndent(v any) ([]byte, error) {
	return jsonMarshal(v)
}

// dispatchStatus tries the daemon first, falls back to direct DB open.
func dispatchStatus(cwd string) statusinfo.Status {
	if !statusNoDaemon && os.Getenv("GIANTMEM_NO_DAEMON") == "" {
		sock := daemon.DefaultSocketPath()
		if daemon.SocketAlive(sock, 250*time.Millisecond) {
			cli := daemon.NewClient(sock, 5*time.Second)
			var s statusinfo.Status
			err := cli.Call("status", &daemon.StatusParams{
				Root:    cwd,
				Project: statusProject,
				StaleD:  statusStaleD,
			}, &s)
			if err == nil {
				return s
			}
			if !daemon.IsSchemaDrift(err) {
				fmt.Fprintf(os.Stderr, "daemon error, falling back: %v\n", err)
			}
		}
	}
	return statusDirect(cwd)
}

func statusDirect(cwd string) statusinfo.Status {
	var archive, live *sql.DB
	if d, err := db.Open(archiveDBPath()); err == nil {
		archive = d
		defer archive.Close()
	}
	if d, err := db.Open(liveDBPath()); err == nil {
		live = d
		defer live.Close()
	}
	return statusinfo.Build(archive, live, cwd, archiveBasePath(), statusProject, statusStaleD)
}

func printStatus(s statusinfo.Status) {
	fmt.Printf("project:           %s\n", s.Project)
	if s.ActiveFeature != "" {
		fmt.Printf("active feature:    %s\n", s.ActiveFeature)
	}
	fmt.Printf("live docs today:   %d\n", s.LiveDocsToday)
	fmt.Printf("live docs total:   %d\n", s.LiveDocsTotal)
	if s.LastLiveWriteAt != "" {
		fmt.Printf("last live write:   %s\n", s.LastLiveWriteAt)
	}
	if s.LastIndexedAt != "" {
		fmt.Printf("last archive ix:   %s\n", s.LastIndexedAt)
	}
	if s.StaleWorkspaces > 0 {
		fmt.Printf("stale workspaces:  %d\n", s.StaleWorkspaces)
	}
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "JSON output (for statuslines)")
	statusCmd.Flags().IntVar(&statusStaleD, "stale-days", 0, "include stale workspace count (0 disables; statuslines should set 30)")
	statusCmd.Flags().StringVar(&statusRoot, "root", "", "use this dir instead of cwd for project detection")
	statusCmd.Flags().StringVar(&statusProject, "project", "", "override project filter")
	statusCmd.Flags().StringVar(&statusWriteFile, "write-cache", "", "write JSON to this file (used by statusline)")
	statusCmd.Flags().BoolVar(&statusNoDaemon, "no-daemon", false, "skip giantmemd; open DBs directly")
	rootCmd.AddCommand(statusCmd)
}

package cmd

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bryangrimes/gm/internal/daemon"
	"github.com/bryangrimes/gm/internal/db"
	"github.com/bryangrimes/gm/internal/output"
	"github.com/bryangrimes/gm/internal/search"
	"github.com/spf13/cobra"
)

var (
	findProject     string
	findDirType     string
	findSourceType  string
	findFeature     string
	findLatest      bool
	findLimit       int
	findJSON        bool
	findPaths       bool
	findFull        bool
	findLiveOnly    bool
	findArchOnly    bool
	findSince       string
	findUntil       string
	findInteractive bool
	findOpenEditor  bool
	findNoDaemon    bool
)

var findCmd = &cobra.Command{
	Use:   "find <query>",
	Short: "Search live + archived workspace docs and sessions (FTS5)",
	Long: `Search live workspace docs (live.db) and archived docs + Claude session
transcripts (archives.db). Default queries both and merges by score.

If giantmemd is running and reachable, the search runs over the daemon's
already-open DB handles (sub-millisecond after connect). Otherwise the CLI
opens the DBs directly. Pass --no-daemon to force direct mode.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runFind,
}

func init() {
	findCmd.Flags().StringVarP(&findProject, "project", "p", "", "filter by project (LIKE)")
	findCmd.Flags().StringVarP(&findDirType, "type", "t", "", "filter by dir_type")
	findCmd.Flags().StringVarP(&findSourceType, "source", "s", "", "filter by source_type (workspace|session|domain|live)")
	findCmd.Flags().StringVarP(&findFeature, "feature", "f", "", "filter by feature name (live.db)")
	findCmd.Flags().BoolVarP(&findLatest, "latest", "l", false, "archive: only latest per project")
	findCmd.Flags().IntVarP(&findLimit, "limit", "n", 20, "max results")
	findCmd.Flags().BoolVar(&findJSON, "json", false, "JSON output")
	findCmd.Flags().BoolVar(&findPaths, "paths", false, "print absolute paths only")
	findCmd.Flags().BoolVar(&findFull, "full", false, "include matching content snippet")
	findCmd.Flags().BoolVar(&findLiveOnly, "live", false, "search only live.db")
	findCmd.Flags().BoolVar(&findArchOnly, "archive", false, "search only archives.db")
	findCmd.Flags().StringVar(&findSince, "since", "", `only docs newer than (e.g. "7d", "2h", RFC3339)`)
	findCmd.Flags().StringVar(&findUntil, "until", "", `only docs older than (e.g. "1d", RFC3339)`)
	findCmd.Flags().BoolVarP(&findInteractive, "interactive", "i", false, "fzf+bat picker; on select prints path or opens with -o")
	findCmd.Flags().BoolVarP(&findOpenEditor, "open", "o", false, "with -i: open selected hit in $EDITOR (default: print path)")
	findCmd.Flags().BoolVar(&findNoDaemon, "no-daemon", false, "skip giantmemd; open DBs directly")
}

func runFind(cmd *cobra.Command, args []string) error {
	query := strings.Join(args, " ")
	params := search.Params{
		Query:       query,
		Project:     findProject,
		DirType:     findDirType,
		SourceType:  findSourceType,
		Feature:     findFeature,
		Latest:      findLatest,
		LiveOnly:    findLiveOnly,
		ArchiveOnly: findArchOnly,
		Since:       findSince,
		Until:       findUntil,
		Limit:       findLimit,
		IncludeFull: findFull,
	}

	hits, err := dispatchFind(params)
	if err != nil {
		return err
	}

	if len(hits) == 0 {
		fmt.Fprintln(os.Stderr, "no results")
		return nil
	}

	switch {
	case findInteractive:
		return interactivePick(hits, query, findOpenEditor)
	case findJSON:
		return output.JSON(hits)
	case findPaths:
		for _, h := range hits {
			fmt.Println(h.Filepath)
		}
	default:
		printHits(hits, findFull)
	}
	return nil
}

// dispatchFind tries the daemon first (when reachable) and falls back to a
// direct DB open. Schema-drift errors trip the fallback automatically.
func dispatchFind(p search.Params) ([]search.Hit, error) {
	if !findNoDaemon && os.Getenv("GIANTMEM_NO_DAEMON") == "" {
		sock := daemon.DefaultSocketPath()
		if daemon.SocketAlive(sock, 250*time.Millisecond) {
			cli := daemon.NewClient(sock, 5*time.Second)
			var out struct {
				Hits []search.Hit `json:"hits"`
			}
			err := cli.Call("find", &daemon.FindParams{
				Query:       p.Query,
				Project:     p.Project,
				DirType:     p.DirType,
				SourceType:  p.SourceType,
				Feature:     p.Feature,
				Latest:      p.Latest,
				LiveOnly:    p.LiveOnly,
				ArchiveOnly: p.ArchiveOnly,
				Since:       p.Since,
				Until:       p.Until,
				Limit:       p.Limit,
				IncludeFull: p.IncludeFull,
			}, &out)
			if err == nil {
				return out.Hits, nil
			}
			if !daemon.IsSchemaDrift(err) {
				fmt.Fprintf(os.Stderr, "daemon error, falling back: %v\n", err)
			}
		}
	}
	return findDirect(p)
}

func findDirect(p search.Params) ([]search.Hit, error) {
	var archive, live *sql.DB
	if _, err := os.Stat(archiveDBPath()); err == nil {
		if d, err := db.Open(archiveDBPath()); err == nil {
			archive = d
			defer archive.Close()
		}
	}
	if _, err := os.Stat(liveDBPath()); err == nil {
		if d, err := db.Open(liveDBPath()); err == nil {
			live = d
			defer live.Close()
		}
	}
	return search.Run(archive, live, p)
}

// interactivePick fans hits into fzf with a bat preview.
func interactivePick(hits []search.Hit, query string, openEditor bool) error {
	fzf, err := exec.LookPath("fzf")
	if err != nil {
		return fmt.Errorf("fzf not found in PATH (brew install fzf)")
	}

	var input strings.Builder
	for _, h := range hits {
		marker := h.SourceType
		if h.DirType != "" && h.DirType != "root" {
			marker = h.SourceType + "/" + h.DirType
		}
		ts := h.Timestamp
		if h.Source == "live" {
			ts = "live"
			marker = "live"
			if h.Feature != "" {
				marker = "live/" + h.Feature
			} else if h.DirType != "" {
				marker = "live/" + h.DirType
			}
		}
		display := fmt.Sprintf("[%6.2f] %-30s %s", h.Score, h.Project+"/"+ts, marker)
		input.WriteString(h.Filepath)
		input.WriteString("\t")
		input.WriteString(display)
		input.WriteString("\n")
	}

	preview := fmt.Sprintf(
		"file=$(echo {} | cut -f1); " +
			"if command -v bat >/dev/null; then " +
			"bat --color=always --style=numbers --line-range=:200 \"$file\" 2>/dev/null; " +
			"else sed -n '1,120p' \"$file\"; fi")

	cmd := exec.Command(fzf,
		"--ansi",
		"--delimiter", "\t",
		"--with-nth", "2",
		"--preview", preview,
		"--preview-window", "right:60%:wrap",
		"--header", "query: "+query+" | enter: select | esc: cancel",
		"--bind", "ctrl-u:preview-half-page-up,ctrl-d:preview-half-page-down",
	)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return nil
		}
		return err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil
	}
	path := strings.SplitN(line, "\t", 2)[0]
	if openEditor {
		ed := os.Getenv("EDITOR")
		if ed == "" {
			ed = "vi"
		}
		c := exec.Command(ed, path)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	fmt.Println(path)
	return nil
}

func printHits(hits []search.Hit, full bool) {
	for _, h := range hits {
		var marker string
		switch h.Source {
		case "live":
			marker = "live"
			if h.Feature != "" {
				marker = "live/" + h.Feature
			} else if h.DirType != "" {
				marker = "live/" + h.DirType
			}
		default:
			marker = h.SourceType
			if h.DirType != "" && h.DirType != "root" {
				marker = h.SourceType + "/" + h.DirType
			}
			if h.IsLatest {
				marker += " (latest)"
			}
		}
		ts := h.Timestamp
		if h.Source == "live" {
			ts = "live"
		}
		fmt.Printf("[%6.2f] %s/%s %s\n", h.Score, h.Project, ts, marker)
		fmt.Printf("        %s\n", h.Filepath)
		if full && h.Snippet != "" {
			fmt.Printf("        %s\n", strings.ReplaceAll(h.Snippet, "\n", " "))
		}
	}
}

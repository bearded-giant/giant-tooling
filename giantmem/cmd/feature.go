package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"text/tabwriter"

	archive "github.com/bearded-giant/giant-tooling/giantmem/internal/archiver"
	"github.com/spf13/cobra"
)

var featureCmd = &cobra.Command{
	Use:   "feature",
	Short: "Feature lifecycle: new, start, pause, complete, reopen, list, archive",
}

var (
	ftArchiveForce  bool
	ftArchiveDryRun bool
	ftListStatus    string
)

func featurePyPath() string {
	dir := os.Getenv("GIANT_TOOLING_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "dev", "giant-tooling")
	}
	return filepath.Join(dir, "workspace", "scripts", "feature.py")
}

func runFeaturePy(verb string, args []string) error {
	py := featurePyPath()
	if _, err := os.Stat(py); err != nil {
		return fmt.Errorf("feature.py not found at %s (set GIANT_TOOLING_DIR)", py)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	full := append([]string{py, "--cwd", cwd, verb}, args...)
	c := exec.Command("python3", full...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = os.Environ()
	return c.Run()
}

func featurePySubcmd(use, verb, short string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFeaturePy(verb, args)
		},
	}
}

type featureMeta struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Branch  string `json:"branch"`
	Created string `json:"created"`
}

func findFeaturesJSON() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, ".giantmem", "features", "features.json")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(".giantmem/features/features.json not found (cwd upward) — run /new-feature or ws-init first")
		}
		dir = parent
	}
}

var featureListCmd = &cobra.Command{
	Use:   "list",
	Short: "List features from features.json (in_progress first)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := findFeaturesJSON()
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var feats map[string]featureMeta
		if err := json.Unmarshal(raw, &feats); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		order := map[string]int{"in_progress": 0, "pending": 1, "paused": 2, "complete": 3, "archived": 4}
		var rows []featureMeta
		for name, f := range feats {
			if f.Name == "" {
				f.Name = name
			}
			if ftListStatus != "" && f.Status != ftListStatus {
				continue
			}
			rows = append(rows, f)
		}
		sort.Slice(rows, func(i, j int) bool {
			oi, oj := order[rows[i].Status], order[rows[j].Status]
			if oi != oj {
				return oi < oj
			}
			return rows[i].Name < rows[j].Name
		})
		if len(rows) == 0 {
			fmt.Println("no features matched")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "FEATURE\tSTATUS\tBRANCH\tCREATED")
		for _, f := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Name, f.Status, f.Branch, f.Created)
		}
		return w.Flush()
	},
}

var featureArchiveCmd = &cobra.Command{
	Use:   "archive [name]",
	Short: "Archive a feature: verify in live.db, rm dir, set status=archived (no name = every complete feature)",
	Long: `Archive one feature by name, or — with no name — every status=complete
feature in features.json. Each feature's files are verified as mirrored in
live.db before its dir is removed (live_docs rows kept). Status flips to
archived. live.db is the durable archive; no filesystem snapshot is taken.

--force: include/allow features whose status != complete.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			r, err := archive.ArchiveFeature("", args[0], ftArchiveForce, ftArchiveDryRun)
			if err != nil {
				return err
			}
			return reportFeatureResults([]archive.FeatureResult{r})
		}
		results, err := archive.ArchiveCompleted("", ftArchiveForce, ftArchiveDryRun)
		if err != nil {
			return err
		}
		return reportFeatureResults(results)
	},
}

func init() {
	featureArchiveCmd.Flags().BoolVar(&ftArchiveForce, "force", false, "allow archiving when status != complete")
	featureArchiveCmd.Flags().BoolVar(&ftArchiveDryRun, "dry-run", false, "show what would happen")
	featureArchiveCmd.ValidArgsFunction = completeFeatures

	featureListCmd.Flags().StringVar(&ftListStatus, "status", "", "filter by status (in_progress, pending, paused, complete, archived)")

	featureCmd.AddCommand(
		featurePySubcmd("new <name>", "new", "Create a feature (proposal/tasks/facts/notes)"),
		featurePySubcmd("start [name]", "start", "Promote a pending feature to in_progress"),
		featurePySubcmd("pause [name]", "pause", "Pause the active (or named) feature"),
		featurePySubcmd("complete [name]", "complete", "Mark the active (or named) feature complete"),
		featurePySubcmd("reopen <name>", "reopen", "Reopen a paused/completed feature"),
		featureListCmd,
		featureArchiveCmd,
	)
	rootCmd.AddCommand(featureCmd)
}

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/spf13/cobra"
)

var (
	artifactType    []string
	artifactStatus  []string
	artifactFeature string
	artifactDomain  string
	artifactJSON    bool
	artifactPaths   bool
)

var artifactCmd = &cobra.Command{
	Use:     "artifact",
	Aliases: []string{"art", "artifacts"},
	Short:   "Query typed .giantmem/ artifacts (proposals, delta-specs, tasks, plans, ...)",
}

var artifactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List artifacts in the current workspace, filtered by type/status/feature/domain",
	RunE:  runArtifactList,
}

var artifactShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Print the full content (frontmatter + body) for one artifact",
	Args:  cobra.ExactArgs(1),
	RunE:  runArtifactShow,
}

var artifactReindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Rebuild .giantmem/artifacts.json from disk",
	RunE:  runArtifactReindex,
}

var artifactOrphansCmd = &cobra.Command{
	Use:   "orphans",
	Short: "List artifact-shaped files lacking frontmatter",
	RunE:  runArtifactOrphans,
}

func init() {
	for _, c := range []*cobra.Command{artifactListCmd} {
		c.Flags().StringSliceVarP(&artifactType, "type", "t", nil, "filter by type (repeat or comma-separate)")
		c.Flags().StringSliceVarP(&artifactStatus, "status", "s", nil, "filter by status (default: all)")
		c.Flags().StringVarP(&artifactFeature, "feature", "f", "", "filter by feature name")
		c.Flags().StringVarP(&artifactDomain, "domain", "d", "", "filter by domain")
		c.Flags().BoolVar(&artifactJSON, "json", false, "JSON output")
		c.Flags().BoolVar(&artifactPaths, "paths", false, "print absolute paths only")
	}
	artifactCmd.AddCommand(artifactListCmd, artifactShowCmd, artifactReindexCmd, artifactOrphansCmd)
	rootCmd.AddCommand(artifactCmd)
}

// resolveWorkspace returns the current workspace directory + a freshly built
// index. When the on-disk artifacts.json is stale or missing it scans live.
func resolveWorkspace() (string, *artifacts.Index, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	ws, ok := artifacts.FindWorkspace(cwd)
	if !ok {
		return "", nil, fmt.Errorf("no .giantmem/ found walking up from %s", cwd)
	}
	idx, err := artifacts.Scan(ws)
	if err != nil {
		return ws, nil, err
	}
	return ws, idx, nil
}

func filterIndex(idx *artifacts.Index) []artifacts.Artifact {
	wantType := setFromSlice(artifactType)
	wantStatus := setFromSlice(artifactStatus)

	out := make([]artifacts.Artifact, 0, len(idx.Artifacts))
	for _, a := range idx.Artifacts {
		if len(wantType) > 0 && !wantType[a.Type] {
			continue
		}
		if len(wantStatus) > 0 && !wantStatus[a.Status] {
			continue
		}
		if artifactFeature != "" && a.Feature != artifactFeature {
			continue
		}
		if artifactDomain != "" && a.Domain != artifactDomain {
			continue
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].Feature != out[j].Feature {
			return out[i].Feature < out[j].Feature
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func setFromSlice(in []string) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	m := make(map[string]bool, len(in))
	for _, raw := range in {
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				m[p] = true
			}
		}
	}
	return m
}

func runArtifactList(cmd *cobra.Command, args []string) error {
	ws, idx, err := resolveWorkspace()
	if err != nil {
		return err
	}
	rows := filterIndex(idx)

	if artifactJSON {
		out := struct {
			Workspace string               `json:"workspace"`
			Repo      string               `json:"repo"`
			Branch    string               `json:"branch"`
			Artifacts []artifacts.Artifact `json:"artifacts"`
		}{ws, idx.Repo, idx.Branch, rows}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if artifactPaths {
		for _, a := range rows {
			fmt.Println(filepath.Join(ws, a.Path))
		}
		return nil
	}

	fmt.Printf("# repo=%s branch=%s artifacts=%d\n", idx.Repo, idx.Branch, len(rows))
	for _, a := range rows {
		fmt.Printf("%-12s %-8s %-22s %s\n", a.Type, a.Status, a.Feature+"/"+a.Domain+a.Name, a.ID)
	}
	return nil
}

func runArtifactShow(cmd *cobra.Command, args []string) error {
	id := args[0]
	ws, idx, err := resolveWorkspace()
	if err != nil {
		return err
	}
	var match *artifacts.Artifact
	for i := range idx.Artifacts {
		if idx.Artifacts[i].ID == id {
			match = &idx.Artifacts[i]
			break
		}
	}
	if match == nil {
		return fmt.Errorf("no artifact with id %q in %s", id, ws)
	}
	abs := filepath.Join(ws, match.Path)
	raw, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	fmt.Printf("# %s\n# path: %s\n# status: %s\n\n", match.ID, abs, match.Status)
	os.Stdout.Write(raw)
	return nil
}

func runArtifactReindex(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	ws, ok := artifacts.FindWorkspace(cwd)
	if !ok {
		return fmt.Errorf("no .giantmem/ found walking up from %s", cwd)
	}
	idx, err := artifacts.Scan(ws)
	if err != nil {
		return err
	}
	if err := artifacts.Save(ws, idx); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d artifacts)\n", artifacts.IndexPath(ws), len(idx.Artifacts))
	return nil
}

func runArtifactOrphans(cmd *cobra.Command, args []string) error {
	ws, idx, err := resolveWorkspace()
	if err != nil {
		return err
	}
	count := 0
	for _, a := range idx.Artifacts {
		if a.HasFront {
			continue
		}
		fmt.Printf("%-12s %s\n", a.Type, filepath.Join(ws, a.Path))
		count++
	}
	if count == 0 {
		fmt.Fprintln(os.Stderr, "no orphans")
	}
	return nil
}

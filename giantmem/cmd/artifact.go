package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/spf13/cobra"
)

var (
	artifactType        []string
	artifactStatus      []string
	artifactFeature     string
	artifactDomain      string
	artifactRepo        string
	artifactBranch      string
	artifactScope       string
	artifactLifecycle   []string
	artifactJSON        bool
	artifactPaths       bool
	artifactIncludeArch bool
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

var (
	artifactStaleDays int
	artifactStaleAll  bool
)

var artifactStaleCmd = &cobra.Command{
	Use:   "stale",
	Short: "List artifacts with status: stale OR updated > N days ago (default 30)",
	RunE:  runArtifactStale,
}

func init() {
	for _, c := range []*cobra.Command{artifactListCmd} {
		c.Flags().StringSliceVarP(&artifactType, "type", "t", nil, "filter by type (repeat or comma-separate)")
		c.Flags().StringSliceVarP(&artifactStatus, "status", "s", nil, "filter by status (default: all)")
		c.Flags().StringVarP(&artifactFeature, "feature", "f", "", "filter by feature name")
		c.Flags().StringVarP(&artifactDomain, "domain", "d", "", "filter by domain")
		c.Flags().StringVar(&artifactRepo, "repo", "", "repo filter: current (default), all, or repo name")
		c.Flags().StringVar(&artifactBranch, "branch", "", "branch filter — useful when same feature spans multiple worktrees")
		c.Flags().StringVar(&artifactScope, "scope", "", "filter by scope id (matches explicit frontmatter or repo membership in ~/.giantmem-global/scopes.yaml)")
		c.Flags().StringSliceVar(&artifactLifecycle, "lifecycle", nil, "filter by lifecycle (candidate, durable, deprecated; repeat or comma-separate)")
		c.Flags().BoolVar(&artifactIncludeArch, "include-archived", false, "with --repo all, also include archived .giantmem/ snapshots")
		c.Flags().BoolVar(&artifactJSON, "json", false, "JSON output")
		c.Flags().BoolVar(&artifactPaths, "paths", false, "print absolute paths only")
	}
	artifactStaleCmd.Flags().IntVar(&artifactStaleDays, "days", 30, "stale threshold in days; 0 = use lifecycle tier policy")
	artifactStaleCmd.Flags().BoolVar(&artifactStaleAll, "all-repos", false, "scan every discovered workspace, not just current")
	artifactStaleCmd.Flags().StringVar(&artifactScope, "scope", "", "filter by scope id")
	artifactStaleCmd.Flags().StringSliceVar(&artifactLifecycle, "lifecycle", nil, "filter by lifecycle")

	artifactCmd.AddCommand(artifactListCmd, artifactShowCmd, artifactReindexCmd, artifactOrphansCmd, artifactStaleCmd)
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

func filterArtifacts(rows []artifacts.Artifact) []artifacts.Artifact {
	wantType := setFromSlice(artifactType)
	wantStatus := setFromSlice(artifactStatus)
	wantLifecycle := setFromSlice(artifactLifecycle)

	var registry *artifacts.ScopeRegistry
	if artifactScope != "" {
		reg, err := artifacts.LoadScopeRegistry(artifacts.ScopesYAMLPath())
		if err == nil {
			registry = reg
		}
	}

	out := make([]artifacts.Artifact, 0, len(rows))
	for _, a := range rows {
		if len(wantType) > 0 && !wantType[a.Type] {
			continue
		}
		if len(wantStatus) > 0 && !wantStatus[a.Status] {
			continue
		}
		if len(wantLifecycle) > 0 {
			lc := a.Lifecycle
			if lc == "" {
				lc = artifacts.LifecycleDurable
			}
			if !wantLifecycle[lc] {
				continue
			}
		}
		if artifactFeature != "" && a.Feature != artifactFeature {
			continue
		}
		if artifactDomain != "" && a.Domain != artifactDomain {
			continue
		}
		if artifactBranch != "" && a.Branch != artifactBranch {
			continue
		}
		if artifactRepo != "" && artifactRepo != "all" && artifactRepo != "current" {
			if a.Repo != artifactRepo {
				continue
			}
		}
		if artifactScope != "" {
			if a.Scope != "" {
				if a.Scope != artifactScope {
					continue
				}
			} else if registry == nil || !registry.MatchScope(a.Repo, a.Scope, artifactScope) {
				continue
			}
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
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
	if artifactRepo == "all" {
		return runArtifactListAll()
	}

	ws, idx, err := resolveWorkspace()
	if err != nil {
		return err
	}
	rows := filterArtifacts(idx.Artifacts)

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

func runArtifactListAll() error {
	var all []artifacts.Artifact
	var workspaces []string
	var archives []string
	var err error
	if artifactIncludeArch {
		all, workspaces, archives, err = artifacts.CrawlEverything(0, flagArchiveBase)
	} else {
		all, workspaces, err = artifacts.CrawlAll(0)
	}
	if err != nil {
		return err
	}
	_ = archives
	rows := filterArtifacts(all)

	if artifactJSON {
		out := struct {
			Workspaces []string             `json:"workspaces"`
			Artifacts  []artifacts.Artifact `json:"artifacts"`
		}{workspaces, rows}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	if artifactPaths {
		for _, a := range rows {
			fmt.Println(workspaceAbsPath(workspaces, a))
		}
		return nil
	}

	fmt.Printf("# workspaces=%d artifacts=%d (filtered)\n", len(workspaces), len(rows))
	currentRepo := ""
	for _, a := range rows {
		if a.Repo != currentRepo {
			currentRepo = a.Repo
			fmt.Printf("\n## %s (%s)\n", a.Repo, a.Branch)
		}
		fmt.Printf("%-12s %-8s %-30s %s\n", a.Type, a.Status, a.Feature+"/"+a.Domain+a.Name, a.ID)
	}
	return nil
}

// workspaceAbsPath finds the workspace dir whose repo matches the artifact's
// repo, then joins the relative artifact path. Falls back to the artifact's
// own .Path when no match is found.
func workspaceAbsPath(workspaces []string, a artifacts.Artifact) string {
	for _, ws := range workspaces {
		if filepath.Base(filepath.Dir(ws)) == a.Repo {
			return filepath.Join(ws, a.Path)
		}
	}
	return a.Path
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

func runArtifactStale(cmd *cobra.Command, args []string) error {
	var rows []artifacts.Artifact
	if artifactStaleAll {
		all, _, err := artifacts.CrawlAll(0)
		if err != nil {
			return err
		}
		rows = all
	} else {
		_, idx, err := resolveWorkspace()
		if err != nil {
			return err
		}
		rows = idx.Artifacts
	}

	rows = filterArtifacts(rows)

	useTier := artifactStaleDays == 0
	threshold := time.Now().AddDate(0, 0, -artifactStaleDays)
	now := time.Now()

	type staleEntry struct {
		a    artifacts.Artifact
		note string
	}
	stale := make([]staleEntry, 0)
	for _, a := range rows {
		if a.Status == "done" {
			continue
		}
		if a.Status == "stale" {
			stale = append(stale, staleEntry{a, "explicit"})
			continue
		}
		if useTier {
			if artifacts.IsStale(a, now) {
				note := "tier-" + string(artifacts.TierFor(a.Type))
				if artifacts.IsDurableStale(a, now) {
					note = "durable-stale"
				}
				stale = append(stale, staleEntry{a, note})
			}
			continue
		}
		if a.Updated == "" {
			continue
		}
		t, err := time.Parse("2006-01-02", a.Updated)
		if err != nil {
			continue
		}
		if t.Before(threshold) {
			stale = append(stale, staleEntry{a, "age"})
		}
	}

	if len(stale) == 0 {
		fmt.Fprintln(os.Stderr, "no stale artifacts")
		return nil
	}

	if useTier {
		fmt.Printf("# stale (tier policy, total=%d)\n", len(stale))
	} else {
		fmt.Printf("# stale (threshold=%dd, total=%d)\n", artifactStaleDays, len(stale))
	}
	for _, s := range stale {
		fmt.Printf("%-12s %-8s %-14s %-22s %-16s %s\n",
			s.a.Type, s.a.Status, s.note, s.a.Feature+"/"+s.a.Domain+s.a.Name, s.a.Updated, s.a.ID)
	}
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

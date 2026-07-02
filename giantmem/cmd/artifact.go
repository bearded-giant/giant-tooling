package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/projection"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
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
	artifactSince       string
	artifactUntil       string
	artifactSinceDate   string
	artifactUntilDate   string
)

// resolveArtifactDates validates --since/--until and stashes them as YYYY-MM-DD
// bounds (compared lexically against Artifact.Updated). A bad spec errors before
// any query runs.
func resolveArtifactDates() error {
	artifactSinceDate, artifactUntilDate = "", ""
	if artifactSince != "" {
		t, err := search.ParseSince(artifactSince)
		if err != nil {
			return err
		}
		artifactSinceDate = t.Format("2006-01-02")
	}
	if artifactUntil != "" {
		t, err := search.ParseUntil(artifactUntil)
		if err != nil {
			return err
		}
		artifactUntilDate = t.Format("2006-01-02")
	}
	return nil
}

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

var artifactSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Project live_docs into the artifacts table (derive + canonical backfill + embed changed)",
	Long: `Manual escape hatch — normally giantmemd reconciles automatically at start
and continuously via fsnotify. Use this to force a full pass (first-time
backfill, or after editing files while the daemon was stopped). Embeds real
vectors only when GIANTMEM_EMBED_BACKEND is set to a non-stub backend.`,
	RunE: runArtifactSync,
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

var (
	artifactSearchBackend string
	artifactSearchLimit   int
)

var artifactSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Hybrid (FTS + vector + recency + access) search across filtered artifacts",
	Long: `Run hybrid scoring against the current filter set. Requires embeddings
written via 'giantmem embed --backfill'. Backend defaults to the configured
embedder (env: GIANTMEM_EMBED_BACKEND). Score weights are env-tunable
(GIANTMEM_HYBRID_{FTS,VEC,RECENCY,ACCESS}_WEIGHT, must sum to 1.0).`,
	Args: cobra.ExactArgs(1),
	RunE: runArtifactSearch,
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
		c.Flags().StringVar(&artifactSince, "since", "", `only artifacts updated on/after (e.g. "7d", "2026-06-01", RFC3339)`)
		c.Flags().StringVar(&artifactUntil, "until", "", `only artifacts updated on/before (e.g. "1d", "2026-06-30", RFC3339)`)
	}
	artifactStaleCmd.Flags().IntVar(&artifactStaleDays, "days", 30, "stale threshold in days; 0 = use lifecycle tier policy")
	artifactStaleCmd.Flags().BoolVar(&artifactStaleAll, "all-repos", false, "scan every discovered workspace, not just current")
	artifactStaleCmd.Flags().StringVar(&artifactScope, "scope", "", "filter by scope id")
	artifactStaleCmd.Flags().StringSliceVar(&artifactLifecycle, "lifecycle", nil, "filter by lifecycle")

	artifactSearchCmd.Flags().StringVar(&artifactSearchBackend, "backend", "", "embedder backend (stub|python|ollama; default $GIANTMEM_EMBED_BACKEND)")
	artifactSearchCmd.Flags().IntVar(&artifactSearchLimit, "limit", 10, "max results")
	artifactSearchCmd.Flags().StringSliceVarP(&artifactType, "type", "t", nil, "filter by type")
	artifactSearchCmd.Flags().StringSliceVarP(&artifactStatus, "status", "s", nil, "filter by status")
	artifactSearchCmd.Flags().StringVarP(&artifactFeature, "feature", "f", "", "filter by feature")
	artifactSearchCmd.Flags().StringVar(&artifactScope, "scope", "", "filter by scope id")
	artifactSearchCmd.Flags().StringSliceVar(&artifactLifecycle, "lifecycle", nil, "filter by lifecycle")
	artifactSearchCmd.Flags().StringVar(&artifactRepo, "repo", "current", "current|all|<repo>")
	artifactSearchCmd.Flags().BoolVar(&artifactJSON, "json", false, "JSON output")

	artifactCmd.AddCommand(artifactListCmd, artifactShowCmd, artifactReindexCmd, artifactOrphansCmd, artifactStaleCmd, artifactSearchCmd, artifactSyncCmd)
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
		if artifactSinceDate != "" && a.Updated < artifactSinceDate {
			continue
		}
		if artifactUntilDate != "" && a.Updated >= artifactUntilDate {
			continue
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
	if err := resolveArtifactDates(); err != nil {
		return err
	}
	// Prefer the SQL projection when populated; fall back to a filesystem scan
	// on first run (before any reconcile has filled the artifacts table).
	if live := openLiveDBQuiet(); live != nil {
		if artifacts.TableHasRows(live) {
			defer live.Close()
			return runArtifactListFromTable(live)
		}
		live.Close()
	}

	if artifactRepo == "all" {
		return runArtifactListAll()
	}

	ws, idx, err := resolveWorkspace()
	if err != nil {
		return err
	}
	rows := filterArtifacts(idx.Artifacts)
	rows = enrichWithAccessCounts(rows)
	logListAccess(rows, artifactListFilterSummary())

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
	rows = enrichWithAccessCounts(rows)
	logListAccess(rows, artifactListFilterSummary())

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

// runArtifactListFromTable serves `artifact list` from the SQL projection.
// Filtering reuses filterArtifacts so scope/lifecycle/repo semantics stay
// identical to the filesystem path; only the data source changes.
func runArtifactListFromTable(live *sql.DB) error {
	all, err := artifacts.ListArtifacts(live, artifacts.ListFilter{}, "", 0)
	if err != nil {
		return err
	}

	// Default (no --repo, or --repo current) scopes to the current repo, matching
	// the filesystem path that only scanned the current workspace.
	if artifactRepo == "" || artifactRepo == "current" {
		currentRepo := ""
		if cwd, err := os.Getwd(); err == nil {
			if ws, ok := artifacts.FindWorkspace(cwd); ok {
				currentRepo, _ = artifacts.DetectRepoBranch(ws)
			}
		}
		saved := artifactRepo
		artifactRepo = currentRepo
		defer func() { artifactRepo = saved }()
	}

	rows := filterArtifacts(all)
	logListAccess(rows, artifactListFilterSummary())

	if artifactJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"artifacts": rows})
	}
	if artifactPaths {
		for _, a := range rows {
			fmt.Println(tableArtifactAbsPath(a))
		}
		return nil
	}

	fmt.Printf("# source=live.db artifacts=%d\n", len(rows))
	hdr := ""
	for _, a := range rows {
		if a.Repo != hdr {
			hdr = a.Repo
			fmt.Printf("\n## %s (%s)\n", a.Repo, a.Branch)
		}
		fmt.Printf("%-12s %-8s %-30s %s\n", a.Type, a.Status, a.Feature+"/"+a.Domain+a.Name, a.ID)
	}
	return nil
}

func tableArtifactAbsPath(a artifacts.Artifact) string {
	if a.Worktree != "" {
		return filepath.Join(a.Worktree, ".giantmem", a.Path)
	}
	return a.Path
}

func runArtifactSync(cmd *cobra.Command, args []string) error {
	live, err := db.Open(liveDBPath())
	if err != nil {
		return err
	}
	defer live.Close()

	embedder, err := search.NewEmbedder("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "embeddings disabled: %v\n", err)
		embedder = nil
	}
	if embedder != nil {
		defer embedder.Close()
	}

	st, err := projection.Reconcile(live, flagArchiveBase, embedder)
	if err != nil {
		return err
	}
	fmt.Printf("synced live.db -> artifacts: scanned=%d upserted=%d removed=%d canonical_backfilled=%d embedded=%d embed_skipped=%d (embeddings=%v)\n",
		st.Scanned, st.Upserted, st.Removed, st.Canonical, st.Embedded, st.EmbedSkipped, st.Embeddings)
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
	logShowAccess(match.ID)
	fmt.Printf("# %s\n# path: %s\n# status: %s\n\n", match.ID, abs, match.Status)
	os.Stdout.Write(raw)
	return nil
}

func openLiveDBQuiet() *sql.DB {
	d, err := db.Open(liveDBPath())
	if err != nil {
		return nil
	}
	return d
}

func enrichWithAccessCounts(rows []artifacts.Artifact) []artifacts.Artifact {
	if len(rows) == 0 {
		return rows
	}
	d := openLiveDBQuiet()
	if d == nil {
		return rows
	}
	defer d.Close()
	since := time.Now().AddDate(0, 0, -30)
	counts, err := artifacts.AccessCounts(d, since)
	if err != nil {
		return rows
	}
	for i := range rows {
		if n, ok := counts[rows[i].ID]; ok {
			rows[i].AccessCount = n
		}
	}
	return rows
}

func logListAccess(rows []artifacts.Artifact, query string) {
	if len(rows) == 0 {
		return
	}
	d := openLiveDBQuiet()
	if d == nil {
		return
	}
	defer d.Close()
	ids := make([]string, len(rows))
	ranks := make([]int, len(rows))
	for i, a := range rows {
		ids[i] = a.ID
		ranks[i] = i + 1
	}
	_ = artifacts.LogAccesses(d, ids, ranks, query)
}

func logShowAccess(id string) {
	d := openLiveDBQuiet()
	if d == nil {
		return
	}
	defer d.Close()
	_ = artifacts.LogAccess(d, id, "", 0)
}

func artifactListFilterSummary() string {
	pairs := map[string]string{}
	if len(artifactType) > 0 {
		pairs["type"] = strings.Join(artifactType, ",")
	}
	if len(artifactStatus) > 0 {
		pairs["status"] = strings.Join(artifactStatus, ",")
	}
	if artifactFeature != "" {
		pairs["feature"] = artifactFeature
	}
	if artifactDomain != "" {
		pairs["domain"] = artifactDomain
	}
	if artifactRepo != "" {
		pairs["repo"] = artifactRepo
	}
	if artifactBranch != "" {
		pairs["branch"] = artifactBranch
	}
	if artifactScope != "" {
		pairs["scope"] = artifactScope
	}
	if len(artifactLifecycle) > 0 {
		pairs["lifecycle"] = strings.Join(artifactLifecycle, ",")
	}
	return artifacts.AccessFilterSummary(pairs)
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

	// Also refresh the live.db projection so the SQL read path stays in sync
	// with disk. Best-effort: the daemon reconciles continuously anyway.
	if live := openLiveDBQuiet(); live != nil {
		defer live.Close()
		if st, err := projection.Reconcile(live, flagArchiveBase, nil); err == nil {
			fmt.Printf("reconciled artifacts table: upserted=%d removed=%d canonical_backfilled=%d\n",
				st.Upserted, st.Removed, st.Canonical)
		}
	}
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

func runArtifactSearch(cmd *cobra.Command, args []string) error {
	query := args[0]

	weights := search.DefaultHybridWeights()
	if err := weights.Validate(); err != nil {
		return err
	}

	// Source the corpus from the projection table (same as MCP) so CLI search
	// reflects the live memory store, not a one-off filesystem crawl.
	rows, err := mcpSourceArtifacts(artifactRepo)
	if err != nil {
		return err
	}
	rows = filterArtifacts(rows)
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "no artifacts match the filters")
		return nil
	}

	// Query vector: an explicit --backend builds a local embedder; otherwise
	// borrow the daemon's real embedder (sole model owner). nil queryVec =>
	// Hybrid runs FTS/recency-only instead of scoring against a stub vector.
	var queryVec []float32
	var modelLabel string
	if artifactSearchBackend != "" {
		embedder, err := search.NewEmbedder(artifactSearchBackend)
		if err != nil {
			return err
		}
		defer embedder.Close()
		queryVec, err = embedder.Embed(query)
		if err != nil {
			return fmt.Errorf("embed query: %w", err)
		}
		modelLabel = embedder.ModelName()
	} else {
		queryVec, modelLabel = resolveQueryVector(query)
		if modelLabel == "" {
			modelLabel = "none (FTS-only; start daemon for semantic)"
		}
	}

	live, err := db.Open(liveDBPath())
	if err != nil {
		return err
	}
	defer live.Close()

	results, err := search.Hybrid(live, query, queryVec, rows, weights, artifactSearchLimit)
	if err != nil {
		return err
	}

	logSearchAccess(results, query)

	if artifactJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"query":   query,
			"weights": weights,
			"results": results,
		})
	}

	fmt.Printf("# semantic (model=%s, weights fts=%.2f vec=%.2f rec=%.2f acc=%.2f)\n",
		modelLabel,
		weights.FTS, weights.Vector, weights.Recency, weights.Access)
	for i, r := range results {
		fmt.Printf("%2d  %.3f  %-14s %-22s %s\n",
			i+1, r.Score, r.Artifact.Type,
			r.Artifact.Feature+"/"+r.Artifact.Domain+r.Artifact.Name,
			r.Artifact.ID)
	}
	return nil
}

func embedBackendForSearch() string {
	if artifactSearchBackend != "" {
		return artifactSearchBackend
	}
	if v := os.Getenv("GIANTMEM_EMBED_BACKEND"); v != "" {
		return v
	}
	return "stub"
}

func logSearchAccess(results []search.HybridResult, query string) {
	if len(results) == 0 {
		return
	}
	d := openLiveDBQuiet()
	if d == nil {
		return
	}
	defer d.Close()
	ids := make([]string, len(results))
	ranks := make([]int, len(results))
	for i, r := range results {
		ids[i] = r.Artifact.ID
		ranks[i] = i + 1
	}
	_ = artifacts.LogAccesses(d, ids, ranks, "semantic:"+query)
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

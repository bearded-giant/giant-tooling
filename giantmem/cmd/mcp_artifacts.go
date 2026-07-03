package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/mark3labs/mcp-go/mcp"
)

// ----- find_artifact ---------------------------------------------------------

type findArtifactArgs struct {
	Type      string  `json:"type"`
	Domain    string  `json:"domain"`
	Status    string  `json:"status"`
	Feature   string  `json:"feature"`
	Repo      string  `json:"repo"`
	Branch    string  `json:"branch"`
	Scope     string  `json:"scope"`
	Lifecycle string  `json:"lifecycle"`
	Query     string  `json:"query"`
	Since     string  `json:"since"`
	Until     string  `json:"until"`
	Limit     float64 `json:"limit"`
	Semantic  bool    `json:"semantic"`
}

type artifactHit struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Feature     string `json:"feature,omitempty"`
	Domain      string `json:"domain,omitempty"`
	Status      string `json:"status"`
	Path        string `json:"path"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Lifecycle   string `json:"lifecycle,omitempty"`
	AccessCount int    `json:"access_count,omitempty"`
	Updated     string `json:"updated,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
}

func findArtifactHandler(_ context.Context, _ mcp.CallToolRequest, args findArtifactArgs) (*mcp.CallToolResult, error) {
	limit := int(args.Limit)
	if limit <= 0 {
		limit = 20
	}

	var sinceDate, untilDate string
	if args.Since != "" {
		t, err := search.ParseSince(args.Since)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sinceDate = t.Format("2006-01-02")
	}
	if args.Until != "" {
		t, err := search.ParseUntil(args.Until)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		untilDate = t.Format("2006-01-02")
	}

	rows, err := mcpSourceArtifacts(args.Repo)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	out := make([]artifactHit, 0, len(rows))
	wantTypes := mcpSplitCSV(args.Type)
	wantStatus := mcpSplitCSV(args.Status)
	wantLifecycle := mcpSplitCSV(args.Lifecycle)

	var registry *artifacts.ScopeRegistry
	if args.Scope != "" {
		registry, _ = artifacts.LoadScopeRegistry(artifacts.ScopesYAMLPath())
	}

	for _, a := range rows {
		if len(wantTypes) > 0 && !mcpContains(wantTypes, a.Type) {
			continue
		}
		if len(wantStatus) > 0 && !mcpContains(wantStatus, a.Status) {
			continue
		}
		if len(wantLifecycle) > 0 {
			lc := a.Lifecycle
			if lc == "" {
				lc = artifacts.LifecycleDurable
			}
			if !mcpContains(wantLifecycle, lc) {
				continue
			}
		}
		if args.Feature != "" && a.Feature != args.Feature {
			continue
		}
		if args.Domain != "" && a.Domain != args.Domain {
			continue
		}
		if args.Branch != "" && a.Branch != args.Branch {
			continue
		}
		if sinceDate != "" && a.Updated < sinceDate {
			continue
		}
		if untilDate != "" && a.Updated >= untilDate {
			continue
		}
		if args.Scope != "" {
			if a.Scope != "" {
				if a.Scope != args.Scope {
					continue
				}
			} else if registry == nil || !registry.MatchScope(a.Repo, a.Scope, args.Scope) {
				continue
			}
		}
		if args.Semantic {
			// Semantic mode collects all matches; ranking runs below.
			out = append(out, mcpArtifactHit(a, ""))
			continue
		}
		if args.Query != "" {
			snip, ok := mcpGrepSnippet(a, args.Query)
			if !ok {
				continue
			}
			out = append(out, mcpArtifactHit(a, snip))
		} else {
			out = append(out, mcpArtifactHit(a, ""))
		}
		if len(out) >= limit {
			break
		}
	}

	if args.Semantic && args.Query != "" {
		out = mcpHybridRerank(out, rows, args.Query, limit)
	}

	mcpLogArtifactAccess(out, mcpFindFilterSummary(args))

	return jsonResult(map[string]any{
		"results": out,
		"total":   len(out),
	})
}

func mcpHybridRerank(hits []artifactHit, candidates []artifacts.Artifact, query string, limit int) []artifactHit {
	weights := search.DefaultHybridWeights()
	if err := weights.Validate(); err != nil {
		return capHits(hits, limit)
	}
	live := openLiveDBQuiet()
	if live == nil {
		return capHits(hits, limit)
	}
	defer live.Close()

	// Prefer the daemon's embedder (sole model owner). nil => no real embedder
	// anywhere; Hybrid then skips the vector arm and reranks by FTS/recency only
	// rather than polluting scores with a stub vector.
	queryVec, _ := resolveQueryVector(query)

	byID := map[string]artifacts.Artifact{}
	for _, a := range candidates {
		byID[a.ID] = a
	}
	picks := make([]artifacts.Artifact, 0, len(hits))
	for _, h := range hits {
		if a, ok := byID[h.ID]; ok {
			picks = append(picks, a)
		}
	}
	results, err := search.Hybrid(live, query, queryVec, picks, weights, limit)
	if err != nil {
		return capHits(hits, limit)
	}
	out := make([]artifactHit, 0, len(results))
	for _, r := range results {
		out = append(out, mcpArtifactHit(r.Artifact, ""))
	}
	return out
}

func capHits(hits []artifactHit, limit int) []artifactHit {
	if limit > 0 && len(hits) > limit {
		return hits[:limit]
	}
	return hits
}

func mcpFindFilterSummary(args findArtifactArgs) string {
	pairs := map[string]string{
		"type":      args.Type,
		"domain":    args.Domain,
		"status":    args.Status,
		"feature":   args.Feature,
		"repo":      args.Repo,
		"branch":    args.Branch,
		"scope":     args.Scope,
		"lifecycle": args.Lifecycle,
		"query":     args.Query,
	}
	return artifacts.AccessFilterSummary(pairs)
}

func mcpLogArtifactAccess(hits []artifactHit, query string) {
	if len(hits) == 0 {
		return
	}
	live := openLiveDBQuiet()
	if live == nil {
		return
	}
	defer live.Close()
	ids := make([]string, len(hits))
	ranks := make([]int, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
		ranks[i] = i + 1
	}
	_ = artifacts.LogAccesses(live, ids, ranks, query)
}

func mcpArtifactHit(a artifacts.Artifact, snippet string) artifactHit {
	return artifactHit{
		ID:          a.ID,
		Type:        a.Type,
		Feature:     a.Feature,
		Domain:      a.Domain,
		Status:      a.Status,
		Path:        a.Path,
		Repo:        a.Repo,
		Branch:      a.Branch,
		Scope:       a.Scope,
		Lifecycle:   a.Lifecycle,
		AccessCount: a.AccessCount,
		Updated:     a.Updated,
		Snippet:     snippet,
	}
}

// mcpSourceArtifacts returns the artifact corpus for an MCP query, preferring
// the SQL projection (cross-repo, no filesystem crawl) when it's populated and
// falling back to a filesystem crawl/scan on first run. repo: ""/"all" => every
// repo, "current" => the cwd's repo, otherwise a named repo.
func mcpSourceArtifacts(repo string) ([]artifacts.Artifact, error) {
	if live := openLiveDBQuiet(); live != nil {
		if artifacts.TableHasRows(live) {
			defer live.Close()
			all, err := artifacts.ListArtifacts(live, artifacts.ListFilter{}, "", 0)
			if err != nil {
				return nil, err
			}
			switch repo {
			case "", "all":
				return all, nil
			case "current":
				return mcpFilterRepo(all, mcpCurrentRepoName()), nil
			default:
				return mcpFilterRepo(all, repo), nil
			}
		}
		live.Close()
	}

	// filesystem fallback (table empty / live.db absent)
	if repo == "current" {
		_, idx, err := mcpResolveCurrentWorkspace()
		if err != nil {
			return nil, err
		}
		return idx.Artifacts, nil
	}
	all, _, err := artifacts.CrawlAll(0)
	if err != nil {
		return nil, err
	}
	if repo == "" || repo == "all" {
		return all, nil
	}
	return mcpFilterRepo(all, repo), nil
}

func mcpFilterRepo(rows []artifacts.Artifact, repo string) []artifacts.Artifact {
	if repo == "" {
		return rows
	}
	out := make([]artifacts.Artifact, 0, len(rows))
	for _, a := range rows {
		if a.Repo == repo {
			out = append(out, a)
		}
	}
	return out
}

func mcpCurrentRepoName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	ws, ok := artifacts.FindWorkspace(cwd)
	if !ok {
		return ""
	}
	repo, _ := artifacts.DetectRepoBranch(ws)
	return repo
}

func mcpResolveCurrentWorkspace() (string, *artifacts.Index, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	ws, ok := artifacts.FindWorkspace(cwd)
	if !ok {
		return "", nil, mcpErr("no .giantmem/ found at cwd")
	}
	idx, err := artifacts.LoadOrScan(ws)
	if err != nil {
		return ws, nil, err
	}
	return ws, idx, nil
}

func mcpSplitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mcpContains(set []string, v string) bool {
	for _, s := range set {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

// mcpGrepSnippet runs a case-insensitive substring scan over the artifact's
// file body. Returns ok=false when no match. Snippets are 200 chars centered
// on the match.
func mcpGrepSnippet(a artifacts.Artifact, query string) (string, bool) {
	abs := mcpArtifactAbsPath(a)
	if abs == "" {
		return "", false
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", false
	}
	body := string(raw)
	idx := strings.Index(strings.ToLower(body), strings.ToLower(query))
	if idx < 0 {
		return "", false
	}
	start := idx - 80
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + 80
	if end > len(body) {
		end = len(body)
	}
	return strings.ReplaceAll(body[start:end], "\n", " "), true
}

func mcpArtifactAbsPath(a artifacts.Artifact) string {
	for _, ws := range artifacts.DiscoverWorkspaces(0) {
		if filepath.Base(filepath.Dir(ws)) == a.Repo {
			return filepath.Join(ws, a.Path)
		}
	}
	return ""
}

func mcpErr(msg string) error { return &mcpErrType{msg: msg} }

type mcpErrType struct{ msg string }

func (e *mcpErrType) Error() string { return e.msg }

// ----- get_artifact ----------------------------------------------------------

type getArtifactArgs struct {
	ID string `json:"id"`
}

func getArtifactHandler(_ context.Context, _ mcp.CallToolRequest, args getArtifactArgs) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(args.ID) == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	all, err := mcpSourceArtifacts("all")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var match *artifacts.Artifact
	for i := range all {
		if all[i].ID == args.ID {
			match = &all[i]
			break
		}
	}
	if match == nil {
		return mcp.NewToolResultError("no artifact with id " + args.ID), nil
	}
	abs := mcpArtifactAbsPath(*match)
	if abs == "" {
		return mcp.NewToolResultError("could not resolve path for " + args.ID), nil
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fm, body, _ := artifacts.ParseFrontmatter(string(raw))
	return jsonResult(map[string]any{
		"id":          match.ID,
		"path":        abs,
		"frontmatter": fm,
		"content":     body,
	})
}

// ----- find_entity ----------------------------------------------------------

type findEntityArgs struct {
	Name string `json:"name"`
	Repo string `json:"repo"`
}

func findEntityHandler(_ context.Context, _ mcp.CallToolRequest, args findEntityArgs) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(args.Name) == "" {
		return mcp.NewToolResultError("name is required"), nil
	}
	repo := args.Repo
	if repo == "" {
		repo = "all"
	}
	var corpus []artifacts.Artifact
	if repo == "current" {
		_, idx, err := mcpResolveCurrentWorkspace()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		corpus = idx.Artifacts
	} else {
		all, _, err := artifacts.CrawlAll(0)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if repo == "all" {
			corpus = all
		} else {
			for _, a := range all {
				if a.Repo == repo {
					corpus = append(corpus, a)
				}
			}
		}
	}
	entities, err := artifacts.LoadEntities(corpus)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	e, ok := artifacts.FindEntity(entities, args.Name)
	if !ok {
		return mcp.NewToolResultError("no entity matching " + args.Name), nil
	}
	return jsonResult(e)
}

// ----- list_features_with_artifacts ------------------------------------------

type listFeaturesWithArtifactsArgs struct {
	Repo          string `json:"repo"`
	ArtifactTypes string `json:"artifact_types"`
}

func listFeaturesWithArtifactsHandler(_ context.Context, _ mcp.CallToolRequest, args listFeaturesWithArtifactsArgs) (*mcp.CallToolResult, error) {
	repo := args.Repo
	if repo == "" {
		repo = "current"
	}

	var rows []artifacts.Artifact
	if repo == "current" {
		_, idx, err := mcpResolveCurrentWorkspace()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rows = idx.Artifacts
	} else {
		all, _, err := artifacts.CrawlAll(0)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		for _, a := range all {
			if repo == "all" || a.Repo == repo {
				rows = append(rows, a)
			}
		}
	}

	wantTypes := mcpSplitCSV(args.ArtifactTypes)
	grouped := map[string][]artifactHit{}
	for _, a := range rows {
		if a.Feature == "" {
			continue
		}
		if len(wantTypes) > 0 && !mcpContains(wantTypes, a.Type) {
			continue
		}
		key := a.Repo + ":" + a.Feature
		grouped[key] = append(grouped[key], mcpArtifactHit(a, ""))
	}

	type featureGroup struct {
		Repo      string        `json:"repo"`
		Feature   string        `json:"feature"`
		Count     int           `json:"count"`
		Artifacts []artifactHit `json:"artifacts"`
	}
	out := make([]featureGroup, 0, len(grouped))
	for key, items := range grouped {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		out = append(out, featureGroup{
			Repo:      parts[0],
			Feature:   parts[1],
			Count:     len(items),
			Artifacts: items,
		})
	}
	return jsonResult(map[string]any{
		"features": out,
		"total":    len(out),
	})
}

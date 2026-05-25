package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
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
	Limit     float64 `json:"limit"`
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

	var rows []artifacts.Artifact
	if args.Repo == "all" || args.Repo == "" {
		// "all" or unspecified => crawl every workspace; better fan-out for MCP
		// callers asking cross-repo questions
		all, _, err := artifacts.CrawlAll(0)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rows = all
	} else if args.Repo == "current" {
		ws, idx, err := mcpResolveCurrentWorkspace()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rows = idx.Artifacts
		_ = ws
	} else {
		all, _, err := artifacts.CrawlAll(0)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		for _, a := range all {
			if a.Repo == args.Repo {
				rows = append(rows, a)
			}
		}
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
		if args.Scope != "" {
			if a.Scope != "" {
				if a.Scope != args.Scope {
					continue
				}
			} else if registry == nil || !registry.MatchScope(a.Repo, a.Scope, args.Scope) {
				continue
			}
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

	mcpLogArtifactAccess(out, mcpFindFilterSummary(args))

	return jsonResult(map[string]any{
		"results": out,
		"total":   len(out),
	})
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
	all, _, err := artifacts.CrawlAll(0)
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

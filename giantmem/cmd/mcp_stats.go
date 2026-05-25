package cmd

import (
	"context"
	"sort"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/mark3labs/mcp-go/mcp"
)

// ----- get_stats -------------------------------------------------------------

type getStatsArgs struct {
	Scope   string `json:"scope"`
	Repo    string `json:"repo"`
	Feature string `json:"feature"`
}

type statsPayload struct {
	Total             int                       `json:"total"`
	ByType            map[string]int            `json:"by_type"`
	ByLifecycle       map[string]int            `json:"by_lifecycle"`
	ByStatus          map[string]int            `json:"by_status"`
	ByRepo            map[string]int            `json:"by_repo"`
	RecentWrites24h   int                       `json:"recent_writes_24h"`
	RecentAccesses24h int                       `json:"recent_accesses_24h"`
	TopAccessed       []artifacts.AccessSummary `json:"top_accessed"`
	Scope             string                    `json:"scope,omitempty"`
	Repo              string                    `json:"repo,omitempty"`
	Feature           string                    `json:"feature,omitempty"`
}

func getStatsHandler(_ context.Context, _ mcp.CallToolRequest, args getStatsArgs) (*mcp.CallToolResult, error) {
	rows, err := mcpStatsCollect(args)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	p := statsPayload{
		Total:       len(rows),
		ByType:      map[string]int{},
		ByLifecycle: map[string]int{},
		ByStatus:    map[string]int{},
		ByRepo:      map[string]int{},
		Scope:       args.Scope,
		Repo:        args.Repo,
		Feature:     args.Feature,
	}
	for _, a := range rows {
		p.ByType[a.Type]++
		lc := a.Lifecycle
		if lc == "" {
			lc = artifacts.LifecycleDurable
		}
		p.ByLifecycle[lc]++
		p.ByStatus[a.Status]++
		p.ByRepo[a.Repo]++
		if a.Updated == today {
			p.RecentWrites24h++
		}
	}

	live := openLiveDBQuiet()
	if live != nil {
		defer live.Close()
		since24 := now.Add(-24 * time.Hour).Format(time.RFC3339)
		var n int
		if err := live.QueryRow(
			`SELECT COUNT(*) FROM artifact_access WHERE accessed_at >= ?`, since24,
		).Scan(&n); err == nil {
			p.RecentAccesses24h = n
		}
		top, _ := artifacts.TopAccessed(live, now.Add(-30*24*time.Hour), 5)
		idSet := map[string]bool{}
		for _, a := range rows {
			idSet[a.ID] = true
		}
		filtered := make([]artifacts.AccessSummary, 0, len(top))
		for _, t := range top {
			if idSet[t.ArtifactID] {
				filtered = append(filtered, t)
			}
		}
		p.TopAccessed = filtered
	}

	return jsonResult(p)
}

func mcpStatsCollect(args getStatsArgs) ([]artifacts.Artifact, error) {
	repo := args.Repo
	if repo == "" {
		repo = "all"
	}

	var rows []artifacts.Artifact
	if repo == "current" {
		_, idx, err := mcpResolveCurrentWorkspace()
		if err != nil {
			return nil, err
		}
		rows = idx.Artifacts
	} else {
		all, _, err := artifacts.CrawlAll(0)
		if err != nil {
			return nil, err
		}
		rows = all
	}

	var registry *artifacts.ScopeRegistry
	if args.Scope != "" {
		registry, _ = artifacts.LoadScopeRegistry(artifacts.ScopesYAMLPath())
	}

	out := make([]artifacts.Artifact, 0, len(rows))
	for _, a := range rows {
		if repo != "" && repo != "all" && repo != "current" && a.Repo != repo {
			continue
		}
		if args.Feature != "" && a.Feature != args.Feature {
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
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

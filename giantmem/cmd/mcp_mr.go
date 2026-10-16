package cmd

import (
	"context"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/mrs"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/mark3labs/mcp-go/mcp"
)

type findMRArgs struct {
	Ref           string `json:"ref"`
	Project       string `json:"project"`
	Since         string `json:"since"`
	IncludeClosed bool   `json:"include_closed"`
	Live          *bool  `json:"live"`
}

func findMRHandler(ctx context.Context, _ mcp.CallToolRequest, args findMRArgs) (*mcp.CallToolResult, error) {
	a, err := db.Open(archiveDBPath())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer a.Close()
	f := mrs.Filter{Ref: args.Ref, Project: args.Project}
	if args.Since != "" {
		t, err := search.ParseSince(args.Since)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		f.Since = t.UTC().Format(time.RFC3339)
	}
	live := args.Live == nil || *args.Live
	list, err := loadMRs(ctx, a, f, live, args.Ref != "")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !args.IncludeClosed && args.Ref == "" {
		keep := list[:0]
		for _, m := range list {
			if !m.Status.Terminal() {
				keep = append(keep, m)
			}
		}
		list = keep
	}
	return jsonResult(map[string]any{"mrs": list, "total": len(list)})
}

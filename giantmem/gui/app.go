package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

type App struct {
	ctx  context.Context
	live *sql.DB
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	live, err := db.Open(filepath.Join(archiveBase(), "live.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "gui: open live.db: %v\n", err)
		return
	}
	a.live = live
}

func (a *App) shutdown(ctx context.Context) {
	if a.live != nil {
		a.live.Close()
		a.live = nil
	}
}

// ListArtifacts returns artifact rows filtered + sorted. limit<=0 means no limit.
// Frontend sees a JS-side artifacts.ListFilter object (Type/Status/Lifecycle as
// arrays; Scope/Repo/Branch/Feature/Domain as strings).
func (a *App) ListArtifacts(filter artifacts.ListFilter, sortBy string, limit int) ([]artifacts.Artifact, error) {
	if a.live == nil {
		return nil, fmt.Errorf("live db not open")
	}
	return artifacts.ListArtifacts(a.live, filter, sortBy, limit)
}

// FacetCountsResult bundles the three facet maps returned by
// artifacts.FacetCounts so Wails can ship them as one JS object.
type FacetCountsResult struct {
	ByType      map[string]int `json:"byType"`
	ByLifecycle map[string]int `json:"byLifecycle"`
	ByStatus    map[string]int `json:"byStatus"`
}

func (a *App) FacetCounts() (FacetCountsResult, error) {
	if a.live == nil {
		return FacetCountsResult{}, fmt.Errorf("live db not open")
	}
	t, l, s, err := artifacts.FacetCounts(a.live)
	if err != nil {
		return FacetCountsResult{}, err
	}
	return FacetCountsResult{ByType: t, ByLifecycle: l, ByStatus: s}, nil
}

func archiveBase() string {
	if v := os.Getenv("GIANTMEM_ARCHIVE_BASE"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "giantmem_archive")
}

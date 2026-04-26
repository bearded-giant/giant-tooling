// Package sources defines the ingest plugin model. Each Source emits Docs into
// a channel; the central ingest loop upserts them into archives.db.
package sources

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Doc is the canonical doc shape every source emits.
type Doc struct {
	Filepath   string            `json:"filepath"`
	Project    string            `json:"project"`
	SourceType string            `json:"source_type"`
	Timestamp  string            `json:"timestamp"`
	DirType    string            `json:"dir_type,omitempty"`
	SessionID  string            `json:"session_id,omitempty"`
	Topic      string            `json:"topic,omitempty"`
	IsLatest   bool              `json:"is_latest,omitempty"`
	Cwd        string            `json:"cwd,omitempty"`
	Content    string            `json:"content"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// Stats records counts for one source run.
type Stats struct {
	Count int
	Errs  int
}

// Source produces docs. Builtin sources read from disk, external sources spawn
// a subprocess. Emit must close the chan when done; errors go to errCh.
type Source interface {
	Name() string
	Kind() string // "builtin" or "external"
	Enabled() bool
	Emit(ctx context.Context, opts EmitOptions) (<-chan Doc, <-chan error)
}

// EmitOptions are passed to every source on each run.
type EmitOptions struct {
	ArchiveBase    string
	ClaudeProjects string
	Project        string // optional filter
	Force          bool
	DB             *sql.DB // some builtins need to query existing rows for incremental
}

// Config maps to ~/.config/giantmem/sources.toml.
type Config struct {
	Source []SourceConfig `toml:"source"`
}

// SourceConfig is one [[source]] block.
type SourceConfig struct {
	Name       string            `toml:"name"`
	Kind       string            `toml:"kind"` // builtin | external
	Enabled    bool              `toml:"enabled"`
	IngestCmd  string            `toml:"ingest_cmd"`
	Parse      string            `toml:"parse"` // json (currently only)
	Mapping    map[string]string `toml:"mapping"`
}

// LoadConfig reads sources.toml. If missing, returns the default builtin set.
func LoadConfig(path string) (*Config, error) {
	if _, err := os.Stat(path); err != nil {
		return DefaultConfig(), nil
	}
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &c, nil
}

// DefaultConfig returns the canonical builtin set when no file exists.
func DefaultConfig() *Config {
	return &Config{
		Source: []SourceConfig{
			{Name: "workspace-md", Kind: "builtin", Enabled: true},
			{Name: "claude-jsonl", Kind: "builtin", Enabled: true},
			{Name: "domain-json", Kind: "builtin", Enabled: true},
		},
	}
}

// DefaultConfigPath returns the canonical sources.toml location.
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "giantmem", "sources.toml")
}

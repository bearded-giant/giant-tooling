package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	Register("memory-md", func(cfg SourceConfig) Source {
		return &memoryMDSource{cfg: cfg}
	})
}

// memoryMDSource ingests Claude harness memory files,
// ~/.claude/projects/<slug>/memory/*.md, into archives.db so they survive a
// live.db rebuild and ride the archive backups. Same-session immediacy still
// comes from the PostToolUse live_index hook writing them into live.db.
type memoryMDSource struct {
	cfg SourceConfig
}

func (s *memoryMDSource) Name() string  { return "memory-md" }
func (s *memoryMDSource) Kind() string  { return "builtin" }
func (s *memoryMDSource) Enabled() bool { return s.cfg.Enabled }

func (s *memoryMDSource) Emit(ctx context.Context, opts EmitOptions) (<-chan Doc, <-chan error) {
	docCh := make(chan Doc, 32)
	errCh := make(chan error, 4)

	go func() {
		defer close(docCh)
		defer close(errCh)
		paths, _ := filepath.Glob(filepath.Join(opts.ClaudeProjects, "*", "memory", "*.md"))
		for _, p := range paths {
			project := MemoryProjectName(p)
			if opts.Project != "" && !strings.Contains(strings.ToLower(project), strings.ToLower(opts.Project)) {
				continue
			}
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			doc := Doc{
				Filepath:   p,
				Project:    project,
				SourceType: "memory",
				Timestamp:  st.ModTime().Format("20060102_150405"),
				DirType:    "memory",
				IsLatest:   true,
				Content:    readFile(p),
			}
			select {
			case docCh <- doc:
			case <-ctx.Done():
				return
			}
		}
	}()
	return docCh, errCh
}

// MemoryProjectName derives a project label from a harness memory path. The
// slug encodes the cwd with slashes as dashes; the home/dev prefix is dropped
// so `-Users-me-dev-ai-hyphae` becomes `ai-hyphae`, matching live_index.py.
func MemoryProjectName(p string) string {
	slug := filepath.Base(filepath.Dir(filepath.Dir(p)))
	if home, err := os.UserHomeDir(); err == nil {
		prefix := strings.ReplaceAll(home, "/", "-") + "-dev-"
		if strings.HasPrefix(slug, prefix) {
			slug = slug[len(prefix):]
		}
	}
	slug = strings.TrimLeft(slug, "-")
	if slug == "" {
		return "memory"
	}
	return slug
}

package sources

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/ingest"
)

func init() {
	Register("claude-jsonl", func(cfg SourceConfig) Source {
		return &claudeJSONLSource{cfg: cfg}
	})
}

// claudeJSONLSource walks ~/.claude/projects/**/*.jsonl, extracts text +
// session metadata, and emits one Doc per file. Honors mtime-incremental
// behavior via the EmitOptions.DB existing-row lookup unless Force is set.
type claudeJSONLSource struct {
	cfg SourceConfig
}

func (s *claudeJSONLSource) Name() string  { return "claude-jsonl" }
func (s *claudeJSONLSource) Kind() string  { return "builtin" }
func (s *claudeJSONLSource) Enabled() bool { return s.cfg.Enabled }

func (s *claudeJSONLSource) Emit(ctx context.Context, opts EmitOptions) (<-chan Doc, <-chan error) {
	docCh := make(chan Doc, 16)
	errCh := make(chan error, 4)

	go func() {
		defer close(docCh)
		defer close(errCh)
		if opts.ClaudeProjects == "" {
			return
		}

		// build mtime cache for incremental skip
		existing := map[string]time.Time{}
		if !opts.Force && opts.DB != nil {
			rows, err := opts.DB.Query("SELECT filepath, indexed_at FROM documents WHERE source_type = 'session'")
			if err == nil {
				for rows.Next() {
					var fp, ia string
					if err := rows.Scan(&fp, &ia); err == nil {
						if t, err := time.Parse(time.RFC3339, ia); err == nil {
							existing[fp] = t
						}
					}
				}
				rows.Close()
			}
		}

		filepath.WalkDir(opts.ClaudeProjects, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(p, ".jsonl") {
				return nil
			}
			if strings.Contains(p, "subagents") {
				return nil
			}
			projectName := ingest.SessionProjectName(p, opts.ClaudeProjects)
			if opts.Project != "" && !strings.Contains(strings.ToLower(projectName), strings.ToLower(opts.Project)) {
				return nil
			}
			mtime, mErr := ingest.FileMtime(p)
			if mErr != nil {
				return nil
			}
			if !opts.Force {
				if t, ok := existing[p]; ok && !mtime.After(t) {
					return nil
				}
			}
			extract, eErr := ingest.ExtractSessionText(p)
			if eErr != nil || extract == nil {
				return nil
			}
			topic := ingest.LookupTopicOverride(opts.DB, extract.SessionID)
			if topic == "" {
				topic = ingest.DetectTopic(extract.Text)
			}
			ts := mtime.Format("20060102_150405")
			doc := Doc{
				Filepath:   p,
				Project:    projectName,
				SourceType: "session",
				Timestamp:  ts,
				SessionID:  extract.SessionID,
				Topic:      topic,
				Cwd:        extract.Cwd,
				Content:    extract.Text,
			}
			select {
			case docCh <- doc:
			case <-ctx.Done():
				return fs.SkipAll
			}
			return nil
		})
	}()
	return docCh, errCh
}

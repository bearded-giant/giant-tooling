package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func init() {
	Register("session-bundle", func(cfg SourceConfig) Source {
		return &sessionBundleSource{cfg: cfg}
	})
}

const (
	bundleMaxFileSize = 256 << 10
	bundleSniffSize   = 8 << 10
)

var bundleIndexedKinds = map[string]bool{
	"script": true, "patch": true, "doc": true, "data": true, "log": true, "workflow": true,
}

// sessionBundleSource ingests the per-session working files a hook archives
// to <ArchiveBase>/bundles/<slug>/<session-id>/, one Doc per indexable file.
type sessionBundleSource struct {
	cfg SourceConfig
}

type bundleManifest struct {
	SessionID   string `json:"session_id"`
	Cwd         string `json:"cwd"`
	FirstPrompt string `json:"first_prompt"`
	Files       []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"files"`
}

func (s *sessionBundleSource) Name() string  { return "session-bundle" }
func (s *sessionBundleSource) Kind() string  { return "builtin" }
func (s *sessionBundleSource) Enabled() bool { return s.cfg.Enabled }

func (s *sessionBundleSource) Emit(ctx context.Context, opts EmitOptions) (<-chan Doc, <-chan error) {
	docCh := make(chan Doc, 32)
	errCh := make(chan error, 4)

	go func() {
		defer close(docCh)
		defer close(errCh)
		if opts.ArchiveBase == "" {
			return
		}
		existing := bundleIndexedAt(opts)
		manifests, _ := filepath.Glob(filepath.Join(opts.ArchiveBase, "bundles", "*", "*", "bundle.json"))
		for _, mp := range manifests {
			bundleDir := filepath.Dir(mp)
			slug := filepath.Base(filepath.Dir(bundleDir))
			project := MemoryProjectName(filepath.Join("/x", slug, "memory", "x.md"))
			if opts.Project != "" && !strings.Contains(strings.ToLower(project), strings.ToLower(opts.Project)) {
				continue
			}
			raw, err := os.ReadFile(mp)
			if err != nil {
				continue
			}
			var m bundleManifest
			if err := json.Unmarshal(raw, &m); err != nil {
				continue
			}
			sessionID := m.SessionID
			if sessionID == "" {
				sessionID = filepath.Base(bundleDir)
			}
			for _, f := range m.Files {
				rel := filepath.ToSlash(f.Path)
				// IsLocal keeps a crafted manifest from pulling in files outside the bundle
				if !bundleIndexedKinds[f.Kind] || strings.HasPrefix(rel, "claude/subagents/") || !filepath.IsLocal(f.Path) {
					continue
				}
				p := filepath.Join(bundleDir, f.Path)
				st, err := os.Stat(p)
				if err != nil || !st.Mode().IsRegular() || st.Size() > bundleMaxFileSize {
					continue
				}
				if !opts.Force {
					if t, ok := existing[p]; ok && !st.ModTime().After(t) {
						continue
					}
				}
				body, err := os.ReadFile(p)
				if err != nil || bytes.IndexByte(body[:min(len(body), bundleSniffSize)], 0) >= 0 {
					continue
				}
				doc := Doc{
					Filepath:   p,
					Project:    project,
					SourceType: "bundle",
					Timestamp:  st.ModTime().Format("20060102_150405"),
					DirType:    f.Kind,
					SessionID:  sessionID,
					Cwd:        m.Cwd,
					IsLatest:   true,
					Content:    rel + "\n" + m.FirstPrompt + "\n\n" + string(body),
					Metadata:   map[string]string{"bundle": bundleDir, "rel": rel},
				}
				select {
				case docCh <- doc:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return docCh, errCh
}

func bundleIndexedAt(opts EmitOptions) map[string]time.Time {
	existing := map[string]time.Time{}
	if opts.Force || opts.DB == nil {
		return existing
	}
	rows, err := opts.DB.Query("SELECT filepath, indexed_at FROM documents WHERE source_type = 'bundle'")
	if err != nil {
		return existing
	}
	defer rows.Close()
	for rows.Next() {
		var fp, ia string
		if rows.Scan(&fp, &ia) != nil {
			continue
		}
		if t, err := time.Parse(time.DateTime, ia); err == nil {
			existing[fp] = t
		} else if t, err := time.Parse(time.RFC3339, ia); err == nil {
			existing[fp] = t
		}
	}
	return existing
}

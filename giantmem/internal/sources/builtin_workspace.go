package sources

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/ingest"
)

func init() {
	Register("workspace-md", func(cfg SourceConfig) Source {
		return &workspaceMDSource{cfg: cfg}
	})
}

// workspaceMDSource walks ~/giantmem_archive/{project}/{ts}/ for .md files
// and any non-md filebox/* contents. Mirrors the original ingestWorkspaces
// .md and filebox passes.
type workspaceMDSource struct {
	cfg SourceConfig
}

func (s *workspaceMDSource) Name() string  { return "workspace-md" }
func (s *workspaceMDSource) Kind() string  { return "builtin" }
func (s *workspaceMDSource) Enabled() bool { return s.cfg.Enabled }

func (s *workspaceMDSource) Emit(ctx context.Context, opts EmitOptions) (<-chan Doc, <-chan error) {
	docCh := make(chan Doc, 32)
	errCh := make(chan error, 4)

	go func() {
		defer close(docCh)
		defer close(errCh)
		scanRoot := opts.ArchiveBase
		if opts.Project != "" {
			scanRoot = filepath.Join(opts.ArchiveBase, opts.Project)
		}
		latest := ingest.ResolveLatestTimestamps(opts.ArchiveBase)

		filepath.WalkDir(scanRoot, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return fs.SkipDir
				}
				return nil
			}
			if d.Name() == ".giantmem-index" || d.Name() == ".DS_Store" {
				return nil
			}
			parsed, ok := ingest.ParseArchivePath(p, opts.ArchiveBase)
			if !ok {
				return nil
			}
			isMD := strings.HasSuffix(p, ".md")
			isFilebox := strings.Contains(p, string(filepath.Separator)+"filebox"+string(filepath.Separator))
			if !isMD && !isFilebox {
				return nil
			}
			if !isMD && strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			ts := filepath.Join(opts.ArchiveBase, parsed.Project, parsed.Timestamp)
			doc := Doc{
				Filepath:   p,
				Project:    parsed.Project,
				SourceType: "workspace",
				Timestamp:  parsed.Timestamp,
				DirType:    parsed.DirType,
				IsLatest:   latest[ts],
				Content:    readFile(p),
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

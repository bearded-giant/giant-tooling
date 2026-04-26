package sources

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/ingest"
)

func init() {
	Register("domain-json", func(cfg SourceConfig) Source {
		return &domainJSONSource{cfg: cfg}
	})
}

// domainJSONSource walks {archive}/{project}/{ts}/domains/*.json and emits
// a Doc with FTS-friendly flattened content per file.
type domainJSONSource struct {
	cfg SourceConfig
}

func (s *domainJSONSource) Name() string  { return "domain-json" }
func (s *domainJSONSource) Kind() string  { return "builtin" }
func (s *domainJSONSource) Enabled() bool { return s.cfg.Enabled }

func (s *domainJSONSource) Emit(ctx context.Context, opts EmitOptions) (<-chan Doc, <-chan error) {
	docCh := make(chan Doc, 16)
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
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(p, ".json") {
				return nil
			}
			if !strings.Contains(p, string(filepath.Separator)+"domains"+string(filepath.Separator)) {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			parsed, ok := ingest.ParseArchivePath(p, opts.ArchiveBase)
			if !ok {
				return nil
			}
			content, ferr := ingest.FlattenDomainJSON(p)
			if ferr != nil {
				select {
				case errCh <- ferr:
				default:
				}
				return nil
			}
			ts := filepath.Join(opts.ArchiveBase, parsed.Project, parsed.Timestamp)
			doc := Doc{
				Filepath:   p,
				Project:    parsed.Project,
				SourceType: "domain",
				Timestamp:  parsed.Timestamp,
				DirType:    parsed.DirType,
				IsLatest:   latest[ts],
				Content:    content,
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

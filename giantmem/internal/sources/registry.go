package sources

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/bryangrimes/gm/internal/project"
)

// Registry holds the resolved source instances after applying config.
type Registry struct {
	sources []Source
}

// NewRegistry resolves the config into runnable Source instances. Built-in
// constructors must be registered before this is called.
func NewRegistry(cfg *Config) (*Registry, error) {
	r := &Registry{}
	for _, sc := range cfg.Source {
		switch sc.Kind {
		case "builtin":
			ctor, ok := builtins[sc.Name]
			if !ok {
				return nil, fmt.Errorf("unknown builtin source %q", sc.Name)
			}
			r.sources = append(r.sources, ctor(sc))
		case "external":
			r.sources = append(r.sources, newExternalSource(sc))
		default:
			return nil, fmt.Errorf("source %q: unknown kind %q", sc.Name, sc.Kind)
		}
	}
	return r, nil
}

// Sources returns all registered sources.
func (r *Registry) Sources() []Source { return r.sources }

// Filter returns enabled sources whose name matches one of names; empty names
// list = all enabled.
func (r *Registry) Filter(names []string) []Source {
	if len(names) == 0 {
		var out []Source
		for _, s := range r.sources {
			if s.Enabled() {
				out = append(out, s)
			}
		}
		return out
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []Source
	for _, s := range r.sources {
		if want[s.Name()] {
			out = append(out, s)
		}
	}
	return out
}

// Run executes one source against the given db. It consumes the source's
// emit channel and upserts each doc.
func Run(ctx context.Context, db *sql.DB, src Source, opts EmitOptions) (Stats, error) {
	var st Stats
	docCh, errCh := src.Emit(ctx, opts)
	for docCh != nil || errCh != nil {
		select {
		case d, ok := <-docCh:
			if !ok {
				docCh = nil
				continue
			}
			if err := Upsert(db, d); err != nil {
				st.Errs++
			} else {
				st.Count++
			}
		case e, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if e != nil {
				st.Errs++
			}
		case <-ctx.Done():
			return st, ctx.Err()
		}
	}
	return st, nil
}

// Upsert inserts or replaces a doc + its FTS row. Builtins and external
// sources share this code path.
func Upsert(db *sql.DB, d Doc) error {
	if d.Filepath == "" || d.Project == "" || d.SourceType == "" || d.Timestamp == "" {
		return fmt.Errorf("doc missing required fields")
	}
	canonical := canonicalProject(d.Project)
	isLatest := 0
	if d.IsLatest {
		isLatest = 1
	}
	var oldID int64
	if err := db.QueryRow("SELECT id FROM documents WHERE filepath = ?", d.Filepath).Scan(&oldID); err == nil {
		if _, err := db.Exec("DELETE FROM documents_fts WHERE rowid = ?", oldID); err != nil {
			return err
		}
		if _, err := db.Exec("DELETE FROM documents WHERE id = ?", oldID); err != nil {
			return err
		}
	}
	res, err := db.Exec(
		`INSERT INTO documents
            (project, timestamp, source_type, dir_type, filepath, filename,
             is_latest, session_id, topic, indexed_at, cwd, canonical_project)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), ?, ?)`,
		d.Project, d.Timestamp, d.SourceType, nilIfEmpty(d.DirType),
		d.Filepath, filepath.Base(d.Filepath), isLatest,
		nilIfEmpty(d.SessionID), nilIfEmpty(d.Topic),
		nilIfEmpty(d.Cwd), canonical,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	content := d.Content
	if content == "" {
		content = filepath.Base(d.Filepath)
	}
	if _, err := db.Exec("INSERT INTO documents_fts (rowid, content) VALUES (?, ?)", id, content); err != nil {
		return err
	}
	return nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func canonicalProject(name string) string {
	base := archiveBase()
	return project.Canonicalize(name, base)
}

// builtins is populated by individual builtin source files via init().
var builtins = map[string]func(SourceConfig) Source{}

// Register adds a builtin source constructor. Called from init() in builtin
// source files.
func Register(name string, ctor func(SourceConfig) Source) {
	builtins[name] = ctor
}

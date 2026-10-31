package hub

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
)

type Published struct {
	Type      string `json:"type"`
	Target    string `json:"target"`
	Title     string `json:"title"`
	TS        string `json:"ts"`
	SessionID string `json:"sessionId"`
	Project   string `json:"project"`
	Sessions  int    `json:"sessions"`
	Missing   bool   `json:"missing"`
}

type PublishedFilter struct {
	Types []string
	Since string
	Until string
	Query string
}

// badge names; handoff is shared with the artifacts taxonomy so one filter covers both
var PublishedTypes = []string{"claude-artifact", "vault", "handoff"}

var linkKindType = map[string]string{"artifact": "claude-artifact", "vault": "vault", "handoff": "handoff"}

// Since/Until take the same forms as every other --since flag (7d, 2026-10-30, RFC3339).
func ListPublished(d *sql.DB, f PublishedFilter) ([]Published, error) {
	var since, until string
	if f.Since != "" {
		t, err := search.ParseSince(f.Since)
		if err != nil {
			return nil, err
		}
		since = t.UTC().Format(time.RFC3339)
	}
	if f.Until != "" {
		t, err := search.ParseUntil(f.Until)
		if err != nil {
			return nil, err
		}
		until = t.UTC().Format(time.RFC3339)
	}
	rows, err := d.Query(`
        SELECT l.kind, l.target, COALESCE(l.label,''), COALESCE(l.ts,''), l.session_id,
               COALESCE(NULLIF(doc.canonical_project,''), doc.project, '')
          FROM session_links l
          LEFT JOIN documents doc ON doc.session_id = l.session_id AND doc.source_type = 'session'
         WHERE l.kind IN ('artifact','vault','handoff')
           AND instr(l.target, '/.giantmem/') = 0
         ORDER BY l.ts DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want := map[string]bool{}
	for _, t := range f.Types {
		for _, v := range strings.Split(t, ",") {
			if v = strings.TrimSpace(v); v != "" {
				want[v] = true
			}
		}
	}
	byTarget := map[string]*Published{}
	var order []*Published
	for rows.Next() {
		var kind, target, label, ts, sess, proj string
		if err := rows.Scan(&kind, &target, &label, &ts, &sess, &proj); err != nil {
			return nil, err
		}
		typ := linkKindType[kind]
		if len(want) > 0 && !want[typ] {
			continue
		}
		if p, ok := byTarget[typ+"\x00"+target]; ok {
			p.Sessions++
			if p.Project == "" {
				p.Project = proj
			}
			if strings.HasSuffix(p.Title, ".html") && !strings.HasSuffix(label, ".html") && label != "" {
				p.Title = label
			}
			continue
		}
		p := &Published{Type: typ, Target: target, Title: label, TS: ts, SessionID: sess, Project: proj, Sessions: 1}
		byTarget[typ+"\x00"+target] = p
		order = append(order, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	words := strings.Fields(strings.ToLower(f.Query))
	out := make([]Published, 0, len(order))
	for _, p := range order {
		if since != "" && p.TS < since || until != "" && p.TS >= until {
			continue
		}
		p.Title = publishedTitle(p)
		if p.Type != "claude-artifact" {
			_, err := os.Stat(p.Target)
			p.Missing = err != nil
		}
		if len(words) > 0 && !matchesAll(p, words) {
			continue
		}
		out = append(out, *p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	return out, nil
}

func publishedTitle(p *Published) string {
	t := p.Title
	if p.Type == "handoff" || t == "" {
		t = strings.TrimSuffix(filepath.Base(p.Target), ".md")
		if p.Type == "handoff" {
			t = filepath.Base(filepath.Dir(p.Target)) + "/" + t
		}
	}
	return strings.TrimSuffix(t, ".html")
}

func matchesAll(p *Published, words []string) bool {
	hay := strings.ToLower(p.Title + " " + p.Target + " " + p.Project)
	var body string
	for _, w := range words {
		if strings.Contains(hay, w) {
			continue
		}
		if p.Type == "claude-artifact" || p.Missing {
			return false
		}
		if body == "" {
			raw, err := os.ReadFile(p.Target)
			if err != nil {
				return false
			}
			body = strings.ToLower(string(raw))
		}
		if !strings.Contains(body, w) {
			return false
		}
	}
	return true
}

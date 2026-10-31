package artifacts

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		rel     string
		typ     string
		feature string
		name    string
		ok      bool
	}{
		// taxonomy rules keep precedence over the catch-all
		{"features/foo/proposal.md", "proposal", "foo", "", true},
		{"features/foo/facts.md", "facts", "foo", "", true},
		{"features/foo/plans/current.md", "plan", "foo", "current", true},
		{"features/foo/research/notes.md", "research", "foo", "notes", true},
		{"features/foo/specs/auth/spec.md", "delta-spec", "foo", "", true},
		// catch-all: ad-hoc files inside a feature dir
		{"features/foo/e2e/findings/ITLS-1540.md", "file", "foo", "e2e/findings/ITLS-1540.md", true},
		{"features/foo/e2e/output.csv", "file", "foo", "e2e/output.csv", true},
		{"features/foo/export.csv", "file", "foo", "export.csv", true},
		// infra + hidden stay excluded
		{"features/foo/meta.json", "", "", "", false},
		{"features/features.json", "", "", "", false},
		{"features/_index.md", "", "", "", false},
		{"artifacts.json", "", "", "", false},
		{"specs/_history.md", "", "", "", false},
		{".mdlive/tabs.json", "", "", "", false},
		{".mdlive/history/features/foo/x.md", "", "", "", false},
		// everything real carries a type
		{"WORKSPACE.md", "workspace", "", "", true},
		{"notes.md", "notes", "", "notes", true},
		{"history/sessions.md", "history", "", "sessions", true},
		{"history/sessions/20260713_133642_1eda8565.md", "history", "", "sessions/20260713_133642_1eda8565", true},
		{"prompts/kickoff.md", "prompt", "", "kickoff", true},
		{"filebox/dump.json", "filebox", "", "dump.json", true},
		{"context/tree.md", "pattern", "", "tree", true},
		{"context/data.csv", "file", "", "context/data.csv", true},
		// catch-all splits: what a file is beats where it sits
		{"features/foo/filebox/run_7/ITEM.md", "filebox", "foo", "filebox/run_7/ITEM.md", true},
		{"features/foo/grill/01-run.md", "review", "foo", "grill/01-run.md", true},
		{"features/foo/reviews/sub/r.md", "review", "foo", "sub/r", true},
		{"features/foo/research/data.csv", "research", "foo", "data.csv", true},
		{"features/foo/runs/wf_1/impl.json", "run", "foo", "runs/wf_1/impl.json", true},
		{"features/foo/swarm-exec/out.json", "run", "foo", "swarm-exec/out.json", true},
		{"features/foo/jira-research/notes.md", "research", "foo", "jira-research/notes.md", true},
		{"features/foo/mr-description.md", "mr", "foo", "mr-description.md", true},
		{"features/foo/mr-descriptions/v2.md", "mr", "foo", "mr-descriptions/v2.md", true},
		{"features/foo/handoff.md", "handoff", "foo", "handoff.md", true},
		{"features/foo/filebox/handoff.md", "handoff", "foo", "filebox/handoff.md", true},
		{"features/foo/design-1.mmd", "diagram", "foo", "design-1.mmd", true},
		{"features/foo/prompts/p.md", "prompt", "foo", "prompts/p.md", true},
		{"handoff.md", "handoff", "", "handoff.md", true},
		{"mr-description-ci.md", "mr", "", "mr-description-ci.md", true},
		{"filebox/handoff.md", "handoff", "", "filebox/handoff.md", true},
		{"reviews/x.md", "review", "", "x", true},
		{"swarm-output/a.json", "run", "", "swarm-output/a.json", true},
		{"research/deep/x.md", "research", "", "deep/x", true},
	}
	for _, c := range cases {
		got, ok := Classify(c.rel)
		if ok != c.ok {
			t.Errorf("Classify(%q) ok=%v, want %v", c.rel, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Type != c.typ || got.Feature != c.feature || got.Name != c.name {
			t.Errorf("Classify(%q) = {%s %s %s}, want {%s %s %s}",
				c.rel, got.Type, got.Feature, got.Name, c.typ, c.feature, c.name)
		}
	}
}

func TestCanonicalType(t *testing.T) {
	cases := map[string]string{"grill-run": "review", "mr-description": "mr", "brief": "prompt", "reference": "research", "handoff": "handoff", "plan": "plan"}
	for in, want := range cases {
		if got, ok := CanonicalType(in); !ok || got != want {
			t.Errorf("CanonicalType(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "draft", "file", "history"} {
		if _, ok := CanonicalType(in); ok {
			t.Errorf("CanonicalType(%q) accepted", in)
		}
	}
}

func TestDeriveAliasFrontmatter(t *testing.T) {
	a, ok := DeriveFromLiveDoc("features/foo/grill/01-run.md", "---\ntype: grill-run\n---\nbody", "r", "", "")
	if !ok || a.Type != "review" || a.ID != "r/feat:foo:review:grill/01-run.md" {
		t.Fatalf("got %+v", a)
	}
	a, _ = DeriveFromLiveDoc("features/foo/filebox/x.md", "---\ntype: reference\n---\n", "r", "", "")
	if a.Type != "research" {
		t.Fatalf("reference alias: got %q", a.Type)
	}
}

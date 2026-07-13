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
		{"features/foo/jira-research/findings/ITLS-1540.md", "file", "foo", "jira-research/findings/ITLS-1540.md", true},
		{"features/foo/jira-research/output.csv", "file", "foo", "jira-research/output.csv", true},
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

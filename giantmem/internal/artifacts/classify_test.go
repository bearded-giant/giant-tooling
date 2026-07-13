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
		// feature infra stays excluded
		{"features/foo/meta.json", "", "", "", false},
		{"features/features.json", "", "", "", false},
		{"features/_index.md", "", "", "", false},
		// non-feature paths unchanged
		{"WORKSPACE.md", "", "", "", false},
		{"context/tree.md", "pattern", "", "tree", true},
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

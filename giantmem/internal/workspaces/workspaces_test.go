package workspaces

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mkdirs(t *testing.T, base string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindAndGroup(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdirs(t, home,
		".giantmem",
		"dev/x-wt/.bare",
		"dev/x-wt/.giantmem",
		"dev/x-wt/main/.giantmem",
		"dev/x-wt/feat/y/.giantmem",
		"dev/r/.git",
		"dev/r/.giantmem",
		"dev/r/sub/deep/.giantmem",
		"dev/s/.git",
		"dev/s/.giantmem",
		"a/dup/.git", "a/dup/.giantmem",
		"b/dup/.git", "b/dup/.giantmem",
		"Desktop/loose/.giantmem",
		"dev/node_modules/z/.giantmem",
		"dev/.hidden/q/.giantmem",
		"Library/m/.giantmem",
		"d1/d2/d3/.giantmem",
		"d1/d2/d3/d4/.giantmem",
		"d1/d2/d3/d4/d5/.giantmem",
	)

	got := Find([]string{home}, 4)
	want := []string{
		home,
		filepath.Join(home, "Desktop/loose"),
		filepath.Join(home, "a/dup"),
		filepath.Join(home, "b/dup"),
		filepath.Join(home, "d1/d2/d3"),
		filepath.Join(home, "d1/d2/d3/d4"),
		filepath.Join(home, "dev/r"),
		filepath.Join(home, "dev/r/sub/deep"),
		filepath.Join(home, "dev/s"),
		filepath.Join(home, "dev/x-wt"),
		filepath.Join(home, "dev/x-wt/feat/y"),
		filepath.Join(home, "dev/x-wt/main"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Find depth 4:\n got %v\nwant %v", got, want)
	}

	removed := filepath.Join(home, "dev/x-wt/gone-branch")
	projects := Group(home, append(got, removed))
	byName := map[string]Project{}
	for _, p := range projects {
		byName[p.Name] = p
	}
	labels := func(p Project) []string {
		var out []string
		for _, m := range p.Members {
			out = append(out, m.Label)
		}
		return out
	}

	x := byName["x"]
	if x.Kind != Container || x.Root != filepath.Join(home, "dev/x-wt") {
		t.Fatalf("x = %+v", x)
	}
	if l := labels(x); !reflect.DeepEqual(l, []string{RootLabel, "feat/y", "gone-branch", "main"}) {
		t.Fatalf("x labels = %v", l)
	}
	if l := labels(byName["r"]); !reflect.DeepEqual(l, []string{RootLabel, "sub/deep"}) {
		t.Fatalf("r labels = %v", l)
	}
	if s := byName["s"]; s.Kind != Repo || len(s.Members) != 1 || s.Members[0].Label != RootLabel {
		t.Fatalf("s = %+v", s)
	}
	if _, ok := byName["dup (a-dup)"]; !ok {
		t.Fatalf("collision not suffixed: %v", projects)
	}
	if _, ok := byName["dup (b-dup)"]; !ok {
		t.Fatalf("collision not suffixed: %v", projects)
	}
	last := projects[len(projects)-1]
	if last.Name != OtherName || last.Kind != Other {
		t.Fatalf("_other not last: %+v", last)
	}
	if l := labels(last); !reflect.DeepEqual(l, []string{"~", "Desktop/loose", "d1/d2/d3", "d1/d2/d3/d4"}) {
		t.Fatalf("_other labels = %v", l)
	}
}

func TestFindDefaultDepthReachesDeepNest(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, "dev/javascript/frontend-wt/master/packages/tooling/proxy/.giantmem")
	got := Find([]string{home}, 0)
	if len(got) != 1 {
		t.Fatalf("got %v", got)
	}
}

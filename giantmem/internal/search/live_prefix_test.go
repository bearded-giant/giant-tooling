package search

import (
	"path/filepath"
	"testing"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
)

func TestQueryLivePathPrefixes(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	insertLiveDoc(t, d, "/h/a_b/.giantmem/x.md", "retry ledger")
	insertLiveDoc(t, d, "/h/aXb/.giantmem/y.md", "retry ledger")
	insertLiveDoc(t, d, "/h/c/.giantmem/z.md", "retry ledger")

	hits, err := QueryLive(d, Params{Query: "retry", PathPrefixes: []string{"/h/a_b/.giantmem/"}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Filepath != "/h/a_b/.giantmem/x.md" {
		t.Fatalf("underscore must match literally, got %+v", hits)
	}

	hits, err = QueryLive(d, Params{Query: "retry", PathPrefixes: []string{"/h/a_b/", "/h/c/"}, Limit: 10})
	if err != nil || len(hits) != 2 {
		t.Fatalf("two prefixes: %+v %v", hits, err)
	}

	hits, err = QueryLive(d, Params{Query: "retry", Limit: 10})
	if err != nil || len(hits) != 3 {
		t.Fatalf("no prefix: %+v %v", hits, err)
	}
}

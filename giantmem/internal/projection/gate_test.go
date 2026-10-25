package projection

import (
	"testing"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
)

// every live.db commit (access logs, the daemon's own writes) used to trigger a
// full re-read of all live_docs content even when nothing it reads changed.
func TestGate_SkipsUntilLiveDocsChange(t *testing.T) {
	d, base := newLive(t)
	insertDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "---\nstatus: ready\n---\nbody one")
	g := &Gate{Resync: time.Hour}

	if _, ran, err := g.Run(d, base, fakeEmbedder{768}); err != nil || !ran {
		t.Fatalf("first pass: ran=%v err=%v", ran, err)
	}
	if _, ran, err := g.Run(d, base, fakeEmbedder{768}); err != nil || ran {
		t.Fatalf("unchanged live_docs should skip: ran=%v err=%v", ran, err)
	}
	if err := artifacts.LogAccess(d, "repo:proposal", "q", 1); err != nil {
		t.Fatalf("log access: %v", err)
	}
	if _, ran, _ := g.Run(d, base, fakeEmbedder{768}); ran {
		t.Fatal("writes outside live_docs should skip")
	}

	insertDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "---\nstatus: ready\n---\nbody one, edited")
	st, ran, err := g.Run(d, base, fakeEmbedder{768})
	if err != nil || !ran {
		t.Fatalf("content change should run: ran=%v err=%v", ran, err)
	}
	if st.Embedded != 1 {
		t.Fatalf("edited body should re-embed, got %+v", st)
	}

	insertDoc(t, d, "/r/.giantmem/features/bar/proposal.md", "---\nstatus: ready\n---\nnew doc")
	if _, ran, _ := g.Run(d, base, fakeEmbedder{768}); !ran {
		t.Fatal("new row should run")
	}
	if _, err := d.Exec(`DELETE FROM live_docs WHERE path LIKE '%/bar/%'`); err != nil {
		t.Fatal(err)
	}
	st, ran, _ = g.Run(d, base, fakeEmbedder{768})
	if !ran || st.Removed != 1 {
		t.Fatalf("deleted row should run and remove its artifact: ran=%v %+v", ran, st)
	}
}

func TestGate_ResyncForcesPass(t *testing.T) {
	d, base := newLive(t)
	insertDoc(t, d, "/r/.giantmem/features/foo/proposal.md", "---\nstatus: ready\n---\nbody")
	g := &Gate{Resync: 0}
	for i := 0; i < 2; i++ {
		if _, ran, err := g.Run(d, base, nil); err != nil || !ran {
			t.Fatalf("pass %d: ran=%v err=%v", i, ran, err)
		}
	}
}

package cmd

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFeatureArgv(t *testing.T) {
	cases := []struct {
		name    string
		in      featureArgs
		want    []string
		wantErr bool
	}{
		{"new needs name", featureArgs{Action: "new"}, nil, true},
		{"facts needs name", featureArgs{Action: "facts"}, nil, true},
		{"facts", featureArgs{Action: "facts", Name: "wails"}, []string{"feature", "facts", "wails"}, false},
		{"new no paired", featureArgs{Action: "new", Name: "x", NoPaired: true}, []string{"feature", "new", "x", "--no-paired"}, false},
		{"move needs dest", featureArgs{Action: "move", Name: "x"}, nil, true},
		{"unknown action", featureArgs{Action: "explode"}, nil, true},
		{"list takes no name", featureArgs{Action: "list", Name: "x"}, nil, true},
		{"new with flags", featureArgs{Action: "new", Name: "gm-cli", SkipCheckout: true, BuildsOn: "wails-gui"},
			[]string{"feature", "new", "gm-cli", "--builds-on", "wails-gui", "--skip-checkout"}, false},
		{"complete active", featureArgs{Action: "complete", Quick: true, Reason: "shipped"},
			[]string{"feature", "complete", "--quick", "--reason", "shipped"}, false},
		{"move", featureArgs{Action: "move", Name: "x", Dest: "/w/other", DryRun: true},
			[]string{"feature", "move", "x", "/w/other", "--dry-run"}, false},
		{"list status", featureArgs{Action: "list", Status: "pending"},
			[]string{"feature", "list", "--status", "pending"}, false},
	}
	for _, c := range cases {
		got, err := featureArgv(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if !c.wantErr && !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStaleArgvDays(t *testing.T) {
	if got := staleArgv(staleArtifactsArgs{}); !reflect.DeepEqual(got, []string{"artifact", "stale", "--days", "30"}) {
		t.Fatalf("default: %v", got)
	}
	zero := 0.0
	got := staleArgv(staleArtifactsArgs{Days: &zero, Apply: true, IdleDays: 90, Lifecycle: "candidate"})
	want := []string{"artifact", "stale", "--days", "0", "--lifecycle", "candidate", "--apply", "--idle-days", "90"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tier policy: got %v, want %v", got, want)
	}
}

func TestQueryReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`CREATE TABLE t (id INTEGER, body TEXT, blob BLOB); INSERT INTO t VALUES (1, 'a', x'6869'), (2, 'b', NULL), (3, 'c', NULL)`); err != nil {
		t.Fatal(err)
	}
	w.Close()

	ctx := context.Background()
	cols, rows, truncated, err := queryReadOnly(ctx, path, "SELECT id, body, blob FROM t ORDER BY id", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cols, []string{"id", "body", "blob"}) || len(rows) != 2 || !truncated {
		t.Fatalf("cols=%v rows=%v truncated=%v", cols, rows, truncated)
	}
	if rows[0]["blob"] != "hi" || rows[1]["body"] != "b" {
		t.Fatalf("row values: %v", rows)
	}
	if _, rows, truncated, _ = queryReadOnly(ctx, path, "SELECT id FROM t", 3); len(rows) != 3 || truncated {
		t.Fatalf("exact limit: rows=%d truncated=%v", len(rows), truncated)
	}

	for _, q := range []string{"INSERT INTO t VALUES (4, 'd', NULL)", "DELETE FROM t", "DROP TABLE t"} {
		if _, _, _, err := queryReadOnly(ctx, path, q, 10); err == nil {
			t.Fatalf("%q succeeded on a read-only connection", q)
		}
	}
	_, rows, _, _ = queryReadOnly(ctx, path, "SELECT count(*) AS n FROM t", 1)
	if rows[0]["n"] != int64(3) {
		t.Fatalf("table changed: %v", rows)
	}
}

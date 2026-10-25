package search

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const fakeEmbedScript = `import json, sys
print(json.dumps({"ready": True}), flush=True)
for line in sys.stdin:
    print(json.dumps({"vec": [0.5, 0.5, 0.5, 0.5]}), flush=True)
`

func fakePythonEmbedder(t *testing.T, idle time.Duration) *pythonEmbedder {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	script := filepath.Join(t.TempDir(), "embed.py")
	if err := os.WriteFile(script, []byte(fakeEmbedScript), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIANTMEM_EMBED_SCRIPT", script)
	t.Setenv("GIANTMEM_EMBED_PYTHON", "python3")
	e, err := newPythonEmbedder(4, "fake", idle)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func sidecarPid(e *pythonEmbedder) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return 0
	}
	return e.cmd.Process.Pid
}

// the sidecar used to live as long as the daemon, so anything the model process
// accumulated (mps graph cache: 6.8GB in a week) was never released.
func TestPythonEmbedderStopsWhenIdleAndRespawns(t *testing.T) {
	e := fakePythonEmbedder(t, 100*time.Millisecond)
	if _, err := e.Embed("a"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	first := sidecarPid(e)
	if first == 0 {
		t.Fatal("sidecar not running after embed")
	}

	deadline := time.Now().Add(3 * time.Second)
	for sidecarPid(e) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("sidecar still running after idle timeout")
		}
		time.Sleep(20 * time.Millisecond)
	}

	vec, err := e.Embed("b")
	if err != nil {
		t.Fatalf("embed after idle stop: %v", err)
	}
	if len(vec) != 4 {
		t.Fatalf("dim = %d, want 4", len(vec))
	}
	if second := sidecarPid(e); second == 0 || second == first {
		t.Fatalf("expected a fresh sidecar, pids %d -> %d", first, second)
	}
}

func TestPythonEmbedderKeepsSidecarWhileBusy(t *testing.T) {
	e := fakePythonEmbedder(t, 300*time.Millisecond)
	first := sidecarPid(e)
	for i := 0; i < 10; i++ {
		if _, err := e.Embed("a"); err != nil {
			t.Fatalf("embed %d: %v", i, err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	if got := sidecarPid(e); got != first {
		t.Fatalf("sidecar restarted while busy, pids %d -> %d", first, got)
	}
}

// a sidecar killed under memory pressure used to fail every later Embed until a
// daemon restart.
func TestPythonEmbedderRespawnsAfterSidecarDies(t *testing.T) {
	e := fakePythonEmbedder(t, 0)
	if _, err := e.Embed("a"); err != nil {
		t.Fatalf("embed: %v", err)
	}
	e.mu.Lock()
	dead := e.cmd.Process.Pid
	_ = e.cmd.Process.Kill()
	e.mu.Unlock()

	if _, err := e.Embed("b"); err == nil {
		t.Fatal("embed against a dead sidecar should error")
	}
	if _, err := e.Embed("c"); err != nil {
		t.Fatalf("embed after respawn: %v", err)
	}
	if got := sidecarPid(e); got == 0 || got == dead {
		t.Fatalf("expected a fresh sidecar, pids %d -> %d", dead, got)
	}
}

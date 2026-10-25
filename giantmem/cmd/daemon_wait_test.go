package cmd

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
)

func fakeDaemon(t *testing.T) {
	t.Helper()
	// unix socket paths cap at 104 bytes on macOS; t.TempDir() under $TMPDIR is too long
	home, err := os.MkdirTemp("/tmp", "gmw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	sock := daemon.DefaultSocketPath()
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if _, err := bufio.NewReader(c).ReadString('\n'); err == nil {
					c.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":0}` + "\n"))
				}
			}()
		}
	}()
}

func writePID(t *testing.T, pid string) {
	t.Helper()
	if err := os.WriteFile(daemon.DefaultSocketPath()+".pid", []byte(pid), 0o644); err != nil {
		t.Fatal(err)
	}
}

// restart used to report success as soon as any daemon answered, and gave up
// at 5s while launchd was still inside its 10s respawn throttle.
func TestWaitDaemonUp_RequiresNewPID(t *testing.T) {
	fakeDaemon(t)
	writePID(t, "111")
	if err := waitDaemonUp("up", 300*time.Millisecond, 111); err == nil {
		t.Fatal("outgoing daemon's socket should not count as restarted")
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		writePID(t, "222")
	}()
	if err := waitDaemonUp("up", 2*time.Second, 111); err != nil {
		t.Fatalf("new pid answering should count: %v", err)
	}
	if launchdUpWait <= 10*time.Second {
		t.Fatalf("launchdUpWait %s must outlast launchd's 10s ThrottleInterval", launchdUpWait)
	}
}

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/daemon"
	"github.com/spf13/cobra"
)

var (
	daemonForeground bool
	daemonBenchmark  bool
)

var daemonCmd = &cobra.Command{
	Use:   "daemon <command>",
	Short: "Manage giantmemd (long-running RPC server)",
	Long: `Subcommands: serve, start, stop, restart, status, health, install, uninstall.
The daemon caches DB handles in memory and serves the CLI over a unix socket
to avoid 700ms cold starts per invocation.`,
}

var daemonServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the daemon in the foreground (used by start/launchd)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := daemon.NewServer(daemon.DefaultSocketPath(), archiveDBPath(), liveDBPath())
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		fmt.Fprintf(os.Stderr, "giantmemd: serving on %s\n", daemon.DefaultSocketPath())
		err := s.Start(ctx)
		s.Close()
		return err
	},
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start giantmemd in the background",
	RunE: func(cmd *cobra.Command, args []string) error {
		if daemonForeground {
			return daemonServeCmd.RunE(cmd, args)
		}
		if daemon.SocketAlive(daemon.DefaultSocketPath(), 250*time.Millisecond) {
			fmt.Println("daemon already running")
			return nil
		}
		self, err := os.Executable()
		if err != nil {
			return err
		}
		logDir := filepath.Dir(daemon.DefaultSocketPath())
		_ = os.MkdirAll(logDir, 0o755)
		logFile := filepath.Join(logDir, "giantmemd.log")
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		c := exec.Command(self, "daemon", "serve")
		c.Stdout = f
		c.Stderr = f
		c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := c.Start(); err != nil {
			f.Close()
			return err
		}
		f.Close()
		// wait briefly for socket to come up
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if daemon.SocketAlive(daemon.DefaultSocketPath(), 100*time.Millisecond) {
				fmt.Printf("started giantmemd pid=%d log=%s\n", c.Process.Pid, logFile)
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("daemon did not become reachable; see %s", logFile)
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a running giantmemd",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, err := daemonPID()
		if err != nil {
			fmt.Println("daemon not running")
			return nil
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		if err := p.Signal(syscall.SIGTERM); err != nil {
			return err
		}
		// wait for socket to vanish
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if !daemon.SocketAlive(daemon.DefaultSocketPath(), 100*time.Millisecond) {
				fmt.Printf("stopped giantmemd pid=%d\n", pid)
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("daemon did not exit within 3s")
	},
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Stop and start giantmemd",
	RunE: func(cmd *cobra.Command, args []string) error {
		_ = daemonStopCmd.RunE(cmd, args)
		return daemonStartCmd.RunE(cmd, args)
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		sock := daemon.DefaultSocketPath()
		if !daemon.SocketAlive(sock, 250*time.Millisecond) {
			fmt.Printf("not running (socket: %s)\n", sock)
			return nil
		}
		c := daemon.NewClient(sock, 2*time.Second)
		var h daemon.HealthResult
		if err := c.Call("health", nil, &h); err != nil {
			return err
		}
		fmt.Printf("running\n")
		fmt.Printf("  socket:        %s\n", sock)
		fmt.Printf("  uptime:        %s\n", h.Uptime)
		fmt.Printf("  rss:           %.1f MB\n", float64(h.RSS)/1024/1024)
		fmt.Printf("  requests:      %d\n", h.Requests)
		fmt.Printf("  archive schema: v%d (binary v%d)\n", h.ArchiveSchema, h.BinarySchemaArch)
		fmt.Printf("  live schema:    v%d (binary v%d)\n", h.LiveSchema, h.BinarySchemaLive)
		if h.Drift {
			fmt.Println("  schema drift detected — daemon needs restart")
		}
		return nil
	},
}

var daemonHealthCmd = &cobra.Command{
	Use:   "health",
	Short: "Print daemon health JSON (with --benchmark, also runs perf loop)",
	RunE: func(cmd *cobra.Command, args []string) error {
		sock := daemon.DefaultSocketPath()
		if !daemon.SocketAlive(sock, 250*time.Millisecond) {
			return fmt.Errorf("daemon not running")
		}
		c := daemon.NewClient(sock, 5*time.Second)
		var h daemon.HealthResult
		if err := c.Call("health", nil, &h); err != nil {
			return err
		}
		if daemonBenchmark {
			b := runDaemonBench(c, 200)
			h.Bench = &b
		}
		out, _ := json.MarshalIndent(h, "", "  ")
		fmt.Println(string(out))
		return nil
	},
}

func init() {
	daemonStartCmd.Flags().BoolVar(&daemonForeground, "foreground", false, "stay in foreground (don't detach)")
	daemonHealthCmd.Flags().BoolVar(&daemonBenchmark, "benchmark", false, "run a small perf loop and report p50/p99")
	daemonCmd.AddCommand(daemonServeCmd, daemonStartCmd, daemonStopCmd, daemonRestartCmd, daemonStatusCmd, daemonHealthCmd, daemonInstallCmd, daemonUninstallCmd)
	rootCmd.AddCommand(daemonCmd)
}

// daemonPID reads the pidfile written by the running daemon.
func daemonPID() (int, error) {
	sock := daemon.DefaultSocketPath()
	data, err := os.ReadFile(sock + ".pid")
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, err
	}
	if pid == 0 {
		return 0, fmt.Errorf("empty pidfile")
	}
	return pid, nil
}

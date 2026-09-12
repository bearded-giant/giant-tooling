package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const launchdLabel = "com.giantmem.daemon"

func launchdPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
}

// launchdInstalled reports whether the LaunchAgent owns the daemon lifecycle.
// When it does, start/stop/restart must go through launchctl: a bare SIGTERM
// is undone by KeepAlive within seconds, and a bare `serve` races the relaunch
// for the socket.
func launchdInstalled() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := os.Stat(launchdPlistPath())
	return err == nil
}

func launchdDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }
func launchdTarget() string { return launchdDomain() + "/" + launchdLabel }

func launchctl(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// launchdStart starts the service if loaded, else loads it (after a stop it is
// booted out and needs bootstrap again).
func launchdStart() error {
	if err := launchctl("kickstart", launchdTarget()); err == nil {
		return nil
	}
	return launchctl("bootstrap", launchdDomain(), launchdPlistPath())
}

func launchdStop() error { return launchctl("bootout", launchdTarget()) }

func launchdRestart() error {
	if err := launchctl("kickstart", "-k", launchdTarget()); err == nil {
		return nil
	}
	return launchdStart()
}

var daemonInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install giantmemd as a launchd LaunchAgent (macOS only)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if runtime.GOOS != "darwin" {
			return fmt.Errorf("launchd install is macOS-only; on linux, use systemd or run `giantmem daemon start` from your shell init")
		}
		self, err := os.Executable()
		if err != nil {
			return err
		}
		home, _ := os.UserHomeDir()
		plistPath := launchdPlistPath()
		logPath := filepath.Join(home, ".cache", "giantmem", "giantmemd.log")
		_ = os.MkdirAll(filepath.Dir(plistPath), 0o755)
		_ = os.MkdirAll(filepath.Dir(logPath), 0o755)

		plist := buildLaunchdPlist(self, logPath, os.Getenv("GIANTMEM_EMBED_PYTHON"))
		if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
			return err
		}
		// unload any prior version, then load
		_ = exec.Command("launchctl", "unload", plistPath).Run()
		if out, err := exec.Command("launchctl", "load", plistPath).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl load: %w: %s", err, string(out))
		}
		fmt.Printf("installed %s\n", plistPath)
		fmt.Printf("logs: %s\n", logPath)
		return nil
	},
}

var daemonUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the launchd LaunchAgent for giantmemd",
	RunE: func(cmd *cobra.Command, args []string) error {
		plistPath := launchdPlistPath()
		if _, err := os.Stat(plistPath); err != nil {
			fmt.Println("not installed")
			return nil
		}
		_ = exec.Command("launchctl", "unload", plistPath).Run()
		if err := os.Remove(plistPath); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", plistPath)
		return nil
	},
}

func buildLaunchdPlist(binary, logPath, python string) string {
	pythonKey := ""
	if python != "" {
		pythonKey = fmt.Sprintf("    <key>GIANTMEM_EMBED_PYTHON</key>\n    <string>%s</string>\n", python)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
    <string>serve</string>
  </array>
  <key>KeepAlive</key>
  <true/>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
    <key>GIANTMEM_EMBED_BACKEND</key>
    <string>python</string>
%s  </dict>
</dict>
</plist>
`, launchdLabel, binary, logPath, logPath, pythonKey)
}

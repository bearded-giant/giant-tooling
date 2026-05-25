package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Background fsnotify daemon that auto-reindexes .giantmem/ workspaces on edit",
}

var watchStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the watch daemon (forks into the background)",
	RunE:  runWatchStart,
}

var watchStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running watch daemon",
	RunE:  runWatchStop,
}

var watchStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report whether the watch daemon is running",
	RunE:  runWatchStatus,
}

var watchRunCmd = &cobra.Command{
	Use:    "run",
	Hidden: true,
	Short:  "Run the watcher loop in the foreground (internal; invoked by 'start')",
	RunE:   runWatchRun,
}

var watchInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install macOS launchd LaunchAgent for the watch daemon",
	RunE:  runWatchInstall,
}

var (
	watchRoots        []string
	watchDebounceMs   int
	watchExcludeGlobs []string
)

func init() {
	for _, c := range []*cobra.Command{watchStartCmd, watchRunCmd} {
		c.Flags().StringSliceVar(&watchRoots, "root", nil, "watch root (repeat); default $HOME/dev or $GIANTMEM_DEV_ROOTS")
		c.Flags().IntVar(&watchDebounceMs, "debounce-ms", 2000, "debounce window per workspace")
		c.Flags().StringSliceVar(&watchExcludeGlobs, "exclude", nil, "glob to exclude (repeat)")
	}
	watchCmd.AddCommand(watchStartCmd, watchStopCmd, watchStatusCmd, watchRunCmd, watchInstallCmd)
	rootCmd.AddCommand(watchCmd)
}

func watchPIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "giantmem", "giantmem-watch.pid")
}

func watchLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "giantmem", "giantmem-watch.log")
}

func defaultWatchRoots() []string {
	if v := os.Getenv("GIANTMEM_DEV_ROOTS"); v != "" {
		return strings.Split(v, string(os.PathListSeparator))
	}
	if home, err := os.UserHomeDir(); err == nil {
		return []string{filepath.Join(home, "dev")}
	}
	return []string{"."}
}

func defaultExcludes() []string {
	return []string{
		"node_modules", ".venv", ".git", "dist", "build",
		".next", ".turbo", "target", "vendor",
	}
}

// ----- start / stop / status ------------------------------------------------

func runWatchStart(cmd *cobra.Command, args []string) error {
	if pid, alive := readPIDIfAlive(watchPIDPath()); alive {
		fmt.Printf("already running PID %d\n", pid)
		return nil
	}
	// Stale pidfile cleanup
	_ = os.Remove(watchPIDPath())

	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(watchPIDPath()), 0o755); err != nil {
		return err
	}

	logFile, err := os.OpenFile(watchLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	runArgs := []string{"watch", "run"}
	for _, r := range watchRoots {
		runArgs = append(runArgs, "--root", r)
	}
	if watchDebounceMs != 2000 {
		runArgs = append(runArgs, "--debounce-ms", strconv.Itoa(watchDebounceMs))
	}
	for _, g := range watchExcludeGlobs {
		runArgs = append(runArgs, "--exclude", g)
	}
	child := exec.Command(bin, runArgs...)
	child.Stdout = logFile
	child.Stderr = logFile
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := child.Start(); err != nil {
		return fmt.Errorf("spawn watcher: %w", err)
	}
	pid := child.Process.Pid
	if err := os.WriteFile(watchPIDPath(), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		_ = child.Process.Kill()
		return err
	}
	// detach
	_ = child.Process.Release()
	fmt.Printf("started PID %d (log: %s)\n", pid, watchLogPath())
	return nil
}

func runWatchStop(cmd *cobra.Command, args []string) error {
	pid, alive := readPIDIfAlive(watchPIDPath())
	if !alive {
		_ = os.Remove(watchPIDPath())
		fmt.Println("not running")
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("kill PID %d: %w", pid, err)
	}
	// brief wait
	for i := 0; i < 20; i++ {
		if _, ok := readPIDIfAlive(watchPIDPath()); !ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.Remove(watchPIDPath())
	fmt.Printf("stopped PID %d\n", pid)
	return nil
}

func runWatchStatus(cmd *cobra.Command, args []string) error {
	pid, alive := readPIDIfAlive(watchPIDPath())
	if !alive {
		fmt.Println("not running")
		os.Exit(1)
	}
	fmt.Printf("running PID %d (log: %s)\n", pid, watchLogPath())
	return nil
}

func readPIDIfAlive(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return pid, false
	}
	return pid, true
}

// ----- foreground loop -------------------------------------------------------

func runWatchRun(cmd *cobra.Command, args []string) error {
	roots := watchRoots
	if len(roots) == 0 {
		roots = defaultWatchRoots()
	}
	excludes := append(defaultExcludes(), watchExcludeGlobs...)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	workspaces := discoverGiantmemDirs(roots, excludes)
	fmt.Fprintf(os.Stderr, "[watch] discovered %d .giantmem dirs across %d roots\n",
		len(workspaces), len(roots))
	for _, ws := range workspaces {
		if err := w.Add(ws); err != nil {
			fmt.Fprintf(os.Stderr, "[watch] WARN add %s: %v\n", ws, err)
			continue
		}
		// also add immediate subdirs so feature edits get caught
		_ = filepath.Walk(ws, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if !info.IsDir() {
				return nil
			}
			// ws is .giantmem itself; descend into it. For any other dir,
			// skip dotfiles and excluded names.
			if p != ws {
				name := info.Name()
				if strings.HasPrefix(name, ".") || isExcluded(name, excludes) {
					return filepath.SkipDir
				}
				_ = w.Add(p)
			}
			return nil
		})
	}

	debounce := time.Duration(watchDebounceMs) * time.Millisecond
	deb := newDebouncer(debounce, func(ws string) {
		runReindex(ws)
	})

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	bin, _ := os.Executable()
	_ = bin

	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			ws, ok := owningWorkspace(ev.Name)
			if !ok {
				continue
			}
			deb.fire(ws)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "[watch] err: %v\n", err)
		case <-sigs:
			fmt.Fprintln(os.Stderr, "[watch] shutting down")
			_ = os.Remove(watchPIDPath())
			return nil
		}
	}
}

// discoverGiantmemDirs walks each root looking for .giantmem directories.
func discoverGiantmemDirs(roots, excludes []string) []string {
	out := []string{}
	for _, root := range roots {
		root, _ = filepath.Abs(filepath.Clean(os.ExpandEnv(root)))
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if !info.IsDir() {
				return nil
			}
			name := info.Name()
			if name == ".giantmem" {
				out = append(out, p)
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if isExcluded(name, excludes) {
				return filepath.SkipDir
			}
			return nil
		})
	}
	return out
}

func isExcluded(name string, excludes []string) bool {
	for _, ex := range excludes {
		if ex == name {
			return true
		}
	}
	return false
}

// owningWorkspace walks up from path until it finds a .giantmem directory.
func owningWorkspace(path string) (string, bool) {
	cur, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	for i := 0; i < 12; i++ {
		if filepath.Base(cur) == ".giantmem" {
			return cur, true
		}
		gm := filepath.Join(cur, ".giantmem")
		if st, err := os.Stat(gm); err == nil && st.IsDir() {
			return gm, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return "", false
}

func runReindex(ws string) {
	bin, _ := os.Executable()
	worktree := filepath.Dir(ws)
	cmd := exec.Command(bin, "artifact", "reindex")
	cmd.Dir = worktree
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	start := time.Now()
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[watch] reindex %s FAILED in %s: %v\n",
			worktree, time.Since(start), err)
		return
	}
	fmt.Fprintf(os.Stderr, "[watch] reindex %s ok in %s\n", worktree, time.Since(start).Round(time.Millisecond))
}

// ----- debouncer ------------------------------------------------------------

type debouncer struct {
	mu       sync.Mutex
	dur      time.Duration
	timers   map[string]*time.Timer
	callback func(string)
}

func newDebouncer(dur time.Duration, cb func(string)) *debouncer {
	return &debouncer{dur: dur, timers: map[string]*time.Timer{}, callback: cb}
}

func (d *debouncer) fire(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.timers[key]; ok {
		t.Stop()
	}
	d.timers[key] = time.AfterFunc(d.dur, func() {
		d.callback(key)
		d.mu.Lock()
		delete(d.timers, key)
		d.mu.Unlock()
	})
}

// ----- launchd install (macOS only) -----------------------------------------

func runWatchInstall(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.giantmem.watch.plist")
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
  <dict>
    <key>Label</key><string>com.giantmem.watch</string>
    <key>ProgramArguments</key>
    <array>
      <string>` + bin + `</string>
      <string>watch</string>
      <string>run</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key><string>` + watchLogPath() + `</string>
    <key>StandardErrorPath</key><string>` + watchLogPath() + `</string>
  </dict>
</plist>
`
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	loadCmd := exec.Command("launchctl", "load", plistPath)
	if out, err := loadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("installed LaunchAgent at %s\n", plistPath)
	return nil
}

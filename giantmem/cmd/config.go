package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/spf13/cobra"
)

var (
	configJSON bool
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show resolved giantmem configuration: paths, db state, hook + MCP wiring",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := buildConfig()
		if configJSON {
			return output.JSON(c)
		}
		printConfigText(c)
		return nil
	},
}

type configReport struct {
	Binary           string         `json:"binary"`
	Version          string         `json:"version"`
	Commit           string         `json:"commit"`
	BuildDate        string         `json:"build_date"`
	ArchiveBase      string         `json:"archive_base"`
	ArchiveBaseSrc   string         `json:"archive_base_source"`
	ArchiveDB        dbInfo         `json:"archives_db"`
	LiveDB           dbInfo         `json:"live_db"`
	CacheDir         string         `json:"cache_dir"`
	ConfigFile       fileInfo       `json:"config_file"`
	Hooks            []hookInfo     `json:"hooks"`
	MCP              mcpInfo        `json:"mcp"`
	WorktreeCore     fileInfo       `json:"worktree_core"`
	WorkspaceLib     fileInfo       `json:"workspace_lib"`
}

type dbInfo struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	SizeMB   string `json:"size_mb,omitempty"`
	DocCount int    `json:"doc_count,omitempty"`
	Schema   int    `json:"schema_version,omitempty"`
}

type fileInfo struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Note   string `json:"note,omitempty"`
}

type hookInfo struct {
	Event   string `json:"event"`
	Script  string `json:"script"`
	Wired   bool   `json:"wired"`
	OnDisk  bool   `json:"on_disk"`
}

type mcpInfo struct {
	Registered bool   `json:"registered"`
	Command    string `json:"command,omitempty"`
	Args       string `json:"args,omitempty"`
	Healthy    bool   `json:"healthy"`
}

func buildConfig() configReport {
	home, _ := os.UserHomeDir()
	binary, _ := os.Executable()
	c := configReport{
		Binary:    binary,
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		CacheDir:  filepath.Join(home, ".cache", "giantmem"),
	}
	c.ArchiveBase = archiveBasePath()
	if os.Getenv("GIANTMEM_ARCHIVE_BASE") != "" {
		c.ArchiveBaseSrc = "env GIANTMEM_ARCHIVE_BASE"
	} else {
		c.ArchiveBaseSrc = "default ~/giantmem_archive"
	}

	c.ArchiveDB = describeDB(archiveDBPath())
	c.LiveDB = describeDB(liveDBPath())

	cfgPath := filepath.Join(home, ".config", "giantmem", "config.toml")
	c.ConfigFile = fileInfo{Path: cfgPath, Exists: fileExists(cfgPath)}
	if !c.ConfigFile.Exists {
		c.ConfigFile.Note = "missing — using defaults"
	}

	c.Hooks = describeHooks(home)
	c.MCP = describeMCP(home)

	wtCore := filepath.Join(home, "dev", "giant-tooling", "git-worktrees", "worktree-core.sh")
	c.WorktreeCore = fileInfo{Path: wtCore, Exists: fileExists(wtCore)}
	wsLib := filepath.Join(home, "dev", "giant-tooling", "workspace", "workspace-lib.sh")
	c.WorkspaceLib = fileInfo{Path: wsLib, Exists: fileExists(wsLib)}

	return c
}

func describeDB(path string) dbInfo {
	out := dbInfo{Path: path}
	if st, err := os.Stat(path); err == nil {
		out.Exists = true
		out.SizeMB = fmt.Sprintf("%.1f", float64(st.Size())/(1024*1024))
	} else {
		return out
	}
	d, err := db.Open(path)
	if err != nil {
		return out
	}
	defer d.Close()
	if v, err := db.SchemaVersion(d); err == nil {
		out.Schema = v
	}
	// table name varies between archives.db and live.db
	for _, table := range []string{"documents", "live_docs"} {
		var n int
		err := d.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n)
		if err == nil {
			out.DocCount = n
			break
		}
	}
	return out
}

func describeHooks(home string) []hookInfo {
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	raw, _ := os.ReadFile(settingsPath)
	body := string(raw)
	hookFiles := []hookInfo{
		{Event: "PostToolUse", Script: "live_index.py"},
		{Event: "SessionStart", Script: "session_prime.py"},
		{Event: "PreCompact", Script: "precompact_capture.py"},
		{Event: "SessionEnd", Script: "session_end_ingest.py"},
	}
	for i := range hookFiles {
		hookFiles[i].Wired = strings.Contains(body, hookFiles[i].Script)
		hookFiles[i].OnDisk = fileExists(filepath.Join(home, ".claude", "hooks", hookFiles[i].Script))
	}
	return hookFiles
}

func describeMCP(home string) mcpInfo {
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	raw, _ := os.ReadFile(settingsPath)
	body := string(raw)
	out := mcpInfo{}
	if strings.Contains(body, `"giantmem-search"`) {
		out.Registered = true
	}
	if strings.Contains(body, `mcp", "serve"`) || strings.Contains(body, `"mcp","serve"`) {
		out.Command = "giantmem"
		out.Args = "mcp serve"
		out.Healthy = true
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func printConfigText(c configReport) {
	fmt.Printf("binary:        %s\n", c.Binary)
	fmt.Printf("               %s (commit %s, built %s)\n", c.Version, c.Commit, c.BuildDate)
	fmt.Printf("archive_base:  %s  (%s)\n", c.ArchiveBase, c.ArchiveBaseSrc)
	fmt.Printf("archives.db:   %s\n", c.ArchiveDB.Path)
	if c.ArchiveDB.Exists {
		fmt.Printf("               %s MB, %d docs, schema v%d\n", c.ArchiveDB.SizeMB, c.ArchiveDB.DocCount, c.ArchiveDB.Schema)
	} else {
		fmt.Println("               (missing — created on first use)")
	}
	fmt.Printf("live.db:       %s\n", c.LiveDB.Path)
	if c.LiveDB.Exists {
		fmt.Printf("               %s MB, %d docs, schema v%d\n", c.LiveDB.SizeMB, c.LiveDB.DocCount, c.LiveDB.Schema)
	} else {
		fmt.Println("               (missing — created on first hook fire)")
	}
	fmt.Printf("cache_dir:     %s\n", c.CacheDir)
	fmt.Printf("config_file:   %s", c.ConfigFile.Path)
	if c.ConfigFile.Note != "" {
		fmt.Printf("  (%s)", c.ConfigFile.Note)
	}
	fmt.Println()

	fmt.Println("\nhooks:")
	for _, h := range c.Hooks {
		mark := "✓"
		note := ""
		if !h.OnDisk {
			mark = "✗"
			note = "  (script missing)"
		} else if !h.Wired {
			mark = "·"
			note = "  (script on disk but not wired in settings.json)"
		}
		fmt.Printf("  %s %-15s → %s%s\n", mark, h.Event, h.Script, note)
	}

	fmt.Println("\nmcp:")
	if c.MCP.Registered {
		mark := "✓"
		if !c.MCP.Healthy {
			mark = "·"
		}
		fmt.Printf("  %s giantmem-search → %s %s\n", mark, c.MCP.Command, c.MCP.Args)
	} else {
		fmt.Println("  ✗ giantmem-search not registered in settings.json")
	}

	fmt.Println("\nlibraries:")
	for _, lib := range []struct {
		label string
		fi    fileInfo
	}{
		{"worktree-core.sh", c.WorktreeCore},
		{"workspace-lib.sh", c.WorkspaceLib},
	} {
		mark := "✓"
		if !lib.fi.Exists {
			mark = "✗"
		}
		fmt.Printf("  %s %-18s %s\n", mark, lib.label, lib.fi.Path)
	}
}

func init() {
	configCmd.Flags().BoolVar(&configJSON, "json", false, "JSON output")
	rootCmd.AddCommand(configCmd)
}

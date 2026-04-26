package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

var (
	backupDir       string
	backupRemote    string
	backupNoPush    bool
	backupForceInit bool
	backupMessage   string
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Snapshot archives.db to a private git repo (init / push / status)",
	Long: `Maintain a backup of archives.db (and optionally live.db) in a private
git repo. Designed to pair with /schedule for periodic snapshots.

The default backup dir is ~/giantmem_archive_backup. Pass --dir to override.`,
}

var backupInitCmd = &cobra.Command{
	Use:   "init [remote-url]",
	Short: "Initialize the backup repo (clones remote-url, or creates an empty repo)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := backupDirPath()
		if _, err := os.Stat(dir); err == nil {
			if !backupForceInit {
				return fmt.Errorf("%s already exists; pass --force to remove", dir)
			}
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
		}
		if len(args) == 1 {
			remote := args[0]
			fmt.Printf("cloning %s into %s\n", remote, dir)
			c := exec.Command("git", "clone", remote, dir)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return err
			}
		} else {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			c := exec.Command("git", "-C", dir, "init")
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return err
			}
		}
		// .gitattributes: treat dbs as binary, no diff
		gaPath := filepath.Join(dir, ".gitattributes")
		if _, err := os.Stat(gaPath); err != nil {
			os.WriteFile(gaPath, []byte("*.db binary -diff -merge\n"), 0o644)
		}
		fmt.Println("backup repo ready at", dir)
		return nil
	},
}

var backupPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Copy archives.db into the backup repo, commit, and push (unless --no-push)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := backupDirPath()
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			return fmt.Errorf("backup repo not initialized at %s; run: giantmem backup init [remote-url]", dir)
		}
		src := archiveDBPath()
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("archives.db not found at %s", src)
		}
		dst := filepath.Join(dir, "archives.db")
		if err := copyFile(src, dst); err != nil {
			return err
		}

		// also copy live.db if it exists (best-effort, smaller; great for DR)
		if _, err := os.Stat(liveDBPath()); err == nil {
			liveDst := filepath.Join(dir, "live.db")
			_ = copyFile(liveDBPath(), liveDst)
		}

		// stage + commit
		git := func(args ...string) error {
			c := exec.Command("git", append([]string{"-C", dir}, args...)...)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		}
		if err := git("add", "-A"); err != nil {
			return err
		}
		// any changes?
		out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
		if len(out) == 0 {
			fmt.Println("no changes to commit")
			return nil
		}
		msg := backupMessage
		if msg == "" {
			msg = "snapshot " + time.Now().UTC().Format("2006-01-02T15:04:05Z")
		}
		if err := git("commit", "-m", msg); err != nil {
			return err
		}
		if backupNoPush {
			fmt.Println("committed; --no-push so skipping push")
			return nil
		}
		// only push if a remote exists
		remoteOut, _ := exec.Command("git", "-C", dir, "remote").Output()
		if len(remoteOut) == 0 {
			fmt.Println("no remote configured; skipping push (set with: git -C", dir, "remote add origin <url>)")
			return nil
		}
		if err := git("push"); err != nil {
			return err
		}
		fmt.Println("backup pushed")
		return nil
	},
}

var backupStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show backup repo state: last commit, dirty status, remote",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := backupDirPath()
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			return fmt.Errorf("backup repo not initialized at %s", dir)
		}
		fmt.Printf("dir: %s\n", dir)
		out, _ := exec.Command("git", "-C", dir, "log", "-1", "--format=%h %s (%cr)").Output()
		fmt.Printf("last: %s", out)
		out, _ = exec.Command("git", "-C", dir, "status", "--short").Output()
		if len(out) > 0 {
			fmt.Printf("dirty:\n%s", out)
		} else {
			fmt.Println("clean")
		}
		out, _ = exec.Command("git", "-C", dir, "remote", "-v").Output()
		if len(out) > 0 {
			fmt.Printf("remotes:\n%s", out)
		} else {
			fmt.Println("no remotes")
		}
		return nil
	},
}

func backupDirPath() string {
	if backupDir != "" {
		return backupDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "giantmem_archive_backup")
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func init() {
	backupCmd.PersistentFlags().StringVar(&backupDir, "dir", "", "backup repo dir (default ~/giantmem_archive_backup)")
	backupInitCmd.Flags().BoolVar(&backupForceInit, "force", false, "remove existing dir before init")
	backupPushCmd.Flags().BoolVar(&backupNoPush, "no-push", false, "commit only, do not push")
	backupPushCmd.Flags().StringVar(&backupRemote, "remote", "origin", "git remote to push to")
	backupPushCmd.Flags().StringVar(&backupMessage, "message", "", "commit message (default: timestamp)")
	backupCmd.AddCommand(backupInitCmd)
	backupCmd.AddCommand(backupPushCmd)
	backupCmd.AddCommand(backupStatusCmd)
	rootCmd.AddCommand(backupCmd)
}

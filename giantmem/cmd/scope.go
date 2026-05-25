package cmd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/spf13/cobra"
)

var scopeCmd = &cobra.Command{
	Use:   "scope",
	Short: "Manage the cross-repo scope registry (~/.giantmem-global/scopes.yaml)",
}

var scopeInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a default scopes.yaml seeded with the current repo as scope 'personal'",
	RunE:  runScopeInit,
}

var scopeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured scopes",
	RunE:  runScopeList,
}

var scopeShowCmd = &cobra.Command{
	Use:   "show <scope_id>",
	Short: "Show one scope's full record",
	Args:  cobra.ExactArgs(1),
	RunE:  runScopeShow,
}

var scopeAddRepoCmd = &cobra.Command{
	Use:   "add-repo <scope_id> <repo> [repo...]",
	Short: "Add one or more repo names to a scope",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runScopeAddRepo,
}

var scopeSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Rebuild the live.db scopes cache from the YAML registry",
	RunE:  runScopeSync,
}

var scopeJSON bool

func init() {
	scopeListCmd.Flags().BoolVar(&scopeJSON, "json", false, "JSON output")
	scopeCmd.AddCommand(scopeInitCmd, scopeListCmd, scopeShowCmd, scopeAddRepoCmd, scopeSyncCmd)
	rootCmd.AddCommand(scopeCmd)
}

func loadRegistry() (*artifacts.ScopeRegistry, error) {
	return artifacts.LoadScopeRegistry(artifacts.ScopesYAMLPath())
}

func runScopeInit(cmd *cobra.Command, args []string) error {
	path := artifacts.ScopesYAMLPath()
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(os.Stderr, "scope registry already exists at %s; leaving untouched\n", path)
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo := "personal"
	if ws, ok := artifacts.FindWorkspace(cwd); ok {
		r, _ := artifacts.DetectRepoBranch(ws)
		if r != "" {
			repo = r
		}
	}
	seed := []artifacts.Scope{{
		ID:          "personal",
		Description: "default scope seeded by `giantmem scope init`",
		Tags:        []string{"personal"},
		Repos:       []string{repo},
	}}
	if err := artifacts.SaveScopeRegistry(path, seed); err != nil {
		return err
	}
	fmt.Printf("created %s with scope 'personal' covering [%s]\n", path, repo)
	if err := syncScopesToDB(); err != nil {
		fmt.Fprintf(os.Stderr, "warn: cache sync failed: %v\n", err)
	}
	return nil
}

func runScopeList(cmd *cobra.Command, args []string) error {
	reg, err := loadRegistry()
	if err != nil {
		return err
	}
	scopes := reg.Scopes()
	if scopeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"path": reg.Path(), "scopes": scopes})
	}
	if len(scopes) == 0 {
		fmt.Fprintf(os.Stderr, "no scopes configured; run `giantmem scope init`\n")
		return nil
	}
	fmt.Printf("# %s (%d scopes)\n", reg.Path(), len(scopes))
	for _, sc := range scopes {
		repos := append([]string{}, sc.Repos...)
		sort.Strings(repos)
		fmt.Printf("%-28s repos=[%s] tags=[%s]\n",
			sc.ID, strings.Join(repos, ", "), strings.Join(sc.Tags, ", "))
	}
	return nil
}

func runScopeShow(cmd *cobra.Command, args []string) error {
	reg, err := loadRegistry()
	if err != nil {
		return err
	}
	sc, ok := reg.Scope(args[0])
	if !ok {
		return fmt.Errorf("no scope %q", args[0])
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(sc)
}

func runScopeAddRepo(cmd *cobra.Command, args []string) error {
	scopeID := args[0]
	newRepos := args[1:]
	reg, err := loadRegistry()
	if err != nil {
		return err
	}
	sc, ok := reg.Scope(scopeID)
	if !ok {
		return fmt.Errorf("no scope %q; run `giantmem scope init` first", scopeID)
	}
	existing := map[string]bool{}
	for _, r := range sc.Repos {
		existing[r] = true
	}
	for _, r := range newRepos {
		r = strings.TrimSpace(r)
		if r == "" || existing[r] {
			continue
		}
		sc.Repos = append(sc.Repos, r)
		existing[r] = true
	}
	all := reg.Scopes()
	for i := range all {
		if all[i].ID == scopeID {
			all[i] = sc
			break
		}
	}
	if err := artifacts.SaveScopeRegistry(reg.Path(), all); err != nil {
		return err
	}
	sort.Strings(sc.Repos)
	fmt.Printf("scope %s repos=[%s]\n", scopeID, strings.Join(sc.Repos, ", "))
	if err := syncScopesToDB(); err != nil {
		fmt.Fprintf(os.Stderr, "warn: cache sync failed: %v\n", err)
	}
	return nil
}

func runScopeSync(cmd *cobra.Command, args []string) error {
	return syncScopesToDB()
}

func syncScopesToDB() error {
	reg, err := loadRegistry()
	if err != nil {
		return err
	}
	live, err := db.Open(liveDBPath())
	if err != nil {
		return err
	}
	defer live.Close()
	if err := writeScopesCache(live, reg.Scopes()); err != nil {
		return err
	}
	fmt.Printf("synced %d scopes to %s\n", len(reg.Scopes()), liveDBPath())
	return nil
}

func writeScopesCache(d *sql.DB, scopes []artifacts.Scope) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM scopes"); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, sc := range scopes {
		tagsJSON, _ := json.Marshal(sc.Tags)
		reposJSON, _ := json.Marshal(sc.Repos)
		if _, err := tx.Exec(
			`INSERT INTO scopes(scope_id, description, tags, repos, updated_at) VALUES (?, ?, ?, ?, ?)`,
			sc.ID, sc.Description, string(tagsJSON), string(reposJSON), now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/spf13/cobra"
)

var entityCmd = &cobra.Command{
	Use:   "entity",
	Short: "Inspect typed file-level entities derived from .giantmem/domains/*.json",
}

var entityListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every entity (key_file across all domains)",
	RunE:  runEntityList,
}

var entityShowCmd = &cobra.Command{
	Use:   "show <path-or-basename>",
	Short: "Show one entity + its back-references to mentioning artifacts",
	Args:  cobra.ExactArgs(1),
	RunE:  runEntityShow,
}

var (
	entityRepo string
	entityJSON bool
)

func init() {
	for _, c := range []*cobra.Command{entityListCmd, entityShowCmd} {
		c.Flags().StringVar(&entityRepo, "repo", "all", "current | all (default) | <repo>")
		c.Flags().BoolVar(&entityJSON, "json", false, "JSON output")
	}
	entityCmd.AddCommand(entityListCmd, entityShowCmd)
	rootCmd.AddCommand(entityCmd)
}

func collectEntities() ([]artifacts.Entity, error) {
	var corpus []artifacts.Artifact
	if entityRepo == "current" {
		_, idx, err := resolveWorkspace()
		if err != nil {
			return nil, err
		}
		corpus = idx.Artifacts
	} else {
		all, _, err := artifacts.CrawlAll(0)
		if err != nil {
			return nil, err
		}
		if entityRepo == "all" || entityRepo == "" {
			corpus = all
		} else {
			for _, a := range all {
				if a.Repo == entityRepo {
					corpus = append(corpus, a)
				}
			}
		}
	}
	return artifacts.LoadEntities(corpus)
}

func runEntityList(cmd *cobra.Command, args []string) error {
	entities, err := collectEntities()
	if err != nil {
		return err
	}
	if entityJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"entities": entities,
			"total":    len(entities),
		})
	}
	if len(entities) == 0 {
		fmt.Fprintln(os.Stderr, "no entities — run domain exploration first")
		return nil
	}
	fmt.Printf("# entities (total=%d)\n", len(entities))
	for _, e := range entities {
		fmt.Printf("%-20s %3d refs  %s\n", e.Domain, len(e.References), e.Path)
	}
	return nil
}

func runEntityShow(cmd *cobra.Command, args []string) error {
	entities, err := collectEntities()
	if err != nil {
		return err
	}
	e, ok := artifacts.FindEntity(entities, args[0])
	if !ok {
		return fmt.Errorf("no entity matching %q", args[0])
	}
	if entityJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(e)
	}
	fmt.Printf("path:    %s\n", e.Path)
	fmt.Printf("domain:  %s\n", e.Domain)
	fmt.Printf("repo:    %s\n", e.Repo)
	if e.Purpose != "" {
		fmt.Printf("purpose: %s\n", e.Purpose)
	}
	if len(e.Exports) > 0 {
		fmt.Println("exports:")
		for _, x := range e.Exports {
			fmt.Printf("  - %s\n", x)
		}
	}
	fmt.Println("references:")
	if len(e.References) == 0 {
		fmt.Println("  (none — no artifacts mention this path)")
	}
	for _, r := range e.References {
		fmt.Printf("  - %s\n", r)
	}
	return nil
}

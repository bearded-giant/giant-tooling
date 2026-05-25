package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/artifacts"
	"github.com/spf13/cobra"
)

var (
	suggestRepo  string
	suggestLimit int
	suggestJSON  bool
)

var suggestDomainCmd = &cobra.Command{
	Use:   "suggest-domain [text]",
	Short: "TF-IDF over source-specs: top-N domain candidates for a body of text",
	Long: `Rank existing source-spec domains by TF-IDF similarity to the supplied
text. When no positional arg is given, reads stdin. Useful at scaffold time
('which domain should this delta-spec live under?').`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSuggestDomain,
}

func init() {
	suggestDomainCmd.Flags().StringVar(&suggestRepo, "repo", "all", "corpus scope: current | all (default) | <repo>")
	suggestDomainCmd.Flags().IntVar(&suggestLimit, "limit", 3, "max suggestions")
	suggestDomainCmd.Flags().BoolVar(&suggestJSON, "json", false, "JSON output")
	rootCmd.AddCommand(suggestDomainCmd)
}

func runSuggestDomain(cmd *cobra.Command, args []string) error {
	var text string
	if len(args) > 0 {
		text = args[0]
	} else {
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		text = string(raw)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("no input text (positional arg or stdin)")
	}

	var corpus []artifacts.Artifact
	if suggestRepo == "current" {
		_, idx, err := resolveWorkspace()
		if err != nil {
			return err
		}
		corpus = idx.Artifacts
	} else {
		all, _, err := artifacts.CrawlAll(0)
		if err != nil {
			return err
		}
		if suggestRepo == "all" || suggestRepo == "" {
			corpus = all
		} else {
			for _, a := range all {
				if a.Repo == suggestRepo {
					corpus = append(corpus, a)
				}
			}
		}
	}

	suggestions, err := artifacts.SuggestDomains(text, corpus, suggestLimit)
	if err != nil {
		return err
	}

	if suggestJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"limit":       suggestLimit,
			"repo":        suggestRepo,
			"suggestions": suggestions,
		})
	}

	if len(suggestions) == 0 {
		fmt.Fprintln(os.Stderr, "no domain matches")
		return nil
	}
	fmt.Printf("# suggested domains (limit=%d, corpus=%s)\n", suggestLimit, suggestRepo)
	for _, s := range suggestions {
		fmt.Printf("%6.3f  %s\n", s.Score, s.Domain)
	}
	return nil
}

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bearded-giant/giant-tooling/giantmem/internal/db"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/output"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/search"
	"github.com/bearded-giant/giant-tooling/giantmem/internal/usage"
	"github.com/spf13/cobra"
)

var (
	usageSince   string
	usageUntil   string
	usageAccount string
	usageModel   string
	usageJSON    bool

	usagePricesFetch bool
	usagePricesJSON  bool
)

// usageConfig is [usage] in config.toml: org uuid labels and per-model price overrides.
type usageConfig struct {
	Orgs   map[string]string      `toml:"orgs"`
	Prices map[string]usage.Price `toml:"prices"`
}

type usageRow struct {
	Day       string  `json:"day"`
	Account   string  `json:"account"`
	Model     string  `json:"model"`
	Messages  int64   `json:"messages"`
	Input     int64   `json:"input"`
	Cache5m   int64   `json:"cache_5m"`
	Cache1h   int64   `json:"cache_1h"`
	CacheRead int64   `json:"cache_read"`
	Output    int64   `json:"output"`
	Cost      float64 `json:"cost_usd"`
	Running   float64 `json:"running_usd"`
}

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Tokens and cost per day by account and model, from session transcripts",
	Long: `Reads the usage_daily view in archives.db: one row per local day, account, and
model with token buckets and the cost at API list prices. RUNNING is the
cumulative cost of that account + model pair over the window. Ingest fills the
rows; [usage] in config.toml labels orgs and overrides prices.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		uc := loadUserConfig().Usage
		if err := usage.Sync(a, effectivePrices(uc), uc.Orgs); err != nil {
			return err
		}
		since, err := search.ParseSince(usageSince)
		if err != nil {
			return err
		}
		where := []string{"day >= ?"}
		params := []any{since.In(time.Local).Format("2006-01-02")}
		if usageUntil != "" {
			until, err := search.ParseUntil(usageUntil)
			if err != nil {
				return err
			}
			where = append(where, "day < ?")
			params = append(params, until.In(time.Local).Format("2006-01-02"))
		}
		if usageAccount != "" {
			where = append(where, "account = ?")
			params = append(params, usageAccount)
		}
		if usageModel != "" {
			where = append(where, "model LIKE ?")
			params = append(params, usageModel)
		}
		rows, err := a.Query(
			`SELECT day, account, model, messages, input, cache_5m, cache_1h, cache_read, output, cost_usd
             FROM usage_daily WHERE `+strings.Join(where, " AND ")+` ORDER BY day, account, model`, params...)
		if err != nil {
			return err
		}
		defer rows.Close()
		var out []usageRow
		running := map[string]float64{}
		for rows.Next() {
			var r usageRow
			if err := rows.Scan(&r.Day, &r.Account, &r.Model, &r.Messages, &r.Input, &r.Cache5m, &r.Cache1h, &r.CacheRead, &r.Output, &r.Cost); err != nil {
				return err
			}
			k := r.Account + "\x00" + r.Model
			running[k] += r.Cost
			r.Running = running[k]
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if usageJSON {
			return output.JSON(out)
		}
		printUsage(out)
		return nil
	},
}

type priceRow struct {
	Model string `json:"model"`
	usage.Price
}

var usagePricesCmd = &cobra.Command{
	Use:   "prices",
	Short: "USD per MTok each model is priced at; --fetch refreshes from Anthropic's pricing page",
	Long: `Prints usage_prices after syncing it: the built-in table, then prices.toml
under the archive base (written by --fetch from ` + usage.DocsURL + `),
then [usage.prices] in config.toml.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if usagePricesFetch {
			p, err := usage.Fetch(usage.DocsURL)
			if err != nil {
				return err
			}
			if err := usage.SaveFile(pricesPath(), p); err != nil {
				return err
			}
			fmt.Printf("fetched %d models from %s\n", len(p), usage.DocsURL)
		}
		a, err := db.Open(archiveDBPath())
		if err != nil {
			return err
		}
		defer a.Close()
		uc := loadUserConfig().Usage
		if err := usage.Sync(a, effectivePrices(uc), uc.Orgs); err != nil {
			return err
		}
		rows, err := a.Query(`SELECT model, input, cache_5m, cache_1h, cache_read, output FROM usage_prices ORDER BY model`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var out []priceRow
		for rows.Next() {
			var r priceRow
			if err := rows.Scan(&r.Model, &r.Input, &r.Cache5m, &r.Cache1h, &r.CacheRead, &r.Output); err != nil {
				return err
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if usagePricesJSON {
			return output.JSON(out)
		}
		if st, err := os.Stat(pricesPath()); err == nil {
			fmt.Printf("%s fetched %s\n", pricesPath(), st.ModTime().Format("2006-01-02 15:04"))
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "MODEL\tINPUT\tCACHE_5M\tCACHE_1H\tCACHE_R\tOUTPUT")
		for _, r := range out {
			fmt.Fprintf(w, "%s\t%g\t%g\t%g\t%g\t%g\n", r.Model, r.Input, r.Cache5m, r.Cache1h, r.CacheRead, r.Output)
		}
		return w.Flush()
	},
}

func pricesPath() string { return filepath.Join(archiveBasePath(), "prices.toml") }

// effectivePrices layers the fetched table under the config overrides; Sync puts the built-ins beneath both.
func effectivePrices(uc usageConfig) map[string]usage.Price {
	p, err := usage.LoadFile(pricesPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	for k, v := range uc.Prices {
		p[k] = v
	}
	return p
}

func printUsage(rows []usageRow) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DAY\tACCOUNT\tMODEL\tMSGS\tINPUT\tCACHE_W\tCACHE_R\tOUTPUT\tCOST\tRUNNING")
	byAccount, byModel := map[string]float64{}, map[string]float64{}
	var total float64
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%.2f\t%.2f\n", r.Day, r.Account, r.Model, r.Messages,
			tok(r.Input), tok(r.Cache5m+r.Cache1h), tok(r.CacheRead), tok(r.Output), r.Cost, r.Running)
		byAccount[r.Account] += r.Cost
		byModel[r.Model] += r.Cost
		total += r.Cost
	}
	for _, k := range sortedKeysF(byAccount) {
		fmt.Fprintf(w, "total\t%s\t\t\t\t\t\t\t%.2f\t\n", k, byAccount[k])
	}
	for _, k := range sortedKeysF(byModel) {
		fmt.Fprintf(w, "total\t\t%s\t\t\t\t\t\t%.2f\t\n", k, byModel[k])
	}
	fmt.Fprintf(w, "total\t\t\t\t\t\t\t\t%.2f\t\n", total)
	w.Flush()
}

func sortedKeysF(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func tok(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func init() {
	usageCmd.Flags().StringVar(&usageSince, "since", "7d", "first day (e.g. 7d, 2026-10-26)")
	usageCmd.Flags().StringVar(&usageUntil, "until", "", "last day, inclusive (e.g. 2026-11-01)")
	usageCmd.Flags().StringVar(&usageAccount, "account", "", "one account label (e.g. rc-inc)")
	usageCmd.Flags().StringVar(&usageModel, "model", "", "models matching (SQL LIKE, e.g. 'claude-opus%')")
	usageCmd.Flags().BoolVar(&usageJSON, "json", false, "emit JSON")
	usagePricesCmd.Flags().BoolVar(&usagePricesFetch, "fetch", false, "download the pricing page and write prices.toml first")
	usagePricesCmd.Flags().BoolVar(&usagePricesJSON, "json", false, "emit JSON")
	usageCmd.AddCommand(usagePricesCmd)
	rootCmd.AddCommand(usageCmd)
}

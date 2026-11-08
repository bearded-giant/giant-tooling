package cmd

import (
	"fmt"
	"os"
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
		if err := usage.Sync(a, uc.Prices, uc.Orgs); err != nil {
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
	rootCmd.AddCommand(usageCmd)
}

// Package usage prices transcript token counts and labels the orgs they were billed to.
package usage

import "database/sql"

// Price is USD per million tokens for one model.
type Price struct {
	Input     float64 `toml:"input"`
	Cache5m   float64 `toml:"cache_5m"`
	Cache1h   float64 `toml:"cache_1h"`
	CacheRead float64 `toml:"cache_read"`
	Output    float64 `toml:"output"`
}

// DefaultPrices is the first-party API list; cache writes are 1.25x (5m) and 2x (1h) input.
var DefaultPrices = map[string]Price{
	"claude-fable-5-1":  {Input: 10, Cache5m: 12.5, Cache1h: 20, CacheRead: 0.25, Output: 50},
	"claude-mythos-5-1": {Input: 10, Cache5m: 12.5, Cache1h: 20, CacheRead: 0.25, Output: 50},
	"claude-fable-5":    {Input: 10, Cache5m: 12.5, Cache1h: 20, CacheRead: 1, Output: 50},
	"claude-opus-5-5":   {Input: 4, Cache5m: 5, Cache1h: 8, CacheRead: 0.2, Output: 20},
	"claude-opus-5":     {Input: 5, Cache5m: 6.25, Cache1h: 10, CacheRead: 0.5, Output: 25},
	"claude-opus-4-8":   {Input: 5, Cache5m: 6.25, Cache1h: 10, CacheRead: 0.5, Output: 25},
	"claude-opus-4-7":   {Input: 5, Cache5m: 6.25, Cache1h: 10, CacheRead: 0.5, Output: 25},
	"claude-opus-4-6":   {Input: 5, Cache5m: 6.25, Cache1h: 10, CacheRead: 0.5, Output: 25},
	"claude-sonnet-5-5": {Input: 2, Cache5m: 2.5, Cache1h: 4, CacheRead: 0.2, Output: 10},
	"claude-sonnet-5":   {Input: 2, Cache5m: 2.5, Cache1h: 4, CacheRead: 0.2, Output: 10},
	"claude-sonnet-4-6": {Input: 3, Cache5m: 3.75, Cache1h: 6, CacheRead: 0.3, Output: 15},
	"claude-haiku-5-5":  {Input: 0.1, Cache5m: 0.125, Cache1h: 0.2, CacheRead: 0.01, Output: 0.5},
	"claude-haiku-4-5":  {Input: 1, Cache5m: 1.25, Cache1h: 2, CacheRead: 0.1, Output: 5},
}

// Sync writes the tables the usage_daily view joins: the defaults, then the config's prices and org labels on top.
func Sync(db *sql.DB, prices map[string]Price, orgs map[string]string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	put := func(m map[string]Price) error {
		for model, p := range m {
			if _, err := tx.Exec(
				`INSERT INTO usage_prices (model, input, cache_5m, cache_1h, cache_read, output) VALUES (?, ?, ?, ?, ?, ?)
                 ON CONFLICT(model) DO UPDATE SET input = excluded.input, cache_5m = excluded.cache_5m,
                     cache_1h = excluded.cache_1h, cache_read = excluded.cache_read, output = excluded.output`,
				model, p.Input, p.Cache5m, p.Cache1h, p.CacheRead, p.Output,
			); err != nil {
				return err
			}
		}
		return nil
	}
	if err := put(DefaultPrices); err != nil {
		return err
	}
	if err := put(prices); err != nil {
		return err
	}
	for org, label := range orgs {
		if _, err := tx.Exec(
			`INSERT INTO usage_orgs (org, label) VALUES (?, ?) ON CONFLICT(org) DO UPDATE SET label = excluded.label`,
			org, label,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

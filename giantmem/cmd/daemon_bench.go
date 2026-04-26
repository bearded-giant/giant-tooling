package cmd

import (
	"sort"
	"time"

	"github.com/bryangrimes/gm/internal/daemon"
)

// runDaemonBench fires N find + health calls and reports p50/p99 latencies.
// Find target query is something cheap and broad to avoid empty-result skew.
func runDaemonBench(c *daemon.Client, n int) daemon.Bench {
	findLat := make([]float64, 0, n)
	healthLat := make([]float64, 0, n)

	for i := 0; i < n; i++ {
		start := time.Now()
		var out struct{}
		_ = c.Call("find", &daemon.FindParams{Query: "the", Limit: 5}, &out)
		findLat = append(findLat, msSince(start))

		start = time.Now()
		var h daemon.HealthResult
		_ = c.Call("health", nil, &h)
		healthLat = append(healthLat, msSince(start))
	}

	return daemon.Bench{
		FindP50Ms:   percentile(findLat, 0.50),
		FindP99Ms:   percentile(findLat, 0.99),
		StatusP50Ms: percentile(healthLat, 0.50),
		StatusP99Ms: percentile(healthLat, 0.99),
		Iterations:  n,
	}
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)
	idx := int(float64(len(cp)-1) * p)
	return cp[idx]
}

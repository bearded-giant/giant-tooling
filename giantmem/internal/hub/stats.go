package hub

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"
)

// gaps longer than this count as idle, so a session resumed days later is not days long
const DefaultIdleCap = 15 * time.Minute

type SessionLength struct {
	SessionID string `json:"session_id"`
	Project   string `json:"project"`
	FirstTS   string `json:"first_ts"`
	LastTS    string `json:"last_ts"`
	SpanSec   int    `json:"span_sec"`
	ActiveSec int    `json:"active_sec"`
	Calls     int    `json:"calls"`
}

type LengthSummary struct {
	Project   string `json:"project"`
	Sessions  int    `json:"sessions"`
	ActiveP50 int    `json:"active_p50_sec"`
	ActiveP95 int    `json:"active_p95_sec"`
	ActiveAvg int    `json:"active_avg_sec"`
	SpanP50   int    `json:"span_p50_sec"`
}

// SessionLengths measures main-thread calls only: subagent work runs inside the main thread's gaps.
func SessionLengths(d *sql.DB, q ToolQuery, idleCap time.Duration) ([]SessionLength, error) {
	q.Agent = "main"
	where, args := toolWhere(q, []string{"COALESCE(t.ts, '') != ''"}, nil)
	args = append(args, int(idleCap.Seconds()))
	rows, err := d.Query(fmt.Sprintf(`
        WITH g AS (
            SELECT t.session_id, t.project, t.ts,
                   (julianday(t.ts) - julianday(LAG(t.ts) OVER (PARTITION BY t.session_id ORDER BY t.ts))) * 86400 AS gap
              FROM tool_uses t %s)
        SELECT session_id, MAX(project), MIN(ts), MAX(ts),
               CAST(ROUND((julianday(MAX(ts)) - julianday(MIN(ts))) * 86400) AS INTEGER),
               CAST(ROUND(COALESCE(SUM(MIN(gap, ?)), 0)) AS INTEGER), COUNT(*)
          FROM g GROUP BY session_id ORDER BY MIN(ts)`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionLength{}
	for rows.Next() {
		var l SessionLength
		if err := rows.Scan(&l.SessionID, &l.Project, &l.FirstTS, &l.LastTS, &l.SpanSec, &l.ActiveSec, &l.Calls); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SummarizeLengths rolls sessions up per project, busiest project first.
func SummarizeLengths(ls []SessionLength) []LengthSummary {
	active, span := map[string][]int{}, map[string][]int{}
	for _, l := range ls {
		active[l.Project] = append(active[l.Project], l.ActiveSec)
		span[l.Project] = append(span[l.Project], l.SpanSec)
	}
	out := []LengthSummary{}
	for p, a := range active {
		total := 0
		for _, v := range a {
			total += v
		}
		out = append(out, LengthSummary{
			Project:   p,
			Sessions:  len(a),
			ActiveP50: percentile(a, 0.5),
			ActiveP95: percentile(a, 0.95),
			ActiveAvg: total / len(a),
			SpanP50:   percentile(span[p], 0.5),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Project < out[j].Project
	})
	return out
}

// nearest-rank percentile
func percentile(vs []int, p float64) int {
	if len(vs) == 0 {
		return 0
	}
	s := append([]int(nil), vs...)
	sort.Ints(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

type ToolCount struct {
	Tool   string `json:"tool"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

// folds mcp__<server>__<tool> to mcp__<server> so one server's tools read as one row
const mcpServerName = `CASE WHEN t.tool LIKE 'mcp\_\_%' ESCAPE '\' AND instr(substr(t.tool, 6), '__') > 0
                            THEN substr(t.tool, 1, 4 + instr(substr(t.tool, 6), '__')) ELSE t.tool END`

// ToolCounts ranks calls by tool under q's plain filters; Match, Starred, Limit, and WithIO do not apply.
func ToolCounts(d *sql.DB, q ToolQuery, byServer bool) ([]ToolCount, error) {
	name := "t.tool"
	if byServer {
		name = mcpServerName
	}
	where, args := toolWhere(q, nil, nil)
	rows, err := d.Query(fmt.Sprintf(`SELECT %s, COUNT(*), SUM(t.is_error)
                                        FROM tool_uses t %s
                                       GROUP BY 1 ORDER BY 2 DESC, 1`, name, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ToolCount{}
	for rows.Next() {
		var c ToolCount
		if err := rows.Scan(&c.Tool, &c.Calls, &c.Errors); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

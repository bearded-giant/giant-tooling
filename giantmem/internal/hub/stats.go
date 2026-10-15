package hub

import (
	"database/sql"
	"fmt"
)

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

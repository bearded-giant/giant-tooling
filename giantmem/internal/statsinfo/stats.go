// Package statsinfo wraps the archive stats query so it can be called from both
// the daemon handler and the CLI direct-open path.
package statsinfo

import "database/sql"

// Row is one document-count row from the stats query.
type Row struct {
	Project    string `json:"project"`
	SourceType string `json:"source_type"`
	DirType    string `json:"dir_type"`
	Count      int    `json:"count"`
}

// Query returns per-project per-source document counts from the archive DB.
func Query(archive *sql.DB) ([]Row, error) {
	rows, err := archive.Query(`
        SELECT project, source_type, COALESCE(dir_type,'') AS dir_type, COUNT(*)
          FROM documents
         GROUP BY project, source_type, dir_type
         ORDER BY project, source_type, dir_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.Project, &r.SourceType, &r.DirType, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

#!/usr/bin/env python3
"""MCP server exposing giantmem archive search as a tool."""

import os
import sqlite3
from datetime import datetime
from pathlib import Path
from typing import Optional

from mcp.server.fastmcp import FastMCP

ARCHIVE_BASE = Path(
    os.environ.get("GIANTMEM_ARCHIVE_BASE", os.path.expanduser("~/giantmem_archive"))
)
DB_PATH = ARCHIVE_BASE / "archives.db"

mcp = FastMCP("giantmem-search")


def _search(query, project=None, source_type=None, topic=None, limit=10):
    if not DB_PATH.exists():
        return {"error": f"no database at {DB_PATH} -- run gmq ingest first"}

    db = sqlite3.connect(str(DB_PATH))
    db.execute("PRAGMA journal_mode=WAL")

    where = []
    params = []

    if project:
        where.append("d.project = ?")
        params.append(project)
    if source_type:
        where.append("d.source_type = ?")
        params.append(source_type)
    if topic:
        where.append("d.topic = ?")
        params.append(topic)

    where_clause = (" AND " + " AND ".join(where)) if where else ""
    fetch_limit = max(limit * 5, 50)

    sql = f"""
        SELECT d.filepath, d.project, d.timestamp, d.source_type,
               d.dir_type, d.filename, d.session_id, d.topic, rank
        FROM documents_fts f
        JOIN documents d ON d.id = f.rowid
        WHERE documents_fts MATCH ?
        {where_clause}
        ORDER BY rank
        LIMIT ?
    """
    all_params = [query] + params + [fetch_limit]

    try:
        rows = db.execute(sql, all_params).fetchall()
    except sqlite3.OperationalError as e:
        db.close()
        return {"error": str(e)}

    # snippet extraction via fts5
    snippet_sql = f"""
        SELECT snippet(documents_fts, 0, '>>>', '<<<', '...', 40)
        FROM documents_fts f
        JOIN documents d ON d.id = f.rowid
        WHERE documents_fts MATCH ? AND d.filepath = ?
    """

    now = datetime.now()
    results = []
    for row in rows:
        filepath, proj, ts, src, dir_type, filename, sid, tp, rank = row
        # temporal decay
        try:
            archive_dt = datetime.strptime(ts, "%Y%m%d_%H%M%S")
            days_old = max(0, (now - archive_dt).days)
        except (ValueError, TypeError):
            days_old = 0
        decay = 1.0 / (1.0 + days_old * 0.01)
        score = abs(rank) * decay

        # get snippet
        snippet = ""
        try:
            srow = db.execute(snippet_sql, (query, filepath)).fetchone()
            if srow:
                snippet = srow[0]
        except sqlite3.OperationalError:
            pass

        results.append({
            "filepath": filepath,
            "project": proj,
            "source_type": src,
            "dir_type": dir_type,
            "topic": tp,
            "session_id": sid,
            "timestamp": ts,
            "score": round(score, 3),
            "snippet": snippet,
        })

    results.sort(key=lambda r: r["score"], reverse=True)
    results = results[:limit]
    db.close()
    return {"results": results, "total": len(results)}


@mcp.tool()
def search_archive(
    query: str,
    project: Optional[str] = None,
    source_type: Optional[str] = None,
    topic: Optional[str] = None,
    limit: int = 10,
) -> str:
    """Search across archived workspaces, session transcripts, and domain knowledge.

    Args:
        query: FTS5 search query (supports AND, OR, NOT, "phrases", prefix*)
        project: filter by project name (e.g. "cc-wt", "claude-code-config")
        source_type: filter by source: "workspace", "session", or "domain"
        topic: filter by session topic (e.g. "auth", "api", "bug", "feature")
        limit: max results to return (default 10)
    """
    import json
    result = _search(query, project=project, source_type=source_type, topic=topic, limit=limit)
    return json.dumps(result, indent=2)


if __name__ == "__main__":
    mcp.run()

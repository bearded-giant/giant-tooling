#!/usr/bin/env python3
"""sqlite fts5 search for scratch-archive"""

import argparse
import os
import re
import shlex
import shutil
import sqlite3
import subprocess
import sys
from datetime import datetime
from pathlib import Path

ARCHIVE_BASE = Path(os.environ.get("SCRATCH_ARCHIVE_BASE", Path.home() / "scratch_archive"))
DB_PATH = ARCHIVE_BASE / "archives.db"

SKIP_FILES = {".scratch-index", ".DS_Store"}
SKIP_DIRS = {".git"}
VALID_TYPES = {"plans", "context", "research", "reviews", "filebox", "history", "prompts", "features"}
TIMESTAMP_RE = re.compile(r"^\d{8}_\d{6}$")


def get_db(path=None):
    db = sqlite3.connect(str(path or DB_PATH))
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("PRAGMA synchronous=NORMAL")
    return db


SCHEMA_SQL = """
    CREATE TABLE IF NOT EXISTS documents (
        id INTEGER PRIMARY KEY,
        project TEXT NOT NULL,
        branch TEXT NOT NULL,
        timestamp TEXT NOT NULL,
        dir_type TEXT,
        filepath TEXT NOT NULL UNIQUE,
        filename TEXT NOT NULL,
        is_latest INTEGER DEFAULT 0,
        indexed_at TEXT NOT NULL
    );

    CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
        content,
        tokenize='porter unicode61'
    );
"""


def init_schema(db):
    db.executescript("""
        DROP TABLE IF EXISTS documents_fts;
        DROP TABLE IF EXISTS documents;
    """)
    db.executescript(SCHEMA_SQL)
    db.commit()


def ensure_schema(db):
    db.executescript(SCHEMA_SQL)
    db.commit()


def resolve_latest_timestamps(archive_base):
    """find all 'latest' symlinks and return set of resolved real paths"""
    latest = set()
    for link in archive_base.rglob("latest"):
        if link.is_symlink():
            resolved = link.resolve()
            if resolved.is_dir():
                latest.add(str(resolved))
    return latest


def parse_archive_path(filepath, archive_base):
    """extract project/branch/timestamp/dir_type from archive filepath"""
    rel = filepath.relative_to(archive_base)
    parts = rel.parts

    if len(parts) < 4:
        return None

    project = parts[0]
    branch = parts[1]
    timestamp = parts[2]

    if not TIMESTAMP_RE.match(timestamp):
        return None

    # dir_type is the folder right after timestamp, if it matches known types
    dir_type = None
    if len(parts) > 3 and parts[3] in VALID_TYPES:
        dir_type = parts[3]
    elif len(parts) > 3:
        dir_type = "root"

    return {
        "project": project,
        "branch": branch,
        "timestamp": timestamp,
        "dir_type": dir_type,
    }


def do_ingest(args):
    archive_base = ARCHIVE_BASE
    if not archive_base.exists():
        print(f"archive dir not found: {archive_base}", file=sys.stderr)
        return 1

    # scope ingest to a single project if specified
    if args.project:
        scan_root = archive_base / args.project
        if not scan_root.exists():
            print(f"project not found: {args.project}", file=sys.stderr)
            return 1
    else:
        scan_root = archive_base

    db = get_db()

    if args.project:
        # additive: delete only this project's rows, keep everything else
        ensure_schema(db)
        existing = db.execute(
            "SELECT id FROM documents WHERE project = ?", (args.project,)
        ).fetchall()
        for (doc_id,) in existing:
            db.execute("DELETE FROM documents_fts WHERE rowid = ?", (doc_id,))
        db.execute("DELETE FROM documents WHERE project = ?", (args.project,))
        db.commit()
    else:
        # full rebuild
        init_schema(db)

    latest_dirs = resolve_latest_timestamps(archive_base)
    now = datetime.now().isoformat()

    count = 0
    errors = 0

    for md_file in scan_root.rglob("*.md"):
        if md_file.name in SKIP_FILES:
            continue
        if any(skip in md_file.parts for skip in SKIP_DIRS):
            continue

        parsed = parse_archive_path(md_file, archive_base)
        if not parsed:
            continue

        # check if this file's timestamp dir is a latest target
        ts_dir = archive_base / parsed["project"] / parsed["branch"] / parsed["timestamp"]
        is_latest = 1 if str(ts_dir) in latest_dirs else 0

        try:
            content = md_file.read_text(errors="replace")
        except (OSError, PermissionError):
            errors += 1
            continue

        try:
            db.execute(
                """INSERT INTO documents
                   (project, branch, timestamp, dir_type, filepath, filename, is_latest, indexed_at)
                   VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
                (
                    parsed["project"],
                    parsed["branch"],
                    parsed["timestamp"],
                    parsed["dir_type"],
                    str(md_file),
                    md_file.name,
                    is_latest,
                    now,
                ),
            )
            doc_id = db.execute("SELECT last_insert_rowid()").fetchone()[0]
            db.execute(
                "INSERT INTO documents_fts (rowid, content) VALUES (?, ?)",
                (doc_id, content),
            )
            count += 1
        except sqlite3.IntegrityError:
            errors += 1
            continue

    db.commit()
    db.close()

    print(f"indexed {count} documents into {DB_PATH}")
    if errors:
        print(f"skipped {errors} files (read errors or duplicates)")
    return 0


def do_search(args):
    if not DB_PATH.exists():
        print(f"no database found at {DB_PATH} — run 'ingest' first", file=sys.stderr)
        return 1

    db = get_db()

    fts_query = args.pattern

    where = []
    params = []

    if args.project:
        where.append("d.project = ?")
        params.append(args.project)
    if args.type:
        where.append("d.dir_type = ?")
        params.append(args.type)
    if args.branch:
        where.append("d.branch = ?")
        params.append(args.branch)
    if args.latest:
        where.append("d.is_latest = 1")

    where_clause = (" AND " + " AND ".join(where)) if where else ""

    sql = f"""
        SELECT
            d.filepath,
            d.project,
            d.branch,
            d.timestamp,
            d.dir_type,
            d.filename,
            rank
        FROM documents_fts f
        JOIN documents d ON d.id = f.rowid
        WHERE documents_fts MATCH ?
        {where_clause}
        ORDER BY rank
        LIMIT ?
    """
    params = [fts_query] + params + [args.n]

    try:
        rows = db.execute(sql, params).fetchall()
    except sqlite3.OperationalError as e:
        print(f"search error: {e}", file=sys.stderr)
        print("tip: use simple terms or quote phrases with double quotes", file=sys.stderr)
        return 1

    db.close()

    if not rows:
        print(f"no results for: {args.pattern}")
        return 0

    # fzf picker: always use when --file-name (needs interactive pick even in subshell)
    use_fzf = (sys.stdout.isatty() or args.file_name) and shutil.which("fzf") and not args.no_fzf

    if use_fzf:
        return _fzf_pick(rows, args.pattern, open_file=not args.file_name)

    # plain output for piping
    for filepath, project, branch, ts, dir_type, filename, rank in rows:
        lineno = _find_match_line(filepath, args.pattern)
        rel = f"{project}/{branch}/{ts}/{dir_type or ''}/{filename}:{lineno}"
        score = f"[{abs(rank):.2f}]"
        print(f"{score} {rel}")

        if args.full:
            snippet = get_snippet(filepath, args.pattern)
            if snippet:
                for line in snippet.splitlines()[:4]:
                    print(f"        {line.strip()}")
            print()

    return 0


def _find_match_line(filepath, pattern):
    """find first line number matching any search term in the file"""
    terms = pattern.replace('"', "").split()
    try:
        for i, line in enumerate(Path(filepath).read_text(errors="replace").splitlines(), 1):
            lower = line.lower()
            if any(t.lower() in lower for t in terms):
                return i
    except (OSError, PermissionError):
        pass
    return 1


def _open_file(filepath):
    subprocess.run(["open", filepath])


def _fzf_pick(rows, pattern, open_file=False):
    # format: "filepath\tline\t[score] project/branch/ts/type/file:line"
    lines = []
    for filepath, project, branch, ts, dir_type, filename, rank in rows:
        lineno = _find_match_line(filepath, pattern)
        rel = f"{project}/{branch}/{ts}/{dir_type or ''}/{filename}:{lineno}"
        score = f"[{abs(rank):.2f}]"
        lines.append(f"{filepath}\t{lineno}\t{score} {rel}")

    fzf_input = "\n".join(lines)

    # bat preview centered on match line, piped through rg to highlight terms
    # grey background + bold white on matched terms cuts through any theme
    safe_pattern = shlex.quote(pattern)
    preview_cmd = (
        "file=$(echo {} | cut -f1); "
        "line=$(echo {} | cut -f2); "
        "start=$((line > 15 ? line - 15 : 1)); "
        "end=$((line + 15)); "
        'bat --color=always --style=numbers --highlight-line "$line" '
        f'--line-range "$start:$end" "$file" 2>/dev/null | '
        f"rg --color=always --colors 'match:bg:yellow' "
        f"--colors 'match:fg:black' --colors 'match:style:bold' "
        f"--passthru -i {safe_pattern} || "
        'sed -n "${start},${end}p" "$file"'
    )

    cmd = [
        "fzf", "--ansi",
        "--delimiter", "\t",
        "--with-nth", "3",
        "--preview", preview_cmd,
        "--preview-window", "right:60%:wrap",
        "--header", f"search: {pattern} | enter: select | esc: cancel",
        "--bind", "ctrl-u:preview-half-page-up,ctrl-d:preview-half-page-down",
    ]

    try:
        result = subprocess.run(
            cmd, input=fzf_input, stdout=subprocess.PIPE, stderr=None, text=True
        )
    except FileNotFoundError:
        print("fzf not found", file=sys.stderr)
        return 1

    if result.returncode != 0 or not result.stdout.strip():
        return 0

    selected = result.stdout.strip()
    parts = selected.split("\t")
    filepath, lineno = parts[0], parts[1]

    if open_file:
        _open_file(filepath)
    else:
        print(filepath)
    return 0


def get_snippet(filepath, pattern):
    """grab a matching snippet from the file for --full output"""
    try:
        text = Path(filepath).read_text(errors="replace")
    except (OSError, PermissionError):
        return None

    for i, line in enumerate(text.splitlines(), 1):
        if re.search(re.escape(pattern), line, re.IGNORECASE):
            start = max(0, i - 2)
            end = i + 2
            lines = text.splitlines()[start:end]
            return "\n".join(lines)
    return None


def do_stats(args):
    if not DB_PATH.exists():
        print(f"no database found at {DB_PATH} — run 'ingest' first", file=sys.stderr)
        return 1

    db = get_db()

    total = db.execute("SELECT COUNT(*) FROM documents").fetchone()[0]
    print(f"total documents: {total}\n")

    print("by project:")
    for row in db.execute(
        "SELECT project, COUNT(*) as cnt FROM documents GROUP BY project ORDER BY cnt DESC"
    ):
        print(f"  {row[0]}: {row[1]}")

    print("\nby type:")
    for row in db.execute(
        "SELECT dir_type, COUNT(*) as cnt FROM documents GROUP BY dir_type ORDER BY cnt DESC"
    ):
        print(f"  {row[0] or 'root'}: {row[1]}")

    latest = db.execute("SELECT COUNT(*) FROM documents WHERE is_latest = 1").fetchone()[0]
    print(f"\nlatest only: {latest}")

    db.close()
    return 0


def main():
    parser = argparse.ArgumentParser(description="scratch-archive fts5 search")
    sub = parser.add_subparsers(dest="command")

    p_ingest = sub.add_parser("ingest", help="rebuild db from file tree")
    p_ingest.add_argument("--project", "-p", help="ingest only this project")

    p_search = sub.add_parser("search", help="search with fts5")
    p_search.add_argument("pattern", help="search pattern")
    p_search.add_argument("-p", "--project", help="filter by project")
    p_search.add_argument("-t", "--type", help="filter by dir_type")
    p_search.add_argument("-b", "--branch", help="filter by branch")
    p_search.add_argument("-l", "--latest", action="store_true", help="latest archives only")
    p_search.add_argument("-n", type=int, default=20, help="max results (default: 20)")
    p_search.add_argument("--full", action="store_true", help="show matching content snippet")
    p_search.add_argument("--file-name", action="store_true", help="output filepath instead of opening")
    p_search.add_argument("--no-fzf", action="store_true", help="skip fzf, plain output")

    sub.add_parser("stats", help="show indexed doc counts")

    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        return 1

    cmds = {"ingest": do_ingest, "search": do_search, "stats": do_stats}
    return cmds[args.command](args)


if __name__ == "__main__":
    sys.exit(main() or 0)

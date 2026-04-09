#!/usr/bin/env python3
"""sqlite fts5 search for giantmem-archive + session transcripts"""

import argparse
import json
import os
import re
import shlex
import shutil
import sqlite3
import subprocess
import sys
import time
from datetime import datetime
from pathlib import Path

ARCHIVE_BASE = Path(
    os.environ.get("GIANTMEM_ARCHIVE_BASE", os.path.expanduser("~/giantmem_archive"))
)
DB_PATH = ARCHIVE_BASE / "archives.db"

PROJECTS_DIR = Path.home() / ".claude" / "projects"

SKIP_FILES = {".giantmem-index", ".DS_Store"}
SKIP_DIRS = {".git"}
VALID_TYPES = {"plans", "context", "research", "reviews", "filebox", "history", "prompts", "features", "domains"}
TIMESTAMP_RE = re.compile(r"^\d{8}_\d{6}$")

TOPIC_KEYWORDS = {
    'auth': ['auth', 'login', 'jwt', 'token', 'password', 'credential', 'oauth'],
    'api': ['api', 'endpoint', 'route', 'rest', 'graphql'],
    'database': ['database', 'sql', 'query', 'migration', 'model', 'schema'],
    'test': ['test', 'pytest', 'jest', 'coverage', 'mock', 'fixture'],
    'bug': ['bug', 'fix', 'error', 'debug', 'broken', 'failing'],
    'feature': ['feature', 'implement', 'add', 'create', 'build'],
    'refactor': ['refactor', 'cleanup', 'reorganize', 'restructure', 'rename'],
    'config': ['config', 'setting', 'env', 'environment', 'setup'],
    'docs': ['document', 'readme', 'explain', 'describe'],
    'perf': ['performance', 'optimize', 'speed', 'slow', 'cache'],
    'ui': ['ui', 'frontend', 'component', 'style', 'css', 'render'],
    'deploy': ['deploy', 'ci', 'cd', 'pipeline', 'docker'],
    'workspace': ['workspace', 'giantmem', 'hook', 'session', 'mcp', 'plugin'],
}


def get_db(path=None):
    db = sqlite3.connect(str(path or DB_PATH))
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("PRAGMA synchronous=NORMAL")
    return db


SCHEMA_SQL = """
    CREATE TABLE IF NOT EXISTS documents (
        id INTEGER PRIMARY KEY,
        project TEXT NOT NULL,
        timestamp TEXT NOT NULL,
        source_type TEXT NOT NULL DEFAULT 'workspace',
        dir_type TEXT,
        filepath TEXT NOT NULL UNIQUE,
        filename TEXT NOT NULL,
        is_latest INTEGER DEFAULT 0,
        session_id TEXT,
        topic TEXT,
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


def _migrate_schema(db):
    """add new columns to existing db if missing"""
    cursor = db.execute("PRAGMA table_info(documents)")
    cols = {row[1] for row in cursor.fetchall()}
    if "source_type" not in cols:
        db.execute("ALTER TABLE documents ADD COLUMN source_type TEXT NOT NULL DEFAULT 'workspace'")
    if "session_id" not in cols:
        db.execute("ALTER TABLE documents ADD COLUMN session_id TEXT")
    if "topic" not in cols:
        db.execute("ALTER TABLE documents ADD COLUMN topic TEXT")
    db.commit()


def ensure_schema(db):
    db.executescript(SCHEMA_SQL)
    _migrate_schema(db)
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
    """extract project/timestamp/dir_type from archive filepath.
    path layout: {archive_base}/{project}/{timestamp}/[dir_type]/..."""
    rel = filepath.relative_to(archive_base)
    parts = rel.parts

    if len(parts) < 3:
        return None

    project = parts[0]

    if not TIMESTAMP_RE.match(parts[1]):
        return None

    timestamp = parts[1]

    # dir_type is the folder right after timestamp, if it matches known types
    dir_type = None
    if len(parts) > 2 and parts[2] in VALID_TYPES:
        dir_type = parts[2]
    elif len(parts) > 2:
        dir_type = "root"

    return {
        "project": project,
        "timestamp": timestamp,
        "dir_type": dir_type,
    }


def flatten_domain_json(data):
    """flatten a domain json into fts-friendly searchable text"""
    lines = []

    lines.append(f"domain: {data.get('domain', '')}")
    lines.append(f"description: {data.get('description', '')}")

    for feat in data.get("explored_for_features", []):
        lines.append(f"feature: {feat}")

    for ep in data.get("entry_points", []):
        lines.append(f"entry_point: {ep.get('path', '')} ({ep.get('type', '')}) {ep.get('description', '')}")

    for kf in data.get("key_files", []):
        lines.append(f"key_file: {kf.get('path', '')} -- {kf.get('purpose', '')}")
        for export in kf.get("exports", []):
            lines.append(f"  export: {export}")
        for pattern in kf.get("patterns", []):
            lines.append(f"  pattern: {pattern}")
        for dep in kf.get("dependencies", []):
            lines.append(f"  depends_on: {dep}")

    arch = data.get("architecture", {})
    if arch:
        layers = arch.get("layers", [])
        if layers:
            lines.append(f"layers: {' -> '.join(layers)}")
        flow = arch.get("data_flow", "")
        if flow:
            lines.append(f"data_flow: {flow}")
        for p in arch.get("patterns", []):
            lines.append(f"architecture_pattern: {p}")
        for d in arch.get("key_decisions", []):
            lines.append(f"key_decision: {d}")

    dm = data.get("data_models", {})
    for table in dm.get("tables", []):
        lines.append(f"table: {table}")
    for ck in dm.get("cache_keys", []):
        lines.append(f"cache_key: {ck}")

    deps = data.get("dependencies", {})
    for d in deps.get("internal", []):
        lines.append(f"internal_dep: {d}")
    for d in deps.get("external", []):
        lines.append(f"external_dep: {d}")

    for g in data.get("gotchas", []):
        lines.append(f"gotcha: {g}")

    return "\n".join(lines)


def _ingest_file(db, filepath, parsed, is_latest, now, content_override=None):
    """insert a single file into documents + fts. returns (success, error).
    content_override: pre-extracted text to index instead of reading from filepath."""
    if content_override is not None:
        content = content_override
    else:
        try:
            content = filepath.read_text(errors="replace")
        except (OSError, PermissionError):
            return False, True

        # prepend filename so filename searches always match
        content = f"{filepath.name}\n{content}"

        # flatten domain json for better fts indexing
        if filepath.suffix == ".json" and parsed.get("dir_type") == "domains":
            try:
                import json as _json
                data = _json.loads(content)
                content = flatten_domain_json(data)
            except (ValueError, KeyError):
                pass

    source_type = parsed.get("source_type", "workspace")
    session_id = parsed.get("session_id")
    topic = parsed.get("topic")

    try:
        db.execute(
            """INSERT INTO documents
               (project, timestamp, source_type, dir_type, filepath, filename,
                is_latest, session_id, topic, indexed_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (
                parsed["project"],
                parsed["timestamp"],
                source_type,
                parsed["dir_type"],
                str(filepath),
                filepath.name,
                is_latest,
                session_id,
                topic,
                now,
            ),
        )
        doc_id = db.execute("SELECT last_insert_rowid()").fetchone()[0]
        db.execute(
            "INSERT INTO documents_fts (rowid, content) VALUES (?, ?)",
            (doc_id, content),
        )
        return True, False
    except sqlite3.IntegrityError:
        return False, True


def _extract_session_text(jsonl_path):
    """stream-parse a JSONL session file, extract conversation text for indexing.
    returns (text, session_id, project_name, cwd, mtime_ts)"""
    texts = []
    files_touched = []
    bash_cmds = []
    session_id = jsonl_path.stem
    cwd = None

    try:
        with open(jsonl_path, "r") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue

                # fast pre-filter: skip lines that are clearly tool_result junk
                if '"tool_result"' in line and '"tool_use"' not in line:
                    continue

                try:
                    msg = json.loads(line)
                except json.JSONDecodeError:
                    continue

                if not cwd:
                    cwd = msg.get("cwd")

                msg_type = msg.get("type")
                content = msg.get("message", {}).get("content", [])

                if msg_type in ("human", "user"):
                    if isinstance(content, str):
                        texts.append(content)
                    elif isinstance(content, list):
                        for block in content:
                            if isinstance(block, dict) and block.get("type") == "text":
                                text = block.get("text", "")
                                # skip system reminders injected by hooks
                                if "<system-reminder>" in text:
                                    continue
                                texts.append(text)
                            elif isinstance(block, str):
                                texts.append(block)

                elif msg_type == "assistant":
                    if isinstance(content, str):
                        texts.append(content)
                    elif isinstance(content, list):
                        for block in content:
                            if isinstance(block, dict):
                                if block.get("type") == "text":
                                    texts.append(block.get("text", ""))
                                elif block.get("type") == "tool_use":
                                    name = block.get("name", "")
                                    inp = block.get("input", {})
                                    if name in ("Read", "Write", "Edit"):
                                        fp = inp.get("file_path", "")
                                        if fp:
                                            files_touched.append(fp)
                                    elif name == "Bash":
                                        cmd = inp.get("command", "")
                                        if cmd:
                                            bash_cmds.append(cmd[:150])
    except (OSError, PermissionError):
        return None

    if not texts:
        return None

    # build indexable document
    parts = [f"session: {session_id}"]
    if cwd:
        parts.append(f"cwd: {cwd}")
    parts.append("")
    parts.extend(texts)
    if files_touched:
        parts.append("\n--- files touched ---")
        for fp in sorted(set(files_touched)):
            parts.append(fp)
    if bash_cmds:
        parts.append("\n--- commands ---")
        for cmd in bash_cmds[:20]:
            parts.append(cmd)

    return {
        "text": "\n".join(parts),
        "session_id": session_id,
        "cwd": cwd,
    }


def _detect_topic(text):
    """score text against topic keywords, return top topic or 'general'"""
    text_lower = text.lower()
    best_topic = "general"
    best_score = 0
    for topic, keywords in TOPIC_KEYWORDS.items():
        score = sum(len(re.findall(r'\b' + kw + r'\w*\b', text_lower)) for kw in keywords)
        if score > best_score:
            best_score = score
            best_topic = topic
    return best_topic if best_score > 2 else "general"


def _session_project_name(jsonl_path):
    """derive project name from claude projects directory structure.
    path: ~/.claude/projects/-Users-bryan-dev-foo/session.jsonl -> dev/foo"""
    rel = str(jsonl_path.relative_to(PROJECTS_DIR))
    project_dir = rel.split("/")[0]
    name = re.sub(r"^-Users-[^-]+-", "", project_dir).replace("-", "/", 2)
    return name


def _ingest_sessions(db, project_filter=None, force=False):
    """index session JSONL files into the database. returns (count, errors)."""
    if not PROJECTS_DIR.is_dir():
        return 0, 0

    now = datetime.now().isoformat()
    count = 0
    errors = 0

    # build mtime index of already-indexed sessions for incremental skip
    existing = {}
    if not force:
        try:
            rows = db.execute(
                "SELECT filepath, indexed_at FROM documents WHERE source_type = 'session'"
            ).fetchall()
            existing = {r[0]: r[1] for r in rows}
        except sqlite3.OperationalError:
            pass

    for jsonl_path in PROJECTS_DIR.rglob("*.jsonl"):
        if "subagents" in str(jsonl_path):
            continue

        project_name = _session_project_name(jsonl_path)
        if project_filter and project_filter.lower() not in project_name.lower():
            continue

        # incremental: skip if mtime hasn't changed
        fpath_str = str(jsonl_path)
        try:
            mtime = jsonl_path.stat().st_mtime
        except (OSError, FileNotFoundError):
            continue

        if fpath_str in existing and not force:
            indexed_at = existing[fpath_str]
            try:
                indexed_ts = datetime.fromisoformat(indexed_at).timestamp()
                if mtime <= indexed_ts:
                    continue
            except (ValueError, TypeError):
                pass
            # mtime changed -- delete old row and re-index
            old_id = db.execute("SELECT id FROM documents WHERE filepath = ?", (fpath_str,)).fetchone()
            if old_id:
                db.execute("DELETE FROM documents_fts WHERE rowid = ?", (old_id[0],))
                db.execute("DELETE FROM documents WHERE id = ?", (old_id[0],))

        result = _extract_session_text(jsonl_path)
        if not result:
            continue

        topic = _detect_topic(result["text"])
        session_date = time.strftime("%Y%m%d_%H%M%S", time.localtime(mtime))

        parsed = {
            "project": project_name,
            "timestamp": session_date,
            "source_type": "session",
            "dir_type": None,
            "session_id": result["session_id"],
            "topic": topic,
        }

        ok, err = _ingest_file(db, jsonl_path, parsed, 0, now, content_override=result["text"])
        if ok:
            count += 1
        if err:
            errors += 1

    return count, errors


def _ingest_workspaces(db, archive_base, scan_root, project=None):
    """index workspace markdown, domain json, and filebox files. returns (count, errors)."""
    if project:
        ensure_schema(db)
        existing = db.execute(
            "SELECT id FROM documents WHERE project = ? AND source_type != 'session'", (project,)
        ).fetchall()
        for (doc_id,) in existing:
            db.execute("DELETE FROM documents_fts WHERE rowid = ?", (doc_id,))
        db.execute("DELETE FROM documents WHERE project = ? AND source_type != 'session'", (project,))
        db.commit()

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
        parsed["source_type"] = "workspace"
        ts_dir = archive_base / parsed["project"] / parsed["timestamp"]
        is_latest = 1 if str(ts_dir) in latest_dirs else 0
        ok, err = _ingest_file(db, md_file, parsed, is_latest, now)
        if ok:
            count += 1
        if err:
            errors += 1

    for json_file in scan_root.rglob("domains/*.json"):
        if json_file.name.startswith(".") or json_file.name in SKIP_FILES:
            continue
        if any(skip in json_file.parts for skip in SKIP_DIRS):
            continue
        parsed = parse_archive_path(json_file, archive_base)
        if not parsed:
            continue
        parsed["source_type"] = "domain"
        ts_dir = archive_base / parsed["project"] / parsed["timestamp"]
        is_latest = 1 if str(ts_dir) in latest_dirs else 0
        ok, err = _ingest_file(db, json_file, parsed, is_latest, now)
        if ok:
            count += 1
        if err:
            errors += 1

    for fb_file in scan_root.rglob("filebox/*"):
        if not fb_file.is_file():
            continue
        if fb_file.name in SKIP_FILES or fb_file.name.startswith("."):
            continue
        if fb_file.suffix == ".md":
            continue
        if any(skip in fb_file.parts for skip in SKIP_DIRS):
            continue
        parsed = parse_archive_path(fb_file, archive_base)
        if not parsed:
            continue
        parsed["source_type"] = "workspace"
        ts_dir = archive_base / parsed["project"] / parsed["timestamp"]
        is_latest = 1 if str(ts_dir) in latest_dirs else 0
        ok, err = _ingest_file(db, fb_file, parsed, is_latest, now)
        if ok:
            count += 1
        if err:
            errors += 1

    return count, errors


def do_ingest(args):
    sessions_only = getattr(args, "sessions_only", False)
    workspaces_only = getattr(args, "workspaces_only", False)
    force = getattr(args, "force", False)

    db = get_db()

    do_workspaces = not sessions_only
    do_sessions = not workspaces_only

    if do_workspaces:
        archive_base = ARCHIVE_BASE
        if not archive_base.exists():
            if not do_sessions:
                print(f"archive dir not found: {archive_base}", file=sys.stderr)
                return 1
            print(f"warning: archive dir not found, skipping workspaces", file=sys.stderr)
            do_workspaces = False

    ws_count = ws_errors = 0
    s_count = s_errors = 0

    if do_workspaces:
        archive_base = ARCHIVE_BASE
        if args.project:
            scan_root = archive_base / args.project
            if not scan_root.exists():
                print(f"project not found in archive: {args.project}", file=sys.stderr)
                if not do_sessions:
                    return 1
            else:
                if not sessions_only and not args.project:
                    init_schema(db)
                else:
                    ensure_schema(db)
                ws_count, ws_errors = _ingest_workspaces(db, archive_base, scan_root, project=args.project)
        else:
            if not sessions_only:
                # full workspace rebuild -- drop and recreate non-session rows
                ensure_schema(db)
                existing = db.execute(
                    "SELECT id FROM documents WHERE source_type != 'session'"
                ).fetchall()
                for (doc_id,) in existing:
                    db.execute("DELETE FROM documents_fts WHERE rowid = ?", (doc_id,))
                db.execute("DELETE FROM documents WHERE source_type != 'session'")
                db.commit()
            ws_count, ws_errors = _ingest_workspaces(db, archive_base, archive_base)

    if do_sessions:
        ensure_schema(db)
        s_count, s_errors = _ingest_sessions(db, project_filter=args.project, force=force)

    db.commit()
    db.close()

    parts = []
    if do_workspaces:
        parts.append(f"{ws_count} workspace docs")
    if do_sessions:
        parts.append(f"{s_count} sessions")
    print(f"indexed {', '.join(parts)} into {DB_PATH}")

    total_errors = ws_errors + s_errors
    if total_errors:
        print(f"skipped {total_errors} files (read errors or duplicates)")
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
    if args.latest:
        where.append("d.is_latest = 1")
    source = getattr(args, "source", None)
    if source:
        where.append("d.source_type = ?")
        params.append(source)
    topic = getattr(args, "topic", None)
    if topic:
        where.append("d.topic = ?")
        params.append(topic)

    where_clause = (" AND " + " AND ".join(where)) if where else ""

    # fetch extra rows for re-ranking after temporal decay
    fetch_limit = max(args.n * 5, 100)
    sql = f"""
        SELECT
            d.filepath,
            d.project,
            d.timestamp,
            d.source_type,
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
    params = [fts_query] + params + [fetch_limit]

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

    # apply temporal decay: newer archives rank higher
    now = datetime.now()
    decayed = []
    for row in rows:
        filepath, project, ts, source_type, dir_type, filename, rank = row
        days_old = _days_from_timestamp(ts, now)
        decay = 1.0 / (1.0 + days_old * 0.01)
        adjusted_rank = rank * decay
        decayed.append((filepath, project, ts, source_type, dir_type, filename, adjusted_rank))

    decayed.sort(key=lambda r: r[6])
    rows = decayed[: args.n]

    # fzf picker: always use when --file-name (needs interactive pick even in subshell)
    use_fzf = (sys.stdout.isatty() or args.file_name) and shutil.which("fzf") and not args.no_fzf

    if use_fzf:
        return _fzf_pick(rows, args.pattern, open_file=not args.file_name)

    # plain output for piping
    for filepath, project, ts, source_type, dir_type, filename, rank in rows:
        lineno = _find_match_line(filepath, args.pattern)
        tag = f"[{source_type}]" if source_type != "workspace" else ""
        rel = f"{project}/{ts}/{dir_type or ''}/{filename}:{lineno}"
        score = f"[{abs(rank):.2f}]"
        print(f"{score} {tag} {rel}" if tag else f"{score} {rel}")

        if args.full:
            snippet = get_snippet(filepath, args.pattern)
            if snippet:
                for line in snippet.splitlines()[:4]:
                    print(f"        {line.strip()}")
            print()

    return 0


def _days_from_timestamp(ts, now):
    """calculate days between archive timestamp (YYYYMMDD_HHMMSS) and now"""
    try:
        archive_dt = datetime.strptime(ts, "%Y%m%d_%H%M%S")
        return max(0, (now - archive_dt).days)
    except (ValueError, TypeError):
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
    # format: "filepath\tline\t[score] [source] project/ts/type/file:line"
    lines = []
    for filepath, project, ts, source_type, dir_type, filename, rank in rows:
        lineno = _find_match_line(filepath, pattern)
        rel = f"{project}/{ts}/{dir_type or ''}/{filename}:{lineno}"
        score = f"[{abs(rank):.2f}]"
        tag = f"[{source_type}] " if source_type != "workspace" else ""
        lines.append(f"{filepath}\t{lineno}\t{score} {tag}{rel}")

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

    print("by source:")
    for row in db.execute(
        "SELECT source_type, COUNT(*) as cnt FROM documents GROUP BY source_type ORDER BY cnt DESC"
    ):
        print(f"  {row[0]}: {row[1]}")

    print("\nby project:")
    for row in db.execute(
        "SELECT project, COUNT(*) as cnt FROM documents GROUP BY project ORDER BY cnt DESC"
    ):
        print(f"  {row[0]}: {row[1]}")

    print("\nby dir_type (workspace only):")
    for row in db.execute(
        "SELECT dir_type, COUNT(*) as cnt FROM documents WHERE source_type != 'session' GROUP BY dir_type ORDER BY cnt DESC"
    ):
        print(f"  {row[0] or 'root'}: {row[1]}")

    print("\nby topic (sessions only):")
    for row in db.execute(
        "SELECT topic, COUNT(*) as cnt FROM documents WHERE source_type = 'session' AND topic IS NOT NULL GROUP BY topic ORDER BY cnt DESC"
    ):
        print(f"  {row[0]}: {row[1]}")

    latest = db.execute("SELECT COUNT(*) FROM documents WHERE is_latest = 1").fetchone()[0]
    print(f"\nlatest only: {latest}")

    db.close()
    return 0


def main():
    parser = argparse.ArgumentParser(description="giantmem-archive fts5 search")
    sub = parser.add_subparsers(dest="command")

    p_ingest = sub.add_parser("ingest", help="rebuild db from file tree + sessions")
    p_ingest.add_argument("--project", "-p", help="ingest only this project")
    p_ingest.add_argument("--sessions-only", action="store_true", help="only index session transcripts")
    p_ingest.add_argument("--workspaces-only", action="store_true", help="only index workspace archives")
    p_ingest.add_argument("--force", action="store_true", help="force full rebuild (ignore mtime)")

    p_search = sub.add_parser("search", help="search with fts5")
    p_search.add_argument("pattern", help="search pattern")
    p_search.add_argument("-p", "--project", help="filter by project")
    p_search.add_argument("-t", "--type", help="filter by dir_type (workspace only)")
    p_search.add_argument("-s", "--source", choices=["workspace", "session", "domain"], help="filter by source type")
    p_search.add_argument("--topic", help="filter by session topic")
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

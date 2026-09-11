#!/usr/bin/env bash
# fixture check for giantmem-db-prune.sh: selection, export, delete, fts consistency
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

export GIANTMEM_ARCHIVE_BASE="$tmp/archive"
export GIANTMEM_BACKUP_DEST="$tmp/dest"
mkdir -p "$GIANTMEM_ARCHIVE_BASE"
db="$GIANTMEM_ARCHIVE_BASE/test.db"

sqlite3 "$db" <<'SQL'
CREATE TABLE live_docs (path TEXT PRIMARY KEY, project TEXT NOT NULL, worktree_path TEXT,
  feature TEXT, dir_type TEXT, session_id TEXT, git_sha TEXT, mtime INTEGER NOT NULL,
  ingested_at TEXT NOT NULL, content TEXT NOT NULL, canonical_project TEXT);
CREATE VIRTUAL TABLE live_docs_fts USING fts5(path, project, feature, dir_type, content,
  tokenize='porter unicode61', content='live_docs', content_rowid='rowid');
CREATE TRIGGER live_docs_ai AFTER INSERT ON live_docs BEGIN
  INSERT INTO live_docs_fts(rowid, path, project, feature, dir_type, content)
  VALUES (new.rowid, new.path, new.project, COALESCE(new.feature,''), COALESCE(new.dir_type,''), new.content);
END;
CREATE TRIGGER live_docs_ad AFTER DELETE ON live_docs BEGIN
  INSERT INTO live_docs_fts(live_docs_fts, rowid, path, project, feature, dir_type, content)
  VALUES ('delete', old.rowid, old.path, old.project, COALESCE(old.feature,''), COALESCE(old.dir_type,''), old.content);
END;
CREATE TABLE artifacts (id TEXT PRIMARY KEY, repo TEXT, path TEXT);
INSERT INTO live_docs VALUES
  ('/old/a.md','junk-wt',NULL,NULL,'features',NULL,NULL,strftime('%s','now','-400 days'),'x','ancient junk','junk-wt'),
  ('/old/b.md','keep-wt',NULL,NULL,'features',NULL,NULL,strftime('%s','now','-400 days'),'x','ancient keeper','keep-wt'),
  ('/new/c.md','junk-wt',NULL,NULL,'features',NULL,NULL,strftime('%s','now'),'x','fresh junk','junk-wt');
INSERT INTO artifacts VALUES ('a1','junk-wt','/old/a.md'),('a2','keep-wt','/old/b.md');
SQL

run() { "$here/../internal/prune/prune.sh" --db test.db "$@" 2>/dev/null; }
count() { sqlite3 "$db" "SELECT COUNT(*) FROM $1;"; }

run --older-than 90d --repo junk-wt | grep -q 'ancient junk\|junk-wt' || { echo "FAIL: dry run listed nothing"; exit 1; }
[ "$(count live_docs)" = 3 ] || { echo "FAIL: dry run mutated db"; exit 1; }

"$here/../internal/prune/prune.sh" --db test.db --older-than 90d --repo junk-wt --yes --no-vacuum >/dev/null 2>&1
[ "$(count live_docs)" = 2 ] || { echo "FAIL: expected 2 docs left, got $(count live_docs)"; exit 1; }
[ "$(sqlite3 "$db" "SELECT path FROM live_docs ORDER BY path" | tr '\n' ' ')" = "/new/c.md /old/b.md " ] \
  || { echo "FAIL: wrong rows survived"; exit 1; }
[ "$(count artifacts)" = 1 ] || { echo "FAIL: artifacts row not pruned"; exit 1; }
[ "$(sqlite3 "$db" "SELECT COUNT(*) FROM live_docs_fts WHERE live_docs_fts MATCH 'ancient'")" = 1 ] \
  || { echo "FAIL: fts out of sync with live_docs"; exit 1; }

enc=$(ls "$GIANTMEM_BACKUP_DEST"/*.gpg)
[ -s "$enc" ] || { echo "FAIL: no ciphertext published"; exit 1; }
gpg --batch --yes --decrypt "$enc" > "$tmp/restore.db" 2>/dev/null
[ "$(sqlite3 "$tmp/restore.db" "SELECT content FROM live_docs")" = "ancient junk" ] \
  || { echo "FAIL: archive does not round-trip"; exit 1; }

sqlite3 "$db" "ATTACH '$tmp/restore.db' AS r; INSERT OR IGNORE INTO live_docs SELECT * FROM r.live_docs;"
[ "$(count live_docs)" = 3 ] || { echo "FAIL: restore path broken"; exit 1; }
echo "ok: all checks passed"

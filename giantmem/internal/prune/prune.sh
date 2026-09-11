#!/usr/bin/env bash
# giantmem-db-prune.sh -- lift old / per-repo live_docs rows out of live.db into a
# standalone encrypted sqlite archive parked next to the db backups.
#
# Selection: --older-than and/or --repo (ANDed). Nothing mutates without --yes.
# Order: export -> integrity_check -> gpg -> publish -> delete -> fts optimize -> VACUUM.
#
# Restore:
#   gpg --decrypt live-prune-<stamp>.db.gpg > restore.db
#   sqlite3 live.db "ATTACH 'restore.db' AS r; \
#     INSERT OR IGNORE INTO live_docs SELECT * FROM r.live_docs;"
set -euo pipefail

ARCHIVE_BASE="${GIANTMEM_ARCHIVE_BASE:-$HOME/giantmem_archive}"
GPG_KEY="${GIANTMEM_BACKUP_GPG_KEY:-33F36CDDD530C52910A4608D61258A79557ECB4A}"
DEST="${GIANTMEM_BACKUP_DEST:-$HOME/Library/Mobile Documents/com~apple~CloudDocs/giantmem-db-backups}"
STAGING="$HOME/.cache/giantmem/db-prune-staging"
LOG="$HOME/.cache/giantmem/db-prune.log"

SQLITE="$(command -v sqlite3 || echo /usr/bin/sqlite3)"
GPG="$(command -v gpg || echo /opt/homebrew/bin/gpg)"

db_name=live.db
older=""
repo=""
apply=0
stats=0
vacuum=1
vacuum_only=0

usage() {
  cat >&2 <<'USAGE'
usage: giantmem-db-prune.sh [--older-than 90d] [--repo NAME] [--yes]
                            [--db live.db] [--no-vacuum] [--stats] [--vacuum-only]

  --older-than N[d|w|m|y]  rows with mtime older than this
  --repo NAME              match canonical_project or project
  --yes                    actually export+delete (default: dry run)
  --stats                  print size by repo/month and exit
  --vacuum-only            skip selection; just checkpoint + reclaim disk

VACUUM needs an exclusive lock. Stop the writers first, or it gets skipped:
  launchctl unload ~/Library/LaunchAgents/com.bryan.giantmem-*.plist
  pkill -x Giantmem; pkill -f 'giantmem daemon serve'
USAGE
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --older-than) older="${2:?}"; shift 2 ;;
    --repo)       repo="${2:?}"; shift 2 ;;
    --db)         db_name="${2:?}"; shift 2 ;;
    --yes)        apply=1; shift ;;
    --no-vacuum)  vacuum=0; shift ;;
    --stats)      stats=1; shift ;;
    --vacuum-only) vacuum_only=1; shift ;;
    -h|--help)    usage ;;
    *) echo "unknown arg: $1" >&2; usage ;;
  esac
done

DB="$ARCHIVE_BASE/$db_name"
mkdir -p "$(dirname "$LOG")" "$STAGING"
log() { printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$*" | tee -a "$LOG" >&2; }

for tool in "$SQLITE" "$GPG"; do
  [ -x "$tool" ] || { log "FATAL: missing tool $tool"; exit 2; }
done
[ -f "$DB" ] || { log "FATAL: no db at $DB"; exit 2; }

sizes_now() {
  local wal="$DB-wal" main wal_mb
  main=$(( $(wc -c <"$DB" | tr -d ' ') / 1048576 ))
  if [ -f "$wal" ]; then
    wal_mb=$(( $(wc -c <"$wal" | tr -d ' ') / 1048576 ))
    if [ "$wal_mb" -gt 0 ]; then
      printf '%sMB (+%sMB wal)' "$main" "$wal_mb"
      return 0
    fi
  fi
  printf '%sMB' "$main"
}

reclaim() {
  local before="$1" wal
  log "checkpointing WAL then vacuuming (needs ~$((before / 1073741824))GB free scratch)"
  "$SQLITE" "$DB" >/dev/null <<'SQL' || log "WARN checkpoint did not complete"
PRAGMA busy_timeout = 600000;
PRAGMA wal_checkpoint(TRUNCATE);
SQL
  if "$SQLITE" "$DB" >/dev/null <<'SQL'
PRAGMA busy_timeout = 600000;
VACUUM;
SQL
  then
    # VACUUM in WAL mode writes the whole new db through the wal, so without a
    # second checkpoint the file sizes add up to ~2x and look like growth.
    "$SQLITE" "$DB" >/dev/null <<'SQL' || log "WARN post-vacuum checkpoint did not complete; wal stays large until one runs"
PRAGMA busy_timeout = 600000;
PRAGMA wal_checkpoint(TRUNCATE);
SQL
  else
    wal="$DB-wal"
    log "WARN vacuum skipped: another process holds $DB (daemon, GUI, statusline, or an ingest)."
    log "WARN freed pages stay in the file and get reused. To reclaim now: stop the writers, then"
    log "WARN   $0 --db $db_name --vacuum-only"
    [ -f "$wal" ] && log "WARN wal is $(( $(wc -c <"$wal" | tr -d ' ') / 1048576 ))MB; a checkpoint collapses it"
    return 0
  fi
  return 0
}

if [ "$stats" = 1 ]; then
  "$SQLITE" -column -header "$DB" "
    SELECT COALESCE(NULLIF(canonical_project,''),project) repo,
           strftime('%Y-%m', mtime, 'unixepoch') month,
           COUNT(*) docs, sum(length(content))/1048576 mb
    FROM live_docs GROUP BY 1,2 ORDER BY 4 DESC LIMIT 40;"
  exit 0
fi

if [ "$vacuum_only" = 1 ]; then
  reclaim "$(wc -c <"$DB" | tr -d ' ')"
  log "done: $DB now $(sizes_now)"
  exit 0
fi

preds=()
if [ -n "$older" ]; then
  n="${older%[dwmy]}"
  case "$older" in
    *d) span="$n days" ;;
    *w) span="$((n * 7)) days" ;;
    *m) span="$n months" ;;
    *y) span="$n years" ;;
    *)  span="$older days" ;;
  esac
  case "$n" in *[!0-9]*|'') echo "bad --older-than: $older" >&2; exit 2 ;; esac
  preds+=("mtime < strftime('%s','now','-$span')")
fi
if [ -n "$repo" ]; then
  # single quotes would break the generated SQL; reject instead of escaping
  case "$repo" in *\'*) echo "--repo may not contain a quote" >&2; exit 2 ;; esac
  preds+=("(canonical_project = '$repo' OR project = '$repo')")
fi
[ ${#preds[@]} -gt 0 ] || { echo "need --older-than and/or --repo" >&2; usage; }

WHERE="${preds[0]}"
[ ${#preds[@]} -gt 1 ] && WHERE="$WHERE AND ${preds[1]}"

read -r n_rows n_mb <<<"$("$SQLITE" -separator ' ' "$DB" \
  "SELECT COUNT(*), COALESCE(sum(length(content)),0)/1048576 FROM live_docs WHERE $WHERE;")"
log "match: $n_rows docs / ${n_mb}MB  where $WHERE"
[ "$n_rows" -gt 0 ] || { log "nothing to prune"; exit 0; }

if [ "$apply" != 1 ]; then
  "$SQLITE" -column -header "$DB" "
    SELECT COALESCE(NULLIF(canonical_project,''),project) repo,
           COALESCE(dir_type,'?') dir_type, COUNT(*) docs, sum(length(content))/1048576 mb
    FROM live_docs WHERE $WHERE GROUP BY 1,2 ORDER BY 4 DESC LIMIT 20;"
  echo "dry run -- pass --yes to export, encrypt, and delete these rows" >&2
  exit 0
fi

stamp="$(date -u '+%Y%m%dT%H%M%SZ')"
tag="${db_name%.db}-prune-$stamp"
out="$STAGING/$tag.db"
enc="$out.gpg"
rm -f "$out" "$enc"

has_artifacts="$("$SQLITE" "$DB" \
  "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='artifacts';")"
art_export=""
art_delete=""
if [ "$has_artifacts" = 1 ]; then
  art_export="CREATE TABLE a.artifacts AS SELECT * FROM artifacts WHERE path IN (SELECT path FROM a.live_docs);"
  art_delete="DELETE FROM artifacts WHERE path IN (SELECT path FROM a.live_docs);"
fi

"$SQLITE" "$DB" >/dev/null <<SQL
PRAGMA busy_timeout = 60000;
ATTACH '$out' AS a;
CREATE TABLE a.live_docs AS SELECT * FROM live_docs WHERE $WHERE;
$art_export
DETACH a;
SQL

exported="$("$SQLITE" "$out" 'SELECT COUNT(*) FROM live_docs;')"
[ "$exported" = "$n_rows" ] || { log "FAIL: exported $exported != matched $n_rows -- db untouched"; exit 1; }
check="$("$SQLITE" "$out" 'PRAGMA integrity_check;' 2>&1 | head -1)"
[ "$check" = ok ] || { log "FAIL: export integrity_check='$check' -- db untouched"; exit 1; }

"$GPG" --batch --yes --trust-model always -r "$GPG_KEY" --output "$enc" --encrypt "$out"
[ -s "$enc" ] || { log "FAIL: empty ciphertext -- db untouched"; exit 1; }

mkdir -p "$DEST"
tmp="$DEST/.$tag.db.gpg.tmp.$$"
cp "$enc" "$tmp"
mv -f "$tmp" "$DEST/$tag.db.gpg"
log "archived $exported docs -> $DEST/$tag.db.gpg ($(wc -c <"$enc" | tr -d ' ') bytes)"

before="$(wc -c <"$DB" | tr -d ' ')"
# ponytail: one big DELETE under busy_timeout; stop the sweep daemon first if it ever loses the race
"$SQLITE" "$DB" >/dev/null <<SQL
PRAGMA busy_timeout = 60000;
ATTACH '$out' AS a;
$art_delete
DELETE FROM live_docs WHERE path IN (SELECT path FROM a.live_docs);
DETACH a;
INSERT INTO live_docs_fts(live_docs_fts) VALUES('optimize');
SQL
rm -f "$out" "$enc"

if [ "$vacuum" = 1 ]; then
  reclaim "$before"
fi
log "done: $DB $((before / 1048576))MB -> $(sizes_now)"

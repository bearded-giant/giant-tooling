#!/usr/bin/env bash
# giantmem-db-backup.sh -- local-first encrypted backup of the giantmem DBs.
#
# Per db: sqlite3 .backup (safe online snapshot under WAL) -> PRAGMA
# integrity_check -> gpg --encrypt to your key -> publish to iCloud, overwriting
# the single current copy only after validation (one .prev kept for rollback).
#
# No network/VPS/tailscale. gpg is asymmetric: backup needs only the PUBLIC key,
# RESTORE needs the private key 8390A4002604AC93 -- keep an exported copy safe.
#
# Restore:
#   gpg --decrypt live.db.gpg > live.db && sqlite3 live.db 'PRAGMA integrity_check;'
set -euo pipefail

ARCHIVE_BASE="${GIANTMEM_ARCHIVE_BASE:-$HOME/giantmem_archive}"
GPG_KEY="${GIANTMEM_BACKUP_GPG_KEY:-8390A4002604AC93}"
DEST="${GIANTMEM_BACKUP_DEST:-$HOME/Library/Mobile Documents/com~apple~CloudDocs/giantmem-db-backups}"
STAGING="$HOME/.cache/giantmem/db-backup-staging"
LOG="$HOME/.cache/giantmem/db-backup.log"
DBS=(live.db archives.db)

SQLITE="$(command -v sqlite3 || echo /usr/bin/sqlite3)"
GPG="$(command -v gpg || echo /opt/homebrew/bin/gpg)"

mkdir -p "$(dirname "$LOG")" "$STAGING" "$DEST"
log() { printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S')" "$*" | tee -a "$LOG" >&2; }

for tool in "$SQLITE" "$GPG"; do
  [ -x "$tool" ] || { log "FATAL: missing tool $tool"; exit 2; }
done

backup_one() {
  local name="$1"
  local src="$ARCHIVE_BASE/$name"
  local staged="$STAGING/$name"
  local enc="$STAGING/$name.gpg"
  [ -f "$src" ] || { log "skip $name (not present)"; return 0; }

  rm -f "$staged" "$enc"
  # online hot-copy: consistent even while the daemon writes (WAL).
  if ! "$SQLITE" "$src" ".backup '$staged'"; then
    log "FAIL $name: sqlite .backup"; return 1
  fi

  local check
  check="$("$SQLITE" "$staged" 'PRAGMA integrity_check;' 2>&1 | head -1)"
  if [ "$check" != "ok" ]; then
    log "FAIL $name: integrity_check='$check' -- NOT publishing (last good kept)"; return 1
  fi

  if ! "$GPG" --batch --yes --trust-model always -r "$GPG_KEY" \
        --output "$enc" --encrypt "$staged"; then
    log "FAIL $name: gpg encrypt"; return 1
  fi
  [ -s "$enc" ] || { log "FAIL $name: empty ciphertext"; return 1; }

  # publish: copy into iCloud, rotate current->prev, atomic rename tmp->current.
  local cur="$DEST/$name.gpg" prev="$DEST/$name.gpg.prev" tmp="$DEST/.$name.gpg.tmp.$$"
  cp "$enc" "$tmp"
  [ -f "$cur" ] && mv -f "$cur" "$prev"
  mv -f "$tmp" "$cur"

  log "ok $name: integrity ok, $(wc -c <"$enc" | tr -d ' ') bytes encrypted -> $cur"
  rm -f "$staged" "$enc"
  return 0
}

rc=0
log "backup start -> $DEST"
for db in "${DBS[@]}"; do
  backup_one "$db" || rc=1
done
log "done rc=$rc"
exit "$rc"

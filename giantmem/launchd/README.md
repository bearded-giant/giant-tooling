# launchd agents

macOS LaunchAgents for giantmem background sweeps.

## session-sweep

Re-indexes `~/.claude/projects/**/*.jsonl` into `archives.db` every 5 minutes. Closes the gap where SessionEnd-only ingest misses still-active sessions and any out-of-band jsonl edits.

Install:

```bash
# edit the plist if your binary path / user differ
cp giantmem/launchd/com.bryan.giantmem-session-sweep.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.bryan.giantmem-session-sweep.plist
launchctl list | grep giantmem  # confirm loaded
```

Logs land at `~/.cache/giantmem/session-sweep.{out,err}`.

Uninstall:

```bash
launchctl unload ~/Library/LaunchAgents/com.bryan.giantmem-session-sweep.plist
rm ~/Library/LaunchAgents/com.bryan.giantmem-session-sweep.plist
```

## db-backup

Local-first encrypted backup of `live.db` + `archives.db` every 2 hours, via `../scripts/giantmem-db-backup.sh`. Per db: `sqlite3 .backup` (consistent online snapshot under WAL) -> `PRAGMA integrity_check` -> `gpg --encrypt` to key `8390A4002604AC93` -> publish to iCloud Drive (`~/Library/Mobile Documents/com~apple~CloudDocs/giantmem-db-backups/`), overwriting the single current `.gpg` only after validation. One `.prev` is kept for rollback. No network/VPS/tailscale. Replaced the old restic-to-VPS job.

gpg is asymmetric: backup needs only the public key, **restore needs the private key** — keep an exported copy of `8390A4002604AC93` somewhere safe or the encrypted DBs are unreadable.

Overrides via env: `GIANTMEM_BACKUP_DEST`, `GIANTMEM_BACKUP_GPG_KEY`, `GIANTMEM_ARCHIVE_BASE`.

Install:

```bash
cp giantmem/launchd/com.bryan.giantmem-db-backup.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.bryan.giantmem-db-backup.plist
launchctl list | grep giantmem  # confirm loaded
```

Logs land at `~/.cache/giantmem/db-backup.log` (and launchd `db-backup-launchd.{out,err}`).

Restore:

```bash
gpg --decrypt live.db.gpg > live.db
sqlite3 live.db 'PRAGMA integrity_check;'
```

Uninstall:

```bash
launchctl unload ~/Library/LaunchAgents/com.bryan.giantmem-db-backup.plist
rm ~/Library/LaunchAgents/com.bryan.giantmem-db-backup.plist
```

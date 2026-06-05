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

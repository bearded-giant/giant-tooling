# Install

One command:

```bash
make bootstrap
```

That builds the CLI + GUI, registers the daemon LaunchAgent, installs a 5-min session-ingest LaunchAgent, and runs an initial backfill. Run `make help` to see piecemeal targets.

After `make bootstrap`, install the **writer hooks** from the [claude-code-config](https://github.com/bearded-giant/claude-code-config) repo. Without them `live_docs` only captures out-of-band edits caught by the daemon's backfill — Claude PostToolUse writes (the high-signal stream) won't land.

---

## Prereqs

| Tool | Purpose | Install |
|---|---|---|
| `go` 1.21+ | build the CLI + GUI backend | https://go.dev/dl/ |
| `node` 18+ | build the GUI frontend | `brew install node` |
| `wails` v2 | macOS app bundler | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |
| `sqlite3` | DB CLI (already system-installed on macOS) | — |

`~/.local/bin` must be on `$PATH`. Add to your shell rc if not:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

## What `make bootstrap` does

1. **check-prereqs** — verifies the four tools above.
2. **cli** — `make -C giantmem install` → `~/.local/bin/giantmem`.
3. **gui** — `make -C giantmem/gui install` → `/Applications/Giantmem.app`.
4. **daemon-install** — `giantmem daemon install` → registers `~/Library/LaunchAgents/...giantmemd.plist`, starts the daemon. Daemon runs the artifacts-projection reconciler + serves the `embed` RPC + does a startup filesystem backfill.
5. **session-sweep** — installs `~/Library/LaunchAgents/com.giantmem-session-sweep.plist` (5-min `giantmem ingest --sessions-only`). Path-rewritten from the bundled template so it works for any user.
6. **first-run** — `giantmem index backfill` (fills `live.db` from every `.giantmem/` under `$GIANTMEM_DEV_ROOTS`) plus `giantmem ingest --sessions-only` (one-shot session sweep).

## Writer hooks (separate)

The hooks live in [claude-code-config](https://github.com/bearded-giant/claude-code-config) and are installed via `stow` to `~/.claude`:

| Hook | Trigger | Effect |
|---|---|---|
| `live_index.py` | PostToolUse (Write/Edit/MultiEdit on `.md`) | writes row to `live.db.live_docs` |
| `session_end_ingest.py` | SessionEnd | runs `giantmem ingest --sessions-only` for that session |

Setup:

```bash
git clone https://github.com/bearded-giant/claude-code-config ~/dev/claude-code-config
cd ~/dev/claude-code-config
stow -t ~/.claude -d . claude   # or whatever the repo's stow target is
```

## Env vars (all optional)

| Var | Default | Purpose |
|---|---|---|
| `GIANTMEM_ARCHIVE_BASE` | `~/giantmem_archive` | location of `live.db` + `archives.db` |
| `GIANTMEM_DEV_ROOTS` | `~/dev` | colon-sep list of repo roots scanned by `index backfill` |
| `GIANTMEM_EMBED_BACKEND` | unset (no embeddings) | `python` / `ollama` for hybrid-search vectors |
| `GIANTMEM_NO_DAEMON` | unset | force CLI to bypass `giantmemd` and open DBs directly |

## Verify

```bash
giantmem doctor                  # health check
giantmem daemon status           # daemon should say "running"
launchctl list | grep giantmem   # should list giantmemd + session-sweep
open /Applications/Giantmem.app  # GUI launches; activity tab populated
```

## Uninstall

```bash
make uninstall-session-sweep        # remove the 5-min agent
giantmem daemon uninstall           # remove giantmemd LaunchAgent
rm ~/.local/bin/giantmem
rm -rf /Applications/Giantmem.app
# live.db / archives.db are untouched — back them up before removing
# ~/giantmem_archive yourself if you don't want the data.
```

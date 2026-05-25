# Scoped memory — giant-tooling surfaces

What this repo ships from the scoped-memory feature. Focuses on giantmem CLI, MCP, scripts, and embedder daemon. Mental model + session-hook wiring + `/review-memory` live on the claude-code-config side: see [`scoped-memory-guide.md`](../../claude-code-config/docs/scoped-memory-guide.md) and [`scoped-memory-overview.md`](../../claude-code-config/docs/scoped-memory-overview.md).

## What landed

Three phases, all merged to `main`.

| Phase | Concept | Tables added | Subcommands |
|---|---|---|---|
| 1 | scopes, lifecycle, access log | `scopes`, `artifact_access` (live.db v3) | `scope`, `access` |
| 2 | sqlite-vec embeddings + hybrid scoring | `artifact_embeddings` (vec0), `artifact_embedding_meta` (live.db v4) | `embed`, `artifact search` |
| 3 | watcher + TF-IDF + entity promotion | none | `watch`, `suggest-domain`, `entity` |

## CLI

```
giantmem scope init                                    # seed ~/.giantmem-global/scopes.yaml
giantmem scope list|show <id>|add-repo <id> <repo>...|sync

giantmem artifact list --scope <id> --lifecycle <stage>
giantmem artifact show <id>
giantmem artifact stale --days 0                       # tier policy (A=never, B=180d, C=90d)
giantmem artifact reindex
giantmem artifact orphans

giantmem access top --limit N                          # most-accessed last 30d
giantmem access prune --older-than <dur>               # 180d / 6h / 1d12h syntax

giantmem embed --backfill [--reset] [--scope X] [--repo Y] [--backend stub|python|ollama] [--limit N]
giantmem artifact search <query> [--scope X] [--lifecycle Y] [-t TYPE] [--limit N]

giantmem suggest-domain [text]                         # TF-IDF over source-spec corpus; reads stdin
giantmem entity list|show <path-or-basename> [--repo current|all|<name>]

giantmem watch start|stop|status                       # fork/manage fsnotify daemon
giantmem watch run                                     # foreground (internal; used by start)
giantmem watch install                                 # macOS launchd LaunchAgent
```

All artifact subcommands accept `--scope` + `--lifecycle` filters now. `--days 0` on `stale` switches from a fixed cutoff to per-type retention tier policy.

## MCP

New tools registered by `giantmem mcp serve`:

| Tool | Returns |
|---|---|
| `get_stats(scope, repo, feature)` | counts by `type`/`lifecycle`/`status`/`repo`, `recent_writes_24h`, `recent_accesses_24h`, `top_accessed[5]` |
| `find_entity(name, repo)` | one Entity + back-references to mentioning artifacts |
| `find_artifact(..., scope, lifecycle, semantic)` | existing tool, three new args |

`find_artifact` with `semantic=true` routes through the same hybrid scorer as the CLI `artifact search`.

## Storage

Pure-Go SQLite + sqlite-vec via blank import `_ "modernc.org/sqlite/vec"` in `internal/db/db.go`. No CGO. Migrations register in `internal/db/migrations.go`; runs on every `db.Open`.

| Table | Purpose |
|---|---|
| `scopes` | cache of `~/.giantmem-global/scopes.yaml` (rebuilt by `scope sync`) |
| `artifact_access` | `(artifact_id, query, rank, accessed_at)` — one row per list/show/find result |
| `artifact_embeddings` | vec0 virtual table, `embedding FLOAT[$GIANTMEM_EMBED_DIM]` (default 768) |
| `artifact_embedding_meta` | `(artifact_id, rowid, body_hash, dim, model, updated_at)` — gates re-embed |

## Embedder backends

Three pluggable backends behind `internal/search/embedding.go`:

| Backend | Runtime dep | Use when |
|---|---|---|
| `stub` (default) | none | Tests + CI. Deterministic hash-based vectors. NOT semantic. |
| `python` | `python3` + `sentence-transformers` in PATH | Real semantic ranking. Long-running subprocess speaking JSON. |
| `ollama` | Ollama daemon on `OLLAMA_HOST` (default `http://127.0.0.1:11434`) | Already running Ollama. HTTP per call. |

Python backend spawns `workspace/scripts/embed.py` (sentence-transformers, default model `BAAI/bge-base-en-v1.5`). First call cold-starts ~3s; subsequent calls hit the warm daemon. Body-hash gating means backfill is idempotent — only changed artifacts re-embed.

## Hybrid scoring

`internal/search/hybrid.go` blends four signals with env-tunable weights (must sum to 1.0):

```
GIANTMEM_HYBRID_FTS_WEIGHT=0.5
GIANTMEM_HYBRID_VEC_WEIGHT=0.25
GIANTMEM_HYBRID_RECENCY_WEIGHT=0.15
GIANTMEM_HYBRID_ACCESS_WEIGHT=0.1
```

- **FTS**: substring hit on id/feature/domain/name (score 1.0 on hit)
- **Vector**: `1 / (1 + distance)` from sqlite-vec KNN
- **Recency**: `exp(-ageDays / 60)` (~60d half-life)
- **Access**: normalized 30-day count vs max in candidate set

Default behavior unchanged: `giantmem find` and `giantmem artifact list` stay FTS-only. Hybrid is opt-in via `giantmem artifact search` or MCP `semantic=true`.

## Watcher daemon

`giantmem watch start` forks an fsnotify watcher across `$GIANTMEM_DEV_ROOTS` (or `~/dev`). Per-workspace debounce 2s; coalesced edits trigger one `giantmem artifact reindex` against the owning worktree. Default excludes: `node_modules`, `.venv`, `.git`, `dist`, `build`, `.next`, `.turbo`, `target`, `vendor`. PID at `~/.cache/giantmem/giantmem-watch.pid`, log at `…/giantmem-watch.log`. Stale pidfile detected and cleaned on next `start`. SIGTERM cleans up cleanly. Auto-reindex writes do NOT log to `artifact_access`.

`giantmem watch install` writes `~/Library/LaunchAgents/com.giantmem.watch.plist` and launchctl-loads it (macOS only).

## Scripts (`workspace/scripts/`)

| Script | Purpose |
|---|---|
| `backfill_lifecycle.py` | Walks `.giantmem/` artifacts, stamps `lifecycle: durable` (or `candidate` for research/discoveries). Idempotent. `--all-repos` scans every workspace under `$GIANTMEM_DEV_ROOTS`. Skips registry JSON like `features.json`/`artifacts.json`. |
| `embed.py` | Long-running sentence-transformers daemon. Reads `{"text":"..."}` JSON lines from stdin; emits `{"vec":[...]}` responses. First line is `{"ready":true}` handshake. |

## Env knobs (full list)

```
GIANTMEM_SCOPES_PATH              override ~/.giantmem-global/scopes.yaml
GIANTMEM_EMBED_BACKEND={stub,python,ollama}
GIANTMEM_EMBED_MODEL              default BAAI/bge-base-en-v1.5
GIANTMEM_EMBED_DIM                default 768
GIANTMEM_EMBED_SCRIPT             path to embed.py
GIANTMEM_HYBRID_FTS_WEIGHT        default 0.5
GIANTMEM_HYBRID_VEC_WEIGHT        default 0.25
GIANTMEM_HYBRID_RECENCY_WEIGHT    default 0.15
GIANTMEM_HYBRID_ACCESS_WEIGHT     default 0.1
GIANTMEM_DEV_ROOTS                colon-separated; watcher + cross-repo crawl root list
OLLAMA_HOST                       default http://127.0.0.1:11434
```

## Bootstrap

```bash
cd ~/dev/giant-tooling/giantmem && make install
giantmem scope init                                          # seed registry
giantmem scope add-repo personal dotfiles giant-tooling      # add repos
python3 ~/dev/giant-tooling/workspace/scripts/backfill_lifecycle.py --all-repos
giantmem watch start                                         # auto-reindex
GIANTMEM_EMBED_BACKEND=python giantmem embed --backfill      # real semantic (opt-in)
```

## Cross-ref

| Topic | Location |
|---|---|
| Mental model + narrative | `claude-code-config/docs/scoped-memory-guide.md` |
| One-page overview | `claude-code-config/docs/scoped-memory-overview.md` |
| `/review-memory` slash command | `claude-code-config/commands/review-memory.md` |
| `preload_packs.yaml` session-hook layers | `claude-code-config/config/preload_packs.yaml` |
| Session-hook refactor | `workspace/workspace_session_hook.py` (this repo) |
| Backend decision rationale | (was at `claude-code-config/.giantmem/features/scoped-memory/research/sqlite_vec_decision.md`; .giantmem/ is gitignored) |

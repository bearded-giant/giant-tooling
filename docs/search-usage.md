# Search usage

How to search across the giantmem indexes. One CLI verb (`giantmem`) covers four corpora:

| Corpus | DB | What's in it | How it gets there |
|---|---|---|---|
| **archive** | `~/giantmem_archive/archives.db` | Archived `.giantmem/` markdown, Claude JSONL transcripts | `giantmem archive run` (workspace), session-end hook (sessions), explicit `giantmem ingest` |
| **live** | `~/giantmem_archive/live.db` | Current `.giantmem/` content across every workspace | `live_index.py` PostToolUse hook, `giantmem index live` (full rescan) |
| **artifacts** | `<workspace>/artifacts.json` | Typed artifact index (proposal/delta-spec/tasks/...) | `giantmem artifact reindex` (or `giantmem watch start` for auto) |
| **access log** | `live.db.artifact_access` | Per-call list/show/find rows. Feeds hybrid ranking. | every `artifact list|show|search` and MCP `find_artifact` call |

Indexing is mostly automatic. Hooks handle live + sessions. `archive run` indexes archived snapshots. Re-run `giantmem ingest` only for full rebuilds.

## `giantmem find` — content FTS5

```bash
giantmem find "jwt refresh"

# corpus selectors
giantmem find "jwt refresh" --live          # current .giantmem/ only
giantmem find "jwt refresh" --archive       # archives.db only
giantmem find "jwt refresh" -s session      # session transcripts only (archive)
giantmem find "jwt refresh" -s workspace

# filters
giantmem find "jwt refresh" -p cc-wt        # project
giantmem find "workspace hook" --topic workspace
giantmem find "acceptance criteria" -t features    # archive dir_type
giantmem find "migration plan" -l                  # latest archives only

# session-content drilldowns
giantmem find "jwt refresh" --tool Write,Edit       # only lines where Claude used these tools
giantmem find "jwt refresh" --ext md,go             # only matches touching these extensions

# output shape
giantmem find "jwt refresh" --no-interactive       # disable fzf
giantmem find "jwt refresh" --paths                # filepath only
giantmem find "jwt refresh" --full                 # include snippet
giantmem find "jwt refresh" -n 5                   # cap N

# time window (sessions / archives)
giantmem find "jwt refresh" --since 7d
giantmem find "jwt refresh" --until 1d
```

## `giantmem artifact` — typed artifact query

Separate from content search. Queries `artifacts.json` by frontmatter (type, status, feature, domain, repo, branch, scope, lifecycle). Cross-repo with `--repo all`.

```bash
giantmem artifact list                              # current workspace
giantmem artifact list -t delta-spec -s ready
giantmem artifact list --repo all -t proposal
giantmem artifact list --scope personal --lifecycle durable
giantmem artifact list --lifecycle candidate        # pending /review-memory items
giantmem artifact show <id>
giantmem artifact stale --days 0                    # tier policy (A=never, B=180d, C=90d)
giantmem artifact orphans                           # files missing frontmatter
giantmem artifact reindex                           # rebuild artifacts.json
```

## `giantmem artifact search` — hybrid (FTS + vector + recency + access)

Opt-in semantic search. Requires `giantmem embed --backfill` to populate `artifact_embeddings`. Default backend = `stub` (deterministic, NOT semantic); switch to `python` or `ollama` via `GIANTMEM_EMBED_BACKEND`.

```bash
giantmem embed --backfill --backend stub            # one-time, fast, NOT semantic
GIANTMEM_EMBED_BACKEND=python giantmem embed --backfill
giantmem artifact search "scope yaml registry"
giantmem artifact search "auth flow" -t proposal --limit 5
giantmem artifact search "lifecycle" --json
```

Weights env-tunable, sum-to-1.0:

```
GIANTMEM_HYBRID_FTS_WEIGHT=0.5
GIANTMEM_HYBRID_VEC_WEIGHT=0.25
GIANTMEM_HYBRID_RECENCY_WEIGHT=0.15
GIANTMEM_HYBRID_ACCESS_WEIGHT=0.1
```

Full backend / model details in [scoped-memory.md](scoped-memory.md).

## Ingest

```bash
giantmem ingest                                     # all enabled sources from ~/.config/giantmem/sources.toml
giantmem ingest -s workspace-md
giantmem ingest -s claude-jsonl
giantmem index init                                 # bootstrap dbs
giantmem index live                                 # full rescan of live.db
giantmem index sessions                             # backfill sessions
```

## Stats + access

```bash
giantmem stats                                      # counts by project / source / dir_type
giantmem access top --limit 10                      # most-accessed artifacts last 30d
giantmem access prune --older-than 180d             # trim artifact_access
```

## fzf integration (`gma`)

`gma` is a shell wrapper over `giantmem artifact list` with fzf preview. Default `--repo all`. Pipes selection into `$EDITOR` or prints `path:line`.

```bash
gma                                                 # fzf picker, all repos
gma --scope personal -t delta-spec
gma --lifecycle candidate
```

## MCP tools

After `giantmem mcp serve` (or daemon `mcp install`), agents call:

| Tool | Purpose |
|---|---|
| `search_archive` | Content FTS over `archives.db` (sessions + workspace). Mirrors `giantmem find --archive`. Args: `query`, `project`, `source_type`, `topic`, `tool_filter`, `ext_filter`, `include_read`, `limit`. |
| `find_artifact` | Typed artifact lookup. Args: `type`, `status`, `feature`, `domain`, `repo`, `branch`, `scope`, `lifecycle`, `query`, `semantic`, `limit`. `semantic=true` routes through hybrid scorer. |
| `get_artifact` | Full body + frontmatter for one artifact id. |
| `list_features_with_artifacts` | Group artifacts by feature across one or all repos. |
| `get_stats` | Counts by type/lifecycle/status/repo + `recent_writes_24h`, `recent_accesses_24h`, `top_accessed[5]`. |
| `feature_status`, `workspace_tree`, `recent_writes`, `list_sessions`, `get_session_summary` | other lookups |

All MCP tools log artifact accesses to `artifact_access` so the hybrid scorer learns from agent activity.

## FTS5 query syntax

Used by `giantmem find` and the search_archive MCP tool.

```
jwt refresh             both terms (implicit AND)
"jwt refresh"           exact phrase
jwt OR refresh          either term
jwt NOT cookie          exclude term
jwt*                    prefix match
```

Punctuation queries (`hub-and-spoke`) are auto-quoted by the CLI / MCP layer.

## Auto-reindex

```bash
giantmem watch start                                # fork fsnotify daemon
giantmem watch status
```

Watches `$GIANTMEM_DEV_ROOTS` (or `~/dev`). 2s debounce per workspace. Edits to `.giantmem/**` trigger `giantmem artifact reindex` against the owning worktree. Auto-reindex writes do NOT log to `artifact_access`.

## Architecture

```
session-end hook (workspace/workspace_session_end.py)
  └── ingests Claude JSONL → archives.db (incremental, by mtime)

PostToolUse hook (workspace/scripts/live_index.py)
  └── upserts .giantmem/** edits → live.db

giantmem watch (fsnotify)
  └── debounce 2s per workspace → giantmem artifact reindex → artifacts.json

giantmem archive run
  └── mv .giantmem → archive tree, ingest, re-init

giantmem find
  └── reads live.db + archives.db, BM25 + temporal decay, merged ranking

giantmem artifact (list/show/stale/...)
  └── reads artifacts.json + scope registry, writes artifact_access

giantmem artifact search
  └── reads artifact_embeddings (vec0) + artifact_access + artifacts.json,
      blends FTS + vector + recency + access via internal/search/hybrid.go

giantmem mcp serve
  └── exposes all of the above as MCP tools for Claude
```

## Available session topics (assigned by ingest)

`auth`, `api`, `database`, `test`, `bug`, `feature`, `refactor`, `config`, `docs`, `perf`, `ui`, `deploy`, `workspace`, `general`

## See also

- [scoped-memory.md](scoped-memory.md) — scope / lifecycle / hybrid / watch / entities (the bulk of new search-adjacent features)
- [../giantmem/README.md](../giantmem/README.md) — full capability list
- [../giantmem/USAGE.md](../giantmem/USAGE.md) — daily-driver cheat sheet

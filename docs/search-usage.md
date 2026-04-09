# Unified Archive Search -- Usage

## What gets searched

One index (`~/giantmem_archive/archives.db`) covers three source types:

| Source | What's in it | How it gets there |
|--------|-------------|-------------------|
| **workspace** | Archived `.giantmem/` markdown (plans, research, reviews, features, history) | Auto-indexed when you run `gmq archive` |
| **session** | Claude JSONL transcripts -- user prompts, assistant text, files touched, bash commands | Auto-indexed at session end (hook) |
| **domain** | Domain JSON explorations from `/plan-feature` | Auto-indexed with workspace archives |

**Indexing is automatic.** Sessions index at the end of every Claude session via `workspace_session_end.py`. Workspace archives index when you run `gmq archive`. You only need to run `gmq ingest` manually for a full rebuild or to catch up after the initial migration.

## CLI (`gmq`)

```bash
# search everything
gmq search "jwt refresh"

# filter by source type
gmq search "jwt refresh" -s session
gmq search "jwt refresh" -s workspace
gmq search "jwt refresh" -s domain

# filter by project
gmq search "jwt refresh" -p cc-wt

# filter by session topic
gmq search "workspace hook" --topic workspace
gmq search "auth flow" --topic auth

# filter by dir_type (workspace only)
gmq search "acceptance criteria" -t features

# latest archives only (workspace)
gmq search "migration plan" -l

# plain output (no fzf picker)
gmq search "jwt refresh" --no-fzf

# filepath only (for scripting)
gmq search "jwt refresh" --file-name

# show snippets inline
gmq search "jwt refresh" --full

# limit results
gmq search "jwt refresh" -n 5
```

### Ingest commands

```bash
gmq ingest                        # full rebuild: workspaces + sessions
gmq ingest -p cc-wt               # one project (workspace archive only)
gmq ingest --sessions-only        # re-index all sessions (incremental by mtime)
gmq ingest --workspaces-only      # re-index workspace archives only
gmq ingest --sessions-only --force  # force full session rebuild (ignore mtime)
```

### Stats

```bash
gmq stats
# shows: total docs, breakdown by source, project, dir_type, topic
```

## MCP tool (`search_archive`)

Available as a Claude Code tool after restart. Same search, callable by agents.

```
Tool: search_archive
Params:
  query: str (required)     -- FTS5 query (AND, OR, NOT, "phrases", prefix*)
  project: str (optional)   -- filter by project name
  source_type: str (optional) -- "workspace" | "session" | "domain"
  topic: str (optional)     -- session topic filter
  limit: int (default 10)   -- max results

Returns JSON:
  {
    "results": [
      {
        "filepath": "/path/to/file",
        "project": "cc-wt",
        "source_type": "session",
        "dir_type": null,
        "topic": "auth",
        "session_id": "abc123...",
        "timestamp": "20260405_165643",
        "score": 5.71,
        "snippet": "...matched text with >>>highlights<<<..."
      }
    ],
    "total": 3
  }
```

## Available session topics

auth, api, database, test, bug, feature, refactor, config, docs, perf, ui, deploy, workspace, general

## FTS5 query syntax

```
jwt refresh            -- both terms (implicit AND)
"jwt refresh"          -- exact phrase
jwt OR refresh         -- either term
jwt NOT cookie         -- exclude term
jwt*                   -- prefix match
```

## Architecture

```
session end hook (workspace_session_end.py)
  └── fires: gmq ingest --sessions-only (background, incremental)

gmq archive
  └── fires: gmq ingest --project {name} (background)

archives.db (FTS5)
  ├── documents table (source_type, session_id, topic, dir_type, etc.)
  └── documents_fts virtual table (porter stemming + unicode)

gmq search / search_archive MCP
  └── reads archives.db, applies BM25 + temporal decay
```

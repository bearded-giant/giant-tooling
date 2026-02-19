# domains CLI

Standalone tool for browsing and searching the code domain knowledge base. Works outside of Claude Code -- use it from any terminal for your own research, colleague conversations, or session prep.

Domain JSONs are structured explorations of code areas (auth layer, payment flow, merchant API, etc.) created by `/plan-feature` inside Claude Code. They live in `.giantmem/domains/` and get archived into SQLite FTS5 by `giantmem-archive`.


## Commands

### list -- what's been mapped

Show all domains in the current workspace.

```bash
# basic table
domains list

# output:
# repo: customcheckout
# domains: 3 indexed
#
# +------------------+--------------------------------------------+--------------------------+------------------+
# | Domain           | Description                                | Explored                 | Features         |
# +------------------+--------------------------------------------+--------------------------+------------------+
# | auth_session     | Authentication and session management layer | 2026-02-16 (today)       | jwt-enforcement  |
# | merchant_api     | Merchant-facing API endpoints              | 2026-02-14 (2 days ago)  | merchant-settings|
# | payment_flow     | Payment processing pipeline                | 2026-02-02 (14 days ago) [STALE] | checkout  |
# +------------------+--------------------------------------------+--------------------------+------------------+

# aliases
domains ls
```

### show -- deep dive on one domain

Pretty-print the full contents of a domain JSON with colored output.

```bash
domains show auth_session

# output:
# auth_session - Authentication and session management layer
# explored: 2026-02-16 (today)
# features: jwt-session-cookie, jwt-session-enforcement
#
# Entry Points
#   src/api/auth/session_resource.py
#     type: api_endpoint
#     description: REST endpoint for session operations
#
# Key Files
#   src/services/auth_session/session_store.py
#     purpose: Redis-backed session storage
#     exports: get_session, create_session, invalidate_session
#     patterns: repository pattern
#     dependencies: redis_client, config.JWT_SESSION_SECRET
#
# Architecture
#   layers: API resource, service, Redis store
#   data_flow: request -> auth middleware -> session_store -> Redis
#   patterns: repository pattern, middleware chain
#   key_decisions: sessions in Redis with TTL, session ID is opaque token
#
# Data Models
#   cache_keys: rc_session_{user_id}_{store_id}_{session_id}
#
# Dependencies
#   internal: merchant_auth, jwt_service
#   external: redis, pyjwt
#
# Gotchas
#   - SQLAlchemy session isolation causes stale cache in tests
#   - Redis SCAN needed for lookup by session_id

# aliases
domains cat auth_session
```

### search -- find across live workspace domains

Search all domain JSONs in the current workspace for a keyword. Searches file paths, function names, patterns, gotchas -- everything.

```bash
# find which domains mention a file
domains search "session_store"

# output:
# search: "session_store"
#
# auth_session (.giantmem/domains/auth_session.json)
#   Authentication and session management layer
#   key_files: src/services/auth_session/session_store.py: Redis-backed session storage
#   architecture.data_flow: request -> auth middleware -> session_store -> Redis
#
# 1 domain(s) matched

# find which domains use redis
domains search "redis"

# find a specific pattern
domains search "repository pattern"

# narrow to a section
domains search "redis" --section gotchas

# aliases
domains s "redis"
domains grep "redis"
```

### archive -- search across ALL projects and history

Hits the SQLite FTS5 database from giantmem-archive. Searches archived domain JSONs from every project and branch you've ever archived. This is where it gets powerful -- cross-project domain knowledge.

```bash
# basic archive search
domains archive "shopify_sessions"

# output:
# archive search: "shopify_sessions" (2 domain file(s) matched)
#
# auth_session  [cc-wt/main]  Authentication and session management layer
#   data_models.cache_keys: rc_session_{user_id}_{store_id}_{session_id}
#   key_files: src/services/auth_session/session_store.py: Redis-backed session storage
#
# session_management  [edgerouter/stage]  Shopify session handling for app proxy
#   key_files: src/sessions/shopify_sessions.py: Session token validation
#   architecture.data_flow: app proxy -> session middleware -> shopify API
#
# use --show for full domain details

# full details with file paths and scores
domains archive "shopify_sessions" --show

# filter to one project
domains archive "redis" -p cc-wt

# latest archives only (skip old snapshots)
domains archive "middleware" -l

# combine filters
domains archive "jwt" -p cc-wt -l -n 5

# aliases
domains a "jwt"
domains arc "jwt"
```

### export -- shareable markdown

Dump a domain as clean markdown. Share with colleagues, paste into docs, or use as LLM context.

```bash
# print to stdout
domains export auth_session

# write to file
domains export auth_session -o auth_session.md

# export and share
domains export auth_session -o /tmp/auth_session.md && open /tmp/auth_session.md
```

The markdown output:

```markdown
# auth_session

Authentication and session management layer

explored: 2026-02-16
features: jwt-session-cookie, jwt-session-enforcement

## Entry Points

| Path | Type | Description |
|------|------|-------------|
| `src/api/auth/session_resource.py` | api_endpoint | REST endpoint for session operations |

## Key Files

**`src/services/auth_session/session_store.py`** - Redis-backed session storage
  exports: `get_session`, `create_session`, `invalidate_session`
  patterns: repository pattern
  depends on: redis_client, config.JWT_SESSION_SECRET

## Architecture

**layers:** API resource, service, Redis store
**data_flow:** request -> auth middleware -> session_store -> Redis

## Gotchas

- SQLAlchemy session isolation causes stale cache in tests
- Redis SCAN needed for lookup by session_id
```

### fzf -- interactive picker

Browse domains with fzf. Preview pane shows full domain contents. Select one to pretty-print it.

```bash
domains fzf
# opens fzf with domain list, preview pane on the right
# navigate with arrows, type to filter, enter to select
```


## Real-world workflows

### Starting a new session

Before diving into code, check what's been mapped:

```bash
domains list                    # what domains exist?
domains show auth_session       # refresh memory on the auth layer
```

### "How does X work?"

Colleague asks how sessions work. Don't dig through code -- search the knowledge base:

```bash
domains search "session"        # check live workspace
domains archive "session" -l    # or search all projects
domains export auth_session -o /tmp/sessions.md  # share the markdown
```

### Prepping context for Claude Code

Before starting a Claude session on auth work, find and note the relevant domain files:

```bash
domains search "auth"
# -> auth_session (.giantmem/domains/auth_session.json)

# then in Claude Code:
# "read .giantmem/domains/auth_session.json and help me add session rotation"
```

### Cross-project research

"Did we solve this pattern before in another project?"

```bash
domains archive "rate limiting"
domains archive "webhook" -p edgerouter --show
domains archive "caching" --show
```

### Checking for stale knowledge

```bash
domains list  # look for [STALE] markers
# then in Claude Code: /update-domains --all-stale
```


## Options

| Flag | Scope | Description |
|------|-------|-------------|
| `-p, --path PATH` | global | Override .giantmem/domains/ location |
| `--section SECTION` | search | Restrict to section: key_files, architecture, gotchas, etc |
| `-p, --project NAME` | archive | Filter to a specific project |
| `-l, --latest` | archive | Search only latest archived snapshots |
| `-n N` | archive | Max results (default: 20) |
| `--show` | archive | Show full structured domain details |
| `-o, --output FILE` | export | Write markdown to file instead of stdout |


## How domains get created

1. `/plan-feature` in Claude Code explores code areas and writes domain JSONs to `.giantmem/domains/`
2. `/update-domains` refreshes them after code changes
3. `/complete-feature` auto-refreshes domains whose files were modified
4. `giantmem-archive archive` copies them to `~/giantmem_archive/` and indexes into SQLite FTS5
5. `domains archive` searches that FTS5 index with structured output


## Setup

The script auto-detects `.giantmem/domains/` by walking up from cwd. For archive search, it reads `~/giantmem_archive/archives.db` (set `GIANTMEM_ARCHIVE_BASE` to override).

```bash
# make sure it's on your PATH (already in scripts/)
which domains

# or run directly
python3 ~/dev/claude-code-config/scripts/domains list
```

# giantmem roadmap

This is the working roadmap for everything beyond the v2 cut. Items are grouped by tier. Each item has enough detail that future-me (or an agent) can pick it up cold.

Build order: Tier 1 top-to-bottom, then Tier 2, then Tier 3 as appetite allows. Mark items done in the status table.

## Status

| Phase | What | Done |
|-------|------|------|
| 1 | Cobra skeleton, `find`, `stats`, `version` | ✓ |
| 2 | live.db + PostToolUse hook + cross-DB find | ✓ |
| 3 | sessions list/show/find/resume + cwd backfill | ✓ |
| 4 | `archive`, `worktree`, `workspace` consolidation | ✓ |
| 5 | port Python ingester, MCP server, delete giantmem-archive dir | ✓ |
| 6 | Tier 1 (doctor, MCP tools, prime, cd) | ✓ |
| 7 | Tier 2 (status, tail, capture, since/until, interactive) | ✓ |
| 8 | Tier 3 | pending |

## Locked design decisions

These resolve the open questions from the brainstorm. Not up for debate during implementation; if a build issue forces a revisit, document it here.

1. **SessionStart prime injects as a system-reminder, not visible chat.** Doesn't pollute the user's screen. Claude sees it; user doesn't.
2. **MCP tool surface = multiple narrow tools.** `search_archive`, `list_sessions`, `get_session_summary`, `recent_writes`, `feature_status`, `workspace_tree`. Better Claude reasoning than one fat `query` tool.
3. **`giantmem doctor` ships read-only first.** `--fix` is a follow-up after the diagnostics are battle-tested.

---

# Tier 1 — biggest seamless wins

Build in this order. Each item is independent of the others; no blocking dependencies between them.

## 1.1 `giantmem doctor`

One command, full health report. Replaces five manual checks.

**Subcommand:** `giantmem doctor [--json] [--root PATH...]`

**Checks:**

| Check | Severity | Detection |
|-------|----------|-----------|
| Orphan worktrees | warn | git worktree list says X exists, fs has no dir |
| Orphan `.giantmem/` | warn | `.giantmem/` on disk in a path with no `.git` (worktree removed without archive) |
| Stale workspaces | info | newest md older than 30 days (configurable) |
| Broken `latest` symlinks | error | `~/giantmem_archive/<proj>/latest` points to missing dir |
| archives.db drift | warn | live_docs paths exist on disk but archive run hasn't ingested them; or sessions on disk newer than indexed_at |
| Missing features.json | info | `.giantmem/features/` exists but no features.json or _index.md |
| Hook not installed | error | `~/.claude/settings.json` PostToolUse missing live_index.py |
| MCP entry stale | error | `~/.claude/settings.json` MCP `giantmem-search` doesn't point at `giantmem mcp serve` |
| Live DB corruption | error | `PRAGMA integrity_check` on live.db / archives.db |

**Output:** grouped by severity, file paths + actions printed for each finding. `--json` returns a structured report.

**Files:** `cmd/doctor.go`, `internal/health/checks.go`. ~300 LOC.

**Acceptance:** `giantmem doctor` runs clean on a healthy machine. Deliberate breakage in each category gets caught and printed with a specific remediation hint.

---

## 1.2 More MCP tools

Today's surface = one tool (`search_archive`). Add five more so Claude can self-discover state instead of asking.

**Tools to add** (`cmd/mcp.go` plus helpers in `internal/mcptools/`):

| Tool | Args | Returns |
|------|------|---------|
| `list_sessions` | project?, limit? | recent sessions with id, project, cwd, topic, ts |
| `get_session_summary` | id | metadata + first user message + file writes count + bash count + topic |
| `recent_writes` | project?, since? (default 24h) | live_docs rows newer than `since`, sorted desc |
| `feature_status` | project? | features.json contents per project (which feature `in_progress`, which complete) |
| `workspace_tree` | path? | listing of `.giantmem/` subdirs + file counts per dir_type |

Each is a thin wrapper around existing SQL. Total ~250 LOC. Reuses the mcp-go scaffolding from `cmd/mcp.go`.

**Acceptance:** all six tools listed by `tools/list`. Each callable with smoke JSON-RPC. Claude can chain: `recent_writes` → `get_session_summary` → `search_archive` for "what did I do this week."

---

## 1.3 SessionStart prime hook

When Claude starts in a worktree, auto-inject context so the first message has state.

**Components:**

1. `giantmem prime --json` (new subcommand, `cmd/prime.go`). Reads cwd, emits:
   - active feature name (from features.json)
   - 3 most recent live_docs for this project
   - 2 most recent sessions for this project
   - last 5 entries from `.giantmem/history/sessions.md` if present
2. `~/.claude/hooks/session_prime.py` — SessionStart hook. Calls `giantmem prime --json` for `CLAUDE_PROJECT_DIR`. Wraps result in `<system-reminder>...</system-reminder>` and prints to stdout (Claude reads stdout from SessionStart hooks as injected context).
3. Wire into `~/.claude/settings.json` SessionStart array (additive — keep existing memory + workspace hooks).

**Acceptance:** new Claude session in a worktree with active feature shows the active feature name in its first reasoning. Visible only to Claude, not the user's screen.

**Files:** `cmd/prime.go` (~80 LOC), `~/.claude/hooks/session_prime.py` (~50 LOC). Settings.json edit.

---

## 1.4 `giantmem cd <fuzzy>`

Fuzzy-jump to any worktree. Cuts cd-and-tab pain.

**Subcommand:** `giantmem cd <pattern>` — prints best-match worktree path to stdout. Multi-match → fzf picker (if available). No match → exit 1.

Match priority:
1. Exact worktree dir name match
2. `<project>/<branch>` substring match (e.g. `cd orch/main`)
3. Branch name match across all worktrees

**Sources:**
- `git worktree list` for every project found via `wt_projects` (parsed from registered prefixes) OR walk `~/dev` once for bare repos with `.bare` siblings (cached at `~/.cache/giantmem/worktrees.json`, refresh on `gj --refresh`).

**Shell wrapper** (printed by `giantmem worktree shell-init`):

```bash
gj() {
  local target
  target=$(giantmem cd "$@") || return 1
  cd "$target"
}
```

**Acceptance:** `gj orch/main` cd's to `~/dev/ai/chat-orchestrator-wt/main`. `gj garbage` exits 1. `gj orch` opens fzf with both worktrees.

**Files:** `cmd/cd.go` (~150 LOC). Update `cmd/worktree.go` shell-init to include `gj`.

---

# Tier 2 — daily polish

Ship in any order after Tier 1. Each is small.

## 2.1 Statusline data

`giantmem status --json` emits one-shot snapshot:

```json
{
  "active_feature": "better-search",
  "live_docs_today": 12,
  "stale_workspaces": 7,
  "current_session_id": "...",
  "last_indexed_at": "2026-04-26T13:48:56Z"
}
```

`hooks/statusline.js` (existing) shells out, caches for 30s, renders selected fields. Sub-50ms cold; near-zero with cache.

**Files:** `cmd/status.go` (~80 LOC), edit `hooks/statusline.js`.

## 2.2 `giantmem tail`

`tail -f` for live_docs. Stream new rows as the hook inserts them.

Polling implementation: `SELECT * FROM live_docs WHERE ingested_at > ? ORDER BY ingested_at` every 1s, track high-water mark. Each row prints: timestamp, project, dir_type, path, first 80 chars.

**Files:** `cmd/tail.go` (~80 LOC).

## 2.3 `giantmem capture "..."`

Quick brain-dump entry point. Appends a timestamped block to:
- `.giantmem/features/<active>/notes.md` if active feature exists in cwd
- `.giantmem/notes.md` otherwise

Format:
```
## 2026-04-26 14:33  [session: 40503b40]
<content>
```

Pipeable: `echo "idea: ..." | giantmem capture`.

**Files:** `cmd/capture.go` (~60 LOC).

## 2.4 `giantmem find --since 7d` / `--until`

Recency filter. Time-bound on `live_docs.mtime` and `documents.timestamp`. Parse `7d`, `2h`, `3w`, RFC3339.

**Files:** edit `cmd/find.go` (~30 LOC change).

## 2.5 `giantmem find -i` (interactive)

Restore the fzf+bat preview the old bash search had. Native: emit results to fzf with bat preview, on select emit path or open in `$EDITOR` (`-o`).

Soft dependency on `fzf` and `bat`; print a hint if missing.

**Files:** edit `cmd/find.go` (~80 LOC).

---

# Tier 3 — when motivated

Each is independent and individually optional. Pick by appetite.

## 3.1 `giantmem backup push`

Disaster-proof archives.db.

Implementation: copy `archives.db` to `~/giantmem_archive_backup/` (a private git repo). `git add archives.db && git commit -m "snapshot $(date +%F)" && git push`. First run requires `--init <remote-url>` to clone the repo.

`/schedule` integration for weekly. Drop a recipe in USAGE.md.

**Files:** `cmd/backup.go` (~120 LOC).

## 3.2 `giantmem session export <id>`

Clean markdown transcript suitable for sharing. Strips system reminders, redacts paths optionally, preserves user/assistant turns + tool calls in collapsed format.

**Files:** `cmd/session.go` extension (~150 LOC), `internal/sessions/export.go`.

## 3.3 `giantmem session diff <a> <b>`

Compare two sessions:
- file set: what each touched (intersection / symmetric difference)
- topic: detected topic per session
- length / duration
- shared participants (files both saw)

**Files:** `cmd/session.go` extension (~100 LOC).

## 3.4 PreCompact hook

`~/.claude/hooks/precompact_capture.py` writes a snapshot of current scratch state (active feature, plans/current.md tail, last 3 discoveries) into `.giantmem/history/precompact_<ts>.md` before Claude compacts. Survives compaction.

**Files:** new hook (~80 LOC). Settings.json edit.

## 3.5 SessionEnd ingest

`~/.claude/hooks/session_end_ingest.py` reads the just-ended JSONL and runs `giantmem ingest --sessions-only` for that single file. Sessions become searchable instantly instead of waiting for next ingest.

Better: extend the existing `workspace_session_end.py` to call `giantmem ingest --sessions-only` at the end of its work.

**Files:** edit existing hook (~20 LOC).

## 3.6 `giantmem timeline`

Visual terminal timeline (Unicode bars or simple text grid) of sessions + archives across projects, last N days. Eye candy more than utility.

**Files:** `cmd/timeline.go` (~200 LOC).

## 3.7 `giantmem plan list`

Aggregate `plans/current.md` across all live workspaces. Single "what am I in the middle of?" answer when juggling many repos.

**Files:** `cmd/plan.go` (~80 LOC).

---

# Beyond Tier 3 (idea bank, no commitment)

These got brought up but don't yet earn tier placement. Documented so they aren't lost.

| Idea | Why later |
|------|-----------|
| Multi-machine sync of archives.db | Need to actually own a second machine first |
| Per-project config (`.giantmem-config`) | No concrete need yet; flags + env cover it |
| `giantmem grep <regex>` | FTS5 covers 95%; only matters for regex-only queries |
| `giantmem diff` (since-last-archive) | git diff already does this |
| `giantmem feature` wrapper | Existing `/new-feature`, `/start-feature` skills cover it |
| Plan-mode integration | Plan mode is a Claude feature; not my CLI's job |

---

# Acceptance signals for Tier 1 done

When all four Tier 1 items ship:

1. New machine setup is `make install` + paste one source line into `.bashrc` + restart Claude. Period.
2. `giantmem doctor` after fresh install reports clean.
3. `gj <pattern>` works for any worktree.
4. Starting Claude in a worktree where you have prior work shows that work in Claude's reasoning without prompting.
5. Claude can answer "what did I write this week" via MCP without me typing context.

Once 1-5 hold, Tier 1 is shipped.

# giantmem roadmap (continued)

This is the second-phase plan, following [PLAN.md](PLAN.md). Phase numbering continues from there.

## Status

| Phase | What | Done |
|-------|------|------|
| 1-5 | v2 cut: rename + ingest + MCP port + dir cleanup | ✓ |
| 6-8 | Tier 1-3 from PLAN.md | ✓ |
| 9 | Hardening: staleness, dedup, hook hygiene, naming canonicalization | ✓ |
| 10 | Workflow completeness: doctor --fix, ignore file, config, resume cd, install, completions | ✓ |
| 11 | Schema versioning + migration framework | ✓ |
| 12 | Ingest plugin model | pending |
| 13 | Daemon mode (`giantmemd`) | pending |

Cross-machine sync intentionally skipped per user direction.

## Note: SQLite size growth (deferred)

`archives.db` at first ingest = ~30 MB. Live + archive growth rate ~5-10 MB/week with current usage pattern. Hard cap at year scale: ~250 MB. Tractable on local disk for now.

If/when this becomes painful (multi-GB), options to evaluate:
1. Externalize FTS index only — keep `documents` table local, push `documents_fts` to cloud-hosted SQLite (Turso/LiteFS) or a managed Postgres + tsvector
2. Tiered storage — recent (90 days) local, older shipped to S3 + a "rehydrate" subcommand
3. Drop content from archived rows after N days — keep metadata + snippet only

Decision deferred. Track DB size via `giantmem doctor` and revisit when monthly delta exceeds 100 MB.

---

# Phase 9 — hardening & staleness

## 9.1 Prune live.db on archive

When `giantmem archive run` moves `<worktree>/.giantmem/`, all `live_docs` rows under that path become orphans. They show up in `find --live` pointing at gone files.

**Fix:** in `archiver.Run`, after the rename succeeds, open `live.db` and `DELETE FROM live_docs WHERE worktree_path = ? OR path LIKE ?` keyed on the archived dir. Triggers handle FTS row cleanup.

**Acceptance:** archive a workspace, `find --live` returns 0 hits referencing the moved dir.

## 9.2 Dedup live ↔ archive

Even with 9.1, a window exists where a file is in `live.db` (just-written) and `archives.db` (just-archived). `find` (default scope) returns both rows.

**Fix:** when emitting hits, drop archive hits whose `filepath` is also in the current live result set. Cheap because we have both result sets in memory.

**Acceptance:** archive a doc immediately after writing; `find` returns one hit (live), not two.

## 9.3 MCP read-only hint

`tools/list` currently shows `destructiveHint:true, idempotentHint:false, openWorldHint:true` on `search_archive` (and the rest). Search is read-only, idempotent, and closed-world relative to local disk.

**Fix:** for each tool registered in `cmd/mcp.go` and `cmd/mcp_tools.go`, add `mcp.WithReadOnlyHintTrue()`, `mcp.WithIdempotentHintTrue()`, `mcp.WithDestructiveHintFalse()`, `mcp.WithOpenWorldHintFalse()` (or whichever the lib exposes).

**Acceptance:** `tools/list` JSON shows `readOnlyHint:true, destructiveHint:false` for all six tools.

## 9.4 Hook failure log

`live_index.py` fails silently on db lock contention. SessionEnd ingest can also fail silently.

**Fix:** wrap each hook's main in try/except that appends a single line to `~/.cache/giantmem/hook.log` with timestamp, hook name, exception. Rotate at 1 MB.

**Acceptance:** force a db-lock scenario, check the log captures it.

## 9.5 Project naming canonicalization

`dev/ai/chat-orchestrator` (sessions) vs `chat-orchestrator-wt` (archives) split is durable but inconsistent. Causes doctor to double-report and `stats` to fragment.

**Fix:** introduce `project.Canonicalize(name) string` that:
- Strips `dev/<lang>/` prefix from session-style names
- Maps to `<base>-wt` if the bare-with-worktrees layout exists on disk
- Configurable via `~/.config/giantmem/canonical.json` for hand-curated mappings

Apply in:
- `internal/ingest` when writing rows
- `cmd/find.go` and `cmd/session.go` LIKE filters

Migration: `giantmem index migrate --canonicalize` rewrites existing rows.

**Acceptance:** `stats` shows one row per project for chat-orchestrator across both source_types.

## 9.6 `capture` mkdir fix

`giantmem capture` fails if `.giantmem/features/<name>/` doesn't yet exist (rare, but real on a freshly-created feature mid-init).

**Fix:** `os.MkdirAll(filepath.Dir(target), 0o755)` before append.

**Acceptance:** `capture` works on a freshly-named feature with no `notes.md` yet.

---

# Phase 10 — workflow completeness

## 10.1 `giantmem doctor --fix`

Currently read-only. Each finding has a hint; `--fix` runs them.

**Fixers:**

| Category | Fixer |
|----------|-------|
| `symlink` (broken latest) | rebind to newest timestamp dir |
| `drift` (project not indexed) | run `ingest --project <name>` |
| `orphan` (stray .giantmem/ no .git) | offer to archive (interactive prompt unless `--auto`) |
| `worktree` (gone path in git) | run `git worktree prune` |
| `mcp` (settings entry stale) | rewrite the entry in settings.json |
| `hook` (PostToolUse missing) | inject the hook config |
| `db` (integrity errors) | print recovery hints; do not auto-rebuild |
| `stale` | leave (covered by ignore) |

Flags: `--fix` (apply), `--fix-categories=symlink,drift` (subset), `--auto` (no prompts).

**Acceptance:** `doctor --fix` clears all error-severity findings on a deliberately-broken machine.

## 10.2 `.giantmem-ignore`

Per-workspace opt-out file. Doctor and stale-scan honor it.

**Format:** `.giantmem-ignore` at workspace root, gitignore-style globs. Matches workspace-relative paths.

**Special directives:**
- `# stale-ok` — workspace can be stale; don't flag
- `# orphan-ok` — `.giantmem/` without `.git` ancestor is intentional

**Global:** `~/.config/giantmem/global-ignore` for system-wide patterns.

**Acceptance:** create `.giantmem-ignore` with `# stale-ok` in 5 dormant workspaces; doctor stops listing them.

## 10.3 `giantmem config`

Single source of truth for "what's configured."

**Output:**
```
giantmem config
  binary:        /Users/bryan/.local/bin/giantmem  (build a9d93c7)
  archive_base:  /Users/bryan/giantmem_archive  (env: GIANTMEM_ARCHIVE_BASE)
  archives.db:   /Users/bryan/giantmem_archive/archives.db  (38 MB, 1884 docs)
  live.db:       /Users/bryan/giantmem_archive/live.db  (0.1 MB, 1 doc)
  cache_dir:     /Users/bryan/.cache/giantmem
  config_file:   /Users/bryan/.config/giantmem/config.toml  (missing — using defaults)
hooks:
  PostToolUse → live_index.py  ✓ wired
  SessionStart → session_prime.py  ✓ wired
  PreCompact → precompact_capture.py  ✓ wired
  SessionEnd → session_end_ingest.py  ✓ wired
mcp:
  giantmem-search → giantmem mcp serve  ✓ registered
worktree-core:
  /Users/bryan/dev/giant-tooling/git-worktrees/worktree-core.sh  ✓ found
workspace-lib:
  /Users/bryan/dev/giant-tooling/workspace/workspace-lib.sh  ✓ found
```

`--json` for scripts. `--write-defaults` writes a `config.toml` template with all current resolved values.

## 10.4 Session resume uses `giantmem cd` matcher

Today: hardcoded fallback to `<cwd>-wt/main` then `master`. Brittle.

**Fix:** if recorded `cwd` doesn't exist, call the same matcher that powers `giantmem cd` with the basename of `cwd` as pattern. If unique match, chdir there. If multi-match, print candidates and exit.

**Acceptance:** session whose recorded cwd was `~/dev/foo` resumes correctly when project moved to `~/dev/foo-wt/main` regardless of branch name.

## 10.5 `shell-init --install`

Today: prints output, user copies. `--install` appends to `.bashrc`/`.zshrc` with sentinel.

```bash
# >>> giantmem shell-init >>>
source ~/dev/giant-tooling/git-worktrees/worktree-core.sh
gj() { ... }
# <<< giantmem shell-init <<<
```

Re-running detects existing block and updates in place. `--target ~/.zshrc` overrides. `--dry-run` prints what would change.

## 10.6 Shell completions for ids/projects

Cobra `RegisterFlagCompletionFunc` lets us provide context-aware suggestions.

| Flag | Source |
|------|--------|
| `--project` everywhere | `SELECT DISTINCT project FROM documents` ∪ `live_docs` |
| `--feature` | active features from features.json across worktrees |
| session id-prefix args | `SELECT session_id FROM documents WHERE source_type='session'` |
| project arg in `archive open/dedup` | dirs under `~/giantmem_archive/` |

**Acceptance:** `giantmem session resume 40<TAB>` completes to `40503b40`. `find -p <TAB>` lists projects.

---

# Phase 11 — schema versioning

## 11.1 user_version pragma

Both DBs gain `PRAGMA user_version = N`. Migrations are numbered Go funcs in `internal/db/migrations/`.

```go
package migrations

var Archive = []Migration{
    {Version: 1, Apply: func(d *sql.DB) error { /* original schema */ }},
    {Version: 2, Apply: func(d *sql.DB) error { /* add cwd column */ }},
    // future migrations append here
}
```

`db.Open()` reads `PRAGMA user_version`, applies all newer migrations in order, sets the new version. Idempotent.

`live.db` has its own migration list.

## 11.2 Migration safety

- All migrations run in a single transaction (per migration, not the whole stack)
- Each migration logs `applied migration <name> v<N>` to stderr
- If a migration fails, it rolls back; `db.Open` returns error; user sees clear "schema migration v3 failed: ..."
- `giantmem config` shows current schema versions for both DBs

## 11.3 Bake into existing entry points

Replace `db.EnsureArchive` and `db.EnsureLive` calls scattered across the code with a single `db.Open(path)` that auto-runs migrations to head. Move all `CREATE TABLE IF NOT EXISTS` and `ALTER TABLE` statements out of ingest paths and into versioned migrations.

**Acceptance:** delete `archives.db`, run `giantmem stats` — recreated and migrated to head. Restore from a v1 backup, run any command — auto-migrated to current.

---

# Phase 12 — ingest plugin model

## 12.1 Source registry

Today's ingest hard-codes three sources: workspace-md, claude-jsonl, domain-json. Future: Linear tickets, Slack threads, MR descriptions, browser bookmarks, etc.

**Config:** `~/.config/giantmem/sources.toml`

```toml
[[source]]
name = "workspace-md"
kind = "builtin"
enabled = true

[[source]]
name = "claude-jsonl"
kind = "builtin"
enabled = true

[[source]]
name = "linear-tickets"
kind = "external"
ingest_cmd = "linear-export --since 7d"
parse = "json"
mapping.project = ".team.key"
mapping.session_id = ".id"
mapping.timestamp = ".updatedAt"
mapping.content = ".description"
```

## 12.2 Plugin protocol

External sources are subprocesses. They emit JSONL on stdout where each line is a doc-shaped object. `giantmem ingest --source linear-tickets` runs the cmd, parses, upserts via the same code path as built-in sources.

**Doc schema (canonical):**
```json
{
  "filepath": "linear://TKT-123",
  "project": "growth",
  "source_type": "linear",
  "timestamp": "20260426_140000",
  "content": "...",
  "metadata": { "url": "...", "topic": "..." }
}
```

## 12.3 Built-ins refactored

Existing workspace/jsonl/domain ingest become plugin-style sources internally. Same code, opt-out-able from config.

## 12.4 Source-aware filters

`giantmem find -s linear`, `giantmem stats` groups by source. No code change for filters; they already use `source_type`.

---

# Phase 13 — daemon mode

## 13.1 `giantmemd`

Long-running daemon serving a unix socket. Eliminates 700ms cold start per CLI invocation.

**Wire format:** JSON-RPC 2.0 over `~/.cache/giantmem/giantmemd.sock`. One method per giantmem subcommand surface: `find`, `status`, `prime`, `cd`, etc.

## 13.2 Client routing

CLI checks for daemon socket. If present, sends RPC and renders. If absent, falls back to direct DB open. Identical output either way.

`giantmem daemon start|stop|restart|status` controls the daemon.

`giantmem daemon health` returns uptime, RSS, requests served.

## 13.3 launchd integration (macOS)

`giantmem daemon install` writes `~/Library/LaunchAgents/com.giantmem.daemon.plist` and `launchctl load`s it. Daemon survives logout, restarts on crash.

## 13.4 Hot reload

Ingest writes don't conflict with running daemon (SQLite WAL handles it). Schema migrations: daemon detects version bump on next request and either reloads itself or returns 503 with "restart pending."

## 13.5 Performance budget

- statusline call: < 5 ms (vs 700 ms today)
- `gj` cd: < 10 ms
- find with 200-row result: < 30 ms

Verified via `giantmem daemon health --benchmark`.

---

# Build order

Sequence preserves working CLI throughout.

1. **9.1, 9.2, 9.6, 9.3** — quick correctness fixes (XS each)
2. **9.4** — hook log helper, used by everything below
3. **11.1, 11.3** — schema versioning before more migrations land
4. **9.5** — project canonicalization (uses 11's migration framework)
5. **10.1 doctor --fix** — actionable maintenance
6. **10.2 .giantmem-ignore + 10.3 config** — operational hygiene
7. **10.4 resume cd, 10.5 shell-init --install, 10.6 completions** — UX polish
8. **12 plugins** — bigger refactor; sequence after 11 lands
9. **13 daemon** — last; touches every command path

# Acceptance signals for "phase 9-13 done"

1. `giantmem find` never returns dead-file hits
2. `giantmem doctor --fix` clears its own findings
3. `.giantmem-ignore` quiets known-stale workspaces
4. `giantmem config` is the single source of truth for setup state
5. `giantmem session resume <id>` works post-bare-migration without manual help
6. New shells get `gj` and source line via one `--install` command
7. `<TAB>` completes session ids and project names
8. Schema migration is automatic and observable
9. New ingest sources can be added without touching giantmem source
10. Daemon mode reduces statusline / cd latency to <10ms warm

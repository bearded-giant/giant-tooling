# Workspace Claude Code Hooks

Two Python hooks bridge `workspace-lib.sh` with Claude Code sessions. Both are stdlib only and wrap `main()` in a bare `try/except`, so a broken hook never breaks a session.

| Hook | Event | Purpose |
|------|-------|---------|
| `workspace_session_hook.py` | SessionStart | Bootstrap `.giantmem/` if missing, inject context and recent sessions |
| `workspace_session_end.py` | SessionEnd | Write a session summary file, extract discoveries and plans |

```
claude starts
     |
     v
SessionStart hook
     |
     +-- source=startup, no .giantmem/ or scratch/ --> workspace_init, then inject
     +-- otherwise --> inject only
     v
[Claude session runs]
     |
     v
SessionEnd hook
     |
     +-- read transcript JSONL
     +-- derive topic and brief
     +-- create .giantmem/history/sessions/{timestamp}_{id}.md
     +-- append one line to .giantmem/history/sessions.md
     +-- append discoveries to .giantmem/context/discoveries.md
     +-- write .giantmem/plans/current.md when steps were found
     v
session ends
```

## Wiring

Claude Code runs hooks from `~/.claude/settings.json`. In the claude-code-config repo the two hooks are chained through `hooks/dispatch.py`, which loads modules by name from `~/.claude/hooks/`:

```json
{
  "hooks": {
    "SessionStart": [{ "hooks": [{ "type": "command",
      "command": "python3 ~/.claude/hooks/dispatch.py workspace_session_hook ..." }] }],
    "SessionEnd":   [{ "hooks": [{ "type": "command",
      "command": "python3 ~/.claude/hooks/dispatch.py workspace_session_end ..." }] }]
  }
}
```

That repo carries its own copies of both hook files. This doc describes the versions in `workspace/` here, which you can also wire directly:

```json
{ "type": "command", "command": "python3 $GIANT_TOOLING_DIR/workspace/workspace_session_hook.py" }
```

The start hook finds `workspace-lib.sh` through `$GIANT_TOOLING_DIR` (default `~/dev/giant-tooling`).

## SessionStart: workspace_session_hook.py

Input on stdin:

```json
{ "session_id": "...", "cwd": "/path/to/project", "source": "startup" }
```

`source` is `startup`, `resume`, or `clear`. Bootstrap runs only on `startup`, and only when neither `.giantmem/` nor a legacy `scratch/` dir exists. Context is read and injected on every source.

Output on stdout, each section present only when its file exists:

| Section | Source | Limit |
|---------|--------|-------|
| `[Workspace bootstrapped for {name}]` | printed when `workspace_init` just ran | |
| `=== WORKSPACE CONTEXT ===` | `WORKSPACE.md` | first 2000 chars |
| `=== RECENT SESSIONS ===` | `history/sessions/*.md`, newest first | 3 files, `Topic:` and `Brief:` lines |
| `=== ACTIVE ARTIFACTS ===` | `artifacts.json` (built by `giantmem artifact reindex`) | counts by type and feature, up to 3 ready items per feature |
| `=== ACTIVE PLAN ===` | `plans/current.md` | first 1500 chars |
| `=== RECENT DISCOVERIES ===` | `context/discoveries.md` | last 20 lines |

A trailing reminder line tells Claude where to save findings and plans.

## SessionEnd: workspace_session_end.py

Input on stdin:

```json
{ "session_id": "...", "cwd": "/path/to/project", "transcript_path": "~/.claude/projects/.../session.jsonl" }
```

The hook returns early when there is no `.giantmem/` (or `scratch/`) dir, no transcript path, or no messages. Otherwise it:

1. Reads the transcript JSONL and pulls out user prompts, assistant text, tool calls with file paths, and timestamps.
2. Scores the text against the topic keyword table and picks the top topic when it clears a small threshold, else `general`.
3. Uses the first user prompt (trimmed to 80 chars) as the brief.
4. Regex-matches discoveries and plan steps from assistant text.
5. Writes the session file, appends the index line, appends discoveries, and saves plans.

### Discovery extraction

Each pattern captures the trigger word plus the next 10 to 100 characters. Matches shorter than 20 chars are dropped, longer than 200 are truncated, and at most 10 survive.

| Category | Trigger words |
|----------|---------------|
| `finding` | discovered, found, learned, realized, noticed |
| `architecture` | pattern, architecture, structure |
| `gotcha` | gotcha, caveat, watch out, careful, note that, important |
| `convention` | convention, standard, style, naming |
| `dependency` | dependency, requires, depends on, import, imports |
| `config` | config, configuration, setting, environment |
| `entry` | entry point, main, bootstrap, init |

### Plan extraction

Numbered list items (`1.` or `1)`) longer than 15 chars, plus lines marked `TODO`, `NEXT`, or `STEP` longer than 10 chars. At most 15 steps.

### Topic keywords

| Topic | Keywords |
|-------|----------|
| `auth` | auth, login, jwt, token, session, password, credential |
| `api` | api, endpoint, route, rest, graphql, request, response |
| `database` | database, sql, query, migration, model, schema, table |
| `test` | test, spec, pytest, jest, coverage, mock, fixture |
| `bug` | bug, fix, error, issue, debug, broken, failing |
| `feature` | feature, implement, add, create, new, build |
| `refactor` | refactor, cleanup, reorganize, restructure, rename |
| `config` | config, setting, env, environment, setup, install |
| `docs` | document, readme, comment, explain, describe |
| `perf` | performance, optimize, speed, slow, fast, cache |
| `ui` | ui, frontend, component, style, css, render, display |
| `deploy` | deploy, ci, cd, pipeline, docker, kubernetes |

### Output files

| File | Content |
|------|---------|
| `history/sessions/{YYYYMMDD_HHMMSS}_{id8}.md` | Session summary, format below |
| `history/sessions.md` | One appended line per session |
| `context/discoveries.md` | Appended `- YYYY-MM-DD HH:MM: [category] finding` lines |
| `plans/current.md` | Extracted steps. Overwritten, or appended when the file changed within the last hour |

Session file:

```markdown
# Session: YYYY-MM-DD HH:MM - HH:MM

## Summary
Topic: {topic}
Brief: {first user prompt}

## User Prompts
- ... (up to 10)

## Files Touched
### Modified   (Edit, MultiEdit; up to 20)
### Created    (Write; up to 10)
### Read       (Read; up to 15)

## Tool Usage
- {tool}: {count}

## Commands Run
- `{bash command}` (up to 10)

## Discoveries Extracted
- [{category}] {finding}

## Metadata
- Session ID: {full id}
- Generated: YYYY-MM-DD HH:MM:SS
```

Index line:

```
- YYYY-MM-DD HH:MM: [{topic}] {id8} - {brief, 50 chars} ({N edits, M discoveries} | read-only)
```

Summary on stderr, visible in the terminal:

```
Workspace: session:{filename}, {N} discoveries, plans
```

## Manual vs Automatic

| Action | Manual (shell) | Automatic (hooks) |
|--------|----------------|-------------------|
| Bootstrap workspace | `workspace_init` | SessionStart |
| Add discovery | `workspace_discover "note"` | SessionEnd (extracted) |
| Update plan | edit `plans/current.md` | SessionEnd (extracted) |
| Mark complete | `workspace_complete` | manual only |
| View status | `workspace_status` | manual only |

## Troubleshooting

Hook not running:

```bash
jq '.hooks.SessionStart, .hooks.SessionEnd' ~/.claude/settings.json
```

Test either hook by hand:

```bash
echo '{"session_id":"test","cwd":"/path/with/.giantmem","source":"startup"}' | \
  python3 $GIANT_TOOLING_DIR/workspace/workspace_session_hook.py

echo '{"session_id":"test","cwd":"/path/with/.giantmem","transcript_path":"/path/to/session.jsonl"}' | \
  python3 $GIANT_TOOLING_DIR/workspace/workspace_session_end.py
```

No discoveries extracted: extraction is regex on trigger words, so unusual phrasing will not match. `workspace_discover` is the manual fallback.

## Dependencies

Python 3 standard library, `bash` for the `workspace_init` subprocess, and `workspace-lib.sh` at `$GIANT_TOOLING_DIR/workspace/`.

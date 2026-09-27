# Workspace Claude Code Hooks

Two Python hooks bridge `workspace-lib.sh` with Claude Code sessions. Both are stdlib only and wrap `main()` in a bare `try/except`, so a broken hook never breaks a session.

| Hook | Event | Purpose |
|------|-------|---------|
| `workspace_session_hook.py` | SessionStart | Bootstrap `.giantmem/` if missing, inject `WORKSPACE.md` and the active plan |
| `workspace_session_end.py` | SessionEnd | Write a session file and index line, refresh `WORKSPACE.md` tables, summarize the session with haiku |

```
claude starts
     |
     v
SessionStart hook
     |
     +-- source=startup, no .giantmem/ or scratch/ --> workspace_init via workspace-lib.sh
     +-- inject WORKSPACE.md and plans/current.md
     v
[Claude session runs]
     |
     v
SessionEnd hook
     |
     +-- no .giantmem/ or scratch/ --> minimal auto-init (own Python, no shell lib)
     +-- read transcript JSONL
     +-- create .giantmem/history/sessions/{timestamp}_{id}.md (Topic/Brief = pending)
     +-- append one line to .giantmem/history/sessions.md
     +-- regenerate ## Features and ## Timeline in WORKSPACE.md
     +-- spawn detached `--summarize` child
              |
              +-- claude -p --model haiku over the session file
              +-- rewrite Topic, Brief, add ## Outcomes, patch the index line
     v
session ends
```

## Wiring

Claude Code runs hooks from `~/.claude/settings.json`. The claude-code-config repo chains both through `hooks/dispatch.py`, which loads modules by name from `~/.claude/hooks/`:

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

That repo holds the canonical copies. The files in `workspace/` here mirror them and can be wired directly:

```json
{ "type": "command", "command": "python3 $GIANT_TOOLING_DIR/workspace/workspace_session_hook.py" }
```

The start hook looks for `workspace-lib.sh` at `$WORKSPACE_LIB`, then `~/.claude/lib/workspace/workspace-lib.sh`, then `$GIANT_TOOLING_DIR/workspace/workspace-lib.sh`.

## SessionStart: workspace_session_hook.py

Input on stdin:

```json
{ "session_id": "...", "cwd": "/path/to/project", "source": "startup" }
```

`source` is `startup`, `resume`, or `clear`. Bootstrap runs only on `startup`, and only when neither `.giantmem/` nor a legacy `scratch/` dir exists. Context is injected on every source.

Output on stdout, each section present only when its file exists:

| Section | Source | Limit |
|---------|--------|-------|
| `[Workspace bootstrapped for {name}]` | printed when `workspace_init` just ran | |
| `=== WORKSPACE CONTEXT ===` | `WORKSPACE.md` | first 3500 chars |
| `=== ACTIVE PLAN ===` | `plans/current.md` | first 1500 chars |

## SessionEnd: workspace_session_end.py

Input on stdin:

```json
{ "session_id": "...", "cwd": "/path/to/project", "transcript_path": "~/.claude/projects/.../session.jsonl" }
```

When neither `.giantmem/` nor `scratch/` exists the hook creates a minimal `.giantmem/` itself (`context/`, `plans/`, `history/sessions/`, `filebox/`, `research/`, `reviews/`, `WORKSPACE.md`, `.gitkeep`) and prints `Workspace: initialized .giantmem/ in {dir}` to stderr. It then returns early when there is no transcript path, no messages, or no user or assistant content. Otherwise it:

1. Reads the transcript JSONL and pulls out user prompts, tool calls with file paths, and timestamps.
2. Writes the session file with `Topic: pending` and `Brief: pending`.
3. Appends the index line to `history/sessions.md`.
4. Regenerates `## Features` (from each `features/*/meta.json`) and `## Timeline` (ten most recently modified `.md` files outside `history/`, excluding `WORKSPACE.md` and `_index.md`) in `WORKSPACE.md`, inserting the sections after `## Purpose` when missing.
5. Prints `Workspace: session:{filename}` to stderr.
6. Spawns `python3 workspace_session_end.py --summarize {session_file}` detached, with stdin, stdout, and stderr closed.

### Summarizer

The child runs `claude -p --model haiku --output-format text --no-session-persistence --strict-mcp-config --disable-slash-commands --tools ""` over the first 12000 chars of the session file (`--bare` added when `ANTHROPIC_API_KEY` is set), with a 180 second timeout and `GIANTMEM_SUMMARY_CHILD=1` so the nested session's own SessionEnd hook exits immediately. The prompt asks for:

```
TOPIC: <one lowercase tag, 1-2 words, hyphenated>
BRIEF: <one sentence under 90 chars>
- <outcome 1>
- <outcome 2>
- <outcome 3>
```

On success it rewrites the `Topic:` and `Brief:` lines, inserts `## Outcomes` with up to three bullets after the brief, and patches the matching line in `history/sessions.md`. When `claude` is not on PATH or the call fails, both stay `pending`.

### Output files

| File | Content |
|------|---------|
| `history/sessions/{YYYYMMDD_HHMMSS}_{id8}.md` | Session summary, format below |
| `history/sessions.md` | One appended line per session |
| `WORKSPACE.md` | `## Features` and `## Timeline` regenerated |

Session file:

```markdown
---
type: history
repo: {project dir name}
status: done
lifecycle: candidate
created: YYYY-MM-DD
---
# Session: YYYY-MM-DD HH:MM - HH:MM

## Summary
Topic: {topic}
Brief: {brief}

## Outcomes            (added by the summarizer)
- ...

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

## Metadata
- Session ID: {full id}
- Generated: YYYY-MM-DD HH:MM:SS
```

Index line:

```
- YYYY-MM-DD HH:MM: [{topic}] {id8} - {brief, 50 chars} ({N edits} | read-only)
```

## Manual vs Automatic

| Action | Manual (shell) | Automatic (hooks) |
|--------|----------------|-------------------|
| Bootstrap workspace | `workspace_init` | SessionStart, or SessionEnd fallback |
| Add discovery | `workspace_discover "note"` | manual only |
| Update plan | edit `plans/current.md` | manual only |
| Session summary | `workspace_session_note` | SessionEnd |
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

Topic and brief stuck at `pending`: the summarizer child could not run `claude`, or the call failed or timed out. Run the `--summarize` form by hand against the session file to see the error:

```bash
python3 $GIANT_TOOLING_DIR/workspace/workspace_session_end.py --summarize /path/to/.giantmem/history/sessions/{file}.md
```

## Dependencies

Python 3 standard library, `bash` for the `workspace_init` subprocess, `workspace-lib.sh` on one of the lookup paths above, and the `claude` CLI on PATH for summaries.

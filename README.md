# giant-tooling

Workspace, worktree, and scratch-archive management tooling for Claude Code development workflows.

These tools were extracted from a private utility repo where they evolved over months of daily use. The commit history starts fresh here, but the code is battle-tested.

## What's in here

### workspace/

Manages `.giantmem/` directories that live inside any project repo. The .giantmem dir is a structured workspace for plans, features, research, context, and session history that Claude Code reads and writes during sessions.

The system has two parts. `workspace-lib.sh` provides shell functions (`ws`, `wsb`, `wst`, `wsa`, etc.) for creating, migrating, and inspecting .giantmem dirs from your terminal. Two Python hooks integrate with Claude Code directly: `workspace_session_hook.py` runs at session start to bootstrap .giantmem/ and inject context, and `workspace_session_end.py` parses the JSONL transcript at session end to extract discoveries and create session summaries.

Also includes feature tracking -- `.giantmem/features/` directories with specs, facts, and metadata that persist across sessions, plus a migration tool (`workspace-migrate-features.py`) for converting legacy plan files into the feature structure.

See [workspace/README.md](workspace/README.md) for shell and Claude commands, directory structure, and feature workflow. Design docs in [workspace/docs/](workspace/docs/).

### git-worktrees/

Interactive wizard (`worktree-helper-generator.sh`) that generates a self-contained bash script for managing git worktrees in a specific project. You run the wizard once per project, answer prompts (project name, command prefix, stack type, etc.), and it outputs a config file that creates shell functions like `{prefix}` (switch/create worktree), `{prefix}l` (list), `{prefix}r` (remove with .giantmem backup), and a dozen more.

Generated scripts use a bare repo layout (`.bare/` alongside worktree directories), auto-bootstrap `.giantmem/` in new worktrees, back up .giantmem/ to `~/giantmem_archive/` on worktree removal, and handle stack-specific setup for python/node/go/rust/ruby.

See [git-worktrees/README.md](git-worktrees/README.md) for the full command reference, setup walkthrough, and directory layout. Architecture details in [git-worktrees/docs/](git-worktrees/docs/).

### scratch-archive/

Archives .giantmem/ directories to `~/giantmem_archive/{project}/{branch}/{timestamp}/` and makes them searchable. Two scripts work together: `scratch-archive.sh` handles the file copy, builds a ripgrep-based `.scratch-index`, manages `latest` symlinks, and triggers FTS5 ingestion. `scratch-search.py` maintains a SQLite FTS5 database (`archives.db`) with ranked full-text search, fzf interactive picker with bat-highlighted previews, and project/branch/type filtering.

Indexes `.md` files and `domains/*.json` files. Domain JSONs get flattened into searchable text (entry points, key files, architecture patterns, gotchas) before indexing.

See [scratch-archive/USAGE.md](scratch-archive/USAGE.md) for all commands, flags, and alias setup.

### domain-search/

Standalone CLI (`domains`) for browsing and searching the code domain knowledge base outside of Claude Code. Domain JSONs are structured explorations of code areas (auth layer, payment flow, etc.) created by `/plan-feature` inside Claude Code sessions. This tool lets you list, show, search, and export them from any terminal.

Commands: `list` (show all domains with staleness), `show` (pretty-print a domain), `search` (keyword search across live workspace domains), `archive` (search the FTS5 database across all projects and history), `export` (dump as shareable markdown), `fzf` (interactive picker with preview).

See [domain-search/usage.md](domain-search/usage.md) for the full command reference and workflow examples.

## Setup

Set the env var (optional, defaults to `$HOME/dev/giant-tooling`):

```bash
export GIANT_TOOLING_DIR="$HOME/dev/giant-tooling"
```

Source the workspace library in your shell config:

```bash
source ~/dev/giant-tooling/workspace/workspace-lib.sh
```

Set up scratch-archive aliases:

```bash
alias scratch-archive='~/dev/giant-tooling/scratch-archive/scratch-archive.sh'
alias sa='scratch-archive'
alias saa='sa archive'
alias sal='sa list'
alias sas='sa search'
alias saq='~/dev/giant-tooling/scratch-archive/scratch-search.py'
```

Generate worktree helpers for your projects:

```bash
~/dev/giant-tooling/git-worktrees/worktree-helper-generator.sh
```

Make the domain search CLI available:

```bash
alias domains='~/dev/giant-tooling/domain-search/domains'
```

## Conventions

All scripts use Python stdlib only (no pip dependencies). Shell scripts target bash with `set -euo pipefail`. Hook scripts never crash -- all exceptions are silently caught so they don't break Claude Code sessions.

## License

MIT. See [LICENSE](LICENSE).

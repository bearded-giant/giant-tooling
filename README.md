# giant-tooling

Workspace, worktree, and scratch-archive management tooling for Claude Code development workflows.

These tools were extracted from a private utility repo where they evolved over months of daily use. The commit history starts fresh here, but the code is battle-tested.

## What's in here

1. **workspace/** -- scratch directory lifecycle management, session hooks, feature tracking for Claude Code projects
2. **git-worktrees/** -- worktree helper generator that creates per-project shell functions for git worktree workflows
3. **scratch-archive/** -- archive and FTS5 search system for scratch directories across projects

## Setup

Set the env var (optional, defaults to `$HOME/dev/giant-tooling`):

```bash
export GIANT_TOOLING_DIR="$HOME/dev/giant-tooling"
```

Source the workspace library:

```bash
source ~/dev/giant-tooling/workspace/workspace-lib.sh
```

Set up scratch-archive aliases:

```bash
alias scratch-archive='~/dev/giant-tooling/scratch-archive/scratch-archive.sh'
alias sa='scratch-archive'
alias saq='~/dev/giant-tooling/scratch-archive/scratch-search.py'
```

Generate worktree helpers for your projects:

```bash
~/dev/giant-tooling/git-worktrees/worktree-helper-generator.sh
```

## Docs

Each directory has its own README with detailed usage. See also:

- `workspace/docs/` -- workspace system design, hooks, Claude Code integration
- `git-worktrees/docs/` -- worktree helper details
- `scratch-archive/USAGE.md` -- archive and search commands

## License

MIT. See [LICENSE](LICENSE).

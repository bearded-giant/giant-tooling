# Worktree Helper - Quickstart

Set up git worktree management for any project in under a minute.

## New Project Setup

```bash
wt_init
```

The wizard prompts for:

| Prompt | Example | Notes |
|--------|---------|-------|
| Project name | `edgerouter` | Config filename: `wt-edgerouter.sh` |
| Command prefix | `edgewt` | All commands start with this: `edgewt`, `edgewtl`, etc. |
| Worktree base directory | `~/dev/lua/edgerouter-wt` | Where all worktrees live |
| Stack | `python`, `node`, `lua`, `bash`, `other` | Determines version file and defaults |
| Default branch search order | `main master develop` | Used when creating branches from default |
| CLAUDE.md source branch | `main` | Branch to symlink CLAUDE.md from |
| Env files | `.env` or `.env.local` | Copied from wt-bootstrap/ to new worktrees |
| Use direnv? | `n` | Whether to copy .envrc and run `direnv allow` |
| Archive name | `edgerouter-wt` | Directory name in ~/giantmem_archive/ |
| Version file | `.python-version` | Created in each new worktree |
| Version content | `3.11.12` | Written to version file (empty = copy from bootstrap) |
| Package hint | `Run 'posh'...` | Shown after worktree creation |
| Workspace alias base | `edges` | Creates `edges`, `edgestree`, etc. |

Output: `~/dotfiles/shell/scripts/worktrees/wt-{project}.sh` - sourced immediately and auto-loaded on next shell start.

## First Use

After `wt_init`, initialize the bare repo:

```bash
edgewt_init ~/dev/lua/edgerouter     # from existing local repo (fast)
edgewt_init git@github.com:org/repo.git  # or from remote URL
```

Or just run `edgewt master` - if the bare repo doesn't exist, you'll be prompted for the source.

### What init does

1. Clones bare repo to `{base}/.bare/`
2. Detaches HEAD so no branch is blocked
3. Creates first worktree for the default branch
4. Creates `wt-bootstrap/` directory
5. Runs setup (version file, .giantmem/, .claude/)

After init, populate `wt-bootstrap/` with files you want copied to every new worktree (.env, CLAUDE.md, docker-compose.override.yml, etc.).

## Commands

All commands use your chosen prefix. Examples below use `edgewt`.

| Command | Description |
|---------|-------------|
| `edgewt_init <source>` | Initialize bare repo from local path or URL |
| `edgewt [branch]` | Switch to worktree, create if missing |
| `edgewt` | Toggle to last visited worktree |
| `edgewtl` | List all worktrees with status |
| `edgewtb` | List local and remote branches |
| `edgewta <branch> [base]` | Add worktree explicitly |
| `edgewtr <branch>` | Remove worktree (backs up .giantmem/) |
| `edgewts` | Current worktree status |
| `edgewtp` | Pull (fast-forward only) |
| `edgewtpr [branch]` | Pull with rebase |
| `edgewtf` | Fetch all remotes |
| `edgewtc <target> [source]` | Copy bootstrap files between worktrees |
| `edgewtprune` | Prune stale worktree references |
| `edgewtrepair` | Repair worktrees after directory moves |

### Workspace Backup Commands

Workspace directories are automatically backed up when removing worktrees.

| Command | Description |
|---------|-------------|
| `edgewtbs` | Backup current worktree's .giantmem/ |
| `edgewtsb [branch]` | Backup any worktree's .giantmem/ |
| `edgewtsl` | List all workspace backups |
| `edgewtso <branch>` | Open/cd to workspace backup |

Backups go to `~/giantmem_archive/{archive_name}/{branch}/{timestamp}/` with a `latest` symlink.

### Workspace Commands

| Command | Description |
|---------|-------------|
| `edges` | Workspace status (uses WS_BASE alias) |
| `edgestree` | Workspace tree |
| `edgesdiscover` | Record discoveries |
| `edgescomplete` | Mark workspace complete |
| `edgessync` | Sync workspace |

## Utility Commands

| Command | Description |
|---------|-------------|
| `wt_init` | Set up worktree management for a new project |
| `wt_projects` | List all registered worktree projects |

## Workflow Examples

### Starting a New Feature
```bash
edgewt feature-auth      # creates worktree, sets up environment
# work on feature...
git add . && git commit -m "add auth"
git push
```

### Quick Context Switch
```bash
edgewt main              # switch to main
git pull
edgewt                   # back to previous branch
```

### Review a PR
```bash
edgewtf                  # fetch latest
edgewt pr-123            # checkout PR branch
# review code...
edgewtr pr-123           # clean up when done (.giantmem backed up)
```

## Directory Structure

```
~/dev/lua/edgerouter-wt/
├── .bare/              # bare git repo (shared)
├── .last-branch        # tracks last visited branch
├── wt-bootstrap/       # files copied to new worktrees
│   ├── .env
│   ├── CLAUDE.md
│   └── ...
├── master/             # worktree: master branch
│   └── .giantmem/      # workspace (created by setup)
├── feature-auth/       # worktree: feature branch
└── bugfix-123/         # worktree: bugfix branch
```

## Current Projects

Run `wt_projects` to see all registered worktree projects.

## Architecture

```
~/dotfiles/shell/scripts/worktrees/
  worktree-core.sh          # shared library (~750 lines)
  wt-customcheckout.sh      # cwt config (~20 lines)
  wt-frontend.sh            # fewt config (~20 lines)
  wt-merchantanalytics.sh   # mwt config (~30 lines)
```

Per-project config files set variables (`CWT_BASE`, `CWT_STACK`, etc.) and call `wt_register <prefix>` which creates all the shell functions via eval. Config is read at runtime via bash indirect expansion.

### Stack Support

| Stack | Version File | Package Hint | Notes |
|-------|-------------|-------------|-------|
| `python` | `.python-version` | Poetry shell | Full support |
| `node` | `.nvmrc` | pnpm install | Full support |
| `lua` | - | - | No version file or hint defaults |
| `bash` | - | - | No version file or hint defaults |
| `other` | - | - | No version file or hint defaults |

Lua, bash, and other stacks get the same core worktree management. Stack-specific setup (version managers, dependency hints) can be added to the `_post_setup` hook in the config file.

## Troubleshooting

**"Cannot access bare repository"**
```bash
cd ~/dev/myapp-wt/.bare && git status
```

**"Branch already checked out"**
```bash
edgewtl  # see which worktree has it
```

**Commands not found**
```bash
source ~/.bashrc  # reload shell config
```

**Worktrees broken after moving directory**
```bash
edgewtrepair  # re-links orphaned worktrees
```

## Related Files

- `worktree-core.sh` - shared library (in `~/dotfiles/shell/scripts/worktrees/`)
- `../workspace/workspace-lib.sh` - workspace functions (.giantmem/, session tracking)

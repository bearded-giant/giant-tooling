# Git Worktree Helper Scripts

This directory contains bash scripts to simplify working with Git worktrees for managing multiple branches simultaneously.

## What are Git Worktrees?

Git worktrees allow you to have multiple branches checked out at the same time in different directories. This is especially useful for:
- Working on multiple features simultaneously
- Quick bug fixes without disturbing your current work
- Code reviews without switching branches
- Testing different versions side by side

## Files in this Directory

### worktree-helper-template.sh
The generic template for creating project-specific worktree helpers. **DO NOT source this directly**.

### worktree-helper-customcheckout.sh
A specific implementation for the CustomCheckout project, demonstrating advanced features like:
- Bootstrap directory copying
- Poetry environment setup
- Pre-commit hook installation
- Custom file copying (CLAUDE.md, AGENTS.md, .giant-ai/)

## Quick Start

### Using the Generator (Recommended)

```bash
wt-gen  # or ./worktree-helper-generator.sh
```

The wizard handles everything: stack detection, shell config, and generates a ready-to-use script.

### Automatic Bare Repo Initialization

**New worktree repos are handled automatically.** When you first use a generated helper (e.g., `ma main`) and the bare repo doesn't exist, you'll be prompted:

```
Worktree structure not initialized at /Users/you/dev/myapp-wt

Initialize from existing repo? Enter source path or URL:
> /Users/you/dev/myapp
```

The script will:
1. Create the worktree base directory
2. Clone the source as a bare repo to `.bare/`
3. Configure remote fetch refs
4. Create the first worktree (main/master)
5. Create `wt-bootstrap/` for shared files

You can also initialize explicitly with `{prefix}_init`:
```bash
ma_init /path/to/existing/repo
ma_init git@github.com:org/repo.git
```

### Manual Setup (Alternative)

If you prefer manual setup:

1. **Set up your worktree structure:**
   ```bash
   mkdir -p ~/dev/myproject-worktree
   cd ~/dev/myproject-worktree
   git clone --bare https://github.com/user/myproject.git .bare
   cd .bare
   git config remote.origin.fetch "+refs/heads/*:refs/remotes/origin/*"
   git fetch origin
   ```

2. **Source in your shell config:**
   ```bash
   echo "source ~/path/to/worktree-myproject.sh" >> ~/.bashrc
   ```

## Available Commands

After sourcing your worktree helper script, you'll have these commands:

### Navigation
- `wt [branch]` - Switch to worktree or create new one
- `wt` - Switch to last visited worktree
- `wtl` - List all worktrees with status
- `wtb` - List all branches (local and remote)

### Management
- `wta <branch> [base]` - Add new worktree explicitly
- `wtr <branch> [-f]` - Remove worktree
- `wts` - Show current worktree status
- `wtp` - Pull updates for current branch (fast-forward only)
- `wtpr` - Pull with rebase for current branch
- `wtf` - Fetch all branches
- `wtprune` - Clean up stale worktrees

## Project Structure Example

```
~/dev/myproject-worktree/
├── .bare/              # Bare repository (shared .git)
├── .last-branch        # Tracks last visited worktree
├── bootstrap/          # (Optional) Files to copy to new worktrees
│   ├── .env.example
│   ├── .giant-ai/
│   └── docker-compose.override.yml
├── main/               # Worktree for main branch
├── feature-auth/       # Worktree for feature branch
└── bugfix-login/       # Worktree for bugfix branch
```

## Customization Examples

### For a Node.js Project
```bash
# In _wt_setup_worktree() function:
if [ -f "$target_dir/package.json" ]; then
    echo "Installing npm dependencies..."
    (cd "$target_dir" && npm install)
    echo "✓ Dependencies installed"
fi
```

### For a Python/Poetry Project
```bash
# In _wt_setup_worktree() function:
if [ -f "$target_dir/pyproject.toml" ] && command -v poetry &>/dev/null; then
    echo "Setting up Poetry environment..."
    (cd "$target_dir" && poetry install)
    (cd "$target_dir" && poetry run pre-commit install)
    echo "✓ Poetry environment ready"
fi
```

### Copying Bootstrap Files
```bash
# In _wt_setup_worktree() function:
if [ -d "$bootstrap_dir" ]; then
    cp -r "$bootstrap_dir"/* "$target_dir"/ 2>/dev/null
    cp -r "$bootstrap_dir"/.[^.]* "$target_dir"/ 2>/dev/null
    echo "✓ Bootstrap files copied"
fi
```

## Best Practices

1. **Naming Convention**: Name your script `worktree-{projectname}.sh`
2. **Bootstrap Directory**: Keep common files in a `bootstrap/` directory
3. **Environment Files**: Never commit `.env` files, but keep `.env.example`
4. **Dependencies**: Install project dependencies in `_wt_setup_worktree()`
5. **Git Hooks**: Set up pre-commit hooks automatically

## Troubleshooting

### "Cannot access bare repository"
Make sure you've cloned with `--bare` flag:
```bash
git clone --bare <repo-url> .bare
```

### "Branch already checked out"
A branch can only be checked out in one worktree. Use `wtl` to see where it's checked out.

### Permission Issues
Ensure your scripts are executable:
```bash
chmod +x worktree-*.sh
```

## Advanced Features

The CustomCheckout implementation (`worktree-helper-customcheckout.sh`) includes:
- `cwtc` command for copying files between worktrees
- `cwtpr` command for pull with rebase
- `cwtrepair` command for fixing worktrees after directory moves
- Automatic Poetry environment setup
- Support for copying `.giant-ai/` directories
- Context directory management
- Automatic upstream setting for new branches

Look at this file for inspiration when building complex worktree setups.

## Command Reference (CustomCheckout)

For the `worktree-helper-customcheckout.sh` implementation, all commands are prefixed with `c`:

- `cwt [branch]` - Switch/create worktree
- `cwtl` - List worktrees
- `cwtb` - List branches
- `cwts` - Status
- `cwtp` - Pull (fast-forward)
- `cwtpr [branch]` - Pull with rebase (optionally from specific branch)
- `cwtf` - Fetch all
- `cwta` - Add worktree
- `cwtr` - Remove worktree
- `cwtc` - Copy bootstrap files
- `cwtprune` - Prune stale worktrees
- `cwtrepair` - Repair after directory moves
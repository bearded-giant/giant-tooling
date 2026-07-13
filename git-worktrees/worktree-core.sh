#!/bin/bash

# worktree-core.sh - shared worktree helper library
# sourced by per-project config files (wt-*.sh)
# all functions parameterized by prefix

[[ "${__WT_CORE_LOADED:-}" == "1" ]] && return 0
__WT_CORE_LOADED=1

# source workspace library (correct path)
__WT_WORKSPACE_LIB="${GIANT_TOOLING_DIR:-$HOME/dev/giant-tooling}/workspace/workspace-lib.sh"
[ -f "$__WT_WORKSPACE_LIB" ] && source "$__WT_WORKSPACE_LIB"

declare -a __WT_REGISTERED_PREFIXES=()

# read per-project config via indirect expansion
__wt_config() {
    local prefix="$1" key="$2" default="${3:-}"
    local var="${prefix^^}_${key}"
    echo "${!var:-$default}"
}

# ---------------------------------------------------------------------------
# helper functions (parameterized)
# ---------------------------------------------------------------------------

__wt_current_branch() {
    git rev-parse --abbrev-ref HEAD 2>/dev/null
}

__wt_in_worktree() {
    local prefix="$1"
    local base=$(__wt_config "$prefix" BASE)
    [[ "$PWD" == "$base/"* ]] && [ -d ".git" -o -f ".git" ]
}

__wt_branch_status() {
    local branch="$1"
    local upstream
    upstream=$(git rev-parse --abbrev-ref "$branch@{upstream}" 2>/dev/null)

    if [ -z "$upstream" ]; then
        echo "(no upstream)"
        return
    fi

    local ahead behind
    ahead=$(git rev-list --count "$upstream..$branch" 2>/dev/null)
    behind=$(git rev-list --count "$branch..$upstream" 2>/dev/null)

    if [ "$ahead" -eq 0 ] && [ "$behind" -eq 0 ]; then
        echo "(up to date)"
    elif [ "$ahead" -gt 0 ] && [ "$behind" -eq 0 ]; then
        echo "(ahead $ahead)"
    elif [ "$ahead" -eq 0 ] && [ "$behind" -gt 0 ]; then
        echo "(behind $behind)"
    else
        echo "(diverged: ^$ahead v$behind)"
    fi
}

# ---------------------------------------------------------------------------
# setup - post-creation worktree initialization driven by config
# ---------------------------------------------------------------------------

__wt_setup() {
    local prefix="$1" branch="$2"
    local base=$(__wt_config "$prefix" BASE)
    local target_dir="$base/$branch"
    local bootstrap_dir="$base/wt-bootstrap"

    # version file (.python-version or .nvmrc)
    local vfile=$(__wt_config "$prefix" VERSION_FILE)
    local vcontent=$(__wt_config "$prefix" VERSION_CONTENT)
    if [ -n "$vfile" ]; then
        if [ -n "$vcontent" ] && [ ! -f "$target_dir/$vfile" ]; then
            echo "$vcontent" > "$target_dir/$vfile"
            echo "Created $vfile ($vcontent)"
        elif [ -f "$bootstrap_dir/$vfile" ] && [ ! -f "$target_dir/$vfile" ]; then
            cp "$bootstrap_dir/$vfile" "$target_dir/"
            echo "Created $vfile from bootstrap"
        fi
    fi

    # bulk copy from wt-bootstrap/ (CLAUDE.md handled by claude session hook)
    if [ -d "$bootstrap_dir" ]; then
        echo "Copying bootstrap files..."
        find "$bootstrap_dir" -maxdepth 1 -name "CLAUDE.md" -prune -o -name ".*" -prune -o -print | tail -n +2 | while read -r f; do cp -r "$f" "$target_dir"/ 2>/dev/null; done
        find "$bootstrap_dir" -maxdepth 1 -name ".[^.]*" ! -name ".git" -print | while read -r f; do cp -r "$f" "$target_dir"/ 2>/dev/null; done
        echo "Bootstrap files copied"
    fi

    # named extra files
    local extras=$(__wt_config "$prefix" BOOTSTRAP_EXTRAS)
    for file in $extras; do
        [ -z "$file" ] && continue
        if [ -f "$bootstrap_dir/$file" ] && [ ! -f "$target_dir/$file" ]; then
            cp "$bootstrap_dir/$file" "$target_dir/"
            echo "$file copied"
        elif [ -f "$base/$file" ] && [ ! -f "$target_dir/$file" ]; then
            cp "$base/$file" "$target_dir/"
            echo "$file copied from base"
        fi
    done

    # named extra directories
    local extra_dirs=$(__wt_config "$prefix" BOOTSTRAP_DIRS)
    for dir in $extra_dirs; do
        [ -z "$dir" ] && continue
        if [ -d "$bootstrap_dir/$dir" ] && [ ! -d "$target_dir/$dir" ]; then
            cp -r "$bootstrap_dir/$dir" "$target_dir/$dir"
            echo "$dir directory copied"
        fi
    done

    # copy context/ from first existing worktree
    local copy_ctx=$(__wt_config "$prefix" COPY_CONTEXT "false")
    if [ "$copy_ctx" = "true" ] && [ ! -d "$target_dir/context" ]; then
        for existing_wt in "$base"/*/context; do
            if [ -d "$existing_wt" ] && [ "$(dirname "$existing_wt")" != "$target_dir" ]; then
                cp -r "$existing_wt" "$target_dir/context"
                echo "Context directory copied from $(basename "$(dirname "$existing_wt")")"
                break
            fi
        done
    fi

    # env files
    local env_files=$(__wt_config "$prefix" ENV_FILES)
    for envfile in $env_files; do
        [ -z "$envfile" ] && continue
        if [ -f "$bootstrap_dir/$envfile" ] && [ ! -f "$target_dir/$envfile" ]; then
            cp "$bootstrap_dir/$envfile" "$target_dir/"
            echo "$envfile copied"
        fi
    done

    # direnv
    local use_direnv=$(__wt_config "$prefix" DIRENV "false")
    if [ "$use_direnv" = "true" ]; then
        if [ -f "$base/.envrc" ] && [ ! -f "$target_dir/.envrc" ]; then
            cp "$base/.envrc" "$target_dir/"
            echo ".envrc copied from base"
        fi
        if [ -f "$target_dir/.envrc" ] && command -v direnv &>/dev/null; then
            (cd "$target_dir" && direnv allow)
            echo "direnv allowed"
        fi
    fi

    # python framework setup (uv auto-installs; poetry/pip leave to user)
    local py_framework=$(__wt_config "$prefix" PY_FRAMEWORK "")
    case "$py_framework" in
        uv)
            if ! command -v uv &>/dev/null; then
                echo "Warning: PY_FRAMEWORK=uv but 'uv' not on PATH, skipping venv setup"
            elif [ -f "$target_dir/pyproject.toml" ]; then
                echo "Creating uv venv..."
                (cd "$target_dir" && uv venv) || echo "Warning: uv venv failed"
                echo "Running uv sync..."
                (cd "$target_dir" && uv sync) || echo "Warning: uv sync failed"
            else
                echo "uv: no pyproject.toml found, skipping venv/sync"
            fi
            ;;
    esac

    # workspace init
    if type workspace_init &>/dev/null; then
        workspace_init "$target_dir" "$branch"
    else
        [ ! -d "$target_dir/.giantmem" ] && mkdir -p "$target_dir/.giantmem"
    fi

    # offer initial workspace feature (branch name -> feature name)
    __wt_offer_feature "$prefix" "$branch" "$target_dir"

    # custom post-setup hook
    if type "_${prefix}_post_setup" &>/dev/null 2>&1; then
        "_${prefix}_post_setup" "$branch" "$target_dir"
    fi

    cd "$target_dir"
    local hint=$(__wt_config "$prefix" PKG_HINT)
    [ -n "$hint" ] && echo "$hint"
}

# ---------------------------------------------------------------------------
# offer initial workspace feature (branch name -> feature name, like /new-feature)
# ---------------------------------------------------------------------------

__wt_offer_feature() {
    local prefix="$1" branch="$2" target_dir="$3"
    local feature_py="${GIANT_TOOLING_DIR:-$HOME/dev/giant-tooling}/workspace/scripts/feature.py"

    [ -t 0 ] || return 0
    [ -f "$feature_py" ] || return 0
    [ -d "$target_dir/.giantmem/features" ] || return 0

    # skip base/default branches — feature.py rejects a feature named == base
    local search_branches
    search_branches=$(__wt_config "$prefix" DEFAULT_BRANCHES "main master develop")
    for b in $search_branches; do
        [ "$branch" = "$b" ] && return 0
    done

    echo -n "Create initial workspace feature '$branch'? (Y/n) "
    read -r response
    [[ "$response" =~ ^[Nn]$ ]] && return 0

    python3 "$feature_py" --cwd "$target_dir" new "$branch" --skip-checkout
}

# ---------------------------------------------------------------------------
# init - initialize bare repo structure from source
# ---------------------------------------------------------------------------

__wt_init() {
    local prefix="$1" source_repo="$2"
    local base=$(__wt_config "$prefix" BASE)

    if [ -d "$base/.bare" ]; then
        echo "Already initialized at $base/.bare"
        return 1
    fi

    if [ -z "$source_repo" ]; then
        echo "Usage: ${prefix}_init <source-repo-path-or-url>"
        echo ""
        echo "Examples:"
        echo "  ${prefix}_init /path/to/existing/repo"
        echo "  ${prefix}_init git@github.com:org/repo.git"
        return 1
    fi

    mkdir -p "$base"

    echo "Cloning bare repository..."
    if ! git clone --bare "$source_repo" "$base/.bare"; then
        echo "Error: Failed to clone repository"
        return 1
    fi

    cd "$base/.bare"

    # fix origin if cloned from local repo
    if [ -d "$source_repo" ]; then
        local real_remote
        real_remote=$(git -C "$source_repo" remote get-url origin 2>/dev/null)
        if [ -n "$real_remote" ]; then
            echo "Updating origin to actual remote: $real_remote"
            git remote set-url origin "$real_remote"
        else
            echo "Warning: Source repo has no origin remote configured"
        fi
    fi

    git config remote.origin.fetch "+refs/heads/*:refs/remotes/origin/*"
    git fetch origin

    # determine default branch
    local default_branch
    default_branch=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
    if [ -z "$default_branch" ]; then
        local search_branches=$(__wt_config "$prefix" DEFAULT_BRANCHES "main master develop")
        for branch in $search_branches; do
            if git rev-parse --verify "origin/$branch" >/dev/null 2>&1; then
                default_branch="$branch"
                break
            fi
        done
    fi

    if [ -z "$default_branch" ]; then
        echo "Error: Could not determine default branch"
        return 1
    fi

    echo "Default branch: $default_branch"

    # detach HEAD so no branch is "checked out" in the bare repo
    # this prevents "already checked out" errors when creating worktrees
    git checkout --detach "origin/$default_branch" 2>/dev/null ||
        git update-ref --no-deref HEAD "$(git rev-parse "origin/$default_branch")" 2>/dev/null

    echo "Creating worktree for '$default_branch'..."
    if ! git worktree add "../$default_branch" "$default_branch"; then
        echo "Error: Failed to create worktree"
        return 1
    fi

    mkdir -p "$base/wt-bootstrap"
    echo "Created wt-bootstrap directory for shared files"

    # run setup on first worktree (creates .python-version, .giantmem/, .claude/)
    __wt_setup "$prefix" "$default_branch"

    echo ""
    echo "Initialization complete!"
    echo ""
    echo "Next steps:"
    echo "  1. Copy any .env files to wt-bootstrap/"
    echo "  2. Use '${prefix} <branch>' to create new worktrees"

    cd "$base/$default_branch"
}

# ---------------------------------------------------------------------------
# main - navigate to worktree or create new one
# ---------------------------------------------------------------------------

__wt_main() {
    local prefix="$1"; shift
    local base=$(__wt_config "$prefix" BASE)
    local last_file=$(__wt_config "$prefix" LAST_FILE)

    # push branch to origin only when opted in; default keeps new branches local
    local push_branch=0
    local args=() a
    for a in "$@"; do
        case "$a" in
            --push|-p) push_branch=1 ;;
            *) args+=("$a") ;;
        esac
    done
    if [ ${#args[@]} -gt 0 ]; then set -- "${args[@]}"; else set --; fi

    # auto-init prompt if bare repo doesn't exist
    if [ ! -d "$base/.bare" ]; then
        echo "Worktree structure not initialized at $base"
        echo ""
        echo "To initialize, provide one of:"
        echo "  - Local repo path:  /path/to/existing/repo"
        echo "  - Git clone URL:    git@github.com:org/repo.git"
        echo ""
        echo -n "Source: "
        read -r source_repo
        if [ -n "$source_repo" ]; then
            __wt_init "$prefix" "$source_repo"
            [ $? -ne 0 ] && return 1
        else
            echo "Cancelled. Run '${prefix}_init <source>' to initialize."
            return 1
        fi
        return 0
    fi

    if [ -z "$1" ]; then
        # no argument - toggle to last branch
        if [ -f "$last_file" ]; then
            local last_branch
            last_branch=$(cat "$last_file")
            if [ -d "$base/$last_branch" ]; then
                if __wt_in_worktree "$prefix"; then
                    local current_branch
                    current_branch=$(basename "$PWD")
                    if [ "$current_branch" != "$last_branch" ]; then
                        echo "$current_branch" >"$last_file"
                    fi
                fi
                cd "$base/$last_branch"
                echo "-> $last_branch $(__wt_branch_status "$(__wt_current_branch)")"
            else
                echo "Last branch '$last_branch' no longer exists"
                cd "$base"
            fi
        else
            echo "No last branch recorded"
            cd "$base"
        fi
    else
        if [ -d "$base/$1" ]; then
            # worktree exists - navigate
            if __wt_in_worktree "$prefix"; then
                local current_branch
                current_branch=$(basename "$PWD")
                if [ "$current_branch" != "$1" ]; then
                    echo "$current_branch" >"$last_file"
                fi
            fi
            cd "$base/$1"
            echo "-> $1 $(__wt_branch_status "$(__wt_current_branch)")"
        else
            # worktree doesn't exist - create
            echo "Worktree '$1' not found. Creating..."

            cd "$base/.bare" || {
                echo "Error: Cannot access bare repository at $base/.bare"
                return 1
            }

            local created=0
            local error_msg=""

            if git rev-parse --verify "$1" >/dev/null 2>&1; then
                echo "Found local branch '$1'"
                echo "Create worktree for branch '$1'? (y/N)"
                read -r response
                if [[ ! "$response" =~ ^[Yy]$ ]]; then
                    echo "Cancelled."
                    return 1
                fi
                if git worktree add "../$1" "$1" 2>/dev/null; then
                    echo "Added worktree for existing local branch '$1'"
                    created=1
                else
                    if git worktree list | grep -q "/$1 "; then
                        error_msg="Branch '$1' is already checked out in another worktree"
                    else
                        error_msg="Failed to add worktree for branch '$1'"
                    fi
                fi
            elif git rev-parse --verify "origin/$1" >/dev/null 2>&1; then
                echo "Found remote branch 'origin/$1'"
                echo "Create worktree for branch '$1' tracking origin/$1? (y/N)"
                read -r response
                if [[ ! "$response" =~ ^[Yy]$ ]]; then
                    echo "Cancelled."
                    return 1
                fi
                if git worktree add -b "$1" "../$1" "origin/$1" 2>/dev/null; then
                    echo "Created local branch '$1' tracking origin/$1"
                    created=1
                else
                    error_msg="Failed to create worktree from remote branch 'origin/$1'"
                fi
            else
                echo "Branch '$1' not found locally or remotely"
                local default_branch
                default_branch=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
                if [ -z "$default_branch" ]; then
                    local search_branches=$(__wt_config "$prefix" DEFAULT_BRANCHES "main master develop")
                    for branch in $search_branches; do
                        if git rev-parse --verify "origin/$branch" >/dev/null 2>&1; then
                            default_branch="$branch"
                            break
                        fi
                    done
                fi

                if [ -z "$default_branch" ]; then
                    error_msg="Could not determine default branch"
                else
                    echo "Create new worktree for branch '$1' from $default_branch? (y/N)"
                    read -r response
                    if [[ ! "$response" =~ ^[Yy]$ ]]; then
                        echo "Cancelled."
                        return 1
                    fi
                    if git worktree add -b "$1" "../$1" "origin/$default_branch" 2>/dev/null; then
                        echo "Created new branch '$1' from $default_branch"
                        created=1
                    else
                        error_msg="Failed to create new branch '$1' from $default_branch"
                    fi
                fi
            fi

            if [ $created -eq 1 ]; then
                echo "$1" >"$last_file"
                cd "$base/$1"
                if [ $push_branch -eq 1 ]; then
                    echo "Setting upstream to origin/$1..."
                    if git push -u origin "$1" 2>/dev/null; then
                        echo "Upstream set to origin/$1"
                    else
                        echo "Note: Could not push to origin (may need permissions or remote setup)"
                    fi
                else
                    echo "Branch '$1' is local-only (no remote). Push later: git push -u origin $1"
                fi

                __wt_setup "$prefix" "$1"
                cd "$base/$1"
                echo "-> $1 (ready)"
            else
                echo "Error: $error_msg"
                if [[ "$error_msg" == *"already checked out"* ]]; then
                    echo "Hint: Use '${prefix}l' to see all worktrees"
                elif [[ "$error_msg" == *"Failed"* ]]; then
                    echo "Hint: Try 'git fetch --all' first, or check 'git worktree list'"
                fi
                return 1
            fi
        fi
    fi
}

# ---------------------------------------------------------------------------
# list worktrees with branch status
# ---------------------------------------------------------------------------

__wt_list() {
    local prefix="$1"
    local base=$(__wt_config "$prefix" BASE)
    local original_dir="$PWD"

    echo "Worktrees in $base:"
    echo "------------------------------------"

    cd "$base/.bare" 2>/dev/null || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    git worktree list | while read -r line; do
        if [[ "$line" == *"(bare)"* ]]; then
            echo "$line"
        else
            local path branch status
            path=$(echo "$line" | awk '{print $1}')
            branch=$(echo "$line" | sed -n 's/.*\[\(.*\)\].*/\1/p')

            if [ -n "$branch" ] && [ -d "$path" ]; then
                status=$(cd "$path" && __wt_branch_status "$branch" 2>/dev/null || echo "")
                echo "$line $status"
            else
                echo "$line"
            fi
        fi
    done

    cd "$original_dir"
}

# ---------------------------------------------------------------------------
# list branches
# ---------------------------------------------------------------------------

__wt_branches() {
    local prefix="$1"
    local base=$(__wt_config "$prefix" BASE)
    local original_dir="$PWD"

    cd "$base/.bare" 2>/dev/null || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    echo "Local branches:"
    echo "--------------"
    git branch --format='%(refname:short)' | while read -r branch; do
        local wt_path
        wt_path=$(git worktree list --porcelain | grep -B2 "branch refs/heads/$branch" | grep "^worktree" | cut -d' ' -f2)
        if [ -n "$wt_path" ]; then
            echo "  $branch -> $(basename "$wt_path")"
        else
            echo "  $branch"
        fi
    done

    echo ""
    echo "Remote branches (origin):"
    echo "------------------------"
    git branch -r --format='%(refname:short)' | grep "^origin/" | grep -v "HEAD" | sed 's/^origin\//  /'

    cd "$original_dir"
}

# ---------------------------------------------------------------------------
# pull / pull with rebase
# ---------------------------------------------------------------------------

__wt_pull() {
    local prefix="$1"
    if ! __wt_in_worktree "$prefix"; then
        echo "Error: Not in a worktree"
        return 1
    fi

    local branch
    branch=$(__wt_current_branch)
    echo "Pulling updates for '$branch'..."

    if git pull --ff-only 2>&1; then
        echo "Updated '$branch'"
        __wt_branch_status "$branch"
    else
        echo "Pull failed - you may need to merge or rebase"
        echo "Hint: Use 'git pull --rebase' or resolve conflicts"
    fi
}

__wt_pull_rebase() {
    local prefix="$1"; shift
    if ! __wt_in_worktree "$prefix"; then
        echo "Error: Not in a worktree"
        return 1
    fi

    local branch target_branch="$1"
    branch=$(__wt_current_branch)

    if [ -n "$target_branch" ]; then
        echo "Pulling with rebase for '$branch' from origin/$target_branch..."
        if git pull --rebase origin "$target_branch" 2>&1; then
            echo "Rebased '$branch' on origin/$target_branch"
            __wt_branch_status "$branch"
        else
            echo "Rebase failed - you may need to resolve conflicts"
            echo "Hint: Use 'git status' to see conflicts, 'git rebase --abort' to cancel"
        fi
    else
        echo "Pulling with rebase for '$branch'..."
        if git pull --rebase 2>&1; then
            echo "Rebased '$branch'"
            __wt_branch_status "$branch"
        else
            echo "Rebase failed - you may need to resolve conflicts"
            echo "Hint: Use 'git status' to see conflicts, 'git rebase --abort' to cancel"
        fi
    fi
}

# ---------------------------------------------------------------------------
# add worktree (explicit)
# ---------------------------------------------------------------------------

__wt_add() {
    local prefix="$1"; shift
    local base=$(__wt_config "$prefix" BASE)
    local last_file=$(__wt_config "$prefix" LAST_FILE)

    # push branch to origin only when opted in; default keeps new branches local
    local push_branch=0
    local args=() a
    for a in "$@"; do
        case "$a" in
            --push|-p) push_branch=1 ;;
            *) args+=("$a") ;;
        esac
    done
    if [ ${#args[@]} -gt 0 ]; then set -- "${args[@]}"; else set --; fi

    if [ -z "$1" ]; then
        echo "Usage: ${prefix}a <branch-name> [base-branch] [--push|-p]"
        return 1
    fi

    cd "$base/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    local created=0
    local error_msg=""

    if [ -z "$2" ]; then
        if git rev-parse --verify "$1" >/dev/null 2>&1; then
            echo "Found local branch '$1'"
            echo "Create worktree for branch '$1'? (y/N)"
            read -r response
            if [[ ! "$response" =~ ^[Yy]$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add "../$1" "$1" 2>/dev/null; then
                echo "Added worktree for existing local branch '$1'"
                created=1
            else
                if git worktree list | grep -q "/$1 "; then
                    error_msg="Branch '$1' is already checked out in another worktree"
                else
                    error_msg="Failed to add worktree for branch '$1'"
                fi
            fi
        elif git rev-parse --verify "origin/$1" >/dev/null 2>&1; then
            echo "Found remote branch 'origin/$1'"
            echo "Create worktree for branch '$1' tracking origin/$1? (y/N)"
            read -r response
            if [[ ! "$response" =~ ^[Yy]$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add -b "$1" "../$1" "origin/$1" 2>/dev/null; then
                echo "Created local branch '$1' tracking origin/$1"
                created=1
            else
                error_msg="Failed to create worktree from remote branch"
            fi
        else
            local default_branch
            default_branch=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
            if [ -z "$default_branch" ]; then
                local search_branches=$(__wt_config "$prefix" DEFAULT_BRANCHES "main master develop")
                for branch in $search_branches; do
                    if git rev-parse --verify "origin/$branch" >/dev/null 2>&1; then
                        default_branch="$branch"
                        break
                    fi
                done
            fi

            if [ -z "$default_branch" ]; then
                error_msg="Could not determine default branch"
            else
                echo "Create new worktree for branch '$1' from $default_branch? (y/N)"
                read -r response
                if [[ ! "$response" =~ ^[Yy]$ ]]; then
                    echo "Cancelled."
                    return 1
                fi
                if git worktree add -b "$1" "../$1" "origin/$default_branch" 2>/dev/null; then
                    echo "Created new branch '$1' from $default_branch"
                    created=1
                else
                    error_msg="Failed to create new branch"
                fi
            fi
        fi
    else
        if git rev-parse --verify "$2" >/dev/null 2>&1; then
            echo "Create new worktree for branch '$1' from $2? (y/N)"
            read -r response
            if [[ ! "$response" =~ ^[Yy]$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add -b "$1" "../$1" "$2" 2>/dev/null; then
                echo "Created new branch '$1' from $2"
                created=1
            else
                error_msg="Failed to create branch from '$2'"
            fi
        elif git rev-parse --verify "origin/$2" >/dev/null 2>&1; then
            echo "Create new worktree for branch '$1' from origin/$2? (y/N)"
            read -r response
            if [[ ! "$response" =~ ^[Yy]$ ]]; then
                echo "Cancelled."
                return 1
            fi
            if git worktree add -b "$1" "../$1" "origin/$2" 2>/dev/null; then
                echo "Created new branch '$1' from origin/$2"
                created=1
            else
                error_msg="Failed to create branch from 'origin/$2'"
            fi
        else
            error_msg="Base branch '$2' not found"
        fi
    fi

    if [ $created -eq 1 ]; then
        echo "$1" >"$last_file"
        cd "$base/$1"
        local needs_upstream=0
        if ! git branch -vv | grep -q "^\* $1 .*\[origin/$1\]"; then
            needs_upstream=1
        fi
        if [ $needs_upstream -eq 1 ] && [ $push_branch -eq 1 ]; then
            echo "Setting upstream to origin/$1..."
            if git push -u origin "$1" 2>/dev/null; then
                echo "Upstream set to origin/$1"
            else
                echo "Note: Could not push to origin (may need permissions or remote setup)"
            fi
        elif [ $needs_upstream -eq 1 ]; then
            echo "Branch '$1' is local-only (no remote). Push later: git push -u origin $1"
        fi

        __wt_setup "$prefix" "$1"
        cd "$base/$1"
        echo "-> $1 (created)"
    else
        echo "Error: $error_msg"
        return 1
    fi
}

# ---------------------------------------------------------------------------
# remove worktree (sweeps .giantmem into live.db before delete)
# ---------------------------------------------------------------------------

__wt_remove() {
    local prefix="$1"; shift
    local base=$(__wt_config "$prefix" BASE)
    local last_file=$(__wt_config "$prefix" LAST_FILE)

    if [ -z "$1" ]; then
        echo "Usage: ${prefix}r <branch-name> [-f|--force]"
        return 1
    fi

    local branch_name="$1"
    local worktree_dir="$base/$branch_name"
    local workspace_source="$worktree_dir/.giantmem"
    [ ! -d "$workspace_source" ] && workspace_source="$worktree_dir/scratch"

    if [ "$2" != "-f" ] && [ "$2" != "--force" ]; then
        echo "Are you sure you want to delete worktree '$branch_name'? (y/N)"
        read -r response
        if [[ ! "$response" =~ ^[Yy]$ ]]; then
            echo "Cancelled"
            return 0
        fi
    fi

    # sweep workspace into live.db before removal — content survives in
    # live_docs even after the worktree dir is gone.
    if [ -d "$workspace_source" ]; then
        if command -v giantmem >/dev/null 2>&1; then
            echo "Sweeping workspace into live.db..."
            if ! giantmem index backfill --workspace "$workspace_source"; then
                echo "ERROR: Sweep failed. Worktree removal cancelled to preserve your work."
                echo "Hint: fix the sweep error, then re-run, or pass --force to skip the sweep."
                return 1
            fi
        else
            echo "WARN: giantmem not on PATH — skipping sweep. Workspace will be deleted unindexed."
        fi
        rm -rf "$workspace_source"
    fi

    cd "$base/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    local force=""
    if [ "$2" = "-f" ] || [ "$2" = "--force" ]; then
        force="--force"
    fi

    if git worktree remove $force "../$branch_name" 2>&1; then
        echo "Removed worktree '$branch_name'"
        git worktree prune

        if [ -f "$last_file" ]; then
            local last_branch
            last_branch=$(cat "$last_file")
            if [ "$last_branch" = "$branch_name" ]; then
                rm "$last_file"
            fi
        fi
    else
        echo "Error: Failed to remove worktree '$branch_name'"
        echo "Hint: Use '${prefix}r $branch_name --force' to force removal"
        return 1
    fi
}

# ---------------------------------------------------------------------------
# rename worktree (directory + branch + upstream)
# ---------------------------------------------------------------------------

__wt_rename() {
    local prefix="$1"; shift
    local base=$(__wt_config "$prefix" BASE)
    local last_file=$(__wt_config "$prefix" LAST_FILE)

    if [ -z "$1" ] || [ -z "$2" ]; then
        echo "Usage: ${prefix}rn <old-name> <new-name>"
        return 1
    fi

    local old_name="$1"
    local new_name="$2"
    local old_dir="$base/$old_name"
    local new_dir="$base/$new_name"

    if [ ! -d "$old_dir" ]; then
        echo "Error: Worktree '$old_name' not found"
        return 1
    fi

    if [ -d "$new_dir" ]; then
        echo "Error: '$new_name' already exists"
        return 1
    fi

    if [[ "$PWD" == "$old_dir"* ]]; then
        echo "Error: Cannot rename worktree you're currently in"
        echo "Hint: Switch to another worktree first"
        return 1
    fi

    cd "$base/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    # move worktree directory
    if ! git worktree move "../$old_name" "../$new_name" 2>&1; then
        echo "Error: Failed to move worktree"
        return 1
    fi
    echo "Worktree moved: $old_name -> $new_name"

    # rename the branch
    if git branch -m "$old_name" "$new_name" 2>&1; then
        echo "Branch renamed: $old_name -> $new_name"
    else
        echo "Note: Could not rename branch (may already have the target name)"
    fi

    # update upstream
    cd "$new_dir"
    if git push -u origin "$new_name" 2>/dev/null; then
        echo "Upstream set to origin/$new_name"
        if git push origin --delete "$old_name" 2>/dev/null; then
            echo "Deleted old remote branch origin/$old_name"
        else
            echo "Note: Could not delete old remote branch origin/$old_name"
        fi
    else
        echo "Note: Could not push new branch name to origin"
    fi

    # update .last-branch
    if [ -f "$last_file" ]; then
        if [ "$(cat "$last_file")" = "$old_name" ]; then
            echo "$new_name" > "$last_file"
            echo "Updated last-branch reference"
        fi
    fi

    echo "-> Renamed '$old_name' to '$new_name'"
}

# ---------------------------------------------------------------------------
# status
# ---------------------------------------------------------------------------

__wt_status() {
    local prefix="$1"
    if ! __wt_in_worktree "$prefix"; then
        echo "Not in a git worktree"
        return 1
    fi

    local branch status
    branch=$(__wt_current_branch)
    status=$(__wt_branch_status "$branch")

    echo "Branch: $branch $status"
    git status -sb
}

# ---------------------------------------------------------------------------
# sweep current worktree's workspace into live.db (no archive move)
# ---------------------------------------------------------------------------

__wt_backup_workspace_current() {
    local prefix="$1"

    if ! __wt_in_worktree "$prefix"; then
        echo "Not in a git worktree"
        return 1
    fi

    local worktree_dir
    worktree_dir=$(git rev-parse --show-toplevel)
    local workspace_source="$worktree_dir/.giantmem"
    [ ! -d "$workspace_source" ] && workspace_source="$worktree_dir/scratch"

    if [ ! -d "$workspace_source" ]; then
        echo "No workspace directory found"
        return 1
    fi
    if ! command -v giantmem >/dev/null 2>&1; then
        echo "Error: giantmem not on PATH"
        return 1
    fi
    giantmem index backfill --workspace "$workspace_source"
}

# ---------------------------------------------------------------------------
# fetch
# ---------------------------------------------------------------------------

__wt_fetch() {
    local prefix="$1"
    local base=$(__wt_config "$prefix" BASE)
    local original_dir="$PWD"

    cd "$base/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    echo "Fetching all remotes..."
    if git fetch --all --prune; then
        echo "Fetch completed"
        echo ""
        echo "Branches with remote changes:"
        git for-each-ref --format='%(refname:short) %(upstream:short)' refs/heads | while read -r local upstream; do
            if [ -n "$upstream" ]; then
                local behind
                behind=$(git rev-list --count "$local..$upstream" 2>/dev/null)
                if [ "$behind" -gt 0 ]; then
                    echo "  $local is behind $upstream by $behind commits"
                fi
            fi
        done
    else
        echo "Error: Fetch failed"
        cd "$original_dir"
        return 1
    fi

    cd "$original_dir"
}

# ---------------------------------------------------------------------------
# copy bootstrap files between worktrees
# ---------------------------------------------------------------------------

__wt_copy() {
    local prefix="$1"; shift
    local base=$(__wt_config "$prefix" BASE)

    if [ -z "$1" ]; then
        echo "Usage: ${prefix}c <target-worktree> [source-worktree]"
        echo "  Copies bootstrap files to target worktree"
        echo "  If source not specified, uses first available worktree with files"
        return 1
    fi

    local target_dir="$base/$1"
    local source_dir=""

    if [ ! -d "$target_dir" ]; then
        echo "Error: Target worktree '$1' not found"
        echo "Hint: Use '${prefix}l' to see available worktrees"
        return 1
    fi

    if [ -n "$2" ]; then
        source_dir="$base/$2"
        if [ ! -d "$source_dir" ]; then
            echo "Error: Source worktree '$2' not found"
            return 1
        fi
    else
        local env_files=$(__wt_config "$prefix" ENV_FILES)
        for wt in "$base"/*; do
            if [ -d "$wt" ] && [ "$wt" != "$target_dir" ] && [ "$(basename "$wt")" != ".bare" ] && [ "$(basename "$wt")" != "wt-bootstrap" ] && [ "$(basename "$wt")" != "scratch" ] && [ "$(basename "$wt")" != ".giantmem" ]; then
                for envfile in $env_files; do
                    if [ -f "$wt/$envfile" ]; then
                        source_dir="$wt"
                        echo "Using source worktree: $(basename "$wt")"
                        break 2
                    fi
                done
                if [ -f "$wt/docker-compose.override.yml" ] || [ -d "$wt/context" ]; then
                    source_dir="$wt"
                    echo "Using source worktree: $(basename "$wt")"
                    break
                fi
            fi
        done

        if [ -z "$source_dir" ]; then
            echo "Error: No suitable source worktree found with bootstrap files"
            return 1
        fi
    fi

    echo "Copying bootstrap files from $(basename "$source_dir") to $(basename "$target_dir")..."

    # env files
    local env_files=$(__wt_config "$prefix" ENV_FILES)
    for envfile in $env_files; do
        if [ -f "$source_dir/$envfile" ] && [ ! -f "$target_dir/$envfile" ]; then
            cp "$source_dir/$envfile" "$target_dir/"
            echo "$envfile copied"
        fi
    done

    # .claude directory (CLAUDE.md handled by claude session hook)
    mkdir -p "$target_dir/.claude"

    # extra files
    local extras=$(__wt_config "$prefix" BOOTSTRAP_EXTRAS)
    for file in $extras; do
        [ -z "$file" ] && continue
        if [ -f "$source_dir/$file" ] && [ ! -f "$target_dir/$file" ]; then
            cp "$source_dir/$file" "$target_dir/"
            echo "$file copied"
        elif [ -f "$base/wt-bootstrap/$file" ] && [ ! -f "$target_dir/$file" ]; then
            cp "$base/wt-bootstrap/$file" "$target_dir/"
            echo "$file copied from wt-bootstrap"
        elif [ -f "$base/$file" ] && [ ! -f "$target_dir/$file" ]; then
            cp "$base/$file" "$target_dir/"
            echo "$file copied from base"
        fi
    done

    # extra directories
    local extra_dirs=$(__wt_config "$prefix" BOOTSTRAP_DIRS)
    for dir in $extra_dirs; do
        [ -z "$dir" ] && continue
        if [ -d "$source_dir/$dir" ] && [ ! -d "$target_dir/$dir" ]; then
            cp -r "$source_dir/$dir" "$target_dir/$dir"
            echo "$dir directory copied"
        elif [ -d "$base/wt-bootstrap/$dir" ] && [ ! -d "$target_dir/$dir" ]; then
            cp -r "$base/wt-bootstrap/$dir" "$target_dir/$dir"
            echo "$dir directory copied from wt-bootstrap"
        fi
    done

    # context directory
    local copy_ctx=$(__wt_config "$prefix" COPY_CONTEXT "false")
    if [ "$copy_ctx" = "true" ] && [ -d "$source_dir/context" ] && [ ! -d "$target_dir/context" ]; then
        cp -r "$source_dir/context" "$target_dir/context"
        echo "Context directory copied"
    fi

    # hidden config files from source (skip git files and already-handled files)
    for file in "$source_dir"/.[^.]*; do
        if [ -f "$file" ]; then
            local filename
            filename=$(basename "$file")
            case "$filename" in
            .git | .gitignore | .gitmodules) ;;
            .env | .env.local | .env.example | .envrc) ;;
            *)
                if [ ! -f "$target_dir/$filename" ]; then
                    cp "$file" "$target_dir/"
                    echo "$filename copied"
                fi
                ;;
            esac
        fi
    done

    # direnv
    local use_direnv=$(__wt_config "$prefix" DIRENV "false")
    if [ "$use_direnv" = "true" ]; then
        if [ -f "$base/.envrc" ] && [ ! -f "$target_dir/.envrc" ]; then
            cp "$base/.envrc" "$target_dir/"
            echo ".envrc copied from base"
        fi
        if [ -f "$target_dir/.envrc" ] && command -v direnv &>/dev/null; then
            (cd "$target_dir" && direnv allow)
            echo "direnv allowed"
        fi
    fi

    echo "Bootstrap files copy completed for '$1'"
}

# ---------------------------------------------------------------------------
# prune
# ---------------------------------------------------------------------------

__wt_prune() {
    local prefix="$1"
    local base=$(__wt_config "$prefix" BASE)
    local original_dir="$PWD"

    cd "$base/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    echo "Pruning stale worktrees..."
    if git worktree prune -v; then
        echo "Prune completed"
    else
        echo "Error: Prune failed"
        cd "$original_dir"
        return 1
    fi

    cd "$original_dir"
}

# ---------------------------------------------------------------------------
# repair
# ---------------------------------------------------------------------------

__wt_repair() {
    local prefix="$1"
    local base=$(__wt_config "$prefix" BASE)
    local skip=$(__wt_config "$prefix" REPAIR_SKIP "wt-bootstrap scratch .giantmem")
    local original_dir="$PWD"

    cd "$base/.bare" || {
        echo "Error: Cannot access bare repository"
        return 1
    }

    echo "Repairing worktrees in $base..."
    git worktree prune -v

    for dir in "$base"/*; do
        if [ -d "$dir" ] && [ -f "$dir/.git" ]; then
            local dirname
            dirname=$(basename "$dir")

            # skip non-worktree directories
            local should_skip=0
            for skip_dir in .bare $skip; do
                if [ "$dirname" = "$skip_dir" ]; then
                    should_skip=1
                    break
                fi
            done
            [ $should_skip -eq 1 ] && continue

            if ! git worktree list | grep -q "$dir"; then
                echo "Found orphaned worktree: $dirname"
                if git rev-parse --verify "$dirname" >/dev/null 2>&1; then
                    echo "Re-adding worktree for branch: $dirname"
                    rm -f "$dir/.git"
                    if git worktree add "$dir" "$dirname" 2>/dev/null; then
                        echo "Repaired worktree: $dirname"
                    else
                        echo "Could not repair $dirname - may need manual intervention"
                    fi
                else
                    echo "Branch '$dirname' not found in repository"
                fi
            else
                echo "Worktree OK: $dirname"
            fi
        fi
    done

    echo "Repair complete. Running list..."
    cd "$original_dir"
    __wt_list "$prefix"
}

# ---------------------------------------------------------------------------
# workspace sweep — flush .giantmem into live.db (snapshot dirs are dead)
# ---------------------------------------------------------------------------

__wt_workspace_list() {
    local prefix="$1"
    local base=$(__wt_config "$prefix" BASE)
    local archive_name=$(__wt_config "$prefix" ARCHIVE_NAME "$(basename "$base")")

    if ! command -v giantmem >/dev/null 2>&1; then
        echo "Error: giantmem not on PATH"
        return 1
    fi
    echo "live_docs rows for project '$archive_name':"
    giantmem artifact list --repo "$archive_name" 2>/dev/null || \
        echo "(no rows; run ${prefix}sb from inside a worktree to sweep)"
}

__wt_workspace_backup() {
    local prefix="$1"; shift
    local base=$(__wt_config "$prefix" BASE)
    local branch_name=""

    if [ -z "$1" ]; then
        if __wt_in_worktree "$prefix"; then
            branch_name=$(basename "$PWD")
        else
            echo "Usage: ${prefix}sb [branch-name]"
            echo "  Sweep the workspace into live.db"
            echo "  If no branch specified and in a worktree, uses current branch"
            return 1
        fi
    else
        branch_name="$1"
    fi

    local worktree_dir="$base/$branch_name"
    local workspace_source="$worktree_dir/.giantmem"
    [ ! -d "$workspace_source" ] && workspace_source="$worktree_dir/scratch"

    if [ ! -d "$worktree_dir" ]; then
        echo "Error: Worktree '$branch_name' not found"
        return 1
    fi
    if [ ! -d "$workspace_source" ]; then
        echo "No workspace directory found in '$branch_name'"
        return 1
    fi
    if ! command -v giantmem >/dev/null 2>&1; then
        echo "Error: giantmem not on PATH"
        return 1
    fi
    giantmem index backfill --workspace "$workspace_source"
}

# Open is a no-op: snapshot dirs are deprecated. The GUI + `giantmem artifact
# list` are the read surfaces now. Kept as a stub so existing muscle-memory
# doesn't error out silently.
__wt_workspace_open() {
    local prefix="$1"
    echo "${prefix}so: snapshot directories are deprecated."
    echo "Use the Giantmem GUI or 'giantmem artifact list' to browse workspace content."
}

# ---------------------------------------------------------------------------
# workspace aliases
# ---------------------------------------------------------------------------

__wt_ws_status()   { workspace_status; }
__wt_ws_tree()     { workspace_tree; }
__wt_ws_discover() { shift; workspace_discover "$@"; }
__wt_ws_complete() { workspace_complete; }
__wt_ws_sync()     { workspace_sync; }

# ---------------------------------------------------------------------------
# tab completion
# ---------------------------------------------------------------------------

__wt_complete() {
    local prefix="$1" cur="$2"
    local base=$(__wt_config "$prefix" BASE)
    local skip=$(__wt_config "$prefix" REPAIR_SKIP "wt-bootstrap scratch .giantmem")
    local branches=()

    if [ -d "$base" ]; then
        for dir in "$base"/*; do
            if [ -d "$dir" ]; then
                local dirname
                dirname=$(basename "$dir")
                local should_skip=0
                for skip_dir in .bare .last-branch .giantmem wt-bootstrap $skip; do
                    if [ "$dirname" = "$skip_dir" ]; then
                        should_skip=1
                        break
                    fi
                done
                [ $should_skip -eq 1 ] && continue
                [[ "$dirname" == .* ]] && continue
                branches+=("$dirname")
            fi
        done
    fi
    COMPREPLY=($(compgen -W "${branches[*]}" -- "$cur"))
}

__wt_complete_all() {
    local prefix="$1" cur="$2"
    local base=$(__wt_config "$prefix" BASE)

    cd "$base/.bare" 2>/dev/null || return
    local branches
    branches=$(git branch -a --format='%(refname:short)' | sed 's/^origin\///' | sort -u)
    COMPREPLY=($(compgen -W "$branches" -- "$cur"))
}

# ---------------------------------------------------------------------------
# wt_register - create all user-facing functions for a prefix
# ---------------------------------------------------------------------------

wt_register() {
    local p="$1"
    local uc="${p^^}"

    # derive LAST_FILE
    eval "${uc}_LAST_FILE=\"\${${uc}_BASE}/.last-branch\""

    # main command
    eval "${p}() { __wt_main '${p}' \"\$@\"; }"

    # list/info
    eval "${p}l() { __wt_list '${p}'; }"
    eval "${p}b() { __wt_branches '${p}'; }"
    eval "${p}s() { __wt_status '${p}'; }"

    # git ops
    eval "${p}p() { __wt_pull '${p}'; }"
    eval "${p}pr() { __wt_pull_rebase '${p}' \"\$@\"; }"
    eval "${p}a() { __wt_add '${p}' \"\$@\"; }"
    eval "${p}r() { __wt_remove '${p}' \"\$@\"; }"
    eval "${p}rn() { __wt_rename '${p}' \"\$@\"; }"
    eval "${p}f() { __wt_fetch '${p}'; }"

    # bootstrap
    eval "${p}c() { __wt_copy '${p}' \"\$@\"; }"
    eval "${p}bs() { __wt_backup_workspace_current '${p}'; }"

    # maintenance
    eval "${p}prune() { __wt_prune '${p}'; }"
    eval "${p}repair() { __wt_repair '${p}'; }"

    # workspace archive
    eval "${p}sl() { __wt_workspace_list '${p}'; }"
    eval "${p}sb() { __wt_workspace_backup '${p}' \"\$@\"; }"
    eval "${p}so() { __wt_workspace_open '${p}' \"\$@\"; }"

    # init
    eval "${p}_init() { __wt_init '${p}' \"\$@\"; }"

    # workspace aliases (if WS_BASE configured)
    local ws_base="${uc}_WS_BASE"
    ws_base="${!ws_base:-}"
    if [ -n "$ws_base" ]; then
        eval "${ws_base}() { workspace_status; }"
        eval "${ws_base}tree() { workspace_tree; }"
        eval "${ws_base}discover() { workspace_discover \"\$@\"; }"
        eval "${ws_base}complete() { workspace_complete; }"
        eval "${ws_base}sync() { workspace_sync; }"
    fi

    # completions
    eval "_${p}_complete() {
        local cur=\"\${COMP_WORDS[\$COMP_CWORD]}\"
        __wt_complete '${p}' \"\$cur\"
    }"
    eval "_${p}_complete_all() {
        local cur=\"\${COMP_WORDS[\$COMP_CWORD]}\"
        __wt_complete_all '${p}' \"\$cur\"
    }"

    complete -F "_${p}_complete" "${p}"
    complete -F "_${p}_complete" "${p}r"
    complete -F "_${p}_complete" "${p}rn"
    complete -F "_${p}_complete" "${p}c"
    complete -F "_${p}_complete" "${p}sb"
    complete -F "_${p}_complete" "${p}so"
    complete -F "_${p}_complete_all" "${p}a"

    __WT_REGISTERED_PREFIXES+=("$p")
}

# ---------------------------------------------------------------------------
# wt_projects - list registered projects
# ---------------------------------------------------------------------------

wt_projects() {
    if [ ${#__WT_REGISTERED_PREFIXES[@]} -eq 0 ]; then
        echo "No worktree projects registered"
        return 0
    fi

    printf "%-10s %-40s %s\n" "PREFIX" "BASE" "ARCHIVE"
    printf "%-10s %-40s %s\n" "------" "----" "-------"
    for p in "${__WT_REGISTERED_PREFIXES[@]}"; do
        local base=$(__wt_config "$p" BASE)
        local archive=$(__wt_config "$p" ARCHIVE_NAME "$(basename "$base")")
        printf "%-10s %-40s %s\n" "$p" "$base" "$archive"
    done
}

# ---------------------------------------------------------------------------
# wt_adopt - convert existing repo into bare + worktree layout in place
# ---------------------------------------------------------------------------
#
# Takes an existing non-bare git repo and restructures it as:
#   <parent>/<name>-wt/.bare/         (bare repo, was <repo>/.git)
#   <parent>/<name>-wt/<branch>/      (worktree, was <repo>/)
#
# Preserves uncommitted changes and untracked files (whole working tree
# is moved). Does not run wt_init - user runs that separately to bind
# prefix shell functions.
#
# Usage: wt_adopt [path]   (path defaults to PWD)

wt_adopt() {
    local src="${1:-$PWD}"
    src="${src/#\~/$HOME}"

    if [ ! -d "$src" ]; then
        echo "Error: $src is not a directory" >&2
        return 1
    fi

    src="$(cd "$src" && pwd)"

    if ! git -C "$src" rev-parse --git-dir >/dev/null 2>&1; then
        echo "Error: $src is not a git repository" >&2
        return 1
    fi

    if [ "$(git -C "$src" rev-parse --is-bare-repository 2>/dev/null)" = "true" ]; then
        echo "Error: $src is already a bare repository" >&2
        return 1
    fi

    if [ ! -d "$src/.git" ]; then
        echo "Error: $src/.git is not a directory (linked worktree?)" >&2
        return 1
    fi

    local branch
    branch=$(git -C "$src" branch --show-current)
    if [ -z "$branch" ]; then
        echo "Error: detached HEAD - checkout a branch first" >&2
        return 1
    fi

    if [ -f "$src/.gitmodules" ]; then
        echo "Error: submodules detected, adopt flow does not handle them" >&2
        return 1
    fi

    local parent name base
    parent="$(dirname "$src")"
    name="$(basename "$src")"
    base="$parent/${name}-wt"

    if [ -e "$base" ]; then
        echo "Error: target $base already exists" >&2
        return 1
    fi

    echo "Adopting: $src"
    echo "  branch: $branch"
    echo "  target: $base/{.bare, $branch}"
    echo ""
    read -rp "Proceed? (y/N): " confirm
    [[ ! "$confirm" =~ ^[Yy]$ ]] && echo "Cancelled." && return 1

    mkdir -p "$base" || return 1

    # leave src before mv
    cd "$parent" || return 1

    # move .git out as bare
    if ! mv "$src/.git" "$base/.bare"; then
        echo "Error: failed to move .git" >&2
        return 1
    fi

    git -C "$base/.bare" config --bool core.bare true
    git -C "$base/.bare" config --unset core.worktree 2>/dev/null
    git -C "$base/.bare" config remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*' 2>/dev/null

    # move working tree to worktree slot (mkdir parent for slashes in branch names)
    mkdir -p "$(dirname "$base/$branch")"
    if ! mv "$src" "$base/$branch"; then
        echo "Error: failed to move working tree" >&2
        return 1
    fi

    # wire worktree manually (target dir is non-empty so worktree add cannot bind it)
    # sanitize slot name - git's worktree internals expect flat dir names
    local slot="${branch//\//-}"
    local wtdir="$base/.bare/worktrees/$slot"
    mkdir -p "$wtdir" || return 1
    echo "ref: refs/heads/$branch" > "$wtdir/HEAD"
    echo "../.." > "$wtdir/commondir"
    echo "$base/$branch/.git" > "$wtdir/gitdir"

    # move bare's index into per-worktree slot (bare keeps no index)
    [ -f "$base/.bare/index" ] && mv "$base/.bare/index" "$wtdir/index"

    # gitfile pointer in working tree
    echo "gitdir: $wtdir" > "$base/$branch/.git"

    # verify
    if ! git -C "$base/$branch" status >/dev/null 2>&1; then
        echo "Error: worktree wiring failed - check $base manually" >&2
        return 1
    fi

    cd "$base/$branch"

    echo ""
    echo "Adopted successfully."
    echo "  bare:     $base/.bare"
    echo "  worktree: $base/$branch (branch: $branch)"
    echo ""
    echo "Next:"
    echo "  wt_init                  # wizard - set base dir to: $base"
    echo "                           # SKIP the '<prefix>_init' step it suggests at the end"
    echo "  <prefix> <branch>        # navigate/create worktrees after sourcing"
}

# ---------------------------------------------------------------------------
# wt_init - wizard to create new project config
# ---------------------------------------------------------------------------

wt_init() {
    local worktrees_dir
    worktrees_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

    echo "New Worktree Project"
    echo "==================="
    echo ""

    read -rp "Project name (e.g., myapp): " project_name
    [ -z "$project_name" ] && echo "Cancelled." && return 1

    read -rp "Command prefix (e.g., appwt): " prefix
    [ -z "$prefix" ] && echo "Cancelled." && return 1

    if type "${prefix}" &>/dev/null; then
        echo "Warning: '${prefix}' already exists as a command."
        read -rp "Continue anyway? (y/N): " confirm
        [[ ! "$confirm" =~ ^[Yy]$ ]] && return 1
    fi

    # detect adopt scenario: if PWD itself has .bare/, user is at the base
    # dir; if PWD's parent has .bare/, user is inside a worktree subdir.
    local base_default
    if [ -d "$PWD/.bare" ]; then
        base_default="$PWD"
    elif [ -d "$(dirname "$PWD")/.bare" ]; then
        base_default="$(dirname "$PWD")"
    else
        base_default="${PWD}-wt"
    fi
    echo ""
    echo "Worktree base: parent dir holding .bare/ + worktree subdirs"
    echo "  ex: /path/{name}-wt   (NOT /path/{name}-wt/.bare)"
    read -rp "Worktree base directory [$base_default]: " base_dir
    base_dir="${base_dir:-$base_default}"
    base_dir="${base_dir/#\~/$HOME}"
    # strip trailing /.bare in case user pasted full bare path
    base_dir="${base_dir%/.bare}"

    echo "Stack options: python, node, lua, bash, other"
    read -rp "Stack [python]: " stack
    stack="${stack:-python}"

    local py_framework=""
    if [ "$stack" = "python" ]; then
        echo "Python framework: uv, poetry, pip, none"
        read -rp "Framework [uv]: " py_framework
        py_framework="${py_framework:-uv}"
        case "$py_framework" in
            uv|poetry|pip|none) ;;
            *) echo "Unknown framework '$py_framework', defaulting to none"; py_framework="none" ;;
        esac
    fi

    local default_br_default="main master develop"
    [ "$stack" = "python" ] && default_br_default="stage main master"
    read -rp "Default branch search order [$default_br_default]: " default_branches
    default_branches="${default_branches:-$default_br_default}"

    local first_branch="${default_branches%% *}"

    local env_default=".env"
    [ "$stack" = "node" ] && env_default=".env.local"
    read -rp "Env files (space-separated) [$env_default]: " env_files
    env_files="${env_files:-$env_default}"

    read -rp "Use direnv? (y/N): " use_direnv
    local direnv_val="false"
    [[ "$use_direnv" =~ ^[Yy]$ ]] && direnv_val="true"

    local archive_default
    archive_default="$(basename "$base_dir")"
    read -rp "Archive name for ~/giantmem_archive/ [$archive_default]: " archive_name
    archive_name="${archive_name:-$archive_default}"

    local vfile_default=".python-version"
    local vcontent_default="3.11.12"
    case "$stack" in
        node)
            vfile_default=".nvmrc"
            vcontent_default=""
            ;;
        lua|bash|other)
            vfile_default=""
            vcontent_default=""
            ;;
        python)
            # uv reads python version from pyproject.toml requires-python; skip pinning
            # none = bare python, no version pin needed
            if [ "$py_framework" = "uv" ] || [ "$py_framework" = "none" ]; then
                vfile_default=""
                vcontent_default=""
            fi
            ;;
    esac
    version_file=""
    version_content=""
    if [ -n "$vfile_default" ]; then
        read -rp "Pin runtime version via $vfile_default? (y/N): " pin_version
        if [[ "$pin_version" =~ ^[Yy]$ ]]; then
            read -rp "Version file [$vfile_default]: " version_file
            version_file="${version_file:-$vfile_default}"
            read -rp "Version content [$vcontent_default]: " version_content
            version_content="${version_content:-$vcontent_default}"
        fi
    fi

    local hint_default=""
    case "$stack" in
        python)
            case "$py_framework" in
                uv)     hint_default="Run 'uv sync' to install dependencies" ;;
                poetry) hint_default="Run 'posh' to activate the Poetry shell" ;;
                pip)    hint_default="Activate venv: source .venv/bin/activate && pip install -r requirements.txt" ;;
            esac
            ;;
        node)   hint_default="Run 'pnpm install' to set up dependencies" ;;
    esac
    local pkg_hint=""
    if [ -n "$hint_default" ]; then
        read -rp "Package hint [$hint_default]: " pkg_hint
        pkg_hint="${pkg_hint:-$hint_default}"
    fi

    local ws_default="${prefix%wt}ws"
    [ "$ws_default" = "ws" ] && ws_default="${prefix}ws"
    echo "Workspace status alias: ${ws_default} -> workspace_status, ${ws_default}tree, ${ws_default}sync, etc."
    read -rp "Workspace alias prefix (empty to skip) [$ws_default]: " ws_base
    ws_base="${ws_base:-$ws_default}"

    local uc="${prefix^^}"
    local config_file="$worktrees_dir/wt-${project_name}.sh"

    cat > "$config_file" << CONF
#!/bin/bash
# worktree config: ${project_name} (prefix: ${prefix})

source "\${BASH_SOURCE[0]%/*}/worktree-core.sh"

${uc}_BASE="${base_dir}"
${uc}_STACK="${stack}"
${uc}_PY_FRAMEWORK="${py_framework}"
${uc}_DEFAULT_BRANCHES="${default_branches}"
${uc}_ENV_FILES="${env_files}"
${uc}_DIRENV="${direnv_val}"
${uc}_BOOTSTRAP_EXTRAS="AGENTS.md"
${uc}_BOOTSTRAP_DIRS=""
${uc}_COPY_CONTEXT="false"
${uc}_ARCHIVE_NAME="${archive_name}"
${uc}_REPAIR_SKIP="wt-bootstrap scratch .giantmem"
${uc}_VERSION_FILE="${version_file}"
${uc}_VERSION_CONTENT="${version_content}"
${uc}_PKG_HINT="${pkg_hint}"
${uc}_WS_BASE="${ws_base}"

wt_register ${prefix}
CONF

    chmod +x "$config_file"

    echo ""
    echo "Created: $config_file"
    echo "Sourcing..."
    source "$config_file"
    echo ""
    if [ -d "$base_dir/.bare" ]; then
        echo "Bare repo already exists at $base_dir/.bare (e.g. from wt_adopt)."
        echo "Skip ${prefix}_init - bare is ready to use."
        echo ""
        echo "Commands:"
    else
        echo "Next steps:"
        echo "  1. ${prefix}_init <path-or-url>   initialize bare repo from existing repo"
        echo ""
        echo "  Example:"
        echo "    ${prefix}_init ~/path/to/repo"
        echo "    ${prefix}_init git@github.com:org/repo.git"
        echo ""
        echo "Commands (available after init):"
    fi
    echo "  ${prefix} <branch>     navigate/create worktree"
    echo "  ${prefix}l             list worktrees"
    echo "  ${prefix}a <branch>    add worktree explicitly"
    echo "  ${prefix}r <branch>    remove worktree"
    echo "  ${prefix}sl            list workspace backups"
    echo ""
    echo "Auto-loaded on next shell start via load_modules.sh"
}

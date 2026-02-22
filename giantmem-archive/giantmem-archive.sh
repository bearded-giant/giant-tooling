#!/usr/bin/env bash
# giantmem-archive.sh - standalone .giantmem directory archiving
# archives .giantmem/ dirs to ~/giantmem_archive/{project}/{timestamp}/

set -euo pipefail

GIANTMEM_ARCHIVE_BASE="${GIANTMEM_ARCHIVE_BASE:-$HOME/giantmem_archive}"
GIANTMEM_INDEX_FILE=".giantmem-index"

# build search index for an archive directory
build_index() {
    local archive_dir="$1"
    local index_file="$archive_dir/$GIANTMEM_INDEX_FILE"

    # index .md files and domain .json files: filepath:line:content
    # --no-ignore: don't respect gitignore (archives are outside repos)
    # pattern "." matches any character (i.e., non-empty lines)
    rg -n --no-ignore --glob "*.md" --glob "domains/*.json" "." "$archive_dir" 2>/dev/null > "$index_file" || true

    local count=$(wc -l < "$index_file" | tr -d ' ')
    echo "Indexed: $count lines"
}

usage() {
    cat <<EOF
Usage: $(basename "$0") --action <verb> [options]

Actions:
  archive [--clean] [--project <name>] [src]  Archive a .giantmem directory
  archive --feature <all|name>                Archive completed features (mv out of .giantmem/features/)
  list [project]                              List archives (all or specific project)
  open <project> [ts]                          Open archive in Finder
  search <pattern> [flags]                    Search archives with fzf (interactive)
  index [project]                             Rebuild search indexes for archives
  help                                        Show this help

Search flags:
  -p <project>   Filter by project
  -t <type>      Filter by type: plans|context|research|reviews|filebox|history|prompts|features|domains
  -d <path>      Search specific dir (e.g., 20251220_143022)
  -l             Search only "latest" archives
  -C <n>         Context lines in preview (default: 5)

Examples:
  $(basename "$0") --action archive                            # archive ./.giantmem with auto-detect
  $(basename "$0") --action archive --clean                    # archive and remove ./.giantmem
  $(basename "$0") --action archive --project cc-wt            # archive under cc-wt/{timestamp}/
  $(basename "$0") --action archive --feature all              # archive all completed features
  $(basename "$0") --action archive --feature jwt-session      # archive one completed feature
  $(basename "$0") --action list                               # list all projects
  $(basename "$0") --action list myproj                        # list archives for project
  $(basename "$0") --action open myproj                        # open latest archive
  $(basename "$0") --action search "jwt"                       # search all archives
  $(basename "$0") --action search "replica" -p mas            # search specific project
  $(basename "$0") --action search "auth" -t plans -l          # search only latest, plans only

Archive location: $GIANTMEM_ARCHIVE_BASE/{project}/{timestamp}/
Feature archive:  $GIANTMEM_ARCHIVE_BASE/{project}/{timestamp}/features/{name}/
EOF
}

# check if a feature has status "complete" or "completed"
_is_feature_complete() {
    local json_file="$1"
    local feature_dir="$2"
    local name="$3"

    # try features.json first
    if [ -f "$json_file" ]; then
        local status
        status=$(python3 -c "
import json, sys
with open('$json_file') as f:
    data = json.load(f)
feat = data.get('$name', {})
print(feat.get('status', ''))
" 2>/dev/null)
        if [[ "$status" == "complete" || "$status" == "completed" ]]; then
            return 0
        fi
    fi

    # fall back to spec.md
    if [ -f "$feature_dir/spec.md" ]; then
        if grep -qi '^status:.*complete' "$feature_dir/spec.md" 2>/dev/null; then
            return 0
        fi
    fi

    return 1
}

# remove a feature entry from features.json
_remove_feature_from_json() {
    local json_file="$1"
    local name="$2"

    [ -f "$json_file" ] || return 0
    python3 -c "
import json
with open('$json_file') as f:
    data = json.load(f)
data.pop('$name', None)
with open('$json_file', 'w') as f:
    json.dump(data, f, indent=2)
    f.write('\n')
" 2>/dev/null
}

# remove a feature row from _index.md
_remove_feature_from_index() {
    local index_file="$1"
    local name="$2"

    [ -f "$index_file" ] || return 0
    local tmp="${index_file}.tmp"
    grep -v "\[${name}\]" "$index_file" > "$tmp" 2>/dev/null || true
    mv "$tmp" "$index_file"
}

# archive completed features by moving them to the archive tree
do_archive_feature() {
    local target="$1"
    shift
    local project_override=""
    local dry_run=false

    # parse passthrough for --project, --dry-run
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --project) project_override="$2"; shift 2 ;;
            --dry-run) dry_run=true; shift ;;
            *) shift ;;
        esac
    done

    # resolve project name
    local project_name=""

    if [ -n "$project_override" ]; then
        project_name="$project_override"
    else
        local git_toplevel
        git_toplevel=$(git rev-parse --show-toplevel 2>/dev/null || echo "")
        if [ -n "$git_toplevel" ] && [ -f "$git_toplevel/.git" ]; then
            project_name=$(basename "$(dirname "$git_toplevel")")
        else
            project_name=$(basename "$PWD")
        fi
    fi

    local giantmem_dir="$PWD/.giantmem"
    local features_dir="$giantmem_dir/features"
    local json_file="$features_dir/features.json"
    local index_file="$features_dir/_index.md"

    if [ ! -d "$features_dir" ]; then
        echo "ERROR: No .giantmem/features/ directory found"
        return 1
    fi

    # single timestamp for the batch
    local timestamp=$(date '+%Y%m%d_%H%M%S')
    local project_backup_dir="$GIANTMEM_ARCHIVE_BASE/$project_name"
    local archive_base="$project_backup_dir/$timestamp/features"

    # collect features to archive
    local features_to_archive=()

    if [ "$target" = "all" ]; then
        for fdir in "$features_dir"/*/; do
            [ -d "$fdir" ] || continue
            local fname
            fname=$(basename "$fdir")
            [ "$fname" = "_index.md" ] && continue
            if _is_feature_complete "$json_file" "$fdir" "$fname"; then
                features_to_archive+=("$fname")
            fi
        done
        if [ ${#features_to_archive[@]} -eq 0 ]; then
            echo "No completed features found to archive"
            return 0
        fi
    else
        local fdir="$features_dir/$target"
        if [ ! -d "$fdir" ]; then
            echo "ERROR: Feature not found: $target"
            return 1
        fi
        if ! _is_feature_complete "$json_file" "$fdir" "$target"; then
            echo "ERROR: Feature '$target' is not marked as complete"
            return 1
        fi
        features_to_archive+=("$target")
    fi

    if [ "$dry_run" = true ]; then
        echo "[dry-run] Would archive ${#features_to_archive[@]} feature(s):"
        for fname in "${features_to_archive[@]}"; do
            local src="$features_dir/$fname"
            local dest="$archive_base/$fname"
            local size=$(du -sh "$src" 2>/dev/null | cut -f1)
            echo "  $fname ($size)"
            echo "    mv $src"
            echo "    -> $dest"
        done
        echo "[dry-run] No files moved"
        return 0
    fi

    mkdir -p "$archive_base"

    local archived_count=0
    for fname in "${features_to_archive[@]}"; do
        local src="$features_dir/$fname"
        local dest="$archive_base/$fname"

        echo "Archiving feature: $fname"
        echo "  From: $src"
        echo "    To: $dest"

        if mv "$src" "$dest"; then
            _remove_feature_from_json "$json_file" "$fname"
            _remove_feature_from_index "$index_file" "$fname"
            archived_count=$((archived_count + 1))
            echo "  Done"
        else
            echo "  ERROR: Failed to move $fname"
        fi
    done

    if [ $archived_count -gt 0 ]; then
        # build search index for the timestamp dir
        build_index "$project_backup_dir/$timestamp"

        # update latest symlink
        local latest_link="$project_backup_dir/latest"
        [ -L "$latest_link" ] && rm "$latest_link"
        ln -s "$timestamp" "$latest_link"

        # update fts5 search db in background
        python3 "$(dirname "$0")/giantmem-search.py" ingest --project "$project_name" 2>/dev/null &

        echo "Archived $archived_count feature(s) to $project_backup_dir/$timestamp/features/"
    fi
}

do_archive() {
    local clean=false
    local project_override=""

    # parse flags
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --clean) clean=true; shift ;;
            --project) project_override="$2"; shift 2 ;;
            *) break ;;
        esac
    done

    local scratch_source="${1:-}"
    if [ -z "$scratch_source" ]; then
        if [ -d "$PWD/.giantmem" ]; then
            scratch_source="$PWD/.giantmem"
        else
            scratch_source="$PWD/scratch"
        fi
    fi
    local project_name=""

    if [ -n "$project_override" ]; then
        project_name="$project_override"
    else
        # detect worktree: .git is a file, not a directory
        local git_toplevel
        git_toplevel=$(git rev-parse --show-toplevel 2>/dev/null)
        if [ -n "$git_toplevel" ] && [ -f "$git_toplevel/.git" ]; then
            # worktree: project is the parent dir (e.g., edgerouter-wt)
            project_name=$(basename "$(dirname "$git_toplevel")")
        else
            project_name=$(basename "$(dirname "$(realpath "$scratch_source")")")
        fi
    fi

    if [ ! -d "$scratch_source" ]; then
        echo "ERROR: No .giantmem directory found at: $scratch_source"
        return 1
    fi

    local project_backup_dir="$GIANTMEM_ARCHIVE_BASE/$project_name"
    mkdir -p "$project_backup_dir"

    local timestamp=$(date '+%Y%m%d_%H%M%S')
    local backup_dir="$project_backup_dir/$timestamp"

    echo "Archiving: $scratch_source"
    echo "      To: $backup_dir"

    if cp -r "$scratch_source" "$backup_dir"; then
        # build search index
        build_index "$backup_dir"

        # update latest symlink
        local latest_link="$project_backup_dir/latest"
        [ -L "$latest_link" ] && rm "$latest_link"
        ln -s "$timestamp" "$latest_link"

        local size=$(du -sh "$backup_dir" 2>/dev/null | cut -f1)
        echo "Archived: $size (latest -> $timestamp)"

        # update fts5 search db in background
        python3 "$(dirname "$0")/giantmem-search.py" ingest --project "$project_name" 2>/dev/null &

        if [ "$clean" = true ]; then
            rm -rf "$scratch_source"
            echo "Cleaned: $scratch_source"

            # re-init workspace so .giantmem/ isn't left empty
            local parent_dir="$(dirname "$scratch_source")"
            local ws_lib="${GIANT_TOOLING_DIR:-$HOME/dev/giant-tooling}/workspace/workspace-lib.sh"
            if [ -f "$ws_lib" ]; then
                source "$ws_lib"
                workspace_init "$parent_dir" "$(basename "$parent_dir")"
            fi
        fi

        return 0
    else
        echo "ERROR: Failed to archive"
        return 1
    fi
}

do_list() {
    local project_name="${1:-}"
    local archive_dir="$GIANTMEM_ARCHIVE_BASE"

    [ -n "$project_name" ] && archive_dir="$archive_dir/$project_name"

    if [ ! -d "$archive_dir" ]; then
        echo "No archives found"
        return 0
    fi

    echo "Archives in $archive_dir:"
    echo "────────────────────────────────────"

    if [ -n "$project_name" ]; then
        for backup in "$archive_dir"/*; do
            [ -d "$backup" ] && [ ! -L "$backup" ] || continue
            local backup_name=$(basename "$backup")
            local size=$(du -sh "$backup" 2>/dev/null | cut -f1)
            if [ -L "$archive_dir/latest" ] && [ "$(readlink "$archive_dir/latest")" = "$backup_name" ]; then
                echo "  $backup_name ($size) <- latest"
            else
                echo "  $backup_name ($size)"
            fi
        done
    else
        for project_dir in "$archive_dir"/*; do
            [ -d "$project_dir" ] || continue
            local proj=$(basename "$project_dir")
            local count=$(find "$project_dir" -mindepth 1 -maxdepth 1 -type d -name "[0-9]*_[0-9]*" 2>/dev/null | wc -l | tr -d ' ')
            echo "  $proj/: $count archives"
        done
    fi
}

do_open() {
    if [ -z "${1:-}" ]; then
        echo "Usage: $(basename "$0") open <project> [timestamp]"
        return 1
    fi

    local project="$1"
    local timestamp="${2:-}"
    local target_dir="$GIANTMEM_ARCHIVE_BASE/$project"

    [ -n "$timestamp" ] && target_dir="$target_dir/$timestamp"

    # use latest symlink if no timestamp specified
    if [ -z "$timestamp" ] && [ -L "$target_dir/latest" ]; then
        target_dir="$target_dir/latest"
    fi

    if [ ! -d "$target_dir" ] && [ ! -L "$target_dir" ]; then
        echo "Archive not found: $target_dir"
        return 1
    fi

    echo "Opening: $target_dir"
    open "$target_dir"
}

do_index() {
    local project="${1:-}"
    local search_path="$GIANTMEM_ARCHIVE_BASE"

    [ -n "$project" ] && search_path="$search_path/$project"

    if [ ! -d "$search_path" ]; then
        echo "Path not found: $search_path"
        return 1
    fi

    if ! command -v rg &>/dev/null; then
        echo "ERROR: ripgrep (rg) is required. Install with: brew install ripgrep"
        return 1
    fi

    echo "Rebuilding indexes in $search_path..."
    local count=0

    # find all timestamp directories (YYYYMMDD_HHMMSS pattern)
    while IFS= read -r archive_dir; do
        [ -d "$archive_dir" ] || continue
        echo -n "  $(basename "$(dirname "$archive_dir")")/$(basename "$archive_dir"): "
        build_index "$archive_dir"
        count=$((count + 1))
    done < <(find "$search_path" -type d -name "[0-9]*_[0-9]*" 2>/dev/null)

    echo "Rebuilt $count indexes"
}

do_search() {
    local script_dir
    script_dir="$(dirname "$0")"
    local db_path="$GIANTMEM_ARCHIVE_BASE/archives.db"

    # use fts5 python search when db exists
    if [ -f "$db_path" ]; then
        python3 "$script_dir/giantmem-search.py" search "$@"
        return $?
    fi

    # fallback: legacy rg/fzf search
    local pattern=""
    local project=""
    local type=""
    local dir=""
    local latest_only=false
    local context=5

    while [[ $# -gt 0 ]]; do
        case "$1" in
            -p) project="$2"; shift 2 ;;
            -t) type="$2"; shift 2 ;;
            -d) dir="$2"; shift 2 ;;
            -l) latest_only=true; shift ;;
            -C) context="$2"; shift 2 ;;
            -*)
                echo "Unknown flag: $1"
                return 1
                ;;
            *)
                if [ -z "$pattern" ]; then
                    pattern="$1"
                else
                    echo "Unexpected argument: $1"
                    return 1
                fi
                shift
                ;;
        esac
    done

    if [ -z "$pattern" ]; then
        echo "Usage: $(basename "$0") search <pattern> [-p project] [-t type] [-d dir] [-l] [-C n]"
        return 1
    fi

    if ! command -v fzf &>/dev/null; then
        echo "ERROR: fzf is required for search. Install with: brew install fzf"
        return 1
    fi
    if ! command -v rg &>/dev/null; then
        echo "ERROR: ripgrep (rg) is required for search. Install with: brew install ripgrep"
        return 1
    fi

    local search_path="$GIANTMEM_ARCHIVE_BASE"
    if [ -n "$project" ]; then
        search_path="$search_path/$project"
        if [ ! -d "$search_path" ]; then
            echo "Project not found: $project"
            return 1
        fi
    fi
    if [ -n "$dir" ]; then
        search_path="$search_path/$dir"
        if [ ! -d "$search_path" ]; then
            echo "Directory not found: $dir"
            return 1
        fi
    fi

    local grep_paths=""
    if [ "$latest_only" = true ]; then
        while IFS= read -r latest_link; do
            resolved=$(readlink -f "$latest_link" 2>/dev/null)
            [ -d "$resolved" ] && grep_paths="$grep_paths $resolved"
        done < <(find "$search_path" -name "latest" -type l 2>/dev/null)

        if [ -z "$grep_paths" ]; then
            echo "No 'latest' archives found"
            return 1
        fi
    else
        grep_paths="$search_path"
    fi

    if [ -n "$type" ]; then
        case "$type" in
            plans|context|research|reviews|filebox|history|prompts|features|domains) ;;
            *)
                echo "Invalid type: $type"
                echo "Valid types: plans, context, research, reviews, filebox, history, prompts, features, domains"
                return 1
                ;;
        esac
    fi

    local results=""
    local index_files=""

    for search_dir in $grep_paths; do
        while IFS= read -r idx; do
            index_files="$index_files $idx"
        done < <(find "$search_dir" -name "$GIANTMEM_INDEX_FILE" -type f 2>/dev/null)
    done

    if [ -n "$index_files" ]; then
        if [ -n "$type" ]; then
            results=$(cat $index_files 2>/dev/null | rg "$pattern" | rg "/$type/" || true)
        else
            results=$(cat $index_files 2>/dev/null | rg "$pattern" || true)
        fi
    fi

    if [ -z "$results" ]; then
        if [ -n "$type" ]; then
            results=$(rg -n --no-ignore --glob "*.md" "$pattern" $grep_paths 2>/dev/null | rg "/$type/" || true)
        else
            results=$(rg -n --no-ignore --glob "*.md" "$pattern" $grep_paths 2>/dev/null || true)
        fi
    fi

    if [ -z "$results" ]; then
        echo "No matches found for: $pattern"
        return 0
    fi

    local selected
    selected=$(echo "$results" | \
        fzf --ansi \
            --delimiter ':' \
            --preview 'line={2}; start=$((line > 10 ? line - 10 : 1)); bat --color=always --highlight-line "$line" --line-range "$start:$((line + 10))" {1} 2>/dev/null || sed -n "$start,$((line + 10))p" {1}' \
            --preview-window 'right:60%:wrap' \
            --header "Searching: $pattern | Enter to select | Esc to cancel" \
            --bind 'ctrl-u:preview-half-page-up,ctrl-d:preview-half-page-down' \
        || true)

    if [ -n "$selected" ]; then
        local file line
        file=$(echo "$selected" | cut -d: -f1)
        line=$(echo "$selected" | cut -d: -f2)
        echo "$file:$line"
    fi
}

# main -- parse --action and --feature, pass the rest through
action=""
feature=""
dry_run=false
passthrough=()

while [[ $# -gt 0 ]]; do
    case "$1" in
        --action) action="$2"; shift 2 ;;
        --feature) feature="$2"; shift 2 ;;
        --dry-run) dry_run=true; shift ;;
        -h|--help) action="help"; shift ;;
        *) passthrough+=("$1"); shift ;;
    esac
done

# backward compat: bare positional subcommand (e.g. `giantmem-archive list`)
if [ -z "$action" ] && [ ${#passthrough[@]} -gt 0 ]; then
    case "${passthrough[0]}" in
        archive|a|list|ls|l|open|o|search|s|index|idx|i|help)
            action="${passthrough[0]}"
            passthrough=("${passthrough[@]:1}")
            ;;
    esac
fi

[ -z "$action" ] && action="help"

case "$action" in
    archive|a)
        if [ -n "$feature" ]; then
            feature_args=("${passthrough[@]+"${passthrough[@]}"}")
            [ "$dry_run" = true ] && feature_args+=("--dry-run")
            do_archive_feature "$feature" "${feature_args[@]+"${feature_args[@]}"}"
        else
            do_archive "${passthrough[@]+"${passthrough[@]}"}"
        fi
        ;;
    list|ls|l)
        do_list "${passthrough[@]+"${passthrough[@]}"}"
        ;;
    open|o)
        do_open "${passthrough[@]+"${passthrough[@]}"}"
        ;;
    search|s)
        do_search "${passthrough[@]+"${passthrough[@]}"}"
        ;;
    index|idx|i)
        do_index "${passthrough[@]+"${passthrough[@]}"}"
        ;;
    help)
        usage
        ;;
    *)
        echo "Unknown action: $action"
        usage
        exit 1
        ;;
esac

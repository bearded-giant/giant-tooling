#!/bin/bash
# list-features - show feature status table from features.json cache
# read-only: never mutates spec.md, meta.json, or features.json
# usage: list-features [--dir <path>] [--all]

set -euo pipefail

features_dir=""
show_archived=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir)
      features_dir="$2"
      shift 2
      ;;
    --all)
      show_archived=1
      shift
      ;;
    -h|--help)
      echo "usage: list-features [--dir <path>] [--all]"
      echo "  default: ./.giantmem/features"
      echo "  --dir   appends /.giantmem/features if not already present"
      echo "  --all   include archived features (excluded by default)"
      return 0 2>/dev/null || exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      return 1 2>/dev/null || exit 1
      ;;
  esac
done

if [ -z "$features_dir" ]; then
  features_dir="$(pwd)/.giantmem/features"
else
  case "$features_dir" in
    */.giantmem/features) ;;
    */.giantmem/features/) ;;
    *) features_dir="${features_dir%/}/.giantmem/features" ;;
  esac
fi

if [ ! -d "$features_dir" ]; then
  echo "no features directory at: $features_dir" >&2
  return 1 2>/dev/null || exit 1
fi

cache="$features_dir/features.json"

if [ ! -f "$cache" ]; then
  # check if any feature subdirs exist
  has_features=0
  for d in "$features_dir"/*/; do
    [ -d "$d" ] || continue
    has_features=1
    break
  done
  if [ "$has_features" -eq 0 ]; then
    echo "no features yet in $features_dir"
    return 0 2>/dev/null || exit 0
  fi
  echo "no features.json cache at: $cache" >&2
  echo "feature subdirs exist but cache is missing -- run a feature command to initialize" >&2
  return 1 2>/dev/null || exit 1
fi

# parse cache into tab-separated rows: last_session\tstatus\tbranch\tname
# supports shapes:
#   {"features": [{"name": "x", ...}, ...]}
#   {"x": {...}, "y": {...}}
#   [{"name": "x", ...}, ...]
data=$(SHOW_ARCHIVED="$show_archived" python3 -c "
import json, os, sys

show_archived = os.environ.get('SHOW_ARCHIVED') == '1'

with open('$cache') as f:
    raw = json.load(f)

if isinstance(raw, dict) and 'features' in raw and isinstance(raw['features'], list):
    items = [(feat.get('name', '?'), feat) for feat in raw['features']]
elif isinstance(raw, dict):
    items = list(raw.items())
elif isinstance(raw, list):
    items = [(feat.get('name', '?'), feat) for feat in raw]
else:
    sys.exit(0)

if not items:
    sys.exit(0)

rows = []
for name, feat in items:
    status = feat.get('status', 'unknown')
    if not show_archived and status == 'archived':
        continue
    branch = feat.get('branch', '') or '-'
    last = feat.get('last_session', feat.get('completed', feat.get('created', 'n/a')))
    rows.append((last, status, branch, name))
rows.sort(key=lambda r: r[0], reverse=True)
for r in rows:
    print('\t'.join(r))
")

if [ -z "$data" ]; then
  echo "no features found in $features_dir"
  return 0 2>/dev/null || exit 0
fi

# calculate column widths
max_name=7 max_status=6 max_branch=6
while IFS=$'\t' read -r _ status branch name; do
  (( ${#name} > max_name )) && max_name=${#name}
  (( ${#status} > max_status )) && max_status=${#status}
  (( ${#branch} > max_branch )) && max_branch=${#branch}
done <<< "$data"

nw=$((max_name + 2))
sw=$((max_status + 2))
bw=$((max_branch + 2))
dw=15

h_line() {
  local c="$1" m="$2" r="$3"
  printf "%s" "$c"
  printf '%*s' "$nw" '' | tr ' ' '-'
  printf "%s" "$m"
  printf '%*s' "$sw" '' | tr ' ' '-'
  printf "%s" "$m"
  printf '%*s' "$bw" '' | tr ' ' '-'
  printf "%s" "$m"
  printf '%*s' "$dw" '' | tr ' ' '-'
  printf "%s\n" "$r"
}

pad() { printf " %-*s" "$(($2 - 1))" "$1"; }

h_line '+' '+' '+'
printf "|"; pad "Feature" "$nw"; printf "|"; pad "Status" "$sw"; printf "|"; pad "Branch" "$bw"; printf "|"; pad "Last Modified" "$dw"; printf "|\n"
h_line '+' '+' '+'

while IFS=$'\t' read -r date status branch name; do
  printf "|"; pad "$name" "$nw"; printf "|"; pad "$status" "$sw"; printf "|"; pad "$branch" "$bw"; printf "|"; pad "$date" "$dw"; printf "|\n"
  h_line '+' '+' '+'
done <<< "$data"

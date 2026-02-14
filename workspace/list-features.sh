#!/bin/bash
# list-features - show feature status table from scratch/features/

set -euo pipefail

features_dir=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dir)
      features_dir="$2"
      shift 2
      ;;
    -h|--help)
      echo "usage: list-features [--dir <path>]"
      echo "  default: ./scratch/features"
      echo "  --dir appends /scratch/features if not already present"
      return 0 2>/dev/null || exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      return 1 2>/dev/null || exit 1
      ;;
  esac
done

if [ -z "$features_dir" ]; then
  features_dir="$(pwd)/scratch/features"
else
  # append scratch/features if the path doesn't already end with it
  case "$features_dir" in
    */scratch/features) ;;
    */scratch/features/) ;;
    *) features_dir="${features_dir%/}/scratch/features" ;;
  esac
fi

if [ ! -d "$features_dir" ]; then
  echo "no features directory at: $features_dir" >&2
  return 1 2>/dev/null || exit 1
fi

# collect feature data: date, status, branch, name
data=""
for dir in "$features_dir"/*/; do
  [ -d "$dir" ] || continue
  name=$(basename "$dir")
  [ "$name" = "_index.md" ] && continue

  spec="$dir/spec.md"
  meta="$dir/meta.json"

  if [ -f "$spec" ]; then
    status=$(grep -m1 '^status:' "$spec" | awk '{print $2}' || echo "unknown")
    modified=$(stat -f '%Sm' -t '%Y-%m-%d' "$spec" 2>/dev/null || stat -c '%Y' "$spec" 2>/dev/null | head -c10 || echo "unknown")
  else
    status="unknown"
    modified="n/a"
  fi

  # read branch from meta.json first, fall back to facts.md
  branch=""
  if [ -f "$meta" ]; then
    branch=$(grep -o '"branch"[[:space:]]*:[[:space:]]*"[^"]*"' "$meta" 2>/dev/null | head -1 | sed 's/.*"branch"[[:space:]]*:[[:space:]]*"//;s/"//' || true)
  fi
  if [ -z "$branch" ] && [ -f "$dir/facts.md" ]; then
    branch=$(grep -m1 '^branch:' "$dir/facts.md" 2>/dev/null | awk '{print $2}' || true)
  fi
  [ -z "$branch" ] && branch="-"

  data+="${modified}\t${status}\t${branch}\t${name}\n"
done

if [ -z "$data" ]; then
  echo "no features found in $features_dir"
  return 0 2>/dev/null || exit 0
fi

# sort by date descending, then render table
sorted=$(echo -e "$data" | sort -rn)

# calculate column widths
max_name=7 max_status=6 max_branch=6
while IFS=$'\t' read -r _ status branch name; do
  (( ${#name} > max_name )) && max_name=${#name}
  (( ${#status} > max_status )) && max_status=${#status}
  (( ${#branch} > max_branch )) && max_branch=${#branch}
done <<< "$sorted"

nw=$((max_name + 2))
sw=$((max_status + 2))
bw=$((max_branch + 2))
dw=15

# box drawing
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
done <<< "$sorted"

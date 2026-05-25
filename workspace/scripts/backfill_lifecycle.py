#!/usr/bin/env python3
"""
Backfill `lifecycle:` frontmatter into existing .giantmem/ artifacts.

Usage:
    backfill_lifecycle.py [workspace_dir] [--dry-run] [--all-repos]

Rules:
  - Files under research/ or context/discoveries.md -> lifecycle: candidate
  - All other artifact-shaped files -> lifecycle: durable
  - Files that already declare lifecycle are left untouched (idempotent).

Stdlib only — matches the rest of giant-tooling/workspace/scripts/.
"""

import argparse
import json
import os
import sys
from pathlib import Path

VALID_LIFECYCLES = {"candidate", "durable", "deprecated"}
ARTIFACT_EXTS = (".md", ".json", ".yaml", ".yml")


def default_lifecycle(rel: Path) -> str:
    p = str(rel).replace(os.sep, "/").lower()
    if p.endswith("context/discoveries.md"):
        return "candidate"
    parts = p.split("/")
    if "research" in parts:
        return "candidate"
    return "durable"


def parse_frontmatter(text: str):
    if not text.startswith("---\n"):
        return None, text
    end = text.find("\n---\n", 4)
    if end == -1:
        return None, text
    block = text[4:end]
    body = text[end + 5:]
    fm = []
    has_lifecycle = False
    for line in block.split("\n"):
        if line.strip().startswith("lifecycle:"):
            has_lifecycle = True
        fm.append(line)
    return {"lines": fm, "has_lifecycle": has_lifecycle, "body": body}, text


def patch_md(path: Path, lifecycle: str, dry_run: bool) -> str:
    text = path.read_text()
    parsed, _ = parse_frontmatter(text)
    if parsed is None:
        return "skipped-no-frontmatter"
    if parsed["has_lifecycle"]:
        return "skipped-has-lifecycle"
    new_lines = list(parsed["lines"])
    inserted = False
    for i, line in enumerate(new_lines):
        if line.startswith("status:"):
            new_lines.insert(i + 1, f"lifecycle: {lifecycle}")
            inserted = True
            break
    if not inserted:
        new_lines.append(f"lifecycle: {lifecycle}")
    new_block = "---\n" + "\n".join(new_lines) + "\n---\n" + parsed["body"]
    if dry_run:
        return f"would-stamp-{lifecycle}"
    path.write_text(new_block)
    return f"stamped-{lifecycle}"


def patch_json(path: Path, lifecycle: str, dry_run: bool) -> str:
    try:
        data = json.loads(path.read_text())
    except json.JSONDecodeError:
        return "skipped-bad-json"
    if not isinstance(data, dict):
        return "skipped-not-object"
    # Only stamp JSON that already declares a `type:` — i.e. is itself
    # an artifact. Registries like features.json and artifacts.json
    # are not artifacts; they nest artifact records.
    if "type" not in data:
        return "skipped-not-artifact-json"
    if "lifecycle" in data:
        return "skipped-has-lifecycle"
    data["lifecycle"] = lifecycle
    if dry_run:
        return f"would-stamp-{lifecycle}"
    path.write_text(json.dumps(data, indent=2) + "\n")
    return f"stamped-{lifecycle}"


def walk(workspace_dir: Path):
    for path in workspace_dir.rglob("*"):
        if not path.is_file():
            continue
        rel = path.relative_to(workspace_dir)
        if any(part.startswith(".") for part in rel.parts):
            continue
        if path.suffix not in ARTIFACT_EXTS:
            continue
        yield path, rel


def process(workspace_dir: Path, dry_run: bool) -> dict:
    counts = {"stamped": 0, "skipped": 0, "errors": 0}
    print(f"workspace: {workspace_dir}")
    for path, rel in walk(workspace_dir):
        lifecycle = default_lifecycle(rel)
        try:
            if path.suffix == ".json":
                result = patch_json(path, lifecycle, dry_run)
            else:
                result = patch_md(path, lifecycle, dry_run)
        except Exception as e:  # noqa: BLE001
            print(f"  ERROR {rel}: {e}", file=sys.stderr)
            counts["errors"] += 1
            continue
        if result.startswith(("stamped", "would-stamp")):
            counts["stamped"] += 1
            print(f"  {result:24s} {rel}")
        else:
            counts["skipped"] += 1
    return counts


def discover_workspaces() -> list[Path]:
    roots_env = os.environ.get("GIANTMEM_DEV_ROOTS", "")
    roots = [Path(r).expanduser() for r in roots_env.split(":") if r]
    if not roots:
        roots = [Path.home() / "dev"]
    out: list[Path] = []
    for root in roots:
        if not root.exists():
            continue
        for path in root.rglob(".giantmem"):
            if path.is_dir():
                out.append(path)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("workspace_dir", nargs="?", default=".")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--all-repos", action="store_true",
                    help="walk every .giantmem/ under $GIANTMEM_DEV_ROOTS (default ~/dev)")
    args = ap.parse_args()

    if args.all_repos:
        workspaces = discover_workspaces()
        if not workspaces:
            print("no .giantmem/ workspaces discovered", file=sys.stderr)
            sys.exit(1)
    else:
        target = Path(args.workspace_dir).resolve()
        if target.name != ".giantmem":
            candidate = target / ".giantmem"
            if candidate.exists():
                target = candidate
        if not target.exists():
            print(f"error: .giantmem/ not found at {target}", file=sys.stderr)
            sys.exit(1)
        workspaces = [target]

    totals = {"stamped": 0, "skipped": 0, "errors": 0}
    for ws in workspaces:
        c = process(ws, args.dry_run)
        for k, v in c.items():
            totals[k] += v
    print()
    print(f"totals: stamped={totals['stamped']} skipped={totals['skipped']} errors={totals['errors']}")
    if args.dry_run:
        print("(dry-run: no files written)")


if __name__ == "__main__":
    main()

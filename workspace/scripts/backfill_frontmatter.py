#!/usr/bin/env python3
"""
Backfill YAML frontmatter into legacy .giantmem/ artifacts.

Usage:
    backfill_frontmatter.py [workspace_dir] [--dry-run] [--apply-to TYPE]

This script:
1. Walks .giantmem/ looking for artifact files
2. Infers artifact type from path (per artifact_registry taxonomy)
3. Adds frontmatter to .md/.yaml files lacking it
4. Adds top-level frontmatter keys to .json files lacking them
5. Reports orphans (files in artifact locations without inferable type)

Idempotent: re-running on already-stamped files is a no-op.

Type inference from path:
  .giantmem/specs/{domain}/spec.md                          -> source-spec
  .giantmem/features/{name}/specs/{domain}/spec.md          -> delta-spec
  .giantmem/features/{name}/proposal.md                     -> proposal
  .giantmem/features/{name}/spec.md                         -> proposal (legacy)
  .giantmem/features/{name}/design.md                       -> design
  .giantmem/features/{name}/tasks.md                        -> tasks
  .giantmem/features/{name}/plans/*.md                      -> plan
  .giantmem/features/{name}/research/*.md                   -> research
  .giantmem/research/*.md                                   -> research
  .giantmem/features/{name}/reviews/*.md                    -> review
  .giantmem/features/{name}/{name}-notes.md                 -> notes
  .giantmem/domains/{name}.json                             -> domain
  .giantmem/context/patterns.md, .giantmem/context/*.md     -> pattern
"""

import sys
import json
import argparse
import subprocess
from pathlib import Path
from datetime import datetime, timezone
from dataclasses import dataclass


REQUIRED_FIELDS = {"type", "status"}
DEFAULT_STATUS = "ready"


@dataclass
class ArtifactSpec:
    type: str
    feature: str = ""           # empty for repo-level
    domain: str = ""
    name: str = ""              # for domain artifacts


def detect_repo(workspace_dir: Path) -> tuple[str, str]:
    """Return (repo_name, branch). Falls back to dir name + 'unknown' if git fails."""
    repo_root = workspace_dir.parent if workspace_dir.name == ".giantmem" else workspace_dir
    repo_name = repo_root.name
    branch = "unknown"
    try:
        result = subprocess.run(
            ["git", "-C", str(repo_root), "rev-parse", "--abbrev-ref", "HEAD"],
            capture_output=True, text=True, check=False,
        )
        if result.returncode == 0:
            branch = result.stdout.strip()
    except FileNotFoundError:
        pass
    return repo_name, branch


def classify(path: Path, workspace_dir: Path) -> ArtifactSpec | None:
    """Return ArtifactSpec or None if path is not a recognized artifact."""
    try:
        rel = path.relative_to(workspace_dir)
    except ValueError:
        return None
    parts = rel.parts

    if len(parts) >= 3 and parts[0] == "specs" and parts[-1] == "spec.md":
        return ArtifactSpec(type="source-spec", domain=parts[1])

    if len(parts) >= 5 and parts[0] == "features" and parts[2] == "specs" and parts[-1] == "spec.md":
        return ArtifactSpec(type="delta-spec", feature=parts[1], domain=parts[3])

    if len(parts) >= 3 and parts[0] == "features":
        feature = parts[1]
        tail = parts[2]

        if tail == "proposal.md":
            return ArtifactSpec(type="proposal", feature=feature)
        if tail == "spec.md":
            return ArtifactSpec(type="proposal", feature=feature)
        if tail == "design.md":
            return ArtifactSpec(type="design", feature=feature)
        if tail == "tasks.md":
            return ArtifactSpec(type="tasks", feature=feature)
        if tail == "facts.md":
            return ArtifactSpec(type="facts", feature=feature)
        if tail == f"{feature}-notes.md":
            return ArtifactSpec(type="notes", feature=feature)
        if tail == "plans" and len(parts) >= 4 and parts[3].endswith(".md"):
            return ArtifactSpec(type="plan", feature=feature, name=parts[3].removesuffix(".md"))
        if tail == "research" and len(parts) >= 4 and parts[3].endswith(".md"):
            return ArtifactSpec(type="research", feature=feature, name=parts[3].removesuffix(".md"))
        if tail == "reviews" and len(parts) >= 4 and parts[3].endswith(".md"):
            return ArtifactSpec(type="review", feature=feature, name=parts[3].removesuffix(".md"))

    if len(parts) >= 2 and parts[0] == "research" and parts[-1].endswith(".md"):
        return ArtifactSpec(type="research", name=parts[-1].removesuffix(".md"))

    if len(parts) >= 2 and parts[0] == "plans" and parts[-1].endswith(".md"):
        return ArtifactSpec(type="plan", name=parts[-1].removesuffix(".md"))

    if len(parts) == 2 and parts[0] == "domains" and parts[1].endswith(".json"):
        return ArtifactSpec(type="domain", name=parts[1].removesuffix(".json"))

    if len(parts) == 2 and parts[0] == "context" and parts[1].endswith(".md"):
        return ArtifactSpec(type="pattern", name=parts[1].removesuffix(".md"))

    return None


def parse_existing_frontmatter(text: str) -> tuple[dict, str]:
    """Return (frontmatter_dict, body). Empty dict if none found."""
    if not text.startswith("---\n"):
        return {}, text
    end = text.find("\n---\n", 4)
    if end == -1:
        return {}, text
    block = text[4:end]
    body = text[end + 5:]
    fm = {}
    for line in block.split("\n"):
        line = line.rstrip()
        if not line or line.startswith("#"):
            continue
        if ":" in line:
            k, _, v = line.partition(":")
            fm[k.strip()] = v.strip()
    return fm, body


def render_frontmatter(fm: dict) -> str:
    """Render a frontmatter dict back to text. Preserves insertion order."""
    keys_order = ["type", "feature", "repo", "branch", "domain", "status",
                  "name", "created", "updated", "tags"]
    lines = ["---"]
    seen = set()
    for k in keys_order:
        if k in fm:
            lines.append(f"{k}: {fm[k]}")
            seen.add(k)
    for k, v in fm.items():
        if k not in seen:
            lines.append(f"{k}: {v}")
    lines.append("---\n")
    return "\n".join(lines)


def needs_backfill(fm: dict) -> bool:
    return not REQUIRED_FIELDS.issubset(fm.keys())


def build_frontmatter(spec: ArtifactSpec, repo: str, branch: str,
                      existing: dict, mtime_iso: str) -> dict:
    fm = dict(existing)
    fm.setdefault("type", spec.type)
    if spec.feature:
        fm.setdefault("feature", spec.feature)
    else:
        fm.setdefault("repo", repo)
    fm.setdefault("branch", branch)
    if spec.domain:
        fm.setdefault("domain", spec.domain)
    if spec.name and "name" not in fm:
        fm["name"] = spec.name
    fm.setdefault("status", DEFAULT_STATUS)
    fm.setdefault("created", mtime_iso)
    fm["updated"] = mtime_iso
    return fm


def backfill_md(path: Path, spec: ArtifactSpec, repo: str, branch: str,
                dry_run: bool) -> bool:
    text = path.read_text()
    fm, body = parse_existing_frontmatter(text)
    if not needs_backfill(fm) and not (spec.feature and "feature" not in fm):
        return False

    mtime = datetime.fromtimestamp(path.stat().st_mtime, tz=timezone.utc).strftime("%Y-%m-%d")
    new_fm = build_frontmatter(spec, repo, branch, fm, mtime)
    new_text = render_frontmatter(new_fm) + body

    if dry_run:
        print(f"  [dry-run] would stamp {path}")
        return True

    path.write_text(new_text)
    print(f"  stamped {path}")
    return True


def backfill_json(path: Path, spec: ArtifactSpec, repo: str, branch: str,
                  dry_run: bool) -> bool:
    try:
        data = json.loads(path.read_text())
    except json.JSONDecodeError as e:
        print(f"  [error] {path}: {e}", file=sys.stderr)
        return False
    if not isinstance(data, dict):
        return False

    required_present = all(k in data for k in REQUIRED_FIELDS)
    if required_present:
        return False

    mtime = datetime.fromtimestamp(path.stat().st_mtime, tz=timezone.utc).strftime("%Y-%m-%d")
    merged = build_frontmatter(spec, repo, branch, {}, mtime)
    for k, v in merged.items():
        data.setdefault(k, v)

    if dry_run:
        print(f"  [dry-run] would stamp {path}")
        return True

    path.write_text(json.dumps(data, indent=2) + "\n")
    print(f"  stamped {path}")
    return True


def walk_artifacts(workspace_dir: Path):
    for path in workspace_dir.rglob("*"):
        if not path.is_file():
            continue
        if any(part.startswith(".") for part in path.relative_to(workspace_dir).parts):
            continue
        if path.suffix not in (".md", ".json", ".yaml", ".yml"):
            continue
        yield path


def main():
    parser = argparse.ArgumentParser(description="Backfill YAML frontmatter in .giantmem/ artifacts")
    parser.add_argument("workspace_dir", nargs="?", default=".",
                        help="Path to .giantmem/ or its parent (defaults to cwd)")
    parser.add_argument("--dry-run", action="store_true", help="Print actions, write nothing")
    parser.add_argument("--apply-to", default="",
                        help="Comma-separated type filter (e.g. 'proposal,delta-spec'). Empty = all.")
    args = parser.parse_args()

    workspace_dir = Path(args.workspace_dir).resolve()
    if workspace_dir.name != ".giantmem":
        candidate = workspace_dir / ".giantmem"
        if candidate.exists():
            workspace_dir = candidate
    if not workspace_dir.exists():
        print(f"error: .giantmem/ not found at {workspace_dir}", file=sys.stderr)
        sys.exit(1)

    repo, branch = detect_repo(workspace_dir)
    type_filter = {t.strip() for t in args.apply_to.split(",") if t.strip()}

    print(f"workspace: {workspace_dir}")
    print(f"repo: {repo}, branch: {branch}")
    if type_filter:
        print(f"filter: {sorted(type_filter)}")
    print()

    stamped = 0
    skipped = 0
    orphans = []

    for path in walk_artifacts(workspace_dir):
        spec = classify(path, workspace_dir)
        if spec is None:
            orphans.append(path)
            continue
        if type_filter and spec.type not in type_filter:
            skipped += 1
            continue
        if path.suffix == ".json":
            changed = backfill_json(path, spec, repo, branch, args.dry_run)
        else:
            changed = backfill_md(path, spec, repo, branch, args.dry_run)
        if changed:
            stamped += 1

    print()
    print(f"stamped: {stamped}")
    print(f"already-clean / filtered: {skipped}")
    if orphans:
        print(f"orphans (unclassified, no frontmatter applied): {len(orphans)}")
        for o in orphans[:20]:
            print(f"  {o.relative_to(workspace_dir)}")
        if len(orphans) > 20:
            print(f"  ... and {len(orphans) - 20} more")

    if args.dry_run:
        print("\n(dry-run: no files written)")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""
Migrate legacy features/{name}/spec.md to features/{name}/proposal.md.

Usage:
    migrate_spec_to_proposal.py [workspace_dir] [--dry-run] [--feature NAME]
                                [--symlink-days N] [--no-split]

Per artifact_registry decision 3 (rename feature-spec -> proposal):

1. For each .giantmem/features/{name}/spec.md:
   - If proposal.md exists -> skip (idempotent)
   - Parse frontmatter + body
   - If body has '## Requirements' block: split into
     .giantmem/features/{name}/specs/{auto-domain}/spec.md (delta-spec, ADDED Requirements)
   - Rename spec.md -> proposal.md, update frontmatter type
   - Create spec.md symlink -> proposal.md (muscle-memory back-compat, 30 days)

Auto-domain default: feature name. Override with --domain <name>.

Run backfill_frontmatter.py FIRST so spec.md has frontmatter before split.
"""

import os
import re
import sys
import argparse
from pathlib import Path
from datetime import datetime, timezone


def parse_frontmatter(text: str) -> tuple[dict, str, str]:
    """Return (frontmatter_dict, frontmatter_raw_block, body)."""
    if not text.startswith("---\n"):
        return {}, "", text
    end = text.find("\n---\n", 4)
    if end == -1:
        return {}, "", text
    block_raw = text[:end + 5]
    block_inner = text[4:end]
    body = text[end + 5:]
    fm = {}
    for line in block_inner.split("\n"):
        line = line.rstrip()
        if not line or line.startswith("#"):
            continue
        if ":" in line:
            k, _, v = line.partition(":")
            fm[k.strip()] = v.strip()
    return fm, block_raw, body


def render_frontmatter(fm: dict) -> str:
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


def split_requirements_section(body: str) -> tuple[str, str]:
    """
    Find a '## Requirements' (or 'Acceptance Criteria') section and split it out.
    Returns (body_without_section, requirements_block) — requirements_block is empty if none.

    Section starts at the heading line, ends at the next H2 ('## ') or EOF.
    """
    pattern = re.compile(r"^(##\s+(?:Requirements|Acceptance Criteria)\b.*?)$",
                         re.MULTILINE | re.IGNORECASE)
    m = pattern.search(body)
    if not m:
        return body, ""

    start = m.start()
    rest = body[m.end():]
    next_h2 = re.search(r"^##\s+", rest, re.MULTILINE)
    if next_h2:
        end = m.end() + next_h2.start()
    else:
        end = len(body)

    section = body[start:end].rstrip()
    new_body = (body[:start] + body[end:]).rstrip() + "\n"
    return new_body, section


def make_delta_spec(req_section: str, feature: str, domain: str,
                    repo: str, branch: str, today: str) -> str:
    """Convert a legacy Requirements block into ADDED Requirements delta-spec."""
    if not req_section.startswith("## "):
        req_section = "## " + req_section.lstrip("# ").strip()

    body_lines = req_section.splitlines()
    if body_lines:
        body_lines[0] = "## ADDED Requirements"
    body = "\n".join(body_lines).rstrip() + "\n"

    body += "\n## MODIFIED Requirements\n\n(empty)\n"
    body += "\n## REMOVED Requirements\n\n(empty)\n"

    fm = {
        "type": "delta-spec",
        "feature": feature,
        "domain": domain,
        "status": "ready",
        "repo": repo,
        "branch": branch,
        "created": today,
        "updated": today,
    }
    return render_frontmatter(fm) + f"\n# Delta for {domain}\n\n" + body


def detect_repo(workspace_dir: Path) -> tuple[str, str]:
    import subprocess
    repo_root = workspace_dir.parent if workspace_dir.name == ".giantmem" else workspace_dir
    repo_name = repo_root.name
    branch = "unknown"
    try:
        r = subprocess.run(["git", "-C", str(repo_root), "rev-parse", "--abbrev-ref", "HEAD"],
                           capture_output=True, text=True, check=False)
        if r.returncode == 0:
            branch = r.stdout.strip()
    except FileNotFoundError:
        pass
    return repo_name, branch


def migrate_feature(feature_dir: Path, workspace_dir: Path, repo: str, branch: str,
                    domain_override: str, do_split: bool, symlink_days: int,
                    dry_run: bool) -> str:
    """Return status string: 'migrated' | 'skipped' | 'no-spec' | 'error'."""
    name = feature_dir.name
    spec_path = feature_dir / "spec.md"
    proposal_path = feature_dir / "proposal.md"

    if proposal_path.exists():
        return "skipped"
    if not spec_path.exists():
        return "no-spec"

    text = spec_path.read_text()
    fm, _, body = parse_frontmatter(text)

    today = datetime.now(tz=timezone.utc).strftime("%Y-%m-%d")
    fm.setdefault("created", today)
    fm["type"] = "proposal"
    fm.setdefault("feature", name)
    fm.setdefault("repo", repo)
    fm.setdefault("branch", branch)
    fm.setdefault("status", "ready")
    fm["updated"] = today

    new_body, req_section = (body, "") if not do_split else split_requirements_section(body)
    domain = domain_override or name

    delta_path = None
    if req_section.strip():
        delta_dir = feature_dir / "specs" / domain
        delta_path = delta_dir / "spec.md"
        if delta_path.exists():
            req_section = ""  # don't overwrite

    proposal_text = render_frontmatter(fm) + new_body

    if dry_run:
        print(f"  [dry-run] {name}: spec.md -> proposal.md")
        if req_section.strip() and delta_path is not None:
            print(f"  [dry-run] {name}: extract Requirements -> {delta_path.relative_to(workspace_dir)}")
        return "migrated"

    proposal_path.write_text(proposal_text)

    if req_section.strip() and delta_path is not None:
        delta_path.parent.mkdir(parents=True, exist_ok=True)
        delta_path.write_text(make_delta_spec(req_section, name, domain, repo, branch, today))
        print(f"  {name}: extracted Requirements -> {delta_path.relative_to(workspace_dir)}")

    spec_path.unlink()
    try:
        os.symlink("proposal.md", str(spec_path))
        print(f"  {name}: spec.md -> proposal.md (symlink left for ~{symlink_days}d back-compat)")
    except OSError as e:
        print(f"  {name}: symlink failed ({e}); proposal.md created without back-compat link", file=sys.stderr)

    return "migrated"


def main():
    parser = argparse.ArgumentParser(description="Migrate legacy spec.md -> proposal.md per feature")
    parser.add_argument("workspace_dir", nargs="?", default=".")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--feature", default="", help="Only migrate this feature")
    parser.add_argument("--domain", default="", help="Override auto-domain (default: feature name)")
    parser.add_argument("--no-split", action="store_true",
                        help="Do not extract Requirements into delta-spec")
    parser.add_argument("--symlink-days", type=int, default=30,
                        help="Informational only — symlink left for muscle-memory back-compat")
    args = parser.parse_args()

    workspace_dir = Path(args.workspace_dir).resolve()
    if workspace_dir.name != ".giantmem":
        candidate = workspace_dir / ".giantmem"
        if candidate.exists():
            workspace_dir = candidate
    features_dir = workspace_dir / "features"
    if not features_dir.exists():
        print(f"error: {features_dir} not found", file=sys.stderr)
        sys.exit(1)

    repo, branch = detect_repo(workspace_dir)
    print(f"workspace: {workspace_dir}")
    print(f"repo: {repo}, branch: {branch}")
    print()

    counts = {"migrated": 0, "skipped": 0, "no-spec": 0, "error": 0}
    targets = ([features_dir / args.feature] if args.feature
               else sorted(p for p in features_dir.iterdir() if p.is_dir()))

    for feature_dir in targets:
        if not feature_dir.exists() or not feature_dir.is_dir():
            continue
        if feature_dir.name.startswith("_"):
            continue
        status = migrate_feature(feature_dir, workspace_dir, repo, branch,
                                 args.domain, not args.no_split,
                                 args.symlink_days, args.dry_run)
        counts[status] = counts.get(status, 0) + 1

    print()
    for k in ("migrated", "skipped", "no-spec", "error"):
        print(f"{k}: {counts.get(k, 0)}")

    if args.dry_run:
        print("\n(dry-run: no files written)")


if __name__ == "__main__":
    main()

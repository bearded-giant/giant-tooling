#!/usr/bin/env python3
"""
Migrate existing .giantmem/plans/ to .giantmem/features/ structure.

Usage:
    workspace-migrate-features.py [workspace_dir] [--dry-run] [--interactive]

This script:
1. Scans plans/ for existing plan files
2. Groups related plans into features (by naming patterns)
3. Extracts beta flags, config keys from content
4. Creates feature folders with spec.md, facts.md, meta.json
5. Updates _index.md
6. Moves processed plans to filebox/migrated_plans/

Run with --dry-run first to see what would be created.
"""

import os
import sys
import re
import json
import shutil
from pathlib import Path
from datetime import datetime
from dataclasses import dataclass, field
from typing import Optional

@dataclass
class FeatureCandidate:
    name: str
    source_files: list = field(default_factory=list)
    status: str = "complete"
    beta_flag: str = ""
    config_keys: list = field(default_factory=list)
    builds_on: str = ""
    purpose: str = ""
    key_files: list = field(default_factory=list)
    test_commands: list = field(default_factory=list)
    raw_content: str = ""


def slugify(name: str) -> str:
    """Convert name to kebab-case slug."""
    name = name.lower()
    name = re.sub(r'[_\s]+', '-', name)
    name = re.sub(r'[^a-z0-9-]', '', name)
    name = re.sub(r'-+', '-', name)
    return name.strip('-')


def extract_beta_flags(content: str) -> list:
    """Extract beta flag names from content."""
    patterns = [
        r'beta[_\s]?flag[s]?[:\s]+[`\'"]?(\w+)[`\'"]?',
        r'enable_\w+',
        r'feature[_\s]?flag[s]?[:\s]+[`\'"]?(\w+)[`\'"]?',
    ]
    flags = set()
    for pattern in patterns:
        matches = re.findall(pattern, content, re.IGNORECASE)
        for match in matches:
            if isinstance(match, tuple):
                match = match[0] if match[0] else match[1] if len(match) > 1 else ''
            if match and match.startswith('enable_'):
                flags.add(match)
    return list(flags)


def extract_config_keys(content: str) -> list:
    """Extract config/env keys from content."""
    patterns = [
        r'[A-Z][A-Z0-9_]{2,}(?:_[A-Z0-9]+)+',  # UPPER_CASE_KEYS
    ]
    keys = set()
    for pattern in patterns:
        matches = re.findall(pattern, content)
        for match in matches:
            # filter out common non-config patterns
            if not any(skip in match for skip in ['HTTP_', 'API_', 'GET_', 'POST_', 'TODO', 'NOTE', 'IMPORTANT']):
                if any(hint in match for hint in ['SECRET', 'KEY', 'TOKEN', 'TTL', 'URL', 'HOST', 'PORT', 'DB_', 'REDIS_', 'JWT_']):
                    keys.add(match)
    return list(keys)


def extract_test_commands(content: str) -> list:
    """Extract test commands from content."""
    patterns = [
        r'docker compose run[^\n]+pytest[^\n]+',
        r'pytest[^\n]+',
    ]
    commands = []
    for pattern in patterns:
        matches = re.findall(pattern, content)
        commands.extend(matches)
    return list(set(commands))[:3]  # limit to 3


def extract_purpose(content: str, filename: str) -> str:
    """Extract purpose/description from content."""
    # look for ## Purpose, ## Overview, or first paragraph after title
    patterns = [
        r'##\s*(?:Purpose|Overview|Summary)\s*\n+([^\n#]+)',
        r'#[^\n]+\n+([^\n#]+)',
    ]
    for pattern in patterns:
        match = re.search(pattern, content)
        if match:
            purpose = match.group(1).strip()
            if len(purpose) > 20 and not purpose.startswith('<!--'):
                return purpose[:200]
    return f"Migrated from {filename}"


def analyze_plans_directory(scratch_dir: Path) -> dict:
    """Analyze plans/ and group into feature candidates."""
    plans_dir = scratch_dir / "plans"
    if not plans_dir.exists():
        return {}

    # group files by feature name patterns
    feature_groups = {}

    for plan_file in plans_dir.glob("*.md"):
        if plan_file.name == "current.md":
            continue

        filename = plan_file.stem
        content = plan_file.read_text()

        # determine feature name from filename
        # patterns: jwt_session_cookie_plan.md, tranche1_llm_implementation.md
        feature_name = filename

        # strip common suffixes
        for suffix in ['_plan', '_implementation', '_design', '_analysis', '_notes']:
            if feature_name.endswith(suffix):
                feature_name = feature_name[:-len(suffix)]

        # handle tranche patterns -> combine into parent feature
        tranche_match = re.match(r'tranche(\d+)_(.+)', feature_name)
        if tranche_match:
            feature_name = tranche_match.group(2)

        feature_slug = slugify(feature_name)

        if feature_slug not in feature_groups:
            feature_groups[feature_slug] = FeatureCandidate(name=feature_slug)

        candidate = feature_groups[feature_slug]
        candidate.source_files.append(plan_file.name)
        candidate.raw_content += f"\n\n--- {plan_file.name} ---\n{content}"

        # extract metadata
        flags = extract_beta_flags(content)
        if flags:
            candidate.beta_flag = flags[0]  # use first found

        config = extract_config_keys(content)
        candidate.config_keys.extend(config)

        tests = extract_test_commands(content)
        candidate.test_commands.extend(tests)

        if not candidate.purpose:
            candidate.purpose = extract_purpose(content, plan_file.name)

    # dedupe
    for candidate in feature_groups.values():
        candidate.config_keys = list(set(candidate.config_keys))
        candidate.test_commands = list(set(candidate.test_commands))[:3]

    return feature_groups


def create_feature_folder(scratch_dir: Path, candidate: FeatureCandidate, dry_run: bool = False) -> bool:
    """Create feature folder structure."""
    feature_dir = scratch_dir / "features" / candidate.name

    if dry_run:
        print(f"  Would create: features/{candidate.name}/")
        print(f"    spec.md, facts.md, meta.json")
        print(f"    From: {', '.join(candidate.source_files)}")
        if candidate.beta_flag:
            print(f"    Beta flag: {candidate.beta_flag}")
        return True

    feature_dir.mkdir(parents=True, exist_ok=True)

    # create spec.md
    spec_content = f"""# Feature: {candidate.name.replace('-', ' ').title()}

builds_on: {candidate.builds_on or "none"}
status: {candidate.status}
created: {datetime.now().strftime('%Y-%m-%d')}
migrated_from: {', '.join(candidate.source_files)}

## Purpose

{candidate.purpose}

## Scope

<!-- extracted from migration - review and update -->

## Key Decisions

<!-- review migrated content for decisions -->

## Acceptance Criteria

- [ ] review migrated content

## Files Modified

<!-- update based on actual implementation -->
"""
    (feature_dir / "spec.md").write_text(spec_content)

    # create facts.md
    facts_content = f"""# {candidate.name} facts

## Identifiers

beta_flag: {candidate.beta_flag}
config_keys:
"""
    for key in candidate.config_keys[:5]:
        facts_content += f"  - {key}\n"
    if not candidate.config_keys:
        facts_content += "  - \n"

    facts_content += """
## Endpoints

affected:
  -
new:
  -

## Key Files

"""
    for f in candidate.key_files[:5]:
        facts_content += f"- {f}\n"
    if not candidate.key_files:
        facts_content += "- \n"

    facts_content += """
## Test Commands

```bash
"""
    for cmd in candidate.test_commands:
        facts_content += f"{cmd}\n"
    if not candidate.test_commands:
        facts_content += "# add test commands\n"
    facts_content += "```\n"

    (feature_dir / "facts.md").write_text(facts_content)

    # create meta.json
    meta = {
        "name": candidate.name,
        "status": candidate.status,
        "builds_on": [candidate.builds_on] if candidate.builds_on else [],
        "beta_flag": candidate.beta_flag,
        "created": datetime.now().strftime('%Y-%m-%d'),
        "migrated_from": candidate.source_files,
        "last_session": datetime.now().strftime('%Y-%m-%d')
    }
    (feature_dir / "meta.json").write_text(json.dumps(meta, indent=2))

    print(f"  Created: features/{candidate.name}/")
    return True


def update_feature_index(scratch_dir: Path, features: dict, dry_run: bool = False):
    """Update _index.md with migrated features."""
    index_file = scratch_dir / "features" / "_index.md"

    # build table rows
    rows = []
    quick_ref_flags = []

    for name, candidate in sorted(features.items()):
        row = f"| [{name}]({name}/) | {candidate.status} | "
        if candidate.beta_flag:
            row += f"`{candidate.beta_flag}`"
            quick_ref_flags.append(candidate.beta_flag)
        row += f" | {candidate.builds_on or '-'} |"
        rows.append(row)

    if dry_run:
        print("\n  Would update _index.md with:")
        for row in rows:
            print(f"    {row}")
        return

    content = f"""# Feature Index

<!-- Claude maintains this file. Use /list-features to display, /new-feature to create -->

## Active Features

| Feature | Status | Beta Flag | Builds On |
|---------|--------|-----------|-----------|
{chr(10).join(rows)}

## Quick Reference

beta_flags:
"""
    for flag in quick_ref_flags:
        content += f"  - {flag}\n"

    content += """
<!-- add other quick reference items as needed -->
"""

    index_file.write_text(content)
    print(f"  Updated: features/_index.md")


def archive_migrated_plans(scratch_dir: Path, features: dict, dry_run: bool = False):
    """Move migrated plan files to filebox/migrated_plans/."""
    archive_dir = scratch_dir / "filebox" / "migrated_plans"

    all_source_files = set()
    for candidate in features.values():
        all_source_files.update(candidate.source_files)

    if dry_run:
        print(f"\n  Would archive {len(all_source_files)} plan files to filebox/migrated_plans/")
        return

    archive_dir.mkdir(parents=True, exist_ok=True)

    plans_dir = scratch_dir / "plans"
    for filename in all_source_files:
        src = plans_dir / filename
        if src.exists():
            shutil.move(str(src), str(archive_dir / filename))
            print(f"  Archived: {filename}")


def main():
    import argparse
    parser = argparse.ArgumentParser(description="Migrate plans to features structure")
    parser.add_argument("scratch_dir", nargs="?", default=".", help="Path to .giantmem/ or its parent")
    parser.add_argument("--dry-run", action="store_true", help="Show what would be done")
    parser.add_argument("--interactive", "-i", action="store_true", help="Confirm each feature")
    parser.add_argument("--no-archive", action="store_true", help="Don't archive migrated plans")
    args = parser.parse_args()

    # find workspace dir (.giantmem preferred, scratch as fallback)
    scratch_dir = Path(args.scratch_dir)
    if scratch_dir.name not in (".giantmem", "scratch"):
        if (scratch_dir / ".giantmem").exists():
            scratch_dir = scratch_dir / ".giantmem"
        else:
            scratch_dir = scratch_dir / "scratch"

    if not scratch_dir.exists():
        print(f"Error: workspace directory not found at {scratch_dir}")
        sys.exit(1)

    print(f"Analyzing: {scratch_dir}")

    # ensure features dir exists
    features_dir = scratch_dir / "features"
    if not args.dry_run:
        features_dir.mkdir(exist_ok=True)

    # analyze and group plans
    features = analyze_plans_directory(scratch_dir)

    if not features:
        print("No plan files found to migrate.")
        sys.exit(0)

    print(f"\nFound {len(features)} feature candidates:\n")

    # create features
    created = {}
    for name, candidate in features.items():
        print(f"\n{name}:")

        if args.interactive and not args.dry_run:
            response = input(f"  Create feature '{name}'? [Y/n/s(kip)] ").strip().lower()
            if response == 'n':
                print("  Skipped (user)")
                continue
            if response == 's':
                print("  Skipped")
                continue

        if create_feature_folder(scratch_dir, candidate, args.dry_run):
            created[name] = candidate

    if created:
        print("\n--- Updating index ---")
        update_feature_index(scratch_dir, created, args.dry_run)

        if not args.no_archive:
            print("\n--- Archiving migrated plans ---")
            archive_migrated_plans(scratch_dir, created, args.dry_run)

    print("\n--- Migration complete ---")
    if args.dry_run:
        print("(dry-run mode - no changes made)")
    else:
        print(f"Created {len(created)} feature folders")
        print("Review generated files and update spec.md/facts.md as needed")


if __name__ == "__main__":
    main()

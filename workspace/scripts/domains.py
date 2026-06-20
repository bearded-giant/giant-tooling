#!/usr/bin/env python3
"""Read-only domain knowledge-base queries. Replaces the LLM-walked /list-domains
and /search-domains procedures with one process each. Reads .giantmem/domains/.
"""
import argparse
import json
import os
import sys
from datetime import date, datetime

STALE_DAYS = 7


def load_index(cwd):
    p = os.path.join(cwd, ".giantmem", "domains", "_index.json")
    if not os.path.exists(p):
        print("No domains indexed yet. Run /plan-feature to explore code domains.")
        sys.exit(0)
    raw = json.load(open(p))
    if isinstance(raw, dict) and "domains" in raw:
        raw = raw["domains"]
    if isinstance(raw, dict):
        items = [{**v, "name": v.get("name", k)} for k, v in raw.items()]
    else:
        items = list(raw)
    return items, os.path.dirname(p)


def days_since(d):
    if not d:
        return None
    try:
        return (date.today() - datetime.strptime(d[:10], "%Y-%m-%d").date()).days
    except ValueError:
        return None


def fld(dom, *keys):
    for k in keys:
        if dom.get(k):
            return dom[k]
    return ""


def cmd_list(a, cwd):
    items, ddir = load_index(cwd)
    if not items:
        print("No domains indexed yet.")
        return
    if not a.verbose:
        print(f"Code Domains ({len(items)} indexed)\n")
        print("| Domain | Description | Explored | Features |")
        print("|--------|-------------|----------|----------|")
        for d in items:
            feats = d.get("features") or d.get("explored_for_features") or []
            print(f"| {d.get('name','?')} | {fld(d,'description')} | "
                  f"{fld(d,'last_explored','explored')} | {', '.join(feats) if isinstance(feats,list) else feats} |")
    else:
        for d in items:
            name = d.get("name", "?")
            exp = fld(d, "last_explored", "explored")
            ds = days_since(exp)
            stale = f"  [STALE - {ds} days]" if ds is not None and ds > STALE_DAYS else ""
            paths = d.get("key_paths") or d.get("paths") or []
            feats = d.get("features") or d.get("explored_for_features") or []
            # coverage counts may live in the index or require the domain json
            nkf = d.get("key_files_count")
            nep = d.get("entry_points_count")
            if nkf is None or nep is None:
                dj = os.path.join(ddir, f"{name}.json")
                if os.path.exists(dj):
                    try:
                        full = json.load(open(dj))
                        nkf = len(full.get("key_files", []))
                        nep = len(full.get("entry_points", []))
                    except (json.JSONDecodeError, OSError):
                        pass
            print(f"{name} - {fld(d,'description')}{stale}")
            print(f"  explored: {exp}" + (f" ({ds} days ago)" if ds is not None else ""))
            if paths:
                print(f"  paths: {', '.join(paths) if isinstance(paths,list) else paths}")
            if nkf is not None or nep is not None:
                print(f"  coverage: {nkf or 0} key files, {nep or 0} entry points")
            if feats:
                print(f"  features: {', '.join(feats) if isinstance(feats,list) else feats}")
            print()
    print("\nLoad a domain: read .giantmem/domains/{name}.json")
    print("Search domains: /search-domains <query>")
    print("Refresh stale: /update-domains --all-stale")


def _search_json(obj, q, path=""):
    """yield (jsonpath, matched-value-snippet) for q (case-insensitive) anywhere in obj."""
    ql = q.lower()
    if isinstance(obj, dict):
        for k, v in obj.items():
            yield from _search_json(v, q, f"{path}.{k}" if path else k)
    elif isinstance(obj, list):
        for v in obj:
            yield from _search_json(v, q, path)
    elif isinstance(obj, str):
        if ql in obj.lower():
            snip = obj if len(obj) <= 90 else obj[:87] + "..."
            yield (path, snip)


def cmd_search(a, cwd):
    query = a.query
    items, ddir = load_index(cwd)
    ql = query.lower()
    # quick filter from index (description + key_paths); fall back to all on no hit
    cand = []
    for d in items:
        hay = (fld(d, "description") + " " + " ".join(d.get("key_paths") or d.get("paths") or [])).lower()
        if ql in hay:
            cand.append(d.get("name"))
    names = cand or [d.get("name") for d in items]

    results = []
    for name in names:
        dj = os.path.join(ddir, f"{name}.json")
        if not os.path.exists(dj):
            continue
        try:
            full = json.load(open(dj))
        except (json.JSONDecodeError, OSError):
            continue
        hits = list(_search_json(full, query))
        if hits:
            results.append((name, hits))

    print(f'Search: "{query}"\n')
    if not results:
        print("No matches.")
        print("- check the term (typo?)")
        print("- /list-domains to see what's indexed")
        print("- /plan-feature to explore new areas")
        return
    shown = results[:5]
    for name, hits in shown:
        print(f"{name} (.giantmem/domains/{name}.json)")
        seen = set()
        for jp, snip in hits[:6]:
            key = (jp, snip)
            if key in seen:
                continue
            seen.add(key)
            print(f"  {jp}: {snip}")
        print()
    extra = len(results) - len(shown)
    tail = f" (+{extra} more)" if extra > 0 else ""
    print(f"{len(results)} domain(s) matched{tail}. Load with: read .giantmem/domains/{shown[0][0]}.json")
    if a.load:
        print("\n--load: read the matching domain JSONs above for full detail.")


def main():
    p = argparse.ArgumentParser(prog="domains")
    p.add_argument("--cwd", default=os.getcwd())
    sub = p.add_subparsers(dest="verb", required=True)
    li = sub.add_parser("list")
    li.add_argument("--verbose", action="store_true")
    li.set_defaults(fn=cmd_list)
    se = sub.add_parser("search")
    se.add_argument("query")
    se.add_argument("--load", action="store_true")
    se.set_defaults(fn=cmd_search)
    a = p.parse_args()
    a.fn(a, a.cwd)


if __name__ == "__main__":
    main()

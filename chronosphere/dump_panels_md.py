#!/usr/bin/env python3
"""Dump panels from a dashboard YAML to a markdown file with PromQL queries.

Usage:
    python3 dump_panels_md.py <dashboard.yaml> <out.md>
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path


def main() -> None:
    if len(sys.argv) != 3:
        sys.exit("usage: dump_panels_md.py <dashboard.yaml> <out.md>")
    yaml_path = Path(sys.argv[1])
    out_path = Path(sys.argv[2])

    text = yaml_path.read_text()
    m = re.search(r"(?m)^  dashboard_json: '(.+)'\s*$", text, re.DOTALL)
    if not m:
        sys.exit(f"no dashboard_json line in {yaml_path}")
    raw = m.group(1).replace("''", "'")
    dash = json.loads(raw)

    slug_m = re.search(r"(?m)^  slug:\s*(\S+)", text)
    name_m = re.search(r"(?m)^  name:\s*(.+)$", text)
    slug = slug_m.group(1) if slug_m else "?"
    display_name = name_m.group(1).strip() if name_m else "?"

    spec = dash["spec"]
    panels = spec["panels"]
    items = spec["layouts"][0]["spec"]["items"]

    ordered_keys: list[str] = []
    seen: set[str] = set()
    for it in items:
        key = it["content"]["$ref"].split("/")[-1]
        if key not in seen and key in panels:
            ordered_keys.append(key)
            seen.add(key)
    for k in panels:
        if k not in seen:
            ordered_keys.append(k)

    lines: list[str] = []
    lines.append(f"# {display_name} dashboard panels")
    lines.append("")
    lines.append(f"- slug: `{slug}`")
    lines.append(f"- URL: https://recharge.chronosphere.io/dashboards/{slug}")
    lines.append(f"- source YAML: `{yaml_path}`")
    lines.append("")
    lines.append("---")
    lines.append("")

    for key in ordered_keys:
        p = panels[key]
        spec_p = p.get("spec", {})
        display = spec_p.get("display", {})
        name = display.get("name", key)
        desc = display.get("description", "").strip()
        queries = spec_p.get("queries", [])

        lines.append(f"## {name}")
        lines.append("")
        lines.append(f"- panel key: `{key}`")
        if desc:
            lines.append(f"- description: {desc}")
        plugin = spec_p.get("plugin", {})
        ptype = plugin.get("kind", "?")
        unit = plugin.get("spec", {}).get("y_axis", {}).get("unit", {}).get("kind") if ptype == "TimeSeriesChart" else None
        lines.append(f"- chart type: `{ptype}`" + (f" (y unit: {unit})" if unit else ""))
        lines.append("")
        for i, q in enumerate(queries, 1):
            qspec = q.get("spec", {}).get("plugin", {}).get("spec", {})
            promql = qspec.get("query", "")
            series_fmt = qspec.get("series_name_format")
            lines.append(f"### query {i}")
            lines.append("")
            lines.append("```promql")
            lines.append(promql)
            lines.append("```")
            lines.append("")
            if series_fmt:
                lines.append(f"series format: `{series_fmt}`")
                lines.append("")
        lines.append("---")
        lines.append("")

    out_path.write_text("\n".join(lines))
    print(f"wrote {out_path} ({len(ordered_keys)} panels)")


if __name__ == "__main__":
    main()

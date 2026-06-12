#!/usr/bin/env python3
"""Inject chat-api-specific HTTP request panels into the generated YAML.

Runs after `gen_dashboard.py --from-registry chat-api`. Reads the YAML in
CWD, mutates the embedded dashboard_json to add 2 panels for the
`chat_api_api_request_*` metrics emitted by ai-tooling/recharge-chat-api
MR !3 (Flask before/after_request hooks → DataDog statsd).

Panels added below the existing envoy + pod-count layout:
  - httpReqMinByStatusClass — req/min by status_class
  - httpLatencyP95ByRoute   — p95 latency by route

Apply: chronoctl dashboards update -f chat-api.yaml
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

YAML_PATH = Path("chat-api.yaml")

PANELS = {
    "httpReqMinByStatusClass": {
        "kind": "Panel",
        "spec": {
            "display": {
                "description": "HTTP req/min by status class — chat_api_api_request_count (post MR !3)",
                "name": "HTTP req/min by status class",
            },
            "formulas": [],
            "links": [],
            "plugin": {
                "kind": "TimeSeriesChart",
                "spec": {
                    "legend": {"mode": "List", "position": "Bottom", "values": []},
                    "tooltip": {"mode": "nearby"},
                    "visual": {
                        "area_opacity": 0.3,
                        "line_width": 1.5,
                        "null_behavior": "NullAsZero",
                        "point_radius": 3,
                        "stack": "All",
                    },
                    "y_axis": {"min": None, "unit": {"abbreviate": True, "decimal_places": 0, "kind": "Decimal"}},
                },
            },
            "queries": [{
                "kind": "DataQuery",
                "spec": {
                    "plugin": {
                        "kind": "PrometheusTimeSeriesQuery",
                        "spec": {
                            "query": 'sum by (status_class) (rate(chat_api_api_request_count{env="$env"}[$__rate_interval])) * 60',
                            "series_name_format": "{{status_class}}",
                        },
                    },
                },
            }],
        },
    },
    "httpLatencyP95ByRoute": {
        "kind": "Panel",
        "spec": {
            "display": {
                "description": "p95 HTTP request latency by route — chat_api_api_request_duration_ms (post MR !3)",
                "name": "HTTP p95 latency by route",
            },
            "formulas": [],
            "links": [],
            "plugin": {
                "kind": "TimeSeriesChart",
                "spec": {
                    "legend": {"mode": "List", "position": "Bottom", "values": []},
                    "tooltip": {"mode": "nearby"},
                    "visual": {
                        "area_opacity": 0.3,
                        "display": "line",
                        "line_width": 1.5,
                        "null_behavior": "NullAsZero",
                        "point_radius": 3,
                        "show_points": "Never",
                    },
                    "y_axis": {"label": "", "min": None, "unit": {"kind": "Milliseconds"}},
                },
            },
            "queries": [{
                "kind": "DataQuery",
                "spec": {
                    "plugin": {
                        "kind": "PrometheusTimeSeriesQuery",
                        "spec": {
                            "query": 'histogram_quantile(0.95, sum by (le, route) (rate(chat_api_api_request_duration_ms_milliseconds_bucket{env="$env"}[$__rate_interval])))',
                            "series_name_format": "{{route}}",
                        },
                    },
                },
            }],
        },
    },
}

NEW_ITEMS = [
    {"content": {"$ref": "#/spec/panels/httpReqMinByStatusClass"}, "height": 5, "width": 12, "x": 0, "y": 25},
    {"content": {"$ref": "#/spec/panels/httpLatencyP95ByRoute"}, "height": 5, "width": 12, "x": 12, "y": 25},
]


def main() -> None:
    if not YAML_PATH.exists():
        sys.exit(f"missing {YAML_PATH}. run gen_dashboard.py --from-registry chat-api first.")

    text = YAML_PATH.read_text()
    m = re.search(r"(?m)^(  dashboard_json: ')(.+)('\s*)$", text, re.DOTALL)
    if not m:
        sys.exit("dashboard_json line not found")

    raw = m.group(2).replace("''", "'")
    dash = json.loads(raw)
    spec = dash["spec"]

    spec["panels"].update(PANELS)

    items = spec["layouts"][0]["spec"]["items"]
    existing_keys = {it["content"]["$ref"].split("/")[-1] for it in items}
    for item in NEW_ITEMS:
        key = item["content"]["$ref"].split("/")[-1]
        if key not in existing_keys:
            items.append(item)

    new_raw = json.dumps(dash, separators=(",", ":")).replace("'", "''")
    new_text = text[: m.start()] + m.group(1) + new_raw + m.group(3) + text[m.end():]
    YAML_PATH.write_text(new_text)
    print(f"injected {len(PANELS)} custom panels into {YAML_PATH}")
    print(f"apply: chronoctl dashboards update -f {YAML_PATH}")


if __name__ == "__main__":
    main()

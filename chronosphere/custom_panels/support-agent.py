#!/usr/bin/env python3
"""Inject support-agent-specific OTel panels into the generated dashboard YAML.

Runs after `gen_dashboard.py --from-registry support-agent`. Reads the YAML
in CWD, mutates the embedded dashboard_json to add 3 custom panels for
`support_agent_chat_*` metrics, writes back.

Custom panels added (below the existing envoy + pod-count layout):
  - chatDurationP95     — histogram_quantile p95 of chat duration
  - chatOutcomeRate     — rate by outcome label
  - chatIterationsAvg   — avg gauge across worker instances

Then run: chronoctl dashboards update -f support-agent.yaml
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

YAML_PATH = Path("support-agent.yaml")

PANELS = {
    "chatDurationP95": {
        "kind": "Panel",
        "spec": {
            "display": {"description": "p95 chat duration (ms) — support_agent_chat_duration_ms_milliseconds", "name": "Chat Duration p95"},
            "formulas": [],
            "links": [],
            "plugin": {
                "kind": "TimeSeriesChart",
                "spec": {
                    "tooltip": {"mode": "nearby"},
                    "visual": {"area_opacity": 0.3, "display": "line", "line_width": 1.5, "null_behavior": "NullAsZero", "point_radius": 3, "show_points": "Never"},
                    "y_axis": {"label": "", "min": None, "unit": {"kind": "Milliseconds"}},
                },
            },
            "queries": [{
                "kind": "DataQuery",
                "spec": {
                    "plugin": {
                        "kind": "PrometheusTimeSeriesQuery",
                        "spec": {
                            "query": 'histogram_quantile(0.95, sum by (le) (rate(support_agent_chat_duration_ms_milliseconds_bucket{env="$env"}[$__rate_interval])))',
                            "series_name_format": "p95",
                        },
                    },
                },
            }],
        },
    },
    "chatOutcomeRate": {
        "kind": "Panel",
        "spec": {
            "display": {"description": "rate of chat outcomes by outcome label — support_agent_chat_outcome", "name": "Chat Outcome Rate"},
            "formulas": [],
            "links": [],
            "plugin": {
                "kind": "TimeSeriesChart",
                "spec": {
                    "legend": {"mode": "List", "position": "Bottom", "values": []},
                    "tooltip": {"mode": "nearby"},
                    "visual": {"area_opacity": 0.3, "line_width": 1.5, "null_behavior": "NullAsZero", "point_radius": 3, "stack": "All"},
                    "y_axis": {"min": None, "unit": {"abbreviate": True, "decimal_places": 2, "kind": "Decimal"}},
                },
            },
            "queries": [{
                "kind": "DataQuery",
                "spec": {
                    "plugin": {
                        "kind": "PrometheusTimeSeriesQuery",
                        "spec": {
                            "query": 'sum by (outcome) (rate(support_agent_chat_outcome{env="$env"}[$__rate_interval]))',
                            "series_name_format": "{{outcome}}",
                        },
                    },
                },
            }],
        },
    },
    "chatIterationsAvg": {
        "kind": "Panel",
        "spec": {
            "display": {"description": "avg chat iterations across instances — support_agent_chat_iterations", "name": "Chat Iterations (avg)"},
            "formulas": [],
            "links": [],
            "plugin": {
                "kind": "TimeSeriesChart",
                "spec": {
                    "tooltip": {"mode": "nearby"},
                    "visual": {"area_opacity": 0.3, "line_width": 1.5, "null_behavior": "NullAsZero", "point_radius": 3},
                    "y_axis": {"min": None, "unit": {"abbreviate": True, "decimal_places": 1, "kind": "Decimal"}},
                },
            },
            "queries": [{
                "kind": "DataQuery",
                "spec": {
                    "plugin": {
                        "kind": "PrometheusTimeSeriesQuery",
                        "spec": {
                            "query": 'avg(support_agent_chat_iterations{env="$env"})',
                            "series_name_format": "avg iterations",
                        },
                    },
                },
            }],
        },
    },
    "httpReqMinByStatusClass": {
        "kind": "Panel",
        "spec": {
            "display": {"description": "HTTP req/min by status class — support_agent_api_request_count (post MR !508)", "name": "HTTP req/min by status class"},
            "formulas": [],
            "links": [],
            "plugin": {
                "kind": "TimeSeriesChart",
                "spec": {
                    "legend": {"mode": "List", "position": "Bottom", "values": []},
                    "tooltip": {"mode": "nearby"},
                    "visual": {"area_opacity": 0.3, "line_width": 1.5, "null_behavior": "NullAsZero", "point_radius": 3, "stack": "All"},
                    "y_axis": {"min": None, "unit": {"abbreviate": True, "decimal_places": 0, "kind": "Decimal"}},
                },
            },
            "queries": [{
                "kind": "DataQuery",
                "spec": {
                    "plugin": {
                        "kind": "PrometheusTimeSeriesQuery",
                        "spec": {
                            "query": 'sum by (status_class) (rate(support_agent_api_request_count{env="$env"}[$__rate_interval])) * 60',
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
            "display": {"description": "p95 HTTP request latency by route — support_agent_api_request_duration_ms (post MR !508)", "name": "HTTP p95 latency by route"},
            "formulas": [],
            "links": [],
            "plugin": {
                "kind": "TimeSeriesChart",
                "spec": {
                    "legend": {"mode": "List", "position": "Bottom", "values": []},
                    "tooltip": {"mode": "nearby"},
                    "visual": {"area_opacity": 0.3, "display": "line", "line_width": 1.5, "null_behavior": "NullAsZero", "point_radius": 3, "show_points": "Never"},
                    "y_axis": {"label": "", "min": None, "unit": {"kind": "Milliseconds"}},
                },
            },
            "queries": [{
                "kind": "DataQuery",
                "spec": {
                    "plugin": {
                        "kind": "PrometheusTimeSeriesQuery",
                        "spec": {
                            "query": 'histogram_quantile(0.95, sum by (le, route) (rate(support_agent_api_request_duration_ms_milliseconds_bucket{env="$env"}[$__rate_interval])))',
                            "series_name_format": "{{route}}",
                        },
                    },
                },
            }],
        },
    },
}

NEW_ITEMS = [
    {"content": {"$ref": "#/spec/panels/chatDurationP95"}, "height": 5, "width": 8, "x": 0, "y": 25},
    {"content": {"$ref": "#/spec/panels/chatOutcomeRate"}, "height": 5, "width": 8, "x": 8, "y": 25},
    {"content": {"$ref": "#/spec/panels/chatIterationsAvg"}, "height": 5, "width": 8, "x": 16, "y": 25},
    {"content": {"$ref": "#/spec/panels/httpReqMinByStatusClass"}, "height": 5, "width": 12, "x": 0, "y": 30},
    {"content": {"$ref": "#/spec/panels/httpLatencyP95ByRoute"}, "height": 5, "width": 12, "x": 12, "y": 30},
]


def main() -> None:
    if not YAML_PATH.exists():
        sys.exit(f"missing {YAML_PATH}. run gen_dashboard.py --from-registry support-agent first.")

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
    new_text = text[: m.start()] + m.group(1) + new_raw + m.group(3) + text[m.end() :]
    YAML_PATH.write_text(new_text)
    print(f"injected {len(PANELS)} custom panels into {YAML_PATH}")
    print(f"apply: chronoctl dashboards update -f {YAML_PATH}")


if __name__ == "__main__":
    main()

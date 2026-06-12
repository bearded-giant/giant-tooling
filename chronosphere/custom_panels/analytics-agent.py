#!/usr/bin/env python3
"""Inject analytics-agent-specific OTel panels into the generated dashboard YAML.

Runs after `gen_dashboard.py --from-registry analytics-agent`. Reads the
YAML in CWD, mutates the embedded dashboard_json to add panels for:

  Existing OTel metrics (populate immediately):
    - analyzeRateBySkill        analyze_requests_total{skill=...}
    - analyzeRateByStatus       analyze_requests_total{status=...}
    - llmCallsRate              llm_calls_total
    - llmCallDurationP95        llm_call_duration_seconds_bucket
    - sqlExecRate               sql_executions_total
    - sqlExecDurationP95        sql_execution_duration_seconds_bucket

  Post-MR !130 HTTP metrics (populate after deploy):
    - httpReqMinByStatusClass   http_requests_total{service_name=...}
    - httpLatencyP95ByRoute     http_request_duration_seconds_bucket

Apply: chronoctl dashboards update -f analytics-agent.yaml
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

YAML_PATH = Path("analytics-agent.yaml")
JOB_FILTER = 'job="analytics-agent"'
SVC_FILTER = 'service_name="analytics-agent"'


def _ts_panel(name: str, description: str, query: str, series_format: str, unit: dict | None = None, stack: bool = False) -> dict:
    visual = {
        "area_opacity": 0.3,
        "line_width": 1.5,
        "null_behavior": "NullAsZero",
        "point_radius": 3,
    }
    if stack:
        visual["stack"] = "All"
    return {
        "kind": "Panel",
        "spec": {
            "display": {"description": description, "name": name},
            "formulas": [],
            "links": [],
            "plugin": {
                "kind": "TimeSeriesChart",
                "spec": {
                    "legend": {"mode": "List", "position": "Bottom", "values": []},
                    "tooltip": {"mode": "nearby"},
                    "visual": visual,
                    "y_axis": {"min": None, "unit": unit or {"abbreviate": True, "decimal_places": 0, "kind": "Decimal"}},
                },
            },
            "queries": [{
                "kind": "DataQuery",
                "spec": {
                    "plugin": {
                        "kind": "PrometheusTimeSeriesQuery",
                        "spec": {"query": query, "series_name_format": series_format},
                    },
                },
            }],
        },
    }


PANELS = {
    "analyzeRateBySkill": _ts_panel(
        name="Analyze req/min by skill",
        description="rate of analyze_requests_total grouped by skill",
        query=f'sum by (skill) (rate(analyze_requests_total{{env="$env",{JOB_FILTER}}}[$__rate_interval])) * 60',
        series_format="{{skill}}",
        stack=True,
    ),
    "analyzeRateByStatus": _ts_panel(
        name="Analyze req/min by status",
        description="rate of analyze_requests_total grouped by status (success/error/unsupported)",
        query=f'sum by (status) (rate(analyze_requests_total{{env="$env",{JOB_FILTER}}}[$__rate_interval])) * 60',
        series_format="{{status}}",
        stack=True,
    ),
    "llmCallsRate": _ts_panel(
        name="LLM calls/min",
        description="rate of llm_calls_total",
        query=f'sum(rate(llm_calls_total{{env="$env",{JOB_FILTER}}}[$__rate_interval])) * 60',
        series_format="calls/min",
    ),
    "llmCallDurationP95": _ts_panel(
        name="LLM call p95 latency",
        description="p95 of llm_call_duration_seconds_bucket",
        query=f'histogram_quantile(0.95, sum by (le) (rate(llm_call_duration_seconds_bucket{{env="$env",{JOB_FILTER}}}[$__rate_interval])))',
        series_format="p95",
        unit={"kind": "Seconds"},
    ),
    "sqlExecRate": _ts_panel(
        name="SQL exec/min",
        description="rate of sql_executions_total (Snowflake)",
        query=f'sum(rate(sql_executions_total{{env="$env",{JOB_FILTER}}}[$__rate_interval])) * 60',
        series_format="execs/min",
    ),
    "sqlExecDurationP95": _ts_panel(
        name="SQL exec p95 latency",
        description="p95 of sql_execution_duration_seconds_bucket (Snowflake)",
        query=f'histogram_quantile(0.95, sum by (le) (rate(sql_execution_duration_seconds_bucket{{env="$env",{JOB_FILTER}}}[$__rate_interval])))',
        series_format="p95",
        unit={"kind": "Seconds"},
    ),
    "httpReqMinByStatusClass": _ts_panel(
        name="HTTP req/min by status class",
        description="HTTP req/min by status_class — http_requests_total (post MR !130)",
        query=f'sum by (status_class) (rate(http_requests_total{{env="$env",{SVC_FILTER}}}[$__rate_interval])) * 60',
        series_format="{{status_class}}",
        stack=True,
    ),
    "httpLatencyP95ByRoute": _ts_panel(
        name="HTTP p95 latency by route",
        description="p95 HTTP latency by route — http_request_duration_seconds_bucket (post MR !130)",
        query=f'histogram_quantile(0.95, sum by (le, route) (rate(http_request_duration_seconds_bucket{{env="$env",{SVC_FILTER}}}[$__rate_interval])))',
        series_format="{{route}}",
        unit={"kind": "Seconds"},
    ),
}

NEW_ITEMS = [
    {"content": {"$ref": "#/spec/panels/analyzeRateBySkill"}, "height": 5, "width": 12, "x": 0, "y": 25},
    {"content": {"$ref": "#/spec/panels/analyzeRateByStatus"}, "height": 5, "width": 12, "x": 12, "y": 25},
    {"content": {"$ref": "#/spec/panels/llmCallsRate"}, "height": 5, "width": 12, "x": 0, "y": 30},
    {"content": {"$ref": "#/spec/panels/llmCallDurationP95"}, "height": 5, "width": 12, "x": 12, "y": 30},
    {"content": {"$ref": "#/spec/panels/sqlExecRate"}, "height": 5, "width": 12, "x": 0, "y": 35},
    {"content": {"$ref": "#/spec/panels/sqlExecDurationP95"}, "height": 5, "width": 12, "x": 12, "y": 35},
    {"content": {"$ref": "#/spec/panels/httpReqMinByStatusClass"}, "height": 5, "width": 12, "x": 0, "y": 40},
    {"content": {"$ref": "#/spec/panels/httpLatencyP95ByRoute"}, "height": 5, "width": 12, "x": 12, "y": 40},
]


def main() -> None:
    if not YAML_PATH.exists():
        sys.exit(f"missing {YAML_PATH}. run gen_dashboard.py --from-registry analytics-agent first.")

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

#!/usr/bin/env python3
"""Check collector reconciliation health for a given trading date.

Usage examples:
  ./scripts/check_collector_reconcile.py
  ./scripts/check_collector_reconcile.py --date 20260403
  ./scripts/check_collector_reconcile.py --strict
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import sys
import textwrap
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Dict, Optional, Tuple


REPO_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_BASE_URL = "http://127.0.0.1:8080"
DEFAULT_REPORT_DIR = REPO_ROOT / "data" / "database" / "collector_reports"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Check whether collector reconciliation finished for a date and summarize health."
    )
    parser.add_argument(
        "--date",
        default=dt.date.today().strftime("%Y%m%d"),
        help="Target date in YYYYMMDD format. Defaults to today.",
    )
    parser.add_argument(
        "--base-url",
        default=DEFAULT_BASE_URL,
        help=f"Collector API base URL. Defaults to {DEFAULT_BASE_URL}.",
    )
    parser.add_argument(
        "--report-dir",
        default=str(DEFAULT_REPORT_DIR),
        help=f"Fallback reconcile report directory. Defaults to {DEFAULT_REPORT_DIR}.",
    )
    parser.add_argument(
        "--timeout",
        type=float,
        default=3.0,
        help="HTTP timeout in seconds. Defaults to 3.",
    )
    parser.add_argument(
        "--strict",
        action="store_true",
        help="Exit non-zero unless reconcile status is passed, no open gaps remain, and target coverage is complete.",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="Print raw merged JSON payload instead of the formatted summary.",
    )
    return parser.parse_args()


def fetch_json(url: str, timeout: float) -> Optional[Dict[str, Any]]:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as response:
            return json.loads(response.read().decode("utf-8"))
    except (urllib.error.URLError, TimeoutError, json.JSONDecodeError):
        return None


def unwrap_response(payload: Optional[Dict[str, Any]]) -> Optional[Dict[str, Any]]:
    if not payload:
        return None
    code = payload.get("code", payload.get("Code"))
    if code == 0:
        data = payload.get("data", payload.get("Data"))
        if isinstance(data, dict):
            return data
    return None


def load_report_from_file(report_dir: Path, date: str) -> Optional[Dict[str, Any]]:
    path = report_dir / f"reconcile-{date}.json"
    if not path.exists():
        return None
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError:
        return None


def normalize_status_payload(payload: Optional[Dict[str, Any]]) -> Dict[str, Any]:
    if not payload:
        return {}
    runtime = payload.get("runtime") or {}
    jobs = payload.get("jobs") or {}
    schedule = payload.get("schedule") or {}
    return {
        "runtime": runtime,
        "jobs": jobs,
        "schedule": schedule,
    }


def summarize_report(report: Dict[str, Any]) -> Tuple[int, int, int]:
    passed = 0
    partial = 0
    failed = 0
    for domain in report.get("domains", []):
        status = domain.get("status")
        if status == "reconciled":
            passed += 1
        elif status in {"partial", "best_effort", "unsupported_historical_rebuild", "skipped_non_trading_day"}:
            partial += 1
        else:
            failed += 1
    return passed, partial, failed


def domain_health_lines(report: Dict[str, Any]) -> list[str]:
    lines: list[str] = []
    domains = report.get("domains", [])
    interesting = [
        "metadata",
        "quote_snapshot",
        "kline",
        "trade_history",
        "order_history",
        "live_capture",
        "finance",
        "f10",
        "collector_gap",
    ]
    domain_map = {domain.get("domain"): domain for domain in domains}
    for name in interesting:
        item = domain_map.get(name)
        if not item:
            continue
        before_rows = item.get("before_rows", 0)
        after_rows = item.get("after_rows", 0)
        covered = item.get("covered_items", 0)
        expected = item.get("expected_items", 0)
        covered_text = "-"
        if expected:
            covered_text = f"{covered}/{expected}"
        lines.append(
            f"- {name}: status={item.get('status')} rows={before_rows}->{after_rows} coverage={covered_text} target_covered={item.get('target_covered')}"
        )
    return lines


def build_summary(date: str, status_payload: Dict[str, Any], report: Optional[Dict[str, Any]], report_source: str) -> str:
    runtime = status_payload.get("runtime") or {}
    jobs = status_payload.get("jobs") or {}
    last_runs = jobs.get("last") or {}

    lines = [
        f"Collector reconcile check for {date}",
        "",
        f"Status source: {'API' if status_payload else 'unavailable'}",
        f"Report source: {report_source}",
        f"Open gaps: {runtime.get('open_gap_count', 'unknown')}",
    ]

    for job_name in ("startup_catchup", "daily_full_sync", "daily_reconcile", "manual_reconcile"):
        job = last_runs.get(job_name)
        if not job:
            continue
        lines.append(
            f"Last {job_name}: status={job.get('status')} trigger={job.get('trigger')} started_at={job.get('started_at')} completed_at={job.get('completed_at', '-')}"
        )

    if not report:
        lines.extend(
            [
                "",
                "No reconciliation report found for the requested date.",
            ]
        )
        return "\n".join(lines)

    passed, partial, failed = summarize_report(report)
    lines.extend(
        [
            "",
            f"Report status: {report.get('status')}",
            f"Trading day: {report.get('is_trading_day')}",
            f"Open gaps after reconcile: {report.get('open_gap_count')}",
            f"Domain summary: passed={passed} partial={partial} failed={failed}",
        ]
    )
    if report.get("errors"):
        lines.append("Errors:")
        for item in report["errors"]:
            lines.append(f"- {item}")
    lines.append("Domains:")
    lines.extend(domain_health_lines(report))
    return "\n".join(lines)


def evaluate_exit_code(report: Optional[Dict[str, Any]], status_payload: Dict[str, Any], strict: bool) -> int:
    if not report:
        return 1
    if report.get("status") == "failed":
        return 1
    if strict:
        if report.get("status") != "passed":
            return 1
        if report.get("open_gap_count", 0) not in (0, None):
            return 1
        for domain in report.get("domains", []):
            if domain.get("status") == "failed":
                return 1
            expected = domain.get("expected_items", 0)
            covered = domain.get("covered_items", 0)
            if expected and covered < expected:
                return 1
    runtime = status_payload.get("runtime") or {}
    if strict and runtime.get("open_gap_count", 0) not in (0, None):
        return 1
    return 0


def main() -> int:
    args = parse_args()
    query = urllib.parse.urlencode({"date": args.date})
    status_url = f"{args.base_url.rstrip('/')}/api/collector/status"
    report_url = f"{args.base_url.rstrip('/')}/api/collector/reconcile?{query}"

    status_payload = normalize_status_payload(unwrap_response(fetch_json(status_url, args.timeout)))
    report = unwrap_response(fetch_json(report_url, args.timeout))
    report_source = "API"
    if report is None:
        report = load_report_from_file(Path(args.report_dir), args.date)
        report_source = "file" if report is not None else "missing"

    merged = {
        "date": args.date,
        "status": status_payload,
        "report": report,
        "report_source": report_source,
    }
    if args.json:
        print(json.dumps(merged, ensure_ascii=False, indent=2))
    else:
        print(build_summary(args.date, status_payload, report, report_source))
        print("")
        print(
            textwrap.dedent(
                """\
                Exit code policy:
                - 0: report exists and no hard failure detected
                - 1: report missing, report failed, or --strict health checks failed
                """
            ).rstrip()
        )
    return evaluate_exit_code(report, status_payload, args.strict)


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Render a readable stock report from the local TDX API service."""

from __future__ import annotations

import argparse
import json
import math
import sys
import textwrap
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timedelta
from typing import Any


DEFAULT_BASE_URL = "http://127.0.0.1:8080"


def request_json(base_url: str, path: str, params: dict[str, Any] | None = None) -> Any:
    query = ""
    if params:
        query = "?" + urllib.parse.urlencode(params)
    url = f"{base_url}{path}{query}"
    req = urllib.request.Request(url, headers={"Accept": "application/json"})
    with urllib.request.urlopen(req, timeout=15) as resp:
        body = resp.read().decode("utf-8")
    payload = json.loads(body)
    if isinstance(payload, dict) and "code" in payload and payload.get("code") != 0:
        raise RuntimeError(payload.get("message") or f"request failed: {path}")
    return payload


def safe_get_api_data(payload: Any) -> Any:
    if isinstance(payload, dict) and "data" in payload:
        return payload["data"]
    return payload


def format_price(value: Any) -> str:
    if value is None:
        return "-"
    if isinstance(value, (int, float)):
        return f"{float(value) / 1000:.2f}"
    return str(value)


def format_pct(value: float | None) -> str:
    if value is None or math.isnan(value):
        return "-"
    sign = "+" if value > 0 else ""
    return f"{sign}{value:.2f}%"


def format_int(value: Any) -> str:
    if value in (None, ""):
        return "-"
    try:
        return f"{int(value):,}"
    except (TypeError, ValueError):
        return str(value)


def format_amount_yi(value: Any) -> str:
    if value in (None, ""):
        return "-"
    try:
        return f"{float(value) / 100000000:.2f}亿"
    except (TypeError, ValueError):
        return str(value)


def format_amount_readable(value: Any) -> str:
    if value in (None, ""):
        return "-"
    try:
        amount = float(value)
    except (TypeError, ValueError):
        return str(value)

    if amount >= 100000000:
        return f"{amount / 100000000:.2f}亿"
    if amount >= 10000:
        return f"{amount / 10000:.2f}万"
    return f"{amount:.2f}元"


def format_hands_with_shares(value: Any) -> str:
    if value in (None, ""):
        return "-"
    try:
        hands = int(value)
    except (TypeError, ValueError):
        return str(value)
    shares = hands * 100
    return f"{hands:,} 手 / {shares:,} 股"


def format_time(value: str | None) -> str:
    if not value:
        return "-"
    try:
        dt = datetime.fromisoformat(value)
        return dt.strftime("%Y-%m-%d %H:%M")
    except ValueError:
        return value


def format_trade_side(status: Any) -> str:
    mapping = {0: "买盘", 1: "卖盘", 2: "平盘"}
    return mapping.get(status, f"状态{status}")


def pad(text: Any, width: int) -> str:
    text = str(text)
    if len(text) >= width:
        return text[:width]
    return text + " " * (width - len(text))


def divider(title: str) -> str:
    return f"\n=== {title} ==="


def render_kv(items: list[tuple[str, str]]) -> str:
    label_width = max(len(label) for label, _ in items) if items else 0
    lines = []
    for label, value in items:
        lines.append(f"{label.ljust(label_width)} : {value}")
    return "\n".join(lines)


def summarize_minute_data(
    minute_data: dict[str, Any] | None,
    report_date: str,
) -> list[tuple[str, str]]:
    if not minute_data:
        return [("分时数据", "暂无")]

    points = minute_data.get("List") or []
    if not points:
        return [
            ("分时日期", minute_data.get("date", report_date)),
            ("分时状态", "报告交易日是交易日，但分时数据为空，需要排查接口或数据源"),
        ]

    last_point = points[-1]
    price = last_point.get("Price")
    volume = last_point.get("Volume")
    if volume in (None, ""):
        volume = last_point.get("Number")
    time_value = last_point.get("Time")
    return [
        ("分时日期", minute_data.get("date", "-")),
        ("分时点数", format_int(minute_data.get("Count"))),
        ("最新分时", format_time(time_value)),
        ("最新价", format_price(price)),
        ("最新量", format_int(volume)),
    ]


def pick_recent_workday_dates(base_url: str, today: datetime, window_days: int = 14) -> list[str]:
    end_date = today.strftime("%Y-%m-%d")
    start_date = (today - timedelta(days=window_days)).strftime("%Y-%m-%d")
    payload = request_json(
        base_url,
        "/api/workday/range",
        {"start": start_date, "end": end_date},
    )
    items = safe_get_api_data(payload) or {}
    workdays = items.get("list") or []
    dates = [item.get("numeric") for item in workdays if item.get("numeric")]
    return list(reversed(dates))


def determine_report_date(workday_data: dict[str, Any]) -> str:
    current = (workday_data.get("date") or {}).get("numeric")
    if workday_data.get("is_workday") and current:
        return current

    previous = workday_data.get("previous") or []
    if previous:
        return previous[-1].get("numeric", current)
    if current:
        return current
    raise RuntimeError("无法确定报告交易日")


def fetch_minute_data(base_url: str, code: str, report_date: str) -> dict[str, Any]:
    payload = request_json(base_url, "/api/minute", {"code": code, "date": report_date})
    return safe_get_api_data(payload) or {}


def fetch_trade_data(base_url: str, code: str, report_date: str) -> dict[str, Any]:
    payload = request_json(base_url, "/api/trade", {"code": code, "date": report_date})
    return safe_get_api_data(payload) or {}


def fetch_kline_series(base_url: str, code: str, limit: int) -> list[dict[str, Any]]:
    payload = request_json(
        base_url,
        "/api/kline-all/ths",
        {"code": code, "type": "day", "limit": limit},
    )
    data = safe_get_api_data(payload) or {}
    return data.get("list") or []


def pick_kline_for_date(kline_list: list[dict[str, Any]], report_date: str) -> dict[str, Any] | None:
    for item in reversed(kline_list):
        if format_time(item.get("Time"))[:10].replace("-", "") == report_date:
            return item
    return None


def resolve_identity(base_url: str, code: str) -> dict[str, str]:
    search_payload = request_json(base_url, "/api/search", {"keyword": code})
    matches = safe_get_api_data(search_payload) or []
    for item in matches:
        if item.get("code") == code:
            return {
                "name": item.get("name", "-"),
                "exchange": str(item.get("exchange", "-")).upper(),
            }
    if matches:
        item = matches[0]
        return {
            "name": item.get("name", "-"),
            "exchange": str(item.get("exchange", "-")).upper(),
        }
    return {"name": "-", "exchange": "-"}


def build_snapshot_from_kline(
    kline_item: dict[str, Any],
    amount_hint: Any = None,
) -> list[tuple[str, str]]:
    last_price = kline_item.get("Close")
    prev_close = kline_item.get("Last")
    change_value = None
    change_pct = None
    if last_price is not None and prev_close not in (None, 0):
        change_value = (float(last_price) - float(prev_close)) / 1000
        change_pct = (float(last_price) - float(prev_close)) / float(prev_close) * 100

    amount_value = amount_hint
    if amount_value in (None, "", 0):
        amount_value = kline_item.get("Amount")

    return [
        ("最新价", format_price(last_price)),
        ("昨收", format_price(prev_close)),
        ("涨跌额", "-" if change_value is None else f"{change_value:+.2f}"),
        ("涨跌幅", format_pct(change_pct)),
        ("今开", format_price(kline_item.get("Open"))),
        ("最高", format_price(kline_item.get("High"))),
        ("最低", format_price(kline_item.get("Low"))),
        ("成交量", format_hands_with_shares(kline_item.get("Volume"))),
        ("成交额", format_amount_readable(amount_value)),
    ]


def render_order_book(title: str, levels: list[dict[str, Any]]) -> str:
    lines = [divider(title), "档位  价格      数量"]
    if not levels:
        lines.append("暂无盘口数据")
        return "\n".join(lines)

    for index, level in enumerate(levels, start=1):
        side = "买" if level.get("Buy") else "卖"
        lines.append(
            f"{side}{index}  {pad(format_price(level.get('Price')), 8)}  {format_int(level.get('Number'))}"
        )
    return "\n".join(lines)


def render_kline_section(kline_list: list[dict[str, Any]] | None, rows: int) -> str:
    lines = [divider("最近日K")]
    if not kline_list:
        lines.append("暂无K线数据")
        return "\n".join(lines)

    lines.append("日期         开盘     最高     最低     收盘     涨跌幅     成交量(手)")
    recent = kline_list[-rows:]
    for item in recent:
        last_close = item.get("Last")
        close_price = item.get("Close")
        pct = None
        if last_close not in (None, 0) and close_price is not None:
            pct = (float(close_price) - float(last_close)) / float(last_close) * 100
        lines.append(
            "  ".join(
                [
                    pad(format_time(item.get("Time"))[:10], 10),
                    pad(format_price(item.get("Open")), 7),
                    pad(format_price(item.get("High")), 7),
                    pad(format_price(item.get("Low")), 7),
                    pad(format_price(close_price), 7),
                    pad(format_pct(pct), 8),
                    format_int(item.get("Volume")),
                ]
            )
        )
    return "\n".join(lines)


def render_trade_section(trade_data: dict[str, Any] | None, rows: int) -> str:
    lines = [divider("最近成交")]
    if not trade_data or not trade_data.get("List"):
        lines.append("暂无逐笔成交数据")
        return "\n".join(lines)

    trades = trade_data.get("List") or []
    lines.append("时间               成交价    成交量(手)  笔数   方向")
    for item in trades[-rows:]:
        lines.append(
            "  ".join(
                [
                    pad(format_time(item.get("Time")), 16),
                    pad(format_price(item.get("Price")), 7),
                    pad(format_int(item.get("Volume")), 8),
                    pad(format_int(item.get("Number")), 4),
                    format_trade_side(item.get("Status")),
                ]
            )
        )
    return "\n".join(lines)


def render_order_book_section(
    quote: dict[str, Any] | None,
    report_date: str,
    today_numeric: str,
) -> str:
    if report_date != today_numeric:
        return "\n".join(
            [
                divider("盘口五档"),
                "历史交易日暂无五档盘口接口，为保证同一交易日口径，本报告不展示盘口数据。",
            ]
        )
    if not quote:
        return "\n".join([divider("盘口五档"), "暂无盘口数据"])
    return "\n".join(
        [
            render_order_book("买盘五档", quote.get("BuyLevel") or []),
            render_order_book("卖盘五档", quote.get("SellLevel") or []),
        ]
    )


def render_workday_section(workday_data: dict[str, Any] | None) -> str:
    lines = [divider("交易日状态")]
    if not workday_data:
        lines.append("暂无交易日信息")
        return "\n".join(lines)

    current = (workday_data.get("date") or {}).get("iso", "-")
    is_workday = workday_data.get("is_workday")
    previous = workday_data.get("previous") or []
    next_days = workday_data.get("next") or []
    lines.append(render_kv([
        ("查询日期", current),
        ("是否交易日", "是" if is_workday else "否"),
        ("上一个交易日", previous[-1]["iso"] if previous else "-"),
        ("下一个交易日", next_days[0]["iso"] if next_days else "-"),
    ]))
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="输入股票代码，输出分类清晰、适合阅读的股票完整报告。"
    )
    parser.add_argument("code", help="6位股票代码，例如 000001")
    parser.add_argument(
        "--base-url",
        default=DEFAULT_BASE_URL,
        help=f"API 基础地址，默认 {DEFAULT_BASE_URL}",
    )
    parser.add_argument(
        "--kline-rows",
        type=int,
        default=8,
        help="展示最近几条日K，默认 8",
    )
    parser.add_argument(
        "--trade-rows",
        type=int,
        default=12,
        help="展示最近几条逐笔成交，默认 12",
    )
    args = parser.parse_args()

    code = args.code.strip()

    try:
        today = datetime.now()
        today_numeric = today.strftime("%Y%m%d")
        identity = resolve_identity(args.base_url, code)
        workday_payload = request_json(
            args.base_url, "/api/workday", {"date": today.strftime("%Y-%m-%d")}
        )
        workday_data = safe_get_api_data(workday_payload) or {}
        report_date = determine_report_date(workday_data)
        minute_data = fetch_minute_data(args.base_url, code, report_date)
        trade_data = fetch_trade_data(args.base_url, code, report_date)
        kline_list = fetch_kline_series(args.base_url, code, max(30, args.kline_rows + 10))
        report_kline = pick_kline_for_date(kline_list, report_date)
        quote = None
        if report_date == today_numeric:
            quote_payload = request_json(args.base_url, "/api/quote", {"code": code})
            quotes = safe_get_api_data(quote_payload) or []
            if quotes:
                quote = quotes[0]
    except urllib.error.URLError as exc:
        print(f"无法连接到服务: {exc}", file=sys.stderr)
        print(f"请确认容器或服务已启动，并且 {args.base_url} 可访问。", file=sys.stderr)
        return 1
    except Exception as exc:  # noqa: BLE001 - CLI script
        print(f"获取股票数据失败: {exc}", file=sys.stderr)
        return 1

    if not report_kline:
        print(f"未找到股票 {code} 在报告交易日 {report_date} 的K线数据。", file=sys.stderr)
        return 1

    title = f"{identity['name']} ({identity['exchange']}{code})"
    subtitle = "TDX Stock Report"
    intro = textwrap.fill(
        f"这份报告严格锁定在同一个报告交易日 {report_date}，"
        "只展示该交易日的数据，避免混用不同日期的行情、分时和成交记录。",
        width=78,
    )

    sections = [
        f"{title}\n{subtitle}\n{'=' * max(len(title), len(subtitle))}",
        intro,
        divider("核心概览"),
        render_kv(
            [("报告交易日", report_date)] +
            build_snapshot_from_kline(report_kline, quote.get("Amount") if quote else None)
        ),
        divider("分时概览"),
        render_kv(
            summarize_minute_data(
                minute_data,
                report_date,
            )
        ),
        render_order_book_section(quote, report_date, today_numeric),
        render_kline_section(kline_list, max(1, args.kline_rows)),
        render_trade_section(trade_data, max(1, args.trade_rows)),
        render_workday_section(workday_data),
    ]

    print("\n".join(sections))
    return 0


if __name__ == "__main__":
    sys.exit(main())

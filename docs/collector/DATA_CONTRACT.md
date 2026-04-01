# Collector Data Contract

## Contract Rules

These rules apply to all collector domains:

1. Primary storage is SQLite.
2. CSV, when present, is export or archive only.
3. Published data must be written only after staging validation passes.
4. Primary keys, dedupe rules, and time semantics must be documented before schema commit.
5. If an upstream field meaning is unresolved, keep it explicitly unresolved in schema notes and validation logic.
6. Collector storage and contracts must be expressed in collector-owned domain models, not upstream `protocol.*` structs.

## Decoupling Rules

- `tdx-api` is the current provider implementation, not the collector core.
- collector code may depend on provider interfaces and collector domain models
- collector core must not use upstream protocol structs as persistent schema definitions
- swapping the provider should require adapter replacement, not collector-core redesign
- `collector/provider_tdx.go` is the current upstream adapter boundary; new collector core files must not import `github.com/injoyai/tdx` or `protocol.*`

## Provider Contract Inventory

Collector-owned provider contracts currently include:

- `UniverseProvider`
- `CalendarProvider`
- `QuoteProvider`
- `MinuteProvider`
- `KlineProvider`
- `TradeHistoryProvider`
- `OrderHistoryProvider`
- `FinanceProvider`
- `F10Provider`

Collector-owned provider-facing domain models currently include:

- `Instrument`
- `TradingDay`
- `QuoteSnapshot`
- `MinutePoint`
- `KlineBar`
- `TradeTick`
- `OrderHistorySnapshot`
- `FinanceSnapshot`
- `F10Category`
- `F10Content`

## Domain Contract Table

| Domain | Current source | Coverage | Final storage | Planned collection mode | Primary identity rule | Validation baseline |
|---|---|---|---|---|---|---|
| codes | TDX code table + optional index config | stock, etf, index | SQLite | scheduled refresh + startup recovery | `exchange + code` | non-empty universe, stable code uniqueness |
| workday | index day series | exchange workdays | SQLite | scheduled refresh + startup recovery | `date` | monotonic dates, no duplicates |
| kline | TDX kline + index kline | stock, etf, index; minute to year | SQLite | scheduled incremental + gap backfill | `code + period + time/date` | continuity, overlap consistency, no duplicate bar key |
| trade_history | TDX historical trade day | stock, etf | SQLite | scheduled backfill + gap recovery | to be fixed from raw payload and stored identity | date coverage, dedupe, replay stability |
| trade_derived_bars | derived from stored trade_history | stock, etf; 1/5/15/30/60 minute | SQLite | recompute from stored raw trades | `code + period + time` | reproducible from raw trade source |
| order_history | TDX history orders | stock | SQLite | scheduled backfill + gap recovery | `code + date + row order` unless stronger identity is proven | row count stability, date coverage, no silent field coercion |
| quote_snapshot | TDX quote | stock, etf | SQLite | trading-session polling | `code + capture_time` | capture cadence, market-session bounds |
| minute_live | TDX minute endpoint | stock, etf | SQLite | trading-session polling | `code + trading_date + minute` | minute continuity within session |
| trade_live | TDX minute trade endpoint | stock, etf | SQLite | trading-session polling + reconciliation | to be fixed from raw payload and capture strategy | append-only within session, close reconciliation |
| finance | TDX finance info | stock | SQLite | periodic refresh by update marker | `code + updated_date` | valid update marker, idempotent refresh |
| f10_category | TDX company info category | stock | SQLite | periodic refresh | `code + filename + start + length` | no duplicate category entry |
| f10_content | TDX company info content | stock | SQLite + optional text archive | on category sync or change detection | `code + filename + start + length + content_hash` | content hash stability and directory-content match |

## Known Unresolved Semantics

The following are not yet formal facts and must remain treated as unresolved until proven:

- the exact business meaning of `BuySellDelta` in `order_history`
- the definitive uniqueness key for live trade capture rows
- the definitive uniqueness key for historical trade rows beyond proven replay-safe identity

No agent may convert these unresolved semantics into hard business claims without evidence from code, payload sampling, and validation.

## Storage Intent By Domain

| Domain | DB-first | Optional file archive |
|---|---|---|
| codes | yes | no |
| workday | yes | no |
| kline | yes | no |
| trade_history | yes | yes |
| trade_derived_bars | yes | yes |
| order_history | yes | no |
| quote_snapshot | yes | no |
| minute_live | yes | no |
| trade_live | yes | no |
| finance | yes | no |
| f10_category | yes | no |
| f10_content | yes | yes |

## Implemented Metadata Publish Rules

- `codes` and `workday` now publish through collector-owned staging tables before replacing published rows.
- replay safety is proven by collector tests that rerun metadata refresh after reopening the same `collector.db`, `codes.db`, and `workday.db`.
- metadata publish state is persisted through `collector_cursor` records with:
  - `domain = codes, asset_type = metadata, instrument = all`
  - `domain = workday, asset_type = metadata, instrument = all`

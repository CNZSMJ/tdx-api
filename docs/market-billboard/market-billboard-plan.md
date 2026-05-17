# Market Billboard Plan

## Scope

- Add the market billboard domain for full long/short public trading-board applications.
- Use Eastmoney RPT as the first data source.
- Do not add a separate service or repository.
- Do not use `longhubang` or `dragon_tiger` naming in code or API paths.

## Naming

- Upstream package: `eastmoney/rpt`.
- Domain package: `market/billboard`.
- Governance domain: `market_billboard`.
- API root: `/api/market/billboard`.
- Instrument query parameter: `full_code`.

## Data Source

- Daily entries: `RPT_DAILYBILLBOARD_DETAILSNEW`.
- Buy-side seats: `RPT_BILLBOARD_DAILYDETAILSBUY`.
- Sell-side seats: `RPT_BILLBOARD_DAILYDETAILSSELL`.
- Instrument statistics: `RPT_BILLBOARD_TRADEALL`.
- Institution statistics: `RPT_ORGANIZATION_TRADE_DETAILS`.

## Asset Scope

- Collect only SH/SZ/BJ A-share stocks.
- Filter unsupported instruments before raw-row persistence.
- Do not store ETF, index, convertible bond, or other unsupported rows.

## Unit Contract

- Do not reuse TDX protocol display unit helpers for Eastmoney RPT data.
- Define an Eastmoney RPT field-level unit contract.
- Preserve original values in raw rows.
- Normalize structured amount and market-cap fields into `*_milli` int64 fields.
- Use explicit suffixes for non-price units, including `*_volume_share`, `*_pct`, and `*_ratio`.

## Storage

- Store filtered raw RPT rows in `eastmoney_rpt_raw_row`.
- Store normalized facts in market billboard tables:
  - `market_billboard_entry`
  - `market_billboard_reason`
  - `market_billboard_entry_reason`
  - `market_billboard_seat`
  - `market_billboard_seat_trade`
  - `market_billboard_institution_trade`
  - `market_billboard_instrument_stat`
  - `market_billboard_sync_status`
- Entry stable keys must include date, instrument, and source reason identity. Do not key by `full_code + trade_date` only.
- Seat `rank` is a normalized derived field based on upstream row order.

## Sync

- Default bootstrap start date: `20250101`.
- Daily schedule: 21:30 Asia/Shanghai.
- Daily sync window: latest 5 trading days.
- Use conservative pagination:
  - `pageSize=5000`.
  - HTTP timeout `10s`.
  - retry count `3`.
  - seat detail concurrency default `3`.
- Prefer date-range bulk loading for buy/sell seat RPTs, then join locally.
- Record per `trade_date + report_name` sync status.

## Governance

- Add `market_billboard` to `/api/collector/status`.
- Add a dedicated governance job for scheduled billboard sync.
- Billboard failures must not block daily close sync.

## API

- Read local DB only; do not proxy Eastmoney from GET handlers.
- API responses include `freshness` and `source` metadata.
- Missing, failed, and empty-success coverage states are represented in response metadata, not as HTTP errors.
- Parameter and storage/service failures still use HTTP errors.
- Default list pagination uses cursor style with `limit=100`, `max=500`.
- Expose source metadata only when `include_source=true`.

## CLI

- Add `cmd/market-billboard-sync`.
- Add `cmd/market-billboard-diagnose`.
- Support explicit date ranges and dry-run where useful.

## Tests

- Unit-test source code normalization, asset filtering, and unit conversion.
- Unit-test idempotent storage and entry/seat/institution/stat mapping.
- Test API freshness/coverage metadata.
- Test governance catalog/status projection includes `market_billboard`.

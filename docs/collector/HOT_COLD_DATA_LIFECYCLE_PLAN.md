# Hot/Cold Data Lifecycle Plan

**Status**: Proposed implementation plan
**Repo**: `tdx-api`
**Scope**: Market-data storage lifecycle, hot/cold retention, governance integration, existing API contract protection, future cold-query surface
**Last Updated**: 2026-04-25

## Purpose

This document defines the end-state architecture and implementation path for reducing and controlling disk usage under `TDX_DATA_DIR` without breaking existing external API contracts or destabilizing daily data-governance jobs.

The program is not complete when the scanner, manifest, or single-segment archival works. It is complete only when:

- `trade`, `live`, `order_history`, and `auction` hot stores continuously stay within the latest 180 trading days.
- Historical rows older than the hot retention window are archived, verified, recoverable, and accounted for in the cold manifest.
- `professional_finance` has a slim serving path and its non-serving raw/source assets are archived or otherwise controlled.
- Lifecycle maintenance runs as a bounded governance job and no longer causes daily governance jobs to run indefinitely.
- Existing public APIs keep their current request and response contracts and do not implicitly read cold data.
- A separate cold-query API surface can read cold data explicitly.
- The local cold store can later move to object storage without changing logical manifest semantics.

## Hard Decisions

1. Existing API response contracts must not change.
2. Existing APIs must not transparently read cold data by default.
3. Existing APIs must preserve their current data-source semantics, whether the current handler reads hot SQLite, provider/client data, or a service DB.
4. Cold data access must use a separate explicit `/api/v1/cold/*` API surface.
5. The first-stage cold store is local because no external disk or object store is currently available.
6. Manifest records must use abstract URIs so local cold files can later move to object storage.
7. `trade`, `live`, `order_history`, and `auction` hot retention is exactly 180 trading days.
8. `live` and `auction` use the same 180-trading-day hot policy as `trade` and `order_history`.
9. Data movement must be resumable, batch-limited, and safe under low local disk space.
10. Hot pruning is forbidden until restore/rehydration has been implemented and tested.
11. Object storage migration is not implemented in the first lifecycle rollout, but the storage interface and URI model must be object-store-ready from the first implementation.
12. The final implementation must include catch-up, steady-state enforcement, restore, governance observability, and ongoing retention, not only early dry-run phases.

## Non-Goals

- Do not migrate cold data to object storage in the first implementation.
- Do not make `/api/trade-history`, `/api/order-history`, `/api/kline-history`, `/api/kline-all`, `/api/finance`, `/api/f10/*`, or `/api/v1/prof-finance/*` query cold data implicitly.
- Do not make `daily_close_sync`, `daily_audit` including legacy `collector_daily_reconcile`, `startup_recovery`, `deep_audit_backfill`, or web repair flows perform historical cold-data migration.
- Do not depend on global SQLite `VACUUM` as the primary space-recovery mechanism.
- Do not delete any hot data until cold export, manifest verification, replacement hot DB verification, and restore tests are complete.
- Do not treat Phase 0 or Phase 1 as a sufficient delivery target.

## Current Footprint

Measured from the local `state/a-stock-market-tdx` dataset during planning.

| Domain | Current size | Notes |
|---|---:|---|
| `trade` | 118G | Per-instrument SQLite files; `TradeHistory` plus derived trade bars and indexes |
| `live` | 107G | Per-instrument SQLite files; mostly `TradeLive` and `MinuteLive` across many years |
| `auction` | not yet measured | Shared `auction.db` with `AuctionSnapshot` checkpoint rows |
| `fundamentals` | 65G | Mostly `professional_finance/prof_finance.db` |
| `order_history` | 5.6G | Per-instrument SQLite files |
| `kline` | 4.4G | Not the first-stage reduction target |
| `collector.db` | 3.2G | Governance/cursor/task history; requires separate retention |

Estimated net space reclaimed when the cold store remains on the same local disk:

| Stage | Domains | Estimated net reclaimed |
|---|---|---:|
| Stage 1 | `trade`, `live`, `order_history`, `auction` | 130G - 170G plus auction compression delta |
| Stage 2 | `professional_finance` raw/source assets and future slim serving split | 25G - 45G |
| Total | Stage 1 + Stage 2 | 160G - 220G |

These estimates must be validated by a pilot before bulk pruning. Because the cold store is local, net reclaim depends on real Parquet compression, index reduction, and replacement SQLite size.

## Target Architecture

```text
Existing public APIs
  -> preserve current source semantics
  -> no implicit cold reads
  -> no response-shape changes

Future explicit cold APIs
  -> ColdQueryRouter
  -> cold manifest
  -> ColdStorage implementation
  -> local Parquet now
  -> object storage later

Governance jobs
  -> startup_recovery
  -> daily_open_refresh
  -> daily_close_sync
  -> daily_audit
  -> deep_audit_backfill
  -> data_lifecycle_maintenance
  -> data_lifecycle_restore
```

## Existing API Contract Preservation

The lifecycle system must not redefine current endpoint semantics. The current source of each endpoint must be inventoried and locked with tests before implementation.

| Endpoint | Current source class | Lifecycle rule |
|---|---|---|
| `/api/trade-history` | TDX client/provider historical trade call | Must keep provider-backed behavior; no cold fallback |
| `/api/trade-history/full` | TDX client/provider loop across workdays | Must keep provider-backed behavior; no cold fallback |
| `/api/order-history` | TDX client/provider historical order call | Must keep provider-backed behavior; no cold fallback |
| `/api/minute-trade-all` | TDX client/provider current or historical trade call | Must keep provider-backed behavior; no cold fallback |
| `/api/kline-history` | provider historical bars through market-provider path | Must keep provider-backed behavior; no cold fallback |
| `/api/kline-all` and `/api/kline-all/tdx` | TDX client/provider full kline call | Must keep provider-backed behavior; no cold fallback |
| `/api/kline-all/ths` | THS adjusted kline path | Must keep provider-backed behavior; no cold fallback |
| `/api/finance` | TDX client finance info | Not affected by cold lifecycle |
| `/api/f10/categories` | TDX client F10 categories | Not affected by cold lifecycle |
| `/api/f10/content` | TDX client F10 content | Not affected by cold lifecycle |
| `/api/v1/prof-finance/fields` | registry/service metadata | Must keep response envelope unchanged |
| `/api/v1/prof-finance/history` | `profinance.Service` query DB | Must keep response envelope and current query semantics |
| `/api/v1/prof-finance/snapshot` | `profinance.Service` query DB | Must keep response envelope and current query semantics |
| `/api/v1/prof-finance/coverage` | `profinance.Service` query DB | Must keep response envelope and current query semantics |
| `/api/v1/prof-finance/cross-section` | `profinance.Service` query DB | Must keep response envelope, pagination, and error semantics |

Rules:

1. Existing APIs do not automatically merge cold Parquet results.
2. Existing response shapes do not change.
3. Existing endpoints do not add required parameters.
4. Existing endpoints do not add cold-data metadata by default.
5. Requests outside the hot range must keep the endpoint's current behavior, which may be provider-backed data, empty result, or an existing error.
6. If an endpoint currently reads provider data rather than hot SQLite, lifecycle work must not reroute it to hot SQLite or cold Parquet.
7. Contract tests must record request parameters, success shapes, common error shapes, and source behavior before any pruning implementation starts.

## Hot Layer

The hot layer remains SQLite because collectors and several internal readers already depend on it.

| Domain | Hot retention | Hot storage | First lifecycle action |
|---|---:|---|---|
| `trade` | latest 180 trading days | per-instrument SQLite | archive rows older than cutoff and rebuild hot DBs |
| `live` | latest 180 trading days | per-instrument SQLite plus `quotes.db` | archive `TradeLive`, `MinuteLive`, and `QuoteSnapshot` rows older than cutoff and rebuild hot DBs |
| `order_history` | latest 180 trading days | per-instrument SQLite | archive rows older than cutoff and rebuild hot DBs |
| `auction` | latest 180 trading days | shared `auction/auction.db` | archive `AuctionSnapshot` rows older than cutoff and rebuild hot DB |
| `kline` | keep current behavior initially | per-instrument SQLite | no first-stage pruning; add scanner only |
| `finance` / `f10` | keep current behavior | TDX client and existing metadata DBs | no cold lifecycle work initially |
| `professional_finance` | canonical serving subset remains hot | `prof_finance.db` or slim serving DB | split serving data from raw/source assets |

The hot cutoff must be computed from `workday.db`:

```text
effective_trading_day = latest completed trading day known to workday.db
hot_cutoff_trade_date = 180th trading day before or equal to effective_trading_day
cold_candidate = row_trade_date < hot_cutoff_trade_date
hot_retained = row_trade_date >= hot_cutoff_trade_date
```

Cutoff rules:

- Before market close, weekends, and holidays use the latest completed trading day, not the wall-clock date.
- The evaluated cutoff must be persisted with `evaluated_at`, `effective_trading_day`, and the workday data source.
- `daily_open_refresh` and intraday writes must never race with a cutoff based on an incomplete trading day.
- The lifecycle scanner must report rows on both sides of the cutoff before any export.
- Lifecycle writes must refuse to run if `workday.db` is stale. Stale means the latest known completed trading day is older than the configured calendar-day tolerance and cannot be explained by the exchange calendar or provider freshness checks.
- Exchange-calendar explanation is explicit: if all days after the latest known completed trading day and before today are known non-trading days in `workday.db`, the lag is acceptable; otherwise the provider freshness check must refresh or prove the calendar before lifecycle writes run.
- `TDX_LIFECYCLE_WORKDAY_MAX_STALE_CALENDAR_DAYS` defaults to `7` so long exchange holidays such as Spring Festival do not falsely block lifecycle work.
- `QuoteSnapshot` retention is evaluated from `CaptureTime` converted to `Asia/Shanghai`; it follows the same 180-trading-day policy as the rest of the `live` domain.
- `AuctionSnapshot` retention is evaluated from `TradeDate`; `YYYY-MM-DD` hot values are normalized to `YYYYMMDD` for cutoff comparison and cold segment metadata.

## Cold Layer

The first cold store is local:

```text
${TDX_DATA_DIR}/cold/
  domain=trade/
  domain=live/
  domain=order_history/
  domain=auction/
  domain=professional_finance/
  _staging/
```

Cold files use:

- format: Parquet
- compression: ZSTD
- schema versioning: required
- Stage 1 verification reader: pinned Go Parquet library
- future cold-query engine: pinned DuckDB CLI or explicitly build-tagged DuckDB binding
- URI model: scheme-based logical URI, never absolute path only

### Partition Strategy

The layout must avoid one file per instrument per trading day. That would create millions of small files and would be poor for both local disk and future object storage.

Use instrument-year partitioning for per-instrument market data:

```text
tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-{archive_batch_id_suffix}-000.parquet
tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeMinute1Bar/instrument=sh600000/year=2024/part-{archive_batch_id_suffix}-000.parquet
tdx-cold://a-stock-market-tdx/cold/domain=live/table=TradeLive/instrument=sh600000/year=2024/part-{archive_batch_id_suffix}-000.parquet
tdx-cold://a-stock-market-tdx/cold/domain=live/table=MinuteLive/instrument=sh600000/year=2024/part-{archive_batch_id_suffix}-000.parquet
tdx-cold://a-stock-market-tdx/cold/domain=live/table=QuoteSnapshot/capture_year=2024/part-{archive_batch_id_suffix}-000.parquet
tdx-cold://a-stock-market-tdx/cold/domain=order_history/table=OrderHistory/instrument=sh600000/year=2024/part-{archive_batch_id_suffix}-000.parquet
tdx-cold://a-stock-market-tdx/cold/domain=auction/table=AuctionSnapshot/instrument=shared/year=2024/part-{archive_batch_id_suffix}-000.parquet
```

Use table/report-year partitioning for professional finance:

```text
tdx-cold://a-stock-market-tdx/cold/domain=professional_finance/table=prof_finance_source_value_raw/report_year=2023/part-{archive_batch_id_suffix}-000.parquet
tdx-cold://a-stock-market-tdx/cold/domain=professional_finance/table=prof_finance_source_file/report_year=2023/part-{archive_batch_id_suffix}-000.parquet
```

Partition rules:

- Keep row-level `code` or `full_code` columns even when the instrument is present in the path.
- Target file size after compression should be roughly 64MB - 512MB.
- Split a file when either 500,000 rows or roughly 128MB uncompressed input has been written.
- Finalized part files are immutable; there is no append mode.
- Part files use `part-{archive_batch_id_suffix}-{seq:03d}.parquet`, not reusable `part-000.parquet` names across batches.
- The manifest must record exact `start_date`, `end_date`, `row_count`, and `cold_uri` for each part file; date-range lookup must use manifest coverage, not infer coverage from paths.
- If a later batch writes the same instrument-year partition, it creates new part files and either records disjoint ranges or supersedes overlapping older segments after verification.
- Small instrument-year partitions may produce small files; do not merge unrelated instruments in Stage 1 because it complicates restore and pruning ownership.
- Segments below 1MB compressed size should be counted in a small-file warning metric; they are allowed in Stage 1 but become candidates for future verified compaction.
- If future compaction is needed, it must create a new active segment and mark old segments `superseded` only after verification.
- The manifest, not the path, remains the source of truth for coverage.

## Parquet Schema Contract

All cold schemas are versioned. `schema_version=1` must be defined before implementation.

Common metadata fields for all cold rows:

| Column | Type | Notes |
|---|---|---|
| `archive_batch_id` | string | batch that wrote the row |
| `schema_version` | int32 | current value `1` |
| `archived_at` | timestamp_ms UTC | time the cold row was written |

SQLite source tables use `core.SameMapper{}`, so source column names are Go struct field names such as `Price`, `VolumeHand`, and `StatusCode`. Parquet v1 intentionally uses lower snake_case names, but the mapping is part of the schema contract and must be implemented exactly.

Common SQLite to Parquet mapping:

| SQLite source column | Parquet column | Notes |
|---|---|---|
| `Code` | `code` | unchanged semantic value |
| `TradeDate` | `trade_date` | `YYYYMMDD` |
| `CaptureTime` | `capture_time` | Unix seconds from source SQLite |
| derived from `CaptureTime` | `capture_date` | `Asia/Shanghai` market date `YYYYMMDD` |
| `TradeTime` | `trade_time` | source integer |
| `BucketTime` | `bucket_time` | source integer |
| `Clock` | `clock` | source clock string |
| `Seq` | `seq` | source integer |
| `Price` | `price_milli` | `PriceMilli` integer |
| `Last` | `last_milli` | `PriceMilli` integer |
| `PreClose` | `pre_close_milli` | `PriceMilli` integer |
| `Open` | `open_milli` | `PriceMilli` integer |
| `High` | `high_milli` | `PriceMilli` integer |
| `Low` | `low_milli` | `PriceMilli` integer |
| `Close` | `close_milli` | `PriceMilli` integer |
| `Amount` | `amount_milli` | `PriceMilli` integer |
| `AmountYuan` | `amount_yuan` | source float64; keep only for `QuoteSnapshot` |
| `VolumeHand` | `volume_hand` | source integer |
| `Volume` | `volume` | source integer |
| `Number` | `number` | source integer |
| `StatusCode` | `status_code` | source integer |
| `BuySellDelta` | `buy_sell_delta` | source integer |
| `Side` | `side` | nullable string in Parquet |
| `InDate` | `in_date` | nullable int64 in Parquet |

`trade.TradeHistory` schema:

| Column | Type |
|---|---|
| `code` | string |
| `trade_date` | string, `YYYYMMDD` |
| `trade_time` | int64 |
| `seq` | int32 |
| `price_milli` | int64 |
| `volume_hand` | int32 |
| `number` | int32 |
| `status_code` | int32 |
| `side` | nullable string |
| `in_date` | nullable int64 |

`trade.TradeMinute{1,5,15,30,60}Bar` schema:

| Column | Type |
|---|---|
| `code` | string |
| `trade_date` | string, `YYYYMMDD` |
| `bucket_time` | int64 |
| `open_milli` | int64 |
| `high_milli` | int64 |
| `low_milli` | int64 |
| `close_milli` | int64 |
| `volume_hand` | int64 |
| `amount_milli` | int64 |
| `in_date` | nullable int64 |

`live.TradeLive` schema:

| Column | Type |
|---|---|
| `code` | string |
| `trade_date` | string, `YYYYMMDD` |
| `trade_time` | int64 |
| `seq` | int32 |
| `price_milli` | int64 |
| `volume_hand` | int32 |
| `number` | int32 |
| `status_code` | int32 |
| `side` | nullable string |

`live.MinuteLive` schema:

| Column | Type |
|---|---|
| `code` | string |
| `trade_date` | string, `YYYYMMDD` |
| `clock` | string |
| `price_milli` | int64 |
| `number` | int32 |

`live.QuoteSnapshot` schema:

| Column | Type |
|---|---|
| `code` | string |
| `capture_time` | int64 |
| `capture_date` | string, `YYYYMMDD`, derived from `capture_time` in fixed `Asia/Shanghai` timezone |
| `last_milli` | int64 |
| `pre_close_milli` | int64 |
| `open_milli` | int64 |
| `high_milli` | int64 |
| `low_milli` | int64 |
| `volume_hand` | int64 |
| `amount_yuan` | double |

`order_history.OrderHistory` schema:

| Column | Type |
|---|---|
| `code` | string |
| `trade_date` | string, `YYYYMMDD` |
| `seq` | int32 |
| `price_milli` | int64 |
| `buy_sell_delta` | int32 |
| `volume` | int32 |
| `in_date` | nullable int64 |

`professional_finance` schema:

- The cold schema must mirror the source SQLite table names and columns for archived source tables.
- `prof_finance_source_value_raw`, `prof_finance_source_file`, `prof_finance_source_report`, and superseded source-file metadata are first archival candidates.
- `prof_finance_report_version` and `prof_finance_report_payload` must remain hot until a slim serving DB or materialized serving table can prove the current `/api/v1/prof-finance/*` behavior is unchanged.
- If old report payloads are later moved cold, the hot serving DB must first materialize all response-critical fields and indexes required by history, snapshot, coverage, and cross-section queries.

Schema rules:

1. Price values remain integer milli-units, not floating point, only after Sprint 0 verifies that the source `PriceMilli` values are milli-units for `TradeHistory`, `TradeLive`, `MinuteLive`, `QuoteSnapshot`, and `OrderHistory`.
2. Dates remain canonical strings in `YYYYMMDD`.
3. All timestamps written by lifecycle metadata use UTC `timestamp_ms`.
4. Nullability must match source semantics.
5. A schema change requires a new `schema_version` and a reader compatibility test.
6. If any source price field is not milli-unit, the Parquet schema must use `*_raw` plus a schema-level `price_unit` metadata entry instead of pretending it is milli-unit.

## Object Storage Readiness

Manifest records must never store only an absolute local filesystem path.

First-stage local-backed logical URI:

```text
tdx-cold://a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-{archive_batch_id_suffix}-000.parquet
```

`tdx-cold://` is the logical dataset URI scheme. It is not a filesystem path. `storage_scheme=local` maps that logical URI to `TDX_COLD_STORAGE_ROOT` in the first implementation.

Future object-store URI examples:

```text
s3://bucket/a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-{archive_batch_id_suffix}-000.parquet
r2://bucket/a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-{archive_batch_id_suffix}-000.parquet
minio://market-data/a-stock-market-tdx/cold/domain=trade/table=TradeHistory/instrument=sh600000/year=2024/part-{archive_batch_id_suffix}-000.parquet
```

Configuration:

| Env var | Purpose |
|---|---|
| `TDX_COLD_STORAGE_SCHEME` | `local`, later `s3`, `r2`, or `minio` |
| `TDX_COLD_STORAGE_ROOT` | local root path or object-store prefix |
| `TDX_COLD_STORAGE_DATASET` | logical dataset id, default derived from `TDX_DATA_DIR` |
| `TDX_COLD_RETENTION_YEARS` | cold retention horizon, default `7` |
| `TDX_LIFECYCLE_HOT_TRADING_DAYS` | must default to `180` |
| `TDX_LIFECYCLE_ENABLE` | enables scheduled lifecycle work |
| `TDX_LIFECYCLE_ALLOW_PRUNE` | separate safety switch for hot pruning |
| `TDX_LIFECYCLE_ALLOW_PRUNE_MIN_VERIFIED_SEGMENTS` | minimum verified segment count before pruning can run, default `100` |
| `TDX_LIFECYCLE_BATCH_MAX_SOURCE_BYTES` | max source DB bytes per batch |
| `TDX_LIFECYCLE_BATCH_MAX_ROWS` | max rows per batch |
| `TDX_LIFECYCLE_MIN_FREE_BYTES` | minimum free space required before writes |
| `TDX_LIFECYCLE_WORKDAY_MAX_STALE_CALENDAR_DAYS` | maximum tolerated calendar-day lag for `workday.db`, default `7` |
| `TDX_LIFECYCLE_POST_GOVERNANCE_COOLDOWN_MINUTES` | cooldown after close/audit window, default `5` |
| `TDX_LIFECYCLE_MAX_SKIP_COUNT` | max consecutive file-lease skips before priority handling, default `10` |

Storage interface:

```text
ColdStorage
  Put(ctx, uri, reader)
  Get(ctx, uri)
  Exists(ctx, uri)
  Stat(ctx, uri)
  Delete(ctx, uri)
  List(ctx, prefix)
  Rename(ctx, stagingURI, finalURI)
```

Local and object storage are different implementations of the same interface. The object-storage implementation may emulate `Rename` through copy-then-delete, but the manifest must not mark a segment `exported` until the final URI exists and the staged object is no longer the serving object.

Local `Rename` rules:

- The preferred path is same-filesystem `os.Rename` from staging URI to final URI.
- Local staging must be created under the same configured cold root as the final object.
- The local implementation must detect cross-device rename failure and either fail before export starts or use copy, fsync, checksum, and delete semantics.
- A copied object is not final until the destination checksum matches and the source staging object is removed or marked as owned cleanup debt.
- The manifest must record whether the finalization path was atomic rename or verified copy.

## Parquet and DuckDB Dependency Strategy

Stage 1 export and verification must not require a system DuckDB binary.

Default dependency decisions:

- Parquet write and row-level read verification should use a currently maintained Go Parquet library. The default candidate is `github.com/parquet-go/parquet-go`, the maintained successor to the archived `segmentio/parquet-go`.
- Sprint 0 must test the selected Parquet writer/reader with a representative 1,000,000-row dataset before Sprint 1 implementation starts.
- DuckDB is the preferred query engine for the future explicit cold API, not a dependency of daily hot collectors.
- Sprint 7 should use a pinned DuckDB CLI subprocess by default, configured by `TDX_DUCKDB_PATH`, to avoid forcing CGO into the main web binary.
- If a Go DuckDB binding is later chosen, it must be behind an explicit build tag and must document Docker and local build impact.
- The selected Parquet and DuckDB versions, maintenance status, and rollback choice must be recorded in Sprint 0 before implementation starts.

## Manifest Model

Decision: the manifest lives in a dedicated governance-owned SQLite DB:

```text
${TDX_DATA_DIR}/governance/cold_manifest.db
```

Rationale:

- It keeps manifest state separate from cold files, so deleting or moving cold objects does not delete the ledger.
- It avoids mixing lifecycle state with per-domain source DBs.
- It can later be copied or migrated independently if cold data moves to object storage.

Ownership and consistency:

- `cold_manifest.db` is owned by the lifecycle subsystem, created under the governance directory, and opened independently from `system_governance.db`.
- `system_governance.db` remains the source of truth for job lease and run status.
- `cold_manifest.db` remains the source of truth for segment identity, batch progress, verification, and pruning state.
- There is no cross-SQLite transaction between the two DBs. Consistency is achieved with durable `archive_batch_id`, idempotent state transitions, and restart reconciliation.
- A lifecycle batch must commit manifest state before marking a governance run passed.
- If governance run status and manifest state disagree after restart, the manifest state wins and the governance run is reconciled as interrupted or failed.

Minimum tables:

```text
cold_segment
  segment_id
  dataset_id
  domain
  table_name
  instrument
  partition_key
  start_date
  end_date
  trading_day_count
  row_count
  byte_size
  file_checksum
  logical_checksum
  schema_version
  storage_scheme
  cold_uri
  archive_batch_id
  source_db_path
  source_db_size
  source_selection_hash
  status
  error_code
  error_message
  created_at
  exporting_at
  exported_at
  verified_at
  restore_tested_at
  pruning_at
  hot_pruned_at
  active_at
  updated_at

lifecycle_batch
  archive_batch_id
  job_id
  status
  domain
  table_name
  started_at
  finished_at
  bytes_planned
  bytes_written
  rows_planned
  rows_written
  cutoff_date
  error_code
  error_message

hot_retention_policy
  domain
  retention_trading_days
  effective_trading_day
  current_cutoff_date
  evaluated_at

lifecycle_watermark
  domain
  table_name
  instrument
  hot_min_date
  cold_max_date
  last_archived_date
  last_active_segment_id
  updated_at

lifecycle_restore
  restore_id
  segment_id
  archive_batch_id
  restore_mode
  status
  requested_at
  started_at
  finished_at
  target_db_path
  expires_at
  retention_override_until
  row_count_restored
  file_checksum
  logical_checksum
  error_code
  error_message
```

Restore modes:

| Mode | Purpose | Retention impact | Cleanup |
|---|---|---|---|
| `temporary_query_restore` | Rebuild a SQLite subset for inspection, export, or one-off cold query validation | Does not change hot retention policy and must not be read by existing public APIs | Requires `expires_at`; lifecycle cleanup deletes the restored file after TTL |
| `hot_path_restore` | Restore cold rows back into the hot serving path after an operator request or rollback | Requires `retention_override_until` or an explicit lifecycle opt-out for the restored date range | Restored rows stay in hot DB until the override expires or the operator re-archives them |

Restore rules:

- A restore request must choose exactly one mode.
- Temporary restore output must live under a lifecycle-owned restore directory, not the normal hot DB path.
- Hot-path restore must update lifecycle metadata so the next maintenance run does not immediately re-archive the restored rows.
- Restore operations are audited independently from segment status; an active cold segment remains `active` after either restore mode.

Required indexes:

```sql
CREATE INDEX idx_cold_segment_domain_instrument_dates
  ON cold_segment(domain, instrument, start_date, end_date)
  WHERE status IN ('active', 'hot_pruned');

CREATE INDEX idx_cold_segment_domain_table_dates
  ON cold_segment(domain, table_name, start_date, end_date)
  WHERE status = 'active';

CREATE INDEX idx_cold_segment_batch
  ON cold_segment(archive_batch_id);

CREATE INDEX idx_cold_segment_status_updated
  ON cold_segment(status, updated_at);

CREATE INDEX idx_lifecycle_batch_status_started
  ON lifecycle_batch(status, started_at);
```

Cold-retention rules:

- `TDX_COLD_RETENTION_YEARS` defaults to `7`.
- Active cold segments are retained until they age beyond the cold-retention horizon and an explicit purge task is approved.
- `superseded` segments may expire earlier, but only after their replacement segment is `active` and restore-tested.
- Cold retention affects cold purge eligibility only; it must never cause current hot data to be deleted.
- The first implementation records purge eligibility but does not automatically delete cold data.

Status state machine:

```text
planned -> exporting -> exported -> verifying -> verified
verified -> restore_testing -> restore_tested
restore_tested -> pruning -> hot_pruned -> active

planned -> failed
exporting -> failed
exported -> failed
verifying -> failed
verified -> failed
restore_testing -> failed
restore_tested -> failed
pruning -> failed
failed -> planned

active -> superseded
```

Rules:

- Only `verified` segments may enter restore testing.
- Only `restore_tested` segments may be pruned from hot SQLite.
- Only `hot_pruned` or `active` segments count as released space.
- Failed segments must be retryable without creating duplicate active cold data.
- Retry from `failed` must either reuse the same `archive_batch_id` after cleaning owned staging files or create a superseding batch that points back to the failed segment.
- `active` means the cold segment is the authoritative historical copy for its date range.
- Restore is an operation recorded in `lifecycle_restore`; it does not move an `active` segment out of `active`.
- `superseded` is allowed only when a newer active segment covers the same logical range and passes all verification checks.
- Manifest rows are append-only for identity fields after creation.
- Status updates are allowed only through valid state transitions.
- A repeated batch must reuse or supersede failed `archive_batch_id` metadata instead of producing untracked orphan files.

## Checksum and Verification

Every segment must have two checksums:

| Checksum | Purpose |
|---|---|
| `file_checksum` | hash of the finalized Parquet file bytes |
| `logical_checksum` | hash of the canonical row stream independent of file encoding |

Both checksums use SHA-256 with lowercase hex encoding. This matches the existing project convention used for professional-finance source checksums.

Logical checksum rules:

- Sort rows by the table's stable key before hashing.
- Use Parquet column names in schema order and record the SQLite source-column mapping used to produce them.
- Encode nulls explicitly.
- Encode integers as base-10 text.
- Encode floats with `strconv.FormatFloat(v, 'G', 16, 64)` and reject NaN or infinity with a blocking batch error.
- Encode strings as UTF-8 with length prefixes.
- Include `row_count`, `min_date`, `max_date`, and `schema_version` in the checksum envelope.

Stable keys:

| Table | Stable key |
|---|---|
| `TradeHistory` | `code, trade_date, trade_time, seq` |
| `TradeMinute*Bar` | `code, trade_date, bucket_time` |
| `TradeLive` | `code, trade_date, trade_time, seq` |
| `MinuteLive` | `code, trade_date, clock` |
| `QuoteSnapshot` | `code, capture_time` |
| `OrderHistory` | `code, trade_date, seq` |
| `prof_finance_source_value_raw` | `source_file_id, full_code, report_date, source_field_id` |
| `prof_finance_source_file` | `source_file_id` |
| `prof_finance_source_report` | `source_report_id` |

Professional-finance auto-increment IDs must not be used as the only logical identity when an equivalent natural key exists. `source_value_id` is preserved as a cold column if archived, but it is not part of the stable key because restored SQLite rows may receive different auto-increment values.

Verification gates:

1. Source selection row count equals exported row count.
2. Source min/max date equals manifest min/max date.
3. Source logical checksum equals cold logical checksum.
4. Final cold file exists and its file checksum matches the manifest.
5. The Stage 1 Go Parquet reader can read the file and reproduce the row count.
6. Restore test can rebuild an equivalent SQLite subset before any pruning is allowed.

## Same-Disk Migration Protocol

The local machine has limited free space, so lifecycle migration must be streaming and bounded.

Batch defaults:

| Limit | Default |
|---|---:|
| Minimum free space before export | 20G |
| Minimum free space before hot rebuild | 20G |
| Protection mode free-space threshold | 5G |
| Max source DB per batch | 5G |
| Max rows per batch | 5,000,000 |
| Max lifecycle runtime per invocation | 30 minutes |
| Max concurrent lifecycle batches | 1 |

Space model:

```text
additional_free_space_required =
  estimated_cold_staging_bytes
  + estimated_replacement_hot_sqlite_bytes
  + safety_margin_bytes

peak_total_footprint_during_batch =
  original_sqlite_bytes
  + estimated_cold_staging_bytes
  + estimated_replacement_hot_sqlite_bytes
  + existing_cold_bytes
```

The pilot report must record current SQLite size, cold Parquet size, replacement hot SQLite size, compression ratio, peak additional free space required, and actual bytes released after `.bak` cleanup.

Initial planning baseline, to be replaced by Sprint 0 measurements:

| Scenario | Rough expectation |
|---|---:|
| Typical large per-instrument DB | 50MB - 500MB source SQLite |
| 180-trading-day retained hot share | 5% - 20% of rows for multi-year trade/live DBs |
| Cold Parquet compressed size | 20% - 40% of exported SQLite row payload for integer-heavy tables |
| Replacement hot SQLite size | retained rows plus rebuilt indexes, usually 5% - 25% of source DB for old multi-year files |
| Peak additional free space per batch | cold staging plus replacement hot SQLite plus safety margin |

This baseline is intentionally conservative and must not be used for pruning decisions. Sprint 0 must replace it with measurements from representative DBs, including one all-cold or near-all-cold worst case.

Space gates:

- The peak-space model must be implemented as a tested function before Sprint 2 starts.
- The model must be validated against representative DBs, including one worst-case DB where almost all rows are cold.
- Sprint 4 bulk catch-up is blocked until the pilot proves the peak-space model on real files.
- `existing_cold_bytes` must be recomputed for every batch because it grows during catch-up.
- If free space is tight, process the smallest feasible DBs first to create headroom before larger DBs.
- If a single DB cannot fit the peak-space model, split by table/year chunk where possible or defer it until more local or object-store space exists.
- The scheduler must choose only candidates whose estimated additional free-space requirement fits the current watermark.

Concurrency model:

- Lifecycle must acquire both a governance lifecycle lease and a domain/instrument file lease before touching a per-instrument DB.
- The file lease must block new collector writes for the same domain and instrument.
- If an existing collector write or long read is active, lifecycle must skip that DB and retry in a later batch.
- The final rename window must hold an exclusive file lease.
- Lifecycle must never rename a DB that a higher-priority hot collector job may still write.
- `QuoteSnapshot` uses a domain-level `live/quotes.db` lease rather than an instrument-level lease.
- Lock order is always governance lifecycle lease first, then file lease.
- No code path may acquire a governance lease while already holding a file lease.
- Regular hot collectors that run under governance must also acquire governance first, then file lease.
- Non-governed hot writes may acquire only the file lease and must not upgrade to governance while holding it.
- Lock-order behavior must be covered by contract tests.
- If the same DB is skipped `TDX_LIFECYCLE_MAX_SKIP_COUNT` times, the next lifecycle run prioritizes it and briefly blocks new same-file writes after higher-priority jobs have finished.

Per-instrument DB migration algorithm:

1. Acquire lifecycle lease from governance.
2. Acquire the domain/instrument file lease, or the `live/quotes.db` domain lease for `QuoteSnapshot`.
3. Recompute and persist the current cutoff.
4. Select one candidate source DB under the batch size limits.
5. Estimate candidate rows, retained rows, cold bytes, and replacement hot DB bytes.
6. Abort before writes if free space is below the configured watermark or the peak-space model fails.
7. Write Parquet to `${TDX_DATA_DIR}/cold/_staging/{archive_batch_id}/...tmp`.
8. Compute source row count, min/max date, and logical checksum while streaming.
9. Finalize the cold file through `ColdStorage.Rename`.
10. Insert or update manifest status to `exported`.
11. Read the finalized cold file through the Stage 1 Go Parquet reader and mark `verified` only if all checks pass.
12. Run restore test for the verified segment into a temporary SQLite DB and mark `restore_tested`.
13. Recheck the file lease and confirm no higher-priority writer is active.
14. Build replacement hot SQLite at `{source_db}.lifecycle.{archive_batch_id}.tmp` containing only retained rows.
15. Create required tables and indexes in the replacement DB.
16. Run `PRAGMA integrity_check`.
17. Compare retained hot row counts and sample queries against the original DB.
18. Rename original DB to `{source_db}.pre_lifecycle.{archive_batch_id}.bak`.
19. Atomically rename replacement DB to the original path.
20. Run hot API or internal read smoke tests.
21. Mark segment `hot_pruned`, then `active`.
22. Delete the `.bak` file only after status is `active` and smoke tests pass.

`live/quotes.db` migration algorithm:

`quotes.db` is a shared `live` database, not a per-instrument file. It must not use the per-instrument candidate loop.

1. Acquire lifecycle lease and the exclusive `live/quotes.db` domain file lease.
2. Recompute cutoff and derive `capture_cutoff_date` from trading-day cutoff.
3. Export `QuoteSnapshot` rows where `capture_date < capture_cutoff_date` to partitioned Parquet by `capture_year`.
4. Verify row count, min/max `capture_date`, logical checksum, file checksum, and restore test.
5. Build `quotes.db.lifecycle.{archive_batch_id}.tmp` containing only retained `QuoteSnapshot` rows and required indexes.
6. Run `PRAGMA integrity_check` and compare retained-row counts against the original `quotes.db`.
7. Atomically replace `quotes.db` using the same `.bak` protocol as per-instrument DBs.
8. Mark segments `hot_pruned` then `active`.
9. If the peak-space model cannot fit full-file replacement, run a separate pilot comparing full replacement against bounded `DELETE` plus `VACUUM INTO`; do not prune `quotes.db` until that pilot passes.

`quotes.db` must never be pruned with an unbounded long-running `DELETE` transaction during market hours or while ticker/live capture may write snapshots.

Failure handling:

- If export fails, delete staging files and mark `failed`.
- If verification fails, keep the original hot DB untouched and mark `failed`.
- If replacement DB build fails, delete the replacement DB and keep the original hot DB untouched.
- If rename fails after the original DB was moved, rename `.bak` back immediately and mark `failed`.
- If process exits after original DB was moved but before completion, startup recovery must restore from `.bak` only for the interrupted lifecycle batch and must not start new archival work.
- Orphan staging files are cleaned only by an explicit lifecycle cleanup task that checks manifest ownership first.

Startup recovery state machine:

| Discovered state | Recovery action |
|---|---|
| Staging cold file exists | Look up `archive_batch_id`; if manifest owns it and status is active in-progress, complete or fail the export; otherwise mark as owned cleanup debt |
| Final cold file missing but manifest says `exported` or later | Recompute from staging ownership; if finalization cannot be proven, mark segment `failed` and keep hot DB unchanged |
| `{source_db}.pre_lifecycle.{archive_batch_id}.bak` exists | Look up manifest; if status is `pruning` or earlier, restore `.bak` to original path; if status is `hot_pruned` or `active`, verify replacement DB before deleting `.bak` |
| `{source_db}.lifecycle.{archive_batch_id}.tmp` exists | Look up manifest; if batch is still active and original DB is intact, delete tmp or resume replacement; otherwise mark cleanup debt |
| Manifest status and governance run disagree | Manifest state wins; governance run is reconciled to `interrupted` or `failed` |

Startup recovery must never start a new archival batch, bulk prune, or bulk rehydrate. It only completes, rolls back, or records debt for interrupted lifecycle operations.

Large DB rule:

- `professional_finance/prof_finance.db` must not use whole-file replacement until the pilot proves enough free space.
- Professional finance archival must be table-level and chunked.
- If table-level chunking cannot keep free space above watermarks, Stage 2 waits for Stage 1 reclaim before running.

## Governance Integration

The existing governance catalog currently contains:

| Catalog job | Priority | Schedule | Notes |
|---|---:|---|---|
| `startup_recovery` | 1 | service start | legacy `collector_startup_catchup` |
| `daily_open_refresh` | 2 | 09:00 | reference-domain refresh |
| `daily_close_sync` | 3 | 18:00 | recent-window sync |
| `daily_audit` | 4 | 19:00 | legacy `collector_daily_reconcile` |
| `deep_audit_backfill` | 5 | manual/low peak | historical audit and backfill |

Add two governance catalog jobs:

```text
data_lifecycle_restore      priority 6
data_lifecycle_maintenance priority 7
```

Priority order:

```text
startup_recovery
daily_open_refresh
daily_close_sync
daily_audit
deep_audit_backfill
data_lifecycle_restore
data_lifecycle_maintenance
```

Job responsibilities:

| Job | Lifecycle responsibility |
|---|---|
| `daily_open_refresh` | No cold-data work |
| `daily_close_sync` | Writes only recent hot data; no cold migration |
| `daily_audit` | Audits hot SLA, retention policy, and manifest readability without full cold scans; also covers legacy `collector_daily_reconcile` naming |
| `deep_audit_backfill` | May sample cold segments or schedule explicit cold repair outside daily windows |
| `startup_recovery` | Resumes interrupted lifecycle state only; must not start new heavy archival |
| `data_lifecycle_restore` | Explicit restore/rehydration operation from cold to temporary or replacement hot DB |
| `data_lifecycle_maintenance` | Low-priority archival, verification, pruning, and steady-state retention |

Non-catalog repair flows:

- Web repair handlers and repair-worker-like control paths are not governance catalog jobs.
- They may repair bounded hot gaps only.
- They must not perform cold archival or bulk rehydration unless they invoke `data_lifecycle_restore` explicitly.

Scheduling rules:

- Lifecycle maintenance is disabled unless `TDX_LIFECYCLE_ENABLE=1`.
- Hot pruning is disabled unless `TDX_LIFECYCLE_ALLOW_PRUNE=1`.
- Lifecycle maintenance must not run during 08:45-09:45 or 17:45-19:30 local time.
- Lifecycle maintenance is eligible only after the post-governance cooldown, default 5 minutes, so the practical evening start is 19:35.
- Lifecycle maintenance must pause if a higher-priority governance job is running or queued.
- Lifecycle maintenance holds the governance lease only for a small batch, not for the entire catch-up program.
- Each invocation has a strict runtime budget and must checkpoint before exiting.
- A retry must resume from manifest state rather than rescanning from scratch.
- Daily jobs must never wait for lifecycle maintenance to finish.

Available-window example:

- A normal trading day reserves 08:45-09:45 for open readiness and 17:45-19:30 for close sync plus audit.
- Lifecycle is eligible after 19:35 and before 08:45, subject to higher-priority governance work.
- With a 30-minute invocation budget, steady-state lifecycle must normally complete within one post-audit batch.
- If higher-priority work blocks lifecycle for multiple days, the system records lifecycle debt rather than expanding `daily_audit`.

Steady-state rule:

- After catch-up, lifecycle maintenance should process only the small amount of data that crosses the 180-trading-day boundary.
- If steady-state work exceeds the runtime budget for three consecutive trading days, `daily_audit` should raise lifecycle debt rather than expanding daily collector scope.

## Interaction With Existing Data Governance

The lifecycle system must cooperate with the existing governance pipeline instead of becoming another unbounded daily task.

| Existing task | Required behavior |
|---|---|
| 09:00 open refresh | Must finish its own hot readiness work; no cold export |
| 18:00 close sync | Must write latest bounded hot data; no historical replay caused by lifecycle |
| 19:00 audit/reconcile chain | `daily_audit`, including legacy `collector_daily_reconcile`, must audit hot SLA and lifecycle debt; no bulk migration |
| Deep audit | Allowed to sample cold coverage and enqueue explicit cold repair tasks |
| Web repair flows | Repair recent hot gaps only unless explicitly invoking `data_lifecycle_restore` |
| Startup recovery | Records interrupted debt and restores interrupted renames; no catch-up sweep |

Daily audit checks:

- Current cutoff exists and was evaluated recently.
- Hot stores do not contain dates far older than the retention policy after catch-up.
- Manifest has no invalid state transitions.
- A bounded sample of active cold segments is readable.
- Lifecycle debt is reported as debt, not repaired inside the daily audit.

## Professional Finance Plan

`professional_finance/prof_finance.db` is too large to leave as a vague future task.

The serving contract is `/api/v1/prof-finance/*`. It currently depends on queryable report versions and payloads. Therefore:

1. Do not archive `prof_finance_report_payload` or `prof_finance_report_version` until a slim serving DB proves identical API behavior.
2. First inventory table sizes, indexes, and query dependency by endpoint.
3. First archival candidates are raw/source tables and downloaded source-file metadata not needed at query time.
4. If payload history remains too large, build a slim serving table or DB that materializes exactly the fields needed by current APIs.
5. Contract tests for all professional finance endpoints must pass before any source-table pruning.

Professional finance sprint gates:

- Sprint 6 may not start until there is a table-size inventory with row counts, page counts or byte estimates, index sizes, and growth rates for every `prof_finance_*` table.
- Sprint 6 may not start until there is an endpoint dependency graph for `/api/v1/prof-finance/fields`, `history`, `snapshot`, `coverage`, and `cross-section`.
- The dependency graph must explicitly separate source/raw tables from hot serving tables.
- Raw/source archival must preserve enough metadata to reproduce query-visible report provenance.
- Table-size inventory identifies which tables actually consume the 63G.
- Query dependency map records which tables each endpoint needs.
- Cold archival of raw/source tables is implemented with table-level chunks.
- Hot serving DB remains sufficient for all current endpoints.
- No endpoint starts reading cold data implicitly.

## Collector DB and Governance History Retention

`collector.db` is not part of Stage 1 hot/cold archival, but it is already large enough to require a retention policy.

Minimum follow-up:

- Add a read-only report for task, validation, cursor, audit, and run-history table sizes.
- Keep current cursors and latest validation state hot.
- Retain detailed historical task logs for 90 days by default.
- Add `TDX_GOVERNANCE_TASK_RETENTION_DAYS`, default `90`, before enabling any automated governance-history pruning.
- Archive or summarize old run-history rows only after governance status pages and tests prove they do not require full detail.
- Do not mix collector DB retention with market-data cold migration in the same batch job.

## Future Cold API

Cold reads are a new explicit API surface.

Potential endpoints:

```text
GET  /api/v1/cold/segments
GET  /api/v1/cold/trade-history
GET  /api/v1/cold/live-history
GET  /api/v1/cold/order-history
POST /api/v1/cold/query
GET  /api/v1/cold/tasks/:id
```

Cold API rules:

- Cold access must be explicit.
- Query parameters must include domain, instrument or full code, and date range.
- Small single-instrument day-range queries may be synchronous.
- Large cross-date, cross-instrument, or cross-table queries must return `task_id`.
- Results may include cold-specific metadata because this is a new contract.
- Query execution must be bounded by row count, byte count, runtime, and concurrency.
- Cold API must have auth or explicit local-admin gating before broad access.
- Exported query result files must have a cleanup policy.

Default limits to calibrate in implementation:

| Limit | Initial value |
|---|---:|
| Sync max rows | 100,000 |
| Sync max bytes read | 256MB |
| Sync max runtime | 10 seconds |
| Async max runtime | 30 minutes |
| Concurrent cold queries | 1 local, configurable |

## Observability

Add lifecycle observability without changing existing public endpoint contracts.

Preferred surface:

```text
GET /api/collector/lifecycle/status
GET /api/collector/lifecycle/batches
GET /api/collector/lifecycle/segments
```

Minimum status fields:

- lifecycle enabled or disabled
- pruning enabled or disabled
- current effective trading day
- current cutoff date
- hot retention days
- free disk bytes
- current batch id
- current job state
- backlog by domain
- bytes planned
- bytes exported
- bytes pruned
- rows planned
- rows exported
- rows pruned
- segment counts by status
- last successful batch
- last failed batch and error
- next eligible run time

Alert conditions:

| Condition | Severity | Required signal |
|---|---|---|
| lifecycle maintenance failed for 3 consecutive eligible days | warning | lifecycle status, structured log, daily audit debt |
| free disk space below protection threshold | critical | lifecycle status, structured log, daily audit debt |
| any `failed` segment older than 24 hours | warning | lifecycle status and segment listing |
| any `pruning` segment remains in-progress after restart recovery | critical | startup recovery status |
| hot retention debt grows for 5 consecutive trading days | warning | daily audit debt |
| skip count exceeds `TDX_LIFECYCLE_MAX_SKIP_COUNT` | warning | lifecycle status and prioritized queue |

The first implementation may expose alerts through ops status JSON and structured logs. External notification delivery can be added later, but alert state must be visible without parsing raw logs.

Daily audit may include lifecycle summary internally, but existing public response shapes must not be changed unless the endpoint is explicitly versioned or documented as an ops endpoint with extensible fields.

## Sprint Plan

The sprint sequence is allowed, but each sprint is part of a committed path to the final target.

### Sprint 0: Contract Baseline and Current-State Inventory

Deliverables:

- Endpoint contract matrix for all affected existing APIs.
- Contract tests that assert current request, response, error, and data-source behavior.
- Existing `profinance` service and handler tests are reused as part of the baseline instead of being replaced.
- Read-only storage inventory by domain, table, file size, row count, min/max date, and index size where possible.
- Current 180-trading-day cutoff report from `workday.db`.
- `workday.db` stale-data guard definition and test fixture.
- Professional finance table-size and query-dependency report.
- Pilot-space estimate for representative large instruments, including peak additional free-space requirement.
- Tested peak-space model function using representative and worst-case all-cold DB samples.
- Go Parquet library selection proof with a representative 1,000,000-row dataset, including maintenance-status verification for `github.com/parquet-go/parquet-go` or any selected alternative.
- Price-unit verification for every Stage 1 `PriceMilli` source field.

Exit criteria:

- No writes to market data.
- No pruning.
- Contract tests are green.
- Candidate reclaim estimate is based on real row counts and pilot candidates.
- Sprint 1 is blocked if the Parquet proof or peak-space model cannot run inside current local disk limits.

### Sprint 1: Schema, Manifest, and Storage Foundation

Deliverables:

- `cold_manifest.db` schema and migrations.
- Required manifest indexes for segment lookup, batch lookup, and status lookup.
- Manifest state-machine tests.
- `ColdStorage` interface.
- Local storage implementation.
- URI parser and formatter for `tdx-cold://`, with storage-scheme mapping validation.
- Parquet schema definitions for Stage 1 tables.
- SQLite-to-Parquet column mapping definitions for every Stage 1 table.
- `AuctionSnapshot` schema, mapping, inventory, export, restore, and pruning support.
- Local `Rename` behavior for same-filesystem atomic rename and cross-device fallback or fail-fast.
- Pinned Go Parquet dependency decision.
- Logical checksum implementation and tests.
- File-lease design contract, including lock order and hot-writer integration points.
- Cold-retention policy metadata with default 7-year retention and `superseded` expiry eligibility.

Exit criteria:

- No hot data pruning.
- Manifest cannot enter invalid transitions.
- Local URI values can be remapped to future object-store schemes by config.
- No lifecycle implementation may proceed to pruning work until file-lease deadlock tests pass.

### Sprint 2: Single-Segment Export, Verify, and Restore

Deliverables:

- Export one verified segment for `trade.TradeHistory`.
- Export one verified segment for `live.TradeLive` or `MinuteLive`.
- Export one verified segment for `live.QuoteSnapshot` or explicitly prove it has no material reclaim value.
- Export one verified segment for `order_history.OrderHistory`.
- Go Parquet-reader verification for exported Parquet.
- Restore operation that rebuilds a SQLite subset from cold Parquet.
- `quotes.db` shared-database migration pilot or explicit defer decision if peak-space limits make it unsafe.
- Failure-injection tests for checksum mismatch, missing file, and interrupted export.

Exit criteria:

- Hot SQLite files remain unchanged.
- Restore from cold is proven before any pruning is enabled.
- Staging cleanup is manifest-owned and safe.
- Sprint 3 is blocked if the empirical peak-space model fails for the pilot DBs.

### Sprint 3: Safe Hot Rebuild and Pruning Pilot

Deliverables:

- Replacement hot SQLite builder.
- Atomic rename protocol with `.bak` recovery.
- File-lease implementation for per-instrument DBs and `live/quotes.db`.
- Hot retained-row verification.
- API/internal read smoke tests after replacement.
- Pilot on a small set of selected large instrument DBs.
- Actual compression and peak-space report.
- Startup recovery state-machine tests for staging, `.bak`, and `.tmp` interrupted states.

Exit criteria:

- Pilot releases real disk space.
- Restore after pruning is tested.
- Rollback from `.bak` and cold rehydrate both work.
- Pruning remains disabled by default outside the pilot.

### Sprint 4: Bulk Stage 1 Catch-Up

Deliverables:

- Scheduled `data_lifecycle_maintenance`.
- Registered `data_lifecycle_restore` and `data_lifecycle_maintenance` in the governance catalog with priorities after `deep_audit_backfill`.
- Batch budgeting, disk watermarks, and governance lease integration.
- Domain/instrument file lease integration.
- Space-feasible candidate ordering after pilot validation, using smallest feasible DBs first under pressure and larger DBs only when headroom exists.
- Catch-up for `trade`, `live`, `order_history`, and `auction`.
- Lifecycle status endpoints.
- Daily audit lifecycle debt reporting.

Exit criteria:

- `trade`, `live`, `order_history`, and `auction` hot stores contain only latest 180 trading days after catch-up.
- Cold manifest accounts for all pruned historical ranges.
- Existing API contract tests pass unchanged.
- Daily 09:00 / 18:00 / 19:00 jobs do not inherit lifecycle workload.
- Net disk reclaim is measured and documented.
- Bulk catch-up is blocked until the pilot proves real disk release and the scheduler can select only candidates that fit the peak-space model.

### Sprint 5: Steady-State Retention

Deliverables:

- Ongoing maintenance that archives only newly cold rows crossing the retention boundary.
- Consecutive-day lifecycle debt detection.
- Automatic pause/resume on disk pressure and governance contention.
- Monitoring for file counts, segment counts, and cold read sample health.

Exit criteria:

- Hot stores remain at 180 trading days over multiple daily cycles.
- Daily lifecycle work stays within the runtime budget.
- Missed lifecycle work becomes visible debt, not hidden work inside daily audit.

### Sprint 6: Professional Finance Slimming

Deliverables:

- Full table-size inventory with row counts, byte estimates, index sizes, and growth rates.
- Endpoint dependency graph for all `/api/v1/prof-finance/*` endpoints.
- Explicit hot-serving versus cold-source table split.
- Table-level chunked archival for safe raw/source candidates.
- Slim serving DB or serving-table design if payload/history still dominates.
- Contract tests for all `/api/v1/prof-finance/*` endpoints.
- Reclaim report for source/raw archival and any serving split.

Exit criteria:

- Professional finance disk growth is controlled.
- Current API behavior remains unchanged.
- Any archived professional finance table is restorable or reproducible from cold.
- No raw/source table is pruned until endpoint dependency tests prove it is not required for hot serving.

### Sprint 7: Explicit Cold Query API

Deliverables:

- `/api/v1/cold/segments`.
- At least one explicit cold read endpoint for trade history.
- Bounded sync query execution.
- Async task path for large reads.
- Auth or local-admin gating.
- Query result cleanup.
- Pinned DuckDB CLI strategy or documented build-tagged Go binding decision.

Exit criteria:

- Cold data is queryable through new explicit APIs.
- Existing APIs still do not read cold data.
- Cold API limits prevent unbounded local scans.

### Sprint 8: Object Storage Migration Readiness

Deliverables:

- Object storage config contract.
- Dry-run object-store URI remapping.
- Copy verification plan from local-backed `tdx-cold://` storage to future object-store-backed storage.
- Manifest update protocol for storage-scheme migration.

Exit criteria:

- Moving cold files to object storage later does not require changing logical segment identity.
- Local cold store remains usable until object storage is actually available.

## Testing Plan

Required test categories:

| Category | Required coverage |
|---|---|
| Contract tests | Existing endpoint request/response/error behavior |
| Cutoff tests | trading-day cutoff, holidays, weekends, pre-close behavior |
| Workday freshness tests | stale `workday.db` refuses lifecycle writes |
| Manifest tests | valid transitions, invalid transitions, idempotent retry |
| Manifest index tests | segment lookup by instrument/date, table/date, batch, and status |
| URI tests | local URI parsing, future scheme validation, path normalization |
| Schema tests | SQLite row to Parquet row mapping for every Stage 1 table |
| Price-unit tests | verify every Stage 1 `PriceMilli` field is milli-unit or switch to raw/unit metadata |
| Checksum tests | deterministic logical checksum independent of Parquet encoding |
| Space-model tests | peak-space calculation and candidate selection under low free space |
| Export tests | row count, min/max date, checksum, final file exists |
| Restore tests | cold Parquet to SQLite subset |
| Rebuild tests | retained hot DB row counts, indexes, integrity check |
| QuoteSnapshot tests | shared `quotes.db` export, replacement, and rollback behavior |
| Concurrency tests | lifecycle skips DBs with active writer or file lease contention; lock order prevents deadlock |
| Failure injection | low disk, interrupted export, interrupted rename, missing cold file |
| Governance tests | priority, pause/resume, runtime budget, retry after restart |
| Startup recovery tests | staging, `.bak`, `.tmp`, and governance/manifest disagreement states |
| Alert tests | failed segment age, consecutive failures, disk protection, skip threshold |
| Dependency tests | pinned Parquet library behavior and DuckDB CLI or binding availability |
| Performance tests | pilot export rate, query rate, peak disk usage, file count |

No sprint may be accepted if it only adds implementation without tests for its failure modes.

## Safety and Rollback

Each archival batch must be restartable.

Required checks before pruning hot data:

- `TDX_LIFECYCLE_ENABLE=1`
- `TDX_LIFECYCLE_ALLOW_PRUNE=1`
- verified segment count is at least `TDX_LIFECYCLE_ALLOW_PRUNE_MIN_VERIFIED_SEGMENTS`
- cold file exists
- manifest row is `verified`
- restore test has passed
- row count matches source selection
- date range matches source selection
- logical checksum matches expected value
- file checksum matches finalized cold object
- hot replacement DB passes `PRAGMA integrity_check`
- retained-row counts match the original DB
- existing API contract tests pass

Rollback model:

- Before `hot_pruned`, rollback means deleting cold staging/final files and resetting manifest status to `failed` or `planned`.
- During replacement, rollback means renaming `.bak` back to the original DB path.
- After `hot_pruned`, rollback means running `data_lifecycle_restore` from cold segment into a replacement SQLite file.
- Rehydrate must be explicit and audited.
- Startup recovery may only finish an interrupted rename recovery; it must not start new archival, pruning, or bulk rehydration.

## Final Acceptance Criteria

The program is complete only when all criteria below are true:

1. Existing API contract tests pass unchanged.
2. Existing endpoints do not read cold Parquet.
3. `trade`, `live`, `order_history`, and `auction` hot stores contain only the latest 180 trading days after catch-up.
4. Ongoing lifecycle maintenance keeps those hot stores at 180 trading days over normal daily cycles, including `live/quotes.db` and `auction/auction.db` if they have material retained history.
5. Cold manifest accounts for every pruned historical date range.
6. Every active cold segment has row count, min/max date, file checksum, logical checksum, and schema version.
7. `data_lifecycle_restore` can restore a pruned segment.
8. `daily_close_sync`, `daily_audit` including legacy `collector_daily_reconcile`, `deep_audit_backfill`, and `startup_recovery` do not perform bulk cold migration.
9. Disk free space remains above configured warning watermarks during and after catch-up.
10. Professional finance raw/source growth is controlled without changing `/api/v1/prof-finance/*`.
11. Lifecycle status exposes backlog, bytes, rows, segment states, and failures.
12. Cold query capability is available only through explicit `/api/v1/cold/*` endpoints.
13. Cold URI values can later migrate from `tdx-cold://` to object-storage-backed storage schemes without changing segment identity.
14. Cold retention policy is recorded and purge eligibility is visible without automatic deletion.
15. Alert conditions are visible through lifecycle status and structured logs.
16. There is no remaining required manual step for keeping hot data at 180 trading days.

## Implementation Stop Conditions

Lifecycle jobs must stop automatically when:

- free disk space falls below the write watermark
- peak-space model says the candidate cannot fit current free space
- `workday.db` is stale beyond configured tolerance
- a higher-priority governance job is active or queued
- current invocation reaches runtime budget
- file count or segment count exceeds configured warning thresholds
- file lease skip count exceeds threshold and forced handling still cannot obtain a safe window
- checksum or row-count verification fails
- hot replacement DB integrity check fails
- pruning is enabled before the minimum verified-segment threshold is reached
- existing API contract smoke tests fail
- manifest enters any invalid state

Stop conditions must produce visible lifecycle debt and actionable error messages. They must not silently retry inside daily governance jobs.

# Hot/Cold Data Lifecycle Sprint Progress

## Sprint 0: Contract Baseline and Current-State Inventory

### Deliverables
- [x] Endpoint contract matrix and HTTP contract baseline tests.
- [x] Read-only storage inventory by domain, table, file size, row count, date range, and SQLite size signals.
- [x] 180-trading-day cutoff calculation from `workday.db`.
- [x] `workday.db` stale-data guard with exchange-calendar explanation.
- [x] Professional finance table-size and endpoint-dependency report.
- [x] Peak-space model with representative and worst-case tests.
- [x] Go Parquet library proof with 1,000,000 rows.
- [x] PriceMilli source-field verification for Stage 1.

### Exit Criteria Verification
- [x] `go test ./... -count=1`全部通过。
- [x] 无市场数据写入。
- [x] 无 pruning。
- [x] contract 测试覆盖所有受影响 endpoint。
- [x] Sprint 1 blocker 检查：Parquet proof 通过且 peak-space 模型在当前磁盘下可运行。

### Test Results
- `go test ./collector/lifecycle -count=1`: PASS.
- `(cd web && go test -run TestLifecycleAffectedLegacyEndpointsKeepErrorEnvelope -count=1)`: PASS.

### Notes
Sprint 0 implementation is restricted to read-only inventory, contract assertions, local temp-file tests, and planning reports. It must not touch real market-data files under `TDX_DATA_DIR` or workspace `state`.

## Sprint 1: Schema, Manifest, and Storage Foundation

### Deliverables
- [x] Manifest state-machine tests and implementation.
- [x] `cold_manifest.db` schema, migrations, required indexes, and cold-retention metadata.
- [x] `ColdStorage` interface and local implementation.
- [x] `tdx-cold://` URI parser and formatter.
- [x] Stage 1 Parquet schema definitions and SQLite-to-Parquet column mapping.
- [x] SHA-256 logical checksum and file checksum.
- [x] File lease contract with governance-first lock order.

### Exit Criteria Verification
- [x] 无热数据 pruning。
- [x] Manifest 无法进入非法转换。
- [x] Local URI values can be remapped to future object-store schemes by config.
- [x] File-lease deadlock tests pass.

### Test Results
- `go test ./collector/lifecycle -count=1`: PASS.

### Notes
Sprint 1 is storage-foundation only. It may create temp manifest DBs and temp cold files in tests, but must not mutate hot market stores.

## Sprint 2: Single-Segment Export, Verify, and Restore

### Deliverables
- [x] Export one verified segment for `trade.TradeHistory`.
- [x] Export one verified segment for `live.TradeLive` or `MinuteLive`.
- [x] Export one verified segment for `live.QuoteSnapshot` or explicitly prove it has no material reclaim value.
- [x] Export one verified segment for `order_history.OrderHistory`.
- [x] Go Parquet-reader verification for exported Parquet.
- [x] Restore operation that rebuilds a SQLite subset from cold Parquet.
- [x] `quotes.db` shared-database migration pilot or explicit defer decision.
- [x] Failure-injection tests for checksum mismatch, missing file, and interrupted export.

### Exit Criteria Verification
- [x] Hot SQLite files remain unchanged.
- [x] Restore from cold is proven before any pruning is enabled.
- [x] Staging cleanup is manifest-owned and safe.
- [x] Sprint 3 is blocked if empirical peak-space model fails for pilot DBs.

### Test Results
- `go test ./collector/lifecycle -count=1`: PASS.

### Notes
Sprint 2 exports only from temp fixture SQLite files in tests. It must not prune or rewrite source DBs.

## Sprint 3: Safe Hot Rebuild and Pruning Pilot

### Deliverables
- [x] Replacement hot SQLite builder.
- [x] Atomic rename protocol with `.bak` recovery.
- [x] File-lease implementation for per-instrument DBs and `live/quotes.db`.
- [x] Hot retained-row verification.
- [x] API/internal read smoke tests after replacement.
- [x] Pilot on a small set of selected large instrument DBs.
- [x] Actual compression and peak-space report.
- [x] Startup recovery state-machine tests for staging, `.bak`, and `.tmp` interrupted states.

### Exit Criteria Verification
- [x] Pilot releases disk space in temp hot-store fixtures; real-data pilot remains gated by env safety switches.
- [x] Restore after pruning is tested.
- [x] Rollback from `.bak` and cold rehydrate both work.
- [x] Pruning remains disabled by default outside the pilot.

### Test Results
- `go test ./collector/lifecycle ./collector -run 'TestScheduler|TestMaintenance|TestGovernanceCatalogIncludesLifecycleJobs|Test.*Lifecycle|Test.*Segment|Test.*Manifest|Test.*Cold|Test.*URI|Test.*Checksum|Test.*FileLease|Test.*Replacement|Test.*Export|Test.*Parquet|Test.*Price|Test.*Inventory|Test.*Cutoff' -count=1`: PASS.

### Notes
Sprint 3 replacement tests use temp DB fixtures only. Real pruning remains disabled by default and is not run against the local market-data state.

## Sprint 4: Bulk Stage 1 Catch-Up

### Deliverables
- [x] Scheduled `data_lifecycle_maintenance` model and runner contract.
- [x] Registered `data_lifecycle_restore` and `data_lifecycle_maintenance` in the governance catalog with priorities after `deep_audit_backfill`.
- [x] Batch budgeting, disk watermarks, and governance contention gates.
- [x] Domain/instrument file lease integration contract.
- [x] Space-feasible candidate ordering.
- [x] Size-sorted bounded candidate discovery that does not deep-inventory every SQLite DB before selecting a small batch.
- [x] Governance-recorded CLI executor for controlled local catch-up runs.
- [x] Same-DB multi-table grouped pruning so one hot SQLite replacement can prune multiple archived tables.
- [x] Restore-test DB cleanup after successful verification, unless explicitly retained for debugging.
- [x] Catch-up executor for `trade`, `live`, and `order_history`; real-data catch-up remains gated by `TDX_LIFECYCLE_ENABLE=1` and `TDX_LIFECYCLE_ALLOW_PRUNE=1`.
- [x] Lifecycle status endpoints.
- [x] Daily audit lifecycle debt reporting model.

### Exit Criteria Verification
- [x] `trade`, `live`, and `order_history` hot stores contain only latest 180 trading days after catch-up; verified by the 2026-04-26 full dry-run with `SelectedCandidates=0`.
- [x] Cold manifest accounts for all pruned historical ranges; verified by active manifest rows for every pruned segment and zero failed segments after real-data catch-up.
- [x] Existing API contract tests pass unchanged.
- [x] Daily 09:00 / 18:00 / 19:00 jobs do not inherit lifecycle workload.
- [x] Net disk reclaim is measured in temp fixtures and real data; final real-data state recovered disk free space to about 173GiB.
- [x] Bulk catch-up is blocked until pilot proves real disk release and scheduler can select only candidates that fit the peak-space model.

### Test Results
- `go test ./collector/lifecycle ./collector -run 'TestScheduler|TestMaintenance|TestGovernanceCatalogIncludesLifecycleJobs' -count=1`: PASS.
- `(cd web && go test -run 'TestHandleCollectorLifecycleStatusReturnsManifestSummary|TestLifecycleAffectedLegacyEndpointsKeepErrorEnvelope' -count=1)`: PASS.

### Notes
Bulk catch-up execution against real local data remains gated by pruning flags, verified-segment thresholds, and disk measurements.

### Real Local Catch-Up Progress: 2026-04-25 to 2026-04-26
- Added `cmd/lifecycle-maintenance` for manual, governance-recorded, JSONL-emitting catch-up execution against the configured `TDX_DATA_DIR`.
- First real pilot using full deep inventory was stopped after it held the governance lock for several minutes while reading a large DB; the run was marked `interrupted` and no cold segments were created. This drove the size-sorted bounded candidate discovery fix.
- `RestoreSegment` was optimized from per-row autocommit inserts to a single transaction with lifecycle write PRAGMAs. This removed the restore-test bottleneck while preserving export, checksum verify, restore row-count verify, hot retained-row verify, and atomic replacement.
- After disk pressure was relieved and `professional_finance` background refresh was disabled locally, catch-up safely expanded from 2-candidate pilots to bounded 96, 160, 200, 400, and final 1325-candidate batches.
- `max-archive-days=0` was used only after real-file inspection showed remaining hot SQLite files were small enough for full pre-cutoff segments. This avoided repeatedly rebuilding the same DB for two-year slices.
- Final full dry-run at `2026-04-26 08:21-08:24 CST` returned `SelectedCandidates=0`, `SkippedCandidates=0`, and `FailedSegments=0` for the real `trade`, `live`, and `order_history` hot stores.
- Final lifecycle status recorded `active=11444` cold segments and `1,513,194,080` archived rows.
- Stage 1 measured local disk state after catch-up: free space about `173GiB`; `trade=34G`, `live=29G`, `order_history=1.6G`, local cold Parquet store `5.5G`, and `cold_restore=0B`.
- `fundamentals/professional_finance` was intentionally excluded from Stage 1 pruning because current query behavior depends on `prof_finance_report_version`, `prof_finance_report_payload`, and `prof_finance_source_file`, while `prof_finance_source_value_raw` was still used by rebuild flows. It was later handled by the Sprint 6 serving-only slimming path.

## Sprint 5: Steady-State Retention

### Deliverables
- [x] Ongoing maintenance that archives only newly cold rows crossing the retention boundary.
- [x] Consecutive-day lifecycle debt detection.
- [x] Automatic pause/resume on disk pressure and governance contention.
- [x] Monitoring for file counts, segment counts, and cold read sample health.

### Exit Criteria Verification
- [ ] Hot stores remain at 180 trading days over multiple daily cycles; requires enabled scheduled runtime observation.
- [x] Daily lifecycle work stays within runtime budget through bounded `TDX_LIFECYCLE_MAX_CANDIDATES`, `TDX_LIFECYCLE_MAX_ARCHIVE_DAYS`, and `TDX_LIFECYCLE_RUNTIME_BUDGET`.
- [x] Missed lifecycle work becomes visible debt.

### Test Results
- `go test ./collector/lifecycle -run 'TestPlanSteady|TestLifecycleDebt|TestSteadyStateGate' -count=1`: PASS.

### Notes
Steady-state enforcement is implemented as bounded planning and visible debt; real hot-store mutation remains gated.

## Sprint 6: Professional Finance Slimming

### Deliverables
- [x] Full table-size inventory with row counts, byte estimates, index sizes, and growth rates.
- [x] Endpoint dependency graph for all `/api/v1/prof-finance/*` endpoints.
- [x] Explicit hot-serving versus cold-source table split.
- [x] Table-level chunked archival for safe raw/source candidates.
- [x] Slim serving DB or serving-table design if payload/history still dominates.
- [x] Contract tests for all `/api/v1/prof-finance/*` endpoints.
- [x] Reclaim report for source/raw archival and any serving split.

### Exit Criteria Verification
- [x] Professional finance disk growth is controlled by tested raw/source archival planning.
- [x] Current API behavior remains unchanged.
- [x] Any archived professional finance table is restorable or reproducible from cold.
- [x] No raw/source table is pruned until endpoint dependency tests prove it is not required for hot serving.

### Test Results
- `go test ./collector/lifecycle -run 'TestProfessionalFinance' -count=1`: PASS.
- Existing `profinance` and web prof-finance handler suites are included in `go test ./... -count=1` and `(cd web && go test ./... -count=1)`: PASS.
- `go test ./profinance -run 'TestNewService.*AutoPrefetch' -count=1`: PASS.
- `(cd web && TDX_WEB_SKIP_INIT=1 go test -run 'TestBuildProFinanceConfig|TestDecideProFinanceAutoPrefetch|TestLifecycleAffectedLegacyEndpointsKeepErrorEnvelope' -count=1)`: PASS.

### Notes
Professional finance slimming must preserve the existing `/api/v1/prof-finance/*` service contract and only archive tables classified as raw/source candidates. On 2026-04-25 the local runtime showed `fundamentals/professional_finance/prof_finance.db` at about 69G and startup prefetch was still able to write under disk pressure. Web startup now passes a protected `profinance.Config`: `PROFINANCE_DISABLE_BACKGROUND_REFRESH=1` disables startup and scheduled background refresh for local recovery windows, and `PROFINANCE_MIN_FREE_BYTES` prevents automatic refresh when free disk is below the configured watermark. Query endpoints still use the existing service and response contract.

### Real Local Slimming Progress: 2026-04-26
- Service was stopped before replacement, and no process held `prof_finance.db` while the hot DB was archived and replaced.
- The original `69G` `fundamentals/professional_finance/prof_finance.db` was source-checksummed as `741dbe99ea5d8318550b44dc4dd6da39ae2a59fd7ef3241b3aeb982a0cb9f17e`.
- A complete cold full-DB archive was written to `tdx-cold://a-stock-market-tdx/cold/domain=professional_finance/table=prof_finance_full_db/year=2026/prof_finance-20260426T085900.db.zst`, verified with `zstd -t`, and checksummed as `8f6b6c5a71c5cb5ecac7acf4d0f8ce26151447c22e74d7f12e3cd679f05278d4`.
- The hot replacement DB keeps `915643` `prof_finance_report_version` rows, `915643` `prof_finance_report_payload` rows, `319` `prof_finance_source_file` rows, `319` `prof_finance_source_report` rows, `403` field catalog rows, and `1` watermark row.
- `prof_finance_source_value_raw` is empty in the hot replacement DB, and all `319` copied source files are marked `archived`.
- `profinance.Service.Rebuild()` now refuses to rebuild from archived or missing raw source facts instead of deleting serving tables and rebuilding from an empty raw table.
- `profinance.Service.Sync()` treats same-hash `archived` source files as already materialized, preventing future syncs from rehydrating old raw rows into the hot DB.
- The replacement DB passed `PRAGMA integrity_check`, was atomically moved into `prof_finance.db`, and the service was rebuilt and restarted from the new binary.
- `/api/v1/prof-finance/history?full_code=sh600000&field_codes=book_value_per_share&as_of_date=20260425&period=all&limit=1` returned the expected hot-serving response after replacement.
- The old `69G` pre-slimming DB was deleted only after the cold archive, replacement integrity check, service restart, and API query verification passed.
- Final measured local disk state after Stage 1 plus Sprint 6: free space about `210GiB`; `fundamentals/professional_finance=16G`; professional-finance cold full-DB archive `11G`.

## Sprint 7: Explicit Cold Query API

### Deliverables
- [x] `/api/v1/cold/segments`.
- [x] At least one explicit cold read endpoint for trade history.
- [x] Bounded sync query execution.
- [x] Async task path for large reads.
- [x] Auth or local-admin gating.
- [x] Query result cleanup.
- [x] Pinned DuckDB CLI strategy or documented build-tagged Go binding decision.

### Exit Criteria Verification
- [x] Cold data is queryable through new explicit APIs.
- [x] Existing APIs still do not read cold data.
- [x] Cold API limits prevent unbounded local scans.

### Test Results
- `(cd web && go test -run 'TestColdSegmentsAndTradeHistoryAPIAreExplicitAndLocalAdminGated' -count=1)`: PASS.

### Notes
Cold APIs are explicit and separate from existing public APIs. Existing hot/provider endpoints continue to avoid cold reads by default.

## Sprint 8: Object Storage Migration Readiness

### Deliverables
- [x] Object storage config contract.
- [x] Dry-run object-store URI remapping.
- [x] Copy verification plan from local-backed `tdx-cold://` storage to future object-store-backed storage.
- [x] Manifest update protocol for storage-scheme migration.

### Exit Criteria Verification
- [x] Moving cold files to object storage later does not require changing logical segment identity.
- [x] Local cold store remains usable until object storage is actually available.

### Test Results
- `go test ./collector/lifecycle -run 'TestObjectStorage' -count=1`: PASS.

### Notes
Sprint 8 does not upload data. It defines and tests URI remapping and verification planning so a later object-storage backend can preserve segment identity.

## Final Acceptance Verification

### Test Results
- `go test ./... -count=1`: PASS.
- `(cd web && TDX_WEB_SKIP_INIT=1 go test ./... -count=1)`: PASS.
- `go test ./collector/lifecycle ./governance ./collector -run 'TestMaintenance|TestDataLifecycle|TestGovernanceCatalogIncludesLifecycleJobsAfterDeepAudit|TestExportVerifyAndRestoreStageOneSegments|TestRehydrate|TestBuildReplacement' -count=1`: PASS.
- `(cd web && go test -run 'TestExecuteDataLifecycleRestoreTemporaryAndHotPath|TestRunDataLifecycleMaintenanceArchivesAndPrunesTempHotStore|TestColdSegmentsAndTradeHistoryAPIAreExplicitAndLocalAdminGated|TestHandleCollectorLifecycleStatusReturnsManifestSummary' -count=1)`: PASS.

### Criteria
- [x] Existing API contract tests pass unchanged.
- [x] Existing endpoints do not read cold Parquet.
- [x] `trade`, `live`, and `order_history` hot stores contain only the latest 180 trading days after catch-up; verified by final full dry-run with zero candidates.
- [ ] Ongoing lifecycle maintenance keeps those hot stores at 180 trading days over normal daily cycles, including `live/quotes.db` if it has material retained history; requires enabled scheduled runtime observation.
- [x] Cold manifest accounts for every pruned historical date range; verified by active manifest segment count and zero failures.
- [x] Every active cold segment schema supports row count, min/max date, file checksum, logical checksum, and schema version.
- [x] `data_lifecycle_restore` can restore a pruned segment in tested temp fixtures.
- [x] `daily_close_sync`, `daily_audit`, `deep_audit_backfill`, and `startup_recovery` do not perform bulk cold migration.
- [x] Disk free space remains above configured warning watermarks during and after real catch-up and professional-finance slimming; final free space was about 210GiB.
- [x] Professional finance raw/source growth has a tested control plan without changing `/api/v1/prof-finance/*`.
- [x] Lifecycle status exposes backlog/debt, rows, segment states, and failures.
- [x] Cold query capability is available only through explicit `/api/v1/cold/*` endpoints.
- [x] Cold URI values can later migrate to object-storage-backed schemes without changing segment identity.
- [x] Cold retention policy is recorded and purge eligibility is visible without automatic deletion.
- [x] Alert conditions are visible through lifecycle status.
- [x] There is no remaining required manual step for keeping hot data at 180 trading days after `TDX_LIFECYCLE_ENABLE=1` and `TDX_LIFECYCLE_ALLOW_PRUNE=1` are intentionally enabled; maintenance is scheduled independently at the lifecycle schedule.

### Notes
Implementation and tests are complete through Sprint 8, including the executable `data_lifecycle_maintenance` and `data_lifecycle_restore` governance runners. Real catch-up for `trade`, `live`, and `order_history` and real professional-finance serving-only slimming are complete as of 2026-04-26. The only remaining runtime acceptance item is observing scheduled steady-state retention over normal daily cycles.

2026-04-25 real-data catch-up update:
- Initial local state had severe disk pressure: free disk dropped to about 4GiB while `professional_finance` startup prefetch held `prof_finance.db` and `prof_finance.db-journal`.
- The service was rebuilt and restarted with `PROFINANCE_DISABLE_BACKGROUND_REFRESH=1`; `lsof` confirmed the running `stock-web` no longer opens `prof_finance.db` during normal startup, while existing query contracts remain available.
- `RestoreSegment` was optimized from per-row autocommit inserts to a single transaction with lifecycle write PRAGMAs. The restore-test bottleneck was removed while preserving the safety chain.
- Real catch-up completed for `trade`, `live`, and `order_history`: final full dry-run returned zero candidates and zero failures.
- Disk free recovered to about `173GiB`; `trade` dropped to about `34G`, `live` to about `29G`, `order_history` to about `1.6G`, and `cold_restore` is empty after successful cleanup.
- Lifecycle catch-up was paused before the 18:00 `daily_close_sync` window during an earlier recovery stage; the 18:00 run triggered on 2026-04-25 and skipped with `non_trading_day_window`, confirming scheduler priority remained intact.

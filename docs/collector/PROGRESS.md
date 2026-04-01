# Collector Progress

## Current Snapshot

- Current phase: `6 - Fundamentals`
- Phase status: `in_progress`
- Current task: `Add finance and F10 periodic sync with idempotent refresh, content persistence, and unresolved-field-safe contracts.`
- Next phase after current completion: `7 - Final Acceptance`

## Phase Status Board

| Phase | Name | Status |
|---|---|---|
| 0a | Control Plane | done |
| 0b | Provider Decoupling | done |
| 1 | Metadata | done |
| 2 | Historical Kline | done |
| 3 | Historical Trade | done |
| 4 | Order History | done |
| 5 | Live Capture | done |
| 6 | Fundamentals | in_progress |
| 7 | Final Acceptance | pending |

## Recently Completed

- Phase `0a` is complete
- Phase `0b` is complete
- Created the collector control documents under `docs/collector/`
- Defined the phase model, anti-drift rules, and documentation entrypoint
- Defined the final collector domains and storage targets
- Defined provider decoupling as a prerequisite before collector implementation phases
- Added the `collector` control package with:
  - `collector.db` schema scaffolding
  - machine-readable state file loading and saving
  - validation gate scaffolding
  - operation-log persistence helpers
  - control-document consistency tests
- Added collector-owned provider-facing domain contracts for:
  - instruments
  - trading days
  - quote snapshots
  - minute snapshots
  - kline bars
  - historical trades
  - order history
  - finance
  - F10 directory/content
- Added collector provider interfaces for all planned domains
- Added the first `tdx-api` provider adapter at `collector/provider_tdx.go`
- Added an anti-coupling test to block new non-adapter imports of `github.com/injoyai/tdx` and `protocol.*`
- Added collector-owned metadata staging/publish flow for `codes.db` and `workday.db`
- Added metadata cursor persistence in `collector.db`
- Added replay-safe metadata validation logs
- Added metadata tests that prove publish correctness and restart-safe replay
- Added collector-owned kline staging/publish flow with:
  - per-code SQLite publish databases
  - per-period staging tables
  - cursor persistence in `collector.db`
  - overlap-safe replay replacement
  - gap recording in `collector_gap`
- Added kline tests covering:
  - first publish correctness
  - overlap-safe replay after restart
  - missing-window gap recording
- Added collector-owned historical trade publish flow with:
  - DB-first raw trade storage
  - replay-safe per-day replacement
  - derived 1/5/15/30/60 minute bars published from stored raw trades
  - trade cursor persistence in `collector.db`
- Added historical trade tests covering:
  - first DB publish correctness
  - replay-safe day replacement
  - derived minute-bar reproducibility from stored raw rows
- Added collector-owned order-history publish flow with:
  - DB-first raw order-history storage
  - replay-safe per-day replacement
  - order-history cursor persistence in `collector.db`
  - raw `BuySellDelta` preservation
- Added order-history tests covering:
  - first DB publish correctness
  - replay-safe day replacement
  - unresolved field preservation for `BuySellDelta`
- Added collector-owned live capture flow with:
  - quote snapshot append
  - minute live day replacement
  - trade live day replacement
  - end-of-day reconciliation entrypoint
  - live cursor persistence in `collector.db`
- Added live capture tests covering:
  - quote/session publish correctness
  - replay-safe day replacement
  - reconciliation overwrite safety

## Current Phase Checklist

- [ ] Define finance/F10 storage contract
- [ ] Add idempotent finance refresh by update marker
- [ ] Add F10 directory/content persistence and replay-safe sync
- [ ] Add fundamentals tests for duplicate prevention and content consistency
- [ ] Update contracts and logs for phase 6 outputs

## Current Phase Rules

- Keep provider access behind collector interfaces introduced in phase `0b`.
- Finance refresh must remain idempotent by update marker or equivalent replay-safe key.
- F10 content sync must preserve raw text and prevent duplicate content rows.
- Unresolved semantics must remain explicitly unresolved in fundamentals contracts too.

## Current Blockers

- None recorded yet

## Next Single Task

Implement finance and F10 periodic sync with idempotent refresh and content consistency checks, without weakening the anti-fabrication rules around unresolved semantics.

## Completed Phase 0a Exit Evidence

- `collector.db` schema exists in code and is migration-safe
- phase state can be read and updated consistently
- validation gate scaffolding exists
- control-plane tests pass
- control documents and machine state remain consistent

## Completed Phase 0b Exit Evidence

- provider interfaces exist for planned collector domains
- collector-owned domain models exist for provider-facing flows
- the first `tdx-api` provider adapter compiles
- anti-coupling tests pass
- `go test ./collector -run 'TestCollectorCoreAvoidsDirectTDXCoupling|TestTDXProviderCompileContract|TestDocsConsistency' -v` passes

## Exit Criteria For Phase 1

- codes refresh has stateful publish protection
- workday refresh has stateful publish protection
- metadata replay after restart does not duplicate published rows
- metadata validation and persistence tests pass
- `go test ./collector -run 'TestMetadataRefreshPublishesCodesAndWorkdays|TestMetadataRefreshIsReplaySafeAcrossRestart|TestCollectorCoreAvoidsDirectTDXCoupling|TestDocsConsistency' -v` passes

## Completed Phase 2 Exit Evidence

- kline write path uses staging and publish flow
- kline cursor persistence survives restart
- overlap replay does not duplicate or corrupt bars
- kline validation and replay tests pass
- `go test ./collector -run 'TestKlineRefreshPublishesAndPersistsCursor|TestKlineRefreshIsOverlapSafeAcrossRestart|TestKlineRefreshRecordsGap|TestMetadataRefreshPublishesCodesAndWorkdays|TestMetadataRefreshIsReplaySafeAcrossRestart|TestCollectorCoreAvoidsDirectTDXCoupling|TestDocsConsistency' -v` passes

## Completed Phase 3 Exit Evidence

- historical trade raw data lands in DB as primary storage
- replay of a previously published trade day is dedupe-safe
- derived minute bars are reproducible from stored raw trade data
- historical trade validation and replay tests pass
- `go test ./collector -run 'TestTradeRefreshPublishesDBFirstAndPersistsCursor|TestTradeRefreshIsReplaySafeAndDerivedBarsAreReproducible|TestKlineRefreshPublishesAndPersistsCursor|TestKlineRefreshIsOverlapSafeAcrossRestart|TestKlineRefreshRecordsGap|TestMetadataRefreshPublishesCodesAndWorkdays|TestMetadataRefreshIsReplaySafeAcrossRestart|TestCollectorCoreAvoidsDirectTDXCoupling|TestDocsConsistency' -v` passes

## Completed Phase 4 Exit Evidence

- order-history raw data lands in DB as primary storage
- replay of a previously published order-history day is dedupe-safe
- unresolved field semantics remain explicitly unresolved in contracts and tests
- order-history validation and replay tests pass
- `go test ./collector -run 'TestOrderHistoryRefreshPublishesDBFirstAndPersistsCursor|TestOrderHistoryReplayPreservesRawDeltaValues|TestTradeRefreshPublishesDBFirstAndPersistsCursor|TestTradeRefreshIsReplaySafeAndDerivedBarsAreReproducible|TestKlineRefreshPublishesAndPersistsCursor|TestKlineRefreshIsOverlapSafeAcrossRestart|TestKlineRefreshRecordsGap|TestMetadataRefreshPublishesCodesAndWorkdays|TestMetadataRefreshIsReplaySafeAcrossRestart|TestCollectorCoreAvoidsDirectTDXCoupling|TestDocsConsistency' -v` passes

## Completed Phase 5 Exit Evidence

- live quote/minute/trade data lands in DB-first storage
- session restart is replay-safe
- end-of-day reconciliation does not corrupt historical data
- live capture validation and replay tests pass
- `go test ./collector -run 'TestLiveCaptureStoresQuotesAndSessionData|TestLiveCaptureReplayAndReconcileAreSafe|TestOrderHistoryRefreshPublishesDBFirstAndPersistsCursor|TestOrderHistoryReplayPreservesRawDeltaValues|TestTradeRefreshPublishesDBFirstAndPersistsCursor|TestTradeRefreshIsReplaySafeAndDerivedBarsAreReproducible|TestKlineRefreshPublishesAndPersistsCursor|TestKlineRefreshIsOverlapSafeAcrossRestart|TestKlineRefreshRecordsGap|TestMetadataRefreshPublishesCodesAndWorkdays|TestMetadataRefreshIsReplaySafeAcrossRestart|TestCollectorCoreAvoidsDirectTDXCoupling|TestDocsConsistency' -v` passes

## Exit Criteria For Phase 6

- finance data lands in DB-first storage with idempotent refresh
- F10 directory/content lands in DB-first storage with replay-safe sync
- duplicate finance/F10 refresh is safe
- fundamentals validation and replay tests pass

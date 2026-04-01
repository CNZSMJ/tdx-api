# Collector Progress

## Current Snapshot

- Current phase: `3 - Historical Trade`
- Phase status: `in_progress`
- Current task: `Move historical trade ingestion to DB-first storage with replay-safe publish and derived-bar reproducibility.`
- Next phase after current completion: `4 - Order History`

## Phase Status Board

| Phase | Name | Status |
|---|---|---|
| 0a | Control Plane | done |
| 0b | Provider Decoupling | done |
| 1 | Metadata | done |
| 2 | Historical Kline | done |
| 3 | Historical Trade | in_progress |
| 4 | Order History | pending |
| 5 | Live Capture | pending |
| 6 | Fundamentals | pending |
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

## Current Phase Checklist

- [ ] Define DB-first historical trade storage contract
- [ ] Add historical trade staging/publish flow
- [ ] Add replay-safe dedupe rules for historical trade rows
- [ ] Add derived-bar reproducibility tests
- [ ] Update contracts and logs for phase 3 outputs

## Current Phase Rules

- Keep provider access behind collector interfaces introduced in phase `0b`.
- Do not keep historical trade data CSV-only once phase `3` begins.
- Any derived minute bars must be reproducible from stored raw historical trade rows.
- Replay of an already-seen trade day must not duplicate published trade rows.

## Current Blockers

- None recorded yet

## Next Single Task

Implement DB-first historical trade ingestion with replay-safe publish and tests that prove derived bars can be reproduced from stored raw trade data.

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

## Exit Criteria For Phase 3

- historical trade raw data lands in DB as primary storage
- replay of a previously published trade day is dedupe-safe
- derived minute bars are reproducible from stored raw trade data
- historical trade validation and replay tests pass

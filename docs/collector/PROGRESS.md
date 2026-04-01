# Collector Progress

## Current Snapshot

- Current phase: `2 - Historical Kline`
- Phase status: `in_progress`
- Current task: `Build the kline staging/publish flow with cursor persistence, gap-safe replay, and overlap-safe validation.`
- Next phase after current completion: `3 - Historical Trade`

## Phase Status Board

| Phase | Name | Status |
|---|---|---|
| 0a | Control Plane | done |
| 0b | Provider Decoupling | done |
| 1 | Metadata | done |
| 2 | Historical Kline | in_progress |
| 3 | Historical Trade | pending |
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

## Current Phase Checklist

- [ ] Define kline staging schema and publish contract
- [ ] Add kline cursor persistence and overlap-safe replay rules
- [ ] Add gap detection for kline periods
- [ ] Add restart-safe kline publish tests
- [ ] Update contracts and logs for phase 2 outputs

## Current Phase Rules

- Keep provider access behind collector interfaces introduced in phase `0b`.
- Do not write kline rows directly into published tables without staging validation.
- Any overlap replay must prove that rerunning an already-published window does not corrupt published bars.

## Current Blockers

- None recorded yet

## Next Single Task

Implement the first safe kline staging/publish pipeline with cursor persistence and overlap-safe replay tests, without changing existing query routes yet.

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

## Exit Criteria For Phase 2

- kline write path uses staging and publish flow
- kline cursor persistence survives restart
- overlap replay does not duplicate or corrupt bars
- kline validation and replay tests pass

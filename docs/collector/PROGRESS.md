# Collector Progress

## Current Snapshot

- Current phase: `1 - Metadata`
- Phase status: `in_progress`
- Current task: `Make codes/workday refresh fully stateful, restart-safe, and safe to rerun without publishing duplicates.`
- Next phase after current completion: `2 - Historical Kline`

## Phase Status Board

| Phase | Name | Status |
|---|---|---|
| 0a | Control Plane | done |
| 0b | Provider Decoupling | done |
| 1 | Metadata | in_progress |
| 2 | Historical Kline | pending |
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

## Current Phase Checklist

- [ ] Design metadata publish flow for `codes` and `workday`
- [ ] Add metadata state persistence to `collector.db`
- [ ] Add safe rerun protection and duplicate-prevention checks
- [ ] Add startup recovery path for metadata refresh
- [ ] Update contracts and logs for phase 1 outputs

## Current Phase Rules

- Keep provider access behind collector interfaces introduced in phase `0b`.
- Do not publish metadata updates directly over existing state without validation.
- Any metadata refresh must be safe to replay after restart without creating duplicate published rows.

## Current Blockers

- None recorded yet

## Next Single Task

Implement safe metadata refresh scaffolding for `codes` and `workday`, backed by `collector.db` state and replay-safe publish rules, without breaking current query behavior.

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

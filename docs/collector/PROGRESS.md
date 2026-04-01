# Collector Progress

## Current Snapshot

- Current phase: `0b - Provider Decoupling`
- Phase status: `in_progress`
- Current task: `Define collector provider interfaces, collector-owned domain models, and the first tdx provider adapter boundary.`
- Next phase after current completion: `1 - Metadata`

## Phase Status Board

| Phase | Name | Status |
|---|---|---|
| 0a | Control Plane | done |
| 0b | Provider Decoupling | in_progress |
| 1 | Metadata | pending |
| 2 | Historical Kline | pending |
| 3 | Historical Trade | pending |
| 4 | Order History | pending |
| 5 | Live Capture | pending |
| 6 | Fundamentals | pending |
| 7 | Final Acceptance | pending |

## Completed In Current Phase

- Phase `0a` is complete
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

## Current Phase Checklist

- [ ] Define provider interfaces for planned collector domains
- [ ] Define collector-owned domain models for provider-facing flows
- [ ] Add the first `tdx-api` provider adapter skeleton
- [ ] Add anti-coupling tests or static checks
- [ ] Update contracts and logs for phase 0b outputs

## Current Phase Rules

- Do not start metadata automation until phase 0b exit criteria are satisfied.
- Do not let collector core depend directly on `tdx.Client`, `tdx.Manage`, or `protocol.*`.
- Any new provider-facing model must belong to collector-owned contracts, not upstream protocol structs.

## Current Blockers

- None recorded yet

## Next Single Task

Define collector provider interfaces and collector-owned domain models for `codes`, `workday`, `kline`, `trade_history`, `order_history`, `finance`, and `f10`, without changing business ingestion behavior yet.

## Completed Phase 0a Exit Evidence

- `collector.db` schema exists in code and is migration-safe
- phase state can be read and updated consistently
- validation gate scaffolding exists
- control-plane tests pass
- control documents and machine state remain consistent

## Exit Criteria For Phase 0b

- provider interfaces exist for planned collector domains
- collector-owned domain models exist for provider-facing flows
- the first `tdx-api` provider adapter compiles
- anti-coupling tests or checks pass

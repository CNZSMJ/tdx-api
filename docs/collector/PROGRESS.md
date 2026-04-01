# Collector Progress

## Current Snapshot

- Current phase: `0a - Control Plane`
- Phase status: `in_progress`
- Current task: `Implement the collector control plane foundation without changing existing business ingestion behavior.`
- Next phase after current completion: `0b - Provider Decoupling`

## Phase Status Board

| Phase | Name | Status |
|---|---|---|
| 0a | Control Plane | in_progress |
| 0b | Provider Decoupling | pending |
| 1 | Metadata | pending |
| 2 | Historical Kline | pending |
| 3 | Historical Trade | pending |
| 4 | Order History | pending |
| 5 | Live Capture | pending |
| 6 | Fundamentals | pending |
| 7 | Final Acceptance | pending |

## Completed In Current Phase

- Created the collector control documents under `docs/collector/`
- Defined the phase model, anti-drift rules, and documentation entrypoint
- Defined the final collector domains and storage targets
- Defined provider decoupling as a prerequisite before collector implementation phases

## Current Phase Checklist

- [x] Create the collector document set
- [ ] Design the `collector.db` schema and migration strategy
- [ ] Implement machine-readable phase state handling
- [ ] Implement validation gate scaffolding
- [ ] Implement operation-log writing rules or helpers
- [ ] Add blocking tests for control-plane consistency

## Current Phase Rules

- Do not change ingestion business logic beyond what is required to support the control plane.
- Do not start metadata automation until phase 0 exit criteria are satisfied.
- Any schema proposal must preserve existing published data.

## Current Blockers

- None recorded yet

## Next Single Task

Design and implement the initial `collector.db` schema plus the first phase-gate plumbing, while keeping all existing collector behavior unchanged.

## Exit Criteria For Phase 0a

- `collector.db` schema exists in code and is migration-safe
- phase state can be read and updated consistently
- validation gate scaffolding exists
- control-plane tests pass
- control documents and machine state remain consistent

## Planned Entry Criteria For Phase 0b

- phase 0a exit criteria are satisfied
- provider boundary is documented in code-facing terms
- anti-coupling rules are stable enough to enforce in tests

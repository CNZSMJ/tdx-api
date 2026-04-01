# Collector Master Plan

## Mission

Build a fully automated market-data collector for this repository that can continue safely across agents and sessions until all planned data domains are ingested, validated, published, and queryable.

## Final Target

The final collector must satisfy all of the following:

- collect all currently obtainable data domains that matter for research and replay
- run without manual triggering
- recover automatically after long downtime
- never silently pollute existing data
- preserve a complete audit trail of work, tests, and phase progress
- remain executable by any coding agent that follows the control documents

## Scope

In scope:

- metadata collection
- historical market data collection
- live trading-session capture
- fundamentals and F10 collection
- control-plane state, validation, recovery, and observability

Out of scope:

- new L2 upstream protocols
- strategy logic, factor engine, and backtest engine
- non-TDX external data vendors

## Final Data Domains

| Domain | Coverage | Final storage | Status target |
|---|---|---|---|
| codes | stock, etf, index universe | SQLite | automated |
| workday | exchange trading calendar | SQLite | automated |
| kline | stock, etf, index; minute to year | SQLite | automated |
| trade_history | stock, etf historical trades | SQLite + optional CSV export | automated |
| trade_derived_bars | 1/5/15/30/60 minute bars from trade history | SQLite + optional CSV export | automated |
| order_history | historical order distribution | SQLite | automated |
| quote_snapshot | live quote snapshots for stock and etf | SQLite | automated |
| minute_live | live intraday minute series for stock and etf | SQLite | automated |
| trade_live | live intraday minute trades for stock and etf | SQLite | automated |
| finance | finance snapshots by report update | SQLite | automated |
| f10_category | F10 directory metadata | SQLite | automated |
| f10_content | F10正文 | SQLite + optional text archive | automated |

## Safety Model

Every collector domain must follow this flow:

1. Plan collection ranges from universe, cursors, and trading calendar.
2. Write new data into staging tables or staging files.
3. Validate staging output against data contracts.
4. Publish atomically into official storage only after validation passes.
5. Record cursor, run status, validation evidence, and operation log.

Publishing directly into official tables without staging is forbidden.

## Decoupling Requirement

The collector must be designed as a loosely coupled system relative to the current `tdx-api` implementation.

Required architecture:

1. provider layer
   - talks to the current upstream implementation
   - current default implementation is a `tdx-api provider`
2. collector core
   - owns planning, scheduling, gap recovery, staging, validation, publish, and run-state management
   - must not depend directly on `tdx.Client`, `tdx.Manage`, or `protocol.*`
3. storage layer
   - owns database schema, publish flow, and query-side persistence
   - must consume collector domain models, not upstream protocol structs

This decoupling is a prerequisite for collector implementation, not a later cleanup.

## Provider Boundary Requirement

Before any major collector phase begins, the project must define:

- collector provider interfaces
- collector-owned domain models
- a first `tdx-api` provider adapter

Without these three pieces, downstream collector phases risk hard-coupling business logic to the current upstream implementation.

## Anti-Drift Controls

These controls exist to reduce agent drift and fabricated completion:

- single entry file: [START_HERE.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/START_HERE.md)
- single phase source of truth: [STATE.yaml](/Users/huangjiahao/workspace/tdx-api/docs/collector/STATE.yaml)
- single implementation roadmap: [PROGRESS.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/PROGRESS.md)
- mandatory validation rules: [TEST_MATRIX.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/TEST_MATRIX.md)
- mandatory data semantics: [DATA_CONTRACT.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/DATA_CONTRACT.md)
- mandatory work evidence: [WORK_LOG.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/WORK_LOG.md)

## Phase Plan

| Phase | Name | Goal | Required output | Exit gate |
|---|---|---|---|---|
| 0a | Control Plane | Create collector state, gate, and documentation control plane | `collector.db` schema design, phase state rules, validation runner design, handoff docs | phase state works, gate logic implemented, docs aligned |
| 0b | Provider Decoupling | Establish provider interfaces, collector domain models, and the first `tdx-api` adapter | provider contracts, adapter skeleton, collector-owned models, anti-coupling tests | collector core no longer depends on raw `tdx-api` internals |
| 1 | Metadata | Fully automate `codes` and `workday` refresh and recovery | automated schedulers, cursors, validation, recovery | metadata updates safe and repeatable |
| 2 | Historical Kline | Make kline ingestion safe, resumable, and gap-aware | staging publish flow, cursors, gap scanner, scheduler | no direct overwrite, gap recovery proven |
| 3 | Historical Trade | Replace CSV-only flow with DB-first trade ingestion and derived bars | DB schema, staging publish flow, derived bar validation | trade replay coverage and derived bar checks pass |
| 4 | Order History | Add automated order-history ingestion | schema, gap recovery, field validation | order-history coverage and validation pass |
| 5 | Live Capture | Add automated live session capture for quote, minute, and trade | trading-session scheduler, day partitioning, close reconciliation | intraday capture and close reconciliation pass |
| 6 | Fundamentals | Add finance and F10 automated collection | report update rules, directory/content sync, content hashing | fundamentals and F10 validation pass |
| 7 | Final Acceptance | Prove end-to-end automation and recovery | full acceptance report | all domains green, downtime recovery proven |

## Phase Exit Definition

A phase is complete only when all of the following are true:

- all checklist items in [PROGRESS.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/PROGRESS.md) are complete
- all blocking tests in [TEST_MATRIX.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/TEST_MATRIX.md) pass
- affected contracts in [DATA_CONTRACT.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/DATA_CONTRACT.md) are updated
- [WORK_LOG.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/WORK_LOG.md) contains exact evidence
- [STATE.yaml](/Users/huangjiahao/workspace/tdx-api/docs/collector/STATE.yaml) has `allowed_to_advance: true`

## Recovery Requirement

Long downtime recovery is mandatory. The final system must:

- detect missing collection windows after restart
- compute gaps from trading calendar and collection cursors
- backfill gaps before resuming steady-state incremental collection
- keep old published data unchanged unless replacement data passes validation

## Final Acceptance Standard

The collector is accepted only when:

- all planned domains are implemented
- all collection jobs run automatically
- all publish flows are staging-safe
- all downtime recovery tests pass
- all phase logs, progress files, and state files are complete and consistent

# Collector Test Matrix

## How To Use This File

This file defines the blocking validation for each phase.

Rules:

- A phase is not complete unless all blocking tests for that phase pass.
- If a test cannot be run, the phase cannot be marked complete.
- Every test run must be recorded in [WORK_LOG.md](./WORK_LOG.md) with the exact command and outcome.
- Partial testing is not enough to advance a phase.

## Global Blocking Tests

Run these for every code-changing phase unless a phase entry explicitly says otherwise:

1. `go test ./...`
2. `cd web && go test ./...`
3. Any impacted smoke checks from `scripts/run_api_checks.py`
4. Any newly added collector-specific tests

If startup, routing, or Docker behavior is affected, also run:

1. `./scripts/build_local_image.sh`
2. `docker compose up -d`
3. `docker compose ps`
4. health endpoint verification

## Phase 0 - Control Plane

Blocking:

- control documents exist and are internally consistent
- machine state file is parseable and matches progress state
- any added control-plane code passes root and web Go tests

Required evidence:

- file list under `docs/collector/`
- exact commands used to validate parseability or tests

## Phase 0b - Provider Decoupling

Blocking:

- collector provider interfaces exist for all planned domains
- collector-owned domain models exist for all required collection flows touched in this phase
- the first `tdx-api` provider adapter compiles
- collector core does not introduce new direct dependencies on `tdx.Client`, `tdx.Manage`, or `protocol.*`
- anti-coupling tests or static checks pass

Required evidence:

- provider interface list
- domain model list
- adapter compile/test result
- anti-coupling validation result

## Phase 1 - Metadata

Blocking:

- `codes` refresh runs safely without breaking existing cache
- `workday` refresh remains correct
- metadata cursor/state persistence works across restart
- startup recovery does not duplicate published metadata

Required evidence:

- metadata update test
- restart persistence test
- duplicate-prevention test

## Phase 2 - Historical Kline

Blocking:

- kline write path uses staging and publish flow
- gap scanner identifies missing ranges correctly
- rerun of the same window is idempotent
- overlap windows do not corrupt published rows

Required evidence:

- incremental run test
- overlap replay test
- downtime gap recovery test
- per-period coverage validation

## Phase 3 - Historical Trade

Blocking:

- trade raw data lands in DB as primary storage
- derived bars are reproducible from stored trade data
- CSV is export-only, not the sole source of truth
- reruns do not duplicate trades

Required evidence:

- year-range ingestion test
- derived-bar consistency test
- duplicate ingestion test
- recovery after interruption test

## Phase 4 - Order History

Blocking:

- order-history ingestion is automated
- storage is DB-first
- overlap and gap recovery work
- field validation does not silently coerce unresolved semantics into facts

Required evidence:

- date-range ingestion test
- overlap replay test
- unresolved-field handling test

## Phase 5 - Live Capture

Blocking:

- trading-session scheduler starts and stops safely
- live snapshots do not overwrite historical data
- close reconciliation works after session end
- restart during market session recovers correctly

Required evidence:

- intraday capture test
- session restart test
- end-of-day reconciliation test

## Phase 6 - Fundamentals

Blocking:

- finance updates are idempotent by report update marker
- F10 category sync and content sync are consistent
- repeated sync does not duplicate content

Required evidence:

- finance refresh test
- F10 directory-content consistency test
- duplicate-content prevention test

## Phase 7 - Final Acceptance

Blocking:

- all previous phases are complete
- full collector startup performs automated catch-up
- long-downtime scenario is verified
- final acceptance report is generated and recorded

Required evidence:

- end-to-end integration run
- long-downtime recovery run
- final domain coverage report

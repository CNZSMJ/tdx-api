# Governance Sprint Tracking

## Source of Truth Read

- Read and aligned on `/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api/docs/collector/SYSTEM_DATA_GOVERNANCE_PLAN.md` on 2026-04-19.
- This tracking file is the execution log for the plan and must stay aligned with the plan's non-negotiables and sprint ordering.

## Current Architecture Model

- Governance state DB path:
  - `${TDX_DATA_DIR}/governance/system_governance.db`
  - Fallback: `./data/database/governance/system_governance.db`
- Governance lock path:
  - `${TDX_DATA_DIR}/governance/system_governance.lock`
  - Fallback: `./data/database/governance/system_governance.lock`
- Governance reports path:
  - `${TDX_DATA_DIR}/governance/reports/...`
  - Fallback: `./data/database/governance/reports/...`
- Model split:
  - `system_governance.db` stores system governance control-plane records only.
  - Existing business SQLite files keep domain-local collector or market data state only.
  - OS filesystem lock is authoritative for single-writer governance exclusion.
  - DB lock metadata is observability only, never the authority.

## Non-Negotiables

- Runtime governance state must be physically isolated from existing business SQLite files.
- The governance lock must be a cross-process OS file lock, not an in-memory mutex.
- `daily_open_refresh`, `daily_close_sync`, and `daily_audit` must pass trading-calendar gate before entering collection logic.
- `codes` and `workday` require pre-open fast-retry inside the same `daily_open_refresh` run.
- `startup_recovery` restores backlog or missed windows only; it must not default to full-market historical rescans.
- `daily_close_sync` only targets `T/T-1`; it must not regress into a full sync.
- Hidden embedded cron registration in `codes/workday/block/profinance` must be removed by Sprint 2.

## Current Sprint

- Sprint 8: Deep Audit / Backfill

## Sprint 1 Goal

- Establish the isolated governance DB and OS lock.
- Add unified governance models and persistence primitives.
- Start surfacing unified governance status through `/api/collector/status`.
- Keep existing runtime behavior intact while mapping current jobs into the new control plane.

## Sprint 1 Initial Test Plan

- Red tests for governance path resolution:
  - uses `TDX_DATA_DIR` when set
  - falls back to `./data/database/governance`
- Red tests for governance DB isolation:
  - creates a dedicated DB under `t.TempDir()`
  - never shares collector DB paths
- Red tests for OS file lock behavior:
  - second concurrent acquisition fails while first holder is active
  - lock release enables reacquisition
- Red tests for control-plane persistence:
  - can write and read `GovernanceRun`, `GovernanceTask`, and `DomainHealthSnapshot`
  - persists lock holder metadata separately from lock authority
- Red tests for status aggregation:
  - unified job names appear in status payload without changing existing execution semantics

## Passed Validation

- Read source-of-truth governance plan.
- Confirmed active git toplevel is `/Users/huangjiahao/workspace/industry-investment-suite/repos/tdx-api`.
- Governance control-plane base paths, isolated DB schema, lock contention, and legacy job catalog tests passed:
  - `go test ./collector -run 'TestResolveGovernancePaths|TestOpenGovernanceStoreUsesIsolatedSchema|TestGovernanceStorePersistsControlPlaneRecords|TestGovernanceLockRejectsSecondProcess|TestGovernanceJobCatalogAndLegacyMapping'`
- Unified runtime governance projection and API exposure tests passed:
  - `go test ./collector -run 'TestRuntimeUnifiedGovernanceStatusProjectsLegacyRunsAndDomains'`
  - `TDX_WEB_SKIP_INIT=1 go test -run 'TestHandleCollectorStatusIncludesGovernanceView'`
- Race verification passed:
  - `go test -race ./collector -run 'TestResolveGovernancePaths|TestOpenGovernanceStoreUsesIsolatedSchema|TestGovernanceStorePersistsControlPlaneRecords|TestGovernanceLockRejectsSecondProcess|TestGovernanceJobCatalogAndLegacyMapping|TestRuntimeUnifiedGovernanceStatusProjectsLegacyRunsAndDomains'`
  - `TDX_WEB_SKIP_INIT=1 go test -race -run 'TestHandleCollectorStatusIncludesGovernanceView'`
- Sprint 2 validations passed:
  - `go test -run 'TestCodesConstructorDoesNotStartAutoRefreshCron|TestWorkdayConstructorDoesNotStartAutoRefreshCron'`
  - `go test ./collector -run 'TestBlockServiceConstructorDoesNotStartAutoRefreshCron'`
  - `go test ./profinance -run 'TestNewServiceDoesNotStartDailyAutoPrefetchCron'`
  - `go test ./governance -run 'TestDailyOpenRefresh'`
  - `go test -race -run 'TestCodesConstructorDoesNotStartAutoRefreshCron|TestWorkdayConstructorDoesNotStartAutoRefreshCron'`
  - `go test -race ./collector -run 'TestBlockServiceConstructorDoesNotStartAutoRefreshCron'`
  - `go test -race ./profinance -run 'TestNewServiceDoesNotStartDailyAutoPrefetchCron'`
  - `go test -race ./governance -run 'TestDailyOpenRefresh'`
- Sprint 3 validations passed:
  - `go test ./collector -run 'TestRuntimeResolveRecentTradingDatesUsesLatestTwoTradingDays|TestRuntimeExecuteDailyCloseSyncUsesProvidedDatesAndReturnsFailures'`
  - `go test ./governance -run 'TestDailyCloseSync|TestDailyOpenRefresh'`
  - `TDX_WEB_SKIP_INIT=1 go test -run 'TestHandleCollectorStatusIncludesGovernanceView'`
  - `go test -race ./collector -run 'TestRuntimeResolveRecentTradingDatesUsesLatestTwoTradingDays|TestRuntimeExecuteDailyCloseSyncUsesProvidedDatesAndReturnsFailures'`
  - `go test -race ./governance -run 'TestDailyCloseSync|TestDailyOpenRefresh'`
  - `TDX_WEB_SKIP_INIT=1 go test -race -run 'TestHandleCollectorStatusIncludesGovernanceView'`
- Sprint 6 validations passed:
  - `go test ./governance -run 'TestRepairWorker'`
  - `go test -race ./governance -run 'TestRepairWorker'`
  - `go test ./collector -run 'TestRuntimeRepairCloseSyncFailureTargetsSingleDomainWindow|TestRuntimeExecuteDailyCloseSyncUsesProvidedDatesAndReturnsFailures'`
  - `TDX_WEB_SKIP_INIT=1 go test -run 'TestRunGovernanceRepairWorkerExecutesStartupMissedWindowTask|TestHandleCollectorStatusIncludesGovernanceView'` (run from `web/`)
  - `TDX_WEB_SKIP_INIT=1 go test -race -run 'TestRunGovernanceRepairWorkerExecutesStartupMissedWindowTask|TestHandleCollectorStatusIncludesGovernanceView'` (run from `web/`)
- Sprint 7 validations passed:
  - `go test ./collector -run 'TestFundamentalsRefreshFinanceIfUpdatedSkipsUnchangedSnapshots|TestFundamentalsSyncF10IfChangedSkipsUnchangedContentFetch|TestRuntimeExecuteDailyCloseSyncIncrementallyRefreshesFundamentalsDomains|TestRuntimeExecuteDailyCloseSyncSkipsUnchangedFundamentalsArtifacts|TestRuntimeRepairCloseSyncFailureTargetsSingleDomainWindow|TestRuntimeExecuteDailyCloseSyncUsesProvidedDatesAndReturnsFailures'`
  - `go test -race ./collector -run 'TestFundamentalsRefreshFinanceIfUpdatedSkipsUnchangedSnapshots|TestFundamentalsSyncF10IfChangedSkipsUnchangedContentFetch|TestRuntimeExecuteDailyCloseSyncIncrementallyRefreshesFundamentalsDomains|TestRuntimeExecuteDailyCloseSyncSkipsUnchangedFundamentalsArtifacts|TestRuntimeRepairCloseSyncFailureTargetsSingleDomainWindow|TestRuntimeExecuteDailyCloseSyncUsesProvidedDatesAndReturnsFailures'`
  - `go test ./profinance -run 'TestSyncIfNeededSkipsWhenArtifactsAndServingWatermarkAreCurrent|TestSyncIfNeededRunsWhenVisibleReportAdvances'`
  - `go test -race ./profinance -run 'TestSyncIfNeededSkipsWhenArtifactsAndServingWatermarkAreCurrent|TestSyncIfNeededRunsWhenVisibleReportAdvances'`
- Sprint 8 validations passed:
  - `go test ./collector -run 'TestRuntimeResolveDeepAuditDatesFromExplicitWindow|TestRuntimeResolveDeepAuditDatesFromOpenGapBacklog'`
  - `go test -race ./collector -run 'TestRuntimeResolveDeepAuditDatesFromExplicitWindow|TestRuntimeResolveDeepAuditDatesFromOpenGapBacklog'`
  - `go test ./governance -run 'TestDeepAuditBackfillStoresHistoricalEvidenceAndTasks|TestRepairWorker'`
  - `go test -race ./governance -run 'TestDeepAuditBackfillStoresHistoricalEvidenceAndTasks|TestRepairWorker'`
  - `TDX_WEB_SKIP_INIT=1 go test -run 'TestHandleCollectorDeepAuditTriggersIndependentBackfillRunner|TestRunGovernanceRepairWorkerExecutesStartupMissedWindowTask|TestHandleCollectorStatusIncludesGovernanceView'` (run from `web/`)
  - `TDX_WEB_SKIP_INIT=1 go test -race -run 'TestHandleCollectorDeepAuditTriggersIndependentBackfillRunner|TestRunGovernanceRepairWorkerExecutesStartupMissedWindowTask|TestHandleCollectorStatusIncludesGovernanceView'` (run from `web/`)
- Final package verification passed:
  - `go test ./collector ./governance ./profinance`
  - `go test -race ./collector ./governance ./profinance`
  - `TDX_WEB_SKIP_INIT=1 go test ./...` (run from `web/`)
  - `TDX_WEB_SKIP_INIT=1 go test -race ./...` (run from `web/`)

## Completed

- Added governance path resolution against `TDX_DATA_DIR` with repo-local fallback.
- Added isolated governance schema:
  - `governance_schema_version`
  - `governance_run`
  - `governance_task`
  - `governance_domain_health_snapshot`
  - `governance_lock_metadata`
  - `governance_evidence`
- Added authoritative OS file lock helper for `system_governance.lock`.
- Added unified governance job catalog and legacy-name mapping for current hidden and explicit jobs.
- Added runtime-side unified governance projection for legacy `startup/18:00/19:00` runs.
- Extended `/api/collector/status` with a `governance` payload containing paths, jobs, tasks, lock metadata, and projected domain summaries.
- Removed constructor-time `09:00` cron ownership from:
  - `tdx.Codes`
  - `tdx.Workday`
  - `collector.BlockService`
  - `profinance.Service`
- Added governance-level `daily_open_refresh` runner with:
  - trading-calendar gate
  - same-run pre-open fast-retry for `codes/workday`
  - backlog task creation for failed domains
- Wired `09:00` scheduling through a single web-level governance entry.
- Added runtime resolver for latest two trading windows and runtime executor for close-window sync.
- Added governance-level `daily_close_sync` runner with:
  - trading-calendar gate
  - `T/T-1` target resolution
  - repair-task creation from execution failures
- Wired `18:00` scheduling through governance runner instead of legacy catch-up path.
- Added generalized repair-worker execution model:
  - task claim moves `open -> in_progress`
  - governance lock is released before executor callback runs
  - final write-back reacquires governance lock
- Added targeted close-sync repair entry for:
  - `kline`
  - `trade_history`
  - `live_capture`
  - `order_history`
- Wired a web-level governance repair worker dispatcher that can execute:
  - `startup_recovery` missed-window tasks by replaying `daily_open_refresh / daily_close_sync / daily_audit`
  - `daily_open_refresh` domain refresh tasks
  - `daily_close_sync` repair tasks
  - `daily_audit` repairable recent-window tasks
- Updated startup recovery flow to immediately run one repair-worker pass after backlog inspection so missed-window tasks are not left as dead records.
- Added fundamentals incremental rules:
  - `RefreshFinanceIfUpdated` only republishes when provider `updated_date` advances
  - `SyncF10IfChanged` only republishes when category signature changes
  - `f10` cursor semantics are now stable signature based, not raw category-count based
- Extended `daily_close_sync` to run recent-window sync plus incremental `finance/f10` refresh for stock instruments.
- Added `professional_finance.SyncIfNeeded` with explicit reasons:
  - `source_watermark_missing`
  - `source_report_advanced`
  - `serving_watermark_stale`
  - `up_to_date`
- Switched web-level `professional_finance` governance execution from unconditional `PrefetchAll` to `SyncIfNeeded`.
- Added `deep_audit_backfill` governance runner with:
  - independent job name and run records
  - historical-window target selection
  - evidence persistence for per-date reports and summary artifact
  - batch task materialization using the same audit classification semantics as `daily_audit`
- Added runtime historical-date resolution for:
  - explicit `start/end` trading windows
  - backlog-derived windows from open historical `kline` gaps
- Added web-level deep-audit integration:
  - `runDeepAuditBackfill(...)`
  - `POST /api/collector/deep-audit`
  - optional low-peak cron via `COLLECTOR_DEEP_AUDIT_SCHEDULE`
- `/api/collector/status` schedule payload now exposes governance-native names:
  - `daily_open_refresh`
  - `daily_close_sync`
  - `daily_audit`
  - `deep_audit_backfill`
- Hardened async and race verification support:
  - `profinance.Service.Close()` now waits for startup prefetch goroutines
  - `collector/provider_throttle_test.go` test double is now race-safe so package-wide `-race` validation is reliable

## Review Follow-Up 2026-04-19

- Re-opened governance follow-up from code review to address five correctness issues in the uncommitted Sprint 1-8 implementation.
- Red tests added for:
  - preserving `CloseSyncFailure` payloads in `daily_close_sync` repair tasks
  - startup missed-window detection against actual scheduled trading windows
  - replaying stored target windows for startup missed `daily_close_sync`
  - replaying interrupted governance runs through startup recovery
  - preserving `professional_finance` domain health on status reads
  - ordering lower numeric task priorities before higher numeric ones
- Red verification confirmed current implementation gaps:
  - `go test ./governance -run 'TestDailyCloseSyncCreatesRepairTasksForExecutionFailures|TestStartupRecoveryQueuesMissedWindowsAndInterruptedRuns'`
  - `go test ./collector -run 'TestGovernanceStoreListsLowerNumericPrioritiesFirst|TestRuntimeUnifiedGovernanceStatusProjectsLegacyRunsAndDomains'`
  - `TDX_WEB_SKIP_INIT=1 go test ./... -run 'TestCollectStartupRecoverySnapshot|TestRunGovernanceRepairWorkerExecutesStartupMissedCloseSyncTaskWithStoredWindow|TestRunGovernanceRepairWorkerReplaysInterruptedCloseSyncRun'` in `web/`
- Important implementation decisions for the follow-up:
  - startup recovery missed-job model now carries the actual target window, not only the job name
  - close-sync and audit governance runners support replay with explicit target dates so startup compensation can reproduce the original missed window
  - `professional_finance` status projection only seeds `unknown` when no prior governance snapshot exists
  - governance task scheduling treats smaller numeric priority as higher urgency
- Review follow-up implementation is now green:
  - `go test ./governance -run 'TestDailyCloseSyncCreatesRepairTasksForExecutionFailures|TestStartupRecoveryQueuesMissedWindowsAndInterruptedRuns'`
  - `go test ./collector -run 'TestGovernanceStoreListsLowerNumericPrioritiesFirst|TestRuntimeUnifiedGovernanceStatusProjectsLegacyRunsAndDomains'`
  - `TDX_WEB_SKIP_INIT=1 go test ./... -run 'TestCollectStartupRecoverySnapshot|TestRunGovernanceRepairWorkerExecutesStartupMissedCloseSyncTaskWithStoredWindow|TestRunGovernanceRepairWorkerReplaysInterruptedCloseSyncRun'` in `web/`
  - `go test ./collector ./governance`
  - `go test -race ./collector ./governance`
  - `TDX_WEB_SKIP_INIT=1 go test ./...` in `web/`
  - `TDX_WEB_SKIP_INIT=1 go test -race ./...` in `web/`

## Sprint 1 Exit Gate

- Green.
- Exit-gate notes:
  - isolated governance DB is active
  - OS lock contention is verified
  - unified governance naming exists
  - `/api/collector/status` now exposes unified governance state

## Sprint 2 Goal

- Remove hidden embedded `09:00` schedulers from `codes/workday/block/profinance`.
- Introduce a single `daily_open_refresh` system job entry point.
- Add trading-calendar gate for `09:00`.
- Add pre-open fast-retry for open-critical domains `codes/workday`.

## Sprint 2 Initial Test Plan

- Red tests for hidden-cron removal:
  - constructors do not auto-register or start `09:00` cron when governance owns scheduling
- Red tests for `daily_open_refresh` gate:
  - non-trading day records one `skipped` run with `non_trading_day_window`
- Red tests for pre-open fast-retry:
  - `codes/workday` retry within `09:00-09:30`
  - success inside retry budget yields a recovered run
  - exhausted retry budget yields a backlog task
- Red tests for single-entry scheduling:
  - one `09:00` governance trigger fans out to `codes/workday/block/profinance`
  - duplicate in-process `codes` refresh no longer occurs

## Sprint 2 Exit Gate

- Green.
- Exit-gate notes:
  - hidden constructor cron ownership is removed
  - `09:00` now runs through one system entry
  - `codes/workday` fast-retry semantics are covered by tests
  - non-trading-day skip behavior is explicit

## Sprint 3 Goal

- Cut over `18:00` from legacy full sync semantics to `daily_close_sync`.
- Restrict the sync window to `T/T-1`.
- Convert per-date execution failures into repair tasks.

## Sprint 3 Exit Gate

- Green.
- Exit-gate notes:
  - `18:00` path now resolves recent trading windows instead of backlog-style catch-up
  - runtime execution receives only provided `T/T-1` dates
  - failures materialize as governance repair tasks

## Sprint 4 Goal

- Upgrade `19:00` from reconcile report only into formal audit taskification.
- Emit `repair_pending / degraded / blocked / unsupported` outcomes.
- Bind audit artifacts to governance tasks and domain health snapshots.

## Sprint 4 Initial Test Plan

- Red tests for non-trading-day audit skip with explicit `skipped` run reason.
- Red tests for audit classification:
  - repairable issue -> repair task or repaired result
  - non-repairable issue -> degraded / blocked / unsupported task
- Red tests for audit output binding:
  - report path stored as evidence
  - governance tasks reference the audited target window

## Known Blockers

- Current `collector.ReconcileDate` still conflates sync, repair, and reporting; Sprint 4 needs an audit-oriented wrapper without regressing the new Sprint 3 close-sync path.
- The current status payload still carries legacy `daily_full_sync` naming in some non-governance branches; later cleanup should finish the terminology cutover without reintroducing compatibility shims.

## Important Decisions

- `codes` remote refresh ownership stays on `manager.Codes.Update()`.
- `tdx.DefaultCodes` is kept warm via DB-only mirror reload `Update(true)` to avoid duplicate upstream fetches while preserving current in-memory consumers.
- Sprint 3 close-sync execution intentionally skips fundamentals incrementalization for now; `finance/f10/profinance` change-based rules are reserved for Sprint 7 rather than keeping the old full sweep alive.
- Repair worker authority model is now:
  - OS lock protects task claim and final write-back
  - executor callbacks run outside the lock so they can safely invoke nested governance runners
  - `degraded` tasks still cannot be reopened by later `open` upserts
- `startup_recovery` compensation is implemented as governance tasks + immediate repair-worker replay rather than reintroducing a legacy startup full catch-up path.
- `finance` incremental boundary is provider `UpdatedDate`; the system still queries the provider per stock at `18:00`, but it no longer rewrites local state when `updated_date` is unchanged.
- `f10` incremental boundary is a stable SHA-256 signature over normalized category metadata; unchanged category trees no longer trigger content refetch or DB rewrite.
- `professional_finance` incremental boundary is the combination of remote latest visible report date and local serving watermark freshness, rather than blind daily sync.
- `deep_audit_backfill` stays entirely outside the `09/18/19` window semantics; manual trigger and optional cron both route into a dedicated runner and dedicated run records.

## Next Step

- All planned governance sprints are implemented and validated in-repo.
- Future follow-up, if requested:
  - add richer backlog derivation beyond `kline` gaps for deep-audit seed ranges
  - expose a dedicated governance UI panel over the unified `/api/collector/status` payload

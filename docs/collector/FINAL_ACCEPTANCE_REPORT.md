# Collector Final Acceptance Report

## Final Status

- Status: `accepted`
- Verified at: `2026-04-02 22:38:18 CST`
- Phase: `7 - Final Acceptance`

## Scope Completed

- Added a collector startup catch-up runtime in `collector/runtime.go` to automate metadata refresh, kline refresh, historical trade catch-up, order-history catch-up, live-day reconciliation, and fundamentals sync behind the provider boundary.
- Wired the collector runtime into `web/server.go` so the service now performs:
  - one startup catch-up after boot
  - one full synchronization run every day at `18:00` local server time
  - one daily reconciliation and repair run every day at `19:00` local server time
  - one report write to `data/database/collector_reports/reconcile-YYYYMMDD.json` after each reconciliation run
- Hardened collector operations so the service now also provides:
  - distinct persistent schedule records for startup catch-up vs daily full sync
  - startup missed-run compensation for the latest required `18:00` / `19:00` maintenance windows
  - a status/observability API at `/api/collector/status`
  - provider request throttling to reduce rate-limit / ban risk during catch-up and reconciliation
  - full trading-day bootstrap for empty cursors instead of only taking the latest day
  - richer reconciliation evidence with per-domain before/after row counts, table-level counts, cursor summaries, and target-date coverage fields in the stored JSON report
- Added an external API at `/api/collector/reconcile`:
  - `POST` with `date=YYYYMMDD` to run reconciliation for the requested date
  - `GET` with `date=YYYYMMDD` to read the stored reconciliation report
- Added an external API at `/api/collector/status`:
  - `GET` to inspect recent schedule runs, open gap count, active job state, and the configured daily schedules
- Added phase-7 acceptance coverage in `collector/runtime_test.go` and `collector/acceptance_test.go`.
- Verified restart catch-up behavior across:
  - `codes`
  - `workday`
  - `kline`
  - `trade_history`
  - `order_history`
  - `quote_snapshot`
  - `minute_live`
  - `trade_live`
  - `finance`
  - `f10_category`
  - `f10_content`

## Exact Validation Evidence

1. End-to-end acceptance and startup catch-up:

   ```text
   $ go test ./collector -run 'TestCollectorRuntimeStartupCatchUpAcrossDomains|TestCollectorFinalAcceptanceEndToEndCatchUp' -v
   === RUN   TestCollectorFinalAcceptanceEndToEndCatchUp
   --- PASS: TestCollectorFinalAcceptanceEndToEndCatchUp (0.06s)
   === RUN   TestCollectorRuntimeStartupCatchUpAcrossDomains
   --- PASS: TestCollectorRuntimeStartupCatchUpAcrossDomains (0.06s)
   PASS
   ok  	github.com/injoyai/tdx/collector	0.884s
   ```

2. Full collector suite:

   ```text
   $ go test ./collector -v
   PASS
   ok  	github.com/injoyai/tdx/collector	3.734s
   ```

3. Repository root suite:

   ```text
   $ go test ./...
   ok  	github.com/injoyai/tdx/collector	4.368s
   ok  	github.com/injoyai/tdx/extend	(cached)
   ok  	github.com/injoyai/tdx/protocol	(cached)
   ```

4. Web suite:

   ```text
   $ cd web && go test ./...
   ?   	web	[no test files]
   ```

## Acceptance Notes

- Final acceptance does not promote unresolved semantics to fact.
- The daily `18:00` full synchronization keeps all repairable completed-day datasets current before the reconciliation window.
- The daily `19:00` reconciliation republished all repairable completed-day domains for the target date, then writes a report to disk.
- Empty-cursor startup catch-up now backfills all available trading days rather than only the latest one, which closes the original fresh-bootstrap completeness gap.
- Provider calls are serialized with a minimum interval inside collector runtime construction so full backfills do not hammer upstream too aggressively.
- If the service starts after the latest scheduled `18:00` or `19:00` maintenance window was missed, collector now compensates that missed window automatically after startup catch-up finishes.
- `quote_snapshot` cannot be reconstructed historically for non-current dates with the current provider APIs, so historical reconciliation marks it explicitly as unsupported instead of fabricating completeness.
- `finance` and `F10` are refreshed during reconciliation, but they remain provider-current snapshots rather than backdated historical reconstructions for arbitrary dates.
- The meaning of `BuySellDelta` remains unresolved and is still stored and validated as raw data only.
- The definitive uniqueness key for historical/live trade rows remains unresolved beyond the replay-safe identities already enforced by the collector tests.
- No blocking issues remained after the final validation run.

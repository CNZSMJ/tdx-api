# Collector Start Here

This is the only handoff entry for any coding agent working on the collector system.

## Purpose

Build the market-data collector in controlled phases until the final target is complete:

- full data coverage
- full automation
- safe staging and publish flow
- zero silent data pollution
- repeatable progress across different agents

## Mandatory Reading Order

Read these files in order before doing any work:

1. [MASTER_PLAN.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/MASTER_PLAN.md)
2. [PROGRESS.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/PROGRESS.md)
3. [STATE.yaml](/Users/huangjiahao/workspace/tdx-api/docs/collector/STATE.yaml)
4. [TEST_MATRIX.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/TEST_MATRIX.md)
5. [DATA_CONTRACT.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/DATA_CONTRACT.md)
6. [WORK_LOG.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/WORK_LOG.md)

Do not skip files. Do not rely on memory from previous sessions.

## Non-Negotiable Rules

1. Work only on the current phase in [STATE.yaml](/Users/huangjiahao/workspace/tdx-api/docs/collector/STATE.yaml).
2. Do not start the next phase until all exit criteria for the current phase are satisfied.
3. Do not fabricate results, data semantics, test outcomes, or completion state.
4. If a field meaning is not proven by code, payload samples, or existing docs, record it as unresolved and stop treating it as fact.
5. Do not overwrite production tables or files directly. All collector work must follow `plan -> staging -> validate -> publish -> record`.
6. Do not change storage contracts outside the current phase scope.
7. Do not commit unless all blocking tests for the current phase pass.
8. Do not build collector core directly on top of `tdx.Client`, `tdx.Manage`, or `protocol.*`. Provider access must go through the collector provider adapter boundary.
8. Do not mark a phase complete unless:
   - all phase checklist items are complete
   - all blocking tests pass
   - [WORK_LOG.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/WORK_LOG.md) is updated
   - [PROGRESS.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/PROGRESS.md) is updated
   - [STATE.yaml](/Users/huangjiahao/workspace/tdx-api/docs/collector/STATE.yaml) is updated
9. If upstream instability prevents reliable verification, stop, log the blocker, and do not advance the phase.
10. If the worktree already contains unrelated user changes, do not revert them.

## Execution Loop

For every session, follow this exact loop:

1. Read all mandatory files in order.
2. Confirm the current phase and the single current task from [STATE.yaml](/Users/huangjiahao/workspace/tdx-api/docs/collector/STATE.yaml).
3. Review the current phase section in [PROGRESS.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/PROGRESS.md).
4. Review the required validation in [TEST_MATRIX.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/TEST_MATRIX.md).
5. Review the affected data domain rules in [DATA_CONTRACT.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/DATA_CONTRACT.md).
6. Implement only the current task.
7. Run all blocking tests for the current phase.
8. Update [WORK_LOG.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/WORK_LOG.md) with exact commands and outcomes.
9. Update [PROGRESS.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/PROGRESS.md) with completed work and next step.
10. Update [STATE.yaml](/Users/huangjiahao/workspace/tdx-api/docs/collector/STATE.yaml) with the new machine-readable state.
11. Commit only after the above steps are complete.
12. Advance to the next phase only if `allowed_to_advance: true` is justified by the completed checklist and passing tests.

## Stop Conditions

Stop and record a blocker immediately if any of the following is true:

- required file is missing or contradictory
- current phase is unclear
- test evidence is incomplete
- data semantics are unresolved but implementation depends on them
- collector work would introduce new direct coupling to `tdx-api` internals instead of going through provider adapters
- upstream service is unstable and validation cannot be trusted
- a required migration risks overwriting existing data without staging/publish safety

## Required Session Output

Every work session must leave behind:

- code or docs for the current task
- exact validation evidence in [WORK_LOG.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/WORK_LOG.md)
- updated current status in [PROGRESS.md](/Users/huangjiahao/workspace/tdx-api/docs/collector/PROGRESS.md)
- updated machine state in [STATE.yaml](/Users/huangjiahao/workspace/tdx-api/docs/collector/STATE.yaml)

If any one of these is missing, the session is incomplete.

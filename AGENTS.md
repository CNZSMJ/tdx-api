# AGENTS.md

## Scope

This repository is the `tdx-api` child repo inside the Industry Investment Suite workspace.

Workspace-level rules from the parent workspace `AGENTS.md` still apply here. This file adds repo-local constraints for `tdx-api`.

## Repository Role

- `tdx-api` owns TDX market data access, collection, local storage behavior, and API or web surfaces built on top of that data.
- Keep core data acquisition, normalization, collector behavior, storage access, and API semantics inside this repo.
- Do not move `tdx-api` domain logic into sibling repos, workspace docs, or external wrappers unless the user explicitly asks for a cross-repo extraction.

## Implementation Rules

- Treat this repo as a first-release codebase unless the user explicitly asks for compatibility work.
- Do not add speculative backward-compatibility logic, deprecated API aliases, fallback response shapes, or config migration shims unless explicitly requested.
- Prefer one clear contract for API inputs, outputs, and environment variables rather than layered legacy behavior.
- When shared workspace market data is needed, prefer `TDX_DATA_DIR`; otherwise preserve repo-local defaults such as `./data/database`.
- Keep data-path decisions explicit and environment-driven rather than hardcoding workspace-specific absolute paths.

## Boundaries

- Runtime or generated data under `data/` or external directories should not be casually rewritten during refactors.
- Changes to collectors, storage layout, or API contracts should be made coherently in this repo rather than patched with compatibility branches.
- If a task also requires edits in sibling repos, keep the commits separate by repository.

## Git Hygiene

- Run Git commands from `repos/tdx-api`, not from the workspace root.
- Before staging or committing, confirm the active repo with `git rev-parse --show-toplevel`.
- Stage only the files relevant to the current `tdx-api` task.

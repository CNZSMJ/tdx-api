# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Layout

This is a single-context repo.

## Before exploring, read these

- `CONTEXT.md` at the repo root, if it exists.
- `docs/adr/`, reading ADRs that touch the area about to be changed, if they exist.

If these files do not exist, proceed silently. Do not flag their absence and do not suggest creating them upfront.

## Use the glossary's vocabulary

When output names a domain concept in an issue title, refactor proposal, hypothesis, or test name, use the term as defined in `CONTEXT.md`.

If the concept is not in the glossary yet, either avoid inventing new vocabulary or note the gap for a future documentation pass.

## Flag ADR conflicts

If an output contradicts an existing ADR, surface the contradiction explicitly instead of silently overriding it.

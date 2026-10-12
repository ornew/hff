# HFF

Keep this file to mandatory working rules and documentation links. Put language
contracts and design rationale in their own documents.

## Documentation map

- `README.md`: overview and documentation index; `spec/`: normative contracts.
- `docs/tutorials/`, `docs/guides/`, `docs/cookbook/`: learning, feature guides
  and runnable recipes.
- `docs/reference/`, `docs/backends/`: API and backend reference.
- `docs/design/`: decisions; `docs/development/`: contributor and site workflows.
- `site/`: Nanosite configuration and wrapper; `hack/`: development scripts.

Use the `documentation`, `performance`, `backlog` and `nanosite` skills when
relevant.
Write README, specs, docs and comments in English. Reader-facing docs must
stand alone without development conversations or backlog IDs. Add a tutorial
or guide with each implemented feature. Update affected contracts and examples.

## Development and checks

- Use Go 1.27.1. Separate standard-library and external imports. Run `gofmt`
  and `go vet` before committing Go changes, along
  with relevant project checks documented under `docs/development/`.
- Treat `../pego` as a read-only reference during HFF work.
- Put repository development scripts in `hack/`; keep `site/build.sh` as the
  dedicated documentation-site entry point. Do not edit generated output by hand.
- Ask the owner about unspecified language semantics, with alternatives,
  tradeoffs and a recommendation. After asking asynchronously, keep the turn
  active and wait for the answer before dependent work.
- Reuse the persistent Luna documentation/backlog sidekick for documentation
  and Issue #1 management. The primary owns implementation, tests, site code,
  Git operations and main integration; the sidekick owns documentation context
  and backlog issue/comments after handoff. Reuse independent review context
  where appropriate.

- For high-impact changes, request critical review from a reviewer whose context
  is independent of the worker; reuse existing reviewers. Share requirements,
  diffs and validation evidence, and resolve material findings before completion.

## Backlog, commits and branches

- Issue #1 is the living backlog: https://github.com/ornew/hff/issues/1.
  Preserve its body, completion record, plan and progress as distinct surfaces;
  keep progress current-only. Categories are three-digit B, O, F, Q and D.
  Record completion only after verified integration, with backlog ID, linked
  commit and one-line summary. IDs are temporary and never belong in reader
  docs or commit messages.
- Write commits, issues and PRs in the owner's voice: omit references to agents,
  AI assistance, the owner/user, requests, delegation or work history. Follow
  `docs/commit-messages.md`; include a body explaining the problem and rationale.
  Do not add agent signatures. Format commit hashes as plain text or Markdown
  links, never inline code.
- Commit and push directly to main when within the authorized task scope; make
  logical units separately. Branch prefixes include `feat/`, `fix/`, `perf/`
  and `docs/`; do not use `codex/` or `claude/`.

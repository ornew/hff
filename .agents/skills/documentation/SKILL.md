---
name: documentation
description: Maintain HFF user guides, contracts, design records and development documentation.
---

# Documentation

- User docs: `docs/tutorials/`, `docs/guides/`, and `docs/cookbook/` cover
  learning, feature use and runnable recipes. Preserve the repository's plural
  directory names.
- `spec/` contains normative language and API contracts; `docs/reference/` is
  lookup material; `docs/backends/` explains backend behavior and limits.
- `docs/design/` records decisions and alternatives. `docs/development/` covers
  contributor, validation and site workflows. Keep usage docs separate from
  development history and specs.
- Follow existing indexes and `docs/design/000-template.md`; remove prompts
  from finished pages, verify examples and links, and keep reader docs
  independent of conversations and Issue #1 IDs.
- Add a tutorial or guide with each implemented feature. Update affected
  contracts and examples; do not hand-edit generated files.
- The documentation sidekick owns documentation context and Issue #1 after
  handoff. The primary owns implementation, tests, site code and Git. The
  primary reviews documentation changes and verifies saved results.

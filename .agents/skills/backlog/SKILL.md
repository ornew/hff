---
name: backlog
description: Maintain HFF's Issue #1 backlog, plan, progress and completion record.
---

# Backlog

- Use HFF Issue #1: https://github.com/ornew/hff/issues/1. The linked records
  are the [completion record](https://github.com/ornew/hff/issues/1#issuecomment-6107246824),
  [plan](https://github.com/ornew/hff/issues/1#issuecomment-6107246987), and
  [progress](https://github.com/ornew/hff/issues/1#issuecomment-6107247192).
  Retrieve the current issue and comments before proposing or making updates.
- Categories use three-digit IDs: B bugs, O optimizations, F features, Q
  quality/tools and D documentation. Preserve allocated IDs and check current
  items and archives before assigning new ones.
- Keep the issue body to rules, incomplete checklist items and links. Keep the
  plan/priorities and progress synchronized; progress records current work,
  pending decisions, blockers and deferred essentials only.
- Record completion only after acceptance criteria and verified integration.
  Add backlog ID, linked commit hash and one-line result to the completion
  record, then remove the completed open item and update related plan/progress.
  Preserve unfinished parent scope and avoid duplicate entries.
- IDs are temporary tracking labels; never put them in reader docs or commit
  messages. Fetch before edits and read back every changed surface.
- The documentation sidekick owns Issue #1 and its management comments after
  handoff. The primary owns implementation, tests, site code and Git integration;
  the primary reviews changes and verifies saved state.

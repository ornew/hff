---
name: performance
description: Evaluate HFF optimizations with reproducible measurements and correctness evidence.
---

# Performance Evidence

- Use `docs/optimizations/000-template.md`. Describe the operation, workload,
  backend and execution profile; behavior in one backend does not establish
  behavior in another.
- Establish correctness first. Pin revisions, toolchain, inputs, configuration
  and hardware. Serialize runs and use paired or alternating baseline/candidate
  samples. Measure affected operations separately and check normal paths.
- Keep focused raw logs outside Git unless deliberately versioned. Record
  commands and provenance; report timing, allocations and live memory where
  relevant, including noise and unmeasured paths.
- Update the optimization catalog with applicability and evidence. Separate
  plans, local results and production observations; avoid incomparable claims.

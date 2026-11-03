# Changelog

## v0.1.0 — 2026-11-04 — first module release

- Run sequential model/tool calls with bounded steps, complete-round history
  windows, partial execution records, usage totals, and lifecycle hooks.
- Support explicit tool confirmation with continue/stop denial policies.
- Replace reflection-based snapshots with typed `Clone()` methods, reusing SDK
  cloning. Keep hook and confirmation snapshots isolated while removing redundant
  request and tool-record copies.
- Depend on `ai-sdk v0.7.0`. The Go module release tag is `ai-tool-loop/v0.1.0`.

### Workspace migration

- `ai-tool-loop/utils.Clone` is removed; use protocol types' `.Clone()` methods.
- Extension snapshots follow SDK rules: use acyclic JSON-shaped data for
  isolation. Custom application objects, callbacks, and errors remain shared.
- Callers must not mutate inputs concurrently with execution or cloning.

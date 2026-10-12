# Coding Guidelines and Standards

This document establishes the architectural conventions and code style rules for developers and AI agents working on this repository.

## 1. Relaxed Go Naming Conventions for Enhanced Readability

Traditional Go community guidelines enforce strict naming conventions (e.g., lowercase-only filenames, strict camelCase for all identifiers, very short or single-letter variable names). In this repository, **we prioritize code readability, expressiveness, and maintainability over strict adherence to traditional Go conventions**.

### 1.1 Filename Conventions
- Hyphenated / kebab-case naming is explicitly permitted and encouraged for file names (e.g., `model-client.go`, `stream-handler.go`, `tool-registry.go`, `open-ai-provider.go`) when it helps visual separation and readability across large codebases.
- Standard Go file conventions (such as `_test.go` suffixes for test files) remain respected for Go toolchain compatibility.

### 1.2 Variable and Identifier Naming
- Snake_case (`variable_name`, `parameter_name`, `field_name`) is permitted for local variables, private struct fields, and helper functions when it improves readability or aligns with domain models (e.g., API schemas, LLM protocol terms).
- Descriptive, intention-revealing variable names are strongly preferred over cryptic, short, or single-letter names (e.g., prefer `client_factory` or `clientFactory` over `cf`, prefer `stream_response` over `sr`).

### 1.3 Exported Identifiers
- Exported types, functions, methods, and constants must still begin with a capital letter to comply with Go's language-level visibility rules. CamelCase should be used for exported symbols (e.g., `ClientFactory`, `GenerateText`).

## 2. How to Write a Go Module

The repository is a Go workspace (`go.work`). Each module is one `go.mod` and one responsibility. `ai-sdk` is the reference module: it owns model calls, tool calling, and the provider-neutral contract other modules build on.

### 2.1 Module shape

```
<module>/
  go.mod
  protocol/
    types.go
    methods.go
  <a few files for this module's own behavior>
  utils/                  # optional; type-independent helpers only
  <impl>/<name>/          # optional; one backend per directory
```

In `ai-sdk`, `client.go` and `hooks.go` own one model call's lifecycle, `utils/` holds generic helpers such as `CloneMap`, and `providers/<name>/` is one provider backend.

### 2.2 `protocol/` — required, exactly two source files

Every module keeps its public contract in `protocol/`. That directory contains `types.go` and `methods.go`, and no other non-test source file.

`types.go` is the contract, readable at a glance like a TypeScript `.d.ts` file. It holds type declarations, constants, and interfaces only. In `ai-sdk` this is the provider-neutral model-call contract.

`methods.go` is the behavior for those types: methods, constructors (e.g. `TextMessage`), and unexported helpers (e.g. `cloneToolCall`). Go requires a method to live in the same package as its receiver, so protocol behavior stays in this file.

A new type, constant, or interface goes in `types.go`. New behavior goes in `methods.go`. A `*_test.go` file may sit beside them when the package needs tests.

`protocol` may import `utils`. It does not import the module root, hooks, or implementations. Callers import `protocol` directly and do not re-export its types.

### 2.3 Stay small and follow SOLID

Write the smallest module that does one job. A file should be readable in one pass. Add an abstraction only when a second caller or a second implementation needs it.

- **Single responsibility.** One module changes for one reason. `protocol` defines the contract. The module root orchestrates. One implementation package serves one backend.
- **Open/closed.** A new backend is a new directory under `<impl>/<name>/`. Existing protocol types and existing backends stay unchanged.
- **Liskov substitution.** Every implementation is usable wherever the `protocol` interface is accepted, with the same observable contract.
- **Interface segregation.** Interfaces in `types.go` stay small and specific to one caller need. Split an interface when callers use different slices of it.
- **Dependency inversion.** The module root and callers depend on `protocol`. Implementations depend on `protocol`, and `protocol` does not depend on them. `utils` imports neither `protocol` nor the module root, so the dependency direction cannot cycle.

### 2.4 Few files per directory

A directory stays readable when each file has one job and the directory has few of them.

- Add a function to the file that already owns that responsibility. In `protocol`, that file is `methods.go`. A type-independent helper goes in the matching file under `utils/`.
- Outside `protocol`, group code by responsibility (lifecycle, hooks, one backend). The `types.go` / `methods.go` split is reserved for the contract package.
- Keep an implementation package flat. It depends on `protocol`, not on the module root.
- `*_test.go` files sit next to the code they test. They are the only extra files a package adds for tests.

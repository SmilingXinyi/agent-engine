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

## 2. Workspace and Module Architecture

- The repository uses Go workspaces (`go.work`) to manage multiple modular packages.
- Modules should remain clean, decoupled, and focused on their distinct responsibilities.
- The `ai-sdk` module acts as the core SDK package for AI model interaction, tool calling, and agent workflows.

## 3. Type Definitions and Function Placement

Type declarations should be readable at a glance, similar to a TypeScript `.d.ts` file. Keep definitions and behavior separated as follows.

- `ai-sdk/protocol/types.go` contains the provider-neutral model-call contract: type declarations, constants, and interfaces only. Do not add functions or methods here.
- `ai-sdk/protocol/methods.go` contains methods, constructors (e.g. `TextMessage`), and package-private helpers (e.g. `cloneToolCall`) for those types.
- `ai-sdk/client.go` and `ai-sdk/hooks.go` own one model call's lifecycle. They import `protocol` and do not re-export its types.
- `ai-sdk/utils/` contains general-purpose helpers that do not depend on `protocol` types (e.g. generic `CloneMap`). `protocol` may import `utils`, but `utils` must never import `protocol` or `ai`, or an import cycle appears.
- Provider implementations live under `ai-sdk/providers/<name>/` and depend on `protocol`, not on the client.
- Go requires methods to be declared in the same package as their receiver type, so protocol methods stay in package `protocol`.
- Do not create a new file in the same directory for a single function. Add it to that package's `methods.go`, or move it to `utils/` if it is type-independent.

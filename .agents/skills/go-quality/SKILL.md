---
name: go-quality
description: Improve, review, or write Go code with idiomatic design, maintainable APIs, generics, methods, interfaces, error handling, concurrency, tests, fuzzing, benchmarks, linting, and CI quality gates. Use when working on .go files, go.mod/go.work modules, Go package architecture, Go code review, refactors, performance work, or requests involving golangci-lint, go test, go vet, staticcheck, govulncheck, race detection, coverage, or Go best practices.
---

# Go Quality

## Workflow

Start by reading the existing package shape, `go.mod`, test files, and local tooling config before proposing style changes. Preserve established project conventions unless they conflict with correctness, public API stability, or clear Go idioms.

When changing Go code:

1. Run `gofmt`/`go fmt`; use `goimports` if imports changed and it is available.
2. Prefer small packages with clear ownership. Keep unexported implementation under `internal/` when callers should not depend on it.
3. Put behavior on the type that owns state or invariants. Do not write Java-style manager/helper free functions that take a receiver-like first argument when a method would make the API clearer.
4. Use free functions for stateless algorithms, constructors, package-level operations, and behavior that should not become part of a type's method set.
5. Avoid wrapper and fallback functions by default. Treat them as a design smell unless they preserve a real compatibility boundary, enforce policy, isolate external dependencies, or are explicitly requested by the user.
6. Use generics when they remove duplicated type-specific code without hiding simple logic. Start from concrete code, then introduce type parameters once the repeated shape is obvious.
7. Represent enums with a declared type using the smallest unsigned integer that can hold the values, constants defined with `iota`, and generated string/text helpers from `github.com/dmarkham/enumer` registered as a `tool` in `go.mod` and invoked with `go:generate`.
8. Design types around clear invariants: prefer useful or harmless zero values, document nil behavior, validate at boundaries, and keep domain invariants near the owning type.
9. Keep interfaces small and consumer-owned. Accept interfaces at boundaries; return concrete types unless there is a strong abstraction reason.
10. Propagate `context.Context` explicitly as the first parameter for cancellable work. Never store it in structs.
11. Handle errors at the point where action is possible; otherwise wrap with useful context using `%w` and return.
12. Keep library code quiet unless logging is part of the API contract. Do not log and return the same error unless double reporting is intended.
13. Add or update tests with the change. Prefer table tests for cases, subtests for named scenarios, race tests for concurrency, fuzz tests for parsers/decoders, golden files for stable fixtures, and benchmarks for performance claims.
14. Validate with the repository's normal commands. If none exist, use the baseline commands below.

Baseline validation:

```bash
go test ./...
go test -race ./...
go vet ./...
golangci-lint run ./...
govulncheck ./...
```

Skip unavailable optional tools only after checking whether they are installed or configured. Report skipped validation explicitly.

## Review Checklist

Use this checklist for reviews and refactors:

- API: package names, exported identifiers, constructors, and doc comments read naturally from the caller side.
- Methods: receiver names are short and meaningful; pointer receivers are used for mutation, large structs, locks, or consistent method sets.
- Wrappers/fallbacks: no thin pass-through functions, fallback variants, or compatibility shims unless the request explicitly asks for them or the design has a concrete boundary that justifies them.
- Generics: constraints are minimal, usually `any` or `comparable`; generic interfaces avoid over-constraining implementations.
- Enums: enum-like domains use a named smallest-fit unsigned integer type, `iota` constants, a `go.mod` `tool` entry for `github.com/dmarkham/enumer`, and `go:generate` declarations for generated helpers.
- Type design: zero values are useful or explicitly documented; constructors/options are justified; nil handling and validation boundaries are clear.
- Interfaces: define behavior, not data containers; avoid pointer-to-interface; avoid exporting broad interfaces for mocking alone.
- Data boundaries: transport DTOs do not leak persistence/API compatibility concerns into domain types when behavior or invariants differ.
- Errors: no ignored errors; no panic for ordinary failures; sentinel/typed errors are used only when callers need to branch.
- Concurrency: goroutines have cancellation/exit paths; channel ownership is clear; shared state is protected; tests run with `-race` when relevant.
- Resources: `io.Closer`, temp files, goroutines, timers, and channels have explicit ownership and cleanup.
- Testing: tests exercise public behavior, include edge cases, and use clear `got/want` failure messages; complex comparisons use diffs; golden files live under `testdata/`.
- Security: secrets use `crypto/rand`, paths are sanitized, HTTP servers set timeouts, and unsafe operations are locally justified.
- Tooling: `gofmt`, `go test`, `go vet`, lint, vulnerability checks, generation checks, and CI match the repo's support policy.

## References

Load these only when needed:

- `references/patterns.md`: idiomatic Go design guidance, including methods vs functions, type design, constructors/options, interfaces, generics, errors, context, concurrency, tests, generated code, build tags, security, and layout.
- `references/tooling.md`: concrete commands and configuration guidance for formatting, tests, race detection, fuzzing, coverage, benchmarks, linting, vet, staticcheck, govulncheck, and CI.
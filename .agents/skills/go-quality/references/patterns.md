# Go Quality Patterns

Sources consulted: Effective Go, Go Code Review Comments, Go module layout guidance, Go generics blog posts, Go fuzz/security docs, Go diagnostics docs, command docs, golangci-lint docs, Staticcheck docs, and govulncheck docs.

## Methods vs Functions

Prefer a method when behavior belongs to a named type's state, invariants, identity, synchronization, or protocol. A function like `func CloseClient(c *Client) error` is usually worse than `func (c *Client) Close() error` because it hides ownership and bloats the package namespace.

Use a free function when the operation is stateless, creates a value, coordinates multiple independent types, implements a generic algorithm, or should not become part of the type's public method set.

## Wrapper and Fallback Functions

Do not add thin wrapper functions, duplicate fallback variants, or pass-through compatibility helpers unless the user explicitly asks for them or the design has a concrete reason. A wrapper that only renames another function, swaps argument order, adds vague "safe" behavior, or preserves an unused old call path usually increases API surface and maintenance cost.

When tempted to add a wrapper or fallback:

- Prefer fixing the caller to use the real API directly.
- Prefer moving behavior onto the type that owns the invariant.
- Prefer a small interface at the dependency boundary over a package-level wrapper.
- Prefer one clear implementation with options/config over parallel "normal" and "fallback" paths.
- Keep a wrapper only for backwards compatibility, cross-package boundary protection, observability/policy injection, dependency isolation, or a temporary migration with a removal plan.

Receiver guidance:

- Use short receiver names derived from the type, such as `c *Client`, not `this`, `self`, or long names repeated on every line.
- Use pointer receivers when the method mutates state, must avoid copying a large value, contains a lock, or needs one consistent method set.
- Use value receivers for small immutable values, but keep receiver style consistent for a type unless there is a reason to mix.
- Do not define methods just to simulate classes. Keep packages as the main unit of design.

## API and Type Design

Prefer types whose zero value is useful or harmless. If the zero value is invalid, document that and force construction through a constructor only when it protects a real invariant.

Use constructors for required normalization, validation, dependency wiring, or ownership setup. Avoid constructors that only return `&T{}` with renamed fields. Prefer a config struct when many options are logically data; prefer functional options only when optional settings are numerous, stable enough for API callers, and benefit from validation or defaults.

Be explicit about nil behavior. Decide whether nil receivers, nil slices, nil maps, and nil function fields are accepted, and make tests match that contract. Prefer nil slices as natural empty values unless JSON or API compatibility requires `[]`.

Validate at package, transport, storage, and user-input boundaries. Keep domain invariants near the owning type so callers cannot create partially valid values and hope later code catches them.

Separate transport DTOs from domain types when JSON tags, backward compatibility, persistence shape, nullable fields, or validation semantics would otherwise leak into the domain model. Keep direct struct reuse when the type is genuinely just data and no invariant is being hidden.

Use `time.Time` and `time.Duration` rather than stringly typed time. Inject a small clock dependency only for time-dependent logic that needs deterministic tests; otherwise call `time.Now` at the ownership boundary and pass concrete times inward.

## Generics

Use generics for reusable data structures, algorithms over slices/maps/channels, type-safe helpers, and APIs that previously required duplicate implementations or `interface{}` plus type assertions.

Avoid generics when:

- Only one concrete type exists.
- An ordinary interface captures behavior more simply.
- Reflection or `any` still dominates the implementation.
- Type parameters make call sites harder to read than duplicated concrete code.

Rules of thumb:

- Write concrete code first; add type parameters after duplication or type-safety pressure is real.
- Keep constraints minimal. Use `any` unless operations require `comparable`, ordering, or specific methods.
- Prefer generic interfaces like `Comparer[T]` when the method signature needs the concrete peer type.
- Do not put strong constraints on generic interfaces unless every implementation truly needs them; leave stricter constraints to concrete implementations.

## Enums

Represent enum-like domains with a declared type whose underlying type is the smallest unsigned integer that can hold all values: `uint8`, `uint16`, `uint32`, or `uint64`. Prefer `uint8` for small closed sets unless growth expectations make the next size materially clearer.

Declare enum values with `iota`:

```go
//go:generate go tool enumer -type=Status -trimprefix=Status -text
type Status uint8

const (
	StatusUnknown Status = iota
	StatusPending
	StatusRunning
	StatusFailed
)
```

Register `github.com/dmarkham/enumer` as a Go tool in `go.mod` and run it through `go generate`:

```go
tool github.com/dmarkham/enumer
```

Keep generated enum helpers checked in when the repository already checks in generated code or when CI/release workflows depend on generated files being present. Re-run `go generate ./...` after changing enum values and include tests for parsing, text/JSON behavior, or invalid values when those behaviors are part of the API.

## Interfaces

Accept interfaces, return concrete types. Define interfaces close to the consumer that needs substitutability. Keep interfaces small, often one or two methods, and name one-method interfaces with an `-er` style when natural: `Reader`, `Writer`, `Formatter`.

Avoid:

- Large "service" interfaces invented before multiple implementations exist.
- Returning interfaces from constructors just to hide implementation.
- Pointers to interfaces.
- Interfaces used only because tests want mocks; first check whether the public API can be tested directly.

If an exported interface must not be implemented outside the package, add an unexported method or keep the interface internal. Use this deliberately and document the compatibility reason.

Use compile-time assertions sparingly when documenting important conformance:

```go
var _ io.Reader = (*Buffer)(nil)
```

## Errors

Handle an error, return it, or intentionally ignore it with a comment when the result is truly irrelevant. Add context at package boundaries:

```go
if err != nil {
	return fmt.Errorf("load config %q: %w", path, err)
}
```

Use `errors.Is`/`errors.As` with wrapped errors when callers need branching. Avoid string matching. Avoid logging and returning the same error unless double reporting is intended. Use `panic` for programmer errors and impossible states, not normal input, I/O, or dependency failures.

Library packages should usually not log. Accept a logger, metrics recorder, or tracer at service boundaries when observability is part of the behavior. Avoid passing observability dependencies through deep utility functions unless the signal would otherwise be lost.

## Context

Pass `context.Context` explicitly as the first parameter after a receiver:

```go
func (c *Client) Fetch(ctx context.Context, id string) (*Item, error)
```

Do not store contexts in structs, invent custom context interfaces, or replace propagation with `context.TODO()` in real call chains. Derive deadlines/cancellation at ownership boundaries and call the cancel function.

## Package Layout

Let package names describe what callers use, not implementation layers. Keep simple packages flat. Move supporting packages under `internal/` when external modules should not depend on them. Use `cmd/<name>` for multiple binaries when a repo has more than one command. Avoid premature `pkg/`, `util/`, `common/`, and `helpers` packages.

## Concurrency

Prefer clear ownership over clever channel networks. Every goroutine should have an exit path. Use contexts, done channels, or closing ownership consistently. Protect shared memory with mutexes or channels; do not mix casually. Run `go test -race ./...` after concurrency changes.

Make cleanup ownership explicit for `io.Closer`, files, temporary directories, tickers, timers, goroutines, and channels. The function that creates a resource should usually close it, return a closer, or document that ownership moved to the caller.

## Testing

Use table tests with named cases and `t.Run` when behavior has meaningful cases. Prefer `got` and `want` names in assertions. Use `cmp.Diff` or a project-standard comparison helper for complex values instead of opaque `reflect.DeepEqual` failures.

Use `testdata/` for stable parser, formatter, protocol, or fixture data. Golden tests should have an explicit update flag or documented update command and should avoid silently rewriting expected output during normal test runs.

## Generated Code and Build Tags

Generated files should start with a standard `// Code generated ... DO NOT EDIT.` comment. Keep generation commands reproducible with `go generate`, checked-in tool declarations, or documented CI steps. Verify generated files in CI when generated output is committed.

Keep build-tagged files narrow and named for their platform or constraint, such as `foo_linux.go` or `net_unix.go`. Add tests for supported build constraints when platform-specific behavior matters.

## Security

Use `crypto/rand` for secrets, tokens, nonces, and keys. Do not use `math/rand` for security-sensitive values.

Sanitize paths before file access when any path segment comes from users or external systems. Prefer fixed roots, `filepath.Clean`, and checks that prevent escaping the intended directory.

Set `http.Server` timeouts for production-facing servers. Treat `unsafe`, shell execution, dynamic SQL, and path traversal exceptions as local decisions that need comments, tests, or clear input constraints.

## Documentation

Exported packages, types, functions, methods, and constants should have useful doc comments that start with the identifier when practical. Comments should explain behavior, contracts, edge cases, and concurrency/error semantics, not repeat the name.

## Sources

- Effective Go: https://go.dev/doc/effective_go.html
- Go Code Review Comments: https://go.dev/wiki/CodeReviewComments
- Organizing a Go module: https://go.dev/doc/modules/layout
- When To Use Generics: https://go.dev/blog/when-generics
- Generic Interfaces: https://go.dev/blog/generic-interfaces
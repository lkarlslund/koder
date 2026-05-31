# Go Quality Tooling

Use the project's existing `Makefile`, `Taskfile`, CI workflow, or scripts first. If no standard exists, use this baseline.

## Formatting and Imports

```bash
go fmt ./...
gofmt -w path/to/file.go
goimports -w path/to/file.go
```

`gofmt` is non-negotiable for Go. Use `goimports` when imports changed, if available. Do not hand-format generated code unless the generator expects it.

## Tests

```bash
go test ./...
go test -run TestName ./path/to/pkg
go test -count=1 ./...
go test -race ./...
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

Use table-driven tests for many input/output cases. Use subtests with stable names for scenario clarity. For test diffs, prefer `cmp.Diff(want, got)` and label output as `diff (-want +got)`.

Run `-race` for goroutines, shared memory, caches, handlers, background workers, and timeout/cancellation changes. It is slower; use it in CI or targeted local runs when full-suite cost is high.

## Fuzzing

Fuzz parsers, decoders, normalizers, URL/path handling, security-sensitive validation, and code that accepts untrusted input.

```bash
go test ./...
go test -fuzz=FuzzName ./path/to/pkg
go test -fuzz=FuzzName -fuzztime=30s ./path/to/pkg
```

Keep fuzz targets deterministic and fast. Seed known edge cases with `f.Add`. Commit useful corpus entries that reproduce fixed bugs when appropriate.

## Benchmarks and Profiles

```bash
go test -bench=. ./path/to/pkg
go test -bench=. -benchmem ./path/to/pkg
go test -run=^$ -bench=BenchmarkName -cpuprofile=cpu.out ./path/to/pkg
go tool pprof cpu.out
```

Do not claim performance improvements without a benchmark or profile. Stabilize benchmark inputs and avoid measuring setup unless setup is the target.

## Vet and Static Analysis

```bash
go vet ./...
staticcheck ./...
golangci-lint run ./...
golangci-lint config verify
golangci-lint linters
```

`go test` runs a subset of vet checks by default, but explicit `go vet ./...` is still a useful CI step. Staticcheck finds correctness, simplification, style, and performance issues. Golangci-lint is a runner that executes many linters in parallel and supports project config.

Recommended golangci-lint approach:

- Keep the config checked in and versioned.
- Start with defaults or a modest explicit set; avoid `enable-all` unless the team actively curates exclusions.
- Include correctness linters before style linters.
- Treat `//nolint` as a last resort and require a reason when possible.
- Verify config in CI.

Minimal `.golangci.yml` shape for v2:

```yaml
version: "2"
run:
  timeout: 5m
  relative-path-mode: gomod
linters:
  default: standard
```

Add stricter linters gradually after the baseline is clean.

## Vulnerability Checks

```bash
govulncheck ./...
govulncheck -test ./...
govulncheck -mode binary ./my-binary
```

Govulncheck uses Go vulnerability data and narrows findings to code paths or binary symbols that can affect the application. Run it before releases and in CI for services.

## Modules

```bash
go mod tidy
go list -m all
go list ./...
go work sync
```

Run `go mod tidy` after dependency or import changes. Avoid unrelated module churn in focused patches.

## CI Baseline

A reasonable default CI gate:

```bash
go mod tidy
git diff --exit-code -- go.mod go.sum
go test ./...
go vet ./...
golangci-lint run ./...
govulncheck ./...
```

Add `go test -race ./...` for libraries and services where runtime cost is acceptable, or for targeted packages when full-suite race runs are too expensive.

## Sources

- Go command docs: https://pkg.go.dev/cmd/go
- Testing package docs: https://pkg.go.dev/testing
- Go fuzzing docs: https://go.dev/doc/security/fuzz/
- Race detector docs: https://go.dev/doc/articles/race_detector.html
- Coverage docs: https://go.dev/doc/build-cover
- Go diagnostics docs: https://go.dev/doc/diagnostics.html
- Govulncheck docs: https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck
- Golangci-lint docs: https://golangci-lint.run/
- Golangci-lint config docs: https://golangci-lint.run/docs/configuration/file/
- Staticcheck docs: https://staticcheck.dev/docs/
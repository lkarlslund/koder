# Architecture

`koder` is a single-binary Go application with these main subsystems:

- `internal/app`: Cobra entrypoints and process bootstrap
- `internal/config`: TOML config plus XDG path resolution
- `internal/store`: Pebble-backed or JSON-file-backed persistence for sessions, chats, messages, parts, approvals, tasks, milestones, and todos
- `internal/provider`: OpenAI-compatible `/models` and `/chat/completions` client
- `internal/tools`: typed local tool execution surface
- `internal/agent`: prompt handling, tool approval flow, and event emission
- `internal/tui`: Bubble Tea TUI with transcript viewport, sidebar, and composer

Current v1 shape is a modular monolith inside one binary. The TUI talks to in-process services rather than a daemon.

## Storage

`koder` uses the `internal/store` package as its persistence boundary.

- The default backend is `pebble`.
- An alternate `jsonfs` backend stores inspectable JSON files on disk.
- Both backends persist the same core application state: sessions, chats, messages, parts, approvals, tasks, milestone plans, and todos.

The configured backend is selected from `config.toml` through `store.backend`.

## Verification

There is no repo-level `Makefile` or `Taskfile` test wrapper today. Contributors should run the Go checks directly.

Recommended baseline verification order:

1. `go test ./...`
2. `go test -race ./...`
3. `go vet ./...`
4. `go test -cover ./...`

Optional deeper checks when the tools are installed locally:

1. `staticcheck ./...`
2. `golangci-lint run`
3. `govulncheck ./...`

# Progress

## Current shape

`koder` is now a browser-based local coding agent. The CLI starts one local Go daemon, serves the embedded browser app, and opens a session URL under `/s/<session-id>`. The browser app is the only interactive UI surface.

Current architecture highlights:

- `internal/app` owns browser application state and actions.
- `internal/webui` serves embedded assets and bridges browser websocket RPC to the app controller.
- `internal/session` owns live session state, chats, planning data, and session-scoped mutation.
- `internal/agent` owns model turns, tools, approvals, compaction, and chat loop behavior.
- `internal/store` persists sessions, chats, transcripts, approvals, milestones, tasks, and runtime metadata.

## Current gaps

- Continue reducing direct store mutation outside live owners.
- Make browser RPC methods and state deltas smaller and more explicit.
- Keep simplifying session/chat orchestration around one source of truth.
- Improve browser UX for long-running tools, compaction, and background chats.

## Contributor testing workflow

This repo does not currently define a root `Makefile` or `Taskfile` for validation. Use direct Go commands instead.

Recommended verification order:

1. `go test ./...`
2. `go test -race ./...`
3. `go vet ./...`
4. `go test -cover ./...`

Optional deeper checks when available on your machine:

1. `staticcheck ./...`
2. `golangci-lint run`
3. `govulncheck ./...`

Test-writing defaults for this repo:

- Prefer table-driven tests with `t.Run`.
- Prefer deterministic tests using temp dirs, in-memory state, and local fakes over networked or time-sensitive flows.
- Add regression coverage for validation branches and error paths, not only happy paths.
- Keep testability refactors narrow and behavior-preserving.

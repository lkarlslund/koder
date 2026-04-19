# Update: 2026-04-19 uncommitted

## Goal

Create the first working implementation of `koder` from the approved plan.

## Changes

- Added the Go module and CLI.
- Implemented config loading and XDG paths.
- Implemented provider connectivity for OpenAI-compatible APIs and llama.cpp.
- Implemented SQLite persistence for sessions, messages, parts, approvals, and tasks.
- Implemented a Bubble Tea TUI with session list, transcript viewport, and composer.
- Implemented typed local tools and approval flow.
- Added switchable permission profiles with rule-based evaluation.
- Persisted the active permission profile on each session and exposed `/perm <profile>` in the TUI.
- Added a blocking approval chooser in the TUI for pending `ask` decisions.
- Moved the sidebar to the right and replaced the session browser with workspace and git status information.
- Added git status parsing and display for changed files relative to `HEAD`.
- Added `koder resume` with a startup session picker, where `Esc` creates a new session.
- Changed plain `koder` startup to always create a fresh session.
- Added a local `/quit` command and routed `Ctrl-C` through the same quit behavior.
- Added automatic session title generation using the active model after the 1st, 3rd, and 10th prompts.
- Changed `/new` and fresh startup sessions to remain unsaved until the first prompt is actually sent.

## Tests run

- `go test ./...`
- `go vet ./...`
- `staticcheck ./...`
- `golangci-lint run`

## Unresolved issues

- `websearch` remains intentionally unimplemented.
- Native model tool-calling is not wired yet.

## Next step

Continue tightening the approval flow so approved or denied tool calls can optionally resume the interrupted model turn automatically.

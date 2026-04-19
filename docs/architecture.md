# Architecture

`koder` is a single-binary Go application with these main subsystems:

- `internal/app`: Cobra entrypoints and process bootstrap
- `internal/config`: TOML config plus XDG path resolution
- `internal/store`: SQLite persistence for sessions, messages, parts, approvals, and tasks
- `internal/provider`: OpenAI-compatible `/models` and `/chat/completions` client
- `internal/tools`: typed local tool execution surface
- `internal/agent`: prompt handling, tool approval flow, and event emission
- `internal/tui`: Bubble Tea TUI with transcript viewport, sidebar, and composer

Current v1 shape is a modular monolith inside one binary. The TUI talks to in-process services rather than a daemon.

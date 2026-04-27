# koder

`koder` is a Go TUI coding agent that mixes a dense terminal-first interface with a provider-agnostic inference layer.

Current implementation includes:

- Bubble Tea based terminal UI
- OpenAI-compatible `/v1/chat/completions` streaming client
- JSON-backed Pebble or inspectable JSON-folder session storage
- Typed local tools with approval support
- Docs workflow for architecture, roadmap, progress, and per-update notes

## Commands

```bash
koder
koder --cwd /path/to/worktree
koder resume --project-root /path/to/worktree
koder resume --cwd /path/to/worktree --all-sessions
koder doctor
koder debug info
koder session tail --id 1 --addr 127.0.0.1:61347
koder version
```

## Build Metadata

For debug API and `koder version` build metadata, build the binary with:

```bash
scripts/build-koder
```

That injects version, commit, dirty state, and build time into the binary via Go `-ldflags -X`.

## Testing

This repo does not currently provide a root `Makefile` or `Taskfile` wrapper for validation. Run the Go checks directly:

```bash
go test ./...
go test -race ./...
go vet ./...
go test -cover ./...
```

When available locally, run deeper checks as well:

```bash
staticcheck ./...
golangci-lint run
govulncheck ./...
```

Test conventions for this repo:

- Prefer table-driven tests with `t.Run`.
- Prefer deterministic tests built around temp dirs and local fakes.
- Add coverage for validation and error branches, not only happy paths.
- Keep production refactors narrow and behavior-preserving when they only improve testability.

## Live Debug API

Set `KODER_DEBUG_API` before launching `koder` to expose a read-only local debug API from the running process:

```bash
KODER_DEBUG_API=127.0.0.1:61347 koder
```

If you prefer an ephemeral port, use `127.0.0.1:0`; `koder` will show the resolved address in the UI while running.

Useful endpoints:

- `/debug/runtime`
- `/debug/sessions`
- `/debug/sessions/<id>/transcript`
- `/debug/sessions/<id>/events`
- `/debug/http`

## Provider Configuration

`koder` no longer assumes a local model server. You must configure at least one OpenAI-compatible provider in `config.toml` and set `default_provider` before running the TUI.

Example:

```toml
default_provider = "local-llama"

[providers.local-llama]
name = "Local llama.cpp"
base_url = "http://127.0.0.1:8888/v1"
default_model = "qwen2.5-coder"
context_window = 32768
auto_compact_at = 85
stream = true
timeout = "2m"
```

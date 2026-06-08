# koder

`koder` is a browser-based coding agent for working inside a local checkout with your choice of OpenAI-compatible model provider. It gives the model a real project workspace, structured code tools, persistent chat history, resumable sessions, permissions, skills, MCP integrations, and a web UI built for steering long-running work.

It is meant for developers who want an agent that stays close to the codebase: inspect files, search with structured results, edit safely, run commands when allowed, split work into milestones and tasks, and keep the whole session inspectable.

## Why use it?

- **Bring your own model.** Use local or remote OpenAI-compatible `/v1/chat/completions` providers, configure models in the UI, and choose a separate model for compaction when useful.
- **Work in a real browser UI.** Start `koder`, open the web app, and manage chats, preferences, providers, MCP servers, permissions, and workspace state from one place.
- **Resume where you left off.** Sessions, chats, tool results, context usage, milestones, tasks, approvals, and compaction summaries are persisted locally.
- **Use safer code tools.** The model gets typed tools for reading, globbing, grep-style search, code search, targeted edits, explicit full-file writes, shell/exec sessions, image viewing, web fetch/search, skills, and task orchestration.
- **Avoid accidental rewrites.** Targeted changes should go through `edit`. The `write` tool creates new files by default and refuses to overwrite an existing file unless `force_overwrite=true`.
- **Scale work with chats.** Use milestones, tasks, and background chats to separate planning, decomposition, and execution work without losing the main thread.
- **Customize behavior.** User-editable managed assets live under `~/.koder`, including prompts and bundled skills.
- **Inspect what happened.** Optional local debug APIs expose runtime state, sessions, transcripts, events, and HTTP activity for troubleshooting.

## Quick Start

Build or install `koder`, then start it in a repository:

```bash
koder --project-root /path/to/worktree
```

By default, `koder` binds the web UI on a local ephemeral port and opens your browser. To choose the address or avoid opening a browser:

```bash
koder --project-root /path/to/worktree --web-bind 127.0.0.1:8080
koder --project-root /path/to/worktree --nobrowser
```

Resume previous work:

```bash
koder resume --project-root /path/to/worktree
koder resume --all-sessions
```

Check configuration and provider connectivity:

```bash
koder doctor
```

## Providers

`koder` does not require a specific hosted service. Configure one or more OpenAI-compatible providers in Preferences or in `config.toml`, then pick the default model from the web UI.

Example local provider:

```toml
[defaults]
provider_id = "local-llama"
model_id = "qwen3-coder"

[compaction]
auto_at_percent = 85
keep_tool_calls = 2

[providers.local-llama]
name = "Local llama.cpp"
base_url = "http://127.0.0.1:8888/v1"
stream = true
timeout = "10m"

[[models]]
provider_id = "local-llama"
model_id = "qwen3-coder"
context_window = 32768
```

Compaction can use the active chat model or a separate configured model. This is useful when your main coding model is expensive, slow, or not ideal for summarizing long histories.

## How It Works

`koder` runs as a local Go process. The browser UI talks to that process; the process talks to your model provider and executes approved local tools against the selected workspace.

The agent sees a structured tool surface instead of guessing at raw terminal workflows:

- `read`, `glob`, `grep`, and `code_search` for understanding the repo.
- `edit` for targeted replacements in existing files.
- `write` for new files or explicit full-file overwrites.
- `bash` and `exec_*` for command execution when allowed.
- milestone, task, and `chat_start` tools for organizing larger work.
- `skill`, MCP, web, and image tools for extending what the agent can do.

Permission profiles control network access, root filesystem mode, workspace mode, additional mounts, and per-tool policy. On Linux, shell sandboxing uses `bwrap` when shell tools are enabled.

## Requirements

- Go toolchain for building from source.
- At least one OpenAI-compatible provider.
- `rg` is optional; search falls back to a Go implementation when ripgrep is unavailable.
- `bwrap` is currently required for sandboxed shell command execution on Linux.
- macOS and Windows can run the web UI and non-shell features, but shell sandboxing is currently Linux-oriented.

## Useful Commands

```bash
koder
koder --project-root /path/to/worktree
koder --web-bind 127.0.0.1:8080
koder --nobrowser
koder resume --project-root /path/to/worktree
koder resume --all-sessions
koder doctor
koder debug info
koder session --help
koder skill --help
koder version
```

## Debug API

Koder exposes debug endpoints on the same web server as the UI. If the UI is
running at `http://127.0.0.1:44323`, the debug API is under
`http://127.0.0.1:44323/debug`.

Useful endpoints include:

- `/debug/runtime`
- `/debug/sessions`
- `/debug/sessions/<id>/transcript`
- `/debug/sessions/<id>/events`
- `/debug/chats`
- `/debug/http`

## Build

For normal local development:

```bash
go test ./...
go build ./cmd/koder
```

For release-style build metadata in `koder version` and the debug API:

```bash
scripts/build-koder
```

That injects version, commit, dirty state, and build time into the binary via Go linker flags.

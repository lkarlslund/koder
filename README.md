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
koder doctor
koder version
```

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

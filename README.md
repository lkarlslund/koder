# koder

`koder` is a Go TUI coding agent that mixes a dense terminal-first interface with a provider-agnostic inference layer.

Current implementation includes:

- Bubble Tea based terminal UI
- OpenAI-compatible `/v1/chat/completions` streaming client
- SQLite-backed sessions, transcript parts, tasks, and approvals
- Typed local tools with approval support
- Docs workflow for architecture, roadmap, progress, and per-update notes

## Commands

```bash
koder
koder doctor
koder version
```

## Local llama.cpp test target

The default provider profile targets a local llama.cpp server at `http://127.0.0.1:8888/v1`.

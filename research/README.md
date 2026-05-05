# Research

This folder contains external repos and libraries we inspect while shaping `koder`.

## Agent Comparisons

These are the agent-style projects we compare behavior and architecture against:

- `claude-code`  
  Anthropic's coding agent. Useful reference for transcript structure, token accounting, interrupt behavior, and tool/session flow.

- `codex`  
  OpenAI Codex CLI/TUI. Useful reference for request shaping, typed event flow, queueing, approvals, and structured context handling.

- `opencode`  
  OpenCode agent and UI stack. Useful reference for tool-call rendering, provider/model handling, and assistant/tool message structure.

- `pi-mono`  
  PI's coding agent. Useful reference for context tracking, especially anchoring on last known usage and estimating the tail after it.

- `qwen-code`  
  Qwen's coding agent. Useful reference for alternative agent UX, session/message structure, and tool/chat handling.

## UI / Runtime References

These are not primary agent comparisons, but they are still useful implementation references:

- `bubbletea`
- `ratatui`
- `tcell`
- `ink`
- `jetkvm-desktop`
- `arboard`

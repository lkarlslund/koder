# Progress

## 2026-04-19

- Bootstrapped the `koder` Go module and CLI entrypoints.
- Added config, provider, store, tool, agent, and TUI packages.
- Added SQLite persistence and OpenAI-compatible chat/model client support.
- Added docs structure and initial implementation notes.
- Replaced fixed per-tool approval modes with switchable permission profiles and rule evaluation.
- Added session-level permission profile persistence and a `/perm` command.
- Added an in-TUI approval chooser so pending tool approvals are explicitly presented as approve or deny actions.
- Moved the sidebar to the right and repurposed it as a workspace panel instead of a session list.
- Added git workspace status against `HEAD`, including branch, sync summary, and changed files.
- Changed startup semantics so `koder` opens a fresh session and `koder resume` opens a session picker.
- Added a local `/quit` command and routed `Ctrl-C` through the same quit path.
- Added model-generated session titles that refresh after the 1st, 3rd, and 10th user prompts.
- Changed new-session behavior so draft sessions are kept in memory and only saved after the first real prompt.
- Changed approved model tool calls to resume the same LLM turn so tool output is fed back into the conversation automatically.
- Added a live animated working indicator in the TUI header while the remote model is active.
- Added mouse-wheel scrolling for the transcript viewport so previous chat history can be browsed directly in the TUI.
- Changed transcript rendering so user prompts appear as styled message blocks instead of bracketed role labels.
- Changed the interaction loop so persisted events refresh the transcript immediately while a turn is still running, including tool and task updates.
- Changed mouse handling so native terminal selection works by default, with opt-in mouse capture via `/mouse on` when viewport scrolling is needed.
- Changed slash-command autocomplete to show only internal app commands, and kept internal slash commands out of the LLM tool contract.
- Separated internal slash commands from tool invocation so slash input is handled by the runtime, while model tool use stays on the dedicated tool contract.
- Fixed startup mouse handling so `ui.mouse` is respected again and mouse scrolling works on launch when enabled in config.
- Cleaned up model conversation rebuilding so `system_notice` stays out of prompt history and tool calls are fed back as structured semantics instead of UI wrapper text.
- Verified `go test ./...`, `go vet ./...`, `staticcheck ./...`, and `golangci-lint run`.

## Current gaps

- Tool execution is slash-command driven; LLM-native tool calling is not wired yet.
- `websearch` is stubbed.
- The TUI is functional but still early compared to Codex/OpenCode depth.

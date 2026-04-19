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
- Moved transient status out of the header into the sidebar and added a visible end-of-transcript working spinner so activity appears in the chat flow.
- Changed user prompt bubbles to render with explicit blank top and bottom padding lines, and kept user text on the same bubble background instead of inheriting markdown resets.
- Removed the top header line entirely, moved session/provider details into the sidebar, and made the transcript-tail working spinner more visible.
- Replaced the external Glamour markdown renderer with an internal renderer so transcript styling stays under local control and no longer fights background resets.
- Tightened internal markdown list rendering so consecutive bullet and numbered items no longer get blank lines between them.
- Split model-turn activity from generic loading so the bottom spinner only appears for active LLM work, with a stable `Working ...` label instead of startup or resume text.
- Moved hotkey hints out of the footer and into a dedicated `Keys` section at the bottom of the sidebar so the composer area stays clean.
- Fixed the bottom working spinner animation by refreshing the transcript viewport on each spinner tick while the model is active.
- Bottom-aligned the main TUI view so the chat input/footer sits flush with the terminal bottom edge instead of floating above it.
- Added sidebar context metrics from the latest token usage, plus `/compact` and pre-turn auto-compaction using the current session model and a persisted compaction summary boundary.
- Added a named TUI theme palette, moved transcript and markdown colors onto theme tokens, inserted a spacer line above the composer, and styled the input box to match the user chat bubble background.
- Persisted backend/model-turn failures as a single assistant error message in session history, and showed immediate pre-stream prompt errors directly in the draft transcript instead of only in sidebar status.
- Fixed user chat bubble rendering so blank padding lines, explicit multi-line input, and wrapped long lines all keep a consistent full-width background span like Codex.
- Centralized busy and spinner handling into a dedicated TUI state model so transcript/sidebar spinners are driven by one source of truth, which fixes stalled animation during `/compact` and other status-driven busy flows.
- Fixed transcript rendering so `system_notice` metadata like stored `usage` no longer leaks into visible chat messages.
- Replaced the heuristic markdown scanner with a `goldmark` CommonMark/GFM parser plus local ANSI renderer, which fixes headings and list detection and adds broader support for nested lists, blockquotes, links, tables, task lists, and fenced code blocks.
- Imported a curated set of non-default OpenCode theme palettes (`tokyonight`, `gruvbox`, `flexoki`, `rosepine`), expanded our named color tokens to cover more semantic markdown roles, and switched the default `koder` theme to `tokyonight`.
- Fixed TUI height calculation to measure the real footer height instead of subtracting a hardcoded 10 lines, which removes the blank rows that were being left at the top of taller terminals.
- Fixed transcript rendering for assistant `compaction` parts so compacted session summaries now pass through the markdown renderer instead of displaying raw `##` and `-` source text.
- Replaced the old one-off resume dialog with a reusable filterable picker model, wired `koder resume` through it, and added a new `/theme` picker with live preview and persisted theme selection inspired by OpenCode’s dialog-select flow.
- Verified `go test ./...`, `go vet ./...`, `staticcheck ./...`, and `golangci-lint run`.

## Current gaps

- Tool execution is slash-command driven; LLM-native tool calling is not wired yet.
- `websearch` is stubbed.
- The TUI is functional but still early compared to Codex/OpenCode depth.

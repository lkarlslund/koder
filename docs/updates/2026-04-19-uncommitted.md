# Update: 2026-04-19 uncommitted

## Later corrections

- This note reflects the repository state as of 2026-04-19. Current `koder` no longer uses SQLite for persistence; the active store layer supports `pebble` and `jsonfs`, with `pebble` as the default backend.

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
- Changed approval handling so once a gated tool is approved and executed, the tool result is sent back into the model automatically.
- Added a live animated working indicator in the TUI header while the model is processing.
- Added mouse-wheel scrolling support for the transcript viewport.
- Changed the chat transcript to render user prompts as filled message blocks instead of `[user]` and `[assistant]` labels.
- Changed turn handling so persisted events reload the visible transcript immediately, and task updates are now written into chat history too.
- Changed mouse handling to default to native terminal selection, and added `/mouse on` and `/mouse off` to toggle viewport mouse capture explicitly.
- Changed slash-command autocomplete to exclude tool commands, and added a guard test so internal slash commands are not offered to the model prompt.
- Removed the shared slash-command execution path so internal commands like `/perm`, `/approve`, and `/deny` are handled explicitly by the runtime instead of reusing tool-style slash dispatch.
- Fixed startup mouse initialization so configured mouse capture is enabled again at launch instead of only after a manual `/mouse on`.
- Cleaned up conversation serialization for the model: `system_notice` no longer leaks back into prompts, approval decorations stay out of model context, and tool calls are normalized from metadata before being re-sent.
- Removed the header status line, moved status into the sidebar, and rendered the working spinner as a transcript-tail activity line so active turns are visible where the conversation is happening.
- Changed user prompt bubble rendering to bypass markdown styling, add blank top and bottom padding lines, and keep the prompt text on the bubble background instead of falling back to black.
- Removed the remaining top header line entirely, moved session and provider metadata into the sidebar, and changed the transcript-tail activity indicator to a more visible animated working bar.
- Replaced Glamour with a native `internal/markdown` renderer for headings, lists, blockquotes, fenced code, and inline emphasis/code so transcript styling is controlled locally.
- Tightened native markdown list rendering so adjacent bullet and numbered list items stay compact instead of being separated by blank lines.
- Split model activity from generic loading so only active LLM turns show the transcript-tail spinner, and changed that indicator to display `Working ...` instead of transient resume or wait text.
- Moved the hotkey/help hints from above the chat input into a `Keys` section at the bottom of the sidebar.
- Fixed the transcript-tail spinner animation by refreshing the viewport content on each spinner tick while a model turn is active.
- Bottom-aligned the overall TUI layout so the footer and chat composer stay at the very bottom of the terminal window.
- Added shared context usage metrics, surfaced `used / max / % used` in the sidebar, and implemented manual `/compact` plus pre-turn auto-compaction with persisted summary boundaries in session history.
- Added a named theme palette for TUI and markdown colors, inserted a blank spacer line between the transcript and composer, and styled the input box with the same background as user chat bubbles.
- Changed backend and model-turn request failures to produce a single assistant-visible error message in the chat transcript, and fixed draft-session rendering so immediate first-prompt connection errors are visible too.
- Fixed user message bubble rendering so top and bottom padding lines match the full bubble width, and multi-line or wrapped user input keeps one continuous background span across each rendered line.
- Refactored TUI busy handling around a centralized busy/spinner state model so spinner rendering no longer depends on scattered `loading` vs `modelWorking` flags, and `/compact` now keeps animating while status events arrive.
- Fixed the transcript renderer to ignore `system_notice` parts, which removes spurious `usage` text from the chat window while keeping usage metadata available for sidebar/context accounting.
- Replaced the markdown line parser with a `goldmark` CommonMark/GFM AST renderer using local theme styles, which fixes `##` and `-` rendering and broadens support to nested lists, links, blockquotes, tables, task lists, and fenced code blocks.
- Borrowed non-default OpenCode theme palettes into `koder`, expanded the named theme token set for richer markdown/link/list styling, and made `tokyonight` the default theme instead of the old local placeholder palette.
- Fixed TUI resize logic to compute viewport height from the measured footer height instead of reserving a hardcoded 10 rows, which stops the app from leaving several blank lines at the top of the terminal.
- Fixed assistant compaction-summary rendering so `compaction` parts use the markdown renderer like normal assistant text, which resolves raw `##` headings and `-` list markers showing up after auto-compaction.
- Replaced the hardcoded resume-session picker with a reusable filterable picker model and used it for a new `/theme` command, including live theme preview while moving or filtering, OpenCode-style cancel-to-restore behavior, and persisted theme selection on confirm.
- Added named sidebar palette tokens and styled the right-hand status panel with a theme-controlled background, foreground, and border so the sidebar is visually distinct in every theme.
- Fixed slash autocomplete so exact matches for commands that still need arguments, such as `/perm`, continue to autocomplete on `Enter` instead of being treated as a finished command and doing nothing.
- Embedded the full local OpenCode theme catalog into `koder` instead of maintaining a tiny handpicked subset, and added Claude-inspired `claude-dark`, `claude-light`, and daltonized palettes so the theme picker covers both reference agents that actually ship reusable theme sets.

## Tests run

- `go test ./...`
- `go vet ./...`
- `staticcheck ./...`
- `golangci-lint run`

## Unresolved issues

- `websearch` remains intentionally unimplemented.
- Native model tool-calling is not wired yet.

## Next step

Reuse the new filterable picker for more runtime selection flows such as permission profiles or in-session session switching, so ad hoc one-off chooser UIs can be removed.

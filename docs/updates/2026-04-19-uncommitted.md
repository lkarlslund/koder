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

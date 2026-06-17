# Debug API

Koder exposes a JSON debug API from the same HTTP server as the browser UI. If
the UI is running at `http://127.0.0.1:7979`, the debug API is rooted at
`http://127.0.0.1:7979/debug`.

The debug API is intentionally operational rather than user-facing. It is used
to inspect live websocket clients, session and chat hydration, queued work,
stored transcript state, provider HTTP traffic, active LLM requests, and process
profiling. Most endpoints are read-only, but a few endpoints can change live
state. Those mutating endpoints are called out explicitly below.

There is no separate authentication layer in the debug API. Treat it as local
developer tooling and do not expose it on an untrusted network.

## Quick Start

Get the base URL from the running process output:

```sh
./koder-dev.sh
```

The process prints a line similar to:

```text
koder web ui: http://[::]:7979/s/<session-id>
```

Use the host and port from that URL:

```sh
BASE=http://127.0.0.1:7979
curl -sS "$BASE/debug/health" | jq .
curl -sS "$BASE/debug/runtime" | jq .
curl -sS "$BASE/debug/sessions" | jq .
```

The CLI also has a small helper:

```sh
koder debug info
```

For polling recorded session events:

```sh
koder session tail --id <session-id> --addr http://127.0.0.1:7979
```

## Data Sources

The API combines two kinds of state:

- Store-backed state: sessions, chats, timelines, planning data, approvals, and
  persisted chat metadata.
- Recorder-backed state: connected browser clients, selected session/chat per
  client, live chat runtime status, queue length, pending approvals, running tool
  call counts, lifecycle events, and provider HTTP traces.

This distinction matters. A chat can exist in the store without a hydrated live
runtime. In that case the debug response can report stored queue and transcript
counts, but not goroutine status or active tool activity.

## Response Format

Responses are JSON and are pretty-printed by the server. Errors use this shape:

```json
{
  "error": "message"
}
```

Common status codes:

- `200`: success.
- `400`: invalid input, invalid selector, unsupported action, or malformed JSON.
- `404`: requested client, chat, or route was not found.
- `405`: unsupported HTTP method for that endpoint.
- `503`: a debug-only service is unavailable, such as live chat rewind when no
  rewinder is installed.

## Runtime Endpoints

### `GET /debug/health`

Minimal liveness probe.

```sh
curl -sS "$BASE/debug/health" | jq .
```

Response fields:

- `ok`: always `true` when the server responds.
- `debug`: debug API base URL known by the process.

### `GET /debug/runtime`

Returns process, websocket client, chat runtime, and deep-debug status.

```sh
curl -sS "$BASE/debug/runtime" | jq .
```

Top-level fields:

- `process`: process timestamp, debug URL, build metadata, status, last error,
  and websocket client count.
- `clients`: known browser websocket clients, including disconnected clients
  retained by the recorder.
- `chats`: live chat runtime summaries known by the recorder.
- `deep_debug`: whether deep debug capture is enabled.

Useful `jq` views:

```sh
curl -sS "$BASE/debug/runtime" \
  | jq '.process, {clients: [.clients[] | {id, connected, selected_session, selected_chat}], chats}'
```

### `POST /debug/runtime`

Mutating endpoint. Toggles the recorder deep-debug flag.

```sh
curl -sS -X POST "$BASE/debug/runtime" \
  -H 'Content-Type: application/json' \
  -d '{"deep_debug":true}' | jq .
```

Request fields:

- `deep_debug`: boolean.

The response is the same shape as `GET /debug/runtime`.

Deep debug is intended for short-lived investigation. It can increase retained
diagnostic detail and should not be left on casually.

## Client Endpoints

### `GET /debug/clients`

Lists all known websocket clients.

```sh
curl -sS "$BASE/debug/clients" | jq '.clients[] | {id, connected, selected_session, selected_chat, stick_to_bottom}'
```

Client fields include:

- `id`: browser client ID.
- `connected`: whether the websocket is currently connected.
- `connected_at` and `last_seen`.
- `remote_addr` and `user_agent`.
- `selected_session` and `selected_chat`.
- browser/UI state such as focus, viewport size, scroll position,
  stick-to-bottom, open dialog, and interrupt button state.
- browser performance counters such as loaded and rendered timeline item counts,
  transcript DOM node count, markdown cache entries, last websocket payload
  bytes, last outbound websocket bytes, last chat/state delta bytes, and the
  active transcript render window.

### `GET /debug/clients/{client_id}`

Returns one client record.

```sh
curl -sS "$BASE/debug/clients/<client-id>" | jq .
```

Returns `404` if the client is unknown.

## Chat Runtime Endpoints

These endpoints use recorder-backed live chat summaries. They do not include
full stored transcript data.

### `GET /debug/chats`

Lists live chat runtime summaries.

```sh
curl -sS "$BASE/debug/chats" | jq '.chats[] | {id, session_id, title, status, busy, queue_len, running_tool_calls}'
```

Chat fields include:

- `id` and `session_id`.
- `title`.
- `status` and `status_text`.
- `active` and `busy`.
- `queue_len`.
- pending assistant text and reasoning byte lengths.
- pending approvals and running tool call count.

### `GET /debug/chats/{chat_id}`

Returns one live chat runtime summary.

```sh
curl -sS "$BASE/debug/chats/<chat-id>" | jq .
```

Returns `404` if the chat has no live recorder state.

## Session Endpoints

### `GET /debug/sessions`

Returns architecture notes, runtime summary, and all stored sessions with nested
chat debug summaries.

```sh
curl -sS "$BASE/debug/sessions" | jq .
```

Top-level fields:

- `architecture`: short description of ownership and debug data sources.
- `runtime`: same shape as `/debug/runtime`.
- `sessions`: stored session debug entries.

Session debug fields include:

- `id`, `title`, and `project_root`.
- `hydration`: `stored` or `hydrated`.
- `hydrated`: boolean convenience field.
- chat counts: stored, hydrated, visible, archived.
- `selected_client_count`.
- `record`: raw stored session record.
- `chats`: per-chat debug entries.
- `data_notes`: notes about missing or partial live state.

Per-chat debug fields include:

- `id`, `session_id`, `title`, `workflow_role`, and `archived`.
- `hydration` and `hydrated`.
- `queue_len`, `timeline_count`, pending approvals, pending executable tool
  calls, selected client count.
- context-token cache fields.
- `last_message`.
- `runtime`: live recorder summary when hydrated.
- `diagnostics`: known inconsistencies, such as a runtime reporting
  `running_tools` without running tool calls.

Useful view:

```sh
curl -sS "$BASE/debug/sessions" \
  | jq '.sessions[] | {id, title, project_root, hydration, stored_chat_count, hydrated_chat_count, visible_chat_count, archived_chat_count}'
```

### `GET /debug/sessions/{session_id}`

Returns detailed state for one session.

```sh
curl -sS "$BASE/debug/sessions/<session-id>" | jq .
```

Fields:

- `architecture`: same architecture notes as `/debug/sessions`.
- `debug`: session debug entry.
- `session`: raw stored session record.
- `chats`: raw stored chat records for the session.
- `timeline`: default chat timeline. The default chat is the first root chat
  where `ParentChatID` is empty, or the first chat if there is no root chat.
- `approvals`: pending approvals found in all session chats.
- `milestone_plan`: stored plan for the session.
- `tasks`: stored task items for milestones in the plan.
- `events`: recorded events for this session.

This endpoint is broad and can return a large payload for long chats. Prefer the
more targeted chat transcript endpoints when investigating one chat.

### `GET /debug/sessions/{session_id}/transcript`

Returns the default chat timeline for a session.

```sh
curl -sS "$BASE/debug/sessions/<session-id>/transcript" | jq '.timeline | length'
```

For a specific chat, use
`/debug/sessions/{session_id}/chats/{chat_id}/transcript`.

### `GET /debug/sessions/{session_id}/events`

Returns recorded events scoped to one session.

```sh
curl -sS "$BASE/debug/sessions/<session-id>/events" | jq '.events[] | {timestamp, source, kind, text, error}'
```

Events are recorder-backed and limited in memory. The default retention is the
most recent 256 global events and 256 events per session.

### `GET /debug/events`

Returns global recorded events across sessions.

```sh
curl -sS "$BASE/debug/events" | jq '.events[-20:]'
```

Global events include lifecycle entries that may not be tied to a session.

### `GET /debug/sessions/{session_id}/analysis`

Returns a lightweight analysis of session timeline and event patterns.

```sh
curl -sS "$BASE/debug/sessions/<session-id>/analysis" | jq .
```

Fields:

- `session_id`.
- `continue_count` and `continues`: lifecycle continue events.
- `bad_stop_count` and `bad_stops`: assistant messages that appear to stop with
  a prompt-like handoff immediately before another assistant tool-call message.
- `transcript_count`.

This is a debugging heuristic, not a source of truth.

### `GET /debug/sessions/{session_id}/approvals`

Returns pending approval records found in the session transcript.

```sh
curl -sS "$BASE/debug/sessions/<session-id>/approvals" | jq .
```

Approval records include session/chat ID, tool kind, tool-call ID, preview
command/path/pattern, status, and timestamp.

### `GET /debug/sessions/{session_id}/milestones`

Returns the stored milestone plan.

```sh
curl -sS "$BASE/debug/sessions/<session-id>/milestones" | jq .
```

Response fields:

- `session_id`.
- `plan`.

### `GET /debug/sessions/{session_id}/tasks`

Returns stored task items for milestones in the stored plan.

```sh
curl -sS "$BASE/debug/sessions/<session-id>/tasks" | jq .
```

Response fields:

- `session_id`.
- `tasks`.

### `GET /debug/sessions/{session_id}/legacy-tasks`

Returns legacy background task records for the session.

```sh
curl -sS "$BASE/debug/sessions/<session-id>/legacy-tasks" | jq .
```

This endpoint exists because older data and tooling may still refer to legacy background task records.

## Session Chat Endpoints

### `GET /debug/sessions/{session_id}/chats/{chat_id}`

Returns a focused debug summary for one chat in one session.

```sh
curl -sS "$BASE/debug/sessions/<session-id>/chats/<chat-id>" | jq .
```

Fields:

- `session_id` and `chat_id`.
- `chat`: session-scoped chat debug entry.
- `latest_compaction`: latest compaction timeline item summary, if present.
- `latest_usage`: latest assistant usage record, if present.
- `http_traces`: last 5 provider HTTP traces for this chat.

This endpoint is useful for checking whether a chat is hydrated, whether the
stored context-token cache is known, and what the latest usage/compaction record
claims.

### `GET /debug/sessions/{session_id}/chats/{chat_id}/transcript`

Returns stored timeline items for one chat.

```sh
curl -sS "$BASE/debug/sessions/<session-id>/chats/<chat-id>/transcript" | jq '.timeline[-5:]'
```

Query parameters:

- `limit=N`: return at most `N` timeline items.
- `tail=true`: when used with `limit`, return the last `N` items instead of the
  first `N`.

Examples:

```sh
curl -sS "$BASE/debug/sessions/$SESSION/chats/$CHAT/transcript?tail=true&limit=10" \
  | jq '.timeline[] | {id, seq, created_at, content}'
```

Without `limit`, the full chat transcript is returned. Long chats can produce
large responses.

### `POST /debug/sessions/{session_id}/chats/{chat_id}/rewind`

Mutating endpoint. Rewinds a live chat by removing the anchor item and all later
timeline items through the hydrated chat model. The store is updated by the chat
runtime.

This is intended for controlled debugging and recovery. It should not be used as
a general production workflow.

Request body options:

```json
{
  "anchor_item_id": "019...",
  "selector": ""
}
```

Fields:

- `anchor_item_id`: explicit timeline item ID to remove from.
- `selector`: optional selector used when `anchor_item_id` is omitted.

Supported selectors:

- `first_compaction_error`: first failed compaction item in the chat timeline.
  This is also the default selector when both fields are omitted.

Examples:

```sh
curl -sS -X POST "$BASE/debug/sessions/$SESSION/chats/$CHAT/rewind" \
  -H 'Content-Type: application/json' \
  -d '{"anchor_item_id":"019..."}' | jq .
```

```sh
curl -sS -X POST "$BASE/debug/sessions/$SESSION/chats/$CHAT/rewind" \
  -H 'Content-Type: application/json' \
  -d '{"selector":"first_compaction_error"}' | jq .
```

Response fields:

- `session_id`.
- `chat_id`.
- `anchor_item_id`.
- `result`: implementation-specific rewind result from the live chat runtime.

The endpoint returns `503` if the debug server was not wired to a live chat
rewinder.

## HTTP Trace Endpoints

Provider requests are recorded by the provider client. These endpoints are the
main way to investigate cache misses, prompt shape changes, partial streams, and
stuck LLM requests.

Recorded completed traces are capped in memory. The default retention is the
most recent 20 completed provider HTTP traces. Active traces are kept while the
request is in flight.

Trace fields include:

- `timestamp`.
- `provider_id`.
- `session_id` and `chat_id`.
- `method` and `path`.
- `status`, `duration_ms`, request/response byte counts.
- truncated `request_body` and `response_body`.
- request/response headers.
- `meta`: derived diagnostics, including request hashes, system-message hash,
  tools hash, common-prefix bytes from the previous request for that chat, and
  prompt-progress fields when available.
- `error`.

Request and response bodies are diagnostic data. Do not assume they are safe to
share externally.

### `GET /debug/http`

Returns active and completed provider HTTP traces. Supports filters.

```sh
curl -sS "$BASE/debug/http" | jq .
```

Query parameters:

- `session_id=<id>`.
- `chat_id=<id>`.
- `provider_id=<provider>`.
- `limit=N`.

Example:

```sh
curl -sS "$BASE/debug/http?session_id=$SESSION&chat_id=$CHAT&limit=5" \
  | jq '.traces[] | {timestamp, provider_id, status, duration_ms, request_bytes, response_bytes, meta}'
```

### `GET /debug/sessions/{session_id}/http`

Returns active and completed provider HTTP traces for one session.

```sh
curl -sS "$BASE/debug/sessions/$SESSION/http?limit=10" | jq .
```

### `GET /debug/sessions/{session_id}/chats/{chat_id}/http`

Returns active and completed provider HTTP traces for one chat.

```sh
curl -sS "$BASE/debug/sessions/$SESSION/chats/$CHAT/http?limit=10" | jq .
```

### `GET /debug/sessions/{session_id}/chats/{chat_id}/http/active`

Returns only active provider HTTP traces for one chat.

```sh
curl -sS "$BASE/debug/sessions/$SESSION/chats/$CHAT/http/active" | jq .
```

This endpoint is useful when the browser says a chat is streaming but no visible
output is arriving.

### `POST /debug/sessions/{session_id}/chats/{chat_id}/http/active`

Mutating endpoint. Cancels one or all active provider HTTP requests for one chat.

Request body:

```json
{
  "request_id": "019...",
  "action": "cancel"
}
```

Fields:

- `request_id`: optional active request ID. If omitted, all active requests for
  that chat are canceled.
- `action`: optional. Empty or `cancel` are accepted.

Cancel one active request:

```sh
curl -sS -X POST "$BASE/debug/sessions/$SESSION/chats/$CHAT/http/active" \
  -H 'Content-Type: application/json' \
  -d '{"request_id":"019...","action":"cancel"}' | jq .
```

Cancel all active requests for a chat:

```sh
curl -sS -X POST "$BASE/debug/sessions/$SESSION/chats/$CHAT/http/active" \
  -H 'Content-Type: application/json' \
  -d '{"action":"cancel"}' | jq .
```

Response fields:

- `session_id`.
- `chat_id`.
- `canceled`: number of requests whose contexts were canceled.
- `active`: active traces after cancellation. A just-canceled request can still
  appear briefly with `canceling: true`.

## Profiling Endpoints

The debug server mounts Go `net/http/pprof` handlers:

- `GET /debug/pprof/`
- `GET /debug/pprof/cmdline`
- `GET /debug/pprof/profile`
- `GET /debug/pprof/symbol`
- `GET /debug/pprof/trace`

Examples:

```sh
go tool pprof "$BASE/debug/pprof/profile?seconds=30"
go tool pprof "$BASE/debug/pprof/heap"
curl -sS "$BASE/debug/pprof/goroutine?debug=2"
```

The index handler also exposes the standard pprof profile names.

## Common Investigation Recipes

### Find the Active Session and Chat for a Browser Tab

```sh
curl -sS "$BASE/debug/clients" \
  | jq '.clients[] | select(.connected) | {id, selected_session, selected_chat, document_visible, window_focused}'
```

### Inspect One Chat

```sh
SESSION=<session-id>
CHAT=<chat-id>

curl -sS "$BASE/debug/sessions/$SESSION/chats/$CHAT" | jq .
curl -sS "$BASE/debug/sessions/$SESSION/chats/$CHAT/transcript?tail=true&limit=20" | jq .
curl -sS "$BASE/debug/sessions/$SESSION/chats/$CHAT/http?limit=5" | jq .
```

### Check Whether a Chat Is Hydrated

```sh
curl -sS "$BASE/debug/sessions/$SESSION" \
  | jq --arg chat "$CHAT" '.debug.chats[] | select(.id == $chat) | {hydration, hydrated, runtime, diagnostics}'
```

`hydration: "stored"` means the chat exists in storage but no live recorder state
is currently available. `hydration: "hydrated"` means the recorder knows about a
live chat runtime.

### Compare Recent LLM Requests

```sh
curl -sS "$BASE/debug/sessions/$SESSION/chats/$CHAT/http?limit=5" \
  | jq '.traces[] | {timestamp, request_bytes, status, meta}'
```

Relevant metadata:

- `request_sha256`: hash of the full request body.
- `messages_sha256`: hash of the JSON `messages` array.
- `system_sha256`: hash of the first system message content.
- `tools_sha256`: hash of the JSON `tools` array.
- `previous_lcp_bytes`: common prefix length between this request and the
  previous request for the same chat/session.
- prompt-progress fields from llama.cpp streams when present.

### Check Stuck LLM Calls

```sh
curl -sS "$BASE/debug/sessions/$SESSION/chats/$CHAT/http/active" | jq .
```

If the active request needs to be canceled:

```sh
curl -sS -X POST "$BASE/debug/sessions/$SESSION/chats/$CHAT/http/active" \
  -H 'Content-Type: application/json' \
  -d '{"action":"cancel"}' | jq .
```

### Look for Suspicious Auto-Continue or Bad Stop Patterns

```sh
curl -sS "$BASE/debug/sessions/$SESSION/analysis" | jq .
curl -sS "$BASE/debug/sessions/$SESSION/events" | jq '.events[-30:]'
```

### Inspect Pending Approvals

```sh
curl -sS "$BASE/debug/sessions/$SESSION/approvals" | jq .
```

### Inspect Planning State

```sh
curl -sS "$BASE/debug/sessions/$SESSION/milestones" | jq .
curl -sS "$BASE/debug/sessions/$SESSION/tasks" | jq .
curl -sS "$BASE/debug/sessions/$SESSION/legacy-tasks" | jq .
```

## Implementation Notes

The debug API is registered in `internal/debugsrv/debug.go` and mounted by the
browser server in `internal/webui/server.go`. The recorder is created during
startup in `cmd/koder/root.go` and passed into the provider client and web UI.

The session and chat debug endpoints intentionally read directly from the store
for persisted records, while live runtime status comes from the recorder. This
means a debug response can show both:

- persisted truth, such as a chat record and stored transcript count; and
- live truth, such as whether a runtime goroutine is busy or has queued input.

When these disagree, prefer the owning model for mutation. The debug API should
remain an inspection and emergency-recovery surface, not a second production
control plane.

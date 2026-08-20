# Koder voice protocol v1

`voice.v1` is the only integration boundary between the native client and
Koder. Connect with WebSocket `GET /voice/v1`; use `wss` whenever the HTTP
origin is HTTPS.

## Authentication and conversation selection

When Koder has a voice token, every request requires:

```http
Authorization: Bearer <token>
```

Authentication happens before WebSocket upgrade. The same header is required
for `/voice/v1/sessions` and `/voice/v1/artifacts/...`. Failed authentication
returns HTTP 401. A second simultaneous live voice connection from the same
Android installation returns HTTP 409; different registered devices may hold
calls concurrently. Reconnects with the same device and `call_id` replace the
old socket atomically.

Before opening a live WebSocket, native clients use the authenticated session
and chat endpoints:

- `GET /voice/v1/sessions` lists normal Koder sessions in `sessions` and
  advertises an optional signed Android update;
- `GET /voice/v1/sessions/<session-id>/chats` lists every chat in a session.
  Clients show ordinary chats as context but only select chats whose role is
  `voice`;
- `POST /voice/v1/sessions/<session-id>/chats` with an optional `title` creates
  a top-level voice chat in that regular session;
- `POST /voice/v1/sessions/temporary` creates a quick session with one voice
  chat and returns them as `session` and `chat`;
- `GET /voice/v1/server-info` returns a live, authenticated diagnostics snapshot
  with build/runtime identity, uptime, memory and concurrency counters, session
  counts, and current voice-connection state. Clients measure request round-trip
  time themselves so the displayed latency includes the network path.

For compatibility with pre-hierarchy clients, `POST /voice/v1/sessions`
creates a legacy one-chat voice session, while `PATCH` and `DELETE` on
`/voice/v1/sessions/<id>` manage those legacy conversations.

These requests do not acquire a device's live-voice-connection lease, start
audio routing, or request microphone permission.

Optional query parameters:

- `call_id`: stable client-generated ID for reconnects;
- `session_id` and `chat_id`: native session and voice chat to resume;
- `voice_session_id`: legacy one-chat voice session to resume;
- `resume_utterance_id`: the in-flight utterance the client will resend after
  `ready` on a replacement socket;
- `resume_transcript` and `resume_message`: whether those text events already
  reached the client; and
- `resume_output_sequence`: the first output PCM sequence the client still
  needs. Earlier output frames are not replayed.

Resume cursors are meaningful only with `resume_utterance_id`. Invalid cursors
are rejected during the HTTP handshake.

Text frames are UTF-8 JSON and include `"protocol":"voice.v1"`. Binary frames
are PCM envelopes described below.

## Client text frames

- `hello`: request a fresh `ready` snapshot and optionally set
  `response_pacing` to `concise`, `normal`, or `detailed` for subsequent turns
  on this connection. The pacing instruction is never added to chat history.
- `ping`: request `pong`.
- `select_voice_session` with `voice_session_id`: switch the durable voice-chat
  transcript owned by a legacy client.
- `create_voice_session` with an optional `title`: create and select a durable
  voice chat. Native clients own creation; the browser only lists voice chats.
- `select_session` with `session_id`: open a normal session.
- `select_voice_chat` with `session_id` and `chat_id`: select a voice chat in
  that session. Selecting an ordinary chat is rejected.
- `create_voice_chat` with `session_id` and optional `title`: create and select
  a voice chat in an existing regular session.
- `create_temporary_voice_chat` with optional `title`: create and select a
  quick session containing one voice chat.
- `utterance` with unique `utterance_id`, final `text`, and optional
  `session_id`: submit typed or already-transcribed input.
- `audio_start` with unique `utterance_id`, the exact advertised
  `audio_format`, and optional `languages`: begin one endpointed utterance.
- `audio_commit` with matching `utterance_id` and optional `session_id`: finish
  audio and start STT/routing.
- `audio_cancel`: discard the current audio utterance.
- `history` with `before_id` and `limit`: request older durable voice-chat
  turns. Native clients use a limit of five.
- `search_history` with `query` and an optional limit up to 20: search the
  complete durable transcript on Koder. Each result includes nearby messages
  so the client can jump to it without loading every intervening page.

Only one audio utterance may be open. Selecting a work or voice session cancels
any server-side partial audio.

`languages` is a sorted list of up to eight lowercase ISO 639-1 codes selected
by this client. Omit it for server-configured or unrestricted automatic
detection. One value becomes the STT service's hard language hint; multiple
values keep detection automatic and provide the selected languages as
recognition context. This reflects the OpenAI-compatible transcription API,
which has one `language` field rather than a native allow-list.

Examples:

```json
{"type":"hello","protocol":"voice.v1","response_pacing":"normal"}
{"type":"select_voice_session","protocol":"voice.v1","voice_session_id":"voice-id"}
{"type":"create_voice_session","protocol":"voice.v1","title":"Phone work"}
{"type":"utterance","protocol":"voice.v1","utterance_id":"uuid","text":"Check my email"}
{"type":"audio_start","protocol":"voice.v1","utterance_id":"uuid","audio_format":{"encoding":"pcm_s16le","sample_rate":16000,"channels":1},"languages":["da","en"]}
{"type":"audio_commit","protocol":"voice.v1","utterance_id":"uuid"}
{"type":"history","protocol":"voice.v1","before_id":"oldest-visible-message-id","limit":5}
{"type":"search_history","protocol":"voice.v1","query":"laptop BIOS","limit":20}
```

## Server text frames

- `ready`: listening state, `audio_config`, `call_state`, and an optional signed
  Android `app_update`.
- `state`: `recording`, `transcribing`, `processing`, `working`, or `speaking` for an
  utterance.
- `transcript`: final server STT text.
- `message`: concise result, generic parts, and optional delegation provenance.
- `history`: chronological older transcript entries plus `has_more`.
- `history_search`: recent matches, each with the exact `match` and a small
  chronological `context` window for anchored display.
- `tts_start`: output audio format follows in binary frames.
- `tts_end`: all output audio has been sent.
- `error`: user-presentable failure. The connection normally remains usable and
  is followed by a fresh `ready`.
- `pong`: UTC `server_time`.

`call_state.sessions` lists normal Koder sessions, `session_id` identifies the
selected session, `chats` lists its chats, and `chat_id` identifies the selected
voice chat. Chat summaries include role, activity, runtime state, and the
model-authored `status_text` published through the global `chat_status` tool.
`call_state.voice_sessions` lists selectable durable voice chats and omits
archived and deleted ones for compatibility with older clients. Deleted
sessions are also omitted from REST lists and server statistics; deletion is
permanent rather than a user-visible organization state.
Session summaries carry `archived`, `pinned`, and `favorite` flags
plus an RFC 3339 `updated_at` timestamp. `last_message` is the latest completed
spoken result, and monotonic `result_count` lets each client maintain its own
read cursor without server-side device state. Ephemeral `busy` and `status`
fields describe a currently loaded chat's live work and are never persisted.
Pinned chats are ordered first and each group is then ordered most recently
used first. Organization changes do not change `updated_at`; it remains the
conversation's actual last-activity time.

Native clients manage the normal hierarchy through authenticated REST calls:

- `PATCH /voice/v1/sessions/{session_id}` renames or archives/restores a session.
- `DELETE /voice/v1/sessions/{session_id}` permanently deletes an idle session.
- `PATCH /voice/v1/sessions/{session_id}/chats/{chat_id}` renames or
  archives/restores a chat.
- `DELETE /voice/v1/sessions/{session_id}/chats/{chat_id}` permanently deletes
  an archived leaf chat. Requiring archive first prevents accidental loss and
  preserves parent/child chat integrity.

`call_state.history` contains only the newest five complete conversational
turns, and `history_has_more` indicates whether the client can request an older
page. A history cursor is the first visible transcript entry ID; pages remain
chronological and do not normally split a user's utterance from its answer.
`processing` means the voice chat's model loop is thinking or preparing its
answer. `working` is emitted when that loop starts running tools and remains the
effective state across subsequent tool/model iterations until a later state
arrives. Its optional `working_on` field identifies an ordinary session when
the current tool delegates there; a later `working` frame may refine the target
without interrupting the busy state. Clients may delay their local waiting cue
for brief processing, but start it immediately for `working`. `speaking` is
always sent before streamed TTS audio so that cue can stop first. Working states
are ephemeral and are never persisted as transcript turns.
`voice_session_id` and `active_session_id` are legacy selection fields.

```json
{
  "type": "ready",
  "protocol": "voice.v1",
  "state": "listening",
  "audio_config": {
    "input": {"encoding":"pcm_s16le","sample_rate":16000,"channels":1},
    "output": {"encoding":"pcm_s16le","sample_rate":44100,"channels":1},
    "max_utterance_seconds": 60
  },
  "call_state": {
    "session_id": "session-id",
    "chat_id": "voice-chat-id",
    "sessions": [],
    "chats": []
  },
  "app_update": {
    "channel": "local",
    "application_id": "com.lkarlslund.koder.dev",
    "version_code": 42,
    "version_name": "0.1.0-local.example",
    "signing_certificate_sha256": "64 lowercase hex characters",
    "apk_sha256": "64 lowercase hex characters",
    "apk_size": 123456,
    "minimum_voice_protocol": "voice.v1",
    "download_uri": "/voice/v1/android/koder.apk"
  }
}
```

The update URI uses the same bearer authentication as the WebSocket. Clients
must only offer an update whose application ID and signing certificate match
the installed app and whose version code is newer. They must verify the byte
size, APK SHA-256, package metadata, and APK signer before invoking Android's
package installer.

## Android device binding

Koder normally authenticates each Android installation with its own revocable
bearer token. The browser UI creates a one-time, 30-minute invitation and shows
two QR codes: an authenticated invitation URL for downloading the embedded APK,
then a `koder://bind` URI containing the server origin and invitation code.

Android handles the binding URI and sends one unauthenticated request to
`POST /voice/v1/bind`:

```json
{
  "code": "kdb1_one_time_secret",
  "device": {
    "installation_id": "app-installation-uuid",
    "name": "Google Pixel 9",
    "manufacturer": "Google",
    "model": "Pixel 9",
    "android_version": "16",
    "app_version": "0.1.0-local.example",
    "app_id": "com.lkarlslund.koder.dev"
  }
}
```

The successful response contains the public device record and the device token
exactly once. Android encrypts it with Android Keystore. Koder persists only its
SHA-256 digest in `voice-devices.json` with mode `0600`. Subsequent Android
requests include the token and bounded `X-Koder-Device-*` identity headers so
Koder can display last use and current handset/app metadata. Revoking a device
immediately rejects its token.

## Connected-phone tool provider

While the main call owns its `call_id`, Android may open a second authenticated
WebSocket at `GET /voice/v1/device?call_id=<same-id>`. This sidecar exists so a
voice-chat tool call can wait for Android while the main socket continues to
stream voice state and audio. It never acquires a second call lease.

Android first sends `device_hello` with only the action IDs the user enabled
and for which Android permission is currently available:

```json
{"type":"device_hello","protocol":"voice.v1","capabilities":["device_status","search_contacts"]}
```

Koder filters unknown IDs and replies with `device_ready`. During a voice-chat
turn it can send a request; Android returns either a bounded result or error:

```json
{"type":"device_tool_request","protocol":"voice.v1","request_id":"uuid","action":"search_contacts","arguments":{"query":"Steen"}}
{"type":"device_tool_result","protocol":"voice.v1","request_id":"uuid","result":{"text":"Found one contact","data":{"contacts":[]}}}
```

The server owns action descriptions and argument schemas; client-provided text
never becomes a tool definition. A disconnected or disabled phone action is
not offered to the model. Android confirms consequential actions on the phone
before returning success. The server accepts one provider belonging to the
active call, bounds frames to 128 KiB, results to 64 KiB, and waits at most two
minutes for local permission or confirmation.

## Binary PCM envelope

All integers are big-endian except PCM samples, which are signed 16-bit
little-endian. The fixed header is 12 bytes:

| Offset | Bytes | Meaning |
| --- | ---: | --- |
| 0 | 4 | ASCII `KVA1` |
| 4 | 1 | kind: `1` input PCM, `2` output PCM |
| 5 | 1 | flags; zero in v1 |
| 6 | 2 | reserved; must be zero |
| 8 | 4 | unsigned sequence number |
| 12 | 1..65536 | non-empty, even-sized PCM16LE payload |

Sequences start at zero independently for each input and output stream and must
be contiguous. The server rejects malformed, oversized, odd-sized, wrong-kind,
or out-of-order frames. Total input is bounded by `max_utterance_seconds`.

## Generic message parts

```json
{
  "type": "message",
  "protocol": "voice.v1",
  "utterance_id": "uuid",
  "message": {
    "spoken_text": "Here is the screenshot.",
    "transcript_id": "durable-timeline-item-id",
    "parts": [{
      "id": "optional-id",
      "mime_type": "image/png",
      "uri": "/voice/v1/artifacts/session/session-id/attachment-id",
      "metadata": {"name":"current-state.png","alt":"Current app state"}
    }],
    "delegation": {
      "session_id": "session-id",
      "session_title": "Android app",
      "chat_id": "chat-id",
      "needs_attention": false
    }
  }
}
```

Every part requires `mime_type`. `data` is any inline JSON value; `uri` is a
resource reference; clients must retain a generic fallback for unknown MIME
types. Relative URIs resolve against the Koder HTTP origin. Send the bearer
token only to the same origin.

`spoken_text` and the primary `text/plain` part contain conversational speech,
not Markdown or document formatting. A deliberate inline visual uses
`metadata.presentation = "true"`; artifact parts with a `uri` are also visual.
When a voice-active client normally hides the transcript, receiving either
kind must open a separate presentation surface without making the transcript
visible. A user can close that surface independently or explicitly open the
transcript.

Koder may send a `render` frame before the final answer when a tool deliberately
produces displayable output:

```json
{"type":"render","protocol":"voice.v1","utterance_id":"uuid","parts":[{"mime_type":"image/png","uri":"/voice/v1/artifacts/session/id/image"}]}
```

It uses the same generic `parts` contract as `message`. Clients display visual
parts immediately while keeping the voice turn active. Parts with
`metadata.surface = "transcript"` are recorded without interrupting the voice
surface. `application/vnd.koder.tool-activity+json` contains generic `tool`,
`title`, `status`, and `summary` fields. `metadata.render_key` lets clients
suppress the duplicate when a durable part appears again in the final message.

Structured visuals use `application/vnd.koder.presentation+json`. Its `data`
is a versioned document rather than a task-specific screen:

```json
{
  "version": 1,
  "blocks": [
    {"kind":"text","text":"Tomorrow","style":"heading"},
    {"kind":"key_value","items":[{"key":"Time","value":"10:00"}]},
    {"kind":"list","items":[{"title":"DHL Stafet","detail":"Mindeparken"}]},
    {"kind":"progress","label":"Calendar sync","value":1,"max":1},
    {"kind":"image","uri":"/voice/v1/artifacts/session/id/map.png","alt":"Map"},
    {"kind":"action","label":"Open details","uri":"https://example.com/event"},
    {"kind":"file","name":"appointment.ics","uri":"/voice/v1/artifacts/offered/token","mime_type":"text/calendar"}
  ]
}
```

Text styles are `body`, `heading`, `caption`, or `code`. List items use
`title` and optional `detail`; key-value items use `key` and `value`. URIs are
relative to Koder or absolute HTTP(S) links. Clients should render known blocks
with native controls, show a useful placeholder for unknown blocks, and fall
back to the generic MIME-part view when a document version is unsupported.

Transcript entries may include the same `parts` array. An entry with role
`activity` and empty text represents durable tool activity rather than spoken
assistant text.

### Phone photo tools

When Android grants Photos access and enables the capability, a voice chat is
offered four separate tools:

- `phone_photos_search` returns bounded photo IDs, names, and capture times
  without transferring pixels.
- `phone_photos_thumbs` transfers up to 12 low-resolution candidates into
  managed session-temporary storage for `view_image` inspection.
- `phone_photo_view` transfers one selected photo at inspection resolution.
- `phone_photo_transfer` copies one selected original to an explicit workspace
  path under Koder's ordinary filesystem access and permission policy.

The device sidecar carries artifacts as bounded base64 fields in
`device_tool_result`. A result may contain at most 12 artifacts and 25 MiB of
decoded bytes in total. Koder materializes bytes immediately and never stores
them inline in chat history.

`transcript_id`, when present, is the stable ID of the durable assistant
timeline entry represented by this live message. Clients use it to attach
bookmarks, follow-ups, and transcript navigation to the same response after a
reconnect. It is omitted when no durable assistant entry exists.

## Reconnect and compatibility

Clients reconnect with the same `call_id` and `voice_session_id`, then wait for
`ready` and resend the same in-flight utterance with its original
`utterance_id`. Koder owns the work independently of either WebSocket and
deduplicates that resend. The resume query cursor suppresses transcript,
message, and PCM output the client already received. An audio utterance is
replayed as its original `audio_start`, contiguous binary input frames, and
`audio_commit`; a recording interrupted before commit may continue appending
frames after reconnect. Reusing an utterance ID with different text or audio is
an error. Final exchanges already accepted by Koder remain in the durable
voice-chat transcript.

Unknown JSON fields are ignored. Unknown frame types receive `error`. Shared
fixtures in `testdata` are decoded by both Go and Kotlin tests, including the
phone-tool request/result contract.

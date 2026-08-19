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
returns HTTP 401. A second simultaneous live voice connection returns HTTP 409.

Before opening a live WebSocket, native clients use the authenticated
conversation endpoint:

- `GET /voice/v1/sessions` lists durable voice conversations and advertises an
  optional signed Android update;
- `POST /voice/v1/sessions` with `{"title":"Personal"}` creates a durable
  conversation and returns it as `voice_session` alongside the refreshed list.
- `GET /voice/v1/server-info` returns a live, authenticated diagnostics snapshot
  with build/runtime identity, uptime, memory and concurrency counters, session
  counts, and current voice-connection state. Clients measure request round-trip
  time themselves so the displayed latency includes the network path.

These requests do not acquire the single-live-voice-connection lease, start
audio routing, or request microphone permission.

Optional query parameters:

- `call_id`: stable client-generated ID for reconnects;
- `voice_session_id`: durable voice chat to resume. With no value, Koder uses
  the most recently updated voice chat or creates the first one.

Text frames are UTF-8 JSON and include `"protocol":"voice.v1"`. Binary frames
are PCM envelopes described below.

## Client text frames

- `hello`: request a fresh `ready` snapshot and optionally set
  `response_pacing` to `concise`, `normal`, or `detailed` for subsequent turns
  on this connection. The pacing instruction is never added to chat history.
- `ping`: request `pong`.
- `select_voice_session` with `voice_session_id`: switch the durable voice-chat
  transcript owned by the current live call.
- `create_voice_session` with an optional `title`: create and select a durable
  voice chat. Native clients own creation; the browser only lists voice chats.
- `select_session` with `session_id`: select an ordinary work target. An empty
  ID restores semantic automatic selection.
- `utterance` with unique `utterance_id`, final `text`, and optional
  `session_id`: submit typed or already-transcribed input.
- `audio_start` with unique `utterance_id`, the exact advertised
  `audio_format`, and optional `languages`: begin one endpointed utterance.
- `audio_commit` with matching `utterance_id` and optional `session_id`: finish
  audio and start STT/routing.
- `audio_cancel`: discard the current audio utterance.
- `history` with `before_id` and `limit`: request older durable voice-chat
  turns. Native clients use a limit of five.

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
```

## Server text frames

- `ready`: listening state, `audio_config`, `call_state`, and an optional signed
  Android `app_update`.
- `state`: `recording`, `transcribing`, `processing`, `working`, or `speaking` for an
  utterance.
- `transcript`: final server STT text.
- `message`: concise result, generic parts, and optional delegation provenance.
- `history`: chronological older transcript entries plus `has_more`.
- `tts_start`: output audio format follows in binary frames.
- `tts_end`: all output audio has been sent.
- `error`: user-presentable failure. The connection normally remains usable and
  is followed by a fresh `ready`.
- `pong`: UTC `server_time`.

`call_state.sessions` lists ordinary and quick work targets.
`call_state.voice_sessions` lists durable voice chats. Session summaries carry
an RFC 3339 `updated_at` timestamp and are ordered most recently used first.
`call_state.history` contains only the newest five complete conversational
turns, and `history_has_more` indicates whether the client can request an older
page. A history cursor is the first visible transcript entry ID; pages remain
chronological and do not normally split a user's utterance from its answer.
`working` is emitted only immediately before work is delegated into another
chat. Its `working_on` field contains that ordinary session's bounded summary.
Clients may play a local waiting cue until a later state arrives; `speaking` is
always sent before streamed TTS audio so that cue can stop first. Working states
are ephemeral and are never persisted as transcript turns.
`voice_session_id` and `active_session_id` identify the current choices.

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
    "voice_session_id": "voice-id",
    "active_session_id": "work-id",
    "sessions": [],
    "voice_sessions": []
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

## Reconnect and compatibility

Clients reconnect with the same `call_id` and `voice_session_id`, then wait for
`ready`. V1 does not replay an in-flight utterance; clients must not blindly
resend committed work. Final exchanges already accepted by Koder are in the
durable voice-chat transcript.

Unknown JSON fields are ignored. Unknown frame types receive `error`. Shared
fixtures in `testdata` are decoded by both Go and Kotlin tests, including the
phone-tool request/result contract.

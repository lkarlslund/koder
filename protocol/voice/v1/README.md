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

These requests do not acquire the single-live-voice-connection lease, start
audio routing, or request microphone permission.

Optional query parameters:

- `call_id`: stable client-generated ID for reconnects;
- `voice_session_id`: durable voice chat to resume. With no value, Koder uses
  the most recently updated voice chat or creates the first one.

Text frames are UTF-8 JSON and include `"protocol":"voice.v1"`. Binary frames
are PCM envelopes described below.

## Client text frames

- `hello`: request a fresh `ready` snapshot.
- `ping`: request `pong`.
- `select_voice_session` with `voice_session_id`: switch the durable voice-chat
  transcript owned by the current live call.
- `create_voice_session` with an optional `title`: create and select a durable
  voice chat. Native clients own creation; the browser only lists voice chats.
- `select_session` with `session_id`: select an ordinary work target. An empty
  ID restores semantic automatic selection.
- `utterance` with unique `utterance_id`, final `text`, and optional
  `session_id`: submit typed or already-transcribed input.
- `audio_start` with unique `utterance_id` and the exact advertised
  `audio_format`: begin one endpointed utterance.
- `audio_commit` with matching `utterance_id` and optional `session_id`: finish
  audio and start STT/routing.
- `audio_cancel`: discard the current audio utterance.

Only one audio utterance may be open. Selecting a work or voice session cancels
any server-side partial audio.

Examples:

```json
{"type":"select_voice_session","protocol":"voice.v1","voice_session_id":"voice-id"}
{"type":"create_voice_session","protocol":"voice.v1","title":"Phone work"}
{"type":"utterance","protocol":"voice.v1","utterance_id":"uuid","text":"Check my email"}
{"type":"audio_start","protocol":"voice.v1","utterance_id":"uuid","audio_format":{"encoding":"pcm_s16le","sample_rate":16000,"channels":1}}
{"type":"audio_commit","protocol":"voice.v1","utterance_id":"uuid"}
```

## Server text frames

- `ready`: listening state, `audio_config`, `call_state`, and an optional signed
  Android `app_update`.
- `state`: `recording`, `transcribing`, `processing`, `working`, or `speaking` for an
  utterance.
- `transcript`: final server STT text.
- `message`: concise result, generic parts, and optional delegation provenance.
- `tts_start`: output audio format follows in binary frames.
- `tts_end`: all output audio has been sent.
- `error`: user-presentable failure. The connection normally remains usable and
  is followed by a fresh `ready`.
- `pong`: UTC `server_time`.

`call_state.sessions` lists ordinary and quick work targets.
`call_state.voice_sessions` lists durable voice chats.
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

## Reconnect and compatibility

Clients reconnect with the same `call_id` and `voice_session_id`, then wait for
`ready`. V1 does not replay an in-flight utterance; clients must not blindly
resend committed work. Final exchanges already accepted by Koder are in the
durable voice-chat transcript.

Unknown JSON fields are ignored. Unknown frame types receive `error`. Shared
fixtures in `testdata` are decoded by both Go and Kotlin tests.

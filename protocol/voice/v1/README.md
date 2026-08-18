# Koder voice protocol v1

The Android client connects to `GET /voice/v1` with an `Authorization: Bearer
<token>` header. Control frames are UTF-8 JSON text. The server sends `ready`
immediately after the WebSocket upgrade.

Client frames:

- `{"type":"hello","protocol":"voice.v1"}` refreshes call state.
- `{"type":"select_session","protocol":"voice.v1","session_id":"..."}`
  changes the call's active target without starting work.
- `{"type":"utterance","protocol":"voice.v1","utterance_id":"...","text":"...","session_id":"..."}`
  submits a final transcript. `session_id` is optional and overrides routing.
- `{"type":"ping","protocol":"voice.v1"}` requests a `pong`.

Server frames:

- `ready` contains `call_state.sessions`, `active_session_id`, and state
  `listening`.
- `state` reports transient processing state for an utterance.
- `message` contains concise `spoken_text`, generic MIME-typed `parts`, and
  optional delegation provenance.
- `error` contains a user-presentable error string.
- `pong` contains `server_time`.

Audio transport is deliberately additive. The usable first client performs
speech recognition on Android and sends final text utterances; later v1 frames
may add binary PCM streaming without changing message or delegation semantics.

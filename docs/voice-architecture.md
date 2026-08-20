# Voice-first Android architecture

Status: implemented first release

Koder's Android client is a native phone-style voice surface over the existing
session system. Android owns call audio and endpointing. Koder owns speech
services, durable voice-chat history, coordinated work, and
generic presentations.

The app lives in this repository under `clients/android`, but remains a separate
Gradle product. Its only integration boundary is the authenticated, versioned
protocol in `protocol/voice/v1`.

## Product model

There are three distinct objects:

- A **voice chat** is a durable `SessionKindVoice` session with one
  `WorkflowRoleVoice` chat. It records final user transcripts and concise
  assistant results. A user creates and selects voice chats in Android, while
  the Web UI lists them in a separate Voice Chats tab for inspection.
- A **voice call** is one ephemeral authenticated WebSocket connection. Koder
  enforces one live call process-wide while allowing any number of durable
  voice chats.
- A **work target** is an ordinary or quick Koder session. Its existing chat
  performs the work with its own tools, permissions, approvals, transcript,
  and workspace boundary.

The voice chat is a coordination and audit layer, not a privileged agent. It
cannot bypass a target chat's permission or approval state.

## Request flow

```text
Android Core-Telecom call
  -> AudioRecord (16 kHz mono PCM16)
  -> Silero VAD and endpoint state machine
  -> authenticated voice.v1 WebSocket
  -> remote OpenAI-compatible STT
  -> normal persistent chat with the Voice profile
  -> optional permission-gated tool call to the connected Android phone
  -> voice-scoped session tools when other work is needed
  -> existing, quick, or persistent Koder session
  -> ordinary target chat and its normal tools
  -> concise result plus generic MIME parts
  -> remote streaming TTS
  -> AudioTrack and Android companion feed
```

Every final transcript is a normal user turn in the selected durable voice
chat. The Voice role supplies short, conversational behavior and the normal
chat loop supplies history, model execution, tool calls, and the final answer.
There is no separate stateless router or summarizer model call.

Session mechanics live in the voice-only tool definitions, not in the role
prompt. `session_list` returns bounded work-session summaries,
`session_delegate` asks an existing session to inspect its history or do work,
and `session_start` creates a temporary or persistent target. Tool arguments
and lifecycle guidance therefore stay synchronized with the capabilities the
model is actually offered. Quick chats remain visible and can be closed or
promoted in the Web UI.

Delegation waits for the target chat's sealed response. A busy target, approval
request, or input request becomes a short voice result directing the user to
the Web UI. The target chat remains the source of truth.
Android labels the initial model phase as **Thinking** and begins its local busy
cue only if that phase lasts more than two seconds. Koder switches the wire
state to `working` as soon as the voice chat runs any tool, keeps that state
across multi-tool model iterations, and optionally adds the delegated session
summary so Android can show where the work is happening. Android carries an
already-playing cue across that transition and stops it before speech playback.
The Voice role returns one or two short, plain conversational sentences after
tool work. Its final response explicitly overrides the shared document-format
guidance: Markdown, headings, lists, tables, code, link syntax, and raw URLs do
not belong in speech. The server also strips accidental formatting before TTS
and before sending the primary text part to Android.

When the user asks to see something, or visual structure is materially useful,
the voice chat deliberately calls the voice-only `present` tool. Tool results
and offered artifacts remain in the durable voice timeline and can also be
surfaced as generic visual parts. The tool's own description contains the
mechanics; the role prompt only defines the generic conversational behavior.

## Server boundaries

- `internal/voice` owns call state, delegation contracts,
  audio framing, and the process-wide call lease.
- `internal/voiceapi` owns HTTP/WebSocket authentication and `voice.v1` wire
  DTOs.
- `internal/app/voice.go` adapts the controller, sessions, providers, chat
  runtime, and artifacts to those interfaces.
- `internal/provider` exposes OpenAI-compatible transcription and streaming
  speech APIs.
- `internal/chatstatus` and the `chat_status` tool provide model-authored,
  descriptive chat status without replacing runtime states such as reasoning,
  streaming, waiting, error, or idle.
- `internal/phonedevice` owns the one-active-call phone capability catalog and
  RPC hub. `internal/tools/phonetool` exposes one dynamic voice-only `phone`
  tool whose action enum contains only capabilities currently advertised by
  Android.

Voice chats and their transcripts are normal selectable Web UI sessions. Voice
and quick sessions use one chat; Android intentionally does not reproduce the
browser workspace or planning UI.

Transcript history uses the same indexed timeline-page storage path as the Web
UI. A ready snapshot carries only the newest five complete user/assistant
turns. Android requests older five-turn pages when the user reaches the top,
prepends them without moving the visible content, and stops when `has_more` is
false. Koder never hydrates an entire long transcript merely to serve a native
history page.

## Android boundaries

The client is Kotlin with programmatic native Views. `CallController` composes
separate interfaces for:

- `MicrophoneCapture` / `AndroidMicrophoneCapture`;
- `VoiceActivityDetector` / pinned Silero ONNX Runtime inference;
- `VadEndpointPipeline` for pre-roll, silence, and maximum duration;
- `VoiceConnection` for authenticated transport and reconnect backoff;
- `StreamingAudioPlayback` / `AudioTrack`;
- `TelecomVoiceCall` for audio focus, hold, headset controls, speaker,
  earpiece, wired routes, and Bluetooth.
- `PhoneDeviceConnection` for the authenticated phone-tool sidecar and
  `AndroidPhoneToolProvider` for permission checks, local queries, intents,
  direct actions, and phone-side confirmation.

Silero consumes 512 samples at 16 kHz and retains its recurrent state and
64-sample context. The model is downloaded from a pinned upstream commit during
the build and accepted only if its SHA-256 matches. Speech recognition and
synthesis do not run on Android.

## App updates

Local-development and official clients are separate Android applications:
`com.lkarlslund.koder.dev` and `com.lkarlslund.koder`. Each has its own stable,
private signing key. A Koder build embeds exactly one matching signed APK and a
small manifest; Android source/signing fingerprints make local builds reuse the
cached APK unless an input changed.

The optional update manifest travels in the authenticated `ready` frame and
the APK is streamed from `/voice/v1/android/koder.apk` using the same bearer
token. Android only offers a higher version for its own application ID and
signing certificate. It then verifies the downloaded size and SHA-256 plus the
package ID, version code, and APK signer before opening the system installer.
Android retains the final install-consent and unknown-source controls. GitHub
release builds create the official APK once and embed identical bytes in both
Linux architectures, keeping Koder a single-file download.

Local VAD remains active during streamed playback. Speech onset stops playback
and starts a new utterance, allowing the user to interrupt Koder. The first
release deliberately does not implement incoming calls, an always-listening
wake word, or offline speech inference.

## Phone-contributed tools

Phone tools exist only while their Android app is connected to the active voice
call. The user enables groups in Settings; all groups start disabled. Android
re-checks runtime or special-access permission when connecting, then advertises
only usable action IDs. Revoking a permission removes those actions on the next
conversation. Koder owns the trusted catalog and never accepts tool names,
schemas, or prompt text from Android.

The initial catalog covers device/network/battery/storage status, location,
contacts, calendar, SMS, current notification and email previews, calls,
email/contact/calendar drafts, maps, alarms and timers, clipboard, HTTPS links,
media control, installed app discovery/launching, and Android sharing. There is
no generic Android API for browsing arbitrary email inboxes, so inbox access
continues to come from ordinary Koder integrations; notification access can
surface mail previews already shown by Android.

Read operations run off the UI thread and return generic bounded JSON. Calls,
SMS sending, drafts, writes, navigation, media/app control, and sharing require
a visible confirmation dialog on the phone. System draft and chooser screens
retain their own final review as well. SMS and call-log permissions have store
distribution restrictions; Koder's local sideload channel can offer SMS access,
while a future store build may need to omit it unless it qualifies as a default
handler.

Server address and bearer token are encrypted at rest with an Android Keystore
key. Reconnect uses a stable call ID for the logical call and exponential
backoff. An in-flight utterance is not automatically replayed, preventing
duplicate delegated work.

Speech-language preference belongs to the phone rather than the durable voice
chat or server. Android can leave detection automatic, select one language for
a hard STT hint, or select several expected languages. The latter are sent as
recognition context because the OpenAI-compatible API exposes only one hard
`language` value. The preference applies to every utterance on the next
connection and is validated as bounded ISO 639-1 codes at the server boundary.

## Generic presentations

The voice protocol does not enumerate calendar, email, diff, screenshot, or
task widgets. A response has concise speech and zero or more generic parts:

```json
{
  "spoken_text": "Here is the calendar entry.",
  "parts": [
    {
      "id": "optional-stable-id",
      "mime_type": "text/calendar",
      "uri": "/voice/v1/artifacts/offered/token",
      "metadata": {
        "name": "appointment.ics",
        "title": "Appointment with Steen"
      }
    }
  ]
}
```

`data` can be any JSON value for an inline representation. `uri` references a
resource. Android renders inline plain/Markdown text and `image/*`, and shows a
generic attachment card for everything else. Same-origin artifacts are fetched
with the bearer credential; generic files are copied into a cache-only
`FileProvider` URI before another Android app is allowed to read them. Unknown
MIME types remain usable instead of invalidating the message.

An inline part created by `present` carries `metadata.presentation = "true"`.
While voice is active, Android switches from the quiet voice surface to a
separate presentation surface automatically. The spoken response remains in
the hidden conversation history; showing a table, image, or attachment does
not reveal the transcript. Closing the presentation returns to the voice
surface, while the existing Show transcript control remains an explicit user
choice.

Koder only exposes artifacts already surfaced by a delegated tool result.
Artifact URLs share the voice bearer boundary and cannot read arbitrary paths.

## Speech configuration

The `[voice]` configuration selects remote OpenAI-compatible speech endpoints
independently of Android:

```toml
[voice]
stt_provider_id = "speech"
stt_model_id = "koder-stt"
stt_language = "en"
tts_provider_id = "speech"
tts_model_id = "koder-tts"
tts_voice = "F1"
tts_language = "en"
input_sample_rate = 16000
output_sample_rate = 44100
max_utterance_seconds = 60

[providers.speech]
kind = "openai-compatible"
base_url = "http://127.0.0.1:8099/v1"
timeout = "2m"
```

Koder sends endpointed input as WAV-wrapped PCM16 to
`/audio/transcriptions`. It requests raw PCM from `/audio/speech` and forwards
even-sized chunks to Android as they arrive. The local tested provider is
`audio.cpp`, serving Qwen ASR as `koder-stt` and Supertonic as `koder-tts`.

## Security and deployment

Android installations use separate revocable bearer tokens created through the
browser UI's one-time QR binding flow. Koder stores only token digests in its
state directory, while Android protects the issued token with Android Keystore.
The legacy `KODER_VOICE_TOKEN` or `--voice-token` value is imported once as a
device registration for migration and can then be removed. Cleartext `ws://` is
suitable only on a trusted private network; use an HTTPS reverse proxy for
Internet access so Android connects with `wss://`.

The voice endpoint is `/voice/v1`; it does not expose the browser or `/debug`
protocol. The exact transport is documented in
`protocol/voice/v1/README.md`.

## Verification contract

The release gates are:

- Go unit, fuzz-seed, interoperability, handler, controller, provider, and
  artifact tests with mocked speech/model boundaries;
- `go vet` and `golangci-lint`;
- Android JVM tests for protocol, VAD endpointing, and connection behavior;
- the API 36 managed-device suite for real Activity startup, Silero inference,
  authenticated mock WebSocket audio in both directions, and lifecycle;
- opt-in Android-to-live-Koder smoke testing;
- opt-in synthesized speech acceptance: audio.cpp TTS -> Koder streaming input
  -> audio.cpp STT -> voice coordination -> streaming TTS -> audio.cpp STT.

Live tests are opt-in because they require configured services and may write to
an explicitly selected ordinary chat. Mocked tests never require a live speech
or model provider.

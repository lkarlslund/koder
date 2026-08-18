# Voice-first Android architecture

Status: proposed

This document defines the intended architecture and delivery plan for a
voice-first Android client for Koder. The client behaves like an ongoing voice
call, while Koder acts as a durable coordinator above its existing sessions and
chats. The same connection can also present short text and images.

The first implementation should live in this repository under
`clients/android`. The Android application and Koder server remain separately
built products joined by a versioned protocol under `protocol/voice`.

## Goals

- Make talking to Koder feel like a phone call, including Bluetooth headset
  routing, audio focus, mute, hang-up, and interruption behavior.
- Let a persistent voice coordinator find, resume, or create ordinary Koder
  sessions and chats on the user's behalf.
- Perform actual work in ordinary chats so existing permissions, approvals,
  transcripts, tools, and workspace boundaries remain authoritative.
- Support concise speech accompanied by short text, images, and arbitrary
  future artifacts without defining domain-specific presentation types.
- Preserve useful voice history and delegated-work provenance across calls.
- Recover cleanly from mobile network interruptions without duplicating
  committed utterances or delegated work.
- Keep microphone audio private, authenticated, and unretained by default.

## Non-goals for the first release

- Reimplementing the browser workspace on Android.
- General remote access to the browser RPC or debug API.
- Incoming calls, background wake-up, or always-listening behavior.
- Perfect full-duplex audio. The first release is turn-based with barge-in.
- A domain-specific Android UI for calendars, email, tasks, or source control.
- Offline model inference on the phone.
- Supporting clients other than the Android application, while keeping the
  protocol capable of doing so later.

## Core concepts

The design separates durable conversation, transient media, and delegated
work.

### Voice hub

The voice hub is a persistent Koder session with a dedicated workflow role. It
holds the durable transcript of final user utterances, spoken responses,
clarifications, confirmations, and references to delegated work.

The voice hub coordinates work but does not receive shell, filesystem,
browser, email, calendar, or arbitrary MCP tools directly. It receives a small
allowlist of global discovery, delegation, and response tools.

Proposed domain additions:

```go
SessionKindVoiceHub
WorkflowRoleVoice
```

A voice hub has no project root. Initially, one hub per Koder installation is
sufficient. The domain model should not prevent multiple identities or hubs in
the future.

### Voice call

A voice call is an ephemeral live connection between Android and Koder. It
owns audio negotiation, utterance state, partial transcripts, playback state,
and reconnection state. A call refers to a voice hub but is not itself a Koder
session.

Call records need only be retained long enough to support reconnection and
diagnostics. Final utterances and responses belong to the hub timeline.

### Delegation

A delegation is a durable correlation between a voice-hub utterance and an
ordinary Koder chat performing work. It records the selected target, objective,
state, public result, artifacts, and pending requests for user input or
approval.

Delegation is global and Engine-owned. Existing chat tools remain scoped to a
session and direct child chats; their ownership rules must not be weakened to
implement voice routing.

### Temporary session

A temporary session supports one-off work that does not belong in an existing
project or durable subject. It has no managed project directory and has an
explicit expiry policy. It is distinct from the current quick coding session,
which owns a managed workspace and supports promotion.

## End-to-end flow

```text
Android Core-Telecom call
  -> AudioRecord
  -> on-device VAD and pre-roll
  -> authenticated voice WebSocket
  -> streaming or endpointed STT
  -> persistent voice-hub chat
  -> global session discovery and delegation
  -> ordinary target session/chat and tools
  -> public result and artifact references
  -> concise voice-hub response
  -> streaming TTS and multimodal message
  -> AudioTrack and Android companion feed
```

Example: “Show a screenshot of the current state of that development.”

1. Android commits the final utterance after local VAD detects endpointing.
2. Koder records the transcript in the voice hub.
3. The hub searches bounded session candidates and chooses or clarifies the
   target.
4. The delegation service sends the objective to an ordinary chat.
5. That chat uses its normal tools and permissions to create the screenshot.
6. The result references an authenticated artifact.
7. The hub returns a short spoken response with text and image content parts.
8. Android starts playback and displays the image in the call feed.

## Repository layout

```text
clients/android/             Native Kotlin Android project
protocol/voice/v1/           Normative wire contract and shared fixtures
docs/voice-architecture.md   Architecture and delivery plan
internal/voice/              Call and utterance application service
internal/delegation/         Cross-session delegated job ownership
internal/artifact/           Authenticated content and representations
internal/speech/             STT and TTS service boundaries
internal/voiceapi/           Voice protocol transport adapter
```

Package names are proposed boundaries, not a requirement to create empty
packages upfront. Packages should be introduced only with the vertical slice
that needs them.

`internal/voiceapi` owns wire DTOs and translates them into calls on
`internal/voice`. Wire types must not leak into `domain`, `session`, or `chat`.
The browser RPC remains independent of the voice protocol.

## Android application

The Android application should be native Kotlin using:

- Jetpack Compose for pairing, call controls, transcript, and content display.
- Jetpack Core-Telecom for call lifecycle, audio focus, Bluetooth routing, and
  headset integration.
- Kotlin coroutines and `Flow` for call, media, and network state.
- `AudioRecord` and `AudioTrack` for microphone capture and playback.
- A foreground call/microphone service started while the activity is visible.

Do not combine Core-Telecom routing with direct
`AudioManager.setCommunicationDevice` calls. Core-Telecom should own endpoint
selection. Native C or C++ is justified only behind a Kotlin interface when a
specific codec, VAD, or echo-cancellation implementation requires it.

### Android components

```text
CallActivity / Compose UI
  -> CallViewModel
  -> CallController
       -> TelecomController
       -> AudioCapture
       -> VoiceActivityDetector
       -> AudioPlayback
       -> VoiceConnection
       -> CompanionFeedStore
```

The UI observes state and sends user intentions. It must not own sockets,
audio devices, or call lifecycle state.

### Call state

```text
idle
  -> connecting
  -> listening
  -> committing
  -> processing
  -> speaking
  -> listening

Any active state may enter reconnecting or ended.
Barge-in transitions speaking to listening after cancelling playback.
```

### Voice activity detection

VAD runs on the phone and should:

- Retain a configurable 300–500 ms pre-roll buffer.
- Start an utterance with the pre-roll when speech is detected.
- Commit after a configurable period of silence.
- Enforce a maximum utterance duration.
- Stop or duck assistant playback on barge-in.
- Continue showing an explicit listening indicator whenever the microphone is
  active.

The protocol remains compatible with server-side endpointing, but Android VAD
is authoritative for the first release.

### Companion feed

The active-call screen shows the latest short text or image without displacing
call controls. A secondary feed view shows durable multimodal messages from the
current hub. Images open into a zoomable full-screen viewer while the call
continues.

Android reports the currently focused artifact to Koder. This allows phrases
such as “describe that image” to resolve without relying solely on model
inference.

## Generic multimodal messages

The protocol must not enumerate calendar, email, task, diff, or other
domain-specific presentation kinds. A visible assistant message contains
optional spoken text and MIME-typed parts.

```json
{
  "message_id": "019...",
  "utterance_id": "019...",
  "spoken_text": "Here is the calendar entry I created.",
  "parts": [
    {
      "mime_type": "text/markdown",
      "text": "**Appointment with Steen**\n\nTomorrow at 10:00"
    },
    {
      "mime_type": "text/calendar",
      "artifact_id": "019...",
      "name": "appointment-with-steen.ics",
      "disposition": "attachment"
    }
  ]
}
```

A content part contains exactly one of inline text or an artifact reference.

```go
type ContentPart struct {
	MIMEType    string
	Text        string
	ArtifactID  id.ID
	Name        string
	AltText     string
	Disposition string
}
```

The final Go definition should follow local JSON and domain conventions. The
conceptual invariants are:

- `MIMEType` is required.
- Exactly one of `Text` and `ArtifactID` is set.
- `Disposition` is an optional `inline` or `attachment` hint.
- Unsupported MIME types do not invalidate the containing message.
- Unknown referenced content is displayed as a generic named attachment.
- Alt text is required for an inline visual artifact unless a textual part
  already provides an equivalent description.

The first Android client renders `text/plain`, a safe subset of
`text/markdown`, and `image/*`. It presents other content as an attachment that
can be opened through Android. Arbitrary HTML is not rendered in the first
release.

### Artifact representations

One logical artifact may offer several representations:

- Original machine-readable content, such as `text/calendar`.
- A server-produced `image/png` preview.
- An accessible `text/plain` description.
- A smaller image thumbnail.

The client chooses the best supported representation. The call handshake
advertises supported MIME patterns, maximum image dimensions, and supported
audio formats. Adding a new integration does not require adding a new Android
presentation type.

Interactions such as approval, input requests, progress, and errors are
protocol events rather than content-part types. Their eventual visible result
is an ordinary multimodal message.

## Artifact service

Artifacts are immutable, authenticated resources with provenance.

```go
type Artifact struct {
	ID         id.ID
	SessionID  id.ID
	ChatID     id.ID
	ToolCallID string
	MIMEType   string
	Name       string
	AltText    string
	Size       int64
	CreatedAt  time.Time
	ExpiresAt  *time.Time
}
```

The model never invents artifact URLs. Tools and delegation results return
artifact IDs, and the voice API translates authorized IDs into download URLs.
Binary artifacts are transferred over authenticated HTTPS rather than base64
inside WebSocket JSON. Thumbnail and full-size representations may have
separate immutable URLs.

The existing attachment and offered-file implementations should be reused or
adapted where their ownership and lifetime match. A public offered-file token
must not become the authorization model for the mobile artifact API.

## Session discovery and routing

The hub receives a bounded set of candidate sessions rather than complete
transcripts. Initial retrieval considers:

- Session title and user-defined aliases.
- Project-root basename.
- Chat titles.
- Recent user-visible summaries and last messages.
- Recency and previous voice-hub selections.
- User-defined tags and available integration capabilities.

The initial search can be lexical and recency-weighted. Semantic indexing can
be added behind the same search boundary after behavior and privacy have been
evaluated.

Routing policy:

- Resume an existing target when one candidate is a strong match.
- Ask a concise clarification when several plausible targets remain.
- Use a temporary session for isolated one-off work.
- Create a durable session for a clearly durable new subject, according to
  configured policy or after confirmation.
- Never silently cross a sensitive permission boundary merely because a
  session looks relevant.

## Delegation service

The voice role receives a dedicated global service instead of unrestricted
access to the session registry.

```go
type Service interface {
	SearchSessions(context.Context, Query) ([]Candidate, error)
	Dispatch(context.Context, Request) (Job, error)
	Reply(context.Context, id.ID, string) error
	Cancel(context.Context, id.ID) error
	Subscribe(context.Context, id.ID) (<-chan Event, error)
}
```

The concrete API may change as tests expose the right ownership boundaries.
The important behavior is event-driven completion rather than model polling.

A delegation stores:

- Voice hub, call, utterance, target session, and target chat IDs.
- Objective and retention policy.
- Running, waiting-input, waiting-approval, completed, failed, or cancelled
  state.
- Public result text and artifact IDs.
- Pending question or approval reference.
- Created, updated, and completed timestamps.

Worker requests for input or approval are forwarded to the active voice call.
If no call is active, they remain durable and appear when the client reconnects.
The user's response is correlated with the job before it is sent to the worker.

Long-running work may outlive a call. The hub gives a short acknowledgement,
the job continues under ordinary chat ownership, and a later call or Android
notification can surface completion.

## Voice role

The voice role should initially expose only tools equivalent to:

- Search and inspect bounded session candidates.
- Dispatch work to a selected or newly created session.
- Reply to or cancel delegated work.
- Present a generic multimodal response.

It should not expose raw storage mutation or operational tools. The delegated
chat executes work with the target session's normal model, tools, permissions,
and approval rules.

Voice responses should:

- Use short, speakable sentences without Markdown or raw URLs in speech.
- Put useful detail in content parts rather than reading it aloud.
- Acknowledge long-running work without claiming it is complete.
- Read back consequential external actions before requesting confirmation.
- Preserve error details visually while speaking a concise explanation.

## Speech services

Speech provider boundaries should not require batch providers to pretend they
stream.

```go
type Transcriber interface {
	Transcribe(context.Context, Audio) (Transcript, error)
}

type StreamingTranscriber interface {
	StartTranscription(context.Context, StreamConfig) (TranscriptionStream, error)
}

type Synthesizer interface {
	OpenSpeech(context.Context, SpeechRequest) (SpeechStream, error)
}
```

The first STT implementation may buffer a VAD-delimited utterance and call a
fast transcription endpoint on commit. The wire protocol includes partial
transcript events from the beginning so streaming STT can be introduced without
a protocol redesign.

The current browser TTS path buffers the complete provider response and returns
base64. Refactor the provider boundary so voice can read the response body as a
cancelable stream while the browser path remains able to consume the entire
response.

Speech and chat model selection belong in a voice-specific configuration
section rather than inheriting browser UI TTS settings implicitly.

## Voice protocol

The voice client connects to a dedicated versioned endpoint:

```text
/api/voice/v1/ws
```

The browser WebSocket and voice WebSocket remain separate. Control events use
JSON text frames; media uses binary frames. The normative schemas and shared
fixtures live under `protocol/voice/v1`.

Every control envelope contains:

```json
{
  "version": 1,
  "type": "utterance.commit",
  "event_id": "019...",
  "call_id": "019...",
  "utterance_id": "019...",
  "sequence": 42,
  "payload": {}
}
```

Required protocol properties:

- Major-version negotiation during `hello`.
- Idempotent committed utterances and confirmation replies.
- Monotonic per-direction sequence numbers.
- Stable event IDs for feed replay and acknowledgement.
- Explicit maximum control-frame and media-frame sizes.
- Unknown additive fields are ignored.
- Unknown event types produce a protocol error without crashing the call.
- Server and client advertise supported audio formats and content MIME types.

### Client events

```text
hello
call.start
call.resume
utterance.start
utterance.commit
utterance.cancel
playback.interrupt
presentation.focus
input.reply
confirmation.reply
feed.ack
call.end
```

### Server events

```text
hello.accepted
call.ready
stt.partial
stt.final
assistant.status
delegation.updated
input.required
confirmation.required
message.appended
tts.start
tts.end
call.error
call.ended
```

Audio frames flow between `utterance.start` and commit or cancel, and between
`tts.start` and `tts.end`. The binary header identifies direction, call,
utterance or playback stream, sequence, timestamp, and flags. Its exact layout
belongs in the normative protocol specification.

### Initial audio profile

Prefer a simple interoperable first profile:

- Microphone: mono PCM16 at 16 kHz.
- TTS: mono PCM16 at the provider's declared sample rate, preferably 24 kHz.
- Approximately 20 ms per media frame.
- Explicit sample rate and channel count during stream start.

Android and its communication route may resample for the active device. Opus
can be negotiated later without changing call semantics.

### Reconnection

On reconnect, the client supplies its call ID, last received server sequence,
and last acknowledged feed event. The server replays durable control and feed
events, but does not replay stale live audio. Interrupted TTS restarts from a
new playback stream when appropriate.

An utterance commit may be retried with the same utterance and event IDs. It
must never enqueue a second hub prompt or delegation.

## Barge-in and cancellation

When local VAD detects speech during playback:

1. Android stops or ducks `AudioTrack` immediately.
2. Android sends `playback.interrupt` and starts the new utterance with pre-roll.
3. Koder cancels only the active TTS stream by default.
4. Existing delegated work continues unless the user explicitly asks to stop
   it.
5. The hub receives the new utterance and decides whether it supersedes the
   pending response.

This distinction prevents “wait” or an accidental interruption from killing a
long-running worker operation.

## Authentication and network exposure

Mobile support changes Koder's trust boundary. Binding the existing web and
debug server to a LAN is not an acceptable authentication design.

The voice endpoint requires:

- TLS.
- One-time pairing initiated from the trusted browser UI.
- A per-device revocable credential stored in Android secure storage.
- A narrow voice API scope that cannot call browser or debug RPC methods.
- Connection, frame-size, request-rate, and artifact-download limits.
- Device naming, last-used information, and revocation in Koder settings.

A QR code may encode an address, server identity, one-time pairing secret, and
protocol version. The pairing secret is exchanged for a durable device
credential and cannot be reused.

For the earliest personal deployment, a private WireGuard or Tailscale network
may provide transport reachability, but application authentication remains
required before the feature is considered complete.

## Permissions and confirmations

The voice hub cannot grant itself or a worker new permissions. Delegated work
uses the target session's existing permission profile and rules.

External side effects such as sending email, changing calendar entries,
purchases, deletion, or publishing require an explicit confirmation according
to policy. Confirmation is a first-class control event with a concise spoken
read-back and a visible description. High-risk actions may require a tap rather
than accepting voice-only confirmation.

Approval and confirmation records retain the device, call, utterance, target
chat, requested action, and result for auditability.

## Privacy and diagnostics

- Raw microphone audio is not persisted by default.
- Final transcripts and assistant messages are persisted in the voice hub.
- Partial transcripts are transient unless diagnostic recording is explicitly
  enabled.
- Authentication secrets and raw audio never enter ordinary debug traces.
- Artifact access is authenticated and logged without logging content.
- Diagnostics record latency, byte counts, codec, state transitions, and
  cancellation reasons.
- Any optional audio retention has an explicit setting, bounded duration, and
  visible indicator.

Useful latency measurements include speech-end to final transcript, transcript
to first hub action, delegation duration, text to first TTS byte, and network
jitter-buffer depth.

## Compatibility and contract testing

The Android and server versions are independent. The handshake negotiates a
protocol major version and additive capabilities. A server may support more
than one major version during migrations.

Shared fixtures under `protocol/voice/v1/testdata` cover:

- Every control-event shape.
- Unknown additive fields.
- Unsupported event and protocol errors.
- Duplicate utterance commits.
- Feed replay and acknowledgement.
- MIME parts with known and unknown types.
- Malformed and oversized input.

Go and Kotlin test suites decode the same fixtures. End-to-end tests start a
real Koder voice endpoint with fake STT, chat, delegation, artifact, and TTS
services. Android instrumented tests cover call lifecycle and audio routing;
unit tests cover state machines and protocol behavior.

### Required test seams

Normal unit and integration tests must be deterministic, run without external
credentials, and make no network calls to live model or speech providers. The
implementation therefore requires explicit seams for:

- Microphone input and playback output.
- VAD decisions and time.
- Voice WebSocket transport.
- STT batch and streaming implementations.
- Voice-hub model completion.
- Session search and delegation.
- Artifact storage and download authorization.
- TTS stream creation.
- Android Telecom call and endpoint state.

Production implementations plug into these consumer-owned interfaces. Tests
use small fakes with scripted events rather than broad mocks of internal
implementation details.

### Server test layers

Server tests are required at four layers:

1. Protocol unit tests decode shared fixtures, validate envelopes and media
   headers, enforce size limits, and exercise unknown or additive fields.
2. Call-state tests use fake time and scripted media/control events to cover
   endpointing, duplicate commits, cancellation, barge-in, reconnection, and
   feed acknowledgement.
3. Pipeline integration tests run the real voice API and voice-hub orchestration
   with deterministic fake services. A fake transcriber maps fixture PCM to a
   known transcript, a fake hub model issues scripted tool calls, and a fake
   synthesizer emits known audio chunks.
4. Provider-adapter tests use `httptest.Server` implementations of the relevant
   OpenAI-compatible endpoints. They verify request encoding, streaming,
   cancellation, malformed responses, limits, and error handling without a
   live provider.

A representative pipeline test should cover:

```text
fixture PCM
  -> committed utterance
  -> fake STT final transcript
  -> real voice-hub turn
  -> fake or in-memory delegation
  -> generic multimodal response
  -> fake TTS chunks
  -> protocol audio and message events
```

This test verifies component interoperability while leaving speech quality and
provider availability outside the unit-test contract.

### Android test layers

Android local unit tests are required for:

- Protocol fixture compatibility with Go.
- Call and reconnection state machines.
- Sequence acknowledgement and duplicate suppression.
- VAD-to-utterance framing using fixture PCM and a fake clock.
- Playback queueing, interruption, and stale-stream rejection.
- MIME-part selection and unknown-content fallback.

Instrumented tests cover the Android components that cannot be represented
faithfully on the host JVM, especially lifecycle, permissions, audio focus, and
Core-Telecom integration. Audio capture and playback wrappers remain replaceable
so most behavior is testable without real microphone or Bluetooth hardware.

Hardware smoke tests on at least one phone and one Bluetooth headset are
required before a voice release, but they supplement rather than replace the
automated suite.

### Optional live-provider tests

Live STT, TTS, and model checks are opt-in diagnostics, excluded from the
default test suite and CI pass criteria. They require explicit configuration,
use short fixtures, and report provider latency and compatibility. A live
provider failure must not make the deterministic interoperability suite flaky.

## Delivery plan

Each phase should produce a usable vertical slice and a small verified commit.

### Phase 0: normative protocol and seams

- Add `protocol/voice/v1` schemas, examples, and shared fixtures.
- Define consumer-owned speech, artifact, and delegation interfaces.
- Add protocol decoding, validation, and compatibility tests.
- Add deterministic fake STT, TTS, hub-model, and transport implementations for
  interoperability tests.
- Decide the authentication handshake before accepting non-loopback traffic.

Acceptance criteria:

- Go tests validate all fixtures and reject malformed or oversized envelopes.
- Go and Kotlin tests consume the same normative protocol fixtures.
- A provider-free test exercises audio input through transcript, hub response,
  and emitted TTS frames.
- The protocol clearly specifies idempotency and reconnection behavior.
- No voice package depends on `internal/webui` DTOs.

### Phase 1: text-only voice hub and delegation

- Add voice session kind and workflow role.
- Create or load the installation's persistent hub.
- Implement bounded session search and cross-session delegation.
- Forward worker completion, input requests, and approvals to the hub.
- Add generic multimodal response persistence without Android audio.

Acceptance criteria:

- A text integration test can resume a named existing session.
- A one-off request can use a temporary session.
- Worker questions and results return to the originating hub utterance.
- The hub cannot execute ordinary operational tools directly.

### Phase 2: Android call and endpointed speech

- Create the Kotlin Android project under `clients/android`.
- Implement pairing, Core-Telecom call lifecycle, Bluetooth routing, and call
  controls.
- Add local VAD, PCM capture, the voice WebSocket, and reconnection.
- Implement fast batch STT after utterance commit.
- Stream or chunk TTS PCM to Android playback.

Acceptance criteria:

- A user can start a call from a visible activity and converse for multiple
  turns.
- A connected Bluetooth communication device is used through Core-Telecom.
- Disconnecting and reconnecting does not duplicate a committed utterance.
- Microphone audio is not retained by default.

### Phase 3: companion feed and artifacts

- Add authenticated artifact download and representation negotiation.
- Render short plain/Markdown text and images in Android.
- Add feed replay, acknowledgement, focus reporting, and image zoom.
- Connect screenshots and existing Koder-produced images to artifact IDs.

Acceptance criteria:

- “Show me the current screenshot” displays an authenticated image while a
  concise response is spoken.
- Unknown MIME content appears as a generic attachment.
- Ending and reopening the app restores the durable message feed.

### Phase 4: conversational polish

- Implement barge-in with TTS cancellation.
- Add partial transcripts where supported.
- Add true streaming STT and lower-latency TTS provider adapters.
- Improve endpointing, jitter buffering, audio focus changes, and route changes.

Acceptance criteria:

- Speaking during playback stops audible output promptly without cancelling
  unrelated delegated work.
- Bluetooth connect and disconnect events do not terminate the Koder call.
- Provider cancellation releases response bodies and goroutines.

### Phase 5: durable background work

- Allow delegated jobs to outlive a call.
- Surface completion on the next call and through an Android notification.
- Add temporary-session expiry and cleanup.
- Evaluate semantic session retrieval after lexical behavior is measured.

Acceptance criteria:

- A call can end while work continues, with exactly one later completion.
- Temporary session cleanup cannot delete a promoted or durable session.

## Initial implementation defaults

Unless testing disproves them:

- Keep Android and Koder in this repository as separate builds.
- Use Kotlin, Compose, coroutines, and Core-Telecom.
- Use local VAD with fast batch STT first.
- Use JSON control frames and binary PCM media frames.
- Use MIME-typed message parts and authenticated artifact references.
- Keep voice coordination persistent and calls ephemeral.
- Keep delegated work inside ordinary permission-scoped chats.
- Require explicit application authentication even on a private network.

## Open decisions

The following should be resolved before or during the indicated phase:

- Whether there is exactly one voice hub or one per configured identity.
- Whether a temporary session is visible in the browser UI before promotion.
- The initial Android minimum API level supported by the chosen Core-Telecom
  release.
- The VAD implementation and tuning strategy.
- The exact binary audio-frame header.
- Certificate provisioning and server discovery outside private-overlay
  networks.
- Credential storage, rotation, and device-revocation format.
- Which consequential actions require tap-only confirmation.
- Artifact retention defaults and thumbnail generation.
- Whether the first TTS adapter requires raw PCM or accepts another streamable
  provider format.
- How completion notifications avoid revealing sensitive content on a locked
  phone.

These decisions should remain explicit configuration or protocol capabilities
where practical rather than assumptions embedded in Android UI code.

## Android platform references

- [Android Telecom framework overview](https://developer.android.com/develop/connectivity/telecom)
- [Jetpack Core-Telecom](https://developer.android.com/develop/connectivity/telecom/voip-app/telecom)
- [Foreground service types](https://developer.android.com/develop/background-work/services/fgs/service-types)
- [Foreground service background-start restrictions](https://developer.android.com/develop/background-work/services/fgs/restrictions-bg-start)
- [Android Kotlin-first guidance](https://developer.android.com/kotlin/first)
- [Jetpack Compose](https://developer.android.com/compose)

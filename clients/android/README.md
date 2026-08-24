# Koder Android voice client

This directory is the native Kotlin voice-conversation client. It is a separate
Gradle build in the Koder repository and talks only to `voice.v1`.

## Prerequisites

- JDK 21
- Android SDK platform 36
- Android SDK Build Tools 37.0.0
- Android Emulator plus Google x86 API 28 and x86_64 API 36 images for managed tests
- KVM acceleration on Linux

On the Arch Linux development host, the SDK is `/opt/android-sdk` and JDK 21 is
`/usr/lib/jvm/java-21-openjdk`.

The build downloads the Silero VAD ONNX model from a pinned commit into the
build directory and checks its SHA-256 before packaging. It is not stored in
Git.

## Build and test

From this directory:

```sh
export JAVA_HOME=/usr/lib/jvm/java-21-openjdk
export ANDROID_HOME=/opt/android-sdk
./gradlew testDebugUnitTest lintDebug assembleDebug
./gradlew voiceApi36DebugAndroidTest
```

The JVM suite covers protocol interoperability and deterministic VAD
endpointing without live speech services. The managed-device suite boots the
real Activity, checks setup and conversation-list states, runs real Silero
inference, and tests authenticated mock HTTP/WebSocket interoperability and
negotiated Opus/PCM audio in both directions.

Before a release or compatibility-sensitive change, run the focused matrix on
an Android 9 small phone, an Android 16 phone, and an Android 16 tablet:

```sh
./gradlew voiceCompatibilityGroupDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.class=com.lkarlslund.koder.DeviceMatrixInstrumentedTest
```

The matrix renders light and dark configurations, rotates the UI, checks the
first-run microphone permission boundary, and exercises the supported minimum
API across phone and tablet layouts. The ordinary API 36 suite remains the
fast, comprehensive interoperability run, including simulated reconnects and
audio-route changes. This emulator matrix is intentionally local/release-gated
rather than a per-commit GitHub job, to avoid consuming hosted build minutes.

An opt-in test can authenticate to a live Koder without modifying a chat:

```sh
./gradlew voiceApi36DebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.class=com.lkarlslund.koder.voice.VoiceLiveInstrumentedTest \
  -Pandroid.testInstrumentationRunnerArguments.voiceLiveServer=http://10.0.2.2:7979 \
  -Pandroid.testInstrumentationRunnerArguments.voiceLiveToken=<token>
```

To also delegate a real turn, add an utterance and optionally an exact target:

```sh
  '-Pandroid.testInstrumentationRunnerArguments.voiceLiveUtterance=Reply briefly to this integration test.' \
  -Pandroid.testInstrumentationRunnerArguments.voiceLiveSessionId=<session-id>
```

The live test is skipped unless `voiceLiveServer` is supplied.
Add `-Pandroid.testInstrumentationRunnerArguments.voiceLiveRequireAppUpdate=true`
to require and validate the embedded update advertisement as part of the smoke
test.

## Install on a phone

```sh
../../scripts/prepare-embedded-android local
adb install -r ../../internal/androidupdate/bundle/koder.apk
```

Koder and the app share an automatic `rNNNN` build identity derived from the
repository's total commit count. Local builds append `-local`, plus `-dirty`
when they include tracked changes. The exact commit remains available as
diagnostic metadata. The internal Android version code is tracked separately
so updates stay monotonic; no application version needs to be chosen by hand.

This uses the ignored, stable local-development signing key and the
`com.lkarlslund.koder.dev` app ID. Back up `.signing/local-development.jks`
and its properties file: a lost signing key cannot update an installed app.
Embedded local and official APKs target 64-bit ARM Android phones and compress
native libraries to keep the APK—and the Koder binary containing it—small.
Ordinary Gradle builds remain unfiltered so the x86_64 managed emulator keeps
working. Set `KODER_ANDROID_TARGET_ABIS` only when preparing an embedded APK for
a different device architecture.
After this one-time sideload, newer Koder binaries advertise their embedded APK
in the app. Tap the update offer; the app verifies its ID, version, signer,
size, and SHA-256 before Android asks for install confirmation. Android may ask
once for permission to install apps from Koder.

Official GitHub builds use `com.lkarlslund.koder`, so both channels can coexist.
They cannot update each other. Maintainers create the separate ignored release
key and provision GitHub Actions with:

```sh
../../scripts/configure-android-release-signing --github
```

Back up `.signing/github-release.jks` and its properties separately. The
rolling-release workflow builds the official APK once, embeds the same signed
bytes and metadata into both Linux binaries, and does not publish a standalone
APK.

Start Koder on the trusted LAN with its speech provider configured. On first
launch, Koder Voice offers to scan a binding QR. In Koder's web UI, open the
phone dialog and choose **Bind this phone**; scanning that one-time QR supplies
the server address and creates a private per-device token automatically. The
scanner runs through Google Play services, does not require camera permission,
and adds minimal weight to the APK.

Manual connection remains an advanced fallback for servers configured with a
reusable voice token:

```sh
openssl rand -hex 32
KODER_VOICE_TOKEN='<paste-the-generated-token>' \
  koder serve --web-bind 0.0.0.0:7979 --nobrowser
```

Enter the computer's LAN URL, for example `http://192.168.1.20:7979`, and the
same token under **Set up manually**. Do not use `localhost` on a physical phone. Cleartext
HTTP is only appropriate on a trusted private network; use an HTTPS reverse
proxy and enter its `https://` URL for `wss://` transport elsewhere.

The manual access token is optional when the server has no voice token
configured. QR-issued and manual tokens are encrypted using Android Keystore.
The app then connects without activating audio and presents existing
voice conversations plus a New Conversation action. Conversations show their
last-used time in newest-first order; pull down on the list to refresh it.
Selecting a conversation opens its dedicated voice/text screen. On the first conversation, grant
microphone, notification, and nearby-device permissions. Android Core Telecom
then owns earpiece, speaker, wired headset, hold, and Bluetooth routing as an
internal audio implementation detail.

The persistent user concept is a conversation, not a phone call. Returning to
the home screen pauses the live voice connection without ending or deleting its
conversation. Speech and typed input share the same server path.

The active voice screen has a camera action at the lower right, while the
transcript composer has a `+` action. Either can take a new photo or choose one
from the phone. Koder normalizes and uploads the image as a pending attachment:
the next spoken or typed question consumes it as one model turn, while **Send
photo now** sends an image-only turn. A pending preview can be removed before
it is sent.

Settings also contains the phone's speech-language preference. Leave it on
Automatic for unrestricted detection, choose one language for the strongest
hint, or choose several languages you actually speak to bias detection away
from unrelated languages. Changes apply on the next conversation connection.

Android runs on-device Silero VAD over local PCM. Voice Adjustments lets the
user independently select PCM or 64, 40, 24, or 16 kbit/s Opus for microphone
upload and speech playback. Koder resamples output such as 44.1 kHz TTS PCM to
an Opus-supported rate while streaming; PCM remains available as the
uncompressed diagnostic and maximum-fidelity choice. The protocol falls back
safely with older servers and clients. A bundled pure-Java codec keeps this
path compatible with Android 9 without native ABI libraries. The app also
displays text, images, and generic MIME
attachments. Koder—not Android—calls the configured remote STT and TTS
endpoints. While voice is active, ordinary transcript text stays hidden. A
deliberate visual response automatically opens a separate presentation panel;
closing it returns to the voice-active screen without exposing the transcript.
Generic authenticated files are downloaded to a cache-only content URI before
opening in another Android app.

Opening the transcript starts with the newest five conversational turns. The
view follows new messages only while the user is already near the bottom. When
the user scrolls to the top, Android loads and prepends five older turns while
preserving the current reading position.

See `../../docs/voice-architecture.md` for the as-built design and
`../../protocol/voice/v1/README.md` for the wire contract.

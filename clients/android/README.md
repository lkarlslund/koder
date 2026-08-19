# Koder Android voice client

This directory is the native Kotlin voice-conversation client. It is a separate
Gradle build in the Koder repository and talks only to `voice.v1`.

## Prerequisites

- JDK 21
- Android SDK platform 36
- Android SDK Build Tools 37.0.0
- Android Emulator plus Google x86_64 API 36 image for managed tests
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
inference, and tests authenticated mock HTTP/WebSocket interoperability and PCM
in both directions.

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

Start Koder on the trusted LAN with its speech provider configured:

```sh
openssl rand -hex 32
KODER_VOICE_TOKEN='<paste-the-generated-token>' \
  koder serve --web-bind 0.0.0.0:7979 --nobrowser
```

Enter the computer's LAN URL, for example `http://192.168.1.20:7979`, and the
same token in the app. Do not use `localhost` on a physical phone. Cleartext
HTTP is only appropriate on a trusted private network; use an HTTPS reverse
proxy and enter its `https://` URL for `wss://` transport elsewhere.

The welcome screen explains both fields; the access token is optional when the
server has no voice token configured. The token is encrypted using Android
Keystore. The app then connects without activating audio and presents existing
voice conversations plus a New Conversation action. Conversations show their
last-used time in newest-first order; pull down on the list to refresh it.
Selecting a conversation opens its dedicated voice/text screen. On the first conversation, grant
microphone, notification, and nearby-device permissions. Android Core Telecom
then owns earpiece, speaker, wired headset, hold, and Bluetooth routing as an
internal audio implementation detail.

The persistent user concept is a conversation, not a phone call. Returning to
the home screen pauses the live voice connection without ending or deleting its
conversation. Speech and typed input share the same server path.

Android runs on-device Silero VAD, streams microphone PCM to Koder, plays
Koder's streamed PCM reply, and displays text, images, and generic MIME
attachments. Koder—not Android—calls the configured remote STT and TTS
endpoints. Generic authenticated files are downloaded to a cache-only content
URI before opening in another Android app.

See `../../docs/voice-architecture.md` for the as-built design and
`../../protocol/voice/v1/README.md` for the wire contract.

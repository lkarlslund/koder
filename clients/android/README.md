# Koder Android voice client

This directory is the native Kotlin, phone-style voice client. It is a separate
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
real Activity, runs real Silero inference, and tests authenticated mock
WebSocket PCM in both directions.

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

## Install on a phone

```sh
./gradlew assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

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

The token and address are encrypted using Android Keystore. On first call,
grant microphone, notification, and nearby-device permissions. Android Core
Telecom then owns earpiece, speaker, wired headset, hold, and Bluetooth routing.

The first selector chooses the durable voice chat whose transcript is being
continued. Create additional voice chats in Koder's Web UI. The second selector
chooses an ordinary work session or returns to automatic semantic routing.
Speech and typed input share the same server path.

Android runs on-device Silero VAD, streams microphone PCM to Koder, plays
Koder's streamed PCM reply, and displays text, images, and generic MIME
attachments. Koder—not Android—calls the configured remote STT and TTS
endpoints. Generic authenticated files are downloaded to a cache-only content
URI before opening in another Android app.

See `../../docs/voice-architecture.md` for the as-built design and
`../../protocol/voice/v1/README.md` for the wire contract.

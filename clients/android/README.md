# Koder Android client

This directory contains the native Kotlin voice-first Android client. It is a
separate Gradle build inside the Koder repository.

## Prerequisites

- JDK 21
- Android SDK platform 36
- Android SDK Build Tools 37.0.0
- Android Emulator and a Google APIs x86_64 API 36 system image
- KVM acceleration on Linux

On the current Arch Linux development setup, the SDK is installed under
`/opt/android-sdk`.

## Checks

```sh
JAVA_HOME=/usr/lib/jvm/java-21-openjdk ANDROID_HOME=/opt/android-sdk ./gradlew test lintDebug
JAVA_HOME=/usr/lib/jvm/java-21-openjdk ANDROID_HOME=/opt/android-sdk ./gradlew voiceApi36DebugAndroidTest
```

The managed-device suite includes Silero inference and a mocked `voice.v1`
WebSocket interoperability test. An opt-in test can exercise an explicitly
selected chat on a developer-owned Koder server:

```sh
./gradlew voiceApi36DebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.class=com.lkarlslund.koder.voice.VoiceLiveInstrumentedTest \
  -Pandroid.testInstrumentationRunnerArguments.voiceLiveServer=http://10.0.2.2:7979 \
  -Pandroid.testInstrumentationRunnerArguments.voiceLiveSessionId=<session-id> \
  '-Pandroid.testInstrumentationRunnerArguments.voiceLiveUtterance=Reply briefly to this voice integration test.'
```

The live test is skipped unless `voiceLiveServer` is supplied. It is not a CI
test because it deliberately writes to the selected chat and uses that chat's
configured model.

## Install on a phone

Build and install the debug APK:

```sh
./gradlew assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

Start Koder on a trusted LAN with a voice token, then enter the computer's LAN
address and the same token in the app:

```sh
KODER_VOICE_TOKEN='replace-with-a-long-random-token' \
  koder serve --web-bind 0.0.0.0:7979 --nobrowser
```

For example, use `http://192.168.1.20:7979`, not `localhost`, on a physical
phone. Keep this cleartext development endpoint on a trusted network. The app
requests microphone, notification, and nearby-device permissions when starting
the first call. Android Core Telecom owns earpiece, speaker, wired-headset, and
Bluetooth call routing.

The call screen supports speech and a typed composer, lists ordinary Koder
sessions, speaks concise replies through device TTS, and renders MIME-typed
text, images, and generic attachment fallbacks. The initial usable path relies
on Android's speech recognizer for transcription and endpointing. The packaged
Silero pipeline is the tested foundation for the subsequent raw-audio/STT path.

The build downloads a pinned Silero VAD ONNX model into the module build
directory and verifies its SHA-256 digest before packaging it. The model is not
stored in Git.

Silero VAD inference and its separate endpointing state machine accept
512-sample, mono PCM16 frames at 16 kHz through `VadEndpointPipeline`.

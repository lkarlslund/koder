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

The build downloads a pinned Silero VAD ONNX model into the module build
directory and verifies its SHA-256 digest before packaging it. The model is not
stored in Git.

The first implementation slice provides stateful Silero VAD inference and a
separate endpointing state machine. Audio capture and call integration will feed
512-sample, mono PCM16 frames at 16 kHz into `VadEndpointPipeline`.

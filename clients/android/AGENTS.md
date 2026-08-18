# Android Client Instructions

- Run Gradle commands from `clients/android` with the checked-in wrapper.
- Run Gradle with JDK 21. On Arch Linux, set
  `JAVA_HOME=/usr/lib/jvm/java-21-openjdk`.
- Keep audio capture, VAD inference, endpointing, transport, and playback behind
  separate interfaces so local tests do not require Android hardware.
- Unit tests must not call live model or speech services.
- Use the `voiceApi36DebugAndroidTest` managed-device task for emulator tests.

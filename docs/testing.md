# Testing Koder and Koder Voice

Koder uses change-based verification tiers. Run the smallest tier that can
realistically catch regressions from the change, then move up when the change
crosses a package, process, protocol, device, or release boundary. More tests
are not automatically stronger verification if they do not exercise the
changed behavior.

All Go commands must run from the real repository root. Android commands run
through `clients/android/gradlew` or from `clients/android` with `./gradlew`.

## Verification tiers

### T0: static checks

Use for documentation, comments, research, and metadata that cannot affect a
build or runtime behavior.

- Inspect the diff and run `git diff --check`.
- Validate a specific document format or generated reference when applicable.
- Do not compile Koder or start an emulator solely for prose changes.

### T1: focused tests

Use for a local behavior change with a narrow ownership boundary.

- Go: run the changed package and the exact new or modified test while
  iterating. Before committing, run the complete changed package.
- Android pure Kotlin logic: run the relevant JVM test class.
- Web assets: run the owning Go package tests because the web UI is embedded
  and exercised through `internal/webui`.

Examples:

```sh
go test ./internal/voice
go test -run '^TestLeaseReconnect$' ./internal/voice

clients/android/gradlew -p clients/android \
  :app:testDebugUnitTest \
  --tests com.lkarlslund.koder.voice.VoiceProtocolTest
```

### T2: component verification

Use when a change affects several files in one component, shared component
state, generated assets, or the component's build output.

- Koder Go component: test all affected packages and direct consumers; run
  `go vet` for those packages when Go APIs, concurrency, or resource ownership
  changed.
- Browser UI: test `internal/webui` plus affected API/controller packages.
- Android app: run JVM tests, lint, and assemble the affected build type.
- Audio/VAD logic that is pure Kotlin should remain in JVM tests; native model
  loading and Android audio APIs require T3.

Typical Android command:

```sh
clients/android/gradlew -p clients/android \
  testDebugUnitTest lintDebug assembleDebug
```

### T3: boundary and end-to-end verification

Use when behavior crosses a protocol, process, persistence, phone API, audio
stack, or user-visible workflow boundary. T3 adds focused integration coverage
to T2; it does not mean running every emulator test.

- Voice WebSocket or JSON changes: test the owning Go voice/API packages and
  Android protocol/client JVM tests.
- Interoperability changes: use mocks at the network/audio boundary for normal
  tests, then run one focused managed-device flow covering the changed path.
- UI interaction, lifecycle, permissions, Telecom, Bluetooth routing, VAD model
  loading, update installation, and interruption require the relevant Android
  instrumentation class or method.
- Persistent session/chat changes should cover storage plus the API or UI path
  that consumes it.
- Deploy to the local port 7979 service only when live-server behavior or the
  embedded APK needs validation. Never touch the separate 17979 instance.

Examples:

```sh
go test ./internal/voice ./internal/voiceapi ./internal/phonedevice

clients/android/gradlew -p clients/android \
  voiceApi36DebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.class=\
com.lkarlslund.koder.voice.VoiceConnectionInstrumentedTest
```

Prefer a class or single test selector during development. Use the three-device
compatibility group only for layout, API-level, or device-compatibility risks.

### T4: full release and nightly verification

Use for releases, dependency/toolchain upgrades, broad refactors, shared
storage/runtime changes, and scheduled health checks.

- Run all Go tests under `./cmd/...` and `./internal/...`.
- Run Go race tests for concurrency-sensitive packages. A scheduled job may
  run the wider race suite.
- Run `go vet`, repository lint, and vulnerability checks according to release
  policy.
- Run all Android JVM tests and lint.
- Build the signed build type used by the release.
- Run the complete Android managed-device compatibility group when the change
  affects device behavior; otherwise a daily/nightly job owns the full matrix.
- Exercise the embedded APK metadata/download/update path for a Koder release.

Baseline commands:

```sh
go test ./cmd/... ./internal/...
go vet ./cmd/... ./internal/...
scripts/lint

clients/android/gradlew -p clients/android testDebugUnitTest lintDebug
clients/android/gradlew -p clients/android voiceCompatibilityGroupDebugAndroidTest
```

`govulncheck` and broad `-race` runs are valuable scheduled/release gates, but
they are not required after every isolated UI or documentation edit.

## Change selection guide

| Change | Minimum tier | Required focus |
| --- | --- | --- |
| Documentation or research | T0 | Diff and document validity |
| One Go package's internal behavior | T1 | Changed package |
| Shared Go API or browser API | T2 | Owner and direct consumers |
| Android pure logic or rendering math | T1 | Relevant JVM tests |
| Android screen/layout behavior | T2 | JVM tests, lint, APK assembly |
| Android lifecycle or interaction | T3 | Focused managed-device test |
| Voice protocol frame/schema | T3 | Go and Android protocol sides |
| STT/TTS remote adapter | T3 | Mock streaming boundary and focused flow |
| VAD model/audio routing/Telecom/Bluetooth | T3 | Real Android instrumentation |
| Signing, embedded APK, or updater | T3 | Build, metadata, download/update path |
| Dependency/toolchain or broad runtime change | T4 | Full relevant suites |
| Release/nightly health check | T4 | Full policy gates |

When several rows apply, use the highest tier and combine their required focus.

## Test design and runtime rules

- Unit tests must be deterministic, offline, and fast. A unit test that waits
  on a long timeout usually has a synchronization or cleanup defect.
- Close servers, clients, sessions, goroutines, files, and databases explicitly
  with `t.Cleanup` or `defer`; do not make suite runtime depend on timeout-based
  teardown.
- Prefer fake clocks, channels, latches, and in-process mock servers over
  sleeps. Give waits a short failure deadline and a useful error message.
- Keep remote STT/TTS and model providers mocked in routine tests. Live service
  checks are explicit opt-in diagnostics, not commit gates.
- Put protocol behavior in transport-independent tests where possible, then
  keep a smaller number of interoperability and emulator tests for wiring.
- A focused emulator failure caused by provisioning or system UI must be
  reported; do not claim E2E success based only on JVM tests.
- Do not rerun unrelated expensive suites to hide a flaky failure. Diagnose the
  flake, isolate its owner, and fix or quarantine it with an explicit issue.
- Record skipped applicable checks in the handoff, including why they were
  skipped.

## Commit and deployment policy

Each logical implementation step must pass its applicable tier before it is
committed and pushed. Documentation-only commits use T0. A later code step gets
its own verification and commit.

Deployment is not a substitute for tests. For Android development, first pass
the selected tier, then build Koder so the updated APK is embedded, deploy only
the approved port 7979 service, and verify its health and update metadata.

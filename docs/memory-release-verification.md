# Memory first-production verification

This record captures the release gate completed on 2026-08-23 for the first production
Memory implementation. It complements the reproducible tests and operational runbooks;
it is not a substitute for rerunning the applicable tier after future changes.

## Automated gates

- `go test ./cmd/... ./internal/...` passed, including the real Chromium Memory
  explorer lifecycle and graph-limit tests.
- `go vet ./cmd/... ./internal/...` passed.
- `GOTOOLCHAIN=go1.26.3 scripts/lint` passed with zero findings. The explicit toolchain
  matches the module and the installed golangci-lint; the host's development Go 1.27
  standard library is newer than the lint binary's parser.
- Race detection passed for the Memory service, Pebble and memory stores, curation,
  and the curation adapter.
- Android `testDebugUnitTest` and `lintDebug` passed.
- A locally signed ARM64 `com.lkarlslund.koder.dev` APK was assembled and its signer,
  package metadata, byte length, and SHA-256 were validated by the embedding script.

The Android managed-device compatibility group was not rerun because Memory does not
change Android code, device APIs, layouts, audio, or the voice protocol. That matrix remains
the release/nightly gate for device-affecting changes under `docs/testing.md`.

## Copied-state migration

The approved 7979 service was stopped only long enough to create a validated Pebble
checkpoint of its Memory store, then immediately restarted. All migration work used
temporary copied data directories:

1. the checkpoint opened as an independent copied Koder state directory;
2. backend-neutral export completed with the required personal-data acknowledgement;
3. import into a genuinely empty state directory completed atomically;
4. exported and imported counts matched at 2 chunks, 2 revisions, and no entries, links,
   evidence, or assets;
5. a new validated Pebble checkpoint was created from the imported store.

The live database was never copied while open and was never used as the migration target.

## Live deployment

The clean `r1904-local` build is the expected final identity after this verification record
is committed. Deployment is limited to the user service on port 7979. Its release check
requires all of the following:

- `/api/health` reports `ok`;
- `/debug/runtime` reports the clean `r1904-local` build from the final commit;
- `/debug/memory` reports Pebble open, schema `current`, index generation 1 `ready`,
  and maintenance available;
- a one-time Android invitation downloads an APK whose size, SHA-256, and content type
  exactly match the embedded manifest.

Port 17979 is outside the deployment scope and must not be restarted or modified.


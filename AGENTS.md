# Repo Instructions

## Testing

- Select verification by change risk using `docs/testing.md`. It covers both
  Koder and Koder Voice, including when focused tests, interoperability tests,
  Android emulation, or full release/nightly suites are required.

## Go Commands

- Run Go commands normally from the real repository root at `/home/lak/github-repos/koder`.
- Do not use `.codex` sandbox/workaround directories or copied worktrees when running `go test`, `go build`, `go vet`, `staticcheck`, `golangci-lint`, or `govulncheck`.

## Commit Discipline

- After each successful, verified implementation step, create a git commit and push it so the branch stays in sync with completed work.
- Prefer small, logically grouped commits over large catch-up commits.
- Before committing Go changes, run the strongest applicable verification for this repo.
- Never commit ignored files or directories. Do not use force-add on paths covered by `.gitignore` or equivalent ignore rules.

## Debug API

- The running browser server exposes operational debug endpoints under `/debug`; see `docs/debug-api.md` before using or changing them.

## Build Versioning

- `scripts/build-number` derives the shared Koder and Android build identity from
  the repository's total commit count, formatted as `rNNNN`. Do not maintain a
  separate semantic version by hand.
- Clean local builds append `-local`; builds with tracked changes append
  `-local-dirty`. Android's internal `versionCode` is independently monotonic
  so existing development installations can always upgrade.

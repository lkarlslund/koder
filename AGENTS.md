# Repo Instructions

## Go Commands

- Run Go commands normally from the real repository root at `/home/lak/github-repos/koder`.
- Do not use `.codex` sandbox/workaround directories or copied worktrees when running `go test`, `go build`, `go vet`, `staticcheck`, `golangci-lint`, or `govulncheck`.

## Commit Discipline

- After each successful, verified implementation step, create a git commit so the branch stays in sync with completed work.
- Prefer small, logically grouped commits over large catch-up commits.
- Before committing Go changes, run the strongest applicable verification for this repo.

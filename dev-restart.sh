#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_SCRIPT="$ROOT_DIR/scripts/build-koder"
BIN="${KODER_DEV_BIN:-/tmp/koder-dev-${USER:-user}/koder}"
SETTLE_SECONDS="${KODER_DEV_SETTLE_SECONDS:-10}"
POLL_SECONDS="${KODER_DEV_POLL_SECONDS:-1}"
STOP_TIMEOUT_SECONDS="${KODER_DEV_STOP_TIMEOUT_SECONDS:-180}"

child_pid=""

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

cleanup() {
  if [[ -n "$child_pid" ]] && kill -0 "$child_pid" 2>/dev/null; then
    stop_koder "$child_pid"
  fi
}

trap cleanup EXIT INT TERM

build_koder() {
  "$BUILD_SCRIPT" "$BIN" >/dev/null
}

launch_koder() {
  "$BIN" "$@" &
  child_pid="$!"
  log "launched koder pid=$child_pid"
}

stop_koder() {
  local pid="$1"
  local waited=0
  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
    return 0
  fi
  log "stopping koder pid=$pid for restart"
  kill -USR1 "$pid" 2>/dev/null || true
  while kill -0 "$pid" 2>/dev/null; do
    if (( waited >= STOP_TIMEOUT_SECONDS )); then
      log "koder pid=$pid did not stop after ${STOP_TIMEOUT_SECONDS}s; terminating"
      kill -TERM "$pid" 2>/dev/null || true
      sleep 2
      if kill -0 "$pid" 2>/dev/null; then
        log "koder pid=$pid did not terminate; killing"
        kill -KILL "$pid" 2>/dev/null || true
      fi
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done
  wait "$pid" 2>/dev/null || true
}

source_signature() {
  (
    cd "$ROOT_DIR"
    find \
      cmd internal docs scripts \
      -type f \
      ! -path '*/.git/*' \
      -printf '%p %T@ %s\n' 2>/dev/null
    find . \
      -maxdepth 1 \
      -type f \
      \( -name '*.go' -o -name '*.mod' -o -name '*.sum' -o -name '*.md' -o -name '*.sh' \) \
      -printf '%p %T@ %s\n' 2>/dev/null
  ) | sort
}

wait_for_settle() {
  local previous
  local current
  local quiet

  previous="$(source_signature)"
  quiet=0
  while (( quiet < SETTLE_SECONDS )); do
    sleep 1
    current="$(source_signature)"
    if [[ "$current" == "$previous" ]]; then
      quiet=$((quiet + 1))
    else
      previous="$current"
      quiet=0
    fi
  done
}

log "building koder..."
build_koder
launch_koder "$@"
last_signature="$(source_signature)"

while true; do
  sleep "$POLL_SECONDS"
  if [[ -n "$child_pid" ]] && ! kill -0 "$child_pid" 2>/dev/null; then
    wait "$child_pid" 2>/dev/null || true
    child_pid=""
    log "koder exited; waiting for next successful build"
  fi

  current_signature="$(source_signature)"
  if [[ "$current_signature" == "$last_signature" ]]; then
    continue
  fi

  log "changes detected; waiting ${SETTLE_SECONDS}s for code to settle..."
  wait_for_settle
  last_signature="$(source_signature)"

  log "building koder..."
  if build_koder; then
    if [[ -n "$child_pid" ]] && kill -0 "$child_pid" 2>/dev/null; then
      stop_koder "$child_pid"
      child_pid=""
    fi
    launch_koder "$@"
  else
    log "build failed; keeping current koder process"
  fi
done

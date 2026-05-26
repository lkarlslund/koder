#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_SCRIPT="$ROOT_DIR/scripts/build-koder"
BIN="${KODER_DEV_BIN:-/tmp/koder-dev-${USER:-user}/koder}"
SETTLE_SECONDS="${KODER_DEV_SETTLE_SECONDS:-10}"
POLL_SECONDS="${KODER_DEV_POLL_SECONDS:-1}"
STOP_GRACE_SECONDS="${KODER_DEV_STOP_GRACE_SECONDS:-5}"
STOP_TIMEOUT_SECONDS="${KODER_DEV_STOP_TIMEOUT_SECONDS:-20}"
RESTART_EXIT_CODE="${KODER_DEV_RESTART_EXIT_CODE:-75}"

child_pid=""
shutting_down=0

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

exit_status_text() {
  local status="$1"
  if (( status == 0 )); then
    printf 'status 0'
  elif (( status > 128 )); then
    printf 'signal %d (status %d)' "$((status - 128))" "$status"
  else
    printf 'status %d' "$status"
  fi
}

cleanup() {
  if (( shutting_down )); then
    return 0
  fi
  shutting_down=1
  if [[ -n "$child_pid" ]] && kill -0 "$child_pid" 2>/dev/null; then
    stop_koder "$child_pid" "shutdown" || true
  fi
}

handle_signal() {
  local signal="$1"
  local status="$2"
  if [[ "$signal" == "TERM" ]]; then
    log "received TERM from outside dev-restart; shutting down with error"
  else
    log "received $signal; shutting down"
  fi
  cleanup
  exit "$status"
}

trap cleanup EXIT
trap 'handle_signal INT 130' INT
trap 'handle_signal TERM 1' TERM

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
  local reason="${2:-restart}"
  local waited=0
  local stat=""
  local exit_status=0
  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
    wait "$pid" 2>/dev/null || true
    return 0
  fi
  if [[ "$reason" == "restart" ]]; then
    log "stopping koder pid=$pid for restart"
  else
    log "stopping koder pid=$pid for $reason"
  fi
  if [[ "$reason" == "restart" ]]; then
    kill -USR1 "$pid" 2>/dev/null || true
  else
    kill -TERM "$pid" 2>/dev/null || true
  fi

  while true; do
    if ! kill -0 "$pid" 2>/dev/null; then
      wait "$pid" 2>/dev/null || exit_status=$?
      report_stopped_status "$reason" "$exit_status"
      return "$exit_status"
    fi
    stat="$(ps -o stat= -p "$pid" 2>/dev/null)"
    stat="${stat//[[:space:]]/}"
    if [[ "$stat" == Z* ]]; then
      wait "$pid" 2>/dev/null || exit_status=$?
      report_stopped_status "$reason" "$exit_status"
      return "$exit_status"
    fi
    if (( waited >= STOP_GRACE_SECONDS )); then
      break
    fi
    sleep 1
    waited=$((waited + 1))
  done

  log "koder pid=$pid did not stop after ${STOP_GRACE_SECONDS}s; terminating"
  kill -TERM "$pid" 2>/dev/null || true
  while true; do
    if ! kill -0 "$pid" 2>/dev/null; then
      wait "$pid" 2>/dev/null || exit_status=$?
      report_stopped_status "$reason" "$exit_status"
      return "$exit_status"
    fi
    stat="$(ps -o stat= -p "$pid" 2>/dev/null)"
    stat="${stat//[[:space:]]/}"
    if [[ "$stat" == Z* ]]; then
      wait "$pid" 2>/dev/null || exit_status=$?
      report_stopped_status "$reason" "$exit_status"
      return "$exit_status"
    fi
    if (( waited >= STOP_TIMEOUT_SECONDS )); then
      log "koder pid=$pid did not terminate after ${STOP_TIMEOUT_SECONDS}s; killing"
      kill -KILL "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || exit_status=$?
      report_stopped_status "$reason" "$exit_status"
      return "$exit_status"
    fi
    sleep 1
    waited=$((waited + 1))
  done
}

report_stopped_status() {
  local reason="$1"
  local exit_status="$2"
  if [[ "$reason" == "restart" ]]; then
    if (( exit_status == RESTART_EXIT_CODE )); then
      log "koder exited for restart with $(exit_status_text "$exit_status")"
    else
      log "koder exited unexpectedly during restart with $(exit_status_text "$exit_status")"
    fi
  fi
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

log_signature_changes() {
  local previous="$1"
  local current="$2"
  diff -u <(printf '%s\n' "$previous") <(printf '%s\n' "$current") |
    awk '
      /^[+-][^+-]/ {
        line = substr($0, 2)
        sub(/ [0-9]+(\.[0-9]+)? [0-9]+$/, "", line)
        if (line != "" && !seen[line]++) {
          print "  " line
        }
      }
    ' >&2 || true
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
    exit_status=0
    wait "$child_pid" 2>/dev/null || exit_status=$?
    child_pid=""
    if (( exit_status == RESTART_EXIT_CODE )); then
      log "koder exited for restart with $(exit_status_text "$exit_status"); rebuilding"
      log "building koder..."
      build_koder
      launch_koder "$@"
      last_signature="$(source_signature)"
      continue
    fi
    log "koder exited unexpectedly with $(exit_status_text "$exit_status")"
    exit 1
  fi

  current_signature="$(source_signature)"
  if [[ "$current_signature" == "$last_signature" ]]; then
    continue
  fi

  log "changes detected; waiting ${SETTLE_SECONDS}s for code to settle..."
  log_signature_changes "$last_signature" "$current_signature"
  wait_for_settle
  last_signature="$(source_signature)"

  log "building koder..."
  if build_koder; then
    if [[ -n "$child_pid" ]] && kill -0 "$child_pid" 2>/dev/null; then
      stop_status=0
      stop_koder "$child_pid" "restart" || stop_status=$?
      child_pid=""
      if (( stop_status != RESTART_EXIT_CODE )); then
        exit 1
      fi
    fi
    launch_koder "$@"
    last_signature="$(source_signature)"
  else
    log "build failed; keeping current koder process"
  fi
done

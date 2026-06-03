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
KODER_OUTPUT_LOG="${KODER_DEV_OUTPUT_LOG:-$(mktemp "/tmp/koder-dev-${USER:-user}.output.XXXXXX.log")}"
KODER_DEV_WEB_BIND="${KODER_DEV_WEB_BIND:-0.0.0.0:7979}"

child_pid=""
shutting_down=0
restart_failures=0

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
    report_live_koder_status
    report_koder_output
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

has_web_bind_arg() {
  local arg
  for arg in "$@"; do
    case "$arg" in
      --)
        return 1
        ;;
      --web-bind|--web-bind=*)
        return 0
        ;;
    esac
  done
  return 1
}

launch_args() {
  if has_web_bind_arg "$@"; then
    printf '%s\0' "$@"
    return 0
  fi
  printf '%s\0' "--web-bind=$KODER_DEV_WEB_BIND" "$@"
}

launch_koder() {
  local args=()
  mapfile -d '' -t args < <(launch_args "$@")
  : >"$KODER_OUTPUT_LOG"
  "$BIN" "${args[@]}" > >(tee -a "$KODER_OUTPUT_LOG") 2>&1 &
  child_pid="$!"
  log "launched koder pid=$child_pid"
  log "koder output log: $KODER_OUTPUT_LOG"
}

stop_koder() {
  local pid="$1"
  local reason="${2:-restart}"
  shift 2 || true
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
    request_koder_restart "$@" || kill -USR1 "$pid" 2>/dev/null || true
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

web_bind_arg() {
  local expect_value=0
  local arg
  for arg in "$@"; do
    if (( expect_value )); then
      printf '%s\n' "$arg"
      return 0
    fi
    case "$arg" in
      --)
        break
        ;;
      --web-bind=*)
        printf '%s\n' "${arg#--web-bind=}"
        return 0
        ;;
      --web-bind)
        expect_value=1
        ;;
    esac
  done
  printf '%s\n' "$KODER_DEV_WEB_BIND"
}

restart_needed_url() {
  local bind
  local addr
  local host
  local port
  bind="$(web_bind_arg "$@")"
  addr="${bind#http://}"
  addr="${addr#https://}"
  host="${addr%:*}"
  port="${addr##*:}"
  if [[ "$host" == "$addr" ]]; then
    host="127.0.0.1"
    port="$addr"
  fi
  case "$host" in
    ""|"0.0.0.0"|"::"|"[::]")
      host="127.0.0.1"
      ;;
  esac
  printf 'http://%s:%s/api/restart-needed\n' "$host" "$port"
}

restart_rpc_url() {
  local bind
  local addr
  local host
  local port
  bind="$(web_bind_arg "$@")"
  addr="${bind#http://}"
  addr="${addr#https://}"
  host="${addr%:*}"
  port="${addr##*:}"
  if [[ "$host" == "$addr" ]]; then
    host="127.0.0.1"
    port="$addr"
  fi
  case "$host" in
    ""|"0.0.0.0"|"::"|"[::]")
      host="127.0.0.1"
      ;;
  esac
  printf 'http://%s:%s/api/rpc/restart_process\n' "$host" "$port"
}

request_koder_restart() {
  local url
  local output
  url="$(restart_rpc_url "$@")"
  log "requesting koder restart through $url"
  if output="$(curl --fail --silent --show-error --max-time 5 -X POST -H 'Content-Type: application/json' --data '{"hard":true}' "$url" 2>&1)"; then
    log "restart request acknowledged"
    return 0
  fi
  log "restart request failed: $output"
  return 1
}

notify_restart_needed() {
  local pid="$1"
  shift || true
  local url
  local output
  local payload
  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
    return 0
  fi
  url="$(restart_needed_url "$@")"
  payload="$(restart_needed_payload)"
  log "new koder build is ready; notifying $url with $(restart_needed_log_label "$payload")"
  if output="$(curl --fail --silent --show-error --max-time 2 -X POST -H 'Content-Type: application/json' --data "$payload" "$url" 2>&1)"; then
    log "restart-needed notification acknowledged"
    return 0
  fi
  log "restart-needed notification failed: $output"
  return 1
}

json_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/ }"
  value="${value//$'\r'/ }"
  printf '%s' "$value"
}

version_field() {
  local text="$1"
  local key="$2"
  awk -F': ' -v key="$key" '$1 == key {print $2; exit}' <<<"$text"
}

restart_needed_payload() {
  local version_text=""
  local first_line=""
  local version=""
  local commit=""
  local dirty=""
  local build_time=""
  local build_id=""
  version_text="$("$BIN" version 2>/dev/null || true)"
  first_line="$(sed -n '1p' <<<"$version_text")"
  version="${first_line##* }"
  commit="$(version_field "$version_text" commit)"
  dirty="$(version_field "$version_text" dirty)"
  build_time="$(version_field "$version_text" build_time)"
  build_id="$commit"
  if [[ "$dirty" == "true" && -n "$build_id" ]]; then
    build_id="${build_id}-dirty"
  fi
  if [[ -n "$build_time" && -n "$build_id" ]]; then
    build_id="${build_id} @ ${build_time}"
  fi
  printf '{"version":"%s","commit":"%s","dirty":"%s","build_time":"%s","build_id":"%s"}' \
    "$(json_escape "$version")" \
    "$(json_escape "$commit")" \
    "$(json_escape "$dirty")" \
    "$(json_escape "$build_time")" \
    "$(json_escape "$build_id")"
}

restart_needed_log_label() {
  local payload="$1"
  local label
  label="$(sed -n 's/.*"build_id":"\([^"]*\)".*/\1/p' <<<"$payload")"
  if [[ -n "$label" ]]; then
    printf 'build %s' "$label"
  else
    printf 'build metadata'
  fi
}

report_stopped_status() {
  local reason="$1"
  local exit_status="$2"
  if [[ "$reason" == "restart" ]]; then
    if (( exit_status == RESTART_EXIT_CODE )); then
      log "koder exited for restart with $(exit_status_text "$exit_status")"
    else
      log "koder exited unexpectedly during restart with $(exit_status_text "$exit_status")"
      report_koder_output
    fi
  elif [[ "$reason" == "shutdown" ]]; then
    log "koder exited after shutdown with $(exit_status_text "$exit_status")"
    report_koder_output
  fi
}

report_live_koder_status() {
  if [[ -z "$child_pid" ]]; then
    log "koder pid: none"
    return 0
  fi
  if kill -0 "$child_pid" 2>/dev/null; then
    log "koder pid=$child_pid is still running; no koder exit code is available yet"
    return 0
  fi
  log "koder pid=$child_pid has already exited; collecting exit code"
}

report_koder_output() {
  if [[ ! -s "$KODER_OUTPUT_LOG" ]]; then
    log "koder output: <empty>"
    return 0
  fi
  log "last koder output from $KODER_OUTPUT_LOG:"
  tail -n "${KODER_DEV_OUTPUT_TAIL_LINES:-40}" "$KODER_OUTPUT_LOG" >&2 || true
}

pid_running() {
  local pid="$1"
  local stat=""
  if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
    return 1
  fi
  stat="$(ps -o stat= -p "$pid" 2>/dev/null || true)"
  stat="${stat//[[:space:]]/}"
  [[ "$stat" != Z* ]]
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
      log "koder exited for restart with $(exit_status_text "$exit_status"); relaunching last successful build"
      launch_koder "$@"
      continue
    fi
    log "koder exited unexpectedly with $(exit_status_text "$exit_status")"
    report_koder_output
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
      notify_restart_needed "$child_pid" "$@" || true
      last_signature="$(source_signature)"
      continue
    fi
    launch_koder "$@"
    restart_failures=0
    last_signature="$(source_signature)"
  else
    log "build failed; keeping current koder process"
  fi
done

#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RUN_DIR="$SCRIPT_DIR/run"
LOG_DIR="$SCRIPT_DIR/logs"

BACKEND_PID_FILE="$RUN_DIR/water-be.pid"
FRONTEND_PID_FILE="$RUN_DIR/water-fe.pid"
BACKEND_LOG_FILE="$LOG_DIR/water-be.log"
FRONTEND_LOG_FILE="$LOG_DIR/water-fe.log"

DEFAULT_BACKEND_ADDR=":8080"
DEFAULT_FRONTEND_HOST="127.0.0.1"
DEFAULT_FRONTEND_PORT="5173"

mkdir -p "$RUN_DIR" "$LOG_DIR"

backend_addr() {
  printf '%s' "${WATER_HTTP_ADDR:-$DEFAULT_BACKEND_ADDR}"
}

backend_url() {
  local addr
  addr="$(backend_addr)"
  if [[ "$addr" == http://* || "$addr" == https://* ]]; then
    printf '%s' "$addr"
  elif [[ "$addr" == :* ]]; then
    printf 'http://127.0.0.1%s' "$addr"
  elif [[ "$addr" == *:* ]]; then
    printf 'http://%s' "$addr"
  else
    printf 'http://127.0.0.1:%s' "$addr"
  fi
}

frontend_host() {
  printf '%s' "${WATER_FE_HOST:-$DEFAULT_FRONTEND_HOST}"
}

frontend_port() {
  printf '%s' "${WATER_FE_PORT:-$DEFAULT_FRONTEND_PORT}"
}

frontend_url() {
  printf 'http://%s:%s' "$(frontend_host)" "$(frontend_port)"
}

is_pid_running() {
  local pid="${1:-}"
  [[ -n "$pid" ]] && kill -0 "$pid" >/dev/null 2>&1
}

pid_from_file() {
  local pid_file="$1"
  if [[ -f "$pid_file" ]]; then
    tr -d '[:space:]' < "$pid_file"
  fi
  return 0
}

is_service_running() {
  local pid_file="$1"
  local pid
  pid="$(pid_from_file "$pid_file")"
  is_pid_running "$pid"
}

wait_for_url() {
  local url="$1"
  local attempts="${2:-30}"
  local delay="${3:-0.3}"
  local i
  for ((i = 1; i <= attempts; i++)); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done
  return 1
}

stop_by_pid_file() {
  local name="$1"
  local pid_file="$2"
  local pid
  pid="$(pid_from_file "$pid_file")"
  if ! is_pid_running "$pid"; then
    rm -f "$pid_file"
    printf '%s 未运行\n' "$name"
    return 0
  fi

  kill "$pid"
  for _ in {1..20}; do
    if ! is_pid_running "$pid"; then
      rm -f "$pid_file"
      printf '%s 已停止\n' "$name"
      return 0
    fi
    sleep 0.2
  done

  kill -9 "$pid" >/dev/null 2>&1 || true
  rm -f "$pid_file"
  printf '%s 已强制停止\n' "$name"
}

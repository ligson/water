#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

print_status() {
  local name="$1"
  local pid_file="$2"
  local url="$3"
  local health_url="${4:-$url}"
  local log_file="$5"
  local pid
  pid="$(pid_from_file "$pid_file")"

  if is_pid_running "$pid"; then
    if curl -fsS "$health_url" >/dev/null 2>&1; then
      printf '%s：运行中 pid=%s url=%s\n' "$name" "$pid" "$url"
    else
      printf '%s：进程存在但访问检查失败 pid=%s url=%s\n' "$name" "$pid" "$url"
    fi
  else
    printf '%s：未运行 url=%s\n' "$name" "$url"
  fi
  printf '日志：%s\n' "$log_file"
}

print_status "后端" "$BACKEND_PID_FILE" "$(backend_url)" "$(backend_url)/api/health" "$BACKEND_LOG_FILE"
print_status "前端" "$FRONTEND_PID_FILE" "$(frontend_url)" "$(frontend_url)" "$FRONTEND_LOG_FILE"

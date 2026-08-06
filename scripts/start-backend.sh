#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

if is_service_running "$BACKEND_PID_FILE"; then
  printf '后端已在运行：pid=%s, %s\n' "$(pid_from_file "$BACKEND_PID_FILE")" "$(backend_url)"
  exit 0
fi

rm -f "$BACKEND_PID_FILE"

nohup bash -c 'cd "$1" && exec env WATER_HTTP_ADDR="$2" go run ./cmd/water' \
  bash "$PROJECT_ROOT/water-be" "$(backend_addr)" >"$BACKEND_LOG_FILE" 2>&1 &

printf '%s\n' "$!" > "$BACKEND_PID_FILE"

if wait_for_url "$(backend_url)/api/health" 40 0.25; then
  printf '后端已启动：pid=%s, %s\n' "$(pid_from_file "$BACKEND_PID_FILE")" "$(backend_url)"
  printf '日志：%s\n' "$BACKEND_LOG_FILE"
  exit 0
fi

printf '后端启动后健康检查未通过，请查看日志：%s\n' "$BACKEND_LOG_FILE" >&2
exit 1

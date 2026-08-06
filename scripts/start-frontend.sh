#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

if is_service_running "$FRONTEND_PID_FILE"; then
  printf '前端已在运行：pid=%s, %s\n' "$(pid_from_file "$FRONTEND_PID_FILE")" "$(frontend_url)"
  exit 0
fi

rm -f "$FRONTEND_PID_FILE"

if [[ ! -d "$PROJECT_ROOT/water-fe/node_modules" ]]; then
  printf '前端依赖不存在，正在执行 npm install...\n'
  (cd "$PROJECT_ROOT/water-fe" && npm install)
fi

nohup bash -c 'cd "$1" && exec env VITE_API_BASE="$2" npm run dev -- --host "$3" --port "$4"' \
  bash "$PROJECT_ROOT/water-fe" "${VITE_API_BASE:-$(backend_url)}" "$(frontend_host)" "$(frontend_port)" \
  >"$FRONTEND_LOG_FILE" 2>&1 &

printf '%s\n' "$!" > "$FRONTEND_PID_FILE"

if wait_for_url "$(frontend_url)" 40 0.25; then
  printf '前端已启动：pid=%s, %s\n' "$(pid_from_file "$FRONTEND_PID_FILE")" "$(frontend_url)"
  printf '后端地址：%s\n' "${VITE_API_BASE:-$(backend_url)}"
  printf '日志：%s\n' "$FRONTEND_LOG_FILE"
  exit 0
fi

printf '前端启动后访问检查未通过，请查看日志：%s\n' "$FRONTEND_LOG_FILE" >&2
exit 1

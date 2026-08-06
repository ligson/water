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

python3 - "$FRONTEND_PID_FILE" "$FRONTEND_LOG_FILE" "$PROJECT_ROOT/water-fe" "${VITE_API_BASE:-$(backend_url)}" "$(frontend_host)" "$(frontend_port)" <<'PY'
import os
import subprocess
import sys

pid_file, log_file, project_root, api_base, host, port = sys.argv[1:7]
cmd = [
    "bash",
    "-lc",
    f'cd "{project_root}" && exec env VITE_API_BASE="{api_base}" npm run dev -- --host "{host}" --port "{port}"',
]

pid = os.fork()
if pid > 0:
    sys.exit(0)

os.setsid()
pid = os.fork()
if pid > 0:
    sys.exit(0)

with open(log_file, "ab", buffering=0) as log, open(os.devnull, "rb") as devnull:
    proc = subprocess.Popen(cmd, stdin=devnull, stdout=log, stderr=log, close_fds=True)
    with open(pid_file, "w", encoding="utf-8") as fh:
        fh.write(str(proc.pid))
PY

if wait_for_url "$(frontend_url)" 40 0.25; then
  printf '前端已启动：pid=%s, %s\n' "$(pid_from_file "$FRONTEND_PID_FILE")" "$(frontend_url)"
  printf '后端地址：%s\n' "${VITE_API_BASE:-$(backend_url)}"
  printf '日志：%s\n' "$FRONTEND_LOG_FILE"
  exit 0
fi

printf '前端启动后访问检查未通过，请查看日志：%s\n' "$FRONTEND_LOG_FILE" >&2
exit 1

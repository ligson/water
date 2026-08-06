#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

if is_service_running "$BACKEND_PID_FILE"; then
  printf '后端已在运行：pid=%s, %s\n' "$(pid_from_file "$BACKEND_PID_FILE")" "$(backend_url)"
  exit 0
fi

rm -f "$BACKEND_PID_FILE"

python3 - "$BACKEND_PID_FILE" "$BACKEND_LOG_FILE" "$PROJECT_ROOT/water-be" "$(backend_addr)" "$RUN_DIR/water-be-dev" <<'PY'
import os
import subprocess
import sys

pid_file, log_file, project_root, addr, binary_path = sys.argv[1:6]
cmd = [
    "bash",
    "-lc",
    f'cd "{project_root}" && go build -o "{binary_path}" ./cmd/water && exec env WATER_HTTP_ADDR="{addr}" "{binary_path}"',
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

if wait_for_url "$(backend_url)/api/health" 40 0.25; then
  printf '后端已启动：pid=%s, %s\n' "$(pid_from_file "$BACKEND_PID_FILE")" "$(backend_url)"
  printf '日志：%s\n' "$BACKEND_LOG_FILE"
  exit 0
fi

printf '后端启动后健康检查未通过，请查看日志：%s\n' "$BACKEND_LOG_FILE" >&2
exit 1

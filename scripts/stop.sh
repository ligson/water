#!/usr/bin/env bash

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

stop_by_pid_file "前端" "$FRONTEND_PID_FILE"
stop_by_pid_file "后端" "$BACKEND_PID_FILE"

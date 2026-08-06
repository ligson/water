#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

"$SCRIPT_DIR/start-backend.sh"
"$SCRIPT_DIR/start-frontend.sh"

printf '\n开发服务已就绪：\n'
"$SCRIPT_DIR/status.sh"

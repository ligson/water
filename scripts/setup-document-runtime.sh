#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VENV_DIR="${WATER_DOCUMENT_VENV:-$PROJECT_ROOT/water-be/.venv-document}"
REQUIREMENTS="$PROJECT_ROOT/water-be/runtime/document/requirements.txt"

select_python() {
  if [[ -n "${WATER_DOCUMENT_BOOTSTRAP_PYTHON:-}" ]]; then
    printf '%s' "$WATER_DOCUMENT_BOOTSTRAP_PYTHON"
    return
  fi
  for candidate in python3.11 python3.12 python3.13 python3.10 python3; do
    if command -v "$candidate" >/dev/null 2>&1; then
      if "$candidate" -c 'import sys; raise SystemExit(0 if (3, 10) <= sys.version_info[:2] < (3, 14) else 1)' >/dev/null 2>&1; then
        command -v "$candidate"
        return
      fi
    fi
  done
  printf '需要 Python 3.10-3.13 才能安装文档解析运行时。\n' >&2
  exit 1
}

PYTHON_BIN="$(select_python)"
printf '使用 Python：%s\n' "$PYTHON_BIN"
"$PYTHON_BIN" -m venv "$VENV_DIR"
"$VENV_DIR/bin/python" -m pip install --disable-pip-version-check --upgrade pip
"$VENV_DIR/bin/python" -m pip install --disable-pip-version-check -r "$REQUIREMENTS"
"$VENV_DIR/bin/python" -I -c 'from markitdown import MarkItDown; print("MarkItDown 文档运行时已就绪")'
printf '文档运行时：%s\n' "$VENV_DIR/bin/python"

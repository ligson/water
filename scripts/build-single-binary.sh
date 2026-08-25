#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSION="${1:-dev}"
OUTPUT_PATH="${2:-$PROJECT_ROOT/water-be/bin/water}"
EMBED_DIR="$PROJECT_ROOT/water-be/internal/web/dist"

for command_name in go npm cp rm mkdir; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf '缺少构建命令：%s\n' "$command_name" >&2
    exit 1
  fi
done

restore_embed_dir() {
  rm -rf "$EMBED_DIR"
  mkdir -p "$EMBED_DIR"
  printf '%s\n' 'The production frontend is generated here before building the single Water binary.' > "$EMBED_DIR/placeholder.txt"
}

trap restore_embed_dir EXIT

printf '构建前端并准备 Go embed 资源...\n'
(
  cd "$PROJECT_ROOT/water-fe"
  npm ci
  npm run build
)

rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -R "$PROJECT_ROOT/water-fe/dist/." "$EMBED_DIR/"

mkdir -p "$(dirname "$OUTPUT_PATH")"
printf '编译单体二进制：%s\n' "$OUTPUT_PATH"
(
  cd "$PROJECT_ROOT/water-be"
  CGO_ENABLED=0 go build \
    -tags='netgo osusergo' \
    -trimpath \
    -ldflags="-s -w -X main.buildVersion=$VERSION" \
    -o "$OUTPUT_PATH" \
    ./cmd/water
)

printf '单体二进制已生成：%s\n' "$OUTPUT_PATH"

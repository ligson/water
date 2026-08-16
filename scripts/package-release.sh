#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSION="${1:-}"
OUTPUT_DIR="$PROJECT_ROOT/output/release"
STAGING_ROOT="$PROJECT_ROOT/output/release-staging"

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  printf '版本必须使用 SemVer 标签，例如 v0.1.0 或 v0.1.0-rc.1。\n' >&2
  exit 1
fi

for command_name in go npm tar shasum; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf '缺少打包命令：%s\n' "$command_name" >&2
    exit 1
  fi
done

rm -rf "$OUTPUT_DIR" "$STAGING_ROOT"
mkdir -p "$OUTPUT_DIR" "$STAGING_ROOT"
trap 'rm -rf "$STAGING_ROOT"' EXIT

printf '构建前端生产包...\n'
(
  cd "$PROJECT_ROOT/water-fe"
  npm ci
  npm run build
)

frontend_name="water-fe_${VERSION}"
frontend_stage="$STAGING_ROOT/$frontend_name"
mkdir -p "$frontend_stage"
cp -R "$PROJECT_ROOT/water-fe/dist" "$frontend_stage/dist"
cp "$PROJECT_ROOT/water-fe/README.md" "$frontend_stage/README.md"
cp "$PROJECT_ROOT/water-fe/docker/nginx.conf" "$frontend_stage/nginx.conf"
cp "$PROJECT_ROOT/LICENSE" "$frontend_stage/LICENSE"
printf '%s\n' "$VERSION" > "$frontend_stage/VERSION"
tar -C "$STAGING_ROOT" -czf "$OUTPUT_DIR/${frontend_name}.tar.gz" "$frontend_name"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
)

for target in "${targets[@]}"; do
  read -r target_os target_arch <<< "$target"
  package_name="water-be_${VERSION}_${target_os}_${target_arch}"
  package_stage="$STAGING_ROOT/$package_name"
  mkdir -p "$package_stage"

  printf '构建后端：%s/%s...\n' "$target_os" "$target_arch"
  (
    cd "$PROJECT_ROOT/water-be"
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
      -trimpath \
      -ldflags="-s -w" \
      -o "$package_stage/water" \
      ./cmd/water
  )

  cp "$PROJECT_ROOT/water-be/README.md" "$package_stage/README.md"
  cp "$PROJECT_ROOT/LICENSE" "$package_stage/LICENSE"
  printf '%s\n' "$VERSION" > "$package_stage/VERSION"
  tar -C "$STAGING_ROOT" -czf "$OUTPUT_DIR/${package_name}.tar.gz" "$package_name"
done

(
  cd "$OUTPUT_DIR"
  shasum -a 256 ./*.tar.gz > checksums.txt
)

printf '发版包已生成：%s\n' "$OUTPUT_DIR"

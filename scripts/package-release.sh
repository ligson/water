#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERSION="${1:-}"
OUTPUT_DIR="$PROJECT_ROOT/output/release"
STAGING_ROOT="$PROJECT_ROOT/output/release-staging"
EMBED_DIR="$PROJECT_ROOT/water-be/internal/web/dist"

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
restore_embed_dir() {
  rm -rf "$EMBED_DIR"
  mkdir -p "$EMBED_DIR"
  printf '%s\n' 'The production frontend is generated here before building the single Water binary.' > "$EMBED_DIR/placeholder.txt"
}
trap 'restore_embed_dir; rm -rf "$STAGING_ROOT"' EXIT

printf '构建前端生产包...\n'
(
  cd "$PROJECT_ROOT/water-fe"
  npm ci
  npm run build
)

rm -rf "$EMBED_DIR"
mkdir -p "$EMBED_DIR"
cp -R "$PROJECT_ROOT/water-fe/dist/." "$EMBED_DIR/"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
)

for target in "${targets[@]}"; do
  read -r target_os target_arch <<< "$target"
  package_name="water_${VERSION}_${target_os}_${target_arch}"
  package_stage="$STAGING_ROOT/$package_name"
  mkdir -p "$package_stage"

  printf '构建单体二进制：%s/%s...\n' "$target_os" "$target_arch"
  (
    cd "$PROJECT_ROOT/water-be"
    CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
      -tags='netgo osusergo' \
      -trimpath \
      -ldflags="-s -w -X main.buildVersion=$VERSION" \
      -o "$package_stage/water" \
      ./cmd/water
  )

  cp "$PROJECT_ROOT/LICENSE" "$package_stage/LICENSE"
  printf '%s\n' "$VERSION" > "$package_stage/VERSION"

  if [[ "$target_os" == "linux" ]]; then
    cp "$PROJECT_ROOT/packaging/linux/README.md" "$package_stage/README.md"
    cp "$PROJECT_ROOT/README.md" "$package_stage/PROJECT_README.md"
    cp "$PROJECT_ROOT/packaging/linux/install.sh" "$package_stage/install.sh"
    cp "$PROJECT_ROOT/packaging/linux/uninstall.sh" "$package_stage/uninstall.sh"
    cp "$PROJECT_ROOT/packaging/linux/water.service" "$package_stage/water.service"
    cp "$PROJECT_ROOT/packaging/linux/water.env.example" "$package_stage/water.env.example"
    cp "$PROJECT_ROOT/packaging/linux/config.yaml.example" "$package_stage/config.yaml.example"
    chmod 0755 "$package_stage/install.sh" "$package_stage/uninstall.sh"
  else
    cp "$PROJECT_ROOT/README.md" "$package_stage/README.md"
  fi

  COPYFILE_DISABLE=1 tar -C "$STAGING_ROOT" -czf "$OUTPUT_DIR/${package_name}.tar.gz" "$package_name"
done

(
  cd "$OUTPUT_DIR"
  shasum -a 256 ./*.tar.gz > checksums.txt
)

printf '发版包已生成：%s\n' "$OUTPUT_DIR"

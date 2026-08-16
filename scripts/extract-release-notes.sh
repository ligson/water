#!/usr/bin/env bash

set -euo pipefail

VERSION="${1:-}"
CHANGELOG_PATH="${2:-CHANGELOG.md}"

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  printf '版本必须使用 SemVer 标签，例如 v0.1.0 或 v0.1.0-rc.1。\n' >&2
  exit 1
fi

if [[ ! -f "$CHANGELOG_PATH" ]]; then
  printf '找不到变更记录：%s\n' "$CHANGELOG_PATH" >&2
  exit 1
fi

release_version="${VERSION#v}"
notes="$({
  awk -v version="$release_version" '
    BEGIN {
      exact = "## [" version "]"
      dated = exact " - "
    }
    $0 == exact || index($0, dated) == 1 {
      found = 1
      next
    }
    found && /^## / {
      exit
    }
    found {
      lines[++count] = $0
    }
    END {
      if (!found) {
        exit 2
      }
      while (count > 0 && lines[count] ~ /^[[:space:]]*$/) {
        count--
      }
      first = 1
      while (first <= count && lines[first] ~ /^[[:space:]]*$/) {
        first++
      }
      for (i = first; i <= count; i++) {
        print lines[i]
      }
    }
  ' "$CHANGELOG_PATH"
} || true)"

if [[ -z "${notes//[[:space:]]/}" ]]; then
  printf 'CHANGELOG 中缺少版本 %s 的非空章节，请先添加标题：## [%s] - YYYY-MM-DD\n' \
    "$VERSION" "$release_version" >&2
  exit 1
fi

printf '%s\n' "$notes"

#!/usr/bin/env bash

set -euo pipefail

SERVICE_NAME="water"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="$SCRIPT_DIR"
CONFIG_DIR="$SCRIPT_DIR"
DATA_DIR="$SCRIPT_DIR/data"
PURGE=0

usage() {
  printf '用法：sudo ./uninstall.sh [--install-dir PATH] [--config-dir PATH] [--purge]\n'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --purge)
      PURGE=1
      shift
      ;;
    --install-dir)
      [[ $# -ge 2 ]] || { printf '缺少 --install-dir 参数。\n' >&2; exit 1; }
      INSTALL_DIR="$2"
      shift 2
      ;;
    --config-dir)
      [[ $# -ge 2 ]] || { printf '缺少 --config-dir 参数。\n' >&2; exit 1; }
      CONFIG_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf '未知参数：%s\n' "$1" >&2
      exit 1
      ;;
  esac
done

[[ "${EUID:-$(id -u)}" -eq 0 ]] || { printf '请使用 root 或 sudo 运行。\n' >&2; exit 1; }

if [[ -f "$CONFIG_DIR/water.env" ]]; then
  configured_data_dir="$(awk -F= '$1 == "WATER_DATA_DIR" { sub(/^[^=]*=/, ""); print; exit }' "$CONFIG_DIR/water.env")"
  if [[ "$configured_data_dir" == /* ]]; then
    DATA_DIR="$configured_data_dir"
  fi
fi

if [[ -f "$CONFIG_DIR/config.yaml" ]]; then
  configured_data_dir="$(awk '
    /^storage:[[:space:]]*$/ { in_storage = 1; next }
    in_storage && /^[^[:space:]]/ { in_storage = 0 }
    in_storage && /^[[:space:]]+data_dir:/ {
      sub(/^[^:]*:[[:space:]]*/, "")
      gsub(/^"|"$/, "")
      print
      exit
    }
  ' "$CONFIG_DIR/config.yaml")"
  if [[ "$configured_data_dir" == /* ]]; then
    DATA_DIR="$configured_data_dir"
  elif [[ -n "$configured_data_dir" ]]; then
    DATA_DIR="$CONFIG_DIR/$configured_data_dir"
  fi
fi

systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true
rm -f "/etc/systemd/system/$SERVICE_NAME.service"
systemctl daemon-reload
rm -rf "$INSTALL_DIR" "$CONFIG_DIR"

if [[ "$PURGE" -eq 1 ]]; then
  rm -rf "$DATA_DIR"
  printf '已删除数据目录：%s\n' "$DATA_DIR"
else
  printf '已保留数据目录：%s\n' "$DATA_DIR"
fi

printf '若水 systemd 服务已卸载。\n'

#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_NAME="water"
INSTALL_DIR="$SCRIPT_DIR"
CONFIG_DIR="$SCRIPT_DIR"
ENV_FILE="$CONFIG_DIR/water.env"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
DATA_DIR="$SCRIPT_DIR/data"
DATA_SET=0
WORKSPACE_DIR="$SCRIPT_DIR"
WORKSPACE_SET=0
SERVICE_USER="${SUDO_USER:-water}"
SERVICE_GROUP=""
HTTP_ADDR=""
ENV_SOURCE=""
START_SERVICE=1
TIMEZONE=""

usage() {
  cat <<'EOF'
用法：sudo ./install.sh [选项]

选项：
  --user NAME             systemd 服务用户，默认 water
  --group NAME            systemd 服务组，默认 water
  --install-dir PATH      二进制和运行时目录，默认当前安装包目录
  --config-dir PATH       环境文件目录，默认当前安装包目录
  --data-dir PATH         SQLite/运行数据目录，默认当前目录/data
  --workspace-dir PATH    服务工作目录，默认当前安装包目录
  --http-addr ADDR        监听地址；新安装默认 :8080，升级保留已有配置
  --env-file PATH         复制已有环境文件，安装时保留其中的 PIN 等配置
  --no-start              安装并 enable，但不立即启动
  -h, --help              显示帮助
EOF
}

die() {
  printf '安装失败：%s\n' "$*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --user)
      [[ $# -ge 2 ]] || die "缺少 --user 参数"
      SERVICE_USER="$2"
      shift 2
      ;;
    --group)
      [[ $# -ge 2 ]] || die "缺少 --group 参数"
      SERVICE_GROUP="$2"
      shift 2
      ;;
    --install-dir)
      [[ $# -ge 2 ]] || die "缺少 --install-dir 参数"
      INSTALL_DIR="$2"
      shift 2
      ;;
    --config-dir)
      [[ $# -ge 2 ]] || die "缺少 --config-dir 参数"
      CONFIG_DIR="$2"
      ENV_FILE="$CONFIG_DIR/water.env"
      CONFIG_FILE="$CONFIG_DIR/config.yaml"
      shift 2
      ;;
    --data-dir)
      [[ $# -ge 2 ]] || die "缺少 --data-dir 参数"
      DATA_DIR="$2"
      DATA_SET=1
      shift 2
      ;;
    --workspace-dir)
      [[ $# -ge 2 ]] || die "缺少 --workspace-dir 参数"
      WORKSPACE_DIR="$2"
      WORKSPACE_SET=1
      shift 2
      ;;
    --http-addr)
      [[ $# -ge 2 ]] || die "缺少 --http-addr 参数"
      HTTP_ADDR="$2"
      shift 2
      ;;
    --env-file)
      [[ $# -ge 2 ]] || die "缺少 --env-file 参数"
      ENV_SOURCE="$2"
      shift 2
      ;;
    --no-start)
      START_SERVICE=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "未知参数：$1"
      ;;
  esac
done

if [[ "$WORKSPACE_SET" -eq 0 && -f "$ENV_FILE" ]]; then
  configured_workspace="$(awk -F= '
    $1 == "WATER_WORKSPACE_DIR" || $1 == "WATER_WORKSPACE_HOST_PATH" {
      sub(/^[^=]*=/, "")
      print
      exit
    }
  ' "$ENV_FILE")"
  if [[ "$configured_workspace" == /* ]]; then
    WORKSPACE_DIR="$configured_workspace"
  fi
fi

if [[ "$WORKSPACE_SET" -eq 0 && -f "/etc/systemd/system/$SERVICE_NAME.service" ]]; then
  configured_workspace="$(awk -F= '$1 == "WorkingDirectory" { print $2; exit }' "/etc/systemd/system/$SERVICE_NAME.service")"
  if [[ "$configured_workspace" == /* ]]; then
    WORKSPACE_DIR="$configured_workspace"
  fi
fi

if [[ -z "$SERVICE_GROUP" ]]; then
  if id "$SERVICE_USER" >/dev/null 2>&1; then
    SERVICE_GROUP="$(id -gn "$SERVICE_USER")"
  else
    SERVICE_GROUP="$SERVICE_USER"
  fi
fi

if [[ "$DATA_SET" -eq 0 && -f "$ENV_FILE" ]]; then
  configured_data_dir="$(awk -F= '$1 == "WATER_DATA_DIR" { sub(/^[^=]*=/, ""); print; exit }' "$ENV_FILE")"
  if [[ "$configured_data_dir" == /* ]]; then
    DATA_DIR="$configured_data_dir"
  fi
fi

[[ "${EUID:-$(id -u)}" -eq 0 ]] || die "请使用 root 或 sudo 运行。"
command -v systemctl >/dev/null 2>&1 || die "目标系统没有 systemctl。"
command -v install >/dev/null 2>&1 || die "目标系统缺少 install 命令。"
[[ -x "$SCRIPT_DIR/water" ]] || die "安装包中缺少可执行文件 water。"
[[ -f "$SCRIPT_DIR/water.service" ]] || die "安装包中缺少 water.service。"
[[ "$INSTALL_DIR" == /* && "$CONFIG_DIR" == /* && "$DATA_DIR" == /* && "$WORKSPACE_DIR" == /* ]] \
  || die "安装目录、配置目录、数据目录和工作区必须使用绝对路径。"
[[ "$INSTALL_DIR$CONFIG_DIR$DATA_DIR$WORKSPACE_DIR" != *['|@']* && \
  "$INSTALL_DIR$CONFIG_DIR$DATA_DIR$WORKSPACE_DIR" != *[[:space:]]* ]] \
  || die "目录路径暂不支持空白、| 或 @ 字符。"
[[ -d "$WORKSPACE_DIR" ]] || die "工作区不存在：$WORKSPACE_DIR；请先创建或传入正确路径。"

if ! awk -F: -v group="$SERVICE_GROUP" '$1 == group { found = 1 } END { exit(found ? 0 : 1) }' /etc/group; then
  command -v groupadd >/dev/null 2>&1 || die "服务组不存在且系统没有 groupadd：$SERVICE_GROUP"
  groupadd --system "$SERVICE_GROUP"
fi
if ! id "$SERVICE_USER" >/dev/null 2>&1; then
  command -v useradd >/dev/null 2>&1 || die "服务用户不存在且系统没有 useradd：$SERVICE_USER"
  useradd --system --gid "$SERVICE_GROUP" --home-dir "$DATA_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
fi
[[ -z "$ENV_SOURCE" || -f "$ENV_SOURCE" ]] || die "环境文件不存在：$ENV_SOURCE"

backup_root="/var/backups/water"
backup_dir="$backup_root/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$backup_dir"

systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true

if [[ -f "$INSTALL_DIR/water" ]]; then
  install -D -m 0755 "$INSTALL_DIR/water" "$backup_dir/water"
fi
if [[ -f "$ENV_FILE" ]]; then
  install -D -m 0600 "$ENV_FILE" "$backup_dir/water.env"
fi

install -d -m 0755 "$INSTALL_DIR"
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$CONFIG_DIR"
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$DATA_DIR"
install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$DATA_DIR/home"
install -m 0755 "$SCRIPT_DIR/water" "$INSTALL_DIR/.water.new"
mv -f "$INSTALL_DIR/.water.new" "$INSTALL_DIR/water"

if [[ -n "$ENV_SOURCE" ]]; then
  install -m 0600 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$ENV_SOURCE" "$ENV_FILE"
elif [[ ! -f "$ENV_FILE" ]]; then
  umask 077
  printf '%s\n' 'WATER_ACCESS_PIN=change-me' > "$ENV_FILE"
fi

env_value() {
  local key="$1"
  awk -F= -v key="$key" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$ENV_FILE"
}

legacy_http_addr="$(env_value WATER_HTTP_ADDR)"
legacy_data_dir="$(env_value WATER_DATA_DIR)"
legacy_database_path="$(env_value WATER_DATABASE_PATH)"
legacy_document_engine="$(env_value WATER_DOCUMENT_ENGINE)"
legacy_document_python="$(env_value WATER_DOCUMENT_PYTHON)"
legacy_timezone="$(env_value TZ)"
TIMEZONE="${legacy_timezone:-Asia/Shanghai}"

if [[ ! -f "$CONFIG_FILE" ]]; then
  config_http_addr="${HTTP_ADDR:-${legacy_http_addr:-:8080}}"
  config_data_dir="${legacy_data_dir:-$DATA_DIR}"
  config_database_path="${legacy_database_path:-$DATA_DIR/water.db}"
  config_document_engine="${legacy_document_engine:-native}"
  config_document_python="${legacy_document_python:-}"
  umask 077
  printf '%s\n' \
    'server:' \
    "  http_addr: \"$config_http_addr\"" \
    '' \
    'storage:' \
    "  data_dir: \"$config_data_dir\"" \
    "  database_path: \"$config_database_path\"" \
    '' \
    'document:' \
    "  engine: $config_document_engine" \
    "  python: \"$config_document_python\"" > "$CONFIG_FILE"
fi

if grep -qE '^(HOME|PATH|LD_LIBRARY_PATH|TZ|WATER_HTTP_ADDR|WATER_DATA_DIR|WATER_DATABASE_PATH|WATER_DOCUMENT_ENGINE|WATER_DOCUMENT_PYTHON|WATER_WORKSPACE_DIR|WATER_WORKSPACE_HOST_PATH|WATER_UID|WATER_GID|WATER_WEB_BIND_ADDRESS|WATER_WEB_PORT|WATER_IMAGE_TAG|WATER_BE_IMAGE_TAG|WATER_FE_IMAGE_TAG)=' "$ENV_FILE"; then
  env_temp="$(mktemp "$CONFIG_DIR/.water.env.XXXXXX")"
  awk '!/^(HOME|PATH|LD_LIBRARY_PATH|TZ|WATER_HTTP_ADDR|WATER_DATA_DIR|WATER_DATABASE_PATH|WATER_DOCUMENT_ENGINE|WATER_DOCUMENT_PYTHON|WATER_WORKSPACE_DIR|WATER_WORKSPACE_HOST_PATH|WATER_UID|WATER_GID|WATER_WEB_BIND_ADDRESS|WATER_WEB_PORT|WATER_IMAGE_TAG|WATER_BE_IMAGE_TAG|WATER_FE_IMAGE_TAG)=/' "$ENV_FILE" > "$env_temp"
  install -m 0600 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$env_temp" "$ENV_FILE"
  rm -f "$env_temp"
fi

if [[ ! -s "$ENV_FILE" ]]; then
  printf '%s\n' 'WATER_ACCESS_PIN=change-me' > "$ENV_FILE"
fi

chown "$SERVICE_USER:$SERVICE_GROUP" "$CONFIG_FILE"
chmod 0640 "$CONFIG_FILE"

service_temp="$(mktemp "$CONFIG_DIR/.water.service.XXXXXX")"
sed \
  -e "s|@SERVICE_USER@|$SERVICE_USER|g" \
  -e "s|@SERVICE_GROUP@|$SERVICE_GROUP|g" \
  -e "s|@WORKING_DIR@|$WORKSPACE_DIR|g" \
  -e "s|@ENV_FILE@|$ENV_FILE|g" \
  -e "s|@INSTALL_DIR@|$INSTALL_DIR|g" \
  -e "s|@CONFIG_FILE@|$CONFIG_FILE|g" \
  -e "s|@DATA_DIR@|$DATA_DIR|g" \
  -e "s|@TIMEZONE@|$TIMEZONE|g" \
  "$SCRIPT_DIR/water.service" > "$service_temp"
install -m 0644 "$service_temp" "$CONFIG_DIR/$SERVICE_NAME.service"
rm -f "$service_temp"
install -m 0644 "$CONFIG_DIR/$SERVICE_NAME.service" "/etc/systemd/system/$SERVICE_NAME.service"

systemctl daemon-reload
systemctl enable "$SERVICE_NAME" >/dev/null
if [[ "$START_SERVICE" -eq 1 ]]; then
  systemctl start "$SERVICE_NAME"
  systemctl --no-pager --full status "$SERVICE_NAME" || true
fi

printf '若水已安装：%s\n' "$INSTALL_DIR/water"
printf 'systemd 服务：%s\n' "$SERVICE_NAME"
printf '配置文件：%s\n' "$ENV_FILE"
printf '数据目录：%s\n' "$DATA_DIR"
printf '备份目录：%s\n' "$backup_dir"

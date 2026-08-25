#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_NAME="water"
INSTALL_DIR="/opt/water"
CONFIG_DIR="/etc/water"
ENV_FILE="$CONFIG_DIR/water.env"
DATA_DIR="/var/lib/water"
WORKSPACE_DIR="/workspace"
SERVICE_USER="water"
SERVICE_GROUP="water"
HTTP_ADDR=":8080"
ENV_SOURCE=""
START_SERVICE=1

usage() {
  cat <<'EOF'
用法：sudo ./install.sh [选项]

选项：
  --user NAME             systemd 服务用户，默认 water
  --group NAME            systemd 服务组，默认 water
  --data-dir PATH         SQLite/运行数据目录，默认 /var/lib/water
  --workspace-dir PATH    服务工作目录，默认 /workspace
  --http-addr ADDR        监听地址，默认 :8080
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
    --data-dir)
      [[ $# -ge 2 ]] || die "缺少 --data-dir 参数"
      DATA_DIR="$2"
      shift 2
      ;;
    --workspace-dir)
      [[ $# -ge 2 ]] || die "缺少 --workspace-dir 参数"
      WORKSPACE_DIR="$2"
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

[[ "${EUID:-$(id -u)}" -eq 0 ]] || die "请使用 root 或 sudo 运行。"
command -v systemctl >/dev/null 2>&1 || die "目标系统没有 systemctl。"
command -v install >/dev/null 2>&1 || die "目标系统缺少 install 命令。"
[[ -x "$SCRIPT_DIR/water" ]] || die "安装包中缺少可执行文件 water。"
[[ -f "$SCRIPT_DIR/water.service" ]] || die "安装包中缺少 water.service。"
[[ "$DATA_DIR" == /* && "$WORKSPACE_DIR" == /* ]] || die "数据目录和工作区必须使用绝对路径。"
[[ "$DATA_DIR$WORKSPACE_DIR" != *['|@']* && "$DATA_DIR$WORKSPACE_DIR" != *[[:space:]]* ]] \
  || die "数据目录和工作区暂不支持空白、| 或 @ 字符。"
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
  cat > "$ENV_FILE" <<EOF
WATER_ACCESS_PIN=change-me
WATER_HTTP_ADDR=$HTTP_ADDR
WATER_DATA_DIR=$DATA_DIR
WATER_DATABASE_PATH=$DATA_DIR/water.db
WATER_DOCUMENT_ENGINE=native
HOME=$DATA_DIR/home
PATH=/opt/water/runtime/go/bin:/opt/water/runtime/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
EOF
fi

set_env_value() {
  local key="$1"
  local value="$2"
  local temp_file
  temp_file="$(mktemp "$CONFIG_DIR/.water.env.XXXXXX")"
  awk -v key="$key" -v value="$value" '
    index($0, key "=") == 1 {
      if (!done) print key "=" value
      done = 1
      next
    }
    { print }
    END { if (!done) print key "=" value }
  ' "$ENV_FILE" > "$temp_file"
  install -m 0600 -o "$SERVICE_USER" -g "$SERVICE_GROUP" "$temp_file" "$ENV_FILE"
  rm -f "$temp_file"
}

set_env_value WATER_HTTP_ADDR "$HTTP_ADDR"
set_env_value WATER_DATA_DIR "$DATA_DIR"
set_env_value WATER_DATABASE_PATH "$DATA_DIR/water.db"
set_env_value HOME "$DATA_DIR/home"
set_env_value PATH "/opt/water/runtime/go/bin:/opt/water/runtime/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

sed \
  -e "s|@SERVICE_USER@|$SERVICE_USER|g" \
  -e "s|@SERVICE_GROUP@|$SERVICE_GROUP|g" \
  -e "s|@WORKING_DIR@|$WORKSPACE_DIR|g" \
  -e "s|@ENV_FILE@|$ENV_FILE|g" \
  -e "s|@INSTALL_DIR@|$INSTALL_DIR|g" \
  "$SCRIPT_DIR/water.service" > "$CONFIG_DIR/$SERVICE_NAME.service"
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

# Linux systemd 部署

Linux Release 包中的 `water` 已经包含 Vue 前端、HTTP API、任务 WebSocket 和终端 WebSocket。`install.sh` 会把它安装成一个 systemd 服务，不需要 Node.js、Nginx 或 Docker。

这里的“不需要”仅指若水服务本身。Agent 执行工作区的构建和测试时，宿主机仍需提供对应项目使用的 Go、Node.js、Python、Java、Git、ripgrep 等工具。

## 安装包内容

- `water`：单体可执行文件。
- `install.sh`：安装或升级服务，保留配置和数据。
- `uninstall.sh`：卸载服务，默认保留数据。
- `water.service`：systemd unit 模板。
- `water.env.example`：环境变量示例。
- `README.md`：随版本包提供的安装和使用说明。
- `PROJECT_README.md`：若水项目总览。

## 安装与升级

先解压对应架构的 Linux 包：

```bash
tar -xzf water_v0.1.2_linux_amd64.tar.gz
cd water_v0.1.2_linux_amd64
```

首次安装需要指定工作区目录。普通 Linux 使用默认 `water` 用户时，安装器会在 `useradd/groupadd` 可用时自动创建低权限账户：

```bash
sudo ./install.sh \
  --user water \
  --group water \
  --workspace-dir /srv/water/workspace \
  --data-dir /var/lib/water \
  --http-addr :8080
```

安装器会：

1. 备份旧二进制和环境文件到 `/var/backups/water/<timestamp>/`。
2. 原子替换 `/opt/water/water`。
3. 创建或保留 `/etc/water/water.env`。
4. 写入 `/etc/systemd/system/water.service` 并 enable。
5. 默认启动服务。

升级时再次执行同一命令即可。已有 `/etc/water/water.env` 不会被默认覆盖，SQLite 数据也不会被删除。

## 配置与检查

配置文件：`/etc/water/water.env`。至少应设置一个稳定的 `WATER_ACCESS_PIN`，并确认：

```ini
WATER_HTTP_ADDR=:8080
WATER_DATA_DIR=/var/lib/water
WATER_DATABASE_PATH=/var/lib/water/water.db
WATER_ACCESS_PIN=change-me
```

```bash
sudo systemctl status water --no-pager
sudo journalctl -u water -n 100 --no-pager
curl http://127.0.0.1:8080/api/health
```

## 从 Docker 迁移

迁移前先停止写入并备份 SQLite：

```bash
sudo docker compose --env-file .env down
sudo cp -a data "data.backup.$(date +%Y%m%d-%H%M%S)"
```

Docker 的 `WATER_ACCESS_PIN`、`WATER_DOCUMENT_ENGINE` 和地址配置应复制到 `/etc/water/water.env`。将 Docker 数据目录作为 systemd 的 `--data-dir`，不要重新初始化数据库。服务用户必须能够读写数据目录和工作区；安装器不会自动修改工作区所有权。

验证 systemd 服务健康后，再删除 Docker 容器和镜像。迁移失败时停止 systemd 服务，恢复数据目录备份，再执行原 Compose 文件即可回滚。

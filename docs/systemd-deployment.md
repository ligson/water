# Linux systemd 部署

Linux Release 包中的 `water` 已经包含 Vue 前端、HTTP API、任务 WebSocket 和终端 WebSocket。`install.sh` 会把它安装成一个 systemd 服务，不需要 Node.js、Nginx 或 Docker。

这里的“不需要”仅指若水服务本身。Agent 执行工作区的构建和测试时，宿主机仍需提供对应项目使用的 Go、Node.js、Python、Java、Git、ripgrep 等工具。

## Linux 兼容性

Linux 发布包中的 `water` 使用 `CGO_ENABLED=0`、`netgo` 和 `osusergo` 编译，并使用纯 Go SQLite 驱动。因此二进制本身是静态链接的，不依赖目标系统安装的 glibc，也不会因为 Synology 等老系统的 glibc 版本不同而无法启动。可以在目标机上检查：

```bash
file /opt/water/water
ldd /opt/water/water
```

`file` 应显示 `statically linked`，`ldd` 应显示 `not a dynamic executable` 或等价结果。

这只保证若水服务二进制本身的兼容性。Agent 执行用户项目时调用的 Go、Node.js、Python、Git、ripgrep 等外部工具仍然可能依赖宿主机的系统库，需要按目标机器准备兼容版本。不要直接把另一台容器里的动态链接工具复制到老系统上；例如 `jq` 可能依赖更高版本的 glibc。

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

直接在 Release 包解压目录执行 `install.sh` 时，二进制、配置和数据默认都使用当前目录，不需要重复传安装目录参数：

```bash
sudo ./install.sh
```

默认数据目录为当前目录下的 `data/`，新安装默认监听 `:8080`。升级时若当前目录已有 `water.env`，会保留其中的 PIN、监听地址和其他配置。

如果 Agent 工作区不是当前 Release 包目录，只需额外指定 `--workspace-dir`。使用 `sudo ./install.sh` 时，若 `SUDO_USER` 已存在，安装器默认使用该用户及其主组；普通 Linux 没有现成用户时，才会回退到 `water` 用户，并在 `useradd/groupadd` 可用时自动创建低权限账户。

安装器支持 `--install-dir` 和 `--config-dir`。如果希望二进制、运行时工具、配置和数据全部归档到一个部署目录，可将两者设为同一路径：

```bash
sudo ./install.sh \
  --user ligson \
  --group users \
  --install-dir /srv/water \
  --config-dir /srv/water \
  --workspace-dir /srv/water/workspace \
  --data-dir /srv/water/data \
  --http-addr :8080 \
  --env-file /path/to/water.env
```

升级时继续使用相同的目录参数；安装器会保留已有 `water.env` 和 SQLite 数据。

安装器会：

1. 备份旧二进制和环境文件到 `/var/backups/water/<timestamp>/`。
2. 原子替换 `--install-dir/water`。
3. 创建或保留 `--config-dir/water.env`。
4. 写入 `/etc/systemd/system/water.service` 并 enable。
5. 默认启动服务。

升级时再次执行同一命令即可。已有 `water.env` 不会被默认覆盖，SQLite 数据也不会被删除。

## 配置与检查

配置文件：当前安装目录下的 `water.env`（或 `--config-dir/water.env`）。至少应设置一个稳定的 `WATER_ACCESS_PIN`，并确认：

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

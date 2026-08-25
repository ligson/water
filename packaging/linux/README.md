# 若水 Linux 单体安装包

这个目录包含一个已经把 Vue 前端嵌入其中的 `water` Go 二进制，以及 Linux systemd 安装文件。

## 安装

需要 root 权限，并且目标系统需要 systemd。直接在 Release 包解压目录执行时，安装器默认把二进制、配置和数据放在当前目录，不需要重复传目录参数：

```bash
sudo ./install.sh
```

默认目录结构为：`water`、`water.env`、`runtime/` 和 `data/`。新安装默认监听 `:8080`；如果目录中已有 `water.env`，升级时会保留其中的监听地址、PIN 和 `WATER_WORKSPACE_DIR`（也兼容旧 Docker 配置中的 `WATER_WORKSPACE_HOST_PATH`）。

若目标系统已经有运行用户，可以直接指定现有用户和组。安装器不会自动修改工作区所有权：

```bash
sudo ./install.sh \
  --user nas-user \
  --group users \
  --workspace-dir /volume1/homes/nas-user/workspace \
  --http-addr :13013 \
  --env-file /path/to/existing/water.env
```

使用 `sudo ./install.sh` 时，若 `SUDO_USER` 已存在，安装器默认使用该用户及其主组；也可以显式指定 `--user` 和 `--group`。`--env-file` 只在安装时复制到当前配置目录的 `water.env`，权限为服务用户可读的私有文件。升级时默认保留已有配置和数据库。

如果需要把 Release 包安装到另一个目录，才需要指定目录参数：

```bash
sudo ./install.sh \
  --user ligson \
  --group users \
  --install-dir /srv/water \
  --config-dir /srv/water \
  --workspace-dir /srv/water/workspace \
  --data-dir /srv/water/data \
  --http-addr :13013 \
  --env-file /path/to/existing/water.env
```

升级时继续在同一个 Release 包目录执行即可；安装器会保留已有 `water.env` 和 SQLite 数据。

安装完成后：

```bash
systemctl status water --no-pager
systemctl is-enabled water
curl http://127.0.0.1:8080/api/health
```

打开 `http://<服务器地址>:8080` 访问若水工作台。请在配置中设置 `WATER_ACCESS_PIN`，不要把 PIN 写进 Git 或公开日志。

`water` 自身启动不依赖 Node.js、Go 或 Python；但 Agent 要编译、测试用户项目时，仍需要服务主机安装对应项目工具。systemd 的默认 PATH 包含 `<install-dir>/runtime/go/bin`、`<install-dir>/runtime/bin`、`/usr/local/bin` 和系统目录，可按主机环境在 `water.env` 中调整。

## 常用操作

```bash
sudo systemctl restart water
sudo journalctl -u water -f
sudo systemctl stop water
```

## 卸载

普通卸载会停止服务并移除 unit、二进制和配置目录，保留数据目录：

```bash
sudo ./uninstall.sh
```

确认不再需要数据和配置时才使用：

```bash
sudo ./uninstall.sh --purge
```

使用自定义目录时，卸载也要传入相同的 `--install-dir` 和 `--config-dir`。

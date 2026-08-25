# Docker 部署

若水现在由一个容器组成：Go 二进制同时提供 API、任务/终端 WebSocket 和内嵌的 Vue 静态页面。SQLite、Skills、会话和 Provider 配置保存在宿主机数据目录中，不写入镜像。

## 安全边界

- 真实 `.env`、访问 PIN、SQLite、日志和镜像归档不得进入 Git。
- 只挂载明确的工作区目录，不挂载宿主机根目录、Docker Socket、SSH 私钥目录或其他用户目录。
- 后端使用宿主机工作区所有者的 UID/GID 运行，避免生成 root 所有的项目文件。
- `完全访问` 只代表若水可在已挂载的工作区内执行操作，不会获得容器外路径。
- 生产环境应通过 HTTPS 反向代理访问；直接映射端口适合可信内网。

## 构建镜像

在仓库根目录执行：

```bash
docker build --platform linux/amd64 --build-arg VERSION=dev -f water-be/docker/Dockerfile -t ligson/water:latest .
```

镜像构建阶段会先执行前端 `npm run build`，再把 `dist/` 嵌入 Go 二进制。运行镜像预置 Go、Node.js、npm、Python 3、Git、SSH client、curl、ripgrep 和常用编译工具。项目额外依赖仍应由对应工作区自行声明和安装。

## 部署目录

在部署主机创建独立目录，并仅在主机上生成 `.env`：

```text
water/
├── docker-compose.yml
├── .env                 # 权限 600，不进入 Git
└── data/                # SQLite、Skills、运行数据，不进入 Git
```

可从 `docker/.env.example` 开始配置。必须填写：

- `WATER_ACCESS_PIN`：建议使用至少 16 位随机值。
- `WATER_WORKSPACE_HOST_PATH`：允许若水操作的宿主机工作区绝对路径。
- `WATER_UID` / `WATER_GID`：该工作区所有者的数字 UID/GID。

启动和检查：

```bash
docker compose --env-file .env -f docker-compose.yml up -d
docker compose --env-file .env -f docker-compose.yml ps
curl -fsS http://127.0.0.1:13013/api/health
```

默认只允许部署主机通过 `http://127.0.0.1:13013` 访问。可信内网可显式设置 `WATER_WEB_BIND_ADDRESS=0.0.0.0` 后访问 `http://<部署主机>:13013`；跨主机或公网使用时，应保持本机绑定并通过 HTTPS 反向代理访问。单体 Go 服务直接处理 `/ws/` WebSocket 长连接；若前面还有反向代理，需透传 Upgrade、关闭代理缓冲并放宽长连接超时。

模型服务位于 NAS 宿主机时，Provider 地址可使用 `http://host.docker.internal:<端口>/v1`；模型服务位于同一个 Docker 网络时，使用对应容器服务名。不要在容器中使用 `127.0.0.1` 指向宿主机模型。

## 升级与备份

升级前至少备份 `data/` 与远端 Compose；镜像使用不可变时间戳或提交号标签，验证通过后再删除旧镜像。恢复时停止容器、还原 `data/` 和镜像标签，然后重新启动。

不要把 `.env` 或 `data/` 打进部署压缩包。跨机器传输推荐只传 Docker 镜像归档与通用 Compose 文件，私有配置在目标机单独维护。

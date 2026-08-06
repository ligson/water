# 开发脚本

这些脚本用于本地开发期启动、停止和查看 Water 前后端服务状态。

## 常用命令

```bash
./scripts/start-all.sh
./scripts/start-backend.sh
./scripts/start-frontend.sh
./scripts/status.sh
./scripts/stop.sh
```

默认地址：

- 后端：`http://127.0.0.1:8080`
- 前端：`http://127.0.0.1:5173`

运行时文件：

- PID：`scripts/run/`
- 日志：`scripts/logs/`

## 可选环境变量

- `WATER_HTTP_ADDR`：后端监听地址，默认 `:8080`
- `WATER_FE_HOST`：前端开发服务 host，默认 `127.0.0.1`
- `WATER_FE_PORT`：前端开发服务端口，默认 `5173`
- `VITE_API_BASE`：前端连接的后端地址，默认跟随 `WATER_HTTP_ADDR`

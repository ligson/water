# 开发脚本

这些脚本用于本地开发期启动、停止和查看 Water 前后端服务状态；生产单体构建使用 `build-single-binary.sh`。

## 常用命令

```bash
./scripts/start-all.sh
./scripts/start-backend.sh
./scripts/start-frontend.sh
./scripts/status.sh
./scripts/stop.sh
./scripts/build-single-binary.sh dev water-be/bin/water
```

可选文档增强运行时：

```bash
./scripts/setup-document-runtime.sh
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
- 单体模式不需要 `VITE_API_BASE`：前端由同一个 Go 服务提供，API 和 WebSocket 使用当前页面的同源地址。
- `WATER_DOCUMENT_ENGINE`：文档引擎，默认 `native`；仅显式设为 `markitdown` 时使用可选 Python 运行时
- `WATER_DOCUMENT_BOOTSTRAP_PYTHON`：安装可选文档增强运行时时使用的 Python 3.10-3.13
- `WATER_DOCUMENT_VENV`：可选文档增强运行时安装目录，默认 `water-be/.venv-document`

DOCX、XLSX、PPTX 和带文本层 PDF 默认由 Go 内置解析器读取，不依赖 Python。`setup-document-runtime.sh` 只安装 Microsoft MarkItDown 可选增强能力，主要用于旧 XLS 或解析效果对比；安装时需要联网，安装后可离线使用。

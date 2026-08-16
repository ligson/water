# Water Backend

Water 后端使用 Go 构建，第一阶段目标是提供最小 HTTP 服务、SQLite 存储、统一响应 envelope 和自动 migration。

## 启动

```bash
go run ./cmd/water
```

默认配置：

- HTTP 地址：`:8080`
- 数据目录：`data`
- SQLite 数据库：`data/water.db`

可通过环境变量覆盖：

- `WATER_HTTP_ADDR`
- `WATER_DATA_DIR`
- `WATER_DATABASE_PATH`
- `WATER_ACCESS_PIN`

## 访问 PIN

后端默认启用单用户访问 PIN。首次启动时，如果没有设置 `WATER_ACCESS_PIN`，服务会自动生成一个初始 PIN 并在启动日志中输出一次；用户解锁后，前端会保存短期 session token，用于后续 HTTP API 和 WebSocket 连接。

固定 PIN 启动：

```bash
WATER_ACCESS_PIN=123456 go run ./cmd/water
```

通过 `WATER_ACCESS_PIN` 重新启动会重置本地 PIN，并清理旧 session。前端设置页也可以在已解锁状态下修改 PIN。

PIN 校验带有持久化递增锁定：连续错误第 3 次锁定 1 分钟，第 4 次锁定 5 分钟，第 5 次锁定 15 分钟，第 6 次及以后每次锁定 30 分钟。锁定期间即使提交正确 PIN 也会返回 HTTP `429`，响应包含 `Retry-After`、`retryAfterSeconds` 和 `lockedUntil`；成功解锁或成功修改 PIN 后清零失败次数。失败次数和锁定截止时间保存在 SQLite 中，刷新页面或重启服务不会绕过限制。

## 健康检查

```bash
curl http://localhost:8080/api/health
```

响应使用统一 envelope：

```json
{
  "success": true,
  "requestId": "req_xxx",
  "message": "ok",
  "httpCode": 200,
  "data": {}
}
```

## Provider API

Provider API 使用统一 envelope，API Key 存储在 SQLite 中但不会在响应 JSON 中返回明文。

```bash
curl -X POST http://localhost:8080/api/providers \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Local Ollama",
    "type": "openai-compatible",
    "baseUrl": "http://localhost:11434/v1",
    "model": "qwen2.5-coder:7b",
    "apiKey": "",
    "contextWindowTokens": 8192,
    "timeoutMs": 30000,
    "streamIdleTimeoutMs": 60000,
    "isDefault": true
  }'
```

`contextWindowTokens` 表示该模型的上下文窗口长度，默认 `8192`。Agent 构建 Context Pack 时默认使用其中 80% 作为输入预算，剩余部分留给回复、工具结果回填和 tokenizer 估算误差。

`timeoutMs` 表示连接模型服务及等待响应头的超时，默认 30 秒；它不限制 SSE 流的总持续时间。`streamIdleTimeoutMs` 表示流建立后连续没有收到任何数据的最长时间，默认 60 秒，用于识别模型服务卡死。两项都可以在前端 Provider 弹窗的“高级参数”中调整。

常用接口：

- `GET /api/providers`
- `POST /api/providers`
- `GET /api/providers/{id}`
- `PUT /api/providers/{id}`
- `DELETE /api/providers/{id}`
- `POST /api/providers/{id}/default`
- `POST /api/providers/{id}/test`
- `POST /api/provider-models`：根据已保存 Provider 或当前表单里的 `baseUrl` / `apiKey` 调用 OpenAI-compatible `/models`，返回可选模型列表。

## Workspace API

```bash
curl -X POST http://localhost:8080/api/workspaces \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Water",
    "rootPath": "/Users/me/workspace/water",
    "permissionMode": "request_approval",
    "trusted": true
  }'
```

常用接口：

- `GET /api/workspaces`
- `POST /api/workspaces`
- `GET /api/workspaces/{id}`
- `PUT /api/workspaces/{id}`
- `DELETE /api/workspaces/{id}`
- `GET /api/workspaces/{id}/files?path=relative/path`
- `GET /api/workspaces/{id}/files/content?path=relative/file`
- `GET /api/workspaces/{id}/external-paths`
- `POST /api/workspaces/{id}/external-paths`
- `DELETE /api/workspaces/{id}/external-paths/{pathId}`

外部路径授权支持：

- `pathType`: `file` 或 `directory`
- `accessMode`: `read` 或 `write`

工作区文件接口只允许读取当前工作区根路径下的相对路径；后端会拒绝绝对路径、`..` 逃逸路径和软链接逃逸路径。文件内容预览最多返回 512 KiB，超过时会标记 `truncated: true`。

## Task API

```bash
curl -X POST http://localhost:8080/api/workspaces/{workspaceId}/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title": "实现 Provider 配置页"}'
```

常用接口：

- `GET /api/workspaces/{workspaceId}/tasks`
- `POST /api/workspaces/{workspaceId}/tasks`
- `GET /api/tasks/{taskId}`
- `PUT /api/tasks/{taskId}`
- `DELETE /api/tasks/{taskId}`
- `POST /api/tasks/{taskId}/turns`
- `GET /api/tasks/{taskId}/attachments?id={attachmentId}`
- `GET /api/tasks/{taskId}/events`
- `POST /api/tasks/{taskId}/cancel`

Skill 管理接口：

- `GET /api/skills`
- `POST /api/skills`：使用 multipart `file` 上传 ZIP；安装后默认停用
- `POST /api/skills/install`：JSON `{ "url": "https://.../skill.zip" }`
- `POST /api/skills/{id}/enable`
- `POST /api/skills/{id}/disable`
- `DELETE /api/skills/{id}`

Skill 包必须包含同一目录下的 `skill.json` 和 `SKILL.md`。Agent 上下文只常驻已启用 Skill 的轻量目录，匹配任务时通过 `read_skill` 按需加载完整工作流；普通 Skill 不执行包内程序。完整格式和安全边界见 `docs/skills.md`。

删除任务会先取消该任务当前运行中的 Agent Turn，然后硬删除 `tasks` 记录；该任务下的 Turns、Events、Approvals、Task Summaries 和 Pinned Contexts 依赖 SQLite 外键级联清理。

创建 Turn 时可携带附件。单个附件最大 8 MiB，每轮最多 6 个、合计最大 20 MiB；附件使用 base64 data URL 传入，后端写入当前工作区的 `.water/attachments/{taskId}/` 并将该目录加入 Git 本地 exclude，避免误提交：

```json
{
  "userInput": "分析这张截图里的错误",
  "attachments": [
    {
      "name": "error.png",
      "mimeType": "image/png",
      "dataUrl": "data:image/png;base64,..."
    }
  ]
}
```

支持视觉的 OpenAI-compatible 模型会收到标准 `image_url` 内容块；文本/代码使用 `read_file`。DOCX、XLSX、PPTX 和带文本层 PDF 默认由 Go 内置的 `water-native` 引擎离线提取，无需在部署机器安装 Python、Office 或额外命令。

旧 XLS 或需要对比增强解析效果时，可选安装 MarkItDown。这个步骤不是若水基础部署的前置条件：

```bash
./scripts/setup-document-runtime.sh
```

安装后设置 `WATER_DOCUMENT_ENGINE=markitdown` 才会启用该引擎，也可以用 `WATER_DOCUMENT_PYTHON` 指定解释器。扫描 PDF OCR 和旧 DOC/PPT 暂未内置，建议先离线转换为 PDF 或新版 Office 格式。

`read_document` 默认返回 24576 个字符，最多 65536 个字符；结果截断时返回 `nextOffset`，Agent 会按需继续读取。单个文档安全上限为 50 MiB。解析器只接受 Harness 校验后的本地工作区或已授权路径；MarkItDown 模式关闭插件和网络 URI。

## Task WebSocket

任务实时事件流使用 WebSocket，不套 HTTP envelope。

```text
GET /ws/tasks/{taskId}
```

连接成功后，后端会先回放该任务已有事件，再推送新的实时事件。重连时可带上最后已处理事件序号，后端只回放该序号之后的事件：

```text
GET /ws/tasks/{taskId}?afterSequence=128
```

WebSocket 连接由后端定时发送 ping 控制帧，浏览器或客户端需回复 pong；后端收到 pong 后延长读超时。如果连接异常关闭，前端会按退避策略自动重连，并通过 `afterSequence` 补拉断线期间已经落库的事件。前端仍按 `eventId` 去重，避免重复回放造成消息重复。

事件结构示例：

```json
{
  "eventId": "evt_xxx",
  "type": "turn.started",
  "requestId": "req_xxx",
  "workspaceId": "ws_xxx",
  "taskId": "task_xxx",
  "turnId": "turn_xxx",
  "sequence": 2,
  "createdAt": "2026-08-05T10:00:00+08:00",
  "payload": {}
}
```

## LLM 与 Agent Loop

Provider 使用 OpenAI-compatible `/chat/completions`。创建 Turn 后，后端会异步启动最小 Agent Loop：

```bash
curl -X POST http://localhost:8080/api/tasks/{taskId}/turns \
  -H 'Content-Type: application/json' \
  -d '{"userInput": "帮我看一下这个项目"}'
```

后端会把模型流式输出写入任务事件：

- `turn.started`
- `context.pack.built`
- `agent.message.delta`
- `agent.message.completed`
- `agent.tool_calls.detected`
- `approval.requested`
- `approval.resolved`
- `approval.continuation.started`
- `tool.call.started`
- `tool.completed`
- `tool.failed`
- `turn.summary`
- `turn.completed`
- `turn.failed`
- `turn.interrupted`

模型返回 `tool_calls` 时，后端会通过 Harness 执行工具或生成审批请求；Agent Prompt 会携带后端内部构建的 Context Pack 基础信息。
`context.pack.built` 会返回本轮估算上下文用量、预算、模型上下文窗口和是否截断，供前端展示“上下文约 X / Y tokens”。
`turn.summary` 会在 Turn 完成前汇总本轮工具产物，包括 `write_file` 产生的文件变更、增删行统计，以及 `run_command` 产生的命令/验证结果。前端聊天窗口用它展示类似 Codex 的任务产物卡。

## Tool 与 Approval API

工具统一通过任务接口执行：

```bash
curl -X POST http://localhost:8080/api/tasks/{taskId}/tools \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "read_file",
    "arguments": {"path": "/Users/me/workspace/water/README.md"}
  }'
```

第一版工具：

- `list_dir`
- `read_file`
- `read_document`
- `read_skill`
- `write_file`
- `run_command`

请求审批模式下，`write_file` 和 `run_command` 会返回 `202 Accepted` 和审批单。批准审批：

```bash
curl -X POST http://localhost:8080/api/approvals/{approvalId}/resolve \
  -H 'Content-Type: application/json' \
  -d '{"status": "approved", "message": "同意"}'
```

查询工作区审批：

```bash
curl http://localhost:8080/api/workspaces/{workspaceId}/approvals?status=pending
```

Agent Loop 中产生的审批会保存后端内部工具请求快照。用户批准后，后端会自动恢复原工具调用，将工具结果回填给模型继续推理；用户拒绝后，当前 Turn 会写入 `turn.interrupted`，前端显示为已中断。

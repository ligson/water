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
    "isDefault": true
  }'
```

`contextWindowTokens` 表示该模型的上下文窗口长度，默认 `8192`。Agent 构建 Context Pack 时默认使用其中 80% 作为输入预算，剩余部分留给回复、工具结果回填和 tokenizer 估算误差。

常用接口：

- `GET /api/providers`
- `POST /api/providers`
- `GET /api/providers/{id}`
- `PUT /api/providers/{id}`
- `DELETE /api/providers/{id}`
- `POST /api/providers/{id}/default`
- `POST /api/providers/{id}/test`

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
- `GET /api/workspaces/{id}/external-paths`
- `POST /api/workspaces/{id}/external-paths`
- `DELETE /api/workspaces/{id}/external-paths/{pathId}`

外部路径授权支持：

- `pathType`: `file` 或 `directory`
- `accessMode`: `read` 或 `write`

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
- `GET /api/tasks/{taskId}/events`
- `POST /api/tasks/{taskId}/cancel`

删除任务会先取消该任务当前运行中的 Agent Turn，然后硬删除 `tasks` 记录；该任务下的 Turns、Events、Approvals、Task Summaries 和 Pinned Contexts 依赖 SQLite 外键级联清理。

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
- `turn.completed`
- `turn.failed`

模型返回 `tool_calls` 时，后端会通过 Harness 执行工具或生成审批请求；Agent Prompt 会携带后端内部构建的 Context Pack 基础信息。
`context.pack.built` 会返回本轮估算上下文用量、预算、模型上下文窗口和是否截断，供前端展示“上下文约 X / Y tokens”。

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

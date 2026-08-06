# Water MVP 实施路线图

本文档是 Water 从当前状态到 MVP 完成的具体施工清单。它比 `docs/mvp-plan.md` 更细，后续开发按本文档逐项推进。

## 1. 当前完成状态

已完成：

- 后端工程 `water-be` 初始化。
- HTTP 服务入口。
- 统一 JSON envelope。
- `requestId` 中间件。
- SQLite 初始化。
- goose migration。
- 基础数据表。
- `/api/health`。
- Provider CRUD。
- Provider 默认切换。
- Provider 连接测试。
- Provider API Key 响应脱敏。
- Workspace CRUD。
- Workspace 外部路径授权 CRUD。
- Task / Turn / Event 后端主链路。
- WebSocket 任务事件流。
- OpenAI-compatible Chat / ChatStream 抽象。
- Agent Loop 最小版。
- Harness 权限引擎与只读工具。
- Approval 与写文件工具。
- Context Pack 与摘要缓存基础能力。
- 前端 `water-fe` MVP。
- 后端基础测试。

未开始：

- 无阻塞 MVP 主链路的问题。

## 2. 总体顺序

推荐继续按以下顺序做：

```text
Task / Turn / Event
 -> WebSocket Event Stream
 -> LLM Chat / ChatStream
 -> Agent Loop 最小版
 -> Tool Registry + 只读工具
 -> Approval + 写文件工具
 -> 命令执行审批
 -> Context Pack / 摘要缓存
 -> 前端 Provider / Workspace / Task 页面
 -> 前后端联调
```

## 3. 后端任务清单

### 3.1 Task / Turn / Event

目标：建立任务主链路，让工作区下可以创建任务、追加 Turn、保存事件。

新增包：

```text
internal/task
internal/event
```

建议接口：

- `GET /api/workspaces/{workspaceId}/tasks`
- `POST /api/workspaces/{workspaceId}/tasks`
- `GET /api/tasks/{taskId}`
- `DELETE /api/tasks/{taskId}`
- `POST /api/tasks/{taskId}/turns`
- `GET /api/tasks/{taskId}/events`

数据行为：

- Task 必须归属于 Workspace。
- Turn 必须归属于 Task。
- Event 必须支持 `eventId`、`requestId`、`workspaceId`、`taskId`、`turnId`、`sequence`、`type`、`payloadJson`、`createdAt`。
- `GET /events` 按 `sequence` 升序返回。
- 空列表返回 `[]`。

测试：

- 创建 Task。
- 创建 Turn。
- 写入 Event。
- 读取 Task 事件流历史。
- 删除 Workspace 时级联删除 Task / Turn / Event。

验收：

- 后端能完整保存一段任务过程，即使还没有模型调用。

### 3.2 WebSocket 任务事件流

状态：已完成。

目标：浏览器能订阅任务事件，后端能推送结构化事件。

新增包：

```text
internal/realtime
```

建议接口：

- `GET /ws/tasks/{taskId}`

事件结构：

```json
{
  "eventId": "evt_xxx",
  "type": "agent.message.delta",
  "requestId": "xxx",
  "workspaceId": "xxx",
  "taskId": "xxx",
  "turnId": "xxx",
  "sequence": 1,
  "createdAt": "2026-08-05T10:00:00+08:00",
  "payload": {}
}
```

任务：

- 建立 taskId -> subscribers 映射。
- Event 写入 SQLite 后广播。
- 客户端断线不影响任务历史。
- WebSocket 只传事件，不使用 HTTP envelope。

测试：

- 客户端连接后收到新事件。
- 多客户端订阅同一任务。
- 断线后可通过 HTTP 读取历史事件。

验收：

- 前端未来可以只靠 WebSocket 实时展示任务过程。

当前实现：

- `GET /ws/tasks/{taskId}` 建立 WebSocket 连接。
- 连接成功后先回放该任务历史事件，再订阅实时事件。
- Task / Turn 事件写入 SQLite 后会广播给当前订阅者。
- 同一任务支持多个客户端订阅。
- WebSocket 只发送事件对象，不使用 HTTP envelope。

### 3.3 OpenAI-compatible LLM Client

状态：已完成。

目标：Provider 不只做连接测试，而是能真实 Chat / ChatStream。

新增或扩展包：

```text
internal/llm
```

核心接口：

```go
type Client interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
    ChatStream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error)
}
```

任务：

- 支持 `/chat/completions` 非流式。
- 支持 SSE/HTTP chunk 流式。
- 支持系统消息、用户消息、工具结果消息。
- 支持原生 `tool_calls` 的解析。
- API Key 从 Provider 读取，不输出到日志。

测试：

- 使用 `httptest.Server` 模拟 OpenAI-compatible 服务。
- 测试非流式解析。
- 测试流式 delta 解析。
- 测试错误响应。

验收：

- 后端可以用 SQLite Provider 调通本地模型接口。

当前实现：

- 支持 OpenAI-compatible `/chat/completions` 非流式请求。
- 支持 SSE 流式增量解析。
- 支持系统、用户、助手、工具消息结构和 `tool_calls` 字段。
- Provider 连接测试复用真实 Chat Client。

### 3.4 Agent Loop 最小版

状态：已完成。

目标：用户创建 Turn 后，后端能调用模型并把增量文本写入 Event。

新增包：

```text
internal/agent
```

流程：

```text
POST /api/tasks/{taskId}/turns
 -> 创建 Turn
 -> 写入 turn.started
 -> 加载 Workspace 和 Provider
 -> 构造最小消息上下文
 -> 调用 ChatStream
 -> 写入 agent.message.delta
 -> 写入 agent.message.completed
 -> 写入 turn.completed
```

暂不做：

- 工具调用。
- 上下文压缩。
- 审批。

测试：

- 使用假 LLM client 返回 delta。
- 验证事件顺序。
- 验证 Turn 状态更新。

验收：

- 纯聊天闭环跑通。

当前实现：

- `POST /api/tasks/{taskId}/turns` 创建 Turn 后异步启动 Agent Loop。
- Agent Loop 加载工作区默认 Provider，调用 ChatStream。
- 流式输出写入 `agent.message.delta`、`agent.message.completed`、`turn.completed`。
- 失败时写入 `turn.failed`。
- Agent Prompt 已接入 Context Pack 基础信息。
- 模型返回 `tool_calls` 时会通过 Harness 执行工具或生成审批请求。

### 3.5 Harness + Tool Registry

状态：已完成。

目标：模型不能直接执行动作，必须由 Harness 校验。

新增包：

```text
internal/harness
internal/tools
internal/sandbox
```

核心组件：

- `ToolRegistry`
- `PermissionEngine`
- `Executor`
- `AuditLogger`

第一批工具：

- `list_dir`
- `read_file`

权限规则：

- 工作区内只读工具自动允许。
- 工作区外路径需要已授权外部路径。
- 路径必须规范化，防止 `..` 越界。

测试：

- 工作区内读文件成功。
- 工作区外未授权拒绝。
- 工作区外已授权允许。
- 目录遍历被拦截。

验收：

- Agent 可以申请只读工具，Harness 能安全执行。

当前实现：

- `POST /api/tasks/{taskId}/tools` 统一执行工具。
- 已支持 `list_dir`、`read_file`、`write_file`、`run_command`。
- 工作区内路径按权限模式执行；工作区外路径必须先授权。
- 路径校验使用规范化绝对路径，避免 `..` 越界。

### 3.6 Approval + 写文件工具

状态：已完成。

目标：写文件需要审批闭环。

新增或扩展：

```text
internal/approval
internal/tools/write_file
```

接口：

- `GET /api/workspaces/{workspaceId}/approvals`
- `POST /api/approvals/{approvalId}/resolve`
- `POST /api/tasks/{taskId}/tools`

事件：

- `approval.requested`
- `approval.resolved`
- `tool.completed`
- `tool.failed`

任务：

- 生成简单变更摘要。
- 对代码/文本文件尽量生成简单 diff。
- 用户批准后执行写入。
- 用户拒绝后把拒绝结果回填给 Agent。

测试：

- 请求审批模式写文件生成 approval。
- 批准后文件写入。
- 拒绝后文件不变。
- 完全访问模式下工作区内自动写入。

验收：

- 写文件可见、可审批、可审计。

当前实现：

- 请求审批模式下 `write_file` 会生成 pending approval，文件不会立即写入。
- 用户通过 `/api/approvals/{approvalId}/resolve` 批准后，可带 `approvalId` 重放工具请求完成写入。
- 完全访问模式下工作区内写文件可自动执行。
- 审批请求、审批决策和工具结果都会写入任务事件流。

### 3.7 命令执行审批

状态：已完成 MVP。

目标：支持命令执行，但默认保守。

工具：

- `run_command`

任务：

- context timeout。
- 可取消。
- stdout/stderr 流式事件。
- 输出截断与摘要。
- 请求审批模式下等待用户决定。
- 完全访问模式下工作区内可自动执行。

测试：

- 命令输出事件。
- 超时取消。
- 拒绝后不执行。
- 输出截断。

验收：

- 用户能看到命令执行全过程并可中断。

当前实现：

- `run_command` 已接入统一工具接口。
- 请求审批模式下需要 approval；完全访问模式下可直接执行。
- 命令在工作区目录或已授权目录下运行。
- 支持超时和输出截断。
- stdout/stderr 第一版合并为工具结果输出，暂未做逐行流式事件和中断接口。

### 3.8 Context Pack 与摘要缓存

状态：已完成基础能力。

目标：让本地短上下文模型好用。

新增包：

```text
internal/contextpack
```

任务：

- 文件摘要缓存。
- 工作区轻量索引。
- 任务滚动摘要。
- 钉住上下文。
- Context Pack 预算 80%。
- 系统预置压缩模板。

接口：

- 第一版先作为后端内部能力，不额外开放 HTTP 接口。
- 后续前端需要手动压缩或钉住上下文时再补 API。

测试：

- 文件 hash 不变复用摘要。
- 长工具输出被摘要。
- 钉住文件超预算时裁剪。
- 手动压缩生成任务摘要。

验收：

- Agent 不再把完整仓库或长日志直接塞给模型。

当前实现：

- 支持文件摘要与任务摘要 upsert / 读取。
- Context Pack Builder 按 80% 上下文预算组织系统说明、任务摘要、文件摘要和当前输入。
- 支持钉住文件优先排序和超预算截断。

## 4. 前端任务清单

### 4.1 前端初始化

状态：已完成 MVP。

目标：创建 `water-fe`。

技术：

- Vue 3。
- TypeScript。
- Vite。
- Ant Design Vue。

任务：

- 初始化路由。
- 初始化 Ant Design Vue。
- 建立 API client。
- 统一解析 HTTP envelope。
- 建立基础布局。

验收：

- 页面可构建，并能通过 API client 访问后端 envelope 接口。

### 4.2 Provider 页面

状态：已完成 MVP。

页面：

- Provider 列表。
- 新建/编辑弹窗或抽屉。
- 测试连接。
- 设置默认。

字段：

- 名称。
- Base URL。
- Model。
- API Key。
- 是否默认。
- 高级配置折叠。

验收：

- 用户能完整配置本地模型 Provider。

当前实现：

- 支持 Provider 创建、列表展示和连接测试。
- API Key 使用密码框录入。
- 第一版暂未做编辑抽屉和高级配置折叠。

### 4.3 Workspace 页面

状态：已完成 MVP。

页面：

- 工作区列表。
- 新建/编辑工作区。
- 权限模式选择。
- 默认 Provider 选择。
- 外部路径授权管理。

验收：

- 用户能创建多个工作区。
- 用户能查看和撤销外部路径授权。

当前实现：

- 支持工作区创建、切换和权限模式选择。
- 新建工作区会默认绑定当前默认 Provider。
- 支持外部路径授权创建、查看和撤销。

### 4.4 Task 页面

状态：已完成 MVP。

页面：

- 左侧任务列表。
- 中间对话区。
- 右侧事件/工具面板。
- WebSocket 连接状态。
- 打断按钮。

验收：

- 用户能创建任务、发送消息、看到流式回复和事件。

当前实现：

- 支持任务创建、任务切换、发送 Turn。
- 支持任务事件历史加载和 WebSocket 实时订阅。
- 支持展示 Agent 流式回复和连接状态。
- 支持当前任务打断。

### 4.5 Approval UI

状态：已完成 MVP。

页面组件：

- 审批抽屉或弹窗。
- 风险说明。
- 目标路径或命令。
- 简单 diff / 摘要。
- 批准 / 拒绝。

验收：

- 写文件和命令执行都能经过审批 UI。

当前实现：

- 支持 pending approval 列表。
- 支持批准与拒绝。
- 支持展示目标、风险说明和预期影响。
- 写文件 diff/摘要展示后续增强。

## 5. 当前下一步

当前最应该继续做：

1. 增强写文件审批 diff/摘要。
2. 增加命令输出逐行流式事件和前端展示。
3. 增强 Agent 工具调用多轮回填，让模型能基于工具结果继续推理。
4. 增加 Context Pack 手动压缩和钉住上下文 API/UI。
5. 增加更完整的端到端浏览器测试。

当前 MVP 主链路已经具备 Provider 配置、工作区、任务、事件流、模型流式回复、工具审批、Context Pack 基础能力和前端工作台。

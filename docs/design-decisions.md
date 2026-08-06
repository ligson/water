# Water 设计决策记录

本文档记录若水（Water）当前已经讨论清楚的产品与架构决策。后续如果设计改变，优先更新本文档，并同步更新根目录 `CHANGELOG.md`。

## 1. 产品定位

Water 是一个单用户、可自托管、内网/离线可用的 AI 编程智能体。

它不是简单的网页聊天壳，也不是让模型直接拥有系统权限的自动化工具。Water 的核心是 Harness：通过工具定义、权限模式、审批流、工作区边界、事件审计和可中断执行，把本地或内网大模型变成可控的编程助手。

## 2. 工程结构

采用前后端分离，但保持部署思路简单。

```text
water/
├── water-be/              # Go 后端
├── water-fe/              # Vue 前端
├── docs/                  # 设计文档
├── CHANGELOG.md
├── AGENTS.md
└── README.md
```

### 后端

- 语言：Go。
- 数据库：SQLite。
- 职责：Agent Loop、Provider 调用、工具执行、权限判断、审批状态、审计日志、HTTP/WebSocket API。
- 不引入 Node.js 或 Python 作为后端主服务。

### 前端

- 框架：Vue 3 + TypeScript + Vite。
- 组件库：Ant Design Vue。
- 职责：工作区管理、模型 Provider 配置、对话任务、流式事件展示、工具调用展示、审批与打断。

## 3. Provider 配置

第一版只实现 OpenAI-compatible Provider，但配置结构必须支持多个 Provider，避免把模型服务写死。

### 页面配置原则

Provider 配置页面要简单，普通用户优先看到少量必要字段：

- Provider 名称。
- 类型：第一版固定为 `OpenAI Compatible`。
- Base URL。
- Model。
- API Key：可为空；页面默认脱敏展示。
- 设为默认。
- 测试连接。

高级配置折叠隐藏：

- 自定义 Header。
- Timeout。
- Max retries。
- Stream idle timeout。
- 是否启用。

### 建议配置模型

```yaml
llm:
  default_provider: local
  providers:
    local:
      type: openai-compatible
      base_url: http://localhost:11434/v1
      api_key: ""
      model: qwen2.5-coder:7b
      enabled: true
```

后端内部不应直接读取固定环境变量完成调用，而应该先加载当前工作区或全局默认 Provider，再由 `llm` 模块创建客户端。

### API Key 存储

第一版 Provider API Key 直接存 SQLite，不做加密。

原因是 Water 第一阶段面向内网/本机单用户环境，安全边界相对可控。后续如需增强安全，可以再引入系统 Keychain、加密字段或环境变量引用。

最低要求：

- 页面展示必须脱敏。
- 日志、错误信息、审计事件不得输出明文 API Key。
- 导出配置时默认不包含 API Key，除非用户明确选择。

## 4. 通信方式

### 结论

Water 前端与后端之间优先使用 WebSocket。

Water 后端与 OpenAI-compatible 模型服务之间按模型服务协议使用 HTTP/SSE 流。

### 理由

浏览器与 Water 后端之间不仅需要展示模型增量文本，还需要承载工具调用事件、审批请求、审批结果、任务打断、任务恢复、终端输出等双向交互。WebSocket 更适合这个通道。

OpenAI-compatible 的流式模型接口通常以 HTTP chunk/SSE 形式返回增量内容，后端作为 Provider 客户端消费即可，不要求前端直接感知 Provider 的流式协议。

### Codex 公开资料参考

公开 Codex App Server 文档说明，Codex app-server 面向富客户端提供认证、会话历史、审批与 streamed agent events；其支持的传输包括 stdio、WebSocket 和 Unix socket，其中 WebSocket 使用 JSON-RPC 消息。文档也提到 Codex 观测事件中包含 SSE 与 WebSocket stream activity。

这能确认 Codex 公开的 app-server 集成路径支持 WebSocket；但公开资料没有直接说明 ChatGPT 桌面 App 或云端 UI 内部具体使用 WebSocket 还是 SSE。因此 Water 的设计不把“Codex 内部 UI 协议”当作事实依赖，只借鉴其“面向富客户端的双向事件通道”思路。

### HTTP JSON Envelope

Water 对外 HTTP JSON 接口必须统一使用以下响应结构：

```json
{
  "success": true,
  "requestId": "xxx",
  "message": "",
  "httpCode": 200,
  "data": {}
}
```

字段约定：

- `success` 表示接口处理是否成功。
- `requestId` 表示每次请求的唯一 ID，由后端生成或透传请求头中的请求 ID。
- `message` 表示给前端显示的说明信息。
- `httpCode` 必须与 HTTP 状态码保持一致。
- `data` 表示业务数据主体，没有数据时返回空对象。

新增 HTTP handler 时必须复用统一响应封装，避免每个 handler 手写不同格式。

### WebSocket 事件

WebSocket 不强制使用 HTTP envelope。任务事件流建议采用轻量结构：

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

建议事件类型：

- `task.started`
- `turn.started`
- `agent.message.delta`
- `agent.message.completed`
- `tool.call.requested`
- `approval.requested`
- `approval.resolved`
- `tool.call.started`
- `tool.call.output`
- `tool.call.completed`
- `turn.interrupted`
- `turn.completed`
- `error`

要求：

- `eventId` 必须唯一，便于前端去重和后续回放。
- `sequence` 在同一个 Turn 内递增，便于前端排序。
- 关键事件需要落 SQLite，前端断线重连后可以按任务读取历史事件。

## 5. 单用户与多工作区

Water 第一版不做多用户系统。

但必须支持多个工作区。工作区是 Water 的核心上下文单位，类似 Codex 里的项目/任务运行目录概念。

工作区是默认上下文，不是绝对监牢。用户可以给 Agent 一个外部文件路径，让它读取或修改该文件；这类外部路径访问必须被识别为跨工作区动作，并按权限模式或审批策略处理。

外部路径授权按工作区持久保存。用户一旦把某个外部路径授权给当前工作区，后续该工作区内的任务可以复用这条授权；UI 需要能展示、撤销这些授权记录。

外部路径授权 UI 建议放在工作区设置页，不单独做一级页面。授权粒度支持文件和目录：

- 文件授权只允许访问该文件。
- 目录授权按路径前缀生效。
- 权限区分 `read` 和 `write`。
- 第一版不做过期时间。
- 授权记录需要包含路径、类型、权限、来源任务、创建时间、最近使用时间。

### Workspace

工作区建议包含：

- ID。
- 名称。
- 根路径。
- 默认 Provider。
- 权限模式。
- 是否信任。
- 已授权的外部路径记录。
- 创建时间、最近打开时间。

### Task / Thread

任务是用户与 Agent 的一段对话或一次工作委托。

任务归属于某个工作区，包含多轮 Turn、消息、工具调用、审批记录和事件日志。

### Turn

Turn 是一次用户输入触发的 Agent 工作过程。一个 Turn 内可能包含：

- 用户消息。
- 模型增量输出。
- 工具调用意图。
- 审批请求。
- 工具执行结果。
- 文件变更摘要。
- 结束状态。

Turn 结束或暂停状态需要明确区分：

- `completed`：正常完成。
- `failed`：执行失败。
- `interrupted`：用户打断或主动取消。
- `waiting_approval`：暂停等待用户审批。

## 6. 权限模式

第一版只暴露两个用户容易理解的模式。

### 请求审批

默认推荐。

适合不完全信任当前仓库、需求还在探索、或用户希望逐步确认每个高风险动作的场景。

建议行为：

- 工作区内读文件、列目录可自动执行。
- 写文件需要审批。
- 删除文件需要审批。
- 命令执行是否审批由用户当前权限模式和策略决定。
- Python 执行需要审批或白名单。
- 网络访问默认关闭，需要审批或配置允许。
- 工作区外访问需要用户在任务或审批中明确授权；授权成功后保存到当前工作区。

### 完全访问

高信任模式。

适合用户明确希望 Agent 自主完成本地开发任务的场景。

建议行为：

- 工作区内读写文件可自动执行。
- 工作区内命令和 Python 可自动执行。
- 所有动作仍必须记录审计日志。
- UI 必须明显展示当前处于完全访问模式。
- 工作区外访问不应被隐式打开，除非用户已在当前工作区持久授权对应外部路径。

## 7. 工具调用原则

模型不能直接执行工具。模型只能提出工具调用意图，Harness 负责判断是否允许执行。

第一版不按具体问题堆叠专用工具。文件、目录、系统信息、磁盘容量、内存使用、CPU 使用率、Git 状态和测试结果等需求，优先由模型规划并选择 `list_dir`、`read_file`、`write_file`、`run_command` 这类通用工具完成；Harness 负责校验、审批、执行和结果回填。

`run_command` 可以维护一组保守的只读系统检查命令白名单，例如 `df -h /`、`vm_stat`、`sysctl hw.memsize`、`free -h`、`wmic OS get FreePhysicalMemory,TotalVisibleMemorySize /Value`、`top -l 1 -s 0 -n 0`、`top -bn1`、`wmic cpu get loadpercentage /Value`。这不是问题专用工具，而是 Harness 对通用命令工具的安全策略。

系统信息类问题不能只依赖模型推断命令。Harness 应把当前后端 `os/arch` 显式放入 System Prompt，并在执行层按平台选择 shell：类 Unix 使用 `sh -c`，Windows 使用 `cmd /C`。模型负责规划，Harness 负责给出可靠边界和跨平台执行能力。

长命令输出应完整写入事件日志，但回填给 LLM 时需要截断或摘要化，避免本地模型被进程列表、测试日志等长文本拖慢或超时。

推荐流程：

```text
用户输入
 -> Agent Loop
 -> LLM 输出自然语言或工具调用意图
 -> Harness 校验工具名、参数、工作区边界和权限模式
 -> 必要时生成审批请求并保存工具请求快照
 -> 用户批准后恢复执行，用户拒绝后中断 Turn
 -> 工具执行
 -> 结果写入事件日志并回填给 LLM
 -> Agent 继续或结束
```

工具执行必须具备：

- 参数校验。
- 工作区路径归一化与越界检查。
- 超时控制。
- 可取消。
- 审计日志。
- 面向 UI 的结构化事件。

审批恢复要求：

- 审批记录保存后端内部工具请求快照，用于批准后恢复执行。
- 批准后写入 `approval.continuation.started`，执行原工具调用，并把工具结果回填给模型继续推理。
- 拒绝后写入 `turn.interrupted`，避免任务一直停在 `waiting_approval`。
- 工具请求快照不对前端列表暴露，避免写文件内容或命令参数在不必要的位置展示。

更完整的执行流图、上下文分层和默认文档落盘规则，见 `docs/harness-architecture.md`。

## 8. 第一阶段建议范围

第一阶段先做产品底座，不急于实现复杂 Agent 能力。

建议顺序：

1. 确认设计文档。
2. Go 后端最小 HTTP 服务。
3. SQLite 初始化与迁移。
4. Provider 配置 CRUD。
5. Provider 连接测试。
6. Chat API 调通 OpenAI-compatible 模型。
7. WebSocket 任务事件流。
8. 前端 Provider 配置页面。
9. 前端工作区与任务基础页面。
10. 只读文件工具。
11. 写文件工具与审批。
12. 命令执行审批。

### SQLite 迁移

SQLite 迁移使用现成 Go migration 库，不自研 migration runner。

第一版选用 `github.com/pressly/goose/v3`：

- 支持 SQL migration 文件。
- 可以在 Go 服务启动时执行迁移。
- 适合嵌入单体后端。
- migration 文件放在 `water-be/migrations/`。

服务启动时默认自动迁移本地 SQLite 数据库；如果后续需要更严格的生产部署流程，再增加手动迁移命令。

### Diff 预览

第一版不做复杂 Diff 编辑器。

写文件审批时需要提供简单、可理解的变更说明：

- 目标路径。
- 新增、修改、删除的大致摘要。
- 对代码/文本文件尽量展示简单 diff 或前后片段。
- 对大文件或二进制文件只展示摘要与风险提示。

## 9. 暂不做

以下能力暂缓，避免早期设计过重：

- 多用户、租户、复杂 RBAC。
- 云端同步。
- 插件市场。
- Sub-Agent。
- 长期记忆系统。
- 复杂 Python 沙箱。
- 多数据库适配。
- 在线依赖安装。
- 模型能力画像与多模型路由。
- 复杂上下文可视化。

## 10. 待讨论问题

- 暂无会阻塞 MVP 初始化的设计问题。
- 后续可继续细化任务摘要/历史复盘页、工作区文件索引页、外部路径授权审计页。

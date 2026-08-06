# Water MVP 计划

本文档把 Water 第一阶段拆成可执行任务。目标是先做出一个稳、可用、可观察、可审批的最小闭环，而不是一开始追求完整 Codex 级能力。

## 1. MVP 目标

第一阶段完成以下闭环：

```text
创建工作区
 -> 页面配置 OpenAI-compatible Provider
 -> 测试 Provider 可用
 -> 创建任务并发送消息
 -> 后端流式调用模型
 -> 前端通过 WebSocket 看到任务事件
 -> Agent 可申请基础文件工具
 -> Harness 根据权限模式执行或请求审批
 -> 工具结果回填模型
 -> 任务结束并留存审计记录
```

## 2. 不在 MVP 范围内

- 多用户、租户、复杂登录系统。
- 云端同步。
- 插件市场。
- Sub-Agent。
- 长期记忆系统。
- 完整 Python 沙箱。
- 多数据库适配。
- 在线依赖安装。
- 复杂 Diff 编辑器。
- 自动生成 Git commit 或 PR。

## 3. 阶段拆解

### 阶段 0：文档与约定

目标：让后续编码有明确边界。

任务：

- 维护 `AGENTS.md`。
- 维护 `docs/design-decisions.md`。
- 维护 `docs/harness-architecture.md`。
- 维护 `docs/mvp-plan.md`。
- 每次文件改动更新 `CHANGELOG.md`。

验收：

- 关键技术栈、目录、权限、Provider、通信协议已有中文说明。
- 未经用户确认不初始化完整骨架。

### 阶段 1：后端基础

目标：建立 Go 后端最小可运行服务。

任务：

- 初始化 `water-be`。
- 建立 `cmd/water` 入口。
- 建立 `internal/api`、`internal/config`、`internal/store` 基础包。
- 提供健康检查接口。
- 实现统一 HTTP JSON envelope。
- 每个请求生成或透传 `requestId`。

验收：

- `GET /api/health` 返回统一 envelope。
- `httpCode` 与实际 HTTP 状态码一致。
- 错误响应也使用统一 envelope。

### 阶段 2：SQLite 与配置

目标：能持久化 Provider、工作区、任务基础信息。

任务：

- 接入 SQLite。
- 使用 `github.com/pressly/goose/v3` 建立 SQL migration 机制。
- 设计并创建基础表：`providers`、`workspaces`、`tasks`、`turns`、`events`、`approvals`。
- 设计并创建上下文相关表：`file_summaries`、`workspace_indexes`、`task_summaries`、`pinned_contexts`。
- Provider API Key 第一版明文存 SQLite。
- 日志与响应中禁止输出明文 API Key。

验收：

- 服务启动时通过 goose 自动创建或迁移数据库。
- Provider CRUD 可用。
- 查询 Provider 时 API Key 默认脱敏。

### 阶段 3：Provider 管理

目标：页面可以简单配置模型 Provider。

任务：

- 后端实现 Provider CRUD。
- 后端实现 Provider 连接测试。
- 前端初始化 `water-fe`。
- 前端建立 Provider 配置页面。
- 支持设置默认 Provider。

验收：

- 用户能在页面填写名称、Base URL、Model、API Key。
- 用户能测试连接。
- 用户能设置默认 Provider。
- 配置不写死到代码里。

### 阶段 4：基础对话

目标：调通 OpenAI-compatible 模型的普通对话与流式输出。

任务：

- 后端实现 `internal/llm` 抽象。
- 实现 OpenAI-compatible client。
- 支持非流式 `Chat`。
- 支持流式 `ChatStream`。
- 按工作区或全局默认 Provider 选择模型。

验收：

- 后端能调用本地或内网 OpenAI-compatible 服务。
- 模型参数来自 SQLite 配置。
- Provider 不可用时返回统一错误 envelope。

### 阶段 5：WebSocket 任务事件流

目标：前端能看到任务执行过程，而不是只看到最终答案。

任务：

- 后端实现 WebSocket 连接。
- 定义轻量事件结构：`type`、`requestId`、`workspaceId`、`taskId`、`turnId`、`payload`。
- 事件结构包含 `eventId`、`sequence`、`createdAt`，便于去重、排序和回放。
- 支持创建任务、开始 Turn、流式输出模型 delta。
- 支持客户端请求打断当前 Turn。
- 前端任务页面展示事件流。

验收：

- 用户发送消息后能看到流式文本。
- 任务开始、输出、完成、错误都有结构化事件。
- 用户可以打断正在执行的 Turn。
- 前端断线重连后可以读取任务历史事件。

### 阶段 6：工作区

目标：支持多个工作区，任务归属于工作区。

任务：

- 后端实现 Workspace CRUD。
- 每个工作区包含名称、根路径、默认 Provider、权限模式、是否信任。
- 工作区设置中提供外部路径授权管理，支持查看和撤销。
- 前端实现工作区列表与切换。
- 创建任务时必须选择或绑定工作区。

验收：

- 用户可以创建多个工作区。
- 每个任务能追溯到所属工作区。
- 工作区默认 Provider 与权限模式可配置。
- 外部路径授权支持文件和目录，目录按路径前缀生效，权限区分读写。

### 阶段 7：只读工具

目标：Agent 可以安全读取工作区内容。

任务：

- 设计 Tool 接口。
- 实现 `list_dir`。
- 实现 `read_file`。
- 做路径归一化与越界判断。
- 工具调用结果写入事件日志。

验收：

- 请求审批模式下，工作区内只读工具可自动执行。
- 工作区外路径会触发审批或授权判断。
- 工具输入、输出摘要、执行耗时可在前端看到。

### 阶段 8：写文件与审批

目标：Agent 可以申请修改文件，用户可以批准或拒绝。

任务：

- 实现 Approval 数据模型。
- 实现 `approval.requested` 与 `approval.resolved` WebSocket 事件。
- 实现 `write_file` 工具。
- 前端实现审批抽屉或弹窗。
- 审批内容包含目标路径、动作类型、风险说明、预期影响。
- 第一版提供简单变更摘要或文本 diff，不做复杂 Diff 编辑器。

验收：

- 请求审批模式下写文件必须等待用户批准。
- 用户拒绝后工具不执行，并把拒绝结果回填给 Agent。
- 完全访问模式下工作区内写文件可自动执行，但仍记录审计。
- 写文件审批中能看到目标路径和简单变更说明；代码/文本文件尽量展示简单 diff 或前后片段。

### 阶段 9：命令执行审批

目标：建立命令执行能力，但默认保守。

任务：

- 实现命令执行工具接口。
- 支持 context timeout 和取消。
- 支持 stdout/stderr 流式事件。
- 请求审批模式下由用户决定是否执行。
- 完全访问模式下允许工作区内命令自动执行。

验收：

- 命令执行前端可见、可取消、可审计。
- 高风险命令不静默执行。
- 命令输出不会撑爆事件日志，需要截断或摘要策略。

### 阶段 10：本地模型上下文优化

目标：让短上下文本地模型也能稳定处理真实项目任务。

任务：

- 实现文件摘要缓存。
- 实现工作区轻量索引。
- 实现任务滚动摘要。
- 实现 Context Pack 组装。
- 支持用户手动触发上下文压缩。
- 支持任务级钉住文件或说明。
- Context Pack 最多使用 Provider 上下文窗口的 80%。
- 文件摘要、任务摘要、工具输出摘要使用系统预置模板。

验收：

- 文件未变化时复用摘要缓存。
- 长工具输出不会直接塞入模型上下文，而是先摘要。
- 任务历史过长时能用滚动摘要替代老消息。
- 用户可以把关键文件或说明钉住到当前任务。
- 钉住上下文超预算时优先保留路径、摘要、关键符号和当前任务相关片段。

## 4. 推荐后端包边界

```text
internal/api        # HTTP 和 WebSocket handler
internal/store      # SQLite 访问
internal/config     # 配置加载与默认值
internal/llm        # Provider 抽象与 OpenAI-compatible client
internal/agent      # Agent Loop 与 Turn 状态机
internal/context    # Context Pack、摘要缓存、任务压缩
internal/tools      # 工具注册、参数校验、执行
internal/sandbox    # 命令、Python、路径权限
internal/auth       # 单用户权限与本地访问控制
```

## 5. 推荐前端模块

```text
src/pages/providers     # Provider 配置
src/pages/workspaces    # 工作区列表与配置
src/pages/tasks         # 任务对话与事件流
src/components/approval # 审批 UI
src/components/events   # 工具事件、命令输出、状态展示
src/api                 # HTTP envelope client
src/ws                  # WebSocket task client
src/context             # 钉住上下文、上下文压缩入口
```

## 6. 关键验收标准

MVP 可以认为完成的标准：

- 单机启动后，用户能在页面配置 OpenAI-compatible Provider。
- 用户能创建工作区并选择权限模式。
- 用户能创建任务并看到模型流式回复。
- 用户能看到 Agent 的工具调用过程。
- 工作区内只读工具可用。
- 写文件和命令执行具备审批闭环。
- 工作区可以持久保存外部路径授权，并允许用户查看和撤销。
- 所有 HTTP JSON 响应使用统一 envelope。
- 关键行为写入 SQLite 审计事件。
- 本地模型上下文通过摘要缓存和 Context Pack 控制，不把完整仓库直接塞给模型。

## 7. 下一步建议

下一次进入编码前，优先初始化后端最小服务与统一 HTTP envelope。这样可以先把项目的“脊柱”立住，再逐步挂 Provider、SQLite、WebSocket 和前端页面。

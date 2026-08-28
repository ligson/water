# 自动任务

若水的自动任务是持久化的提示词计划。后端会在服务运行时按计划触发，每次触发都会创建一条独立的普通任务和 Turn，因此可以继续使用现有的 Agent Loop、审批、任务事件、断线补偿和完整执行日志。

## 第一版能力

- 每天固定时间执行，例如 `09:30`
- 固定间隔执行，最小间隔 5 分钟
- 保存时区，默认 `Asia/Shanghai`
- 启用、暂停、立即执行
- 同一个自动任务不允许重叠运行，重叠时跳过本次
- 执行失败或服务重启中断后，按有限次数重试
- 遇到审批时暂停，等待用户处理
- 每次运行保留提示词快照、状态、结果摘要、错误和关联任务 ID
- 自动任务从待执行队列中串行启动，优先保证本地模型资源稳定；同一计划在审批等待期间也不会重复触发

## 生命周期

```text
自动任务定义
  -> queued
  -> running
  -> succeeded / failed / waiting_approval / interrupted / cancelled / skipped
```

调度器运行在 Go 后端中，不依赖浏览器。服务重启时，已经开始的运行会标记为 `interrupted` 并按重试策略排队；尚未开始的运行不会因为浏览器关闭而丢失。

## API

```text
GET    /api/scheduled-tasks?workspaceId=:id
POST   /api/scheduled-tasks
GET    /api/scheduled-tasks/:id
PUT    /api/scheduled-tasks/:id
DELETE /api/scheduled-tasks/:id
POST   /api/scheduled-tasks/:id/enable
POST   /api/scheduled-tasks/:id/disable
POST   /api/scheduled-tasks/:id/run-now
GET    /api/scheduled-tasks/:id/runs
GET    /api/scheduled-task-runs/:id
POST   /api/scheduled-task-runs/:id/cancel
```

自动任务执行记录中的 `taskId` 可以直接打开现有任务详情，查看完整事件流、工具调用、审批和最终结果。自动任务列表只展示结果摘要，避免每次打开页面先被冗长的任务产物淹没；审批通过后继续执行产生的最终回复也会在状态对账时补回摘要。

## 后续扩展

后续可增加 Cron、高级通知、排队并发策略和独立的全局执行记录页，但不应绕过现有权限引擎、审批和任务完成裁判。

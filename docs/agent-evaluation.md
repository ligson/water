# 若水 Agent 评测基线

若水的“智能”不能只看模型回复是否流畅，必须同时看是否完成目标、是否有真实验证、是否重复读取、是否进入循环，以及是否能在中断后恢复。

## 运行历史任务评测

在 `water-be` 目录执行：

```bash
go run ./cmd/water-eval --db ../data/water.db
```

也可以只评测指定任务：

```bash
go run ./cmd/water-eval --db ../data/water.db --task-id task_xxx,task_yyy
```

输出是 JSON，核心字段包括：

- `observedCompletion`：观察到的 `turn.completed` 占任务数比例，不等同于业务真实成功率。
- `verifiedTasks` / `unverifiedCompletedTasks`：已完成任务中有无测试、构建或验收证据；没有验证的完成状态必须视为风险，而不是成功。
- `averageReplayScore`：根据失败、中断、重复读取、验证和端到端验收计算的执行质量分数。
- `toolFailures` / `repeatedReads`：工具稳定性和信息增益问题。
- `correctedToolCalls`：Harness 自动修复工具名称、参数别名或目录误读的次数。
- `cachedReads`：同一资源再次被请求时复用已有真实结果的次数；该指标上升通常表示重复读取被拦截。
- `structuredErrors` / `replans` / `recoverySuggestions`：失败是否带有可恢复错误契约，Agent 是否切换到替代路径，以及 Harness 是否给出了命令拆分、系统替代命令或失败证据定位建议。
- `validations` / `failedValidations`：测试、构建和验收覆盖情况。
- `endToEndTasks`：有端到端验收证据的任务数。

评测命令只读任务事件，不会执行模型、修改工作区或改变任务状态。

## 固定回归任务

内置基线位于 `water-be/internal/eval/eval.go`，覆盖：

- 登录 401 诊断
- 注册、登录和用户 CRUD 实现
- 前端构建修复
- 系统信息只读检查
- Markdown 报告生成
- 流式超时恢复
- 工作区外路径审批
- 错误工具调用自愈

每次调整 Agent Loop、Context Pack、工具策略或完成裁判器后，至少比较同一批任务的完成率、平均回放分数、重复读取数和端到端验收数。没有真实验收证据的“已完成”只能算观察指标，不能算业务成功。

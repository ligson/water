# Water Frontend

Water 前端使用 Vue 3 + TypeScript + Vite + Ant Design Vue。

## 启动

```bash
npm install
npm run dev
```

默认连接同源后端。开发时可通过 `.env.local` 指定后端地址：

```text
VITE_API_BASE=http://localhost:8080
```

## 当前页面

- Provider 配置与连接测试。
- Workspace 创建与切换。
- Task 创建、Turn 发送与任务事件流展示。
- WebSocket 实时任务事件订阅。
- 当前任务打断。
- Pending Approval 列表与批准/拒绝。
- 工作区外部路径授权管理。

## 构建

```bash
npm run build
```

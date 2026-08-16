# 发版流程

若水使用 Git 标签触发 GitHub Actions 发版。工作流只响应符合 SemVer 的 `v*` 标签，例如 `v0.1.0` 或 `v0.2.0-rc.1`。

## 自动产物

每次发版会先运行后端测试和 `go vet`，然后生成：

| 产物 | 目标平台 |
| --- | --- |
| `water-be_<version>_linux_amd64.tar.gz` | Linux x86-64 |
| `water-be_<version>_linux_arm64.tar.gz` | Linux ARM64 |
| `water-be_<version>_darwin_amd64.tar.gz` | macOS Intel |
| `water-be_<version>_darwin_arm64.tar.gz` | macOS Apple Silicon |
| `water-fe_<version>.tar.gz` | 架构无关的前端静态资源 |
| `checksums.txt` | 所有压缩包的 SHA-256 |

产物同时保留为 GitHub Actions Artifact，并发布到对应标签的 GitHub Release。后端使用 `CGO_ENABLED=0` 交叉编译，SQLite migration 已嵌入二进制；前端包包含 Vite 构建后的 `dist/` 和可复用的 `nginx.conf`，需要由 Nginx 等静态服务器托管并代理 `/api/` 与 `/ws/`。

## 发版操作

当用户明确要求“发版”时，按以下顺序执行：

1. 确认版本号；未指定时，在最新 SemVer 标签基础上递增 patch，没有历史标签则从 `v0.1.0` 开始。
2. 运行后端测试、`go vet`、前端构建及跨平台打包验证。
3. 检查工作区、暂存区和新增文件，排除密钥、Token、`.env`、数据库、日志、PID、构建产物和本机绝对路径。
4. 更新 `CHANGELOG.md`，提交并推送代码。
5. 创建 annotated tag，并把标签推送到 `origin`。
6. 等待 `Release packages` 工作流成功，再确认 GitHub Release 与 SHA-256 文件存在。

本地可使用相同脚本验证打包：

```bash
./scripts/package-release.sh v0.1.0
```

本地产物位于 Git 忽略的 `output/release/`。发版标签一旦公开不应复用或强制移动；修复后使用新的 patch 版本重新发布。

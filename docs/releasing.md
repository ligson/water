# 发版流程

若水使用 Git 标签触发 GitHub Actions 发版。工作流只响应符合 SemVer 的 `v*` 标签，例如 `v0.1.0` 或 `v0.2.0-rc.1`。

## 自动产物

每次发版会先运行后端测试和 `go vet`，然后生成：

| 产物 | 目标平台 |
| --- | --- |
| `water_<version>_linux_amd64.tar.gz` | Linux x86-64，前后端单体二进制 + systemd 安装文件 |
| `water_<version>_linux_arm64.tar.gz` | Linux ARM64，前后端单体二进制 + systemd 安装文件 |
| `water_<version>_darwin_amd64.tar.gz` | macOS Intel，前后端单体二进制 |
| `water_<version>_darwin_arm64.tar.gz` | macOS Apple Silicon，前后端单体二进制 |
| `checksums.txt` | 所有压缩包的 SHA-256 |

产物同时保留为 GitHub Actions Artifact，并发布到对应标签的 GitHub Release。Release 说明严格取自根目录 `CHANGELOG.md` 的对应版本章节。脚本先构建 Vue `dist/`，再使用 Go `embed` 编译进每个平台的 `water` 二进制；SQLite migration 同样嵌入二进制，运行时不需要单独的前端目录或 Nginx。

Linux 包额外包含 `install.sh`、`uninstall.sh`、`water.service`、`water.env.example`、安装版 `README.md` 和项目总览 `PROJECT_README.md`。`install.sh` 会安装到 `/opt/water`，配置写入 `/etc/water/water.env`，数据默认写入 `/var/lib/water`，升级时保留配置和数据库。普通 Linux 可自动创建默认低权限账户；Synology 等缺少 `useradd` 的系统需传入已有用户和组。安装器不会自动修改工作区所有权。

## CHANGELOG 格式

日常改动记录在 `## [Unreleased]` 下。发版前将这部分改成不带 `v` 的版本章节，并重新创建空的 `Unreleased` 章节：

```markdown
## [Unreleased]

## [0.2.0] - 2026-09-01

- 新增某项能力。
- 修复某个问题。
```

标签 `v0.2.0` 只会读取 `[0.2.0]` 章节，到下一个二级标题为止。工作流找不到对应章节或章节为空时会停止发版。

## 发版操作

当用户明确要求“发版”时，按以下顺序执行：

1. 确认版本号；未指定时，在最新 SemVer 标签基础上递增 patch，没有历史标签则从 `v0.1.0` 开始。
2. 运行后端测试、`go vet`、前端构建及跨平台打包验证。
3. 检查工作区、暂存区和新增文件，排除密钥、Token、`.env`、数据库、日志、PID、构建产物和本机绝对路径。
4. 将 `CHANGELOG.md` 的 `Unreleased` 内容归档为对应版本章节，提交并推送代码。
5. 创建 annotated tag，并把标签推送到 `origin`。
6. 等待 `Release packages` 工作流成功，再确认 GitHub Release 与 SHA-256 文件存在。

本地可使用相同脚本验证打包：

```bash
./scripts/package-release.sh v0.1.0
```

本地只生成一个可运行的单体二进制：

```bash
./scripts/build-single-binary.sh dev water-be/bin/water
```

可在打标签前预览将要发布的 Release 说明：

```bash
./scripts/extract-release-notes.sh v0.1.0
```

本地产物位于 Git 忽略的 `output/release/`。发版标签一旦公开不应复用或强制移动；修复后使用新的 patch 版本重新发布。

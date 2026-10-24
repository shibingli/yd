# 发布与预编译二进制

`.github/workflows/release.yml` 是 yd 唯一的标签发布入口。推送 `vX.Y.Z` 形式的标签后，
GitHub Actions 会自动完成四平台构建、打包、校验和生成与 Release 资产上传。

## 触发方式

| 方式 | 用途 |
|---|---|
| 推送 `vX.Y.Z` 标签 | 正常发布：构建并创建/更新对应 Release |
| Actions → Release → Run workflow，填写已有标签 | 补发或重建资产（例如为历史 Release 补齐二进制） |

标签必须严格匹配 `v[0-9]+.[0-9]+.[0-9]+`；`v1.2.3-rc.1`、`v1.2` 等标签会被显式跳过，
工作流以“检查通过、构建跳过”结束，不会创建 Release。

## 构建矩阵与资产

| 平台 | 架构 | 资产 |
|---|---|---|
| Linux | amd64 | `yd_<tag>_linux_amd64.tar.gz` |
| Linux | arm64 | `yd_<tag>_linux_arm64.tar.gz` |
| Windows | amd64 | `yd_<tag>_windows_amd64.zip` |
| Windows | arm64 | `yd_<tag>_windows_arm64.zip` |

每个归档包含：

- `yd` / `yd.exe`：独立 CLI 二进制；
- `LICENSE`：Apache-2.0 许可；
- 归档顶层目录为 `yd_<tag>_<os>_<arch>/`。

Release 同时附带 `checksums.txt`（SHA-256），可执行：

```bash
sha256sum -c checksums.txt
```

构建固定使用 `CGO_ENABLED=0`、`-trimpath` 与 `-ldflags "-s -w"`，产物为纯 Go 静态链接
二进制，前端资产已由 `go:embed` 打入，无需运行时依赖。

## 版本注入

构建时向 `github.com/shibingli/yd/core/version` 注入三项：

| 变量 | 取值 |
|---|---|
| `Version` | 标签名，例如 `v1.9.0` |
| `Commit` | 标签对应的提交 SHA |
| `Date` | UTC 构建时间（RFC3339） |

因此 `yd --version` 会输出形如 `yd v1.9.0 (<commit>, <date>)` 的版本信息。工作流在
linux/amd64 上会实际运行一次该命令并断言标签出现在输出中。

## Release 说明

- 新标签：Release 说明由「下载/安装/校验」段落 + GitHub 自动生成变更说明组成。
- 已存在的 Release（补发场景）：**只上传/替换资产，不覆盖已经人工撰写的说明**。
- 重复运行同一标签：`gh release upload --clobber` 原地替换资产，不产生重复附件。

## 本地复现

```bash
tag=v1.9.0
commit=$(git rev-parse HEAD)
date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w \
    -X github.com/shibingli/yd/core/version.Version=${tag} \
    -X github.com/shibingli/yd/core/version.Commit=${commit} \
    -X github.com/shibingli/yd/core/version.Date=${date}" \
  -o /tmp/yd ./cmd/yd
/tmp/yd --version
```

## 约束

- 不得在脚本、CI 或文档中另写一套发布流程；标签发布只经该工作流。
- 不得把构建产物提交入库；Release 资产是唯一的分发载体。
- 不得跳过 `verify` 作业：编译整个模块并运行 `go vet` 后才进入四平台交叉构建。
- 不得在 Release 说明或资产名中加入与构建无关的标记；资产名固定为
  `yd_<tag>_<goos>_<goarch>.<ext>`。

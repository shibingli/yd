# yd 实施计划：目录结构重整

> 需求来源（用户原话）：
> 「重新整理一下当前项目的目录结构，我发现当前目录的结构很乱，工具类、中间件类、核心功能类
> 都没进行分类，都堆在根目录，很不友好」
>
> 用户已选定方案：**按分层分类**；且 **`middleware/` 独立成顶层目录**。

## 1. 为何单独成篇、且排在最后

这是**纯结构调整**，但影响面极大：约 40 个包的 import 路径、脚手架模板里的模块引用、
`repotest` 的路径守卫、ldflags 的注入目标、全部文档里的路径引用。

因此：

- **排在所有功能工作完成并提交推送之后**执行——先拿到一个可回滚的点；
- **一次成型**，不与其它改动并行：半途状态会留下「一半包在新路径、一半在旧路径」的树，
  而它**能编译**（只要 import 改对了），却与文档、守卫、模板全对不上，最难排查；
- 完成后必须跑**全量门禁**（`build` / `vet` / `gofmt` / `test -race` / 交叉编译 / e2e）。

## 2. 目标结构（用户选定）

```
yd/
├── cmd/yd/                命令行入口
├── core/                  基础原语（与领域无关）
│   └── afs/ b64x/ config/ errx/ jsonx/ logx/ version/
├── http/                  HTTP 与前端交付
│   └── router/ render/ htmx/ sse/ ws/ openapi/ i18n/
├── middleware/            中间件（顶层独立，用户指定）
├── security/              安全与鉴权
│   └── guomi/ jwt/ auth/ authz/   （auth/gormstore 随 auth 下移）
├── runtime/               进程与运行时
│   └── app/ server/ grpcx/ lifecycle/ service/
├── data/                  数据访问（不变）
│   └── gormx/ redisx/ asynqx/
├── tooling/               开发期工具与脚手架
│   └── scaffold/ gen/ build/ docker/ assets/
│       toolchain/ align/ tools/ ko/ completion/ cli/
├── web/                   内嵌前端资产（不变）
├── repotest/              仓库守卫（顶层，与 e2e/ 对称）
└── e2e/                   端到端（不变）
```

## 3. 逐包映射（执行时按此表，不得遗漏）

| 现路径 | 新路径 |
|---|---|
| `afs` `b64x` `config` `errx` `jsonx` `logx` `version` | `core/<同名>` |
| `router` `render` `htmx` `sse` `ws` `openapi` `i18n` | `http/<同名>` |
| `middleware` | `middleware`（**不变**） |
| `guomi` `jwt` `auth` `authz` | `security/<同名>` |
| `auth/gormstore` | `security/auth/gormstore` |
| `app` `server` `grpcx` `lifecycle` `service` | `runtime/<同名>` |
| `data/gormx` `data/redisx` `data/asynqx` | **不变** |
| `scaffold` `gen` `build` `docker` `assets` `toolchain` `align` `tools` `ko` `completion` `cli` | `tooling/<同名>` |
| `internal/repotest` | `repotest`（顶层，与 `e2e/` 对称） |
| `cmd/yd` `web` `e2e` | **不变** |

## 4. 必须一并改到的六处（漏一处就会留下不一致）

1. **全部 import 路径**——替换顺序**必须先长后短**（`auth/gormstore` 要先于 `auth`），
   否则会把已经改好的新路径再前缀一次。
2. **脚手架模板里的模块引用**：形如 `{{ .YdModule }}/auth`、`{{ .YdModule }}/logx` 的字符串
   散落在 `tooling/scaffold/templates/**`；生成的 `go.mod` 仍 `replace` 到模块根，因此只需改包路径。
3. **ldflags 注入目标**：`build` 包向 `version` 包注入版本信息的 `-X <pkg>.<var>` 路径。
4. **`repotest` 的路径守卫**：守卫里以**字符串**写死了 `auth/`、`ws/`、`sse/`、`align/`、
   `ko/`、`toolchain/`、`server/`、`jsonx/`、`b64x/`、`errx/`、`repotest/` 等前缀，
   以及 `jsonx/`、`b64x/` 作为「允许直接 import 标准库」的豁免目录。
   这些**不会因为文件移动而自动更新**——把守卫改对是本任务最容易被忽略的一步。
5. **文档与配置里的路径**：`CLAUDE.md`（§1 源码目录、§14 工具目录）、`AGENTS.md` §1「允许直接修改」
   的目录清单、`docs/developer_guidelines.md`、根 `README.md`。
6. **`.gitignore` / 构建脚本 / Dockerfile 模板**里出现的路径。

## 5. 验收标准

1. `go build ./...`、`go vet ./...`、`gofmt -l .` 全部零输出；
2. `go test -race ./...` 全绿，且**包数与重整前一致**（用 `go list ./... | wc -l` 对照，
   防止某个包在移动中静默消失）；
3. 交叉编译（`GOOS=linux GOARCH=arm64`、`GOOS=windows GOARCH=amd64`，`CGO_ENABLED=0`）通过；
4. `go test -tags e2e ./e2e/` 通过——它用**真实 CLI 生成项目并编译启动**，
   是「模板里的模块引用改对了没有」的最强证据；
5. 根目录下**不再有**任何属于 `core`/`http`/`security`/`runtime`/`tooling` 的包目录；
6. `docs/需求设计说明书.md` 保持只读未改。

## 6. 已知代价

- **这是一次破坏性变更**：模块的公开 import 路径全部改变。本仓处于 v0 且尚无下游使用者，
  因此可以接受；但若已有部署，需要在发布说明里标注 `BREAKING CHANGE`。
- **历史记录的归属会变**：`git log --follow` 仍可追到单个文件，但按目录浏览历史会断层。
  这是所有大规模目录重整的共同代价，不额外缓解。
- **不做「兼容别名」**：保留旧路径的空壳包会让两套路径长期并存，比一次性改彻底更糟。

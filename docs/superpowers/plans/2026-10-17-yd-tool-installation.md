# yd 实施计划：可选外部工具的探测与一键安装

> 需求来源（用户原话）：
> 「本机的依赖工具没有安装时，如：ko、betteralign没有安装时，要提示方法进行安装，
> 最好是在new项目的时候一次性都安装好」

## 0. 需求差异说明

`docs/需求设计说明书.md` 为**只读需求基线**，其中没有关于外部工具管理的条目。
本文件即该需求变更的差异说明，后续以本文件、`CLAUDE.md`、`AGENTS.md` 为准。

**与基线的第二处偏离（有意）**：基线 §二.3 的 `.gitignore` 模板没有忽略 `.yd/`，
而本需求把工具二进制装进项目内的 `.yd/tools/`——不忽略就会出现在用户的 `git status` 里，
甚至被误提交几十 MB。因此生成项目的 `.gitignore` 增加 `.yd/` 一行（含双语说明），
基线文档本身保持只读不改；`scaffold.scaffold_test.go` 的 `specGitignore` 常量作为基线的镜像
同步更新，并在注释里标明这是有意偏离而非漂移。

## 1. 现状与缺口

框架已经有三处「可选外部工具」，各自解析、各自给提示：

| 工具 | 解析入口 | 既有提示常量 | 用途 |
|---|---|---|---|
| betteralign | `align.Resolve` | `align.InstallHint` | 内存对齐（CLAUDE.md §8） |
| ko | `ko.Resolve` | `ko.InstallHint` | 无 Dockerfile 打包（CLAUDE.md §13） |
| tailwindcss | `toolchain.ResolveTailwind` / `build.ResolveTailwindBin` | 无 | 样式表编译 |

四处共性：解析链都是「显式参数 → 环境变量 → `.yd/tools/` → `PATH`」（`LocalToolDir = ".yd/tools"`）。 缺
口在于：

1. **没有统一的探测入口**：用户无法一次问清「这台机器还缺什么」，只能各自撞到错误。
2. **没有安装能力**：提示是一行 `go install ...`，要用户自己复制、自己判断装到哪；
   而装到 `GOBIN`（默认 `~/go/bin`）与装到项目内 `.yd/tools/` 是两种不同的落地方式。
3. **`yd new` 结束即撒手**：新项目建好后，用户要等到第一次 `yd align` / `yd ko` 才发现工具缺失。

## 2. 设计

### 2.1 新增 `tools` 包：单一的探测与安装入口

- **注册表**：每个工具一条 `Tool` 记录（`Name` / `Summary` / `InstallHint` / `GoInstall` /
  `VersionArgs` / `DocURL`）。`InstallHint` **直接复用** `align.InstallHint` 与 `ko.InstallHint`，
  不另写一份文案，从根上杜绝「两处提示不一致」。
- **探测 `Check(dir)`**：逐个调用既有解析函数（`align.Resolve` / `ko.Resolve` /
  `toolchain.ResolveTailwind` / `exec.LookPath("docker")`），**只读、不触网、不执行工具**。
- **安装 `Install(ctx, dir, names, log)`**：`go install <pkg>@latest`，并把 `GOBIN` 指向
  **`<项目>/.yd/tools`**（绝对路径）——装完即落在解析链的第三段，无需任何额外配置。
- **依赖方向**：`tools` → `align` / `ko` / `toolchain`，是单向的。`align` 与 `ko`
  **不得**反向 import `tools`（守卫强制，避免成环）。
- **可安装性**：`GoInstall` 为空的条目（tailwindcss、docker）只报告与提示，安装时返回
  `ErrNotInstallable` 并给出各自的获取途径。

### 2.2 `yd tools` 命令

```
yd tools [check]              # 默认：列出全部工具的状态与安装提示
yd tools install [名称...]     # 安装（缺省为全部可安装项）
```

状态表逐行给出「工具 / 是否就绪 / 路径或版本 / 缺失时的安装提示」，双语。

### 2.3 `yd new` 收尾时统一处理

项目生成成功后，追加一步：

1. `tools.Missing(dir)` 探测；
2. 有缺失时打印提示表（含 `yd tools install` 这条统一入口）；
3. **交互模式**：`Confirm("现在就安装缺失的工具吗？", 默认 yes)`，答是则 `InstallAll`；
4. **非交互模式**：只打印提示，**不触网**。

`--install-tools` 标志用于在非交互场景（CI、脚本）显式要求安装。

**为什么 `--yes` 不隐含安装**：`yd new --yes` 是 e2e 与 CI 的入口，若它隐含 `go install`
就会把网络依赖与数十秒的下载带进门禁；安装是需要显式表达的副作用，用一个独立标志承载。
交互模式下默认答案是「是」，因此「新项目时一次性装好」在人工使用时依然成立。

**安装失败绝不影响建项目**：项目此时已经生成完毕，安装失败只记 Warn 并给出提示，
`yd new` 仍返回成功。这条与 CLAUDE.md §8「工具缺失不得导致构建失败」同一口径。

## 3. 任务分解

| # | 任务 | 产物 |
|---|---|---|
| 1 | `tools` 包（注册表 / 探测 / 安装 / 提示） | `tools/*.go` + 测试 |
| 2 | `yd tools` 命令 | `cli/tools.go` + 测试 |
| 3 | `yd new` 收尾接入 + `--install-tools` | `cli/new.go` + 测试 |
| 4 | 脚手架模板同步 | `scaffold/templates/**`（README / 生成项目治理文档） |
| 5 | 治理文档 | `CLAUDE.md`、`AGENTS.md`、`docs/developer_guidelines.md` |
| 6 | 守卫与验收 | `internal/repotest`、`docs/verification.md` |

## 4. 验收标准

1. `yd tools check` 在缺工具与齐全两种机器状态下都给出正确结论（用注入的探测结果覆盖）。
2. `yd tools install` 把二进制定位到 `<项目>/.yd/tools/`，安装后 `Check` 由缺失转为就绪。
3. **测试绝不真的执行 `go install` 或触网**：命令执行点必须可注入。
4. `yd new` 交互模式会询问并安装；`--yes` 不触网；`--install-tools` 强制安装。
5. 安装失败时 `yd new` 仍成功，且错误可 `errors.Is` 到哨兵。
6. 每个注册表条目的 `InstallHint` 非空，且与 `align.InstallHint` / `ko.InstallHint` 一致（守卫断言）。
7. 全仓门禁：`go build` / `go vet` / `gofmt` 零输出，`go test -race ./...` 全绿，交叉编译通过。

## 5. 已知取舍

- **不自动改 `PATH`**：安装只落在项目内 `.yd/tools/`，解析链天然能找到；往用户 `PATH` 或
  `~/go/bin` 写东西属于越界，且在 CI 上不可预期。
- **不静默安装**：除交互确认与显式 `--install-tools` 外，任何命令都不擅自触网。
- **docker 与 tailwindcss 只提示不代装**：前者是系统级守护进程，后者由 `yd assets` 管线管理，
  统一走 `go install` 反而会误导。

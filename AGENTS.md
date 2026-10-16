# AGENTS.md — 自主 AI Agent 自动化规约

面向在 **yd** 仓库内自主执行「代码修复 / 功能迭代」的 AI Agent。本文件定义修改权限边界、测试自愈流程与安全合规检查。

## 1. 修改权限边界

**允许直接修改**

- 源码目录：`config/`、`afs/`、`jsonx/`、`b64x/`、`logx/`、`router/`、`render/`、`htmx/`、`sse/`、`ws/`、`jwt/`、`guomi/`、`middleware/`、`i18n/`、`openapi/`、`server/`、`grpcx/`、`data/**`、`app/`、`cli/`、`scaffold/`、`gen/`、`build/`、`docker/`、`assets/`、`toolchain/`、`web/**`
- 测试与文档：`*_test.go`、`testdata/**`、`docs/**`（除需求基线）

**禁止修改**

- `docs/需求设计说明书.md` —— 需求基线，只读。
- `LICENSE` 的授权条款正文（仅允许更新末尾版权行）。
- 任何已锁定校验和的 `web/static/js/vendor/**` 内容——升级必须走 `yd assets update` 重新锁定，禁止手改。

**需要人工确认**

- 新增第三方依赖（尤其引入 cgo 或超过 1MB 体积的依赖）。
- 变更公开 API 的破坏性签名（须在提交信息中标注 `BREAKING CHANGE`）。
- 修改 `config` 的默认值（会影响所有下游项目）。

## 2. 测试自愈流程（Test-and-Fix）

任何修改都必须以如下闭环收尾，**最多 3 轮**；3 轮未收敛则停止并输出诊断报告，不得继续盲改。

```
1. go build ./...            # 编译
   └─ 失败 → 读编译错误 → 定点修复 → 回到 1
2. go vet ./...              # 静态检查
   └─ 告警 → 修复 → 回到 1
3. go test ./... -race       # 单元测试
   └─ 失败 → 用 systematic-debugging 定位根因（禁止改测试断言来"通过"）→ 修复 → 回到 1
4. GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...   # 交叉编译护栏
5. go run ./cmd/yd assets verify --offline                 # 资产完整性
```

自愈规则：

- **不得**通过删除/注释测试、放宽断言、`t.Skip` 来让流水线变绿。
- 修复缺陷时必须**先补一条能复现缺陷的测试**，再修实现。
- 每轮自愈的失败证据（命令 + 原始输出）必须保留在提交信息或 PR 描述中。

## 3. 安全合规检查清单

提交前逐条自检：

- [ ] **密钥零硬编码**：源码、模板、测试夹具中不得出现真实密钥/口令/Token（用环境变量或 `testdata` 中的公开测试向量）。
- [ ] **国密合规**：涉及敏感数据（身份证、银行卡、口令）的加解密必须走 `guomi`（SM2/SM3/SM4），禁止 MD5/SHA1 用于口令存储。
- [ ] **口令存储**：必须使用 `guomi.HashPassword`（SM3 + 盐 + 迭代），比较必须 `subtle.ConstantTimeCompare`。
- [ ] **传输加密**：前端敏感字段提交必须声明式触发 SM2 前置加密（`data-yd-encrypt="sm2"`）。
- [ ] **依赖白名单**：新增依赖须为活跃维护的纯 Go 库，且无已知高危 CVE；不得引入运行时 CDN。
- [ ] **注入面**：SQL 只允许通过 GORM 参数化查询；模板输出默认走 `html/template` 转义，使用 `template.HTML` 必须写明可信来源。
- [ ] **请求边界**：外部输入必须限长（WebSocket `MaxMessageSize`、请求体 `MaxBytesReader`），并设置读写超时。
- [ ] **JSON/Base64 统一走封装层**：全仓禁止 `import "encoding/json"` 与 `import "encoding/base64"`，
      一律使用 `github.com/yun-dev/yd/jsonx` 与 `github.com/yun-dev/yd/b64x`。
      自检：`grep -rn '"encoding/json"\|"encoding/base64"' --include=*.go .` 无命中。
- [ ] **中英文同步（注释 + 日志文案）**：所有注释与所有日志文案逐行采用
      `English | 中文`（竖线分割，英文在前、中文在后）。豁免：编译指令、生成标记、注释内代码示例/树状图/URL。
- [ ] **日志统一走 logx**：全仓禁止 `import "log"`（`server` 包的 `http.Server.ErrorLog`
      适配器除外，且其输出必须转发进 logx），禁止 `fmt.Print*` 当日志，禁止第三方日志库。
- [ ] **OpenAPI 注释完整**：对外 HTTP 处理器的 doc 注释必须含完整注解块
      （`@Summary/@Description/@Tags/@Accept/@Produce/@Param/@Success/@Failure/@Security/@Router`），缺项即未完成。
- [ ] **敏感日志**：访问日志与慢查询日志不得输出口令、Token、密钥明文；使用 `logger.ParamsFilter` 屏蔽。
- [ ] **CORS**：`AllowCredentials=true` 时禁止回显 `*`。
- [ ] **资产完整性**：`web/assets.lock.json` 中每项必须有 sha256，且 `yd assets verify --offline` 通过。

## 4. 提交纪律（强制，Agent 必须遵守）

- **提交信息一律使用中文 Conventional Commits**，格式：
  ```
  <类型>(<范围>): <中文简述>

  <可选正文>

  Co-Authored-By: Eric Shi <shibingli@sina.com>
  ```
- **署名口径唯一**：本仓所有提交一律使用 `Co-Authored-By: Eric Shi <shibingli@sina.com>`，
  不得替换、不得省略、不得追加其他署名。
- **严禁任何 Claude / Anthropic / AI 相关内容**出现在提交信息、作者信息、脚注、
  分支名、PR 描述、代码注释与文档中（含 `Claude`、`claude`、`Anthropic`、
  `noreply@anthropic.com`、`Generated with Claude Code`、`🤖` 等）。
  提交前自检：`git log -1 --format=%B | grep -iE 'claude|anthropic'` 必须无输出。
- Agent 在自动化流程中创建提交时，必须先设置作者身份：
  `git -c user.name="Eric Shi" -c user.email="shibingli@sina.com" commit ...`

## 5. 交付物要求

- 每个功能变更须包含：实现 + 单元测试 + （如涉及用户可见行为）`docs/` 下的说明更新。
- 新增包须在包文档注释中说明其职责边界与不做什么。
- 生成的代码变更须能通过 `yd gen` 幂等重放（重复执行不产生 diff）。

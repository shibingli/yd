# yd 全栈 Web 框架与脚手架 —— 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 `github.com/yun-dev/yd` —— 一个 Go 1.27+ 全栈 Web 框架库 + `yd` 脚手架 CLI，交付从 `yd new` 到 `yd docker` 的完整生命周期能力，后端融合 Htmx 4.0 事件总线与 Tailwind v4 样式体系。

**Architecture:** 单一 Go Module，同时承载「框架库」（可被脚手架生成的项目 import）+「CLI」（`cmd/yd`）+「内嵌模板与前端资产」（`go:embed`）。框架库内部自研核心原语（配置引擎、基数树路由、零拷贝日志、自适应 JSON/Base64、RFC6455 WebSocket、国密套件），外部生态（GORM / go-redis / asynq / quic-go / grpc）以独立子包适配。路由实现 `http.Handler`，因此 HTTP/1.1、H2C、HTTP/3、ACME 与 gRPC 双通道可复用同一 Handler。前端资产全部 `go:embed` 进二进制，通过带校验和的资产清单支持在线更新与完全离线运行。

**Tech Stack:** Go 1.27.2 · viper(v1.21) + pflag(v1.0.10) + afero(v1.15) · sonic(v1.15.4)/go-json(v0.11.2) 条件编译 · cristalhq/base64(v0.1.2) · gorm(v1.31.2) · go-redis/v9(v9.23) · asynq(v0.26) · quic-go(v0.64) · grpc · gmsm(v1.4.1) · klauspost/compress + andybalholm/brotli · htmx 4.0.0 + Alpine 3.17.4 + Tailwind v4.3.3 standalone。

**Spec:** `docs/需求设计说明书.md`

---

## Global Constraints

- **语言版本底线**：`go 1.27.2`（go.mod `go` 指令写 `1.27.2`），允许更高。
- **模块路径**：`github.com/yun-dev/yd`；CLI 二进制名 `yd`；环境变量前缀 **`YD_`**
  （需求基线原文写 `YUNGO_`，团队决定统一改为 `YD_`；基线文档只读不改，以本计划为准）。
- **提交纪律**：中文 Conventional Commits + 固定署名尾行
  `Co-Authored-By: Eric Shi <shibingli@sina.com>`；
  提交信息、作者、PR 与代码注释中**严禁出现任何 Claude / Anthropic / AI 助手相关内容**。
  自检：`git log -1 --format=%B | grep -iE 'claude|anthropic'` 必须无输出。
- **开源协议**：Apache-2.0（`LICENSE` 必须为完整 Apache License 2.0 原文）。归属团队 YunDev（yun-dev.cn、yun-dev.net）。
- **配置优先级（高→低）**：命令行参数 > 环境变量(含 `.env`) > 配置文件(yaml/v3 / TOML) > `viper.SetDefault` 默认值。环境变量 `YD_DB_MAXCONN` 必须映射到 `db.maxconn`。
- **前端资产零 CDN**：运行时严禁从公网 CDN 动态引入；全部 `go:embed`。同时支持在线更新（`yd assets update`）与离线独立使用（`yd assets --offline`，校验和不符即失败）。
- **架构规范**：脚手架产物严格遵循 golang-standards/project-layout；Docker 产物基于 `scratch`。
- **国密组件**：仅允许纯 Go 实现（gmsm），禁止 cgo/外挂非 Go 组件。
- **零拷贝主张**：日志、WebSocket、Base64、JWT 载荷路径不得引入中间字符串/缓冲区复制；热路径禁止反射。
- **中英文同步注释（强制）**：所有注释逐行采用 `English | 中文` 形式（半角竖线分割，英文在前、中文在后，
  竖线两侧各一个空格）。豁免：`//go:build` 指令、`// Code generated ... DO NOT EDIT.`、注释内代码示例/目录树/URL。
- **OpenAPI 注释必须完整（强制）**：对外 HTTP 处理器的 doc 注释必须含完整注解块
  （`@Summary/@Description/@Tags/@Accept/@Produce/@Param/@Success/@Failure/@Security/@Router`），缺项视为未完成；
  `openapi` 包负责从源码解析注解并与路由树合并。
- **JSON/Base64 统一封装层（强制）**：全仓禁止 `import "encoding/json"` 与 `import "encoding/base64"`，
  JSON 一律走 `jsonx`，Base64 一律走 `b64x`；`logx` 的零拷贝编码器手写 JSON 字节，同样不得引入 `encoding/json`。
  守卫测试见 Task 1 的 `internal/repotest`。
- **默认蓝色主题**：Tailwind v4 `@theme` 语义化变量，主色为蓝色系。
- 所有包必须 `go vet ./...` 通过；`CGO_ENABLED=0` 下可交叉编译到 linux/amd64、linux/arm64、windows/amd64。

## Review Focus

以下 5 类输入/条件最可能在使用中咬人，每条都必须在指定任务里有对应测试：

1. **路由混合分段**：`/:id-xx.png` 与字面量 `/abc-xx.png` 同时存在、`/:id.jpg` 收到 `/.jpg`、百分号编码（`/a%2Fb.jpg`）、重复参数名 —— 期望静态段优先、参数捕获最短且确定、非法编码不被错误解码；`router` 任务需覆盖。
2. **配置优先级**：pflag 带非空默认值与环境变量/配置文件同时存在 —— 未显式传参时**必须**由 env/文件胜出（viper 经典陷阱）；`config` 任务需覆盖。
3. **WebSocket 帧边界**：方向性 mask 校验、64 位长度、控制帧插入分片消息、非法 UTF-8、超限消息 —— 必须按 RFC 6455 返回 1002/1007/1009 且无 goroutine 泄漏；`ws` 任务需覆盖。
4. **资产离线**：断网 / 404 / 校验和不符 / `--offline` 且有文件缺失 —— 不得污染已存在的 vendor 目录，`--offline` 绝不触网并明确报错；`assets` 任务需覆盖。
5. **压缩协商**：已压缩内容、HEAD、204/304、SSE 流、WS 升级握手 —— 不得被包裹；`Accept-Encoding: gzip;q=0` 必须禁用 gzip；`compress` 任务需覆盖。

---

## 文件结构（责任划分）

```
yd/
├── go.mod  LICENSE  README.md  CLAUDE.md  AGENTS.md  Dockerfile
├── .gitignore  .gitattributes  .env.template
├── cmd/yd/main.go                        # CLI 入口（仅装配）
├── config/                               # viper 分层配置引擎
├── afs/                                  # afero 抽象文件系统门面
├── jsonx/                                # 自适应 JSON（build tags 分流）
├── b64x/                                 # 自适应 Base64（build tags 分流）
├── logx/                                 # 零拷贝日志
├── router/                               # 基数树路由（tree/pattern/http 适配）
├── render/                               # html/template 渲染引擎
├── htmx/                                 # Htmx 4 服务端辅助（HX-* 头 / partial / oob）
├── sse/                                  # W3C SSE 流 + 广播
├── ws/                                   # RFC6455 纯自研实现
├── jwt/                                  # 免反射 JWT
├── guomi/                                # SM2/SM3/SM4 强类型套件
├── middleware/                           # cors ratelimit compress i18n recover requestid accesslog static
├── i18n/                                 # 目录加载 + 翻译器
├── openapi/                              # OpenAPI 3.0 文档构建 + 内置查看器
├── server/                               # HTTP/1.1 + H2C + HTTP/3 + ACME + 优雅退出
├── grpcx/                                # gRPC 双通道（同端口 h2c 分流 / 独立端口）
├── data/gormx/  data/redisx/  data/asynqx/   # 数据生态插件
├── app/                                  # 应用门面（装配以上一切）
├── web/                                  # go:embed 前端资产（static/ templates/ assets.lock.json）
├── scaffold/                             # yd new 的项目模板（模板化 + 渲染）
├── gen/                                  # 代码生成器（module / crud）
├── toolchain/                            # 外部工具（tailwindcss）解析、下载、校验
└── cli/                                  # 命令实现 new/dev/gen/build/docker/assets
```

---

# Phase A —— 基础原语

### Task 1: 仓库骨架与治理文件

**Files:**
- Create: `go.mod`, `LICENSE`, `README.md`, `CLAUDE.md`, `AGENTS.md`, `.gitignore`, `.gitattributes`, `.env.template`, `docs/developer_guidelines.md`, `Dockerfile`
- Test: `internal/repotest/repo_test.go`（校验治理文件存在且内容符合硬约束）

**Interfaces:**
- Produces: 模块 `github.com/yun-dev/yd`，`go 1.27.2`；后续所有包以此为根。

- [ ] **Step 1: 写失败测试** `internal/repotest/repo_test.go`
  - 断言 `LICENSE` 含 `Apache License`、`Version 2.0, January 2004`、且行数 > 180；
  - 断言 `.gitignore` 逐行包含规范中给出的 22 行（含 `bin/`、`dist/`、`.tailwindcss/`、`node_modules/`）；
  - 断言 `.gitattributes` 包含 `* text=auto eol=lf` 与 `*.png binary`；
  - 断言 `CLAUDE.md` 含 `Apache-2.0`、`YunDev`、`go 1.27`、`YD_`、`Co-Authored-By: Eric Shi <shibingli@sina.com>`；
  - 断言 `AGENTS.md` 含 `Test-and-Fix` 与同一署名口径；
  - 断言 `CLAUDE.md`/`AGENTS.md` 中出现 `claude`/`anthropic` 的行**仅限**"禁止出现"这类约束语句本身（提交纪律自检）。
- [ ] **Step 2: 运行测试确认失败** `go test ./internal/repotest/ -run TestGovernanceFiles -v` → FAIL（文件不存在）
- [ ] **Step 3: 创建文件**
  - `go.mod`：module + go 1.27.2；
  - `LICENSE`：Apache License 2.0 完整原文（含 APPENDIX）；
  - `.gitignore` / `.gitattributes`：**逐字**复制 Spec §二.3 给出的两份模板（去掉"请谨慎使用此类代码。"提示行）；
  - `CLAUDE.md`：技术栈版本表、仓库地址 `https://github.com/yun-dev/yd`、协议 Apache-2.0、归属 YunDev、代码风格（错误包装 `%w`、包内禁止 `init()` 副作用、导出符号必须有文档注释、热路径禁止反射）；
  - `AGENTS.md`：修改权限边界（不得改 `docs/需求设计说明书.md`）、Test-and-Fix 自愈流程（编译失败→修→重跑，最多 3 轮）、安全合规检查清单（国密、密钥不入库、依赖白名单）；
  - `docs/developer_guidelines.md`：前后端协同约束（端点必须返回 HTML 片段而非 JSON、`hx-*` 命名、模板目录约定、新增 htmx 扩展须登记 assets.lock.json）；
  - `Dockerfile`：多阶段 `golang:1.27-alpine` → `scratch`（见 Task 31 详细约定）。
- [ ] **Step 4: 运行测试确认通过** `go test ./internal/repotest/ -v` → PASS
- [ ] **Step 5: git init 并提交** `git init && git add -A && git commit -m "chore: bootstrap yd module and governance files"`

---

### Task 2: 分层配置引擎 `config`

**Files:**
- Create: `config/config.go`, `config/defaults.go`, `config/env.go`, `config/config_test.go`

**Interfaces:**
- Consumes: 无。
- Produces:
  - `type Options struct { Name string; ConfigDir string; ConfigName string; EnvPrefix string; EnvFile string; FS afero.Fs; Flags *pflag.FlagSet }`
  - `type Config struct { *viper.Viper }`
  - `func Load(opts Options) (*Config, error)`
  - `func (c *Config) BindFlags(fs *pflag.FlagSet, mapping map[string]string) error`（`mapping` 形如 `{"port":"server.port"}`）
  - `func (c *Config) Watch(fn func(*Config))`（viper.WatchConfig 封装）
  - `func (c *Config) Unmarshal(raw any) error`（强类型导出到 struct）
  - `config.Defaults(c *Config)`：注册全部安全底线默认值。
- 关键规则：`SetEnvPrefix("YUNGO")`、`SetEnvKeyReplacer(strings.NewReplacer(".", "_"))`、`AutomaticEnv()`；`.env` 通过 afero 读取并 `SetDefault` 注入（env 优先），**不覆盖已存在的真实环境变量**；配置文件支持 yaml/yaml.v3/toml/json，搜索 `ConfigDir` 与 `.`；`ConfigName` 默认 `config`，并按 `config.<ext>` 与 `config.local.<ext>` 顺序合并（local 覆盖）。

- [ ] **Step 1: 写失败测试** `config/config_test.go`
  - `TestPriorityFlagBeatsEnvAndFile`：文件写 `server.port: 8080`、env `YD_SERVER_PORT=9090`、flag `--port` 未设置 → 期望 `9090`；`flag.Set("port","7070")` 后再 `BindFlags` → 期望 `7070`。**必须**做到「未显式设置的 flag（含默认值 8080）不得覆盖 env」——实现手法：`BindFlags` 前对每个 flag 检查 `f.Changed`，未 Changed 则改用 `SetDefault` 从 `f.DefValue` 注入。
  - `TestNestedEnvMappingFromDotEnv`：`.env` 内 `YD_DB_MAXCONN=42` → `GetInt("db.maxconn")==42`。
  - `TestTypedUnmarshal`：`Unmarshal(&struct{ Server struct{ Port int } }{})` 得到 9090。
  - `TestConfigLocalOverridesBase`：`config.yaml` 与 `config.local.yaml` 同时存在，后者胜出。
  - `TestWatchReload`：写入新文件内容后 200ms 内 `fn` 被调用一次。
- [ ] **Step 2: 运行确认失败** `go test ./config/ -v` → FAIL
- [ ] **Step 3: 实现** `config.go`（Load/BindFlags/Watch/Unmarshal）、`env.go`（afero 读 `.env`：`KEY=VALUE`、`#` 注释、可选用引号）、`defaults.go`（server.port=8080、server.read_timeout=15s、server.write_timeout=15s、server.idle_timeout=60s、log.level=info、log.format=json、server.h2c=true、server.http3.enabled=false、db.maxconn=100、redis.mode=single 等）。
- [ ] **Step 4: 运行确认通过** `go test ./config/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(config): layered viper config engine with flag/env/file/default precedence"`

---

### Task 3: 抽象文件系统门面 `afs`

**Files:**
- Create: `afs/afs.go`, `afs/afs_test.go`

**Interfaces:**
- Produces:
  - `type FS = afero.Fs`
  - `func NewOS(root string) FS`、`func NewMem() FS`、`func NewEmbed(f fs.FS) FS`（`afero.FromIOFS`）
  - `func CopyDir(src, dst FS, srcDir, dstDir string) error`（递归复制，保留权限位，跳过目录自身）
  - `func WriteFileIfChanged(fs FS, path string, data []byte) (bool, error)`（内容相同不写，返回是否变更 —— 供 dev 热重载与 assets 更新复用）
  - `func Exists(fs FS, path string) bool`、`func Sha256File(fs FS, path string) (string, error)`
  - `func Walk(fs FS, root string, fn func(path string, info os.FileInfo, err error) error) error`
- 说明：`NewOS("")` 等价 `afero.NewOsFs()`；`NewMem()` 用于测试与虚拟化。

- [ ] **Step 1: 写失败测试**：`TestMemFSRoundTrip`（Mem 上写读一致）、`TestCopyDirPreservesNestedFiles`、`TestWriteFileIfChangedSkipsIdentical`、`TestSha256FileMatchesCrypto`。
- [ ] **Step 2: 运行确认失败** `go test ./afs/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./afs/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(afs): afero facade with mem/os/embed backends"`

---

### Task 4: 自适应 JSON 抽象层 `jsonx`

**Files:**
- Create: `jsonx/jsonx.go`, `jsonx/jsonx_amd64.go`(build tag `amd64 && !jsonx_std`), `jsonx/jsonx_generic.go`(build tag `!amd64 || jsonx_std`), `jsonx/jsonx_test.go`
- Modify: `go.mod`（+ `github.com/bytedance/sonic`, `github.com/goccy/go-json`）

**Interfaces:**
- Produces（两个实现文件必须导出完全相同的符号集，`jsonx.go` 只放 `doc.go` 式说明与共享错误变量）：
  - `func Marshal(v any) ([]byte, error)`
  - `func Unmarshal(data []byte, v any) error`
  - `func MarshalIndent(v any, prefix, indent string) ([]byte, error)`
  - `func Valid(data []byte) bool`
  - `func NewEncoder(w io.Writer) *Encoder` / `func NewDecoder(r io.Reader) *Decoder`（`Encoder.Encode/SetEscapeHTML`、`Decoder.Decode/UseNumber`）
  - `func Backend() string`（`"sonic"` / `"go-json"` / `"encoding/json"`）
- 说明：`jsonx_std` 构建标签走标准库（用于排障与兼容性测试）。

- [ ] **Step 1: 写失败测试** `jsonx_test.go`：`TestRoundTripStandardTypes`（struct/map/slice/pointer/嵌套/`json.RawMessage`/`time.Time`/`omitempty`）、`TestUnmarshalIntoAnyMatchesStdlib`、`TestEncoderDecoderStream`、`TestValid`、`TestBackendNameIsKnown`。
- [ ] **Step 2: 运行确认失败** `go test ./jsonx/ -v` → FAIL
- [ ] **Step 3: 实现**：amd64 文件 `import "github.com/bytedance/sonic"` 转发；generic 文件 `import gojson "github.com/goccy/go-json"` 转发；标准库变体以同名 API 直接转 `encoding/json`。
- [ ] **Step 4: 运行确认通过**：`go test ./jsonx/ -v` 与 `go test -tags jsonx_std ./jsonx/ -v`、`GOARCH=arm64 go build ./jsonx/` 均需 PASS。
- [ ] **Step 5: 提交** `git commit -m "feat(jsonx): hardware-adaptive JSON with sonic/go-json/stdlib build tags"`

---

### Task 5: 自适应 Base64 抽象层 `b64x`

**Files:**
- Create: `b64x/b64x.go`, `b64x/b64x_amd64.go`(build tag `amd64 && !b64x_std`), `b64x/b64x_generic.go`(build tag `!amd64 || b64x_std`), `b64x/b64x_test.go`
- Modify: `go.mod`（+ `github.com/cristalhq/base64`）

**Interfaces:**
- Produces（两实现符号一致）：
  - `var StdEncoding, RawStdEncoding, URLEncoding, RawURLEncoding *Encoding`
  - `type Encoding struct{ ... }`，方法：`Encode(dst, src []byte)`、`Decode(dst, src []byte) (int, error)`、`EncodeToString(src []byte) string`、`DecodeString(s string) ([]byte, error)`
  - **零拷贝扩展**：`AppendEncode(dst, src []byte) []byte`、`AppendDecode(dst, src []byte) ([]byte, error)`、`DecodedLen(n int) int`、`EncodedLen(n int) int`
  - `func Backend() string`
- 说明：amd64 底层用 `cristalhq/base64`（SIMD/asm 加速），非 amd64 降级 `encoding/base64`。

- [ ] **Step 1: 写失败测试**：`TestStdEncodingMatchesStdlib`（对随机 0..256 字节长度逐一对拍 `encoding/base64`）、`TestURLAndRawVariants`、`TestAppendEncodePreallocates`（容量足够时 `&dst[0]` 不变，证明零重分配）、`TestDecodeErrorOnInvalid`、`TestBackendNameIsKnown`。
- [ ] **Step 2: 运行确认失败** `go test ./b64x/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过**：`go test ./b64x/ -v`、`go test -tags b64x_std ./b64x/ -v`、`GOARCH=arm64 go build ./b64x/` 均 PASS。
- [ ] **Step 5: 提交** `git commit -m "feat(b64x): hardware-adaptive base64 with stdlib fallback"`

---

### Task 6: 零拷贝日志系统 `logx`

**Files:**
- Create: `logx/logx.go`, `logx/field.go`, `logx/encoder_json.go`, `logx/encoder_console.go`, `logx/level.go`, `logx/pool.go`, `logx/logx_test.go`, `logx/bench_test.go`

**Interfaces:**
- Produces:
  - `type Level int8`：`DebugLevel, InfoLevel, WarnLevel, ErrorLevel, FatalLevel`；`func ParseLevel(string) (Level, error)`
  - `type Field struct{ Key string; Type FieldType; ... }`（值以联合字段承载，热路径不装箱）
  - 构造函数：`Str(k, v string) Field`、`Strs`、`Int(k string, v int) Field`、`Int64`、`Uint64`、`Float64`、`Bool`、`Time(k string, v time.Time)`、`Dur(k string, v time.Duration)`、`Err(err error) Field`、`Bytes(k string, v []byte) Field`、`Any(k string, v any) Field`（显式标注为慢路径）、`Raw(JSON []byte) Field`
  - `type Logger struct{...}`：`New(opts Options) *Logger`，`Options{ Writer io.Writer; Level Level; Format string /* json|console */; TimeFormat string; Caller bool; Service string }`
  - 方法：`Debug/Info/Warn/Error(msg string, fields ...Field)`、`Fatal(...)`、`With(fields ...Field) *Logger`、`Named(string) *Logger`、`Enabled(Level) bool`、`Sync() error`、`SetLevel(Level)`
  - `func (l *Logger) Slow(threshold time.Duration) *Logger`（返回子 logger，供 gormx 打点）—— 或独立函数 `logx.SlowQuery(l *Logger, sql string, rows int64, el time.Duration, err error)`。
- 实现要点：`sync.Pool` 持有 `[]byte` 缓冲（初始 1KB，超过 64KB 不放回）；`Enabled` 前置判断避免构造字段；JSON 编码直接用 `strconv.AppendInt/AppendFloat` + `append`，**不产生中间 string**；每行一次 `Write`；`Fatal` 记录后 `os.Exit(1)`（提供 `Options.ExitFunc` 便于测试）。

- [ ] **Step 1: 写失败测试** `logx_test.go`：`TestJSONShapeStable`（解析输出 JSON，断言 `level/ts/msg/字段`）、`TestLevelFiltering`、`TestWithAddsPersistentFields`、`TestErrFieldUnwraps`、`TestConsoleFormatHumanReadable`、`TestSlowQueryFields`。
- [ ] **Step 2: 运行确认失败** `go test ./logx/ -v` → FAIL
- [ ] **Step 3: 实现** + `bench_test.go`：`BenchmarkInfoFields`、`BenchmarkDisabledLevel`（断言 `AllocsPerRun` 足够低，作为回归护栏而非硬阈值：Disabled 路径 0 allocs）。
- [ ] **Step 4: 运行确认通过** `go test ./logx/ -v -bench . -benchtime 100x` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(logx): zero-copy structured logger with pooled byte buffers"`

---

# Phase B —— HTTP 核心

### Task 7: 基数树路由 —— 模式解析与匹配内核

**Files:**
- Create: `router/pattern.go`, `router/tree.go`, `router/params.go`, `router/tree_test.go`, `router/pattern_test.go`

**Interfaces:**
- Produces:
  - `type TokenKind uint8`（`TokenLiteral`, `TokenParam`）
  - `type Token struct{ Kind TokenKind; Text string /* literal 文本或参数名 */ }`
  - `type Segment struct{ Raw string; Tokens []Token; Kind SegmentKind }`，`type SegmentKind uint8`（`SegStatic`, `SegParam`, `SegMixed`, `SegWildcard`）
  - `func ParsePattern(p string) ([]Segment, error)` —— 支持 `:name`、`*name`（catch-all，仅末段）、混合段 `:id.jpg`、`:id-xx.png`、`file-:name.:ext`；转义 `\:` 表示字面冒号；参数名 `[A-Za-z_][A-Za-z0-9_]*`；空段（`//`）报错 `ErrBadPattern`。
  - `func matchSegment(seg *Segment, value string) ([]Param, bool)` —— 用「带记忆化的回溯」在 token 序列上匹配：状态 `(tokenIdx, pos)` 记忆化，保证最坏 O(tokens×len) 且不指数爆炸；参数捕获**最短优先**（左到右非贪婪），静态 token 精确匹配。
  - `type Params struct{ keys []string; values []string }`：`Get(name string) string`、`ByName`、`Len()`、`Reset()`、`Append(key, val string)`（由 `sync.Pool` 复用）。
  - `type Tree struct{...}`：`Add(method, pattern string, handler any) error`、`Find(method, path string, ps *Params) (handler any, tsr bool)`、`Allowed(path string) []string`、`Walk(fn func(method, pattern string, handler any))`
  - 优先级：静态段 > 混合/参数段 > 通配段；同一父节点下静态子节点按首字节索引；匹配失败回溯到次优先子节点；`tsr` 表示尾斜杠重定向需求。
- 冲突检测：同 method 下重复模式 `ErrConflict`；同名参数在同一模式中重复 `ErrDuplicateParam`。

- [ ] **Step 1: 写失败测试**（`pattern_test.go` + `tree_test.go`）
  - `TestMatchMixedSuffix`：模式 `/:id.jpg`，路径 `/avatar.jpg` → `id=avatar`；`/.jpg` → `id=""` 需按 Spec 语义**404**（参数必须非空），`/a/b.jpg` → 404。
  - `TestMatchMixedWithStaticAndDash`：模式 `/:id-xx.png`，`/42-xx.png` → `id=42`；`/42-yy.png` → 404；`/a-b-xx.png` → `id=a-b`。
  - `TestStaticBeatsMixed`：同时注册 `/abc-xx.png` 与 `/:id-xx.png`，前者命中静态处理器。
  - `TestMultiParamSegment`：`/archive/:y-:m-:d.html`，`/archive/2026-10-16.html` → 三参齐全且最短优先（`y=2026`）。
  - `TestWildcardCatchAll`：`/static/*filepath` 捕获含 `/` 尾段。
  - `TestPercentEncodedSegmentNotDecoded`：`/a%2Fb.jpg` 在 `/:id.jpg` 上捕获 `id=a%2Fb`（保持原始编码，由上层决定解码）。
  - `TestDuplicateParamRejected`、`TestConflictRejected`、`TestTrailingSlashFlag`、`TestParamsReuseNoAlloc`（Pool 复用后 `AllocsPerRun` 为 0）。
- [ ] **Step 2: 运行确认失败** `go test ./router/ -run 'TestMatch|TestStatic|TestWildcard|TestPercent|TestDuplicate|TestConflict|TestTrailing|TestParams|TestMultiParam' -v` → FAIL
- [ ] **Step 3: 实现**：`pattern.go` 分词 + `matchSegment` 记忆化匹配；`tree.go` 节点（静态子表 `map[byte][]*node` + `staticChildren`、`paramChild`、`mixedChildren`、`wildcardChild`）+ 回溯式 `Find`；`params.go` Pool。
- [ ] **Step 4: 运行确认通过** `go test ./router/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(router): radix tree with partial-path and suffix segment matching"`

---

### Task 8: 路由 HTTP 适配层

**Files:**
- Create: `router/router.go`, `router/route.go`, `router/group.go`, `router/ctx.go`, `router/router_test.go`

**Interfaces:**
- Consumes: Task 7 的 `Tree`、`Params`。
- Produces:
  - `type HandlerFunc func(*Ctx)`
  - `type Ctx struct{ Writer http.ResponseWriter; Request *http.Request; Params *Params; ... }` 方法：`Param(name) string`、`Query(k) string`、`QueryDefault(k, def)`、`Header(k)`、`SetHeader`、`Status(int) *Ctx`、`String(int, string) error`、`JSON(int, any) error`（用 jsonx）、`HTML(int, template string, data any) error`、`NoContent() error`、`Bind(v any) error`（jsonx 解 JSON / 表单 / query 三态）、`Abort()`、`Next()`（中间件链）、`Set/Get(key, any)`。
  - `type Router struct{...}`：`New(opts ...Option) *Router`、`Use(mw ...Middleware)`、`GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS(pattern string, h HandlerFunc)`、`Group(prefix string, mw ...Middleware) *Group`、`Static(urlPrefix, dir string) `、`StaticFS(urlPrefix string, fs afero.Fs)`、`ServeHTTP(w, r)`、`Routes() []RouteInfo`、`NotFound(h HandlerFunc)`、`MethodNotAllowed(h HandlerFunc)`、`PanicHandler(func(*Ctx, any))`。
  - `type Middleware func(*Ctx)`；`type RouteInfo struct{ Method, Pattern, Name string; Handler HandlerFunc }`。
  - `Ctx` 由 `sync.Pool` 复用；`Router` 实现 `http.Handler`。
- 行为：405 自动带 `Allow` 头；`OPTIONS *` 返回全部允许方法；`tsr` 命中时 301/308 重定向；`*Ctx` 在中间件中未 `Next()` 即终止链。

- [ ] **Step 1: 写失败测试**（用 `httptest`）
  - `TestDynamicRoutesWithSuffixThroughHTTP`：`GET /42-xx.png` 返回处理器写入内容且 `c.Param("id")=="42"`。
  - `TestMethodNotAllowedHasAllowHeader`：注册 GET，发 POST → 405 且 `Allow: GET, OPTIONS`。
  - `TestGroupMiddlewareOrder`：两个 group 中间件按注册顺序执行，`Abort` 终止后续。
  - `TestTrailingSlashRedirects`：`/users/` 注册 → `GET /users` 308 到 `/users/`。
  - `TestCtxPoolResetNoParamLeak`：连续两请求，第二请求不得看到第一请求的 `Param`。
  - `TestStaticFileServedFromAferoFS`：MemFS 中放文件，`GET /static/a.txt` 命中，`ETag`/`Last-Modified` 存在。
  - `TestBindJSONFormAndQuery`。
- [ ] **Step 2: 运行确认失败** `go test ./router/ -run TestDynamicRoutesWithSuffixThroughHTTP -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./router/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(router): http adapter with ctx pool, groups and static fs serving"`

---

### Task 9: 模板渲染 `render` 与内嵌资产 `web`

**Files:**
- Create: `render/render.go`, `render/funcs.go`, `render/render_test.go`, `web/embed.go`, `web/assets.lock.json`（Task 33 填充内容，此处先建骨架）
- Modify: `go.mod`

**Interfaces:**
- Produces:
  - `render.New(fs afero.Fs, opts Options) (*Engine, error)`，`Options{ Pattern string /* 默认 "templates/**/*.html" */; Dev bool /* 关闭模板缓存 */; Funcs template.FuncMap; LeftDelim, RightDelim string }`
  - `(*Engine).Render(w io.Writer, name string, data any) error`
  - `(*Engine).Clone() *Engine`（`AddFunc` 后派生，避免全局可变状态）
  - `render.Funcs()`：`dict`、`list`、`tr(key, args...)`（i18n 钩子，由 i18n 包注入）、`asset(path)`（带哈希查询串）、`hx`（htmx 属性辅助）、`json`、`default`、`contains`、`hasPrefix`、`humanSize`、`humanTime`。
  - `web.FS() afero.Fs`（`go:embed static templates` 的只读 afero）；`web/embed.go` 只做 embed 与 FS 暴露，`//go:embed all:static all:templates assets.lock.json`。
- 说明：`Dev=true` 时每个模板集合不缓存（`yd dev` 热生效）。

- [ ] **Step 1: 写失败测试**：`TestRenderLayoutWithPartial`（layout + define + template 组合）、`TestDevModePicksUpTemplateChange`（改 MemFS 后重渲染输出变化）、`TestAssetFuncAppendsHash`、`TestFuncsDictAndDefault`。
- [ ] **Step 2: 运行确认失败** `go test ./render/ -v` → FAIL
- [ ] **Step 3: 实现**（注意：`html/template` 对 `hx-*` 属性无特殊处理，无需 `template.HTMLAttr`）
- [ ] **Step 4: 运行确认通过** `go test ./render/ ./web/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(render): embedded template engine with dev hot reload"`

---

### Task 10: Htmx 4 服务端辅助 `htmx`

**Files:**
- Create: `htmx/htmx.go`, `htmx/response.go`, `htmx/partial.go`, `htmx/htmx_test.go`

**Interfaces:**
- Consumes: `router.Ctx`（Task 8）、`render`（Task 9）。
- Produces:
  - `func IsRequest(r *http.Request) bool`、`func IsBoosted(r *http.Request) bool`、`func RequestType(r *http.Request) string`（`partial`/`full`）、`func Target(r *http.Request) string`、`func Source(r *http.Request) string`、`func CurrentURL(r *http.Request) string`
  - 响应头（写入 `http.ResponseWriter`，均为 Htmx 4 名称）：`Trigger(w, events ...Event)`（`Event{Name string; Detail any}`，JSON 形式）、`PushURL(w, url)`、`ReplaceURL(w, url)`、`Redirect(w, url)`、`Location(w, Location{Path, Target, Swap, ...})`、`Refresh(w)`、`Retarget(w, sel)`、`Reswap(w, swap string)`、`Reselect(w, sel)`
  - `type Partial struct{ Target, Swap string; Content template.HTML }`；`func RenderPartials(w io.Writer, parts ...Partial) error` —— 输出 `<hx-partial hx-target="#x" hx-swap="beforeend">…</hx-partial>`（htmx 4 首选多区域更新机制）
  - `func OOB(target, swap string, content template.HTML) template.HTML` —— `<div hx-swap-oob="innerHTML:#x">…</div>`（兼容路径）
  - `func Error(w http.ResponseWriter, status int, msg string) error` —— 配合前端 `hx-status:4xx/5xx`
  - `func TriggerFromHeader(w, name string)` 便捷封装。
- 说明：本包只依赖标准库 + `router` 的最小接口（`http.ResponseWriter`/`*http.Request`），不反向依赖 app。

- [ ] **Step 1: 写失败测试**：`TestIsRequestDetectsHxHeader`、`TestTriggerJSONEncoding`（`HX-Trigger` 为 `{"evt":{"k":1}}`）、`TestPushReplaceRedirectHeaders`、`TestPartialsRenderExactMarkup`、`TestOOBExactMarkup`、`TestRetargetReswapReselect`。
- [ ] **Step 2: 运行确认失败** `go test ./htmx/ -v` → FAIL
- [ ] **Step 3: 实现**（Htmx 4 头名清单见 spec 附录；注意 `HX-Trigger` 在 htmx 4 中仅作为**响应**头，请求侧已改名 `HX-Source`）
- [ ] **Step 4: 运行确认通过** `go test ./htmx/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(htmx): htmx 4 server helpers (headers, partials, oob)"`

---

### Task 11: 基础中间件（recover / requestid / accesslog / cors / ratelimit）

**Files:**
- Create: `middleware/middleware.go`, `middleware/recover.go`, `middleware/requestid.go`, `middleware/accesslog.go`, `middleware/cors.go`, `middleware/ratelimit.go`, `middleware/*_test.go`

**Interfaces:**
- Consumes: `router.Ctx`、`logx`。
- Produces（全部返回 `router.Middleware`）：
  - `Recover(l *logx.Logger) router.Middleware` —— 捕获 panic、记录堆栈、返回 500（若已写出头则不重复写）
  - `RequestID(header string) router.Middleware` —— 读 `X-Request-Id` 或 UUIDv4（`crypto/rand` 实现，不引外部 uuid 依赖），回写响应头与 `Ctx`
  - `AccessLog(l *logx.Logger) router.Middleware` —— 方法/路径/状态/字节/耗时/request_id
  - `CORS(o CORSOptions) router.Middleware`，`CORSOptions{ AllowOrigins []string /* 支持 *.example.com */; AllowMethods, AllowHeaders, ExposeHeaders []string; AllowCredentials bool; MaxAge time.Duration }`
  - `RateLimit(o RateLimitOptions) router.Middleware`，`RateLimitOptions{ Rate float64; Burst int; KeyFunc func(*router.Ctx) string; CleanupInterval time.Duration }` —— 分片令牌桶 + 惰性清理；超限返回 429 且带 `Retry-After`、`X-RateLimit-Limit/Remaining/Reset`。
  - `Chain(mw ...router.Middleware) router.Middleware`。
- 注意 CORS 三条硬规则：`AllowCredentials=true` 时不得回 `*`；预检请求（`OPTIONS` + `Access-Control-Request-Method`）直接 204 短路；未匹配来源时**不写** CORS 头（而非报错）。

- [ ] **Step 1: 写失败测试**：`TestRecoverReturns500AndLogs`、`TestRecoverDoesNotDoubleWrite`、`TestRequestIDFromHeaderAndGenerated`、`TestCORSWildcardSubdomain`、`TestCORSCredentialsNeverStar`、`TestCORSPreflightShortCircuits204`、`TestRateLimitBurstThen429WithHeaders`、`TestRateLimitKeyFuncIsolation`。
- [ ] **Step 2: 运行确认失败** `go test ./middleware/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./middleware/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(middleware): recover, request id, access log, cors, rate limit"`

---

### Task 12: 高级压缩中间件（gzip / brotli / zstd 权重协商）

**Files:**
- Create: `middleware/compress.go`, `middleware/compress_test.go`
- Modify: `go.mod`（+ `github.com/klauspost/compress`, `github.com/andybalholm/brotli`）

**Interfaces:**
- Produces:
  - `Compress(o CompressOptions) router.Middleware`
  - `CompressOptions{ Algorithms []Algorithm /* 默认 []Algorithm{Brotli, Zstd, Gzip}，数组顺序即优先级权重 */; MinSize int /* 默认 1024 */; Level int /* 0=各自默认 */; ExcludePatterns []string /* 默认匹配 /ws, /sse, /events 前缀 */ }`
  - `parseAcceptEncoding(h string) map[string]float64`（q 值解析；`q=0` 表示禁用）
  - `func Negotiate(accept string, algos []Algorithm) Algorithm`（导出以便单测与复用）
  - `Algorithm` 常量：`Gzip`, `Brotli`, `Zstd`，含 `Name()`、`Encoding()`（`gzip`/`br`/`zstd`）。
- 硬规则（Review Focus #5）：`HEAD`、`204`、`304`、已含 `Content-Encoding`、`Content-Type: text/event-stream`、`Upgrade` 请求、命中 `ExcludePatterns` —— 一律**不包裹** ResponseWriter；`WriteHeader` 前判断 `Content-Length < MinSize` 也不压缩（需延迟头写入，用 `bufWriter` 缓冲首个写入块）；编码器从 `sync.Pool` 取用并在 `Close` 归还；响应追加 `Vary: Accept-Encoding`。

- [ ] **Step 1: 写失败测试**
  - `TestNegotiateQValues`：`br;q=0.5, gzip;q=1.0` → Gzip；`gzip;q=0` → 不选 gzip；无头 → 不压缩。
  - `TestCompressGzipRoundTrip`：大 HTML 响应压缩后 `gzip.NewReader` 解出原文，且 `Content-Encoding: gzip`、`Vary` 存在。
  - `TestCompressSkipsSmallBody`：小于 `MinSize` 的响应无 `Content-Encoding`。
  - `TestCompressSkipsSSE`：`text/event-stream` 响应头不变且逐块 flush 生效。
  - `TestCompressSkipsNoContentAndNotModified`、`TestCompressSkipsHeadRequest`、`TestCompressSkipsUpgradeRequest`。
  - `TestBrotliAndZstdPreferredOverGzip`：默认权重下 `Accept-Encoding: gzip, br, zstd` → `br`。
- [ ] **Step 2: 运行确认失败** `go test ./middleware/ -run TestCompress -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./middleware/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(middleware): gzip/brotli/zstd negotiation with streaming-safe skips"`

---

### Task 13: 国际化 `i18n` 与本地化中间件

**Files:**
- Create: `i18n/i18n.go`, `i18n/translator.go`, `i18n/middleware.go`, `i18n/i18n_test.go`

**Interfaces:**
- Produces:
  - `type Bundle struct{...}`：`New(fs afero.Fs, dir string, defaultLang string) (*Bundle, error)` —— 目录下 `zh-CN.yaml` / `en.yaml` / `*.toml` / `*.json` 通过 viper 解析（复用配置引擎解析能力）；`Reload() error`
  - `(*Bundle).Translator(lang string) *Translator`；`(*Translator).T(key string, args ...any) string`（`{{.Name}}` 与 `%s` 两种占位风格都支持）、`(*Translator).Lang() string`
  - `Bundle.MatchLanguage(accept string) string` —— 解析 `Accept-Language`（q 值排序 + 前缀回退 `zh-CN`→`zh`→default）
  - 中间件：`i18n.Middleware(b *Bundle, o Options) router.Middleware`，`Options{ QueryParam string /* lang */; CookieName string /* yd_lang */; HeaderName string /* X-Lang */ }`，解析顺序：query > cookie > header > Accept-Language；把 `*Translator` 放入 `Ctx`；`i18n.FromContext(c *router.Ctx) *Translator`
  - `i18n.TemplateFunc(c *router.Ctx) func(key string, args ...any) string`（供 render.Funcs 注入）
- 硬约束：未知 key 返回 key 本身（不 panic）；缺失语言回退默认语言；并发安全（`sync.RWMutex` 保护热重载）。

- [ ] **Step 1: 写失败测试**：`TestLoadYamlAndTomlCatalogs`、`TestNestedKeyLookup`、`TestMissingKeyReturnsKey`、`TestArgsInterpolationBothStyles`、`TestMatchLanguageQValues`、`TestMatchLanguagePrefixFallback`、`TestMiddlewareResolutionOrder`、`TestConcurrentReload`（`-race`）。
- [ ] **Step 2: 运行确认失败** `go test ./i18n/ -race -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./i18n/ -race -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(i18n): bundle from afero fs with q-value language negotiation"`

---

### Task 14: OpenAPI 3.0 文档 `openapi`

**Files:**
- Create: `openapi/openapi.go`, `openapi/schema.go`, `openapi/middleware.go`, `openapi/viewer.go`, `openapi/openapi_test.go`

**Interfaces:**
- Consumes: `router.Routes()`（`RouteInfo`）、`jsonx`。
- Produces:
  - `type Document struct{ OpenAPI string; Info Info; Servers []Server; Paths map[string]PathItem; Components Components }`（字段带 json tag，`jsonx.Marshal` 输出）
  - `type Builder struct{...}`：`NewBuilder(info Info) *Builder`、`(b).AddRoute(method, pattern, o Operation)`、`(b).RegisterSchema(name string, v any) string`（基于 Go struct 反射生成 JSON Schema 引用，反射仅用于**构建期**，不进热路径）、`(b).Build() *Document`
  - `func FromRouter(r *router.Router, info Info, o Options) *Document` —— 遍历路由，把 `:id.jpg` 这类模式转换为 OpenAPI 路径模板 `{id}.jpg`，参数自动进 `parameters`。
  - `func (d *Document) JSON() ([]byte, error)`、`func (d *Document) YAML() ([]byte, error)`
  - `func Handler(d *Document, o Options) router.HandlerFunc` —— `GET {Path}`（默认 `/openapi.json`）返回文档；`GET {DocsPath}`（默认 `/docs`）返回**内置查看器**：单页 HTML，纯手写 CSS/JS（无 CDN），左侧路由列表 + 右侧详情（参数、响应、示例）。
- 说明：`Options{ Path, DocsPath string; Blocked bool /* 生产可关闭 */ }`。

- [ ] **Step 1: 写失败测试**：`TestFromRouterConvertsPatternParams`（`/:id.jpg` → `/​{id}.jpg` 且 param 存在）、`TestGroupPrefixApplied`、`TestJSONHasOpenAPIVersion`（`3.0.3`）、`TestSchemaFromStruct`、`TestViewerHTMLHasNoExternalCDN`（HTML 中不得出现 `http://`/`https://` 引用的 `src`/`href`）、`TestYAMLValid`。
- [ ] **Step 2: 运行确认失败** `go test ./openapi/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./openapi/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(openapi): 3.0 document builder from router with built-in viewer"`

---

### Task 15: 服务器 `server`（HTTP/1.1 + H2C + HTTP/3 + ACME + 优雅退出）

**Files:**
- Create: `server/server.go`, `server/options.go`, `server/acme.go`, `server/server_test.go`
- Modify: `go.mod`（+ `github.com/quic-go/quic-go`, `golang.org/x/net`, `golang.org/x/crypto`）

**Interfaces:**
- Produces:
  - `type Options struct{ Host string; Port int; TLS TLSConfig; H2C bool; HTTP3 HTTP3Config; ACME ACMEConfig; ReadTimeout, WriteTimeout, IdleTimeout, ShutdownTimeout time.Duration; Handler http.Handler; Logger *logx.Logger }`
  - `type TLSConfig struct{ Enabled bool; CertFile, KeyFile string; MinVersion uint16 }`
  - `type HTTP3Config struct{ Enabled bool; Port int /* 0 表示与 Port 相同 */ }`
  - `type ACMEConfig struct{ Enabled bool; Domains []string; Email string; CacheDir string; HostPolicyStrict bool }`
  - `type Server struct{...}`：`New(o Options) *Server`、`(*Server).Start() error`（非阻塞，返回监听错误）、`(*Server).Shutdown(ctx context.Context) error`、`(*Server).Addr() string`、`(*Server).Run(ctx context.Context) error`（Start + 等待 ctx/信号 + Shutdown）
  - `func OptionsFromConfig(c *config.Config) Options`（全部键均可被 env/flag 覆盖）
- 混合启动语义：HTTP/1.1 与 H2C 共用同一 `http.Server`（`h2c.NewHandler`），HTTP/3 在 UDP 端口独立 `http3.Server` 复用同一 Handler，并在明文响应写 `Alt-Svc: h3=":PORT"`（由 `server` 注入中间件）；`ACME.Enabled` 时用 `autocert.Manager` 作为 `TLSConfig.GetCertificate` 并复用其 `HTTPHandler` 处理 `/.well-known/acme-challenge/`；关闭顺序：先 HTTP/3 后 HTTP，再等待 `ShutdownTimeout`。

- [ ] **Step 1: 写失败测试**：`TestHTTP1AndH2CServeSameHandler`（h2c 客户端 `http2.Transport{AllowHTTP:true}` 拿到响应）、`TestAltSvcHeaderWhenHTTP3Enabled`、`TestGracefulShutdownDrainsInflight`（进行中请求在 Shutdown 后被完成）、`TestOptionsFromConfigOverriddenByEnv`、`TestACMEHostPolicyRejectsUnknownDomain`（不触网，仅校验策略函数）。
- [ ] **Step 2: 运行确认失败** `go test ./server/ -v` → FAIL
- [ ] **Step 3: 实现**（HTTP/3 集成测试用 `quic-go/http3` 客户端，UDP 端口随机以避免冲突）
- [ ] **Step 4: 运行确认通过** `go test ./server/ -v -race` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(server): http/1.1 + h2c + http/3 mixed server with acme and graceful shutdown"`

---

### Task 16: gRPC 双通道 `grpcx`

**Files:**
- Create: `grpcx/grpcx.go`, `grpcx/mux.go`, `grpcx/grpcx_test.go`
- Modify: `go.mod`（+ `google.golang.org/grpc`）

**Interfaces:**
- Produces:
  - `type Options struct{ Enabled bool; Mode string /* "same-port" | "separate" */; Port int; MaxRecvMsgSize, MaxSendMsgSize int; Reflection bool }`
  - `func NewServer(o Options, opts ...grpc.ServerOption) *grpc.Server`
  - `func DualHandler(grpcSrv *grpc.Server, httpHandler http.Handler) http.Handler` —— 按 `r.ProtoMajor==2 && strings.HasPrefix(Content-Type, "application/grpc")` 分流到 gRPC，否则走 HTTP 路由（同端口双通道）
  - `func RegisterFromConfig(...)` 便捷装配；`OptionsFromConfig(c *config.Config) Options`（键 `grpc.enabled` 等）
- 说明：`same-port` 模式必须配合 H2C（Task 15 的 `H2C: true`）。

- [ ] **Step 1: 写失败测试**：`TestDualHandlerRoutesGRPCByContentType`（用真实 grpc client 打同一端口的 HTTP server：`httptest.NewUnstartedServer` + h2c handler，注册一个 echo 服务并断言回包；同时断言普通 GET `/health` 仍返回 HTTP 响应）。
- [ ] **Step 2: 运行确认失败** `go test ./grpcx/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./grpcx/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(grpcx): dual-channel grpc over h2c same port"`

---

### Task 17: 应用门面 `app`

**Files:**
- Create: `app/app.go`, `app/services.go`, `app/app_test.go`

**Interfaces:**
- Consumes: `config`、`afs`、`logx`、`router`、`render`、`server`、`middleware`、`i18n`、`openapi`、`web`。
- Produces:
  - `type App struct{...}`：`New(opts ...Option) (*App, error)`，`Option`：`WithConfig(*config.Config)`、`WithFS(afero.Fs)`、`WithLogger(*logx.Logger)`、`WithConfigDir(string)`。
  - 路由代理：`(*App).GET/POST/PUT/PATCH/DELETE(pattern string, h router.HandlerFunc)`、`(*App).Group(prefix string, mw ...router.Middleware) *router.Group`、`(*App).Router() *router.Router`
  - 中间件与渲染：`(*App).Use(mw ...router.Middleware)`、`(*App).Render(c *router.Ctx, status int, name string, data any) error`（合并 htmx 局部/整页语义：`HX-Request` 时按 `X-Partial` 或模板命名约定选择片段）
  - 服务容器：`(*App).Set(name string, v any)`、`(*App).Get(name string) any`、`(*App).MustGet(name string) any`；内建键常量 `app.ServiceDB`、`app.ServiceRedis`、`app.ServiceAsynq`、`app.ServiceI18n`。
  - 生命周期：`(*App).Run(ctx context.Context) error`（装配 server → 启动 → 等待信号 → 优雅关闭 + 关闭已注册的 Closers）、`(*App).OnShutdown(func(context.Context) error)`、`(*App).Server() *server.Server`、`(*App).Config() *config.Config`、`(*App).Logger() *logx.Logger`
  - `app.Default()`：按约定装配（embed FS 的 templates/static、默认中间件链 Recover→RequestID→AccessLog→Compress→CORS→I18n）。

- [ ] **Step 1: 写失败测试**：`TestNewWiresDefaultsFromEmbed`、`TestHtmxRequestRendersPartial`（`HX-Request: true` + 片段模板选择）、`TestServiceContainer`、`TestOnShutdownCalledInReverseOrder`、`TestRunStopsOnContextCancel`。
- [ ] **Step 2: 运行确认失败** `go test ./app/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./app/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(app): application facade wiring router, render, config and lifecycle"`

---

# Phase C —— 实时通信与安全

### Task 18: SSE 实时流 `sse`

**Files:**
- Create: `sse/sse.go`, `sse/broker.go`, `sse/sse_test.go`

**Interfaces:**
- Produces:
  - `type Event struct{ ID, Name string; Data []byte; Retry time.Duration; Comment string }`
  - `type Writer struct{...}`：`NewWriter(w http.ResponseWriter, o Options) (*Writer, error)`（写 `Content-Type: text/event-stream; charset=utf-8`、`Cache-Control: no-cache`、`Connection: keep-alive`、`X-Accel-Buffering: no`，并断言 `http.Flusher`）；`(*Writer).WriteEvent(Event) error`、`(*Writer).WriteData(string) error`、`(*Writer).Comment(string) error`、`(*Writer).Heartbeat(time.Duration)`（周期 `: ping`，独立 goroutine，`Close` 停止）、`(*Writer).Close() error`。
  - `type Broker struct{...}`：`NewBroker(o BrokerOptions) *Broker`；`(*Broker).Subscribe(topic string) (<-chan Event, func())`、`(*Broker).Publish(topic string, e Event)`（非阻塞投递，慢消费者丢帧并计数）、`(*Broker).PublishHTML(topic string, target, swap string, html string)`（Htmx SSE 专用：`event: <topic>` + `data: <hx-partial…>`）、`(*Broker).Clients(topic string) int`、`(*Broker).Close()`。
  - `func Handler(b *Broker, topic string, o Options) router.HandlerFunc` —— 一站式 SSE 端点（含 `Last-Event-ID` 支持）。
- 数据行格式必须严格：多行 data 拆成多行 `data:`；空行结束事件；`\n` 转义。

- [ ] **Step 1: 写失败测试**：`TestWriterExactWireFormat`（断言完整字节流：`id: 1\nevent: msg\ndata: hello\ndata: world\nretry: 3000\n\n`）、`TestHeartbeatEmitsComment`、`TestBrokerFanoutToMultipleSubscribers`、`TestBrokerDropsForSlowConsumerWithoutBlocking`、`TestHandlerSendsHtmxPartialEvent`、`TestWriterRequiresFlusher`（非 Flusher 的 ResponseWriter 返回错误）。
- [ ] **Step 2: 运行确认失败** `go test ./sse/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./sse/ -v -race` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(sse): w3c event stream writer and pub/sub broker"`

---

### Task 19: WebSocket RFC6455 —— 握手与帧编解码

**Files:**
- Create: `ws/handshake.go`, `ws/frame.go`, `ws/conn.go`, `ws/mask.go`, `ws/utf8.go`, `ws/ws_test.go`, `ws/frame_test.go`

**Interfaces:**
- Consumes: `b64x`（Accept 计算）。
- Produces:
  - `type Options struct{ ReadBufferSize, WriteBufferSize int; HandshakeTimeout time.Duration; MaxMessageSize int64 /* 默认 32MB */; RequireMask bool /* 默认 true */; Subprotocols []string; CheckOrigin func(*http.Request) bool; CompressionMode int }`
  - `func Upgrade(w http.ResponseWriter, r *http.Request, o Options) (*Conn, error)` —— 校验方法 GET、`Connection` 含 `Upgrade`、`Upgrade: websocket`、`Sec-WebSocket-Version: 13`、`Sec-WebSocket-Key` 存在且 16 字节；计算 `Sec-WebSocket-Accept = b64(SHA1(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))`；选择子协议；`http.Hijacker` 取 `net.Conn`（不支持时返回 `ErrHijackUnsupported`，并显式说明 HTTP/3 下不可用）。
  - `type Conn struct{...}`：`ReadMessage() (OpCode, []byte, error)`（零拷贝：返回的切片指向连接读缓冲，下次读前有效，文档必须写清）、`NextReader() (*MessageReader, error)`、`WriteMessage(op OpCode, p []byte) error`、`WriteControl(op OpCode, p []byte, deadline time.Time) error`、`SetReadDeadline/SetWriteDeadline`、`SetPingHandler/SetPongHandler/SetCloseHandler(func(code CloseCode, reason string))`、`Close(code CloseCode, reason string) error`、`CloseNow() error`、`LocalAddr/RemoteAddr/Subprotocol`、`EnableWriteCompression(bool)`
  - `type OpCode byte`：`TextMessage=1, BinaryMessage=2, CloseMessage=8, PingMessage=9, PongMessage=10`；`type CloseCode uint16`：标准码常量（`CloseNormalClosure=1000`…`CloseTLSHandshake=1015` 中保留码不得发送）。
  - `frame.go`：`readFrameHeader(r *bufio.Reader, buf []byte) (frameHeader, error)`、`writeFrameHeader(dst []byte, fin bool, op OpCode, mask bool, payloadLen int64) int`、`maskBytes(key [4]byte, pos int, b []byte)`（含 8 字节批量 XOR 优化）。
  - `utf8.go`：`func validUTF8(p []byte) bool`（增量、无分配，支持跨帧状态）。
- 协议硬规则：客户端→服务端必须 masked（否则 1002）；服务端→客户端必须 unmasked；控制帧 FIN 必须为 1 且载荷 ≤125（否则 1002）；RSV 位未协商时必须为 0；分片消息中允许交织控制帧；文本消息必须为合法 UTF-8（否则 1007）；超过 `MaxMessageSize` 回 1009；未收到 Close 直接断开时返回 `ErrUnexpectedEOF`。

- [ ] **Step 1: 写失败测试**
  - `TestHandshakeAcceptValueRFCExample`：`dGhlIHNhbXBsZSBub25jZQ==` → `s3pPLMBiTxaQ9kYGzzhZRbK+xOo=`（RFC 6455 §1.3 官方向量）。
  - `TestHandshakeRejectsBadVersionAndMissingKey`。
  - `TestFrameHeaderLengths`：载荷 0/125/126/65535/65536 的头部字节逐一断言。
  - `TestMaskRoundTrip`：对每个长度类 `pose%4` 组合做 mask→unmask 幂等。
  - `TestReadUnmaskedClientFrameFails1002`、`TestOversizedMessageFails1009`、`TestInvalidUTF8TextFails1007`（三例均断言对端收到的 Close code）。
  - `TestControlFrameTooLargeRejected`、`TestFragmentedMessageReassemblyWithInterleavedPing`。
  - `TestNoGoroutineLeakAfterClose`（`runtime.NumGoroutine` 前后对比）。
- [ ] **Step 2: 运行确认失败** `go test ./ws/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./ws/ -v -race` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(ws): rfc6455 handshake and zero-copy frame codec"`

---

### Task 20: WebSocket 互操作验证与 Htmx 集成

**Files:**
- Create: `ws/interop_test.go`, `ws/htmx.go`, `ws/htmx_test.go`
- Modify: `go.mod`（**测试**依赖：`github.com/coder/websocket`）

**Interfaces:**
- Produces:
  - `ws.HtmxHandler(o HtmxOptions) router.HandlerFunc`：`HtmxOptions{ OnMessage func(*Conn, HtmxMessage); OnOpen func(*Conn); OnClose func(*Conn, error); Subprotocols []string }`，`HtmxMessage{ Headers map[string]string; Body string }`（`hx-ws` 扩展以 JSON `{"HEADERS":{...},"BODY":"..."}` 收发）。
  - `(*Conn).WriteHtmxFragment(target, swap, html string) error` —— 输出 `<hx-partial hx-target="…" hx-swap="…">…</hx-partial>`。
- 互操作测试用独立实现（coder/websocket）双向对拍：我方服务端 ↔ 对方客户端；对方服务端 ↔ 我方客户端（`ws.Dial(url, o)` 需在本任务补齐：`func Dial(ctx context.Context, url string, o DialOptions) (*Conn, error)`，含握手校验与 `Sec-WebSocket-Accept` 验证）。

- [ ] **Step 1: 写失败测试**：`TestInteropServerOurImplClientCoder`（文本/二进制/分片/ping-pong）、`TestInteropServerCoderClientOurImpl`、`TestHtmxFragmentWireFormat`、`TestDialRejectsBadAccept`。
- [ ] **Step 2: 运行确认失败** `go test ./ws/ -run TestInterop -v` → FAIL
- [ ] **Step 3: 实现** `Dial` + `htmx.go`
- [ ] **Step 4: 运行确认通过** `go test ./ws/ -race -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "test(ws): interop against coder/websocket; feat(ws): htmx ws handler"`

---

### Task 21: 免反射 JWT `jwt`

**Files:**
- Create: `jwt/jwt.go`, `jwt/sign.go`, `jwt/claims.go`, `jwt/scan.go`, `jwt/jwt_test.go`
- Modify: `go.mod`（仅标准库 crypto；无外部依赖）

**Interfaces:**
- Consumes: `b64x`（RawURLEncoding）、`jsonx`。
- Produces:
  - `type Alg string`：`HS256/HS384/HS512/RS256/RS384/RS512/PS256/PS384/PS512/ES256/ES384/ES512/EdDSA`
  - `type Header struct{ Alg Alg; Typ string; Kid string; Extra map[string]string }`
  - `type Claims struct{ raw []byte }`：`func (c Claims) Get(key string) (string, bool)`、`GetInt64`、`GetBool`、`GetFloat64`、`GetJSON(key string, v any) error`（仅当需要时才反射解析，默认走字节扫描 `scan.go`）、`Subject/Issuer/ID/Audience`、`ExpiresAt/NotBefore/IssuedAt`、`Has(key)`、`Keys()`（诊断用，慢路径）、`Raw() []byte`
  - `func Sign(claims map[string]any, key any, alg Alg, o SignOptions) (string, error)`；`SignOptions{ Kid string; Header map[string]string }`
  - `func Parse(token string, keyFn KeyFunc, o ParseOptions) (*Token, error)`；`type KeyFunc func(header Header) (any, error)`（支持 `kid` 轮换）；`ParseOptions{ ValidAlgs []Alg; Leeway time.Duration; Issuer string; Audience string; RequireExp bool; Now func() time.Time }`（`Now` 供确定性测试）
  - `type Token struct{ Header Header; Claims Claims; Signature []byte }`；错误变量：`ErrMalformed`, `ErrSignatureInvalid`, `ErrTokenExpired`, `ErrTokenNotValidYet`, `ErrAlgNotAllowed`, `ErrIssuerMismatch`, `ErrAudienceMismatch`, `ErrMissingExp`（全部可 `errors.Is`）。
  - 中间件：`jwt.Middleware(o MiddlewareOptions) router.Middleware`，`MiddlewareOptions{ KeyFunc; ParseOptions; TokenLookup string /* 默认 "header:Authorization:Bearer " */; CtxKey string; ErrorHandler func(*router.Ctx, error) }`。
- 硬约束（Spec §三.5"零拷贝与无缝集成"）：禁止使用 `map[string]any` 解析载荷做校验；`exp/nbf/iat/iss/aud/sub` 判定直接在解码后的原始字节上扫描；签名验证在原始 `signingInput` 字节上进行（`b64x.AppendEncode` 拼接到复用缓冲）。

- [ ] **Step 1: 写失败测试**：`TestHS256AgainstRFC7519Vector`（RFC 7519 §3.1 官方 token 逐字节校验）、`TestRS256AndES256RoundTrip`、`TestPS256RoundTrip`、`TestExpiredAndNotYetValidWithLeeway`、`TestAlgNoneRejected`（`alg:none` 必须拒绝）、`TestAlgConfusionRejected`（RS256 token 用 HMAC 公钥当密钥验证必须失败）、`TestKeyFuncKidRotation`、`TestClaimsScanDoesNotAllocate`（`AllocsPerRun` 断言）、`TestMiddlewareExtractsBearerAndSetsContext`。
- [ ] **Step 2: 运行确认失败** `go test ./jwt/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./jwt/ -race -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(jwt): reflection-free jwt with zero-copy claim scanning"`

---

### Task 22: 国密合规加密套件 `guomi`

**Files:**
- Create: `guomi/sm2.go`, `guomi/sm3.go`, `guomi/sm4.go`, `guomi/keys.go`, `guomi/*_test.go`
- Modify: `go.mod`（+ `github.com/tjfoc/gmsm`）

**Interfaces:**
- Produces:
  - **SM3**：`func Sum(data []byte) [32]byte`、`func SumHex(data []byte) string`、`func New() hash.Hash`、`func HMAC(key, data []byte) [32]byte`、`func HashPassword(password string, o PasswordOptions) (string, error)` / `func VerifyPassword(encoded, password string) (bool, error)`，`PasswordOptions{ SaltLen int /* 16 */; Iterations int /* 100000，基于 SM3 的 PBKDF2 手写实现 */ }`，编码格式 `sm3pbkdf2$<iter>$<saltB64>$<hashB64>`（`crypto/subtle.ConstantTimeCompare` 比较）。
  - **SM2**：`type PrivateKey`/`PublicKey`（薄封装 `gmsm/sm2`）；`GenerateKey(rand io.Reader) (*PrivateKey, error)`、`Encrypt(pub *PublicKey, data []byte) ([]byte, error)`（C1C3C2，ASN.1 与非 ASN.1 两式：`EncryptASN1` / `EncryptRaw`）、`Decrypt`、`Sign(priv, data []byte) ([]byte, error)`（SM3 摘要 + ASN.1 签名）、`Verify(pub, data, sig []byte) (bool, error)`、`SignHex/VerifyHex`、`MarshalPrivateKeyPEM/MarshalPublicKeyPEM/ParsePrivateKeyPEM/ParsePublicKeyPEM`、`PrivateKeyFromHex/PublicKeyFromHex`。
  - **SM4**：`NewCipher(key []byte) (cipher.Block, error)`（16 字节密钥校验）、`EncryptCBC(key, iv, plaintext []byte) ([]byte, error)`（PKCS#7 填充）、`DecryptCBC`、`SealGCM(key, nonce, plaintext, aad []byte) ([]byte, error)` / `OpenGCM`、`EncryptECB/DecryptECB`（明确标注仅用于协议兼容）、`RandomKey() ([]byte, error)`。
  - `func CipherSuiteInfo() map[string]string`（算法/模式/来源，用于 `/health` 与审计输出）。
- 硬约束：**纯 Go**（gmsm 无 cgo），不得引入 OpenSSL 绑定；所有随机数来自 `crypto/rand`；GCM nonce 长度校验（12 字节推荐）。

- [ ] **Step 1: 写失败测试**
  - `TestSM3StandardVector`：`"abc"` → `66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0`（GB/T 32905-2016 官方向量）。
  - `TestSM3LongVector`：64 次 `"abcd"` 重复 → `debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732`。
  - `TestSM2EncryptDecryptRoundTrip`（含空串、1 字节、1MB）、`TestSM2SignVerifyAndTamperFails`、`TestSM2PEMRoundTrip`、`TestSM2CrossCheckWithGmsm`。
  - `TestSM4CBCRoundTripWithPKCS7`、`TestSM4GCMRoundTripAndTamperFails`、`TestSM4KeyLengthValidated`。
  - `TestPasswordHashAndVerify`（含错误密码、编码格式含迭代数与盐）。
- [ ] **Step 2: 运行确认失败** `go test ./guomi/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./guomi/ -race -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(guomi): pure-go SM2/SM3/SM4 suite with salted password hashing"`

---

# Phase D —— 数据生态

### Task 23: GORM 插件 `data/gormx`

**Files:**
- Create: `data/gormx/gormx.go`, `data/gormx/config.go`, `data/gormx/logger.go`, `data/gormx/gormx_test.go`
- Modify: `go.mod`（+ `gorm.io/gorm`, `gorm.io/driver/mysql`, `gorm.io/driver/postgres`, `github.com/glebarez/sqlite`）

**Interfaces:**
- Consumes: `config`、`logx`。
- Produces:
  - `type Options struct{ Driver string /* mysql|postgres|sqlite */; DSN string; MaxOpenConns, MaxIdleConns int; ConnMaxLifetime, ConnMaxIdleTime time.Duration; SlowThreshold time.Duration /* 默认 200ms */; LogLevel string; PrepareStmt bool /* 默认 true */; SkipDefaultTransaction bool }`
  - `func Open(o Options, l *logx.Logger) (*gorm.DB, error)`、`func OptionsFromConfig(c *config.Config) Options`（键 `db.driver`/`db.dsn`/`db.maxconn`/`db.maxidle`/`db.slow_threshold`/`db.prepare_stmt`）
  - `func NewLogger(l *logx.Logger, slow time.Duration, level string) gormlogger.Interface` —— 实现 GORM `logger.Interface`：`Trace` 中慢查询走 `logx` 的零分配打点（`SlowQuery`），错误级别映射，`ParamsFilter` 屏蔽敏感参数。
  - `func AutoMigrate(db *gorm.DB, models ...any) error`、`func Health(db *gorm.DB) error`（`sql.DB.PingContext`）、`func Close(db *gorm.DB) error`
  - `gormx.Register(app *app.App) (*gorm.DB, error)`（可选装配糖，写入 `app.ServiceDB`）
- 硬规则：`PrepareStmt: true` 默认开启；慢查询日志必须是**无反射、无字符串拼接**路径（只记录已格式化的 SQL 与耗时/行数）。

- [ ] **Step 1: 写失败测试**（用 `glebarez/sqlite` 内存库，零外部依赖）
  - `TestOpenSQLiteWithPrepareStmtEnabled`（断言 `db.Config.PrepareStmt == true`，并连打两次同 SQL 走预编译路径）
  - `TestSlowQueryLoggedThroughLogx`（`SlowThreshold=1ns`，断言日志缓冲中出现 `slow_query` 字段与 `duration`）
  - `TestConnPoolSettingsApplied`（读回 `sql.DB.Stats().MaxOpenConnections`）
  - `TestAutoMigrateAndHealth`、`TestParamsFilterRedactsSensitive`
- [ ] **Step 2: 运行确认失败** `go test ./data/gormx/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./data/gormx/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(gormx): gorm plugin with prepare-stmt and zero-alloc slow query logging"`

---

### Task 24: Redis 插件与泛型缓存装饰器 `data/redisx`

**Files:**
- Create: `data/redisx/redisx.go`, `data/redisx/cache.go`, `data/redisx/config.go`, `data/redisx/*_test.go`
- Modify: `go.mod`（+ `github.com/redis/go-redis/v9`, `github.com/alicebob/miniredis/v2`(测试)）

**Interfaces:**
- Consumes: `config`、`jsonx`（缓存序列化必须绑定自适应 JSON 层）。
- Produces:
  - `type Options struct{ Mode string /* single|sentinel|cluster */; Addr string; Addrs []string; Username, Password string; DB int; MasterName string; PoolSize, MinIdleConns int; DialTimeout, ReadTimeout, WriteTimeout time.Duration; KeyPrefix string }`
  - `func Open(o Options) (redis.UniversalClient, error)`、`func OptionsFromConfig(c *config.Config) Options`（键 `redis.mode`/`redis.addr`/`redis.addrs`/`redis.master_name`/…）
  - `func Health(ctx context.Context, cli redis.UniversalClient) error`
  - **泛型缓存装饰器**：`type Cache[T any] struct{...}`；`NewCache[T any](cli redis.UniversalClient, o CacheOptions) *Cache[T]`，`CacheOptions{ Prefix string; TTL time.Duration; Jitter time.Duration /* 防雪崩 */; NegativeTTL time.Duration }`
    - `(*Cache[T]).GetOrLoad(ctx, key string, load func(context.Context) (T, error)) (T, error)` —— 单飞（`golang.org/x/sync/singleflight`）防击穿
    - `(*Cache[T]).Get/Set/Delete/Invalidate(ctx, key)`；`Set` 用 `jsonx.Marshal`
    - `(*Cache[T]).LoadMany(ctx, keys []string, loader func(context.Context, []string) (map[string]T, error)) (map[string]T, error)` —— pipeline MGET + 回填
- 硬规则：所有键自动加 `KeyPrefix`；序列化走 `jsonx`（非 `encoding/json` 直连）；`GetOrLoad` 在 load 失败时**不得**污染缓存。

- [ ] **Step 1: 写失败测试**（用 `miniredis` 内存服务，零外部依赖）
  - `TestOpenSingleSentinelClusterModes`（三种模式构造成功；cluster 模式用 `redis.NewClusterClient` 类型断言）
  - `TestCacheGetOrLoadCachesAndReuses`（loader 只被调用一次）
  - `TestCacheSingleflightUnderConcurrency`（100 并发 → loader 调用 1 次，`-race`）
  - `TestCacheLoadErrorNotCached`、`TestCacheKeyPrefixApplied`、`TestLoadManyPipelinesMissing`、`TestCacheUsesAdaptiveJSON`（写入后由 `jsonx.Unmarshal` 可解）
- [ ] **Step 2: 运行确认失败** `go test ./data/redisx/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./data/redisx/ -race -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(redisx): redis universal client and generic cache decorator"`

---

### Task 25: 异步任务队列 `data/asynqx`

**Files:**
- Create: `data/asynqx/asynqx.go`, `data/asynqx/registry.go`, `data/asynqx/queues.go`, `data/asynqx/config.go`, `data/asynqx/*_test.go`
- Modify: `go.mod`（+ `github.com/hibiken/asynq`）

**Interfaces:**
- Consumes: `config`、`logx`、`jsonx`。
- Produces:
  - `type Options struct{ RedisAddr string; RedisPassword string; RedisDB int; Concurrency int; Queues map[string]int /* 权重，如 {"critical":3,"default":2,"low":1} */; StrictPriority bool; ShutdownTimeout time.Duration; RetryDelayFunc func(retryCount int, err error, task *asynq.Task) time.Duration }`
  - `func ParseQueues(s string) (map[string]int, error)` —— 解析 `"critical:3, default:2, low:1"`
  - `type Registry struct{...}`：`NewRegistry() *Registry`、`(*Registry).Register(taskType string, h Handler) *Registry`（声明式：`type Handler func(context.Context, Payload) error`，`Payload` 为 `map[string]any`，由 `jsonx` 解析）、`(*Registry).Handlers() map[string]Handler`
  - `type Client struct{...}`：`NewClient(asynq.RedisClientOpt, o Options) (*Client, error)`、`(*Client).Enqueue(ctx, taskType string, payload any, opts ...asynq.Option) (*asynq.TaskInfo, error)`、`(*Client).EnqueueIn/EnqueueAt`、`(*Client).Inspector() *asynq.Inspector`、`Close()`
  - `type Server struct{...}`：`NewServer(reg *Registry, asynq.RedisClientOpt, o Options, l *logx.Logger) *Server`、`(*Server).Start() error`、`(*Server).Shutdown()`、`(*Server).Run(ctx) error`
  - **任务上下文追踪**：`type TaskContext struct{ TaskID, Type, Queue string; RetryCount, MaxRetry int; TraceID string }`，`asynqx.FromContext(ctx) *TaskContext`；中间件把 `logx` 子 logger 注入 ctx（`asynqx.LoggerFrom(ctx)`）。
  - 失败重试默认指数倒退：内置 `ExponentialBackoff(base time.Duration, max time.Duration) func(int, error, *asynq.Task) time.Duration`（`base * 2^(n-1)` 且封顶 + 抖动）。
  - `OptionsFromConfig(c *config.Config)`（键 `asynq.queues`、`asynq.concurrency`、`asynq.redis_addr` …）。

- [ ] **Step 1: 写失败测试**（用 `miniredis` + 真实 asynq server 端到端）
  - `TestRegisterAndHandleTaskEndToEnd`（enqueue → handler 收到 payload）
  - `TestUnknownTaskTypeReturnsError`
  - `TestQueueWeightsParsedFromString`（`"critical:3,default:2,low:1"` → map 精确相等；非法输入报错）
  - `TestExponentialBackoffMonotonicAndCapped`（递增、封顶、含抖动边界）
  - `TestTaskContextInjectedWithTraceID`、`TestHandlerPanicRecoveredAndRetried`
  - `TestShutdownStopsServer`（`-race`）
- [ ] **Step 2: 运行确认失败** `go test ./data/asynqx/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./data/asynqx/ -race -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(asynqx): declarative task registry with weighted queues and backoff"`

---

# Phase E —— CLI 与脚手架

### Task 26: CLI 骨架（pflag 命令注册 + 配置绑定）

**Files:**
- Create: `cmd/yd/main.go`, `cli/cli.go`, `cli/command.go`, `cli/global.go`, `cli/cli_test.go`

**Interfaces:**
- Consumes: `config`、`version`（编译期注入）。
- Produces:
  - `type Command struct{ Name, Short, Long, Usage string; Flags func(*pflag.FlagSet); Run func(ctx context.Context, env *Env) error }`
  - `type Env struct{ Cfg *config.Config; FS afero.Fs; Out, Err io.Writer; In io.Reader; WorkDir string; Version string; IsTTY bool }`
  - `func Register(c Command)`、`func Execute(ctx context.Context, args []string, env *Env) error`（POSIX 风格：`yd <cmd> [flags] [args]`，`--help/-h` 每个命令独立，未知命令列出可用命令并返回非零）
  - 全局标志：`--config/-c <file>`、`--env-file`、`--verbose/-v`、`--quiet/-q`、`--no-color`、`--version`；全局标志通过 `config.BindFlags` 绑定到 `log.level` 等键。
  - `cli.Prompt(env, question, default)` / `cli.Confirm` / `cli.Choose(env, q, options)` —— 极简交互，非 TTY 时取默认值（便于 CI 与测试）。
- 注册 6 个子命令：`new`、`dev`、`gen`、`build`、`docker`、`assets`（各自 Task 实现）。

- [ ] **Step 1: 写失败测试**：`TestExecuteUnknownCommandErrors`、`TestGlobalFlagsBindToConfig`（`--verbose` → `log.level=debug`）、`TestCommandHelpPrinted`、`TestNonTTYChooseUsesDefault`、`TestVersionFlag`。
- [ ] **Step 2: 运行确认失败** `go test ./cli/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./cli/ -v && go run ./cmd/yd --version` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(cli): pflag command registry with config binding"`

---

### Task 27: 前端资产治理 `assets`（在线更新 + 离线独立使用）

**Files:**
- Create: `cli/assets.go`, `assets/manifest.go`, `assets/update.go`, `assets/verify.go`, `assets/assets_test.go`, `web/assets.lock.json`
- Modify: `go.mod`（无新增；使用 `net/http`）

**Interfaces:**
- Consumes: `afs`、`web`（默认清单）、`cli`。
- Produces:
  - `type Asset struct{ Name, Version, URL, Path, SHA256, License string; Size int64; Kind string /* js|css|wasm|binary|html */; Required bool }`
  - `type Manifest struct{ Version int; GeneratedAt time.Time; Sources []string; Assets []Asset }`；`func LoadManifest(fs afero.Fs, path string) (*Manifest, error)`、`(*Manifest).Save(fs, path) error`、`(*Manifest).Find(name string) (*Asset, bool)`
  - `func Update(ctx context.Context, o UpdateOptions) (*UpdateReport, error)`，`UpdateOptions{ FS afero.Fs; ManifestPath string; Root string /* 写入根，如 "web" */; Offline bool; CheckOnly bool; Force bool; Sources []string; HTTPClient *http.Client; Concurrency int }`
    - 在线：按 `Sources` 顺序尝试下载（默认 `https://cdn.jsdelivr.net/npm/{pkg}@{ver}/{file}`，回退 `https://registry.npmmirror.com/{pkg}/-/{pkg}-{ver}.tgz` 解包，再回退 `https://unpkg.com/...`），计算 SHA-256；
    - 若清单已有校验和 → **必须**匹配，否则报 `ErrChecksumMismatch` 且**不写入**；
    - 若清单无校验和（首次锁定）→ 写入并回填 SHA256；
    - 离线：仅校验本地文件存在且哈希匹配，**任何网络调用都被禁止**（`Offline=true` 时注入一个 `http.RoundTripper` 直接返回 `ErrOffline`，测试断言其从未被调用）。
  - `func Verify(fs afero.Fs, m *Manifest, root string) (*VerifyReport, error)` —— 逐文件 `ok|missing|mismatch`，返回非 nil error 若任一不合规。
  - CLI：
    - `yd assets list`（表格输出 name/version/size/kind/sha256 前 8 位）
    - `yd assets update [--check] [--force] [--source URL] [--offline]`
    - `yd assets verify [--offline]`
    - `yd assets add <name> --url <url> --path <path> [--version v]`（追加并锁定校验和）
- 硬规则（Review Focus #4）：**先下载到内存、校验通过后才落盘**（绝不半写）；写盘用 `afs.WriteFileIfChanged`；任一资产失败 → 整体返回聚合错误，已成功的保持更新并明确列出失败项；`--offline` 绝不触网。

- [ ] **Step 1: 写失败测试**（`httptest.Server` 伪造源，零真实网络）
  - `TestUpdateDownloadsAndLocksChecksum`
  - `TestUpdateRejectsChecksumMismatchWithoutWriting`（断言目标文件保持旧内容）
  - `TestOfflineModeNeverPerformsNetwork`（`RoundTripper` 计数器为 0；缺失文件 → 报错）
  - `TestOfflineVerifyPassesWithAllFilesPresent`
  - `TestCheckOnlyDoesNotWrite`（`--check` 仅报告）
  - `TestSourceFallbackOrder`（首个源 404 → 命中第二源）
  - `TestAddLocksChecksumAndAppendsToManifest`
- [ ] **Step 2: 运行确认失败** `go test ./assets/ -v` → FAIL
- [ ] **Step 3: 实现** + 用真实清单初始化 `web/assets.lock.json`（htmx 4.0.0 及扩展、Alpine 3.17.4、idiomorph 0.8.0）
- [ ] **Step 4: 运行确认通过** `go test ./assets/ -v && go run ./cmd/yd assets list` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(assets): online asset updater with sha256 lock and strict offline mode"`

---

### Task 28: 项目模板与 `yd new`

**Files:**
- Create: `scaffold/scaffold.go`, `scaffold/options.go`, `scaffold/render.go`, `scaffold/templates/**`（embed）, `scaffold/scaffold_test.go`, `cli/new.go`
- Modify: `go.mod`（模板文件本身不含依赖，无需改）

**Interfaces:**
- Produces:
  - `type Options struct{ Name string /* 项目名/目录 */; Module string /* 默认 github.com/<user>/<name> */; DB string /* mysql|postgres|sqlite|none */; Redis bool; Asynq bool; GRPC bool; I18n bool; HtmxExts []string; Tailwind bool; License string /* Apache-2.0 */; Force bool; LocalReplace string /* 开发期 replace 到本地 yd 仓库 */ }`
  - `func Generate(ctx context.Context, o Options, fs afero.Fs, log *logx.Logger) (*Result, error)`；`Result{ Files []string; Skipped []string; ModulePath string }`
  - `func Interactive(ctx context.Context, env *cli.Env, base Options) (Options, error)`（`yd new` 无参且 TTY 时逐项询问，非 TTY 时取默认值）
- 生成物清单（**必须全部存在**）：
  ```
  <name>/
  ├── go.mod  go.sum(占位不生成)  LICENSE  README.md  Dockerfile  .gitignore  .gitattributes
  ├── .env.template  .yd.json              # .yd.json 记录 name/module/features/assetRoot
  ├── CLAUDE.md  AGENTS.md
  ├── configs/config.yaml  configs/config.local.yaml.template
  ├── cmd/app/main.go
  ├── internal/{controller,service,repository,model,middleware,router}/…
  ├── web/templates/{layouts,partials,pages}/*.html
  ├── web/static/css/app.css   web/static/js/yd-app.js
  ├── web/assets.lock.json     # 从框架 web/assets.lock.json 复制并裁剪（仅所选扩展）
  ├── docs/developer_guidelines.md
  └── build/README.md
  ```
  - `LICENSE`：完整 Apache-2.0（与仓库同源，从 `scaffold/templates/_shared/LICENSE` 读取）。
  - `.gitignore` / `.gitattributes`：**逐字**采用 Spec §二.3 两份模板。
  - `CLAUDE.md` / `AGENTS.md`：按 Spec §二.4 生成，内容含项目名、模块路径、所选特性、技术栈版本。
  - `docs/developer_guidelines.md`：前后端协同约束（HTML 片段契约、`hx-*` 约定、模板目录、资产更新流程）。
  - 模板渲染：`text/template` + `{{.Name}}` 等字段；文件路径中的 `_name_` 占位符替换（如 `_name_/` 目录）；二值文件（LICENSE/图片）按原样复制。
  - 幂等性：目标文件已存在且内容相同 → 跳过并计入 `Skipped`；内容不同且未 `--force` → 报错列出冲突。
- CLI `yd new <project-name>`：标志 `--module --db --redis --asynq --grpc --i18n --htmx-ext --tailwind --license --force --yes/-y --local-replace`；成功后打印后续步骤（`cd <name> && yd dev`）。

- [ ] **Step 1: 写失败测试**（MemFS + `fstest`）
  - `TestGenerateProducesAllRequiredFiles`（逐条断言文件存在 + 关键内容：`go.mod` 含模块路径与 `go 1.27.2`；`LICENSE` 含 Apache；`.gitignore` 逐行匹配）
  - `TestGenerateHiddenFilesNotLostOnMemFS`（`.gitignore`/`.env.template` 不因 `afero.Walk` 漏写）
  - `TestGenerateTailwindAddsCssEntry`、`TestGenerateFeaturesReflectedInConfigAndRoutes`（选 sqlite+redis+asynq+grpc 时 `configs/config.yaml` 含对应段、`internal/router` 含对应注册代码）
  - `TestGenerateIdempotentSkipsIdentical`、`TestGenerateConflictsWithoutForce`
  - `TestLocalReplaceAddsGoModReplace`
  - `TestInteractiveNonTTYUsesDefaults`
- [ ] **Step 2: 运行确认失败** `go test ./scaffold/ -v` → FAIL
- [ ] **Step 3: 实现**模板与渲染
- [ ] **Step 4: 运行确认通过**：`go test ./scaffold/ -v`；并真实生成一次到临时目录，`go build ./...` 必须通过（结合 `--local-replace`）。
- [ ] **Step 5: 提交** `git commit -m "feat(scaffold): project templates and yd new command"`

---

### Task 29: `yd dev` 热重载

**Files:**
- Create: `cli/dev.go`, `toolchain/tailwind.go`, `toolchain/watch.go`, `cli/dev_test.go`
- Modify: `go.mod`（+ `github.com/fsnotify/fsnotify`）

**Interfaces:**
- Consumes: `afs`、`cli`、`toolchain`。
- Produces:
  - `toolchain.ResolveTailwind(o ToolOptions) (string, error)` —— 解析顺序：`--bin` 标志 > 配置 `tailwind.bin` > 环境 `YD_TAILWIND_BIN` > `.yd/tools/tailwindcss[.exe]` > `PATH`；找不到返回 `ErrToolNotFound`（可被 `yd assets` 下载安装）。
  - `type BuildResult struct{ CSSPath string; Bytes int; Duration time.Duration }`；`toolchain.BuildCSS(ctx, bin, o CSSOptions) (*BuildResult, error)`，`CSSOptions{ Input string /* web/static/css/app.css */; Output string /* web/static/css/app.css 编译产物? → 用 web/static/css/yd.css */; Minify bool; Watch bool; Content []string /* 扫描的模板/Go 源路径，Tailwind v4 用 @source 指令时可为空 */ }`
  - `toolchain.NewWatcher(o WatchOptions) (*Watcher, error)`，`WatchOptions{ Roots []string; Ignore []string /* 默认 .git,bin,dist,node_modules,.tailwindcss */; Debounce time.Duration /* 200ms */ }`；`(*Watcher).Events() <-chan Event`、`Close()`
  - `cli/dev.go`：`yd dev` 主循环 —— ① 首次全量构建（CSS + Go）；② 启动子进程 `go run`/`bin/.dev-app` 并转发 stdout/stderr；③ 监听 `.go`/`configs/**`/`web/templates/**`/`web/static/**`；④ Go 变更 → 重新编译 + 重启子进程；CSS/模板变更 → 仅触发 Tailwind 重建 + 浏览器通过 SSE 事件 `reload` 触发无刷新局部刷新（后端注入 `/__yd/dev` SSE 端点，生产禁用）；⑤ `Ctrl+C` 优雅退出（先 SIGTERM 子进程再 Kill）。
  - 标志：`--port`、`--no-tailwind`、`--no-watch`、`--no-reload`、`--debounce`、`--tailwind-bin`。
- 硬规则：热重载端点仅在 `dev` 模式注册，且由 `app.Option` 显式开启（生产代码路径不含该端点）。

- [ ] **Step 1: 写失败测试**：`TestDebounceCoalescesRapidEvents`（连续 5 次写文件 → 只收到 1 次事件）、`TestIgnoreDirectories`、`TestResolveTailwindPrefersFlagThenEnvThenLocalBinThenPath`（用临时文件伪造）、`TestBuildCSSInvokesBinaryWithExpectedArgs`（用假可执行脚本记录 argv）、`TestDevRestartsChildOnGoChange`（用假构建器 + 短生命周期子进程）、`TestDevReloadEndpointOnlyInDevMode`。
- [ ] **Step 2: 运行确认失败** `go test ./toolchain/ ./cli/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./toolchain/ ./cli/ -race -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(dev): live reload for go, tailwind and templates"`

---

### Task 30: `yd build` 一键跨平台打包

**Files:**
- Create: `cli/build.go`, `build/build.go`, `build/build_test.go`

**Interfaces:**
- Produces:
  - `type Options struct{ Dir string; Output string /* 默认 bin/<name> */; Targets []Target; LDFlags []string; Tags []string; Tailwind TailwindOptions; Embed bool; Offline bool /* 强制 -mod=vendor */; TrimPath bool /* 默认 true */; Version, Commit, Date string }`
  - `type Target struct{ GOOS, GOARCH string }`；`func ParseTargets(s string) ([]Target, error)`（`"linux/amd64,linux/arm64,windows/amd64"`）
  - `func Build(ctx context.Context, o Options, log *logx.Logger) (*Report, error)`：步骤 ①（可选）Tailwind 剪裁压缩：`toolchain.BuildCSS(minify)` 输出 `web/static/css/yd.css`；② 版本注入 `-ldflags "-s -w -X github.com/yun-dev/yd/version.Version=…"`；③ `go build`（`CGO_ENABLED=0`、`-trimpath`）；④ 多目标时输出 `dist/<name>_<os>_<arch>[.exe]`，单目标输出 `--output`；⑤ 校验产物大小与 `file` 头（PE/ELF magic）。
  - `version` 包：`version.Version/Commit/Date` + `version.String()`。
  - CLI `yd build`：`--os --arch --targets --output --tailwind/--no-tailwind --minify --offline --tags --ldflags`；默认当前平台；`--all` 展开为 linux/amd64,linux/arm64,darwin/arm64,windows/amd64。
- 硬规则：产物必须**单文件无依赖**（静态链接，`CGO_ENABLED=0`），`scratch` 镜像可直接运行。

- [ ] **Step 1: 写失败测试**：`TestParseTargets`、`TestBuildProducesStaticBinary`（真实编译一个极小 main 包，断言产物存在、ELF/PE magic、`go version -m` 可读）、`TestBuildInjectsVersionLdflags`（编译 `version` 打印程序并运行断言输出）、`TestBuildOfflineUsesVendorMode`（断言 `-mod=vendor` 出现在执行参数）、`TestBuildMultiTargetOutputsDistNames`。
- [ ] **Step 2: 运行确认失败** `go test ./build/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./build/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(build): cross-platform static build with version injection"`

---

### Task 31: `yd docker` 与多阶段 Dockerfile

**Files:**
- Create: `cli/docker.go`, `docker/docker.go`, `docker/docker_test.go`, `Dockerfile`（根目录模板）
- Modify: `scaffold/templates/**`（Dockerfile 模板同源）

**Interfaces:**
- Produces:
  - `type Options struct{ Dir, Dockerfile, Tag string; Platform string; NoCache bool; BuildArgs map[string]string; Target string; Push bool; DockerBin string }`
  - `func BuildImage(ctx context.Context, o Options, fs afero.Fs, log *logx.Logger) (*Result, error)`：① 通过 afero 读取内置 Dockerfile 模板（`docker/templates/Dockerfile`，`//go:embed`），若项目根已有 `Dockerfile` 则优先使用项目文件；② 调用 `docker build`（`--platform`、`--build-arg`、`-t`）；③ 解析输出行以报告镜像 ID 与大小；④ 校验 docker daemon 可用（`docker version --format {{.Server.Version}}`），不可用返回 `ErrDaemonUnavailable`。
  - Dockerfile 模板内容（必须使用 scratch）：
    ```dockerfile
    FROM golang:1.27-alpine AS builder
    WORKDIR /src
    COPY go.mod go.sum ./
    RUN go mod download
    COPY . .
    RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/app ./cmd/app
    FROM scratch
    COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
    COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
    COPY --from=builder /out/app /app
    EXPOSE 8080
    ENTRYPOINT ["/app"]
    ```
  - CLI `yd docker`：`--tag（默认 <name>:<version>）--platform --no-cache --file --push --docker-bin`。
- 硬规则：镜像文件系统除二进制与证书/时区外不引入运行时依赖；构建前自动执行 `yd build` 的 Tailwind 阶段（保证 CSS 已裁剪，`go:embed` 才能嵌入最新样式）——通过 `--with-assets` 默认开启。

- [ ] **Step 1: 写失败测试**：`TestBuiltinDockerfileUsesScratchAndMultiStage`（解析模板字符串断言 `FROM scratch`、`AS builder`、`CGO_ENABLED=0`）、`TestProjectDockerfilePreferredOverBuiltin`（MemFS 放 `Dockerfile` → 使用项目文件）、`TestBuildImageArgsConstructed`（假 docker 脚本记录 argv：含 `-t`、`--platform`、`--no-cache`、`--build-arg`）、`TestDaemonUnavailableReturnsTypedError`（docker bin 指向非零退出脚本）。
- [ ] **Step 2: 运行确认失败** `go test ./docker/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./docker/ -v` → PASS；若本机 Docker 可用，追加一次真实 `yd docker` 冒烟（Task 34 覆盖）。
- [ ] **Step 5: 提交** `git commit -m "feat(docker): scratch-based multi-stage image build"`

---

### Task 32: 代码生成器 `yd gen module|crud`

**Files:**
- Create: `gen/gen.go`, `gen/model.go`, `gen/templates.go`, `gen/schema.go`, `gen/crud.go`, `gen/*_test.go`, `cli/gen.go`
- Modify: `go.mod`（无新增；DB 反射用 `database/sql` + 现有驱动）

**Interfaces:**
- Produces:
  - `type Field struct{ Name, GoName, GoType, Column, JSONName string; Primary, Nullable, Indexed, Unique bool; Size int; Comment string; Widget string /* text|textarea|select|checkbox|date|number|password|file */; Label string; Validate string /* 如 "required,max=64" */ }`
  - `type Model struct{ Name, GoName, Table, Module string; Fields []Field; Timestamps bool }`
  - `func ModelFromStruct(v any, o StructOptions) (*Model, error)`（Go struct → Model，读 `gorm`/`json`/`yd` 标签）
  - `func ModelFromTable(ctx context.Context, db *sql.DB, table string, o TableOptions) (*Model, error)`（读 information_schema / PRAGMA，按方言分支）
  - `func ParseFields(s string) ([]Field, error)`（`"name:string:required,age:int,avatar:file"` 形式）
  - `func Generate(m *Model, o Options, fs afero.Fs) (*Result, error)`：产出
    - `internal/model/<snake>.go`（GORM 模型 + 标签）
    - `internal/repository/<snake>_repo.go`（CRUD 仓储）
    - `internal/service/<snake>_service.go`
    - `internal/controller/<snake>_controller.go`（**返回 HTML 片段**，htmx 语义：列表片段 / 表单片段 / 行片段 / 校验错误 422）
    - `internal/router/<snake>_routes.go`（`RegisterXxxRoutes(g *router.Group, c *Controller)`）
    - `web/templates/<snake>/{list.html,form.html,row.html,_fields.html}`
    - `internal/model/<snake>_gen_test.go`（生成的编译通过性测试占位）
  - 注册机制：修改 `internal/router/routes_gen.go`（存在则由 `//yd:gen:begin/end` 标记包裹并替换区块；不存在则创建）。
  - CLI：`yd gen module <name> --fields "..." [--force]`、`yd gen crud <name> --table <t> --dsn <dsn>` 或 `--struct pkg.Type`；`--dry-run` 打印将写入的文件与差异摘要。
- 硬规则：生成文件顶部必须带 `// Code generated by yd gen; DO NOT EDIT.`；重复生成幂等（内容一致则跳过）；生成后自动执行 `gofmt`（`go/format`）。

- [ ] **Step 1: 写失败测试**：
  - `TestParseFieldsSpec`（类型映射 string→string、int→int64、bool→bool、float→float64、time→time.Time、file→string）
  - `TestModelFromStructReadsTags`
  - `TestModelFromSQLiteTable`（真实 sqlite 内存表 → 字段/主键/可空）
  - `TestGenerateProducesFormattedGoCode`（`format.Source` 可解析且无错）
  - `TestGeneratedControllerReturnsHTMLFragments`（断言控制器内使用 `c.HTML` 与 `htmx` 辅助，不含 `c.JSON` 作为主响应）
  - `TestGenerateIdempotent`、`TestGenerateRespectsExistingCustomFiles`（不覆盖无生成标记的用户文件）
  - `TestRoutesGenMarkerBlockReplacedNotDuplicated`（连续两次生成，`routes_gen.go` 内注册行只有一份）
- [ ] **Step 2: 运行确认失败** `go test ./gen/ -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./gen/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(gen): module and crud generator with htmx fragments"`

---

# Phase F —— 前端

### Task 33: 前端资产vendoring（htmx 4.0.0 + 扩展 + Alpine）

**Files:**
- Create: `web/static/js/vendor/*.js`（下载物）, `web/static/js/vendor/LICENSES.md`
- Modify: `web/assets.lock.json`

**Interfaces:**
- Consumes: Task 27 的 `assets.Update`。
- Produces（清单条目，版本锁定）：
  | name | version | 文件 | 用途 |
  |---|---|---|---|
  | `htmx` | 4.0.0 | `htmx.min.js` | 事件总线核心 |
  | `hx-sse` | 4.0.0 | `ext/hx-sse.min.js` | SSE 流 |
  | `hx-ws` | 4.0.0 | `ext/hx-ws.min.js` | WebSocket 双向 |
  | `hx-preload` | 4.0.0 | `ext/hx-preload.min.js` | 预加载/骨架屏 |
  | `hx-targets` | 4.0.0 | `ext/hx-targets.min.js` | 多目标响应 |
  | `hx-upsert` | 4.0.0 | `ext/hx-upsert.min.js` | 精细化 DOM 差量（按 id upsert） |
  | `hx-head` | 4.0.0 | `ext/hx-head.min.js` | head 合并 |
  | `hx-pending` | 4.0.0 | `ext/hx-pending.min.js` | 加载态 |
  | `hx-multipart` | 4.0.0 | `ext/hx-multipart.min.js` | 流式局部更新 |
  | `hx-download` | 4.0.0 | `ext/hx-download.min.js` | 文件下载 |
  | `alpinejs` | 3.17.4 | `alpine.min.js` | 轻量响应式 |
  | `hx-alpine-compat` | 4.0.0 | `ext/hx-alpine-compat.min.js` | Alpine 融合 |
  | `idiomorph` | 0.8.0 | `idiomorph.min.js` | 独立 DOM 差量（可选直接用） |
- 每项记录 `url`（`https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/ext/hx-sse.min.js` 等）、`sha256`、`license`（用 `assets add`/`update` 锁定）。
- 说明：`hx-status:*`（客户端事件响应拦截）、`hx-validate`（客户端校验）、`hx-encoding`（JSON 序列化）、`innerMorph/outerMorph`（Idiomorph 算法）均为 **htmx 4 内建能力**，无需额外插件文件；`docs/developer_guidelines.md` 中须列明该映射关系。

- [ ] **Step 1: 写失败测试**：`web/embed_test.go::TestAllManifestAssetsExistAndMatchChecksum`（离线校验：清单每一项文件存在且 sha256 一致）、`TestNoExternalCdnReferenceInTemplates`（`web/templates/**` 与生成的 HTML 中不得出现 `src="http` / `href="http` 引用 js/css）。
- [ ] **Step 2: 运行确认失败** `go test ./web/ -run 'TestAllManifest|TestNoExternalCdn' -v` → FAIL
- [ ] **Step 3: 执行下载与锁定**：`go run ./cmd/yd assets update` （`--source` 指向 jsdelivr），填充 `web/static/js/vendor/`，回填 sha256；生成 `LICENSES.md`（各库名称、版本、协议：htmx BSD-2-Clause、Alpine MIT、Idiomorph BSD-2-Clause）。
- [ ] **Step 4: 运行确认通过** `go test ./web/ -v` 与 `go run ./cmd/yd assets verify` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(web): vendor htmx 4.0.0, extensions and alpine with locked checksums"`

---

### Task 34: Tailwind v4 主题体系与样式管线

**Files:**
- Create: `web/static/css/app.css`（源，含 `@theme`）, `web/static/css/yd.css`（构建产物，签入以便离线可用）, `web/tailwind.manifest.json`（`@source` 扫描路径与产物校验和）
- Modify: `toolchain/tailwind.go`（Task 29）、`Dockerfile`

**Interfaces:**
- Produces：
  - `app.css` 结构：
    ```css
    @import "tailwindcss";
    @source "../../internal/**/*.go";      /* 扫描 Go 内嵌模板字符串 */
    @source "../templates/**/*.html";
    @theme {
      --color-brand-50: …; … --color-brand-950: …;   /* 蓝色系 11 阶 */
      --color-surface: …; --color-surface-muted: …;
      --color-content: …; --color-content-muted: …;
      --color-border-subtle: …;
      --font-sans: "Inter", ui-sans-serif, system-ui, sans-serif;
      --radius-card: 0.875rem;
      --shadow-card: 0 1px 2px rgb(2 6 23 / 0.06), 0 8px 24px -12px rgb(2 6 23 / 0.18);
      --spacing-sidebar: 16rem;
      --spacing-sidebar-collapsed: 4.5rem;
    }
    /* 管理端组件层：.yd-sidebar .yd-topbar .yd-card .yd-table .htmx-indicator … */
    ```
  - 主题切换：`:root` / `[data-theme="dark"]` 双份语义变量，`yd-app.js` 持久化到 `localStorage`。
  - 语义化类名必须以变量驱动（`bg-surface text-content border-border-subtle`），**禁止**在模板里散落硬编码色值。
- 硬规则：产物 `yd.css` 必须签入仓库（保证无 Tailwind 二进制时也能离线构建与运行）；`yd build` 会用二进制重新生成并覆盖。

- [ ] **Step 1: 写失败测试**：`TestThemeDefinesBlueBrandScaleAndSemanticTokens`（解析 `app.css`，断言 `--color-brand-500` 等 11 阶齐全 + 语义变量存在）、`TestDarkThemeOverridesSemanticTokens`、`TestBuiltCssIsMinifiedAndNonEmpty`（`yd.css` 存在且 > 3KB、无 `/*` 注释块）、`TestNoHardcodedHexInTemplates`（`web/templates/**` 内不得出现 `#rrggbb` 字面色值）。
- [ ] **Step 2: 运行确认失败** `go test ./web/ -run 'TestTheme|TestDark|TestBuiltCss|TestNoHardcoded' -v` → FAIL
- [ ] **Step 3: 实现** CSS 与主题；用真实 Tailwind v4.3.3 standalone（下载到 `.yd/tools/`，加入 `.gitignore`）执行一次 `toolchain.BuildCSS(minify)` 生成 `yd.css`。
- [ ] **Step 4: 运行确认通过** `go test ./web/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(web): tailwind v4 theme tokens and compiled stylesheet"`

---

### Task 35: 前端国密沙箱（SM2/SM3/SM4 JS 实现与验证）

**Files:**
- Create: `web/static/js/yd-guomi.js`（纯 JS，UMD，无依赖）、`web/static/js/yd-guomi-htmx.js`（htmx 钩子）、`web/static/js/yd-guomi.test.mjs`（Node 测试）, `web/guomi_vector_test.go`（Go 侧生成对照向量）
- Modify: `web/embed.go`（embed 新文件）

**Interfaces:**
- Produces（`window.ydGuomi`）：
  - `ydGuomi.sm3(data: Uint8Array|string): Uint8Array`、`sm3Hex(string): string`
  - `ydGuomi.sm2.generateKeyPair(): Promise<{publicKey: string(hex,04||x||y), privateKey: string(hex)}>`、`sm2.encrypt(pubHex, data: Uint8Array, opts?): Uint8Array`（C1C3C2，与 Go 端 `guomi.Encrypt` **字节级一致**）、`sm2.decrypt(privHex, ct): Uint8Array`、`sm2.sign/verify`（SM3 摘要 + DER 签名）
  - `ydGuomi.sm4.encryptCBC/decryptCBC/encryptGCM/decryptGCM`
  - `ydGuomi.ready(): Promise<void>`（惰性初始化 BigInt 常量表）
- htmx 钩子（`yd-guomi-htmx.js`）：
  - 声明式：`<form hx-post="/login" data-yd-encrypt="sm2" data-yd-pubkey="/api/guomi/pubkey">` —— 在 `htmx:config:request` 阶段拦截 `ctx.request.body`，对标记为 `data-yd-sensitive` 的字段用 SM2 加密后替换值，并加 `X-YD-Encrypted: sm2` 头；
  - 静态资产完整性：`ydGuomi.verifyIntegrity(url, expectSm3Hex)`（fetch → SM3 → 比对），供管理端校验本地缓存凭证。
- 验证方式（关键）：纯 JS 实现必须与 Go `guomi` 包**双向互操作**：
  - `web/guomi_vector_test.go` 用固定私钥/随机数种子生成 SM2 密文与签名、SM3 摘要（GB/T 官方向量 + 随机向量），写成 `testdata/guomi_vectors.json`；
  - `yd-guomi.test.mjs`（`node:test`）读取同一 JSON，断言 JS 侧 `sm3Hex` 与向量一致、`sm2.decrypt` 能解 Go 密文、`sm2.encrypt` 结果 Go 侧可解（由 `TestJSVectorsAcceptedByGo` 反向消费 JS 生成的 `testdata/guomi_js_vectors.json`）。
  - 测试入口：`go test ./web/ -run TestGuomi` 内部调用 `node web/static/js/yd-guomi.test.mjs`（若 `node` 不在 PATH 则 `t.Skip`，CI 上必须存在）。

- [ ] **Step 1: 写失败测试**：Go 侧 `TestSM3VectorsMatchJS`、`TestSM2GoDecryptsJS`、`TestSM2JSDecryptsGo`、`TestSM4CBCInteropWithJS`；JS 侧同源断言。
- [ ] **Step 2: 运行确认失败**：`go test ./web/ -run TestGuomi -v` → FAIL（`yd-guomi.js` 不存在）
- [ ] **Step 3: 实现** JS（SM3：纯 JS 压缩函数；SM2：BigInt 椭圆曲线点运算 + KDF；SM4：S 盒查表 + CBC/GCM），并导出 UMD。
- [ ] **Step 4: 运行确认通过** `go test ./web/ -run TestGuomi -v` 与 `node web/static/js/yd-guomi.test.mjs` 双双 PASS。
- [ ] **Step 5: 提交** `git commit -m "feat(web): pure-js guomi sandbox with go interop vectors"`

---

### Task 36: 前端国际化 `yd-i18n.js`

**Files:**
- Create: `web/static/js/yd-i18n.js`, `web/static/js/yd-i18n.test.mjs`, `web/i18n_test.go`

**Interfaces:**
- Produces：
  - `window.ydI18n.init({ lang, fallback, catalogs })`、`t(key, args)`、`setLang(lang)`（写 `localStorage` + `yd_lang` cookie + `document.documentElement.lang`）、`current()`、`on(fn)`
  - 服务端注入：模板布局中输出 `<script id="yd-i18n-catalog" type="application/json">{{ .I18nJSON }}</script>`（由 `i18n` 包序列化当前语言目录，复用 render 的 `json` 函数）
  - 声明式 DOM 绑定：`<span data-yd-i18n="user.title"></span>`、`<input data-yd-i18n-attr="placeholder:user.search">`，`htmx:after:swap` 后自动重新绑定新内容（与 htmx 局部更新协同）
  - 数字/日期格式化：`t.n(date, {dateStyle:'medium'})`（用 `Intl`，尊重当前 lang）
- 硬规则：目录 JSON 由服务端注入（不额外请求，离线可用）；缺失 key 回退 fallback 语言再回退 key 本身；`htmx:after:swap` 钩子只处理新增节点（避免重复翻译）。

- [ ] **Step 1: 写失败测试**：`TestCatalogInjectedIntoLayout`、`TestMissingKeyFallsBackToDefaultThenKey`、`TestDataAttributeBindingAndHtmxReswap`（Node + 极简 DOM stub）、`TestSetLangPersistsCookieAndLocalStorage`。
- [ ] **Step 2: 运行确认失败** `go test ./web/ -run TestI18n -v` → FAIL
- [ ] **Step 3: 实现**
- [ ] **Step 4: 运行确认通过** `go test ./web/ -run TestI18n -v && node web/static/js/yd-i18n.test.mjs` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(web): client-side i18n bound to htmx swaps"`

---

### Task 37: 管理后台模板（Admin Dashboard）

**Files:**
- Create: `web/templates/layouts/base.html`, `web/templates/layouts/dashboard.html`, `web/templates/partials/{sidebar,topbar,flash,confirm,table,form,modal,pagination,skeleton}.html`, `web/templates/pages/{login,dashboard,users,settings,realtime,i18n_demo,error}.html`, `web/static/js/yd-app.js`, `web/templates_test.go`

**Interfaces:**
- Consumes: `render`、`htmx`、`i18n`、Tasks 33/34/35/36 的资产。
- Produces：
  - `dashboard.html` 布局：左侧**常驻/折叠**导航（`hx-boost` + `hx-target:inherited="#yd-workspace"` + `hx-push-url`），顶部状态栏（面包屑、语言切换、主题切换、用户菜单），中央 `<main id="yd-workspace">` 高吞吐工作区；骨架屏（`hx-indicator` + `.yd-skeleton`）与 `transition`。
  - 页面均以 htmx 局部加载：侧栏链接 → `hx-get` 片段端点；表单 → `hx-post` + `hx-status:422="target:#errors"`；表格行 → `hx-swap="outerMorph"`（保留输入状态）。
  - `realtime.html`：SSE（`hx-sse` 扩展，`hx-sse:connect` / `sse-swap`）与 WebSocket（`hx-ws`）示例面板。
  - `login.html`：国密传输前置加密（`data-yd-encrypt="sm2"`），提交前 SM2 加密凭证。
  - `i18n_demo.html`：语言热切换（不刷新页面，靠 `yd-i18n` + htmx 片段重取）。
  - `yd-app.js`：主题切换与持久化、侧栏折叠记忆、`hx-preload` 预热、SSE 断线重连提示、`ydGuomi` 初始化；**不引入任何外部依赖**。
  - 资产引用：全部相对路径 `/static/...`，由 `app` 的 `StaticFS(web.FS())` 提供；脚本按顺序 `htmx.min.js` → 扩展 → `alpine` → `hx-alpine-compat` → `yd-guomi.js` → `yd-guomi-htmx.js` → `yd-i18n.js` → `yd-app.js`。
- 硬规则：无 Node 构建；无 CDN；所有交互零整页刷新（除 `login` 成功跳转）。

- [ ] **Step 1: 写失败测试** `web/templates_test.go`：
  - `TestAllTemplatesParse`（`render.New` 加载全部模板无错）
  - `TestDashboardLayoutHasSidebarTopbarWorkspace`（渲染后断言 `id="yd-workspace"`、`.yd-sidebar`、`.yd-topbar`）
  - `TestScriptsLoadedInOrderAndLocally`（断言顺序与本地路径，且无 `http`）
  - `TestNoInlineCdnAndNoNodeArtifacts`（无 `node_modules`/`.tailwindcss` 引用）
  - `TestLoginFormDeclaresGuomiEncryption`（含 `data-yd-encrypt="sm2"`）
  - `TestFormsUseHtmxWithStatusHandling`（每个表单含 `hx-post` 或 `hx-get`，且至少一个含 `hx-status:422`）
  - `TestEveryPageHasI18nDataAttributes`
- [ ] **Step 2: 运行确认失败** `go test ./web/ -run TestDashboard -v` → FAIL
- [ ] **Step 3: 实现** 模板与 JS
- [ ] **Step 4: 运行确认通过** `go test ./web/ -v` → PASS
- [ ] **Step 5: 提交** `git commit -m "feat(web): admin dashboard templates wired with htmx partial navigation"`

---

# Phase G —— 端到端验证

### Task 38: 全链路端到端验收

**Files:**
- Create: `e2e/e2e_test.go`, `e2e/scaffold_e2e_test.go`, `docs/verification.md`

**Interfaces:**
- Consumes: 全部前置任务。
- Produces：可复现的验收脚本与结果记录（`docs/verification.md`），包含实际命令与真实输出。

- [ ] **Step 1: 编写端到端测试**（`//go:build e2e` 标签，避免默认测试变慢）
  - `TestScaffoldUserBuildRun`: 在临时目录执行 `yd new demo --db sqlite --redis --asynq --i18n --htmx-ext sse,ws,preload,targets --local-replace <repo>`；断言文件齐全；`go build ./...` 通过；
  - 启动生成的 app（随机端口），逐一验证：`GET /` 返回仪表盘整页；带 `HX-Request: true` 请求返回**片段**（无 `<html>`）；`GET /42-xx.png` 命中带后缀路由；SSE 端点 `Content-Type: text/event-stream` 且首帧可读；WS 端点完成握手并回显；`GET /openapi.json` 为合法 OpenAPI 3.0；`/docs` 查看器可访问；登录表单 SM2 密文可被后端解密；
  - `yd build --targets linux/amd64,linux/arm64,windows/amd64` 产出三个产物且 magic 正确；
  - `yd docker` 若 daemon 可用则构建镜像并 `docker run` 冒烟（不可用则 skip 并记录）。
- [ ] **Step 2: 运行** `go test -tags e2e ./e2e/ -v -timeout 20m` → 记录真实输出
- [ ] **Step 3: 修复发现的问题**（每修复一处回到对应包的单元测试补一条回归用例）
- [ ] **Step 4: 全量质量门禁**：`go vet ./...`、`go test ./... -race`、`go build ./...`、`GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` 全部通过；`go run ./cmd/yd assets verify --offline` 通过
- [ ] **Step 5: 撰写 `docs/verification.md`** 并提交 `git commit -m "test(e2e): full lifecycle verification and quality gates"`

---

## 需求覆盖对照表（Spec → Task）

| Spec 条目 | 覆盖任务 |
|---|---|
| 二.1 CLI 五命令 + viper/afero/pflag | 26,27,28,29,30,31,32；2,3 |
| 二.2 分层配置与优先级 | 2 |
| 二.3 LICENSE/.gitignore/.gitattributes | 1,28 |
| 二.4 CLAUDE.md/AGENTS.md/developer_guidelines.md | 1,28 |
| 三.1 Go 1.27.2 + 自适应 JSON/Base64 | 1,4,5 |
| 三.2 基数树 + 部分路径/后缀匹配 | 7,8 |
| 三.3 GORM / go-redis / asynq | 23,24,25 |
| 三.4 零拷贝日志 | 6 |
| 三.5 JWT/WebSocket/SSE/CORS/限流/OpenAPI/i18n/gRPC/压缩 | 21,19,20,18,11,14,13,16,12 |
| 三.6 HTTP/1.1 + H2C + HTTP/3 + ACME | 15 |
| 三.7 SM2/SM3/SM4 | 22 |
| 三.8 project-layout + scratch Dockerfile | 28,31 |
| 四.1 无 Node/Htmx4+Tailwind4 | 29,30,33,34 |
| 四.2 前端国密沙箱 | 35 |
| 四.3 Htmx 4 插件全集成 | 10,33,37 |
| 四.4 Tailwind v4 @theme 蓝主题 | 34 |
| 四.5 管理后台模板 | 37 |
| 四.6 前端 i18n | 36 |
| 新增：前端依赖在线更新 + 离线独立使用 | 27,33 |

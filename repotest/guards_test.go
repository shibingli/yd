package repotest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shibingli/yd/core/regexpx"
)

// skippedDirs 是不参与源码守卫扫描的目录。
// Directories excluded from the source-guard scan.
var skippedDirs = map[string]bool{
	".git": true, ".scratch": true, ".yd": true, "bin": true, "dist": true,
	"node_modules": true, ".tailwindcss": true, "testdata": true, "vendor": true,
}

// goFile 是被扫描到的单个 Go 源文件。
// A single scanned Go source file.
type goFile struct {
	rel     string // 仓库相对路径（斜杠分隔）| repo-relative slash path
	src     string // 文件内容 | file content
	isTest  bool   // 是否为测试文件 | whether it is a test file
	lines   []string
	absPath string
}

// scanGoFiles 遍历仓库中的全部 .go 文件。
// scanGoFiles walks every .go file in the repository.
func scanGoFiles(t *testing.T, root string) []goFile {
	t.Helper()
	var files []goFile
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skippedDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		src := string(b)
		files = append(files, goFile{
			rel:     rel,
			src:     src,
			isTest:  strings.HasSuffix(rel, "_test.go"),
			lines:   strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n"),
			absPath: p,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 Go 源文件失败: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("未扫描到任何 Go 源文件，守卫测试可能失效")
	}
	return files
}

// scanTemplateFiles 遍历仓库中的全部 .tmpl 模板文件。
// scanTemplateFiles walks every .tmpl template file in the repository.
//
// 模板会被渲染成用户项目的源码与配置，其注释与出厂代码的注释受同一条中英规则约束。
// Templates are rendered into a user project's sources and configuration, so their comments fall
// under the same bilingual rule as shipped code.
func scanTemplateFiles(t *testing.T, root string) []goFile {
	t.Helper()
	var files []goFile
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skippedDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".tmpl") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		src := string(b)
		files = append(files, goFile{
			rel:     rel,
			src:     src,
			isTest:  false,
			lines:   strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n"),
			absPath: p,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("扫描模板文件失败: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("未扫描到任何模板文件，守卫测试可能失效")
	}
	return files
}

// TestNoDirectEncodingImports 强制 JSON / Base64 编解码统一走 jsonx / b64x。
// TestNoDirectEncodingImports enforces that JSON/Base64 always go through jsonx/b64x.
//
// 生产代码中禁止 import "encoding/json" 与 "encoding/base64"；唯一例外是
// 封装层自身（core/jsonx/、core/b64x/）。测试文件允许把标准库当作独立对照实现（测试预言机）。
// Production code must not import "encoding/json" or "encoding/base64"; the only
// exemptions are the wrapper packages themselves (core/jsonx/, core/b64x/). Test files may
// use the standard library as an independent oracle.
func TestNoDirectEncodingImports(t *testing.T) {
	rules := []struct {
		importPath string
		allowedDir string
		hint       string
	}{
		{`"encoding/json"`, "core/jsonx/", "请改用 github.com/shibingli/yd/core/jsonx（Marshal/Unmarshal/NewEncoder/NewDecoder/Valid）"},
		{`"encoding/base64"`, "core/b64x/", "请改用 github.com/shibingli/yd/core/b64x（StdEncoding/URLEncoding/… 及其 Encode/Decode/AppendEncode/AppendDecode）"},
	}

	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if f.isTest {
			continue
		}
		if strings.HasPrefix(f.rel, rules[0].allowedDir) || strings.HasPrefix(f.rel, rules[1].allowedDir) {
			continue
		}
		for _, r := range rules {
			if !strings.Contains(f.src, r.importPath) {
				continue
			}
			// 只统计真的出现在 import 块里的引用，避免误伤注释与字符串
			if !hasImport(f.lines, r.importPath) {
				continue
			}
			t.Errorf("%s 直接 import 了 %s；%s", f.rel, r.importPath, r.hint)
		}
	}
}

// hasImport 判断某行是否为 import 语句（含别名与括号形式）。
// hasImport reports whether any line imports the given quoted path.
func hasImport(lines []string, quoted string) bool {
	re := regexpx.MustCompile(`^\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"` + regexpx.QuoteMeta(strings.Trim(quoted, `"`)) + `"\s*$`)
	for _, l := range lines {
		if re.MatchString(strings.TrimRight(l, " \t")) {
			return true
		}
	}
	return false
}

// TestNoStdlibLogOrPrintfLogging 强制所有日志走自研 logx。
// TestNoStdlibLogOrPrintfLogging enforces that every log goes through logx.
//
// 例外：server 包为了满足 http.Server.ErrorLog（要求 *log.Logger）而写的适配器，
// 其输出必须转发进 logx。
// Exception: the server package adapts http.Server.ErrorLog (which demands a
// *log.Logger); that adapter must forward into logx.
func TestNoStdlibLogOrPrintfLogging(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if f.isTest || strings.HasPrefix(f.rel, "runtime/server/") {
			continue
		}
		if hasImport(f.lines, `"log"`) {
			t.Errorf("%s 直接 import 了标准库 log；日志必须统一使用 github.com/shibingli/yd/core/logx", f.rel)
		}
		for i, l := range f.lines {
			trimmed := strings.TrimSpace(l)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(l, "fmt.Print(") || strings.Contains(l, "fmt.Printf(") ||
				strings.Contains(l, "fmt.Println(") || strings.Contains(l, "fmt.Fprint(") {
				t.Errorf("%s:%d 使用 fmt.Print* 作为输出；请改用 logx", f.rel, i+1)
			}
		}
	}
}

// logCallRe 匹配 logx 的级别方法调用及其首个字符串字面量。
// logCallRe matches logx level calls together with their first literal.
var logCallRe = regexpx.MustCompile(`\.(?:Debug|Info|Warn|Error|Fatal)\(\s*"([^"\\]*)"`)

// testingCallRe 匹配 testing 包的断言方法；它们的名字与 logx 级别方法重合，
// 必须排除，否则会把测试断言文案误判为日志文案。
// testingCallRe matches testing assertion helpers; their names collide with logx
// level methods and must be excluded, otherwise assertions are misread as logs.
var testingCallRe = regexpx.MustCompile(`\b(?:t|b|tb|ft|st|env)\.(?:Fatal|Fatalf|Error|Errorf)\(`)

// TestLogMessagesAreBilingual 强制日志文案与注释一样中英同步。
// TestLogMessagesAreBilingual enforces the same bilingual rule on log messages.
func TestLogMessagesAreBilingual(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if strings.HasPrefix(f.rel, "repotest/") {
			continue // 守卫自身不可避免地引用被禁止的字面量 | the guard itself quotes them
		}
		if f.isTest {
			// 该规则针对出厂代码；测试里的日志字符串多为编码器夹具，
			// 例如 logx 自身测试需要构造极短的消息来断言输出形状。
			// This rule targets shipped code; log strings in tests are mostly encoder
			// fixtures (logx's own tests need very short messages to assert output shape).
			continue
		}
		for i, l := range f.lines {
			trimmed := strings.TrimSpace(l)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if testingCallRe.MatchString(l) {
				continue // 测试断言，不是日志 | test assertion, not a log
			}
			for _, m := range logCallRe.FindAllStringSubmatch(l, -1) {
				msg := m[1]
				if msg == "" {
					continue
				}
				if !strings.Contains(msg, " | ") {
					t.Errorf("%s:%d 日志文案未中英同步（缺少 \" | \"）：%q\n"+
						"    要求形如 l.Info(\"server started | 服务器已启动\", ...)", f.rel, i+1, msg)
				}
			}
		}
	}
}

// TestNoFmtErrorfOutsideErrx 强制错误统一声明为哨兵。
// TestNoFmtErrorfOutsideErrx enforces that errors are declared as sentinels.
//
// 默认禁止 fmt.Errorf：附加细节用 errx.Detail，保留底层错误用 errx.Wrap /
// errx.WrapDetail。唯一例外是 errx 包自身的实现。
// fmt.Errorf is forbidden by default: use errx.Detail for extra context and
// errx.Wrap / errx.WrapDetail to keep the underlying error. The only exemption
// is the errx package implementation itself.
func TestNoFmtErrorfOutsideErrx(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if f.isTest || strings.HasPrefix(f.rel, "core/errx/") || strings.HasPrefix(f.rel, "repotest/") {
			continue
		}
		for i, l := range f.lines {
			if strings.Contains(l, "fmt.Errorf(") {
				t.Errorf("%s:%d 使用了 fmt.Errorf；请改为 errx.Detail/errx.Wrap/errx.WrapDetail，"+
					"并把哨兵错误在包级用 errx.New 声明", f.rel, i+1)
			}
		}
	}
}

// TestGovernanceStatesNewConstraints 校验治理文件已写入新约束。
// TestGovernanceStatesNewConstraints checks that governance docs state the new rules.
func TestGovernanceStatesNewConstraints(t *testing.T) {
	root := repoRoot(t)
	checks := map[string][]string{
		"CLAUDE.md": {
			"Co-Authored-By: Eric Shi <shibingli@sina.com>", // 提交署名口径 | commit signature
			"YD_",           // 环境变量前缀 | env prefix
			"logx",          // 日志统一走自研 | logging via logx
			"中英文同步",         // 注释与日志文案双语 | bilingual comments and logs
			"禁止在提交信息",       // 提交纪律 | commit discipline
			"OpenAPI",       // OpenAPI 注解要求 | OpenAPI annotations
			"jsonx", "b64x", // 编解码统一封装层 | codec wrappers
			"errx", "fmt.Errorf", // 错误声明式哨兵 | declared sentinel errors
			"sec_key",                 // 每用户 JWT 密钥 | per-user JWT key
			"多租户",                     // 租户硬边界 | tenant boundary
			"betteralign",             // 内存对齐要求 | memory alignment
			"data-yd-i18n",            // 前端 i18n 约定 | frontend i18n contract
			"优雅退出",                    // 关停顺序与长连接登记 | shutdown order and long-lived connection tracking
			"kardianos",               // 服务化唯一入口 | the single service-manager entry point
			"yd ko",                   // 无 Dockerfile 打包路径 | the Dockerfile-less packaging path
			"yd tools",                // 外部工具统一入口 | the single entry point for external tools
			"security/auth/gormstore", // 鉴权数据落库 | auth data lives in the database
			"PresetDSN",               // 连接串预置 | connection-string presets
			"_txlock",                 // SQLite 立即事务约束 | the SQLite immediate-transaction constraint
			"yd authz check",          // 权限静态校验 | the permission linter
			"data-yd-perm",            // 前端声明式权限判定 | declarative front-end capability guards
			"不是安全边界",                  // 前端判定红线 | the front-end is not the enforcement point
			"默认初始化",                   // 首启种子数据 | first-start seed data
			"yd completion",           // Shell 补全入口 | the shell-completion entry point
			"自动建库",                    // 库不存在时自动创建 | creating a missing database on connect
		},
		"AGENTS.md": {
			"Co-Authored-By: Eric Shi <shibingli@sina.com>",
			"Test-and-Fix",
			"logx",
			"jsonx", "b64x",
			"OpenAPI",
			"claude", // 必须出现"禁止 claude 相关内容"的约束 | must state the prohibition
			"优雅退出",
			"service",   // 服务化约束 | service-mode constraint
			"交叉编译",      // 平台相关符号护栏 | platform-specific symbol guard
			"yd ko",     // 镜像打包只走既有入口 | image packaging only through the existing entry points
			"gormstore", // 鉴权数据落库 | auth data lives in the database
			"PresetDSN", // 连接串预置 | connection-string presets
			"无 CGO",     // 纯 Go 默认形态 | the CGO-free default build
		},
		"docs/developer_guidelines.md": {
			"YD_",
			"Co-Authored-By: Eric Shi <shibingli@sina.com>",
			"logx",
			"OpenAPI",
			"sec_key",
			"betteralign",
			"data-yd-i18n",
			"优雅退出",
		},
	}
	for name, wants := range checks {
		content := strings.ToLower(readRepoFile(t, root, name))
		for _, want := range wants {
			if !strings.Contains(content, strings.ToLower(want)) {
				t.Errorf("%s 缺少约束描述: %q", name, want)
			}
		}
	}
}

// TestEnvTemplateUsesYDPrefix 校验环境变量模板使用 YD_ 前缀。
// TestEnvTemplateUsesYDPrefix checks the env template uses the YD_ prefix.
func TestEnvTemplateUsesYDPrefix(t *testing.T) {
	content := readRepoFile(t, repoRoot(t), ".env.template")
	if strings.Contains(content, "YUNGO_") {
		t.Error(".env.template 仍含旧前缀 YUNGO_，应统一为 YD_")
	}
	if !strings.Contains(content, "YD_SERVER_PORT") || !strings.Contains(content, "YD_DB_MAXCONN") {
		t.Error(".env.template 未使用 YD_ 前缀示例")
	}
}

// TestLongLivedPackagesDoNotImportLifecycle 强制长连接包与关闭编排之间的依赖方向。
// TestLongLivedPackagesDoNotImportLifecycle enforces the dependency direction between the long-lived connection packages and the shutdown orchestrator.
//
// ws 与 sse 只声明自己的最小「关闭信号」接口，由 app 侧注入实现；若它们反向 import
// lifecycle，依赖图会成环，且这两个包将无法脱离关闭编排单独复用。
// ws and sse declare their own minimal "shutdown signal" interface, implemented on the app side;
// importing lifecycle back would create a cycle and make both packages unusable without the
// orchestrator.
func TestLongLivedPackagesDoNotImportLifecycle(t *testing.T) {
	const lifecyclePath = "github.com/shibingli/yd/runtime/lifecycle"
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if f.isTest {
			continue
		}
		if !strings.HasPrefix(f.rel, "http/ws/") && !strings.HasPrefix(f.rel, "http/sse/") {
			continue
		}
		if hasImport(f.lines, `"`+lifecyclePath+`"`) {
			t.Errorf("%s 反向 import 了 %s；长连接包只应声明自己的最小关闭信号接口，"+
				"由 app 侧注入实现，避免依赖成环", f.rel, lifecyclePath)
		}
	}
}

// cjkRe 匹配中日韩统一表意文字。| cjkRe matches CJK unified ideographs.
var cjkRe = regexpx.MustCompile(`[\p{Han}]`)

// commentLineRe 拆出一条注释行的「前缀」与「正文」。前缀包含注释符、列表符号与 OpenAPI 注解名，
// 它们必须留在行首，不参与中英交换。
// commentLineRe splits a comment line into its prefix and body. The prefix holds the comment
// marker, a list bullet and an OpenAPI annotation name: all of them stay at the front and take no
// part in the language swap.
var commentLineRe = regexpx.MustCompile(`^(\s*(?://|#)+\s*(?:[-*]\s+|\d+\.\s+)?(?:@[A-Za-z]+\s+)?)(.*)$`)

// TestBilingualCommentsAreEnglishFirst 强制注释与日志文案「英文在前、中文在后」。
// TestBilingualCommentsAreEnglishFirst enforces the English-first order of bilingual comments.
//
// 规则见 CLAUDE.md §3.1。判定方式很窄，只抓真正违规的形态：一行注释按 " | " 恰好切成两段，
// 且左段含中文、右段不含——那一定是「中文在前」。取值列表（`# 数据库：mysql | postgres | sqlite`）
// 与注释里的表格行（正文以 `|` 开头）都切成两段以上或以 `|` 起头，天然不在此列。
// 测试文件整体豁免：它们会内嵌 Go 源码作为夹具，夹具里的注释行必须逐字保留
// （openapi 的注解解析用例正是如此），与 TestLogMessagesAreBilingual 的豁免理由一致。
// The rule is CLAUDE.md §3.1. The check is deliberately narrow and only catches the genuine
// violation: a comment line that splits into exactly two parts on " | " with CJK on the left and
// none on the right is Chinese-first. Value lists and table rows inside comments split into more
// than two parts or start with "|", so they never match. Test files are exempt wholesale because
// they embed Go sources as fixtures whose comment lines must stay verbatim — the openapi annotation
// tests are exactly that case — mirroring the exemption in TestLogMessagesAreBilingual.
func TestBilingualCommentsAreEnglishFirst(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if f.isTest || strings.HasPrefix(f.rel, "repotest/") {
			continue
		}
		checkCommentOrder(t, f.rel, f.lines, false)
	}
	for _, f := range scanTemplateFiles(t, repoRoot(t)) {
		checkCommentOrder(t, f.rel, f.lines, true)
	}
}

// checkCommentOrder reports every Chinese-first bilingual comment in one file. | checkCommentOrder 报告单个文件中所有「中文在前」的双语注释。
func checkCommentOrder(t *testing.T, rel string, lines []string, allowHash bool) {
	t.Helper()
	for i, l := range lines {
		m := commentLineRe.FindStringSubmatch(strings.TrimRight(l, " \t"))
		if m == nil {
			continue
		}
		marker, body := m[1], m[2]
		if !allowHash && strings.HasPrefix(strings.TrimLeft(marker, " \t"), "#") {
			continue
		}
		if !strings.Contains(body, " | ") {
			continue
		}
		parts := strings.Split(body, " | ")
		if len(parts) != 2 {
			continue // 取值列表或表格行 | a value list or a table row
		}
		left, right := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if left == "" || right == "" || strings.HasPrefix(left, "|") {
			continue
		}
		if cjkRe.MatchString(left) && !cjkRe.MatchString(right) {
			t.Errorf("%s:%d 注释为「中文在前」，规则要求「英文在前、中文在后」：%s\n"+
				"    应写成 `// English text | 中文文本`（CLAUDE.md §3.1）", rel, i+1, strings.TrimSpace(l))
		}
	}
}

// cgoImportRe 匹配 cgo 的伪包导入。| cgoImportRe matches the cgo pseudo-package import.
var cgoImportRe = regexpx.MustCompile(`^\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"C"\s*$`)

// cgoSQLiteDrivers 是需要 cgo 的 SQLite 驱动，本仓一律不得引入。
// cgoSQLiteDrivers are the cgo-based SQLite drivers this repository must never pull in.
var cgoSQLiteDrivers = []string{
	"github.com/mattn/go-sqlite3",
	"github.com/mutecomm/go-sqlcipher",
}

// TestNoCgoInDefaultBuild 强制默认构建形态为无 CGO。
// TestNoCgoInDefaultBuild enforces a CGO-free default build.
//
// 本仓的默认形态是「纯 Go、零 cgo」：二进制静态链接、可直接放进 scratch 级空镜像，
// 且交叉编译到任意平台都不需要目标平台的 C 工具链。SQLite 因此必须用纯 Go 实现
// （glebarez/sqlite → modernc.org/sqlite），换成 mattn/go-sqlite3 这类 cgo 驱动会
// 一次性破坏全部三条性质，而且症状要到交叉编译或容器运行时才暴露。
// 使用方日后自行引入 cgo 是他们的选择，但框架自身不得成为那个引入点。
// The default here is pure Go with zero cgo: the binary is statically linked, drops straight into a
// scratch-level image, and cross-compiles to any platform without that platform's C toolchain.
// SQLite must therefore be a pure-Go implementation (glebarez/sqlite → modernc.org/sqlite);
// switching to a cgo driver such as mattn/go-sqlite3 breaks all three properties at once, and the
// symptom only shows up at cross-compile or container runtime. A downstream project adding cgo is
// its own choice, but the framework must not be the one that introduces it.
func TestNoCgoInDefaultBuild(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if strings.HasPrefix(f.rel, "repotest/") {
			continue // 守卫自身引用了待搜字面量 | the guard itself quotes the searched literals
		}
		for i, l := range f.lines {
			if cgoImportRe.MatchString(strings.TrimRight(l, " \t")) {
				t.Errorf("%s:%d 导入了 cgo 伪包 %q；默认构建必须无 CGO"+
					"（SQLite 用 github.com/glebarez/sqlite）", f.rel, i+1, "C")
			}
		}
	}

	gomod := readRepoFile(t, repoRoot(t), "go.mod")
	for _, drv := range cgoSQLiteDrivers {
		// 只拦直接依赖：间接出现在模块图里但不参与编译是可以接受的。
		// 两种 require 写法都要覆盖——块内的单行，以及单行 require 语句；
		// 只认前者会漏掉 `require github.com/mattn/go-sqlite3 v1.14.22` 这种写法。
		// Only direct requirements are rejected: an indirect module-graph entry that is not compiled
		// in is acceptable. Both require forms must be covered — one line inside a require block and a
		// single-line require statement; matching only the former misses
		// `require github.com/mattn/go-sqlite3 v1.14.22`.
		re := regexpx.MustCompile(`(?m)^\s*(?:require\s+)?` + regexpx.QuoteMeta(drv) + `\s`)
		if re.MatchString(gomod) {
			t.Errorf("go.mod 直接依赖了需要 cgo 的 %s；请改用纯 Go 的 github.com/glebarez/sqlite", drv)
		}
	}
}

// TestAuthDomainStaysFreeOfGORM 强制 auth 域包不引入任何 ORM 依赖。
// TestAuthDomainStaysFreeOfGORM keeps the auth domain package free of any ORM dependency.
//
// auth 只描述租户、用户、角色与权限的领域模型与规则，存储经 Store 接口注入；
// 一旦它自己 import 了 GORM，内存实现与任何第三方存储都再也无法独立复用，
// 领域规则也就与某一种数据库绑死了。持久化实现放在子包（security/auth/gormstore）。
// The auth package describes the domain model and rules for tenants, users, roles and permissions;
// storage arrives through the Store interface. Importing GORM here would make the in-memory store
// and any third-party backend unusable on their own, and would tie the domain rules to one database.
// Persistence lives in a subpackage (security/auth/gormstore).
func TestAuthDomainStaysFreeOfGORM(t *testing.T) {
	ormPaths := []string{`"gorm.io/gorm"`, `"gorm.io/driver/mysql"`, `"gorm.io/driver/postgres"`}
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		// 只看 auth 包直属文件；子包（如 security/auth/gormstore）本就是允许放 ORM 的地方。
		// Only the auth package's own files: subpackages such as security/auth/gormstore are where the ORM belongs.
		if !strings.HasPrefix(f.rel, "security/auth/") || strings.Contains(strings.TrimPrefix(f.rel, "security/auth/"), "/") {
			continue
		}
		for _, p := range ormPaths {
			if hasImport(f.lines, p) {
				t.Errorf("%s import 了 %s；auth 域包必须保持零 ORM 依赖，持久化实现请放到子包", f.rel, p)
			}
		}
	}
}

// TestNoDirectPanic 禁止非测试源码直接发起 panic。
// TestNoDirectPanic forbids direct panic calls in non-test sources.
//
// 唯一豁免是 `recover()` 块里的 `panic(rec)` 重新抛出：它转发的是已经发生的 panic，交给外层
// Recover 收尾；去掉它等于吞掉 panic，请求会在 panic 之后继续往下跑，比 panic 本身更危险。
// 判定在 AST 层面完成：同一函数（含函数字面量）内既有 recover() 调用，又有恰好接收一个裸标识符的
// panic，该 panic 才被豁免。测试文件整体不参与扫描，否则测试里验证 panic 行为的夹具会全部命中。
// The only exemption is the `panic(rec)` re-raise inside a `recover()` block: it forwards a panic
// that already happened to the outer Recover, and removing it would swallow the panic and let the
// request continue, which is worse than the panic itself. The check works on the AST: a panic whose
// sole argument is a bare identifier is exempt only when the same function (function literals
// included) also calls recover(). Test files are not scanned, or fixtures that assert panic
// behavior would all trip the guard.
func TestNoDirectPanic(t *testing.T) {
	fset := token.NewFileSet()
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if f.isTest || strings.HasPrefix(f.rel, "repotest/") {
			continue
		}
		file, err := parser.ParseFile(fset, f.absPath, f.src, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", f.rel, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			var body *ast.BlockStmt
			switch fn := n.(type) {
			case *ast.FuncDecl:
				body = fn.Body
			case *ast.FuncLit:
				body = fn.Body
			default:
				return true
			}
			if body != nil {
				checkDirectPanics(t, f.rel, fset, body)
			}
			return true
		})
	}
}

// checkDirectPanics reports direct panic calls in one function body, exempting recover re-raises. | checkDirectPanics
// 报告单个函数体中的直接 panic 调用，豁免 recover 之后的重新抛出。
func checkDirectPanics(t *testing.T, rel string, fset *token.FileSet, body *ast.BlockStmt) {
	t.Helper()
	hasRecover := false
	var panics []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false // nested functions are checked on their own | 嵌套函数单独检查
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		switch ident.Name {
		case "recover":
			hasRecover = true
		case "panic":
			panics = append(panics, call)
		}
		return true
	})
	for _, call := range panics {
		if hasRecover && isBareIdentifierArgument(call) {
			continue
		}
		pos := fset.Position(call.Pos())
		t.Errorf("%s:%d 直接发起了 panic；接线期错误请登记并由 Validate/Execute 返回，"+
			"运行期错误请返回 error（唯一豁免：recover() 块中的 panic(rec) 重新抛出）", rel, pos.Line)
	}
}

// isBareIdentifierArgument reports whether a panic call takes exactly one bare identifier. | isBareIdentifierArgument
// 报告 panic 调用是否恰好接收一个裸标识符。
func isBareIdentifierArgument(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	_, ok := call.Args[0].(*ast.Ident)
	return ok
}

// TestToolResolversDoNotImportTheRegistry 强制工具解析包与 tools 注册表之间的依赖方向。
// TestToolResolversDoNotImportTheRegistry enforces the dependency direction between the tool resolvers and the tools registry.
//
// tools 聚合 align / ko / toolchain 三个解析器，因此这三个包**不得**反向 import tools：
// 一旦反向引用就成环，注册表也就无法再引用它们各自的 InstallHint 常量，
// 只能各写一份提示文案，而这正是「两处提示不一致」的来源。
// The tools package aggregates the align, ko and toolchain resolvers, so those three must never
// import it back: the reference would be a cycle, and the registry would lose its ability to reuse
// their InstallHint constants — forcing a second copy of each hint, which is exactly how two hints
// drift apart.
func TestToolResolversDoNotImportTheRegistry(t *testing.T) {
	const registryPath = "github.com/shibingli/yd/tooling/tools"
	resolvers := []string{"tooling/align/", "tooling/ko/", "tooling/toolchain/"}
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if f.isTest || strings.HasPrefix(f.rel, "repotest/") {
			continue
		}
		if !slices.ContainsFunc(resolvers, func(p string) bool { return strings.HasPrefix(f.rel, p) }) {
			continue
		}
		if hasImport(f.lines, `"`+registryPath+`"`) {
			t.Errorf("%s 反向 import 了 %s；工具解析包只暴露 Resolve 与 InstallHint，"+
				"由 tools 包单向聚合，避免依赖成环", f.rel, registryPath)
		}
	}
}

// stringLiteralBody 是 Go 字符串字面量的「体内」正则片段：非引号非反斜杠的字符，或一个转义序列。
// stringLiteralBody is the regex fragment for the inside of a Go string literal: either a character
// that is neither a quote nor a backslash, or one escape sequence.
const stringLiteralBody = `(?:[^"\\]|\\.)*`

// stringLiteralRe 匹配一个完整的 Go 字符串字面量并**捕获其内容**。
// stringLiteralRe matches a whole Go string literal and **captures its body**.
//
// 捕获括号不是装饰：调用方取的是 `m[1]`，少了它就是一处处越界 panic——
// 而 panic 会让整个守卫失效，比漏报更难察觉。TestStringLiteralReStaysUsable 钉住这一点。
// The capture group is load-bearing: callers read `m[1]`, and without it every use panics —
// a panicking guard fails wholesale, which is harder to notice than a missed report.
// TestStringLiteralReStaysUsable pins it down.
var stringLiteralRe = `"(` + stringLiteralBody + `)"`

// stringLiteralMatchRe 是 stringLiteralRe 的编译结果，包级只编译一次。
// stringLiteralMatchRe is stringLiteralRe compiled once at package level.
//
// 逐行编译会把这个守卫从「读文本」变成「反复解析正则」——换到 regexp2 之后解析成本更高，
// 因此把编译移到初始化期。
// Compiling per line turns this guard from reading text into repeatedly parsing a pattern; parsing
// costs more under regexp2, so compilation moves to initialization.
var stringLiteralMatchRe = regexpx.MustCompile(stringLiteralRe)

// asciiWordRe 判定一段文本里是否存在至少两个连续 ASCII 字母。
// asciiWordRe reports whether text contains at least two consecutive ASCII letters.
var asciiWordRe = regexpx.MustCompile(`[A-Za-z]{2,}`)

// TestStringLiteralReStaysUsable 钉住文案守卫所依赖的正则形态。
// TestStringLiteralReStaysUsable pins the pattern the copy guard depends on.
//
// 存在的理由：守卫一旦 panic 或被改坏，表现是「测试通过」而不是「测试失败」——
// 因为报错的那一行根本没执行到。因此对守卫自身做断言，与对被测代码做断言同样必要。
// Why it exists: when the guard panics or is edited badly, the symptom is a *passing* test rather
// than a failing one, because the reporting line never runs. Asserting on the guard itself is
// therefore as necessary as asserting on the code it checks.
func TestStringLiteralReStaysUsable(t *testing.T) {
	re := stringLiteralMatchRe
	cases := []struct{ in, want string }{
		{`x := "plain"`, "plain"},
		{`x := "中文"`, "中文"},
		// 含转义引号的字面量必须整体捕获，否则尾部残留的中文会永远误报。
		// A literal with an escaped quote must be captured whole, or the Chinese tail reports forever.
		{`y := "[data-theme=\"dark\"] 中文"`, `[data-theme=\"dark\"] 中文`},
		{`z := "a | b"`, "a | b"},
	}
	for _, c := range cases {
		m := re.FindAllStringSubmatch(c.in, -1)
		if len(m) != 1 || len(m[0]) != 2 {
			t.Fatalf("输入 %q 的匹配 = %v，期望恰好一个匹配且带一个捕获组", c.in, m)
		}
		if m[0][1] != c.want {
			t.Errorf("输入 %q 捕获到 %q，期望 %q", c.in, m[0][1], c.want)
		}
	}
}

// outputCallRe 匹配会产生运行时文案的调用：错误哨兵、日志级别方法、CLI 打印。
// outputCallRe matches the calls that produce runtime copy: error sentinels, log levels, CLI prints.
//
// 要求字面量**紧跟左括号**（允许空白），这样 `err.Error()` 这类无参方法调用不会被误判成日志调用。
// The literal must follow the opening parenthesis (whitespace allowed), so a no-argument call such as
// `err.Error()` is never mistaken for a log call.
//
// **覆盖边界（刻意划定）**：本守卫覆盖**出厂代码的运行时文案**——错误哨兵、日志、CLI 打印——
// 以及全部注释。**不覆盖测试断言消息**（`t.Fatalf` / `t.Errorf` / `t.Logf` 等）：
// 那是开发者诊断信息，不是程序对用户的「异常输出 / 提示 / 结果响应」，且本仓既有的
// TestLogMessagesAreBilingual 同样豁免测试文件。若要收紧到覆盖测试断言，
// 在 outputCallRe 里加上 `(?:t|b|tb)\.(?:Fatal|Error|Log)(?:f|ln)?\(\s*"` 即可，
// 但那会让守卫的命中量翻数倍，属于**规范口径**的变更，需先确认。
// **Deliberate coverage boundary**: this guard covers runtime copy in shipped code — error sentinels,
// logs, CLI prints — plus every comment. It does **not** cover test assertion messages
// (`t.Fatalf` / `t.Errorf` / `t.Logf` and friends): those are developer diagnostics rather than the
// program's error output, prompts or result responses, and the pre-existing
// TestLogMessagesAreBilingual exempts test files the same way. Tightening it is a change of policy,
// not of implementation, so it needs a decision first.
var outputCallRe = regexpx.MustCompile(`(?:errx\.(?:New|Detail|Wrap|WrapDetail)\(|\.(?:Debug|Info|Warn|Error|Fatal)\(\s*"|Fprint(?:f|ln)?\()`)

// TestBilingualCoverage 强制「每一处注释与运行时文案都不得只有一种语言」。
// TestBilingualCoverage enforces that no comment or runtime string carries only one language.
//
// 这是 CLAUDE.md §3.1 细化后的形态，与只查语序的 TestBilingualCommentsAreEnglishFirst 互补：
//   - 行末注释（代码之后）与单行注释没有换行的余地，必须同行写 `English | 中文`；
//   - 方法与多行注释允许换行分隔，但**整块必须两种语言都有**；
//   - 异常输出、提示、结果响应（哨兵、日志、CLI 打印）同样必须同行成对。
//
// This is the refined shape of CLAUDE.md §3.1 and complements TestBilingualCommentsAreEnglishFirst,
// which only checks ordering:
//   - a trailing comment or a one-line comment has nowhere to break, so it must carry
//     "English | 中文" on that very line;
//   - method docs and multi-line blocks may split the languages across lines, but the block must
//     hold both languages;
//   - error output, prompts and result responses (sentinels, logs, CLI prints) are pairs too.
func TestBilingualCoverage(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if strings.HasPrefix(f.rel, "repotest/") {
			continue // 守卫自身的代码示例与文案即规则本身 | the guard quotes the rule it enforces
		}
		checkBilingualCoverage(t, f, false)
	}
	for _, f := range scanTemplateFiles(t, repoRoot(t)) {
		if strings.HasSuffix(f.rel, ".md.tmpl") {
			continue // Markdown 模板里的 `#` 是标题而不是注释 | in Markdown templates `#` is a heading, not a comment
		}
		checkBilingualCoverage(t, f, true)
	}
}

// commentStart 返回行内注释符的下标；字符串字面量里的 `//` 不算。没有任何注释时返回 -1。
// commentStart returns the index of the line's comment marker, ignoring `//` inside string literals,
// or -1 when the line carries no comment.
func commentStart(line string, allowHash bool) int {
	var quote byte
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case quote != 0:
			if ch == '\\' {
				i++
			} else if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'' || ch == '`':
			quote = ch
		case ch == '/' && i+1 < len(line) && line[i+1] == '/':
			return i
		case allowHash && ch == '#' && i == indent:
			return i
		}
	}
	return -1
}

// checkBilingualCoverage 报告单个文件里所有「只有一种语言」的注释与运行时文案。
// checkBilingualCoverage reports every one-language comment and runtime string in one file.
func checkBilingualCoverage(t *testing.T, f goFile, allowHash bool) {
	t.Helper()

	// 语序守卫已认得的豁免：编译指令与生成标记。| The exemptions the ordering guard already knows.
	directiveRe := regexpx.MustCompile(`^\s*(?://|#)\s*(?:go:build|go:generate|\+build|Code generated)`)

	for i := 0; i < len(f.lines); i++ {
		line := strings.TrimRight(f.lines[i], " \t")
		start := commentStart(line, allowHash)
		if start < 0 || directiveRe.MatchString(line) {
			continue
		}
		// 收集整块注释，判断它是行末注释还是独立块。| Gather the whole block: trailing comment or standalone.
		bodies := []string{strings.TrimSpace(strings.TrimLeft(line[start:], "/#"))}
		trailing := strings.TrimSpace(line[:start]) != ""
		j := i + 1
		for !trailing && j < len(f.lines) {
			next := strings.TrimRight(f.lines[j], " \t")
			s := commentStart(next, allowHash)
			if s < 0 || strings.TrimSpace(next[:s]) != "" {
				break
			}
			bodies = append(bodies, strings.TrimSpace(strings.TrimLeft(next[s:], "/#")))
			j++
		}

		block := strings.Join(bodies, "\n")
		if !cjkRe.MatchString(block) {
			i = j - 1
			continue
		}
		switch {
		case trailing || len(bodies) == 1:
			if !strings.Contains(bodies[0], " | ") {
				t.Errorf("%s:%d 行末/单行注释只有中文，必须同行写成 `English | 中文`：%s",
					f.rel, i+1, strings.TrimSpace(line))
			}
		default:
			// 多行块允许换行分隔，但整块必须两种语言都有。| A multi-line block may break lines, but must hold both languages.
			if !asciiWordRe.MatchString(block) {
				t.Errorf("%s:%d 多行注释整块只有中文，英文段落缺失（可换行分隔）：%s",
					f.rel, i+1, strings.TrimSpace(line))
			}
		}
		i = j - 1
	}

	// 运行时文案：哨兵、日志、CLI 打印必须同行成对。| Runtime copy: sentinels, logs and CLI prints pair on one line.
	for i, line := range f.lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") || !outputCallRe.MatchString(line) {
			continue
		}
		// 字面量正则必须认转义：含转义引号的字符串（如 CSS 选择器 `[data-theme=\"dark\"]`）
		// 若按朴素的 `[^"]*` 切分，会在转义引号处断开，尾部残留一段没有分隔符的中文，
		// 从而**永远误报**——那是无法通过修改被测代码消除的假阳性。
		// The pattern must understand escapes: a literal containing an escaped quote (a CSS selector
		// such as `[data-theme=\"dark\"]`) splits at that quote under a naive `[^"]*` and leaves a
		// Chinese tail with no separator, which reports **forever** — a false positive no change to
		// the code under test can clear.
		for _, m := range stringLiteralMatchRe.FindAllStringSubmatch(line, -1) {
			lit := m[1]
			if cjkRe.MatchString(lit) && !strings.Contains(lit, " | ") {
				t.Errorf("%s:%d 运行时文案只有中文，必须同行写成 `English | 中文`：%q", f.rel, i+1, lit)
			}
		}
	}
}

// koImportRe 匹配对 ko 库的 import 行（含其子包）。| koImportRe matches an import line of the ko library, subpackages included.
var koImportRe = regexpx.MustCompile(`^\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"github\.com/google/ko(?:/[^"]*)?"\s*$`)

// TestKoStaysAnExternalTool 强制 ko 只作为外部 CLI 使用，不得成为 Go 依赖。
// TestKoStaysAnExternalTool enforces that ko stays an external CLI rather than a Go dependency.
//
// ko 作为库会把 go-containerregistry 等一大串依赖拖进本模块，违反依赖克制；
// 本仓对 docker / betteralign / Tailwind 一律采用「外部工具 + 注入点 + 缺失时优雅降级」的范式，
// ko 必须保持一致。
// Pulling ko in as a library drags go-containerregistry and a long tail of dependencies into this
// module, which the dependency-restraint rule forbids; docker, betteralign and Tailwind are all
// handled as external tools behind an injection point that degrades gracefully when missing, and
// ko has to stay consistent with that.
func TestKoStaysAnExternalTool(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if strings.HasPrefix(f.rel, "repotest/") {
			continue // 守卫自身引用了该字面量 | the guard itself quotes the literal
		}
		for i, l := range f.lines {
			if koImportRe.MatchString(strings.TrimRight(l, " \t")) {
				t.Errorf("%s:%d import 了 ko 的 Go 库；ko 只能作为外部 CLI 调用"+
					"（见 CLAUDE.md「镜像打包」一节）", f.rel, i+1)
			}
		}
	}
}

// TestNoLegacyEnvPrefixInGoSources 校验源码中不残留旧前缀。
// TestNoLegacyEnvPrefixInGoSources checks no Go source keeps the legacy prefix.
func TestNoLegacyEnvPrefixInGoSources(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if strings.HasPrefix(f.rel, "repotest/") {
			continue // 守卫自身包含待搜索字面量 | the guard itself contains the searched literal
		}
		if strings.Contains(f.src, `SetEnvPrefix("YUNGO")`) {
			t.Errorf("%s 仍在使用旧环境变量前缀 YUNGO，应改为 YD", f.rel)
		}
	}
}

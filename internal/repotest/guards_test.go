package repotest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

// TestNoDirectEncodingImports 强制 JSON / Base64 编解码统一走 jsonx / b64x。
// TestNoDirectEncodingImports enforces that JSON/Base64 always go through jsonx/b64x.
//
// 生产代码中禁止 import "encoding/json" 与 "encoding/base64"；唯一例外是
// 封装层自身（jsonx/、b64x/）。测试文件允许把标准库当作独立对照实现（测试预言机）。
// Production code must not import "encoding/json" or "encoding/base64"; the only
// exemptions are the wrapper packages themselves (jsonx/, b64x/). Test files may
// use the standard library as an independent oracle.
func TestNoDirectEncodingImports(t *testing.T) {
	rules := []struct {
		importPath string
		allowedDir string
		hint       string
	}{
		{`"encoding/json"`, "jsonx/", "请改用 github.com/yun-dev/yd/jsonx（Marshal/Unmarshal/NewEncoder/NewDecoder/Valid）"},
		{`"encoding/base64"`, "b64x/", "请改用 github.com/yun-dev/yd/b64x（StdEncoding/URLEncoding/… 及其 Encode/Decode/AppendEncode/AppendDecode）"},
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
	re := regexp.MustCompile(`^\s*(?:[A-Za-z_][A-Za-z0-9_]*\s+)?"` + regexp.QuoteMeta(strings.Trim(quoted, `"`)) + `"\s*$`)
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
		if f.isTest || strings.HasPrefix(f.rel, "server/") {
			continue
		}
		if hasImport(f.lines, `"log"`) {
			t.Errorf("%s 直接 import 了标准库 log；日志必须统一使用 github.com/yun-dev/yd/logx", f.rel)
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
var logCallRe = regexp.MustCompile(`\.(?:Debug|Info|Warn|Error|Fatal)\(\s*"([^"\\]*)"`)

// testingCallRe 匹配 testing 包的断言方法；它们的名字与 logx 级别方法重合，
// 必须排除，否则会把测试断言文案误判为日志文案。
// testingCallRe matches testing assertion helpers; their names collide with logx
// level methods and must be excluded, otherwise assertions are misread as logs.
var testingCallRe = regexp.MustCompile(`\b(?:t|b|tb|ft|st|env)\.(?:Fatal|Fatalf|Error|Errorf)\(`)

// TestLogMessagesAreBilingual 强制日志文案与注释一样中英同步。
// TestLogMessagesAreBilingual enforces the same bilingual rule on log messages.
func TestLogMessagesAreBilingual(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if strings.HasPrefix(f.rel, "internal/repotest/") {
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
		if f.isTest || strings.HasPrefix(f.rel, "errx/") || strings.HasPrefix(f.rel, "internal/repotest/") {
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
		},
		"AGENTS.md": {
			"Co-Authored-By: Eric Shi <shibingli@sina.com>",
			"Test-and-Fix",
			"logx",
			"jsonx", "b64x",
			"OpenAPI",
			"claude", // 必须出现"禁止 claude 相关内容"的约束 | must state the prohibition
		},
		"docs/developer_guidelines.md": {
			"YD_",
			"Co-Authored-By: Eric Shi <shibingli@sina.com>",
			"logx",
			"OpenAPI",
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

// TestNoLegacyEnvPrefixInGoSources 校验源码中不残留旧前缀。
// TestNoLegacyEnvPrefixInGoSources checks no Go source keeps the legacy prefix.
func TestNoLegacyEnvPrefixInGoSources(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if strings.HasPrefix(f.rel, "internal/repotest/") {
			continue // 守卫自身包含待搜索字面量 | the guard itself contains the searched literal
		}
		if strings.Contains(f.src, `SetEnvPrefix("YUNGO")`) {
			t.Errorf("%s 仍在使用旧环境变量前缀 YUNGO，应改为 YD", f.rel)
		}
	}
}

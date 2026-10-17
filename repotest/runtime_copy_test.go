package repotest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/shibingli/yd/core/regexpx"
)

// 本文件是 CLAUDE.md §3.1 的**英中对称**守卫：既有的 checkBilingualCoverage 只抓
// 「有中文但没有 `|`」，抓不到反方向的「只有英文」。这里按 AST 的参数位置取文案，
// 不再靠行内正则猜测，因此既能识别多行调用，也不会把 `logx.Str("addr", v)` 的字段名
// 误当成文案。
// This file is the **English-Chinese symmetry** guard for CLAUDE.md §3.1: the existing
// checkBilingualCoverage only catches "Chinese without a `|`", never the opposite "English only".
// It takes copy by AST argument position instead of guessing with a line regex, so it sees
// multi-line calls and never mistakes a `logx.Str("addr", v)` field name for copy.

const (
	// runtimeCopyErrxPath 是 errx 的导入路径。| runtimeCopyErrxPath is the errx import path.
	runtimeCopyErrxPath = "github.com/shibingli/yd/core/errx"
	// runtimeCopyFmtPath 是 fmt 的导入路径。| runtimeCopyFmtPath is the fmt import path.
	runtimeCopyFmtPath = "fmt"
	// runtimeCopyHTTPPath 是 net/http 的导入路径。| runtimeCopyHTTPPath is the net/http import path.
	runtimeCopyHTTPPath = "net/http"
)

// runtimeCopyLatinWordRe 判定一段文案里是否存在「像英文单词」的连续字母。
// 少于三个字母的片段（动词、`ok`、`id`）不算文案，避免把 `%s`、`%d` 这类纯格式串误报。
// runtimeCopyLatinWordRe decides whether copy contains a run of letters that looks like an English
// word. Runs shorter than three letters (verbs, `ok`, `id`) do not count as copy, which keeps pure
// format strings such as `%s` and `%d` out of the report.
var runtimeCopyLatinWordRe = regexpx.MustCompile(`[A-Za-z]{3,}`)

// TestRuntimeCopyHasBothLanguages 强制「运行时文案不得只有英文」。
// TestRuntimeCopyHasBothLanguages enforces that no runtime copy is English-only.
//
// 规则见 CLAUDE.md §3.1：异常输出、提示、结果响应必须写成 `English | 中文`，英文在前。
// 覆盖范围与既有守卫对齐，只检查**出厂代码**：测试里的 `t.Fatal` / `t.Error` 是开发者诊断，
// 不是程序对用户的输出，既有守卫的注释也明确豁免它们。
// 不检查的部分：
//   - 以 `yd ` 开头的字面量——那是可直接复制执行的命令行或 `yd <cmd>:` 前缀，
//     翻译它会得到一个不能执行的命令；
//   - 不含三个连续字母的字面量——那是格式串或纯数据。
//
// The rule is CLAUDE.md §3.1: error output, prompts and result responses must read
// `English | 中文`, English first. Coverage matches the existing guard and checks **shipped code**
// only: `t.Fatal` / `t.Error` inside tests are developer diagnostics rather than program output,
// which the existing guard's documentation already exempts.
// Not checked:
//   - literals starting with `yd `, which are runnable command lines or `yd <cmd>:` prefixes —
//     translating them would produce a command the user cannot run;
//   - literals without three consecutive letters, which are format strings or pure data.
func TestRuntimeCopyHasBothLanguages(t *testing.T) {
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		if f.isTest {
			continue
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, f.absPath, f.src, 0)
		if err != nil {
			t.Fatalf("%s 解析失败，守卫无法运行: %v", f.rel, err)
		}
		aliases := runtimeCopyImportAliases(parsed)
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			idx, ok := runtimeCopyArgIndex(call, aliases)
			if !ok || idx >= len(call.Args) {
				return true
			}
			msg, ok := runtimeCopyLiteral(call.Args[idx])
			if !ok {
				return true
			}
			checkRuntimeCopy(t, f.rel, fset.Position(call.Args[idx].Pos()).Line, msg)
			return true
		})
	}
}

// runtimeCopyImportAliases 把文件里的导入别名映射到导入路径。
// runtimeCopyImportAliases maps the file's import aliases to import paths.
func runtimeCopyImportAliases(f *ast.File) map[string]string {
	aliases := make(map[string]string, len(f.Imports))
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		aliases[name] = path
	}
	return aliases
}

// runtimeCopyArgIndex 返回调用中「用户可见文案」的参数下标。
// runtimeCopyArgIndex returns the argument index that carries user-visible copy.
//
// 判定顺序很重要：`http.Error` 的方法名也是 Error，必须先按导入路径认领，
// 否则会把 ResponseWriter 当成日志文案。
// The order matters: `http.Error` is also named Error, so the package path must claim it first;
// otherwise the ResponseWriter would be read as log copy.
func runtimeCopyArgIndex(call *ast.CallExpr, aliases map[string]string) (int, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	id, isIdent := sel.X.(*ast.Ident)
	pkgPath := ""
	if isIdent {
		pkgPath = aliases[id.Name]
	}
	switch {
	case pkgPath == runtimeCopyHTTPPath && sel.Sel.Name == "Error":
		return 1, true // http.Error(w, msg, code) | http.Error(w, 文案, 状态码)
	case pkgPath == runtimeCopyErrxPath:
		switch sel.Sel.Name {
		case "New":
			return 0, true
		case "Detail":
			return 1, true
		case "WrapDetail":
			return 2, true
		}
		return 0, false
	case pkgPath == runtimeCopyFmtPath && strings.HasPrefix(sel.Sel.Name, "Fprint"):
		return 1, true // fmt.Fprint*(w, msg, ...) | fmt.Fprint*(w, 文案, ...)
	case sel.Sel.Name == "String" && len(call.Args) == 2:
		return 1, true // c.String(code, msg) | c.String(状态码, 文案)
	case sel.Sel.Name == "Debug" || sel.Sel.Name == "Info" || sel.Sel.Name == "Warn" ||
		sel.Sel.Name == "Error" || sel.Sel.Name == "Fatal":
		return 0, true // logx level calls | logx 各级日志
	}
	return 0, false
}

// runtimeCopyLiteral 取出字面量的文本；简单字符串拼接（`"a" + b`）按可判定的字面量部分拼接。
// runtimeCopyLiteral returns the literal text; a simple concatenation (`"a" + b`) joins the parts
// that are statically decidable.
func runtimeCopyLiteral(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, lok := runtimeCopyLiteral(e.X)
		right, rok := runtimeCopyLiteral(e.Y)
		if !lok || !rok {
			return "", false
		}
		return left + right, true
	}
	return "", false
}

// checkRuntimeCopy 报告一条非双语文案。
// checkRuntimeCopy reports one non-bilingual message.
func checkRuntimeCopy(t *testing.T, rel string, line int, msg string) {
	t.Helper()
	trimmed := strings.TrimSpace(msg)
	if !runtimeCopyLatinWordRe.MatchString(trimmed) {
		return // 纯格式串或纯数据 | a pure format string or pure data
	}
	if strings.HasPrefix(trimmed, "yd ") {
		return // 可执行命令或 `yd <cmd>:` 前缀 | a runnable command or a `yd <cmd>:` prefix
	}
	if left, right, ok := strings.Cut(trimmed, " | "); ok {
		if cjkRe.MatchString(right) && !cjkRe.MatchString(left) {
			return // 已按「英文在前、中文在后」成对 | already paired English-first
		}
		if !cjkRe.MatchString(right) {
			t.Errorf("%s:%d 运行时文案的分隔符右侧没有中文，必须写成 `English | 中文`：%q",
				rel, line, msg)
			return
		}
		t.Errorf("%s:%d 运行时文案为「中文在前」，规则要求「英文在前、中文在后」：%q",
			rel, line, msg)
		return
	}
	t.Errorf("%s:%d 运行时文案只有英文，必须写成 `English | 中文`：%q", rel, line, msg)
}

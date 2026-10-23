package repotest

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// TestRegexStaysBehindWrapper 强制全仓正则只经 ydx/regexpx，禁止直接使用标准库 regexp、
// regexp2 或任何其它正则引擎。
// TestRegexStaysBehindWrapper enforces that every regular expression goes through ydx/regexpx and
// that no other regex engine (the standard library regexp, regexp2, ...) is imported directly.
//
// 统一的理由不是语法糖，而是三件只有包装层能保证的事：按模式选择执行引擎（不含环视/反向引用的
// 模式走不回溯的 Pike VM 线性引擎，需要 .NET 构造的模式走回溯 VM + 强制超时）、超时下的失败关闭
// （回溯 VM 可能被恶意输入触发灾难性回溯），以及所有下标一律是字节偏移（引擎内部以符文工作时，
// 直接当字节用会在中文源码上切错位置）。任何一处绕过包装层，都会让其中一项失效。
// The reason is not syntax sugar but three properties only the wrapper can guarantee: engine
// selection (patterns without lookaround or backreferences use the non-backtracking Pike VM;
// patterns that need .NET constructs use the backtracking VM plus the mandatory timeout),
// fail-closed behavior on timeout (the backtracking VM can be driven into catastrophic
// backtracking by hostile input), and byte-offset indices everywhere (an engine that works in
// runes would otherwise slice CJK source mid-character). Any direct use bypasses one of the three.
//
// regexpx 自身也不再包装第三方引擎；两个执行引擎都在 ydx/regexpx 内部实现。
// regexpx no longer wraps a third-party engine either: both execution engines live inside
// ydx/regexpx.
func TestRegexStaysBehindWrapper(t *testing.T) {
	const wrapperDir = ""
	for _, f := range scanGoFiles(t, repoRoot(t)) {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, f.absPath, f.src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s 解析失败，守卫无法运行: %v", f.rel, err)
		}
		for _, imp := range parsed.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if wrapperDir != "" && strings.HasPrefix(f.rel, wrapperDir) {
				continue
			}
			if p == "regexp" || p == "github.com/dlclark/regexp2" {
				t.Errorf("%s 直接导入了 %q；正则必须经 github.com/shibingli/yd/ydx/regexpx"+
					"（见 CLAUDE.md「正则表达式」一节）", f.rel, p)
			}
		}
	}
}

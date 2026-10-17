package repotest

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// TestRegexStaysBehindWrapper 强制全仓正则只经 core/regexpx，禁止直接使用标准库 regexp 或 regexp2。
// TestRegexStaysBehindWrapper enforces that every regular expression goes through core/regexpx and
// that neither the standard library regexp nor regexp2 is imported directly.
//
// 统一的理由不是语法糖，而是三件只有包装层能保证的事：双引擎选择（能走 RE2 的模式走标准库线性引擎，
// 只有需要 .NET 构造的模式才走 regexp2 + 强制超时）、超时下的失败关闭（regexp2 会回溯，恶意输入可触发
// 灾难性回溯），以及符文下标到字节下标的换算（regexp2 以符文工作，直接当字节用会在中文源码上切错位置）。
// 任何一处绕过包装层，都会让其中一项失效。
// The reason is not syntax sugar but three properties only the wrapper can guarantee: engine
// selection (RE2-compatible patterns use the standard library's linear engine; only patterns that
// need .NET constructs use regexp2 plus the mandatory timeout), fail-closed behavior on timeout
// (regexp2 backtracks, so hostile input can trigger catastrophic backtracking), and the rune-to-byte
// index conversion (regexp2 works in runes, and using them as byte offsets slices CJK source
// mid-character). Any direct use bypasses one of the three.
//
// 包装层是唯一允许导入这两个引擎的地方：标准库 regexp 提供快路径，regexp2 提供兼容路径。
// The wrapper is the only place allowed to import either engine: the standard library provides the
// fast path and regexp2 provides the compatibility path.
func TestRegexStaysBehindWrapper(t *testing.T) {
	const wrapperDir = "core/regexpx/"
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
			if strings.HasPrefix(f.rel, wrapperDir) {
				continue // 包装层同时持有两个引擎 | the wrapper owns both engines
			}
			if p == "regexp" || p == "github.com/dlclark/regexp2" {
				t.Errorf("%s 直接导入了 %q；正则必须经 github.com/shibingli/yd/core/regexpx"+
					"（见 CLAUDE.md「正则表达式」一节）", f.rel, p)
			}
		}
	}
}

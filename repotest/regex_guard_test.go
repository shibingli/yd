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
// 统一的理由不是语法糖，而是三件只有包装层能保证的事：RE2 兼容模式（$、\d \s \w、未知转义与标准库一致）、
// 强制的匹配超时（regexp2 会回溯，恶意输入可触发灾难性回溯），以及符文下标到字节下标的换算
// （regexp2 以符文工作，直接当字节用会在中文源码上切错位置）。任何一处绕过包装层，都会让其中一项失效。
// The reason is not syntax sugar but three properties only the wrapper can guarantee: RE2
// compatibility mode ($, \d \s \w and unknown escapes behave like the standard library), a mandatory
// match timeout (regexp2 backtracks, so hostile input can trigger catastrophic backtracking), and the
// rune-to-byte index conversion (regexp2 works in runes, and using them as byte offsets slices CJK
// source mid-character). Any direct use bypasses one of the three.
//
// 包装层自身是唯一允许导入 regexp2 的地方；它也不得导入标准库 regexp，否则两套语义会同时存在。
// The wrapper is the only place allowed to import regexp2; it must not import the standard library
// regexp either, or both semantic models would coexist.
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
				if p == "regexp" {
					t.Errorf("%s 在包装层内导入了标准库 regexp；包装层只能使用 regexp2", f.rel)
				}
				continue
			}
			if p == "regexp" || p == "github.com/dlclark/regexp2" {
				t.Errorf("%s 直接导入了 %q；正则必须经 github.com/shibingli/yd/core/regexpx"+
					"（见 CLAUDE.md「正则表达式」一节）", f.rel, p)
			}
		}
	}
}

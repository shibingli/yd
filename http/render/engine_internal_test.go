package render

// Internal guards over the compiled program shape. | 对编译程序形状的内部守卫。

import (
	"testing"

	"github.com/shibingli/yd/web"
)

// TestFrameworkTemplatesCompileToPrograms proves every shipped template entry
// uses the fast executor instead of the standard-library fallback. | TestFrameworkTemplatesCompileToPrograms
// 证明每个自带模板入口都使用快速执行器，而不是标准库回退。
func TestFrameworkTemplatesCompileToPrograms(t *testing.T) {
	e, err := New(web.FS(), Options{})
	if err != nil {
		t.Fatalf("New(web.FS()) 失败: %v", err)
	}
	st := e.state.Load()
	if st == nil {
		t.Fatal("engine state is nil | 引擎状态为 nil")
	}
	for name, p := range st.programs {
		if p.err != nil {
			t.Errorf("%s: load error: %v", name, p.err)
			continue
		}
		if p.fallback != nil {
			t.Errorf("%s: fell back to html/template execution | %s 回退到了 html/template 执行", name, name)
		}
	}
}

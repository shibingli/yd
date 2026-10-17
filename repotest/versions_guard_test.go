package repotest

import (
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/versions"
)

// TestScaffoldVersionsMatchSources 强制脚手架的版本常量始终由 go.mod 与资产锁文件派生。
// TestScaffoldVersionsMatchSources enforces that the scaffold's version constants are always derived
// from go.mod and the asset lock.
//
// 版本常量不是手写常量，而是 `yd versions sync` 的产物。没有这条守卫，一次依赖升级很容易只改 go.mod
// 而忘记模板，`yd new` 随后就会继续生成旧版本的项目——这类漂移不会让任何代码编译失败，只会静默产出过时的项目。
// The version constants are not hand-written: they are produced by `yd versions sync`. Without this
// guard a dependency upgrade can easily touch go.mod while forgetting the templates, and `yd new`
// would keep generating stale projects — a drift that breaks no build and only shows up as an
// outdated project later.
func TestScaffoldVersionsMatchSources(t *testing.T) {
	root := repoRoot(t)
	fsys := afero.NewOsFs()
	if err := versions.Check(fsys, root); err != nil {
		inv, loadErr := versions.Load(fsys, root)
		if loadErr == nil {
			t.Logf("sources: go=%s htmx=%s", inv.GoDirective, inv.HtmxVersion)
		}
		t.Fatalf("脚手架版本与真源不一致，请运行 yd versions sync: %v", err)
	}
}

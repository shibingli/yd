// app_script_test.go — behaviour gate for the generated project's shell script.
// app_script_test.go — 生成项目外壳脚本的行为门禁。
//
// The shell script owns two anti-flash paths that no Go test can exercise: preloading a page's declared
// assets before htmx swaps it in, and opening the right-hand drawer only when its fragment has arrived.
// This test renders a real project into memory and runs the Node suite in testdata against the emitted
// `web/static/js/yd-app.js`, so template actions and path mapping are part of the artefact under test.
// Node stays optional: a machine without it skips with a reason instead of failing.
// 外壳脚本拥有两条 Go 测试无法覆盖的防闪路径：在 htmx 换入页面前预载该页声明的资产，以及只在片段到达后
// 打开右侧抽屉。本测试在内存中渲染真实项目，并针对产出的 `web/static/js/yd-app.js` 运行 testdata 里的
// Node 套件，因此模板动作与路径映射都属于被测产物。Node 仍是可选依赖：没有它的机器带原因跳过而不是失败。
package scaffold

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
)

const (
	// appScriptProject is the project name used for the rendered shell script. | appScriptProject 是渲染
	// 外壳脚本所用的项目名。
	appScriptProject = "app-script-app"
	// appScriptSuite is the Node behaviour suite run against the rendered script. | appScriptSuite 是针对
	// 渲染产物运行的 Node 行为套件。
	appScriptSuite = "testdata/yd_app_script.test.mjs"
)

// TestGeneratedAppScriptPreloadsAssetsAndDefersDrawerOpen runs the Node behaviour suite over the shell
// script emitted by Generate. | TestGeneratedAppScriptPreloadsAssetsAndDefersDrawerOpen 针对 Generate
// 产出的外壳脚本运行 Node 行为套件。
//
// A failing suite means one of two user-visible regressions: navigating to a page with #yd-page-assets
// throws before its styles and scripts are preloaded (the incoming page paints unstyled first), or a
// drawer opens empty and fills a moment later. | 套件失败意味着两种用户可见回归之一：进入带
// #yd-page-assets 的页面时在预载样式与脚本之前抛错（新页面先以未换样式的内容绘制），或抽屉先空着打开、
// 随后才被填充。
func TestGeneratedAppScriptPreloadsAssetsAndDefersDrawerOpen(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("skipping: node is not available on PATH (%v); install Node.js to run the shell script suite | 跳过：PATH 中没有 node（%v），请安装 Node.js 以运行外壳脚本套件", err, err)
	}

	fs := afero.NewMemMapFs()
	if _, err := Generate(context.Background(), DefaultOptions(appScriptProject), fs, nil); err != nil {
		t.Fatalf("generate %s: %v | 生成 %s 失败: %v", appScriptProject, err, appScriptProject, err)
	}
	source, err := afero.ReadFile(fs, appScriptProject+"/web/static/js/yd-app.js")
	if err != nil {
		t.Fatalf("read rendered yd-app.js: %v | 读取渲染后的 yd-app.js 失败: %v", err, err)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "yd-app.js")
	if err := os.WriteFile(script, source, 0o600); err != nil {
		t.Fatalf("write %s: %v | 写入 %s 失败: %v", script, err, script, err)
	}

	cmd := exec.Command("node", appScriptSuite, script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", appScriptSuite, err, out)
	}
}

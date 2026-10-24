// progress_javascript_test.go — the request bar's timing and navigation contract.
// progress_javascript_test.go — 请求进度条的计时与导航契约。
//
// The bar is driven entirely by the browser client, so the node suite is the only place
// its anti-flicker delay, trickle ceiling and navigation filtering can be pinned. Running
// that suite from `go test ./web` keeps the shipped script and its tests in one gate.
// 进度条完全由浏览器客户端驱动，因此它的防闪烁延迟、逼近上限与导航过滤只能由 node 套件钉住。
// 从 `go test ./web` 运行该套件，使随包发布的脚本与其测试落在同一道门禁里。
package web

import (
	"os"
	"os/exec"
	"testing"
)

const progressJSTestScript = "web/static/js/yd-progress.test.mjs"

// TestProgressJSSuite runs the node:test suite over the shipped progress bar. |
// TestProgressJSSuite 对随包发布的进度条运行 node:test 套件。
//
// Node is optional, exactly as it is for the authz, guomi and realtime suites: a machine
// without it skips with a reason instead of failing. | Node 与权限、国密、实时套件一样
// 是可选的：没有它的机器会带原因跳过而不是失败。
func TestProgressJSSuite(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("skipping: node is not available on PATH (%v); install Node.js to run the javascript progress suite | 跳过：PATH 中没有 node（%v），请安装 Node.js 以运行 JS 进度条套件", err, err)
	}
	script := ""
	for _, candidate := range []string{progressJSTestScript, "static/js/yd-progress.test.mjs"} {
		if _, err := os.Stat(candidate); err == nil {
			script = candidate
			break
		}
	}
	if script == "" {
		t.Fatalf("missing %s | 缺少 %s", progressJSTestScript, progressJSTestScript)
	}
	out, err := exec.Command("node", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", script, err, out)
	}
}

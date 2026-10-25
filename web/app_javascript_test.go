// app_javascript_test.go — boot smoke test of the framework shell script.
// app_javascript_test.go — 框架外壳脚本的启动冒烟测试。
//
// The shell script boots synchronously at load; a constructor mistake in the theme path once
// aborted boot and silently removed every delegated listener. Running the node suite from
// `go test ./web` keeps that regression behind the same gate as the shipped script.
// 外壳脚本在加载时同步启动；主题路径里的一次构造函数写法错误曾经中止启动，并静默移除全部委托监听。
// 从 `go test ./web` 运行 node 套件，使该回归与随包发布的脚本落在同一道门禁里。
package web

import (
	"os"
	"os/exec"
	"testing"
)

const appJSTestScript = "web/static/js/yd-app.test.mjs"

// TestAppJSSuite runs the boot smoke suite over the shipped shell script. |
// TestAppJSSuite 对随包发布的外壳脚本运行启动冒烟套件。
//
// Node is optional, exactly as it is for the other JavaScript suites: a machine without it skips
// with a reason instead of failing. | Node 与其它 JS 套件一样是可选的：没有它的机器会带原因跳过而不是失败。
func TestAppJSSuite(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("skipping: node is not available on PATH (%v); install Node.js to run the shell boot suite | 跳过：PATH 中没有 node（%v），请安装 Node.js 以运行外壳启动套件", err, err)
	}
	script := ""
	for _, candidate := range []string{appJSTestScript, "static/js/yd-app.test.mjs"} {
		if _, err := os.Stat(candidate); err == nil {
			script = candidate
			break
		}
	}
	if script == "" {
		t.Fatalf("missing %s | 缺少 %s", appJSTestScript, appJSTestScript)
	}
	out, err := exec.Command("node", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", script, err, out)
	}
}

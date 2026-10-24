// realtime_javascript_test.go — the contract between the Go framing and the browser client.
// realtime_javascript_test.go — Go 帧格式与浏览器客户端之间的契约。
//
// The wire format is written twice, once per language, so the two copies must be
// pinned to each other: a change on one side alone must fail a test rather than
// produce a channel that silently drops frames. The node suite also pins the
// transport ladder's decision table.
// 线格式在两种语言里各写了一份，因此两份必须互相钉住：单方面改动必须让测试失败，而不是
// 产生一条静默丢帧的通道。node 套件同时固定传输阶梯的判定表。
package web

import (
	"os"
	"os/exec"
	"testing"
)

const realtimeJSTestScript = "web/static/js/yd-realtime.test.mjs"

// TestRealtimeJSSuite runs the node:test suite over the shipped client. |
// TestRealtimeJSSuite 对随包发布的客户端运行 node:test 套件。
//
// Node is optional, exactly as it is for the authz and guomi suites: a machine
// without it skips with a reason instead of failing. | Node 与权限、国密套件一样
// 是可选的：没有它的机器会带原因跳过而不是失败。
func TestRealtimeJSSuite(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("skipping: node is not available on PATH (%v); install Node.js to run the javascript realtime suite | 跳过：PATH 中没有 node（%v），请安装 Node.js 以运行 JS 实时套件", err, err)
	}
	script := ""
	for _, candidate := range []string{realtimeJSTestScript, "static/js/yd-realtime.test.mjs"} {
		if _, err := os.Stat(candidate); err == nil {
			script = candidate
			break
		}
	}
	if script == "" {
		t.Fatalf("missing %s | 缺少 %s", realtimeJSTestScript, realtimeJSTestScript)
	}
	out, err := exec.Command("node", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", script, err, out)
	}
}

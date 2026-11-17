// message_editor_javascript_test.go — idle warm-up gate for the message editor loader.
// message_editor_javascript_test.go — 消息编辑器加载器空闲预热的行为门禁。
//
// The compact composer used to show a bare textarea first and the EasyMDE toolbar a moment later,
// which users read as a flash when a conversation opened. The loader now fetches the vendor during
// idle time; the node suite asserts that warm-up adds one same-origin script and upgrades nothing.
// 紧凑撰写区过去先显示裸文本域、片刻后才出现 EasyMDE 工具栏，用户打开会话时会看成一次闪动。加载器
// 现在在空闲时取脚本；node 套件断言预热只追加一个同源脚本，且不会升级任何编辑器。
package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const messageEditorJSTestScript = "web/static/js/yd-message-editor.test.mjs"

// TestMessageEditorJSSuite runs the idle warm-up suite over the shipped editor loader. | TestMessageEditorJSSuite
// 对随包发布的消息编辑器加载器运行空闲预热套件。
//
// Node is optional, exactly as for the other JavaScript suites. | Node 与其它 JavaScript 套件一样是可选的。
func TestMessageEditorJSSuite(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("skipping: node is not available on PATH (%v); install Node.js to run the editor loader suite | 跳过：PATH 中没有 node（%v），请安装 Node.js 以运行编辑器加载器套件", err, err)
	}
	script := ""
	for _, candidate := range []string{messageEditorJSTestScript, "static/js/yd-message-editor.test.mjs"} {
		if _, err := os.Stat(candidate); err == nil {
			script = candidate
			break
		}
	}
	if script == "" {
		t.Fatalf("missing %s | 缺少 %s", messageEditorJSTestScript, messageEditorJSTestScript)
	}
	// Run from the package directory so the suite's relative require() resolves to the shipped file.
	// 从包目录运行，套件的相对 require() 才会解析到随包发布的脚本。
	cmd := exec.Command("node", filepath.Base(script))
	cmd.Dir = filepath.Dir(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", script, err, out)
	}
}

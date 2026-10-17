// authz_js_test.go — the contract between the Go registry and its browser side.
// authz_js_test.go — Go 注册表与其浏览器端之间的契约。
//
// Two promises are pinned here. First, the shipped asset is the arbiter of nothing: it reads the id
// the registry names, so a rename on one side alone is a test failure rather than a button that is
// never hidden. Second, the red line of the front-end guard — a hidden button is not a guarded route
// — ships inside the asset itself, because a comment that only lives in a plan document is a comment
// nobody reads while writing a handler.
// 这里钉住两个承诺。第一，随包发布的资产不自行裁决任何事：它读取注册表命名的那个 id，
// 因此单方面改名会表现为测试失败，而不是一个永远不被隐藏的按钮。
// 第二，前端守卫的红线——隐藏的按钮不等于受守卫的路由——随资产一同发布，
// 因为只活在计划文档里的注释，写处理函数的人根本不会读到。
//
// The node suite that proves the behaviour (19 cases over the payload, the four spellings, the
// declarative guards and the after-swap re-binding) is executed from here, so one `go test ./web/`
// covers both sides. | 证明其行为的 node 套件（覆盖载荷、四种写法、声明式守卫与交换后重新绑定的
// 19 个用例）由本文件执行，因此一次 `go test ./web/` 两侧都覆盖。
package web

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/authz"
	"github.com/shibingli/yd/errx"
)

var (
	// errAuthzJSScriptMissing reports a missing JavaScript test entry point. | errAuthzJSScriptMissing 表示缺少 JS 测试入口。
	errAuthzJSScriptMissing = errx.New("web: the javascript authz test script is missing | web：缺少 JS 权限测试脚本")

	// errAuthzJSRunFailed reports a failing Node run. | errAuthzJSRunFailed 表示 Node 运行失败。
	errAuthzJSRunFailed = errx.New("web: the javascript authz test suite failed | web：JS 权限测试套件执行失败")
)

const (
	// authzJSTestScriptName is the Node test entry point relative to the repository root. | authzJSTestScriptName 是相对仓库根目录的 Node 测试入口。
	authzJSTestScriptName = "web/static/js/yd-authz.test.mjs"

	// authzJSAssetName is the shipped script, relative to the embedded tree root. | authzJSAssetName 是随包发布的脚本，相对于内嵌树根目录。
	authzJSAssetName = "static/js/yd-authz.js"

	// authzJSRedLine is the sentence the asset must keep publishing. It is spelled out here rather
	// than derived, because the point is exactly that a human wrote it down for other humans.
	// authzJSRedLine 是该资产必须持续发布的句子。它在这里逐字写出而不是派生得到，
	// 因为重点恰恰在于：这是人写给人的一句话。
	authzJSRedLine = "NOT A SECURITY BOUNDARY"
)

// TestAuthzJSSuite runs the node:test suite over the very file the browser receives. | TestAuthzJSSuite 对浏览器收到的那个文件运行 node:test 套件。
//
// Node is optional, exactly as it is for the guomi vectors: a machine without it skips with a
// reason instead of failing, and nothing else in the suite depends on it. | Node 与国密向量一样是可选的：
// 没有它的机器会带原因跳过而不是失败，套件中的其它测试都不依赖它。
func TestAuthzJSSuite(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("skipping: node is not available on PATH (%v); install Node.js to run the javascript authz suite | 跳过：PATH 中没有 node（%v），请安装 Node.js 以运行 JS 权限套件", err, err)
	}

	script := ""
	for _, candidate := range []string{authzJSTestScriptName, "static/js/yd-authz.test.mjs"} {
		if _, err := os.Stat(candidate); err == nil {
			script = candidate
			break
		}
	}
	if script == "" {
		t.Fatalf("%v: %s | 缺少 %s", errAuthzJSScriptMissing, authzJSTestScriptName, authzJSTestScriptName)
	}

	// The module is loaded relative to the script through import.meta.url, so the working directory
	// does not matter and the suite runs the same from the repository root and from web/. | 模块经
	// import.meta.url 相对脚本加载，因此工作目录无关紧要，从仓库根目录与从 web/ 运行结果一致。
	out, err := exec.Command("node", script).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: node %s: %v\n%s", errAuthzJSRunFailed, script, err, out)
	}
	t.Logf("node %s output | 输出:\n%s", script, out)
}

// TestAuthzJSAssetReadsTheRegistry proves the two sides name the same things: the payload id comes
// from the registry rather than from a literal that happens to match today, the public surface the
// templates and the CLI documentation refer to is actually published, and the red line ships with
// the asset.
// TestAuthzJSAssetReadsTheRegistry 证明两侧命名的是同一批东西：载荷 id 来自注册表，
// 而不是一个恰好对得上的字面量；模板与 CLI 文档所指的接口确实被发布；
// 红线也随资产一同发布。
func TestAuthzJSAssetReadsTheRegistry(t *testing.T) {
	raw, err := afero.ReadFile(FS(), authzJSAssetName)
	if err != nil {
		t.Fatalf("reading embedded %s failed: %v | 读取内嵌的 %s 失败：%v", authzJSAssetName, err, authzJSAssetName, err)
	}
	source := string(raw)
	if len(source) < 2048 {
		t.Fatalf("%s is only %d bytes, the guard would be near vacuous | %s 只有 %d 字节，守卫几乎形同虚设", authzJSAssetName, len(source), authzJSAssetName, len(source))
	}

	// Zero CDN, zero build step: the asset is shipped inside the binary and must not reach out.
	// 零 CDN、零构建：该资产随二进制分发，不得向外发起请求。
	for _, ref := range []string{"http://", "https://", "//cdn.", "unpkg.com", "cdnjs.cloudflare.com"} {
		if strings.Contains(source, ref) {
			t.Errorf("%s references %q; the asset must stay local | %s 引用了 %q：该资产必须保持本地", authzJSAssetName, ref, authzJSAssetName, ref)
		}
	}

	// The id the script reads is the one the registry names, whichever way it is spelled in Go.
	// 脚本读取的 id 就是注册表命名的那个，无论它在 Go 里写作什么形式。
	if want := "PAYLOAD_ID = '" + authz.AuthzScriptID + "'"; !strings.Contains(source, want) {
		t.Errorf("%s does not declare %s | %s 没有声明 %s", authzJSAssetName, want, authzJSAssetName, want)
	}

	// The documented surface is the shipped surface. | 文档写定的接口就是实际发布的接口。
	for _, member := range []string{"PERM:", "ROLE:", "can:", "hasRole:", "perms:", "roles:", "subject:", "bind:"} {
		if !strings.Contains(source, member) {
			t.Errorf("%s does not publish %q | %s 没有发布 %q", authzJSAssetName, member, authzJSAssetName, member)
		}
	}

	// The red line, in the file that a front-end author actually opens. | 红线写在前端作者真正会打开的那个文件里。
	if !strings.Contains(source, authzJSRedLine) {
		t.Errorf("%s dropped %q: the front-end guard must keep saying it is not a security boundary | %s 丢掉了 %q：前端守卫必须持续声明它不是安全边界", authzJSAssetName, authzJSRedLine, authzJSAssetName, authzJSRedLine)
	}
	for _, reminder := range []string{"authz.Require", "authz.Allow"} {
		if !strings.Contains(source, reminder) {
			t.Errorf("%s no longer names %s as the real guard | %s 不再指明 %s 才是真正的守卫", authzJSAssetName, reminder, authzJSAssetName, reminder)
		}
	}
}

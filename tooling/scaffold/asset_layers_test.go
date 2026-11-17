// asset_layers_test.go — the generated project's main/user asset split.
// asset_layers_test.go — 生成项目的主资源与用户资源分层。
//
// The main assets (`yd-app.js`, `app.css`, `yd-main.css`, `components.css`, `ui.css`) belong to the
// scaffold and are replaced by `yd sync`; the user assets (`yd-app.user.js`, `user.css`) are created
// once and never overwritten. This suite pins the file set and the load/import order that makes the
// split work: main first, user last.
// 主资源（`yd-app.js`、`app.css`、`yd-main.css`、`components.css`、`ui.css`）归脚手架所有，
// `yd sync` 可以整份替换；用户资源（`yd-app.user.js`、`user.css`）只创建一次，永不被覆盖。本套件
// 固定文件集合与让分层成立的加载/导入顺序：主资源在前、用户资源在后。
package scaffold_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibingli/yd/tooling/build"
	"github.com/shibingli/yd/tooling/scaffold"
)

// TestGeneratedProjectSplitsMainAndUserAssets asserts the two layers exist and that the user layer is
// loaded after the main one. | TestGeneratedProjectSplitsMainAndUserAssets 断言两层文件都存在，且用户层
// 排在主资源之后加载。
func TestGeneratedProjectSplitsMainAndUserAssets(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("layered-assets-app"))

	for _, rel := range []string{
		"web/static/js/yd-app.user.js",
		"web/static/css/user.css",
		"web/static/css/yd-main.css",
	} {
		if !pr.exists(rel) {
			t.Errorf("generated project lacks the layered asset %q | 生成项目缺少分层资源 %q", rel, rel)
		}
	}

	app := pr.read("web/static/css/app.css")
	imports := []string{
		`@import "tailwindcss"`,
		`@import "./yd-main.css"`,
		`@import "./user.css"`,
	}
	last := -1
	for _, want := range imports {
		at := strings.Index(app, want)
		if at < 0 {
			t.Errorf("app.css is missing %q; the layer order breaks | app.css 缺少 %q；分层顺序被破坏", want, want)
			continue
		}
		if at <= last {
			t.Errorf("app.css imports %[1]q out of order; main must come before user | app.css 中 %[1]q 的顺序不正确；主资源必须在用户资源之前", want)
		}
		last = at
	}

	main := pr.read("web/static/css/yd-main.css")
	for _, want := range []string{`@import "./components.css"`, `@import "./ui.css"`, "@source", "@theme"} {
		if !strings.Contains(main, want) {
			t.Errorf("yd-main.css is missing %q; the main layer moved or lost a directive | yd-main.css 缺少 %q；主层被移动或丢了指令", want, want)
		}
	}

	base := pr.read("web/templates/partials/base.html")
	mainScript := strings.Index(base, `asset "/static/js/yd-app.js"`)
	userScript := strings.Index(base, `asset "/static/js/yd-app.user.js"`)
	if mainScript < 0 || userScript < 0 || userScript < mainScript {
		t.Errorf("base.html must load yd-app.js before yd-app.user.js (main=%d user=%d) | base.html 必须先加载 yd-app.js 再加载 yd-app.user.js（main=%d user=%d）", mainScript, userScript, mainScript, userScript)
	}

	bundle := pr.read("web/bundle_generated.go")
	mainBundle := strings.Index(bundle, `"/static/js/yd-app.js"`)
	userBundle := strings.Index(bundle, `"/static/js/yd-app.user.js"`)
	if mainBundle < 0 || userBundle < 0 || userBundle < mainBundle {
		t.Errorf("bundle_generated.go must append yd-app.user.js after yd-app.js (main=%d user=%d) | bundle_generated.go 必须把 yd-app.user.js 排在 yd-app.js 之后（main=%d user=%d）", mainBundle, userBundle, mainBundle, userBundle)
	}
}

// TestUserOwnedAssetPaths pins which files scream "mine" to the sync: only the two user layer files.
// | TestUserOwnedAssetPaths 固定哪些文件属于用户：只有两个用户层文件。
func TestUserOwnedAssetPaths(t *testing.T) {
	owned := []string{
		"web/static/js/yd-app.user.js",
		"web/static/css/user.css",
	}
	for _, rel := range owned {
		if !scaffold.IsUserOwned(rel) {
			t.Errorf("%q must be user-owned | %q 必须属于用户资源", rel, rel)
		}
	}
	for _, rel := range []string{
		"web/static/js/yd-app.js",
		"web/static/css/app.css",
		"web/static/css/yd-main.css",
		"web/static/css/components.css",
		"web/static/css/ui.css",
	} {
		if scaffold.IsUserOwned(rel) {
			t.Errorf("%q is a main resource and must never be treated as user-owned | %q 是主资源，绝不能按用户资源处理", rel, rel)
		}
		if !scaffold.IsMainAsset(rel) {
			t.Errorf("%q must be registered as a main asset | %q 必须登记为主资源", rel, rel)
		}
	}
	if want := "/" + strings.TrimPrefix(scaffold.UserScriptPath, "web/"); scaffold.UserScriptURL != want {
		t.Errorf("UserScriptURL = %q, want %q derived from UserScriptPath | UserScriptURL = %q，期望由 UserScriptPath 推导出的 %q", scaffold.UserScriptURL, want, scaffold.UserScriptURL, want)
	}
}

// TestGeneratedLayeredStylesCompile runs the real Tailwind compiler over the generated entry so a
// broken import chain or a comment that closes early cannot ship. It is skipped when the optional
// tailwindcss binary is absent, exactly like the other tool-dependent suites.
// TestGeneratedLayeredStylesCompile 用真实 Tailwind 编译生成项目的样式入口，避免错误的导入链或提前
// 结束的注释进入产物。缺少可选的 tailwindcss 二进制时与其它工具相关套件一样跳过。
func TestGeneratedLayeredStylesCompile(t *testing.T) {
	bin, err := build.ResolveTailwindBin(repoRoot(t), "")
	if err != nil {
		t.Skipf("skipping: tailwindcss is not available (%v) | 跳过：没有可用的 tailwindcss（%v）", err, err)
	}

	for _, tc := range []struct {
		name     string
		tailwind bool
		want     []string
	}{
		{
			name:     "tailwind",
			tailwind: true,
			want:     []string{"--color-brand-600", ".yd-shell", "background-color: var(--color-surface)"},
		},
		{
			name:     "no-tailwind",
			tailwind: false,
			want:     []string{"--color-surface", ".yd-skeleton"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := scaffold.DefaultOptions("layered-css-app")
			opts.Tailwind = tc.tailwind
			pr, res := generateMem(t, opts)
			dir := t.TempDir()
			prefix := pr.name + "/"
			for _, file := range res.Files {
				rel := strings.TrimPrefix(file, prefix)
				if !strings.HasPrefix(rel, "web/") {
					continue
				}
				dst := filepath.Join(dir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					t.Fatalf("create %s: %v | 创建 %s 失败: %v", filepath.Dir(dst), err, filepath.Dir(dst), err)
				}
				if err := os.WriteFile(dst, []byte(pr.read(rel)), 0o644); err != nil {
					t.Fatalf("write %s: %v | 写入 %s 失败: %v", dst, err, dst, err)
				}
			}
			// A user-layer probe proves user.css is imported after the main layer, not merely present on disk.
			// | 用户层探针证明 user.css 确实在主层之后被导入，而不只是存在于磁盘上。
			userCSSPath := filepath.Join(dir, "web", "static", "css", "user.css")
			userCSS, err := os.ReadFile(userCSSPath)
			if err != nil {
				t.Fatalf("read user.css: %v | 读取 user.css 失败: %v", err, err)
			}
			probe := []byte("\n.yd-user-layer-probe { color: rgb(1 2 3); }\n")
			if err := os.WriteFile(userCSSPath, append(userCSS, probe...), 0o644); err != nil {
				t.Fatalf("append user.css probe: %v | 追加 user.css 探针失败: %v", err, err)
			}

			// @source 会扫描内部 Go 源码；空目录足以让通配符不报错，本用例只验证样式入口。
			// @source scans internal Go sources; an empty directory keeps the glob valid, and this case only
			// verifies the stylesheet entry.
			if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
				t.Fatalf("create internal: %v | 创建 internal 失败: %v", err, err)
			}

			out := filepath.Join(dir, "yd.css")
			cmd := exec.Command(bin,
				"-i", filepath.Join(dir, "web", "static", "css", "app.css"),
				"-o", out)
			cmd.Dir = dir
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tailwindcss failed: %v\n%s | Tailwind 编译失败: %v\n%s", err, output, err, output)
			}
			css, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read compiled css: %v | 读取编译产物失败: %v", err, err)
			}
			body := string(css)
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("compiled stylesheet lacks %q; the layered imports did not reach Tailwind | 编译产物缺少 %q；分层导入没有进入 Tailwind", want, want)
				}
			}
			probeAt := strings.LastIndex(body, ".yd-user-layer-probe")
			if probeAt < 0 {
				t.Fatalf("compiled stylesheet lacks the user.css probe; the user layer was not imported | 编译产物缺少 user.css 探针；用户层没有被导入")
			}
			mainAnchor := ".yd-shell"
			if !tc.tailwind {
				mainAnchor = ".yd-skeleton"
			}
			if mainAt := strings.LastIndex(body, mainAnchor); mainAt >= 0 && probeAt < mainAt {
				t.Errorf("user.css compiles before %s; user rules must come last | user.css 编译在 %s 之前；用户规则必须排在最后", mainAnchor, mainAnchor)
			}
		})
	}
}

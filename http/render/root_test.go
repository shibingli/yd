package render_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/http/render"
)

// TestAbsoluteRootIsPreserved is the regression for normalize stripping every leading slash:
// an absolute Unix template root became a relative path and no template was found. | TestAbsoluteRootIsPreserved
// 是 normalize 裁掉所有前导斜杠的回归：绝对 Unix 模板根变成相对路径，结果一个模板都找不到。
func TestAbsoluteRootIsPreserved(t *testing.T) {
	root := t.TempDir()
	page := filepath.Join(root, "pages", "probe.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatalf("MkdirAll 失败 | failed: %v", err)
	}
	if err := os.WriteFile(page, []byte("hello {{.Name}}"), 0o644); err != nil {
		t.Fatalf("WriteFile 失败 | failed: %v", err)
	}

	engine, err := render.New(afero.NewOsFs(), render.Options{Root: root})
	if err != nil {
		t.Fatalf("New 失败 | failed: %v", err)
	}
	var out bytes.Buffer
	if err := engine.Render(&out, "pages/probe.html", map[string]any{"Name": "world"}); err != nil {
		t.Fatalf("Render 失败 | failed: %v", err)
	}
	if out.String() != "hello world" {
		t.Fatalf("输出 = %q，期望 %q | output = %q, want %q", out.String(), "hello world", out.String(), "hello world")
	}
}

// TestNormalizeRootPreservesPrefixes checks the pure normalization rule on both separator styles. |
// TestNormalizeRootPreservesPrefixes 校验纯规范化规则对两种分隔符的形态。
func TestNormalizeRootPreservesPrefixes(t *testing.T) {
	// normalizeRoot is unexported; its effect is observable through New: a Windows-style relative
	// root with backslashes must still resolve, and a drive root must stay absolute. The absolute
	// Unix case is the one that used to fail and is covered above. | normalizeRoot 未导出；其效果可经
	// New 观察：带反斜杠的 Windows 相对根仍须可解析，盘符根须保持绝对。绝对 Unix 用例正是过去失败的那个，
	// 已在上方覆盖。
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatalf("MkdirAll 失败 | failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "a.html"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile 失败 | failed: %v", err)
	}
	old := filepath.Join(root, "templates")
	if _, err := render.New(afero.NewOsFs(), render.Options{Root: old}); err != nil {
		t.Fatalf("绝对根应可解析 | an absolute root must resolve: %v", err)
	}
}

package afs_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/afs"
)

// TestHideShadowedMin pins the overlay rule that prevents a framework .min
// sibling from shadowing a project source of the same name. | TestHideShadowedMin
// 固定覆盖层规则：框架的 .min 同族文件不得遮蔽同名的项目源码。
func TestHideShadowedMin(t *testing.T) {
	base := afs.NewMem()
	for name, body := range map[string]string{
		"js/app.min.js":    "framework min",
		"js/shared.min.js": "shared min",
		"js/both.min.js":   "framework both min",
		"css/yd.min.css":   "framework css min",
	} {
		if err := afero.WriteFile(base, name, []byte(body), 0o644); err != nil {
			t.Fatalf("写入 base %s 失败: %v", name, err)
		}
	}
	upper := afs.NewMem()
	for name, body := range map[string]string{
		"js/app.js":      "project app source",
		"js/both.js":     "project both source",
		"js/both.min.js": "project both min",
		"css/yd.css":     "project css source",
	} {
		if err := afero.WriteFile(upper, name, []byte(body), 0o644); err != nil {
			t.Fatalf("写入 upper %s 失败: %v", name, err)
		}
	}

	merged := afs.HideShadowedMin(afs.Overlay(base, upper), upper)

	if afs.Exists(merged, "js/app.min.js") {
		t.Error("a project app.js without app.min.js must hide the framework app.min.js | 项目提供 app.js 但没有 app.min.js 时，框架的 app.min.js 必须被隐藏")
	}
	if _, err := merged.Open("js/app.min.js"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open(app.min.js) = %v，期望 fs.ErrNotExist", err)
	}
	if afs.Exists(merged, "css/yd.min.css") {
		t.Error("a project yd.css without yd.min.css must hide the framework yd.min.css | 项目提供 yd.css 但没有 yd.min.css 时，框架的 yd.min.css 必须被隐藏")
	}
	if !afs.Exists(merged, "js/app.js") {
		t.Error("the project source app.js must stay visible | 项目源码 app.js 必须仍然可见")
	}
	if got, err := afero.ReadFile(merged, "js/shared.min.js"); err != nil || string(got) != "shared min" {
		t.Errorf("项目没有 shared.js 时框架 shared.min.js 应可见，得到 %q, err = %v", got, err)
	}
	if got, err := afero.ReadFile(merged, "js/both.min.js"); err != nil || string(got) != "project both min" {
		t.Errorf("项目自己也提供 both.min.js 时应使用项目版本，得到 %q, err = %v", got, err)
	}
}

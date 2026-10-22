package scaffold_test

import (
	"bytes"
	"go/format"
	"os"
	"strings"
	"testing"

	"github.com/shibingli/yd/tooling/icons"
	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/spf13/afero"
)

// TestGeneratedGoFilesAreGofmtClean 断言脚手架产出的每个 Go 文件都等于 gofmt 的结果。
// TestGeneratedGoFilesAreGofmtClean asserts every Go file produced by the scaffold equals its gofmt output.
//
// 生成项目的 CLAUDE.md 要求 gofmt 零告警；如果模板本身没有经过 format.Source，用户建完项目的第一步
// 就会失败。这里逐文件比对，而不是只检查能否解析。| The generated project's CLAUDE.md requires a clean
// gofmt; without format.Source in the scaffold, the user's first check fails right after `yd new`. The test
// compares every file byte for byte instead of merely checking that it parses.
func TestGeneratedGoFilesAreGofmtClean(t *testing.T) {
	o := scaffold.DefaultOptions("fmt-app")
	pr, _ := generateMem(t, o)

	var files []string
	err := afero.Walk(pr.fs, o.Name, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk generated project failed | 遍历生成项目失败: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("generated project has no Go files | 生成项目没有任何 Go 文件")
	}

	for _, p := range files {
		raw, err := afero.ReadFile(pr.fs, p)
		if err != nil {
			t.Fatalf("read %[1]s failed | 读取 %[1]s 失败: %v", p, err)
		}
		formatted, err := format.Source(raw)
		if err != nil {
			t.Errorf("gofmt cannot parse %[1]s | %[1]s 无法被 gofmt 解析: %v", p, err)
			continue
		}
		if !bytes.Equal(raw, formatted) {
			t.Errorf("%s 不是 gofmt-clean | %s is not gofmt-clean", p, p)
		}
	}
}

// TestGeneratedProjectShipsIconSprite 断言生成项目自带与模板引用一致的图标 sprite。
// TestGeneratedProjectShipsIconSprite asserts the generated project ships an icon sprite matching its template references.
//
// 新项目应当直接通过 `yd icons check`；否则每条新项目的第一条 CI 命令就会失败。| A fresh project must pass
// `yd icons check` out of the box; otherwise the first CI command in every new project fails.
func TestGeneratedProjectShipsIconSprite(t *testing.T) {
	o := scaffold.DefaultOptions("icon-app")
	pr, _ := generateMem(t, o)
	if !pr.exists("web/static/img/icons.svg") {
		t.Fatalf("generated project is missing the icon sprite | 生成项目缺少图标 sprite: %s", pr.at("web/static/img/icons.svg"))
	}

	res, err := icons.Check(icons.Options{
		FS:     pr.fs,
		Roots:  []string{pr.at("web/templates")},
		Output: pr.at("web/static/img/icons.svg"),
	})
	if err != nil {
		t.Fatalf("yd icons check failed on the generated project | yd icons check 在生成项目上失败: %v", err)
	}
	if len(res.Names) == 0 {
		t.Fatal("the icon sprite is empty; generated templates must reference at least one icon | 图标 sprite 为空，生成模板应当至少引用一个图标")
	}
}

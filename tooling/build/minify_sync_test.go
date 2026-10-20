package build

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestFrameworkMinAssetsAreUpToDate is the drift guard for the committed
// framework assets: a source change without regenerating its .min sibling fails
// here. | TestFrameworkMinAssetsAreUpToDate 是已提交框架资产的漂移守卫：
// 改了源码却没有重新生成 .min 同族文件会在这里失败。
//
// Both halves skip when the corresponding optional tool is missing; the skip
// names the tool and the installation command so a CI machine can be fixed. | 两个
// 子测试在对应可选工具缺失时跳过，并在跳过原因里给出工具名与安装命令，便于修复 CI 机器。
func TestFrameworkMinAssetsAreUpToDate(t *testing.T) {
	root := filepath.Join("..", "..")

	t.Run("javascript and svg", func(t *testing.T) {
		if _, err := ResolveMinifyBin(root, ""); err != nil {
			t.Skipf("minify 不可用，跳过 JS/SVG 同步校验（%s）: %v", MinifyInstallHint, err)
		}
		res, err := BuildMinify(context.Background(), root, MinifyOptions{
			Root: DefaultMinifyRoot, CheckOnly: true,
		}, testLogger())
		if err != nil {
			t.Fatalf("BuildMinify(--check) 失败: %v", err)
		}
		if len(res.Missing) > 0 || len(res.Mismatched) > 0 || len(res.Stale) > 0 {
			t.Errorf("框架压缩 JS/SVG 已漂移，运行 `yd assets minify`: missing=%v mismatched=%v stale=%v",
				res.Missing, res.Mismatched, res.Stale)
		}
	})

	t.Run("css", func(t *testing.T) {
		bin, err := ResolveTailwindBin(root, "")
		if err != nil {
			t.Skipf("tailwindcss 不可用，跳过 CSS 同步校验: %v", err)
		}
		tmp := filepath.Join(t.TempDir(), "yd.min.css")
		if _, err := BuildTailwind(context.Background(), root, TailwindOptions{
			Enabled: true, Minify: true, MinifyOnly: true,
			Bin: bin, MinOutput: tmp,
		}, testLogger()); err != nil {
			t.Fatalf("Tailwind --minify 失败: %v", err)
		}
		want, err := os.ReadFile(filepath.Join(root, "web/static/css/yd.min.css"))
		if err != nil {
			t.Fatalf("缺少已提交的 yd.min.css: %v", err)
		}
		got, err := os.ReadFile(tmp)
		if err != nil {
			t.Fatalf("读取临时产物失败: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Error("yd.min.css is out of date, run `yd assets minify` | yd.min.css 已漂移，运行 `yd assets minify` 重新生成")
		}
	})
}

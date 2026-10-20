package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestResolveMinifyBinOrder pins the documented resolution order: explicit flag,
// then YD_MINIFY_BIN, then .yd/tools, then PATH. | TestResolveMinifyBinOrder 固定
// 文档化的解析顺序：显式标志 > YD_MINIFY_BIN > .yd/tools > PATH。
func TestResolveMinifyBinOrder(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, ".yd", "tools", minifyBinName())
	writeFile(t, local, "#!/bin/sh\n")
	envBin := filepath.Join(t.TempDir(), minifyBinName())
	writeFile(t, envBin, "#!/bin/sh\n")
	flagBin := filepath.Join(t.TempDir(), minifyBinName())
	writeFile(t, flagBin, "#!/bin/sh\n")

	t.Run("explicit flag wins", func(t *testing.T) {
		t.Setenv(minifyEnvBin, envBin)
		got, err := ResolveMinifyBin(dir, flagBin)
		if err != nil {
			t.Fatalf("ResolveMinifyBin 失败: %v", err)
		}
		if got != flagBin {
			t.Fatalf("解析结果 = %q, 期望 %q", got, flagBin)
		}
	})

	t.Run("env wins over local tools", func(t *testing.T) {
		t.Setenv(minifyEnvBin, envBin)
		got, err := ResolveMinifyBin(dir, "")
		if err != nil {
			t.Fatalf("ResolveMinifyBin 失败: %v", err)
		}
		if got != envBin {
			t.Fatalf("解析结果 = %q, 期望 %q", got, envBin)
		}
	})

	t.Run("local tools dir", func(t *testing.T) {
		t.Setenv(minifyEnvBin, "")
		got, err := ResolveMinifyBin(dir, "")
		if err != nil {
			t.Fatalf("ResolveMinifyBin 失败: %v", err)
		}
		if got != local {
			t.Fatalf("解析结果 = %q, 期望 %q", got, local)
		}
	})
}

// TestResolveMinifyBinMissing asserts the sentinel returned when nothing is found. | TestResolveMinifyBinMissing
// 断言找不到可执行文件时返回的哨兵错误。
func TestResolveMinifyBinMissing(t *testing.T) {
	t.Setenv(minifyEnvBin, "")
	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveMinifyBin(t.TempDir(), ""); !errors.Is(err, ErrMinifyNotFound) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrMinifyNotFound)", err)
	}
}

// TestBuildMinifyWritesMinSiblings pins the per-file conversion and the vendor
// skip: app.js becomes app.min.js while vendor assets are never touched. | TestBuildMinifyWritesMinSiblings
// 固定逐文件转换与 vendor 跳过：app.js 变成 app.min.js，vendor 资产一律不动。
func TestBuildMinifyWritesMinSiblings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "web", "static", "js", "app.js"), "console.log(1)\n")
	writeFile(t, filepath.Join(dir, "web", "static", "js", "vendor", "htmx.min.js"), "/*min*/\n")
	bin := filepath.Join(dir, minifyBinName())
	writeFile(t, bin, "stand-in for the minify executable\n")

	fake := &fakeExec{onCall: func(_ string, args []string) {
		out, ok := flagValue(args, "-o")
		if !ok {
			return
		}
		_ = os.WriteFile(out, []byte("console.log(1)"), 0o644)
	}}
	res, err := buildMinifyWith(context.Background(), dir, MinifyOptions{
		Bin: bin, Root: DefaultMinifyRoot,
	}, testLogger(), fake.exec)
	if err != nil {
		t.Fatalf("buildMinifyWith 失败: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("minify 调用次数 = %d, 期望 1（vendor 应跳过）: %v", len(fake.calls), fake.calls)
	}
	argv := fake.calls[0]
	wantOut := filepath.Join(dir, "web", "static", "js", "app.min.js")
	wantIn := filepath.Join(dir, "web", "static", "js", "app.js")
	if got, _ := flagValue(argv, "-o"); got != wantOut {
		t.Errorf("-o = %q, 期望 %q", got, wantOut)
	}
	if got := argv[len(argv)-1]; got != wantIn {
		t.Errorf("输入 = %q, 期望 %q", got, wantIn)
	}
	if res.Files != 1 || res.Bytes != int64(len("console.log(1)")) {
		t.Errorf("结果 = %+v, 期望 Files=1 Bytes=%d", res, len("console.log(1)"))
	}
	if _, err := os.Stat(wantOut); err != nil {
		t.Errorf("缺少 %s: %v", wantOut, err)
	}
}

// TestBuildMinifyMinifiesSVG pins the second asset kind: an .svg source produces .min.svg and is
// counted separately, while CSS remains Tailwind's responsibility. | TestBuildMinifyMinifiesSVG
// 固定第二种资产：.svg 源码产出 .min.svg 并单独计数，CSS 仍由 Tailwind 负责。
func TestBuildMinifyMinifiesSVG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "web", "static", "img", "icons.svg")
	writeFile(t, src, "<svg>  <symbol/></svg>\n")
	writeFile(t, filepath.Join(dir, "web", "static", "css", "app.css"), "@import \"tailwindcss\";\n")
	bin := filepath.Join(dir, minifyBinName())
	writeFile(t, bin, "stand-in\n")

	fake := &fakeExec{onCall: func(_ string, args []string) {
		out, ok := flagValue(args, "-o")
		if !ok {
			return
		}
		_ = os.WriteFile(out, []byte("<svg><symbol/></svg>"), 0o644)
	}}
	res, err := buildMinifyWith(context.Background(), dir, MinifyOptions{
		Bin: bin, Root: DefaultMinifyRoot,
	}, testLogger(), fake.exec)
	if err != nil {
		t.Fatalf("buildMinifyWith 失败: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("minify 调用次数 = %d, 期望 1（CSS 不应进入本阶段）: %v", len(fake.calls), fake.calls)
	}
	out := filepath.Join(dir, "web", "static", "img", "icons.min.svg")
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("缺少 %s: %v", out, err)
	}
	if res.Files != 1 || res.SVGFiles != 1 {
		t.Errorf("结果 = %+v, 期望 Files=1 SVGFiles=1", res)
	}
}

// TestBuildMinifyCleansStaleOutput pins the cleanup of a manifest-tracked
// .min.js whose source disappeared, while a live pair survives. | TestBuildMinifyCleansStaleOutput
// 固定清理清单中源文件已消失的 .min.js，同时保留仍然有效的成对文件。
func TestBuildMinifyCleansStaleOutput(t *testing.T) {
	dir := t.TempDir()
	jsDir := filepath.Join(dir, "web", "static", "js")
	writeFile(t, filepath.Join(jsDir, "app.js"), "console.log(1)\n")
	writeFile(t, filepath.Join(jsDir, "stale.js"), "console.log(2)\n")
	bin := filepath.Join(dir, minifyBinName())
	writeFile(t, bin, "stand-in\n")

	fake := &fakeExec{onCall: func(_ string, args []string) {
		out, _ := flagValue(args, "-o")
		_ = os.WriteFile(out, []byte("console.log(1)"), 0o644)
	}}
	if _, err := buildMinifyWith(context.Background(), dir, MinifyOptions{
		Bin: bin, Root: DefaultMinifyRoot,
	}, testLogger(), fake.exec); err != nil {
		t.Fatalf("first buildMinifyWith 失败: %v", err)
	}
	if err := os.Remove(filepath.Join(jsDir, "stale.js")); err != nil {
		t.Fatalf("删除 stale.js 失败: %v", err)
	}
	if _, err := buildMinifyWith(context.Background(), dir, MinifyOptions{
		Bin: bin, Root: DefaultMinifyRoot,
	}, testLogger(), fake.exec); err != nil {
		t.Fatalf("second buildMinifyWith 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jsDir, "app.min.js")); err != nil {
		t.Errorf("app.min.js 应保留: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jsDir, "stale.min.js")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale.min.js 应被删除，实际 err = %v", err)
	}
}

// TestBuildMinifyPreservesUnmanagedMinFiles proves cleanup never deletes a
// hand-vendored .min.js that this stage did not generate. | TestBuildMinifyPreservesUnmanagedMinFiles
// 证明清理绝不删除本阶段未生成的手工 .min.js。
func TestBuildMinifyPreservesUnmanagedMinFiles(t *testing.T) {
	dir := t.TempDir()
	jsDir := filepath.Join(dir, "web", "static", "js")
	writeFile(t, filepath.Join(jsDir, "app.js"), "console.log(1)\n")
	writeFile(t, filepath.Join(jsDir, "chart.min.js"), "hand-vendored")
	bin := filepath.Join(dir, minifyBinName())
	writeFile(t, bin, "stand-in\n")

	fake := &fakeExec{onCall: func(_ string, args []string) {
		out, _ := flagValue(args, "-o")
		_ = os.WriteFile(out, []byte("console.log(1)"), 0o644)
	}}
	if _, err := buildMinifyWith(context.Background(), dir, MinifyOptions{
		Bin: bin, Root: DefaultMinifyRoot,
	}, testLogger(), fake.exec); err != nil {
		t.Fatalf("buildMinifyWith 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jsDir, "chart.min.js")); err != nil {
		t.Errorf("手工 vendor 的 chart.min.js 不应被删除: %v", err)
	}
}

// TestBuildMinifyCleansWhenNoSourcesRemain proves cleanup runs even when the
// last source disappeared, without needing the minify tool. | TestBuildMinifyCleansWhenNoSourcesRemain
// 证明即使最后一个源码消失、且没有 minify 工具，清理仍会执行。
func TestBuildMinifyCleansWhenNoSourcesRemain(t *testing.T) {
	dir := t.TempDir()
	jsDir := filepath.Join(dir, "web", "static", "js")
	writeFile(t, filepath.Join(jsDir, "stale.js"), "console.log(1)\n")
	bin := filepath.Join(dir, minifyBinName())
	writeFile(t, bin, "stand-in\n")

	fake := &fakeExec{onCall: func(_ string, args []string) {
		out, _ := flagValue(args, "-o")
		_ = os.WriteFile(out, []byte("console.log(1)"), 0o644)
	}}
	if _, err := buildMinifyWith(context.Background(), dir, MinifyOptions{
		Bin: bin, Root: DefaultMinifyRoot,
	}, testLogger(), fake.exec); err != nil {
		t.Fatalf("first buildMinifyWith 失败: %v", err)
	}
	if err := os.Remove(filepath.Join(jsDir, "stale.js")); err != nil {
		t.Fatalf("删除 stale.js 失败: %v", err)
	}
	if _, err := buildMinifyWith(context.Background(), dir, MinifyOptions{
		Bin:  filepath.Join(t.TempDir(), "missing-minify"),
		Root: DefaultMinifyRoot,
	}, testLogger(), fake.exec); err != nil {
		t.Fatalf("无源码时不得解析工具: %v", err)
	}
	if _, err := os.Stat(filepath.Join(jsDir, "stale.min.js")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale.min.js 应被删除，实际 err = %v", err)
	}
}

// TestBuildMinifyCheckReportsManifestOrphans proves --check reports a tracked
// output whose source disappeared. | TestBuildMinifyCheckReportsManifestOrphans
// 证明 --check 会报告清单中源文件已消失的产物。
func TestBuildMinifyCheckReportsManifestOrphans(t *testing.T) {
	dir := t.TempDir()
	jsDir := filepath.Join(dir, "web", "static", "js")
	writeFile(t, filepath.Join(jsDir, "stale.min.js"), "stale")
	if err := os.MkdirAll(filepath.Join(dir, ".yd"), 0o755); err != nil {
		t.Fatalf("创建 .yd 失败: %v", err)
	}
	manifest := []byte("{\"outputs\":[\"web/static/js/stale.min.js\"]}\n")
	if err := os.WriteFile(filepath.Join(dir, ".yd", "minify.json"), manifest, 0o644); err != nil {
		t.Fatalf("写入清单失败: %v", err)
	}
	bin := filepath.Join(dir, minifyBinName())
	writeFile(t, bin, "stand-in\n")

	res, err := buildMinifyWith(context.Background(), dir, MinifyOptions{
		Bin: bin, Root: DefaultMinifyRoot, CheckOnly: true,
	}, testLogger(), (&fakeExec{}).exec)
	if err != nil {
		t.Fatalf("buildMinifyWith 失败: %v", err)
	}
	if len(res.Stale) != 1 || res.Stale[0] != "web/static/js/stale.min.js" {
		t.Errorf("Stale = %v，期望 [web/static/js/stale.min.js]", res.Stale)
	}
}

// TestBuildMinifyCheckReportsDrift pins --check: a differing committed .min.js is
// reported and never overwritten. | TestBuildMinifyCheckReportsDrift 固定 --check：
// 内容不同的已提交 .min.js 会被报告，且绝不覆盖。
func TestBuildMinifyCheckReportsDrift(t *testing.T) {
	dir := t.TempDir()
	jsDir := filepath.Join(dir, "web", "static", "js")
	writeFile(t, filepath.Join(jsDir, "app.js"), "console.log(1)\n")
	writeFile(t, filepath.Join(jsDir, "app.min.js"), "old")
	bin := filepath.Join(dir, minifyBinName())
	writeFile(t, bin, "stand-in\n")

	fake := &fakeExec{handler: func(string, []string) (string, int) {
		return "new", 0
	}}
	res, err := buildMinifyWith(context.Background(), dir, MinifyOptions{
		Bin: bin, Root: DefaultMinifyRoot, CheckOnly: true,
	}, testLogger(), fake.exec)
	if err != nil {
		t.Fatalf("buildMinifyWith 失败: %v", err)
	}
	if len(res.Mismatched) != 1 || res.Mismatched[0] != "web/static/js/app.js" {
		t.Errorf("Mismatched = %v, 期望 [web/static/js/app.js]", res.Mismatched)
	}
	if got, err := os.ReadFile(filepath.Join(jsDir, "app.min.js")); err != nil || string(got) != "old" {
		t.Errorf("--check 不得写文件，实际内容 = %q, err = %v", got, err)
	}
}

// TestBuildMinifySkipsMissingRoot pins the no-op path: a project without
// web/static never resolves the tool and never fails. | TestBuildMinifySkipsMissingRoot
// 固定无操作路径：没有 web/static 的项目既不解析工具也不失败。
func TestBuildMinifySkipsMissingRoot(t *testing.T) {
	fake := &fakeExec{}
	res, err := buildMinifyWith(context.Background(), t.TempDir(), MinifyOptions{
		Bin: filepath.Join(t.TempDir(), "does-not-exist"),
	}, testLogger(), fake.exec)
	if err != nil {
		t.Fatalf("缺少 Root 时不应报错: %v", err)
	}
	if res.Files != 0 || len(res.Mismatched) != 0 || len(res.Missing) != 0 {
		t.Errorf("结果 = %+v, 期望零值", res)
	}
	if len(fake.calls) != 0 {
		t.Errorf("解析工具前应返回，实际调用 %v", fake.calls)
	}
}

// TestBuildMinifyCheckMissingRootRequiresTool proves --check does not pass a
// machine without minify merely because the tree is empty. | TestBuildMinifyCheckMissingRootRequiresTool
// 证明 --check 不会因为目录为空就在缺少 minify 的机器上通过。
func TestBuildMinifyCheckMissingRootRequiresTool(t *testing.T) {
	_, err := buildMinifyWith(context.Background(), t.TempDir(), MinifyOptions{
		Bin:       filepath.Join(t.TempDir(), "missing-minify"),
		CheckOnly: true,
	}, testLogger(), (&fakeExec{}).exec)
	if !errors.Is(err, ErrMinifyNotFound) {
		t.Fatalf("err = %v，期望 ErrMinifyNotFound", err)
	}
}

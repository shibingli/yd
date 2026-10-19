package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// errFakeTailwind is the stand-in error returned by Tailwind hook fixtures. | errFakeTailwind 是 Tailwind 钩子夹具返回的替身错误。
var errFakeTailwind = errors.New("fake tailwind failure")

// TestResolveTailwindBinOrder pins the documented resolution order: explicit flag,
// then YD_TAILWIND_BIN, then .yd/tools, then PATH. | TestResolveTailwindBinOrder 固定
// 文档化的解析顺序：显式标志 > YD_TAILWIND_BIN > .yd/tools > PATH。
func TestResolveTailwindBinOrder(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, ".yd", "tools", tailwindBinName())
	writeFile(t, local, "#!/bin/sh\n")
	envBin := filepath.Join(t.TempDir(), tailwindBinName())
	writeFile(t, envBin, "#!/bin/sh\n")
	flagBin := filepath.Join(t.TempDir(), tailwindBinName())
	writeFile(t, flagBin, "#!/bin/sh\n")

	t.Run("explicit flag wins", func(t *testing.T) {
		t.Setenv(tailwindEnvBin, envBin)
		got, err := ResolveTailwindBin(dir, flagBin)
		if err != nil {
			t.Fatalf("ResolveTailwindBin 失败: %v", err)
		}
		if got != flagBin {
			t.Fatalf("解析结果 = %q, 期望 %q", got, flagBin)
		}
	})

	t.Run("env wins over local tools", func(t *testing.T) {
		t.Setenv(tailwindEnvBin, envBin)
		got, err := ResolveTailwindBin(dir, "")
		if err != nil {
			t.Fatalf("ResolveTailwindBin 失败: %v", err)
		}
		if got != envBin {
			t.Fatalf("解析结果 = %q, 期望 %q", got, envBin)
		}
	})

	t.Run("local tools dir", func(t *testing.T) {
		t.Setenv(tailwindEnvBin, "")
		got, err := ResolveTailwindBin(dir, "")
		if err != nil {
			t.Fatalf("ResolveTailwindBin 失败: %v", err)
		}
		if got != local {
			t.Fatalf("解析结果 = %q, 期望 %q", got, local)
		}
	})
}

// TestResolveTailwindBinFindsWindowsCommandShim proves a .cmd shim installed by a package manager
// is found on PATH: looking up "tailwindcss.exe" explicitly would miss it, even though the same
// shim works for yd dev and yd tools. | TestResolveTailwindBinFindsWindowsCommandShim 证明包管理器
// 安装的 .cmd 垫片能从 PATH 找到：显式查找 "tailwindcss.exe" 会漏掉它，而同一个垫片在 yd dev 与
// yd tools 中都能工作。
func TestResolveTailwindBinFindsWindowsCommandShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: .cmd shims are a Windows package-manager convention | 仅 Windows：.cmd 垫片是 Windows 包管理器约定")
	}
	shimDir := t.TempDir()
	shim := filepath.Join(shimDir, "tailwindcss.cmd")
	writeFile(t, shim, "@echo off\r\n")
	t.Setenv(tailwindEnvBin, "")
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := ResolveTailwindBin(t.TempDir(), "")
	if err != nil {
		t.Fatalf("ResolveTailwindBin 失败 | ResolveTailwindBin failed: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(got), filepath.Clean(shim)) {
		t.Fatalf("解析结果 = %[1]q，期望 %[2]q | resolved %[1]q, want %[2]q", got, shim)
	}
}

// TestResolveTailwindBinMissing asserts the sentinel returned when nothing is found. | TestResolveTailwindBinMissing 断言找不到可执行文件时返回的哨兵错误。
func TestResolveTailwindBinMissing(t *testing.T) {
	t.Setenv(tailwindEnvBin, "")
	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveTailwindBin(t.TempDir(), ""); !errors.Is(err, ErrTailwindNotFound) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrTailwindNotFound)", err)
	}
}

// TestBuildTailwindRejectsMissingInput asserts the input stylesheet is checked first. | TestBuildTailwindRejectsMissingInput 断言优先校验输入样式表是否存在。
func TestBuildTailwindRejectsMissingInput(t *testing.T) {
	_, err := BuildTailwind(context.Background(), t.TempDir(), TailwindOptions{
		Enabled: true,
		Minify:  true,
		Input:   "web/static/css/app.css",
		Output:  "web/static/css/yd.css",
	}, testLogger())
	if !errors.Is(err, ErrTailwindInput) {
		t.Fatalf("错误 = %v, 期望 errors.Is(err, ErrTailwindInput)", err)
	}
}

// TestBuildTailwindRunsResolvedBinary runs a stand-in Tailwind binary that records
// its argv, proving the -i/-o/--minify argument shape. | TestBuildTailwindRunsResolvedBinary
// 运行一个记录 argv 的替身 Tailwind 二进制，验证 -i/-o/--minify 参数形态。
func TestBuildTailwindRunsResolvedBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("替身脚本依赖 POSIX shell; the stand-in script needs a POSIX shell")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "web", "static", "css", "app.css"), "@import \"tailwindcss\";\n")
	argvFile := filepath.Join(dir, "argv.txt")
	bin := filepath.Join(dir, tailwindBinName())
	writeFile(t, bin, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \""+argvFile+"\"\nprintf 'compiled' > \"$4\"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("赋予可执行权限失败: %v", err)
	}

	bytesWritten, err := BuildTailwind(context.Background(), dir, TailwindOptions{
		Enabled: true,
		Minify:  true,
		Bin:     bin,
		Input:   "web/static/css/app.css",
		Output:  "web/static/css/yd.css",
	}, testLogger())
	if err != nil {
		t.Fatalf("BuildTailwind 失败: %v", err)
	}
	if bytesWritten != int64(len("compiled")) {
		t.Errorf("产出字节数 = %d, 期望 %d", bytesWritten, len("compiled"))
	}
	recorded, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("读取 argv 记录失败: %v", err)
	}
	for _, want := range []string{"-i", "-o", "--minify"} {
		if !containsLine(string(recorded), want) {
			t.Errorf("argv 记录 = %q, 缺少 %q", recorded, want)
		}
	}
}

// TestBuildTailwindArgvWithInjectedExec pins the Tailwind argv through the injectable
// command hook, so the argument shape is covered on every platform. | TestBuildTailwindArgvWithInjectedExec
// 通过可注入的命令钩子固定 Tailwind 的 argv，使参数形态在所有平台上都被覆盖。
func TestBuildTailwindArgvWithInjectedExec(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "web", "static", "css", "app.css"), "@import \"tailwindcss\";\n")
	bin := filepath.Join(dir, tailwindBinName())
	writeFile(t, bin, "stand-in for the tailwindcss executable\n")

	fake := &fakeExec{
		handler: func(string, []string) (string, int) { return "compiled ok\n", 0 },
		onCall: func(_ string, args []string) {
			path, ok := flagValue(args, "-o")
			if !ok {
				return
			}
			_ = os.WriteFile(path, []byte("/* compiled */"), 0o644)
		},
	}

	bytesWritten, err := buildTailwindWith(context.Background(), dir, TailwindOptions{
		Enabled: true,
		Minify:  true,
		Bin:     bin,
	}, testLogger(), fake.exec)
	if err != nil {
		t.Fatalf("buildTailwindWith 失败: %v", err)
	}
	if bytesWritten != int64(len("/* compiled */")) {
		t.Errorf("产出字节数 = %d, 期望 %d", bytesWritten, len("/* compiled */"))
	}
	argv := fake.calls[0]
	if got, _ := flagValue(argv, "-i"); got != filepath.Join(dir, DefaultTailwindInput) {
		t.Errorf("-i = %q, 期望 %q", got, filepath.Join(dir, DefaultTailwindInput))
	}
	if got, _ := flagValue(argv, "-o"); got != filepath.Join(dir, DefaultTailwindOutput) {
		t.Errorf("-o = %q, 期望 %q", got, filepath.Join(dir, DefaultTailwindOutput))
	}
	if !slices.Contains(argv, "--minify") {
		t.Errorf("argv = %v, 缺少 --minify", argv)
	}
}

// containsLine reports whether s holds a line equal to want. | containsLine 判断 s 是否包含等于 want 的一行。
func containsLine(s, want string) bool {
	for _, line := range splitLines(s) {
		if line == want {
			return true
		}
	}
	return false
}

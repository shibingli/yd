package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// writeFakeTool writes a fake executable that appends every received argv entry to argsFile and then exits with exitCode. | writeFakeTool 写一个假的可执行脚本：把收到的 argv 逐行追加进 argsFile，再以 exitCode 退出。
func writeFakeTool(t *testing.T, dir, argsFile string, exitCode int) string {
	t.Helper()
	code := strconv.Itoa(exitCode)
	if runtime.GOOS == "windows" {
		// The batch file records one argument per line: %~1 strips quotes and shift walks the list. | 批处理逐行记录参数：%~1 去掉可能的引号，shift 逐个前移。
		body := "@echo off\r\n" +
			"if exist \"" + argsFile + "\" del \"" + argsFile + "\"\r\n" +
			":loop\r\n" +
			"if \"%~1\"==\"\" goto done\r\n" +
			">> \"" + argsFile + "\" echo %~1\r\n" +
			"shift\r\n" +
			"goto loop\r\n" +
			":done\r\n" +
			"exit /b " + code + "\r\n"
		p := filepath.Join(dir, "fake-tailwind.cmd")
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("写入假工具失败 | write fake tool: %v", err)
		}
		return p
	}
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsFile + "\"\nexit " + code + "\n"
	p := filepath.Join(dir, "fake-tailwind.sh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("写入假工具失败 | write fake tool: %v", err)
	}
	return p
}

// readLines reads a text file and returns its non-empty lines. | readLines 读取文本文件并返回其中的非空行。
func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败 | read %s: %v", path, path, err)
	}
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// writeFile writes a file and fails the test on error. | writeFile 写入文件，失败即终止测试。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败 | mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败 | write %s: %v", path, path, err)
	}
}

func TestResolveTailwindPrefersFlagThenEnvThenLocalBinThenPath(t *testing.T) {
	t.Setenv(EnvTailwindBin, "")
	dir := t.TempDir()
	suffix := exeSuffix()

	touch := func(p string) string {
		writeFile(t, p, "# fake tailwind | 假 tailwind\n")
		return p
	}
	flagBin := touch(filepath.Join(dir, "flag", "tailwindcss"+suffix))
	configBin := touch(filepath.Join(dir, "config", "tailwindcss"+suffix))
	envBin := touch(filepath.Join(dir, "env", "tailwindcss"+suffix))
	localDir := filepath.Join(dir, "project")
	localBin := touch(filepath.Join(localDir, ".yd", "tools", "tailwindcss"+suffix))
	pathBin := touch(filepath.Join(dir, "path", "tailwindcss"+suffix))
	lookup := func(string) (string, error) { return pathBin, nil }
	missing := filepath.Join(dir, "empty")

	cases := []struct {
		name string
		o    ToolOptions
		want string
	}{
		{"标志优先 | flag wins", ToolOptions{Bin: flagBin, ConfigBin: configBin, EnvBin: envBin, LocalDir: localDir, PathLookup: lookup}, flagBin},
		{"配置次之 | config next", ToolOptions{ConfigBin: configBin, EnvBin: envBin, LocalDir: localDir, PathLookup: lookup}, configBin},
		{"环境变量次之 | env next", ToolOptions{EnvBin: envBin, LocalDir: localDir, PathLookup: lookup}, envBin},
		{"本地目录次之 | local bin next", ToolOptions{LocalDir: localDir, PathLookup: lookup}, localBin},
		{"最后回退 PATH | PATH last", ToolOptions{LocalDir: missing, PathLookup: lookup}, pathBin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveTailwind(tc.o)
			if err != nil {
				t.Fatalf("ResolveTailwind 返回错误 | ResolveTailwind: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ResolveTailwind = %q，期望 %q | want %q", got, tc.want, tc.want)
			}
		})
	}

	t.Run("字段为空时读取环境变量 | empty field falls back to the environment", func(t *testing.T) {
		t.Setenv(EnvTailwindBin, envBin)
		got, err := ResolveTailwind(ToolOptions{LocalDir: localDir, PathLookup: lookup})
		if err != nil {
			t.Fatalf("ResolveTailwind 返回错误 | ResolveTailwind: %v", err)
		}
		if got != envBin {
			t.Fatalf("ResolveTailwind = %q，期望 %q | want %q", got, envBin, envBin)
		}
	})

	t.Run("裸命令名走 PATH | bare name resolves on PATH", func(t *testing.T) {
		got, err := ResolveTailwind(ToolOptions{Bin: "tailwindcss", PathLookup: lookup})
		if err != nil {
			t.Fatalf("ResolveTailwind 返回错误 | ResolveTailwind: %v", err)
		}
		if got != pathBin {
			t.Fatalf("ResolveTailwind = %q，期望 %q | want %q", got, pathBin, pathBin)
		}
	})

	t.Run("配置项指向不存在的路径时返回哨兵 | a dangling configured path returns the sentinel", func(t *testing.T) {
		_, err := ResolveTailwind(ToolOptions{
			Bin:        filepath.Join(dir, "nope", "tailwindcss"+suffix),
			LocalDir:   missing,
			PathLookup: func(string) (string, error) { return "", errors.New("not found") },
		})
		if !errors.Is(err, ErrToolNotFound) {
			t.Fatalf("err = %v，期望 ErrToolNotFound | want ErrToolNotFound", err)
		}
	})

	t.Run("全部缺失返回哨兵 | nothing resolvable returns the sentinel", func(t *testing.T) {
		_, err := ResolveTailwind(ToolOptions{
			LocalDir:   missing,
			PathLookup: func(string) (string, error) { return "", errors.New("not found") },
		})
		if !errors.Is(err, ErrToolNotFound) {
			t.Fatalf("err = %v，期望 ErrToolNotFound | want ErrToolNotFound", err)
		}
	})
}

func TestBuildCSSInvokesBinaryWithExpectedArgs(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	bin := writeFakeTool(t, dir, argsFile, 0)
	out := filepath.Join(dir, "yd.css")
	const css = "/* tailwind */\n"
	writeFile(t, out, css)

	res, err := BuildCSS(context.Background(), bin, CSSOptions{
		Input:   "web/static/css/app.css",
		Output:  out,
		Minify:  true,
		Content: []string{"web/templates", "internal"},
	})
	if err != nil {
		t.Fatalf("BuildCSS 返回错误 | BuildCSS: %v", err)
	}
	if res.CSSPath != out {
		t.Errorf("CSSPath = %q，期望 %q | want %q", res.CSSPath, out, out)
	}
	if res.Bytes != len(css) {
		t.Errorf("Bytes = %d，期望 %d | want %d", res.Bytes, len(css), len(css))
	}
	if res.Duration <= 0 {
		t.Errorf("Duration = %v，期望正值 | want a positive duration", res.Duration)
	}

	want := []string{"-i", "web/static/css/app.css", "-o", out, "--minify", "--content", "web/templates", "--content", "internal"}
	if got := readLines(t, argsFile); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v，期望 %v | want %v", got, want, want)
	}
}

func TestBuildCSSSurfacesToolFailure(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeTool(t, dir, filepath.Join(dir, "args.txt"), 7)

	_, err := BuildCSS(context.Background(), bin, CSSOptions{Input: "app.css", Output: filepath.Join(dir, "out", "yd.css")})
	if !errors.Is(err, ErrBuildFailed) {
		t.Fatalf("err = %v，期望 ErrBuildFailed | want ErrBuildFailed", err)
	}
	if !strings.Contains(err.Error(), "7") {
		t.Errorf("错误信息未包含退出码 7：%v | error does not mention exit code 7", err)
	}

	t.Run("空二进制路径返回哨兵 | an empty binary path returns the sentinel", func(t *testing.T) {
		_, err := BuildCSS(context.Background(), "  ", CSSOptions{Input: "app.css", Output: filepath.Join(dir, "yd.css")})
		if !errors.Is(err, ErrToolNotFound) {
			t.Fatalf("err = %v，期望 ErrToolNotFound | want ErrToolNotFound", err)
		}
	})

	t.Run("输出缺失返回哨兵 | a missing output returns the sentinel", func(t *testing.T) {
		ok := writeFakeTool(t, t.TempDir(), filepath.Join(dir, "args2.txt"), 0)
		_, err := BuildCSS(context.Background(), ok, CSSOptions{Input: "app.css", Output: filepath.Join(dir, "never", "yd.css")})
		if !errors.Is(err, ErrBuildFailed) {
			t.Fatalf("err = %v，期望 ErrBuildFailed | want ErrBuildFailed", err)
		}
	})
}

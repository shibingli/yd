package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shibingli/yd/align"
	"github.com/spf13/pflag"
)

// alignParseFlags parses args with a fresh align flag set. | alignParseFlags 用全新的 align 标志集解析 args。
func alignParseFlags(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	fs := pflag.NewFlagSet(alignCommandName, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerAlignFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("解析 align 标志失败：%v | parsing align flags: %v", err, err)
	}
	return fs
}

// alignFakeTool writes a fake betteralign that replays two diagnostics and exits with the analyzer's "diagnostics found" code. | alignFakeTool 写一个假 betteralign：回放两条告警并以分析器的「发现诊断」退出码结束。
func alignFakeTool(t *testing.T, dir string) string {
	t.Helper()
	lines := []string{
		`E:\repo\config\config.go:76:14: struct with 104 pointer bytes could be 96 (lowers GC scan-work estimate)`,
		`E:\repo\router\pattern.go:77:14: 8 bytes saved: struct of size 96 could be 88`,
	}
	if runtime.GOOS == "windows" {
		body := "@echo off\r\n"
		for _, l := range lines {
			body += "echo " + l + "\r\n"
		}
		body += "exit /b 3\r\n"
		p := filepath.Join(dir, "fake-align.cmd")
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("写入假分析器失败：%v | write fake analyzer: %v", err, err)
		}
		return p
	}
	body := "#!/bin/sh\nprintf '%s\\n'"
	for _, l := range lines {
		body += " '" + l + "'"
	}
	body += "\nexit 3\n"
	p := filepath.Join(dir, "fake-align.sh")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("写入假分析器失败：%v | write fake analyzer: %v", err, err)
	}
	return p
}

// TestAlignCommandRegistered keeps align registered exactly once: Register panics on duplicates. | TestAlignCommandRegistered 保证 align 只注册一次：Register 对重名会 panic。
func TestAlignCommandRegistered(t *testing.T) {
	count := 0
	for _, c := range Commands() {
		if c.Name != alignCommandName {
			continue
		}
		count++
		if c.Run == nil {
			t.Error("align 命令缺少 Run | the align command has no Run")
		}
		if c.Flags == nil {
			t.Error("align 命令缺少 Flags | the align command has no Flags")
		}
		if c.Usage != alignUsageLine {
			t.Errorf("Usage = %q，期望 %q | want %q", c.Usage, alignUsageLine, alignUsageLine)
		}
	}
	if count != 1 {
		t.Fatalf("align 命令注册了 %d 次，期望 1 次 | want exactly 1 registration", count)
	}
}

// TestAlignHelp lists every flag in the help text. | TestAlignHelp 断言帮助文本列出全部标志。
func TestAlignHelp(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{"align", "--help"}, te.env); err != nil {
		t.Fatalf("align --help 返回错误：%v | align --help: %v", err, err)
	}
	got := te.out.String()
	for _, want := range []string{
		"--" + flagAlignApply,
		"--" + flagAlignTest,
		"--" + flagAlignExcludeDir,
		"--" + flagAlignBin,
		alignUsageLine,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("帮助文本缺少 %q：\n%s", want, got)
		}
	}
}

// TestAlignDefaultTargets falls back to ./... when no pattern is given. | TestAlignDefaultTargets 未传包模式时回退到 ./...。
func TestAlignDefaultTargets(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	o, _ := alignOptions(te.env, alignParseFlags(t))

	if len(o.Targets) != 0 {
		t.Errorf("Targets = %v，期望为空 | want no targets", o.Targets)
	}
	args := o.Args()
	if got := args[len(args)-1]; got != align.DefaultTarget {
		t.Errorf("最后一个参数 = %q，期望 %q | want %q", got, align.DefaultTarget, align.DefaultTarget)
	}
}

// TestAlignForwardsFlags maps every flag onto the align package options. | TestAlignForwardsFlags 断言每个标志都映射到 align 包选项。
func TestAlignForwardsFlags(t *testing.T) {
	dir := t.TempDir()
	env := &Env{WorkDir: dir, Out: io.Discard}
	fs := alignParseFlags(t,
		"--apply",
		"--test",
		"--exclude-dir=generated",
		"--exclude-dir", "vendor,testdata",
		"--bin=C:/tools/betteralign.exe",
		"./version/...", "./errx/...",
	)

	o, tool := alignOptions(env, fs)

	if !o.Apply {
		t.Error("Apply = false，期望 --apply 生效 | want --apply")
	}
	if !o.IncludeTests {
		t.Error("IncludeTests = false，期望 --test 生效 | want --test")
	}
	if got := strings.Join(o.ExcludeDirs, "|"); got != "generated|vendor,testdata" {
		t.Errorf("ExcludeDirs = %q，期望两次 --exclude-dir 都被保留 | want both occurrences", got)
	}
	if got := strings.Join(o.Targets, " "); got != "./version/... ./errx/..." {
		t.Errorf("Targets = %q，期望位置参数原样透传 | want the positional patterns", got)
	}
	if o.Dir != dir {
		t.Errorf("Dir = %q，期望工作目录 %q | want %q", o.Dir, dir, dir)
	}
	if tool.Bin != "C:/tools/betteralign.exe" {
		t.Errorf("Bin = %q，期望标志值 | want the flag value", tool.Bin)
	}
	if tool.LocalDir != dir {
		t.Errorf("LocalDir = %q，期望工作目录 %q | want %q", tool.LocalDir, dir, dir)
	}
	joined := strings.Join(o.Args(), " ")
	for _, want := range []string{"-apply", "-test", "-exclude_dirs generated,vendor,testdata", "./version/..."} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv 缺少 %q：%s | argv misses %q", want, joined, want)
		}
	}
}

// TestAlignPrintsBilingualSummary runs the command against a fake analyzer and asserts the summary. | TestAlignPrintsBilingualSummary 用假分析器运行命令并断言摘要。
func TestAlignPrintsBilingualSummary(t *testing.T) {
	dir := t.TempDir()
	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	bin := alignFakeTool(t, dir)

	if err := Execute(context.Background(), []string{"align", "--bin", bin}, te.env); err != nil {
		t.Fatalf("yd align 返回错误：%v | yd align: %v", err, err)
	}
	got := te.out.String()
	for _, want := range []string{"align: 2 diagnostics | 2 条告警", "duration | 耗时"} {
		if !strings.Contains(got, want) {
			t.Errorf("摘要缺少 %q：\n%s", want, got)
		}
	}
	if strings.Contains(got, "applied | 已应用") {
		t.Errorf("未传 --apply 时不得提示已应用：\n%s", got)
	}

	te.out.Reset()
	if err := Execute(context.Background(), []string{"align", "--apply", "--bin", bin}, te.env); err != nil {
		t.Fatalf("yd align --apply 返回错误：%v | yd align --apply: %v", err, err)
	}
	if got := te.out.String(); !strings.Contains(got, "applied | 已应用") {
		t.Errorf("--apply 时必须提示已应用：\n%s", got)
	}
}

// TestAlignMissingToolHintsWithoutPanic keeps the optional tool from ever breaking the command. | TestAlignMissingToolHintsWithoutPanic 保证可选工具永远不会让命令崩溃。
func TestAlignMissingToolHintsWithoutPanic(t *testing.T) {
	t.Setenv(align.EnvToolBin, "")
	orig := align.PathLookup
	align.PathLookup = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { align.PathLookup = orig })

	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = t.TempDir() // 没有 .yd/tools，也没有 PATH | no .yd/tools and no PATH

	err := Execute(context.Background(), []string{"align"}, te.env)
	if !errors.Is(err, align.ErrToolNotFound) {
		t.Fatalf("err = %v，期望 align.ErrToolNotFound | want align.ErrToolNotFound", err)
	}
	got := te.errs.String()
	for _, want := range []string{align.InstallHint, align.LocalToolDir} {
		if !strings.Contains(got, want) {
			t.Errorf("安装提示缺少 %q：\n%s", want, got)
		}
	}
}

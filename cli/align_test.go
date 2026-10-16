package cli

import (
	"context"
	"errors"
	"fmt"
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

// alignIteratingTool writes a fake analyzer that replays a different number of diagnostics per invocation, counting invocations in a file beside itself. | alignIteratingTool 写一个假分析器：每次调用回放不同数量的诊断，调用次数记在自身旁边的文件里。
func alignIteratingTool(t *testing.T, dir string, steps []int) string {
	t.Helper()
	if len(steps) == 0 {
		t.Fatal("steps 不能为空 | steps must not be empty")
	}
	lines := func(count int) []string {
		out := make([]string, 0, count)
		for i := 0; i < count; i++ {
			out = append(out, fmt.Sprintf("E:\\repo\\pkg\\file.go:%d:14: 8 bytes saved: struct of size %d could be %d", i+1, 96+8*i, 88+8*i))
		}
		return out
	}
	exitCode := func(count int) int {
		if count == 0 {
			return 0
		}
		return 3
	}

	if runtime.GOOS == "windows" {
		var b strings.Builder
		b.WriteString("@echo off\r\n")
		b.WriteString("set \"state=%~dp0passes.count\"\r\n")
		b.WriteString("set n=0\r\n")
		b.WriteString("if exist \"%state%\" set /p n=<\"%state%\"\r\n")
		b.WriteString("set /a n=n+1\r\n")
		b.WriteString(">\"%state%\" echo %n%\r\n")
		for i := range steps {
			b.WriteString(fmt.Sprintf("if %%n%%==%d goto p%d\r\n", i+1, i+1))
		}
		b.WriteString(fmt.Sprintf("goto p%d\r\n", len(steps)))
		for i, count := range steps {
			b.WriteString(fmt.Sprintf(":p%d\r\n", i+1))
			for _, line := range lines(count) {
				b.WriteString("echo " + line + "\r\n")
			}
			b.WriteString(fmt.Sprintf("exit /b %d\r\n", exitCode(count)))
		}
		p := filepath.Join(dir, "iterating-align.cmd")
		if err := os.WriteFile(p, []byte(b.String()), 0o755); err != nil {
			t.Fatalf("写入迭代假分析器失败：%v | write the iterating fake analyzer: %v", err, err)
		}
		return p
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("d=$(dirname \"$0\")\n")
	b.WriteString("s=\"$d/passes.count\"\n")
	b.WriteString("n=0\n")
	b.WriteString("[ -f \"$s\" ] && n=$(cat \"$s\")\n")
	b.WriteString("n=$((n+1))\n")
	b.WriteString("echo \"$n\" > \"$s\"\n")
	b.WriteString("case \"$n\" in\n")
	for i, count := range steps {
		b.WriteString(fmt.Sprintf("%d)\n", i+1))
		for _, line := range lines(count) {
			b.WriteString("  printf '%s\\n' '" + line + "'\n")
		}
		b.WriteString(fmt.Sprintf("  exit %d\n  ;;\n", exitCode(count)))
	}
	b.WriteString("*)\n")
	for _, line := range lines(steps[len(steps)-1]) {
		b.WriteString("  printf '%s\\n' '" + line + "'\n")
	}
	b.WriteString(fmt.Sprintf("  exit %d\n  ;;\n", exitCode(steps[len(steps)-1])))
	b.WriteString("esac\n")
	p := filepath.Join(dir, "iterating-align.sh")
	if err := os.WriteFile(p, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("写入迭代假分析器失败：%v | write the iterating fake analyzer: %v", err, err)
	}
	return p
}

// TestAlignApplyIteratesToFixpoint asserts that --apply keeps going until the diagnostics stop and reports the pass count. | TestAlignApplyIteratesToFixpoint 断言 --apply 会迭代到诊断不再减少，并报告实际遍数。
func TestAlignApplyIteratesToFixpoint(t *testing.T) {
	dir := t.TempDir()
	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	bin := alignIteratingTool(t, dir, []int{2, 0})

	if err := Execute(context.Background(), []string{"align", "--apply", "--bin", bin}, te.env); err != nil {
		t.Fatalf("yd align --apply 返回错误：%v | yd align --apply: %v", err, err)
	}
	got := te.out.String()
	for _, want := range []string{"align: 0 diagnostics | 0 条告警", "applied | 已应用", "passes | 遍数: 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("摘要缺少 %q：\n%s | the summary misses %q", want, got, want)
		}
	}
	if strings.Contains(te.errs.String(), "warning | 告警") {
		t.Errorf("收敛时不应告警：\n%s | a converged run must not warn", te.errs.String())
	}
}

// TestAlignMaxPassesCapsTheLoop asserts the flag caps the passes and that a capped run warns instead of silently stopping. | TestAlignMaxPassesCapsTheLoop 断言标志能限制遍数，且被限制的运行会告警而不是悄悄停下。
func TestAlignMaxPassesCapsTheLoop(t *testing.T) {
	dir := t.TempDir()
	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	bin := alignIteratingTool(t, dir, []int{5, 4, 3, 2, 1})

	if err := Execute(context.Background(), []string{"align", "--apply", "--max-passes", "3", "--bin", bin}, te.env); err != nil {
		t.Fatalf("yd align --max-passes 返回错误：%v | yd align --max-passes: %v", err, err)
	}
	if got := te.out.String(); !strings.Contains(got, "passes | 遍数: 3") {
		t.Errorf("摘要缺少遍数 3：\n%s | the summary misses the pass count", got)
	}
	if got := te.errs.String(); !strings.Contains(got, "warning | 告警") || !strings.Contains(got, "--max-passes") {
		t.Errorf("达到上限时应告警并提示 --max-passes：\n%s | a capped run must warn", got)
	}
}

// TestAlignMaxPassesRejectsNegative keeps a nonsensical value from being silently read as the default. | TestAlignMaxPassesRejectsNegative 防止无意义取值被静默当成默认值。
func TestAlignMaxPassesRejectsNegative(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = t.TempDir()

	err := Execute(context.Background(), []string{"align", "--max-passes=-2"}, te.env)
	if !errors.Is(err, errAlignMaxPasses) {
		t.Fatalf("err = %v，期望 errAlignMaxPasses | want errAlignMaxPasses", err)
	}
	if got := te.errs.String(); !strings.Contains(got, "--max-passes") {
		t.Errorf("提示缺少标志名：\n%s | the hint misses the flag name", got)
	}
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
		"--" + flagAlignMaxPasses,
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
		"--max-passes=4",
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
	if o.MaxPasses != 4 {
		t.Errorf("MaxPasses = %d，期望 --max-passes 生效 | want the flag value", o.MaxPasses)
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

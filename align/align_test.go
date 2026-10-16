package align

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/shibingli/yd/logx"
)

// writeFakeTool writes a fake analyzer into dir: it echoes its argv, prints lines and exits with code. | writeFakeTool 往 dir 写一个假分析器：回显 argv、打印 lines 并以 code 退出。
//
// Echoing the argv is what lets a test assert that Args() really reached the process, not just that the function returned the expected slice. | 回显 argv 使测试能断言 Args() 真的到达了进程，而不只是函数返回了预期的切片。
func writeFakeTool(t *testing.T, dir string, lines []string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		var b strings.Builder
		b.WriteString("@echo off\r\n")
		b.WriteString("echo ARGS %*\r\n")
		for _, l := range lines {
			b.WriteString("echo " + l + "\r\n")
		}
		b.WriteString("exit /b " + strconv.Itoa(code) + "\r\n")
		p := filepath.Join(dir, "fake-betteralign.cmd")
		if err := os.WriteFile(p, []byte(b.String()), 0o755); err != nil {
			t.Fatalf("写入假分析器失败：%v | write fake analyzer: %v", err, err)
		}
		return p
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("echo ARGS \"$*\"\n")
	b.WriteString("printf '%s\\n'")
	for _, l := range lines {
		b.WriteString(" '" + l + "'")
	}
	b.WriteString("\n")
	b.WriteString("exit " + strconv.Itoa(code) + "\n")
	p := filepath.Join(dir, "fake-betteralign.sh")
	if err := os.WriteFile(p, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("写入假分析器失败：%v | write fake analyzer: %v", err, err)
	}
	return p
}

// writeIteratingTool writes a fake analyzer that replays a different number of diagnostics on every invocation: the invocation counter lives in a file next to the script, so a test can drive the apply loop pass by pass. | writeIteratingTool 写一个假分析器：每次调用回放不同数量的诊断，调用计数保存在脚本旁边的文件里，因此测试可以逐遍驱动 apply 循环。
//
// A step of zero makes that invocation exit cleanly, which is exactly what the real analyzer does once a tree is aligned. | 某一步为 0 时该次调用干净退出，这正是真实分析器在整棵树对齐后的行为。
func writeIteratingTool(t *testing.T, dir string, steps []int) string {
	t.Helper()
	if len(steps) == 0 {
		t.Fatal("steps 不能为空 | steps must not be empty")
	}
	diagnostics := func(count int) []string {
		out := make([]string, 0, count)
		for i := 0; i < count; i++ {
			out = append(out, fmt.Sprintf(
				`E:\repo\pkg\file.go:%d:14: struct with %d pointer bytes could be %d (lowers GC scan-work estimate)`,
				i+1, 64+i, 32+i))
		}
		return out
	}
	exitCode := func(count int) int {
		if count == 0 {
			return 0
		}
		return exitDiagnostics
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
		b.WriteString(fmt.Sprintf("goto p%d\r\n", len(steps))) // 超出的调用沿用最后一步。 | Later invocations replay the last step.
		for i, count := range steps {
			b.WriteString(fmt.Sprintf(":p%d\r\n", i+1))
			for _, line := range diagnostics(count) {
				b.WriteString("echo " + line + "\r\n")
			}
			b.WriteString(fmt.Sprintf("exit /b %d\r\n", exitCode(count)))
		}
		p := filepath.Join(dir, "iterating-betteralign.cmd")
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
		for _, line := range diagnostics(count) {
			b.WriteString("  printf '%s\\n' '" + line + "'\n")
		}
		b.WriteString(fmt.Sprintf("  exit %d\n  ;;\n", exitCode(count)))
	}
	b.WriteString("*)\n")
	for _, line := range diagnostics(steps[len(steps)-1]) {
		b.WriteString("  printf '%s\\n' '" + line + "'\n")
	}
	b.WriteString(fmt.Sprintf("  exit %d\n  ;;\n", exitCode(steps[len(steps)-1])))
	b.WriteString("esac\n")
	p := filepath.Join(dir, "iterating-betteralign.sh")
	if err := os.WriteFile(p, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("写入迭代假分析器失败：%v | write the iterating fake analyzer: %v", err, err)
	}
	return p
}

// writeFile writes a file, creating its parent directory. | writeFile 写文件并创建其父目录。
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败：%v | mkdir: %v", err, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败：%v | write %s: %v", path, err, path, err)
	}
}

// testLogger returns a logger whose output is discarded, so a run never pollutes the test log. | testLogger 返回输出被丢弃的日志器，使运行不会污染测试日志。
func testLogger() *logx.Logger {
	return logx.New(logx.Options{Writer: io.Discard, Level: logx.DebugLevel, Format: "console"})
}

// repoRoot walks up from this test file to the repository root. | repoRoot 从本测试文件向上回溯到仓库根目录。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件 | cannot locate the test file")
	}
	root := filepath.Dir(filepath.Dir(file))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("仓库根目录缺少 go.mod：%v | missing go.mod in %s: %v", err, root, err)
	}
	return root
}

// TestResolvePrefersFlagThenEnvThenLocalBinThenPath walks the whole precedence chain with faked files. | TestResolvePrefersFlagThenEnvThenLocalBinThenPath 用伪造文件走完整条优先级链。
func TestResolvePrefersFlagThenEnvThenLocalBinThenPath(t *testing.T) {
	t.Setenv(EnvToolBin, "")
	dir := t.TempDir()
	suffix := exeSuffix()

	touch := func(p string) string {
		writeFile(t, p, "# fake betteralign | 假 betteralign\n")
		return p
	}
	flagBin := touch(filepath.Join(dir, "flag", "betteralign"+suffix))
	configBin := touch(filepath.Join(dir, "config", "betteralign"+suffix))
	envBin := touch(filepath.Join(dir, "env", "betteralign"+suffix))
	localDir := filepath.Join(dir, "project")
	localBin := touch(filepath.Join(localDir, ".yd", "tools", "betteralign"+suffix))
	pathBin := touch(filepath.Join(dir, "path", "betteralign"+suffix))
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
			got, err := Resolve(tc.o)
			if err != nil {
				t.Fatalf("Resolve 返回错误：%v | Resolve: %v", err, err)
			}
			if got != tc.want {
				t.Fatalf("Resolve = %q，期望 %q | want %q", got, tc.want, tc.want)
			}
		})
	}

	t.Run("字段为空时读取环境变量 | empty field falls back to the environment", func(t *testing.T) {
		t.Setenv(EnvToolBin, envBin)
		got, err := Resolve(ToolOptions{LocalDir: localDir, PathLookup: lookup})
		if err != nil {
			t.Fatalf("Resolve 返回错误：%v | Resolve: %v", err, err)
		}
		if got != envBin {
			t.Fatalf("Resolve = %q，期望 %q | want %q", got, envBin, envBin)
		}
	})

	t.Run("裸命令名走 PATH | a bare name resolves on PATH", func(t *testing.T) {
		got, err := Resolve(ToolOptions{Bin: toolName, PathLookup: lookup})
		if err != nil {
			t.Fatalf("Resolve 返回错误：%v | Resolve: %v", err, err)
		}
		if got != pathBin {
			t.Fatalf("Resolve = %q，期望 %q | want %q", got, pathBin, pathBin)
		}
	})

	t.Run("全部缺失时返回带安装提示的哨兵 | nothing resolvable returns the sentinel with the hint", func(t *testing.T) {
		_, err := Resolve(ToolOptions{
			Bin:        filepath.Join(dir, "nope", "betteralign"+suffix),
			LocalDir:   missing,
			PathLookup: func(string) (string, error) { return "", errors.New("not found") },
		})
		if !errors.Is(err, ErrToolNotFound) {
			t.Fatalf("err = %v，期望 ErrToolNotFound | want ErrToolNotFound", err)
		}
		if !strings.Contains(err.Error(), InstallHint) {
			t.Errorf("哨兵文案缺少安装提示 %q：%v | the sentinel does not carry the install hint", InstallHint, err)
		}
		if !strings.Contains(err.Error(), LocalToolDir) {
			t.Errorf("哨兵文案缺少本地目录 %q：%v | the sentinel does not mention %s", LocalToolDir, err, LocalToolDir)
		}
	})
}

// TestOptionsArgsAssembly asserts the exact argv of every flag combination. | TestOptionsArgsAssembly 逐一断言每种标志组合的精确 argv。
func TestOptionsArgsAssembly(t *testing.T) {
	cases := []struct {
		name string
		o    Options
		want []string
	}{
		{"默认 | defaults", Options{}, []string{"-test=false", "./..."}},
		{"应用 | apply", Options{Apply: true}, []string{"-apply", "-test=false", "./..."}},
		{"含测试 | tests included", Options{Apply: true, IncludeTests: true}, []string{"-apply", "-test", "./..."}},
		{"排除目录 | excluded dirs", Options{ExcludeDirs: []string{"vendor", "testdata", " "}},
			[]string{"-test=false", "-exclude_dirs", "vendor,testdata", "./..."}},
		{"排除文件 | excluded files", Options{ExcludeDirs: []string{"a,b"}, ExcludeFiles: []string{"gen.go"}},
			[]string{"-test=false", "-exclude_dirs", "a,b", "-exclude_files", "gen.go", "./..."}},
		{"自定义目标 | custom targets", Options{Targets: []string{"./version/...", " ./errx/... "}},
			[]string{"-test=false", "./version/...", "./errx/..."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.o.Args(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Args() = %v，期望 %v | want %v", got, tc.want, tc.want)
			}
		})
	}
}

// TestRunParsesDiagnostics replays a fixed analyzer output and counts the diagnostics. | TestRunParsesDiagnostics 回放固定的分析器输出并统计诊断数。
func TestRunParsesDiagnostics(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeTool(t, dir, []string{
		`E:\repo\config\config.go:76:14: struct with 104 pointer bytes could be 96 (lowers GC scan-work estimate)`,
		`E:\repo\router\pattern.go:77:14: 8 bytes saved: struct of size 96 could be 88`,
		`E:\repo\logx\logx.go:48:14: 8 bytes saved: struct of size 88 could be 80`,
		`auth\rbac.go:168:16: cannot use permissionsFrom(r.Permissions) (value of type []Permission) as []string value in struct literal`,
		`betteralign: analysis skipped due to errors in package`,
	}, exitDiagnostics)

	rep, err := Run(context.Background(), Options{Bin: bin, Dir: dir, Targets: []string{"./..."}}, testLogger())
	if err != nil {
		t.Fatalf("Run 返回错误：%v\n%s | Run: %v", err, rep.Output, err)
	}
	if rep.Diagnostics != 3 {
		t.Errorf("Diagnostics = %d，期望 3（编译错误里的 struct literal 不得计入）| want 3", rep.Diagnostics)
	}
	if rep.Applied {
		t.Error("Applied = true，未传 -apply 时应为 false | want false without -apply")
	}
	if rep.Duration <= 0 {
		t.Errorf("Duration = %v，期望正值 | want a positive duration", rep.Duration)
	}
	for _, want := range []string{"pointer bytes", "-test=false", "./..."} {
		if !strings.Contains(rep.Output, want) {
			t.Errorf("Output 缺少 %q：\n%s | Output misses %q", want, rep.Output, want)
		}
	}

	t.Run("应用运行透传 -apply | an apply run forwards -apply", func(t *testing.T) {
		rep, err := Run(context.Background(), Options{Bin: bin, Dir: dir, Apply: true}, testLogger())
		if err != nil {
			t.Fatalf("Run 返回错误：%v | Run: %v", err, err)
		}
		if !rep.Applied {
			t.Error("Applied = false，传了 -apply 时应为 true | want true with -apply")
		}
		if !strings.Contains(rep.Output, "-apply") {
			t.Errorf("Output 未回显 -apply：\n%s | Output misses -apply", rep.Output)
		}
		// 该假工具每遍都回放同样的 3 条诊断：第 2 遍未再下降，循环就此收敛。 | The fake replays the same three diagnostics every pass, so pass 2 shrinks nothing and the loop converges there.
		if rep.Passes != 2 {
			t.Errorf("Passes = %d，期望 2 | want 2", rep.Passes)
		}
		if !rep.Converged {
			t.Error("诊断不再下降即收敛，Converged 应为 true | a non-shrinking pass converges")
		}
	})

	t.Run("退出码 0 表示没有告警 | exit code 0 means no diagnostics", func(t *testing.T) {
		clean := writeFakeTool(t, t.TempDir(), nil, 0)
		rep, err := Run(context.Background(), Options{Bin: clean, Dir: dir}, testLogger())
		if err != nil {
			t.Fatalf("Run 返回错误：%v | Run: %v", err, err)
		}
		if rep.Diagnostics != 0 {
			t.Errorf("Diagnostics = %d，期望 0 | want 0", rep.Diagnostics)
		}
	})

	t.Run("其它非零退出码返回哨兵 | any other exit code returns the sentinel", func(t *testing.T) {
		broken := writeFakeTool(t, t.TempDir(), []string{"boom | 故障"}, 7)
		rep, err := Run(context.Background(), Options{Bin: broken, Dir: dir}, testLogger())
		if !errors.Is(err, ErrRunFailed) {
			t.Fatalf("err = %v，期望 ErrRunFailed | want ErrRunFailed", err)
		}
		if rep == nil {
			t.Fatal("失败运行也必须返回 Report | a failed run must still return a Report")
		}
	})

	t.Run("工具版本过旧时返回专用哨兵 | an outdated tool returns its own sentinel", func(t *testing.T) {
		old := writeFakeTool(t, t.TempDir(), []string{
			`version\version.go:2:1: package requires newer Go version go1.27 (application built with go1.26)`,
		}, 1)
		_, err := Run(context.Background(), Options{Bin: old, Dir: dir}, testLogger())
		if !errors.Is(err, ErrToolTooOld) {
			t.Fatalf("err = %v，期望 ErrToolTooOld | want ErrToolTooOld", err)
		}
		if !strings.Contains(err.Error(), InstallHint) {
			t.Errorf("哨兵文案缺少安装提示：%v | the sentinel does not carry the install hint: %v", err, err)
		}
	})
}

// TestRunMissingToolReturnsSentinel keeps the optional tool from ever blocking a build. | TestRunMissingToolReturnsSentinel 保证可选工具永远不会阻塞构建。
func TestRunMissingToolReturnsSentinel(t *testing.T) {
	t.Setenv(EnvToolBin, "")
	orig := PathLookup
	PathLookup = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { PathLookup = orig })

	rep, err := Run(context.Background(), Options{Dir: t.TempDir()}, testLogger())
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("err = %v，期望 ErrToolNotFound | want ErrToolNotFound", err)
	}
	if rep != nil {
		t.Errorf("解析失败时不得返回 Report：%+v | no Report may be returned", rep)
	}
	if !strings.Contains(err.Error(), InstallHint) {
		t.Errorf("哨兵文案缺少安装提示 %q：%v | missing the install hint", InstallHint, err)
	}
}

// TestApplyIteratesToFixpoint covers every stop rule of the apply loop. | TestApplyIteratesToFixpoint 覆盖 apply 循环的每一条结束规则。
//
// The analyzer's rewrite is not idempotent, so the command must keep applying until the diagnostics reach zero or stop shrinking - and must say so when the pass limit cut the run short. | 分析器的重写并非幂等，因此命令必须持续应用到诊断归零或不再减少为止；当遍数上限截断运行时，也必须说出来。
func TestApplyIteratesToFixpoint(t *testing.T) {
	t.Run("未开启 --apply 时只跑一遍 | a run without --apply executes exactly one pass", func(t *testing.T) {
		dir := t.TempDir()
		bin := writeIteratingTool(t, dir, []int{3, 0})
		rep, err := Run(context.Background(), Options{Bin: bin, Dir: dir}, testLogger())
		if err != nil {
			t.Fatalf("Run 返回错误：%v | Run: %v", err, err)
		}
		if rep.Passes != 1 {
			t.Errorf("Passes = %d，期望 1 | want 1", rep.Passes)
		}
		if rep.Diagnostics != 3 {
			t.Errorf("Diagnostics = %d，期望 3 | want 3", rep.Diagnostics)
		}
		if !rep.Converged {
			t.Error("单遍运行必然算作已收敛 | a single pass must count as converged")
		}
	})

	t.Run("单遍收敛 | one pass converges", func(t *testing.T) {
		dir := t.TempDir()
		bin := writeIteratingTool(t, dir, []int{0})
		rep, err := Run(context.Background(), Options{Bin: bin, Dir: dir, Apply: true}, testLogger())
		if err != nil {
			t.Fatalf("Run 返回错误：%v | Run: %v", err, err)
		}
		if rep.Passes != 1 || rep.Diagnostics != 0 || !rep.Converged {
			t.Errorf("Passes=%d Diagnostics=%d Converged=%v，期望 1/0/true | want 1/0/true",
				rep.Passes, rep.Diagnostics, rep.Converged)
		}
	})

	t.Run("两遍收敛 | two passes converge", func(t *testing.T) {
		dir := t.TempDir()
		bin := writeIteratingTool(t, dir, []int{3, 0})
		rep, err := Run(context.Background(), Options{Bin: bin, Dir: dir, Apply: true}, testLogger())
		if err != nil {
			t.Fatalf("Run 返回错误：%v | Run: %v", err, err)
		}
		if rep.Passes != 2 {
			t.Errorf("Passes = %d，期望 2 | want 2", rep.Passes)
		}
		if rep.Diagnostics != 0 {
			t.Errorf("Diagnostics = %d，期望 0 | want 0", rep.Diagnostics)
		}
		if !rep.Converged {
			t.Error("Converged = false，期望 true | want true")
		}
	})

	t.Run("不再下降即停止 | the loop stops once the count stops shrinking", func(t *testing.T) {
		dir := t.TempDir()
		bin := writeIteratingTool(t, dir, []int{3, 3, 3})
		rep, err := Run(context.Background(), Options{Bin: bin, Dir: dir, Apply: true}, testLogger())
		if err != nil {
			t.Fatalf("Run 返回错误：%v | Run: %v", err, err)
		}
		if rep.Passes != 2 {
			t.Errorf("Passes = %d，期望在第 2 遍停止 | want the loop to stop after pass 2", rep.Passes)
		}
		if rep.Diagnostics != 3 {
			t.Errorf("Diagnostics = %d，期望 3 | want 3", rep.Diagnostics)
		}
		if !rep.Converged {
			t.Error("不再下降属于收敛，Converged 应为 true | no progress is a converged stop")
		}
		if !strings.Contains(rep.Output, "--- pass 2 | 第 2 遍 ---") {
			t.Errorf("多遍输出应带遍次标签：\n%s | multi-pass output must label each pass", rep.Output)
		}
	})

	t.Run("达到上限仍在下降时告警 | hitting the limit while still shrinking warns", func(t *testing.T) {
		dir := t.TempDir()
		bin := writeIteratingTool(t, dir, []int{5, 4, 3, 2, 1})
		var logs bytes.Buffer
		log := logx.New(logx.Options{Writer: &logs, Level: logx.DebugLevel, Format: "console"})

		rep, err := Run(context.Background(), Options{Bin: bin, Dir: dir, Apply: true, MaxPasses: 3}, log)
		if err != nil {
			t.Fatalf("Run 返回错误：%v | Run: %v", err, err)
		}
		if rep.Passes != 3 {
			t.Errorf("Passes = %d，期望用满上限 3 遍 | want the limit of 3", rep.Passes)
		}
		if rep.Diagnostics != 3 {
			t.Errorf("Diagnostics = %d，期望最后一遍的 3 | want the last pass's 3", rep.Diagnostics)
		}
		if rep.Converged {
			t.Error("触及上限时 Converged 应为 false | the limit stop must not report convergence")
		}
		if got := logs.String(); !strings.Contains(got, "pass limit |") {
			t.Errorf("缺少双语告警日志：\n%s | the bilingual warning is missing", got)
		}
	})

	t.Run("默认上限为 5 遍 | the default limit is five passes", func(t *testing.T) {
		dir := t.TempDir()
		bin := writeIteratingTool(t, dir, []int{6, 5, 4, 3, 2, 1})
		rep, err := Run(context.Background(), Options{Bin: bin, Dir: dir, Apply: true}, testLogger())
		if err != nil {
			t.Fatalf("Run 返回错误：%v | Run: %v", err, err)
		}
		if rep.Passes != DefaultMaxPasses {
			t.Errorf("Passes = %d，期望默认上限 %d 遍 | want the default limit of %d", rep.Passes, DefaultMaxPasses, DefaultMaxPasses)
		}
		if rep.Converged {
			t.Error("六步递减用满 5 遍仍未归零，Converged 应为 false | six shrinking steps never reach zero within five passes")
		}
	})
}

// TestRunRealBetteralignOnRepoPackage runs the analyzer for real over one subpackage of this repository. | TestRunRealBetteralignOnRepoPackage 对本仓库的一个子包真实执行分析器。
//
// The tool is optional, so the test skips - printing the reason - when no binary can be resolved, and also when the resolved binary is older than the module's Go version, which the remedy in the hint repairs. | 该工具是可选的：解析不到二进制时跳过并打印原因；解析到的二进制低于模块的 Go 版本时同样跳过，提示中的办法可修复该状态。
func TestRunRealBetteralignOnRepoPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实执行分析器 | skipped in short mode")
	}
	root := repoRoot(t)

	bin, err := Resolve(ToolOptions{LocalDir: root})
	if err != nil {
		t.Skipf("PATH 上未找到 betteralign，跳过真实执行；安装：%s | betteralign not found, skipping the real run: %v", InstallHint, err)
	}
	t.Logf("真实执行 | real run: %s ./version/...", bin)

	rep, err := Run(context.Background(), Options{Bin: bin, Dir: root, Targets: []string{"./version/..."}}, testLogger())
	if errors.Is(err, ErrToolTooOld) {
		// The analyzer really ran - these assertions are the proof - but a binary built with an older Go cannot type-check a module that declares a newer one, so the run is skipped with the remedy from the hint. | 分析器确实运行了（下面几条断言即为证据），但用旧版 Go 构建的二进制无法对声明了更新 Go 版本的模块做类型检查，因此跳过该次运行并给出提示中的修复办法。
		if rep == nil {
			t.Fatal("版本过旧时也必须返回 Report | a too-old run must still return a Report")
		}
		if rep.Duration <= 0 {
			t.Errorf("Duration = %v，期望正值，说明进程确实被启动过 | want a positive duration, proving the process really ran", rep.Duration)
		}
		if !tooOldRe.MatchString(rep.Output) {
			t.Errorf("Output 里没有版本过旧的证据：\n%s | Output carries no version-skew evidence", rep.Output)
		}
		t.Skipf("已安装的 betteralign 比本模块的 Go 版本旧，跳过真实执行；重新安装：%s | the installed betteralign is older than this module's Go version, skipping: %s", InstallHint, InstallHint)
	}
	if err != nil {
		t.Fatalf("真实执行 betteralign 失败：%v\n%s | real betteralign run: %v", err, rep.Output, err)
	}
	if rep == nil {
		t.Fatal("真实执行返回了空 Report | the real run returned a nil Report")
	}
	if rep.Duration <= 0 {
		t.Errorf("Duration = %v，期望正值 | want a positive duration", rep.Duration)
	}
	t.Logf("真实结果 | real result: diagnostics=%d duration=%v duration_ms=%d output=%q",
		rep.Diagnostics, rep.Duration, rep.Duration.Milliseconds(), rep.Output)
}

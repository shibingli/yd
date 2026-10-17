package tools

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shibingli/yd/logx"
)

// The go install targets are quoted here on purpose: the assertions pin the registry, so a target
// that drifts has to fail a test instead of silently moving. | 这里刻意逐字写出 go install 目标：
// 断言固定的是注册表，目标一旦漂移就必须让测试失败，而不是悄悄搬家。
const (
	betteralignTarget = "github.com/dkorunic/betteralign/cmd/betteralign@latest"
	koTarget          = "github.com/google/ko@latest"
)

// TestInstallAllLandsInTheProjectLocalDir is the acceptance test of the plan: the binaries end up in
// <dir>/.yd/tools, GOBIN is absolute and Check turns ready afterwards. | TestInstallAllLandsInTheProjectLocalDir
// 是计划的验收测试：二进制落在 <dir>/.yd/tools，GOBIN 是绝对路径，随后 Check 转为就绪。
func TestInstallAllLandsInTheProjectLocalDir(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t, "go")
	usePATH(path)
	// An inherited GOBIN must never win over the project-local directory. | 继承来的 GOBIN 绝不能压过项目内目录。
	t.Setenv("GOBIN", filepath.Join(t.TempDir(), "elsewhere"))
	exec := &fakeExec{versionOut: "\x1b[33m≈ fake v1.2.3\x1b[0m\nsecond line\n"}
	injectExec(t, exec.run)

	report, err := InstallAll(t.Context(), root, quietLogger())
	if err != nil {
		t.Fatalf("InstallAll 返回错误 %v | InstallAll failed: %[1]v", err)
	}
	if got := statusNames(report.Installed); strings.Join(got, ",") != toolBetteralign+","+toolKo {
		t.Fatalf("Installed = %v，期望 betteralign,ko | want betteralign,ko", got)
	}
	if len(report.Skipped) != 0 || len(report.Failed) != 0 {
		t.Errorf("Skipped = %v，Failed = %v，期望都为空 | both must be empty", statusNames(report.Skipped), failureNames(report.Failed))
	}

	wantDir := filepath.Join(root, ".yd", "tools")
	installs := exec.installCalls()
	if len(installs) != 2 {
		t.Fatalf("执行了 %d 次 go install，期望 2 次 | ran %d go install calls, want 2", len(installs), len(installs))
	}
	for i, want := range []string{betteralignTarget, koTarget} {
		call := installs[i]
		if call.name != path.known["go"] {
			t.Errorf("第 %d 次安装用了 %q，期望 PATH 上的 go %q", i+1, call.name, path.known["go"])
		}
		if got := strings.Join(call.args, " "); got != "install "+want {
			t.Errorf("第 %d 次安装的参数 = %q，期望 %q", i+1, got, "install "+want)
		}
		if call.dir != root {
			t.Errorf("第 %d 次安装的工作目录 = %q，期望 %q", i+1, call.dir, root)
		}
		gobin := envValue(call.env, "GOBIN")
		if !filepath.IsAbs(gobin) {
			t.Errorf("GOBIN = %q，期望绝对路径 | want an absolute path", gobin)
		}
		if got, err := os.Stat(gobin); err != nil {
			t.Errorf("Stat(%q) 失败 | Stat failed: %v", gobin, err)
		} else if wantInfo, err := os.Stat(wantDir); err != nil {
			t.Errorf("Stat(%q) 失败 | Stat failed: %v", wantDir, err)
		} else if !os.SameFile(got, wantInfo) {
			t.Errorf("GOBIN = %q，期望 %q", gobin, wantDir)
		}
		if n := envCount(call.env, "GOBIN"); n != 1 {
			t.Errorf("子进程环境里有 %d 个 GOBIN，期望 1 个 | child env carries %d GOBIN entries, want 1", n, n)
		}
		if got := envValue(call.env, "GOWORK"); got != "off" {
			t.Errorf("GOWORK = %q，期望 off（工作区模式拒绝 package@version）| want off", got)
		}
	}

	for _, name := range []string{toolBetteralign, toolKo} {
		if _, err := os.Stat(localBin(root, name)); err != nil {
			t.Errorf("项目内未找到 %s | %s is not in the project: %v", name, name, err)
		}
	}

	// Versions come from the version query of the registry, ANSI escapes stripped. | 版本来自注册表的版本查询，且已去掉 ANSI 转义。
	if got := report.Installed[0].Version; got != "≈ fake v1.2.3" {
		t.Errorf("betteralign.Version = %q，期望 %q", got, "≈ fake v1.2.3")
	}
	queries := exec.versionCalls()
	if len(queries) != 2 {
		t.Fatalf("执行了 %d 次版本查询，期望 2 次 | ran %d version queries, want 2", len(queries), len(queries))
	}
	for i, want := range []struct {
		binary string
		args   string
	}{
		{toolBetteralign, "-V=full"},
		{toolKo, "version"},
	} {
		if !strings.Contains(queries[i].name, want.binary) {
			t.Errorf("第 %d 次版本查询用了 %q，期望含 %q", i+1, queries[i].name, want.binary)
		}
		if got := strings.Join(queries[i].args, " "); got != want.args {
			t.Errorf("第 %d 次版本查询的参数 = %q，期望 %q", i+1, got, want.args)
		}
	}

	// Acceptance: the probe turns ready without any further configuration. | 验收：无需任何额外配置，探测即转为就绪。
	for _, st := range Check(root) {
		if st.Tool.GoInstall != "" && !st.Found {
			t.Errorf("%s 安装后仍未就绪 | %s is still not ready after installing", st.Tool.Name, st.Tool.Name)
		}
	}
}

// TestInstallRejectsUnknownNameBeforeTouchingAnything pins the fail-fast contract of the plan. | TestInstallRejectsUnknownNameBeforeTouchingAnything 固定计划的快速失败契约。
func TestInstallRejectsUnknownNameBeforeTouchingAnything(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t, "go")
	usePATH(path)
	exec := &fakeExec{}
	injectExec(t, exec.run)

	report, err := Install(t.Context(), root, []string{toolBetteralign, "golangci-lint"}, quietLogger())
	if report != nil {
		t.Errorf("report = %+v，期望 nil | want nil", report)
	}
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err = %v，期望 ErrUnknownTool | want ErrUnknownTool", err)
	}
	if calls := exec.allCalls(); len(calls) != 0 {
		t.Errorf("执行了 %d 条命令，期望 0 条 | ran %d commands, want 0", len(calls), len(calls))
	}
	if _, err := os.Stat(filepath.Join(root, ".yd")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".yd 目录被创建了，未知名称不应有任何副作用 | unknown names must have no side effect")
	}
}

// TestInstallSkipsReadyToolsAndDuplicates covers the cheap paths: nothing to do means no go lookup and
// no process at all. | TestInstallSkipsReadyToolsAndDuplicates 覆盖省事的路径：无事可做就不查找 go，也不启动任何进程。
func TestInstallSkipsReadyToolsAndDuplicates(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t, "go", toolBetteralign, toolDocker)
	usePATH(path)
	exec := &fakeExec{}
	injectExec(t, exec.run)

	report, err := Install(t.Context(), root, []string{"betteralign", "BetterAlign", toolDocker}, quietLogger())
	if err != nil {
		t.Fatalf("Install 返回错误 %v | Install failed: %[1]v", err)
	}
	if got := statusNames(report.Skipped); strings.Join(got, ",") != toolBetteralign+","+toolDocker {
		t.Errorf("Skipped = %v，期望 betteralign,docker | want betteralign,docker", got)
	}
	if len(report.Installed) != 0 || len(report.Failed) != 0 {
		t.Errorf("Installed = %v，Failed = %v，期望都为空 | both must be empty", statusNames(report.Installed), failureNames(report.Failed))
	}
	if calls := exec.allCalls(); len(calls) != 0 {
		t.Errorf("执行了 %d 条命令，期望 0 条 | ran %d commands, want 0", len(calls), len(calls))
	}
	// The toolchain is only looked up when something actually has to be installed. | 只有确实要安装时才查找工具链。
	if path.askedFor("go") {
		t.Errorf("没有待安装项时仍查找了 go | go was looked up although nothing had to be installed")
	}
}

// TestInstallNamesNotInstallableTools keeps the two hand-installed tools out of go install. | TestInstallNamesNotInstallableTools 把两个手动安装的工具挡在 go install 之外。
func TestInstallNamesNotInstallableTools(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t)
	usePATH(path)
	exec := &fakeExec{}
	injectExec(t, exec.run)

	report, err := Install(t.Context(), root, []string{toolTailwindcss}, quietLogger())
	if report == nil {
		t.Fatal("report = nil，期望带上失败条目 | want a report with the failure")
	}
	if got := failureNames(report.Failed); strings.Join(got, ",") != toolTailwindcss {
		t.Fatalf("Failed = %v，期望 tailwindcss | want tailwindcss", got)
	}
	failure := report.Failed[0].Err
	if !errors.Is(failure, ErrNotInstallable) {
		t.Errorf("err = %v，期望 ErrNotInstallable | want ErrNotInstallable", failure)
	}
	if !strings.Contains(failure.Error(), lookupFor(t, toolTailwindcss).InstallHint) {
		t.Errorf("错误未带上获取途径 | the error does not carry the retrieval route: %v", failure)
	}
	if !errors.Is(err, ErrNotInstallable) {
		t.Errorf("Install 返回 %v，期望匹配 ErrNotInstallable | want ErrNotInstallable", err)
	}
	if calls := exec.allCalls(); len(calls) != 0 {
		t.Errorf("执行了 %d 条命令，期望 0 条 | ran %d commands, want 0", len(calls), len(calls))
	}
	if path.askedFor("go") {
		t.Errorf("不可安装的条目不该要求 Go 工具链 | a non-installable tool must not demand Go")
	}
}

// TestInstallWithoutGoToolchain reports the missing toolchain with the way to get it. | TestInstallWithoutGoToolchain 报告缺失的工具链并给出获取方式。
func TestInstallWithoutGoToolchain(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t)
	usePATH(path)
	exec := &fakeExec{}
	injectExec(t, exec.run)

	report, err := InstallAll(t.Context(), root, quietLogger())
	if report != nil {
		t.Errorf("report = %+v，期望 nil | want nil", report)
	}
	if !errors.Is(err, ErrNoGoToolchain) {
		t.Fatalf("err = %v，期望 ErrNoGoToolchain | want ErrNoGoToolchain", err)
	}
	if !strings.Contains(err.Error(), "go.dev/dl") {
		t.Errorf("错误未给出 Go 的安装途径 | the error does not say where to get Go: %v", err)
	}
	if calls := exec.allCalls(); len(calls) != 0 {
		t.Errorf("执行了 %d 条命令，期望 0 条 | ran %d commands, want 0", len(calls), len(calls))
	}
}

// TestInstallContinuesAfterAFailure is the contract of the plan: one tool failing never aborts the
// rest. | TestInstallContinuesAfterAFailure 是计划的契约：单个工具失败绝不中止其余。
func TestInstallContinuesAfterAFailure(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t, "go")
	usePATH(path)
	exec := &fakeExec{failTarget: koTarget, failOutput: "ko: boom\n"}
	injectExec(t, exec.run)

	report, err := InstallAll(t.Context(), root, quietLogger())
	if report == nil {
		t.Fatal("report = nil，期望保留部分结果 | want the partial report")
	}
	if !errors.Is(err, ErrInstallFailed) {
		t.Fatalf("err = %v，期望 ErrInstallFailed | want ErrInstallFailed", err)
	}
	if got := statusNames(report.Installed); strings.Join(got, ",") != toolBetteralign {
		t.Errorf("Installed = %v，期望 betteralign | want betteralign", got)
	}
	if got := failureNames(report.Failed); strings.Join(got, ",") != toolKo {
		t.Errorf("Failed = %v，期望 ko | want ko", got)
	}
	failure := report.Failed[0].Err
	if !errors.Is(failure, ErrInstallFailed) {
		t.Errorf("失败条目 err = %v，期望 ErrInstallFailed | want ErrInstallFailed", failure)
	}
	if !strings.Contains(failure.Error(), "ko: boom") {
		t.Errorf("失败条目未带上输出末尾 | the failure does not carry the output tail: %v", failure)
	}
	if targets := exec.installTargets(); strings.Join(targets, ",") != betteralignTarget+","+koTarget {
		t.Errorf("安装目标 = %v，期望两次尝试都发生 | both attempts must happen", targets)
	}
	if _, err := os.Stat(localBin(root, toolBetteralign)); err != nil {
		t.Errorf("成功的那一个没有落地 | the successful tool did not land: %v", err)
	}
}

// TestInstallFailsWhenTheToolStaysUnresolvable covers the verification step. | TestInstallFailsWhenTheToolStaysUnresolvable 覆盖验证这一步。
func TestInstallFailsWhenTheToolStaysUnresolvable(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t, "go")
	usePATH(path)
	exec := &fakeExec{noCreate: true}
	injectExec(t, exec.run)

	report, err := Install(t.Context(), root, []string{toolKo}, quietLogger())
	if report == nil {
		t.Fatal("report = nil，期望带上失败条目 | want a report with the failure")
	}
	if !errors.Is(err, ErrInstallFailed) {
		t.Fatalf("err = %v，期望 ErrInstallFailed | want ErrInstallFailed", err)
	}
	if len(report.Installed) != 0 {
		t.Errorf("Installed = %v，期望为空 | want empty", statusNames(report.Installed))
	}
	if got := failureNames(report.Failed); strings.Join(got, ",") != toolKo {
		t.Fatalf("Failed = %v，期望 ko | want ko", got)
	}
	if msg := report.Failed[0].Err.Error(); !strings.Contains(msg, "still not resolvable") {
		t.Errorf("失败信息 = %q，期望说明装完仍解析不到 | want the verification verdict", msg)
	}
}

// TestInstallKeepsGoingWhenTheVersionQueryFails keeps the version query best-effort. | TestInstallKeepsGoingWhenTheVersionQueryFails 保证版本查询只尽力而为。
func TestInstallKeepsGoingWhenTheVersionQueryFails(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t, "go")
	usePATH(path)
	exec := &fakeExec{failVersion: true}
	injectExec(t, exec.run)

	report, err := Install(t.Context(), root, []string{toolBetteralign}, quietLogger())
	if err != nil {
		t.Fatalf("Install 返回错误 %v，版本查询失败不得影响安装 | the version query must not fail the install", err)
	}
	if got := statusNames(report.Installed); strings.Join(got, ",") != toolBetteralign {
		t.Fatalf("Installed = %v，期望 betteralign | want betteralign", got)
	}
	if got := report.Installed[0].Version; got != "" {
		t.Errorf("Version = %q，查询失败时留空 | it must stay empty when the query fails", got)
	}
}

// TestInstallReportsUnexpectedResolutionFailure keeps a broken chain visible in the report. | TestInstallReportsUnexpectedResolutionFailure 让损坏的解析链在报告中可见。
func TestInstallReportsUnexpectedResolutionFailure(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t, "go")
	usePATH(path)
	errBoom := errors.New("resolution exploded")
	fallback := resolveTool
	resolveTool = func(tool Tool, dir string) (string, error) {
		if tool.Name == toolBetteralign {
			return "", errBoom
		}
		return fallback(tool, dir)
	}
	exec := &fakeExec{}
	injectExec(t, exec.run)

	report, err := InstallAll(t.Context(), root, quietLogger())
	if report == nil {
		t.Fatal("report = nil，期望带上失败条目 | want a report with the failure")
	}
	if !errors.Is(err, ErrInstallFailed) || !errors.Is(err, errBoom) {
		t.Fatalf("err = %v，期望同时匹配 ErrInstallFailed 与原始错误 | want both on the chain", err)
	}
	if got := failureNames(report.Failed); strings.Join(got, ",") != toolBetteralign {
		t.Errorf("Failed = %v，期望 betteralign | want betteralign", got)
	}
	if got := statusNames(report.Installed); strings.Join(got, ",") != toolKo {
		t.Errorf("Installed = %v，期望 ko 仍然安装成功 | ko must still be installed", got)
	}
}

// TestInstallUsesAnAbsoluteGobinForARelativeRoot pins the absoluteness requirement. | TestInstallUsesAnAbsoluteGobinForARelativeRoot 固定绝对路径的要求。
func TestInstallUsesAnAbsoluteGobinForARelativeRoot(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	t.Chdir(root)
	path := newFakePATH(t, "go")
	usePATH(path)
	exec := &fakeExec{}
	injectExec(t, exec.run)

	report, err := Install(t.Context(), ".", []string{toolBetteralign}, quietLogger())
	if err != nil {
		t.Fatalf("Install 返回错误 %v | Install failed: %[1]v", err)
	}
	if got := statusNames(report.Installed); strings.Join(got, ",") != toolBetteralign {
		t.Fatalf("Installed = %v，期望 betteralign | want betteralign", got)
	}
	installs := exec.installCalls()
	if len(installs) != 1 {
		t.Fatalf("执行了 %d 次 go install，期望 1 次 | ran %d calls, want 1", len(installs), len(installs))
	}
	gobin := envValue(installs[0].env, "GOBIN")
	if !filepath.IsAbs(gobin) {
		t.Fatalf("GOBIN = %q，期望绝对路径 | want an absolute path", gobin)
	}
	if !samePath(t, gobin, filepath.Join(root, ".yd", "tools")) {
		t.Errorf("GOBIN = %q，期望 %q", gobin, filepath.Join(root, ".yd", "tools"))
	}
}

// fakeExec is the injected ExecCommandFunc: it records every call and plays the part of go install by
// creating the binary in the GOBIN it was handed. | fakeExec 是注入的 ExecCommandFunc：记录每次调用，
// 并通过在收到的 GOBIN 里创建二进制来扮演 go install 的角色。
type fakeExec struct {
	mu          sync.Mutex
	calls       []execCall
	failTarget  string
	failOutput  string
	versionOut  string
	failVersion bool
	noCreate    bool
}

// execCall records one command the installer asked for. | execCall 记录安装器请求执行的一条命令。
type execCall struct {
	name string
	args []string
	dir  string
	env  []string
}

// run implements ExecCommandFunc. | run 实现 ExecCommandFunc。
func (f *fakeExec) run(_ context.Context, name string, args []string, dir string, env []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, execCall{
		name: name,
		args: append([]string(nil), args...),
		dir:  dir,
		env:  append([]string(nil), env...),
	})
	if len(args) == 0 {
		return nil, nil
	}
	if args[0] != "install" {
		if f.failVersion {
			return []byte(f.versionOut), errors.New("version query failed")
		}
		return []byte(f.versionOut), nil
	}
	target := ""
	if len(args) > 1 {
		target = args[1]
	}
	if f.failTarget != "" && target == f.failTarget {
		return []byte(f.failOutput), errors.New("exit status 1")
	}
	if f.noCreate {
		return []byte("installed anyway\n"), nil
	}
	if err := f.createBinary(target, envValue(env, "GOBIN")); err != nil {
		return nil, err
	}
	return []byte("installed\n"), nil
}

// createBinary writes the placeholder binary of target into gobin, the way go install would. | createBinary
// 像 go install 那样把 target 的占位二进制写入 gobin。
func (f *fakeExec) createBinary(target, gobin string) error {
	name := path.Base(strings.SplitN(target, "@", 2)[0])
	if name == "" || name == "." || gobin == "" {
		return errors.New("fake executor: nothing to create | fake executor：没有可创建的目标")
	}
	if err := os.MkdirAll(gobin, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(gobin, name+exeSuffix()), []byte("fake binary"), 0o755)
}

// installCalls returns the recorded go install invocations. | installCalls 返回记录的 go install 调用。
func (f *fakeExec) installCalls() []execCall {
	return f.filterCalls(true)
}

// versionCalls returns the recorded version queries. | versionCalls 返回记录的版本查询。
func (f *fakeExec) versionCalls() []execCall {
	return f.filterCalls(false)
}

// allCalls returns every recorded invocation. | allCalls 返回记录的每一次调用。
func (f *fakeExec) allCalls() []execCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execCall(nil), f.calls...)
}

// installTargets returns the go install targets in call order. | installTargets 按调用顺序返回 go install 目标。
func (f *fakeExec) installTargets() []string {
	calls := f.installCalls()
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		if len(call.args) > 1 {
			out = append(out, call.args[1])
		}
	}
	return out
}

// filterCalls keeps the install or the version invocations. | filterCalls 保留安装或版本查询的调用。
func (f *fakeExec) filterCalls(install bool) []execCall {
	out := make([]execCall, 0, len(f.calls))
	for _, call := range f.allCalls() {
		isInstall := len(call.args) > 0 && call.args[0] == "install"
		if isInstall == install {
			out = append(out, call)
		}
	}
	return out
}

// injectExec swaps the command runner for the duration of one test. | injectExec 在单个测试期间替换命令执行器。
func injectExec(t *testing.T, fn ExecCommandFunc) {
	t.Helper()
	prev := ExecCommand
	ExecCommand = fn
	t.Cleanup(func() { ExecCommand = prev })
}

// envValue returns the last value of key in a recorded child environment. | envValue 返回记录的子进程环境中 key 的最后一个取值。
func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// envCount counts the entries of key in a recorded child environment. | envCount 统计记录的子进程环境中 key 的条目数。
func envCount(env []string, key string) int {
	n := 0
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			n++
		}
	}
	return n
}

// quietLogger discards every record so the assertions stay readable. | quietLogger 丢弃所有记录，使断言保持可读。
func quietLogger() *logx.Logger {
	return logx.New(logx.Options{Writer: io.Discard})
}

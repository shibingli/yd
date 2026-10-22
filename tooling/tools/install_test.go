package tools

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shibingli/ydx/log"
)

// The go install targets are quoted here on purpose: the assertions pin the registry, so a target
// that drifts has to fail a test instead of silently moving. | 这里刻意逐字写出 go install 目标：
// 断言固定的是注册表，目标一旦漂移就必须让测试失败，而不是悄悄搬家。
const (
	betteralignTarget = "github.com/dkorunic/betteralign/cmd/betteralign@latest"
	koTarget          = "github.com/google/ko@latest"
	minifyTarget      = "github.com/tdewolff/minify/v2/cmd/minify@latest"
)

// TestInstallWithSkipsConfiguredBinariesWithoutGoToolchain proves the installer shares the probe's
// configuration: both installable tools are configured, so nothing needs installing and therefore
// no Go toolchain is required. | TestInstallWithSkipsConfiguredBinariesWithoutGoToolchain 证明安装器
// 与探测共享配置：两个可安装工具都已配置，因此无需安装，也就不需要 Go 工具链。
func TestInstallWithSkipsConfiguredBinariesWithoutGoToolchain(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	// PATH has neither go nor any tool; only the configured paths can resolve them. | PATH 上既没有 go
	// 也没有任何工具，只有配置路径能解析它们。
	usePATH(newFakePATH(t))
	bins := make(map[string]string, 3)
	for _, name := range []string{NameBetteralign, NameKo, NameMinify} {
		p := filepath.Join(root, "configured-"+name)
		stubFile(t, p)
		bins[name] = p
	}
	exec := &fakeExec{}
	injectExec(t, exec.run)

	report, err := InstallWith(t.Context(), Options{Dir: root, Binaries: bins}, nil, quietLogger())
	if err != nil {
		t.Fatalf("InstallWith 返回错误 | InstallWith failed: %v", err)
	}
	if got := statusNames(report.Skipped); strings.Join(got, ",") != NameBetteralign+","+NameKo+","+NameMinify {
		t.Fatalf("Skipped = %v，期望 %v | want %v", got,
			[]string{NameBetteralign, NameKo, NameMinify}, []string{NameBetteralign, NameKo, NameMinify})
	}
	if len(report.Installed) != 0 || len(report.Failed) != 0 {
		t.Errorf("Installed = %v，Failed = %v，期望都为空 | both must be empty",
			statusNames(report.Installed), failureNames(report.Failed))
	}
	if calls := exec.allCalls(); len(calls) != 0 {
		t.Errorf("执行了 %d 条命令，期望 0 条 | ran %d commands, want 0", len(calls), len(calls))
	}
}

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
	if got := statusNames(report.Installed); strings.Join(got, ",") != toolBetteralign+","+toolKo+","+toolMinify {
		t.Fatalf("Installed = %v，期望 betteralign,ko,minify | want betteralign,ko,minify", got)
	}
	if len(report.Skipped) != 0 || len(report.Failed) != 0 {
		t.Errorf("Skipped = %v，Failed = %v，期望都为空 | both must be empty", statusNames(report.Skipped), failureNames(report.Failed))
	}

	wantDir := filepath.Join(root, ".yd", "tools")
	installs := exec.installCalls()
	if len(installs) != 3 {
		t.Fatalf("执行了 %d 次 go install，期望 3 次 | ran %d go install calls, want 3", len(installs), len(installs))
	}
	for i, want := range []string{betteralignTarget, koTarget, minifyTarget} {
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

	for _, name := range []string{toolBetteralign, toolKo, toolMinify} {
		if _, err := os.Stat(localBin(root, name)); err != nil {
			t.Errorf("项目内未找到 %s | %s is not in the project: %v", name, name, err)
		}
	}

	// Versions come from the version query of the registry, ANSI escapes stripped. | 版本来自注册表的版本查询，且已去掉 ANSI 转义。
	if got := report.Installed[0].Version; got != "≈ fake v1.2.3" {
		t.Errorf("betteralign.Version = %q，期望 %q", got, "≈ fake v1.2.3")
	}
	queries := exec.versionCalls()
	if len(queries) != 3 {
		t.Fatalf("执行了 %d 次版本查询，期望 3 次 | ran %d version queries, want 3", len(queries), len(queries))
	}
	for i, want := range []struct {
		binary string
		args   string
	}{
		{toolBetteralign, "-V=full"},
		{toolKo, "version"},
		{toolMinify, "--version"},
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

// TestInstallNamesNotInstallableTools keeps the hand-installed docker tool out of go install. |
// TestInstallNamesNotInstallableTools 把手动安装的 docker 工具挡在 go install 之外。
func TestInstallNamesNotInstallableTools(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	path := newFakePATH(t)
	usePATH(path)
	exec := &fakeExec{}
	injectExec(t, exec.run)

	report, err := Install(t.Context(), root, []string{toolDocker}, quietLogger())
	if report == nil {
		t.Fatal("report = nil，期望带上失败条目 | want a report with the failure")
	}
	if got := failureNames(report.Failed); strings.Join(got, ",") != toolDocker {
		t.Fatalf("Failed = %v，期望 docker | want docker", got)
	}
	failure := report.Failed[0].Err
	if !errors.Is(failure, ErrNotInstallable) {
		t.Errorf("err = %v，期望 ErrNotInstallable | want ErrNotInstallable", failure)
	}
	if !strings.Contains(failure.Error(), lookupFor(t, toolDocker).InstallHint) {
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
	if got := statusNames(report.Installed); strings.Join(got, ",") != toolBetteralign+","+toolMinify {
		t.Errorf("Installed = %v，期望 betteralign,minify | want betteralign,minify", got)
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
	if targets := exec.installTargets(); strings.Join(targets, ",") != betteralignTarget+","+koTarget+","+minifyTarget {
		t.Errorf("安装目标 = %v，期望三次尝试都发生 | all three attempts must happen", targets)
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
	resolveTool = func(tool Tool, o Options) (string, error) {
		if tool.Name == toolBetteralign {
			return "", errBoom
		}
		return fallback(tool, o)
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
	if got := statusNames(report.Installed); strings.Join(got, ",") != toolKo+","+toolMinify {
		t.Errorf("Installed = %v，期望 ko、minify 仍然安装成功 | ko and minify must still be installed", got)
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
func quietLogger() *log.Logger {
	return log.New(log.Options{Writer: io.Discard})
}

// TestInstallEnvIsolatesGoCaches proves go install cannot write into the user's global Go caches,
// and that the default proxy chain and checksum policy are applied. | TestInstallEnvIsolatesGoCaches
// 证明 go install 无法写入用户的全局 Go 缓存，且默认代理链与校验策略生效。
func TestInstallEnvIsolatesGoCaches(t *testing.T) {
	t.Setenv("GOPROXY", "")
	t.Setenv("GOSUMDB", "")
	t.Setenv("GOCACHE", "/inherited/go-build")
	t.Setenv("GOPATH", "/inherited/gopath")
	t.Setenv("GOMODCACHE", "/inherited/mod")
	t.Setenv("GOBIN", "/inherited/bin")
	t.Setenv("GOWORK", "on")

	dir := t.TempDir()
	env := installEnv(dir)
	for _, tc := range []struct{ key, want string }{
		{"GOBIN", dir},
		{"GOWORK", "off"},
		{"GOPATH", dir},
		{"GOMODCACHE", filepath.Join(dir, "pkg", "mod")},
		{"GOCACHE", filepath.Join(dir, "go_cache")},
		{"GOPROXY", DefaultGOPROXY},
		{"GOSUMDB", DefaultGOSUMDB},
	} {
		if got := envValue(env, tc.key); got != tc.want {
			t.Errorf("%s = %q，期望 %q | want %q", tc.key, got, tc.want, tc.want)
		}
		if n := envCount(env, tc.key); n != 1 {
			t.Errorf("%s 出现 %d 次，期望 1 次 | appears %d times, want 1", tc.key, n, n)
		}
	}
}

// TestInstallEnvHonoursExplicitProxy keeps an explicitly exported GOPROXY/GOSUMDB instead of
// replacing it with the defaults. | TestInstallEnvHonoursExplicitProxy 保留显式导出的
// GOPROXY/GOSUMDB，而不是用默认值覆盖。
func TestInstallEnvHonoursExplicitProxy(t *testing.T) {
	t.Setenv("GOPROXY", "https://proxy.example.com,direct")
	t.Setenv("GOSUMDB", "sum.golang.org")
	env := installEnv(t.TempDir())
	if got := envValue(env, "GOPROXY"); got != "https://proxy.example.com,direct" {
		t.Errorf("GOPROXY = %q，期望显式取值 | want the explicit value", got)
	}
	if got := envValue(env, "GOSUMDB"); got != "sum.golang.org" {
		t.Errorf("GOSUMDB = %q，期望显式取值 | want the explicit value", got)
	}
}

// TestInstallTailwindDownloadsBinary proves tailwindcss is installed by downloading a release
// asset, with no Go toolchain and no `go install` invocation. | TestInstallTailwindDownloadsBinary
// 证明 tailwindcss 通过下载发布资产安装，不需要 Go 工具链，也不执行 `go install`。
func TestInstallTailwindDownloadsBinary(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	usePATH(newFakePATH(t)) // neither go nor tailwindcss on PATH | PATH 上既没有 go 也没有 tailwindcss
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fake tailwind binary"))
	}))
	defer server.Close()
	exec := &fakeExec{versionOut: "4.1.0\n"}
	injectExec(t, exec.run)

	report, err := InstallWith(t.Context(), Options{
		Dir:         root,
		TailwindURL: server.URL + "/tailwindcss",
	}, []string{NameTailwindcss}, quietLogger())
	if err != nil {
		t.Fatalf("InstallWith 返回错误 | InstallWith failed: %v", err)
	}
	if got := statusNames(report.Installed); strings.Join(got, ",") != NameTailwindcss {
		t.Fatalf("Installed = %v，期望 tailwindcss | want tailwindcss", got)
	}
	bin := filepath.Join(root, ".yd", "tools", "tailwindcss"+exeSuffix())
	data, readErr := os.ReadFile(bin)
	if readErr != nil {
		t.Fatalf("下载的二进制不存在 | downloaded binary is missing: %v", readErr)
	}
	if string(data) != "fake tailwind binary" {
		t.Fatalf("二进制内容 = %q | unexpected content", string(data))
	}
	if calls := exec.installCalls(); len(calls) != 0 {
		t.Errorf("tailwindcss 不应执行 go install | tailwindcss must not run go install: %d calls", len(calls))
	}
}

// TestInstallTailwindUsesConfiguredProxy proves an explicit proxy is used for the download: the
// target host does not resolve, so only the proxy can serve the bytes. | TestInstallTailwindUsesConfiguredProxy
// 证明下载会使用显式代理：目标主机无法解析，只有代理能提供字节。
func TestInstallTailwindUsesConfiguredProxy(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	usePATH(newFakePATH(t))
	var seen atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tailwindcss" {
			seen.Store(true)
		}
		_, _ = w.Write([]byte("via proxy"))
	}))
	defer proxy.Close()
	exec := &fakeExec{}
	injectExec(t, exec.run)

	if _, err := InstallWith(t.Context(), Options{
		Dir:         root,
		TailwindURL: "http://example.invalid/tailwindcss",
		Proxy:       proxy.URL,
	}, []string{NameTailwindcss}, quietLogger()); err != nil {
		t.Fatalf("InstallWith 返回错误 | InstallWith failed: %v", err)
	}
	if !seen.Load() {
		t.Fatal("代理未被使用 | the proxy was not used")
	}
	data, err := os.ReadFile(filepath.Join(root, ".yd", "tools", "tailwindcss"+exeSuffix()))
	if err != nil || string(data) != "via proxy" {
		t.Fatalf("下载结果 = %q, err = %v | download result", data, err)
	}
}

// TestInstallTailwindRejectsEmptyDownload keeps a zero-byte mirror response from leaving an
// executable-looking placeholder. | TestInstallTailwindRejectsEmptyDownload 阻止零字节的镜像响应留下
// 一个看似可执行的占位文件。
func TestInstallTailwindRejectsEmptyDownload(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	usePATH(newFakePATH(t))
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	injectExec(t, (&fakeExec{}).run)

	report, err := InstallWith(t.Context(), Options{
		Dir:         root,
		TailwindURL: server.URL + "/tailwindcss",
	}, []string{NameTailwindcss}, quietLogger())
	if !errors.Is(err, ErrInstallFailed) {
		t.Fatalf("err = %v，期望 ErrInstallFailed | want ErrInstallFailed", err)
	}
	if report == nil || len(report.Failed) != 1 {
		t.Fatalf("report = %+v，期望一条失败 | want one failure", report)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".yd", "tools", "tailwindcss"+exeSuffix())); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("空下载不应留下文件 | an empty download must leave no file: %v", statErr)
	}
}

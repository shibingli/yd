// This file covers the tools command and its two integration points with `yd new`: the status table,
// the installation summary, the usage errors, and the package-wide guard that keeps the whole cli
// test binary away from the optional external tools of the machine it runs on. | 本文件覆盖 tools 命令及其与
// `yd new` 的两处集成：状态表、安装摘要、用法错误，以及让整个 cli 测试二进制远离所在机器上可选外部工具的包级守卫。

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shibingli/yd/align"
	"github.com/shibingli/yd/ko"
	"github.com/shibingli/yd/toolchain"
	"github.com/shibingli/yd/tools"
)

// The registry names the tests below rely on. | 下方测试所依赖的注册表名称。
const (
	// cliToolBetteralign is the optional analyzer. | cliToolBetteralign 是可选分析器。
	cliToolBetteralign = "betteralign"
	// cliToolKo is the optional image builder. | cliToolKo 是可选镜像构建器。
	cliToolKo = "ko"
	// cliToolTailwind is the optional CSS compiler, which cannot be installed by go install. | cliToolTailwind 是可选 CSS 编译器，无法用 go install 安装。
	cliToolTailwind = "tailwindcss"
	// cliToolDocker is the optional container engine, which cannot be installed by go install. | cliToolDocker 是可选容器引擎，无法用 go install 安装。
	cliToolDocker = "docker"
)

// TestMain makes the whole cli test binary independent of the machine it runs on. | TestMain 让整个 cli 测试二进制与所在机器无关。
//
// Two things would otherwise leak in. A probe would find whichever betteralign, ko or tailwindcss the
// developer happens to have installed, so the status assertions would pass or fail by accident; and
// every `yd new` test that answers the interview with defaults would reach tools.InstallAll and start
// a real `go install` - a network side effect inside `go test`, with GOBIN pointing into a temporary
// directory. The stubs are inert: a lookup finds nothing, and a command runner that a test did not
// inject on purpose fails instead of executing anything. | 否则会有两处泄漏。探测会找到开发者本机恰好装了的
// betteralign、ko 或 tailwindcss，使状态断言靠碰运气通过；而每个用默认值回答问答的 `yd new` 测试都会走到
// tools.InstallAll 并启动真实的 `go install`——`go test` 里的网络副作用，GOBIN 还指向临时目录。
// 这些桩是惰性的：查找一律找不到，未被测试刻意注入的命令执行器直接失败而不执行任何东西。
func TestMain(m *testing.M) {
	// The three resolver environment variables would otherwise win over the stubs below. | 三个解析器环境变量
	// 否则会胜过下面的桩。
	_ = os.Setenv(align.EnvToolBin, "")
	_ = os.Setenv(ko.EnvBinary, "")
	_ = os.Setenv(toolchain.EnvTailwindBin, "")

	tools.PathLookup = func(string) (string, error) { return "", os.ErrNotExist }
	ko.PathLookup = func(string) (string, error) { return "", os.ErrNotExist }
	tools.ExecCommand = func(context.Context, string, []string, string, []string) ([]byte, error) {
		return nil, errors.New("cli test: no external command may run | cli 测试：不得运行任何外部命令")
	}
	os.Exit(m.Run())
}

// newToolBin is the project-local path a tool is installed into. | newToolBin 是工具被安装到的项目内路径。
func newToolBin(projectDir, name string) string {
	return filepath.Join(projectDir, filepath.FromSlash(tools.LocalToolDir), name+cliExeSuffix())
}

// stubToolFile writes the placeholder executable the resolvers look for. | stubToolFile 写入解析器所查找的占位可执行文件。
func stubToolFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) 失败 | MkdirAll(%q) failed: %v", filepath.Dir(path), filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("stub"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) 失败 | WriteFile(%q) failed: %v", path, path, err)
	}
}

// cliFakePath is a PathLookup over a fixed command set that also records what was asked for. | cliFakePath
// 是在固定命令集合上的 PathLookup，同时记录被询问过的命令。
type cliFakePath struct {
	mu    sync.Mutex
	known map[string]string
	asked []string
}

// newCliFakePath builds a lookup that knows exactly names. | newCliFakePath 构建一个只认识 names 的查找器。
func newCliFakePath(t *testing.T, names ...string) *cliFakePath {
	t.Helper()
	known := make(map[string]string, len(names))
	for _, name := range names {
		known[name] = filepath.Join(os.TempDir(), "yd-cli-stub", name+cliExeSuffix())
	}
	return &cliFakePath{known: known}
}

// lookup resolves a bare command name against the fixed set. | lookup 在固定集合上解析裸命令名。
func (f *cliFakePath) lookup(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, name)
	if p, ok := f.known[name]; ok {
		return p, nil
	}
	return "", os.ErrNotExist
}

// at returns the path the stub reports for name. | at 返回桩为 name 报告的路经。
func (f *cliFakePath) at(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.known[name]
}

// askedFor reports whether name was ever looked up. | askedFor 报告 name 是否被查找过。
func (f *cliFakePath) askedFor(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, asked := range f.asked {
		if asked == name {
			return true
		}
	}
	return false
}

// useToolPath points both injected lookups at f for the duration of one test. | useToolPath 在单个测试期间把两处注入的查找都指向 f。
//
// The tools package owns one lookup variable and ko owns another, because ko resolves through a
// package-level var; keeping them together in one helper means a test can never stub only half of the
// chain. | tools 包拥有一个查找变量，ko 拥有另一个（ko 经包级变量解析）；把它们放进同一个辅助函数，
// 测试就绝不会只替换链条的一半。
func useToolPath(t *testing.T, f *cliFakePath) {
	t.Helper()
	prevTools, prevKo := tools.PathLookup, ko.PathLookup
	tools.PathLookup, ko.PathLookup = f.lookup, f.lookup
	t.Cleanup(func() { tools.PathLookup, ko.PathLookup = prevTools, prevKo })
}

// cliInstallCall records one command the installer ran. | cliInstallCall 记录安装器运行过的一条命令。
type cliInstallCall struct {
	name string
	args []string
	dir  string
	env  []string
}

// cliFakeInstall plays the part of the go command: it records the call and creates the binary inside
// the GOBIN it was handed, which is what makes the resolvers find the tool afterwards. | cliFakeInstall
// 扮演 go 命令：记录调用，并在收到的 GOBIN 里创建二进制——正因如此解析器随后才能找到该工具。
type cliFakeInstall struct {
	mu    sync.Mutex
	calls []cliInstallCall
	// fail is returned by every install invocation when it is set. | fail 设置后，每次安装调用都返回它。
	fail error
	// skipCreate leaves the binary unwritten, to model an install that "succeeds" but produces nothing. | skipCreate
	// 不写入二进制，用于模拟「成功」却什么都没产出的安装。
	skipCreate bool
}

// run is the injected tools.ExecCommandFunc. | run 是注入的 tools.ExecCommandFunc。
func (f *cliFakeInstall) run(_ context.Context, name string, args []string, dir string, env []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cliInstallCall{
		name: name,
		args: append([]string(nil), args...),
		dir:  dir,
		env:  append([]string(nil), env...),
	})
	// A call that is not `go install` is the version query of a freshly installed tool. | 不是 `go install`
	// 的调用是刚安装工具的版本查询。
	if len(args) > 0 && args[0] != "install" {
		return []byte("fake-version 9.9.9\n"), nil
	}
	if f.fail != nil {
		return []byte("go: download failed\n"), f.fail
	}
	if f.skipCreate {
		return []byte("nothing produced\n"), nil
	}
	if len(args) < 2 {
		return nil, errors.New("fake go: install without a target | 假 go：安装缺少目标")
	}
	name2 := path.Base(args[1])
	if i := strings.IndexByte(name2, '@'); i >= 0 {
		name2 = name2[:i]
	}
	bin := filepath.Join(envValue(env, "GOBIN"), name2+cliExeSuffix())
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(bin, []byte("fake binary"), 0o755); err != nil {
		return nil, err
	}
	return []byte("installed " + name2 + "\n"), nil
}

// installs returns the recorded installation calls, in order. | installs 按顺序返回记录到的安装调用。
func (f *cliFakeInstall) installs() []cliInstallCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cliInstallCall
	for _, c := range f.calls {
		if len(c.args) > 0 && c.args[0] == "install" {
			out = append(out, c)
		}
	}
	return out
}

// count returns how many commands were run in total. | count 返回总共运行了多少条命令。
func (f *cliFakeInstall) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// useToolExec injects the fake go command for the duration of one test. | useToolExec 在单个测试期间注入假 go 命令。
func useToolExec(t *testing.T, f *cliFakeInstall) {
	t.Helper()
	prev := tools.ExecCommand
	tools.ExecCommand = f.run
	t.Cleanup(func() { tools.ExecCommand = prev })
}

// openToolsEnv builds an environment whose working directory is a real temporary directory, because
// the installer writes into <workdir>/.yd/tools on the real file system by design. | openToolsEnv
// 构造工作目录为真实临时目录的环境，因为安装器按设计把文件写进真实文件系统的 <workdir>/.yd/tools。
func openToolsEnv(t *testing.T, in string, tty bool) *testEnv {
	t.Helper()
	te := newTestEnv(t, nil, in, tty)
	te.env.WorkDir = t.TempDir()
	return te
}

// TestToolsCommandRegistered pins the registration of the tools command. | TestToolsCommandRegistered 固定 tools 命令的注册。
func TestToolsCommandRegistered(t *testing.T) {
	count := 0
	for _, c := range Commands() {
		if c.Name != toolsCommandName {
			continue
		}
		count++
		if c.Short == "" || c.Usage == "" || c.Run == nil || c.Flags == nil {
			t.Errorf("tools 命令缺少 Short/Usage/Run/Flags | the command is incomplete: %+v", c)
		}
		if !strings.Contains(c.Short, " | ") {
			t.Errorf("tools 命令的简述未中英同步 | the short help is not bilingual: %q", c.Short)
		}
		if c.Long == "" {
			t.Error("tools 命令缺少长说明 | the long help is missing")
		}
		if !strings.Contains(c.Usage, toolsActionCheck) || !strings.Contains(c.Usage, toolsActionInstall) {
			t.Errorf("用法行未列出两个动作 | the usage line misses an action: %q", c.Usage)
		}
	}
	if count != 1 {
		t.Fatalf("tools 命令注册了 %d 次，期望 1 次 | registered %d times, want 1", count, count)
	}
}

// TestToolsCheckPrintsTableAndHints pins the whole `yd tools` output: one row per tool, the ready
// ones with their path, the missing one as a dash, and a hint block below. | TestToolsCheckPrintsTableAndHints
// 固定 `yd tools` 的完整输出：每个工具一行，就绪的带路径，缺失的显示短横线，下方是提示块。
func TestToolsCheckPrintsTableAndHints(t *testing.T) {
	te := openToolsEnv(t, "", false)
	pathStub := newCliFakePath(t, cliToolBetteralign, cliToolDocker)
	useToolPath(t, pathStub)
	exec := &cliFakeInstall{}
	useToolExec(t, exec)
	// ko is ready through a real project-local copy, which outranks PATH. | ko 通过项目内的真实副本就绪，其优先级高于 PATH。
	localKo := newToolBin(te.env.WorkDir, cliToolKo)
	stubToolFile(t, localKo)

	if err := Execute(context.Background(), []string{toolsCommandName}, te.env); err != nil {
		t.Fatalf("yd tools 失败 | yd tools failed: %v\n%s", err, te.errs.String())
	}

	out := te.out.String()
	if !strings.Contains(out, "tools | 工具: 4") {
		t.Errorf("状态表缺少表头 | the table misses its header:\n%s", out)
	}
	for _, want := range []struct {
		name   string
		state  string
		detail string
	}{
		{cliToolBetteralign, toolsStateReady, pathStub.at(cliToolBetteralign)},
		{cliToolKo, toolsStateReady, localKo},
		{cliToolTailwind, toolsStateMissing, "-"},
		{cliToolDocker, toolsStateReady, pathStub.at(cliToolDocker)},
	} {
		row := fmt.Sprintf("  %-13s %s %s", want.name, want.state, want.detail)
		if !strings.Contains(out, row) {
			t.Errorf("状态表缺少一行 %q | the table misses the row:\n%s", row, out)
		}
	}
	for _, want := range []string{"hints | 提示:", tools.LocalToolDir, "yd tools install"} {
		if !strings.Contains(out, want) {
			t.Errorf("提示块缺少 %q | the hint block misses %q:\n%s", want, want, out)
		}
	}
	if strings.Contains(out, cliToolKo+": ") {
		t.Errorf("就绪的工具不该出现在提示里 | a ready tool must not be hinted:\n%s", out)
	}
	// Probing is read-only: no tool is executed and nothing reaches the network. | 探测是只读的：不执行工具，不触网。
	if got := exec.count(); got != 0 {
		t.Errorf("check 运行了 %d 条命令，期望 0 条 | check ran %d commands, want 0", got, got)
	}
}

// TestToolsCheckAcceptsItsActionWord makes the explicit word behave exactly like the default. | TestToolsCheckAcceptsItsActionWord 让显式动作词与默认行为完全一致。
func TestToolsCheckAcceptsItsActionWord(t *testing.T) {
	te := openToolsEnv(t, "", false)
	useToolPath(t, newCliFakePath(t))
	exec := &cliFakeInstall{}
	useToolExec(t, exec)

	if err := Execute(context.Background(), []string{toolsCommandName, toolsActionCheck}, te.env); err != nil {
		t.Fatalf("yd tools check 失败 | yd tools check failed: %v", err)
	}
	if !strings.Contains(te.out.String(), "tools | 工具: 4") {
		t.Errorf("状态表未打印 | the table was not printed:\n%s", te.out.String())
	}
	if got := exec.count(); got != 0 {
		t.Errorf("check 运行了 %d 条命令，期望 0 条 | check ran %d commands, want 0", got, got)
	}
}

// TestToolsInstallDefaultsToEveryInstallableTool pins the whole installation run: the two Go-installable
// tools, their GOBIN, and the summary. | TestToolsInstallDefaultsToEveryInstallableTool 固定整次安装运行：
// 两个可用 go install 安装的工具、它们的 GOBIN 与摘要。
func TestToolsInstallDefaultsToEveryInstallableTool(t *testing.T) {
	te := openToolsEnv(t, "", false)
	pathStub := newCliFakePath(t, "go", cliToolDocker)
	useToolPath(t, pathStub)
	exec := &cliFakeInstall{}
	useToolExec(t, exec)
	// docker is on PATH, and it must stay out of the plan: `go install` cannot provide it. | docker 位于
	// PATH 上，且必须留在计划之外：`go install` 无法提供它。
	stubToolFile(t, newToolBin(te.env.WorkDir, cliToolDocker))

	if err := Execute(context.Background(), []string{toolsCommandName, toolsActionInstall}, te.env); err != nil {
		t.Fatalf("yd tools install 失败 | yd tools install failed: %v\n%s", err, te.errs.String())
	}

	installs := exec.installs()
	if len(installs) != 2 {
		t.Fatalf("安装了 %d 次，期望 2 次（docker 与 tailwindcss 不可自动安装）| ran %d installs, want 2", len(installs), len(installs))
	}
	wantTargets := []string{
		"github.com/dkorunic/betteralign/cmd/betteralign@latest",
		"github.com/google/ko@latest",
	}
	wantGobin := filepath.Join(te.env.WorkDir, filepath.FromSlash(tools.LocalToolDir))
	for i, call := range installs {
		if want := pathStub.at("go"); call.name != want {
			t.Errorf("第 %d 次安装使用了 %q，期望解析到的 go %q | want %q", i+1, call.name, want, want)
		}
		if got := strings.Join(call.args, " "); got != "install "+wantTargets[i] {
			t.Errorf("第 %d 次安装参数 = %q，期望 %q | want %q", i+1, got, "install "+wantTargets[i], "install "+wantTargets[i])
		}
		if call.dir != te.env.WorkDir {
			t.Errorf("第 %d 次安装的工作目录 = %q，期望 %q | want %q", i+1, call.dir, te.env.WorkDir, te.env.WorkDir)
		}
		if got := envValue(call.env, "GOBIN"); got != wantGobin {
			t.Errorf("第 %d 次安装的 GOBIN = %q，期望 %q | want %q", i+1, got, wantGobin, wantGobin)
		}
	}
	for _, name := range []string{cliToolBetteralign, cliToolKo} {
		if _, err := os.Stat(newToolBin(te.env.WorkDir, name)); err != nil {
			t.Errorf("%s 未落在项目内工具目录 | %s is missing: %v", name, name, err)
		}
	}

	out := te.out.String()
	for _, want := range []string{
		"installed | 已安装: 2",
		"skipped | 已跳过: 0",
		"failed | 失败: 0",
		"fake-version 9.9.9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("摘要缺少 %q | the summary misses %q:\n%s", want, want, out)
		}
	}
	// docker was never planned, so it is neither installed nor skipped - it is reported by the hints
	// of `yd tools` instead. | docker 从未进入计划，因此既不算安装也不算跳过——它由 `yd tools` 的提示报告。
	if strings.Contains(out, cliToolDocker) {
		t.Errorf("不可自动安装的工具不该出现在安装报告中 | docker must stay out of the install report:\n%s", out)
	}
	if !pathStub.askedFor("go") {
		t.Error("安装必须查找 go 工具链 | installing must look up the go toolchain")
	}
}

// TestToolsInstallSkipsAReadyTool proves an already available tool is reported as skipped instead of
// being reinstalled. | TestToolsInstallSkipsAReadyTool 证明已可用的工具被记为跳过，而不是重装一遍。
func TestToolsInstallSkipsAReadyTool(t *testing.T) {
	te := openToolsEnv(t, "", false)
	pathStub := newCliFakePath(t, "go")
	useToolPath(t, pathStub)
	exec := &cliFakeInstall{}
	useToolExec(t, exec)
	stubToolFile(t, newToolBin(te.env.WorkDir, cliToolBetteralign))

	if err := Execute(context.Background(), []string{toolsCommandName, toolsActionInstall, cliToolBetteralign}, te.env); err != nil {
		t.Fatalf("yd tools install betteralign 失败 | failed: %v\n%s", err, te.errs.String())
	}

	if got := exec.count(); got != 0 {
		t.Errorf("就绪的工具运行了 %d 条命令，期望 0 条 | ran %d commands, want 0", got, got)
	}
	// Nothing had to be installed, so the toolchain was never looked up. | 无需安装任何东西，因此从未查找工具链。
	if pathStub.askedFor("go") {
		t.Error("无需安装时不该查找 go 工具链 | the toolchain must not be looked up")
	}
	out := te.out.String()
	for _, want := range []string{"installed | 已安装: 0", "skipped | 已跳过: 1", "failed | 失败: 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("摘要缺少 %q | the summary misses %q:\n%s", want, want, out)
		}
	}
}

// TestToolsInstallNamedToolInstallsOnlyThatTool keeps a named run narrow. | TestToolsInstallNamedToolInstallsOnlyThatTool 让具名运行只做被点名的事。
func TestToolsInstallNamedToolInstallsOnlyThatTool(t *testing.T) {
	te := openToolsEnv(t, "", false)
	useToolPath(t, newCliFakePath(t, "go"))
	exec := &cliFakeInstall{}
	useToolExec(t, exec)

	if err := Execute(context.Background(), []string{toolsCommandName, toolsActionInstall, cliToolBetteralign}, te.env); err != nil {
		t.Fatalf("yd tools install betteralign 失败 | failed: %v\n%s", err, te.errs.String())
	}

	installs := exec.installs()
	if len(installs) != 1 {
		t.Fatalf("安装了 %d 次，期望 1 次 | ran %d installs, want 1", len(installs), len(installs))
	}
	if got := strings.Join(installs[0].args, " "); !strings.Contains(got, cliToolBetteralign) {
		t.Errorf("安装参数 = %q，期望指向 betteralign | want betteralign", got)
	}
	if _, err := os.Stat(newToolBin(te.env.WorkDir, cliToolKo)); err == nil {
		t.Error("具名安装不得顺带安装 ko | a named install must not install ko")
	}
	if out := te.out.String(); !strings.Contains(out, "installed | 已安装: 1") {
		t.Errorf("摘要缺少安装计数 | the summary misses the count:\n%s", out)
	}
}

// TestToolsInstallReportsFailureWithNonZeroExit proves a failed installation is both reported and
// returned, which is what makes cmd/yd exit non-zero. | TestToolsInstallReportsFailureWithNonZeroExit
// 证明安装失败既被报告也被返回，这正是 cmd/yd 非零退出的依据。
func TestToolsInstallReportsFailureWithNonZeroExit(t *testing.T) {
	te := openToolsEnv(t, "", false)
	useToolPath(t, newCliFakePath(t, "go"))
	exec := &cliFakeInstall{fail: errors.New("exit status 1")}
	useToolExec(t, exec)

	err := Execute(context.Background(), []string{toolsCommandName, toolsActionInstall}, te.env)
	if err == nil {
		t.Fatal("安装全部失败时应当返回错误 | a fully failed run must return an error")
	}
	if !errors.Is(err, tools.ErrInstallFailed) {
		t.Errorf("err = %v，期望匹配 ErrInstallFailed | want ErrInstallFailed", err)
	}
	out := te.out.String()
	if !strings.Contains(out, "failed | 失败: 2") {
		t.Errorf("摘要未报告两次失败 | the summary misses the failures:\n%s", out)
	}
	if !strings.Contains(out, "installed | 已安装: 0") {
		t.Errorf("摘要未报告零次成功 | the summary misses the empty install list:\n%s", out)
	}
	errs := te.errs.String()
	for _, want := range []string{"yd tools: ", "download failed"} {
		if !strings.Contains(errs, want) {
			t.Errorf("诊断缺少 %q | the diagnostics miss %q:\n%s", want, want, errs)
		}
	}
}

// TestToolsInstallWithoutGoToolchain explains the one dependency an automatic installation has. | TestToolsInstallWithoutGoToolchain 说明自动安装唯一的前置依赖。
func TestToolsInstallWithoutGoToolchain(t *testing.T) {
	te := openToolsEnv(t, "", false)
	useToolPath(t, newCliFakePath(t))
	exec := &cliFakeInstall{}
	useToolExec(t, exec)

	err := Execute(context.Background(), []string{toolsCommandName, toolsActionInstall}, te.env)
	if !errors.Is(err, tools.ErrNoGoToolchain) {
		t.Fatalf("err = %v，期望匹配 ErrNoGoToolchain | want ErrNoGoToolchain", err)
	}
	if got := exec.count(); got != 0 {
		t.Errorf("没有 go 工具链时运行了 %d 条命令，期望 0 条 | ran %d commands, want 0", got, got)
	}
	errs := te.errs.String()
	for _, want := range []string{"go.dev/dl", "yd tools"} {
		if !strings.Contains(errs, want) {
			t.Errorf("诊断缺少 %q | the diagnostics miss %q:\n%s", want, want, errs)
		}
	}
}

// TestToolsInstallUnknownNameListsTheAlternatives refuses an unknown tool before anything is touched. | TestToolsInstallUnknownNameListsTheAlternatives 在动任何东西之前拒绝未知工具。
func TestToolsInstallUnknownNameListsTheAlternatives(t *testing.T) {
	te := openToolsEnv(t, "", false)
	useToolPath(t, newCliFakePath(t, "go"))
	exec := &cliFakeInstall{}
	useToolExec(t, exec)

	err := Execute(context.Background(), []string{toolsCommandName, toolsActionInstall, "golangci-lint"}, te.env)
	if !errors.Is(err, tools.ErrUnknownTool) {
		t.Fatalf("err = %v，期望匹配 ErrUnknownTool | want ErrUnknownTool", err)
	}
	if got := exec.count(); got != 0 {
		t.Errorf("未知名称运行了 %d 条命令，期望 0 条 | ran %d commands, want 0", got, got)
	}
	errs := te.errs.String()
	for _, want := range []string{"golangci-lint", cliToolBetteralign, cliToolTailwind, "Usage | 用法:"} {
		if !strings.Contains(errs, want) {
			t.Errorf("诊断缺少 %q | the diagnostics miss %q:\n%s", want, want, errs)
		}
	}
	if out := te.out.String(); strings.Contains(out, "installed | 已安装") {
		t.Errorf("被拒绝的运行不该打印报告 | a refused run must not print a report:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(te.env.WorkDir, ".yd")); statErr == nil {
		t.Error("被拒绝的运行不得创建工具目录 | a refused run must not create the tool directory")
	}
}

// TestToolsInstallNotInstallableNamePointsAtTheManualRoute reports a hand-installed tool through the
// report instead of pretending to install it. | TestToolsInstallNotInstallableNamePointsAtTheManualRoute
// 把需手动安装的工具记入报告，而不是假装安装它。
func TestToolsInstallNotInstallableNamePointsAtTheManualRoute(t *testing.T) {
	te := openToolsEnv(t, "", false)
	pathStub := newCliFakePath(t, "go")
	useToolPath(t, pathStub)
	exec := &cliFakeInstall{}
	useToolExec(t, exec)

	err := Execute(context.Background(), []string{toolsCommandName, toolsActionInstall, cliToolTailwind}, te.env)
	if !errors.Is(err, tools.ErrNotInstallable) {
		t.Fatalf("err = %v，期望匹配 ErrNotInstallable | want ErrNotInstallable", err)
	}
	if got := exec.count(); got != 0 {
		t.Errorf("不可安装的工具运行了 %d 条命令，期望 0 条 | ran %d commands, want 0", got, got)
	}
	if pathStub.askedFor("go") {
		t.Error("没有可安装项时不该查找 go 工具链 | the toolchain must not be looked up")
	}
	out := te.out.String()
	if !strings.Contains(out, "failed | 失败: 1") || !strings.Contains(out, "yd assets update") {
		t.Errorf("报告未给出手动安装途径 | the report misses the manual route:\n%s", out)
	}
	if errs := te.errs.String(); !strings.Contains(errs, "部分工具需手动安装") {
		t.Errorf("诊断缺少手动安装提示 | the diagnostics miss the manual hint:\n%s", errs)
	}
}

// TestToolsUnknownActionAndExtraArgumentsAreUsageErrors keeps the two positional mistakes apart from
// a working run. | TestToolsUnknownActionAndExtraArgumentsAreUsageErrors 把两类位置词错误与正常运行区分开。
func TestToolsUnknownActionAndExtraArgumentsAreUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want error
	}{
		{"unknown action | 未知动作", []string{toolsCommandName, "purge"}, ErrUnknownCommand},
		{"check with an argument | check 带参数", []string{toolsCommandName, toolsActionCheck, cliToolBetteralign}, ErrNoCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			te := openToolsEnv(t, "", false)
			useToolPath(t, newCliFakePath(t))
			exec := &cliFakeInstall{}
			useToolExec(t, exec)

			err := Execute(context.Background(), tc.args, te.env)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v，期望匹配 %v | want %v", err, tc.want, tc.want)
			}
			if got := exec.count(); got != 0 {
				t.Errorf("用法错误运行了 %d 条命令，期望 0 条 | ran %d commands, want 0", got, got)
			}
			errs := te.errs.String()
			for _, want := range []string{"Usage | 用法:", toolsActionCheck, toolsActionInstall} {
				if !strings.Contains(errs, want) {
					t.Errorf("诊断缺少 %q | the diagnostics miss %q:\n%s", want, want, errs)
				}
			}
		})
	}
}

// newInterviewPins returns the flags that pin every interview answer, so a test can leave exactly one
// question to the scripted input. | newInterviewPins 返回钉住每个问答选项的标志，使测试正好把一个问题留给脚本输入。
func newInterviewPins() []string {
	return []string{
		"--module", "example.com/demo",
		"--db", "none",
		"--redis=false",
		"--asynq=false",
		"--grpc=false",
		"--i18n=false",
		"--tailwind=false",
		"--htmx-ext", "none",
		"--license", "Apache-2.0",
	}
}

// TestNewPrintsToolHintsWithoutInstalling is the contract of `yd new` in scripts and CI: it explains
// what is missing and stays offline. | TestNewPrintsToolHintsWithoutInstalling 是 `yd new` 在脚本与 CI 中的契约：
// 说明缺什么，并保持离线。
func TestNewPrintsToolHintsWithoutInstalling(t *testing.T) {
	te := openToolsEnv(t, "", false)
	pathStub := newCliFakePath(t)
	useToolPath(t, pathStub)
	exec := &cliFakeInstall{}
	useToolExec(t, exec)

	if err := Execute(context.Background(), []string{newCommandName, "demo", "-y"}, te.env); err != nil {
		t.Fatalf("yd new -y 失败 | yd new -y failed: %v\n%s", err, te.errs.String())
	}

	out := te.out.String()
	for _, want := range []string{
		"missing tools | 缺失工具:",
		cliToolBetteralign + ": ",
		cliToolKo + ": ",
		"install | 安装:",
		"yd tools install",
		tools.LocalToolDir,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("提示缺少 %q | the hints miss %q:\n%s", want, want, out)
		}
	}
	if !strings.Contains(out, "project created | 项目已创建") {
		t.Errorf("生成报告不该被提示取代 | the report is missing:\n%s", out)
	}
	// --yes is the e2e entry point: it must not touch the network, and must not even look for the
	// toolchain it would need. | --yes 是 e2e 入口：不得触网，连它所需的工具链都不该去查找。
	if got := exec.count(); got != 0 {
		t.Errorf("--yes 运行了 %d 条命令，期望 0 条 | --yes ran %d commands, want 0", got, got)
	}
	if pathStub.askedFor("go") {
		t.Error("--yes 不得查找 go 工具链 | --yes must not look up the go toolchain")
	}
	if _, err := os.Stat(filepath.Join(te.env.WorkDir, "demo", ".yd")); err == nil {
		t.Error("--yes 不得在磁盘上创建工具目录 | --yes must not create the tool directory")
	}
}

// TestNewInstallToolsFlagInstallsThem is the explicit opt-in of a script. | TestNewInstallToolsFlagInstallsThem 是脚本的显式选择加入。
func TestNewInstallToolsFlagInstallsThem(t *testing.T) {
	te := openToolsEnv(t, "", false)
	pathStub := newCliFakePath(t, "go")
	useToolPath(t, pathStub)
	exec := &cliFakeInstall{}
	useToolExec(t, exec)

	if err := Execute(context.Background(), []string{newCommandName, "demo", "-y", "--install-tools"}, te.env); err != nil {
		t.Fatalf("yd new --install-tools 失败 | failed: %v\n%s", err, te.errs.String())
	}

	installs := exec.installs()
	if len(installs) != 2 {
		t.Fatalf("安装了 %d 次，期望 2 次 | ran %d installs, want 2", len(installs), len(installs))
	}
	projectDir := filepath.Join(te.env.WorkDir, "demo")
	wantGobin := filepath.Join(projectDir, filepath.FromSlash(tools.LocalToolDir))
	for _, call := range installs {
		if got := envValue(call.env, "GOBIN"); got != wantGobin {
			t.Errorf("GOBIN = %q，期望新项目的工具目录 %q | want %q", got, wantGobin, wantGobin)
		}
	}
	for _, name := range []string{cliToolBetteralign, cliToolKo} {
		if _, err := os.Stat(newToolBin(projectDir, name)); err != nil {
			t.Errorf("%s 未安装进新项目 | %s is missing: %v", name, name, err)
		}
	}
	out := te.out.String()
	for _, want := range []string{"missing tools | 缺失工具:", "installed | 已安装: 2", "failed | 失败: 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q | the output misses %q:\n%s", want, want, out)
		}
	}
}

// TestNewInstallFailureStillSucceeds is the hard rule of the closing step: the project is already
// generated, so a failed installation may only warn. | TestNewInstallFailureStillSucceeds 是收尾步骤的硬性规则：
// 项目已经生成完毕，因此安装失败只允许告警。
func TestNewInstallFailureStillSucceeds(t *testing.T) {
	te := openToolsEnv(t, "", false)
	useToolPath(t, newCliFakePath(t, "go"))
	exec := &cliFakeInstall{fail: errors.New("exit status 1")}
	useToolExec(t, exec)

	if err := Execute(context.Background(), []string{newCommandName, "demo", "-y", "--install-tools"}, te.env); err != nil {
		t.Fatalf("安装失败绝不能让 yd new 失败 | a failed installation must never fail yd new: %v", err)
	}

	out := te.out.String()
	if !strings.Contains(out, "failed | 失败: 2") {
		t.Errorf("摘要未报告失败 | the summary misses the failures:\n%s", out)
	}
	if !existsMem(t, te.env.FS, filepath.Join(te.env.WorkDir, "demo", ".yd.json")) {
		t.Error("安装失败不得影响已生成的项目 | the generated project must survive")
	}
	if errs := te.errs.String(); !strings.Contains(errs, "工具安装失败，项目不受影响") {
		t.Errorf("未记录告警 | the warning is missing:\n%s", errs)
	}
}

// TestNewTTYToolPromptInstallsOnYes covers the interactive default: the question is asked with "yes"
// as its default and a yes installs the tools. | TestNewTTYToolPromptInstallsOnYes 覆盖交互默认值：
// 提问的默认答案是「是」，答是即安装工具。
func TestNewTTYToolPromptInstallsOnYes(t *testing.T) {
	te := openToolsEnv(t, "", true)
	te.env.In = newLineReader("y\n")
	pathStub := newCliFakePath(t, "go")
	useToolPath(t, pathStub)
	exec := &cliFakeInstall{}
	useToolExec(t, exec)

	args := append([]string{newCommandName, "demo"}, newInterviewPins()...)
	if err := Execute(context.Background(), args, te.env); err != nil {
		t.Fatalf("yd new 交互失败 | the interactive run failed: %v\n%s", err, te.errs.String())
	}

	out := te.out.String()
	if !strings.Contains(out, "现在就安装缺失的工具吗？| Install the missing tools now?") {
		t.Errorf("未询问是否安装 | the question was not asked:\n%s", out)
	}
	if len(exec.installs()) != 2 {
		t.Errorf("答是时应安装两个工具 | answering yes must install both:\n%s", out)
	}
	if !existsMem(t, te.env.FS, filepath.Join(te.env.WorkDir, "demo", ".yd.json")) {
		t.Error("答是时项目仍应生成完毕 | the project must still be generated")
	}
	if _, err := os.Stat(newToolBin(filepath.Join(te.env.WorkDir, "demo"), cliToolBetteralign)); err != nil {
		t.Errorf("答是时工具应安装进新项目 | the tool is missing: %v", err)
	}
}

// TestNewTTYToolPromptSkipsOnNo keeps a "no" answer offline. | TestNewTTYToolPromptSkipsOnNo 让「否」保持离线。
func TestNewTTYToolPromptSkipsOnNo(t *testing.T) {
	te := openToolsEnv(t, "", true)
	te.env.In = newLineReader("n\n")
	pathStub := newCliFakePath(t, "go")
	useToolPath(t, pathStub)
	exec := &cliFakeInstall{}
	useToolExec(t, exec)

	args := append([]string{newCommandName, "demo"}, newInterviewPins()...)
	if err := Execute(context.Background(), args, te.env); err != nil {
		t.Fatalf("yd new 交互失败 | the interactive run failed: %v\n%s", err, te.errs.String())
	}

	out := te.out.String()
	if !strings.Contains(out, "现在就安装缺失的工具吗？") {
		t.Errorf("未询问是否安装 | the question was not asked:\n%s", out)
	}
	if !strings.Contains(out, "missing tools | 缺失工具:") {
		t.Errorf("答否时仍应打印提示 | the hints must survive a no:\n%s", out)
	}
	if strings.Contains(out, "installed | 已安装") {
		t.Errorf("答否时不得安装 | nothing may be installed on a no:\n%s", out)
	}
	if got := exec.count(); got != 0 {
		t.Errorf("答否时运行了 %d 条命令，期望 0 条 | ran %d commands, want 0", got, got)
	}
	if pathStub.askedFor("go") {
		t.Error("答否时不得查找 go 工具链 | the toolchain must not be looked up")
	}
}

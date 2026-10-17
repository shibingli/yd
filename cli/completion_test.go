// This file tests the completion command: the registry mapping it feeds the generators with, the detection
// table it reports, and the installation that must never edit a dotfile behind the user's back. | 本文件测试
// completion 命令：它喂给生成器的注册表映射、它报告的探测结果，以及绝不背着用户改动 dotfile 的安装。
//
// The one test that goes beyond the command itself is the bash one: it writes the printed script to a real
// file, has a real bash parse it and source it, and then asks bash what it would offer at the cursor. That
// is the only way to check a completion that is not a text comparison. | 唯一超出命令本身的是 bash 那一项：
// 它把打印出来的脚本写入真实文件，让真实的 bash 解析并 source 它，然后问 bash 在光标处会给出什么候选。这是检查
// 一份补全的唯一不是文本比对的办法。

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibingli/yd/completion"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// completionTestHome is the home directory every completion test resolves. | completionTestHome 是每个补全测试
// 所解析到的主目录。
const completionTestHome = "/yd-test-home"

// completionShellVars is the environment of a POSIX user whose login shell is the given one. | completionShellVars
// 是某个 POSIX 用户的环境，其登录 shell 由参数给出。
func completionShellVars(shell string) map[string]string {
	return map[string]string{"SHELL": shell, "HOME": completionTestHome}
}

// stubCompletion injects the environment and the platform the completion command reads. | stubCompletion
// 注入 completion 命令所读取的环境与平台。
//
// Both are package variables, the way align.PathLookup is: they are what puts the whole table of detection
// cases - every $SHELL value, a Windows machine without one, an unknown home - in front of the command
// without touching the process environment of the machine the test runs on. | 二者都是包级变量，与
// align.PathLookup 的做法一致：正是因为它们，整张探测表——$SHELL 的各个取值、没有它的 Windows 机器、未知的主目录
// ——才能摆在命令面前，而不触碰测试所跑机器的进程环境。
func stubCompletion(t *testing.T, goos string, vars map[string]string) {
	t.Helper()
	origLookup, origGOOS := shellLookup, completionGOOS
	lookup := func(key string) string { return vars[key] }
	shellLookup, completionGOOS = lookup, goos
	t.Cleanup(func() { shellLookup, completionGOOS = origLookup, origGOOS })
}

// completionEnv builds a memory-backed environment for the completion command. | completionEnv 为 completion
// 命令构造内存支撑的环境。
func completionEnv(t *testing.T, goos string, vars map[string]string, in string, tty bool) *testEnv {
	t.Helper()
	te := newTestEnv(t, nil, in, tty)
	stubCompletion(t, goos, vars)
	return te
}

// runCompletion executes one completion command line. | runCompletion 执行一条 completion 命令行。
func execCompletion(t *testing.T, te *testEnv, args ...string) error {
	t.Helper()
	return Execute(context.Background(), append([]string{completionCommandName}, args...), te.env)
}

// commandNames returns the registered command words. | commandNames 返回已注册的命令词。
func registeredCommandNames() []string {
	cmds := Commands()
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	return names
}

// TestCompletionScriptsCoverEveryRegisteredCommand 每个已注册命令都必须出现在四份脚本里。
// TestCompletionScriptsCoverEveryRegisteredCommand requires every registered command in all four scripts.
//
// This is the test that fails the moment somebody adds a command and the completion script is edited by
// hand: the scripts are generated from Commands(), so a command that is missing from one of them means the
// registry mapping dropped it. | 这就是「有人加了命令却去手改补全脚本」时立刻变红的那项测试：脚本由
// Commands() 生成，因此某个命令在某一份脚本里缺失，只可能是注册表映射把它漏掉了。
func TestCompletionScriptsCoverEveryRegisteredCommand(t *testing.T) {
	te := completionEnv(t, "linux", completionShellVars("/bin/bash"), "", false)
	names := registeredCommandNames()
	if len(names) == 0 {
		t.Fatal("the command registry is empty | 命令注册表为空")
	}

	reg := completionRegistry(te.env)
	if len(reg.Commands) != len(names) {
		t.Fatalf("mapped %d commands, want %d | 映射出 %d 个命令，期望 %d 个", len(reg.Commands), len(names), len(reg.Commands), len(names))
	}
	for i, c := range reg.Commands {
		if c.Name != names[i] {
			t.Fatalf("command %d = %q, want %q | 第 %d 个命令不符", i, c.Name, names[i], i)
		}
		if strings.TrimSpace(c.Short) == "" {
			t.Errorf("command %q has no summary | 命令 %q 缺少摘要", c.Name, c.Name)
		}
	}

	for _, sh := range completion.Shells() {
		script, err := completion.Generate(sh, reg)
		if err != nil {
			t.Fatalf("generate %s failed | 生成 %s 失败: %v", sh, sh, err)
		}
		for _, name := range names {
			if !strings.Contains(script, name) {
				t.Errorf("the %s script does not mention %q | %s 脚本没有提到 %q", sh, name, sh, name)
			}
		}
	}
}

// TestCompletionRegistryMirrorsFlagsAndValueKind 每个命令的标志与「是否取值」都必须如实映射。
// TestCompletionRegistryMirrorsFlagsAndValueKind requires every command's flags and value kind to be mirrored faithfully.
//
// The expectations are re-derived from pflag here rather than taken from the mapping function itself, so
// this cannot pass by agreeing with its own implementation: a flag whose value kind is wrong is exactly the
// bug that makes a completion offer a file name where a flag belongs. | 这里的期望值是从 pflag 重新推导出来的，
// 而不是取自映射函数本身，因此它不可能靠「与自己的实现一致」而通过：取值类型弄错，正是补全在本该出现标志的地方
// 给出文件名的那个缺陷。
func TestCompletionRegistryMirrorsFlagsAndValueKind(t *testing.T) {
	te := completionEnv(t, "linux", completionShellVars("/bin/bash"), "", false)
	reg := completionRegistry(te.env)
	mapped := make(map[string]completion.Command, len(reg.Commands))
	for _, c := range reg.Commands {
		mapped[c.Name] = c
	}

	kv := make(map[string]map[string]bool)
	for _, c := range Commands() {
		got, ok := mapped[c.Name]
		if !ok {
			t.Fatalf("command %q is missing from the registry | 注册表中缺少命令 %q", c.Name, c.Name)
		}
		fs := newCommandFlagSet(te.env, c)
		want := make(map[string]bool)
		fs.VisitAll(func(f *pflag.Flag) {
			want[f.Name] = f.NoOptDefVal == ""
		})
		kv[c.Name] = want

		if len(got.Flags) != len(want) {
			t.Errorf("%s: mapped %d flags, want %d | %s：映射出 %d 个标志，期望 %d 个", c.Name, len(got.Flags), len(want), c.Name, len(got.Flags), len(want))
		}
		for _, f := range got.Flags {
			kind, known := want[f.Name]
			if !known {
				t.Errorf("%s: unexpected flag --%s | %s：多出标志 --%s", c.Name, f.Name, c.Name, f.Name)
				continue
			}
			if f.TakesValue != kind {
				t.Errorf("%s: --%s takesValue = %v, want %v | %s：--%s 的取值类型不符", c.Name, f.Name, f.TakesValue, kind, c.Name, f.Name)
			}
			if strings.TrimSpace(f.Usage) == "" {
				t.Errorf("%s: --%s has no usage text | %s：--%s 缺少帮助文案", c.Name, f.Name, c.Name, f.Name)
			}
		}
	}

	// The global flags reach every command that does not shadow them. | 全局标志会到达每个未遮蔽它的命令。
	for _, name := range []string{flagConfig, flagVerbose, flagVersion} {
		if _, ok := kv["build"][name]; !ok {
			t.Fatalf("the build command does not carry the global flag %q | build 命令没有全局标志 %q", name, name)
		}
	}

	// bash and zsh express the distinction as a word list, which is what the scripts branch on. | bash 与 zsh
	// 把这个差别表达为一张词表，脚本正是据此分支。
	bashScript, err := completion.Generate(completion.Bash, reg)
	if err != nil {
		t.Fatalf("generate bash failed | 生成 bash 失败: %v", err)
	}
	valueWords := assignmentWords(t, bashScript, "_yd_value_flags='")
	for _, want := range []string{"--config", "--env-file", "--output", "--os"} {
		if !contains(valueWords, want) {
			t.Errorf("the bash value list misses %q | bash 取值词表缺少 %q: %v", want, want, valueWords)
		}
	}
	for _, unwanted := range []string{"--verbose", "--tailwind", "--all", "--no-color"} {
		if contains(valueWords, unwanted) {
			t.Errorf("the bash value list wrongly holds %q | bash 取值词表不该含 %q: %v", unwanted, unwanted, valueWords)
		}
	}
	// --version is on the list although it is a boolean for most commands: `yd assets add` shadows it with
	// a value-taking one, and the word list is the union across commands. Only the flag name is checked
	// here, so the shadowing rule is what decides it. | --version 在词表里，尽管它对大多数命令都是布尔标志：
	// `yd assets add` 用取值型标志遮蔽了它，而词表是跨命令的并集。这里只按标志名判断，因此决定它的是遮蔽规则。
	if !contains(valueWords, "--version") {
		t.Errorf("the bash value list misses --version, which one command takes a value for | bash 取值词表缺少某个命令要取值的 --version: %v", valueWords)
	}
	assets, ok := mapped["assets"]
	if !ok {
		t.Fatal("the assets command is missing | 缺少 assets 命令")
	}
	takesValue := false
	for _, f := range assets.Flags {
		if f.Name == flagVersion && f.TakesValue {
			takesValue = true
		}
	}
	if !takesValue {
		t.Errorf("the assets command does not shadow --version with a value-taking flag | assets 命令未用取值型 --version 遮蔽全局标志")
	}

	zshScript, err := completion.Generate(completion.Zsh, reg)
	if err != nil {
		t.Fatalf("generate zsh failed | 生成 zsh 失败: %v", err)
	}
	zshWords := strings.Fields(between(t, zshScript, `local value_flags="`, "\"\n"))
	for _, want := range []string{"--config", "--output"} {
		if !contains(zshWords, want) {
			t.Errorf("the zsh value list misses %q | zsh 取值词表缺少 %q: %v", want, want, zshWords)
		}
	}
	if contains(zshWords, "--verbose") {
		t.Errorf("the zsh value list wrongly holds --verbose | zsh 取值词表不该含 --verbose: %v", zshWords)
	}
}

// assignmentWords returns the words of a single-quoted assignment whose line starts with prefix. | assignmentWords
// 返回以 prefix 开头的那一行中，某个单引号赋值语句里的词。
func assignmentWords(t *testing.T, script, prefix string) []string {
	t.Helper()
	return strings.Fields(between(t, script, prefix, "'\n"))
}

// between returns the text between two delimiters, failing the test when either is absent. | between
// 返回两个分隔符之间的文本，任一缺失时让测试失败。
func between(t *testing.T, text, from, to string) string {
	t.Helper()
	start := strings.Index(text, from)
	if start < 0 {
		t.Fatalf("the script has no %q | 脚本里没有 %q", from, from)
	}
	rest := text[start+len(from):]
	end := strings.Index(rest, to)
	if end < 0 {
		t.Fatalf("the script has no %q after %q | 脚本里 %q 之后没有 %q", to, from, from, to)
	}
	return rest[:end]
}

// contains reports whether a word list holds one word. | contains 报告词表中是否含某个词。
func contains(words []string, want string) bool {
	for _, w := range words {
		if w == want {
			return true
		}
	}
	return false
}

// TestCompletionDetect 表驱动校验探测报告与安装路径的预告。
// TestCompletionDetect checks the detection report and the announced installation paths with a table.
func TestCompletionDetect(t *testing.T) {
	posixHome := filepath.Join(completionTestHome, ".yd", "completion.zsh")
	cases := []struct {
		name      string
		goos      string
		vars      map[string]string
		args      []string
		wantOut   []string
		wantErrIs error
	}{
		{
			name: "posix login shell | POSIX 登录 shell", goos: "linux",
			vars:    completionShellVars("/usr/bin/zsh"),
			args:    []string{completionActionDetect},
			wantOut: []string{"shell | shell: zsh", "$SHELL=/usr/bin/zsh", posixHome, filepath.Join(completionTestHome, ".zshrc")},
		},
		{
			name: "override wins over the probe | 命令行指定优先于探测", goos: "linux",
			vars:    completionShellVars("/bin/bash"),
			args:    []string{completionActionDetect, "--shell", "tcsh"},
			wantOut: []string{"shell | shell: csh", "given on the command line | 由命令行指定", filepath.Join(completionTestHome, ".cshrc")},
		},
		{
			name: "windows without a probe | Windows 没有探测值", goos: "windows",
			vars:    map[string]string{"HOME": completionTestHome},
			args:    []string{completionActionDetect},
			wantOut: []string{"shell | shell: powershell", "platform default | 平台默认值", filepath.Join(completionTestHome, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")},
		},
		{
			name: "no probe on posix | POSIX 上没有探测值", goos: "linux",
			vars:      map[string]string{"HOME": completionTestHome},
			args:      []string{completionActionDetect},
			wantErrIs: completion.ErrShellUndetected,
		},
		{
			name: "an unknown probe is refused | 未知探测值被拒绝", goos: "linux",
			vars:      completionShellVars("/bin/fish"),
			args:      []string{completionActionDetect},
			wantErrIs: completion.ErrShellUndetected,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			te := completionEnv(t, tc.goos, tc.vars, "", false)
			err := execCompletion(t, te, tc.args...)
			if tc.wantErrIs != nil {
				if !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("err = %v, want %v | 期望哨兵错误", err, tc.wantErrIs)
				}
				if !strings.Contains(te.errs.String(), "--shell") {
					t.Errorf("the failure does not hint at --shell | 失败信息没有提示 --shell:\n%s", te.errs.String())
				}
				if out := te.out.String(); out != "" {
					t.Errorf("a failed detection printed a plan | 探测失败却打印了方案:\n%s", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("detect failed | 探测失败: %v", err)
			}
			got := te.out.String()
			for _, want := range tc.wantOut {
				if !strings.Contains(got, want) {
					t.Errorf("the report misses %q | 报告缺少 %q:\n%s", want, want, got)
				}
			}
			if !strings.Contains(got, "startup file | 启动文件:") {
				t.Errorf("the report does not name the startup file | 报告没有点名启动文件:\n%s", got)
			}
		})
	}
}

// TestCompletionInstallLeavesTheStartupFileAlone 默认安装必须一个字节都不改启动文件。
// TestCompletionInstallLeavesTheStartupFileAlone leaves the startup file untouched by default.
//
// The prompt is never shown here because the environment is not a TTY, and the input is a reader that fails
// the test when it is read: a non-interactive run has nobody who could answer, so it must not even ask. | 这里
// 不会出现提问，因为环境不是 TTY，而输入是一个被读取就失败的读取器：非交互运行没有人能回答，因此它连问都不该问。
func TestCompletionInstallLeavesTheStartupFileAlone(t *testing.T) {
	fs := afero.NewMemMapFs()
	rc := filepath.Join(completionTestHome, ".bashrc")
	original := []byte("# mine | 我的\nalias ll='ls -l'\n")
	if err := afero.WriteFile(fs, rc, original, 0o644); err != nil {
		t.Fatalf("seed failed | 预置失败: %v", err)
	}

	te := newTestEnv(t, fs, "", false)
	te.env.In = failingReader{t}
	stubCompletion(t, "linux", completionShellVars("/bin/bash"))

	if err := execCompletion(t, te, completionActionInstall); err != nil {
		t.Fatalf("install failed | 安装失败: %v", err)
	}

	got, err := afero.ReadFile(fs, rc)
	if err != nil {
		t.Fatalf("read startup file failed | 读取启动文件失败: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("the startup file changed | 启动文件被改动:\n%q", got)
	}
	if ok, _ := afero.Exists(fs, rc+".yd-backup"); ok {
		t.Error("a backup was taken although nothing was appended | 没有追加却留下了备份")
	}

	script := filepath.Join(completionTestHome, ".yd", "completion.bash")
	written, err := afero.ReadFile(fs, script)
	if err != nil {
		t.Fatalf("read script failed | 读取脚本失败: %v", err)
	}
	want, err := completion.Generate(completion.Bash, completionRegistry(te.env))
	if err != nil {
		t.Fatalf("generate failed | 生成失败: %v", err)
	}
	if string(written) != want {
		t.Error("the installed script differs from the printed one | 落盘脚本与打印的脚本不一致")
	}

	out := te.out.String()
	for _, want := range []string{"written | 已写入: " + script, "unchanged, pass --append-rc"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report misses %q | 报告缺少 %q:\n%s", want, want, out)
		}
	}
	if strings.Contains(out, "appended | 已追加: "+rc) {
		t.Errorf("the report claims an append | 报告声称发生了追加:\n%s", out)
	}
}

// TestCompletionInstallHonoursDirWithoutMovingTheStartupFile --dir 只移动脚本，不移动启动文件。
// TestCompletionInstallHonoursDirWithoutMovingTheStartupFile moves the script and not the startup file.
func TestCompletionInstallHonoursDirWithoutMovingTheStartupFile(t *testing.T) {
	fs := afero.NewMemMapFs()
	te := newTestEnv(t, fs, "", false)
	stubCompletion(t, "linux", completionShellVars("/bin/bash"))

	dir := filepath.Join(completionTestHome, "work")
	if err := execCompletion(t, te, completionActionInstall, "--dir", dir, "--shell", "zsh"); err != nil {
		t.Fatalf("install failed | 安装失败: %v", err)
	}
	script := filepath.Join(dir, ".yd", "completion.zsh")
	if ok, _ := afero.Exists(fs, script); !ok {
		t.Errorf("--dir did not move the script to %s | --dir 没有把脚本移到 %s", script, script)
	}
	if ok, _ := afero.Exists(fs, filepath.Join(completionTestHome, ".zshrc")); ok {
		t.Error("--dir made the install touch the startup file | --dir 让安装触碰了启动文件")
	}
	if out := te.out.String(); !strings.Contains(out, filepath.Join(completionTestHome, ".zshrc")) {
		t.Errorf("the report no longer names the real startup file | 报告没有点名真正的启动文件:\n%s", out)
	}
}

// TestCompletionInstallPrintWritesNothingAndKeepsOutPure --print 不写文件，且 Out 上只有脚本本身。
// TestCompletionInstallPrintWritesNothingAndKeepsOutPure writes nothing and keeps Out pure.
//
// The purity is not cosmetic: `yd completion install --print --shell bash > ~/.yd/completion.bash` is a
// supported way to place the script, and a report glued to it would be sourced as a command by the shell. |
// 这份干净不是装饰：`yd completion install --print --shell bash > ~/.yd/completion.bash` 是放置脚本的正常用法，
// 而粘在后面的报告会被 shell 当成命令执行。
func TestCompletionInstallPrintWritesNothingAndKeepsOutPure(t *testing.T) {
	fs := afero.NewMemMapFs()
	printed := newTestEnv(t, fs, "", false)
	stubCompletion(t, "linux", completionShellVars("/bin/bash"))

	if err := execCompletion(t, printed, completionActionInstall, "--print", "--shell", "bash"); err != nil {
		t.Fatalf("install --print failed | install --print 失败: %v", err)
	}

	direct := newTestEnv(t, fs, "", false)
	if err := execCompletion(t, direct, "bash"); err != nil {
		t.Fatalf("completion bash failed | completion bash 失败: %v", err)
	}
	if printed.out.String() != direct.out.String() {
		t.Error("install --print does not print exactly the generated script | install --print 打印的并非纯粹的生成脚本")
	}
	if !strings.HasPrefix(printed.out.String(), "# yd bash completion") {
		t.Errorf("Out does not start with the script | Out 的开头不是脚本:\n%s", printed.out.String())
	}
	if !strings.Contains(printed.errs.String(), "script | 脚本:") {
		t.Errorf("the plan was not reported on Err | 方案没有报到 Err 上:\n%s", printed.errs.String())
	}
	if ok, _ := afero.Exists(fs, filepath.Join(completionTestHome, ".yd", "completion.bash")); ok {
		t.Error("install --print wrote a file | install --print 写了文件")
	}
	if ok, _ := afero.Exists(fs, filepath.Join(completionTestHome, ".bashrc")); ok {
		t.Error("install --print created a startup file | install --print 创建了启动文件")
	}
}

// TestCompletionInstallAppendRCIsIdempotentAndBackedUp 追加、备份与幂等。
// TestCompletionInstallAppendRCIsIdempotentAndBackedUp covers the append, the backup and the idempotency.
func TestCompletionInstallAppendRCIsIdempotentAndBackedUp(t *testing.T) {
	fs := afero.NewMemMapFs()
	rc := filepath.Join(completionTestHome, ".bashrc")
	original := []byte("# mine | 我的\nalias ll='ls -l'\n")
	if err := afero.WriteFile(fs, rc, original, 0o644); err != nil {
		t.Fatalf("seed failed | 预置失败: %v", err)
	}
	te := newTestEnv(t, fs, "", false)
	stubCompletion(t, "linux", completionShellVars("/bin/bash"))

	if err := execCompletion(t, te, completionActionInstall, "--append-rc"); err != nil {
		t.Fatalf("install --append-rc failed | install --append-rc 失败: %v", err)
	}
	out := te.out.String()
	backup := rc + ".yd-backup"
	if !strings.Contains(out, "backup | 备份: "+backup) {
		t.Errorf("the report does not name the backup | 报告没有点名备份:\n%s", out)
	}
	if !strings.Contains(out, "appended | 已追加: "+rc) {
		t.Errorf("the report does not name the changed file | 报告没有点名被改动的文件:\n%s", out)
	}

	copied, err := afero.ReadFile(fs, backup)
	if err != nil {
		t.Fatalf("read backup failed | 读取备份失败: %v", err)
	}
	if string(copied) != string(original) {
		t.Errorf("the backup is not the original file | 备份与原文件不一致:\n%q", copied)
	}

	after, err := afero.ReadFile(fs, rc)
	if err != nil {
		t.Fatalf("read startup file failed | 读取启动文件失败: %v", err)
	}
	if strings.Count(string(after), completion.Marker) != 1 {
		t.Errorf("the marker appears %d times | 标记出现次数不为 1:\n%s", strings.Count(string(after), completion.Marker), after)
	}
	if !strings.HasPrefix(string(after), string(original)) {
		t.Errorf("the original text was not preserved | 原文没有保留:\n%q", after)
	}

	// The second run must change nothing at all. | 第二次运行必须什么都不改。
	te.out.Reset()
	te.errs.Reset()
	if err := execCompletion(t, te, completionActionInstall, "--append-rc"); err != nil {
		t.Fatalf("second install failed | 第二次安装失败: %v", err)
	}
	second, err := afero.ReadFile(fs, rc)
	if err != nil {
		t.Fatalf("read startup file failed | 读取启动文件失败: %v", err)
	}
	if string(second) != string(after) {
		t.Errorf("the second run changed the startup file | 第二次运行改动了启动文件:\n%q", second)
	}
	if ok, _ := afero.Exists(fs, backup+".2"); ok {
		t.Error("the second run created a numbered backup | 第二次运行创建了编号备份")
	}
	if !strings.Contains(te.out.String(), "已含标记") {
		t.Errorf("the second run did not report the marker | 第二次运行没有报告已含标记:\n%s", te.out.String())
	}
}

// TestCompletionInstallPromptsOnlyOnATTY 只有 TTY 交互运行才会提问，且 --yes 连问都不问。
// TestCompletionInstallPromptsOnlyOnATTY prompts only in an interactive TTY, and --yes does not even ask.
func TestCompletionInstallPromptsOnlyOnATTY(t *testing.T) {
	t.Run("y appends | 回答 y 则追加", func(t *testing.T) {
		te := completionEnv(t, "linux", completionShellVars("/bin/bash"), "y\n", true)
		if err := execCompletion(t, te, completionActionInstall); err != nil {
			t.Fatalf("install failed | 安装失败: %v", err)
		}
		rc := filepath.Join(completionTestHome, ".bashrc")
		if !strings.Contains(te.out.String(), "appended | 已追加: "+rc) {
			t.Errorf("a yes answer did not append | 回答 y 却没有追加:\n%s", te.out.String())
		}
		data, err := afero.ReadFile(te.env.FS, rc)
		if err != nil {
			t.Fatalf("the startup file was not created | 启动文件没有被创建: %v", err)
		}
		if !strings.Contains(string(data), completion.Marker) {
			t.Errorf("the marker is missing | 缺少标记:\n%s", data)
		}
	})

	t.Run("the default is no | 缺省为否", func(t *testing.T) {
		te := completionEnv(t, "linux", completionShellVars("/bin/bash"), "\n", true)
		if err := execCompletion(t, te, completionActionInstall); err != nil {
			t.Fatalf("install failed | 安装失败: %v", err)
		}
		rc := filepath.Join(completionTestHome, ".bashrc")
		if ok, _ := afero.Exists(te.env.FS, rc); ok {
			t.Error("the default answer appended the line | 缺省回答却追加了加载行")
		}
		if !strings.Contains(te.out.String(), "unchanged, pass --append-rc") {
			t.Errorf("the report does not say the file was left alone | 报告没有说明文件未被改动:\n%s", te.out.String())
		}
	})

	t.Run("--yes never asks | --yes 不提问", func(t *testing.T) {
		te := completionEnv(t, "linux", completionShellVars("/bin/bash"), "", true)
		te.env.In = failingReader{t}
		if err := execCompletion(t, te, completionActionInstall, "--yes"); err != nil {
			t.Fatalf("install --yes failed | install --yes 失败: %v", err)
		}
		rc := filepath.Join(completionTestHome, ".bashrc")
		if ok, _ := afero.Exists(te.env.FS, rc); ok {
			t.Error("--yes appended the line | --yes 追加了加载行")
		}
	})
}

// TestCompletionUsageErrors 用法错误必须带用法块并以哨兵失败。
// TestCompletionUsageErrors reports usage errors with the usage block and a sentinel.
func TestCompletionUsageErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no action | 缺少动作", nil},
		{"unknown action | 未知动作", []string{"fish"}},
		{"extra word after a shell | shell 之后多出词", []string{"bash", "extra"}},
		{"extra word after detect | detect 之后多出词", []string{completionActionDetect, "extra"}},
		{"install flag on detect | detect 上的 install 标志", []string{completionActionDetect, "--print"}},
		{"print with append-rc | print 与 append-rc 并用", []string{completionActionInstall, "--print", "--append-rc"}},
		{"extra word after install | install 之后多出词", []string{completionActionInstall, "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			te := completionEnv(t, "linux", completionShellVars("/bin/bash"), "", false)
			if err := execCompletion(t, te, tc.args...); !errors.Is(err, ErrNoCommand) {
				t.Fatalf("err = %v, want ErrNoCommand | 期望 ErrNoCommand", err)
			}
			if !strings.Contains(te.errs.String(), "Usage | 用法:") {
				t.Errorf("the usage block is missing | 缺少用法块:\n%s", te.errs.String())
			}
		})
	}
}

// TestCompletionBashScriptIsValidAndCompletes bash 脚本必须通过 bash -n、真的能被 source 并给出正确候选。
// TestCompletionBashScriptIsValidAndCompletes parses, sources and interrogates the bash script.
//
// The script is written to a real file and handed to a real bash, because a completion script's only
// meaningful test is what the shell does with it: `bash -n` proves it parses, sourcing it proves it loads
// silently, and the four COMPREPLY probes prove it offers commands at the command position, flags after a
// command, nothing after a value-taking flag and nothing at a positional argument. | 脚本被写入真实文件并交给
// 真实的 bash，因为补全脚本唯一有意义的测试就是 shell 会拿它做什么：`bash -n` 证明它可解析，source 证明它加载时
// 安静，四个 COMPREPLY 探针则证明它在命令位置给出命令、在命令之后给出标志、在取值标志之后什么都不给、在位置参数处
// 什么都不给。
func TestCompletionBashScriptIsValidAndCompletes(t *testing.T) {
	bin, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("skipped: bash is not on PATH, so the script cannot be sourced here | 跳过：PATH 上没有 bash，本机无法 source 脚本")
	}

	te := completionEnv(t, "linux", completionShellVars("/bin/bash"), "", false)
	if err := execCompletion(t, te, "bash"); err != nil {
		t.Fatalf("completion bash failed | completion bash 失败: %v", err)
	}
	path := filepath.Join(t.TempDir(), "completion.bash")
	if err := os.WriteFile(path, te.out.Bytes(), 0o644); err != nil {
		t.Fatalf("write failed | 写入失败: %v", err)
	}
	if out, err := exec.Command(bin, "--noprofile", "--norc", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash -n rejected the script | bash -n 拒绝了脚本: %v\n%s", err, out)
	}

	// Every line of the output is prefixed with what it reports, so anything else that reaches the console
	// while the script is loaded - a word, a warning - fails the test instead of hiding in the scrollback.
	// | 输出的每一行都带前缀标明它报告的内容，因此加载脚本时冒出来的任何别的输出（一个词、一句警告）都会让测试失败，
	// 而不是消失在回滚缓冲里。
	driver := `source "$1" || exit 9
reg=$(complete -p "$2") || exit 8
echo "reg:${reg}"
COMP_WORDS=("$2" ''); COMP_CWORD=1; _yd_completion; echo "cmds:${#COMPREPLY[@]}:${COMPREPLY[*]}"
COMP_WORDS=("$2" build --); COMP_CWORD=2; _yd_completion; echo "flags:${#COMPREPLY[@]}:${COMPREPLY[*]}"
COMP_WORDS=("$2" build --output ''); COMP_CWORD=3; _yd_completion; echo "value:${#COMPREPLY[@]}:${COMPREPLY[*]}"
COMP_WORDS=("$2" gen ''); COMP_CWORD=2; _yd_completion; echo "pos:${#COMPREPLY[@]}:${COMPREPLY[*]}"
COMP_WORDS=("$2" --); COMP_CWORD=1; _yd_completion; echo "globals:${#COMPREPLY[@]}:${COMPREPLY[*]}"`
	out, err := exec.Command(bin, "--noprofile", "--norc", "-c", driver, "yd-completion-test", filepath.ToSlash(path), "yd").CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing the script failed | source 脚本失败: %v\n%s", err, out)
	}

	probes := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimRight(line, "\r")
		name, rest, found := strings.Cut(line, ":")
		switch {
		case !found:
			t.Fatalf("unexpected output while loading the script | 加载脚本时出现意外输出: %q", line)
		case name == "reg", name == "cmds", name == "flags", name == "value", name == "pos", name == "globals":
			probes[name] = rest
		default:
			t.Fatalf("unexpected output while loading the script | 加载脚本时出现意外输出: %q", line)
		}
	}

	if !strings.Contains(probes["reg"], "_yd_completion") {
		t.Errorf("bash registered %q | bash 登记的并非本次补全: %q", probes["reg"], probes["reg"])
	}
	cmds := strings.Split(probes["cmds"], ":")[1]
	for _, name := range registeredCommandNames() {
		if !strings.Contains(" "+cmds+" ", " "+name+" ") {
			t.Errorf("yd <TAB> does not offer %q | yd <TAB> 没有给出 %q: %s", name, name, cmds)
		}
	}
	flags := strings.Split(probes["flags"], ":")[1]
	for _, want := range []string{"--config", "--output", "--tailwind", "--version"} {
		if !strings.Contains(" "+flags+" ", " "+want+" ") {
			t.Errorf("yd build --<TAB> does not offer %q | yd build --<TAB> 没有给出 %q: %s", want, want, flags)
		}
	}
	if count := strings.Split(probes["value"], ":")[0]; count != "0" {
		t.Errorf("yd build --output <TAB> offered %s candidates | yd build --output <TAB> 给出了 %s 个候选: %s", count, count, probes["value"])
	}
	if count := strings.Split(probes["pos"], ":")[0]; count != "0" {
		t.Errorf("yd gen <TAB> offered %s candidates | yd gen <TAB> 给出了 %s 个候选: %s", count, count, probes["pos"])
	}
	globals := strings.Split(probes["globals"], ":")[1]
	for _, want := range []string{"--config", "--env-file", "--verbose", "--quiet", "--no-color", "--version"} {
		if !strings.Contains(" "+globals+" ", " "+want+" ") {
			t.Errorf("yd --<TAB> does not offer %q | yd --<TAB> 没有给出 %q: %s", want, want, globals)
		}
	}
}

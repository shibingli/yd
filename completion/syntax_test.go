// This file checks the generated scripts with the real interpreters, on the machines that have them. | 本文件
// 用真实解释器检查生成的脚本，且只在装了该解释器的机器上这么做。
//
// A generator that produces text is only as good as the parser that accepts it, so every shell script is
// written to a temporary file and handed to `bash -n`, `zsh -n`, `csh -n` or the PowerShell parser. When an
// interpreter is missing the subtest is skipped with the reason printed - never silently passed, because a
// skipped check and a passing check must not look alike. | 生成文本的生成器，其品质只由接受它的解析器决定，
// 因此每份脚本都会被写入临时文件并交给 `bash -n`、`zsh -n`、`csh -n` 或 PowerShell 解析器。缺少解释器时该子测试
// 会带原因跳过——绝不静默通过，因为被跳过的检查与通过的检查不能看起来一样。

package completion

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// scriptCheck is one shell's real syntax check. | scriptCheck 是某个 shell 的真实语法检查。
type scriptCheck struct {
	// shell is the shell this check belongs to. | shell 是本次检查所属的 shell。
	shell Shell
	// bins are the interpreter names, tried in order; the first one on PATH is used. | bins 是解释器名字，
	// 按顺序尝试，PATH 上第一个可用的被采用。
	bins []string
	// args builds the argument list that parses path without running it. | args 构造解析 path 而不执行它的参数列表。
	args func(path string) []string
}

// scriptChecks lists the four checks in the order of Shells. | scriptChecks 按 Shells 的顺序列出四种检查。
func scriptChecks() []scriptCheck {
	return []scriptCheck{
		// -n reads the file and stops before running a single command. | -n 读取文件，并在执行任何一条命令之前停下。
		{shell: Bash, bins: []string{"bash"}, args: func(path string) []string {
			return []string{"-n", path}
		}},
		// -f keeps the user's own startup files out of the run, so a broken .zshrc cannot fail this test.
		// | -f 把用户自己的启动文件排除在外，因此一个坏掉的 .zshrc 不会让本测试失败。
		{shell: Zsh, bins: []string{"zsh"}, args: func(path string) []string {
			return []string{"-f", "-n", path}
		}},
		// tcsh is accepted as a stand-in for csh: the two share the `complete` builtin the script uses,
		// and many systems ship only tcsh. | tcsh 可以代替 csh：二者共享脚本所用的 `complete` 内建命令，
		// 且许多系统只带 tcsh。
		{shell: Csh, bins: []string{"csh", "tcsh"}, args: func(path string) []string {
			return []string{"-f", "-n", path}
		}},
		// PowerShell has no -n; its parser is called directly and reports the parse errors instead. | PowerShell
		// 没有 -n；这里直接调用它的解析器并汇报解析错误。
		{shell: PowerShell, bins: []string{"pwsh", "powershell"}, args: func(path string) []string {
			// The path is a temporary path, but it is still quoted: an embedded quote would otherwise end
			// the PowerShell string and turn the path into code. | 路径是临时路径，但依然要引用：否则其中的
			// 引号会提前结束 PowerShell 字符串，把路径变成代码。
			quoted := quotePowerShell(path)
			driver := "$tokens=$null;$errors=$null;" +
				"[System.Management.Automation.Language.Parser]::ParseFile(" + quoted + ",[ref]$tokens,[ref]$errors)|Out-Null;" +
				"if($errors.Count -gt 0){$errors|ForEach-Object{Write-Host $_.ToString()};exit 1}"
			return []string{"-NoProfile", "-NonInteractive", "-Command", driver}
		}},
	}
}

// checkFor returns the syntax check of one shell. | checkFor 返回某个 shell 的语法检查。
func checkFor(t *testing.T, sh Shell) scriptCheck {
	t.Helper()
	for _, check := range scriptChecks() {
		if check.shell == sh {
			return check
		}
	}
	t.Fatalf("no syntax check for %s | 没有为 %s 定义语法检查", sh, sh)
	return scriptCheck{}
}

// lookInterpreter returns the first interpreter of a check that is on PATH. | lookInterpreter 返回该检查在
// PATH 上的第一个解释器。
func lookInterpreter(check scriptCheck) (string, bool) {
	for _, name := range check.bins {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}

// skipMissingInterpreter records why a shell cannot be checked here. | skipMissingInterpreter 记录某个 shell
// 为何无法在本机被检查。
//
// The skip message is the record: it is printed in the test output, so a run that checked nothing says so
// instead of looking green. | 跳过信息本身就是记录：它会被打印进测试输出，因此一次什么都没检查的运行会明确说出来，
// 而不是看起来一片绿。
func skipMissingInterpreter(t *testing.T, check scriptCheck) {
	t.Helper()
	t.Skipf("skipped: none of %[1]s is on PATH, so the %[2]s script cannot be syntax checked on this machine | 跳过：PATH 上既没有 %[1]s，本机无法对 %[2]s 脚本做语法检查",
		strings.Join(check.bins, ", "), check.shell)
}

// writeScript renders a script and writes it to a temporary file. | writeScript 渲染脚本并写入临时文件。
func writeScript(t *testing.T, sh Shell, reg Registry) string {
	t.Helper()
	script, err := Generate(sh, reg)
	if err != nil {
		t.Fatalf("generate %s failed | 生成 %s 失败: %v", sh, sh, err)
	}
	path := filepath.Join(t.TempDir(), sh.FileName())
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatalf("write %s failed | 写入 %s 失败: %v", path, path, err)
	}
	return path
}

// runSyntaxMatrix checks every shell script of one registry with its real interpreter. | runSyntaxMatrix
// 用真实解释器检查某个注册表的每一份脚本。
func runSyntaxMatrix(t *testing.T, label string, reg Registry) {
	t.Helper()
	t.Run(label, func(t *testing.T) {
		checked, skipped := 0, 0
		for _, sh := range Shells() {
			t.Run(string(sh), func(t *testing.T) {
				check := checkFor(t, sh)
				bin, ok := lookInterpreter(check)
				if !ok {
					skipped++
					skipMissingInterpreter(t, check)
				}
				checked++
				path := writeScript(t, sh, reg)
				out, err := exec.Command(bin, check.args(path)...).CombinedOutput()
				if err != nil {
					t.Fatalf("%s rejected the generated script | %s 拒绝了生成的脚本: %v\n%s", bin, bin, err, out)
				}
				if text := strings.TrimSpace(string(out)); text != "" {
					t.Logf("%s reported | %s 报告: %s", bin, bin, text)
				}
			})
		}
		t.Logf("syntax checks: %[1]d run, %[2]d skipped | 语法检查：已执行 %[1]d 项，跳过 %[2]d 项", checked, skipped)
	})
}

// TestGeneratedScriptsAreSyntacticallyValid 四份生成脚本必须被各自的解释器接受。
// TestGeneratedScriptsAreSyntacticallyValid requires all four generated scripts to be accepted by their own interpreter.
func TestGeneratedScriptsAreSyntacticallyValid(t *testing.T) {
	runSyntaxMatrix(t, "fixture registry | 夹具注册表", fixtureRegistry())
}

// TestGeneratedScriptsSurviveHostileText 含引号、美元符与占位符的文案不得弄坏任何一份脚本。
// TestGeneratedScriptsSurviveHostileText keeps hostile text from breaking any of the scripts.
//
// Summaries and help texts are the free text of a command registry: they are translated, quoted, and
// sometimes contain a dollar sign, a percent sign or the name of a placeholder of the generators. Those
// strings travel into the zsh and PowerShell scripts, so they are the realistic place to be hostile. | 摘要与
// 帮助文案是命令注册表里的自由文本：会被翻译、会被引用，有时还带美元符、百分号，甚至生成器自己的占位符名字。
// 这些字符串会进入 zsh 与 PowerShell 脚本，因此攻击性文本放在那里才是现实的。
func TestGeneratedScriptsSurviveHostileText(t *testing.T) {
	runSyntaxMatrix(t, "hostile text | 攻击性文本", hostileTextRegistry())
}

// TestGeneratedBashScriptLoadsAndRegisters bash 脚本必须真的能被 source 并完成注册。
// TestGeneratedBashScriptLoadsAndRegisters requires the bash script to really load and register itself.
//
// `bash -n` only proves the text parses; sourcing it proves the other half - that loading the script is
// silent and that it registers a completion for the program. The output is compared exactly, so a stray
// word printed while sourcing (the `a='b' 'c'` trap of a word list written as several shell words) fails
// here rather than in a user's shell. | `bash -n` 只证明文本能被解析；source 一遍才证明另一半——加载脚本是安静的，
// 并且确实为程序名登记了补全。这里对输出做精确比对，因此 source 时多打出的一个词（把词表写成多个 shell 词会踩到的
// `a='b' 'c'` 陷阱）会在这里失败，而不是在用户的 shell 里失败。
func TestGeneratedBashScriptLoadsAndRegisters(t *testing.T) {
	check := checkFor(t, Bash)
	bin, ok := lookInterpreter(check)
	if !ok {
		skipMissingInterpreter(t, check)
	}
	path := writeScript(t, Bash, fixtureRegistry())

	// $1 is the script, $2 is the program word; -c takes the program name as the next argument. | $1 是脚本，
	// $2 是程序词；-c 之后的第一个参数是程序名。
	driver := `source "$1" || exit 9; complete -p "$2"`
	out, err := exec.Command(bin, "--noprofile", "--norc", "-c", driver, "yd-completion-test", path, fixtureRegistry().Program).CombinedOutput()
	if err != nil {
		t.Fatalf("source failed | source 失败: %v\n%s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), "complete -o default -F _yd_completion yd"; got != want {
		t.Errorf("loading the script printed or registered %q, want %q | 加载脚本的输出或注册结果不符", got, want)
	}
}

// powerShellProbeDriver asks PowerShell what it would offer at five cursors, one line per cursor. | powerShellProbeDriver
// 问 PowerShell 在五个光标位置会给出什么候选，每个光标一行。
//
// `yd` itself has to resolve before the completion engine will even consider the native completer, so the
// driver puts its own directory - where the test wrote a stub program - in front of PATH. The stub exists
// only for this process. | 只有 `yd` 本身能被解析，补全引擎才会考虑这个原生补全器，因此驱动脚本把自己所在目录
// （测试在那里写了桩程序）放到 PATH 最前面。桩程序只对这个进程存在。
const powerShellProbeDriver = `$env:PATH = "$PSScriptRoot;$env:PATH"
. %SCRIPT%
foreach ($line in @('yd ', 'yd bu', 'yd build -', 'yd build --', 'yd --', 'yd build --output ')) {
    $matches = @((TabExpansion2 $line $line.Length).CompletionMatches | ForEach-Object { $_.CompletionText })
    Write-Host ("probe:{0}={1}" -f $line, ($matches -join ' '))
}`

// powerShellStub returns the file name, content and mode of the stand-in program. | powerShellStub 返回替身程序
// 的文件名、内容与权限位。
//
// Each platform names an executable its own way, so the stub is a batch file on Windows and a shell script
// everywhere else; nothing ever runs it, it only has to be found. | 各平台命名可执行文件的方式不同，因此桩程序在
// Windows 上是批处理文件，在其他平台上是 shell 脚本；它永远不会被真正执行，只需要能被找到。
func powerShellStub(program string) (string, string, os.FileMode) {
	if runtime.GOOS == "windows" {
		return program + ".cmd", "@echo off\n", 0o644
	}
	return program, "#!/bin/sh\nexit 0\n", 0o755
}

// TestGeneratedPowerShellScriptCompletes PowerShell 脚本必须真的能被点源，并给出候选。
// TestGeneratedPowerShellScriptCompletes requires the PowerShell script to really load and offer candidates.
//
// This is the check that the bash file gets too, and it earns its keep twice over: the dot-source operator
// loads `.ps1` files only, so a script installed under any other suffix would load in total silence and
// define nothing at all, and the four cursors below pin down which word list is offered where. | 这是 bash 那份
// 也有的检查，而它值两个价钱：点源运算符只加载 `.ps1` 文件，因此以别的后缀安装的脚本会被完全静默地加载、
// 什么都不定义；而下面四个光标位置钉住了在何处给出哪份词表。
func TestGeneratedPowerShellScriptCompletes(t *testing.T) {
	check := checkFor(t, PowerShell)
	bin, ok := lookInterpreter(check)
	if !ok {
		skipMissingInterpreter(t, check)
	}

	dir := t.TempDir()
	name, content, mode := powerShellStub(fixtureRegistry().Program)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), mode); err != nil {
		t.Fatalf("write the stand-in program failed | 写入替身程序失败: %v", err)
	}

	// The script keeps the name FileName gives it: that name is what makes the load line printed into the
	// user's $PROFILE work. | 脚本沿用 FileName 给它的名字：正是这个名字让打印进用户 $PROFILE 的加载行真正生效。
	script := writeScript(t, PowerShell, fixtureRegistry())
	driver := filepath.Join(dir, "probe.ps1")
	body := fill(powerShellProbeDriver, "%SCRIPT%", quotePowerShell(script))
	if err := os.WriteFile(driver, []byte(body), 0o644); err != nil {
		t.Fatalf("write the probe driver failed | 写入探测脚本失败: %v", err)
	}

	out, err := exec.Command(bin, "-NoProfile", "-NonInteractive", "-File", driver).CombinedOutput()
	if err != nil {
		t.Fatalf("probing completion failed | 探测补全失败: %v\n%s", err, out)
	}
	offers := parseProbes(t, string(out))

	want := []string{"yd ", "yd bu", "yd build -", "yd build --", "yd --", "yd build --output "}
	for _, line := range want {
		if _, seen := offers[line]; !seen {
			t.Fatalf("no probe result for %q | 缺少该行的探测结果: %q\n%s", line, line, out)
		}
	}

	// The empty word after the program name offers every command word, exactly once each. | 程序名之后的空词给出
	// 全部命令词，且每个只出现一次。
	assertOffers(t, offers, "yd ", commandWords(fixtureRegistry()))
	// A half-typed command word offers that command and nothing else: the word under the cursor is not yet a
	// known command, so it is treated as the command word being typed. | 只打了一半的命令词只给出那个命令：
	// 光标下的词还不是已知命令，因此它被当成正在输入的命令词。
	assertOffers(t, offers, "yd bu", []string{"build"})
	// A single dash after a known command word offers every spelling of that command's flags, its own and
	// the global ones it does not shadow. | 已知命令词之后的单个横线给出该命令标志的每种写法：它自身的，以及未被
	// 它遮蔽的全局标志。
	assertOffers(t, offers, "yd build -", fixtureArmWords["build"])
	// A double dash offers the long spellings of the same list, because the completer filters candidates by
	// the word being typed. | 双横线给出同一份词表中的长写法，因为补全器会按正在输入的词过滤候选。
	assertOffers(t, offers, "yd build --", longWords(fixtureArmWords["build"]))
	// A flag before any command word offers the global ones and none of the command-only ones. | 命令词之前的标志
	// 给出全局标志，且不含任何只属于命令的标志。
	assertOffers(t, offers, "yd --", longWords(flagWords(fixtureRegistry().GlobalFlags)))
	// The word after a value-taking flag is that flag's value, so no flag is offered for it. What PowerShell
	// puts in their place is the engine's own fallback - file names, as a rule - and not the script's
	// business, which is why this one is asserted differently. | 取值标志之后的词是它的取值，因此不为它提供任何
	// 标志。PowerShell 拿来补位的东西是引擎自己的回退行为（通常正是文件名），不属于脚本的职责，因此这一项的断言
	// 写法不同。
	for _, word := range offers["yd build --output "] {
		if strings.HasPrefix(word, "-") {
			t.Errorf("a flag %q was offered after a value-taking flag | 取值标志之后竟给出了标志: %q", word, word)
		}
	}
}

// parseProbes reads the driver's output into a cursor to offered words map. | parseProbes 把驱动脚本的输出读成
// 「光标 -> 给出的词」的映射。
//
// Anything the driver prints that is not a probe line fails the test: a completion script that talks while it
// loads would print here, and that is exactly the noise a user's shell must never see. | 驱动脚本打印的任何非探测行
// 都会让测试失败：加载时乱说话的补全脚本会在这里现形，而那正是用户的 shell 绝不该看到的噪音。
func parseProbes(t *testing.T, out string) map[string][]string {
	t.Helper()
	offers := make(map[string][]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		rest, found := strings.CutPrefix(line, "probe:")
		if !found {
			t.Fatalf("the script printed unexpected output | 脚本打印了意外输出: %q", line)
		}
		cursor, words, found := strings.Cut(rest, "=")
		if !found {
			t.Fatalf("malformed probe line | 探测行格式错误: %q", line)
		}
		offers[cursor] = strings.Fields(words)
	}
	return offers
}

// assertOffers compares one cursor's offers with what it must offer, order ignored. | assertOffers 比较某个光标
// 给出的候选与它必须给出的候选，忽略顺序。
//
// The order is ignored deliberately: one script sorts by name and another one may not, and that is not what
// this test is about. | 刻意忽略顺序：一份脚本按名字排序，另一份未必，而那不是本测试要管的事。
func assertOffers(t *testing.T, offers map[string][]string, cursor string, want []string) {
	t.Helper()
	got := slices.Clone(offers[cursor])
	slices.Sort(got)
	expect := slices.Clone(want)
	slices.Sort(expect)
	if !slices.Equal(got, expect) {
		t.Errorf("offers for %q = %v, want %v | 该光标处的候选不符", cursor, got, expect)
	}
}

// commandWords returns the command names of a registry. | commandWords 返回注册表的命令名。
func commandWords(r Registry) []string {
	names := make([]string, 0, len(r.Commands))
	for _, c := range r.Commands {
		names = append(names, c.Name)
	}
	return names
}

// longWords returns the double-dash spellings of a word list. | longWords 返回词表中的双横线写法。
//
// The completer filters its candidates by the word being typed, so a probe that stopped at `--` can only be
// answered with the long spellings; this helper states that expectation instead of hiding it in a second
// fixture. | 补全器会按正在输入的词过滤候选，因此停在 `--` 的探测只可能得到长写法；这个辅助函数把这条期望写出来，
// 而不是把它藏进第二份夹具。
func longWords(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if strings.HasPrefix(w, "--") {
			out = append(out, w)
		}
	}
	return out
}

// hostileTextRegistry carries the text a carelessly generated script would choke on. | hostileTextRegistry
// 携带一份粗心生成的脚本会噎住的文本。
//
// The `%COMMANDS%` inside it is deliberate: that is a placeholder of the generators themselves, and the
// single-pass substitution is what must keep it from turning into a second substitution. | 其中的 `%COMMANDS%`
// 是刻意放的：它是生成器自己的占位符，而单趟替换正是阻止它变成第二次替换的原因。
func hostileTextRegistry() Registry {
	nasty := "he said \"hi\" and 'bye'; $HOME `whoami` ${VAR} \\ C:\\path %s %COMMANDS% 中文 —— 引号与美元符"
	return Registry{
		Program:  "yd",
		ToolsDir: ".yd/tools",
		GlobalFlags: []Flag{
			{Name: "verbose", Shorthand: "v", Usage: nasty},
		},
		Commands: []Command{
			{Name: "build", Short: nasty, Flags: []Flag{
				{Name: "output", Shorthand: "o", Usage: nasty, TakesValue: true},
			}},
			{Name: "run-now", Short: "CJK only | 仅中文摘要", Flags: []Flag{
				{Name: "dry-run", Usage: nasty},
			}},
		},
	}
}

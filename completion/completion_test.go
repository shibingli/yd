// This file tests generation: that all four scripts are rendered from one registry view, that every
// command and every flag reaches every script, and that a value-taking flag stays distinguishable from a
// boolean one. | 本文件测试生成：四份脚本都由同一份注册表视图渲染、每个命令与每个标志都进入每份脚本、
// 且需要取值的标志与布尔标志始终可分。

package completion

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// fixtureRegistry is the registry every generation test renders. | fixtureRegistry 是每个生成测试所渲染的注册表。
//
// It carries the three shapes a script has to survive: a global flag, a command that shadows one of them
// (`assets --version` takes a value while `yd --version` is a switch - the live case in this repository),
// and a command with no flags of its own (gen), whose list is therefore the global flags alone. | 它带上脚本
// 必须扛住的三件事：一个全局标志、一个遮蔽了其中之一的命令（`assets --version` 需要取值，而 `yd --version`
// 是开关——本仓的真实情形），以及一个自身没有标志的命令（gen），它的词表因此只有全局标志。
func fixtureRegistry() Registry {
	return Registry{
		Program:  "yd",
		ToolsDir: ".yd/tools",
		GlobalFlags: []Flag{
			{Name: "config", Shorthand: "c", Usage: "config file | 配置文件", TakesValue: true},
			{Name: "verbose", Shorthand: "v", Usage: "verbose logging | 详细日志"},
			{Name: "version", Usage: "print the version | 打印版本"},
		},
		Commands: []Command{
			{Name: "assets", Short: "manage assets | 管理资产", Flags: []Flag{
				{Name: "version", Usage: "asset version | 资产版本", TakesValue: true},
				{Name: "local", Usage: "local assets only | 仅本地资产"},
			}},
			{Name: "build", Short: "build the project | 构建项目", Flags: []Flag{
				{Name: "output", Shorthand: "o", Usage: "output directory | 输出目录", TakesValue: true},
				{Name: "release", Usage: "release build | 发布构建"},
			}},
			{Name: "gen", Short: "generate code | 生成代码"},
			{Name: "new", Short: "scaffold a project | 创建项目", Flags: []Flag{
				{Name: "name", Shorthand: "n", Usage: "project name | 项目名", TakesValue: true},
			}},
		},
	}
}

// fixtureValueWords is the value-taking vocabulary the fixture must produce. | fixtureValueWords 是夹具必须产出的取值词表。
//
// The `--version` entry belongs to the assets command, not to the global switch of the same name, so this
// expectation also pins down that a command's own definition wins over the global one. | `--version` 这一项
// 属于 assets 命令而不是同名的全局开关，因此这份期望同时也钉住了「命令自身的定义优先于全局定义」。
var fixtureValueWords = []string{"--config", "--name", "--output", "--version", "-c", "-n", "-o"}

// fixtureGlobalWords is the global flag vocabulary, in the order the flags are sorted by. | fixtureGlobalWords
// 是全局标志词表，顺序即标志排序后的顺序。
var fixtureGlobalWords = []string{"--config", "-c", "--verbose", "-v", "--version"}

// fixtureArmWords maps a command to the flags its own completion list must offer: its own flags first,
// then the global flags it does not shadow. | fixtureArmWords 把命令映射到它自己的补全列表中必须提供的标志：
// 先是它自身的标志，随后是它未遮蔽的全局标志。
var fixtureArmWords = map[string][]string{
	"assets": {"--config", "-c", "--local", "--verbose", "-v", "--version"},
	"build":  {"--config", "-c", "--output", "-o", "--release", "--verbose", "-v", "--version"},
	"gen":    {"--config", "-c", "--verbose", "-v", "--version"},
	"new":    {"--config", "-c", "--name", "-n", "--verbose", "-v", "--version"},
}

// scriptLine returns the first line of script that starts with prefix. | scriptLine 返回脚本中以 prefix 开头的第一行。
func scriptLine(t *testing.T, script, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("script has no line starting with %q | 脚本缺少以该前缀开头的行: %q", prefix, prefix)
	return ""
}

// quotedWords extracts every single-quoted word of a line, undoing quoteSingle. | quotedWords 提取一行中每个
// 单引号包裹的词，并还原 quoteSingle。
func quotedWords(line string) []string {
	words := make([]string, 0, 4)
	for i := 0; i < len(line); i++ {
		if line[i] != '\'' {
			continue
		}
		var b strings.Builder
		for i++; i < len(line); i++ {
			if line[i] != '\'' {
				b.WriteByte(line[i])
				continue
			}
			if strings.HasPrefix(line[i:], `'\''`) {
				b.WriteByte('\'')
				i += 3
				continue
			}
			break
		}
		words = append(words, b.String())
	}
	return words
}

// bashWordList returns the words of one bash assignment line. | bashWordList 返回某条 bash 赋值语句中的词。
func bashWordList(t *testing.T, script, name string) []string {
	t.Helper()
	return strings.Fields(strings.Join(quotedWords(scriptLine(t, script, name+"=")), " "))
}

// bashArm returns the candidate words of one command's case arm. | bashArm 返回某个命令分支的候选词。
func bashArm(t *testing.T, script, cmd string) []string {
	t.Helper()
	line := scriptLine(t, script, "            "+cmd+") COMPREPLY=($(compgen -W \"")
	_, rest, _ := strings.Cut(line, `compgen -W "`)
	words, _, _ := strings.Cut(rest, `"`)
	return strings.Fields(words)
}

// powerShellWordList returns the words of one PowerShell array line. | powerShellWordList 返回某条 PowerShell
// 数组行的词。
//
// The words are read back out of the generated text by splitting on the quote character, so an assertion
// made with this helper compares what the script actually says rather than what a helper of the generator
// would have said. | 这些词是按引号切分、从生成的文本里读回来的，因此用它做的断言比较的是脚本真正写下的内容，
// 而不是生成器的某个辅助函数会写出的内容。
func powerShellWordList(t *testing.T, script, prefix string) []string {
	t.Helper()
	parts := strings.Split(scriptLine(t, script, prefix), "'")
	words := make([]string, 0, len(parts)/2)
	for i := 1; i < len(parts); i += 2 {
		words = append(words, parts[i])
	}
	return words
}

// cshNextWord returns the word list of one csh next-word rule. | cshNextWord 返回某条 csh 下一个词规则的词表。
//
// The rules of one `complete` statement are indented continuation lines, so they are located by content
// rather than by a line prefix. | 同一条 `complete` 语句的规则是缩进的续行，因此按内容而不是行首前缀定位。
func cshNextWord(t *testing.T, script, cmd string) []string {
	t.Helper()
	prefix := "'n/" + cmd + "/("
	for _, line := range strings.Split(script, "\n") {
		start := strings.Index(line, prefix)
		if start < 0 {
			continue
		}
		rest := line[start+len(prefix):]
		end := strings.Index(rest, ")/'")
		if end < 0 {
			continue
		}
		return strings.Fields(rest[:end])
	}
	t.Fatalf("csh script has no next-word rule for %q | csh 脚本缺少该命令的规则: %q", cmd, cmd)
	return nil
}

// cshFirstWord returns the command words of the csh position rule. | cshFirstWord 返回 csh 位置规则中的命令词。
func cshFirstWord(t *testing.T, script string) []string {
	t.Helper()
	const rule = "p/1/("
	for _, line := range strings.Split(script, "\n") {
		start := strings.Index(line, rule)
		if start < 0 {
			continue
		}
		start += len(rule)
		end := strings.Index(line[start:], ")/")
		if end < 0 {
			continue
		}
		return strings.Fields(line[start : start+end])
	}
	t.Fatal("csh script has no p/1 rule | csh 脚本缺少 p/1 位置规则")
	return nil
}

// TestGenerateRendersEveryShell 四种 shell 都必须渲染出脚本，且同样输入渲染两次逐字节一致。
// TestGenerateRendersEveryShell requires all four shells to render, identically on repeated renders.
func TestGenerateRendersEveryShell(t *testing.T) {
	for _, sh := range Shells() {
		first, err := Generate(sh, fixtureRegistry())
		if err != nil {
			t.Fatalf("shell %q: generate failed | 生成失败: %v", sh, err)
		}
		if strings.TrimSpace(first) == "" {
			t.Errorf("shell %q: script is empty | 脚本为空", sh)
		}
		if !strings.HasSuffix(first, "\n") {
			t.Errorf("shell %q: script does not end with a newline | 脚本未以换行结尾", sh)
		}
		second, err := Generate(sh, fixtureRegistry())
		if err != nil {
			t.Fatalf("shell %q: second generate failed | 第二次生成失败: %v", sh, err)
		}
		if first != second {
			t.Errorf("shell %q: two renders differ | 两次渲染不一致", sh)
		}
	}
}

// TestGenerateCoversEveryCommandInEveryShell 每个命令都必须出现在四种脚本里。
// TestGenerateCoversEveryCommandInEveryShell requires every command to appear in all four scripts.
//
// This is the red-if-forgotten check of the whole feature: a command that is missing from one script means
// somebody hand-wrote or hand-edited a script instead of generating it. | 这是整个特性「漏了就红」的那一项检查：
// 某个命令在某一份脚本里缺席，意味着有人手写或手工改动过脚本，而不是由注册表生成。
func TestGenerateCoversEveryCommandInEveryShell(t *testing.T) {
	reg := fixtureRegistry()
	names := []string{"assets", "build", "gen", "new"}
	for _, sh := range Shells() {
		script, err := Generate(sh, reg)
		if err != nil {
			t.Fatalf("shell %q: generate failed | 生成失败: %v", sh, err)
		}
		for _, name := range names {
			if !strings.Contains(script, name) {
				t.Errorf("shell %q: command %q is missing | 缺少命令: %q", sh, name, name)
			}
		}
		if !strings.Contains(script, reg.Program) {
			t.Errorf("shell %q: program name %q is missing | 缺少程序名: %q", sh, reg.Program, reg.Program)
		}
	}
}

// TestBashListsEveryFlagAndDistinguishesValues 校验 bash 的三张词表与逐命令分支。
// TestBashListsEveryFlagAndDistinguishesValues checks the three bash word lists and the per-command arms.
//
// The value-taking list is what tells a value position apart from a flag position, so it is compared as a
// whole set rather than merely searched for: a flag missing from it turns `yd build --output <TAB>` into a
// list of flags where a file name belongs. | 取值词表正是区分取值位置与标志位置的东西，因此这里按整套集合比对
// 而不是只做子串查找：漏掉一个标志，就会让 `yd build --output <TAB>` 在本该出现文件名的地方列出一堆标志。
func TestBashListsEveryFlagAndDistinguishesValues(t *testing.T) {
	script, err := Generate(Bash, fixtureRegistry())
	if err != nil {
		t.Fatalf("generate failed | 生成失败: %v", err)
	}

	cases := []struct {
		name string
		want []string
		got  []string
	}{
		{"_yd_commands", []string{"assets", "build", "gen", "new"}, bashWordList(t, script, "_yd_commands")},
		{"_yd_global_flags", fixtureGlobalWords, bashWordList(t, script, "_yd_global_flags")},
		{"_yd_value_flags", fixtureValueWords, bashWordList(t, script, "_yd_value_flags")},
	}
	for _, tc := range cases {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s = %v, want %v | 词表不符", tc.name, tc.got, tc.want)
		}
	}

	for cmd, want := range fixtureArmWords {
		if got := bashArm(t, script, cmd); !slices.Equal(got, want) {
			t.Errorf("arm %q = %v, want %v | 分支词表不符", cmd, got, want)
		}
	}
}

// TestPowerShellListsValueFlags powershell 也必须把需要取值的标志单列成表。
// TestPowerShellListsValueFlags requires powershell to list the value-taking flags separately.
//
// The other three scripts express the distinction in the shape of the shell - a word list of their own, a
// next-word rule; in PowerShell it is the table the completer consults before it offers anything, and
// without it `yd build --output <TAB>` lists flags where a value belongs. | 另外三份脚本用 shell 自身的形式表达
// 这个区分——单独的词表、下一个词规则；在 PowerShell 里它就是补全器出手之前先查的那张表，没有它，
// `yd build --output <TAB>` 就会在本该给出取值的地方列出标志。
func TestPowerShellListsValueFlags(t *testing.T) {
	script, err := Generate(PowerShell, fixtureRegistry())
	if err != nil {
		t.Fatalf("generate failed | 生成失败: %v", err)
	}
	if got := powerShellWordList(t, script, "    ValueFlags = @("); !slices.Equal(got, fixtureValueWords) {
		t.Errorf("ValueFlags = %v, want %v | 取值标志词表不符", got, fixtureValueWords)
	}
}

// TestZshAndPowerShellCarryDescriptions zsh 与 powershell 必须带上命令摘要与标志文案。
// TestZshAndPowerShellCarryDescriptions requires zsh and powershell to carry the summaries and help text.
//
// These two shells display a description next to every candidate, which is what makes completion double as
// documentation; a script without the texts is a script that lost half of its value. | 这两种 shell 会在每个
// 候选词旁显示描述，「补全即文档」指的就是这件事；丢掉文案的脚本也就丢掉了一半价值。
func TestZshAndPowerShellCarryDescriptions(t *testing.T) {
	cases := map[Shell][]string{
		Zsh: {
			"'assets:manage assets | 管理资产'",
			"'build:build the project | 构建项目'",
			"'--output:output directory | 输出目录'",
			"'-n:project name | 项目名'",
		},
		PowerShell: {
			"@{ Name = 'assets'; Description = 'manage assets | 管理资产' }",
			"@{ Name = '--output'; Description = 'output directory | 输出目录' }",
			"@{ Name = '-n'; Description = 'project name | 项目名' }",
		},
	}
	for sh, wants := range cases {
		script, err := Generate(sh, fixtureRegistry())
		if err != nil {
			t.Fatalf("shell %q: generate failed | 生成失败: %v", sh, err)
		}
		for _, want := range wants {
			if !strings.Contains(script, want) {
				t.Errorf("shell %q: description %q is missing | 缺少描述: %q", sh, want, want)
			}
		}
	}
}

// TestCshNextWordRules 校验 csh 的逐命令规则与位置规则。
// TestCshNextWordRules checks the csh per-command rules and the position rule.
func TestCshNextWordRules(t *testing.T) {
	script, err := Generate(Csh, fixtureRegistry())
	if err != nil {
		t.Fatalf("generate failed | 生成失败: %v", err)
	}
	if got, want := cshFirstWord(t, script), []string{"assets", "build", "gen", "new"}; !slices.Equal(got, want) {
		t.Errorf("position rule = %v, want %v | 位置规则不符", got, want)
	}
	for cmd, want := range fixtureArmWords {
		if got := cshNextWord(t, script, cmd); !slices.Equal(got, want) {
			t.Errorf("rule %q = %v, want %v | 规则词表不符", cmd, got, want)
		}
	}
}

// TestScriptsKeepPathSnippetCommented 每份脚本里提到工具目录的行都必须仍是注释行。
// TestScriptsKeepPathSnippetCommented requires every line that mentions the tool directory to stay commented.
//
// Editing the user's PATH is not something a completion script may do behind their back, so the snippet is
// shipped commented out in all four scripts. | 改动用户的 PATH 不是补全脚本可以背着用户做的事，因此四份脚本里
// 的片段一律以注释形态提供。
func TestScriptsKeepPathSnippetCommented(t *testing.T) {
	reg := fixtureRegistry()
	for _, sh := range Shells() {
		script, err := Generate(sh, reg)
		if err != nil {
			t.Fatalf("shell %q: generate failed | 生成失败: %v", sh, err)
		}
		mentions := 0
		for _, line := range strings.Split(script, "\n") {
			if !strings.Contains(line, reg.ToolsDir) || !strings.Contains(line, "PATH") {
				continue
			}
			mentions++
			if !strings.HasPrefix(strings.TrimSpace(line), "#") {
				t.Errorf("shell %q: PATH snippet is not commented | PATH 片段未被注释: %q", sh, line)
			}
		}
		if mentions == 0 {
			t.Errorf("shell %q: no PATH snippet | 缺少 PATH 片段", sh)
		}
	}
}

// TestGenerateRejectsBrokenRegistry 非法注册表必须被拒绝而不是渲染出空脚本。
// TestGenerateRejectsBrokenRegistry rejects a broken registry instead of rendering a useless script.
func TestGenerateRejectsBrokenRegistry(t *testing.T) {
	cases := []struct {
		name string
		reg  Registry
	}{
		{"no program | 无程序名", Registry{Commands: []Command{{Name: "build"}}}},
		{"no commands | 无命令", Registry{Program: "yd"}},
		{"empty command name | 命令名为空", Registry{Program: "yd", Commands: []Command{{Name: " "}}}},
	}
	for _, tc := range cases {
		for _, sh := range Shells() {
			if _, err := Generate(sh, tc.reg); !errors.Is(err, ErrInvalidRegistry) {
				t.Errorf("%s / %q: want ErrInvalidRegistry, got %v | 期望哨兵错误", tc.name, sh, err)
			}
		}
	}
}

// TestGenerateRejectsUnknownShell 未支持的 shell 必须返回哨兵错误。
// TestGenerateRejectsUnknownShell returns the sentinel for an unsupported shell.
func TestGenerateRejectsUnknownShell(t *testing.T) {
	if _, err := Generate(Shell("fish"), fixtureRegistry()); !errors.Is(err, ErrUnknownShell) {
		t.Fatalf("want ErrUnknownShell, got %v | 期望哨兵错误", err)
	}
}

// TestParseShell 校验用户输入的 shell 名规范化。
// TestParseShell checks the normalization of a user supplied shell name.
func TestParseShell(t *testing.T) {
	cases := []struct {
		in       string
		want     Shell
		wantErr  bool
		whyItIsA string
	}{
		{in: "bash", want: Bash},
		{in: "BASH", want: Bash, whyItIsA: "case-insensitive | 大小写不敏感"},
		{in: " zsh ", want: Zsh, whyItIsA: "surrounding spaces | 首尾空格"},
		{in: "csh", want: Csh},
		{in: "tcsh", want: Csh, whyItIsA: "the successor of csh shares the builtin | csh 的后继者共用该内建命令"},
		{in: "powershell", want: PowerShell},
		{in: "pwsh", want: PowerShell, whyItIsA: "the PowerShell 7 binary name | PowerShell 7 的二进制名"},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "fish", wantErr: true},
		{in: "bash5", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseShell(tc.in)
		if tc.wantErr {
			if !errors.Is(err, ErrUnknownShell) {
				t.Errorf("ParseShell(%q): want ErrUnknownShell, got %v | 期望哨兵错误", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseShell(%q): unexpected error | 意外错误: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseShell(%q) = %q, want %q | 结果不符 %s", tc.in, got, tc.want, tc.whyItIsA)
		}
	}
}

// TestShellNamesAndFileNames 校验四种 shell 的顺序、名字与文件名。
// TestShellNamesAndFileNames checks the order, the names and the file names of the four shells.
//
// The PowerShell file name is spelled out rather than derived, because it is the one that deliberately
// differs: PowerShell's dot-source operator loads a ".ps1" file only, so a file named after the shell would
// be loaded by nothing at all. | PowerShell 的文件名在这里被写死而不是推导出来，因为它是唯一刻意不同的一个：
// PowerShell 的点源运算符只加载 ".ps1" 文件，按 shell 命名的文件将不会被任何东西加载。
func TestShellNamesAndFileNames(t *testing.T) {
	want := []Shell{Bash, Zsh, Csh, PowerShell}
	if got := Shells(); !slices.Equal(got, want) {
		t.Fatalf("Shells() = %v, want %v | 顺序不符", got, want)
	}
	fileNames := map[Shell]string{
		Bash:       "completion.bash",
		Zsh:        "completion.zsh",
		Csh:        "completion.csh",
		PowerShell: "completion.ps1",
	}
	for _, sh := range want {
		if got := sh.FileName(); got != fileNames[sh] {
			t.Errorf("FileName(%s) = %q, want %q | 文件名不符", sh, got, fileNames[sh])
		}
	}
	if names := shellNames(); names != "bash/zsh/csh/powershell" {
		t.Errorf("shellNames() = %q | 名字列表不符", names)
	}
}

// TestBashWordListIsOneShellWord bash 的词表必须是单个 shell 词。
// TestBashWordListIsOneShellWord requires the bash word list to stay a single shell word.
//
// `_yd_commands='a' 'b'` is not a list of two values: it is a temporary assignment followed by a command
// named "b", so every source of the script would try to run one bogus command per command word. | 这行的固定断言
// 防的是那个具体的坑：`_yd_commands='a' 'b'` 不是两个取值的列表，而是一次临时赋值加上一个名为 "b" 的命令，
// 于是每次 source 脚本都会为每个命令词执行一条莫名其妙的命令。
func TestBashWordListIsOneShellWord(t *testing.T) {
	script, err := Generate(Bash, fixtureRegistry())
	if err != nil {
		t.Fatalf("generate failed | 生成失败: %v", err)
	}
	for _, name := range []string{"_yd_commands", "_yd_global_flags", "_yd_value_flags"} {
		line := scriptLine(t, script, name+"=")
		if words := quotedWords(line); len(words) != 1 {
			t.Errorf("%s is split into %d shell words: %s | 词表被拆成多个 shell 词", name, len(words), line)
		}
		if !strings.HasPrefix(line, name+"='") || !strings.HasSuffix(line, "'") {
			t.Errorf("%s is not single-quoted | 词表未被单引号包裹: %s", name, line)
		}
	}
}

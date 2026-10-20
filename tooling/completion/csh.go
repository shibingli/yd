// This file renders the csh script. The `complete` builtin of csh and tcsh has no notion of
// descriptions, so this is the shortest of the four scripts: one statement with a position rule for the
// command word and one next-word rule per command for its flags. | 本文件渲染 csh 脚本。csh 与 tcsh 的
// `complete` 内建命令没有描述的概念，因此这是四份脚本里最短的一份：一条语句里给出命令词的位置规则，以及每个命令
// 各一条用于其标志的「下一个词」规则。

package completion

import (
	"strings"
)

// cshPreamble is the fixed head of the csh script. | cshPreamble 是 csh 脚本的固定头部。
const cshPreamble = `# yd csh completion, generated from the command registry - do not edit by hand. | yd 的 csh 补全，由命令注册表生成——请勿手工修改。
# Load it now | 立即加载: yd completion csh > ~/.yd/completion.csh && source ~/.yd/completion.csh
# Install it permanently | 永久安装: yd completion install --shell csh`

// cshFooter is the commented environment enhancement at the end of the script. | cshFooter 是脚本末尾那段
// 被注释掉的环境增强。
const cshFooter = `# Optional environment enhancement: the project-local tool directory. | 可选的环境增强：项目内的工具目录。
# Uncomment the setenv below to put %TOOLSDIR% of the current project on PATH. | 取消下面这行 setenv 的注释即可把当前项目的 %TOOLSDIR% 加入 PATH。
# setenv PATH "${PWD}/%TOOLSDIR%:${PATH}"`

// generateCsh renders the csh script of one view. | generateCsh 渲染某个视图的 csh 脚本。
//
// Everything lives in a single `complete` statement because that builtin replaces, rather than extends,
// what it already knows about a command: a second statement for the same command would silently discard
// the first one's rules. | 所有内容都在同一条 `complete` 语句里，因为该内建命令对同一个命令是替换而不是追加：
// 为同一个命令再写一条语句，会悄悄丢掉前一条的规则。
//
// csh's `complete` can condition a rule on the command word or on a position, but not on both at once, so
// only the first positional slot is merged into the command's next-word rule; later slots fall back to the
// shell's own file completion. The other three scripts are position-aware and complete every slot. | csh 的
// `complete` 只能让规则以命令词或位置之一为条件，不能同时两者；因此只把第一个位置槽位并入命令的下一个词规则，
// 更靠后的槽位交给 shell 自带的文件补全。其余三份脚本都按位置补全每一个槽位。
func generateCsh(v view) string {
	patterns := make([]string, 0, len(v.commands)+1)
	patterns = append(patterns, cshSpec("p/1", v.commandNames()))
	for _, c := range v.commands {
		words := make([]string, 0, len(c.Positionals)+len(c.Flags)*2)
		if len(c.Positionals) > 0 {
			words = append(words, c.Positionals[0].Values...)
		}
		words = append(words, flagWords(c.Flags)...)
		patterns = append(patterns, cshSpec("n/"+c.Name, words))
	}
	var b strings.Builder
	b.WriteString(fill(cshPreamble, "%PROGRAM%", v.program))
	b.WriteString("\n\n")
	b.WriteString("complete ")
	b.WriteString(v.program)
	for i, p := range patterns {
		if i == 0 {
			b.WriteString("  ")
		} else {
			b.WriteString(" \\\n    ")
		}
		b.WriteString(p)
	}
	b.WriteByte('\n')
	b.WriteString(fill(cshFooter, "%TOOLSDIR%", v.toolsDir))
	b.WriteByte('\n')
	return b.String()
}

// cshSpec renders one `word/pattern/(list)/` triplet. | cshSpec 渲染一条 `word/pattern/(list)/` 三元组。
func cshSpec(pattern string, words []string) string {
	if len(words) == 0 {
		return quoteSingle(pattern + "/()/")
	}
	return quoteSingle(pattern + "/(" + strings.Join(words, " ") + ")/")
}

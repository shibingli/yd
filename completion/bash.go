// This file renders the bash script. Bash completion items carry no description - that is a bash
// limitation, not an omission - so the script's job is to place the right words at the right position. |
// 本文件渲染 bash 脚本。bash 的补全项不带描述——这是 bash 的限制而不是遗漏——因此脚本的职责是把正确的词
// 放在正确的位置上。

package completion

import (
	"strings"
)

// bashPreamble is the fixed head of the bash script. | bashPreamble 是 bash 脚本的固定头部。
const bashPreamble = `# yd bash completion, generated from the command registry - do not edit by hand. | yd 的 bash 补全，由命令注册表生成——请勿手工修改。
# Load it now | 立即加载: source <(yd completion bash)
# Install it permanently | 永久安装: yd completion install --shell bash`

// bashBody is the fixed function of the bash script. | bashBody 是 bash 脚本中固定的函数体。
//
// `complete -o default` is what leaves a word with no candidate to readline's own file completion, so a
// positional argument such as the project name of `yd new` keeps working. | `complete -o default` 让没有候选词的
// 位置回落到 readline 自带的文件补全，因此 `yd new` 的项目名这类位置参数依然可用。
//
// Only the words before the cursor are searched for the command name, and the value-taking flag check runs
// before any candidate list is built: without the first the word being completed would be read as the
// command name (so `yd bu<TAB>` would complete nothing at all), and without the second `yd build
// --output <TAB>` would offer flags where a file name belongs. | 只在光标之前的词里查找命令名，且取值标志的
// 判断先于任何候选词表的构建：没有前者，正在补全的那个词会被当成命令名（于是 `yd bu<TAB>` 什么都补不出来）；
// 没有后者，`yd build --output <TAB>` 会在本该出现文件名的地方提供标志。
const bashBody = `_yd_completion() {
    local cur cmd word i
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"

    # Only the words before the cursor can name a command. | 只有光标之前的词才能命名命令。
    cmd=""
    for ((i = 1; i < COMP_CWORD; i++)); do
        word="${COMP_WORDS[i]}"
        case "$word" in
            -*) continue ;;
            *) cmd="$word"; break ;;
        esac
    done

    # The word after a value-taking flag is a value, not another flag. | 取值标志之后的那个词是取值，而不是另一个标志。
    if (( COMP_CWORD >= 2 )); then
        case " $_yd_value_flags " in
            *" ${COMP_WORDS[COMP_CWORD - 1]} "*) return 0 ;;
        esac
    fi

    # A flag is being typed: offer the flags the command accepts. | 正在输入标志：提供该命令接受的标志。
    if [[ "$cur" == -* ]]; then
        case "$cmd" in
%COMMANDS%
            *) COMPREPLY=($(compgen -W "$_yd_global_flags" -- "$cur")) ;;
        esac
        return 0
    fi

    # The command word itself is being typed. | 正在输入命令词本身。
    if [[ -z "$cmd" ]]; then
        COMPREPLY=($(compgen -W "$_yd_commands" -- "$cur"))
    fi

    return 0
}

complete -o default -F _yd_completion %PROGRAM%`

// bashFooter is the commented environment enhancement at the end of every script. | bashFooter 是每份脚本末尾
// 那段被注释掉的环境增强。
const bashFooter = `# Optional environment enhancement: the project-local tool directory. | 可选的环境增强：项目内的工具目录。
# Uncomment the export below to put %TOOLSDIR% of the current project on PATH. | 取消下面这行 export 的注释即可把当前项目的 %TOOLSDIR% 加入 PATH。
# export PATH="$PWD/%TOOLSDIR%:$PATH"`

// generateBash renders the bash script of one view. | generateBash 渲染某个视图的 bash 脚本。
func generateBash(v view) string {
	var b strings.Builder
	b.WriteString(bashPreamble)
	b.WriteString("\n\n# Command words | 命令词\n_yd_commands=")
	b.WriteString(bashList(v.commandNames()))
	b.WriteString("\n\n# Flags accepted before the command word | 命令词之前就能接受的标志\n_yd_global_flags=")
	b.WriteString(bashList(flagWords(v.globals)))
	b.WriteString("\n\n# Flags that consume the next word | 会消耗下一个词的标志\n_yd_value_flags=")
	b.WriteString(bashList(v.valueWords))
	b.WriteString("\n\n")
	b.WriteString(fill(bashBody, "%COMMANDS%", bashCases(v), "%PROGRAM%", v.program))
	b.WriteByte('\n')
	b.WriteString(fill(bashFooter, "%TOOLSDIR%", v.toolsDir))
	b.WriteByte('\n')
	return b.String()
}

// bashCases renders the per-command case arms inside the completion function. | bashCases 渲染补全函数中逐命令的
// case 分支。
//
// The word lists are written out inline instead of being stored in per-command variables, because a
// command name is not required to be a valid shell identifier; inlining keeps the script syntactically
// valid whatever the registry happens to contain. | 词表就写在分支里，而不是存进逐命令的变量，因为命令名没有被要求
// 必须是合法的 shell 标识符；写进分支可以让脚本无论注册表里有什么都保持语法合法。
func bashCases(v view) string {
	var b strings.Builder
	for _, c := range v.commands {
		b.WriteString("            ")
		b.WriteString(c.Name)
		b.WriteString(") COMPREPLY=($(compgen -W \"")
		b.WriteString(strings.Join(flagWords(c.Flags), " "))
		b.WriteString("\" -- \"$cur\")) ;;\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// bashList renders words as the right-hand side of a bash assignment: ONE single-quoted, space separated
// word. | bashList 把词渲染为 bash 赋值语句的右值：单个单引号包裹、空格分隔的词。
//
// The whole list is deliberately one shell word. `_yd_commands='a' 'b'` is not a list of two values but a
// temporary assignment followed by a command named "b", so sourcing the script would run one bogus command
// per command word; keeping the list inside a single pair of quotes is what makes `source <(yd completion
// bash)` silent. The quoting additionally means a flag word can never be re-read by the shell. | 整个词表
// 刻意写成单个 shell 词。`_yd_commands='a' 'b'` 不是两个取值的列表，而是一次临时赋值加上一个名为 "b" 的命令，
// 于是每次 source 脚本都会为每个命令词执行一条莫名其妙的命令；把词表放在同一对引号内，才使
// `source <(yd completion bash)` 保持安静。引号还使标志词永远不会被 shell 再次解释。
func bashList(words []string) string {
	if len(words) == 0 {
		return "''"
	}
	return quoteSingle(strings.Join(words, " "))
}

// fill substitutes the placeholders of a script template in a single pass. | fill 单趟替换脚本文本中的占位符。
//
// A single pass matters: substitution text is never re-scanned, so a command name that happens to
// contain a placeholder cannot pull a second substitution out of thin air. | 单趟很重要：替换进去的文本不会被
// 再次扫描，因此某个恰好含占位符的命令名不可能凭空触发第二次替换。
func fill(template string, pairs ...string) string {
	return strings.NewReplacer(pairs...).Replace(template)
}

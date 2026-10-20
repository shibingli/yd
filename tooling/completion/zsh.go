// This file renders the zsh script. Unlike bash, zsh shows a description next to every candidate, so the
// one-line summary of each command and the help text of each flag travel into the script and the
// completion doubles as documentation. | 本文件渲染 zsh 脚本。与 bash 不同，zsh 会在每个候选词旁边显示描述，
// 因此每个命令的一行摘要与每个标志的帮助文案都会进入脚本，补全同时也就是文档。

package completion

import (
	"strings"
)

// zshPreamble is the fixed head of the zsh script. | zshPreamble 是 zsh 脚本的固定头部。
const zshPreamble = `# yd zsh completion, generated from the command registry - do not edit by hand. | yd 的 zsh 补全，由命令注册表生成——请勿手工修改。
# Load it now | 立即加载: source <(yd completion zsh)
# Install it permanently | 永久安装: yd completion install --shell zsh`

// zshHead is the part of the completion function that runs before the per-command branches. | zshHead
// 是补全函数中位于逐命令分支之前的那一段。
//
// $words and $CURRENT come from the completion system: $words holds every word of the command line up to
// and including the one being completed, and $CURRENT is its 1-based index. Only words before it may name
// a command, and a function that adds no candidate returns 1 so that zsh's own file completion takes the
// word instead - which is what keeps the project name of `yd new` and the value of `yd build --output`
// completable as paths. | $words 与 $CURRENT 由补全系统提供：$words 保存命令行中直到正在补全的那个词为止的
// 每个词，$CURRENT 是它从 1 开始的序号。只有它之前的词才可能命名命令；不添加任何候选的函数返回 1，于是该词
// 交给 zsh 自带的文件补全——这正是 `yd new` 的项目名与 `yd build --output` 的取值仍能按路径补全的原因。
const zshHead = `_yd_completion() {
    local -a commands flags global_flags
    local cmd="" word i cmd_index skip
    local value_flags="%VALUE_FLAGS%"

    commands=(
%COMMANDS%
    )

    global_flags=(
%GLOBALS%
    )

    # Only the words before the cursor can name a command; the value of a value-taking flag cannot. |
    # 只有光标之前的词才能命名命令；取值标志的取值不能。
    cmd=""
    cmd_index=-1
    skip=0
    for ((i = 2; i < CURRENT; i++)); do
        word="${words[i]}"
        if (( skip )); then
            skip=0
            continue
        fi
        if [[ "$word" == -* ]]; then
            case " $value_flags " in
                *" $word "*) skip=1 ;;
            esac
            continue
        fi
        cmd="$word"
        cmd_index=$i
        break
    done

    if [[ -z "$cmd" ]]; then
        if [[ "${words[CURRENT]}" == -* ]]; then
            _describe -t flags 'yd flag | yd 标志' global_flags
        else
            _describe -t commands 'yd command | yd 命令' commands
        fi
        return 0
    fi

    # The word after a value-taking flag is a value, not another flag. | 取值标志之后的那个词是取值，而不是另一个标志。
    if (( CURRENT > 1 )); then
        case " $value_flags " in
            *" ${words[CURRENT - 1]} "*) return 1 ;;
        esac
    fi

    # A positional argument: offer the command's positional vocabulary, else zsh's file completion keeps
    # the word. | 位置参数：给出该命令的位置参数词表，否则交给 zsh 的文件补全。
    if [[ "${words[CURRENT]}" != -* ]]; then
        local -a positional
        local pos=0 skip=0
        for ((i = cmd_index + 1; i < CURRENT; i++)); do
            word="${words[i]}"
            if (( skip )); then
                skip=0
                continue
            fi
            if [[ "$word" == -* ]]; then
                case " $value_flags " in
                    *" $word "*) skip=1 ;;
                esac
                continue
            fi
            pos=$((pos + 1))
        done
        pos=$((pos + 1))
        case "$cmd" in
%POSITIONALS%
        esac
        if (( ${#positional[@]} )); then
            _describe -t arguments 'yd argument | yd 参数' positional
            return 0
        fi
        return 1
    fi

    flags=()
    case "$cmd" in
%CASES%
    esac
    flags+=("${global_flags[@]}")
    if (( ${#flags[@]} )); then
        _describe -t flags 'yd flag | yd 标志' flags
    fi
}`

// zshFooter is the registration call plus the commented environment enhancement. | zshFooter 是注册调用与
// 被注释掉的环境增强。
const zshFooter = `# Register the function with the completion system. | 把该函数注册进补全系统。
# compinit has to have run for compdef to exist; when it has not, the function is still defined and the
# next shell that runs compinit picks the registration up. | compdef 需要 compinit 已经运行过；尚未运行时该函数
# 依然被定义，下一次运行 compinit 的 shell 会补上注册。
if (( $+functions[compdef] )); then
    compdef _yd_completion %PROGRAM%
fi

# Optional environment enhancement: the project-local tool directory. | 可选的环境增强：项目内的工具目录。
# Uncomment the export below to put %TOOLSDIR% of the current project on PATH. | 取消下面这行 export 的注释即可把当前项目的 %TOOLSDIR% 加入 PATH。
# export PATH="$PWD/%TOOLSDIR%:$PATH"`

// generateZsh renders the zsh script of one view. | generateZsh 渲染某个视图的 zsh 脚本。
func generateZsh(v view) string {
	head := fill(zshHead,
		"%COMMANDS%", zshLines(commandEntries(v.commands)),
		"%GLOBALS%", zshLines(flagEntries(v.globals)),
		"%CASES%", zshCases(v),
		"%POSITIONALS%", zshPositionalCases(v),
		"%VALUE_FLAGS%", strings.Join(v.valueWords, " "))
	footer := fill(zshFooter, "%PROGRAM%", v.program, "%TOOLSDIR%", v.toolsDir)
	return zshPreamble + "\n\n" + head + "\n\n" + footer + "\n"
}

// zshCases renders the per-command arms that collect one command's flags. | zshCases 渲染收集单个命令标志的
// 逐命令分支。
//
// The global flags are appended once after the case statement instead of being repeated in every arm, so
// a global flag is written in one place and honoured for every command. | 全局标志在 case 之后统一追加一次，
// 而不是在每个分支里重复，因此新增一个全局标志只需写一处，却对每个命令都生效。
func zshCases(v view) string {
	var b strings.Builder
	for _, c := range v.commands {
		b.WriteString("        ")
		b.WriteString(c.Name)
		b.WriteString(")\n            flags=(\n")
		b.WriteString(zshLines(flagEntries(c.Flags)))
		b.WriteString("\n            )\n            ;;\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// zshPositionalCases renders the per-command arms that fill the positional candidate array. | zshPositionalCases
// 渲染填充位置候选数组的逐命令分支。
//
// Each candidate carries the slot's description, so the positional half of the completion shows the same
// documentation the command and flag halves do. | 每个候选都带上槽位的描述，因此补全的位置参数部分与命令、标志
// 两部分一样具备文档价值。
func zshPositionalCases(v view) string {
	var b strings.Builder
	for _, c := range v.commands {
		if !c.hasPositionalValues() {
			continue
		}
		b.WriteString("        ")
		b.WriteString(c.Name)
		b.WriteString(")\n            case \"$pos\" in\n")
		for i, p := range c.Positionals {
			if len(p.Values) == 0 {
				continue
			}
			b.WriteString("                ")
			b.WriteString(p.pattern(i + 1))
			b.WriteString(") positional=(")
			for j, value := range p.Values {
				if j > 0 {
					b.WriteString(" ")
				}
				b.WriteString(quoteSingle(describe(value, p.Description)))
			}
			b.WriteString(") ;;\n")
		}
		b.WriteString("            esac\n            ;;\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// zshLines renders completion entries as one quoted line each, indented for the array they live in. | zshLines
// 把补全项渲染为一行一个的引用文本，并按所在数组缩进。
func zshLines(entries []string) string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, "        "+quoteSingle(e))
	}
	return strings.Join(lines, "\n")
}

// commandEntries renders commands as name/summary pairs. | commandEntries 把命令渲染为「名字/摘要」对。
func commandEntries(commands []Command) []string {
	entries := make([]string, 0, len(commands))
	for _, c := range commands {
		entries = append(entries, describe(c.Name, c.Short))
	}
	return entries
}

// flagEntries renders every spelling of every flag, each carrying the same description. | flagEntries
// 渲染每个标志的全部写法，每条都带同一份描述。
//
// Both spellings are offered because zsh users type both, and a completion that only knows the long form
// is silent exactly when the short form is being typed. | 两种写法都会被提供，因为 zsh 用户两种都会敲；一份只认识长
// 形式的补全，恰恰在用户敲短形式时一言不发。
func flagEntries(flags []Flag) []string {
	entries := make([]string, 0, len(flags)*2)
	for _, f := range flags {
		desc := f.description()
		for _, w := range f.words() {
			entries = append(entries, describe(w, desc))
		}
	}
	return entries
}

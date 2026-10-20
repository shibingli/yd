// This file renders the PowerShell script. The native argument completer of PowerShell 7 and 5.1 takes a
// script block, so the registry is embedded as a hashtable of objects and every candidate is returned as
// a CompletionResult whose tooltip carries the help text - the same "completion as documentation" idea as
// in the zsh script. | 本文件渲染 PowerShell 脚本。PowerShell 7 与 5.1 的原生参数补全器接受一个脚本块，
// 因此注册表以对象哈希表的形式内嵌，每个候选词都作为 CompletionResult 返回，其提示框承载帮助文案——与 zsh 脚本
// 同一套「补全即文档」的思路。

package completion

import (
	"strings"
)

// powerShellPreamble is the fixed head of the PowerShell script. | powerShellPreamble 是 PowerShell 脚本的
// 固定头部。
const powerShellPreamble = `# yd powershell completion, generated from the command registry - do not edit by hand. | yd 的 powershell 补全，由命令注册表生成——请勿手工修改。
# Load it now | 立即加载: yd completion powershell | Out-String | Invoke-Expression
# Install it permanently | 永久安装: yd completion install --shell powershell`

// powerShellBody is the fixed completer of the PowerShell script. | powerShellBody 是 PowerShell 脚本中固定的
// 补全器。
//
// The registry is stored in a global variable rather than a script-local one so that the script works
// both when it is dot-sourced from $PROFILE (the documented way) and when it is executed directly: a
// script-scoped variable would be gone by the time the completer runs in the latter case. | 注册表存放在全局变量
// 而不是脚本局部变量里，使脚本无论被 $PROFILE 点源加载（文档推荐的方式）还是被直接执行都能工作：后一种情形下，
// 脚本作用域的变量在补全器运行时就已消失。
const powerShellBody = `Register-ArgumentCompleter -Native -CommandName %PROGRAM% -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    if ([string]::IsNullOrEmpty($wordToComplete)) {
        $wordToComplete = ''
    }

    $elements = @($commandAst.CommandElements)

    # The first element that is not a flag - or the value of a value-taking flag - is the command. | 第一个
    # 不是标志、也不是取值标志取值的元素就是命令。
    $command = $null
    $commandIndex = -1
    $skip = $false
    for ($i = 1; $i -lt $elements.Count; $i++) {
        $text = $elements[$i].Extent.Text
        if ($skip) {
            $skip = $false
            continue
        }
        if ($text.StartsWith('-')) {
            if ($global:ydCompletion.ValueFlags -contains $text) {
                $skip = $true
            }
            continue
        }
        $command = $text
        $commandIndex = $i
        break
    }

    $previous = $null
    if ($elements.Count -gt 0) {
        $last = $elements[$elements.Count - 1].Extent.Text
        if ($last -eq $wordToComplete) {
            if ($elements.Count -ge 2) {
                $previous = $elements[$elements.Count - 2].Extent.Text
            }
        } else {
            $previous = $last
        }
    }
    if ($null -ne $previous -and $global:ydCompletion.ValueFlags -contains $previous) {
        # A word that follows a value-taking flag is that flag's value, so nothing is offered for it. | 取值标志
        # 之后的那个词是它的取值，因此不为它提供候选。
        return
    }

    # A non-flag word after a known command is a positional argument. | 已知命令之后的非标志词是位置参数。
    if ($null -ne $command -and $commandIndex -ge 0 -and -not $wordToComplete.StartsWith('-') -and $global:ydCompletion.Flags.ContainsKey($command)) {
        $end = $elements.Count
        if ($end -gt 0 -and $elements[$end - 1].Extent.Text -eq $wordToComplete) {
            $end--
        }
        $pos = 0
        $skip = $false
        for ($i = $commandIndex + 1; $i -lt $end; $i++) {
            $text = $elements[$i].Extent.Text
            if ($skip) {
                $skip = $false
                continue
            }
            if ($text.StartsWith('-')) {
                if ($global:ydCompletion.ValueFlags -contains $text) {
                    $skip = $true
                }
                continue
            }
            $pos++
        }
        $pos++
        $slot = $null
        if ($global:ydCompletion.Positionals.ContainsKey($command)) {
            $slots = $global:ydCompletion.Positionals[$command]
            if ($pos -le $slots.Count) {
                $slot = $slots[$pos - 1]
            } elseif ($slots.Count -gt 0 -and $slots[$slots.Count - 1].Variadic) {
                $slot = $slots[$slots.Count - 1]
            }
        }
        if ($null -ne $slot) {
            foreach ($value in $slot.Values) {
                if ($value.StartsWith($wordToComplete)) {
                    [System.Management.Automation.CompletionResult]::new(
                        $value, $value, 'ParameterValue', $slot.Description)
                }
            }
        }
        return
    }

    $items = $global:ydCompletion.Commands
    if ($null -ne $command) {
        $items = $global:ydCompletion.Flags['__global']
        if ($global:ydCompletion.Flags.ContainsKey($command)) {
            $items = $global:ydCompletion.Flags[$command]
        } elseif ($wordToComplete -eq $command) {
            # The word under the cursor is not a known command yet, so it is the command word being typed. | 光标
            # 下的词还不是已知命令，因此它正是正在输入的命令词。
            $items = $global:ydCompletion.Commands
        }
    } elseif ($wordToComplete.StartsWith('-')) {
        # A flag before any command word is one of the global ones. | 命令词之前的标志就是全局标志。
        $items = $global:ydCompletion.Flags['__global']
    }

    foreach ($item in $items) {
        if ($item.Name.StartsWith($wordToComplete)) {
            [System.Management.Automation.CompletionResult]::new(
                $item.Name, $item.Name, 'ParameterValue', $item.Description)
        }
    }
}`

// powerShellFooter is the commented environment enhancement at the end of the script. | powerShellFooter
// 是脚本末尾那段被注释掉的环境增强。
const powerShellFooter = `# Optional environment enhancement: the project-local tool directory. | 可选的环境增强：项目内的工具目录。
# Uncomment the assignment below to put %TOOLSDIR% of the current project on PATH. | 取消下面这行赋值的注释即可把当前项目的 %TOOLSDIR% 加入 PATH。
# $env:PATH = [IO.Path]::Combine($PWD.Path, '%TOOLSDIR%') + [IO.Path]::PathSeparator + $env:PATH`

// generatePowerShell renders the PowerShell script of one view. | generatePowerShell 渲染某个视图的 PowerShell 脚本。
func generatePowerShell(v view) string {
	var b strings.Builder
	b.WriteString(fill(powerShellPreamble, "%PROGRAM%", v.program))
	b.WriteString("\n\n")
	b.WriteString("$global:ydCompletion = @{\n")
	b.WriteString("    Commands = @(\n")
	b.WriteString(powerShellEntries(commandEntries(v.commands), "        "))
	b.WriteString("\n    )\n")
	b.WriteString("    Flags = @{\n")
	b.WriteString("        '__global' = @(\n")
	b.WriteString(powerShellEntries(flagEntries(v.globals), "            "))
	b.WriteString("\n        )\n")
	for _, c := range v.commands {
		b.WriteString("        ")
		b.WriteString(quotePowerShell(c.Name))
		b.WriteString(" = @(\n")
		b.WriteString(powerShellEntries(flagEntries(c.Flags), "            "))
		b.WriteString("\n        )\n")
	}
	b.WriteString("    }\n")
	b.WriteString("    ValueFlags = @(")
	b.WriteString(powerShellWords(v.valueWords))
	b.WriteString(")\n")
	b.WriteString("    Positionals = @{\n")
	b.WriteString(powerShellPositionalEntries(v.commands))
	b.WriteString("    }\n}\n\n")
	b.WriteString(fill(powerShellBody, "%PROGRAM%", v.program))
	b.WriteString("\n\n")
	b.WriteString(fill(powerShellFooter, "%TOOLSDIR%", v.toolsDir))
	b.WriteByte('\n')
	return b.String()
}

// powerShellPositionalEntries renders the per-command positional tables. | powerShellPositionalEntries 渲染逐命令的
// 位置参数表。
//
// Slots are emitted in declaration order because the completer indexes them by position; an empty slot is
// kept as `Values = @()` so later slots do not shift. | 槽位按声明顺序输出，因为补全器按位置索引它们；空槽位保留为
// `Values = @()`，使后续槽位不会错位。
func powerShellPositionalEntries(commands []Command) string {
	var b strings.Builder
	for _, c := range commands {
		if !c.hasPositionalValues() {
			continue
		}
		b.WriteString("        ")
		b.WriteString(quotePowerShell(c.Name))
		b.WriteString(" = @(\n")
		for _, p := range c.Positionals {
			b.WriteString("            @{ Name = ")
			b.WriteString(quotePowerShell(p.Name))
			if p.Description != "" {
				b.WriteString("; Description = ")
				b.WriteString(quotePowerShell(p.Description))
			}
			b.WriteString("; Values = @(")
			b.WriteString(powerShellWords(p.Values))
			b.WriteString(")")
			if p.Variadic {
				b.WriteString("; Variadic = $true")
			}
			b.WriteString(" }\n")
		}
		b.WriteString("        )\n")
	}
	return b.String()
}

// powerShellEntries renders completion entries as hashtable literals, one per line. | powerShellEntries
// 把补全项渲染为哈希表字面量，一行一个。
//
// The entry text is split on the first colon, which is how the name and the description were joined in
// the first place; a description may therefore contain colons of its own. | 条目文本在第一个冒号处分割，
// 那正是名字与描述当初拼接的位置；因此描述本身可以含冒号。
func powerShellEntries(entries []string, indent string) string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		name, desc, found := strings.Cut(e, ":")
		line := indent + "@{ Name = " + quotePowerShell(name)
		if found && desc != "" {
			line += "; Description = " + quotePowerShell(desc)
		}
		lines = append(lines, line+" }")
	}
	return strings.Join(lines, "\n")
}

// powerShellWords renders words as a one-line string array. | powerShellWords 把词渲染为单行字符串数组。
//
// The whole list goes on one line because it is a lookup table for a single `-contains` test: its shape
// carries no meaning that would be worth a line of script per word. | 整个词表写在一行，因为它是给一次
// `-contains` 判断用的查找表：它的形状没有值得为每个词写一行脚本的含义。
func powerShellWords(words []string) string {
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		quoted = append(quoted, quotePowerShell(w))
	}
	return strings.Join(quoted, ", ")
}

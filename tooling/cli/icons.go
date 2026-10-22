// This file implements the icons command: it owns the two action words, the template/output flags
// and the report. Scanning and sprite rendering live in the icons package. | 本文件实现 icons 命令：
// 只负责两个动作词、模板/输出标志与报告；扫描与 sprite 渲染由 icons 包负责。
package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

	"github.com/shibingli/yd/tooling/icons"
	"github.com/shibingli/ydx/errs"
)

// iconsCommandName is the command word of the icon command. | iconsCommandName 是图标命令的命令词。
const iconsCommandName = "icons"

// The icons command's action words. | icons 命令的动作词。
const (
	// iconsActionSync rewrites the sprite from the templates. | iconsActionSync 根据模板重写 sprite。
	iconsActionSync = "sync"
	// iconsActionCheck verifies that the committed sprite is current; it is the default. | iconsActionCheck 校验已提交的 sprite 是否最新；它是默认动作。
	iconsActionCheck = "check"
)

// Icon command flags. | 图标命令标志。
const (
	// flagIconsTemplates overrides the template roots to scan. | flagIconsTemplates 覆盖要扫描的模板根。
	flagIconsTemplates = "templates"
	// flagIconsOutput overrides the sprite path. | flagIconsOutput 覆盖 sprite 路径。
	flagIconsOutput = "output"
)

// iconsUsageLine is the usage line of the icons command. | iconsUsageLine 是 icons 命令的用法行。
const iconsUsageLine = "yd icons [sync|check] [--templates DIRS] [--output FILE]"

// iconsFlags keeps the icons command's flag set. | iconsFlags 保存 icons 命令的标志集。
var iconsFlags *pflag.FlagSet

func init() {
	Register(Command{
		Name:  iconsCommandName,
		Short: "build or verify the on-demand icon sprite | 构建或校验按需图标 sprite",
		Long: "Turn the pinned Iconify set into one small SVG sprite containing only the icons the\n" +
			"templates actually reference. The set JSON is embedded in this CLI; the generated sprite is\n" +
			"embedded in the application binary, so neither build nor runtime touches the network.\n" +
			"把固定版本的 Iconify 图标集转换成只含模板实际引用图标的一个小 SVG sprite。图标集 JSON 内嵌在\n" +
			"本 CLI 中，生成的 sprite 内嵌在应用二进制中，因此构建与运行都不触网。\n" +
			"sync rewrites the sprite; check exits non-zero when the committed file is stale and is the\n" +
			"default action, so CI can run `yd icons`.\n" +
			"sync 重写 sprite；check 在已提交文件过期时以非零码退出，并且是默认动作，因此 CI 可以直接运行\n" +
			"`yd icons`。",
		Usage: iconsUsageLine,
		Flags: registerIconsFlags,
		Run:   runIcons,
		Positionals: []Positional{{
			Name:        "action",
			Description: "action: sync/check | 动作",
			Values:      []string{iconsActionSync, iconsActionCheck},
		}},
	})
}

// registerIconsFlags declares the icons command's flags. | registerIconsFlags 声明 icons 命令的标志。
func registerIconsFlags(fs *pflag.FlagSet) {
	fs.String(flagIconsTemplates, "",
		"comma-separated template roots, default web/templates plus the scaffold tree | 逗号分隔的模板根，缺省为 web/templates 与脚手架模板树")
	fs.String(flagIconsOutput, "",
		"sprite path, default "+icons.DefaultOutput+" | sprite 路径，缺省为 "+icons.DefaultOutput)
	iconsFlags = fs
}

// runIcons dispatches sync and check. | runIcons 派发 sync 与 check。
func runIcons(_ context.Context, env *Env) error {
	action, err := iconsRequest(iconsFlags)
	if err != nil {
		fmt.Fprintf(env.Err, "yd icons: %v\n", err)
		fmt.Fprintf(env.Err, "usage | 用法:\n  %s\n", iconsUsageLine)
		return err
	}
	roots, err := iconsRoots(env)
	if err != nil {
		return err
	}
	output := resolveUserPath(env.WorkDir, iconsOutputPath(iconsFlags))
	opts := icons.Options{FS: env.FS, Roots: roots, Output: output}

	if action == iconsActionSync {
		res, err := icons.Sync(opts)
		if err != nil {
			fmt.Fprintf(env.Err, "yd icons: %v\n", err)
			return err
		}
		fmt.Fprintf(env.Out, "yd icons sync | yd icons 同步\n")
		fmt.Fprintf(env.Out, "icons | 图标: %d\n", len(res.Names))
		fmt.Fprintf(env.Out, "sprite | sprite 路径: %s (%d 字节)\n", output, res.Bytes)
		return nil
	}

	res, err := icons.Check(opts)
	fmt.Fprintf(env.Out, "yd icons check | yd icons 校验\n")
	fmt.Fprintf(env.Out, "icons | 图标: %d\n", len(res.Names))
	if err != nil {
		fmt.Fprintf(env.Err, "yd icons: %v\n", err)
		return err
	}
	fmt.Fprintf(env.Out, "result | 结论: ok | 通过\n")
	return nil
}

// iconsRequest reads the action word, defaulting to check. | iconsRequest 读取动作词，缺省为 check。
func iconsRequest(fs *pflag.FlagSet) (string, error) {
	if fs == nil || len(fs.Args()) == 0 {
		return iconsActionCheck, nil
	}
	args := fs.Args()
	action := strings.ToLower(strings.TrimSpace(args[0]))
	if action != iconsActionSync && action != iconsActionCheck {
		return "", errs.Detail(ErrUnknownCommand,
			"%q (actions | 动作: %s, %s)", args[0], iconsActionSync, iconsActionCheck)
	}
	if len(args) > 1 {
		return "", errs.Detail(ErrNoCommand,
			"%s (%s takes no argument, got %q | %s 不接受参数，收到 %q)",
			iconsUsageLine, action, args[1], action, args[1])
	}
	return action, nil
}

// iconsRoots resolves the template roots: --templates, else every conventional root that exists. |
// iconsRoots 解析模板根：--templates，否则所有存在的约定根。
func iconsRoots(env *Env) ([]string, error) {
	if raw := strings.TrimSpace(flagString(iconsFlags, flagIconsTemplates)); raw != "" {
		parts := strings.Split(raw, ",")
		roots := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			roots = append(roots, resolveUserPath(env.WorkDir, part))
		}
		if len(roots) == 0 {
			return nil, errs.Detail(icons.ErrScan, "no template roots | 没有模板根")
		}
		return roots, nil
	}

	candidates := []string{
		"web/templates",
		filepath.Join("tooling", "scaffold", "templates", "_name_", "web", "templates"),
	}
	roots := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		root := resolveUserPath(env.WorkDir, candidate)
		info, err := env.FS.Stat(root)
		if err == nil && info.IsDir() {
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		return nil, errs.Detail(icons.ErrScan,
			"no templates under web/templates | web/templates 下没有模板")
	}
	return roots, nil
}

// iconsOutputPath returns --output, else the default sprite path. | iconsOutputPath 返回 --output，否则默认 sprite 路径。
func iconsOutputPath(fs *pflag.FlagSet) string {
	if output := strings.TrimSpace(flagString(fs, flagIconsOutput)); output != "" {
		return output
	}
	return icons.DefaultOutput
}

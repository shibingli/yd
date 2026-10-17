// This file implements the authz command: it owns flag handling, the two action words, the reference
// table and the exit-code decision, and nothing else. The scan itself lives in the authz package. |
// 本文件实现 authz 命令：只负责标志处理、两个动作词、参考表与退出码判定，别无其它；
// 扫描本身由 authz 包负责。

package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/security/authz"
)

// authzCommandName is the command word of the permission command. | authzCommandName 是权限命令的命令词。
const authzCommandName = "authz"

// The authz command's action words. | authz 命令的动作词。
const (
	// authzActionCheck validates the tree; it is the default action. | authzActionCheck 校验代码树；它是默认动作。
	authzActionCheck = "check"
	// authzActionList prints the registry as a reference table. | authzActionList 把注册表打印成参考表。
	authzActionList = "list"
)

// flagAuthzDir is the directory the static check scans. | flagAuthzDir 是静态校验扫描的目录。
const flagAuthzDir = "dir"

// authzUsageLine is the usage line of the authz command. | authzUsageLine 是 authz 命令的用法行。
const authzUsageLine = "yd authz check [--dir DIR]\n" +
	"      yd authz list"

// ErrAuthzFailed reports that the static permission check found errors. | ErrAuthzFailed 表示权限静态校验发现了错误。
//
// It is a sentinel so that CI can tell "the tree has a misspelled capability" from "the scan could
// not run", which are two very different mornings. | 它是哨兵，让 CI 能区分「树里有写错的能力」与
// 「扫描根本没跑起来」——那是两个完全不同的早晨。
var ErrAuthzFailed = errx.New("permission check failed | 权限校验未通过")

// authzFlags keeps the authz command's flag set, installed by registerAuthzFlags. | authzFlags 保存 authz
// 命令的标志集，由 registerAuthzFlags 安装。
var authzFlags *pflag.FlagSet

func init() {
	Register(Command{
		Name:  authzCommandName,
		Short: "validate capabilities and print the registry | 校验能力引用并打印权限注册表",
		Long: "Keep the capability vocabulary in one place and prove that the tree only ever refers to it.\n" +
			"把能力词汇表放在一处，并证明整棵树只引用它。\n" +
			"check scans the sources and reports three things: a capability or role the registry does not\n" +
			"know, a capability written as a string literal instead of through a constant, and a declared\n" +
			"capability nothing refers to. The first two fail the command, so a misspelled permission is\n" +
			"caught before it is deployed rather than after it denies every request.\n" +
			"check 扫描源码并报告三件事：注册表不认识的能力或角色、被写成字符串字面量而不是经常量引用的能力、\n" +
			"以及无人引用的已声明能力。前两类让命令失败，因此写错的权限在部署之前就会暴露，而不是在它拒绝每个请求之后。\n" +
			"list prints the registry - capabilities grouped by resource, each with its Go constant, its\n" +
			"template key and its JavaScript key, plus the roles - which is the table to look a name up in.\n" +
			"list 打印注册表——按资源分组的能力，每条都带 Go 常量、模板键与 JavaScript 键，另加角色——\n" +
			"这就是查名字用的那张表。\n" +
			"A test file is skipped by design: that is where the deliberately broken fixtures live.\n" +
			"测试文件按设计跳过：故意写坏的夹具就放在那里。",
		Usage: authzUsageLine,
		Flags: registerAuthzFlags,
		Run:   runAuthz,
	})
}

// registerAuthzFlags declares the authz command's flags. | registerAuthzFlags 声明 authz 命令的标志。
func registerAuthzFlags(fs *pflag.FlagSet) {
	fs.String(flagAuthzDir, "", "directory to scan, default the working directory | 要扫描的目录，缺省为工作目录")
	authzFlags = fs
}

// runAuthz dispatches the two actions. | runAuthz 派发两个动作。
func runAuthz(_ context.Context, env *Env) error {
	action, err := authzRequest(authzFlags)
	if err != nil {
		fmt.Fprintf(env.Err, "yd authz: %v\n", err)
		printAuthzUsage(env.Err)
		return err
	}
	if action == authzActionList {
		printAuthzList(env)
		return nil
	}
	return runAuthzCheck(env)
}

// authzRequest reads the action word. | authzRequest 读取动作词。
//
// A missing word means check, so `yd authz` alone is the everyday entry point. | 缺少动作词时表示 check，
// 因此单独一条 `yd authz` 就是日常入口。
func authzRequest(fs *pflag.FlagSet) (string, error) {
	if fs == nil || len(fs.Args()) == 0 {
		return authzActionCheck, nil
	}
	args := fs.Args()
	action := strings.ToLower(strings.TrimSpace(args[0]))
	switch action {
	case authzActionCheck:
		if len(args) > 1 {
			return "", errx.Detail(ErrNoCommand,
				"%s (check takes no argument, got %q | check 不接受参数，收到 %q)", authzUsageLine, args[1], args[1])
		}
		return authzActionCheck, nil
	case authzActionList:
		if len(args) > 1 {
			return "", errx.Detail(ErrNoCommand,
				"%s (list takes no argument, got %q | list 不接受参数，收到 %q)", authzUsageLine, args[1], args[1])
		}
		return authzActionList, nil
	default:
		return "", errx.Detail(ErrUnknownCommand,
			"%q (actions | 动作: %s, %s)", args[0], authzActionCheck, authzActionList)
	}
}

// runAuthzCheck scans the tree and prints the report. | runAuthzCheck 扫描代码树并打印报告。
//
// The report is printed even when the check failed, and the error is what makes cmd/yd exit
// non-zero: a CI log has to show which line carries the typo, not merely that one exists. | 即使校验失败也会
// 打印报告，由返回的错误让 cmd/yd 以非零码退出：CI 日志必须显示哪一行写着拼错的标识，
// 而不只是「有错」。
func runAuthzCheck(env *Env) error {
	dir := authzDir(env, authzFlags)
	rep, err := authz.Check(authz.CheckOptions{FS: env.FS, Dir: dir})
	rep.Fprint(env.Out)
	if err != nil {
		fmt.Fprintf(env.Err, "yd authz: %v\n", err)
		return err
	}
	if !rep.OK() {
		return errx.Detail(ErrAuthzFailed,
			"%[1]d error(s) under %[2]s | %[2]s 下有 %[1]d 个错误", rep.Errors(), dir)
	}
	return nil
}

// authzDir returns the directory to scan: --dir, else the working directory. | authzDir 返回扫描目录：
// --dir，否则工作目录。
func authzDir(env *Env, fs *pflag.FlagSet) string {
	if dir := strings.TrimSpace(flagString(fs, flagAuthzDir)); dir != "" {
		if env != nil && env.WorkDir != "" && !filepath.IsAbs(dir) {
			return filepath.Join(env.WorkDir, dir)
		}
		return dir
	}
	if env != nil && env.WorkDir != "" {
		return env.WorkDir
	}
	return "."
}

// authzWidths holds the column widths of the reference table. | authzWidths 保存参考表的列宽。
type authzWidths struct {
	// token is the capability token column. | token 是能力标识列。
	token int
	// constant is the Go constant column. | constant 是 Go 常量列。
	constant int
	// template is the template data key column. | template 是模板数据键列。
	template int
	// script is the JavaScript key column. | script 是 JavaScript 键列。
	script int
	// role is the role name column. | role 是角色名列。
	role int
}

// printAuthzList writes the registry as a reference table. | printAuthzList 把注册表写成参考表。
//
// Every spelling of one capability appears on one line - token, Go constant, template key, script
// key - because the point of a single source of truth is that a developer who has the token can find
// the other three without reading this package. | 同一条能力的每种写法都出现在同一行——
// 标识、Go 常量、模板键、脚本键——因为「单一事实来源」的意义就在于：手里有标识的开发者不必翻开本包，
// 就能找到另外三个。
func printAuthzList(env *Env) {
	perms := authz.Permissions()
	roles := authz.Roles()
	fmt.Fprintf(env.Out, "yd authz list | yd authz 列表\n")
	fmt.Fprintf(env.Out, "permissions | 权限: %d\n", len(perms))
	width := authzWidth(perms, roles)
	byGroup := map[string][]authz.Permission{}
	for _, p := range perms {
		byGroup[p.Group] = append(byGroup[p.Group], p)
	}
	for _, group := range authz.Groups() {
		items := byGroup[group]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(env.Out, "  [%s] %d\n", authz.GroupLabel(group), len(items))
		for _, p := range items {
			fmt.Fprintf(env.Out, "    %-*s  %-*s  %-*s  %-*s  %s\n",
				width.token, p.Name,
				width.constant, authz.ConstantName(p.Name),
				width.template, "."+authz.PermSelector+authz.DataKey(p.Name),
				width.script, authz.PermScriptSelector+authz.JSKey(p.Name),
				p.Description)
		}
	}
	fmt.Fprintf(env.Out, "roles | 角色: %d\n", len(roles))
	for _, r := range roles {
		fmt.Fprintf(env.Out, "    %-*s  %-*s  %-*s  %s\n",
			width.role, r.Name,
			width.role, authz.RoleConstantName(r.Name),
			width.template, authz.RoleSelector+authz.RoleDataKey(r.Name),
			r.Description)
	}
}

// authzWidth measures the widest entry of each column. | authzWidth 测量每列最宽的一条记录。
func authzWidth(perms []authz.Permission, roles []authz.Role) authzWidths {
	w := authzWidths{token: 8, constant: 8, template: 8, script: 8, role: 4}
	for _, p := range perms {
		w.token = max(w.token, len(p.Name))
		w.constant = max(w.constant, len(authz.ConstantName(p.Name)))
		w.template = max(w.template, len(authz.PermSelector)+len(authz.DataKey(p.Name)))
		w.script = max(w.script, len(authz.PermScriptSelector)+len(authz.JSKey(p.Name)))
	}
	for _, r := range roles {
		w.role = max(w.role, len(r.Name), len(authz.RoleConstantName(r.Name)), len(authz.RoleSelector)+len(authz.RoleDataKey(r.Name)))
	}
	return w
}

// printAuthzUsage writes the usage block of the authz command. | printAuthzUsage 写出 authz 命令的用法块。
func printAuthzUsage(w io.Writer) {
	fmt.Fprintf(w, "usage | 用法:\n  %s\n", authzUsageLine)
}

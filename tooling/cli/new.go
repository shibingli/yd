// This file implements the new command: it is a thin shell around the scaffold package and owns nothing but
// flag parsing, the interactive interview and the closing hint. | 本文件实现 new 命令：它只是 scaffold
// 包的一层薄壳，只负责标志解析、交互问答与收尾提示。
//
// Layering rule: a flag only takes effect when it was passed explicitly (see flagChanged), so the zero
// value of an unpassed flag can never shadow a default or an interview answer — the same rule the global
// flags follow in cli.go. | 分层规则：只有命令行显式传入的标志才生效（见 flagChanged），因此未传参标志
// 的零值绝不会遮蔽默认值或问答结果——与 cli.go 中全局标志遵循的规则一致。

package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/shibingli/yd/tooling/tools"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/log"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// newCommandName is the command word of the scaffolding command. | newCommandName 是脚手架命令的命令词。
const newCommandName = "new"

// The new command's flag names. | new 命令的标志名。
const (
	// flagNewModule is the go.mod module path. | flagNewModule 是 go.mod 的模块路径。
	flagNewModule = "module"
	// flagNewDB is the database driver. | flagNewDB 是数据库驱动。
	flagNewDB = "db"
	// flagNewGRPC enables the gRPC dual channel. | flagNewGRPC 开启 gRPC 双通道。
	flagNewGRPC = "grpc"
	// flagNewI18n enables internationalisation. | flagNewI18n 开启国际化。
	flagNewI18n = "i18n"
	// flagNewHtmxExt selects the optional Htmx extensions. | flagNewHtmxExt 选择额外的 Htmx 扩展。
	flagNewHtmxExt = "htmx-ext"
	// flagNewLicense is the licence identifier. | flagNewLicense 是开源协议标识。
	flagNewLicense = "license"
	// flagNewForce overwrites conflicting files. | flagNewForce 覆盖冲突文件。
	flagNewForce = "force"
	// flagNewYes answers every question with its default. | flagNewYes 对所有问题取默认值。
	flagNewYes = "yes"
	// flagNewInstallTools installs the missing optional external tools without asking. | flagNewInstallTools
	// 不提问直接安装缺失的可选外部工具。
	flagNewInstallTools = "install-tools"
	// flagNewLocalReplace points the generated go.mod at a local framework checkout. | flagNewLocalReplace 让生成的 go.mod 指向本地框架仓库。
	flagNewLocalReplace = "local-replace"
)

// newUsageLine is the usage line of the new command. | newUsageLine 是 new 命令的用法行。
const newUsageLine = "yd new <project-name> [flags]"

// newFlags keeps the new command's flag set, installed by registerNewFlags. | newFlags 保存 new 命令的标志集，由 registerNewFlags 安装。
var newFlags *pflag.FlagSet

// dbChoices lists the database drivers offered by the interview, in presentation order. | dbChoices 按展示顺序列出问答提供的数据
// 库驱动。
var dbChoices = []string{scaffold.DBMySQL, scaffold.DBPostgres, scaffold.DBSQLite, scaffold.DBDM, scaffold.DBNone}

func init() {
	Register(Command{
		Name:  newCommandName,
		Short: "create a new yd project | 创建新的 yd 项目",
		Long: "Scaffold a new project from the embedded templates, then lay down its configuration and\n" +
			"front-end assets.\n" +
			"依据内嵌模板生成新项目，随后铺开它的配置与前端资产。\n" +
			"\n" +
			"Htmx 4, Tailwind CSS v4 and Alpine.js 3 are always integrated; they are not switches.\n" +
			"Every safe Htmx 4 extension is included by default, so the interview never asks for a subset.\n" +
			"--htmx-ext only adds the opt-in policy layers (csp, compat, htmx-2-compat) on top of that baseline.\n" +
			"Htmx 4、Tailwind CSS v4 与 Alpine.js 3 始终集成，不需要选择。全部安全 Htmx 4 扩展默认包含，\n" +
			"问答不再询问子集；--htmx-ext 只用于追加需显式选择的策略层（csp、compat、htmx-2-compat）。\n" +
			"\n" +
			"Included by default | 默认包含: ws, sse, preload, targets, head, upsert, pending, multipart,\n" +
			"download, browser-indicator, history-cache, live, prompt, ptag, alpine.\n" +
			"Opt-in only | 仅显式选择: csp (needs a nonce policy), compat / htmx-2-compat (restore htmx 2 semantics).\n" +
			"csp 需要 nonce 策略；compat / htmx-2-compat 恢复 htmx 2 语义。\n" +
			"\n" +
			"Without --yes an interactive terminal is asked for every option that was not passed as a flag;\n" +
			"a pipe or CI run keeps every default, so the command stays scriptable.\n" +
			"未传 --yes 时，交互终端会被逐项询问未被标志指定的选项；管道或 CI 环境一律取默认值，因此命令可脚本化。",
		Usage: newUsageLine,
		Flags: registerNewFlags,
		Run:   runNew,
	})
}

// registerNewFlags declares the new command's flags. | registerNewFlags 声明 new 命令的标志。
func registerNewFlags(fs *pflag.FlagSet) {
	fs.String(flagNewModule, "", "module path, defaults to github.com/yun-dev/<name> | 模块路径，默认为 github.com/yun-dev/<name>")
	fs.String(flagNewDB, "", "database driver: mysql/postgres/sqlite/dm/none | 数据库驱动：mysql/postgres/sqlite/dm/none")
	fs.Bool(flagNewGRPC, false, "enable the gRPC dual channel | 开启 gRPC 双通道")
	fs.Bool(flagNewI18n, true, "i18n is on by default; pass --i18n=false to disable it | 默认开启 i18n；用 --i18n=false 关闭")
	fs.StringArray(flagNewHtmxExt, nil, "extra opt-in Htmx policy layers (csp, compat, htmx-2-compat); every safe extension is included by default | 额外的 Htmx 策略层（csp、compat、htmx-2-compat）；全部安全扩展默认包含")
	fs.String(flagNewLicense, "", "licence identifier, defaults to Apache-2.0 | 开源协议标识，默认为 Apache-2.0")
	fs.Bool(flagNewForce, false, "overwrite files that differ from the templates | 覆盖与模板不一致的文件")
	fs.BoolP(flagNewYes, "y", false, "keep every default without prompting | 取默认值，不提问")
	fs.Bool(flagNewInstallTools, false, "install the missing optional external tools (betteralign, ko) into the new project | 把缺失的可选外部工具（betteralign、ko）安装到新项目")
	fs.String(flagNewLocalReplace, "", "local framework checkout, become a go.mod replace (relative paths resolve against the working directory) | 本地框架仓库，写入 go.mod 的 replace（相对路径以工作目录为基准）")
	newFlags = fs
}

// runNew scaffolds one project. | runNew 生成一个项目。
func runNew(ctx context.Context, env *Env) error {
	fs := newFlags
	name, err := newProjectName(env, fs)
	if err != nil {
		return err
	}

	opts := scaffold.DefaultOptions(name)
	applyNewFlags(&opts, env, fs)

	if newPromptsEnabled(env, fs) {
		if err := interviewNew(env, fs, &opts); err != nil {
			return err
		}
	}

	res, err := scaffold.Generate(ctx, opts, newTargetFS(env), log.Default())
	if err != nil {
		return err
	}

	printNewReport(env, opts, res)
	newToolStep(ctx, env, fs, opts.Name)
	return nil
}

// newToolStep reports the optional external tools that are missing and, when the user asks for it,
// installs them into the new project. | newToolStep 报告缺失的可选外部工具，并在用户要求时把它们安装到新项目中。
//
// The project is already generated at this point, so nothing here may fail `yd new`: an installation
// failure only logs a warning and the command still succeeds, exactly like a missing optional tool
// never fails a build (CLAUDE.md §8). | 此时项目已经生成完毕，因此这里绝不能让 `yd new` 失败：安装失败只记一条告警，
// 命令仍然成功——正如可选工具缺失绝不让构建失败（CLAUDE.md §8）。
//
// Installing is a network side effect, so it is always explicit: the interview asks on a terminal with
// "yes" as the default answer, and `--install-tools` forces it in scripts and CI. `--yes` alone only
// prints the hints, because `yd new --yes` is the e2e entry point and must stay offline. | 安装是网络副作用，
// 因此永远显式：终端下由问答征求同意且默认答「是」，脚本与 CI 用 `--install-tools` 强制。仅传 `--yes` 时只打印提示，
// 因为 `yd new --yes` 是 e2e 的入口，必须保持离线。
func newToolStep(ctx context.Context, env *Env, fs *pflag.FlagSet, name string) {
	dir := filepath.Join(env.WorkDir, name)
	if len(tools.Missing(dir)) == 0 {
		return
	}
	fmt.Fprintf(env.Out, "missing tools | 缺失工具:\n")
	for _, line := range tools.Hints(dir) {
		fmt.Fprintf(env.Out, "%s\n", line)
	}
	if !newInstallToolsEnabled(env, fs) {
		return
	}

	report, err := tools.InstallAll(ctx, dir, commandLogger(env))
	if report != nil {
		printToolsReport(env, report)
	}
	if err != nil {
		commandLogger(env).Warn("tool installation failed; the project is unaffected | 工具安装失败，项目不受影响",
			log.Str("error", err.Error()))
	}
}

// newInstallToolsEnabled reports whether the missing tools should be installed right away. | newInstallToolsEnabled 报告是否应立即安装缺失的工具。
func newInstallToolsEnabled(env *Env, fs *pflag.FlagSet) bool {
	if flagBool(fs, flagNewInstallTools) {
		return true
	}
	if !newPromptsEnabled(env, fs) {
		return false
	}
	answer, err := Confirm(env, "现在就安装缺失的工具吗？| Install the missing tools now?", true)
	if err != nil {
		commandLogger(env).Warn("cannot read the install answer; keeping the hints only | 无法读取安装选择，只保留提示",
			log.Str("error", err.Error()))
		return false
	}
	return answer
}

// newProjectName reads the project name from the positional argument, asking for it interactively when it
// was omitted on a terminal. | newProjectName 从定位参数读取项目名，终端下省略时交互询问。
//
// A missing name outside a terminal (pipe, CI, --yes) is a usage error: there is nothing to default to. | 非终端
// 环境（管道、CI、--yes）下缺少项目名属于用法错误：项目名没有可用的默认值。
func newProjectName(env *Env, fs *pflag.FlagSet) (string, error) {
	switch args := fs.Args(); len(args) {
	case 0:
		if !newPromptsEnabled(env, fs) {
			return "", errs.Detail(ErrNoCommand, "%s (give a project name | 请给出项目名)", newUsageLine)
		}
		name, err := Prompt(env, "项目名 | Project name", "")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(name) == "" {
			return "", errs.Detail(ErrNoCommand, "%s (give a project name | 请给出项目名)", newUsageLine)
		}
		return strings.TrimSpace(name), nil
	case 1:
		return strings.TrimSpace(args[0]), nil
	default:
		return "", errs.Detail(ErrNoCommand, "%s (accepts one project name, got %d | 只接受一个项目名，收到 %d 个)", newUsageLine, len(args), len(args))
	}
}

// newPromptsEnabled reports whether the interview should run: only on an interactive terminal and only
// when --yes was not given. | newPromptsEnabled 报告是否应当提问：仅在交互终端且未传 --yes 时提问。
func newPromptsEnabled(env *Env, fs *pflag.FlagSet) bool {
	return env != nil && env.IsTTY && !flagBool(fs, flagNewYes)
}

// newTargetFS returns the file system the project is generated into. | newTargetFS 返回项目的生成目标文件系统。
//
// The project always lands in <workdir>/<name>: for the default working directory that is env.FS itself,
// otherwise a base-path view rooted at WorkDir. | 项目始终落在 <workdir>/<name>：默认工作目录下就是
// env.FS 本身，否则是叠加在 WorkDir 之上的一层基路径视图。
//
// afero's BasePathFs rejects a base of "." during path resolution, hence the explicit special case. | afero 的
// BasePathFs 在基路径为 "." 时会拒绝解析，因此这里显式特判。
func newTargetFS(env *Env) afero.Fs {
	dir := filepath.Clean(env.WorkDir)
	if dir == "" || dir == "." {
		return env.FS
	}
	return afero.NewBasePathFs(env.FS, dir)
}

// applyNewFlags copies the explicitly passed flags into o, leaving every other field at its default. | applyNewFlags
// 把显式传入的标志写入 o，其余字段保持默认值。
func applyNewFlags(o *scaffold.Options, env *Env, fs *pflag.FlagSet) {
	if flagChanged(fs, flagNewModule) {
		o.Module = flagString(fs, flagNewModule)
	}
	if flagChanged(fs, flagNewDB) {
		o.DB = flagString(fs, flagNewDB)
	}
	if flagChanged(fs, flagNewGRPC) {
		o.GRPC = flagBool(fs, flagNewGRPC)
	}
	if flagChanged(fs, flagNewI18n) {
		o.I18n = flagBool(fs, flagNewI18n)
	}
	if flagChanged(fs, flagNewHtmxExt) {
		o.HtmxExts = flagStrings(fs, flagNewHtmxExt)
	}
	if flagChanged(fs, flagNewLicense) {
		o.License = flagString(fs, flagNewLicense)
	}
	if flagChanged(fs, flagNewForce) {
		o.Force = flagBool(fs, flagNewForce)
	}
	if flagChanged(fs, flagNewLocalReplace) {
		o.LocalReplace = newLocalReplace(env, flagString(fs, flagNewLocalReplace))
	}
}

// newLocalReplace resolves the --local-replace value against the working directory. | newLocalReplace 以工作目录为基准解析 --local-replace 的取值。
//
// A go.mod replace path is resolved relative to the generated project, so a value typed relative to the
// caller's directory must be made absolute first, otherwise the replace would point outside the project
// only by accident. | go.mod 的 replace 路径相对于生成的项目解析，因此相对调用方目录输入的取值必须先转为
// 绝对路径，否则该 replace 只是碰巧指向项目之外。
func newLocalReplace(env *Env, value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return v
	}
	return resolveUserPath(env.WorkDir, v)
}

// interviewNew asks for the options that were not pinned by a flag. | interviewNew 询问未被标志钉住的选项。
func interviewNew(env *Env, fs *pflag.FlagSet, o *scaffold.Options) error {
	if !flagChanged(fs, flagNewModule) {
		module, err := Prompt(env, "模块路径 | Module path", o.Module)
		if err != nil {
			return err
		}
		o.Module = module
	}

	if !flagChanged(fs, flagNewDB) {
		idx, err := Choose(env, "数据库驱动 | Database driver", dbChoices, indexOf(dbChoices, o.DB))
		if err != nil {
			return err
		}
		o.DB = dbChoices[idx]
	}

	// i18n is part of the baseline: every generated project ships both catalogues, so the interview
	// never asks whether to enable it. | i18n 属于基线：每个生成项目都带两份语言目录，问答不再询问。
	if !flagChanged(fs, flagNewGRPC) {
		answer, err := Confirm(env, "启用 gRPC 双通道 | Enable the gRPC dual channel", o.GRPC)
		if err != nil {
			return err
		}
		o.GRPC = answer
	}

	// Htmx extensions are part of the baseline; only an explicit flag can add the policy layers.
	// Htmx 扩展属于基线；只有显式标志才能追加策略层。

	if !flagChanged(fs, flagNewLicense) {
		license, err := Prompt(env, "开源协议 | Licence", o.License)
		if err != nil {
			return err
		}
		o.License = license
	}
	return nil
}

// indexOf returns the index of value inside list, or 0 when it is absent. | indexOf 返回 value 在 list 中的下标，不存在时为 0。
func indexOf(list []string, value string) int {
	for i, item := range list {
		if item == value {
			return i
		}
	}
	return 0
}

// printNewReport writes what was created and how to continue. | printNewReport 写出生成结果与后续步骤。
func printNewReport(env *Env, o scaffold.Options, res *scaffold.Result) {
	dir := filepath.Join(env.WorkDir, o.Name)
	fmt.Fprintf(env.Out, "project created | 项目已创建: %s\n", dir)
	fmt.Fprintf(env.Out, "  module   | 模块  : %s\n", res.ModulePath)
	fmt.Fprintf(env.Out, "  features | 特性  : %s\n", strings.Join(featuresOf(o), ", "))
	fmt.Fprintf(env.Out, "  files    | 文件  : %d written | 已写入, %d skipped | 已跳过\n", len(res.Files), len(res.Skipped))
	fmt.Fprintf(env.Out, "next steps | 后续步骤:\n")
	fmt.Fprintf(env.Out, "  cd %s && yd assets update    # download the locked front-end assets | 下载锁定的前端资产\n", o.Name)
	fmt.Fprintf(env.Out, "  cd %s && yd dev              # start the dev server with hot reload | 启动热重载开发服务器\n", o.Name)
}

// featuresOf summarises the selected features for the closing report. | featuresOf 汇总所选特性，用于收尾报告。
func featuresOf(o scaffold.Options) []string {
	features := []string{"db:" + o.DB}
	// Redis 与 asynq 不是开关：每个生成项目都带它们的配置与接线。
	// Redis and asynq are not switches: every generated project ships their configuration and wiring.
	features = append(features, "redis", "asynq")
	if o.GRPC {
		features = append(features, "grpc")
	}
	if o.I18n {
		features = append(features, "i18n")
	}
	if o.Tailwind {
		features = append(features, "tailwind")
	}
	exts := scaffold.EffectiveExtensions(o.HtmxExts)
	if len(exts) > 0 {
		features = append(features, "htmx:"+strings.Join(exts, "+"))
	}
	return features
}

// This file implements the dev command: it is a thin shell around the toolchain package and owns nothing but flag parsing, the configuration lookups and the session report. | 本文件实现 dev 命令：它只是 toolchain 包的一层薄壳，只负责标志解析、配置查询与会话报告。

package cli

import (
	"context"
	"fmt"

	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/tooling/toolchain"
	"github.com/spf13/pflag"
)

// devCommandName is the command word of the development server command. | devCommandName 是开发服务器命令的命令词。
const devCommandName = "dev"

// The dev command's flag names. | dev 命令的标志名。
const (
	// flagDevPort is the port injected into the app as YD_SERVER_PORT. | flagDevPort 是以 YD_SERVER_PORT 注入应用的端口。
	flagDevPort = "port"
	// flagDevNoTailwind skips the Tailwind CSS build. | flagDevNoTailwind 跳过 Tailwind CSS 构建。
	flagDevNoTailwind = "no-tailwind"
	// flagDevNoWatch builds and starts once, without watching files. | flagDevNoWatch 只构建并启动一次，不监听文件。
	flagDevNoWatch = "no-watch"
	// flagDevNoReload disables the dev-only reload endpoint. | flagDevNoReload 关闭仅开发模式的热重载端点。
	flagDevNoReload = "no-reload"
	// flagDevDebounce is the file event coalescing window. | flagDevDebounce 是文件事件抖动合并窗口。
	flagDevDebounce = "debounce"
	// flagDevTailwindBin pins the tailwindcss executable and skips the resolution chain. | flagDevTailwindBin 直接指定 tailwindcss 可执行文件并跳过解析链。
	flagDevTailwindBin = "tailwind-bin"
)

// Config keys the dev command reads on top of the global ones. | dev 命令在全局配置之外读取的配置键。
const (
	// keyDevServerPort is the configured HTTP port, used when --port is absent. | keyDevServerPort 是配置的 HTTP 端口，在 --port 缺省时使用。
	keyDevServerPort = "server.port"
	// keyDevTailwindBin is the configured tailwindcss path, ranked below --tailwind-bin. | keyDevTailwindBin 是配置的 tailwindcss 路径，优先级低于 --tailwind-bin。
	keyDevTailwindBin = "tailwind.bin"
	// keyDevHTTP2ExtendedConnect forces RFC 8441 in the development child. | keyDevHTTP2ExtendedConnect 在开发子进程中强制 RFC 8441。
	keyDevHTTP2ExtendedConnect = "server.http2.extended_connect"
)

// devUsageLine is the usage line of the dev command. | devUsageLine 是 dev 命令的用法行。
const devUsageLine = "yd dev [flags] [-- app args]"

// devFlags keeps the dev command's flag set, installed by registerDevFlags. | devFlags 保存 dev 命令的标志集，由 registerDevFlags 安装。
var devFlags *pflag.FlagSet

func init() {
	Register(Command{
		Name:  devCommandName,
		Short: "run the development server with hot reload | 启动带热重载的开发服务器",
		Long: "Watch the Go sources, the templates and the front-end assets and rebuild accordingly. | 监听 Go 源码、模板与前端资产并据此重新构建。\n" +
			"App arguments are passed after --. | 应用参数写在 -- 之后。\n" +
			"Ctrl+C stops the loop and terminates the child process first. | 按 Ctrl+C 停止主循环，并先终止子进程。",
		Usage: devUsageLine,
		Flags: registerDevFlags,
		Run:   runDev,
	})
}

// registerDevFlags declares the flags of the dev command. | registerDevFlags 声明 dev 命令的标志。
func registerDevFlags(fs *pflag.FlagSet) {
	fs.Int(flagDevPort, 0, "HTTP port injected as YD_SERVER_PORT, 0 keeps the app default | 以 YD_SERVER_PORT 注入的 HTTP 端口，0 表示沿用应用默认值")
	fs.Bool(flagDevNoTailwind, false, "skip the Tailwind CSS build | 跳过 Tailwind CSS 构建")
	fs.Bool(flagDevNoWatch, false, "build and start once without watching files | 只构建并启动一次，不监听文件")
	fs.Bool(flagDevNoReload, false, "disable the dev-only reload endpoint | 关闭仅开发模式的热重载端点")
	fs.Duration(flagDevDebounce, toolchain.DefaultDebounce, "file event debounce window | 文件事件抖动合并窗口")
	fs.String(flagDevTailwindBin, "", "tailwindcss executable, skips the resolution chain | tailwindcss 可执行文件，跳过解析链")
	devFlags = fs
}

// runDev drives the hot-reload loop until the context is cancelled, which is what Ctrl+C does. | runDev 驱动热重载主循环，直到上下文被取消——这正是 Ctrl+C 的效果。
func runDev(ctx context.Context, env *Env) error {
	o := devOptions(env, devFlags)
	runner, err := toolchain.NewDevRunner(o)
	if err != nil {
		return err
	}
	printDevSession(env, o, runner)
	return runner.Run(ctx)
}

// devOptions assembles the toolchain options from the layered environment and the parsed flags. | devOptions 依据分层环境与已解析的标志组装 toolchain 选项。
//
// Precedence follows the repository rule 命令行参数 > 环境变量（含 .env）> 配置文件 > 框架默认值: an explicitly passed flag wins, otherwise the configured value is used, otherwise the toolchain default applies. | 优先级遵循仓库规则「命令行参数 > 环境变量（含 .env）> 配置文件 > 框架默认值」：命令行显式传入的标志优先，其次取配置值，最后落到 toolchain 的默认值。
func devOptions(env *Env, fs *pflag.FlagSet) toolchain.DevOptions {
	debounce, err := fs.GetDuration(flagDevDebounce)
	if err != nil || debounce <= 0 {
		debounce = toolchain.DefaultDebounce
	}
	return toolchain.DevOptions{
		Dir:                  env.WorkDir,
		Port:                 devPort(env, fs),
		TailwindBin:          flagString(fs, flagDevTailwindBin),
		TailwindConfigBin:    configString(env, keyDevTailwindBin),
		NoTailwind:           flagBool(fs, flagDevNoTailwind),
		NoWatch:              flagBool(fs, flagDevNoWatch),
		NoReload:             flagBool(fs, flagDevNoReload),
		Debounce:             debounce,
		AppArgs:              fs.Args(),
		Log:                  logx.Default(),
		Out:                  env.Out,
		HTTP2ExtendedConnect: env.Cfg != nil && env.Cfg.GetBool(keyDevHTTP2ExtendedConnect),
	}
}

// devPort resolves the port injected into the child: --port first, then server.port, then 0 (the app default). | devPort 解析注入子进程的端口：先是 --port，其次是 server.port，最后是 0（应用默认值）。
func devPort(env *Env, fs *pflag.FlagSet) int {
	if flagChanged(fs, flagDevPort) {
		if p, err := fs.GetInt(flagDevPort); err == nil {
			return p
		}
	}
	if env.Cfg != nil {
		return env.Cfg.GetInt(keyDevServerPort)
	}
	return 0
}

// configString reads a string from the layered configuration, tolerating a missing Cfg. | configString 从分层配置读取字符串，Cfg 缺失时返回空串。
func configString(env *Env, key string) string {
	if env.Cfg == nil {
		return ""
	}
	return env.Cfg.GetString(key)
}

// printDevSession writes the session summary before the loop takes over the terminal. | printDevSession 在主循环接管终端之前写出本次会话摘要。
func printDevSession(env *Env, o toolchain.DevOptions, r *toolchain.DevRunner) {
	fmt.Fprintf(env.Out, "yd dev - %s\n", o.Dir)
	fmt.Fprintf(env.Out, "  watch | 文件监听: %s\n", devSwitch(!o.NoWatch))
	fmt.Fprintf(env.Out, "  tailwind | Tailwind 监听: %s\n", devSwitch(!o.NoTailwind))
	fmt.Fprintf(env.Out, "  reload | 热重载: %s\n", devReloadState(r))
	if o.Port > 0 {
		fmt.Fprintf(env.Out, "  port | 端口: %d\n", o.Port)
	}
	if len(o.AppArgs) > 0 {
		fmt.Fprintf(env.Out, "  app args | 应用参数: %v\n", o.AppArgs)
	}
	fmt.Fprintf(env.Out, "ctrl+c stops the loop | 按 ctrl+c 停止主循环\n")
}

// devSwitch renders an enabled/disabled pair. | devSwitch 渲染启用/停用的双语取值。
func devSwitch(on bool) string {
	if on {
		return "enabled | 已启用"
	}
	return "disabled | 已关闭"
}

// devReloadState renders the reload endpoint state together with its path. | devReloadState 渲染热重载端点状态及其路径。
func devReloadState(r *toolchain.DevRunner) string {
	if !r.ReloadEnabled() {
		return devSwitch(false)
	}
	return devSwitch(true) + " " + toolchain.DevReloadPath
}

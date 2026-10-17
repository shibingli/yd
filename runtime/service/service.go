// Package service installs and controls the system service of a yd application: the Windows
// Service Control Manager, systemd and launchd are covered through github.com/kardianos/service.
// | Package service 把 yd 应用安装并控制系统服务：经 github.com/kardianos/service 覆盖
// Windows 服务控制管理器（SCM）、systemd 与 launchd。
//
// Responsibility boundary | 职责边界：
//
//   - it maps this framework's Options onto the kardianos Config, so a project never writes
//     platform specific service files by hand; | 它把本框架的 Options 映射为 kardianos 的 Config，
//     使项目无须手写平台相关的服务文件；
//   - Control performs exactly one service manager action and returns a readable bilingual result;
//     | Control 只执行一个服务管理器动作，并返回可读的双语结果；
//   - Run hands a foreground function to the service manager: Start launches it, Stop cancels its
//     context and waits for it to return, so the process never leaves through os.Exit and the
//     application keeps its own graceful shutdown path. | Run 把前台函数交给服务管理器：Start 启动它，
//     Stop 取消它的 context 并等待其返回，因此进程绝不走 os.Exit，优雅退出仍由应用自己掌控；
//   - it does NOT own the shutdown order of the application (that is the lifecycle registry, driven
//     by app.App), it does not parse command lines (that is the cli package) and it never elevates
//     privileges on its own: a denied action is reported as ErrPermissionDenied. | 它不负责应用的关闭顺序
//     （那是 lifecycle 注册表与 app.App 的职责），不解析命令行（那是 cli 包），也绝不自行提权：
//     权限被拒时只报告 ErrPermissionDenied。
//
// The two entry points of the framework are Control (used by `yd service <action>` and by the
// generated `-service <action>` argument) and Run (used by a generated main when the process is
// started by the service manager). | 框架的两个入口是 Control（供 `yd service <action>` 与生成的
// `-service <action>` 参数使用）与 Run（供生成项目在服务管理器启动进程时使用）。
package service

import (
	"path/filepath"
	"strings"

	"github.com/kardianos/service"
	"github.com/shibingli/yd/core/config"
	"github.com/shibingli/yd/core/errx"
)

// Sentinel errors of the service package, always matched with errors.Is. | service 包的哨兵错误，一律用 errors.Is 判定。
var (
	// ErrUnsupportedPlatform reports a platform without a usable service manager. | ErrUnsupportedPlatform 表示当前平台没有可用的服务管理器。
	ErrUnsupportedPlatform = errx.New("service: no supported service manager on this platform | service：当前平台没有受支持的服务管理器")
	// ErrNotInstalled reports an action against a service that is not installed. | ErrNotInstalled 表示对未安装的服务执行了动作。
	ErrNotInstalled = errx.New("service: the service is not installed | service：服务尚未安装")
	// ErrPermissionDenied reports an action denied for lack of administrator or root rights. | ErrPermissionDenied 表示因缺少管理员或 root 权限而被拒绝。
	ErrPermissionDenied = errx.New("service: administrator or root rights are required | service：需要管理员或 root 权限")
	// ErrUnknownAction reports an action word outside the supported set. | ErrUnknownAction 表示动作词不在支持集合内。
	ErrUnknownAction = errx.New("service: unknown service action | service：未知的服务动作")
	// ErrActionFailed reports a service manager action that failed for another reason. | ErrActionFailed 表示服务管理器动作因其它原因失败。
	ErrActionFailed = errx.New("service: service action failed | service：服务动作执行失败")
	// ErrStopTimeout reports a run function still running when the stop grace period expired. | ErrStopTimeout 表示停止宽限期到期时 run 函数仍未返回。
	ErrStopTimeout = errx.New("service: stop grace period expired | service：停止宽限期已到期")
	// ErrNilRun reports a nil run function handed to Run. | ErrNilRun 表示传给 Run 的 run 函数为空。
	ErrNilRun = errx.New("service: nil run function | service：run 函数为空")
)

// Configuration keys read by OptionsFromConfig; they mirror the `service` section of
// configs/config.yaml and are overridable through YD_SERVICE_* environment variables.
// | OptionsFromConfig 读取的配置键；它们对应 configs/config.yaml 的 `service` 段，
// 并可由 YD_SERVICE_* 环境变量覆盖。
const (
	// KeyName is the service name registered in the service manager. | KeyName 是注册到服务管理器的服务名。
	KeyName = "service.name"
	// KeyDisplayName is the display name shown by the service manager. | KeyDisplayName 是服务管理器显示的显示名。
	KeyDisplayName = "service.display_name"
	// KeyDescription is the long description of the service. | KeyDescription 是服务的详细描述。
	KeyDescription = "service.description"
	// KeyExecutable is the executable the service runs; empty means the current executable. | KeyExecutable 是服务运行的程序；为空表示当前程序。
	KeyExecutable = "service.executable"
	// KeyArguments are the arguments passed to the executable. | KeyArguments 是传给程序的参数。
	KeyArguments = "service.arguments"
	// KeyWorkingDirectory is the initial working directory (unsupported on Windows). | KeyWorkingDirectory 是初始工作目录（Windows 不支持）。
	KeyWorkingDirectory = "service.working_directory"
	// KeyUserName is the account the service runs as. | KeyUserName 是服务运行所用的账号。
	KeyUserName = "service.user_name"
	// KeyDependsOn lists the service dependencies. | KeyDependsOn 列出服务依赖项。
	KeyDependsOn = "service.depends_on"
	// KeyEnv maps extra environment variables of the service. | KeyEnv 映射服务的额外环境变量。
	KeyEnv = "service.env"
	// KeyRestartOnExit keeps the service restarted after an unexpected exit. | KeyRestartOnExit 让服务在异常退出后自动重启。
	KeyRestartOnExit = "service.restart_on_exit"
)

// keyAppName is the fallback configuration key of the service name. | keyAppName 是服务名的回退配置键。
const keyAppName = "app.name"

// fallbackServiceName is the service name used when neither the options nor the executable yield
// one, i.e. when a caller passes an entirely empty Options value. | fallbackServiceName 是选项与程序都无法给出名字时
// （即调用方传入完全空的 Options）使用的服务名。
const fallbackServiceName = "yd"

// restartOnExitKey is the POSIX option that keeps a service restarted by the init system. | restartOnExitKey 是让
// init 系统自动重启服务的 POSIX 选项键。
const restartOnExitKey = "Restart"

// restartAlwaysValue is the value of restartOnExitKey meaning "always restart". | restartAlwaysValue 是 restartOnExitKey 取「始终重启」时的取值。
const restartAlwaysValue = "always"

// onFailureKey is the Windows option that tells the service control manager what to do when the
// process exits unexpectedly. | onFailureKey 是告知服务控制管理器「进程异常退出时怎么办」的 Windows 选项键。
//
// kardianos declares it (and its values) in service_windows.go, so a direct symbol reference
// compiles on Windows only and breaks every POSIX cross-build; the option map is keyed by plain
// strings anyway, and these literals are the exact keys kardianos matches on. | kardianos 把它
// （及其取值）声明在 service_windows.go 里，直接引用符号只能编过 Windows，会让所有 POSIX 交叉编译失败；
// 该选项表本就以普通字符串为键，而这里的字面量正是 kardianos 实际匹配的键。
const onFailureKey = "OnFailure"

// onFailureRestartValue is the value of onFailureKey meaning "restart the service". | onFailureRestartValue 是 onFailureKey 取「重启服务」时的取值。
const onFailureRestartValue = "restart"

// Options describes one system service. | Options 描述一个系统服务。
//
// Fields are ordered so that the struct keeps the smallest possible footprint: the map comes first,
// then the six strings, then the two slices and the single flag. | 字段顺序以最小化结构体体积为准：
// map 在前，随后是六个 string，再是两个切片与单个布尔标志。
type Options struct {
	// EnvVars are extra environment variables handed to the service. | EnvVars 是传给服务的额外环境变量。
	EnvVars map[string]string
	// Name is the service name registered in the service manager; it should carry no spaces. | Name 是注册到服务管理器的服务名，建议不含空格。
	Name string
	// DisplayName is the human readable name shown by the service manager. | DisplayName 是服务管理器展示的可读名称。
	DisplayName string
	// Description is the long description of the service. | Description 是服务的详细描述。
	Description string
	// Executable is the program the service runs; empty means the current executable. | Executable 是服务运行的程序；为空表示当前程序。
	Executable string
	// WorkingDirectory is the initial working directory; Windows ignores it and always starts the
	// service in the system directory. | WorkingDirectory 是初始工作目录；Windows 会忽略它，
	// 始终在系统目录启动服务。
	// A service that reads relative configuration files should therefore pass absolute paths
	// through Arguments. | 因此需要读取相对路径配置的服务应改用 Arguments 传绝对路径。
	WorkingDirectory string
	// UserName is the account the service runs as; empty means the service manager default. | UserName 是服务运行所用的账号；为空表示服务管理器的缺省账号。
	UserName string
	// Arguments are the arguments passed to the executable. | Arguments 是传给程序的参数。
	Arguments []string
	// DependsOn lists the services this service starts after. | DependsOn 列出本服务启动时依赖的服务。
	DependsOn []string
	// RestartOnExit asks the service manager to restart the service after an unexpected exit. | RestartOnExit 请求服务管理器在服务异常退出后自动重启。
	RestartOnExit bool
}

// OptionsFromConfig reads the service options from the layered configuration. | OptionsFromConfig 从分层配置读取服务选项。
//
// The service name falls back to app.name, the display name to the service name and the description
// to the display name, so a project that only sets app.name still yields a complete service entry.
// A nil configuration yields the zero Options, which New fills with the executable base name.
// | 服务名回退到 app.name，显示名回退到服务名，描述回退到显示名，因此只设置了 app.name 的项目也能得到完整的服务条目。
// 配置为 nil 时返回零值 Options，由 New 用程序的基础名补齐。
func OptionsFromConfig(c *config.Config) Options {
	if c == nil {
		return Options{}
	}

	name := strings.TrimSpace(c.GetString(KeyName))
	if name == "" {
		name = strings.TrimSpace(c.GetString(keyAppName))
	}
	display := strings.TrimSpace(c.GetString(KeyDisplayName))
	if display == "" {
		display = name
	}
	description := strings.TrimSpace(c.GetString(KeyDescription))
	if description == "" {
		description = display
	}

	return Options{
		Name:             name,
		DisplayName:      display,
		Description:      description,
		Executable:       strings.TrimSpace(c.GetString(KeyExecutable)),
		WorkingDirectory: strings.TrimSpace(c.GetString(KeyWorkingDirectory)),
		UserName:         strings.TrimSpace(c.GetString(KeyUserName)),
		Arguments:        c.GetStringSlice(KeyArguments),
		DependsOn:        c.GetStringSlice(KeyDependsOn),
		EnvVars:          envFromConfig(c),
		RestartOnExit:    c.GetBool(KeyRestartOnExit),
	}
}

// envFromConfig reads the environment variables of the service. | envFromConfig 读取服务的环境变量。
//
// Two shapes are accepted, because the configuration engine lower-cases every map key while POSIX
// environment variable names are case sensitive: | 接受两种形态，因为配置引擎会把所有映射键转为小写，
// 而 POSIX 环境变量名是大小写敏感的：
//
//   - the list form, one "NAME=value" per entry, keeps the name exactly as written and is therefore
//     the recommended shape; | 列表形式，每项一个 "NAME=value"，名字原样保留，因此是推荐形态；
//   - the mapping form, where the names arrive lower-cased ("YD_APP_ENV" becomes "yd_app_env") and a
//     caller that needs exact names should switch to the list form. | 映射形式的键名会被转为小写
//     （"YD_APP_ENV" 变成 "yd_app_env"），需要精确名字的调用方应改用列表形式。
func envFromConfig(c *config.Config) map[string]string {
	pairs := c.GetStringSlice(KeyEnv)
	env := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		env[name] = value
	}
	if len(env) > 0 {
		return env
	}
	return c.GetStringMapString(KeyEnv)
}

// New builds the service handle of o. | New 构建 o 对应的服务句柄。
//
// The handle is meant for the control actions (Install/Uninstall/Start/Stop/Restart/Status) that
// Control drives; the Run method of the returned handle is driven by a stub program that does
// nothing, so callers that want to host a program must use Run instead. | 该句柄用于 Control 驱动的控制动作
// （Install/Uninstall/Start/Stop/Restart/Status）；其 Run 方法由什么都不做的桩程序支撑，因此需要托管程序的调用方
// 应改用 Run。
//
// An unsupported platform yields ErrUnsupportedPlatform. | 平台不受支持时返回 ErrUnsupportedPlatform。
func New(o Options) (service.Service, error) {
	svc, err := service.New(controlProgram{}, configFor(o))
	if err != nil {
		return nil, platformError(err)
	}
	return svc, nil
}

// configFor maps the framework options onto the kardianos configuration. | configFor 把框架选项映射为 kardianos 配置。
func configFor(o Options) *service.Config {
	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = executableName(o.Executable)
	}

	display := strings.TrimSpace(o.DisplayName)
	if display == "" {
		display = name
	}
	description := strings.TrimSpace(o.Description)
	if description == "" {
		description = display
	}

	cfg := &service.Config{
		Name:             name,
		DisplayName:      display,
		Description:      description,
		UserName:         strings.TrimSpace(o.UserName),
		Arguments:        o.Arguments,
		Executable:       strings.TrimSpace(o.Executable),
		Dependencies:     o.DependsOn,
		WorkingDirectory: strings.TrimSpace(o.WorkingDirectory),
		EnvVars:          o.EnvVars,
	}
	if o.RestartOnExit {
		// Windows reads OnFailure, the POSIX init systems read Restart; only the keys that are
		// set here are written into the generated service file. | Windows 读 OnFailure，
		// 各 POSIX init 系统读 Restart；只有这里设置过的键才会写进生成的服务文件。
		cfg.Option = service.KeyValue{
			onFailureKey:     onFailureRestartValue,
			restartOnExitKey: restartAlwaysValue,
		}
	}
	return cfg
}

// executableName derives a service name from an executable path, falling back to the framework
// name when the path is empty and to the plain base name when it is not. | executableName 从程序路径推导服务名：
// 路径为空时回退为框架名，否则取去掉扩展名的基础名。
func executableName(exe string) string {
	exe = strings.TrimSpace(exe)
	if exe == "" {
		return fallbackServiceName
	}
	base := strings.TrimSuffix(filepath.Base(filepath.Clean(exe)), filepath.Ext(exe))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return fallbackServiceName
	}
	return base
}

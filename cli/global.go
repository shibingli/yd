package cli

import (
	"github.com/spf13/pflag"
)

// Global flag names, shared by every command's flag set. | 全局标志名，被所有命令的标志集共享。
const (
	// flagConfig is the path of the config file to load. | flagConfig 是要加载的配置文件路径。
	flagConfig = "config"
	// flagEnvFile is the path of the .env file to layer below the environment. | flagEnvFile 是要分层到
	// 环境变量之下的 .env 文件路径。
	flagEnvFile = "env-file"
	// flagVerbose raises the log level to debug. | flagVerbose 把日志级别提升到 debug。
	flagVerbose = "verbose"
	// flagQuiet lowers the log level to error. | flagQuiet 把日志级别降到 error。
	flagQuiet = "quiet"
	// flagNoColor disables ANSI colors in CLI output. | flagNoColor 关闭 CLI 输出的 ANSI 颜色。
	flagNoColor = "no-color"
	// flagVersion prints the build version and exits. | flagVersion 打印构建版本并退出。
	flagVersion = "version"
)

// Config keys and values that the global flags write into the configuration layer. | 全局标志写入配置层的配置键与取值。
const (
	// keyLogLevel is the log level key consumed by configureLogger. | keyLogLevel 是 configureLogger 消费的日志级别键。
	keyLogLevel = "log.level"
	// keyLogColor is the color switch consumed by ColorEnabled. | keyLogColor 是 ColorEnabled 消费的颜色开关。
	keyLogColor = "log.color"
	// levelDebug is the level value --verbose selects. | levelDebug 是 --verbose 选择的级别取值。
	levelDebug = "debug"
	// levelError is the level value --quiet selects. | levelError 是 --quiet 选择的级别取值。
	levelError = "error"
)

// globalFlagSpec describes one global flag. | globalFlagSpec 描述一个全局标志。
type globalFlagSpec struct {
	// name is the long flag name without dashes. | name 是不带横线的长标志名。
	name string
	// short is the one-letter shorthand, empty when the flag has none. | short 是单字母简写，无简写时为空。
	short string
	// usage is the bilingual help text. | usage 是双语的帮助文案。
	usage string
	// isString marks the two string-valued global flags; the rest are booleans. | isString 标记两个字符串型
	// 全局标志，其余均为布尔型。
	isString bool
}

// globalFlagSpecs lists the global flags in help order. | globalFlagSpecs 按帮助顺序列出全局标志。
var globalFlagSpecs = []globalFlagSpec{
	{name: flagConfig, short: "c", isString: true, usage: "config file path | 配置文件路径"},
	{name: flagEnvFile, isString: true, usage: ".env file path | .env 文件路径"},
	{name: flagVerbose, short: "v", usage: "enable debug logging | 启用调试日志"},
	{name: flagQuiet, short: "q", usage: "only report errors | 仅输出错误"},
	{name: flagNoColor, usage: "disable ANSI colors | 关闭 ANSI 颜色"},
	{name: flagVersion, usage: "print version and exit | 打印版本并退出"},
}

// registerGlobalFlags adds the global flags to fs. | registerGlobalFlags 把全局标志加入 fs。
//
// Flags already present are skipped, so a command may shadow a global flag with one of its own; that
// is how `yd assets add ... --version 1.0.0` can use --version as the asset version while
// `yd --version` keeps printing the build version. | 已存在的标志会被跳过，因此命令可以用自己的标志
// 遮蔽全局标志：这正是 `yd assets add ... --version 1.0.0` 能把 --version 当作资产版本，而
// `yd --version` 仍然打印构建版本的原因。
func registerGlobalFlags(fs *pflag.FlagSet) {
	for _, spec := range globalFlagSpecs {
		if fs.Lookup(spec.name) != nil {
			continue
		}
		shorthand := spec.short
		if shorthand != "" && fs.ShorthandLookup(shorthand) != nil {
			shorthand = ""
		}
		switch {
		case spec.isString && shorthand != "":
			fs.StringP(spec.name, shorthand, "", spec.usage)
		case spec.isString:
			fs.String(spec.name, "", spec.usage)
		case shorthand != "":
			fs.BoolP(spec.name, shorthand, false, spec.usage)
		default:
			fs.Bool(spec.name, false, spec.usage)
		}
	}
}

// globalFlagUsages renders the global flags as a help block. | globalFlagUsages 把全局标志渲染为帮助文本块。
func globalFlagUsages() string {
	fs := pflag.NewFlagSet("yd", pflag.ContinueOnError)
	registerGlobalFlags(fs)
	return fs.FlagUsages()
}

// newCommandFlagSet builds the flag set of one command: the command's own flags first, then the
// global flags that the command did not shadow. | newCommandFlagSet 构造单个命令的标志集：先命令自身
// 的标志，再加入未被遮蔽的全局标志。
func newCommandFlagSet(env *Env, c Command) *pflag.FlagSet {
	fs := pflag.NewFlagSet(c.Name, pflag.ContinueOnError)
	fs.SetOutput(env.Out)
	if c.Flags != nil {
		c.Flags(fs)
	}
	registerGlobalFlags(fs)
	fs.Usage = func() { printCommandHelp(env.Out, c, fs) }
	return fs
}

// flagChanged reports whether the named flag exists and was passed on the command line. | flagChanged 报告具名标志是否存在且在命令行中被传入。
func flagChanged(fs *pflag.FlagSet, name string) bool {
	if fs == nil {
		return false
	}
	fl := fs.Lookup(name)
	return fl != nil && fl.Changed
}

// flagBool reads a boolean flag, returning false when it is absent or of another type. | flagBool 读取布尔标志，标志缺失或类型不符时返回 false。
func flagBool(fs *pflag.FlagSet, name string) bool {
	if fs == nil || fs.Lookup(name) == nil {
		return false
	}
	v, err := fs.GetBool(name)
	return err == nil && v
}

// flagString reads a string flag, returning "" when it is absent or of another type. | flagString 读取字符串标志，标志缺失或类型不符时返回 ""。
func flagString(fs *pflag.FlagSet, name string) string {
	if fs == nil || fs.Lookup(name) == nil {
		return ""
	}
	v, err := fs.GetString(name)
	if err != nil {
		return ""
	}
	return v
}

// flagStrings reads a repeatable string flag, returning nil when it is absent or of another type. | flagStrings 读取可重复的字符串标志，标志缺失或类型不符时返回 nil。
func flagStrings(fs *pflag.FlagSet, name string) []string {
	if fs == nil || fs.Lookup(name) == nil {
		return nil
	}
	v, err := fs.GetStringArray(name)
	if err != nil {
		return nil
	}
	return v
}

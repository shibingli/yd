package cli

import (
	"context"
	"io"

	"github.com/shibingli/yd/config"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// Command describes one subcommand of the yd CLI. | Command 描述 yd CLI 的一个子命令。
//
// Registration is done at package init through Register; help output is derived entirely from these
// fields, so Short/Usage are mandatory for every command. | 命令在包初始化阶段通过 Register 注册；
// 帮助输出完全由这些字段推导，因此每个命令都必须提供 Short 与 Usage。
//
// A Command value is meant to be executed sequentially: Run receives no flag set of its own and reads
// the parsed flags through the closure installed by Flags, so calling Execute concurrently for the
// same command is not supported. | 一个 Command 值按顺序执行：Run 不接收自己的标志集，而是通过 Flags
// 安装的闭包读取已解析的标志，因此不支持对同一命令并发调用 Execute。
type Command struct {
	// Name is the command word typed after yd. | Name 是紧跟在 yd 之后的命令词。
	Name string
	// Short is the one-line summary shown in the command list. | Short 是命令列表中显示的一行摘要。
	Short string
	// Long is the optional multi-line description shown by the command's --help. | Long 是命令 --help 显示的
	// 可选多行描述。
	Long string
	// Usage is the usage line, e.g. "yd assets update [--check] [--offline]". | Usage 是用法行，例如
	// "yd assets update [--check] [--offline]"。
	Usage string
	// Flags registers the command's own flags; it may be nil for a command without flags and is called
	// before Run on every Execute. | Flags 注册命令自身的标志；无标志的命令可以留空，它在每次 Execute 中
	// 先于 Run 被调用。
	Flags func(*pflag.FlagSet)
	// Run executes the command; it must not be nil. | Run 执行命令，不得为空。
	Run func(ctx context.Context, env *Env) error
}

// Env carries everything a command is allowed to touch. | Env 承载命令可以触及的一切。
//
// Commands never reach for process globals: they write user-visible output to Out, read interactive
// answers from In, touch files through FS and read configuration from Cfg. That is what makes every
// command testable on an in-memory file system with a non-TTY stdin. | 命令不直接使用进程全局状态：
// 用户可见输出写 Out，交互输入读 In，文件访问经 FS，配置读取自 Cfg。这使得每个命令都能在内存文件系统
// 与非 TTY 标准输入下被测试。
type Env struct {
	// Cfg is the layered configuration; Execute loads it from WorkDir when nil. | Cfg 是分层配置；为 nil 时
	// Execute 会从 WorkDir 加载。
	Cfg *config.Config
	// FS is the file system commands read and write through. | FS 是命令读写所经的文件系统。
	FS afero.Fs
	// Out receives user-visible output (tables, reports, prompts). | Out 接收用户可见输出（表格、报告、提示）。
	Out io.Writer
	// Err receives diagnostics that are not routed through logx. | Err 接收未经 logx 的诊断信息。
	Err io.Writer
	// In is the interactive input stream. | In 是交互输入流。
	In io.Reader
	// WorkDir is the base directory for config files, the .env file and asset paths. | WorkDir 是配置文件、
	// .env 文件与资产路径的基准目录。
	WorkDir string
	// Version is the version string printed by --version. | Version 是 --version 打印的版本串。
	Version string
	// IsTTY reports whether In is an interactive terminal; when false every interactive helper returns
	// its default value without reading. | IsTTY 报告 In 是否为交互终端；为 false 时所有交互辅助都直接返回
	// 默认值而不读取输入。
	IsTTY bool
}

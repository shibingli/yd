// This file registers the `yd dev` placeholder: the hot-reload development server lands in Task 30. | 本文件
// 注册 `yd dev` 占位命令：热重载开发服务器将在 Task 30 交付。

package cli

// devCommandName is the command word of the development server command. | devCommandName 是开发服务器命令的命令词。
const devCommandName = "dev"

func init() {
	Register(Command{
		Name:  devCommandName,
		Short: "run the development server with hot reload | 启动带热重载的开发服务器",
		Long: "Watch the Go sources, the templates and the front-end assets, then rebuild and restart the\n" +
			"application whenever any of them changes.\n" +
			"监听 Go 源码、模板与前端资产，任一发生变化即重新构建并重启应用。",
		Usage: "yd dev [flags]",
		Run:   notImplemented(devCommandName),
	})
}

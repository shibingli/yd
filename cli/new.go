// This file registers the `yd new` placeholder: the scaffolding implementation lands in Task 28. | 本文件
// 注册 `yd new` 占位命令：脚手架实现将在 Task 28 交付。

package cli

// newCommandName is the command word of the scaffolding command. | newCommandName 是脚手架命令的命令词。
const newCommandName = "new"

func init() {
	Register(Command{
		Name:  newCommandName,
		Short: "create a new yd project | 创建新的 yd 项目",
		Long: "Scaffold a new project from the embedded templates, then lay down its configuration and\n" +
			"front-end assets.\n" +
			"依据内嵌模板生成新项目，随后铺开它的配置与前端资产。",
		Usage: "yd new <project-name> [flags]",
		Run:   notImplemented(newCommandName),
	})
}

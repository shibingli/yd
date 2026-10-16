// This file registers the `yd gen` placeholder: the code and CRUD generator lands in Task 31. | 本文件注册
// `yd gen` 占位命令：代码与 CRUD 生成器将在 Task 31 交付。

package cli

// genCommandName is the command word of the generator command. | genCommandName 是生成器命令的命令词。
const genCommandName = "gen"

func init() {
	Register(Command{
		Name:  genCommandName,
		Short: "generate code from templates | 依据模板生成代码",
		Long: "Generate models, handlers, routes and migration stubs from the embedded templates, keeping\n" +
			"the generated code reproducible.\n" +
			"依据内嵌模板生成模型、处理器、路由与迁移桩代码，保证生成结果可重现。",
		Usage: "yd gen <kind> <name> [flags]",
		Run:   notImplemented(genCommandName),
	})
}

// This file registers the `yd build` placeholder: the production build pipeline lands in Task 32. | 本文件
// 注册 `yd build` 占位命令：生产构建流水线将在 Task 32 交付。

package cli

// buildCommandName is the command word of the production build command. | buildCommandName 是生产构建命令的命令词。
const buildCommandName = "build"

func init() {
	Register(Command{
		Name:  buildCommandName,
		Short: "build the production binary | 构建生产二进制",
		Long: "Compile the CSS bundle, verify the locked assets and produce the release binary with the\n" +
			"front-end embedded.\n" +
			"编译 CSS 产物、校验锁定的前端资产，并产出内嵌前端的发布二进制。",
		Usage: "yd build [flags]",
		Run:   notImplemented(buildCommandName),
	})
}

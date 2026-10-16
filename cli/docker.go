// This file registers the `yd docker` placeholder: the container build and compose workflow lands in
// Task 33. | 本文件注册 `yd docker` 占位命令：容器构建与 compose 工作流将在 Task 33 交付。

package cli

// dockerCommandName is the command word of the container command. | dockerCommandName 是容器命令的命令词。
const dockerCommandName = "docker"

func init() {
	Register(Command{
		Name:  dockerCommandName,
		Short: "build and run the container image | 构建并运行容器镜像",
		Long: "Render the Dockerfile and the compose files from the project configuration, build the image\n" +
			"and bring the stack up or down.\n" +
			"依据项目配置渲染 Dockerfile 与 compose 文件，构建镜像并启停整套服务。",
		Usage: "yd docker <build|up|down> [flags]",
		Run:   notImplemented(dockerCommandName),
	})
}

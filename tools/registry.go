// This file holds the tool registry: the four optional external tools in a fixed order, plus the
// lookup used by the install command. | 本文件保存工具注册表：固定顺序的四个可选外部工具，以及安装命令所用的查找。
//
// The registry is the single source of truth for names, purposes and install hints. | 注册表是名称、用途与安装提示的唯一来源。

package tools

import (
	"strings"

	"github.com/shibingli/yd/align"
	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/ko"
)

// The registry names, which are also the file names of the project-local copies under .yd/tools. | 注册表名称，同时也是 .yd/tools 下项目内副本的文件名。
const (
	// toolBetteralign is the optional struct-alignment analyzer of the align package. | toolBetteralign 是 align 包可选的结构体对齐分析器。
	toolBetteralign = "betteralign"
	// toolKo is the optional Dockerfile-less image builder of the ko package. | toolKo 是 ko 包可选的无 Dockerfile 镜像构建器。
	toolKo = "ko"
	// toolTailwindcss is the optional Tailwind CSS standalone compiler of the toolchain package. | toolTailwindcss 是 toolchain 包可选的 Tailwind CSS standalone 编译器。
	toolTailwindcss = "tailwindcss"
	// toolDocker is the container engine behind `yd docker` and `yd ko --local`. | toolDocker 是 `yd docker` 与 `yd ko --local` 背后的容器引擎。
	toolDocker = "docker"
)

// registry holds the four entries in the order Registry returns them. | registry 按 Registry 返回的顺序保存四个条目。
//
// betteralign and ko quote align.InstallHint and ko.InstallHint verbatim instead of repeating the
// command, so each hint has exactly one definition and the registry can never drift from the
// resolver that raises it. | betteralign 与 ko 逐字引用 align.InstallHint 与 ko.InstallHint 而不重写命令，
// 因此每份提示只有一处定义，注册表绝不会与抛出它的解析器发生漂移。
//
// tailwindcss and docker carry a sentence instead of a command: neither can be installed with
// go install, and pretending otherwise would send the user down a path the tool does not have. | tailwindcss
// 与 docker 携带的是一句话而不是命令：两者都无法用 go install 安装，装作可以会把用户引到工具本身没有的路上。
var registry = []Tool{
	{
		Name:        toolBetteralign,
		Summary:     "report struct field orders that waste memory | 报告浪费内存的结构体字段顺序",
		InstallHint: align.InstallHint,
		GoInstall:   "github.com/dkorunic/betteralign/cmd/betteralign@latest",
		DocURL:      "https://github.com/dkorunic/betteralign",
		VersionArgs: []string{"-V=full"},
	},
	{
		Name:        toolKo,
		Summary:     "build images straight from Go source without a Dockerfile | 直接从 Go 源码构建镜像，无需 Dockerfile",
		InstallHint: ko.InstallHint,
		GoInstall:   "github.com/google/ko@latest",
		DocURL:      "https://ko.build",
		VersionArgs: []string{"version"},
	},
	{
		Name:        toolTailwindcss,
		Summary:     "compile Tailwind CSS v4 stylesheets | 编译 Tailwind CSS v4 样式表",
		InstallHint: "run `yd assets update` or drop the binary into " + LocalToolDir + " | 运行 `yd assets update`，或把二进制放入 " + LocalToolDir,
		DocURL:      "https://tailwindcss.com/docs/installation/tailwind-cli",
		VersionArgs: []string{"--version"},
	},
	{
		Name:        toolDocker,
		Summary:     "build and run container images | 构建并运行容器镜像",
		InstallHint: "install Docker Engine or Docker Desktop | 安装 Docker Engine 或 Docker Desktop",
		DocURL:      "https://docs.docker.com/engine/install/",
		// No version query on purpose: `docker version` talks to the daemon, and a status probe
		// must stay free of side effects. | 刻意不查询版本：`docker version` 会与守护进程通信，
		// 而状态探测必须没有副作用。
		VersionArgs: nil,
	},
}

// Registry returns the registered tools in probe order. | Registry 按探测顺序返回已登记的工具。
//
// The copy is deep enough to be safe: a caller may reorder the slice or rewrite a VersionArgs entry
// without changing what the next call sees. | 该副本足够深：调用方可以重排切片或改写某个 VersionArgs，
// 而不会改变下一次调用看到的内容。
func Registry() []Tool {
	out := make([]Tool, len(registry))
	for i, t := range registry {
		if len(t.VersionArgs) > 0 {
			t.VersionArgs = append([]string(nil), t.VersionArgs...)
		}
		out[i] = t
	}
	return out
}

// Lookup returns the registry entry called name. | Lookup 返回名为 name 的注册表条目。
//
// The comparison is case-insensitive and ignores surrounding blanks, because the name is also a
// command word a user types. An unknown name yields ErrUnknownTool carrying the available names, so
// the caller never has to guess what to type instead. | 比较忽略大小写与首尾空白，因为该名称同时是用户输入的命令词。
// 未知名称返回 ErrUnknownTool 并带上可用名称，使调用方无需猜测该改成什么。
func Lookup(name string) (Tool, error) {
	want := strings.TrimSpace(name)
	for _, t := range registry {
		if strings.EqualFold(t.Name, want) {
			return t, nil
		}
	}
	return Tool{}, errx.Detail(ErrUnknownTool, "%q; available tools | 可用工具: %s", name, strings.Join(names(), ", "))
}

// names returns the registry names in probe order. | names 按探测顺序返回注册表名称。
func names() []string {
	out := make([]string, 0, len(registry))
	for _, t := range registry {
		out = append(out, t.Name)
	}
	return out
}

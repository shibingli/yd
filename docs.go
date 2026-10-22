// Package yd is the entry point of the YunDev full-stack web framework: a Go
// 1.27 monolith that renders HTML fragments with Htmx 4 instead of shipping a
// separate SPA. | Package yd 是 YunDev 全栈 Web 框架的入口包：一个 Go 1.27 单体框架，
// 用 Htmx 4 直接渲染 HTML 片段，而不是再维护一套独立的前端单页应用。
//
// This file carries the official package description, the machine-readable
// references other tools can import, and runnable examples. The implementation
// lives in the sub-packages listed below. | 本文件承载官方包说明、可供其它工具引用的
// 机器可读元信息，以及可直接运行的示例；具体实现位于下列子包中。
//
// # Design goals | 设计目标
//
//   - Modern monolith over BFF + SPA: the server returns HTML fragments, the
//     browser swaps them in. There is no Node.js build pipeline and no client
//     router to maintain. | 以「全栈单体」取代 BFF + SPA：服务端返回 HTML 片段，
//     浏览器就地替换；没有 Node.js 构建管道，也没有需要维护的客户端路由。
//   - Zero runtime CDN: every front-end asset is embedded into the binary, so an
//     application builds and runs completely offline. | 运行时零 CDN：所有前端资产
//     均内嵌进二进制，应用可完全离线构建与运行。
//   - Performance without ceremony: an in-house radix router, a zero-copy
//     logger, and hardware-adaptive JSON/Base64 layers, all reachable through
//     ordinary net/http handlers. | 性能不需要额外代价：自研基数树路由、零拷贝日志、
//     硬件自适应 JSON/Base64 层，全部通过普通的 net/http 处理器即可使用。
//   - Compliance by default: SM2/SM3/SM4 are first-class, both on the server and
//     in the browser, so sensitive fields can be encrypted before they leave the
//     form. | 合规开箱即用：SM2/SM3/SM4 在服务端与浏览器端都是一等公民，
//     敏感字段可在离开表单之前完成加密。
//
// # Package index | 包索引
//
// Foundation | 基础层:
//
//   - [github.com/shibingli/ydx/config] layered configuration (flags > env > file > defaults). | 分层配置（参数 > 环境变量 > 文件 > 默认值）。
//   - [github.com/shibingli/ydx/afs] afero filesystem facade over OS, memory and embed backends. | afero 文件系统门面（OS / 内存 / embed）。
//   - [github.com/shibingli/ydx/json], [github.com/shibingli/ydx/base64] hardware-adaptive codecs. | 硬件自适应编解码层。
//   - [github.com/shibingli/ydx/log] zero-copy structured logging. | 零拷贝结构化日志。
//   - [github.com/shibingli/ydx/errs] declared sentinel errors. | 声明式哨兵错误。
//
// HTTP layer | HTTP 层:
//
//   - [github.com/shibingli/ydx/router] radix router with mixed segment capture. | 支持段内混合捕获的基数树路由。
//   - [github.com/shibingli/ydx/render], [github.com/shibingli/yd/http/htmx] html/template engine and Htmx 4 helpers. | 模板引擎与 Htmx 4 服务端辅助。
//   - [github.com/shibingli/ydx/middleware] recovery, request id, access log, CORS, rate limiting, compression. | 基础中间件与压缩协商。
//   - [github.com/shibingli/yd/http/i18n], [github.com/shibingli/yd/http/openapi] localisation and 3.0 documents. | 国际化与 OpenAPI 3.0 文档。
//   - [github.com/shibingli/yd/runtime/server], [github.com/shibingli/yd/runtime/grpcx] HTTP/1.1 + H2C + HTTP/3 + ACME, and a gRPC dual channel. | 混合协议服务器与 gRPC 双通道。
//
// Realtime and security | 实时与安全:
//
//   - [github.com/shibingli/ydx/sse], [github.com/shibingli/ydx/websocket] server-sent events and RFC 6455 WebSockets. | 事件流与 WebSocket。
//   - [github.com/shibingli/ydx/jwt], [github.com/shibingli/yd/security/guomi] reflection-free JWTs and SM2/SM3/SM4. | 免反射 JWT 与国密套件。
//
// Data | 数据层:
//
//   - [github.com/shibingli/ydx/gorm], [github.com/shibingli/ydx/redis], [github.com/shibingli/ydx/asynq] GORM, Redis and asynq. | GORM / Redis / asynq 插件。
//
// Application and tooling | 应用与工具链:
//
//   - [github.com/shibingli/yd/runtime/app] the facade that wires everything together. | 装配一切的应用门面。
//   - [github.com/shibingli/yd/web] embedded templates and static assets. | 内嵌模板与静态资产。
//
// # Quick start | 快速开始
//
// Install the CLI and scaffold a project: | 安装 CLI 并初始化项目：
//
//	go install github.com/shibingli/yd/cmd/yd@latest
//	yd new myapp && cd myapp && yd dev
//
// Inside an application, the facade is the only entry point that matters: | 在应用内部，
// 门面是唯一需要关心的入口：
//
//	application, err := app.Default()
//	if err != nil {
//		return err
//	}
//	application.GET("/avatar/:id.jpg", handler) // mixed segment capture | 段内混合捕获
//	return application.Run(ctx)
//
// # References | 引用
//
// The constants below expose the official identity of the framework so that
// generated code, documentation and tooling can reference it instead of
// hard-coding strings. | 下列常量公开框架的官方身份信息，使生成代码、文档与工具链可以
// 引用它们，而不必硬编码字符串。
package yd

import "runtime"

// Official identity of the framework. | 框架的官方身份信息。
const (
	// ModulePath is the Go module path of this repository. | ModulePath 是本仓库的 Go 模块路径。
	ModulePath = "github.com/shibingli/yd"
	// RepositoryURL is the canonical source repository. | RepositoryURL 是官方源码仓库地址。
	RepositoryURL = "https://github.com/shibingli/yd"
	// CLIPackage is the import path of the command line tool. | CLIPackage 是命令行工具的导入路径。
	CLIPackage = ModulePath + "/cmd/yd"
	// BinaryName is the installed name of the command line tool. | BinaryName 是命令行工具安装后的可执行文件名。
	BinaryName = "yd"
	// License is the SPDX identifier of the project licence. | License 是本项目的 SPDX 许可证标识。
	License = "Apache-2.0"
	// Team is the team that maintains the framework. | Team 是维护本框架的团队。
	Team = "YunDev"
	// TeamURL is the team's official site. | TeamURL 是团队官网。
	TeamURL = "https://yun-dev.cn"
	// EnvPrefix is the prefix every environment variable uses. | EnvPrefix 是所有环境变量使用的前缀。
	EnvPrefix = "YD_"
	// GoVersion is the minimum Go toolchain this module requires. | GoVersion 是本模块要求的最低 Go 工具链版本。
	GoVersion = "1.27.2"
	// HtmxVersion is the Htmx release embedded in the binary. | HtmxVersion 是内嵌进二进制的 Htmx 版本。
	HtmxVersion = "4.0.0"
	// TailwindVersion is the Tailwind CSS release used to build the stylesheet. | TailwindVersion 是构建样式表所用的 Tailwind CSS 版本。
	TailwindVersion = "4.3.3"
	// ServiceLibrary is the service manager integration used for native installs. | ServiceLibrary 是用于原生服务安装的服务管理器集成库。
	ServiceLibrary = "github.com/kardianos/service"
	// ServiceLibraryVersion is the pinned version of ServiceLibrary. | ServiceLibraryVersion 是 ServiceLibrary 锁定的版本。
	ServiceLibraryVersion = "v1.3.0"
)

// BuildInfo describes the toolchain that compiled the running binary. | BuildInfo 描述编译当前二进制的工具链。
type BuildInfo struct {
	// Module is the module path the code was compiled from. | Module 是编译所用代码的模块路径。
	Module string
	// GoVersion is the toolchain version reported by the runtime. | GoVersion 是运行时报告的 Go 版本。
	GoVersion string
	// Platform is "GOOS/GOARCH". | Platform 形如 "GOOS/GOARCH"。
	Platform string
}

// Runtime returns the build information of the running binary. | Runtime 返回当前二进制的构建信息。
//
// Documentation sites and health endpoints can reference this instead of
// duplicating the values. | 文档站点与健康检查端点可以引用它，而无需重复这些取值。
func Runtime() BuildInfo {
	return BuildInfo{
		Module:    ModulePath,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

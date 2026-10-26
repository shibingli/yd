// Package web embeds the yd front-end tree — vendored JavaScript, Tailwind stylesheets and page templates — into the binary. | Package web 把 yd 的前端目录树内嵌进二进制，包括 vendoring 的 JavaScript、Tailwind 样式与页面模板。
//
// Nothing in this tree ever reaches the public network: every script and stylesheet ships inside the binary, | 本目录树不会访问公网：所有脚本与样式都随二进制一同分发，
// so the framework builds and runs fully offline; a CDN reference in a template is a test failure, not a fallback. | 因此框架可完全离线构建与运行；模板里出现 CDN 引用属于测试失败，而不是降级路径。
//
// Layout of the embedded tree (paths always use "/" and satisfy fs.ValidPath): | 内嵌目录树的布局（路径一律以 "/" 分隔并满足 fs.ValidPath）：
//
//	static/js/vendored/   third-party JavaScript locked by assets.lock.json | 由 assets.lock.json 锁定的第三方 JavaScript
//	static/css/         Tailwind v4 entry app.css, the shared components.css and the committed build artifact yd.css | Tailwind v4 入口 app.css、共享组件层 components.css 与签入仓库的编译产物 yd.css
//	templates/          page templates consumed by the render package | 供 render 包使用的页面模板
//	i18n/               built-in message catalogues loaded by i18n.New and injected into the layout | 由 i18n.New 加载并注入布局的内置消息目录
//	assets.lock.json    front-end asset manifest carrying sha256 checksums | 携带 sha256 校验和的前端资产清单
//
// The assets package (Task 27) owns Manifest and its verification helpers; | assets 包（Task 27）负责 Manifest 及其校验辅助函数，
// this package deliberately does not depend on it yet, so that web keeps compiling while assets is still under construction. | 本包刻意尚未依赖它，以便在 assets 仍在开发时 web 始终可编译。
package web

import (
	"embed"

	"github.com/shibingli/ydx/afs"
)

// embedded holds the front-end tree; the "all:" prefix also matches dot-prefixed placeholder files such as templates/.gitkeep. | embedded 保存前端目录树；"all:" 前缀连 templates/.gitkeep 这类点号开头的占位文件一并匹配。
//
// The JavaScript pattern is "*.js", which deliberately does not match the "*.test.mjs" suites:
// tests must never be embedded or served from /static. CSS inputs stay embedded because the
// scaffold copies them into generated projects and the coverage guards read them here. |
// JavaScript 模式是 "*.js"，刻意不匹配 "*.test.mjs" 测试套件：测试既不能进入二进制，也不能从 /static 提供。
// CSS 输入仍保留在内嵌树中，因为脚手架会把它们复制进生成项目，覆盖守卫也从这里读取。
//
//go:embed all:static/css all:static/img all:static/js/vendored
//go:embed static/js/*.js assets.lock.json all:templates
var embedded embed.FS

// FS returns the embedded front-end tree as a read-only file system. | FS 以只读文件系统返回内嵌的前端目录树。
//
// Paths use "/" separators and must satisfy fs.ValidPath, for example "static/css/yd.css" or "templates/layouts/base.html". | 路径使用 "/" 分隔且必须满足 fs.ValidPath，例如 "static/css/yd.css" 或 "templates/layouts/base.html"。
//
// Every write operation returns an error wrapping fs.ErrPermission, so a caller that alternates between the OS
// file system in development and the embedded file system in production keeps one code path. | 任何写操作都会返回包装了 fs.ErrPermission 的错误，
// 因此调用方在开发期用操作系统文件系统、生产期用内嵌文件系统时可以共用同一条代码路径。
func FS() afs.FS { return afs.NewEmbed(embedded) }

package scaffold

import (
	"github.com/shibingli/yd/errx"
)

// Options 描述一次项目生成的全部输入。 | Options describes every input of one project generation.
type Options struct {
	// Name 是项目名，同时用作目标目录名（当前目录生成时可为空由 CLI 决定）。 | Name is the project name and the target directory name.
	Name string
	// Module 是 go.mod 的模块路径；为空时默认 github.com/yun-dev/<Name>。 | Module is the go.mod module path; empty means github.com/yun-dev/<Name>.
	Module string
	// DB 是数据库驱动：mysql、postgres、sqlite 或 none。 | DB is the database driver: mysql, postgres, sqlite or none.
	DB string
	// Redis 表示是否接入 go-redis 缓存。 | Redis enables the go-redis cache integration.
	Redis bool
	// Asynq 表示是否接入 asynq 分布式任务队列。 | Asynq enables the asynq distributed task queue.
	Asynq bool
	// GRPC 表示是否开启 gRPC 双通道。 | GRPC enables the gRPC dual-channel server.
	GRPC bool
	// I18n 表示是否开启国际化。 | I18n enables internationalisation.
	I18n bool
	// Tailwind 表示是否接入 Tailwind v4 样式管道。 | Tailwind enables the Tailwind v4 styling pipeline.
	Tailwind bool
	// HtmxExts 是需要随项目加载的 Htmx 4 扩展（ws、sse、preload、alpine）。 | HtmxExts lists the Htmx 4 extensions to ship with the project (ws, sse, preload, alpine).
	HtmxExts []string
	// License 是开源协议标识，当前仅支持 Apache-2.0。 | License is the license identifier; only Apache-2.0 is supported today.
	License string
	// Force 为 true 时覆盖内容不同的既有文件，否则报错。 | Force overwrites existing files with different content instead of failing.
	Force bool
	// LocalReplace 非空时向 go.mod 写入 replace 指向本地 yd 仓库（开发期联调）。 | A non-empty LocalReplace adds a go.mod replace pointing at a local yd checkout (development only).
	LocalReplace string
}

// DefaultOptions 返回以 name 为项目名的默认选项。 | DefaultOptions returns the default options for the project called name.
//
// 默认值 | Defaults: Module = github.com/yun-dev/<name>、DB = sqlite、Tailwind = true、
// License = Apache-2.0、HtmxExts = {preload, sse, ws}，其余特性开关为 false。 | Module = github.com/yun-dev/<name>, DB = sqlite, Tailwind = true,
// License = Apache-2.0, HtmxExts = {preload, sse, ws} and every other feature switch off.
func DefaultOptions(name string) Options {
	return Options{
		Name:     name,
		Module:   defaultModule(name),
		DB:       DBDefault,
		License:  LicenseApache2,
		Tailwind: true,
		HtmxExts: []string{ExtPreload, ExtSSE, ExtWS},
	}
}

// DBDefault 是未指定数据库驱动时使用的默认值。 | DBDefault is the driver used when none is specified.
const DBDefault = DBSQLite

// 支持的数据库驱动取值。 | Supported database driver values.
const (
	// DBNone 表示不生成任何数据库装配。 | DBNone means no database wiring is generated.
	DBNone = "none"
	// DBSQLite 表示 SQLite 驱动。 | DBSQLite is the SQLite driver.
	DBSQLite = "sqlite"
	// DBMySQL 表示 MySQL 驱动。 | DBMySQL is the MySQL driver.
	DBMySQL = "mysql"
	// DBPostgres 表示 PostgreSQL 驱动。 | DBPostgres is the PostgreSQL driver.
	DBPostgres = "postgres"
)

// 支持的 Htmx 4 扩展注册名。 | Supported Htmx 4 extension registration names.
const (
	// ExtWS 是 WebSocket 双向通信扩展。 | ExtWS is the WebSocket bidirectional extension.
	ExtWS = "ws"
	// ExtSSE 是 SSE 单向实时流扩展。 | ExtSSE is the server-sent events extension.
	ExtSSE = "sse"
	// ExtPreload 是多阶段加载与骨架屏扩展。 | ExtPreload is the multi-stage preload / skeleton extension.
	ExtPreload = "preload"
	// ExtAlpine 是 Alpine.js 兼容扩展。 | ExtAlpine is the Alpine.js compatibility extension.
	ExtAlpine = "alpine"
)

// LicenseApache2 是当前唯一支持的开源协议标识。 | LicenseApache2 is the only supported license identifier.
const LicenseApache2 = "Apache-2.0"

// 模板渲染使用的版本常量，必须与需求基线保持一致。 | Version constants used by the templates, kept in sync with the requirements baseline.
const (
	// GoVersion 是生成项目 go.mod 中的 Go 版本。 | GoVersion is the Go version written into the generated go.mod.
	GoVersion = "1.27.2"
	// HtmxVersion 是前端事件总线的 Htmx 版本。 | HtmxVersion is the Htmx version used as the front-end event bus.
	HtmxVersion = "4.0.0"
	// TailwindVersion 是样式管道的 Tailwind 版本。 | TailwindVersion is the Tailwind version of the styling pipeline.
	TailwindVersion = "v4"
	// YdModule 是框架自身的模块路径。 | YdModule is the module path of the framework itself.
	YdModule = "github.com/shibingli/yd"
	// AssetRoot 是生成项目的前端资产根目录，写入 .yd.json。 | AssetRoot is the front-end asset root written into .yd.json.
	AssetRoot = "web"
)

// 生成过程中的哨兵错误，一律用 errors.Is 判定。 | Sentinel errors of the generation pipeline, always matched with errors.Is.
var (
	// ErrNameRequired 表示项目名为空。 | ErrNameRequired reports an empty project name.
	ErrNameRequired = errx.New("project name is required | 项目名不能为空")
	// ErrNameInvalid 表示项目名含非法字符。 | ErrNameInvalid reports a project name with illegal characters.
	ErrNameInvalid = errx.New("invalid project name | 非法的项目名")
	// ErrModuleInvalid 表示模块路径非法。 | ErrModuleInvalid reports an invalid module path.
	ErrModuleInvalid = errx.New("invalid module path | 非法的模块路径")
	// ErrDBUnsupported 表示数据库驱动不在支持列表内。 | ErrDBUnsupported reports a database driver outside the supported list.
	ErrDBUnsupported = errx.New("unsupported database driver | 不支持的数据库驱动")
	// ErrLicenseUnsupported 表示协议不在支持列表内。 | ErrLicenseUnsupported reports a license outside the supported list.
	ErrLicenseUnsupported = errx.New("unsupported license | 不支持的协议")
	// ErrHtmxExtUnknown 表示 Htmx 扩展名未知。 | ErrHtmxExtUnknown reports an unknown Htmx extension name.
	ErrHtmxExtUnknown = errx.New("unknown htmx extension | 未知的 Htmx 扩展")
	// ErrConflict 表示目标文件已存在且内容不同。 | ErrConflict reports existing target files with different content.
	ErrConflict = errx.New("target file exists with different content | 目标文件已存在且内容不同")
	// ErrCanceled 表示生成过程被取消。 | ErrCanceled reports a cancelled generation run.
	ErrCanceled = errx.New("generation canceled | 生成已被取消")
	// ErrTemplateRead 表示读取内嵌模板失败。 | ErrTemplateRead reports a failed read of an embedded template.
	ErrTemplateRead = errx.New("read template failed | 读取模板失败")
	// ErrTemplateRender 表示模板渲染失败。 | ErrTemplateRender reports a failed template render.
	ErrTemplateRender = errx.New("render template failed | 渲染模板失败")
	// ErrWriteFile 表示写入目标文件失败。 | ErrWriteFile reports a failed write to a target file.
	ErrWriteFile = errx.New("write target file failed | 写入目标文件失败")
	// ErrInputRead 表示读取交互输入失败。 | ErrInputRead reports a failed read of interactive input.
	ErrInputRead = errx.New("read interactive input failed | 读取交互输入失败")
)

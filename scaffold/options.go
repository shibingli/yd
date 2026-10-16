package scaffold

import (
	"github.com/shibingli/yd/errx"
)

// Options describes every input of one project generation. | Options 描述一次项目生成的全部输入。
type Options struct {
	// Name is the project name and the target directory name. | Name 是项目名，同时用作目标目录名（当前目录生成时可为空由 CLI 决定）。
	Name string
	// Module is the go.mod module path; empty means github.com/yun-dev/<Name>. | Module 是 go.mod 的模块路径；为空时默认 github.com/yun-dev/<Name>。
	Module string
	// DB is the database driver: mysql, postgres, sqlite or none. | DB 是数据库驱动：mysql、postgres、sqlite 或 none。
	DB string
	// License is the license identifier; only Apache-2.0 is supported today. | License 是开源协议标识，当前仅支持 Apache-2.0。
	License string
	// A non-empty LocalReplace adds a go.mod replace pointing at a local yd checkout (development only). | LocalReplace 非空时向 go.mod 写入 replace 指向本地 yd 仓库（开发期联调）。
	LocalReplace string
	// HtmxExts lists the Htmx 4 extensions to ship with the project (ws, sse, preload, alpine). | HtmxExts 是需要随项目加载的 Htmx 4 扩展（ws、sse、preload、alpine）。
	HtmxExts []string
	// Redis enables the go-redis cache integration. | Redis 表示是否接入 go-redis 缓存。
	Redis bool
	// Asynq enables the asynq distributed task queue. | Asynq 表示是否接入 asynq 分布式任务队列。
	Asynq bool
	// GRPC enables the gRPC dual-channel server. | GRPC 表示是否开启 gRPC 双通道。
	GRPC bool
	// I18n enables internationalisation. | I18n 表示是否开启国际化。
	I18n bool
	// Tailwind enables the Tailwind v4 styling pipeline. | Tailwind 表示是否接入 Tailwind v4 样式管道。
	Tailwind bool
	// Force overwrites existing files with different content instead of failing. | Force 为 true 时覆盖内容不同的既有文件，否则报错。
	Force bool
}

// DefaultOptions returns the default options for the project called name. | DefaultOptions 返回以 name 为项目名的默认选项。
//
// Defaults: Module = github.com/yun-dev/<name>、DB = sqlite、Tailwind = true、 | 默认值
// Module = github.com/yun-dev/<name>, DB = sqlite, Tailwind = true, | License = Apache-2.0、HtmxExts = {preload, sse, ws}，其余特性开关为 false。
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

// DBDefault is the driver used when none is specified. | DBDefault 是未指定数据库驱动时使用的默认值。
const DBDefault = DBSQLite

// Supported database driver values. | 支持的数据库驱动取值。
const (
	// DBNone means no database wiring is generated. | DBNone 表示不生成任何数据库装配。
	DBNone = "none"
	// DBSQLite is the SQLite driver. | DBSQLite 表示 SQLite 驱动。
	DBSQLite = "sqlite"
	// DBMySQL is the MySQL driver. | DBMySQL 表示 MySQL 驱动。
	DBMySQL = "mysql"
	// DBPostgres is the PostgreSQL driver. | DBPostgres 表示 PostgreSQL 驱动。
	DBPostgres = "postgres"
)

// Supported Htmx 4 extension registration names. | 支持的 Htmx 4 扩展注册名。
const (
	// ExtWS is the WebSocket bidirectional extension. | ExtWS 是 WebSocket 双向通信扩展。
	ExtWS = "ws"
	// ExtSSE is the server-sent events extension. | ExtSSE 是 SSE 单向实时流扩展。
	ExtSSE = "sse"
	// ExtPreload is the multi-stage preload / skeleton extension. | ExtPreload 是多阶段加载与骨架屏扩展。
	ExtPreload = "preload"
	// ExtAlpine is the Alpine.js compatibility extension. | ExtAlpine 是 Alpine.js 兼容扩展。
	ExtAlpine = "alpine"
)

// LicenseApache2 is the only supported license identifier. | LicenseApache2 是当前唯一支持的开源协议标识。
const LicenseApache2 = "Apache-2.0"

// Version constants used by the templates, kept in sync with the requirements baseline. | 模板渲染使用的版本常量，必须与需求基线保持一致。
const (
	// GoVersion is the Go version written into the generated go.mod. | GoVersion 是生成项目 go.mod 中的 Go 版本。
	GoVersion = "1.27.2"
	// HtmxVersion is the Htmx version used as the front-end event bus. | HtmxVersion 是前端事件总线的 Htmx 版本。
	HtmxVersion = "4.0.0"
	// TailwindVersion is the Tailwind version of the styling pipeline. | TailwindVersion 是样式管道的 Tailwind 版本。
	TailwindVersion = "v4"
	// YdModule is the module path of the framework itself. | YdModule 是框架自身的模块路径。
	YdModule = "github.com/shibingli/yd"
	// AssetRoot is the front-end asset root written into .yd.json. | AssetRoot 是生成项目的前端资产根目录，写入 .yd.json。
	AssetRoot = "web"
)

// Sentinel errors of the generation pipeline, always matched with errors.Is. | 生成过程中的哨兵错误，一律用 errors.Is 判定。
var (
	// ErrNameRequired reports an empty project name. | ErrNameRequired 表示项目名为空。
	ErrNameRequired = errx.New("project name is required | 项目名不能为空")
	// ErrNameInvalid reports a project name with illegal characters. | ErrNameInvalid 表示项目名含非法字符。
	ErrNameInvalid = errx.New("invalid project name | 非法的项目名")
	// ErrModuleInvalid reports an invalid module path. | ErrModuleInvalid 表示模块路径非法。
	ErrModuleInvalid = errx.New("invalid module path | 非法的模块路径")
	// ErrDBUnsupported reports a database driver outside the supported list. | ErrDBUnsupported 表示数据库驱动不在支持列表内。
	ErrDBUnsupported = errx.New("unsupported database driver | 不支持的数据库驱动")
	// ErrLicenseUnsupported reports a license outside the supported list. | ErrLicenseUnsupported 表示协议不在支持列表内。
	ErrLicenseUnsupported = errx.New("unsupported license | 不支持的协议")
	// ErrHtmxExtUnknown reports an unknown Htmx extension name. | ErrHtmxExtUnknown 表示 Htmx 扩展名未知。
	ErrHtmxExtUnknown = errx.New("unknown htmx extension | 未知的 Htmx 扩展")
	// ErrConflict reports existing target files with different content. | ErrConflict 表示目标文件已存在且内容不同。
	ErrConflict = errx.New("target file exists with different content | 目标文件已存在且内容不同")
	// ErrCanceled reports a cancelled generation run. | ErrCanceled 表示生成过程被取消。
	ErrCanceled = errx.New("generation canceled | 生成已被取消")
	// ErrTemplateRead reports a failed read of an embedded template. | ErrTemplateRead 表示读取内嵌模板失败。
	ErrTemplateRead = errx.New("read template failed | 读取模板失败")
	// ErrTemplateRender reports a failed template render. | ErrTemplateRender 表示模板渲染失败。
	ErrTemplateRender = errx.New("render template failed | 渲染模板失败")
	// ErrWriteFile reports a failed write to a target file. | ErrWriteFile 表示写入目标文件失败。
	ErrWriteFile = errx.New("write target file failed | 写入目标文件失败")
	// ErrInputRead reports a failed read of interactive input. | ErrInputRead 表示读取交互输入失败。
	ErrInputRead = errx.New("read interactive input failed | 读取交互输入失败")
)

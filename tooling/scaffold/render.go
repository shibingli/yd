package scaffold

import (
	"bytes"
	"embed"
	"go/format"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/shibingli/yd/tooling/icons"
	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/gorm"
	"github.com/shibingli/ydx/regexpx"
	"github.com/spf13/afero"
)

// templateFS 内嵌 tooling/scaffold/templates 下的全部模板。
// templateFS embeds every template under tooling/scaffold/templates.
//
// 必须使用 all: 前缀：模板目录里既有 _shared/_name_ 这类下划线开头的目录，
// 也有 .gitignore/.gitattributes 这类点号开头的文件，默认的 embed 规则会跳过它们。
// The all: prefix is mandatory: the tree contains underscore-prefixed directories
// (_shared/_name_) as well as dot-files (.gitignore/.gitattributes) that the default rules skip.
//
//go:embed all:templates
var templateFS embed.FS

// The template root and its two special sub-directories. | 模板根目录与两个特殊子目录。
const (
	// templateRoot is the root path of the embedded templates. | templateRoot 是内嵌模板的根路径。
	templateRoot = "templates"
	// sharedDir holds verbatim (non-rendered) files: LICENSE, .gitignore, .gitattributes and the asset catalogue. | sharedDir 存放原样复制（不渲染）的文件：LICENSE、.gitignore、.gitattributes 与资产目录。
	sharedDir = "_shared"
	// projectDir holds rendered files; its `_name_` path placeholder becomes the project name. | projectDir 存放渲染后写入项目根的文件，其路径中的 _name_ 占位符替换为项目名。
	projectDir = "_name_"
	// tmplSuffix 是脚手架模板文件后缀：带此后缀的文件由 text/template 在生成期渲染，
	// 后缀本身从输出路径中剔除；不带此后缀的文件逐字节原样落到生成项目里。
	// tmplSuffix is the scaffold template suffix: a file carrying it is rendered by text/template at
	// generation time and loses the suffix in its output path, while a file without it is copied
	// byte for byte into the generated project.
	tmplSuffix = ".tmpl"
	// dotPrefix maps a dot-x template name onto the hidden file .x in the output. | dotPrefix 把 dot-x 形式的模板名映射为生成物里的隐藏文件 .x。
	dotPrefix = "dot-"
	// assetCatalogName is the asset manifest name inside a generated project: a copy of the framework's web/assets.lock.json trimmed to the selected extensions. | assetCatalogName 是生成项目里的资产清单名：框架 web/assets.lock.json 经扩展裁剪后的副本。
	assetCatalogName = "assets.lock.json"
)

// namePattern 约束项目名（同时用作目录名）：字母开头，后接字母、数字、点、下划线或中划线。
// namePattern constrains the project name (also the directory name): a letter followed by letters, digits, dots, underscores or dashes.
var namePattern = regexpx.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)

// modulePattern constrains the module path: at least two segments separated by "/". | modulePattern 约束模块路径：至少两段，段间用 "/" 分隔。
var modulePattern = regexpx.MustCompile(`^[a-z0-9][a-z0-9._~-]*(\.[a-z0-9._~-]+)?(/[A-Za-z0-9._~-]+)+$`)

// plannedFile is a rendered target file that has not been written yet. | plannedFile 是渲染完成、尚未落盘的目标文件。
type plannedFile struct {
	// path is the project-relative, "/"-separated path. | path 是项目相对路径，"/" 分隔。
	path string
	// content is the exact byte payload to write. | content 是最终写出的字节。
	content []byte
}

// view is the template context: a derived view of Options. | view 是模板渲染上下文：Options 的派生化视图。
type view struct {
	// project metadata | 项目元信息
	Name         string
	Module       string
	DB           string
	License      string
	LocalReplace string
	// LocalReplaceQuoted is LocalReplace as a Go string literal, so a path with spaces cannot
	// break (or inject into) the go.mod replace directive. | LocalReplaceQuoted 是 LocalReplace
	// 的 Go 字符串字面量，使含空格的路径无法破坏（或注入）go.mod 的 replace 指令。
	LocalReplaceQuoted string
	AssetRoot          string
	// YdVersion pins the framework module in the generated go.mod; empty means none. | YdVersion 在生成的 go.mod 中锁定框架模块；为空表示不锁定。
	YdVersion string
	// YdxVersions pins each ydx module at its own tag in the generated go.mod; modules absent
	// from the map are left to go mod tidy. | YdxVersions 在生成的 go.mod 中按各自标签锁定 ydx
	// 模块；映射中缺失的模块交给 go mod tidy 解析。
	YdxVersions map[string]string
	// YdxModules lists the ydx modules required by the generated project. | YdxModules 列出生成项目依赖的 ydx 模块。
	YdxModules []string
	// YdxReplaces lists the local ydx replacements used in development. | YdxReplaces 列出开发期使用的 ydx 本地替换。
	YdxReplaces []ydxReplace

	// versions and framework constants | 版本与框架常量
	GoVersion       string
	GoMajorMinor    string
	HtmxVersion     string
	TailwindVersion string
	YdModule        string
	YdxModule       string

	// derived fields | 派生字段
	DBDSN    string
	HtmxExts []string

	Features       []string
	HtmxExtScripts []string
	// BundleScripts is the ordered shared-script list baked into web/bundle_generated.go. |
	// BundleScripts 是写入 web/bundle_generated.go 的共享脚本顺序。
	BundleScripts []string

	// feature switches | 特性开关
	GRPC     bool
	Auth     bool
	Captcha  bool
	I18n     bool
	Tailwind bool
}

// newView builds the template context from normalised options. | newView 由规范化后的选项构造模板上下文。
func newView(o Options) view {
	return view{
		Name:         o.Name,
		Module:       o.Module,
		DB:           o.DB,
		License:      o.License,
		LocalReplace: o.LocalReplace,
		LocalReplaceQuoted: func() string {
			if o.LocalReplace == "" {
				return ""
			}
			// Quote only when the path cannot be represented as a bare modfile token, so the
			// common output stays byte-identical to earlier releases while spaces and quotes are
			// handled safely. A doubled slash matters as much as a space: the modfile lexer
			// treats "//" as the start of a comment, so a UNC path (\\server\share, normalized to
			// //server/share) or a path merely containing "//" would otherwise swallow the
			// replacement target. | 仅在路径无法作为裸 modfile 记号时加引号，使常见输出与旧版本
			// 逐字节一致，同时安全处理空格与引号。双斜杠与空格同样重要：modfile 词法器把 "//" 当作
			// 注释起点，否则 UNC 路径（\\server\share，规范化为 //server/share）或仅包含 "//" 的路径
			// 会把替换目标整段吞掉。
			if strings.ContainsAny(o.LocalReplace, " \t\"'`") ||
				strings.Contains(o.LocalReplace, "//") || strings.Contains(o.LocalReplace, "/*") {
				return strconv.Quote(o.LocalReplace)
			}
			return o.LocalReplace
		}(),
		AssetRoot:       AssetRoot,
		YdVersion:       o.YdVersion,
		YdxVersions:     YdxVersions,
		YdxModules:      YdxModules,
		YdxReplaces:     ydxReplaces(o.LocalReplace),
		GRPC:            o.GRPC,
		Auth:            true,
		Captcha:         o.Captcha,
		I18n:            o.I18n,
		Tailwind:        o.Tailwind,
		HtmxExts:        o.HtmxExts,
		GoVersion:       GoVersion,
		GoMajorMinor:    goMajorMinor(GoVersion),
		HtmxVersion:     HtmxVersion,
		TailwindVersion: TailwindVersion,
		YdModule:        YdModule,
		YdxModule:       YdxModule,
		DBDSN:           dsnFor(o.DB, o.Name),
		Features:        featuresOf(o),
		HtmxExtScripts:  extScripts(o.HtmxExts),
		BundleScripts:   bundleScriptsFor(o.HtmxExts),
	}
}

// bundleScriptsFor returns the shared scripts in the exact order base.html loads them. |
// bundleScriptsFor 按 base.html 的装载顺序返回共享脚本。
func bundleScriptsFor(exts []string) []string {
	scripts := make([]string, 0, 4+len(exts)+8)
	scripts = append(scripts, "/static/js/vendored/htmx.min.js")
	scripts = append(scripts, extScripts(exts)...)
	scripts = append(scripts,
		"/static/js/yd-ui.js",
		"/static/js/vendored/alpine.min.js",
		"/static/js/yd-guomi.js",
		"/static/js/yd-guomi-htmx.js",
		"/static/js/yd-authz.js",
		"/static/js/yd-progress.js",
		"/static/js/yd-captcha.js",
		"/static/js/yd-app.js",
	)
	return scripts
}

// ydxReplace is one local ydx replacement in a generated go.mod. | ydxReplace 是生成 go.mod 中的一条 ydx 本地替换。
type ydxReplace struct {
	// Module is the ydx module path. | Module 是 ydx 模块路径。
	Module string
	// PathQuoted is the local filesystem path as a safe go.mod token. | PathQuoted 是作为安全 go.mod 记号的本地路径。
	PathQuoted string
}

// ydxReplaces derives the sibling ydx checkout from the local yd checkout. | ydxReplaces 从本地 yd 检出推导同级的 ydx 检出。
func ydxReplaces(localReplace string) []ydxReplace {
	if localReplace == "" || len(YdxModules) == 0 {
		return nil
	}
	root := filepath.ToSlash(filepath.Join(filepath.Dir(localReplace), "ydx"))
	out := make([]ydxReplace, 0, len(YdxModules))
	for _, module := range YdxModules {
		relative := strings.TrimPrefix(module, YdxModule+"/")
		out = append(out, ydxReplace{Module: module, PathQuoted: quoteModPath(root + "/" + relative)})
	}
	return out
}

// quoteModPath quotes a path only when the go.mod lexer would misread it. | quoteModPath 仅在 go.mod
// 词法器会误读路径时加引号。
func quoteModPath(value string) string {
	if strings.ContainsAny(value, " \t\"'`") || strings.Contains(value, "//") || strings.Contains(value, "/*") {
		return strconv.Quote(value)
	}
	return value
}

// featuresOf 汇总已选特性，供模板与启动日志共用（与 internal/router 的 Features 变量同源）。
// featuresOf summarises the selected features for templates and startup logs (same source as the Features variable in internal/router).
func featuresOf(o Options) []string {
	features := []string{"db:" + o.DB}
	// Redis 与 asynq 不是开关：生成项目一律带它们的配置、消息总线与队列接线。
	// Redis and asynq are not switches: every generated project ships their configuration, message bus and queue wiring.
	features = append(features, "redis", "asynq")
	if o.GRPC {
		features = append(features, "grpc")
	}
	// 鉴权不随选项开关：生成项目一律带多租户登录链路，因此这里恒定登记一次。
	// Authentication is not an option: a generated project always ships the multi-tenant sign-in chain, so it is recorded
	// here unconditionally.
	features = append(features, "auth")
	if o.Captcha {
		features = append(features, "captcha")
	}
	if o.I18n {
		features = append(features, "i18n")
	}
	if o.Tailwind {
		features = append(features, "tailwind")
	}
	if len(o.HtmxExts) > 0 {
		features = append(features, "htmx:"+strings.Join(o.HtmxExts, "+"))
	}
	return features
}

// extScripts 返回所选 Htmx 扩展对应的本地脚本路径（顺序与选项一致）。
// extScripts returns the local script paths of the selected Htmx extensions, in the order given by the options.
func extScripts(exts []string) []string {
	scripts := make([]string, 0, len(exts))
	for _, ext := range exts {
		if p, ok := extScript[ext]; ok {
			scripts = append(scripts, p)
		}
	}
	return scripts
}

// extScript maps extension names onto local script paths (CDN references are forbidden). | extScript 是扩展注册名到本地脚本路径的映射（严禁 CDN 引用）。
var extScript = map[string]string{
	ExtWS:               "/static/js/vendored/hx-ws.min.js",
	ExtSSE:              "/static/js/vendored/hx-sse.min.js",
	ExtPreload:          "/static/js/vendored/hx-preload.min.js",
	ExtAlpine:           "/static/js/vendored/hx-alpine-compat.min.js",
	ExtTargets:          "/static/js/vendored/hx-targets.min.js",
	ExtHead:             "/static/js/vendored/hx-head.min.js",
	ExtUpsert:           "/static/js/vendored/hx-upsert.min.js",
	ExtPending:          "/static/js/vendored/hx-pending.min.js",
	ExtMultipart:        "/static/js/vendored/hx-multipart.min.js",
	ExtDownload:         "/static/js/vendored/hx-download.min.js",
	ExtBrowserIndicator: "/static/js/vendored/hx-browser-indicator.min.js",
	ExtHistoryCache:     "/static/js/vendored/hx-history-cache.min.js",
	ExtLive:             "/static/js/vendored/hx-live.min.js",
	ExtPrompt:           "/static/js/vendored/hx-prompt.min.js",
	ExtPTag:             "/static/js/vendored/hx-ptag.min.js",
	ExtCSP:              "/static/js/vendored/hx-csp.min.js",
	ExtCompat:           "/static/js/vendored/hx-compat.min.js",
	ExtHtmx2Compat:      "/static/js/vendored/htmx-2-compat.min.js",
}

// extNames is the canonical order of valid extensions, used for validation and error hints. | extNames 是合法扩展名的固定顺序，用于校验与错误提示。
var extNames = []string{
	ExtWS, ExtSSE, ExtPreload, ExtAlpine,
	ExtTargets, ExtHead, ExtUpsert, ExtPending, ExtMultipart, ExtDownload,
	ExtBrowserIndicator, ExtHistoryCache, ExtLive, ExtPrompt, ExtPTag,
	ExtCSP, ExtCompat, ExtHtmx2Compat,
}

// Extensions returns the supported Htmx 4 extension names in canonical order. | Extensions 按固定顺序返回支持的 Htmx 4 扩展名。
//
// The returned slice is a copy: callers may reorder or truncate it without affecting validation. | 返回的是副本：
// 调用方可以重排或截断它，而不会影响校验。
func Extensions() []string { return append([]string(nil), extNames...) }

// DefaultExtensions returns every extension a generated project loads without being asked. | DefaultExtensions
// 返回生成项目无需询问就加载的全部扩展。
//
// The three policy / compatibility layers stay opt-in: csp needs a site nonce, while compat and
// htmx-2-compat restore htmx 2 semantics and would undo the 4.x behaviour the scaffold is built
// around. Every other extension is part of the baseline, so `yd new` never asks a visitor to pick
// a subset. | 三个策略/兼容层保持显式选择：csp 需要站点 nonce，compat 与 htmx-2-compat 恢复
// htmx 2 语义，会抵消脚手架所基于的 4.x 行为。其余扩展都属于基线，因此 `yd new` 不再询问访客挑选子集。
func DefaultExtensions() []string {
	out := make([]string, 0, len(extNames))
	for _, ext := range extNames {
		switch ext {
		case ExtCSP, ExtCompat, ExtHtmx2Compat:
			continue
		}
		out = append(out, ext)
	}
	return out
}

// dsnFor builds a sample DSN per driver (the address and credentials only fit local development). | dsnFor 依据数据库驱动生成示例 DSN（仅地址与凭据是本地开发用的默认值）。
//
// 时区、时间配置、空值严格性与默认字符集**不在这里写死**，而是交给 gorm.PresetDSN 补齐——
// 预置表只有一份（框架那份），生成的配置里看到的就是连接时真正生效的形态。
// 显式写出的参数优先于预置值，因此这里只保留**必须由本层决定**的那一个：
// The time zone, time format, null strictness and default character set are not spelled out here: they come from
// gorm.PresetDSN, so the preset table exists exactly once (the framework's) and the generated configuration shows the
// very parameters that will take effect. Explicit parameters win over preset values, so the only one kept here is the
// one this layer has to decide:
//
//   - SQLite 的 `_txlock=immediate` 让写事务在**开始**时就取得写锁。先读后写的延迟事务会被并发提交顶掉快照，
//     随后以 SQLITE_BUSY_SNAPSHOT 失败，而这是任何 busy timeout 都无法化解的——存储层的预检正是「先读后写」。
//     预置表刻意不含它，也正是为了让驱动不去跳过它的解析（见 ydx/gorm 的说明）。
//     SQLite's `_txlock=immediate` takes the write lock when a write transaction *begins*. A deferred transaction that
//     reads first and writes later loses its snapshot to a concurrent commit and then fails with SQLITE_BUSY_SNAPSHOT,
//     which no busy timeout can resolve — and the storage layer's pre-checks read before they write. The preset table
//     deliberately omits it, and deliberately omits the key that would make the driver skip parsing it (see ydx/gorm).
func dsnFor(db, name string) string {
	switch db {
	case DBSQLite:
		return gorm.PresetDSN(DBSQLite, "file:data/"+name+".db?_txlock=immediate")
	case DBMySQL:
		return gorm.PresetDSN(DBMySQL, "root:root@tcp(127.0.0.1:3306)/"+name)
	case DBPostgres:
		return gorm.PresetDSN(DBPostgres, "host=127.0.0.1 user=postgres password=postgres dbname="+name+" port=5432 sslmode=disable")
	case DBDM:
		// DM connects to the instance and selects a schema (default: the user's own). The sample
		// therefore names no schema; a DBA creates the project schema and the caller adds
		// ?schema=NAME. | DM 连接的是实例，schema 只是命名空间（默认取用户自身的模式）。示例
		// 因此不点名 schema；项目模式由 DBA 创建，调用方按需追加 ?schema=NAME。
		return gorm.PresetDSN(DBDM, "dm://SYSDBA:SYSDBA@127.0.0.1:5236")
	default:
		return ""
	}
}

// projectPrefix 返回项目在目标文件系统中的目录前缀（目标 FS 是工程父目录）。
// projectPrefix returns the project's directory prefix inside the target filesystem (the target FS is the parent directory).
func projectPrefix(name string) string {
	return name + "/"
}

// outputPath maps a template path onto an output path inside the target filesystem. | outputPath 把模板路径映射为目标文件系统上的输出路径。
//
// Rules: | 规则
//   - the `_name_` segment becomes the project name, so every artefact lands under <name>/; | 段名 `_name_` 替换为项目名，因此全部产出都落在 <name>/ 之下；
//   - the trailing .tmpl suffix is dropped; | 末段的 .tmpl 后缀被剔除；
//   - a leading dot- maps onto the hidden-file dot (dot-env.template → .env.template). | 末段的 dot- 前缀映射为隐藏文件前缀 "."（如 dot-env.template → .env.template）。
//
// 无 .tmpl 后缀的文件同样只做路径映射：它们逐字节复制，不参与生成期渲染。
// A file without the .tmpl suffix only gets the same path mapping: it is copied byte for byte and
// never rendered at generation time.
func outputPath(tmplPath, name string) string {
	rel := strings.TrimPrefix(tmplPath, templateRoot+"/")
	segments := strings.Split(rel, "/")
	for i, seg := range segments {
		if seg == projectDir {
			segments[i] = name
		}
	}

	last := len(segments) - 1
	base := strings.TrimSuffix(segments[last], tmplSuffix)
	if strings.HasPrefix(base, dotPrefix) {
		base = "." + strings.TrimPrefix(base, dotPrefix)
	}
	segments[last] = base
	return path.Join(segments...)
}

// plan 渲染全部模板并返回按路径排序的文件清单，不触碰目标文件系统。
// plan renders every template and returns the file list sorted by path, without touching the target filesystem.
func plan(o Options) ([]plannedFile, error) {
	v := newView(o)
	files := make([]plannedFile, 0, 32)

	src := afs.NewEmbed(templateFS)
	err := afs.Walk(src, templateRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return errs.WrapDetail(err, ErrTemplateRead, "path=%[1]s | 路径=%[1]s", p)
		}
		if info.IsDir() {
			return nil
		}

		data, err := afero.ReadFile(src, p)
		if err != nil {
			return errs.WrapDetail(err, ErrTemplateRead, "path=%[1]s | 路径=%[1]s", p)
		}

		rel := strings.TrimPrefix(p, templateRoot+"/")
		if strings.HasPrefix(rel, sharedDir+"/") {
			// Verbatim copy (LICENSE and other fixed files stay byte-identical). | 原样复制（LICENSE、.gitignore、.gitattributes 等固定文件，逐字节保持一致）。
			files = append(files, plannedFile{path: projectPrefix(o.Name) + path.Base(rel), content: data})
			return nil
		}

		// 不带 .tmpl 后缀的文件原样复制：生成项目的运行期模板（html/template）、
		// 样式与脚本里的 {{ }} 属于运行时动作而非生成时动作，必须逐字节保留。
		// A file without the .tmpl suffix is copied verbatim: the {{ }} actions inside the generated
		// project's runtime templates (html/template), styles and scripts are runtime — not
		// generation-time — syntax and must survive byte for byte.
		if !strings.HasSuffix(rel, tmplSuffix) {
			files = append(files, plannedFile{path: outputPath(p, o.Name), content: data})
			return nil
		}

		rendered, err := render(data, p, v)
		if err != nil {
			return err
		}
		out := outputPath(p, o.Name)
		if strings.HasSuffix(out, ".go") {
			formatted, formatErr := format.Source(rendered)
			if formatErr != nil {
				return errs.WrapDetail(formatErr, ErrFormat, "path=%[1]s | 路径=%[1]s", out)
			}
			rendered = formatted
		}
		files = append(files, plannedFile{path: out, content: rendered})
		return nil
	})
	if err != nil {
		return nil, err
	}

	manifest, err := frameworkManifest()
	if err != nil {
		return nil, err
	}
	extras, err := generatedDocs(o, v, manifest)
	if err != nil {
		return nil, err
	}
	files = append(files, extras...)

	sprite, err := iconSprite(o.Name, files)
	if err != nil {
		return nil, err
	}
	files = append(files, plannedFile{
		path:    projectPrefix(o.Name) + path.Join("web", "static", "img", "icons.svg"),
		content: sprite,
	})

	sortFiles(files)
	return files, nil
}

// iconSprite builds the project-local icon sprite from the rendered templates. | iconSprite 依据渲染后的模板构建项目本地图标 sprite。
//
// The project must pass `yd icons check` right after generation, and the sprite must be derived from the
// templates that were actually rendered (conditional templates are excluded) rather than from the whole
// embedded template tree. A MemFS holding only the planned templates gives exactly that view, and keeps
// generation atomic: the sprite is just one more planned file, written with the rest. | 生成项目必须开箱通过
// `yd icons check`；sprite 必须来自实际渲染出的模板（被条件排除的模板不参与），而不是整棵内嵌模板树。
// 只装入计划文件的 MemFS 正好提供这个视图，同时保持生成原子性：sprite 只是又一个与其他文件一起写入的计划文件。
func iconSprite(name string, files []plannedFile) ([]byte, error) {
	prefix := projectPrefix(name) + "web/templates/"
	fsys := afs.NewMem()
	found := false
	for _, f := range files {
		if !strings.HasPrefix(f.path, prefix) {
			continue
		}
		rel := strings.TrimPrefix(f.path, projectPrefix(name))
		if dir := path.Dir(rel); dir != "." {
			if err := fsys.MkdirAll(dir, 0o755); err != nil {
				return nil, errs.WrapDetail(err, ErrIconSprite, "mkdir=%[1]s | 目录=%[1]s", dir)
			}
		}
		if err := afero.WriteFile(fsys, rel, f.content, 0o644); err != nil {
			return nil, errs.WrapDetail(err, ErrIconSprite, "path=%[1]s | 路径=%[1]s", rel)
		}
		found = true
	}
	if !found {
		return nil, errs.Detail(ErrIconSprite, "no web/templates planned | 没有计划写入 web/templates")
	}

	refs, err := icons.Scan(fsys, []string{"web/templates"})
	if err != nil {
		return nil, errs.Wrap(err, ErrIconSprite)
	}
	set, err := icons.Lucide()
	if err != nil {
		return nil, errs.Wrap(err, ErrIconSprite)
	}
	sprite, err := icons.Build(set, refs)
	if err != nil {
		return nil, errs.Wrap(err, ErrIconSprite)
	}
	return sprite, nil
}

// render renders one template with text/template. | render 用 text/template 渲染单个模板。
func render(data []byte, name string, v view) ([]byte, error) {
	tmpl, err := template.New(name).Parse(string(data))
	if err != nil {
		return nil, errs.WrapDetail(err, ErrTemplateRender, "path=%[1]s | 路径=%[1]s", name)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, v); err != nil {
		return nil, errs.WrapDetail(err, ErrTemplateRender, "path=%[1]s | 路径=%[1]s", name)
	}
	return buf.Bytes(), nil
}

// sortFiles sorts by path so the output order stays stable and idempotent. | sortFiles 按路径字典序稳定排序，保证输出顺序与幂等性。
func sortFiles(files []plannedFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
}

// goMajorMinor trims the patch number for Docker image tags: 1.27.3 becomes 1.27. | goMajorMinor 去掉补丁号以用于 Docker 镜像标签：1.27.3 变成 1.27。
//
// The golang images are published with major.minor tags, so deriving this value from the same
// GoVersion the generated go.mod uses keeps the builder image from drifting behind the language
// version. | golang 镜像按大版本.次版本发布，因此从生成 go.mod 所用的同一个 GoVersion 派生该值，
// 使构建镜像不会落后于语言版本。
func goMajorMinor(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return v
	}
	return parts[0] + "." + parts[1]
}

package scaffold

import (
	"bytes"
	"embed"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/data/gormx"
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
var namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)

// modulePattern constrains the module path: at least two segments separated by "/". | modulePattern 约束模块路径：至少两段，段间用 "/" 分隔。
var modulePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._~-]*(\.[a-z0-9._~-]+)?(/[A-Za-z0-9._~-]+)+$`)

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
	AssetRoot    string

	// versions and framework constants | 版本与框架常量
	GoVersion       string
	HtmxVersion     string
	TailwindVersion string
	YdModule        string

	// derived fields | 派生字段
	DBDSN    string
	HtmxExts []string

	Features       []string
	HtmxExtScripts []string

	// feature switches | 特性开关
	Redis    bool
	Asynq    bool
	GRPC     bool
	Auth     bool
	I18n     bool
	Tailwind bool
}

// newView builds the template context from normalised options. | newView 由规范化后的选项构造模板上下文。
func newView(o Options) view {
	return view{
		Name:            o.Name,
		Module:          o.Module,
		DB:              o.DB,
		License:         o.License,
		LocalReplace:    o.LocalReplace,
		AssetRoot:       AssetRoot,
		Redis:           o.Redis,
		Asynq:           o.Asynq,
		GRPC:            o.GRPC,
		Auth:            true,
		I18n:            o.I18n,
		Tailwind:        o.Tailwind,
		HtmxExts:        o.HtmxExts,
		GoVersion:       GoVersion,
		HtmxVersion:     HtmxVersion,
		TailwindVersion: TailwindVersion,
		YdModule:        YdModule,
		DBDSN:           dsnFor(o.DB, o.Name),
		Features:        featuresOf(o),
		HtmxExtScripts:  extScripts(o.HtmxExts),
	}
}

// featuresOf 汇总已选特性，供模板与启动日志共用（与 internal/router 的 Features 变量同源）。
// featuresOf summarises the selected features for templates and startup logs (same source as the Features variable in internal/router).
func featuresOf(o Options) []string {
	features := []string{"db:" + o.DB}
	if o.Redis {
		features = append(features, "redis")
	}
	if o.Asynq {
		features = append(features, "asynq")
	}
	if o.GRPC {
		features = append(features, "grpc")
	}
	// 鉴权不随选项开关：生成项目一律带多租户登录链路，因此这里恒定登记一次。
	// Authentication is not an option: a generated project always ships the multi-tenant sign-in chain, so it is recorded
	// here unconditionally.
	features = append(features, "auth")
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
	ExtWS:      "/static/js/vendor/hx-ws.min.js",
	ExtSSE:     "/static/js/vendor/hx-sse.min.js",
	ExtPreload: "/static/js/vendor/hx-preload.min.js",
	ExtAlpine:  "/static/js/vendor/hx-alpine-compat.min.js",
}

// extNames is the canonical order of valid extensions, used for validation and error hints. | extNames 是合法扩展名的固定顺序，用于校验与错误提示。
var extNames = []string{ExtWS, ExtSSE, ExtPreload, ExtAlpine}

// dsnFor builds a sample DSN per driver (the address and credentials only fit local development). | dsnFor 依据数据库驱动生成示例 DSN（仅地址与凭据是本地开发用的默认值）。
//
// 时区、时间配置、空值严格性与默认字符集**不在这里写死**，而是交给 gormx.PresetDSN 补齐——
// 预置表只有一份（框架那份），生成的配置里看到的就是连接时真正生效的形态。
// 显式写出的参数优先于预置值，因此这里只保留**必须由本层决定**的那一个：
// The time zone, time format, null strictness and default character set are not spelled out here: they come from
// gormx.PresetDSN, so the preset table exists exactly once (the framework's) and the generated configuration shows the
// very parameters that will take effect. Explicit parameters win over preset values, so the only one kept here is the
// one this layer has to decide:
//
//   - SQLite 的 `_txlock=immediate` 让写事务在**开始**时就取得写锁。先读后写的延迟事务会被并发提交顶掉快照，
//     随后以 SQLITE_BUSY_SNAPSHOT 失败，而这是任何 busy timeout 都无法化解的——存储层的预检正是「先读后写」。
//     预置表刻意不含它，也正是为了让驱动不去跳过它的解析（见 gormx 的说明）。
//     SQLite's `_txlock=immediate` takes the write lock when a write transaction *begins*. A deferred transaction that
//     reads first and writes later loses its snapshot to a concurrent commit and then fails with SQLITE_BUSY_SNAPSHOT,
//     which no busy timeout can resolve — and the storage layer's pre-checks read before they write. The preset table
//     deliberately omits it, and deliberately omits the key that would make the driver skip parsing it (see gormx).
func dsnFor(db, name string) string {
	switch db {
	case DBSQLite:
		return gormx.PresetDSN(DBSQLite, "file:data/"+name+".db?_txlock=immediate")
	case DBMySQL:
		return gormx.PresetDSN(DBMySQL, "root:root@tcp(127.0.0.1:3306)/"+name)
	case DBPostgres:
		return gormx.PresetDSN(DBPostgres, "host=127.0.0.1 user=postgres password=postgres dbname="+name+" port=5432 sslmode=disable")
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
			return errx.WrapDetail(err, ErrTemplateRead, "path=%s", p)
		}
		if info.IsDir() {
			return nil
		}

		data, err := afero.ReadFile(src, p)
		if err != nil {
			return errx.WrapDetail(err, ErrTemplateRead, "path=%s", p)
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
		files = append(files, plannedFile{path: outputPath(p, o.Name), content: rendered})
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

	sortFiles(files)
	return files, nil
}

// render renders one template with text/template. | render 用 text/template 渲染单个模板。
func render(data []byte, name string, v view) ([]byte, error) {
	tmpl, err := template.New(name).Parse(string(data))
	if err != nil {
		return nil, errx.WrapDetail(err, ErrTemplateRender, "path=%s", name)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, v); err != nil {
		return nil, errx.WrapDetail(err, ErrTemplateRender, "path=%s", name)
	}
	return buf.Bytes(), nil
}

// sortFiles sorts by path so the output order stays stable and idempotent. | sortFiles 按路径字典序稳定排序，保证输出顺序与幂等性。
func sortFiles(files []plannedFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
}

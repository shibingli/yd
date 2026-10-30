package scaffold

import (
	"os"
	"path"
	"strings"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/assets"
	"github.com/shibingli/yd/web"
	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/json"
)

// projectMeta 是写入 .yd.json 的工程元信息，供 yd dev / yd build / yd assets 读取。
// projectMeta is the project metadata written to .yd.json and read by yd dev, yd build and yd assets.
type projectMeta struct {
	// Name is the project name (also the directory name). | Name 是项目名（同时是目录名）。
	Name string `json:"name"`
	// Module is the go.mod module path. | Module 是 go.mod 的模块路径。
	Module string `json:"module"`
	// AssetRoot is the front-end asset root. | AssetRoot 是前端资产根目录。
	AssetRoot string `json:"assetRoot"`
	// Features records the component switches; redis, asynq and cron are always true. | Features 记录组件开关；redis、asynq 与 cron 恒为 true。
	Features projectFeatures `json:"features"`
}

// projectFeatures is the field set of the features section in .yd.json. | projectFeatures 是 .yd.json 中 features 段的字段集。
type projectFeatures struct {
	// DB is the database driver (including none). | DB 是数据库驱动（含 none）。
	DB string `json:"db"`
	// HtmxExts lists the selected Htmx extensions. | HtmxExts 是所选 Htmx 扩展。
	HtmxExts []string `json:"htmxExts"`
	// Redis records that go-redis is wired in; it is not switchable. | Redis 记录 go-redis 已接入；它不是开关。
	Redis bool `json:"redis"`
	// Asynq records that asynq is wired in; it is not switchable. | Asynq 记录 asynq 已接入；它不是开关。
	Asynq bool `json:"asynq"`
	// Cron records that the process-wide cron scheduler is wired in; it is not switchable. |
	// Cron 记录进程级 cron 调度器已接入；它不是开关。
	Cron bool `json:"cron"`
	// GRPC tells whether the gRPC dual channel is enabled. | GRPC 表示是否开启 gRPC 双通道。
	GRPC bool `json:"grpc"`
	// Captcha tells whether sign-in and password changes require verification by default. | Captcha 表示登录与改密是否默认要求验证码。
	Captcha bool `json:"captcha"`
	// WAF tells whether the WAF package, rules and dependencies ship with the project. | WAF 表示项目是否
	// 随包携带 WAF 包、规则与依赖。
	WAF bool `json:"waf"`
	// Auth 表示多租户 + 用户 / 角色 / 权限的登录鉴权链路已接入。
	// Auth tells whether the multi-tenant sign-in chain — users, roles and capabilities — is wired in.
	//
	// 与其它开关不同，它没有对应的脚手架参数：鉴权是每个生成项目都带的能力，写进清单是为了让
	// yd dev / yd build 与运维自检能像读其它开关一样读到它，而不是靠人记得「这个项目一定有登录」。
	// Unlike the other switches it has no scaffold option: authentication ships with every generated project, and the
	// entry exists so yd dev, yd build and operations checks read it exactly like any other switch instead of relying on
	// someone remembering that this project always has a sign-in.
	Auth bool `json:"auth"`
	// I18n tells whether internationalisation is enabled. | I18n 表示是否开启国际化。
	I18n bool `json:"i18n"`
	// Tailwind tells whether Tailwind v4 is wired in. | Tailwind 表示是否接入 Tailwind v4。
	Tailwind bool `json:"tailwind"`
}

// frameworkManifestName 是框架资产清单在 web 内嵌目录树里的路径。
// frameworkManifestName is the path of the framework asset manifest inside the embedded web tree.
//
// 生成项目里的 web/assets.lock.json 是它的副本，因此清单在仓库内只有一份真源。
// The generated project's web/assets.lock.json is a copy of it, so the manifest keeps a single source of truth.
const frameworkManifestName = "assets.lock.json"

// extAssetPrefix 是 Htmx 扩展资产的注册名前缀（hx-ws、hx-sse、hx-preload、hx-alpine-compat）。
// extAssetPrefix is the registration-name prefix of Htmx extension assets (hx-ws, hx-sse, hx-preload, hx-alpine-compat).
const extAssetPrefix = "hx-"

// extAsset 把可选扩展名映射到框架清单中的资产注册名。
// extAsset maps a selectable extension name onto the asset registration name used by the framework manifest.
//
// 两者并非总是一一对应（alpine 对应 hx-alpine-compat），因此必须显式映射而不是拼前缀。
// The two are not always aligned (alpine maps onto hx-alpine-compat), so the mapping is explicit rather than prefix-derived.
var extAsset = map[string]string{
	ExtWS:               "hx-ws",
	ExtSSE:              "hx-sse",
	ExtPreload:          "hx-preload",
	ExtAlpine:           "hx-alpine-compat",
	ExtTargets:          "hx-targets",
	ExtHead:             "hx-head",
	ExtUpsert:           "hx-upsert",
	ExtPending:          "hx-pending",
	ExtMultipart:        "hx-multipart",
	ExtDownload:         "hx-download",
	ExtBrowserIndicator: "hx-browser-indicator",
	ExtHistoryCache:     "hx-history-cache",
	ExtLive:             "hx-live",
	ExtPrompt:           "hx-prompt",
	ExtPTag:             "hx-ptag",
	ExtCSP:              "hx-csp",
	ExtCompat:           "hx-compat",
	ExtHtmx2Compat:      "htmx-2-compat",
}

// frameworkManifest 读取框架内嵌的资产清单（web/assets.lock.json 的唯一真源）。
// frameworkManifest reads the framework's embedded asset manifest (the single source of truth for web/assets.lock.json).
func frameworkManifest() (*assets.Manifest, error) {
	m, err := assets.LoadManifest(web.FS(), frameworkManifestName)
	if err != nil {
		return nil, errs.WrapDetail(err, ErrTemplateRead, "path=web/%[1]s | 路径=web/%[1]s", frameworkManifestName)
	}
	return m, nil
}

// generatedDocs 生成无法用模板表达的产出：.yd.json 与按扩展裁剪的资产清单。
// generatedDocs produces the outputs that templates cannot express: .yd.json and the extension-trimmed asset lock.
func generatedDocs(o Options, v view, m *assets.Manifest) ([]plannedFile, error) {
	meta, err := marshalIndent(projectMeta{
		Name:   o.Name,
		Module: o.Module,
		Features: projectFeatures{
			DB:       o.DB,
			Redis:    true,
			Asynq:    true,
			Cron:     true,
			GRPC:     o.GRPC,
			Captcha:  o.Captcha,
			WAF:      o.WAF,
			I18n:     o.I18n,
			Tailwind: o.Tailwind,
			Auth:     true,
			HtmxExts: o.HtmxExts,
		},
		AssetRoot: AssetRoot,
	})
	if err != nil {
		return nil, err
	}

	lock, err := marshalIndent(trimManifest(m, o.HtmxExts))
	if err != nil {
		return nil, err
	}

	files := []plannedFile{
		{path: projectPrefix(o.Name) + ".yd.json", content: meta},
		{path: projectPrefix(o.Name) + v.AssetRoot + "/" + assetCatalogName, content: lock},
	}

	// 开启 Tailwind 时把框架组件层原样复制进项目：生成项目的模板使用了 .yd-shell、.yd-table 等
	// 框架组件类，而项目自己的 yd.css 一旦生成就会遮蔽框架的兜底 yd.css，因此这份组件层必须
	// 进入项目的 Tailwind 输入，否则页面会在第一次构建后失去全部布局样式。
	// When Tailwind is on, copy the framework component layers verbatim into the project: the generated
	// templates use framework component classes such as .yd-shell and .yd-table, and once the project's own
	// yd.css exists it shadows the framework fallback, so these layers have to be part of the project's
	// Tailwind input; otherwise the first build strips every layout rule from the page.
	if o.Tailwind {
		for _, name := range []string{"components.css", "ui.css"} {
			css, err := frameworkCSS(name)
			if err != nil {
				return nil, err
			}
			files = append(files, plannedFile{
				path:    projectPrefix(o.Name) + v.AssetRoot + "/static/css/" + name,
				content: css,
			})
		}
	}

	// 组件模板与样式不同：它们没有 Tailwind 开关，始终随项目生成。运行时模板只做路径映射，
	// 因此这里逐字节复制框架的唯一真源，生成项目与框架永远不会出现第二份实现。
	// Unlike the stylesheets, component templates have no Tailwind switch and always ship with the
	// project. Runtime templates are copied byte for byte from the framework's single source, so the
	// generated project can never grow a second implementation of the same control.
	componentTemplates, err := frameworkComponentTemplates(o.Name)
	if err != nil {
		return nil, err
	}
	files = append(files, componentTemplates...)
	return files, nil
}

// frameworkCSS 读取框架内嵌的样式层，它是生成项目同名文件的唯一真源。
// frameworkCSS reads one embedded framework stylesheet, the single source of truth for the generated project's file.
func frameworkCSS(name string) ([]byte, error) {
	target := "static/css/" + name
	data, err := afero.ReadFile(web.FS(), target)
	if err != nil {
		return nil, errs.WrapDetail(err, ErrTemplateRead, "path=web/%[1]s | 路径=web/%[1]s", target)
	}
	return data, nil
}

// frameworkComponentTemplates copies web/templates/components/** into the project verbatim. | frameworkComponentTemplates
// 把 web/templates/components/** 逐字节复制进项目。
//
// 组件模板是运行期 html/template 文件，其中的 {{ }} 属于运行时动作而不是生成期动作，因此只能逐字节
// 复制；一旦在生成期渲染，模板动作会被执行掉，生成项目的页面就变成静态快照。
// Component templates are runtime html/template files: their {{ }} actions are runtime syntax, not
// generation-time syntax, so they must be copied byte for byte. Rendering them at generation time would
// execute the actions and turn the generated project's pages into a static snapshot.
func frameworkComponentTemplates(project string) ([]plannedFile, error) {
	const srcRoot = "templates/components"
	var files []plannedFile
	err := afs.Walk(web.FS(), srcRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return errs.WrapDetail(err, ErrTemplateRead, "path=web/%[1]s | 路径=web/%[1]s", p)
		}
		if info.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		data, readErr := afero.ReadFile(web.FS(), p)
		if readErr != nil {
			return errs.WrapDetail(readErr, ErrTemplateRead, "path=web/%[1]s | 路径=web/%[1]s", p)
		}
		files = append(files, plannedFile{
			path:    projectPrefix(project) + path.Join("web", "templates", "components", path.Base(p)),
			content: data,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errs.Detail(ErrTemplateRead, "no component template found under web/%s | web/%s 下没有组件模板", srcRoot, srcRoot)
	}
	return files, nil
}

// trimManifest 复制框架清单并裁剪扩展资产：核心资产全量保留，扩展资产只保留所选扩展对应的那一项。
// trimManifest copies the framework manifest and trims extension assets: every core asset is kept while
// extension assets are reduced to the ones matching the selection.
//
// 未选中的扩展资产被剔除，覆盖 Htmx 官方扩展中脚手架不提供开关的那些（hx-targets、hx-head 等）。
// Unselected extension assets are dropped, which also covers the official extensions the scaffold exposes no switch for
// (hx-targets, hx-head and friends).
func trimManifest(m *assets.Manifest, exts []string) *assets.Manifest {
	selected := make([]string, 0, len(exts))
	for _, ext := range exts {
		if name, ok := extAsset[ext]; ok {
			selected = append(selected, name)
		}
	}

	trimmed := *m
	trimmed.Assets = make([]assets.Asset, 0, len(m.Assets))
	for _, a := range m.Assets {
		// htmx-2-compat is an extension even though it lacks the "hx-" prefix. | htmx-2-compat
		// 是扩展，尽管它没有 "hx-" 前缀。
		if a.Name == "htmx-2-compat" {
			if containsString(selected, a.Name) {
				trimmed.Assets = append(trimmed.Assets, a)
			}
			continue
		}
		if !strings.HasPrefix(a.Name, extAssetPrefix) || containsString(selected, a.Name) {
			trimmed.Assets = append(trimmed.Assets, a)
		}
	}
	return &trimmed
}

// marshalIndent 用 ydx/json 输出两空格缩进的 JSON，并补上结尾换行。
// marshalIndent writes two-space indented JSON through ydx/json and appends a trailing newline.
func marshalIndent(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, errs.Wrap(err, ErrTemplateRender)
	}
	return append(data, '\n'), nil
}

// containsString reports whether list contains value. | containsString 判断 list 是否包含 value。
func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

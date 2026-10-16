package scaffold

import (
	"strings"

	"github.com/shibingli/yd/assets"
	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/jsonx"
	"github.com/shibingli/yd/web"
)

// projectMeta 是写入 .yd.json 的工程元信息，供 yd dev / yd build / yd assets 读取。
// projectMeta is the project metadata written to .yd.json and read by yd dev, yd build and yd assets.
type projectMeta struct {
	// Name 是项目名（同时是目录名）。 | Name is the project name (also the directory name).
	Name string `json:"name"`
	// Module 是 go.mod 的模块路径。 | Module is the go.mod module path.
	Module string `json:"module"`
	// Features 记录所选组件开关。 | Features records the selected component switches.
	Features projectFeatures `json:"features"`
	// AssetRoot 是前端资产根目录。 | AssetRoot is the front-end asset root.
	AssetRoot string `json:"assetRoot"`
}

// projectFeatures 是 .yd.json 中 features 段的字段集。 | projectFeatures is the field set of the features section in .yd.json.
type projectFeatures struct {
	// DB 是数据库驱动（含 none）。 | DB is the database driver (including none).
	DB string `json:"db"`
	// Redis 表示是否接入 go-redis。 | Redis tells whether go-redis is wired in.
	Redis bool `json:"redis"`
	// Asynq 表示是否接入 asynq。 | Asynq tells whether asynq is wired in.
	Asynq bool `json:"asynq"`
	// GRPC 表示是否开启 gRPC 双通道。 | GRPC tells whether the gRPC dual channel is enabled.
	GRPC bool `json:"grpc"`
	// I18n 表示是否开启国际化。 | I18n tells whether internationalisation is enabled.
	I18n bool `json:"i18n"`
	// Tailwind 表示是否接入 Tailwind v4。 | Tailwind tells whether Tailwind v4 is wired in.
	Tailwind bool `json:"tailwind"`
	// HtmxExts 是所选 Htmx 扩展。 | HtmxExts lists the selected Htmx extensions.
	HtmxExts []string `json:"htmxExts"`
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
	ExtWS:      "hx-ws",
	ExtSSE:     "hx-sse",
	ExtPreload: "hx-preload",
	ExtAlpine:  "hx-alpine-compat",
}

// frameworkManifest 读取框架内嵌的资产清单（web/assets.lock.json 的唯一真源）。
// frameworkManifest reads the framework's embedded asset manifest (the single source of truth for web/assets.lock.json).
func frameworkManifest() (*assets.Manifest, error) {
	m, err := assets.LoadManifest(web.FS(), frameworkManifestName)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrTemplateRead, "path=web/%s", frameworkManifestName)
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
			Redis:    o.Redis,
			Asynq:    o.Asynq,
			GRPC:     o.GRPC,
			I18n:     o.I18n,
			Tailwind: o.Tailwind,
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

	return []plannedFile{
		{path: projectPrefix(o.Name) + ".yd.json", content: meta},
		{path: projectPrefix(o.Name) + v.AssetRoot + "/" + assetCatalogName, content: lock},
	}, nil
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
		if !strings.HasPrefix(a.Name, extAssetPrefix) || containsString(selected, a.Name) {
			trimmed.Assets = append(trimmed.Assets, a)
		}
	}
	return &trimmed
}

// marshalIndent 用 jsonx 输出两空格缩进的 JSON，并补上结尾换行。
// marshalIndent writes two-space indented JSON through jsonx and appends a trailing newline.
func marshalIndent(v any) ([]byte, error) {
	data, err := jsonx.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, errx.Wrap(err, ErrTemplateRender)
	}
	return append(data, '\n'), nil
}

// containsString 判断 list 是否包含 value。 | containsString reports whether list contains value.
func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

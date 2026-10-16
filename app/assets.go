package app

import (
	"embed"
	"errors"
	"io/fs"
	"strings"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/config"
	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/i18n"
)

// defaultAssets carries the asset tree the facade ships with. | defaultAssets 承载门面随附的资产树。
//
// It is deliberately minimal: one layout, one stylesheet and one catalog pair,
// enough for Default to boot with real content. | 它刻意保持最小：一份布局、一份样式表
// 与一组语言目录，足以让 Default 装载真实内容启动。
// The dedicated web package takes these over once front-end assets land, at
// which point only defaultAssetsFS needs to change. | 前端资产落地后由专用的 web 包接管，
// 届时只需替换 defaultAssetsFS。
//
//go:embed assets
var defaultAssets embed.FS

// Asset subtree names and the conventions around them. | 资产子树名及其约定。
const (
	// assetsDirName is the embedded directory every asset subtree lives in. | assetsDirName 是全部资产子树所在的内嵌目录。
	assetsDirName = "assets"
	// assetsTemplatesDir holds the html/template tree. | assetsTemplatesDir 存放 html/template 模板树。
	assetsTemplatesDir = "templates"
	// assetsStaticDir holds the files served under /static. | assetsStaticDir 存放 /static 下的静态资源。
	assetsStaticDir = "static"
	// assetsLangsDir holds the i18n catalogs, one file per language tag. | assetsLangsDir 存放语言目录，每种语言一个文件。
	assetsLangsDir = "langs"
	// assetsRoot is the path every asset subtree is resolved from. | assetsRoot 是所有资产子树的解析基准路径。
	assetsRoot = "."
	// defaultLang is the fallback default language of the built-in catalogs. | defaultLang 是内建语言目录的兜底默认语言。
	defaultLang = "zh-CN"
)

// defaultAssetsFS returns the built-in asset filesystem, re-rooted at the
// assets directory so that templates/, static/ and langs/ sit at its root. | defaultAssetsFS
// 返回内建资产文件系统，并以 assets 目录重新定根，使 templates/、static/ 与 langs/ 位于其根下。
//
// The error can only fire if the embedded tree loses its assets directory,
// which the compiler already rejects. | 该错误只可能在内嵌资产树丢失 assets 目录时出现，
// 而那种情况编译器已先拒绝。
func defaultAssetsFS() (afero.Fs, error) {
	sub, err := fs.Sub(defaultAssets, assetsDirName)
	if err != nil {
		return nil, errx.WrapDetail(err, ErrAssetsDir, "%q", assetsDirName)
	}
	return afs.NewEmbed(sub), nil
}

// assetDir resolves dir inside the asset filesystem. | assetDir 在资产文件系统内解析 dir。
//
// io/fs backends (go:embed) are re-rooted with fs.Sub so that nested paths stay
// slash-separated on every platform: afero's BasePathFs joins with the OS
// separator, which fs.ValidPath rejects on Windows. | io/fs 后端（go:embed）
// 用 fs.Sub 重新定根，保证嵌套路径在任何平台上都是斜杠分隔：afero 的 BasePathFs
// 按操作系统分隔符拼接路径，而 fs.ValidPath 在 Windows 上会拒绝这种路径。
//
// A missing directory degrades to an empty in-memory filesystem, so an
// application without that asset kind still boots. | 目录缺失时降级为空的
// 内存文件系统，使缺少该类资产的应用仍可启动。
func assetDir(fsys afero.Fs, dir string) (afero.Fs, error) {
	if fsys == nil {
		return nil, ErrNilFS
	}

	from, ok := fsys.(afero.FromIOFS)
	if !ok {
		return afero.NewBasePathFs(fsys, dir), nil
	}

	sub, err := fs.Sub(from.FS, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return afs.NewMem(), nil
		}
		return nil, errx.WrapDetail(err, ErrAssetsDir, "%q", dir)
	}
	return afs.NewEmbed(sub), nil
}

// i18nBundle loads the message catalogs from the langs subtree. | i18nBundle 从 langs 子树加载语言目录。
//
// The subtree becomes the catalog root (dir ".") so that catalog paths stay
// slash-separated on every backend, embed included. | 子树即目录根（dir "."），
// 从而使语言文件路径在任何后端（含 embed）上都是斜杠分隔。
func (a *App) i18nBundle() (*i18n.Bundle, error) {
	langFS, err := assetDir(a.fs, assetsLangsDir)
	if err != nil {
		return nil, err
	}

	lang := defaultLang
	if a.cfg != nil {
		if v := a.cfg.GetString("i18n.default_lang"); v != "" {
			lang = v
		}
	}

	bundle, err := i18n.New(langFS, assetsRoot, lang)
	if err != nil {
		return nil, errx.Wrap(err, ErrI18n)
	}
	return bundle, nil
}

// isDevelopment reports whether the runtime environment is a development one,
// which enables template hot reloading. | isDevelopment 报告运行环境是否为开发环境，
// 开发环境会启用模板热重载。
//
// An unknown or missing environment counts as development, so that a fresh
// project picks up template edits without restarting. | 环境未知或缺失时按开发环境处理，
// 使新项目无需重启即可看到模板改动。
func isDevelopment(c *config.Config) bool {
	if c == nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(c.GetString("app.env")), "production")
}

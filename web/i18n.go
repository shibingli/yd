// i18n.go — the embedded message catalogues of the yd admin shell. | i18n.go — yd 管理后台内嵌的消息目录。
//
// web/i18n/{zh-CN,en}.yaml ship the two built-in catalogues that every {{tr "key"}} in
// web/templates/** and every data-yd-i18n key resolve against, so an application built on
// package web renders bilingual pages without shipping a single extra file.
// web/i18n/{zh-CN,en}.yaml 是两个内置语言目录，web/templates/** 中的每个 {{tr "key"}}
// 与每个 data-yd-i18n 键都由它们解析，因此基于 package web 构建的应用无需任何额外文件
// 即可渲染中英双语页面。
//
// The catalogue tree is read once at startup, exactly like the project catalogues the scaffold
// generates under web/langs/: i18n.New loads one file per language and keys are lower-cased, so
// `lang.zh-CN` and `lang.zh-cn` are the same key.
// 目录树只在启动期读取一次，与脚手架在 web/langs/ 下生成的项目目录一致：i18n.New 每种语言
// 加载一个文件，键名统一归一化为小写，因此 `lang.zh-CN` 与 `lang.zh-cn` 是同一个键。
package web

import (
	"embed"
	"io/fs"

	"github.com/shibingli/yd/http/i18n"
	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/errs"
)

// Catalogue layout and language tags. | 语言目录布局与语言标签。
const (
	// CatalogsDir is the catalogue subtree of the embedded tree. | CatalogsDir 是内嵌目录树中的语言目录子树。
	CatalogsDir = "i18n"
	// CatalogsRoot is the directory i18n.New loads from, i.e. the subtree root itself.
	// CatalogsRoot 是 i18n.New 的加载目录，即子树根本身。
	CatalogsRoot = "."
	// LangChinese is the tag of the built-in Chinese catalogue. | LangChinese 是内置中文目录的语言标签。
	LangChinese = "zh-CN"
	// LangEnglish is the tag of the built-in English catalogue. | LangEnglish 是内置英文目录的语言标签。
	LangEnglish = "en"
	// DefaultLang is the fallback language: a key missing from the active catalogue is resolved
	// against this one before the key name itself is shown.
	// DefaultLang 是兜底语言：当前目录缺失的键先回退到它，再回退到键名本身。
	DefaultLang = LangChinese
)

// ErrCatalogs reports an embedded catalogue tree that could not be loaded. | ErrCatalogs 表示内嵌语言目录树加载失败。
var ErrCatalogs = errs.New("web: failed to load the embedded message catalogues | web：加载内嵌消息目录失败")

// catalogs holds the built-in catalogue files; the "all:" prefix keeps dot-prefixed placeholders in.
// catalogs 保存内置语言目录文件；"all:" 前缀保证点号开头的占位文件也被打包。
//
//go:embed all:i18n
var catalogs embed.FS

// Catalogs returns the embedded catalogue subtree as a read-only file system. | Catalogs 以只读文件系统返回内嵌语言目录子树。
//
// Paths use "/" separators and satisfy fs.ValidPath, for example "zh-CN.yaml"; the subtree root is
// what i18n.New expects as its directory, so a caller that wants its own bundle writes
// `i18n.New(web.Catalogs(), web.CatalogsRoot, web.DefaultLang)`.
// 路径以 "/" 分隔且满足 fs.ValidPath，例如 "zh-CN.yaml"；子树根正是 i18n.New 需要的目录，
// 因此想要自建 Bundle 的调用方写作 `i18n.New(web.Catalogs(), web.CatalogsRoot, web.DefaultLang)`。
//
// A missing subtree degrades to an empty in-memory file system so that i18n.New reports its own
// ErrNoCatalogs instead of this function panicking at startup.
// 子树缺失时降级为空的内存文件系统，让 i18n.New 报出它自己的 ErrNoCatalogs，
// 而不是让本函数在启动期 panic。
func Catalogs() afs.FS {
	sub, err := fs.Sub(catalogs, CatalogsDir)
	if err != nil {
		return afs.NewMem()
	}
	return afs.NewEmbed(sub)
}

// Bundle loads the embedded catalogues into a ready-to-use i18n bundle. | Bundle 把内嵌语言目录加载为可直接使用的 i18n 束。
//
// It is what an application wires once at startup, right before `r.Use(i18n.Middleware(b, …))`:
// `b, err := web.Bundle()`. The default language is DefaultLang, so a key missing from the active
// catalogue falls back to Chinese and only then to the key name itself.
// 应用在启动期装配一次即可，紧接其后调用 `r.Use(i18n.Middleware(b, …))`：
// `b, err := web.Bundle()`。兜底语言为 DefaultLang，因此当前目录缺失的键先回退到中文，
// 再回退到键名本身。
//
// A failed load is annotated with ErrCatalogs; the original error stays reachable through errors.Is,
// so a caller can still tell ErrNoCatalogs from ErrParseCatalog.
// 加载失败会以 ErrCatalogs 注解；原始错误仍可经 errors.Is 判定，
// 因此调用方依然能区分 ErrNoCatalogs 与 ErrParseCatalog。
func Bundle() (*i18n.Bundle, error) {
	bundle, err := i18n.New(Catalogs(), CatalogsRoot, DefaultLang)
	if err != nil {
		return nil, errs.Wrap(err, ErrCatalogs)
	}
	return bundle, nil
}

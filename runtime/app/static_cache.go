// This file adds cache semantics to /static responses: asset-helper URLs carry a content hash and may therefore be
// cached immutably, while development and hash-less URLs keep revalidating so a changed file is visible at once.
// 本文件给 /static 响应补上缓存语义：模板经 asset 助手生成的地址带内容哈希，可长期不可变；
// 开发期与无哈希地址保持每次校验，静态文件改动立即可见。

package app

import (
	"github.com/spf13/afero"
	"strings"

	"github.com/shibingli/yd/runtime/metricsx"
	"github.com/shibingli/ydx/router"
)

// StaticCacheDescriptor describes the static cache middleware on the dashboard. |
// StaticCacheDescriptor 在仪表盘上描述静态缓存中间件。
var StaticCacheDescriptor = metricsx.Descriptor{Name: "static-cache", Title: "Static cache | 静态缓存", Scope: metricsx.ScopeGlobal}

const (
	// staticCacheImmutable is safe because the URL contains the content hash: a changed file has a new URL. |
	// staticCacheImmutable 是安全的：地址包含内容哈希，文件变化就会产生新地址。
	staticCacheImmutable = "public, max-age=31536000, immutable"
	// staticCacheRevalidate forces a conditional request in development and for unhashed paths. |
	// staticCacheRevalidate 在开发期与无哈希地址上强制条件请求。
	staticCacheRevalidate = "no-cache"
)

// staticCacheMiddleware sets the cache policy of a static response before the file server writes it. |
// staticCacheMiddleware 在文件服务器写出之前设置静态响应的缓存策略。
//
// Only the versioned URLs emitted by the asset helper get the immutable policy; a hand-written
// /static/... reference keeps revalidating, so replacing a file without changing its URL can never
// be masked by a year-long cache. | 只有 asset 助手生成的带版本地址获得不可变策略；
// 手写的 /static/... 引用继续每次校验，因此替换同名文件绝不会被一年的缓存掩盖。
func staticCacheMiddleware(production bool, fsys afero.Fs) router.Middleware {
	return func(c *router.Ctx) {
		if strings.HasPrefix(c.Path(), staticURLPrefix+"/") {
			// The compression middleware writes Vary only when it actually compresses; a static asset declares it
			// whether or not this response was compressed, so a shared cache never conflates the two forms. Static
			// files do not vary by language or cookie, and keeping those dimensions from the i18n middleware would
			// prevent a CDN from storing them at all, so the value is replaced rather than appended. |
			// 压缩中间件只在真正压缩时才写 Vary；静态资源无论本次是否压缩都声明它，共享缓存才不会把压缩与未压缩
			// 的表示混为一谈。静态文件不随语言或 Cookie 变化，保留 i18n 中间件写入的这些维度会让 CDN 完全无法缓存，
			// 因此这里覆盖而不是追加。
			c.Writer.Header().Set("Vary", "Accept-Encoding")
			if production && isContentHash(c.Query("v")) && staticFileExists(fsys, c.Path()) {
				c.SetHeader("Cache-Control", staticCacheImmutable)
			} else {
				c.SetHeader("Cache-Control", staticCacheRevalidate)
			}
		}
		c.Next()
	}
}

// StaticCache returns the static cache policy for this app's resolved environment. It is exported so a
// project assembled with app.New (which generated projects use) gets the same policy as app.Default. |
// StaticCache 返回按本应用解析后的环境选择的静态缓存策略。它被导出，使经 app.New 装配的项目
// （生成项目正是如此）与 app.Default 得到同一套策略。
func (a *App) StaticCache() router.Middleware {
	production := a != nil && a.cfg != nil && !isDevelopment(a.cfg)
	return staticCacheMiddleware(production, a.staticFS)
}

// staticFileExists reports whether a /static request names a real regular file. A hand-written
// ?v=deadbeef on a missing path must not earn a year-long immutable cache, and a 404 must never be
// cached as immutable. | staticFileExists 报告 /static 请求是否指向真实普通文件。缺失路径上的
// 手写 ?v=deadbeef 不得获得一年不可变缓存，404 也绝不能以 immutable 缓存。
func staticFileExists(fsys afero.Fs, path string) bool {
	if fsys == nil {
		return false
	}
	rel := strings.TrimPrefix(path, staticURLPrefix+"/")
	if rel == path || rel == "" {
		return false
	}
	// The shared script bundle is assembled in memory and served by an exact route, so it has no
	// file in the static tree; its URL still carries the content hash the asset helper computed. |
	// 共享脚本 bundle 在内存中组装并由精确路由提供，静态树里没有对应文件；它的地址仍带有资产助手
	// 计算出的内容哈希。
	if rel == "js/yd-bundle.js" {
		return true
	}
	info, err := fsys.Stat(rel)
	return err == nil && info != nil && !info.IsDir()
}

// isContentHash reports the asset helper's version form: eight lower-case hex characters (four SHA-256
// bytes). A hand-written ?v=1 must not earn a year-long immutable cache. |
// isContentHash 报告 asset 助手的版本形态：八个小写十六进制字符（四个 SHA-256 字节）。
// 手写的 ?v=1 不得因此获得一年的不可变缓存。
func isContentHash(value string) bool {
	if len(value) != 8 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

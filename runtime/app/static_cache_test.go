package app

import (
	"net/http"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/ydx/afs"
)

// staticFSWithAppJS builds the minimal static tree the cache tests request. | staticFSWithAppJS 构造缓存测试请求的最小静态树。
func staticFSWithAppJS(t *testing.T) afs.FS {
	t.Helper()
	static := afs.NewMem()
	if err := afero.WriteFile(static, "js/app.js", []byte(`console.log("app")`), 0o644); err != nil {
		t.Fatalf("写入静态资源失败: %v | writing the static asset failed: %v", err, err)
	}
	return static
}

// TestStaticCachePrefersImmutableOnlyForVersionedAssets pins the policy: a content-hashed URL is cached for a
// year, while a hand-written or development URL revalidates. | TestStaticCachePrefersImmutableOnlyForVersionedAssets
// 固定策略：带内容哈希的地址缓存一年，手写地址与开发期地址继续校验。
func TestStaticCachePrefersImmutableOnlyForVersionedAssets(t *testing.T) {
	static := staticFSWithAppJS(t)
	a := newTestApp(t, nil, WithStaticFS(static))
	a.Use(staticCacheMiddleware(false, nil))

	const hash = "0123abcd"
	versioned := doRequest(t, a, http.MethodGet, "/static/js/app.js?v="+hash, nil)
	if got := versioned.Header().Get("Cache-Control"); got != staticCacheRevalidate {
		t.Errorf("development versioned Cache-Control = %q, want %q | 开发期带版本 Cache-Control = %q，期望 %q",
			got, staticCacheRevalidate, got, staticCacheRevalidate)
	}
	if got := versioned.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("static Vary = %q, want Accept-Encoding | 静态 Vary = %q，期望 Accept-Encoding", got, got)
	}

	prod := newTestApp(t, nil, WithStaticFS(staticFSWithAppJS(t)))
	prod.Use(staticCacheMiddleware(true, prod.staticFS))
	cached := doRequest(t, prod, http.MethodGet, "/static/js/app.js?v="+hash, nil)
	if got := cached.Header().Get("Cache-Control"); got != staticCacheImmutable {
		t.Errorf("production versioned Cache-Control = %q, want %q | 生产期带版本 Cache-Control = %q，期望 %q",
			got, staticCacheImmutable, got, staticCacheImmutable)
	}
	plain := doRequest(t, prod, http.MethodGet, "/static/js/app.js", nil)
	if got := plain.Header().Get("Cache-Control"); got != staticCacheRevalidate {
		t.Errorf("production unversioned Cache-Control = %q, want %q | 生产期无版本 Cache-Control = %q，期望 %q",
			got, staticCacheRevalidate, got, staticCacheRevalidate)
	}
	// 手写版本号（不是 8 位十六进制内容哈希）不得获得不可变缓存。 |
	// A hand-written version (not an eight-hex content hash) must not earn the immutable policy.
	forged := doRequest(t, prod, http.MethodGet, "/static/js/app.js?v=1", nil)
	if got := forged.Header().Get("Cache-Control"); got != staticCacheRevalidate {
		t.Errorf("production forged version Cache-Control = %q, want %q | 生产期伪造版本 Cache-Control = %q，期望 %q",
			got, staticCacheRevalidate, got, staticCacheRevalidate)
	}
	// 不存在的文件即使带合法形态的哈希也必须校验，404 不得被共享缓存一年。 |
	// A missing file must revalidate even with a well-formed hash; a 404 must never be cached for a year.
	missing := doRequest(t, prod, http.MethodGet, "/static/js/missing.js?v="+hash, nil)
	if missing.Code != http.StatusNotFound {
		t.Errorf("missing asset status = %d, want %d | 缺失资源状态 = %d，期望 %d",
			missing.Code, http.StatusNotFound, missing.Code, http.StatusNotFound)
	}
	if got := missing.Header().Get("Cache-Control"); got != staticCacheRevalidate {
		t.Errorf("missing asset Cache-Control = %q, want %q | 缺失资源 Cache-Control = %q，期望 %q",
			got, staticCacheRevalidate, got, staticCacheRevalidate)
	}
	// 共享脚本 bundle 由精确路由在内存中生成，静态树里没有文件；带内容哈希时仍应缓存一年。 |
	// The shared script bundle is generated in memory by an exact route and has no file in the static
	// tree; a content-hashed URL must still be immutable.
	bundle := doRequest(t, prod, http.MethodGet, "/static/js/yd-bundle.js?v="+hash+"&part=0", nil)
	if got := bundle.Header().Get("Cache-Control"); got != staticCacheImmutable {
		t.Errorf("bundle Cache-Control = %q, want %q | bundle Cache-Control = %q，期望 %q",
			got, staticCacheImmutable, got, staticCacheImmutable)
	}
}

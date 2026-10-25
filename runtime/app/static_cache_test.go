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
	a.Use(staticCacheMiddleware(false))

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
	prod.Use(staticCacheMiddleware(true))
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
}

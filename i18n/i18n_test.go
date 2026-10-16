package i18n

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/yun-dev/yd/afs"
	"github.com/yun-dev/yd/jsonx"
	"github.com/yun-dev/yd/router"
)

// fixtureDir is the catalog directory used by every fixture filesystem. | fixtureDir 是所有夹具文件系统使用的语言目录。
const fixtureDir = "langs"

// writeCatalog writes one catalog fixture into fsys. | writeCatalog 把一个语言目录夹具写入 fsys。
func writeCatalog(t *testing.T, fsys afero.Fs, name, body string) {
	t.Helper()
	if err := afero.WriteFile(fsys, filepath.Join(fixtureDir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", name, err)
	}
}

// newFixtureFS builds an in-memory catalog directory holding yaml / toml / json files. | newFixtureFS 构造内存语言目录，内含 yaml / toml / json 三种格式的文件。
func newFixtureFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afs.NewMem()
	if err := fsys.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("创建语言目录失败: %v", err)
	}

	writeCatalog(t, fsys, "zh-CN.yaml", `
title: "云开发"
greeting: "你好，{{.Name}}！"
summary: "{{.Name}} 在 {{.City}} 有 {{.Count}} 条记录"
mixed: "{{.Name}} / %s"
nav:
  sidebar:
    home: "首页"
user:
  profile:
    title: "个人资料"
    welcome: "欢迎 %s，你有 %d 条新消息"
`)

	writeCatalog(t, fsys, "en.toml", `
title = "Yun Dev"
greeting = "Hello, {{.Name}}!"
summary = "{{.Name}} in {{.City}} has {{.Count}} records"
mixed = "{{.Name}} / %s"
only_default = "English only"

[nav.sidebar]
home = "Home"

[user.profile]
title = "Profile"
welcome = "Welcome %s, you have %d new messages"
`)

	writeCatalog(t, fsys, "ja.json", `{
  "title": "ユン開発",
  "greeting": "こんにちは、{{.Name}}さん！",
  "user": {"profile": {"title": "プロフィール"}}
}`)

	// The underscore in the file name must be normalized to a dash. | 文件名中的下划线必须归一化为连字符。
	writeCatalog(t, fsys, "zh_TW.yaml", "title: \"雲開發\"\n")

	return fsys
}

// newFixtureBundle loads the fixture catalogs with "en" as the default language. | newFixtureBundle 加载夹具目录，默认语言为 "en"。
func newFixtureBundle(t *testing.T) *Bundle {
	t.Helper()
	b, err := New(newFixtureFS(t), fixtureDir, "en")
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	return b
}

// doRequest serves one request through r and returns the recorder. | doRequest 通过 r 处理一次请求并返回记录器。
func doRequest(r *router.Router, target string, hdr, cookies map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for name, value := range cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestLoadYamlAndTomlCatalogs covers yaml / toml / json loading plus `_` normalization in file names. | TestLoadYamlAndTomlCatalogs 覆盖 yaml / toml / json 加载以及文件名中 `_` 的归一化。
func TestLoadYamlAndTomlCatalogs(t *testing.T) {
	b := newFixtureBundle(t)

	if got, want := b.DefaultLang(), "en"; got != want {
		t.Fatalf("DefaultLang() = %q, 期望 %q", got, want)
	}

	got := b.Langs()
	want := []string{"en", "ja", "zh-CN", "zh-TW"}
	if len(got) != len(want) {
		t.Fatalf("Langs() = %v, 期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Langs() = %v, 期望 %v（升序且含归一化后的 zh-TW）", got, want)
		}
	}

	cases := []struct {
		lang string
		key  string
		want string
	}{
		{"en", "title", "Yun Dev"},
		{"zh-CN", "title", "云开发"},
		{"ja", "title", "ユン開発"},
		{"zh-TW", "title", "雲開發"},
		{"en", "user.profile.title", "Profile"},
		{"zh-CN", "user.profile.title", "个人资料"},
		{"ja", "user.profile.title", "プロフィール"},
	}
	for _, tc := range cases {
		if got := b.Translator(tc.lang).T(tc.key); got != tc.want {
			t.Errorf("Translator(%q).T(%q) = %q, 期望 %q", tc.lang, tc.key, got, tc.want)
		}
	}

	// A yaml-only key must not leak into the toml catalog and vice versa. | 仅存在于 yaml 的键不得串到 toml 目录，反之亦然。
	if got := b.Translator("zh-CN").T("nav.sidebar.home"); got != "首页" {
		t.Errorf("zh-CN nav.sidebar.home = %q, 期望 首页", got)
	}
	if got := b.Translator("en").T("nav.sidebar.home"); got != "Home" {
		t.Errorf("en nav.sidebar.home = %q, 期望 Home", got)
	}
}

// TestNewValidatesCatalogs covers the fail-fast validation of New. | TestNewValidatesCatalogs 覆盖 New 的快速失败校验。
func TestNewValidatesCatalogs(t *testing.T) {
	empty := afs.NewMem()
	if err := empty.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("创建语言目录失败: %v", err)
	}
	if _, err := New(empty, fixtureDir, "en"); err == nil {
		t.Fatal("空目录应当返回错误")
	}

	fsys := afs.NewMem()
	if err := fsys.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("创建语言目录失败: %v", err)
	}
	writeCatalog(t, fsys, "en.yaml", "title: \"Yun Dev\"\n")
	if _, err := New(fsys, fixtureDir, "zh-CN"); err == nil {
		t.Fatal("默认语言缺少目录文件时应当返回错误")
	}
}

// TestNestedKeyLookup covers multi-level drilling and the case-insensitive key contract. | TestNestedKeyLookup 覆盖多级下钻与键名大小写不敏感的契约。
func TestNestedKeyLookup(t *testing.T) {
	b := newFixtureBundle(t)
	tr := b.Translator("zh-CN")

	if got, want := tr.T("user.profile.title"), "个人资料"; got != want {
		t.Fatalf("T(user.profile.title) = %q, 期望 %q", got, want)
	}
	if got, want := tr.T("nav.sidebar.home"), "首页"; got != want {
		t.Fatalf("T(nav.sidebar.home) = %q, 期望 %q", got, want)
	}
	// Keys are normalized to lower case (viper semantics), so upper case input still resolves. | 键名统一归一化为小写（viper 语义），因此大写输入同样可以命中。
	if got, want := tr.T("USER.Profile.Title"), "个人资料"; got != want {
		t.Fatalf("T(USER.Profile.Title) = %q, 期望 %q", got, want)
	}
	// A partially wrong path must miss, never panic. | 部分错误的路径必须落空，绝不 panic。
	if got, want := tr.T("user.nope.title"), "user.nope.title"; got != want {
		t.Fatalf("T(user.nope.title) = %q, 期望 %q", got, want)
	}
}

// TestMissingKeyReturnsKey covers unknown keys, non-scalar targets and language fallback. | TestMissingKeyReturnsKey 覆盖未知键、非标量目标以及语言回退。
func TestMissingKeyReturnsKey(t *testing.T) {
	b := newFixtureBundle(t)
	tr := b.Translator("zh-CN")

	cases := []struct {
		name string
		key  string
		want string
	}{
		{"unknown key", "does.not.exist", "does.not.exist"},
		{"unknown top level", "nope", "nope"},
		{"leaf is a map", "user.profile", "user.profile"},
		{"empty key", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tr.T(tc.key); got != tc.want {
				t.Fatalf("T(%q) = %q, 期望 %q", tc.key, got, tc.want)
			}
		})
	}

	// Missing language falls back to the default language. | 语言缺失时回退默认语言。
	miss := b.Translator("fr")
	if got, want := miss.Lang(), "en"; got != want {
		t.Fatalf("Translator(fr).Lang() = %q, 期望 %q", got, want)
	}
	if got, want := miss.T("title"), "Yun Dev"; got != want {
		t.Fatalf("Translator(fr).T(title) = %q, 期望 %q", got, want)
	}

	// A key present only in the default catalog is served by the fallback catalog. | 仅存在于默认目录的键由回退目录提供。
	if got, want := tr.T("only_default"), "English only"; got != want {
		t.Fatalf("T(only_default) = %q, 期望 %q", got, want)
	}

	// An empty language string resolves to the default language as well. | 空语言字符串同样解析为默认语言。
	if got, want := b.Translator("").Lang(), "en"; got != want {
		t.Fatalf("Translator(\"\").Lang() = %q, 期望 %q", got, want)
	}
}

// TestArgsInterpolationBothStyles covers both `{{.Name}}` and `%s` placeholder styles. | TestArgsInterpolationBothStyles 覆盖 `{{.Name}}` 与 `%s` 两种占位风格。
func TestArgsInterpolationBothStyles(t *testing.T) {
	b := newFixtureBundle(t)

	cases := []struct {
		name string
		lang string
		key  string
		args []any
		want string
	}{
		{"named placeholder", "zh-CN", "greeting", []any{"小明"}, "你好，小明！"},
		{"named placeholder en", "en", "greeting", []any{"Ada"}, "Hello, Ada!"},
		{"printf style", "zh-CN", "user.profile.welcome", []any{"小明", 3}, "欢迎 小明，你有 3 条新消息"},
		{"printf style en", "en", "user.profile.welcome", []any{"Ada", 3}, "Welcome Ada, you have 3 new messages"},
		{"three named placeholders", "zh-CN", "summary", []any{"小明", "上海", 7}, "小明 在 上海 有 7 条记录"},
		{"three named placeholders en", "en", "summary", []any{"Ada", "Paris", 7}, "Ada in Paris has 7 records"},
		{"mixed styles", "zh-CN", "mixed", []any{"小明", "x"}, "小明 / x"},
		{"missing args keep placeholder", "zh-CN", "summary", []any{"小明"}, "小明 在 {{.City}} 有 {{.Count}} 条记录"},
		{"no placeholder keeps template", "zh-CN", "title", []any{"ignored"}, "云开发"},
		{"no args keeps placeholder", "zh-CN", "greeting", nil, "你好，{{.Name}}！"},
		{"non string arg", "en", "user.profile.welcome", []any{"Ada", int64(5)}, "Welcome Ada, you have 5 new messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := b.Translator(tc.lang)
			var got string
			if tc.args == nil {
				got = tr.T(tc.key)
			} else {
				got = tr.T(tc.key, tc.args...)
			}
			if got != tc.want {
				t.Fatalf("T(%q, %v) = %q, 期望 %q", tc.key, tc.args, got, tc.want)
			}
		})
	}
}

// TestMatchLanguageQValues covers q-value ordering and the `*` wildcard. | TestMatchLanguageQValues 覆盖 q 值排序与 `*` 通配。
func TestMatchLanguageQValues(t *testing.T) {
	b := newFixtureBundle(t)

	cases := []struct {
		name   string
		accept string
		want   string
	}{
		{"higher q wins", "en;q=0.8,zh-CN;q=0.9", "zh-CN"},
		{"missing q means 1.0", "en;q=0.9,zh-CN", "zh-CN"},
		{"lower q loses", "zh-CN;q=0.5,en;q=0.9", "en"},
		{"tie keeps client order", "ja;q=0.3,en;q=0.3", "ja"},
		{"wildcard is the default language", "*", "en"},
		{"q=0 is not acceptable", "de;q=0,en;q=0.5", "en"},
		{"browser style list", "zh-CN,zh;q=0.9,en;q=0.8,*;q=0.1", "zh-CN"},
		{"spaces and upper case", " ZH-cn ; q=0.7 , en ; q=0.2 ", "zh-CN"},
		{"empty accept", "", "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := b.MatchLanguage(tc.accept); got != tc.want {
				t.Fatalf("MatchLanguage(%q) = %q, 期望 %q", tc.accept, got, tc.want)
			}
		})
	}
}

// TestMatchLanguagePrefixFallback covers `zh-CN` -> `zh` -> default fallback in both directions. | TestMatchLanguagePrefixFallback 覆盖 `zh-CN` -> `zh` -> 默认语言的双向前缀回退。
func TestMatchLanguagePrefixFallback(t *testing.T) {
	b := newFixtureBundle(t)

	cases := []struct {
		name   string
		accept string
		want   string
	}{
		{"primary subtag matches region", "zh", "zh-CN"},
		{"script subtag falls back", "zh-Hans", "zh-CN"},
		{"script plus region falls back", "zh-Hans-CN", "zh-CN"},
		{"unknown region falls back", "zh-HK", "zh-CN"},
		{"region is dropped", "en-US", "en"},
		{"case insensitive", "EN-us", "en"},
		{"unknown language uses default", "fr-CA", "en"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := b.MatchLanguage(tc.accept); got != tc.want {
				t.Fatalf("MatchLanguage(%q) = %q, 期望 %q", tc.accept, got, tc.want)
			}
		})
	}

	// The opposite direction: only `zh` is shipped, `zh-CN` must still hit it. | 反方向：只提供 `zh` 时，`zh-CN` 仍必须命中它。
	fsys := afs.NewMem()
	if err := fsys.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("创建语言目录失败: %v", err)
	}
	writeCatalog(t, fsys, "en.yaml", "title: \"Yun Dev\"\n")
	writeCatalog(t, fsys, "zh.yaml", "title: \"云开发\"\n")
	onlyZh, err := New(fsys, fixtureDir, "en")
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	for _, accept := range []string{"zh-CN", "zh-CN,zh;q=0.9", "zh-Hans-CN"} {
		if got := onlyZh.MatchLanguage(accept); got != "zh" {
			t.Fatalf("MatchLanguage(%q) = %q, 期望 zh", accept, got)
		}
	}
	if got := onlyZh.Translator("zh-CN").T("title"); got != "云开发" {
		t.Fatalf("Translator(zh-CN).T(title) = %q, 期望 云开发", got)
	}
}

// TestMiddlewareResolutionOrder covers query > cookie > header > Accept-Language. | TestMiddlewareResolutionOrder 覆盖 query > cookie > header > Accept-Language 的解析顺序。
func TestMiddlewareResolutionOrder(t *testing.T) {
	b := newFixtureBundle(t)

	newRouter := func(o Options) *router.Router {
		r := router.New()
		r.Use(Middleware(b, o))
		r.GET("/", func(c *router.Ctx) {
			tr := FromContext(c)
			if tr == nil {
				_ = c.String(http.StatusInternalServerError, "translator missing")
				return
			}
			_ = c.String(http.StatusOK, tr.Lang()+"|"+TemplateFunc(c)("title"))
		})
		return r
	}

	browserHeaders := map[string]string{"Accept-Language": "en-US,en;q=0.9"}
	r := newRouter(Options{})

	cases := []struct {
		name    string
		target  string
		hdr     map[string]string
		cookies map[string]string
		lang    string
		title   string
	}{
		{"query wins", "/?lang=zh-CN", browserHeaders, map[string]string{"yd_lang": "en"}, "zh-CN", "云开发"},
		{"cookie beats header", "/", map[string]string{"Accept-Language": "en-US", "X-Lang": "ja"}, map[string]string{"yd_lang": "en"}, "en", "Yun Dev"},
		{"header beats accept-language", "/", map[string]string{"Accept-Language": "en-US", "X-Lang": "ja"}, nil, "ja", "ユン開発"},
		{"accept-language q order", "/", map[string]string{"Accept-Language": "zh-CN;q=0.9,en;q=0.5"}, nil, "zh-CN", "云开发"},
		{"accept-language fallback", "/", map[string]string{"Accept-Language": "en-US,en;q=0.9"}, nil, "en", "Yun Dev"},
		{"default language", "/", nil, nil, "en", "Yun Dev"},
		{"empty query falls through", "/?lang=", browserHeaders, map[string]string{"yd_lang": "zh-CN"}, "zh-CN", "云开发"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(r, tc.target, tc.hdr, tc.cookies)
			if rec.Code != http.StatusOK {
				t.Fatalf("状态码 = %d, 期望 200", rec.Code)
			}
			if got, want := rec.Body.String(), tc.lang+"|"+tc.title; got != want {
				t.Fatalf("body = %q, 期望 %q", got, want)
			}
			if got := rec.Header().Get("Content-Language"); got != tc.lang {
				t.Fatalf("Content-Language = %q, 期望 %q", got, tc.lang)
			}
		})
	}

	// Custom source names must replace the defaults, not add to them. | 自定义来源名必须替换默认名，而不是叠加。
	t.Run("custom names", func(t *testing.T) {
		custom := newRouter(Options{QueryParam: "l", CookieName: "lang2", HeaderName: "X-My-Lang"})
		rec := doRequest(custom, "/?lang=zh-CN", map[string]string{"X-Lang": "zh-CN"}, map[string]string{"yd_lang": "zh-CN"})
		if got, want := rec.Body.String(), "en|Yun Dev"; got != want {
			t.Fatalf("默认来源名不应生效: body = %q, 期望 %q", got, want)
		}
		rec = doRequest(custom, "/?l=zh-CN", nil, nil)
		if got, want := rec.Body.String(), "zh-CN|云开发"; got != want {
			t.Fatalf("自定义 query 参数名未生效: body = %q, 期望 %q", got, want)
		}
		rec = doRequest(custom, "/", nil, map[string]string{"lang2": "ja"})
		if got, want := rec.Body.String(), "ja|ユン開発"; got != want {
			t.Fatalf("自定义 cookie 名未生效: body = %q, 期望 %q", got, want)
		}
		rec = doRequest(custom, "/", map[string]string{"X-My-Lang": "zh-CN"}, nil)
		if got, want := rec.Body.String(), "zh-CN|云开发"; got != want {
			t.Fatalf("自定义请求头名未生效: body = %q, 期望 %q", got, want)
		}
	})

	// Without the middleware FromContext returns nil and the nil translator stays safe. | 未安装中间件时 FromContext 返回 nil，且 nil 译者保持安全。
	t.Run("without middleware", func(t *testing.T) {
		bare := router.New()
		bare.GET("/", func(c *router.Ctx) {
			tr := FromContext(c)
			if tr != nil {
				t.Errorf("未安装中间件时 FromContext 应返回 nil，实际 %v", tr)
			}
			if got := FromContext(c).T("title"); got != "title" {
				t.Errorf("nil 译者的 T 应返回 key，实际 %q", got)
			}
			if got := TemplateFunc(c)("user.profile.title"); got != "user.profile.title" {
				t.Errorf("nil 模板函数应返回 key，实际 %q", got)
			}
			_ = c.NoContent()
		})
		rec := doRequest(bare, "/", nil, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("状态码 = %d, 期望 204", rec.Code)
		}
	})
}

// TestConcurrentReload hammers Reload against concurrent readers and must pass with -race. | TestConcurrentReload 让 Reload 与并发读取相互冲击，必须在 -race 下通过。
func TestConcurrentReload(t *testing.T) {
	fsys := afs.NewMem()
	if err := fsys.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("创建语言目录失败: %v", err)
	}
	writeCatalog(t, fsys, "en.yaml", "title: \"v0\"\n")
	writeCatalog(t, fsys, "zh-CN.yaml", "greeting: \"你好\"\n")

	b, err := New(fsys, fixtureDir, "en")
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}

	contents := []string{"title: \"v0\"\n", "title: \"v1\"\n", "title: \"v2\"\n"}
	done := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < 200; i++ {
			body := contents[i%len(contents)]
			if err := afero.WriteFile(fsys, filepath.Join(fixtureDir, "en.yaml"), []byte(body), 0o644); err != nil {
				t.Errorf("写入失败: %v", err)
				return
			}
			if err := b.Reload(); err != nil {
				t.Errorf("Reload 失败: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				// "title" only lives in the default catalog, so this also exercises the fallback path. | "title" 只存在于默认目录，因此这里同时覆盖回退路径。
				switch got := b.Translator("zh-CN").T("title"); got {
				case "v0", "v1", "v2":
				default:
					t.Errorf("并发读取得到意外值 %q", got)
					return
				}
				if got := b.MatchLanguage("zh-CN,en;q=0.9"); got != "zh-CN" {
					t.Errorf("并发 MatchLanguage = %q, 期望 zh-CN", got)
					return
				}
				if cat := b.Catalog("en"); cat == nil || cat["title"] == nil {
					t.Errorf("并发 Catalog 为空")
					return
				}
				if langs := b.Langs(); len(langs) != 2 {
					t.Errorf("并发 Langs() = %v, 期望 2 个语言", langs)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got, want := b.Translator("en").T("title"), "v1"; got != want {
		t.Fatalf("重载收敛后 T(title) = %q, 期望 %q", got, want)
	}
}

// TestCatalogForFrontend covers the deep copy handed to the frontend. | TestCatalogForFrontend 覆盖交给前端的深拷贝。
func TestCatalogForFrontend(t *testing.T) {
	b := newFixtureBundle(t)

	cat := b.Catalog("zh-CN")
	if cat == nil {
		t.Fatal("Catalog(zh-CN) 不应为 nil")
	}
	if got, want := cat["title"], "云开发"; got != want {
		t.Fatalf("Catalog[title] = %v, 期望 %v", got, want)
	}
	user, ok := cat["user"].(map[string]any)
	if !ok {
		t.Fatalf("Catalog[user] 类型 = %T, 期望 map[string]any", cat["user"])
	}
	profile, ok := user["profile"].(map[string]any)
	if !ok {
		t.Fatalf("Catalog[user.profile] 类型 = %T, 期望 map[string]any", user["profile"])
	}
	if got, want := profile["title"], "个人资料"; got != want {
		t.Fatalf("Catalog[user.profile.title] = %v, 期望 %v", got, want)
	}

	// Mutating the returned copy must never leak back into the bundle. | 修改返回的副本绝不能回渗到 Bundle。
	cat["title"] = "HACKED"
	user["profile"] = "HACKED"
	if got, want := b.Translator("zh-CN").T("title"), "云开发"; got != want {
		t.Fatalf("副本被修改后 T(title) = %q, 期望 %q", got, want)
	}
	if got, want := b.Translator("zh-CN").T("user.profile.title"), "个人资料"; got != want {
		t.Fatalf("副本被修改后 T(user.profile.title) = %q, 期望 %q", got, want)
	}

	// The catalog is meant for frontend JSON injection. | 该目录用于前端注入 JSON。
	buf, err := jsonx.Marshal(b.Catalog("en"))
	if err != nil {
		t.Fatalf("jsonx.Marshal 失败: %v", err)
	}
	if !strings.Contains(string(buf), "Yun Dev") {
		t.Fatalf("序列化结果缺少 title: %s", buf)
	}

	// Unknown languages fall back to the default catalog. | 未知语言回退默认目录。
	def := b.Catalog("fr")
	if def == nil || def["title"] != "Yun Dev" {
		t.Fatalf("Catalog(fr) = %v, 期望默认目录", def)
	}

	// Reload picks up freshly written files. | Reload 能感知新写入的文件。
	fsys := afs.NewMem()
	if err := fsys.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("创建语言目录失败: %v", err)
	}
	writeCatalog(t, fsys, "en.yaml", "title: \"Yun Dev\"\n")
	reloadBundle, err := New(fsys, fixtureDir, "en")
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if got := reloadBundle.Translator("en").T("title"); got != "Yun Dev" {
		t.Fatalf("Reload 之前 T(title) = %q, 期望 Yun Dev", got)
	}
	if err := afero.WriteFile(fsys, filepath.Join(fixtureDir, "en.yaml"), []byte("title: \"Yun Dev 2\"\n"), 0o644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if got := reloadBundle.Translator("en").T("title"); got != "Yun Dev" {
		t.Fatalf("Reload 之前 T(title) = %q, 期望仍为旧值 Yun Dev", got)
	}
	if err := reloadBundle.Reload(); err != nil {
		t.Fatalf("Reload 失败: %v", err)
	}
	if got := reloadBundle.Translator("en").T("title"); got != "Yun Dev 2" {
		t.Fatalf("Reload 之后 T(title) = %q, 期望 Yun Dev 2", got)
	}
	// A reload failure must keep the previous catalogs intact. | Reload 失败时必须保留原有目录。
	if err := afero.WriteFile(fsys, filepath.Join(fixtureDir, "en.yaml"), []byte("title: [unterminated\n"), 0o644); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := reloadBundle.Reload(); err == nil {
		t.Fatal("损坏的目录文件应当让 Reload 返回错误")
	}
	if got := reloadBundle.Translator("en").T("title"); got != "Yun Dev 2" {
		t.Fatalf("Reload 失败后 T(title) = %q, 期望保留 Yun Dev 2", got)
	}
}

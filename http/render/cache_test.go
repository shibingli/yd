package render

import (
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"
)

// renderString executes one template with the given data. | renderString 用指定数据执行一次模板。
func renderString(t *testing.T, e *Engine, data map[string]any) string {
	t.Helper()
	var b strings.Builder
	if err := e.Render(&b, "pages/home.html", data); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return b.String()
}

// TestBoundSetCacheByLanguage keeps two languages on two cached sets. | TestBoundSetCacheByLanguage 让两种语言各用一份缓存集。
func TestBoundSetCacheByLanguage(t *testing.T) {
	e := newBenchEngine(t)
	en := renderString(t, e, map[string]any{TranslatorKey: benchTranslator{lang: "en"}})
	zh := renderString(t, e, map[string]any{TranslatorKey: benchTranslator{lang: "zh"}})
	if !strings.Contains(en, "en:home.title") || !strings.Contains(zh, "zh:home.title") {
		t.Fatalf("language binding leaked: en=%q zh=%q", en, zh)
	}
	again := renderString(t, e, map[string]any{TranslatorKey: benchTranslator{lang: "en"}})
	if !strings.Contains(again, "en:home.title") {
		t.Fatalf("cached set lost its binding: %q", again)
	}
}

// TestBoundSetCacheByDecisionKey keeps two decision sets apart. | TestBoundSetCacheByDecisionKey 让两个判定集合互不串用。
func TestBoundSetCacheByDecisionKey(t *testing.T) {
	e := newBenchEngine(t)
	admin := renderString(t, e, map[string]any{
		TranslatorKey: benchTranslator{lang: "en"},
		AuthzKey:      benchAuthz{key: "admin", allow: true},
	})
	viewer := renderString(t, e, map[string]any{
		TranslatorKey: benchTranslator{lang: "en"},
		AuthzKey:      benchAuthz{key: "viewer", allow: false},
	})
	if !strings.Contains(admin, "<b>yes</b>") {
		t.Fatalf("admin decision lost: %q", admin)
	}
	if !strings.Contains(viewer, "<i>no</i>") {
		t.Fatalf("viewer set leaked the admin decision: %q", viewer)
	}
}

// TestBoundSetCacheInvalidatedByLoad proves a reload cannot reuse an old parse. | TestBoundSetCacheInvalidatedByLoad 证明重载不会复用旧解析。
func TestBoundSetCacheInvalidatedByLoad(t *testing.T) {
	fsys := afero.NewMemMapFs()
	path := "templates/pages/home.html"
	_ = fsys.MkdirAll("templates/pages", 0o755)
	write := func(body string) {
		if err := afero.WriteFile(fsys, path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{{tr "v1"}}`)
	e, err := New(fsys, Options{Root: "templates"})
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]any{TranslatorKey: benchTranslator{lang: "en"}}
	if got := renderString(t, e, data); !strings.Contains(got, "en:v1") {
		t.Fatalf("initial render = %q", got)
	}
	write(`{{tr "v2"}}`)
	if err := e.load(); err != nil {
		t.Fatal(err)
	}
	if got := renderString(t, e, data); !strings.Contains(got, "en:v2") {
		t.Fatalf("render after reload = %q, want the new template", got)
	}
}

// TestBoundSetCacheConcurrent exercises the cache under the race detector. | TestBoundSetCacheConcurrent 在竞态检测器下验证缓存。
func TestBoundSetCacheConcurrent(t *testing.T) {
	e := newBenchEngine(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lang := "en"
			if i%2 == 1 {
				lang = "zh"
			}
			allow := i%3 == 0
			data := map[string]any{
				TranslatorKey: benchTranslator{lang: lang},
				AuthzKey:      benchAuthz{key: lang + ":" + boolKey(allow), allow: allow},
			}
			for j := 0; j < 100; j++ {
				var b strings.Builder
				if err := e.Render(&b, "pages/home.html", data); err != nil {
					t.Errorf("Render: %v", err)
					return
				}
				out := b.String()
				if !strings.Contains(out, lang+":home.title") {
					t.Errorf("language leaked: %q", out)
					return
				}
				if want := map[bool]string{true: "<b>yes</b>", false: "<i>no</i>"}[allow]; !strings.Contains(out, want) {
					t.Errorf("decision leaked: %q, want %q", out, want)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// boolKey renders a boolean as a stable key. | boolKey 把布尔值渲染为稳定键。
func boolKey(v bool) string {
	if v {
		return "allow"
	}
	return "deny"
}

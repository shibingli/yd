package render_test

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"sync"
	"testing"

	"github.com/shibingli/yd/render"
)

// stubTranslator is the Translator test double used by this file: it renders the
// key with a marker, appends the arguments it received and records every call.
// | stubTranslator 是本文件使用的 Translator 测试替身：把键连同标记一起渲染，追加收到的
// 参数，并记录每次调用。
type stubTranslator struct {
	marker string     // Marker prefix. | 标记前缀。
	calls  []stubCall // Recorded calls. | 已记录的调用。
}

// stubCall is one recorded T call. | stubCall 是一次被记录的 T 调用。
type stubCall struct {
	key  string
	args []any
}

// T renders "marker+key(arg1,arg2)" and records the call. | T 渲染 "marker+key(arg1,arg2)" 并记录本次调用。
func (s *stubTranslator) T(key string, args ...any) string {
	s.calls = append(s.calls, stubCall{key: key, args: args})
	if len(args) == 0 {
		return s.marker + key
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, fmt.Sprint(arg))
	}
	return s.marker + key + "(" + strings.Join(parts, ",") + ")"
}

// translatorTemplate is the fixture page every test below renders. | translatorTemplate 是下方各测试渲染的夹具页面。
const translatorTemplate = `<h1>{{ tr "user.title" }}</h1>`

// newTranslatorEngine builds an engine over the fixture page. | newTranslatorEngine 在夹具页面之上构建引擎。
func newTranslatorEngine(t *testing.T, opts render.Options) *render.Engine {
	t.Helper()

	engine, err := render.New(writeTemplates(t, map[string]string{
		"templates/pages/home.html": translatorTemplate,
	}), opts)
	if err != nil {
		t.Fatalf("New 失败: %v | New failed: %v", err, err)
	}
	return engine
}

// TestTrUsesInjectedTranslator proves a translator injected under
// render.TranslatorKey takes over `{{ tr "..." }}`. | TestTrUsesInjectedTranslator 证明
// 经 render.TranslatorKey 注入的译者接管了 `{{ tr "..." }}`。
func TestTrUsesInjectedTranslator(t *testing.T) {
	engine := newTranslatorEngine(t, render.Options{})
	translator := &stubTranslator{marker: "T:"}

	var buf bytes.Buffer
	if err := engine.Render(&buf, "pages/home.html", map[string]any{render.TranslatorKey: translator}); err != nil {
		t.Fatalf("渲染失败: %v | render failed: %v", err, err)
	}
	if got, want := buf.String(), "<h1>T:user.title</h1>"; got != want {
		t.Errorf("输出 = %q, 期望 %q | output = %q, want %q", got, want, got, want)
	}
	if len(translator.calls) != 1 || translator.calls[0].key != "user.title" {
		t.Errorf("译者调用 = %+v, 期望一次 user.title | translator calls = %+v, want one user.title", translator.calls, translator.calls)
	}
}

// TestTrFallsBackToKey proves every data shape without a translator renders the
// key itself: no panic, no error, and no leftover binding from an earlier render.
// | TestTrFallsBackToKey 证明所有「没有译者」的数据形态都渲染键本身：不 panic、不报错，
// 也不残留上一次渲染的绑定。
func TestTrFallsBackToKey(t *testing.T) {
	engine := newTranslatorEngine(t, render.Options{})

	// A translated render first, so the fallbacks below would catch a leaked binding.
	// | 先做一次带译者的渲染，下方各回退用例便能捕获泄漏的绑定。
	var translated bytes.Buffer
	if err := engine.Render(&translated, "pages/home.html", map[string]any{render.TranslatorKey: &stubTranslator{marker: "T:"}}); err != nil {
		t.Fatalf("带译者的渲染失败: %v | render with translator failed: %v", err, err)
	}
	if got := translated.String(); got != "<h1>T:user.title</h1>" {
		t.Fatalf("带译者的输出 = %q | output with translator = %q", got, got)
	}

	cases := []struct {
		name string
		data any
	}{
		{"nil 数据 | nil data", nil},
		{"不含键的 map | map without the key", map[string]any{"Title": "yd"}},
		{"键下非 Translator | foreign value under the key", map[string]any{render.TranslatorKey: "not a translator"}},
		{"结构体数据 | struct data", struct{ Title string }{Title: "yd"}},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := engine.Render(&buf, "pages/home.html", tc.data); err != nil {
			t.Errorf("%s: 渲染失败: %v | render failed: %v", tc.name, err, err)
			continue
		}
		if got, want := buf.String(), "<h1>user.title</h1>"; got != want {
			t.Errorf("%s: 输出 = %q, 期望键本身 %q | output = %q, want the key %q", tc.name, got, want, got, want)
		}
	}
}

// TestTrForwardsArgs proves `{{ tr "k" "a" 42 }}` hands the extra arguments to
// Translator.T unchanged. | TestTrForwardsArgs 证明 `{{ tr "k" "a" 42 }}` 会把多余参数
// 原样交给 Translator.T。
func TestTrForwardsArgs(t *testing.T) {
	mem := writeTemplates(t, map[string]string{
		"templates/pages/home.html": `{{ tr "greeting" "Yun" 42 }}`,
	})
	engine, err := render.New(mem, render.Options{})
	if err != nil {
		t.Fatalf("New 失败: %v | New failed: %v", err, err)
	}
	translator := &stubTranslator{marker: "T:"}

	var buf bytes.Buffer
	if err := engine.Render(&buf, "pages/home.html", map[string]any{render.TranslatorKey: translator}); err != nil {
		t.Fatalf("渲染失败: %v | render failed: %v", err, err)
	}
	if got, want := buf.String(), "T:greeting(Yun,42)"; got != want {
		t.Errorf("输出 = %q, 期望 %q | output = %q, want %q", got, want, got, want)
	}

	if len(translator.calls) != 1 {
		t.Fatalf("译者调用次数 = %d, 期望 1 | translator calls = %d, want 1", len(translator.calls), len(translator.calls))
	}
	call := translator.calls[0]
	if call.key != "greeting" {
		t.Errorf("键 = %q, 期望 greeting | key = %q, want greeting", call.key, call.key)
	}
	if len(call.args) != 2 || fmt.Sprint(call.args[0]) != "Yun" || fmt.Sprint(call.args[1]) != "42" {
		t.Errorf("参数 = %v, 期望 [Yun 42] | args = %v, want [Yun 42]", call.args, call.args)
	}
}

// TestOptionsFuncsOverrideTr proves a "tr" registered through Options.Funcs wins
// over the injected translator, which is then never consulted. | TestOptionsFuncsOverrideTr
// 证明经 Options.Funcs 注册的 "tr" 优先于注入的译者，此后不再使用注入的译者。
func TestOptionsFuncsOverrideTr(t *testing.T) {
	engine := newTranslatorEngine(t, render.Options{Funcs: template.FuncMap{
		"tr": func(key string, args ...any) string { return "own:" + key },
	}})
	translator := &stubTranslator{marker: "T:"}

	var buf bytes.Buffer
	if err := engine.Render(&buf, "pages/home.html", map[string]any{render.TranslatorKey: translator}); err != nil {
		t.Fatalf("渲染失败: %v | render failed: %v", err, err)
	}
	if got, want := buf.String(), "<h1>own:user.title</h1>"; got != want {
		t.Errorf("输出 = %q, 期望调用方自己的 tr 生效 | output = %q, want the caller's own tr", got, got)
	}
	if len(translator.calls) != 0 {
		t.Errorf("翻译器被调用 %d 次, 期望 0 次 | translator called %d times, want 0", len(translator.calls), len(translator.calls))
	}
}

// TestTrIsolatesConcurrentRenders proves two concurrent executions keep their own
// translator: a template set is shared, so a global binding would mix the languages
// of two requests. | TestTrIsolatesConcurrentRenders 证明两个并发执行各自使用自己的译者：
// 模板集是共享的，全局绑定会把两个请求的语言混在一起。
func TestTrIsolatesConcurrentRenders(t *testing.T) {
	engine := newTranslatorEngine(t, render.Options{})

	const rounds = 32
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		marker := fmt.Sprintf("T%d:", i)
		wg.Add(1)
		go func() {
			defer wg.Done()

			var buf bytes.Buffer
			if err := engine.Render(&buf, "pages/home.html", map[string]any{render.TranslatorKey: &stubTranslator{marker: marker}}); err != nil {
				t.Errorf("并发渲染失败: %v | concurrent render failed: %v", err, err)
				return
			}
			if got, want := buf.String(), "<h1>"+marker+"user.title</h1>"; got != want {
				t.Errorf("并发输出 = %q, 期望 %q | concurrent output = %q, want %q", got, want, got, want)
				return
			}

			// The same set without a translator must stay on the key. | 同一集合在无译者时必须仍返回键本身。
			buf.Reset()
			if err := engine.Render(&buf, "pages/home.html", nil); err != nil {
				t.Errorf("并发回退渲染失败: %v | concurrent fallback render failed: %v", err, err)
				return
			}
			if got, want := buf.String(), "<h1>user.title</h1>"; got != want {
				t.Errorf("并发回退输出 = %q, 期望 %q | concurrent fallback output = %q, want %q", got, want, got, want)
			}
		}()
	}
	wg.Wait()
}

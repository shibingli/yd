package templatebench

// Engine benchmarks on one shared page workload. | 在同一个页面负载上对各引擎做基准。

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"io"
	"os"
	"strings"
	"testing"
	texttemplate "text/template"

	jet "github.com/CloudyKit/jet/v6"
	"github.com/aymerick/raymond"
	"github.com/cbroglie/mustache"
	"github.com/flosch/pongo2/v6"
	"github.com/osteele/liquid"
	liquidrender "github.com/osteele/liquid/render"
	"github.com/spf13/afero"

	ydrender "github.com/shibingli/yd/http/render"
)

//go:embed templates/page.html templates/page.jet templates/page.pongo2 templates/page.liquid templates/page.hbs templates/page.mustache
var templateFS embed.FS

// benchEngine is one engine under comparison. | benchEngine 是参与对比的一个引擎。
type benchEngine struct {
	name    string
	page    func(io.Writer, *Page) error
	mapPage func(io.Writer, map[string]any) error
}

// selected reports whether name passes the YD_BENCH_ONLY filter. | selected 判断 name 是否通过 YD_BENCH_ONLY 过滤。
func selected(name string) bool {
	want := os.Getenv("YD_BENCH_ONLY")
	if want == "" {
		return true
	}
	for _, part := range strings.Split(want, ",") {
		if strings.TrimSpace(part) == name {
			return true
		}
	}
	return false
}

// readTemplate reads one embedded template source. | readTemplate 读取一个内嵌模板源。
func readTemplate(tb testing.TB, name string) string {
	tb.Helper()
	b, err := templateFS.ReadFile(name)
	if err != nil {
		tb.Fatal(err)
	}
	return string(b)
}

// benchEngines builds every engine with its compiled template. | benchEngines 构建所有引擎及其已编译模板。
func benchEngines(tb testing.TB) []benchEngine {
	tb.Helper()
	var out []benchEngine

	// yd compiled engine. | yd 编译引擎。
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, "templates/page.html", []byte(readTemplate(tb, "templates/page.html")), 0o644); err != nil {
		tb.Fatal(err)
	}
	yd, err := ydrender.New(fsys, ydrender.Options{Root: "templates"})
	if err != nil {
		tb.Fatal(err)
	}
	out = append(out, benchEngine{
		name: "yd-render",
		page: func(w io.Writer, p *Page) error { return yd.Render(w, "page.html", p) },
	})

	// html/template and text/template. | 标准库 html/template 与 text/template。
	htmlSrc := readTemplate(tb, "templates/page.html")
	htmlTmpl, err := template.New("page").Parse(htmlSrc)
	if err != nil {
		tb.Fatal(err)
	}
	out = append(out, benchEngine{
		name: "html/template",
		page: func(w io.Writer, p *Page) error { return htmlTmpl.Execute(w, p) },
	})
	textTmpl, err := texttemplate.New("page").Parse(htmlSrc)
	if err != nil {
		tb.Fatal(err)
	}
	out = append(out, benchEngine{
		name: "text/template",
		page: func(w io.Writer, p *Page) error { return textTmpl.Execute(w, p) },
	})

	// Jet. | Jet。
	loader := jet.NewInMemLoader()
	loader.Set("page.jet", readTemplate(tb, "templates/page.jet"))
	set := jet.NewSet(loader)
	jetTmpl, err := set.GetTemplate("page.jet")
	if err != nil {
		tb.Fatal(err)
	}
	out = append(out, benchEngine{
		name: "jet",
		page: func(w io.Writer, p *Page) error { return jetTmpl.Execute(w, nil, p) },
	})

	// Pongo2. | Pongo2。
	pongoTmpl, err := pongo2.FromString(readTemplate(tb, "templates/page.pongo2"))
	if err != nil {
		tb.Fatal(err)
	}
	pongoContext := func(p *Page) pongo2.Context {
		return pongo2.Context{
			"Title": p.Title, "Lang": p.Lang, "Query": p.Query,
			"User": p.User, "Items": p.Items, "Total": p.Total,
		}
	}
	out = append(out, benchEngine{
		name: "pongo2",
		page: func(w io.Writer, p *Page) error { return pongoTmpl.ExecuteWriter(pongoContext(p), w) },
	})

	// Liquid with autoescaping enabled. | 启用自动转义的 Liquid。
	le := liquid.NewEngine()
	le.SetAutoEscapeReplacer(liquidrender.HtmlEscaper)
	liquidTmpl, serr := le.ParseString(readTemplate(tb, "templates/page.liquid"))
	if serr != nil {
		tb.Fatal(serr)
	}
	out = append(out, benchEngine{
		name:    "liquid",
		mapPage: func(w io.Writer, m map[string]any) error { return liquidTmpl.FRender(w, liquid.Bindings(m)) },
	})

	// Raymond (Handlebars). | Raymond（Handlebars）。
	rayTmpl, err := raymond.Parse(readTemplate(tb, "templates/page.hbs"))
	if err != nil {
		tb.Fatal(err)
	}
	out = append(out, benchEngine{
		name: "raymond",
		mapPage: func(w io.Writer, m map[string]any) error {
			s, rerr := rayTmpl.Exec(m)
			if rerr != nil {
				return rerr
			}
			_, werr := io.WriteString(w, s)
			return werr
		},
	})

	// Mustache. | Mustache。
	mustTmpl, err := mustache.ParseString(readTemplate(tb, "templates/page.mustache"))
	if err != nil {
		tb.Fatal(err)
	}
	out = append(out, benchEngine{
		name:    "mustache",
		mapPage: func(w io.Writer, m map[string]any) error { return mustTmpl.FRender(w, m) },
	})

	// quicktemplate (generated). | quicktemplate（代码生成）。
	out = append(out, benchEngine{
		name: "quicktemplate",
		page: func(w io.Writer, p *Page) error { WriteRenderPageQT(w, p); return nil },
	})

	// templ (generated). | templ（代码生成）。
	out = append(out, benchEngine{
		name: "templ",
		page: func(w io.Writer, p *Page) error {
			return RenderPage(p).Render(context.Background(), w)
		},
	})

	return out
}

// BenchmarkEngines compares every engine on a 100-row page and a 5-row page,
// then under parallel load. | BenchmarkEngines 在 100 行页面、5 行页面与并行负载下比较全部引擎。
func BenchmarkEngines(b *testing.B) {
	page := Fixture()
	small := SmallFixture()
	pageMap := MapOf(page)
	smallMap := MapOf(small)

	for _, e := range benchEngines(b) {
		if !selected(e.name) {
			continue
		}
		if e.page != nil {
			b.Run(e.name+"/page100", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if err := e.page(io.Discard, page); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(e.name+"/small5", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if err := e.page(io.Discard, small); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(e.name+"/parallel", func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if err := e.page(io.Discard, page); err != nil {
							b.Fatal(err)
						}
					}
				})
			})
			continue
		}
		b.Run(e.name+"/page100", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := e.mapPage(io.Discard, pageMap); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(e.name+"/small5", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := e.mapPage(io.Discard, smallMap); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(e.name+"/parallel", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if err := e.mapPage(io.Discard, pageMap); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// BenchmarkEnginesMaterialized renders the same page into a reusable buffer, so
// every engine pays for producing the output bytes. The discard benchmark above
// isolates CPU, but it structurally favors streaming engines: they can send
// every chunk straight to io.Discard, while an engine that guarantees an
// all-or-nothing response must materialize the page first. | BenchmarkEnginesMaterialized
// 把同一页面渲染进可复用缓冲，使每个引擎都为产出字节付费。上面的 discard 基准隔离
// CPU，但在结构上偏向流式引擎：它们可以把每个片段直接交给 io.Discard，而保证
// 「要么整页、要么无输出」的引擎必须先物化页面。
func BenchmarkEnginesMaterialized(b *testing.B) {
	page := Fixture()
	small := SmallFixture()
	pageMap := MapOf(page)
	smallMap := MapOf(small)
	buf := bytes.NewBuffer(make([]byte, 0, 1<<20))

	for _, e := range benchEngines(b) {
		if !selected(e.name) {
			continue
		}
		run := func(b *testing.B, p *Page, pm map[string]any) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buf.Reset()
				var err error
				if e.page != nil {
					err = e.page(buf, p)
				} else {
					err = e.mapPage(buf, pm)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		}
		b.Run(e.name+"/page100", func(b *testing.B) { run(b, page, pageMap) })
		b.Run(e.name+"/small5", func(b *testing.B) { run(b, small, smallMap) })
	}
}

// TestEscapingMatrix renders hostile text, attribute and URL values through
// every engine and records which contextual protections actually hold.
// | TestEscapingMatrix 用敌对的文本、属性与 URL 值渲染每个引擎，并记录实际生效的上下文防护。
func TestEscapingMatrix(t *testing.T) {
	p := Fixture()
	p.Items = p.Items[:1]
	p.Title = `<script>alert("title")</script>`
	p.Query = `"><script>alert(1)</script>`
	p.User.Name = `<img src=x onerror=alert(1)>`
	p.Items[0].Name = `<script>alert(2)</script>`
	p.Items[0].Note = `" onmouseover="alert(3)`
	p.Items[0].URL = `javascript:alert(4)`
	pm := MapOf(p)

	for _, e := range benchEngines(t) {
		t.Run(e.name, func(t *testing.T) {
			var buf bytes.Buffer
			var err error
			if e.page != nil {
				err = e.page(&buf, p)
			} else {
				err = e.mapPage(&buf, pm)
			}
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			out := buf.String()
			textSafe := !strings.Contains(out, "<script>alert")
			attrSafe := !strings.Contains(out, `onmouseover="alert`)
			urlSafe := !strings.Contains(strings.ToLower(out), `href="javascript:`)
			t.Logf("text=%v attr=%v url=%v", textSafe, attrSafe, urlSafe)
			if e.name == "text/template" {
				return // the deliberate no-escaping baseline | 刻意的不转义基线
			}
			if !textSafe {
				t.Errorf("%s did not escape HTML text: %s", e.name, out)
			}
			if !attrSafe {
				t.Errorf("%s did not escape an attribute value: %s", e.name, out)
			}
		})
	}
}

// TestEnginesProduceOutput is the fixture sanity check: every engine must render
// the shared workload without error and emit the expected list markers.
// | TestEnginesProduceOutput 是夹具自检：每个引擎都必须无错误地渲染共享负载，并输出预期的列表标记。
func TestEnginesProduceOutput(t *testing.T) {
	page := Fixture()
	pageMap := MapOf(page)
	for _, e := range benchEngines(t) {
		t.Run(e.name, func(t *testing.T) {
			var buf bytes.Buffer
			var err error
			if e.page != nil {
				err = e.page(&buf, page)
			} else {
				err = e.mapPage(&buf, pageMap)
			}
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			out := buf.String()
			for _, want := range []string{"item-1", "item-100", "items"} {
				if !strings.Contains(out, want) {
					t.Fatalf("output misses %q:\n%s", want, out)
				}
			}
			if n := strings.Count(out, "<li "); n != NumberOfItems {
				t.Fatalf("rendered %d rows, want %d", n, NumberOfItems)
			}
		})
	}
}

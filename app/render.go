package app

import (
	"bytes"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/afero"

	"github.com/yun-dev/yd/afs"
	"github.com/yun-dev/yd/errx"
	"github.com/yun-dev/yd/htmx"
	"github.com/yun-dev/yd/router"
)

// Render writes name for c, merging the Htmx fragment/whole-page semantics: | Render 为 c 渲染 name，并合并 Htmx 片段/整页语义：
//
//   - a fragment request renders the fragment name only; that is every Htmx
//     request (HX-Request: true) except one that explicitly asks for a whole
//     page (HX-Request-Type: full, i.e. htmx.IsPartial is false); | 片段请求只渲染片段
//     name；即除显式索要整页（HX-Request-Type: full，也就是 htmx.IsPartial 为假）
//     之外的每个 Htmx 请求（HX-Request: true）；
//   - any other request renders the fragment and wraps it in DefaultLayout,
//     handing the fragment HTML over through the Content/content data keys so
//     that the layout keeps access to the caller's data; | 其余请求先渲染片段，
//     再交给 DefaultLayout 包裹，片段 HTML 通过 Content/content 数据键传入，
//     因此布局仍可访问调用方的数据；
//   - when the application ships no layout template the bare template is served
//     as the whole page. | 应用未提供布局模板时，直接以裸模板作为整页输出。
//
// The body is buffered and written only once rendering succeeded, so a template
// error never leaves a half-written page in the response. | 正文先写入缓冲，
// 渲染成功后才输出，因此模板错误不会在响应里留下半截页面。
func (a *App) Render(c *router.Ctx, status int, name string, data any) error {
	if c == nil {
		return ErrNilContext
	}
	if strings.TrimSpace(name) == "" {
		return errx.Detail(ErrTemplateName, "%q", name)
	}
	if a.renderer == nil {
		return ErrNoRenderer
	}

	if htmx.IsPartial(c.Request) {
		return a.renderFragment(c, status, name, data)
	}

	// Whole page: render the fragment first, then wrap it in the layout. | 整页：先渲染片段，再用布局包裹。
	var page bytes.Buffer
	if err := a.renderer.Render(&page, name, data); err != nil {
		return err
	}
	if a.layout == "" {
		return a.writeHTML(c, status, page.Bytes())
	}

	var full bytes.Buffer
	if err := a.renderer.Render(&full, a.layout, layoutData(data, page.String())); err != nil {
		if errors.Is(err, ErrTemplateNotFound) {
			// No layout in this application: serve the bare template. | 应用没有布局：直接输出裸模板。
			return a.writeHTML(c, status, page.Bytes())
		}
		return err
	}
	return a.writeHTML(c, status, full.Bytes())
}

// renderFragment renders name into a buffer and writes it as text/html. | renderFragment 把 name 渲染到缓冲后以 text/html 输出。
func (a *App) renderFragment(c *router.Ctx, status int, name string, data any) error {
	var buf bytes.Buffer
	if err := a.renderer.Render(&buf, name, data); err != nil {
		return err
	}
	return a.writeHTML(c, status, buf.Bytes())
}

// writeHTML writes an HTML response body with the given status code. | writeHTML 以指定状态码输出 HTML 正文。
func (a *App) writeHTML(c *router.Ctx, status int, body []byte) error {
	if status <= 0 {
		status = http.StatusOK
	}
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	c.WriteHeader(status)
	if len(body) == 0 {
		return nil
	}
	_, err := c.Writer.Write(body)
	return err
}

// layoutData builds the data handed to the layout. | layoutData 构造交给布局的数据。
//
// The fragment is injected as template.HTML: it is trusted because the
// application's own renderer produced it from a template file, never from
// request input, and html/template would otherwise escape it. | 片段以 template.HTML
// 注入：它由本应用自身的渲染器从模板文件产出、不来自请求输入，因此可信；
// 否则 html/template 会把它转义。
func layoutData(data any, fragment string) map[string]any {
	content := template.HTML(fragment) //nolint:gosec // 可信来源见上文 | trusted source, see above
	out := make(map[string]any, 4)
	if src, ok := data.(map[string]any); ok {
		for k, v := range src {
			out[k] = v
		}
	} else if data != nil {
		out[DataKey] = data
	}
	// The rendered fragment always wins over caller supplied keys. | 已渲染片段始终覆盖调用方传入的同名键。
	out[ContentKey] = content
	out[contentKey] = content
	return out
}

// templateRoot is the walk root of the built-in renderer. | templateRoot 是内置渲染器的遍历根。
const templateRoot = "."

// templateRootName names the root of the parsed template set. | templateRootName 是已解析模板集合的根名称。
const templateRootName = "yd"

// templateExt is the extension the built-in renderer collects. | templateExt 是内置渲染器收集的文件扩展名。
const templateExt = ".html"

// rendererOptions configures the built-in template renderer. | rendererOptions 配置内置模板渲染器。
type rendererOptions struct {
	// Dev reparses every template on each render, so edits take effect without
	// a restart; it is what `yd dev` relies on. | Dev 在每次渲染时重新解析全部模板，
	// 使改动无需重启即可生效；`yd dev` 依赖该行为。
	Dev bool
}

// templateRenderer is the built-in router.Renderer: html/template over any
// afero filesystem. | templateRenderer 是内置的 router.Renderer：在任意 afero
// 文件系统上使用 html/template。
//
// Template names are the slash-separated paths relative to the template root,
// e.g. "layouts/base.html". | 模板名是相对模板根的斜杠分隔路径，例如 "layouts/base.html"。
// It is a stand-in for the dedicated render package and implements only what
// the facade needs: named rendering, no function map, no asset hashing. | 它是专用
// render 包的过渡实现，只提供门面所需能力：具名渲染，不含函数表与资产哈希。
type templateRenderer struct {
	fsys afero.Fs
	dev  bool

	mu   sync.RWMutex
	tmpl *template.Template
}

// newTemplateRenderer builds the renderer over fsys. | newTemplateRenderer 在 fsys 上构建渲染器。
//
// A filesystem without a template root yields an empty renderer rather than an
// error; outside Dev mode templates are parsed eagerly so that syntax errors
// surface at startup. | 没有模板根的文件系统会得到空渲染器而不是错误；非 Dev 模式下
// 模板在构造期立即解析，使语法错误在启动时暴露。
func newTemplateRenderer(fsys afero.Fs, o rendererOptions) (*templateRenderer, error) {
	if fsys == nil {
		return nil, ErrNilFS
	}
	r := &templateRenderer{fsys: fsys, dev: o.Dev}
	if !o.Dev {
		tmpl, err := r.load()
		if err != nil {
			return nil, err
		}
		r.tmpl = tmpl
	}
	return r, nil
}

// Render implements router.Renderer. | Render 实现 router.Renderer。
func (r *templateRenderer) Render(w io.Writer, name string, data any) error {
	tmpl, err := r.templates()
	if err != nil {
		return err
	}
	if tmpl.Lookup(name) == nil {
		return errx.Detail(ErrTemplateNotFound, "%q", name)
	}
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		return errx.WrapDetail(err, ErrTemplateExec, "%q", name)
	}
	return nil
}

// templates returns the parsed set, reloading it in Dev mode. | templates 返回已解析的模板集合，Dev 模式下每次重新加载。
func (r *templateRenderer) templates() (*template.Template, error) {
	if r.dev {
		return r.load()
	}

	r.mu.RLock()
	tmpl := r.tmpl
	r.mu.RUnlock()
	if tmpl != nil {
		return tmpl, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tmpl != nil {
		return r.tmpl, nil
	}
	tmpl, err := r.load()
	if err != nil {
		return nil, err
	}
	r.tmpl = tmpl
	return tmpl, nil
}

// load walks the template root and parses every template file as a named
// template. | load 遍历模板根，把每个模板文件解析为具名模板。
func (r *templateRenderer) load() (*template.Template, error) {
	set := template.New(templateRootName)
	err := afs.Walk(r.fsys, templateRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// A missing template root is a normal state (an app without
			// templates), not a failure. | 模板根缺失属于正常状态（没有模板的应用），不算失败。
			if info == nil && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.IsDir() || !strings.HasSuffix(strings.ToLower(info.Name()), templateExt) {
			return nil
		}
		name := templateName(path)
		if name == "" {
			return nil
		}
		body, err := afero.ReadFile(r.fsys, path)
		if err != nil {
			return errx.WrapDetail(err, ErrTemplateParse, "%s", name)
		}
		if _, err := set.New(name).Parse(string(body)); err != nil {
			return errx.WrapDetail(err, ErrTemplateParse, "%s", name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return set, nil
}

// templateName converts a walk path into the template name used by Render. | templateName 把遍历路径转换为 Render 使用的模板名。
func templateName(path string) string {
	name := filepath.ToSlash(path)
	name = strings.TrimPrefix(name, "./")
	return strings.Trim(name, "/")
}

package app

import (
	"bytes"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/spf13/afero"

	"github.com/yun-dev/yd/errx"
	"github.com/yun-dev/yd/htmx"
	"github.com/yun-dev/yd/render"
	"github.com/yun-dev/yd/router"
)

// newRenderer builds the facade's default renderer over the asset filesystem.
// | newRenderer 在资产文件系统上构建门面的默认渲染器。
//
// It is the dedicated render engine rooted at "templates", so template names are
// the slash-separated paths relative to that directory, e.g. "layouts/base.html".
// | 它是根为 "templates" 的专用渲染引擎，模板名即相对该目录的斜杠分隔路径，
// 例如 "layouts/base.html"。
//
// Templates are optional: an application shipping none still boots, and its
// renderer then reports every name as missing instead of failing the wiring.
// | 模板是可选的：不附带模板的应用仍可启动，此时渲染器把每个名字都报告为缺失，
// 而不是让装配失败。
func newRenderer(fsys afero.Fs, dev bool) (*render.Engine, error) {
	opts := render.Options{Root: assetsTemplatesDir, Dev: dev}
	engine, err := render.New(fsys, opts)
	if err == nil {
		return engine, nil
	}
	if !errors.Is(err, render.ErrNoTemplates) && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Nothing to load yet: keep the engine lazy so templates added later take
	// effect without a restart. | 暂无可加载的模板：让引擎保持延迟加载，
	// 之后添加的模板无需重启即可生效。
	opts.Dev = true
	return render.New(fsys, opts)
}

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
		// No layout in this application: serve the bare template. The built-in
		// engine reports the render package's sentinel, while a renderer
		// injected through WithRenderer may report the facade's own. | 应用没有布局：
		// 直接输出裸模板。内置引擎报告 render 包的哨兵，而经 WithRenderer 注入的
		// 渲染器也可能报告门面自身的哨兵。
		if errors.Is(err, render.ErrTemplateNotFound) || errors.Is(err, ErrTemplateNotFound) {
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
// request input, and an html/template backed renderer would otherwise escape
// it. | 片段以 template.HTML 注入：它由本应用自身的渲染器从模板文件产出、
// 不来自请求输入，因此可信；否则基于 html/template 的渲染器会把它转义。
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

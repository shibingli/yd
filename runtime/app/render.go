package app

import (
	"bytes"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/bufpool"
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/http/htmx"
	"github.com/shibingli/yd/http/i18n"
	"github.com/shibingli/yd/http/render"
	"github.com/shibingli/yd/http/router"
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
func newRenderer(fsys, staticFS afero.Fs, dev bool) (*render.Engine, error) {
	opts := render.Options{
		Root:       assetsTemplatesDir,
		Dev:        dev,
		AssetFS:    staticFS,
		Production: !dev,
	}
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
//
// The translator installed by the i18n middleware is injected into the data
// under render.TranslatorKey (see withTranslator), which is what makes the
// `{{ tr "..." }}` calls of the framework templates follow the request
// language. | i18n 中间件装入的译者会以 render.TranslatorKey 为键注入数据
// （见 withTranslator），框架模板中的 `{{ tr "..." }}` 因此跟随请求语言。
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

	data = withTranslator(data, translatorFor(c))

	if htmx.IsPartial(c.Request) {
		return a.renderFragment(c, status, name, data)
	}

	// The compiled engine can append both passes into one pooled buffer; a
	// renderer injected through WithRenderer keeps the generic two-buffer path.
	// | 编译引擎可以把两趟渲染追加进同一个池化缓冲；经 WithRenderer 注入的渲染器
	// 仍走通用的双缓冲路径。
	if ar, ok := a.renderer.(appendRenderer); ok {
		return a.renderWholeAppend(c, status, name, data, ar)
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

// appendRenderer is the zero-copy rendering capability of the compiled engine.
// | appendRenderer 是编译引擎的零拷贝渲染能力。
type appendRenderer interface {
	RenderAppend(dst []byte, name string, data any) ([]byte, error)
}

// bufferRenderer lets the facade reuse the engine's grown output buffers.
// | bufferRenderer 让门面复用引擎已增长的输出缓冲。
type bufferRenderer interface {
	AcquireBuffer(hint int) *render.Buffer
	ReleaseBuffer(b *render.Buffer)
}

// renderWholeAppend renders the fragment and its layout into one buffer. | renderWholeAppend 把片段与布局渲染进同一个缓冲。
func (a *App) renderWholeAppend(c *router.Ctx, status int, name string, data any, ar appendRenderer) error {
	var br bufferRenderer
	var holder *render.Buffer
	buf := bufpool.Get(0)
	if b, ok := a.renderer.(bufferRenderer); ok {
		br = b
		holder = br.AcquireBuffer(0)
		buf = holder.B
	} else {
		defer bufpool.Put(buf)
	}
	if br != nil {
		defer func() { br.ReleaseBuffer(holder) }()
	}
	start := len(buf)
	page, err := ar.RenderAppend(buf, name, data)
	if br != nil {
		holder.B = page
	}
	if err != nil {
		return err
	}
	if a.layout == "" {
		return a.writeHTML(c, status, page[start:])
	}
	// The layout appends after the fragment in the same buffer; only the newly
	// appended region is written. The fragment is converted once for the
	// template.HTML data key, which is inherent to the layout contract.
	// | 布局在同一缓冲中追加到片段之后，只写出新增区域。片段转为 template.HTML 数据键
	// 需要一次字符串转换，这是布局契约本身决定的。
	layoutStart := len(page)
	full, err := ar.RenderAppend(page, a.layout, layoutData(data, string(page[start:])))
	if br != nil {
		holder.B = full
	}
	if err != nil {
		if errors.Is(err, render.ErrTemplateNotFound) || errors.Is(err, ErrTemplateNotFound) {
			return a.writeHTML(c, status, page[start:])
		}
		return err
	}
	return a.writeHTML(c, status, full[layoutStart:])
}

// renderFragment renders name into a buffer and writes it as text/html. | renderFragment 把 name 渲染到缓冲后以 text/html 输出。
func (a *App) renderFragment(c *router.Ctx, status int, name string, data any) error {
	if ar, ok := a.renderer.(appendRenderer); ok {
		var br bufferRenderer
		var holder *render.Buffer
		buf := bufpool.Get(0)
		if b, ok := a.renderer.(bufferRenderer); ok {
			br = b
			holder = br.AcquireBuffer(0)
			buf = holder.B
		} else {
			defer bufpool.Put(buf)
		}
		if br != nil {
			defer func() { br.ReleaseBuffer(holder) }()
		}
		start := len(buf)
		page, err := ar.RenderAppend(buf, name, data)
		if br != nil {
			holder.B = page
		}
		if err != nil {
			return err
		}
		return a.writeHTML(c, status, page[start:])
	}
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

// translatorFor returns the translator the i18n middleware installed for c. | translatorFor 返回 i18n 中间件为 c 装入的译者。
//
// It reports nil when the middleware is absent, which keeps `tr` on its key
// fallback and keeps the render data untouched. | 未安装中间件时返回 nil，
// 此时 `tr` 退回返回键本身，渲染数据也保持不变。
func translatorFor(c *router.Ctx) render.Translator {
	if t := i18n.FromContext(c); t != nil {
		return t
	}
	return nil
}

// withTranslator returns data with the request translator added under
// render.TranslatorKey, so that the engine resolves `{{ tr "..." }}` through it.
// | withTranslator 返回在 render.TranslatorKey 下加入请求译者的数据，使引擎经由它解析
// `{{ tr "..." }}`。
//
// Map data is copied, never mutated, so a caller keeps ownership of its own map;
// an existing key of the same name is overwritten. | map 型数据会被复制而不被修改，
// 调用方仍拥有自己的映射；已存在的同名键会被覆盖。
//
// Data of any other shape (a struct, a slice, nil) is wrapped into a
// map[string]any as layoutData wraps it for the layout — the original value
// stays reachable as .Data — following the facade's single data convention.
// | 其它形态的数据（结构体、切片、nil）按 layoutData 对布局的包装方式包进
// map[string]any（原值仍可通过 .Data 访问），沿用门面唯一的数据约定。
//
// A nil translator (i18n not wired) returns data unchanged, so an application
// without language catalogs renders exactly as it did before. | 译者 nil
// （未装配 i18n）时原样返回数据，因此没有语言目录的应用渲染结果与之前完全一致。
func withTranslator(data any, t render.Translator) any {
	if t == nil {
		return data
	}

	out := make(map[string]any, 2)
	if src, ok := data.(map[string]any); ok {
		out = make(map[string]any, len(src)+1)
		for k, v := range src {
			out[k] = v
		}
	} else if data != nil {
		out[DataKey] = data
	}
	out[render.TranslatorKey] = t
	return out
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

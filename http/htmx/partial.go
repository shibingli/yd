package htmx

import (
	"bytes"
	"html"
	"html/template"
	"io"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/http/router"
)

// ErrWritePartial indicates the rendered <hx-partial> block could not be written to w. | ErrWritePartial 表示无法把渲染好的 <hx-partial> 区块写入 w。
var ErrWritePartial = errx.New("htmx: failed to write hx-partial | htmx：写出 hx-partial 失败")

// Partial describes one <hx-partial> block, the multi-region update mechanism preferred by Htmx 4. | Partial 描述一个 <hx-partial> 区块，即 Htmx 4 首选的多区域更新机制。
//
// A single response may carry several Partials, each specifying its own target and swap strategy, which is more explicit than hx-swap-oob. | 一个响应里可以携带多个 Partial，各自指定目标与交换策略，比 hx-swap-oob 更显式。
type Partial struct {
	// Target is a CSS selector such as "#messages"; an empty value omits hx-target | Target 是 CSS 选择器，如 "#messages"；为空时省略 hx-target
	// (Htmx 4 does not inherit attributes implicitly, so child elements must declare them or use the :inherited modifier). | （Htmx 4 不隐式继承属性，子元素需自行声明或使用 :inherited 修饰符）。
	Target string
	// Swap is the swap strategy, e.g. "beforeend" or "innerMorph"; an empty value omits hx-swap. | Swap 是交换策略，如 "beforeend"、"innerMorph"；为空时省略 hx-swap。
	Swap string
	// Content is the already-rendered HTML fragment, emitted verbatim (without escaping). | Content 是已渲染好的 HTML 片段，原样输出（不做转义）。
	Content template.HTML
}

// RenderPartials writes the <hx-partial> tags corresponding to parts, one after another. | RenderPartials 依次输出 parts 对应的 <hx-partial> 标签。
//
// Each item is written as one line: <hx-partial hx-target="…" hx-swap="…">content</hx-partial> followed by a newline; | 每一项输出为一行：<hx-partial hx-target="…" hx-swap="…">内容</hx-partial> 后接一个换行；
// an empty Target or Swap omits the corresponding attribute, attribute values are HTML-escaped, and Content is inserted verbatim. | Target 或 Swap 为空时省略对应属性，属性值经 HTML 转义，Content 原样插入。
// When parts is empty nothing is written; a failed write returns a wrapped error. | parts 为空时不写出任何内容；写入失败时返回包装后的错误。
func RenderPartials(w io.Writer, parts ...Partial) error {
	if len(parts) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, p := range parts {
		buf.WriteString("<hx-partial")
		if p.Target != "" {
			buf.WriteString(` hx-target="`)
			buf.WriteString(html.EscapeString(p.Target))
			buf.WriteByte('"')
		}
		if p.Swap != "" {
			buf.WriteString(` hx-swap="`)
			buf.WriteString(html.EscapeString(p.Swap))
			buf.WriteByte('"')
		}
		buf.WriteByte('>')
		buf.WriteString(string(p.Content))
		buf.WriteString("</hx-partial>\n")
	}

	if _, err := w.Write(buf.Bytes()); err != nil {
		return errx.Wrap(err, ErrWritePartial)
	}
	return nil
}

// OOB returns a compatibility fragment carrying hx-swap-oob (multi-region updates should prefer RenderPartials). | OOB 返回一段带 hx-swap-oob 的兼容片段（多区域更新优先使用 RenderPartials）。
//
// It is always wrapped in a div; the hx-swap-oob value rules are: when both swap and target are non-empty it is "swap:target", | 始终用 div 包裹；hx-swap-oob 的取值规则：swap 与 target 都非空时取 "swap:target"，
// when only target is set it is target, when only swap is set it is swap, and when both are empty it is "true" (matched by id). | 仅 target 时取 target，仅 swap 时取 swap，都为空时取 "true"（按 id 匹配）。
// content is emitted verbatim, without escaping. | content 原样输出，不做转义。
func OOB(target, swap string, content template.HTML) template.HTML {
	var spec string
	switch {
	case swap != "" && target != "":
		spec = swap + ":" + target
	case target != "":
		spec = target
	case swap != "":
		spec = swap
	default:
		spec = "true"
	}

	var b strings.Builder
	b.WriteString(`<div hx-swap-oob="`)
	b.WriteString(html.EscapeString(spec))
	b.WriteString(`">`)
	b.WriteString(string(content))
	b.WriteString("</div>")
	return template.HTML(b.String())
}

// ErrorFragment returns a reusable form error fragment: <div class="yd-form-error" role="alert">…</div>. | ErrorFragment 返回可复用的表单错误片段：<div class="yd-form-error" role="alert">…</div>。
//
// HTML special characters in msg are escaped, so it can safely carry user input or backend error messages, | msg 中的 HTML 特殊字符会被转义，因此可以安全承载用户输入或后端错误信息，
// and the returned template.HTML can be handed directly to a template for rendering. | 返回的 template.HTML 可直接交给模板渲染。
func ErrorFragment(msg string) template.HTML {
	return template.HTML(`<div class="yd-form-error" role="alert">` + html.EscapeString(msg) + `</div>`)
}

// TriggerFromCtx is a convenience wrapper around Trigger: it writes the HX-Trigger response header through router.Ctx. | TriggerFromCtx 是 Trigger 的便捷封装：通过 router.Ctx 写入 HX-Trigger 响应头。
//
// It must be called before the response headers are written; it does nothing when c is nil. | 必须在响应头写出之前调用；c 为 nil 时不做任何事。
func TriggerFromCtx(c *router.Ctx, events ...Event) {
	if c == nil {
		return
	}
	if v, ok := triggerHeaderValue(events); ok {
		c.SetHeader(headerTrigger, v)
	}
}

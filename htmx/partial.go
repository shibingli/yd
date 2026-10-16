package htmx

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"io"
	"strings"

	"github.com/yun-dev/yd/router"
)

// Partial 描述一个 <hx-partial> 区块，即 Htmx 4 首选的多区域更新机制。
//
// 一个响应里可以携带多个 Partial，各自指定目标与交换策略，比 hx-swap-oob 更显式。
type Partial struct {
	// Target 是 CSS 选择器，如 "#messages"；为空时省略 hx-target
	// （Htmx 4 不隐式继承属性，子元素需自行声明或使用 :inherited 修饰符）。
	Target string
	// Swap 是交换策略，如 "beforeend"、"innerMorph"；为空时省略 hx-swap。
	Swap string
	// Content 是已渲染好的 HTML 片段，原样输出（不做转义）。
	Content template.HTML
}

// RenderPartials 依次输出 parts 对应的 <hx-partial> 标签。
//
// 每一项输出为一行：<hx-partial hx-target="…" hx-swap="…">内容</hx-partial> 后接一个换行；
// Target 或 Swap 为空时省略对应属性，属性值经 HTML 转义，Content 原样插入。
// parts 为空时不写出任何内容；写入失败时返回包装后的错误。
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
		return fmt.Errorf("htmx: 写出 hx-partial 失败: %w", err)
	}
	return nil
}

// OOB 返回一段带 hx-swap-oob 的兼容片段（多区域更新优先使用 RenderPartials）。
//
// 始终用 div 包裹；hx-swap-oob 的取值规则：swap 与 target 都非空时取 "swap:target"，
// 仅 target 时取 target，仅 swap 时取 swap，都为空时取 "true"（按 id 匹配）。
// content 原样输出，不做转义。
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

// ErrorFragment 返回可复用的表单错误片段：<div class="yd-form-error" role="alert">…</div>。
//
// msg 中的 HTML 特殊字符会被转义，因此可以安全承载用户输入或后端错误信息，
// 返回的 template.HTML 可直接交给模板渲染。
func ErrorFragment(msg string) template.HTML {
	return template.HTML(`<div class="yd-form-error" role="alert">` + html.EscapeString(msg) + `</div>`)
}

// TriggerFromCtx 是 Trigger 的便捷封装：通过 router.Ctx 写入 HX-Trigger 响应头。
//
// 必须在响应头写出之前调用；c 为 nil 时不做任何事。
func TriggerFromCtx(c *router.Ctx, events ...Event) {
	if c == nil {
		return
	}
	if v, ok := triggerHeaderValue(events); ok {
		c.SetHeader(headerTrigger, v)
	}
}

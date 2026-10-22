// Package markdown renders message Markdown through goldmark with raw HTML disabled. | Package markdown
// 通过 goldmark 渲染消息 Markdown，并禁用原始 HTML。
//
// The renderer is deliberately conservative: raw HTML is omitted by goldmark's default HTML
// renderer, dangerous link schemes are rejected by it, and the plain-text projection is extracted
// from the same AST that produced the HTML, so search can never see text the reader cannot. |
// 渲染器刻意保守：goldmark 默认 HTML 渲染器会省略原始 HTML 并拒绝危险链接协议，纯文本投影取自同一棵
// AST，因此检索不可能看到读者看不到的文本。
package markdown

import (
	"bytes"
	"strings"

	"github.com/shibingli/ydx/errs"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// ErrRender reports a failed Markdown render. | ErrRender 表示 Markdown 渲染失败。
var ErrRender = errs.New("markdown render failed | Markdown 渲染失败")

// Renderer is the goldmark-backed implementation of messaging.Renderer. | Renderer 是 messaging.Renderer 的 goldmark 实现。
type Renderer struct {
	// engine is the shared goldmark instance. | engine 是共享的 goldmark 实例。
	engine goldmark.Markdown
}

// New builds the renderer with the GitHub-flavoured extensions messages typically use. |
// New 使用消息常用的 GitHub 风格扩展构建渲染器。
func New() *Renderer {
	return &Renderer{engine: goldmark.New(
		goldmark.WithExtensions(extension.GFM),
	)}
}

// Render converts Markdown into a sanitised HTML fragment and a plain-text projection. |
// Render 把 Markdown 转成净化后的 HTML 片段与纯文本投影。
func (r *Renderer) Render(source string) (string, string, error) {
	if r == nil {
		return "", "", ErrRender
	}
	raw := []byte(source)
	var buf bytes.Buffer
	if err := r.engine.Convert(raw, &buf); err != nil {
		return "", "", errs.Wrap(err, ErrRender)
	}
	return buf.String(), plainText(r.engine, raw), nil
}

// plainText extracts text nodes from the parsed Markdown. | plainText 从解析后的 Markdown 中抽取文本节点。
func plainText(engine goldmark.Markdown, source []byte) string {
	doc := engine.Parser().Parse(text.NewReader(source))
	var b strings.Builder
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := node.(type) {
		case *ast.Text:
			b.Write(n.Segment.Value(source))
			if n.SoftLineBreak() || n.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(n.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

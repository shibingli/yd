// Package markdown renders CMS Markdown through goldmark with raw HTML disabled. | Package markdown
// 通过 goldmark 渲染 CMS Markdown，并禁用原始 HTML。
//
// The renderer is deliberately conservative: raw HTML is omitted by goldmark's default HTML
// renderer, dangerous link schemes are rejected by it, and the plain-text projection is extracted
// from the same AST that produced the HTML. GFM tables, task lists and footnotes are enabled, and
// headings receive stable identifiers for the public table of contents. | 渲染器刻意保守：
// goldmark 默认 HTML 渲染器会省略原始 HTML 并拒绝危险链接协议，纯文本投影取自同一棵 AST。
// 启用了 GFM 表格、任务列表与脚注，标题获得稳定标识，供公开站点目录使用。
package markdown

import (
	"bytes"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// ErrRender reports a failed Markdown render. | ErrRender 表示 Markdown 渲染失败。
var ErrRender = errx.New("markdown render failed | Markdown 渲染失败")

// Renderer is the goldmark-backed implementation of cms.Renderer. | Renderer 是 cms.Renderer 的 goldmark 实现。
type Renderer struct {
	// engine is the shared goldmark instance. | engine 是共享的 goldmark 实例。
	engine goldmark.Markdown
}

// New builds the renderer with the extensions CMS content uses. | New 使用 CMS 内容所需的扩展构建渲染器。
func New() *Renderer {
	return &Renderer{engine: goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Footnote),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
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
		return "", "", errx.Wrap(err, ErrRender)
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
		case *ast.FencedCodeBlock:
			// 围栏代码的内容保存在 Lines()，不是 Text 节点；漏掉它会让纯文本投影与检索丢失整段代码。 |
			// Fenced code lives in Lines(), not in Text nodes; missing it drops whole code blocks from the plain-text
			// projection and from search.
			writeLines(&b, n.Lines(), source)
		case *ast.CodeBlock:
			writeLines(&b, n.Lines(), source)
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

// writeLines appends every line of a code block to the projection. | writeLines 把代码块的每一行追加到投影。
func writeLines(b *strings.Builder, lines *text.Segments, source []byte) {
	if lines == nil {
		return
	}
	for i := 0; i < lines.Len(); i++ {
		segment := lines.At(i)
		b.Write(segment.Value(source))
		b.WriteByte(' ')
	}
}

package render

// Standard-library fallback. | 标准库回退。
//
// Most entries compile to the fast executor. A template that html/template had
// to clone into a non-text context (an attribute or script fragment, for
// example) only exists inside the standard library's internal text/template
// namespace, which the exported API cannot reach. Those entries keep the
// compiled engine's full semantics by delegating execution to html/template:
// a pre-escaped clone serves renders without per-request bindings, and a fresh
// clone serves renders that bind a translator or an authority. | 多数入口会编译为
// 快速执行器。被 html/template 克隆到非文本上下文（例如属性或脚本片段）的模板只存在于
// 标准库内部的 text/template 命名空间，导出 API 无法访问。这些入口通过委托给
// html/template 来保持编译引擎的完整语义：无按请求绑定时复用已转义克隆，有译者或判定
// 主体绑定时克隆原始集合再执行。

import (
	"bytes"
	"html/template"
	"io"

	"github.com/shibingli/yd/core/errx"
)

// fallbackProgram executes one entry through html/template. | fallbackProgram 通过 html/template 执行一个入口。
type fallbackProgram struct {
	master  *template.Template
	escaped *template.Template
	funcs   template.FuncMap
	owned   map[string]bool
	name    string
}

// fallback renders the entry directly to w. | fallback 把入口直接渲染到 w。
func (f *fallbackProgram) fallback(w io.Writer, data any) error {
	tr := translatorFrom(data)
	az := authzFrom(data)
	if tr == nil && az == nil {
		return f.execute(f.escaped, w, data)
	}
	clone, err := f.master.Clone()
	if err != nil {
		return errx.Wrap(err, ErrCloneTemplate)
	}
	if b := bindingsFor(tr, az, f.owned, f.funcs); len(b) > 0 {
		clone.Funcs(b)
	}
	return f.execute(clone, w, data)
}

// fallbackAppend renders the entry and appends it to dst. | fallbackAppend 渲染入口并追加到 dst。
func (f *fallbackProgram) fallbackAppend(dst []byte, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := f.fallback(&buf, data); err != nil {
		return dst, err
	}
	return append(dst, buf.Bytes()...), nil
}

// execute runs one template set and wraps its failure. | execute 执行一个模板集并包装失败。
func (f *fallbackProgram) execute(t *template.Template, w io.Writer, data any) error {
	if err := t.ExecuteTemplate(w, f.name, data); err != nil {
		return errx.WrapDetail(err, ErrExecuteTemplate, "name=%[1]s | 名称=%[1]s", f.name)
	}
	return nil
}

// bindingsFor maps the per-execution translator and authority onto the helper
// names, honouring caller overrides registered through Options.Funcs. | bindingsFor
// 把按次执行的译者与判定主体映射到辅助函数名，并尊重经 Options.Funcs 注册的调用方覆盖。
func bindingsFor(tr Translator, az Authz, owned map[string]bool, funcs template.FuncMap) template.FuncMap {
	var out template.FuncMap
	if tr != nil && !owned["tr"] {
		out = template.FuncMap{"tr": translatorFunc(tr)}
	}
	if az != nil {
		for name, fn := range authzFuncs(az) {
			if owned[name] {
				continue
			}
			if out == nil {
				out = template.FuncMap{}
			}
			out[name] = fn
		}
	}
	_ = funcs
	return out
}

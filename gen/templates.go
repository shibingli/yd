package gen

import (
	"bytes"
	"embed"
	"sync"
	"text/template"

	"github.com/shibingli/yd/errx"
)

// artifactTemplates 内嵌全部生成物模板。
// artifactTemplates embeds every artifact template.
//
// 模板放在 .tmpl 文件里而不是 Go 字符串常量里，原因有二：生成 Go 代码需要反引号
// 结构体标签（无法写进 Go 原始字符串），并且仓库守卫只扫描 .go 文件，模板文本得以
// 保持原样。
// The templates live in .tmpl files rather than Go string constants for two reasons:
// generated Go code needs backticked struct tags (impossible inside a Go raw string),
// and the repository guards only scan .go files, so the template text stays intact.
//
//go:embed templates/*.tmpl
var artifactTemplates embed.FS

// templatePrefix 是模板在 embed.FS 与 template.Template 中的路径前缀。
// templatePrefix is the path prefix of templates inside the embed.FS and the template.Template.
const templatePrefix = "templates/"

// 生成期模板使用 <% %> 作为动作分隔符，生成物里的 {{ }} 留给运行期 html/template。
// Generation-time templates use <% %> as their action delimiters so the {{ }}
// inside artifacts stays untouched for the runtime html/template engine.
const (
	leftDelim  = "<%"
	rightDelim = "%>"
)

// loadTemplates 惰性解析内嵌模板，进程内只解析一次。
// loadTemplates parses the embedded templates lazily, once per process.
var loadTemplates = sync.OnceValues(func() (*template.Template, error) {
	t, err := template.New("yd-gen").
		Delims(leftDelim, rightDelim).
		ParseFS(artifactTemplates, templatePrefix+"*.tmpl")
	if err != nil {
		return nil, errx.WrapDetail(err, ErrRender, "parse embedded templates | 解析内嵌模板失败")
	}
	return t, nil
})

// renderTemplate 渲染名为 name 的内嵌模板，name 形如 "model.go.tmpl"。
// renderTemplate renders the embedded template named name, shaped like "model.go.tmpl".
//
// ParseFS 以文件名（不含目录）作为模板名，因此这里与子模板互相引用时都用同一口径的名字。
// ParseFS names a template after its file name without the directory, so the same bare name
// is used here and wherever templates include one another.
func renderTemplate(name string, data any) ([]byte, error) {
	t, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, errx.WrapDetail(err, ErrRender, "template=%s", name)
	}
	return buf.Bytes(), nil
}

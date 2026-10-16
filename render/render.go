// Package render loads html/template sets from an afero filesystem. | Package render 从 afero 文件系统加载 html/template 模板集。
//
// Templates are addressed by their slash-separated path relative to the scan
// root, for example "layouts/base.html" or "pages/users.html". Every file
// under the root is parsed into one template set, so partials can be invoked
// by name from any page. Dev mode skips the cache so template edits take
// effect without a restart, which is what `yd dev` relies on. | 模板以相对扫描根的
// 斜杠路径寻址，例如 "layouts/base.html" 或 "pages/users.html"。根目录下的每个文件
// 都会被解析进同一个模板集，因此任意页面都可按名称调用局部模板。Dev 模式跳过缓存，
// 模板改动无需重启即可生效，`yd dev` 依赖该行为。
package render

import (
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"text/template"

	"github.com/spf13/afero"

	"github.com/yun-dev/yd/afs"
	"github.com/yun-dev/yd/errx"
)

// Sentinel errors. | 哨兵错误。
var (
	// ErrNoTemplates reports that the scan found no template files. | ErrNoTemplates 表示扫描未找到任何模板文件。
	ErrNoTemplates = errx.New("no templates found | 未找到任何模板文件")
	// ErrTemplateNotFound reports an unknown template name. | ErrTemplateNotFound 表示模板名不存在。
	ErrTemplateNotFound = errx.New("template not found | 模板不存在")
	// ErrNilFS reports a missing filesystem. | ErrNilFS 表示未提供文件系统。
	ErrNilFS = errx.New("nil filesystem | 文件系统为 nil")
)

// Options configures an Engine. | Options 配置渲染引擎。
type Options struct {
	// Root is the directory scanned for templates; empty means "templates". | Root 是模板扫描目录，空表示 "templates"。
	Root string
	// Exts lists the file extensions to parse; empty means [".html", ".gotmpl"]. | Exts 列出参与解析的扩展名，空表示 [".html", ".gotmpl"]。
	Exts []string
	// Dev disables caching so edits take effect immediately. | Dev 关闭缓存，使改动立即生效。
	Dev bool
	// Funcs are extra template functions, merged over the defaults. | Funcs 是额外的模板函数，会覆盖同名默认函数。
	Funcs template.FuncMap
	// LeftDelim and RightDelim override the default action delimiters. | LeftDelim 与 RightDelim 覆盖默认的动作分隔符。
	LeftDelim  string
	RightDelim string
}

func (o Options) normalize() Options {
	if o.Root == "" {
		o.Root = "templates"
	}
	o.Root = strings.Trim(strings.ReplaceAll(o.Root, "\\", "/"), "/")
	if len(o.Exts) == 0 {
		o.Exts = []string{".html", ".gotmpl"}
	}
	return o
}

// Engine renders templates from a filesystem. | Engine 从文件系统渲染模板。
//
// It is safe for concurrent use. | 可安全并发使用。
type Engine struct {
	fsys afero.Fs
	opts Options

	mu    sync.RWMutex
	tmpl  *template.Template
	names []string
}

// New loads every template under the configured root. | New 加载配置根目录下的全部模板。
func New(fsys afero.Fs, o Options) (*Engine, error) {
	if fsys == nil {
		return nil, ErrNilFS
	}
	e := &Engine{fsys: fsys, opts: o.normalize()}
	if !e.opts.Dev {
		if err := e.load(); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// Filesystem returns the underlying filesystem. | Filesystem 返回底层文件系统。
func (e *Engine) Filesystem() afero.Fs { return e.fsys }

// Options returns the normalized options. | Options 返回规范化后的配置。
func (e *Engine) Options() Options { return e.opts }

// FuncMap returns the effective template functions. | FuncMap 返回生效的模板函数表。
func (e *Engine) FuncMap() template.FuncMap {
	m := Funcs(e.fsys)
	for k, v := range e.opts.Funcs {
		m[k] = v
	}
	return m
}

// Clone returns an independent engine sharing the same filesystem and options. | Clone 返回共享文件系统与配置的独立引擎。
func (e *Engine) Clone() *Engine {
	c := &Engine{fsys: e.fsys, opts: e.opts}
	if !c.opts.Dev {
		_ = c.load()
	}
	return c
}

// AddFunc registers (or replaces) a template function and reloads. | AddFunc 注册（或替换）模板函数并重新加载。
//
// It returns a new Engine so shared instances are never mutated in place. | 返回新引擎，避免就地修改共享实例。
func (e *Engine) AddFunc(name string, fn any) *Engine {
	c := e.Clone()
	if c.opts.Funcs == nil {
		c.opts.Funcs = template.FuncMap{}
	}
	c.opts.Funcs[name] = fn
	if !c.opts.Dev {
		_ = c.load()
	}
	return c
}

// Names returns the sorted template names. | Names 返回排序后的模板名列表。
func (e *Engine) Names() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, len(e.names))
	copy(out, e.names)
	return out
}

// templateFile pairs a template name with the path it is read from. | templateFile 把模板名与读取路径配对。
//
// The name is relative to the scan root ("layouts/base.html") while the path
// is the real location in the filesystem ("templates/layouts/base.html"). | 名字相对
// 扫描根（"layouts/base.html"），路径是文件系统中的真实位置（"templates/layouts/base.html"）。
type templateFile struct {
	name string
	path string
}

// load parses every matching file into one template set. | load 把全部匹配文件解析进同一个模板集。
func (e *Engine) load() error {
	files, err := e.collect()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errx.Detail(ErrNoTemplates, "root=%s", e.opts.Root)
	}

	tmpl := template.New("").Funcs(e.FuncMap())
	if e.opts.LeftDelim != "" || e.opts.RightDelim != "" {
		tmpl = tmpl.Delims(e.opts.LeftDelim, e.opts.RightDelim)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		b, err := afero.ReadFile(e.fsys, f.path)
		if err != nil {
			return errx.WrapDetail(err, ErrReadTemplate, "path=%s", f.path)
		}
		if _, err := tmpl.New(f.name).Parse(string(b)); err != nil {
			return errx.WrapDetail(err, ErrParseTemplate, "name=%s", f.name)
		}
		names = append(names, f.name)
	}

	e.mu.Lock()
	e.tmpl = tmpl
	e.names = names
	e.mu.Unlock()
	return nil
}

// collect walks the root and returns matching templates in a stable order. | collect 遍历根目录并按稳定顺序返回匹配的模板。
func (e *Engine) collect() ([]templateFile, error) {
	var files []templateFile
	err := afs.Walk(e.fsys, e.opts.Root, func(p string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel := toSlash(p)
		base := path.Base(rel)
		if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
			return nil // 点文件与下划线前缀文件视为局部片段，不作为入口 | dot/underscore files are partials
		}
		if !e.matchExt(rel) {
			return nil
		}
		files = append(files, templateFile{name: trimRoot(rel, e.opts.Root), path: rel})
		return nil
	})
	if err != nil {
		return nil, errx.Wrap(err, ErrWalkTemplates)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	return files, nil
}

func (e *Engine) matchExt(p string) bool {
	for _, ext := range e.opts.Exts {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

// Render executes the named template into w. | Render 把指定名称的模板渲染到 w。
func (e *Engine) Render(w io.Writer, name string, data any) error {
	if e.opts.Dev {
		if err := e.load(); err != nil {
			return err
		}
	}
	e.mu.RLock()
	tmpl := e.tmpl
	e.mu.RUnlock()
	if tmpl == nil {
		return errx.Detail(ErrTemplateNotFound, "name=%s", name)
	}
	if tmpl.Lookup(name) == nil {
		return errx.Detail(ErrTemplateNotFound, "name=%s", name)
	}
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		return errx.WrapDetail(err, ErrExecuteTemplate, "name=%s", name)
	}
	return nil
}

// Exists reports whether a template name is known. | Exists 报告模板名是否为已知模板。
func (e *Engine) Exists(name string) bool {
	if e.opts.Dev {
		_ = e.load()
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.tmpl != nil && e.tmpl.Lookup(name) != nil
}

func toSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// trimRoot strips the scan root prefix so template names stay portable. | trimRoot 去掉扫描根前缀，使模板名保持可移植。
func trimRoot(p, root string) string {
	p = toSlash(p)
	root = toSlash(root)
	if root == "" || root == "." {
		return strings.TrimPrefix(p, "./")
	}
	return strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
}

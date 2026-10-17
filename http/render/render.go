// Package render loads html/template sets from an afero filesystem. | Package render 从 afero 文件系统加载 html/template 模板集。
//
// This engine is built on html/template, not text/template: every value
// interpolated into a template is contextually escaped, which is what makes it
// safe to render user-supplied data. Values that must reach the page verbatim
// have to be typed template.HTML explicitly, so escaping is always an opt-out
// rather than the default. | 本引擎基于 html/template 而非 text/template：所有插入
// 模板的值都会按上下文转义，这正是渲染用户数据时的安全前提。需要原样输出的值必须
// 显式声明为 template.HTML，即转义永远是「显式关闭」而非默认行为。
//
// Templates are addressed by their slash-separated path relative to the scan
// root, for example "layouts/base.html" or "pages/users.html". Every file
// under the root is parsed into one template set, so partials can be invoked
// by name from any page. Dev mode skips the cache so template edits take
// effect without a restart, which is what `yd dev` relies on. | 模板以相对扫描根的
// 斜杠路径寻址，例如 "layouts/base.html" 或 "pages/users.html"。根目录下的每个文件
// 都会被解析进同一个模板集，因此任意页面都可按名称调用局部模板。Dev 模式跳过缓存，
// 模板改动无需重启即可生效，`yd dev` 依赖该行为。
//
// User-facing text is written as `{{ tr "some.key" }}`. The engine ships no
// catalog of its own: a translator is injected through the render data under
// TranslatorKey, and tr returns the key when none is injected. | 面向用户的文案写作
// `{{ tr "some.key" }}`。引擎本身不附带任何语言目录：译者通过渲染数据中的
// TranslatorKey 键注入，未注入时 tr 返回键本身。
//
//	data := map[string]any{render.TranslatorKey: translator, "Users": users}
//	if err := engine.Render(w, "pages/users.html", data); err != nil { ... }
//
// The value under TranslatorKey must satisfy Translator, as i18n.Translator
// does; the app facade fills the key in for every request. | TranslatorKey 下的值
// 必须满足 Translator，i18n.Translator 即如此；app 门面会为每个请求填入该键。
//
// Permissions work the same way: a value satisfying Authz under AuthzKey makes
// `can`, `hasRole`, `perm` and `role` decide for that one execution, and the
// authz package builds that value - together with the maps a template reads as
// .Perm and .Role and the JSON payload of the front end - in one call. | 权限同理：
// AuthzKey 下满足 Authz 的值会让 `can`、`hasRole`、`perm` 与 `role` 在该次执行中作出判定，
// 而 authz 包用一次调用就构造出该值——连同模板以 .Perm 与 .Role 读取的映射、以及前端所需的 JSON 载荷。
package render

import (
	"html/template"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/errx"
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
	// Funcs are extra template functions, merged over the defaults. | Funcs 是额外的模板函数，会覆盖同名默认函数。
	Funcs template.FuncMap
	// Root is the directory scanned for templates; empty means "templates". | Root 是模板扫描目录，空表示 "templates"。
	Root string
	// LeftDelim and RightDelim override the default action delimiters. | LeftDelim 与 RightDelim 覆盖默认的动作分隔符。
	LeftDelim  string
	RightDelim string
	// Exts lists the file extensions to parse; empty means [".html", ".gotmpl"]. | Exts 列出参与解析的扩展名，空表示 [".html", ".gotmpl"]。
	Exts []string
	// Dev disables caching so edits take effect immediately. | Dev 关闭缓存，使改动立即生效。
	Dev bool
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
	tmpl *template.Template

	// source is the pristine parse result: it is never executed, so it can be
	// cloned for every execution that needs its own template functions.
	// | source 是未经执行的解析结果：它从不被执行，因此可为每次需要独立模板函数的
	// 执行克隆一次。
	source *template.Template

	names []string

	opts Options

	mu sync.RWMutex
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
		return errx.Detail(ErrNoTemplates, "root=%[1]s | 根目录=%[1]s", e.opts.Root)
	}

	tmpl := template.New("").Funcs(e.FuncMap())
	if e.opts.LeftDelim != "" || e.opts.RightDelim != "" {
		tmpl = tmpl.Delims(e.opts.LeftDelim, e.opts.RightDelim)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		b, err := afero.ReadFile(e.fsys, f.path)
		if err != nil {
			return errx.WrapDetail(err, ErrReadTemplate, "path=%[1]s | 路径=%[1]s", f.path)
		}
		if _, err := tmpl.New(f.name).Parse(string(b)); err != nil {
			return errx.WrapDetail(err, ErrParseTemplate, "name=%[1]s | 名称=%[1]s", f.name)
		}
		names = append(names, f.name)
	}

	// tmpl stays pristine: it is the clone source of every execution that needs
	// its own template functions. html/template rewrites the parse trees while
	// escaping and refuses to clone a set that has been executed, so the set
	// executed as is has to be a twin of the source. | tmpl 保持原始：它是每次需要
	// 独立模板函数的执行的克隆源。html/template 在转义时会改写解析树，并拒绝克隆已执行过的
	// 集合，因此直接执行的那个集合必须是原始集合的孪生副本。
	source := tmpl
	executed, err := source.Clone()
	if err != nil {
		return errx.WrapDetail(err, ErrCloneTemplate, "root=%[1]s | 根目录=%[1]s", e.opts.Root)
	}

	e.mu.Lock()
	e.tmpl = executed
	e.source = source
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
//
// A translator injected into a map[string]any data value under TranslatorKey
// (see Translator) is bound to `tr` for this execution only, and an authority
// injected under AuthzKey (see Authz) is bound to `can`, `hasRole`, `perm` and
// `role` the same way. | 通过 TranslatorKey 键注入 map[string]any 型数据的译者（见 Translator）
// 仅对本次执行绑定到 `tr`；通过 AuthzKey 注入的判定主体（见 Authz）以同样的方式绑定到
// `can`、`hasRole`、`perm` 与 `role`。
func (e *Engine) Render(w io.Writer, name string, data any) error {
	if e.opts.Dev {
		if err := e.load(); err != nil {
			return err
		}
	}
	tmpl, err := e.executionSet(data)
	if err != nil {
		return err
	}
	if tmpl == nil || tmpl.Lookup(name) == nil {
		return errx.Detail(ErrTemplateNotFound, "name=%[1]s | 名称=%[1]s", name)
	}
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		return errx.WrapDetail(err, ErrExecuteTemplate, "name=%[1]s | 名称=%[1]s", name)
	}
	return nil
}

// executionSet returns the template set data must be executed with. | executionSet 返回执行 data 应使用的模板集。
//
// Without any injected authority it hands back the cached set as is, which is the path every
// render without i18n and without permissions takes. | 未注入任何判定主体时原样返回缓存集合，
// 这是所有既未启用 i18n 也无权限判定的渲染所走的路径。
//
// With one it clones the pristine set and rebinds the helpers that authority owns - "tr" for a
// translator, "can"/"hasRole"/"perm"/"role" for an Authz - for this execution only: a template set
// is shared between goroutines and its function map cannot be swapped after escaping, so a
// per-execution copy is the only safe way to give two concurrent requests two different subjects. |
// 注入判定主体时则克隆原始集合，并仅对本次执行重新绑定该主体拥有的辅助函数——译者绑定 "tr"，
// Authz 绑定 "can"/"hasRole"/"perm"/"role"：模板集在多个 goroutine 间共享，且其函数表在转义后无法替换，
// 因此「每次执行一份副本」是让两个并发请求使用两个不同主体的唯一安全做法。
//
// A helper registered through Options.Funcs wins over the binding of the same name. | 经
// Options.Funcs 注册的同名辅助函数优先于该绑定。
func (e *Engine) executionSet(data any) (*template.Template, error) {
	e.mu.RLock()
	executed, source := e.tmpl, e.source
	e.mu.RUnlock()

	bindings := e.bindings(data)
	if len(bindings) == 0 || source == nil {
		return executed, nil
	}

	bound, err := source.Clone()
	if err != nil {
		return nil, errx.Wrap(err, ErrCloneTemplate)
	}
	return bound.Funcs(bindings), nil
}

// bindings returns the per-execution functions data asks for. | bindings 返回 data 请求的按次执行函数。
//
// It is the single place that decides what a data value can rebind, and it returns nil - not an
// empty map - when there is nothing to bind, so the common path allocates nothing. | 这里是决定
// 「一份数据能重绑什么」的唯一位置；没有可绑定项时返回 nil（而不是空映射），因此常见路径不产生任何分配。
func (e *Engine) bindings(data any) template.FuncMap {
	var bindings template.FuncMap
	if t := translatorFrom(data); t != nil && !e.owns("tr") {
		bindings = template.FuncMap{"tr": translatorFunc(t)}
	}
	if a := authzFrom(data); a != nil {
		for name, fn := range authzFuncs(a) {
			if e.owns(name) {
				continue
			}
			if bindings == nil {
				bindings = template.FuncMap{}
			}
			bindings[name] = fn
		}
	}
	return bindings
}

// owns reports whether the engine registered its own function under name. | owns 报告引擎是否以该名字注册了自己的函数。
func (e *Engine) owns(name string) bool {
	_, ok := e.opts.Funcs[name]
	return ok
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

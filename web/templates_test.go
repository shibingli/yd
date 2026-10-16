/*
templates_test.go — contract tests for the admin templates, Task 37.
templates_test.go — 管理后台模板的契约测试（任务 37）。

The tests render every page through the same html/template pipeline the render package uses and
then assert the promises the front-end makes: only local scripts in one single order, htmx 4
attributes on every interaction, one data-yd-i18n key per user-facing string, and the catalog
envelope injected into the layout.
这些测试用与 render 包相同的 html/template 流水线渲染每个页面，随后断言前端做出的承诺：
只装载本地脚本且顺序唯一、每个交互都使用 htmx 4 属性、每条面向用户的文案都带一个
data-yd-i18n 键、以及布局中注入的目录信封。

Templates are read from the operating system file system because `go test ./web/` runs with web/
as the working directory; the embedded copy is the render package's concern.
模板从操作系统文件系统读取，因为 `go test ./web/` 以 web/ 为工作目录；内嵌副本由 render 包负责。
*/
package web

import (
	"bytes"
	"errors"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/yun-dev/yd/jsonx"
)

// Chrome pages render the dashboard shell; standalone pages must not. | 带外壳的页面渲染管理后台骨架；独立页面不得渲染它。
var chromePages = []string{"dashboard", "pages/users.html", "pages/settings.html", "pages/realtime.html", "pages/i18n_demo.html"}

// standalonePages are the two pages that must work without any shell. | 两个必须脱离外壳独立工作的页面。
var standalonePages = []string{"pages/login.html", "pages/error.html"}

// scriptsInOrder is the single allowed script order of the layout. | 布局唯一允许的脚本顺序。
var scriptsInOrder = []string{
	"/static/js/vendor/htmx.min.js",
	"/static/js/vendor/hx-head.min.js",
	"/static/js/vendor/hx-targets.min.js",
	"/static/js/vendor/hx-upsert.min.js",
	"/static/js/vendor/hx-pending.min.js",
	"/static/js/vendor/hx-preload.min.js",
	"/static/js/vendor/hx-multipart.min.js",
	"/static/js/vendor/hx-download.min.js",
	"/static/js/vendor/hx-sse.min.js",
	"/static/js/vendor/hx-ws.min.js",
	"/static/js/vendor/alpine.min.js",
	"/static/js/vendor/hx-alpine-compat.min.js",
	"/static/js/yd-guomi.js",
	"/static/js/yd-guomi-htmx.js",
	"/static/js/yd-i18n.js",
	"/static/js/yd-app.js",
}

var (
	scriptSrcRe   = regexp.MustCompile(`<script[^>]*\ssrc="([^"]+)"`)
	scriptTagRe   = regexp.MustCompile(`(?s)<script\b[^>]*>`)
	formTagRe     = regexp.MustCompile(`(?s)<form\b[^>]*>`)
	catalogRe     = regexp.MustCompile(`(?s)<script id="yd-i18n-catalog" type="application/json">(.*?)</script>`)
	i18nKeyRe     = regexp.MustCompile(`data-yd-i18n="([^"]*)"`)
	i18nAttrRe    = regexp.MustCompile(`data-yd-i18n-attr="([^"]*)"`)
	i18nKeySyntax = regexp.MustCompile(`^[A-Za-z0-9]+(\.[A-Za-z0-9_-]+)+$`)
	colorRe       = regexp.MustCompile(`style="[^"]*(#[0-9a-fA-F]{3,8}|rgba?\(|hsla?\()`)
	forbiddenRefs = []string{
		"http://",
		"https://",
		"//cdn.",
		"cdn.jsdelivr.net",
		"unpkg.com",
		"cdnjs.cloudflare.com",
		"node_modules",
		"package.json",
		".tailwindcss",
		"integrity=",
	}
)

// templateCatalog is a two-language stand-in for the server side i18n catalogs.
// templateCatalog 是服务端 i18n 目录的两语言替身。
var templateCatalog = map[string]string{
	"app.name":                 "yd",
	"nav.dashboard":            "Dashboard",
	"nav.users":                "Users",
	"topbar.home":              "Home",
	"login.title":              "Sign in",
	"i18n.sample.interpolated": "Hello",
}

// templateFuncs returns the helpers the templates call; it mirrors render.Funcs without importing
// the render package, which is still under construction.
// templateFuncs 返回模板调用的辅助函数；它对应 render.Funcs，但不导入仍在开发中的 render 包。
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		// tr resolves a key from the fixture catalog and returns the key itself when it is missing.
		// tr 从固定目录解析键，缺失时返回键本身。
		"tr": func(key string, args ...any) string {
			if msg, ok := templateCatalog[key]; ok {
				return msg
			}
			return key
		},
		// dict builds the attribute maps handed to partials, like the render helper of the same name.
		// dict 构建传给局部模板的属性映射，与 render 中同名辅助函数一致。
		"dict": func(pairs ...any) (map[string]any, error) {
			if len(pairs)%2 != 0 {
				return nil, errors.New("dict needs an even number of arguments | dict 需要偶数个参数")
			}
			out := make(map[string]any, len(pairs)/2)
			for i := 0; i < len(pairs); i += 2 {
				key, ok := pairs[i].(string)
				if !ok {
					return nil, errors.New("dict keys must be strings | dict 的键必须是字符串")
				}
				out[key] = pairs[i+1]
			}
			return out, nil
		},
		// list builds the slices handed to partials. | list 构建传给局部模板的切片。
		"list": func(items ...any) []any { return items },
	}
}

// templateData is the fixture every page is rendered with; it fills every branch the templates have.
// templateData 是渲染每个页面所用的固定数据；它覆盖模板的所有分支。
func templateData() map[string]any {
	return map[string]any{
		"Title": "Dashboard",
		"Lang":  "en",
		"Theme": "light",
		// I18nJSON is template.JS: the script tag is a JavaScript container, so any other type
		// would be json.Marshal'ed into a quoted string instead of a raw object.
		// I18nJSON 用 template.JS：该脚本标签是 JavaScript 容器，其它类型会被 json.Marshal
		// 成带引号的字符串而不是原始对象。
		"I18nJSON": template.JS(`{"lang":"en","fallback":"zh-CN","catalogs":{"en":{"app":{"name":"yd"}},"zh-CN":{"app":{"name":"yd"}}}}`),
		"Flashes": []map[string]any{
			{"Level": "success", "Key": "users.saved", "Message": "Saved"},
			{"Level": "error", "Key": "", "Message": "Boom"},
		},
		"Breadcrumbs": []map[string]any{
			{"Key": "nav.users", "Label": "Users", "Href": "/users"},
			{"Key": "users.title", "Label": "Users", "Href": ""},
		},
		"User":  map[string]any{"Name": "Yun", "Email": "yun@example.com", "Role": "admin", "Initials": "Y"},
		"Langs": []string{"zh-CN", "en"},
		"Stats": []map[string]any{
			{"Key": "dashboard.kpi.users", "Label": "Users", "Value": "42", "TrendKey": "dashboard.kpi.users_trend", "Trend": "+3%"},
		},
		"Users": []map[string]any{
			{
				"ID": "1", "Initials": "Y", "Name": "Yun", "NameKey": "users.name",
				"Email": "yun@example.com", "EmailKey": "users.email",
				"Role": "admin", "RoleKey": "users.role.admin",
				"Status": "active", "StatusKey": "users.status.active",
			},
		},
		"Pagination": map[string]any{
			"Page": 1, "Pages": 3, "Total": 42,
			"Items": []map[string]any{
				{"Number": 1, "Href": "/users?page=1", "Current": true},
				{"Number": 2, "Href": "/users?page=2", "Current": false},
			},
		},
		"Errors": []map[string]any{{"Key": "users.error.name", "Message": "Name is required"}},
		"Settings": map[string]any{
			"Timezone": "Asia/Shanghai", "Theme": "system", "Lang": "en",
			"NotifyEmail": true, "NotifyRealtime": true,
		},
		"Range": map[string]any{"From": "2026-10-09", "To": "2026-10-16"},
		"Error": map[string]any{"Status": 500, "TitleKey": "error.title", "Title": "Error", "MessageKey": "error.server", "Message": "Server error", "Detail": "trace"},
	}
}

// templateFiles lists every template source file, sorted for a deterministic report.
// templateFiles 列出全部模板源文件，并排序以保证报告稳定。
func templateFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir("templates", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		files = append(files, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no templates found under web/templates | 在 web/templates 下没有找到模板")
	}
	sort.Strings(files)
	return files
}

// loadTemplates parses the whole tree into one template set, exactly like the render package does.
// loadTemplates 把整棵目录树解析进同一个模板集合，与 render 包的做法一致。
func loadTemplates(t *testing.T) *template.Template {
	t.Helper()
	tmpl := template.New("yd").Funcs(templateFuncs())
	for _, file := range templateFiles(t) {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if _, err := tmpl.New(file).Parse(string(raw)); err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
	}
	return tmpl
}

// definedNameRe extracts the quoted names of the list reported by DefinedTemplates.
// definedNameRe 提取 DefinedTemplates 所报告列表中的带引号模板名。
var definedNameRe = regexp.MustCompile(`"([^"]+)"`)

// definedNames lists the content templates of the set: html/template renders DefinedTemplates as
// the sentence `defined templates are: "a", "b"`, while text/template uses ";" separators, so both
// shapes are understood here.
// definedNames 列出集合中的内容模板：html/template 把 DefinedTemplates 渲染成
// `defined templates are: "a", "b"` 这样的句子，而 text/template 用 ";" 分隔，因此两种形式都兼容。
func definedNames(tmpl *template.Template) []string {
	raw := tmpl.DefinedTemplates()
	candidates := []string{}
	for _, match := range definedNameRe.FindAllStringSubmatch(raw, -1) {
		candidates = append(candidates, match[1])
	}
	if len(candidates) == 0 {
		candidates = strings.Split(raw, ";")
	}

	var names []string
	seen := map[string]bool{}
	for _, name := range candidates {
		name = strings.Trim(strings.TrimSpace(name), `"`)
		// File level shells (parse-time names carrying the source path) hold no content.
		// 文件级壳模板（解析时带源路径的名字）没有内容。
		if name == "" || strings.HasSuffix(name, ".html") || seen[name] {
			continue
		}
		if tmpl.Lookup(name) == nil {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// execute renders one named template with the fixture and fails the test on any error.
// execute 用固定数据渲染指定名称的模板，出错即让测试失败。
func execute(t *testing.T, tmpl *template.Template, name string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, templateData()); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return buf.String()
}

// TestAllTemplatesParse proves the whole tree parses and executes as one set, which is the
// precondition for every other promise in this file.
// TestAllTemplatesParse 证明整棵目录树能作为同一集合解析并执行，这是本文件其它承诺的前提。
func TestAllTemplatesParse(t *testing.T) {
	tmpl := loadTemplates(t)

	required := []string{
		"base.head", "base.scripts",
		"dashboard", "dashboard.open", "dashboard.close",
		"partials/sidebar", "partials/topbar", "partials/flash", "partials/confirm",
		"partials/table", "partials/pagination", "partials/skeleton", "partials/skeleton.cards",
		"partials/form.errors", "partials/form.field", "partials/form.select",
		"partials/form.check", "partials/form.actions",
		"partials/modal.open", "partials/modal.close",
		"pages/login.html", "pages/dashboard.html", "pages/dashboard.content",
		"pages/users.html", "pages/users.content",
		"pages/settings.html", "pages/settings.content",
		"pages/realtime.html", "pages/realtime.content",
		"pages/i18n_demo.html", "pages/i18n_demo.content",
		"pages/error.html",
	}
	for _, name := range required {
		if tmpl.Lookup(name) == nil {
			t.Errorf("template %q is missing | 缺少模板 %q", name, name)
		}
	}

	// Every template must also execute: escaping errors only appear at execution time.
	// 每个模板还必须能执行：转义错误只在执行期才会暴露。
	for _, name := range definedNames(tmpl) {
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, name, templateData()); err != nil {
			t.Errorf("execute %s: %v", name, err)
		}
	}
}

// TestDashboardLayoutHasSidebarTopbarWorkspace proves the shell contract of Task 37.
// TestDashboardLayoutHasSidebarTopbarWorkspace 证明任务 37 的外壳契约。
func TestDashboardLayoutHasSidebarTopbarWorkspace(t *testing.T) {
	tmpl := loadTemplates(t)

	for _, page := range chromePages {
		html := execute(t, tmpl, page)
		checks := map[string]string{
			"central workspace":     `<main id="yd-workspace"`,
			"left navigation":       `class="yd-sidebar"`,
			"top bar":               `class="yd-topbar"`,
			"target inheritance":    `hx-target:inherited="#yd-workspace"`,
			"swap inheritance":      `hx-swap:inherited="innerMorph"`,
			"url inheritance":       `hx-push-url:inherited="true"`,
			"boost inheritance":     `hx-boost:inherited="true"`,
			"indicator inheritance": `hx-indicator:inherited="#yd-progress"`,
			"nav links":             `data-yd-nav=`,
		}
		for label, needle := range checks {
			if !strings.Contains(html, needle) {
				t.Errorf("%s: missing %s (%s) | %s 缺少 %s", page, label, needle, page, label)
			}
		}
		if got := strings.Count(html, `id="yd-workspace"`); got != 1 {
			t.Errorf("%s: workspace id appears %d times, want exactly 1 | %s 中工作区 id 出现 %d 次，应为 1 次", page, got, page, got)
		}
	}

	// Standalone pages must stay shell free. | 独立页面不得带外壳。
	for _, page := range standalonePages {
		html := execute(t, tmpl, page)
		for _, needle := range []string{`class="yd-sidebar"`, `class="yd-topbar"`, `id="yd-workspace"`} {
			if strings.Contains(html, needle) {
				t.Errorf("%s: standalone page must not contain %s | %s 作为独立页面不应包含 %s", page, needle, page, needle)
			}
		}
	}
}

// TestScriptsLoadedInOrderAndLocally pins the one allowed script order and the offline rule.
// TestScriptsLoadedInOrderAndLocally 固定唯一允许的脚本顺序与离线规则。
func TestScriptsLoadedInOrderAndLocally(t *testing.T) {
	tmpl := loadTemplates(t)

	for _, page := range append(append([]string{}, chromePages...), standalonePages...) {
		html := execute(t, tmpl, page)
		matches := scriptSrcRe.FindAllStringSubmatch(html, -1)
		got := make([]string, 0, len(matches))
		for _, match := range matches {
			got = append(got, match[1])
		}
		if strings.Join(got, "\n") != strings.Join(scriptsInOrder, "\n") {
			t.Errorf("%s: script order mismatch\ngot  | 实际: %v\nwant | 期望: %v", page, got, scriptsInOrder)
		}
		for _, src := range got {
			if !strings.HasPrefix(src, "/static/js/") {
				t.Errorf("%s: script %q is not a local /static/js path | %s 的脚本 %q 不是本地 /static/js 路径", page, src, page, src)
			}
			for _, banned := range []string{"http", "//", "node_modules"} {
				if strings.Contains(src, banned) {
					t.Errorf("%s: script %q contains %q | %s 的脚本 %q 含有 %q", page, src, banned, page, src, banned)
				}
			}
			// A referenced script must also ship: a typo would silently disable a feature offline.
			// 被引用的脚本还必须真实存在：写错文件名会静默地让某项功能在离线下失效。
			if _, err := os.Stat(filepath.Join(strings.TrimPrefix(src, "/"))); err != nil {
				t.Errorf("%s: script %s is missing from web/: %v | %s 引用的脚本 %s 在 web/ 中不存在：%v", page, src, err, page, src, err)
			}
		}
	}

	// Only the JSON catalog may be inline; no executable script may carry a body.
	// 只有 JSON 目录可以内联；任何可执行脚本都不得带正文。
	for _, page := range append(append([]string{}, chromePages...), standalonePages...) {
		html := execute(t, tmpl, page)
		for _, tag := range scriptTagRe.FindAllString(html, -1) {
			if strings.Contains(tag, "src=") || strings.Contains(tag, `type="application/json"`) {
				continue
			}
			t.Errorf("%s: inline executable script is not allowed: %s | %s 不允许内联可执行脚本：%s", page, tag, page, tag)
		}
	}
}

// TestNoInlineCdnAndNoNodeArtifacts keeps the front-end fully offline: nothing may reach a CDN and
// no node build artifact may leak into the tree.
// TestNoInlineCdnAndNoNodeArtifacts 保持前端完全离线：不得访问任何 CDN，也不得混入 node 构建产物。
func TestNoInlineCdnAndNoNodeArtifacts(t *testing.T) {
	files := templateFiles(t)

	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		body := string(raw)
		for _, banned := range forbiddenRefs {
			if strings.Contains(body, banned) {
				t.Errorf("%s: contains forbidden reference %q | %s 含有被禁止的引用 %q", file, banned, file, banned)
			}
		}
		if loc := colorRe.FindString(body); loc != "" {
			t.Errorf("%s: literal colour in an inline style is not allowed: %s | %s 的行内样式不允许出现字面色值：%s", file, loc, file, loc)
		}
	}

	tmpl := loadTemplates(t)
	for _, page := range append(append([]string{}, chromePages...), standalonePages...) {
		html := execute(t, tmpl, page)
		for _, banned := range forbiddenRefs {
			if strings.Contains(html, banned) {
				t.Errorf("%s: rendered output contains forbidden reference %q | %s 的渲染结果含有被禁止的引用 %q", page, banned, page, banned)
			}
		}
	}
}

// TestLoginFormDeclaresGuomiEncryption pins the transport pre-encryption contract of the sign-in form.
// TestLoginFormDeclaresGuomiEncryption 固定登录表单的传输前置加密契约。
func TestLoginFormDeclaresGuomiEncryption(t *testing.T) {
	tmpl := loadTemplates(t)
	html := execute(t, tmpl, "pages/login.html")

	checks := []string{
		`data-yd-encrypt="sm2"`,
		`data-yd-pubkey="/api/guomi/pubkey"`,
		`hx-post="/login"`,
		`hx-status:422="target:#errors"`,
		`id="errors"`,
		`data-yd-lang=`,
	}
	for _, needle := range checks {
		if !strings.Contains(html, needle) {
			t.Errorf("pages/login.html: missing %s | pages/login.html 缺少 %s", needle, needle)
		}
	}
	if got := strings.Count(html, `data-yd-sensitive="true"`); got < 2 {
		t.Errorf("pages/login.html: %d sensitive fields, want at least 2 | pages/login.html 敏感字段 %d 个，至少需要 2 个", got, got)
	}
	// The form must keep a real action so it still works without JavaScript.
	// 表单必须保留真实的 action，以便无 JavaScript 时仍然可用。
	if !strings.Contains(html, `action="/login"`) || !strings.Contains(html, `method="post"`) {
		t.Error("pages/login.html: the sign-in form must keep method=post and action=/login | pages/login.html 登录表单必须保留 method=post 与 action=/login")
	}
}

// TestFormsUseHtmxWithStatusHandling proves every form is an htmx form and that validation errors
// have a target region.
// TestFormsUseHtmxWithStatusHandling 证明每个表单都是 htmx 表单，且校验错误有落点区域。
func TestFormsUseHtmxWithStatusHandling(t *testing.T) {
	tmpl := loadTemplates(t)
	verbs := []string{"hx-post", "hx-get", "hx-put", "hx-patch", "hx-delete", "hx-ws:send"}

	total, withStatus := 0, 0
	for _, name := range definedNames(tmpl) {
		if !strings.HasPrefix(name, "pages/") {
			continue
		}
		html := execute(t, tmpl, name)
		for _, tag := range formTagRe.FindAllString(html, -1) {
			total++
			verb := ""
			for _, candidate := range verbs {
				if strings.Contains(tag, candidate) {
					verb = candidate
					break
				}
			}
			if verb == "" {
				t.Errorf("%s: form without an htmx verb: %s | %s 的该表单没有 htmx 动词：%s", name, tag, name, tag)
				continue
			}
			if strings.Contains(tag, `method="post"`) && verb != "hx-post" {
				t.Errorf("%s: a POST form must use hx-post, found %s | %s 中 method=post 的表单必须使用 hx-post，实际为 %s", name, verb, name, verb)
			}
			if strings.Contains(tag, "hx-status:422") {
				withStatus++
			}
		}
	}

	if total == 0 {
		t.Fatal("no forms found: the templates under test are wrong | 没有找到任何表单：被测模板有误")
	}
	if withStatus == 0 {
		t.Fatal("no form handles 422: validation errors would be lost | 没有表单处理 422：校验错误会丢失")
	}

	// Every page with a validated form must render the error region. | 每个带校验表单的页面都必须渲染错误区域。
	for _, page := range []string{"pages/login.html", "pages/users.html", "pages/settings.html", "pages/i18n_demo.html"} {
		html := execute(t, tmpl, page)
		if !strings.Contains(html, `id="errors"`) {
			t.Errorf("%s: missing the #errors region | %s 缺少 #errors 区域", page, page)
		}
		if !strings.Contains(html, `hx-status:422="target:#errors"`) {
			t.Errorf("%s: no form routes 422 into #errors | %s 没有表单把 422 路由到 #errors", page, page)
		}
	}
}

// TestEveryPageHasI18nDataAttributes proves no user-facing string escapes the translation layer.
// TestEveryPageHasI18nDataAttributes 证明没有面向用户的文案游离在翻译层之外。
func TestEveryPageHasI18nDataAttributes(t *testing.T) {
	tmpl := loadTemplates(t)

	for _, name := range definedNames(tmpl) {
		if !strings.HasPrefix(name, "pages/") {
			continue
		}
		html := execute(t, tmpl, name)

		keys := i18nKeyRe.FindAllStringSubmatch(html, -1)
		if len(keys) < 5 {
			t.Errorf("%s: only %d data-yd-i18n nodes, want at least 5 | %s 只有 %d 个 data-yd-i18n 节点，至少需要 5 个", name, len(keys), name, len(keys))
		}
		for _, match := range keys {
			key := match[1]
			if key == "" {
				t.Errorf("%s: an empty data-yd-i18n key was rendered | %s 渲染出了空的 data-yd-i18n 键", name, name)
				continue
			}
			if !i18nKeySyntax.MatchString(key) {
				t.Errorf("%s: %q is not a dotted translation key | %s 的 %q 不是点分翻译键", name, key, name, key)
			}
		}
		if attrs := i18nAttrRe.FindAllStringSubmatch(html, -1); len(attrs) == 0 {
			t.Errorf("%s: no data-yd-i18n-attr node, attributes would stay untranslated | %s 没有 data-yd-i18n-attr 节点，属性将无法翻译", name, name)
		}
	}
}

// TestI18nCatalogInjectedIntoLayout proves the catalog envelope reaches the page as a raw JSON
// object and pins the type the data contract has to use.
// TestI18nCatalogInjectedIntoLayout 证明目录信封以原始 JSON 对象送达页面，并固定数据契约必须使用的类型。
func TestI18nCatalogInjectedIntoLayout(t *testing.T) {
	tmpl := loadTemplates(t)

	envelope := map[string]any{
		"lang":     "en",
		"fallback": "zh-CN",
		"catalogs": map[string]any{
			"en":    map[string]any{"app": map[string]any{"name": "yd"}},
			"zh-CN": map[string]any{"app": map[string]any{"name": "yd"}},
		},
	}
	raw, err := jsonx.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	// html/template treats <script type="application/json"> as a JavaScript container, so only
	// template.JS reaches the page as a raw object. | html/template 把
	// <script type="application/json"> 视作 JavaScript 容器，因此只有 template.JS 能以原始对象送达。
	data := templateData()
	data["I18nJSON"] = template.JS(raw)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "dashboard", data); err != nil {
		t.Fatalf("execute dashboard: %v", err)
	}
	html := buf.String()

	match := catalogRe.FindStringSubmatch(html)
	if match == nil {
		t.Fatal("the layout does not inject <script id=\"yd-i18n-catalog\"> | 布局没有注入 <script id=\"yd-i18n-catalog\">")
	}
	if strings.Contains(match[1], "&#34;") || strings.Contains(match[1], "&amp;") {
		t.Errorf("the catalog body is entity escaped: %s | 目录正文被实体转义：%s", match[1], match[1])
	}
	if !strings.HasPrefix(strings.TrimSpace(match[1]), "{") {
		t.Errorf("the catalog body is not a raw JSON object: %s | 目录正文不是原始 JSON 对象：%s", match[1], match[1])
	}

	var decoded struct {
		Lang     string                    `json:"lang"`
		Fallback string                    `json:"fallback"`
		Catalogs map[string]map[string]any `json:"catalogs"`
	}
	if err := jsonx.Unmarshal([]byte(match[1]), &decoded); err != nil {
		t.Fatalf("the injected catalog is not valid JSON (%v): %s | 注入的目录不是合法 JSON（%v）：%s", err, match[1], err, match[1])
	}
	if decoded.Lang != "en" || decoded.Fallback != "zh-CN" {
		t.Errorf("envelope languages = %q/%q, want en/zh-CN | 信封语言为 %q/%q，期望 en/zh-CN", decoded.Lang, decoded.Fallback, decoded.Lang, decoded.Fallback)
	}
	if len(decoded.Catalogs) != 2 {
		t.Errorf("%d catalogs injected, want 2 | 注入了 %d 个目录，期望 2 个", len(decoded.Catalogs), len(decoded.Catalogs))
	}

	// The same envelope typed as template.HTML (or a plain string) is json.Marshal'ed into a quoted
	// JSON string; yd-i18n.js unwraps that too, and this assertion keeps the trap documented.
	// 同样的信封若声明为 template.HTML（或普通字符串）会被 json.Marshal 成带引号的 JSON 字符串；
	// yd-i18n.js 同样能解开它，本断言把这个坑记录下来。
	quoted := templateData()
	quoted["I18nJSON"] = template.HTML(raw) //nolint:staticcheck // deliberately the wrong type | 故意用错误的类型
	buf.Reset()
	if err := tmpl.ExecuteTemplate(&buf, "dashboard", quoted); err != nil {
		t.Fatalf("execute dashboard with template.HTML: %v", err)
	}
	wrapped := catalogRe.FindStringSubmatch(buf.String())
	if wrapped == nil {
		t.Fatal("template.HTML: the catalog script disappeared | template.HTML：目录脚本消失了")
	}
	if !strings.HasPrefix(strings.TrimSpace(wrapped[1]), `"`) {
		t.Errorf("template.HTML was not JSON encoded, the data contract note must be updated: %s | template.HTML 未被 JSON 编码，需更新数据契约说明：%s", wrapped[1], wrapped[1])
	}
	var unwrapped string
	if err := jsonx.Unmarshal([]byte(strings.TrimSpace(wrapped[1])), &unwrapped); err != nil {
		t.Fatalf("the quoted catalog is not a JSON string: %v | 带引号的目录不是 JSON 字符串：%v", err, err)
	}
	if unwrapped != string(raw) {
		t.Errorf("unwrapping the quoted catalog changed it:\n got %s\nwant %s | 解开带引号的目录后内容发生变化", unwrapped, string(raw))
	}
}

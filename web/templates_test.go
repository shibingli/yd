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
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/shibingli/yd/security/authz"
	"github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/regexpx"
)

// templateGrants is what the fixture subject holds, keyed by tokens of the real registry so a
// typo cannot hide here: the helpers below resolve a reference through authz.ResolvePermission,
// exactly like the render binding does. | templateGrants 是固定数据主体持有的能力，
// 以真实注册表的标识为键，因此这里藏不住拼写错误：下面的辅助函数经 authz.ResolvePermission
// 解析引用，与 render 的按次绑定完全一致。
//
// It is a variable because one test renders the same page as a subject that holds less, and every
// such test restores it. | 它是变量，因为有一个测试要以「持有更少」的主体渲染同一个页面，
// 而每个这样的测试都会恢复它。
var templateGrants = []string{authz.PermUserCreate, authz.PermUserRead, authz.PermUserUpdate, authz.PermUserDelete, authz.PermRoleManage}

// templateRoles is what the fixture subject holds as roles. | templateRoles 是固定数据主体持有的角色。
var templateRoles = []string{authz.RoleAdmin}

// templateRef renders a template reference as a string; a missing key arrives as nil. | templateRef 把模板引用渲染为字符串；缺键时抵达的是 nil。
func templateRef(ref any) string {
	if s, ok := ref.(string); ok {
		return s
	}
	return ""
}

// templateCan decides a capability reference through the registry, so `can .Perm.UserCreate`,
// `can "PermUserCreate"` and `can "user:create"` are one decision. | templateCan 经注册表判定能力引用，
// 因此 `can .Perm.UserCreate`、`can "PermUserCreate"` 与 `can "user:create"` 是同一次判定。
func templateCan(ref any) bool {
	token, err := authz.ResolvePermission(templateRef(ref))
	if err != nil {
		return false
	}
	return slices.Contains(templateGrants, token)
}

// templateHasRole decides a role reference through the registry. | templateHasRole 经注册表判定角色引用。
func templateHasRole(ref any) bool {
	name, err := authz.ResolveRole(templateRef(ref))
	if err != nil {
		return false
	}
	return slices.Contains(templateRoles, name)
}

// Chrome pages render the dashboard shell; standalone pages must not. | 带外壳的页面渲染管理后台骨架；独立页面不得渲染它。
var chromePages = []string{
	"dashboard",
	"pages/users.html", "pages/tenants.html", "pages/roles.html", "pages/permissions.html",
	"pages/settings.html", "pages/realtime.html", "pages/i18n_demo.html",
}

// standalonePages are the two pages that must work without any shell. | 两个必须脱离外壳独立工作的页面。
var standalonePages = []string{"pages/login.html", "pages/error.html"}

// scriptsInOrder is the single allowed script order of the layout. It is one script longer than
// base.scripts: yd-theme-boot.js is synchronous and sits in <head> ahead of the stylesheet, because
// resolving the "system" theme after the deferred scripts would paint one wrong frame.
// scriptsInOrder 是布局唯一允许的脚本顺序。它比 base.scripts 多一段：
// yd-theme-boot.js 是同步脚本、位于 <head> 中样式表之前，
// 因为若等 defer 脚本执行后再解析 "system" 主题，就会先画出一帧错误的主题。
var scriptsInOrder = []string{
	"/static/js/yd-theme-boot.js",
	"/static/js/vendored/htmx.min.js",
	"/static/js/vendored/hx-head.min.js",
	"/static/js/vendored/hx-targets.min.js",
	"/static/js/vendored/hx-upsert.min.js",
	"/static/js/vendored/hx-pending.min.js",
	"/static/js/vendored/hx-preload.min.js",
	"/static/js/vendored/hx-multipart.min.js",
	"/static/js/vendored/hx-download.min.js",
	"/static/js/vendored/hx-browser-indicator.min.js",
	"/static/js/vendored/hx-history-cache.min.js",
	"/static/js/vendored/hx-live.min.js",
	"/static/js/vendored/hx-prompt.min.js",
	"/static/js/vendored/hx-ptag.min.js",
	"/static/js/vendored/hx-sse.min.js",
	"/static/js/vendored/hx-ws.min.js",
	"/static/js/vendored/hx-alpine-compat.min.js",
	"/static/js/yd-ui.js",
	"/static/js/vendored/alpine.min.js",
	"/static/js/yd-guomi.js",
	"/static/js/yd-guomi-htmx.js",
	"/static/js/yd-i18n.js",
	"/static/js/yd-authz.js",
	"/static/js/yd-progress.js",
	"/static/js/yd-captcha.js",
	"/static/js/yd-app.js",
}

var (
	scriptSrcRe   = regexpx.MustCompile(`<script[^>]*\ssrc="([^"]+)"`)
	scriptTagRe   = regexpx.MustCompile(`(?s)<script\b[^>]*>`)
	formTagRe     = regexpx.MustCompile(`(?s)<form\b[^>]*>`)
	catalogRe     = regexpx.MustCompile(`(?s)<script id="yd-i18n-catalog" type="application/json">(.*?)</script>`)
	authzRe       = regexpx.MustCompile(`(?s)<script id="` + authz.AuthzScriptID + `" type="application/json">(.*?)</script>`)
	i18nKeyRe     = regexpx.MustCompile(`data-yd-i18n="([^"]*)"`)
	i18nAttrRe    = regexpx.MustCompile(`data-yd-i18n-attr="([^"]*)"`)
	i18nKeySyntax = regexpx.MustCompile(`^[A-Za-z0-9]+(\.[A-Za-z0-9_-]+)+$`)
	idRe          = regexpx.MustCompile(`\sid="([^"]+)"`)
	authzGuardRe  = regexpx.MustCompile(`data-yd-(perm|role)="([^"]*)"`)
	colorRe       = regexpx.MustCompile(`style="[^"]*(#[0-9a-fA-F]{3,8}|rgba?\(|hsla?\()`)
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
		// default mirrors the render helper: it returns v unless v is empty. | default 对应 render 的
		// 同名辅助函数：v 为空时返回默认值。
		"default": func(def, v any) any {
			switch value := v.(type) {
			case nil:
				return def
			case string:
				if value == "" {
					return def
				}
			}
			return v
		},
		// The four authority helpers mirror what the render binding publishes for one execution: a
		// template asks about a capability or a role, and the answer is decided through the registry.
		// 四个权限辅助函数对应 render 为单次执行发布的绑定：模板询问某项能力或某个角色，
		// 答案经注册表作出判定。
		"can":     templateCan,
		"hasRole": templateHasRole,
		"perm": func(ref any) string {
			token, err := authz.ResolvePermission(templateRef(ref))
			if err != nil {
				return templateRef(ref)
			}
			return token
		},
		"role": func(ref any) string {
			name, err := authz.ResolveRole(templateRef(ref))
			if err != nil {
				return templateRef(ref)
			}
			return name
		},
		// asset is the identity here: these tests pin the script order and the local
		// path, while the production .min resolution is covered by http/render tests.
		// asset 在这里是恒等函数：本组测试固定脚本顺序与本地路径，
		// production 的 .min 解析由 http/render 的测试覆盖。
		"asset": func(p string) string { return p },
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
		// Perm and Role are the registry's own derived maps, never a hand written pair: a template
		// reads .Perm.UserCreate and the token it writes came from the one table that defines it.
		// Perm 与 Role 是注册表自己派生的映射，绝不是手写的一对：模板读 .Perm.UserCreate，
		// 它写出的标识来自定义该标识的唯一那张表。
		"Perm": authz.PermMap(),
		"Role": authz.RoleMap(),
		// AuthzJSON carries the same registry in the spelling the browser reads; the anonymous
		// envelope is what a page rendered without a subject reports.
		// AuthzJSON 以浏览器读取的写法携带同一张注册表；匿名信封正是无主体页面所报告的内容。
		"AuthzJSON": authz.NewEnvelope(nil).JS(),
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
				"Disabled": false,
				"Roles":    []map[string]any{{"Value": "admin", "Key": "users.role.admin", "Label": "Administrator"}},
			},
			{
				// The second row is disabled and holds no role, so both branches of
				// partials/users/row.html (enable instead of disable, the empty role list) execute.
				// 第二行已禁用且未持有角色，因此 partials/users/row.html 的两个分支
				//（显示「启用」而非「禁用」、角色为空）都会执行。
				// NameKey/EmailKey/RoleKey are still required by the legacy partials/table.html that
				// pages/dashboard.html renders; partials/users/row.html deliberately ignores them.
				// NameKey/EmailKey/RoleKey 仍是 pages/dashboard.html 所用的旧局部模板
				// partials/table.html 需要的；partials/users/row.html 有意忽略它们。
				"ID": "2", "Initials": "L", "Name": "Lan",
				"Email": "lan@example.com", "EmailKey": "users.email",
				"NameKey": "users.name", "RoleKey": "users.role.viewer",
				"Status": "disabled", "StatusKey": "users.status.disabled",
				"Disabled": true,
			},
		},
		"Tenants": []map[string]any{
			// A tenant name is data, so the option carries no translation key; only the status badge
			// of the row does. | 租户名是数据，因此选项不带翻译键；只有行内的状态徽标带键。
			{
				"ID": "1", "Slug": "acme", "Name": "Acme", "Created": "2026-01-02",
				"Status": "active", "StatusKey": "tenants.status.active", "Disabled": false,
				"Value": "acme", "Selected": true,
			},
			{
				"ID": "2", "Slug": "globex", "Name": "Globex", "Created": "2026-02-03",
				"Status": "disabled", "StatusKey": "tenants.status.disabled", "Disabled": true,
				"Value": "globex",
			},
		},
		"Roles": []map[string]any{
			// One entry serves both readers: partials/roles/list.html reads ID/Name/Description/
			// Permissions, the checkbox group of partials/users/form.html reads Value/Key/Label/Checked.
			// 同一条记录同时服务两个读者：partials/roles/list.html 读 ID/Name/Description/Permissions，
			// partials/users/form.html 的复选框组读 Value/Key/Label/Checked。
			{
				"ID": "1", "Name": "Administrator", "Description": "Full control inside the tenant",
				"Value": "1", "Key": "users.role.admin", "Label": "Administrator", "Checked": true,
				"Permissions": []map[string]any{
					{"Value": "user", "Key": "permissions.group.user", "Label": "User"},
					{"Value": "role", "Key": "permissions.group.role", "Label": "Role"},
				},
			},
			{
				"ID": "2", "Name": "Viewer", "Description": "Read only",
				"Value": "2", "Key": "users.role.viewer", "Label": "Viewer", "Checked": false,
			},
		},
		"Permissions": []map[string]any{
			{"Value": "user", "Key": "permissions.group.user", "Label": "User", "Checked": true},
			{"Value": "role", "Key": "permissions.group.role", "Label": "Role", "Checked": false},
		},
		"Matrix": []map[string]any{
			// The first cell is granted and the second is not, so both the checked and the unchecked
			// branch of the matrix — including the two aria-label keys — are rendered.
			// 第一个格子已授予、第二个未授予，因此矩阵的勾选与未勾选两个分支
			//（连同两个 aria-label 键）都会被渲染。
			{
				"Key": "permissions.group.user", "Label": "User",
				"GroupKey": "permissions.group.user", "Group": "User",
				"Cells": []map[string]any{
					{"Value": "1", "Granted": true},
					{"Value": "2", "Granted": false},
				},
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
var definedNameRe = regexpx.MustCompile(`"([^"]+)"`)

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
		"partials/form.errors", "partials/form.error-items", "partials/form.field", "partials/form.select",
		"partials/form.check", "partials/form.actions",
		"partials/modal.open", "partials/modal.close",
		"partials/users/list", "partials/users/row", "partials/users/form",
		"partials/tenants/list", "partials/tenants/row", "partials/tenants/form",
		"partials/roles/list", "partials/roles/row", "partials/roles/form",
		"partials/permissions/matrix",
		"pages/login.html", "pages/dashboard.html", "pages/dashboard.content",
		"pages/users.html", "pages/users.content",
		"pages/tenants.html", "pages/tenants.content",
		"pages/roles.html", "pages/roles.content",
		"pages/permissions.html", "pages/permissions.content",
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
	// Component partials get their contract fixture instead of the page fixture; a page map does
	// not carry the fields a component expects, and executing them with it would test nothing.
	// 每个模板还必须能执行：转义错误只在执行期才会暴露。组件局部模板使用各自的契约固定数据，
	// 页面数据没有组件期望的字段，用它执行并不能证明什么。
	componentData := componentFixture()
	for _, name := range definedNames(tmpl) {
		data := any(templateData())
		if strings.HasPrefix(name, "components.") {
			if name == "components.navigation.tree.nodes" {
				data = []map[string]any{{"Label": "Child", "Href": "/child"}}
			} else if fixture, ok := componentData[name]; ok {
				data = fixture
			} else {
				continue
			}
		}
		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
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
			"central workspace":  `<main id="yd-workspace"`,
			"left navigation":    `class="yd-sidebar"`,
			"top bar":            `class="yd-topbar"`,
			"target inheritance": `hx-target:inherited="#yd-workspace"`,
			"swap inheritance":   `hx-swap:inherited="innerMorph"`,
			"url inheritance":    `hx-push-url:inherited="true"`,
			"boost inheritance":  `hx-boost:inherited="true"`,
			"progress bar":       `id="yd-progress"`,
			"nav links":          `data-yd-nav=`,
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
		`hx-status:422="target:#errors swap:innerHTML"`,
		`id="errors"`,
		`data-yd-lang-toggle`,
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

	// Every page that hosts a validated form must render the error region, and every form — whether
	// it sits in the page or is fetched into a dialog — must route 422 into it.
	// 每个承载校验表单的页面都必须渲染错误区域；每个表单——无论内联在页面里还是被取进对话框——
	// 都必须把 422 路由进它。
	for _, page := range []string{
		"pages/login.html", "pages/users.html", "pages/tenants.html", "pages/roles.html",
		"pages/permissions.html", "pages/settings.html", "pages/i18n_demo.html",
	} {
		html := execute(t, tmpl, page)
		if !strings.Contains(html, `id="errors"`) {
			t.Errorf("%s: missing the #errors region | %s 缺少 #errors 区域", page, page)
		}
		if got := strings.Count(html, `id="errors"`); got != 1 {
			t.Errorf("%s: the #errors region appears %d times, want exactly 1 | %s 中 #errors 区域出现 %d 次，应为 1 次", page, got, page, got)
		}
	}

	// The dialog forms are answered as fragments, so the routing lives in the partial, not the page.
	// 对话框表单以片段应答，因此这条路由位于局部模板而不是页面里。
	for _, name := range []string{
		"pages/login.html", "pages/settings.html", "pages/i18n_demo.html",
		"partials/users/form", "partials/tenants/form", "partials/roles/form",
	} {
		html := execute(t, tmpl, name)
		if !strings.Contains(html, `hx-status:422="target:#errors swap:innerHTML"`) {
			t.Errorf("%s: no form routes 422 into #errors | %s 没有表单把 422 路由到 #errors", name, name)
		}
	}
}

// executeWith renders one named template with explicit data instead of the shared fixture.
// executeWith 用显式数据而不是共享固定数据渲染指定名称的模板。
func executeWith(t *testing.T, tmpl *template.Template, name string, data any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return buf.String()
}

// TestFormPartialsRenderInBothModes renders the three dialog forms with an edit payload: the create
// branch runs in TestAllTemplatesParse, and a branch that never executes would hide its own errors.
// TestFormPartialsRenderInBothModes 用编辑态数据渲染三个对话框表单：
// 新建分支已由 TestAllTemplatesParse 执行，而从未执行的分支会把自身的错误藏起来。
func TestFormPartialsRenderInBothModes(t *testing.T) {
	tmpl := loadTemplates(t)

	// The tenant slug is the one field that must not change after creation, so the edit answer
	// renders it read-only and says why; the create answer renders it editable instead.
	// 租户标识是创建后唯一不可修改的字段，因此编辑应答把它渲染为只读并说明原因，
	// 新建应答则渲染为可编辑。
	edit := templateData()
	edit["FormAction"] = "/tenants/1"
	edit["Tenant"] = map[string]any{"Name": "Acme", "Slug": "acme"}
	html := executeWith(t, tmpl, "partials/tenants/form", edit)
	for _, needle := range []string{
		`action="/tenants/1"`, `hx-post="/tenants/1"`,
		`id="yd-tenant-slug" name="slug" type="text" value="acme" readonly`,
		`tenants.field.slug_locked`,
	} {
		if !strings.Contains(html, needle) {
			t.Errorf("partials/tenants/form (edit): missing %s | partials/tenants/form（编辑）缺少 %s", needle, needle)
		}
	}
	if strings.Contains(execute(t, tmpl, "partials/tenants/form"), "readonly") {
		t.Error("partials/tenants/form (create): the slug must stay editable | partials/tenants/form（新建）：标识必须可编辑")
	}

	// A role edit pre-fills the name and checks the permissions the role already holds.
	editRole := templateData()
	editRole["FormAction"] = "/roles/1"
	editRole["Role"] = map[string]any{"Name": "Viewer", "Description": "Read only"}
	html = executeWith(t, tmpl, "partials/roles/form", editRole)
	for _, needle := range []string{`hx-post="/roles/1"`, `value="Viewer"`, `id="yd-role-permission-user" name="permissions" value="user" checked`} {
		if !strings.Contains(html, needle) {
			t.Errorf("partials/roles/form (edit): missing %s | partials/roles/form（编辑）缺少 %s", needle, needle)
		}
	}

	// The password is required only while creating: renaming an account must never force a new one.
	// The shared fixture carries .User, so the create case is exercised by dropping it.
	// 口令只在新建时必填：给账号改名绝不能逼着重新设定口令。
	// 共享固定数据带着 .User，因此新建场景要把它去掉才能触发。
	create := templateData()
	delete(create, "User")
	if got := fieldTag(t, executeWith(t, tmpl, "partials/users/form", edit), "yd-user-password"); strings.Contains(got, "required") {
		t.Errorf("partials/users/form (edit): the password became required: %s | partials/users/form（编辑）：口令变成了必填：%s", got, got)
	}
	if got := fieldTag(t, executeWith(t, tmpl, "partials/users/form", create), "yd-user-password"); !strings.Contains(got, "required") || !strings.Contains(got, `data-yd-sensitive="true"`) {
		t.Errorf("partials/users/form (create): the initial password must be required and transported encrypted: %s | partials/users/form（新建）：初始口令必须必填且加密传输：%s", got, got)
	}
}

// fieldTag returns the whole <input> tag that carries the given id, so an assertion about one field
// cannot accidentally match a different one.
// fieldTag 返回带指定 id 的整个 <input> 标签，使针对某个字段的断言不会误命中另一个字段。
func fieldTag(t *testing.T, html, id string) string {
	t.Helper()
	pattern := regexpx.MustCompile(`(?s)<input[^>]*\sid="` + regexpx.QuoteMeta(id) + `"[^>]*>`)
	tag := pattern.FindString(html)
	if tag == "" {
		t.Fatalf("no <input id=%q> in the rendered output | 渲染结果中没有 <input id=%q>", id, id)
	}
	return tag
}

// TestNewPagesKeepElementIdsUnique proves one dialog per page really does keep every id unique: the
// create button and the row edit button fill the same body, so no field id is rendered twice.
// TestNewPagesKeepElementIdsUnique 证明「每页一个对话框」确实让每个 id 唯一：
// 新建按钮与行内编辑按钮填充同一个正文，因此没有任何字段 id 会被渲染两次。
func TestNewPagesKeepElementIdsUnique(t *testing.T) {
	tmpl := loadTemplates(t)

	for _, page := range []string{"pages/users.html", "pages/tenants.html", "pages/roles.html", "pages/permissions.html"} {
		html := execute(t, tmpl, page)
		seen := map[string]bool{}
		for _, match := range idRe.FindAllStringSubmatch(html, -1) {
			if seen[match[1]] {
				t.Errorf("%s: duplicate id %q | %s 中出现重复 id %q", page, match[1], page, match[1])
			}
			seen[match[1]] = true
		}
		if len(seen) == 0 {
			t.Errorf("%s: no id rendered at all | %s 没有渲染出任何 id", page, page)
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
	raw, err := json.Marshal(envelope)
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
	if err := json.Unmarshal([]byte(match[1]), &decoded); err != nil {
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
	if err := json.Unmarshal([]byte(strings.TrimSpace(wrapped[1])), &unwrapped); err != nil {
		t.Fatalf("the quoted catalog is not a JSON string: %v | 带引号的目录不是 JSON 字符串：%v", err, err)
	}
	if unwrapped != string(raw) {
		t.Errorf("unwrapping the quoted catalog changed it:\n got %s\nwant %s | 解开带引号的目录后内容发生变化", unwrapped, string(raw))
	}
}

// authzPayload mirrors the envelope yd-authz.js reads. | authzPayload 对应 yd-authz.js 读取的信封。
type authzPayload struct {
	// Perms maps every JavaScript capability key to its token. | Perms 把每个 JavaScript 能力键映射到它的标识。
	Perms map[string]string `json:"perms"`

	// Roles maps every JavaScript role key to its name. | Roles 把每个 JavaScript 角色键映射到它的名字。
	Roles map[string]string `json:"roles"`

	// Granted lists the capabilities the subject holds. | Granted 列出该主体持有的能力。
	Granted []string `json:"granted"`

	// Mine lists the roles the subject holds. | Mine 列出该主体持有的角色。
	Mine []string `json:"mine"`
}

// TestAuthzPayloadInjectedIntoLayout proves the browser receives the registry itself: the element id
// is the one yd-authz.js reads, the body is raw JSON, and every key is the script spelling.
// TestAuthzPayloadInjectedIntoLayout 证明浏览器收到的是注册表本身：元素 id 就是 yd-authz.js 读取的那个，
// 正文是原始 JSON，且每个键都是脚本写法。
//
// The template data keys deliberately do NOT appear: a page injecting .Perm.UserCreate would hand the
// browser a map whose keys no documented script reference can address, and that mismatch is exactly
// what this assertion exists to catch. | 模板数据键刻意不得出现：注入 .Perm.UserCreate 的页面交给浏览器的
// 是一张任何既定脚本引用都寻址不到的键表，而这正是本条断言要抓住的错配。
func TestAuthzPayloadInjectedIntoLayout(t *testing.T) {
	tmpl := loadTemplates(t)

	pages := append(append([]string{}, chromePages...), standalonePages...)
	for _, page := range pages {
		match := authzRe.FindStringSubmatch(execute(t, tmpl, page))
		if match == nil {
			t.Fatalf("%s: the layout injects no <script id=%q> | %s：布局没有注入 <script id=%q>", page, authz.AuthzScriptID, page, authz.AuthzScriptID)
		}
		body := strings.TrimSpace(match[1])
		if strings.Contains(body, "&#34;") || strings.Contains(body, "&amp;") {
			t.Errorf("%s: the payload is entity escaped: %s | %s：载荷被实体转义：%s", page, body, page, body)
		}
		// The payload must not be able to close its own element: a token carrying "</script" would
		// otherwise turn the rest of the map into markup. | 载荷不得能提前闭合自己的元素：
		// 否则一个携带 "</script" 的标识会把名字表的其余部分变成标记。
		if strings.Contains(body, "</script") {
			t.Errorf("%s: the payload can close its own script element | %s：载荷能提前闭合自己的脚本元素", page, page)
		}
		if !strings.HasPrefix(body, "{") {
			t.Errorf("%s: the payload is not a raw JSON object: %s | %s：载荷不是原始 JSON 对象：%s", page, body, page, body)
		}

		var payload authzPayload
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatalf("%s: the injected payload is not valid JSON (%v): %s | %s：注入的载荷不是合法 JSON（%v）：%s", page, err, body, page, err, body)
		}
		key := authz.JSKey(authz.PermUserCreate)
		if got := payload.Perms[key]; got != authz.PermUserCreate {
			t.Errorf("%s: perms[%s] = %q, want %q | %s：perms[%s] 为 %q，期望 %q", page, key, got, authz.PermUserCreate, page, key, got, authz.PermUserCreate)
		}
		roleKey := authz.RoleJSKey(authz.RoleAdmin)
		if got := payload.Roles[roleKey]; got != authz.RoleAdmin {
			t.Errorf("%s: roles[%s] = %q, want %q | %s：roles[%s] 为 %q，期望 %q", page, roleKey, got, authz.RoleAdmin, page, roleKey, got, authz.RoleAdmin)
		}
		// One spelling rule covers both maps: every key is upper case, so a script reference written
		// in the documented form always resolves. | 一条写法规则覆盖两张表：每个键都是大写，
		// 因此按文档写法书写的脚本引用总能解析。
		for _, names := range []map[string]string{payload.Perms, payload.Roles} {
			for name := range names {
				if name != strings.ToUpper(name) {
					t.Errorf("%s: %q is not the script spelling of a name | %s：%q 不是名字的脚本写法", page, name, page, name)
				}
			}
		}
		// The fixture renders an anonymous page, so both held lists must be present and empty.
		// 固定数据渲染的是匿名页面，因此两个持有列表必须存在且为空。
		if len(payload.Granted) != 0 {
			t.Errorf("%s: granted = %v, want an empty list | %s：granted 为 %v，期望空列表", page, payload.Granted, page, payload.Granted)
		}
		if len(payload.Mine) != 0 {
			t.Errorf("%s: mine = %v, want an empty list | %s：mine 为 %v，期望空列表", page, payload.Mine, page, payload.Mine)
		}
	}
}

// TestAuthzGuardsUseTheRegistryTokens proves the declarative guards and the `can` helper address the
// registry instead of spelling a capability: the token reaches the page because the registry put it
// there, and a subject that holds less receives less.
// TestAuthzGuardsUseTheRegistryTokens 证明声明式守卫与 `can` 辅助函数寻址的是注册表而不是直接书写能力：
// 标识出现在渲染结果里，是因为注册表把它放了进去；而持有更少的主体收到的东西也更少。
//
// The row partial is deliberately driven through `perm`/`role`/`can` with a constant *name*: its dot
// is one row, so the page level .Perm map is not in scope there, and a constant name in a template is
// the form yd authz check sanctions. | 行内局部模板刻意经 `perm`/`role`/`can` 搭配常量*名*驱动：
// 它的点号是一行数据，因此页面级的 .Perm 映射在那里不在作用域内，
// 而模板中的常量名正是 yd authz check 认可的形式。
func TestAuthzGuardsUseTheRegistryTokens(t *testing.T) {
	tmpl := loadTemplates(t)

	page := execute(t, tmpl, "pages/users.html")
	create := `data-yd-perm="` + authz.PermUserCreate + `"`
	if !strings.Contains(page, create) {
		t.Errorf("pages/users.html: the create button carries no %s | pages/users.html：新建按钮没有携带 %s", create, create)
	}

	rows, ok := templateData()["Users"].([]map[string]any)
	if !ok || len(rows) == 0 {
		t.Fatal("the fixture carries no user row | 固定数据没有用户行")
	}
	row := executeWith(t, tmpl, "partials/users/row", rows[0])

	edit := `data-yd-perm="` + authz.PermUserUpdate + `" data-yd-perm-mode="disable"`
	if !strings.Contains(row, edit) {
		t.Errorf("partials/users/row: the edit button is not greyed out but kept, want %s | partials/users/row：编辑按钮未按「变灰保留」处理，期望 %s", edit, edit)
	}
	rotate := `data-yd-role="` + authz.RoleAdmin + `" data-yd-role-mode="disable"`
	if !strings.Contains(row, rotate) {
		t.Errorf("partials/users/row: the key rotation is not guarded by the %s role, want %s | partials/users/row：轮换密钥未由 %s 角色守卫，期望 %s", authz.RoleAdmin, rotate, authz.RoleAdmin, rotate)
	}
	if !strings.Contains(row, `hx-delete="/users/1"`) {
		t.Fatalf("partials/users/row: the delete button is missing while its capability is held | partials/users/row：持有该能力时删除按钮却缺失")
	}

	// The very same row rendered for a subject that does not hold the delete capability: the server
	// renders one button less, which is what `can` decides and what the guard attribute then keeps
	// out of sight for the subjects the payload cannot be rebuilt for.
	// 同一个行由「未持有删除能力」的主体渲染：服务端少渲染一个按钮，
	// 这既是 `can` 作出的判定，也是之后守卫属性为「载荷无法重建」的主体所遮蔽的那一个。
	held := templateGrants
	templateGrants = slices.DeleteFunc(slices.Clone(templateGrants), func(token string) bool { return token == authz.PermUserDelete })
	defer func() { templateGrants = held }()

	if got := executeWith(t, tmpl, "partials/users/row", rows[0]); strings.Contains(got, "hx-delete=") {
		t.Error("partials/users/row: a subject without the delete capability still receives the delete button | partials/users/row：未持有删除能力的主体仍收到了删除按钮")
	}
}

// TestLayoutStaticReferencesUseAssetHelper pins the single entry point for
// static URLs: a raw /static reference would bypass the production .min
// resolution and the content hash. | TestLayoutStaticReferencesUseAssetHelper
// 固定静态 URL 的唯一入口：裸 /static 引用会绕过 production 的 .min 解析与内容哈希。
func TestLayoutStaticReferencesUseAssetHelper(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("templates", "layouts", "base.html"))
	if err != nil {
		t.Fatalf("读取 base.html 失败: %v", err)
	}
	for _, want := range []string{
		`{{ asset "/static/css/yd.css" }}`,
		`{{ asset "/static/js/yd-app.js" }}`,
		`{{ asset "/static/js/yd-theme-boot.js" }}`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("base.html 未通过 asset 解析 %q", want)
		}
	}
}

// TestRealtimeBannerLifecycleHooks pins the single banner the reconnect manager updates: a stable
// text node, an attempt badge, and a retry control. | TestRealtimeBannerLifecycleHooks 固定重连管理器
// 更新的唯一横幅：稳定的文本节点、重试次数徽标与重试按钮。
func TestRealtimeBannerLifecycleHooks(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("templates", "partials", "flash.html"))
	if err != nil {
		t.Fatalf("读取 flash.html 失败: %v", err)
	}
	flash := string(raw)
	for _, want := range []string{
		`id="yd-connection"`,
		"data-yd-connection-text",
		"data-yd-connection-attempt",
		"data-yd-reconnect",
	} {
		if !strings.Contains(flash, want) {
			t.Errorf("flash.html is missing %q | flash.html 缺少 %q", want, want)
		}
	}
}

// TestGlobalProgressBarCoversEveryPage pins the feedback contract: every rendered page carries the
// same fixed bar and loads the same controller, so a slow htmx request or a full-page navigation is
// visible everywhere instead of only inside the dashboard shell.
// TestGlobalProgressBarCoversEveryPage 固定反馈契约：每个渲染出来的页面都带有同一条固定进度条并加载同一个控制器，
// 因此慢请求与整页导航在任何页面都可见，而不只限于管理后台外壳。
func TestGlobalProgressBarCoversEveryPage(t *testing.T) {
	tmpl := loadTemplates(t)
	pages := append(append([]string{}, chromePages...), standalonePages...)

	for _, page := range pages {
		html := execute(t, tmpl, page)
		for _, needle := range []string{
			`id="yd-progress"`,
			`class="yd-progress"`,
			`class="yd-progress__bar"`,
			`data-yd-progress`,
			`src="/static/js/yd-progress.js"`,
		} {
			if !strings.Contains(html, needle) {
				t.Errorf("%s: missing %s | %s 缺少 %s", page, needle, page, needle)
			}
		}
		if strings.Contains(html, `hx-indicator:inherited="#yd-progress"`) {
			t.Errorf("%s still delegates the bar to htmx's rotating indicator | %s 仍在把进度条交给 htmx 的循环指示器", page, page)
		}
		// The bar must stay outside the top bar's stacking context, or the mobile rail covers it.
		// 进度条必须位于顶栏的层叠上下文之外，否则移动端侧栏会把它盖住。
		progressAt := strings.Index(html, `id="yd-progress"`)
		topbarAt := strings.Index(html, `class="yd-topbar"`)
		if topbarAt >= 0 && progressAt > topbarAt {
			t.Errorf("%s renders #yd-progress after the top bar, inside its stacking context | %s 把 #yd-progress 渲染在顶栏之后，落进了它的层叠上下文", page, page)
		}
	}
}

// TestResponsiveShellHooks pins the mobile navigation contract: a trigger in the top bar, an
// off-canvas backdrop, and a stable id the trigger can control. | TestResponsiveShellHooks 固定
// 移动端导航契约：顶栏开关、抽屉遮罩，以及开关可通过 aria-controls 指向的稳定 id。
func TestResponsiveShellHooks(t *testing.T) {
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("templates", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", rel, err)
		}
		return string(raw)
	}
	for _, want := range []struct {
		file string
		text string
	}{
		{"partials/topbar.html", "data-yd-sidebar-open"},
		{"partials/sidebar.html", `id="yd-sidebar"`},
		{"layouts/dashboard.html", "data-yd-sidebar-backdrop"},
		{"layouts/dashboard.html", "yd-shell--app"},
	} {
		if !strings.Contains(read(want.file), want.text) {
			t.Errorf("%s 缺少 %q", want.file, want.text)
		}
	}
}

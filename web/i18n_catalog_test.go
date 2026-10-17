// i18n_catalog_test.go — the catalogue and i18n-attribute contract of the admin templates.
// i18n_catalog_test.go — 管理后台模板的语言目录与 i18n 属性契约测试。
//
// Two promises are pinned here. First, every message the templates ask for exists in *both*
// catalogues: the fallback chain is "active language -> default language -> the key itself", so a
// missing key does not fail loudly, it shows the bare key to a user. Second, every user-facing
// string of the Task 4 pages reaches the client through a hook — data-yd-i18n for element text and
// data-yd-i18n-attr for attributes — because a string without a hook keeps the language it was
// rendered in until the next full page load.
// 这里钉住两个承诺。第一，模板请求的每条消息都同时存在于*两份*目录中：
// 回退链是「当前语言 → 默认语言 → 键名本身」，因此缺失的键不会大声报错，
// 而是把裸键名展示给用户。第二，任务 4 页面的每条面向用户的文案都经钩子抵达客户端——
// 元素文本用 data-yd-i18n、属性用 data-yd-i18n-attr——
// 因为没有钩子的文案会停在它被渲染时的语言，直到下一次整页加载。
package web_test

import (
	"html/template"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"text/template/parse"

	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/authz"
	"github.com/shibingli/yd/web"
)

// parseFuncs is the minimal function map the templates need to parse; the values are never
// executed, because this file inspects the parse tree instead of rendering. The four authority
// helpers are wired to the registry all the same: a guard naming a capability that stopped being
// registered has to be visible here too, not only in the render binding.
// parseFuncs 是模板解析所需的最小函数表；这些取值从不被执行，
// 因为本文件只检视解析树而不渲染。四个权限辅助函数仍然接到注册表上：
// 一个守卫若写了一条不再被注册的能力，在这里也必须显现，而不是只在渲染绑定里显现。
var parseFuncs = template.FuncMap{
	"tr":      func(key string, args ...any) string { return key },
	"dict":    func(pairs ...any) (map[string]any, error) { return nil, nil },
	"list":    func(items ...any) []any { return items },
	"can":     func(ref any) bool { _, err := authz.ResolvePermission(parseRef(ref)); return err == nil },
	"hasRole": func(ref any) bool { _, err := authz.ResolveRole(parseRef(ref)); return err == nil },
	"perm": func(ref any) string {
		token, err := authz.ResolvePermission(parseRef(ref))
		if err != nil {
			return parseRef(ref)
		}
		return token
	},
	"role": func(ref any) string {
		name, err := authz.ResolveRole(parseRef(ref))
		if err != nil {
			return parseRef(ref)
		}
		return name
	},
}

// parseRef normalises a template helper argument into a reference. | parseRef 把模板辅助函数参数归一化为引用。
func parseRef(ref any) string {
	s, _ := ref.(string)
	return s
}

// templateDir is the subtree holding the page templates. | templateDir 是页面模板所在的子树。
const templateDir = "templates"

// commentRe matches a Go template comment, including the whitespace-trimming dashes.
// commentRe 匹配 Go 模板注释，包含两侧去空白的短横线。
var commentRe = regexp.MustCompile(`(?s)\{\{-?\s*/\*.*?\*/\s*-?\}\}`)

// templateSources returns every .html template of the embedded tree, keyed by its path.
// templateSources 返回内嵌树中的每个 .html 模板，以路径为键。
func templateSources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := afs.Walk(web.FS(), templateDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		out[p] = strings.ReplaceAll(string(readEmbedded(t, web.FS(), p)), "\r\n", "\n")
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s failed: %v | 遍历 %s 失败: %v", templateDir, err, templateDir, err)
	}
	if len(out) == 0 {
		t.Fatalf("no template under %s, the guards would be vacuous | %s 下没有模板，守卫形同虚设", templateDir, templateDir)
	}
	return out
}

// walkNodes visits node and every node below it; text/template/parse has no generic walker, so the
// composite node types are spelled out here.
// walkNodes 访问 node 及其下全部节点；text/template/parse 没有通用遍历器，
// 因此这里把复合节点类型逐一列出。
func walkNodes(node parse.Node, visit func(parse.Node)) {
	if node == nil {
		return
	}
	visit(node)

	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, child := range n.Nodes {
			walkNodes(child, visit)
		}
	case *parse.ActionNode:
		if n != nil {
			walkNodes(n.Pipe, visit)
		}
	case *parse.IfNode:
		if n != nil {
			walkNodes(n.Pipe, visit)
			walkNodes(n.List, visit)
			walkNodes(n.ElseList, visit)
		}
	case *parse.RangeNode:
		if n != nil {
			walkNodes(n.Pipe, visit)
			walkNodes(n.List, visit)
			walkNodes(n.ElseList, visit)
		}
	case *parse.WithNode:
		if n != nil {
			walkNodes(n.Pipe, visit)
			walkNodes(n.List, visit)
			walkNodes(n.ElseList, visit)
		}
	case *parse.TemplateNode:
		if n != nil {
			walkNodes(n.Pipe, visit)
		}
	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, cmd := range n.Cmds {
			for _, arg := range cmd.Args {
				walkNodes(arg, visit)
			}
		}
	case *parse.ChainNode:
		if n != nil {
			walkNodes(n.Node, visit)
		}
	}
}

// literalTrKeys returns every literal key passed to a `tr` call, mapped to the files that use it.
// literalTrKeys 返回传给 `tr` 调用的全部字面键，并记录使用它的文件。
//
// A dynamically built key, such as `{{tr (printf "i18n.sample.%s" .)}}`, cannot be enumerated
// statically and is skipped: its first argument is not a string node.
// 例如 `{{tr (printf "i18n.sample.%s" .)}}` 这类动态构造的键无法静态枚举，会被跳过：
// 它的第一个参数不是字符串节点。
func literalTrKeys(t *testing.T, sources map[string]string) map[string][]string {
	t.Helper()
	keys := map[string][]string{}

	for path, src := range sources {
		tmpl, err := template.New(path).Funcs(parseFuncs).Parse(src)
		if err != nil {
			t.Fatalf("parsing %s failed: %v | 解析 %s 失败: %v", path, err, path, err)
		}
		// Every associated template is walked, not only the file body: a partial file consists
		// entirely of {{define}} blocks, so its body tree is empty whitespace and holds nothing.
		// 遍历全部关联模板，而不仅是文件主体：局部模板文件整体由 {{define}} 区块组成，
		// 其主体语法树只有空白，什么也不含。
		walked := 0
		for _, associated := range tmpl.Templates() {
			if associated.Tree == nil || associated.Tree.Root == nil {
				continue
			}
			walked++
			walkNodes(associated.Tree.Root, func(node parse.Node) {
				pipe, ok := node.(*parse.PipeNode)
				if !ok {
					return
				}
				for _, cmd := range pipe.Cmds {
					if len(cmd.Args) < 2 {
						continue
					}
					ident, ok := cmd.Args[0].(*parse.IdentifierNode)
					if !ok || ident.Ident != "tr" {
						continue
					}
					literal, ok := cmd.Args[1].(*parse.StringNode)
					if !ok {
						continue // a dynamic key is out of reach of a static guard | 动态键超出静态守卫的范围
					}
					keys[literal.Text] = append(keys[literal.Text], path)
				}
			})
		}
		if walked == 0 {
			t.Fatalf("%s parsed into no template at all | %s 没有解析出任何模板", path, path)
		}
	}
	return keys
}

// The two client-side hooks. A key built from an action contains "{{" and is therefore not
// captured; a static key is plain dotted text.
// 两个客户端钩子。由动作构造的键含有 "{{"，因此不会被捕获；静态键就是普通的点分文本。
var (
	i18nLiteralRe = regexp.MustCompile(`data-yd-i18n="([^"]*)"`)
	i18nAttrRe    = regexp.MustCompile(`data-yd-i18n-attr="([^"]*)"`)
	staticKeyRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

// literalHookKeys returns the static data-yd-i18n and data-yd-i18n-attr keys of every template,
// mapped to the files that use them. | literalHookKeys 返回每个模板中静态的 data-yd-i18n 与
// data-yd-i18n-attr 键，并记录使用它的文件。
//
// An attribute spec is a comma-separated list of attr:key pairs, so each pair is taken apart
// separately: `data-yd-i18n-attr="title:x.tip, hx-confirm:x.confirm"` binds two keys.
// 属性规格是逗号分隔的 attr:key 列表，因此逐对拆解：
// `data-yd-i18n-attr="title:x.tip, hx-confirm:x.confirm"` 绑定两个键。
func literalHookKeys(t *testing.T, sources map[string]string) map[string][]string {
	t.Helper()
	keys := map[string][]string{}
	add := func(path, key string) {
		if staticKeyRe.MatchString(key) {
			keys[key] = append(keys[key], path)
		}
	}

	for path, src := range sources {
		body := commentRe.ReplaceAllString(src, "")
		for _, match := range i18nLiteralRe.FindAllStringSubmatch(body, -1) {
			add(path, match[1])
		}
		for _, match := range i18nAttrRe.FindAllStringSubmatch(body, -1) {
			for _, pair := range strings.Split(match[1], ",") {
				if colon := strings.Index(pair, ":"); colon >= 0 {
					add(path, strings.TrimSpace(pair[colon+1:]))
				}
			}
		}
	}
	return keys
}

// hasCatalogKey reports whether catalog holds a non-empty message under the dotted key.
// hasCatalogKey 报告 catalog 在点分键下是否存有非空消息。
//
// Segments are lower-cased, mirroring the loader: `lang.zh-CN` and `lang.zh-cn` are one key.
// 各段键统一转小写，与加载器一致：`lang.zh-CN` 与 `lang.zh-cn` 是同一个键。
func hasCatalogKey(catalog map[string]any, key string) bool {
	node := any(catalog)
	for _, segment := range strings.Split(key, ".") {
		m, ok := node.(map[string]any)
		if !ok {
			return false
		}
		node, ok = m[strings.ToLower(segment)]
		if !ok {
			return false
		}
	}
	message, ok := node.(string)
	return ok && message != ""
}

// flattenCatalog returns every dotted leaf key of a nested catalogue. | flattenCatalog 返回嵌套目录中的全部点分叶子键。
func flattenCatalog(catalog map[string]any, prefix string, out map[string]bool) {
	for name, value := range catalog {
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		if nested, ok := value.(map[string]any); ok {
			flattenCatalog(nested, key, out)
			continue
		}
		out[key] = true
	}
}

// intentionallyAbsent holds the keys that must stay out of both catalogues. The i18n demo page
// renders one of them on purpose, to demonstrate the "a missing key falls back to the key name"
// rule; keeping the exemption here is what lets that page keep proving it.
// intentionallyAbsent 保存必须留在两份目录之外的键。i18n 示例页刻意渲染其中之一，
// 用来演示「缺失键回退为键名」的规则；把豁免写在这里，那一页才能继续证明该规则。
var intentionallyAbsent = map[string]bool{
	"i18n.sample.missing": true,
}

// catalogShadowRe splits a catalogue line into its indentation, key and value.
// catalogShadowRe 把目录中的一行拆成缩进、键与取值三部分。
var catalogShadowRe = regexp.MustCompile(`^(\s*)([^:]+):(.*)$`)

// checkCatalogShadowing rejects a catalogue entry that is both a message and the prefix of another
// message, e.g. a message `dashboard.range` beside `dashboard.range.24h`.
// checkCatalogShadowing 拒绝既是消息、又是另一条消息前缀的目录项，
// 例如与 `dashboard.range.24h` 并列的消息 `dashboard.range`。
//
// The loader flattens the file into dotted paths and re-nests them, so such a pair cannot both
// survive: one silently replaces the other, and which one wins depends on map iteration order, i.e.
// on the run. The guard reads the file text instead of the loaded map precisely because the loaded
// map no longer shows the conflict — it only shows the winner.
// 加载器会把文件摊平成点分路径再重新嵌套，因此这样一对键不可能同时存活：
// 其中一个会静默替换另一个，而谁胜出取决于映射遍历顺序，也就是取决于当次运行。
// 该守卫读取文件文本而不是已加载的映射，正是因为加载后的映射已经不显示这场冲突——只显示胜者。
func checkCatalogShadowing(t *testing.T, file, src string) {
	t.Helper()
	var (
		stack  []string
		indent []int
		leaves []string
	)
	for number, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		match := catalogShadowRe.FindStringSubmatch(line)
		if match == nil {
			t.Errorf("%s:%d: the guard cannot read %q, so it cannot vouch for that line | %s:%d：守卫读不懂 %q，因此无法为该行担保",
				file, number+1, line, file, number+1, line)
			continue
		}
		space, name, value := match[1], strings.TrimSpace(match[2]), strings.TrimSpace(match[3])
		depth := len(strings.ReplaceAll(space, "\t", "  ")) / 2
		if depth > len(stack) {
			t.Errorf("%s:%d: %q is indented past its parent | %s:%d：%q 的缩进超过了它的父级", file, number+1, name, file, number+1, name)
			continue
		}
		stack = append(stack[:depth], name)
		indent = append(indent[:depth], depth)
		if value == "" || strings.HasPrefix(value, "#") {
			continue // a parent block, not a message | 父级区块，不是消息
		}
		leaves = append(leaves, strings.Join(stack, "."))
	}

	for _, leaf := range leaves {
		for _, other := range leaves {
			if strings.HasPrefix(other, leaf+".") {
				t.Errorf("%s: %s is both a message and the prefix of %s; the loader flattens keys, so one of the two would shadow the other at random | %s：%s 既是消息又是 %s 的前缀；加载器会摊平键，两者之一将被随机遮蔽",
					file, leaf, other, file, leaf, other)
			}
		}
	}
}

// TestEveryTrKeyHasBothCatalogs is the guard against a key that exists in one language only: the
// templates are enumerated from their parse trees, and every key is looked up in both catalogues.
// TestEveryTrKeyHasBothCatalogs 防止键只存在于一种语言：模板经解析树枚举，每个键在两份目录中逐一查证。
//
// The literal keys of the client-side hooks are checked too: yd-i18n.js resolves them from the very
// same catalogues, so a typo there would show a bare key after a language switch rather than at
// render time. | 客户端钩子的字面键同样被检查：yd-i18n.js 从同一份目录解析它们，
// 因此那里的笔误会在语言切换后显示裸键，而不是在渲染时暴露。
func TestEveryTrKeyHasBothCatalogs(t *testing.T) {
	bundle, err := web.Bundle()
	if err != nil {
		t.Fatalf("loading the embedded catalogues failed: %v | 加载内嵌语言目录失败: %v", err, err)
	}

	langs := bundle.Langs()
	for _, want := range []string{web.LangChinese, web.LangEnglish} {
		if !containsString(langs, want) {
			t.Fatalf("the embedded catalogues do not offer %s, only %v | 内嵌目录没有提供 %s，只有 %v", want, langs, want, langs)
		}
	}

	catalogs := map[string]map[string]any{}
	for _, lang := range []string{web.LangChinese, web.LangEnglish} {
		catalogs[lang] = bundle.Catalog(lang)
		if catalogs[lang] == nil {
			t.Fatalf("catalogue %s loaded as nil | 目录 %s 加载为 nil", lang, lang)
		}
		checkCatalogShadowing(t, lang+".yaml", string(readEmbedded(t, web.Catalogs(), lang+".yaml")))
	}

	sources := templateSources(t)
	keys := literalTrKeys(t, sources)
	if len(keys) == 0 {
		t.Fatal(`no {{tr "key"}} call was found, the guard would be vacuous | 没有找到任何 {{tr "key"}} 调用，守卫形同虚设`)
	}
	hookKeys := literalHookKeys(t, sources)

	// The two catalogues must describe the same key set: a key added to one language only is the
	// drift this test exists for, and it is invisible in the language that has it.
	// 两份目录必须描述同一组键：只加进一种语言的键正是本测试要防的漂移，
	// 而且在拥有它的那种语言里完全看不出来。
	zhKeys, enKeys := map[string]bool{}, map[string]bool{}
	flattenCatalog(catalogs[web.LangChinese], "", zhKeys)
	flattenCatalog(catalogs[web.LangEnglish], "", enKeys)
	for _, key := range sortedKeys(difference(zhKeys, enKeys)) {
		t.Errorf("catalogue %s misses %s, which %s defines | 目录 %s 缺少 %s，而 %s 中已定义", web.LangEnglish, key, web.LangChinese, web.LangEnglish, key, web.LangChinese)
	}
	for _, key := range sortedKeys(difference(enKeys, zhKeys)) {
		t.Errorf("catalogue %s misses %s, which %s defines | 目录 %s 缺少 %s，而 %s 中已定义", web.LangChinese, key, web.LangEnglish, web.LangChinese, key, web.LangEnglish)
	}

	// The report names the files that use a missing key, because the fix has to happen there.
	// A key listed in intentionallyAbsent is skipped: it is absent by design.
	// 报告会指出使用缺失键的文件，因为修复工作发生在那里。
	// 列在 intentionallyAbsent 中的键会被跳过：它们的缺失是刻意的。
	report := func(lang string, set map[string][]string) {
		catalog := catalogs[lang]
		var missing []string
		for key := range set {
			if intentionallyAbsent[key] {
				continue
			}
			if !hasCatalogKey(catalog, key) {
				missing = append(missing, key)
			}
		}
		sort.Strings(missing)
		for _, key := range missing {
			users := append([]string{}, set[key]...)
			sort.Strings(users)
			t.Errorf("catalogue %s misses %s (used by %s) | 目录 %s 缺少 %s（使用于 %s）", lang, key, strings.Join(users, ", "), lang, key, strings.Join(users, ", "))
		}
	}
	report(web.LangChinese, keys)
	report(web.LangEnglish, keys)
	report(web.LangChinese, hookKeys)
	report(web.LangEnglish, hookKeys)

	// The number the report leads with: how many distinct keys the templates ask for, against how
	// many messages the two catalogues define.
	// 报告开头的数字：模板请求的不同键数，对照两份目录定义的条数。
	referenced := map[string]bool{}
	for key := range keys {
		referenced[key] = true
	}
	for key := range hookKeys {
		referenced[key] = true
	}
	for key := range intentionallyAbsent {
		delete(referenced, key)
	}

	t.Logf("%d distinct keys referenced (of which %d literal tr keys and %d literal hook keys), %d messages in %s, %d in %s (deliberately absent: %s) | 共引用 %d 个不同键（其中 %d 个字面 tr 键、%d 个字面钩子键），%s 有 %d 条消息、%s 有 %d 条消息（刻意缺失：%s）",
		len(referenced), len(keys), len(hookKeys), len(zhKeys), web.LangChinese, len(enKeys), web.LangEnglish,
		strings.Join(sortedKeys(intentionallyAbsent), ", "),
		len(referenced), len(keys), len(hookKeys), web.LangChinese, len(zhKeys), web.LangEnglish, len(enKeys),
		strings.Join(sortedKeys(intentionallyAbsent), ", "))
}

// containsString reports whether items holds want. | containsString 报告 items 中是否含有 want。
func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// difference returns the members of a that b does not hold. | difference 返回 a 中 b 没有的成员。
func difference(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for key := range a {
		if !b[key] {
			out[key] = true
		}
	}
	return out
}

// Note | 说明: sortedKeys lives in embed_test.go and is shared by the guards of this package.
// 注意：sortedKeys 定义在 embed_test.go，由本包的各守卫共用。

// Task 4 files are the pages and partials written or rewritten by this task: the tenant, user, role
// and permission administration screens plus the shared form blocks they build on. The
// i18n-attribute guard is scoped to them, because they are the new copy.
// 任务 4 文件是本任务新建或重写的页面与局部模板：租户、用户、角色与权限管理界面，
// 以及它们所依赖的共用表单构件。i18n 属性守卫以此为范围，因为它们是新增文案。
var task4Files = []string{
	"templates/pages/users.html",
	"templates/pages/tenants.html",
	"templates/pages/roles.html",
	"templates/pages/permissions.html",
	"templates/partials/users/list.html",
	"templates/partials/users/row.html",
	"templates/partials/users/form.html",
	"templates/partials/tenants/list.html",
	"templates/partials/tenants/row.html",
	"templates/partials/tenants/form.html",
	"templates/partials/roles/list.html",
	"templates/partials/roles/row.html",
	"templates/partials/roles/form.html",
	"templates/partials/permissions/matrix.html",
	"templates/partials/form.html",
}

// tagRe matches one tag, keeping quoted attribute values in one piece so that a ">" inside a value
// cannot end the tag early. | tagRe 匹配一个标签，把带引号的属性值保持完整，
// 使属性值里的 ">" 不会提前结束标签。
var tagRe = regexp.MustCompile(`(?s)<[^<>"']*(?:"[^"]*"[^<>"']*|'[^']*'[^<>"']*)*>`)

// actionRe matches one template action, including the whitespace-trimming dashes.
// actionRe 匹配一个模板动作，包含两侧去空白的短横线。
var actionRe = regexp.MustCompile(`(?s)\{\{-?.*?-?\}\}`)

// hookRe reports whether a tag carries one of the two client-side hooks. | hookRe 报告标签是否带有两个客户端钩子之一。
var hookRe = regexp.MustCompile(`data-yd-i18n(-attr)?=`)

// attrOpenRe matches the opening of an attribute value and is used to attribute a tr call to the
// attribute it fills. | attrOpenRe 匹配属性取值的起始位置，用于把 tr 调用归到它所填充的属性上。
var attrOpenRe = regexp.MustCompile(`([A-Za-z0-9:_.-]+)\s*=\s*["']`)

// trCallRe matches a literal tr call. | trCallRe 匹配字面 tr 调用。
var trCallRe = regexp.MustCompile(`tr\s+"([^"]+)"`)

// TestEveryUserFacingTextHasI18nAttribute proves the Task 4 pages never render copy a language
// switch cannot reach.
// TestEveryUserFacingTextHasI18nAttribute 证明任务 4 的页面不会渲染出语言切换够不到的文案。
//
// Two rules are enforced per file:
//  1. No literal text between tags: every visible string comes from an action, which is either a
//     {{tr "key"}} or a data value. Copy written straight into the markup is invisible to both
//     catalogues and could only be translated by hand.
//  2. Every literal {{tr "key"}} carries a hook: element text needs data-yd-i18n on its element,
//     an attribute value needs data-yd-i18n-attr="attr:key" on the same tag. A {{tr}} passed to
//     {{template}} is exempt, because the called partial owns the hooks of the markup it emits.
//
// 每个文件执行两条规则：
//  1. 标签之间不得有字面文本：每条可见文案都来自动作，即 {{tr "key"}} 或数据值。
//     直接写进标记的文案对两份目录都不可见，只能靠人工翻译。
//  2. 每个字面 {{tr "key"}} 都带钩子：元素文本需要所在元素上的 data-yd-i18n，
//     属性值需要同一标签上的 data-yd-i18n-attr="attr:key"。
//     传给 {{template}} 的 {{tr}} 豁免，因为被调用的局部模板自己拥有其所产出标记的钩子。
func TestEveryUserFacingTextHasI18nAttribute(t *testing.T) {
	sources := templateSources(t)

	for _, path := range task4Files {
		src, ok := sources[path]
		if !ok {
			t.Errorf("%s does not exist | %s 不存在", path, path)
			continue
		}
		body := commentRe.ReplaceAllString(src, "")
		checkNoLiteralCopy(t, path, body)
		checkTrHooks(t, path, body)
	}
}

// checkNoLiteralCopy fails when a text run between two tags carries letters, because such copy
// never passes through a catalogue. | 当两个标签之间的文本片段含有字母时 checkNoLiteralCopy 判失败，
// 因为这类文案从不经过语言目录。
func checkNoLiteralCopy(t *testing.T, path, body string) {
	t.Helper()
	tags := tagRe.FindAllStringIndex(body, -1)
	cursor := 0
	for _, tag := range tags {
		if run := stripActions(body[cursor:tag[0]]); hasLetter(run) {
			t.Errorf("%s: literal copy %q is rendered without a catalogue key; move it into {{tr \"…\"}} on an element carrying data-yd-i18n | %s：字面文案 %q 未经语言目录就被渲染；请把它改成 {{tr \"…\"}} 并让所在元素带 data-yd-i18n",
				path, strings.TrimSpace(run), path, strings.TrimSpace(run))
		}
		cursor = tag[1]
	}
}

// checkTrHooks verifies rule 2 for every literal tr call of one file. | checkTrHooks 校验单个文件中每个字面 tr 调用的规则 2。
func checkTrHooks(t *testing.T, path, body string) {
	t.Helper()
	tags := tagRe.FindAllStringIndex(body, -1)

	// (a) A tr call inside a tag fills an attribute value, and that attribute needs a hook.
	// (a) 位于标签内部的 tr 调用填充的是属性值，该属性需要钩子。
	for _, span := range tags {
		tag := body[span[0]:span[1]]
		if strings.HasPrefix(tag, "</") || strings.HasPrefix(tag, "<!") {
			continue
		}
		for _, call := range trCallRe.FindAllStringSubmatchIndex(tag, -1) {
			key := tag[call[2]:call[3]]
			name, found := attributeBefore(tag, call[0])
			if !found {
				t.Errorf("%s: {{tr %q}} sits in a tag but in no attribute value | %s：{{tr %q}} 位于标签内却不属于任何属性值", path, key, path, key)
				continue
			}
			if !attributeHookCovers(tag, name, key) {
				t.Errorf("%s: %s=\"{{tr %q}}\" has no data-yd-i18n-attr=%q hook, so the attribute would keep the old language after a switch | %s：%s=\"{{tr %q}}\" 没有 data-yd-i18n-attr=%q 钩子，语言切换后该属性会停在旧语言",
					path, name, key, name+":"+key, path, name, key, name+":"+key)
			}
		}
	}

	// (b) A tr call in content position belongs to the nearest preceding opening tag, which then
	//     needs one of the two hooks. | (b) 位于内容位置的 tr 调用归属于最近的开始标签，
	//     该标签需要两个钩子之一。
	for _, action := range actionRe.FindAllStringIndex(body, -1) {
		if insideTag(tags, action[0], action[1]) {
			continue // handled by (a) | 已由 (a) 处理
		}
		text := body[action[0]:action[1]]
		if !trCallRe.MatchString(text) {
			continue
		}
		// A tr handed to {{template}} is the callee's copy: the partial carries the hook.
		// 交给 {{template}} 的 tr 属于被调用方的文案：钩子在那个局部模板里。
		if strings.HasPrefix(strings.TrimSpace(strings.TrimLeft(text, "{-")), "template") {
			continue
		}
		enclosing := enclosingTag(body, tags, action[0])
		if enclosing == "" {
			t.Errorf("%s: {{tr %s}} is rendered outside any element | %s：{{tr %s}} 在任何元素之外渲染", path, text, path, text)
			continue
		}
		if !hookRe.MatchString(enclosing) {
			t.Errorf("%s: %s renders {{tr%s}} without data-yd-i18n, so a language switch would leave that copy behind | %s：%s 渲染的 {{tr%s}} 没有 data-yd-i18n，语言切换会把这处文案落下",
				path, firstLine(enclosing), text[2:len(text)-2], path, firstLine(enclosing), text[2:len(text)-2])
		}
	}
}

// attributeBefore returns the name of the attribute whose value encloses offset in a tag.
// attributeBefore 返回标签中取值包含偏移 offset 的属性名。
//
// A plain regexp over name="value" pairs is not enough: a template action inside a value carries
// quotes of its own, as in placeholder="{{tr "users.search_placeholder"}}", so the pair would end
// at the first inner quote. Scanning backwards for the nearest name=" opening is immune to that,
// and it is all this check needs.
// 对 name="value" 做简单正则是不够的：取值里的模板动作自带引号，
// 例如 placeholder="{{tr "users.search_placeholder"}}"，取值会在第一个内层引号处结束。
// 改为向前查找最近的 name=" 起始位置即可免疫，而本检查只需要这一点。
func attributeBefore(tag string, offset int) (string, bool) {
	matches := attrOpenRe.FindAllStringSubmatchIndex(tag[:offset], -1)
	if len(matches) == 0 {
		return "", false
	}
	last := matches[len(matches)-1]
	return tag[last[2]:last[3]], true
}

// attributeHookCovers reports whether tag declares data-yd-i18n-attr="name:key" for an attribute.
// attributeHookCovers 报告标签是否声明了指向某属性的 data-yd-i18n-attr="name:key"。
//
// The value may be built from an {{if}}, as in the permission matrix, so the check looks for the
// name:key pair inside the hook value rather than comparing the whole value.
// 该取值可能由 {{if}} 构造，如权限矩阵中所示，因此这里是在钩子取值里查找 name:key 对，
// 而不是整体比较取值。
func attributeHookCovers(tag, name, key string) bool {
	const marker = `data-yd-i18n-attr="`
	start := strings.Index(tag, marker)
	if start < 0 {
		return false
	}
	rest := tag[start+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return false
	}
	value := rest[:end]
	return strings.Contains(value, name+":") && strings.Contains(value, key)
}

// insideTag reports whether [start,end) falls inside one of the tag spans.
// insideTag 报告 [start,end) 是否落在某个标签区间内。
func insideTag(tags [][]int, start, end int) bool {
	for _, tag := range tags {
		if start >= tag[0] && end <= tag[1] {
			return true
		}
	}
	return false
}

// enclosingTag returns the nearest opening tag before offset, empty when there is none.
// enclosingTag 返回偏移前最近的开始标签，没有则返回空串。
func enclosingTag(body string, tags [][]int, offset int) string {
	enclosing := ""
	for _, tag := range tags {
		if tag[1] > offset {
			break
		}
		text := body[tag[0]:tag[1]]
		if strings.HasPrefix(text, "</") || strings.HasPrefix(text, "<!") {
			continue
		}
		enclosing = text
	}
	return enclosing
}

// stripActions removes every template action from a text run. | stripActions 从文本片段中移除全部模板动作。
func stripActions(run string) string {
	return actionRe.ReplaceAllString(run, "")
}

// hasLetter reports whether s holds a Latin or CJK letter, i.e. copy rather than layout.
// hasLetter 报告 s 是否含有拉丁或中日韩字母，即文案而非版面。
func hasLetter(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			return true
		case r >= 0x4E00 && r <= 0x9FFF: // CJK unified ideographs | CJK 统一表意文字
			return true
		case r >= 0x3040 && r <= 0x30FF: // kana | 假名
			return true
		}
	}
	return false
}

// firstLine trims a tag down to its first line, so a multi-line tag stays readable in a report.
// firstLine 把标签裁剪到首行，使多行标签在报告里依然可读。
func firstLine(tag string) string {
	if i := strings.IndexByte(tag, '\n'); i >= 0 {
		return strings.TrimSpace(tag[:i]) + " …"
	}
	return strings.TrimSpace(tag)
}

package web

// components_i18n_test.go — every component must stay readable when the caller supplies only
// catalogue keys. | components_i18n_test.go — 调用方只给目录键时，每个组件仍必须可读。
//
// The catalogue contract is stronger than "the key is present in an attribute": a page rendered on
// the server has to show the message before yd-i18n.js runs, and the same is true inside a drawer or
// modal that was just swapped in. This guard removes every pre-rendered Label/Title/Text counterpart
// from the fixture, strips the client hooks from the output, and then demands that each catalogue key
// still appears somewhere a user or an assistive technology can read it.
// 目录契约比「键出现在属性里」更强：服务端渲染的页面必须在 yd-i18n.js 运行前就显示文案，刚被
// 换入的抽屉或模态也一样。本守卫从固定数据中移除每个预渲染的 Label/Title/Text 对应项，再从输出中
// 剥掉客户端钩子，然后要求每个目录键仍出现在用户或辅助技术可读的位置。

import (
	"bytes"
	"html/template"
	"os"
	"sort"
	"strings"
	"testing"
	"text/template/parse"
)

// TestEveryComponentRendersFromCatalogueKeys renders every component with only its catalogue keys
// and fails when a key is visible solely through a client-side hook. | 只用目录键渲染每个组件，
// 若某个键只能经客户端钩子看到则失败。
func TestEveryComponentRendersFromCatalogueKeys(t *testing.T) {
	// The guard compares against the key itself, so tr returns the key instead of a translated
	// message; otherwise a key that happens to exist in the fixture catalogue would look rendered
	// even when the template only carries a client hook. | 守卫以键名本身作比较，因此 tr 直接返回
	// 键名；否则固定目录中恰好存在的键即使模板只有客户端钩子也会看起来已渲染。
	funcs := templateFuncs()
	funcs["tr"] = func(key string, args ...any) string { return key }
	templates, err := template.New("components").Funcs(funcs).ParseFS(os.DirFS("templates"), "components/*.html", "partials/*.html")
	if err != nil {
		t.Fatalf("parsing components failed: %[1]v | 解析组件失败: %[1]v", err)
	}

	fixture := componentFixture()
	names := make([]string, 0, len(fixture))
	for name := range fixture {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		var data any = fixture[name]
		if name == "components.navigation.tree.nodes" {
			data = map[string]any{"Items": []map[string]any{{"Label": "Child", "LabelKey": "tree.child", "Href": "/child"}}}
		}
		// The shared fixture carries fields for every component. Keep only the fields this template
		// actually reads, otherwise an unrelated LabelKey would be reported against every component.
		// 共享固定数据包含所有组件的字段；只保留本模板真正读取的字段，否则不相干的 LabelKey
		// 会被报告到每个组件上。
		data = filterFixture(data, templateFields(templates, name))
		keys := catalogueKeys(data)
		if len(keys) == 0 {
			continue
		}
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, name, keysOnly(data)); err != nil {
			t.Errorf("%s failed to render with keys only: %v | %s 只给目录键时渲染失败：%v", name, err, name, err)
			continue
		}
		markup := buf.String()
		visible := stripClientI18nHooks(markup)
		hooks := clientHookKeys(markup)
		for _, item := range keys {
			// A key inside a row or option must survive even when the template gates the whole row
			// on the pre-rendered text; a top-level key is enforced only when the component emitted a
			// hook for it. Otherwise the shared fixture's unrelated top-level keys would be reported
			// against every component.
			// 行或选项内的键即使模板用预渲染文本作为整行门槛也必须保留；顶层键只在组件确实为它
			// 输出钩子时才强制。否则共享固定数据里不相干的顶层键会被报告到每个组件上。
			if !item.nested && !hooks[item.key] {
				continue
			}
			if strings.Contains(visible, item.key) {
				continue
			}
			t.Errorf("%s loses catalogue key %q when only the key is supplied; render it with tr as well | %s 只给目录键时丢失 %q；必须同时用 tr 渲染", name, item.key, name, item.key)
		}
	}
}

// templateFields returns every struct field the named template reads. | templateFields 返回指定模板读取的
// 每个结构体字段。
func templateFields(templates *template.Template, name string) map[string]bool {
	fields := map[string]bool{}
	for _, tmpl := range templates.Templates() {
		if tmpl.Name() != name || tmpl.Tree == nil || tmpl.Tree.Root == nil {
			continue
		}
		walkParseNodes(tmpl.Tree.Root, func(node parse.Node) {
			switch typed := node.(type) {
			case *parse.FieldNode:
				for _, ident := range typed.Ident {
					fields[ident] = true
				}
			case *parse.ChainNode:
				for _, ident := range typed.Field {
					fields[ident] = true
				}
			}
		})
	}
	return fields
}

// walkParseNodes visits node and every node below it. | walkParseNodes 访问 node 及其下全部节点。
func walkParseNodes(node parse.Node, visit func(parse.Node)) {
	if node == nil {
		return
	}
	visit(node)
	switch typed := node.(type) {
	case *parse.ListNode:
		if typed == nil {
			return
		}
		for _, child := range typed.Nodes {
			walkParseNodes(child, visit)
		}
	case *parse.ActionNode:
		if typed != nil {
			walkParseNodes(typed.Pipe, visit)
		}
	case *parse.IfNode:
		if typed != nil {
			walkParseNodes(typed.Pipe, visit)
			walkParseNodes(typed.List, visit)
			walkParseNodes(typed.ElseList, visit)
		}
	case *parse.RangeNode:
		if typed != nil {
			walkParseNodes(typed.Pipe, visit)
			walkParseNodes(typed.List, visit)
			walkParseNodes(typed.ElseList, visit)
		}
	case *parse.WithNode:
		if typed != nil {
			walkParseNodes(typed.Pipe, visit)
			walkParseNodes(typed.List, visit)
			walkParseNodes(typed.ElseList, visit)
		}
	case *parse.TemplateNode:
		if typed != nil {
			walkParseNodes(typed.Pipe, visit)
		}
	case *parse.PipeNode:
		if typed == nil {
			return
		}
		for _, cmd := range typed.Cmds {
			for _, arg := range cmd.Args {
				walkParseNodes(arg, visit)
			}
		}
	}
}

// filterFixture keeps only the fields the template reads, recursing through maps and slices.
// filterFixture 只保留模板读取的字段，并递归处理映射与切片。
func filterFixture(value any, fields map[string]bool) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for name, item := range typed {
			if !fields[name] {
				continue
			}
			out[name] = filterFixture(item, fields)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			converted, _ := filterFixture(item, fields).(map[string]any)
			out = append(out, converted)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, filterFixture(item, fields))
		}
		return out
	default:
		return value
	}
}

// catalogueKey is one catalogue key found in a fixture; nested reports whether it belonged to a row
// or option rather than the component's top-level data. | catalogueKey 是固定数据中的一个目录键；
// nested 表示它来自行或选项而不是组件顶层数据。
type catalogueKey struct {
	key    string
	nested bool
}

// catalogueKeys returns every non-empty value stored under a key ending in "Key". The fixture uses
// Key for option identities as well, so this deliberately includes the bare "Key" field. Keys found
// below a slice are marked nested, because a template that gates a whole row on pre-rendered text
// would otherwise drop them silently. | catalogueKeys 返回所有以 "Key" 结尾、取值非空的键；固定
// 数据也用 Key 表示选项标识，因此有意包含裸的 "Key" 字段。切片之下的键标记为 nested，因为用预渲染
// 文本作为整行门槛的模板会悄悄丢掉它们。
func catalogueKeys(value any) []catalogueKey {
	seen := map[string]bool{}
	var walk func(any, bool)
	walk = func(v any, nested bool) {
		switch typed := v.(type) {
		case map[string]any:
			for name, item := range typed {
				if strings.HasSuffix(name, "Key") {
					if key, ok := item.(string); ok && key != "" {
						seen[key] = seen[key] || nested
					}
				}
				walk(item, nested)
			}
		case []map[string]any:
			for _, item := range typed {
				walk(item, true)
			}
		case []any:
			for _, item := range typed {
				walk(item, true)
			}
		}
	}
	walk(value, false)
	keys := make([]catalogueKey, 0, len(seen))
	for key, nested := range seen {
		keys = append(keys, catalogueKey{key: key, nested: nested})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].key < keys[j].key })
	return keys
}

// clientHookKeys returns every catalogue key carried by a data-yd-i18n or data-yd-i18n-attr hook.
// clientHookKeys 返回 data-yd-i18n 或 data-yd-i18n-attr 钩子携带的每个目录键。
func clientHookKeys(markup string) map[string]bool {
	keys := map[string]bool{}
	collect := func(attribute string) {
		for start := 0; ; {
			index := strings.Index(markup[start:], attribute)
			if index < 0 {
				return
			}
			index += start
			valueStart := index + len(attribute)
			end := strings.Index(markup[valueStart:], `"`)
			if end < 0 {
				return
			}
			value := markup[valueStart : valueStart+end]
			if attribute == `data-yd-i18n="` {
				keys[value] = true
			} else {
				for _, pair := range strings.Split(value, ",") {
					if colon := strings.Index(pair, ":"); colon >= 0 {
						keys[strings.TrimSpace(pair[colon+1:])] = true
					}
				}
			}
			start = valueStart + end + 1
		}
	}
	collect(`data-yd-i18n="`)
	collect(`data-yd-i18n-attr="`)
	return keys
}

// keysOnly drops each pre-rendered text field whose corresponding *Key field is present, recursing
// through the maps and slices a component fixture uses. | keysOnly 删除每个存在对应 *Key 的预渲染
// 文本字段，并递归处理组件固定数据使用的映射与切片。
func keysOnly(value any) any {
	// A few labels use a shortened key name, so the generic "<name>Key" lookup would leave the
	// pre-rendered text in place and hide a missing tr fallback. | 少数标签使用缩短的键名，
	// 通用的 "<name>Key" 查找会留下预渲染文本，从而掩盖缺失的 tr 回退。
	counterparts := map[string]string{
		"TriggerLabel": "TriggerKey",
		"SubmitLabel":  "SubmitKey",
		"CancelLabel":  "CancelKey",
		"AddLabel":     "AddKey",
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for name, item := range typed {
			if strings.HasSuffix(name, "Key") {
				out[name] = item
				continue
			}
			if counterpart, ok := counterparts[name]; ok {
				if _, hasKey := typed[counterpart]; hasKey {
					continue
				}
			}
			if _, hasKey := typed[name+"Key"]; hasKey {
				continue
			}
			out[name] = keysOnly(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			converted, _ := keysOnly(item).(map[string]any)
			out = append(out, converted)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, keysOnly(item))
		}
		return out
	default:
		return value
	}
}

// stripClientI18nHooks removes the data-yd-i18n and data-yd-i18n-attr attributes so a key that only
// survives through a hook cannot satisfy the guard. | stripClientI18nHooks 移除 data-yd-i18n 与
// data-yd-i18n-attr 属性，使只靠钩子存活的键无法满足守卫。
func stripClientI18nHooks(markup string) string {
	for _, attribute := range []string{`data-yd-i18n-attr="`, `data-yd-i18n="`} {
		for {
			start := strings.Index(markup, attribute)
			if start < 0 {
				break
			}
			rest := markup[start+len(attribute):]
			end := strings.Index(rest, `"`)
			if end < 0 {
				markup = markup[:start]
				break
			}
			markup = markup[:start] + rest[end+1:]
		}
	}
	return markup
}

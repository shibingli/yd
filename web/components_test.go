package web

// components_test.go — render every UI-kit partial through the same html/template pipeline the
// renderer uses. | components_test.go — 用渲染器同一条 html/template 流水线渲染每个 UI 组件局部模板。
//
// A component that only ever appears inside a page is easy to break without noticing: the page's
// own fixture may fill the branch that hides the bug. Rendering each define directly, with a
// fixture that fills every optional key, catches a missing field, a bad pipeline or a mistyped
// template name at the only moment it is cheap to fix.
// 只出现在页面里的组件很容易在无人察觉时被改坏：页面自带的固定数据可能恰好填中掩盖缺陷的分支。
// 直接用填满每个可选键的固定数据渲染每个 define，能在修复成本最低的时刻发现缺失字段、错误管道或
// 拼错的模板名。

import (
	"bytes"
	"html/template"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/shibingli/yd/http/chart"
)

// componentFixture returns one filled data set per defined template. | componentFixture 为每个已定义
// 模板返回一份填满的数据。
func componentFixture() map[string]map[string]any {
	options := []map[string]any{
		{"Value": "a", "Label": "A", "Key": "option.a", "Selected": true, "Checked": true},
		{"Value": "b", "Label": "B", "Key": "option.b"},
	}
	items := []map[string]any{
		{"Index": 1, "Label": "One", "LabelKey": "step.one", "Href": "/one", "Active": true, "Current": true, "State": "current", "Key": "nav.one", "Danger": true, "Attrs": template.HTMLAttr(`hx-get="/one"`)},
		{"Index": 2, "Label": "Two", "LabelKey": "step.two", "Href": "/two", "State": "done"},
	}
	columns := []map[string]any{
		{"Label": "Name", "LabelKey": "table.name"},
		{"Label": "Amount", "LabelKey": "table.amount", "Numeric": true},
		{"Label": "Actions", "LabelKey": "table.actions", "Actions": true},
	}
	base := map[string]any{
		"ID": "field-id", "Name": "field", "Type": "text", "Value": "v",
		"Label": "Label", "LabelKey": "field.label",
		"Placeholder": "Placeholder", "PlaceholderKey": "field.placeholder",
		"Hint": "Hint", "HintKey": "field.hint",
		"Error": "Error", "ErrorKey": "field.error",
		"Autocomplete": "off", "InputMode": "text", "MaxLength": 64, "Pattern": "[a-z]+",
		"Required": true, "Sensitive": true, "Invalid": true, "Disabled": false, "Readonly": false,
		"Options": options, "Checked": true, "Block": true, "Rows": []any{1, 2},
		"SubmitKey": "action.save", "SubmitLabel": "Save", "SubmitAttrs": template.HTMLAttr(`data-yd-perm="user:create"`),
		"CancelKey": "action.cancel", "CancelLabel": "Cancel", "CancelHref": "/back",
		"Action": "/search", "Get": "/search", "Target": "#results",
		"Accept": "image/*", "Multiple": true,
		"AddLabel": "Add", "AddKey": "ui.add",
		"Source": "#yd-regions", "Province": "110000", "City": "110100", "District": "110101",
		"ProvinceName": "province", "CityName": "city", "DistrictName": "district",
		"From": "2026-01-01", "To": "2026-01-31", "FromName": "from", "ToName": "to",
		"SendURL": "/sms", "TargetID": "#sms",
		"Title": "Title", "TitleKey": "page.title",
		"Lead": "Lead", "LeadKey": "page.lead",
		"Description": "Description", "DescriptionKey": "page.description",
		"Actions": template.HTML(`<button type="button" class="yd-btn">Go</button>`),
		"Icon":    template.HTML(`<span aria-hidden="true">i</span>`),
		"Compact": true, "Text": "Body", "TextKey": "page.body",
		"Level": "warn", "Tone": "success", "Size": "lg",
		"RemoveAttrs": template.HTMLAttr(`hx-delete="/item/1"`), "RemoveKey": "ui.remove",
		"PanelLabel": "Menu", "PanelLabelKey": "menu.label",
		"TriggerClass": "yd-btn yd-btn--ghost", "TriggerAttrs": template.HTMLAttr(`aria-label="menu"`),
		"Items": items, "ItemsHTML": template.HTML(`<button type="button" class="yd-dropdown__item">Raw</button>`),
		"Initials": "YD", "Delta": "+3", "Trend": "up",
		"Caption": "Table", "CaptionKey": "table.caption", "Columns": columns,
		"BodyID": "rows", "BodyAttrs": template.HTMLAttr(`hx-get="/rows"`),
		"Footer": "Source", "FooterKey": "chart.footer",
		"SVG": template.HTML(`<svg class="yd-chart"><title>Chart</title></svg>`),
		"Legend": []map[string]any{
			{"Label": "Requests", "Class": "yd-chart__series--1"},
			{"Label": "Errors", "Class": "yd-chart__series--2"},
		},
		"ID2": "dialog", "Size2": "lg", "Value2": "copied",
	}
	// A few names need a second id because two partials share one key namespace; clone the base.
	clone := func(overrides map[string]any) map[string]any {
		out := make(map[string]any, len(base)+len(overrides))
		for k, v := range base {
			out[k] = v
		}
		for k, v := range overrides {
			out[k] = v
		}
		return out
	}

	return map[string]map[string]any{
		"components.form.field":            clone(nil),
		"components.form.textarea":         clone(nil),
		"components.form.select":           clone(nil),
		"components.form.check":            clone(nil),
		"components.form.radio":            clone(nil),
		"components.form.toggle":           clone(nil),
		"components.form.segmented":        clone(nil),
		"components.form.password":         clone(nil),
		"components.form.actions":          clone(nil),
		"components.form.search":           clone(nil),
		"components.form.file":             clone(nil),
		"components.form.tags":             clone(nil),
		"components.layout.page-header":    clone(nil),
		"components.layout.card.open":      clone(nil),
		"components.layout.card.close":     clone(nil),
		"components.layout.empty":          clone(nil),
		"components.feedback.alert":        clone(nil),
		"components.feedback.tag":          clone(nil),
		"components.feedback.spinner":      clone(nil),
		"components.feedback.skeleton":     clone(nil),
		"components.feedback.toast":        clone(nil),
		"components.feedback.toast-region": clone(nil),
		"components.navigation.tabs":       clone(nil),
		"components.navigation.breadcrumb": clone(nil),
		"components.navigation.steps":      clone(nil),
		"components.navigation.dropdown":   clone(nil),
		"components.data.avatar":           clone(nil),
		"components.data.stat":             clone(nil),
		"components.data.table.open":       clone(nil),
		"components.data.table.close":      clone(nil),
		"components.chart":                 clone(nil),
		"components.overlay.modal.open":    clone(map[string]any{"ID": "modal-id", "Size": "lg"}),
		"components.overlay.modal.close":   clone(nil),
		"components.cn.phone":              clone(nil),
		"components.cn.idcard":             clone(nil),
		"components.cn.bankcard":           clone(nil),
		"components.cn.creditcode":         clone(nil),
		"components.cn.amount":             clone(nil),
		"components.cn.region":             clone(nil),
		"components.cn.daterange":          clone(nil),
		"components.cn.sms":                clone(nil),
		"components.cn.copy":               clone(nil),
		"components.display.accordion": clone(map[string]any{
			"Items": []map[string]any{{"ID": "a1", "Title": "One", "TitleKey": "acc.one", "Open": true, "Body": template.HTML("<p>Body</p>")}},
		}),
		"components.display.aspect": clone(map[string]any{
			"Ratio": "16x9", "Content": template.HTML(`<div>media</div>`), "Caption": "Caption", "CaptionKey": "fig.caption",
		}),
		"components.display.avatar-group": clone(map[string]any{
			"Items": []map[string]any{{"Initials": "A", "Label": "Ann", "LabelKey": "user.ann"}, {"Initials": "B"}},
			"More":  3,
		}),
		"components.display.badge": clone(map[string]any{"Tone": "success", "Label": "Active", "LabelKey": "status.active"}),
		"components.display.button-group": clone(map[string]any{
			"Items": []map[string]any{{"Label": "One", "Href": "/one", "Active": true}, {"Label": "Two", "Href": "/two"}},
		}),
		"components.display.carousel": clone(map[string]any{
			"Label": "Gallery", "LabelKey": "gallery.title",
			"Items": []map[string]any{{"ID": "slide-1", "Content": template.HTML("<p>One</p>"), "Caption": "First"}, {"ID": "slide-2", "Content": template.HTML("<p>Two</p>")}},
		}),
		"components.display.code":      clone(map[string]any{"Language": "go", "Label": "main.go", "LabelKey": "code.lang", "Code": "fmt.Println(1)", "CopyValue": "fmt.Println(1)"}),
		"components.display.countdown": clone(map[string]any{"Seconds": 60, "Label": "Ends in", "LabelKey": "countdown.label"}),
		"components.display.descriptions": clone(map[string]any{
			"Items": []map[string]any{{"Label": "Name", "LabelKey": "field.name", "Value": "Ann"}, {"Label": "Mail", "Value": "a@example.com"}},
		}),
		"components.display.divider":       clone(map[string]any{"Label": "Section"}),
		"components.display.ellipsis":      clone(map[string]any{"Lines": 2, "Text": "Long text", "Title": "Long text"}),
		"components.display.figure":        clone(map[string]any{"Src": "/static/img/yd-icon.svg", "Alt": "Icon", "Caption": "Caption"}),
		"components.display.gradient-text": clone(map[string]any{"Text": "Gradient", "TextKey": "text.gradient"}),
		"components.display.highlight":     clone(map[string]any{"HTML": template.HTML("<strong>Hit</strong>")}),
		"components.display.item":          clone(map[string]any{"Media": template.HTML("<span class=\"yd-avatar\">A</span>"), "Title": "Item", "Text": "Text", "Meta": "Meta", "Actions": template.HTML(`<button class="yd-btn">Go</button>`)}),
		"components.display.kbd":           clone(map[string]any{"Keys": []string{"Ctrl", "K"}, "Label": "Search", "LabelKey": "kbd.search"}),
		"components.display.list":          clone(map[string]any{"Items": []map[string]any{{"Title": "One", "Text": "Text", "Meta": "2", "Href": "/one"}, {"Title": "Two"}}}),
		"components.display.marquee":       clone(map[string]any{"Items": []map[string]any{{"Text": "Notice", "TextKey": "notice.one"}}}),
		"components.display.meter":         clone(map[string]any{"Value": 42, "Max": 100, "Label": "42%", "Tone": "success"}),
		"components.display.number":        clone(map[string]any{"Value": 1234.5, "Format": "currency", "Label": "Revenue", "LabelKey": "stat.revenue"}),
		"components.display.prose":         clone(map[string]any{"Content": template.HTML("<p>Prose</p>")}),
		"components.display.rate":          clone(map[string]any{"ID": "rate", "Name": "rate", "Value": 3, "Stars": []int{5, 4, 3, 2, 1}, "Label": "Rating", "LabelKey": "rate.label"}),
		"components.display.result":        clone(map[string]any{"Status": "success", "Title": "Done", "Text": "All good", "Actions": template.HTML(`<a class="yd-btn" href="/">Home</a>`), "Code": "200"}),
		"components.display.scroll-area":   clone(map[string]any{"Size": "md", "Content": template.HTML("<p>Scroll</p>")}),
		"components.display.slider":        clone(map[string]any{"ID": "slider", "Name": "level", "Value": 30, "Min": 0, "Max": 100, "Step": 5, "Label": "Level", "Hint": "Hint"}),
		"components.display.timeline": clone(map[string]any{
			"Items": []map[string]any{{"Time": "10:00", "Title": "Created", "Text": "By Ann", "State": "done"}, {"Time": "11:00", "Title": "Reviewed", "State": "current"}},
		}),
		"components.display.watermark":   clone(map[string]any{"Text": "CONFIDENTIAL", "Count": 12, "Content": template.HTML("<p>Body</p>")}),
		"components.navigation.anchor":   clone(map[string]any{"Label": "On this page", "LabelKey": "nav.on_page", "Items": []map[string]any{{"Href": "#one", "Label": "One", "Active": true}, {"Href": "#two", "Label": "Two"}}}),
		"components.navigation.affix":    clone(map[string]any{"Content": template.HTML("<p>Sticky</p>")}),
		"components.navigation.back-top": clone(nil),
		"components.navigation.fab":      clone(map[string]any{"Href": "/new", "Label": "New", "LabelKey": "action.new", "Primary": true}),
		"components.navigation.command": clone(map[string]any{
			"TriggerLabel": "Search", "TriggerKey": "action.search", "Placeholder": "Type a command",
			"Items": []map[string]any{{"Group": "Pages", "GroupKey": "cmd.pages", "Label": "Users", "LabelKey": "nav.users", "Href": "/users"}, {"Group": "Pages", "Label": "Roles", "Href": "/roles"}},
		}),
		"components.navigation.context-menu": clone(map[string]any{"Content": template.HTML("<p>Right-click me</p>"), "Items": []map[string]any{{"Label": "Edit", "Href": "/edit"}, {"Label": "Delete", "Href": "/delete", "Danger": true}}}),
		"components.navigation.menubar": clone(map[string]any{
			"Label": "Menu", "LabelKey": "nav.menu",
			"Items": []map[string]any{{"Label": "File", "LabelKey": "menu.file", "Items": []map[string]any{{"Label": "New", "Href": "/new"}, {"Label": "Open", "Href": "/open"}}}},
		}),
		"components.navigation.pagination": clone(map[string]any{
			"Label": "Pagination", "LabelKey": "nav.pagination", "Summary": "1-10 of 42", "PrevHref": "/prev", "NextHref": "/next",
			"Pages": []map[string]any{{"Number": 1, "Href": "/1", "Current": true}, {"Number": 2, "Href": "/2"}, {"Ellipsis": true}, {"Number": 9, "Href": "/9"}},
		}),
		"components.navigation.tree": clone(map[string]any{
			"Items": []map[string]any{{"Label": "Root", "Open": true, "Children": []map[string]any{{"Label": "Child", "Href": "/child", "Meta": "3"}}}, {"Label": "Leaf", "Href": "/leaf"}},
		}),
		"components.navigation.menu": clone(map[string]any{
			"Label": "Navigation", "LabelKey": "nav.menu",
			"Items": []map[string]any{{
				"Section": "Main", "SectionKey": "nav.section.main",
				"Items": []map[string]any{{"Label": "Home", "LabelKey": "nav.home", "Href": "/", "Active": true, "Icon": template.HTML("<span>i</span>")}},
			}},
		}),
		"components.feedback.message":      clone(map[string]any{"Tone": "success", "Text": "Saved", "TextKey": "msg.saved"}),
		"components.feedback.notification": clone(map[string]any{"Tone": "error", "Title": "Failed", "Text": "Retry", "DismissAttrs": template.HTMLAttr(`data-yd-flash-dismiss`)}),
		"components.feedback.popconfirm":   clone(map[string]any{"Message": "Delete?", "Label": "Delete", "LabelKey": "action.delete", "Submit": true, "Attrs": template.HTMLAttr(`hx-delete="/item/1"`)}),
		"components.data.kpi":              clone(map[string]any{"Label": "Requests", "LabelKey": "stat.requests", "Value": "1,234", "Delta": "+3%", "Trend": "up", "Spark": template.HTML(`<svg></svg>`), "Hint": "Last 24h"}),
		"components.data.sort-header":      clone(map[string]any{"Label": "Name", "LabelKey": "field.name", "Href": "?sort=name&dir=asc", "Active": true, "Dir": "asc"}),
		"components.data.log":              clone(map[string]any{"Label": "Log", "LabelKey": "log.title", "Lines": []map[string]any{{"Time": "10:00", "Level": "error", "Body": "boom"}, {"Time": "10:01", "Level": "info", "Body": "ok"}}}),
		"components.form.number":           clone(map[string]any{"ID": "num", "Name": "num", "Value": 5, "Label": "Count", "Hint": "Hint"}),
		"components.form.otp":              clone(map[string]any{"ID": "otp", "Name": "code", "Value": "123456", "Length": 6, "Slots": []int{0, 1, 2, 3, 4, 5}, "Label": "Code"}),
		"components.form.color":            clone(map[string]any{"ID": "color", "Name": "color", "Value": "#2563eb", "Label": "Colour"}),
		"components.form.combobox": clone(map[string]any{
			"ID": "combo", "Name": "role", "Value": "admin", "LabelValue": "Admin", "Label": "Role",
			"Options": []map[string]any{{"Value": "admin", "Label": "Admin", "Key": "role.admin"}, {"Value": "member", "Label": "Member"}},
		}),
		"components.form.date":     clone(map[string]any{"ID": "date", "Name": "date", "Value": "2026-10-25", "Label": "Date"}),
		"components.form.time":     clone(map[string]any{"ID": "time", "Name": "time", "Value": "10:30", "Label": "Time"}),
		"components.form.datetime": clone(map[string]any{"ID": "datetime", "Name": "at", "Value": "2026-10-25T10:30", "Label": "At"}),
		"components.form.editor":   clone(map[string]any{"ID": "editor", "Name": "body", "Value": "# Title", "Label": "Body", "Rows": 8}),
	}
}

// TestEveryComponentRenders parses the component tree once and executes every define with a
// fully populated fixture. | TestEveryComponentRenders 解析组件树一次，并用填满的固定数据执行每个 define。
func TestEveryComponentRenders(t *testing.T) {
	templates, err := template.New("components").Funcs(templateFuncs()).ParseFS(os.DirFS("templates"), "components/*.html", "partials/icon.html")
	if err != nil {
		t.Fatalf("parsing components failed: %v | 解析组件失败: %v", err, err)
	}

	defined := make(map[string]bool)
	for _, tmpl := range templates.Templates() {
		if strings.HasPrefix(tmpl.Name(), "components.") {
			defined[tmpl.Name()] = true
		}
	}
	if len(defined) == 0 {
		t.Fatal("no component template was defined | 没有定义任何组件模板")
	}

	fixture := componentFixture()
	names := make([]string, 0, len(defined))
	for name := range defined {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		var data any
		var ok bool
		if name == "components.navigation.tree.nodes" {
			data = []map[string]any{{"Label": "Child", "Href": "/child"}}
			ok = true
		} else {
			data, ok = fixture[name]
		}
		if !ok {
			t.Errorf("%s has no fixture; add one to componentFixture | %s 没有固定数据，请补进 componentFixture", name, name)
			continue
		}
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
			t.Errorf("%s failed to render: %v | %s 渲染失败: %v", name, err, name, err)
			continue
		}
		if buf.Len() == 0 {
			t.Errorf("%s rendered nothing | %s 没有输出任何内容", name, name)
		}
	}
	for name := range fixture {
		if !defined[name] {
			t.Errorf("fixture %q names a template that does not exist | 固定数据 %q 指向不存在的模板", name, name)
		}
	}
}

// TestChartComponentKeepsTheSVGUnescaped proves the frame does not double-escape the renderer's
// output: a chart that arrives as &lt;svg&gt; is a chart nobody can see.
// TestChartComponentKeepsTheSVGUnescaped 证明外框没有二次转义渲染器的输出：变成 &lt;svg&gt; 的图表
// 是没人看得见的图表。
func TestChartComponentKeepsTheSVGUnescaped(t *testing.T) {
	templates, err := template.New("components").Funcs(templateFuncs()).ParseFS(os.DirFS("templates"), "components/*.html", "partials/icon.html")
	if err != nil {
		t.Fatalf("parsing components failed: %v | 解析组件失败: %v", err, err)
	}
	series := []chart.Series{{Label: "Requests", Points: []chart.Point{{Label: "Jan", Value: 10}, {Label: "Feb", Value: 20}}}}
	svg := chart.Line(series, chart.Options{Title: "Traffic"})
	var buf bytes.Buffer
	err = templates.ExecuteTemplate(&buf, "components.chart", map[string]any{
		"Title":  "Traffic",
		"SVG":    template.HTML(svg),
		"Legend": chart.Legend(series),
	})
	if err != nil {
		t.Fatalf("chart component failed to render: %v | 图表组件渲染失败: %v", err, err)
	}
	out := buf.String()
	if strings.Contains(out, "&lt;svg") {
		t.Error("the chart SVG was HTML-escaped | 图表 SVG 被 HTML 转义了")
	}
	for _, want := range []string{"<svg", "yd-chart-card__legend", "yd-chart__series--1", "<title>Jan: 10</title>"} {
		if !strings.Contains(out, want) {
			t.Errorf("chart component lacks %q | 图表组件缺少 %q", want, want)
		}
	}
}

// TestChinaComponentsKeepSubmittedFields checks the progressive-enhancement promise: every
// China-specific control still carries a visible, named form field even though Alpine is absent
// in the renderer. | TestChinaComponentsKeepSubmittedFields 校验渐进增强承诺：即使渲染器里没有
// Alpine，每个中国常用控件仍然带有可见、带 name 的表单字段。
func TestChinaComponentsKeepSubmittedFields(t *testing.T) {
	templates, err := template.New("components").Funcs(templateFuncs()).ParseFS(os.DirFS("templates"), "components/*.html", "partials/icon.html")
	if err != nil {
		t.Fatalf("parsing components failed: %v | 解析组件失败: %v", err, err)
	}

	fixture := componentFixture()
	controls := map[string][]string{
		"components.cn.phone":      {`name="field"`, `type="tel"`},
		"components.cn.idcard":     {`name="field"`, `maxlength="18"`},
		"components.cn.bankcard":   {`name="field"`, `autocomplete="cc-number"`},
		"components.cn.creditcode": {`name="field"`, `maxlength="18"`},
		"components.cn.amount":     {`name="field"`, `inputmode="decimal"`},
		"components.cn.region":     {`name="province"`, `name="city"`, `name="district"`},
		"components.cn.daterange":  {`name="from"`, `name="to"`, `type="date"`},
		"components.cn.sms":        {`name="field"`, `autocomplete="one-time-code"`},
	}
	for name, wants := range controls {
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, name, fixture[name]); err != nil {
			t.Errorf("%s failed to render: %v | %s 渲染失败: %v", name, err, name, err)
			continue
		}
		out := buf.String()
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("%s lacks %q, so the control would not submit without Alpine | %s 缺少 %q，没有 Alpine 时控件无法提交", name, want, name, want)
			}
		}
	}
}

// TestComponentsRenderFromI18nKeysOnly checks the native i18n contract: a component can be given
// only catalogue keys, with no pre-rendered English or Chinese text, and still produce visible
// copy. This is what lets a controller omit the duplicated Label/Hint values and rely on the
// catalogue as the single source of truth.
// TestComponentsRenderFromI18nKeysOnly 校验原生 i18n 契约：只给组件目录键、不预渲染任何中英文文案，
// 仍能输出可见文案。这样控制器可以省略重复的 Label/Hint 值，把语言目录当作唯一事实来源。
func TestComponentsRenderFromI18nKeysOnly(t *testing.T) {
	templates, err := template.New("components").Funcs(templateFuncs()).ParseFS(os.DirFS("templates"), "components/*.html", "partials/icon.html")
	if err != nil {
		t.Fatalf("parsing components failed: %v | 解析组件失败: %v", err, err)
	}
	cases := []struct {
		name string
		data map[string]any
		want []string
	}{
		{
			name: "components.form.field",
			data: map[string]any{"ID": "email", "Name": "email", "LabelKey": "field.email", "HintKey": "field.email_hint"},
			want: []string{"field.email", "field.email_hint", `data-yd-i18n="field.email"`},
		},
		{
			name: "components.form.select",
			data: map[string]any{
				"ID": "role", "Name": "role", "LabelKey": "field.role",
				"Options": []map[string]any{{"Value": "admin", "Key": "role.admin"}},
			},
			want: []string{"field.role", "role.admin"},
		},
		{
			name: "components.form.check",
			data: map[string]any{"ID": "perm", "Name": "perm", "Value": "user:read", "LabelKey": "perm.read", "HintKey": "perm.read_hint"},
			want: []string{"perm.read", "perm.read_hint"},
		},
		{
			name: "components.form.actions",
			data: map[string]any{"SubmitKey": "action.save", "CancelKey": "action.cancel"},
			want: []string{"action.save", "action.cancel"},
		},
		{
			name: "components.cn.phone",
			data: map[string]any{"ID": "phone", "Name": "phone", "LabelKey": "field.phone", "HintKey": "field.phone_hint"},
			want: []string{"field.phone", "field.phone_hint"},
		},
		{
			name: "components.feedback.alert",
			data: map[string]any{"Level": "info", "TitleKey": "alert.title", "TextKey": "alert.text"},
			want: []string{"alert.title", "alert.text"},
		},
		{
			name: "components.layout.empty",
			data: map[string]any{"TitleKey": "empty.title", "TextKey": "empty.text"},
			want: []string{"empty.title", "empty.text"},
		},
		{
			name: "components.display.result",
			data: map[string]any{"Status": "success", "TitleKey": "result.title", "TextKey": "result.text"},
			want: []string{"result.title", "result.text"},
		},
		{
			name: "components.navigation.fab",
			data: map[string]any{"Href": "/new", "LabelKey": "action.create"},
			want: []string{"action.create"},
		},
		{
			name: "components.data.stat",
			data: map[string]any{"LabelKey": "stat.requests", "Value": "42", "HintKey": "stat.hint"},
			want: []string{"stat.requests", "stat.hint"},
		},
		{
			name: "components.chart",
			data: map[string]any{"TitleKey": "chart.title", "SVG": template.HTML(`<svg class="yd-chart"></svg>`)},
			want: []string{"chart.title"},
		},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, tc.name, tc.data); err != nil {
			t.Errorf("%s failed to render: %v | %s 渲染失败: %v", tc.name, err, tc.name, err)
			continue
		}
		out := buf.String()
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Errorf("%s lacks %q when only keys are supplied | %s 只提供键时缺少 %q", tc.name, want, tc.name, want)
			}
		}
	}
}

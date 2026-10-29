package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkflowDesignerUsesComponentControls pins the designer's runtime-built inspector to the
// shared component contracts: checkboxes use yd-check markup, selects use the ydSelect surface,
// and dynamically inserted components are initialised through Alpine instead of staying native. |
// TestWorkflowDesignerUsesComponentControls 固定设计器运行时属性面板的组件契约：复选框使用
// yd-check 标记，下拉框使用 ydSelect 表面，动态插入的组件通过 Alpine 初始化而不是停留在原生控件。
func TestWorkflowDesignerUsesComponentControls(t *testing.T) {
	root := repoRoot(t)
	script, err := os.ReadFile(filepath.Join(root, "web", "static", "js", "yd-workflow-designer.js"))
	if err != nil {
		t.Fatalf("read designer script: %v | 读取设计器脚本失败", err)
	}
	body := string(script)
	for _, want := range []string{
		"yd-check__input",
		"yd-check__box",
		"yd-check__label",
		"yd-selectbox",
		"ydSelect",
		"Alpine.initTree",
		"Alpine.destroyTree",
		"data-selected-label",
		"yd-flow-icon-chevron-down",
		"optionTemplate.content.appendChild",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("designer script lacks component contract %q | 设计器脚本缺少组件契约 %q", want, want)
		}
	}
	for _, unwanted := range []string{
		"el('input', { type: 'checkbox' })",
		"el('select', { className: 'yd-select' })",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("designer script still builds a native control: %q | 设计器脚本仍在构建原生控件：%q", unwanted, unwanted)
		}
	}

	page, err := os.ReadFile(filepath.Join(root, "tooling", "scaffold", "templates", "_name_", "web", "templates", "mgr", "pages", "workflow_designer.html"))
	if err != nil {
		t.Fatalf("read designer page: %v | 读取设计器页面失败", err)
	}
	if !strings.Contains(string(page), `id="yd-flow-icon-chevron-down"`) {
		t.Error("designer page lacks the server-rendered icon template | 设计器页面缺少服务端图标模板")
	}

	taskPath := filepath.Join(root, "tooling", "scaffold", "templates", "_name_", "web", "templates", "partials", "workflows", "task.html")
	task, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatalf("read workflow task template: %v | 读取任务模板失败", err)
	}
	taskBody := string(task)
	for _, want := range []string{
		`components.form.date`,
		`components.form.datetime`,
		`components.form.time`,
	} {
		if !strings.Contains(taskBody, want) {
			t.Errorf("workflow task template lacks %q | 任务模板缺少 %q", want, want)
		}
	}
	for _, unwanted := range []string{`type="date"`, `type="datetime-local"`, `type="time"`} {
		if strings.Contains(taskBody, unwanted) {
			t.Errorf("workflow task template uses native %q | 任务模板使用原生控件 %q", unwanted, unwanted)
		}
	}
	// FormField.Min/Max bound numeric values, not dates. Forwarding the zero values would render
	// data-max="0" and disable every day in the picker. | FormField.Min/Max 限制的是数值而不是日期；
	// 转发零值会渲染 data-max="0"，让选择器禁用全部日期。
	for _, branch := range []struct {
		component string
		next      string
	}{
		{"components.form.date", `{{- else if eq .Field.Type "datetime" -}}`},
		{"components.form.datetime", `{{- else if eq .Field.Type "time" -}}`},
		{"components.form.time", `{{- else -}}`},
	} {
		start := strings.Index(taskBody, `"`+branch.component+`" (dict`)
		if start < 0 {
			t.Errorf("workflow task template lacks %q | 任务模板缺少 %q", branch.component, branch.component)
			continue
		}
		end := strings.Index(taskBody[start:], branch.next)
		if end < 0 {
			t.Errorf("workflow task template has no end marker for %q | 任务模板缺少 %q 的结束标记", branch.component, branch.component)
			continue
		}
		segment := taskBody[start : start+end]
		if strings.Contains(segment, `"Min" .Field.Min`) || strings.Contains(segment, `"Max" .Field.Max`) {
			t.Errorf("%q must not forward numeric FormField bounds | %q 不得转发数值型表单边界", branch.component, branch.component)
		}
	}
}

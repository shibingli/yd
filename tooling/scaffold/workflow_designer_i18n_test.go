package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibingli/ydx/regexpx"
)

// workflowDesignerLabelPattern captures every catalogue slug referenced by the designer script,
// including NODE_KEYS values and L() call sites. | workflowDesignerLabelPattern 捕获设计器脚本
// 引用的全部目录键，包含 NODE_KEYS 取值与 L() 调用点。
var workflowDesignerLabelPattern = regexpx.MustCompile(`'(label_[a-z0-9_]+)'`)

// TestWorkflowDesignerLabelsStayInCatalogue keeps the designer script from growing its own copy
// of the interface: every slug it references must exist in both catalogues and in the label
// island the page renders. | TestWorkflowDesignerLabelsStayInCatalogue 阻止设计器脚本另存一份界面
// 文案：它引用的每个键都必须同时存在于两本目录与页面渲染的目录岛中。
func TestWorkflowDesignerLabelsStayInCatalogue(t *testing.T) {
	root := repoRoot(t)
	script, err := os.ReadFile(filepath.Join(root, "web", "static", "js", "yd-workflow-designer.js"))
	if err != nil {
		t.Fatalf("read designer script: %v | 读取设计器脚本失败", err)
	}
	keys := make(map[string]bool)
	for _, match := range workflowDesignerLabelPattern.FindAllStringSubmatch(string(script), -1) {
		keys[match[1]] = true
	}
	if len(keys) == 0 {
		t.Fatal("designer script references no catalogue keys | 设计器脚本没有引用任何目录键")
	}

	catalogues := []string{
		filepath.Join(root, "tooling", "scaffold", "templates", "_name_", "web", "i18n", "en.yaml.tmpl"),
		filepath.Join(root, "tooling", "scaffold", "templates", "_name_", "web", "i18n", "zh-CN.yaml.tmpl"),
	}
	for _, path := range catalogues {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read catalogue %s: %v | 读取目录 %s 失败", path, err, path)
		}
		for key := range keys {
			if !hasLinePrefix(string(data), "    "+key+":") {
				t.Errorf("%s lacks %s | %s 缺少 %s", filepath.Base(path), key, filepath.Base(path), key)
			}
		}
	}

	templatePath := filepath.Join(root, "tooling", "scaffold", "templates", "_name_", "web", "templates", "mgr", "pages", "workflow_designer.html")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read designer template: %v | 读取设计器模板失败", err)
	}
	for key := range keys {
		entry := "    \"" + key + "\": {{ tr \"workflow.designer." + key + "\" }}"
		if !hasLinePrefix(string(data), entry) {
			t.Errorf("designer label island lacks %s | 设计器目录岛缺少 %s", key, key)
		}
	}
}

// hasLinePrefix reports whether one line of text starts with prefix. | hasLinePrefix 报告 text
// 是否有一行以 prefix 开头。
func hasLinePrefix(text, prefix string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

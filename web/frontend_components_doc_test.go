package web_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shibingli/ydx/regexpx"
)

// This guard keeps the component catalogue honest: every `components.*` name the manual promises must
// exist in the embedded template tree. Without it a rename or a deleted template leaves the manual
// pointing at a component that renders nothing, which is worse than no documentation at all.
// 本守卫保证组件目录诚实：手册承诺的每个 `components.*` 名称都必须在内嵌模板树中存在。否则一次重命名
// 或删除模板会让手册指向一个渲染不出任何内容的组件，那比没有文档更糟。

var (
	// docComponentRefPattern captures one component template name, including dotted sub-templates.
	// docComponentRefPattern 捕获一个组件模板名，包含带点的子模板。
	docComponentRefPattern = regexpx.MustCompile(`components\.[a-z][a-z0-9]*(?:\.[a-z][a-z0-9-]*)*`)
	// docComponentDefinePattern captures the name of a template define. | docComponentDefinePattern 捕获模板 define 的名称。
	docComponentDefinePattern = regexpx.MustCompile(`\{\{-?\s*define\s+"([^"]+)"`)
)

// TestDocumentedComponentsExist fails when the catalogue names a template that is not defined. | 当目录提到
// 一个未定义的模板时，TestDocumentedComponentsExist 失败。
func TestDocumentedComponentsExist(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "docs", "frontend-components.md"))
	if err != nil {
		t.Fatalf("reading the component catalogue failed: %[1]v | 读取组件目录失败：%[1]v", err)
	}
	files, err := filepath.Glob(filepath.Join("templates", "components", "*.html"))
	if err != nil || len(files) == 0 {
		t.Fatalf("listing component templates failed: %[1]v | 列出组件模板失败：%[1]v", err)
	}
	defined := make(map[string]bool)
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s failed: %[2]v | 读取 %[1]s 失败：%[2]v", file, err)
		}
		for _, m := range docComponentDefinePattern.FindAllStringSubmatch(string(src), -1) {
			defined[m[1]] = true
		}
	}

	seen := make(map[string]bool)
	var refs []string
	for _, ref := range docComponentRefPattern.FindAllString(string(doc), -1) {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	if len(refs) == 0 {
		t.Fatal("the catalogue mentions no component at all, the guard would be vacuous | 目录没有提到任何组件，该守卫形同虚设")
	}

	for _, ref := range refs {
		if defined[ref] {
			continue
		}
		// A reference such as `components.data.table.*` is captured without the wildcard; it names a
		// family, so any defined sub-template satisfies it. | `components.data.table.*` 这类引用会被
		// 捕获为不带通配符的形式；它指代一族模板，因此任一已定义的子模板都满足它。
		family := false
		for name := range defined {
			if strings.HasPrefix(name, ref+".") {
				family = true
				break
			}
		}
		if family {
			continue
		}
		t.Errorf("%s is documented but not defined in web/templates/components | %s 在文档中出现，但未在 web/templates/components 中定义", ref, ref)
	}
}

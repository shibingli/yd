package scaffold_test

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/regexpx"
	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/shibingli/yd/web"
)

// Guards that keep the generated markup and the generated stylesheets in agreement. | 保证生成项目的标记与样式表彼此一致的守卫。
//
// The framework has these guards for its own templates, but the scaffold emits a separate template tree and a separate
// stylesheet; without a guard of its own, a scaffolded project can be missing every layout rule while both builds stay
// green and the browser shows an unstyled page. | 框架对自己的模板有这类守卫，但脚手架产出的是另一套模板树与另一份样式表；
// 若没有自己的守卫，生成项目可能连同布局规则一起缺失，而两边的构建都保持通过、浏览器只显示一个无样式的页面。

// Regexes that mirror how generated templates and scripts spell a class. | 与生成模板、脚本书写类名方式一致的正则。
var (
	// scaffoldClassAttrPattern captures class="..." and class='...' values. | 捕获 class="..." 与 class='...' 的取值。
	scaffoldClassAttrPattern = regexpx.MustCompile(`(?s)\bclass\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	// scaffoldTemplateActionPattern matches one Go template action, e.g. {{.Status}}. | 匹配单个 Go 模板动作，如 {{.Status}}。
	scaffoldTemplateActionPattern = regexpx.MustCompile(`(?s)\{\{.*?\}\}`)
	// scaffoldJSClassCallPattern captures classList calls and className assignments. | 捕获 classList 调用与 className 赋值。
	scaffoldJSClassCallPattern = regexpx.MustCompile(`(?s)classList\.(?:add|remove|toggle|contains)\(([^)]*)\)|className\s*=\s*['"]([^'"]*)['"]`)
	// scaffoldJSStringPattern extracts one quoted literal. | 提取单个带引号的字符串字面量。
	scaffoldJSStringPattern = regexpx.MustCompile(`'([^']*)'|"([^"]*)"`)
	// scaffoldCSSCommentPattern removes comments before selectors are parsed. | 解析选择器前先移除注释。
	scaffoldCSSCommentPattern = regexpx.MustCompile(`(?s)/\*.*?\*/`)
	// scaffoldCSSPreludePattern captures the text in front of every opening brace. | 捕获每个左花括号之前的文本。
	scaffoldCSSPreludePattern = regexpx.MustCompile(`(?s)([^{}]*)\{`)
	// scaffoldYdSelectorPattern captures a yd- class used in a selector. | 捕获选择器中的 yd- 类名。
	scaffoldYdSelectorPattern = regexpx.MustCompile(`\.(yd-[A-Za-z0-9_-]+)`)
)

// scaffoldClassOrigins maps a used class onto the generated files and lines that mention it. | 把使用到的类名映射到提到它的生成文件与行号。
type scaffoldClassOrigins map[string][]string

// add records one reference site and skips a duplicate. | 记录一处引用位置，重复位置跳过。
func (o scaffoldClassOrigins) add(token, site string) {
	for _, seen := range o[token] {
		if seen == site {
			return
		}
	}
	o[token] = append(o[token], site)
}

// scaffoldSiteOf renders the "file:line" label of a byte offset. | 渲染某个字节偏移处的「文件:行」标签。
func scaffoldSiteOf(text, file string, offset int) string {
	return file + ":" + strconv.Itoa(strings.Count(text[:offset], "\n")+1)
}

// maskTemplateActions replaces every Go template action with spaces while preserving offsets and newlines. | 把每个 Go 模板动作替换为空格，同时保持偏移与换行不变。
//
// The masking has to happen before class attributes are matched: an action such as {{ if eq .Lang "zh-CN" }} contains
// double quotes that would otherwise terminate a double-quoted class attribute in the raw template and split the
// following class token mid-name. | 遮蔽必须在匹配 class 属性之前完成：像 {{ if eq .Lang "zh-CN" }} 这样的动作含有双引号，
// 否则会在原始模板里提前终止双引号包围的 class 属性，并把后面的类名截断。
func maskTemplateActions(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	last := 0
	for _, loc := range scaffoldTemplateActionPattern.FindAllStringIndex(text, -1) {
		b.WriteString(text[last:loc[0]])
		for _, r := range text[loc[0]:loc[1]] {
			if r == '\n' {
				b.WriteByte('\n')
				continue
			}
			b.WriteByte(' ')
		}
		last = loc[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// collectScaffoldTemplateClasses records yd- classes in class attributes and scripts. | 记录 class 属性与脚本中的 yd- 类名。
func collectScaffoldTemplateClasses(text, file string, out scaffoldClassOrigins) {
	cleaned := maskTemplateActions(text)
	for _, loc := range scaffoldClassAttrPattern.FindAllStringSubmatchIndex(cleaned, -1) {
		value := ""
		switch {
		case loc[2] >= 0:
			value = cleaned[loc[2]:loc[3]]
		case loc[4] >= 0:
			value = cleaned[loc[4]:loc[5]]
		}
		site := scaffoldSiteOf(cleaned, file, loc[0])
		for _, token := range strings.Fields(value) {
			if strings.HasPrefix(token, "yd-") {
				out.add(token, site)
			}
		}
	}
}

// collectScaffoldScriptClasses records the yd- classes a generated script manipulates. | 记录生成脚本操作的 yd- 类名。
func collectScaffoldScriptClasses(text, file string, out scaffoldClassOrigins) {
	for _, loc := range scaffoldJSClassCallPattern.FindAllStringSubmatchIndex(text, -1) {
		body := ""
		switch {
		case loc[2] >= 0:
			body = text[loc[2]:loc[3]]
		case loc[4] >= 0:
			body = text[loc[4]:loc[5]]
		}
		site := scaffoldSiteOf(text, file, loc[0])
		for _, s := range scaffoldJSStringPattern.FindAllStringSubmatch(body, -1) {
			literal := s[1]
			if literal == "" {
				literal = s[2]
			}
			for _, token := range strings.Fields(literal) {
				if strings.HasPrefix(token, "yd-") {
					out.add(token, site)
				}
			}
		}
	}
}

// scaffoldCSSDefinitions returns every yd- class used in a selector prelude. | 返回选择器前导文本中出现的全部 yd- 类名。
//
// At-rule preludes are skipped; declarations can never leak into a prelude because the pattern cannot cross a closing
// brace. | 以 @ 开头的 at-rule 前导会被跳过；匹配模式无法跨越右花括号，因此声明绝不会混进前导文本。
func scaffoldCSSDefinitions(css string) map[string]bool {
	css = scaffoldCSSCommentPattern.ReplaceAllString(css, " ")
	defined := make(map[string]bool)
	for _, m := range scaffoldCSSPreludePattern.FindAllStringSubmatch(css, -1) {
		prelude := strings.TrimSpace(m[1])
		if prelude == "" || strings.HasPrefix(prelude, "@") {
			continue
		}
		for _, c := range scaffoldYdSelectorPattern.FindAllStringSubmatch(prelude, -1) {
			defined[c[1]] = true
		}
	}
	return defined
}

// scaffoldHasPrefixDefinition reports whether a defined class carries the prefix of a template-truncated token. | 判断是否有已定义类带有模板截断 token 的前缀。
func scaffoldHasPrefixDefinition(defined map[string]bool, prefix string) bool {
	for name := range defined {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// TestGeneratedStylesDefineEveryYdClassUsedByTemplates fails while a generated page asks for a component class that
// neither the project stylesheet nor the copied framework component layer defines. | 当生成页面请求的组件类既未在项目样式表、也未在复制的框架组件层中定义时，
// TestGeneratedStylesDefineEveryYdClassUsedByTemplates 失败。
func TestGeneratedStylesDefineEveryYdClassUsedByTemplates(t *testing.T) {
	o := scaffold.DefaultOptions("css-coverage")
	o.Tailwind = true
	p, _ := generateMem(t, o)

	css := p.read("web/static/css/app.css")
	if p.exists("web/static/css/components.css") {
		css += "\n" + p.read("web/static/css/components.css")
	}
	defined := scaffoldCSSDefinitions(css)
	if len(defined) == 0 {
		t.Fatal("generated stylesheets define no yd- class at all | 生成的样式表未定义任何 yd- 类")
	}

	used := make(scaffoldClassOrigins)
	scanned := 0
	err := afs.Walk(p.fs, p.at("web/templates"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		scanned++
		rel := strings.TrimPrefix(path, p.at(""))
		collectScaffoldTemplateClasses(p.read(rel), rel, used)
		return nil
	})
	if err != nil {
		t.Fatalf("walking generated templates failed: %[1]v | 遍历生成模板失败：%[1]v", err)
	}
	if scanned == 0 {
		t.Fatal("no generated template was scanned, the guard would be vacuous | 未扫描到任何生成模板，该守卫形同虚设")
	}
	if p.exists("web/static/js/yd-app.js") {
		collectScaffoldScriptClasses(p.read("web/static/js/yd-app.js"), "web/static/js/yd-app.js", used)
	}

	tokens := make([]string, 0, len(used))
	for token := range used {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)

	var missing int
	for _, token := range tokens {
		if strings.HasSuffix(token, "-") {
			if scaffoldHasPrefixDefinition(defined, token) {
				continue
			}
			missing++
			t.Errorf("%s is truncated by a template action at %s and no defined class carries that prefix | %s 在 %s 处被模板动作截断，且没有任何已定义的类带有该前缀",
				token, strings.Join(used[token], ", "), token, strings.Join(used[token], ", "))
			continue
		}
		if !defined[token] {
			missing++
			t.Errorf("%s is used at %s but no generated stylesheet defines it | %s 在 %s 处被使用，但没有任何生成的样式表定义它",
				token, strings.Join(used[token], ", "), token, strings.Join(used[token], ", "))
		}
	}
	if missing > 0 {
		t.Errorf("%d of the %d yd- classes the generated markup uses are unstyled | 生成标记使用的 %d 个 yd- 类中有 %d 个没有样式",
			missing, len(tokens), len(tokens), missing)
	}
}

// TestGeneratedComponentsCSSMatchesFramework keeps the copied component layer identical to its single source. | 保证复制的组件层与其单一真源逐字节一致。
func TestGeneratedComponentsCSSMatchesFramework(t *testing.T) {
	o := scaffold.DefaultOptions("css-copy")
	o.Tailwind = true
	p, _ := generateMem(t, o)

	want, err := afero.ReadFile(web.FS(), "static/css/components.css")
	if err != nil {
		t.Fatalf("reading the framework component layer failed: %[1]v | 读取框架组件层失败：%[1]v", err)
	}
	got := p.read("web/static/css/components.css")
	if got != string(want) {
		t.Errorf("the generated components.css drifted from the framework source | 生成的 components.css 与框架源不一致")
	}
}

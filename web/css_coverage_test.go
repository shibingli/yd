package web_test

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/regexpx"
	"github.com/shibingli/yd/web"
)

// Guard tests for the component layer: the markup and the stylesheet must agree. | 组件层的守卫测试：标记与样式表必须一致。
//
// app.css is the only file allowed to decide what an element looks like, so these two tests answer the only
// two questions that matter when the two drift apart: is every class the markup asks for actually styled,
// and does the styling stay inside the theme instead of spelling colours out? | app.css 是唯一有权决定元素外观的文件，
// 因此这两个测试回答了两者脱节时唯一重要的两个问题：标记请求的每个类是否真的已有样式，
// 以及样式是否始终留在主题之内、而不是直接把颜色写出来。

// ydAppJSPath is the script whose class manipulation must be covered as well. | ydAppJSPath 是同样必须覆盖其类操作的脚本。
const ydAppJSPath = "static/js/yd-app.js"

// minEnumeratedYdClasses is the floor below which the scanner itself is presumed broken rather than the
// markup being genuinely smaller. | minEnumeratedYdClasses 是下限，低于它即推定扫描器本身失效，而不是标记真的变小了。
const minEnumeratedYdClasses = 100

// Regexes that mirror the way templates and scripts actually spell a class. | 与模板、脚本真实书写类名方式一致的正则。
var (
	// classAttrPattern captures the value of a class="..." or class='...' attribute. | classAttrPattern 捕获 class="..." 或 class='...' 属性的取值。
	classAttrPattern = regexpx.MustCompile(`(?s)\bclass\s*=\s*(?:"([^"]*)"|'([^']*)')`)

	// templateActionPattern matches one Go template action, e.g. {{.Status}}. | templateActionPattern 匹配单个 Go 模板动作，如 {{.Status}}。
	templateActionPattern = regexpx.MustCompile(`(?s)\{\{.*?\}\}`)

	// jsClassCallPattern captures the arguments of classList.add/remove/toggle/contains and the body of a className assignment. | jsClassCallPattern 捕获 classList.add/remove/toggle/contains 的参数与 className 赋值的内容。
	//
	// The quote characters are matched as a class rather than paired with a backreference, which RE2 does not
	// support; accepting a mismatched pair costs nothing here, because a scanner only reads string bodies. | 引号以字符类匹配，
	// 而不是用 RE2 不支持的逆向引用配对；在这里容忍引号不配对没有代价，因为扫描器只读取字符串内容。
	jsClassCallPattern = regexpx.MustCompile(`(?s)classList\.(?:add|remove|toggle|contains)\(([^)]*)\)|className\s*=\s*['"]([^'"]*)['"]`)

	// jsStringPattern extracts one quoted literal. | jsStringPattern 提取单个带引号的字符串字面量。
	jsStringPattern = regexpx.MustCompile(`'([^']*)'|"([^"]*)"`)

	// selectorPattern captures the prelude of every rule, i.e. the text in front of an opening brace. | selectorPattern 捕获每条规则的前导文本，即左花括号之前的文本。
	selectorPattern = regexpx.MustCompile(`(?s)([^{}]*)\{`)

	// ydDefinitionPattern captures a yd- class name used in a selector. | ydDefinitionPattern 捕获选择器中使用的 yd- 类名。
	ydDefinitionPattern = regexpx.MustCompile(`\.(yd-[A-Za-z0-9_-]+)`)

	// colorLiteralPattern matches every colour notation the component layer must never spell out. | colorLiteralPattern 匹配组件层绝不允许直接写出的各种颜色写法。
	colorLiteralPattern = regexpx.MustCompile(`(?i)#[0-9a-f]{3,8}\b|\brgba?\(|\bhsla?\(|\bokl(?:ch|ab)\(`)
)

// ydClassOrigins maps each enumerated class token to the "file:line" sites that reference it. | ydClassOrigins 把每个枚举到的类名 token 映射到引用它的 "文件:行" 位置。
type ydClassOrigins map[string][]string

// add records one reference site, ignoring a site already recorded for the token. | add 记录一处引用位置，并忽略该 token 已记录过的位置。
func (o ydClassOrigins) add(token, site string) {
	for _, seen := range o[token] {
		if seen == site {
			return
		}
	}
	o[token] = append(o[token], site)
}

// siteOf renders the "file:line" label of a byte offset inside text. | siteOf 渲染 text 中某个字节偏移处的 "文件:行" 标签。
func siteOf(text, file string, offset int) string {
	return file + ":" + strconv.Itoa(strings.Count(text[:offset], "\n")+1)
}

// collectTemplateClasses records every yd- class token spelled in class="..." attributes. | collectTemplateClasses 记录 class="..." 属性中书写的每个 yd- 类名 token。
//
// Only class attribute values are scanned: the same file is full of data-yd-* attributes and #yd-* ids, and
// counting those as classes would demand styles for things that never carry one. | 只扫描 class 属性值：同一个文件里
// 满是 data-yd-* 属性与 #yd-* 标识，把它们算作类名会为根本不会携带类的东西索取样式。
func collectTemplateClasses(text, file string, out ydClassOrigins) {
	for _, loc := range classAttrPattern.FindAllStringSubmatchIndex(text, -1) {
		value := ""
		switch {
		case loc[2] >= 0:
			value = text[loc[2]:loc[3]]
		case loc[4] >= 0:
			value = text[loc[4]:loc[5]]
		}
		site := siteOf(text, file, loc[0])
		// Template actions are replaced by a space before splitting, so that an {{if}} block cannot glue two
		// neighbouring class names into one phantom token. | 拆分前先把模板动作替换为空格，避免 {{if}} 块把相邻的两个类名粘成一个幻影 token。
		for _, token := range strings.Fields(templateActionPattern.ReplaceAllString(value, " ")) {
			if strings.HasPrefix(token, "yd-") {
				out.add(token, site)
			}
		}
	}
}

// collectScriptClasses records every yd- class token a script manipulates. | collectScriptClasses 记录脚本操作的每个 yd- 类名 token。
//
// The script is a first-class author of component classes: it sets data-collapsed on the rail and toggles
// flash visibility, so a class it manipulates but the stylesheet ignores is just as broken as a template one. | 脚本是组件类的一等作者：
// 它给导航栏设置 data-collapsed 并切换闪现消息的可见性，因此它操作而样式表忽略的类，与模板中的同样有问题。
func collectScriptClasses(text, file string, out ydClassOrigins) {
	for _, loc := range jsClassCallPattern.FindAllStringSubmatchIndex(text, -1) {
		body := ""
		switch {
		case loc[2] >= 0:
			body = text[loc[2]:loc[3]]
		case loc[4] >= 0:
			body = text[loc[4]:loc[5]]
		}
		site := siteOf(text, file, loc[0])
		for _, s := range jsStringPattern.FindAllStringSubmatch(body, -1) {
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

// hasPrefixDefinition reports whether any defined class starts with a truncated token. | hasPrefixDefinition 报告是否有已定义的类以某个截断 token 为前缀。
//
// A class attribute such as class="yd-badge yd-badge--{{.Status}}" never spells out a complete token, so the
// literal name cannot appear in the stylesheet; requiring the family prefix keeps the check honest without
// pretending the truncated token is a class of its own. | 形如 class="yd-badge yd-badge--{{.Status}}" 的属性
// 永远不会写出完整 token，因此其字面名不可能出现在样式表中；要求家族前缀命中，既保持检查的有效性，
// 又不把该截断 token 当作一个独立的类。
func hasPrefixDefinition(defined map[string]bool, prefix string) bool {
	for name := range defined {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// TestEveryYdClassUsedInTemplatesIsDefined fails while any component class the markup asks for is unstyled. | TestEveryYdClassUsedInTemplatesIsDefined 在标记请求的任何组件类尚未有样式时失败。
//
// A class that exists only in the markup is the quietest kind of regression: the element still renders, the
// build still passes, and the page is simply wrong in the one place nobody is looking. | 只存在于标记中的类是最安静的一类回归：
// 元素照常渲染、构建照常通过，页面只是在那个人人都不看的地方显示不对。
func TestEveryYdClassUsedInTemplatesIsDefined(t *testing.T) {
	fsys := web.FS()
	css := appSource(t)

	// Definitions are read from selector preludes only, so that a class name quoted inside a declaration
	// value can never be mistaken for a styled class. | 只从选择器前导文本中读取定义，
	// 这样声明值里被引用的类名绝不会被误当成已有样式的类。
	defined := make(map[string]bool)
	for _, prelude := range selectorPattern.FindAllStringSubmatch(cssBlock(t, css, "@layer components"), -1) {
		for _, m := range ydDefinitionPattern.FindAllStringSubmatch(prelude[1], -1) {
			defined[m[1]] = true
		}
	}
	if len(defined) == 0 {
		t.Fatal("the component layer defines no yd- class at all | 组件层未定义任何 yd- 类")
	}

	used := make(ydClassOrigins)
	var scanned int
	err := afs.Walk(fsys, "templates", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		scanned++
		collectTemplateClasses(string(readEmbedded(t, fsys, p)), p, used)
		return nil
	})
	if err != nil {
		t.Fatalf("walking templates failed: %v | 遍历模板失败: %v", err, err)
	}
	if scanned == 0 {
		t.Fatal("no template was scanned, the guard would be vacuous | 未扫描到任何模板，该守卫形同虚设")
	}
	collectScriptClasses(string(readEmbedded(t, fsys, ydAppJSPath)), ydAppJSPath, used)

	tokens := make([]string, 0, len(used))
	for token := range used {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	if len(tokens) < minEnumeratedYdClasses {
		t.Fatalf("enumerated only %d yd- classes over %d templates, below the %d floor, so the scanner is probably broken | 在 %d 个模板中仅枚举到 %d 个 yd- 类，低于 %d 的下限，扫描器很可能已失效",
			len(tokens), scanned, minEnumeratedYdClasses, scanned, len(tokens), minEnumeratedYdClasses)
	}
	t.Logf("enumerated %d yd- class tokens over %d templates | 在 %d 个模板中共枚举 %d 个 yd- 类名 token: %s",
		len(tokens), scanned, scanned, len(tokens), strings.Join(tokens, " "))

	var missing int
	for _, token := range tokens {
		if strings.HasSuffix(token, "-") {
			if hasPrefixDefinition(defined, token) {
				continue
			}
			missing++
			t.Errorf("%s is truncated by a template action at %s and no defined class carries that prefix | %s 在 %s 处被模板动作截断，且没有任何已定义的类带有该前缀",
				token, strings.Join(used[token], ", "), token, strings.Join(used[token], ", "))
			continue
		}
		if !defined[token] {
			missing++
			t.Errorf("%s is used at %s but has no rule in @layer components of %s + %s | %s 在 %s 处被使用，但 %s + %s 的 @layer components 中没有它的规则",
				token, strings.Join(used[token], ", "), appCSSPath, componentsCSSPath, token, strings.Join(used[token], ", "), appCSSPath, componentsCSSPath)
		}
	}
	if missing > 0 {
		t.Errorf("%d of the %d yd- classes the markup uses are unstyled | 标记使用的 %d 个 yd- 类中有 %d 个没有样式",
			missing, len(tokens), len(tokens), missing)
	}
}

// blockSpan returns the half-open byte range of the first block whose prelude is exactly followed by "{",
// braces included. | blockSpan 返回首个「前导字符串后紧跟 "{"」的块的半开字节区间，含花括号本身。
//
// The brace is anchored to the prelude for the same reason cssBlock does it: the dark selector also appears
// inside the @custom-variant line, and that occurrence has no block of its own. | 这里把花括号锚定在前导字符串之后，
// 原因与 cssBlock 相同：暗色选择器同样出现在 @custom-variant 行中，而那一次出现并没有自己的块。
func blockSpan(t *testing.T, css, prelude string) (int, int) {
	t.Helper()
	re := regexpx.MustCompile(regexpx.QuoteMeta(prelude) + `\s*\{`)
	loc := re.FindStringIndex(css)
	if loc == nil {
		t.Fatalf("selector %q has no block in the stylesheet | 样式表中选择器 %q 没有块", prelude, prelude)
	}
	open := loc[1] - 1
	depth := 0
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return open, i + 1
			}
		}
	}
	t.Fatalf("selector %q has an unbalanced block | 选择器 %q 的块括号不平衡", prelude, prelude)
	return 0, 0
}

// inAnySpan reports whether an offset falls inside one of the given spans. | inAnySpan 报告某个偏移是否落在给定的区间之一内。
func inAnySpan(spans [][2]int, offset int) bool {
	for _, span := range spans {
		if offset >= span[0] && offset < span[1] {
			return true
		}
	}
	return false
}

// TestNoHardcodedColorsInAppCSS keeps every colour decision inside the theme. | TestNoHardcodedColorsInAppCSS 把所有颜色决策都限制在主题之内。
//
// The three token blocks are allowed to spell colours out: a palette is exactly the place where a literal
// belongs. The component layer is not: a literal there is invisible to the dark theme and to any future
// rebrand, and it is how a design system rots one rule at a time. | 三个令牌块允许直接写出颜色：调色板正是字面值该待的地方。
// 组件层则不允许：那里的字面值对暗色主题与未来的任何品牌换色都不可见，设计系统正是被一条条规则侵蚀掉的。
//
// Only the blocks themselves are exempt, not the whole file, so a literal that leaks into the component layer
// or into any other rule is reported with its line number. | 豁免的只有这些块本身而非整个文件，因此任何漏进组件层
// 或其它规则的色值都会被连同行号一起报告。
func TestNoHardcodedColorsInAppCSS(t *testing.T) {
	css := string(readEmbedded(t, web.FS(), componentsCSSPath))

	// Every block that defines variables may carry a literal colour; these three are all of them. | 所有定义变量的块都可以携带字面色值；这三个块就是全部。
	var exempt [][2]int
	for _, prelude := range []string{"@theme", ":root", `[data-theme="dark"]`} {
		start, end := blockSpan(t, css, prelude)
		exempt = append(exempt, [2]int{start, end})
	}

	var inside, outside int
	for _, loc := range colorLiteralPattern.FindAllStringIndex(css, -1) {
		if inAnySpan(exempt, loc[0]) {
			inside++
			continue
		}
		outside++
		t.Errorf("%s:%d hard-codes the colour %s outside the token blocks; use a theme variable | %s:%d 在令牌块之外硬编码了颜色 %s，请改用主题变量",
			componentsCSSPath, strings.Count(css[:loc[0]], "\n")+1, css[loc[0]:loc[1]], componentsCSSPath, strings.Count(css[:loc[0]], "\n")+1, css[loc[0]:loc[1]])
	}
	if inside == 0 {
		t.Error(`no colour literal lives in @theme, :root or [data-theme="dark"], so this guard proves nothing | @theme、:root 与 [data-theme="dark"] 中没有任何颜色字面量，该守卫什么也证明不了`)
	}
	t.Logf("colour literals in %s: %d inside the token blocks, %d outside | %s 中的颜色字面量：令牌块内 %d 处，令牌块外 %d 处",
		componentsCSSPath, inside, outside, componentsCSSPath, inside, outside)
}

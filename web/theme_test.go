package web_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shibingli/yd/core/afs"
	"github.com/shibingli/yd/core/regexpx"
	"github.com/shibingli/yd/web"
)

// Stylesheet paths inside the embedded tree. | 内嵌目录树中的样式文件路径。
const (
	// appCSSPath is the Tailwind v4 entry stylesheet. | appCSSPath 是 Tailwind v4 入口样式表。
	appCSSPath = "static/css/app.css"
	// componentsCSSPath is the admin component layer and design tokens imported by app.css. | componentsCSSPath 是 app.css 引入的管理端组件层与设计令牌。
	componentsCSSPath = "static/css/components.css"
	// builtCSSPath is the committed build artifact, present so the project builds without the Tailwind binary. | builtCSSPath 是签入仓库的构建产物，用于在没有 Tailwind 二进制时也能构建。
	builtCSSPath = "static/css/yd.css"
)

// appSource returns the entry stylesheet together with the component layer it imports; together they are the
// stylesheet the theme and component guards talk about. | appSource 返回入口样式表与它引入的组件层；两者合在一起才是主题与组件守卫所谈论的样式表。
func appSource(t *testing.T) string {
	t.Helper()
	return string(readEmbedded(t, web.FS(), appCSSPath)) + "\n" + string(readEmbedded(t, web.FS(), componentsCSSPath))
}

// minBuiltCSSBytes is the lower bound for a plausible compiled stylesheet; a smaller file means the build
// silently produced an empty or truncated result. | minBuiltCSSBytes 是编译后样式表的合理体积下限；更小即意味着构建静默产出了空文件或截断结果。
const minBuiltCSSBytes = 3 * 1024

// brandSteps are the eleven steps of the brand scale, in ascending order. | brandSteps 是品牌色阶的 11 个阶梯，按升序排列。
var brandSteps = []string{"50", "100", "200", "300", "400", "500", "600", "700", "800", "900", "950"}

// semanticColorTokens must be defined in @theme and redefined for dark mode. | semanticColorTokens 必须在 @theme 中定义，并在暗色模式下重新定义。
var semanticColorTokens = []string{
	"--color-surface",
	"--color-surface-muted",
	"--color-content",
	"--color-content-muted",
	"--color-border-subtle",
}

// cssBlock returns the body of the first block whose prelude is exactly followed by "{". | cssBlock 返回首个「前导字符串后紧跟 "{"」的块体。
//
// Matching the brace instead of the next brace after a bare substring matters: the dark selector also
// appears inside the @custom-variant line, and taking that occurrence would return the wrong block. | 匹配花括号
// 而不是「裸子串之后的第一个花括号」很关键：暗色选择器同样出现在 @custom-variant 行中，
// 取那一次出现会返回错误的块。
func cssBlock(t *testing.T, css, prelude string) string {
	t.Helper()
	re := regexpx.MustCompile(regexpx.QuoteMeta(prelude) + `\s*\{`)
	loc := re.FindStringIndex(css)
	if loc == nil {
		t.Fatalf("selector %q has no block in the stylesheet | 样式表中选择器 %q 没有块", prelude, prelude)
	}
	rest := css[loc[1]-1:]
	depth := 0
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[1:i]
			}
		}
	}
	t.Fatalf("selector %q has an unbalanced block | 选择器 %q 的块括号不平衡", prelude, prelude)
	return ""
}

// cssValue returns the value of a custom property inside a block body. | cssValue 返回块体内部某个自定义属性的取值。
//
// The trailing ":" anchor keeps "--color-surface" from matching the longer "--color-surface-muted". | 末尾的 ":" 锚点
// 可避免 "--color-surface" 误匹配更长的 "--color-surface-muted"。
func cssValue(body, name string) (string, bool) {
	re := regexpx.MustCompile(regexpx.QuoteMeta(name) + `\s*:\s*([^;]+);`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// hexBlueDominant reports whether value is a #rrggbb literal in which blue is the strongest channel. | hexBlueDominant
// 报告 value 是否为蓝色通道最强的 #rrggbb 字面量。
//
// The second result is false when the value is not a hex literal, so the caller can skip the hue check
// instead of failing on a differently notated but equally valid colour space. | 当取值不是十六进制字面量时第二个返回值为
// false，调用方可据此跳过色相检查，而不是在书写方式不同但同样合法的色彩空间上判失败。
func hexBlueDominant(value string) (dominant, isHex bool) {
	if len(value) != 7 || value[0] != '#' {
		return false, false
	}
	r, errR := strconv.ParseUint(value[1:3], 16, 8)
	g, errG := strconv.ParseUint(value[3:5], 16, 8)
	b, errB := strconv.ParseUint(value[5:7], 16, 8)
	if errR != nil || errG != nil || errB != nil {
		return false, false
	}
	return b >= r && b >= g, true
}

// sourceGlobPattern extracts every @source glob from a stylesheet. | sourceGlobPattern 提取样式表中的全部 @source 通配。
var sourceGlobPattern = regexpx.MustCompile(`@source\s+"([^"]+)"`)

// TestThemeDefinesBlueBrandScaleAndSemanticTokens pins the design-token contract of app.css. | TestThemeDefinesBlueBrandScaleAndSemanticTokens 锁定 app.css 的设计令牌契约。
//
// Without the eleven brand steps the semantic classes such as bg-brand-600 do not exist, and without the
// semantic tokens every component would have to hard-code a colour, which is exactly what the theme exists
// to prevent. | 缺少 11 阶品牌色阶则 bg-brand-600 之类的类不存在；缺少语义令牌则每个组件都只能硬编码颜色，
// 而这正是主题要消除的东西。
func TestThemeDefinesBlueBrandScaleAndSemanticTokens(t *testing.T) {
	css := string(readEmbedded(t, web.FS(), appCSSPath))

	if first := strings.TrimSpace(strings.SplitN(css, "\n", 2)[0]); first != `@import "tailwindcss";` {
		t.Errorf("app.css must start with `@import \"tailwindcss\";`, got %q | app.css 首行必须是 `@import \"tailwindcss\";`，实际为 %q", first, first)
	}
	for _, want := range []string{`@source "../../templates/**/*.html";`, `@source "../../../http/**/*.go";`} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css is missing the source directive %s | app.css 缺少源指令 %s", want, want)
		}
	}

	theme := cssBlock(t, appSource(t), "@theme")

	values := make(map[string]string, len(brandSteps))
	for _, step := range brandSteps {
		name := "--color-brand-" + step
		value, ok := cssValue(theme, name)
		if !ok {
			t.Errorf("@theme is missing %s | @theme 缺少 %s", name, name)
			continue
		}
		values[step] = value
		if dominant, isHex := hexBlueDominant(value); isHex && !dominant {
			t.Errorf("%s = %s is not a blue hue; blue must dominate | %s = %s 不是蓝色相；蓝色通道必须占优", name, value, name, value)
		}
	}
	if len(brandSteps) != 11 {
		t.Fatalf("the brand scale must have 11 steps, the table has %d | 品牌色阶必须为 11 阶，当前表中有 %d 阶", len(brandSteps), len(brandSteps))
	}

	// Distinct values prove the scale was written out rather than aliased to a single colour. | 取值互不相同，证明色阶是逐阶书写的，而不是全部别名到同一个颜色。
	seen := make(map[string]string, len(values))
	for _, step := range brandSteps {
		value, ok := values[step]
		if !ok {
			continue
		}
		if prev, dup := seen[value]; dup {
			t.Errorf("--color-brand-%s and --color-brand-%s share the value %s | --color-brand-%s 与 --color-brand-%s 取值相同：%s", prev, step, value, prev, step, value)
		}
		seen[value] = step
	}

	for _, name := range append(append([]string{}, semanticColorTokens...),
		"--font-sans", "--radius-card", "--shadow-card", "--spacing-sidebar", "--spacing-sidebar-collapsed") {
		if _, ok := cssValue(theme, name); !ok {
			t.Errorf("@theme is missing the semantic token %s | @theme 缺少语义令牌 %s", name, name)
		}
	}
}

// TestThemeSourcePathsResolveToExistingTrees keeps the @source directives honest. | TestThemeSourcePathsResolveToExistingTrees 确保 @source 指令名副其实。
//
// Tailwind v4 resolves @source relative to the stylesheet, so a directive pointing at a directory that does
// not exist matches nothing, silently compiles an almost empty stylesheet and fails only at runtime as
// missing styling. | Tailwind v4 相对样式文件解析 @source，若指令指向的目录不存在则匹配不到任何文件，
// 会静默编译出近乎空的样式表，直到运行时才以样式缺失的形式暴露。
func TestThemeSourcePathsResolveToExistingTrees(t *testing.T) {
	css := string(readEmbedded(t, web.FS(), appCSSPath))
	globs := sourceGlobPattern.FindAllStringSubmatch(css, -1)
	if len(globs) == 0 {
		t.Fatal("app.css declares no @source glob | app.css 未声明任何 @source 通配")
	}

	stylesheetDir := filepath.Dir(filepath.FromSlash(appCSSPath))
	for _, g := range globs {
		glob := g[1]
		base := glob
		if i := strings.IndexAny(base, "*?["); i >= 0 {
			base = base[:i]
			if i := strings.LastIndex(base, "/"); i >= 0 {
				base = base[:i]
			}
		}
		dir := filepath.Clean(filepath.Join(stylesheetDir, filepath.FromSlash(base)))

		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("@source %q resolves to %q, which does not exist: %v | @source %q 解析为 %q，该路径不存在: %v", glob, dir, err, glob, dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("@source %q resolves to %q, which is not a directory | @source %q 解析为 %q，但它不是目录", glob, dir, glob, dir)
			continue
		}

		// The directory must actually contain files the glob selects; an empty tree is as useless as a missing one. | 该目录必须真的含有通配选中的文件；空目录与缺失目录同样无用。
		suffix := filepath.Ext(glob)
		matched := 0
		err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, suffix) {
				matched++
			}
			return nil
		})
		if err != nil {
			t.Errorf("walking %q failed: %v | 遍历 %q 失败: %v", dir, err, dir, err)
			continue
		}
		if matched == 0 {
			t.Errorf("@source %q matches no %s file under %q | @source %q 在 %q 下未匹配到任何 %s 文件", glob, suffix, dir, glob, dir, suffix)
		}
	}
}

// TestDarkThemeOverridesSemanticTokens proves the dark theme really replaces every semantic token. | TestDarkThemeOverridesSemanticTokens 证明暗色主题确实替换了每一个语义令牌。
//
// A token defined only in :root keeps its light value in dark mode, which usually shows up as near-black
// text on a near-black surface — unreadable, and easy to miss because the rest of the page looks right. | 只在
// :root 中定义的令牌在暗色模式下仍保留亮色取值，通常表现为近黑背景上的近黑文字——既不可读，
// 又因为页面其余部分看起来正常而极易被忽略。
func TestDarkThemeOverridesSemanticTokens(t *testing.T) {
	css := appSource(t)

	light := cssBlock(t, css, ":root")
	dark := cssBlock(t, css, `[data-theme="dark"]`)

	lightAt := strings.Index(css, ":root {")
	darkAt := strings.Index(css, `[data-theme="dark"] {`)
	if lightAt < 0 || darkAt < 0 {
		t.Fatalf("both :root and [data-theme=\"dark\"] must be spelled with a space before the brace | :root 与 [data-theme=\"dark\"] 的花括号前都必须有一个空格")
	}
	if darkAt < lightAt {
		t.Errorf("the dark block must come after :root to win at equal specificity (dark at %d, light at %d) | 暗色块必须位于 :root 之后才能在同等特异性下胜出（暗色 %d，亮色 %d）", darkAt, lightAt, darkAt, lightAt)
	}
	if !strings.Contains(dark, "color-scheme: dark") {
		t.Error("the dark block must declare `color-scheme: dark` so form controls and scrollbars follow | 暗色块必须声明 `color-scheme: dark`，表单控件与滚动条才会跟随")
	}

	for _, name := range semanticColorTokens {
		lightValue, okLight := cssValue(light, name)
		darkValue, okDark := cssValue(dark, name)
		switch {
		case !okLight:
			t.Errorf("%s is not defined in :root | :root 中未定义 %s", name, name)
			continue
		case !okDark:
			t.Errorf("%s is not overridden in [data-theme=\"dark\"] | [data-theme=\"dark\"] 中未覆盖 %s", name, name)
			continue
		}
		if lightValue == darkValue {
			t.Errorf("%s keeps the light value %s in dark mode | %s 在暗色模式下仍为亮色取值 %s", name, lightValue, name, lightValue)
		}
	}

	if len(semanticColorTokens) < 5 {
		t.Fatalf("expected at least 5 semantic colour tokens, the table has %d | 语义颜色令牌至少应有 5 个，当前表中有 %d 个", len(semanticColorTokens), len(semanticColorTokens))
	}
}

// tailwindBanner matches the licence banner every --minify build emits and preserves. | tailwindBanner 匹配每次 --minify 构建都会产出并保留的许可横幅。
var tailwindBanner = regexpx.MustCompile(`^/\*!.*?\*/\s*`)

// TestBuiltCssIsMinifiedAndNonEmpty guarantees the committed stylesheet is a real, usable build artifact. | TestBuiltCssIsMinifiedAndNonEmpty 保证签入的样式表是真实可用的构建产物。
//
// yd.css is committed so that the project builds and runs without the 112 MB Tailwind binary; if it were
// stale, empty or merely a copy of the source, offline builds would produce an unstyled application. | yd.css
// 之所以签入，是为了在没有 112 MB 的 Tailwind 二进制时也能构建与运行；若它过期、为空或只是源文件的副本，
// 离线构建就会产出无样式的应用。
func TestBuiltCssIsMinifiedAndNonEmpty(t *testing.T) {
	raw := readEmbedded(t, web.FS(), builtCSSPath)
	css := string(raw)

	if len(raw) <= minBuiltCSSBytes {
		t.Errorf("%s is %d bytes, below the %d byte floor | %s 为 %d 字节，低于 %d 字节下限", builtCSSPath, len(raw), minBuiltCSSBytes, builtCSSPath, len(raw), minBuiltCSSBytes)
	}
	if strings.Contains(css, "@source ") {
		t.Errorf("%s still carries @source directives, so it is the source rather than a build artifact | %s 中仍含 @source 指令，说明它是源文件而不是构建产物", builtCSSPath, builtCSSPath)
	}

	// Strip the banner Tailwind is required to preserve, then no comment may remain in a minified file. | 去掉 Tailwind 依约必须保留的横幅后，压缩文件中不得残留任何注释。
	body := tailwindBanner.ReplaceAllString(css, "")
	if i := strings.Index(body, "/*"); i >= 0 {
		t.Errorf("%s carries a comment block at byte %d: %.60s | %s 在偏移 %d 处残留注释块: %.60s", builtCSSPath, i, body[i:], builtCSSPath, i, body[i:])
	}
	if n := strings.Count(css, "\n"); n > 1 {
		t.Errorf("%s has %d newlines, so it is not minified | %s 含 %d 个换行，说明未压缩", builtCSSPath, n, builtCSSPath, n)
	}

	for _, want := range []string{"--color-brand-500", "--color-surface", ".yd-card", ".yd-table", ".htmx-indicator"} {
		if !strings.Contains(css, want) {
			t.Errorf("%s does not contain %s | %s 中不含 %s", builtCSSPath, want, builtCSSPath, want)
		}
	}
	if !regexpx.MustCompile(`\[data-theme=["']?dark["']?\]`).MatchString(css) {
		t.Errorf("%s does not contain the dark theme block | %s 中不含暗色主题块", builtCSSPath, builtCSSPath)
	}
}

// TestModalDialogsStayCentered pins the automatic margins that Tailwind Preflight erases. | TestModalDialogsStayCentered
// 锁定会被 Tailwind Preflight 抹掉的自动外边距。
//
// Preflight sets `margin: 0` on every element, which also overrides the user agent's
// `dialog:modal { margin: auto }`; without the explicit rule the confirmation dialog is pinned to the
// top-left corner instead of the middle of the screen. The drawer needs an equally specific rule declared
// later, otherwise it would be centred too. | Preflight 把所有元素的 margin 归零，连带覆盖了浏览器对
// `dialog:modal { margin: auto }` 的设定；缺少显式规则时确认框会钉在左上角而不是屏幕中央。抽屉需要
// 一条特异性相同且声明更晚的规则，否则它也会被居中。
func TestModalDialogsStayCentered(t *testing.T) {
	css := appSource(t)
	confirm := cssBlock(t, css, ".yd-dialog:modal")
	if margin, ok := cssValue(confirm, "margin"); !ok || margin != "auto" {
		t.Errorf(".yd-dialog:modal must declare `margin: auto`, got %q | .yd-dialog:modal 必须声明 `margin: auto`，实际为 %q", margin, margin)
	}

	drawer := cssBlock(t, css, ".yd-drawer:modal")
	if margin, ok := cssValue(drawer, "margin"); !ok || margin != "0 0 0 auto" {
		t.Errorf(".yd-drawer:modal must keep the right-aligned margin, got %q | .yd-drawer:modal 必须保持右对齐外边距，实际为 %q", margin, margin)
	}

	// Equal specificity means source order decides; the drawer must come second. Match the selector together
	// with its opening brace so the comments mentioning it do not skew the offsets. | 特异性相同时由源码顺序
	// 决定，抽屉必须声明在后。匹配选择器及其左花括号，避免提及它的注释干扰偏移量。
	if strings.Index(css, ".yd-drawer:modal {") < strings.Index(css, ".yd-dialog:modal {") {
		t.Error("the drawer rule must follow the dialog rule to win at equal specificity | 抽屉规则必须位于对话框规则之后，才能在同等特异性下胜出")
	}

	built := string(readEmbedded(t, web.FS(), builtCSSPath))
	if !strings.Contains(built, ".yd-dialog:modal{margin:auto}") {
		t.Error("the committed yd.css lacks the modal centring rule; rerun the Tailwind build | 签入的 yd.css 缺少模态居中规则，请重新执行 Tailwind 构建")
	}
}

// hexLiteral matches the six and eight digit hex colour literals banned from templates. | hexLiteral 匹配模板中禁止出现的 6 位与 8 位十六进制色值。
var hexLiteral = regexpx.MustCompile(`#[0-9a-fA-F]{8}\b|#[0-9a-fA-F]{6}\b`)

// TestNoHardcodedHexInTemplates keeps colour decisions inside the theme. | TestNoHardcodedHexInTemplates 把颜色决策限制在主题内部。
//
// A literal colour in a template is invisible to the dark theme and to any future rebrand, and it is how a
// design system rots one <span> at a time. | 模板中的字面色值对暗色主题与未来的任何品牌换色都不可见，
// 设计系统正是被一个个 <span> 侵蚀掉的。
func TestNoHardcodedHexInTemplates(t *testing.T) {
	fsys := web.FS()
	var scanned int

	err := afs.Walk(fsys, "templates", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".html") {
			return nil
		}
		scanned++
		lines := strings.Split(strings.ReplaceAll(string(readEmbedded(t, fsys, p)), "\r\n", "\n"), "\n")
		for i, line := range lines {
			for _, hit := range hexLiteral.FindAllString(line, -1) {
				t.Errorf("%s:%d hard-codes the colour %s; use a semantic class from app.css | %s:%d 硬编码了颜色 %s，请改用 app.css 的语义化类",
					p, i+1, hit, p, i+1, hit)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking templates failed: %v | 遍历模板失败: %v", err, err)
	}
	if scanned == 0 {
		t.Fatal("no template was scanned, the guard would be vacuous | 未扫描到任何模板，该守卫形同虚设")
	}

	// The same rule binds the component layer: it must be driven by variables only. | 同一条规则约束组件层：它必须完全由变量驱动。
	componentLayer := cssBlock(t, appSource(t), "@layer components")
	for i, line := range strings.Split(componentLayer, "\n") {
		for _, hit := range hexLiteral.FindAllString(line, -1) {
			t.Errorf("%s @layer components line %d hard-codes the colour %s; use a theme variable | %s @layer components 第 %d 行硬编码了颜色 %s，请改用主题变量",
				componentsCSSPath, i+1, hit, componentsCSSPath, i+1, hit)
		}
	}
}

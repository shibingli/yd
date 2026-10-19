// switch_test.go — the language and theme switch contracts of the admin shell.
// switch_test.go — 管理后台语言与主题切换的契约测试。
//
// Both switches share one promise: the choice takes effect at once, it is remembered across
// reloads, and it never costs a full page load. The tests below read the shipped sources and pin
// that promise at the exact lines that implement it, because a later refactor that dropped the
// htmx refetch or moved the theme script below the stylesheet would still render a working page —
// just one that flashes, or one whose server-rendered copy stays in the old language.
// 两个切换共有一个承诺：选择立即生效、跨刷新被记住，且绝不以整页加载为代价。
// 下方测试直接读取交付的源码，把该承诺钉在实现它的具体行上：
// 因为若后续重构丢掉 htmx 重取、或把主题脚本挪到样式表之后，页面看起来仍然能用——
// 只是会闪烁一帧，或服务端渲染的文案停在旧语言。
package web_test

import (
	"strings"
	"testing"

	"github.com/shibingli/yd/web"
)

// Front-end paths read by the switch tests. | 切换测试读取的前端路径。
const (
	i18nJSPath      = "static/js/yd-i18n.js"
	themeBootJSPath = "static/js/yd-theme-boot.js"
	appJSPath       = "static/js/yd-app.js"
	baseLayoutPath  = "templates/layouts/base.html"
)

// jsBody returns the body of the first function whose signature contains signature.
// jsBody 返回首个签名包含 signature 的函数体。
//
// Brace counting is enough here: the functions under test contain no brace inside a string or a
// comment, and a missing function is reported instead of silently returning an empty body.
// 这里数花括号就够了：被测函数的字符串与注释里都不含花括号；
// 函数缺失会被明确报错，而不是静默返回空函数体。
func jsBody(t *testing.T, src, signature string) string {
	t.Helper()
	start := strings.Index(src, signature)
	if start < 0 {
		t.Fatalf("the source has no %q | 源码中没有 %q", signature, signature)
	}
	open := strings.IndexByte(src[start:], '{')
	if open < 0 {
		t.Fatalf("%q has no body | %q 没有函数体", signature, signature)
	}
	open += start

	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open+1 : i]
			}
		}
	}
	t.Fatalf("%q has an unbalanced body | %q 的函数体括号不平衡", signature, signature)
	return ""
}

// TestLanguageSwitchReloadsWorkspaceViaHtmx pins the hot language switch: the page is rewritten in
// place from the catalog already in memory, and the workspace is then refetched through htmx so
// every server-rendered string follows without a reload.
// TestLanguageSwitchReloadsWorkspaceViaHtmx 锁定语言热切换：
// 先用手上已有的目录就地重写页面，随后经 htmx 重取工作区，
// 使每一处服务端渲染的文案都跟随切换，而无需刷新页面。
func TestLanguageSwitchReloadsWorkspaceViaHtmx(t *testing.T) {
	js := string(readEmbedded(t, web.FS(), i18nJSPath))

	// 1. The switch is exported, so the top bar, the settings select and the demo page all reach
	//    the same implementation. | 切换函数被导出，顶栏、设置下拉与示例页因此共用同一份实现。
	if !strings.Contains(js, "setLang: setLang,") {
		t.Error("yd-i18n.js does not export setLang on the public API | yd-i18n.js 的公开 API 上没有导出 setLang")
	}

	// 2. Persistence covers all three carriers: localStorage, the yd_lang cookie the server reads,
	//    and <html lang> for assistive technology. | 持久化覆盖三个载体：localStorage、
	//    服务端读取的 yd_lang cookie，以及给辅助技术用的 <html lang>。
	for _, want := range []string{
		"var STORAGE_KEY = 'yd_lang';",
		"var COOKIE_KEY = 'yd_lang';",
		"function persist(lang) {",
		"writeStorage(STORAGE_KEY, lang);",
		"writeCookie(COOKIE_KEY, lang);",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("yd-i18n.js is missing %s, so the choice would not survive a reload | yd-i18n.js 缺少 %s，选择将无法跨刷新保留", want, want)
		}
	}
	if !strings.Contains(jsBody(t, js, "function applyDocumentLang("), "d.documentElement.lang = lang;") {
		t.Error("yd-i18n.js: applyDocumentLang does not write <html lang> | yd-i18n.js：applyDocumentLang 没有写入 <html lang>")
	}

	// 3. setLang rewrites first and refetches second: the reverse order would leave the page in the
	//    old language for the duration of the request, which is the flash the in-place pass removes.
	//    setLang 先就地重写、再重取：反过来会让页面在整个请求期间停在旧语言，
	//    而那正是就地重写要消除的闪烁。
	setLang := jsBody(t, js, "function setLang(")
	bindAt := strings.Index(setLang, "bind(doc(), true);")
	refetchAt := strings.Index(setLang, "refetchWorkspace();")
	switch {
	case bindAt < 0:
		t.Error("setLang does not rewrite the page in place, so the switch would flash the old language | setLang 没有就地重写页面，切换会先闪出旧语言")
	case refetchAt < 0:
		t.Error("setLang does not refetch the workspace, so server-rendered copy would stay in the old language | setLang 没有重取工作区，服务端渲染的文案会停在旧语言")
	case bindAt > refetchAt:
		t.Error("setLang refetches before rewriting in place: the old language stays on screen until the answer arrives | setLang 先重取后就地重写：在应答到达前屏幕上仍是旧语言")
	}
	for _, want := range []string{"persist(next);", "applyDocumentLang(next);"} {
		if !strings.Contains(setLang, want) {
			t.Errorf("setLang is missing %s | setLang 缺少 %s", want, want)
		}
	}

	// 4. The refetch is a GET of the current URL into #yd-workspace with innerHTML, and it must not
	//    push a history entry: the URL never changes, so a push would add a duplicate back entry.
	//    重取是对当前 URL 的 GET，以 innerHTML 换入 #yd-workspace，且不得 push 历史：
	//    URL 从未变化，push 只会给后退栈加一条重复记录。
	if !strings.Contains(js, "var WORKSPACE_SELECTOR = '#yd-workspace';") {
		t.Error("yd-i18n.js does not define the #yd-workspace selector it refetches | yd-i18n.js 没有定义它所重取的 #yd-workspace 选择器")
	}
	refetch := jsBody(t, js, "function refetchWorkspace(")
	for _, want := range []string{
		"api.ajax('GET', url, {",
		"target: WORKSPACE_SELECTOR,",
		"swap: 'innerHTML',",
		"push: false",
	} {
		if !strings.Contains(refetch, want) {
			t.Errorf("refetchWorkspace is missing %s | refetchWorkspace 缺少 %s", want, want)
		}
	}

	// 5. A page without a workspace (the sign-in card) degrades to the in-place rewrite instead of
	//    failing: the guard is part of the contract, not an accident.
	//    没有工作区的页面（登录卡片）退化为就地重写而不是报错：这个判断是契约的一部分，而非偶然。
	if !strings.Contains(refetch, "if (!d.querySelector(WORKSPACE_SELECTOR)) {") {
		t.Error("refetchWorkspace does not skip pages without a workspace | refetchWorkspace 没有跳过没有工作区的页面")
	}
	if !strings.Contains(refetch, "typeof api.ajax !== 'function'") {
		t.Error("refetchWorkspace does not check for htmx before calling it | refetchWorkspace 在调用 htmx 之前没有检查它是否存在")
	}
}

// TestThemeSwitchHasThreeStatesAndNoFlash pins the three-state theme switch and the first-paint
// order that makes it flash-free.
// TestThemeSwitchHasThreeStatesAndNoFlash 锁定三态主题切换，以及让它无闪烁的首帧顺序。
func TestThemeSwitchHasThreeStatesAndNoFlash(t *testing.T) {
	base := string(readEmbedded(t, web.FS(), baseLayoutPath))
	boot := string(readEmbedded(t, web.FS(), themeBootJSPath))
	app := string(readEmbedded(t, web.FS(), appJSPath))

	// 1. The ordering is the whole point: the resolving script runs inside <head> and its tag comes
	//    before the stylesheet link, so the first painted frame already carries the right theme.
	//    顺序正是关键：解析脚本在 <head> 内执行，其标签位于样式表之前，
	//    因此首帧画出来就是正确的主题。
	bootAt := strings.Index(base, `<script src="{{ asset "/static/js/yd-theme-boot.js" }}"></script>`)
	cssAt := strings.Index(base, `<link rel="stylesheet" href="{{ asset "/static/css/yd.css" }}">`)
	switch {
	case bootAt < 0:
		t.Fatal("base.head does not load yd-theme-boot.js synchronously | base.head 没有同步加载 yd-theme-boot.js")
	case cssAt < 0:
		t.Fatal("base.head does not link the stylesheet | base.head 没有链接样式表")
	case bootAt > cssAt:
		t.Error("yd-theme-boot.js is loaded after the stylesheet, so the browser paints one wrong frame first | yd-theme-boot.js 在样式表之后加载，浏览器会先画出错误的一帧")
	}
	if strings.Contains(base[bootAt:bootAt+len(`<script src="{{ asset "/static/js/yd-theme-boot.js" }}"></script>`)], "defer") {
		t.Error("yd-theme-boot.js must stay synchronous: a deferred script resolves the theme after the first paint | yd-theme-boot.js 必须保持同步：defer 脚本会在首帧之后才解析主题")
	}

	// 2. Three states, one whitelist, shared by the boot script and the shell: light, dark and
	//    system, where only "system" needs the browser to decide.
	//    三种状态、一份白名单，由首屏脚本与外壳共用：light、dark 与 system，
	//    其中只有 "system" 需要浏览器来决定。
	for _, want := range []string{
		"var CHOICES = { light: true, dark: true, system: true };",
		"var THEME_KEY = 'yd_theme';",
		"var DARK_QUERY = '(prefers-color-scheme: dark)';",
		"store.getItem(THEME_KEY)",
		"choice(readCookie(THEME_KEY))",
		"d.documentElement.setAttribute('data-theme', resolved);",
	} {
		if !strings.Contains(boot, want) {
			t.Errorf("yd-theme-boot.js is missing %s | yd-theme-boot.js 缺少 %s", want, want)
		}
	}
	for _, want := range []string{
		"var VALID_THEMES = { light: true, dark: true, system: true };",
		"function applyTheme(theme, persist) {",
		"write(THEME_KEY, choice);",
		"writeCookie(THEME_KEY, choice);",
		"d.documentElement.setAttribute('data-theme', resolved);",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("yd-app.js is missing %s | yd-app.js 缺少 %s", want, want)
		}
	}

	// 3. The interactive switch is instant: it writes the attribute and the preference, and never
	//    reloads the page. The only reload in the shell belongs to the realtime reconnect.
	//    交互式切换是即时的：写入属性与偏好，并且从不重载页面。
	//    外壳中唯一的重载属于实时推送的重连。
	for _, name := range []string{"function applyTheme(theme, persist) {", "function toggleTheme() {"} {
		if body := jsBody(t, app, name); strings.Contains(body, "location") {
			t.Errorf("yd-app.js %s touches location, so switching a theme would reload the page | yd-app.js 的 %s 触碰了 location，切换主题会重载页面", name, name)
		}
	}

	// 4. Every theme control offers all three states. | 每个主题控件都提供全部三种状态。
	selects := jsBody(t, app, "function currentTheme() {")
	if !strings.Contains(selects, "VALID_THEMES[fromSelect]") {
		t.Error("yd-app.js does not validate the value of [data-yd-theme-set] | yd-app.js 没有校验 [data-yd-theme-set] 的取值")
	}
	if !strings.Contains(app, "'[data-yd-theme-toggle]'") || !strings.Contains(app, "'[data-yd-theme-set]'") {
		t.Error("yd-app.js does not handle both theme controls | yd-app.js 没有同时处理两种主题控件")
	}

	// 5. The server pre-renders the choice onto <html>, so a light or dark choice is already correct
	//    in the first byte and "system" is the only case left to the browser.
	//    服务端把选择预渲染到 <html> 上，因此浅色/深色选择在首字节就已正确，
	//    "system" 是唯一留给浏览器处理的情况。
	for _, name := range []string{"templates/layouts/dashboard.html", "templates/pages/login.html", "templates/pages/error.html"} {
		html := string(readEmbedded(t, web.FS(), name))
		if !strings.Contains(html, `<html lang="{{.Lang}}" data-theme="{{.Theme}}">`) {
			t.Errorf("%s does not pre-render data-theme onto <html> | %s 没有把 data-theme 预渲染到 <html> 上", name, name)
		}
	}
	if !strings.Contains(base, `<meta name="color-scheme" content="light dark">`) {
		t.Error("base.head does not declare color-scheme, so form controls would not follow the theme | base.head 没有声明 color-scheme，表单控件不会跟随主题")
	}

	// 6. Both concrete themes exist in the stylesheet, and the dark one is the override block the
	//    attribute switches to. | 两种具体主题都在样式表中存在，暗色就是该属性切换到的覆盖块。
	css := appSource(t)
	if !strings.Contains(cssBlock(t, css, `[data-theme="dark"]`), "color-scheme: dark") {
		t.Error("the dark theme block does not declare color-scheme: dark | 暗色主题块没有声明 color-scheme: dark")
	}
}

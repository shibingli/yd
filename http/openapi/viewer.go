package openapi

import (
	"html"
	"strings"
)

// Viewer template placeholders. | 查看器模板占位符。
const (
	viewerTitlePlaceholder  = "__YD_OPENAPI_TITLE__"
	viewerDataPlaceholder   = "__YD_OPENAPI_DATA__"
	viewerScriptPlaceholder = "__YD_VIEWER_SCRIPT__"
)

// renderViewer renders the self-contained viewer page. | renderViewer 渲染自包含的查看器页面。
//
// The page inlines all of its CSS and JavaScript, never references an external CDN,
// and injects the document as an escaped JSON island that the script reads back. | 页面内联
// 全部 CSS 与 JavaScript，不引用任何外部 CDN，并把文档以转义后的 JSON 数据岛注入，由脚本读回。
func renderViewer(d *Document, o Options) ([]byte, error) {
	doc := d.normalized()
	raw, err := doc.JSON()
	if err != nil {
		return nil, err
	}
	return renderViewerWithJSON(doc, o, raw)
}

// renderViewerWithJSON renders the viewer from an already serialized document, so the handler
// can share one encoding between the JSON endpoint and the page. | renderViewerWithJSON 使用已
// 序列化的文档渲染查看器，使处理器能在 JSON 端点与页面之间共用一次编码。
func renderViewerWithJSON(doc Document, o Options, raw []byte) ([]byte, error) {
	// Insert the data first: a title containing the data placeholder would otherwise capture the
	// replacement and leave the script island holding the literal placeholder. | 先插入数据：
	// 否则包含数据占位符的标题会截获替换，让脚本岛里只剩字面占位符。
	page := strings.Replace(viewerTemplate, viewerDataPlaceholder, escapeJSONForScript(raw), 1)
	page = strings.Replace(page, viewerTitlePlaceholder, html.EscapeString(o.title(doc.Info)), 1)
	script := strings.TrimSpace(o.ViewerScript)
	if script == "" {
		script = DefaultViewerScript
	}
	page = strings.Replace(page, viewerScriptPlaceholder, html.EscapeString(script), 1)
	return []byte(page), nil
}

// escapeJSONForScript neutralises the characters that could break out of a script
// element. | escapeJSONForScript 中和可能突破 script 元素的字符。
//
// The replacement uses JSON unicode escapes, not HTML entities: script elements are raw text,
// so entities would be parsed literally and corrupt string values. | 替换使用 JSON 的 Unicode
// 转义而不是 HTML 实体：script 元素是原始文本，实体会被原样解析并破坏字符串内容。
func escapeJSONForScript(raw []byte) string {
	// The operands must be the JSON escape sequences, not the characters themselves; replacing a
	// character with itself is a no-op and leaves "</script>" able to terminate the element.
	// | 替换目标必须是 JSON 转义序列，而不是字符本身；把字符替换成它自己是空操作，会让
	// "</script>" 继续终止脚本元素。
	s := string(raw)
	s = strings.ReplaceAll(s, "<", `\u003c`)
	s = strings.ReplaceAll(s, ">", `\u003e`)
	s = strings.ReplaceAll(s, "&", `\u0026`)
	s = strings.ReplaceAll(s, "\u2028", `\u2028`)
	s = strings.ReplaceAll(s, "\u2029", `\u2029`)
	return s
}

// viewerTemplate is the built-in single page viewer. | viewerTemplate 是内置的单页查看器。
//
// Layout: a navigation list on the left and the selected object on the right. The viewer
// renders document metadata, servers, tags, security schemes and their OAuth flows, operations
// with parameters, request bodies, responses, headers, links and callbacks, webhooks, component
// schemas with composition keywords and constraints, and every x-* extension. | 布局：左侧导航
// 列表，右侧当前对象。查看器渲染文档元数据、服务器、标签、安全方案及其 OAuth 流程，操作及其参数、
// 请求体、响应、响应头、链接与回调，Webhooks，组件 Schema 的组合关键字与约束，以及全部 x-* 扩展。
//
// Every dynamic value is HTML escaped by the esc() helper before injection; URLs are only
// emitted for http(s) schemes. | 所有动态值在注入前均由 esc() 辅助函数做 HTML 转义；只有 http(s)
// 地址会作为链接输出。
const viewerTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__YD_OPENAPI_TITLE__</title>
<style>
:root{--bg:#fff;--fg:#1f2430;--muted:#6b7280;--line:#e5e7eb;--field:#94a3b8;--panel:#f7f9fc;--code:#f3f4f6;--accent:#2563eb;--warn:#b45309;color-scheme:light}
:root[data-theme="dark"]{--bg:#0f1115;--fg:#e6e8eb;--muted:#9aa4b2;--line:#262b33;--field:#64748b;--panel:#151922;--code:#11151c;--accent:#60a5fa;--warn:#fbbf24;color-scheme:dark}
@media (prefers-color-scheme:dark){:root:not([data-theme="light"]):not([data-theme="dark"]){--bg:#0f1115;--fg:#e6e8eb;--muted:#9aa4b2;--line:#262b33;--field:#64748b;--panel:#151922;--code:#11151c;--accent:#60a5fa;--warn:#fbbf24;color-scheme:dark}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,"PingFang SC","Microsoft YaHei",sans-serif}
a{color:var(--accent);text-decoration:none}a:hover{text-decoration:underline}
header.topbar{display:flex;align-items:baseline;gap:10px;flex-wrap:wrap;padding:13px 18px;border-bottom:1px solid var(--line);background:var(--panel);position:sticky;top:0;z-index:3}
header.topbar h1{margin:0;font-size:17px;font-weight:600}
header.topbar .version{color:var(--muted);font-size:12px;border:1px solid var(--line);border-radius:999px;padding:1px 8px}
header.topbar .description{color:var(--muted);font-size:12.5px;flex:1 1 100%;white-space:pre-wrap}
main{display:flex;align-items:flex-start}
aside{width:340px;flex:0 0 340px;border-right:1px solid var(--line);padding:12px;position:sticky;top:52px;max-height:calc(100vh - 52px);overflow:auto}
input[type=search]{width:100%;padding:7px 9px;border:1px solid var(--field);border-radius:7px;background:var(--bg);color:var(--fg);margin-bottom:10px;font:inherit}
input[type=search]:focus-visible,.try input:focus-visible,.try select:focus-visible,.try textarea:focus-visible,.theme-toggle:focus-visible{outline:2px solid var(--accent);outline-offset:1px}
.theme-toggle{padding:4px 9px;border:1px solid var(--field);border-radius:999px;background:var(--bg);color:var(--fg);font:inherit;font-size:12px;font-weight:600;cursor:pointer;white-space:nowrap}
.theme-toggle:hover{background:var(--panel)}
.route{display:flex;gap:8px;align-items:center;width:100%;text-align:left;background:none;border:0;border-radius:7px;padding:6px 8px;cursor:pointer;color:inherit;font:inherit}
.route:hover{background:var(--panel)}
.route.active{background:var(--panel);outline:1px solid var(--line)}
.route .path{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:12.5px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.method{flex:0 0 auto;font-size:11px;font-weight:700;text-transform:uppercase;border-radius:5px;padding:1px 6px;color:#fff}
.method.get{background:#0ea5e9}.method.post{background:#22c55e}.method.put{background:#f59e0b}.method.patch{background:#a855f7}
.method.delete{background:#ef4444}.method.head{background:#64748b}.method.options{background:#14b8a6}.method.trace{background:#7c3aed}
.badge{flex:0 0 auto;font-size:10px;font-weight:700;text-transform:uppercase;border-radius:5px;padding:1px 6px;background:var(--panel);border:1px solid var(--line);color:var(--muted)}
section.detail{flex:1 1 auto;min-width:0;padding:18px 22px 60px}
h2{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin:0 0 6px;font-size:16px;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
h3{margin:22px 0 8px;font-size:13px;text-transform:uppercase;letter-spacing:.04em;color:var(--muted)}
.summary{margin:6px 0 0;font-weight:600}
.description{margin:6px 0 0;white-space:pre-wrap}
.meta{margin:4px 0 0;color:var(--muted);font-size:12.5px}
.hint{color:var(--muted)}
.mono{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
table{border-collapse:collapse;width:100%;font-size:13px;margin:6px 0}
th,td{border:1px solid var(--line);padding:6px 9px;text-align:left;vertical-align:top}
th{background:var(--panel);font-weight:600;color:var(--muted);font-size:12px}
pre.code{background:var(--code);border:1px solid var(--line);border-radius:7px;padding:10px;overflow:auto;font-size:12.5px;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;margin:6px 0;white-space:pre-wrap}
.media{margin:8px 0}
.response{border:1px solid var(--line);border-radius:7px;padding:8px 10px;margin:8px 0}
.resp-head{display:flex;gap:8px;align-items:baseline;flex-wrap:wrap}
.code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-weight:700;color:var(--accent)}
.chip{display:inline-block;border:1px solid var(--line);border-radius:999px;padding:0 7px;margin:0 4px 0 0;font-size:11.5px;color:var(--muted);vertical-align:middle}
.req{color:var(--warn);font-size:11px;font-weight:700;text-transform:uppercase}
.schema{border-left:2px solid var(--line);padding:2px 0 2px 10px;margin:6px 0}
.schema-head{display:flex;gap:6px;align-items:center;flex-wrap:wrap;font-size:13px}
.schema .prop{border-top:1px dashed var(--line);padding:5px 0}
.schema .prop:first-of-type{border-top:0}
.prop-head{display:flex;gap:8px;align-items:baseline;flex-wrap:wrap}
.prop-name{font-weight:600}
.prop-desc{color:var(--muted);font-size:12.5px}
.kv{display:grid;grid-template-columns:max-content 1fr;gap:2px 12px;font-size:13px;margin:6px 0}
.kv dt{color:var(--muted)}
.kv dd{margin:0;min-width:0}
.kv dd pre.code{margin:0}
details.schema-details,.details{margin:4px 0}
details summary{cursor:pointer;color:var(--muted);font-size:12.5px}
details.raw{margin-top:24px}
ul.plain{margin:6px 0;padding-left:18px}
ul.plain li{margin:3px 0}
.ext-title{color:var(--muted);font-size:12px;text-transform:uppercase;letter-spacing:.04em;margin-top:8px}
noscript{display:block;padding:14px 18px;color:var(--muted)}
.route-group{margin:4px 0;border:1px solid var(--line);border-radius:7px;background:var(--bg);overflow:hidden}
.route-group>summary{display:flex;align-items:center;gap:8px;padding:6px 8px;cursor:pointer;font-size:11px;font-weight:700;text-transform:uppercase;letter-spacing:.04em;color:var(--muted);background:var(--panel);list-style:none}
.route-group>summary::-webkit-details-marker{display:none}
.route-group>summary::before{content:"▸";font-size:10px;transition:transform .12s}
.route-group[open]>summary::before{transform:rotate(90deg)}
.route-group__name{flex:1 1 auto;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.route-group__count{flex:0 0 auto;border:1px solid var(--line);border-radius:999px;padding:0 6px;font-weight:600;color:var(--muted);background:var(--bg)}
.route-group__body{display:flex;flex-direction:column;gap:1px;padding:3px}
.route-group[data-active-group]>summary{color:var(--fg)}
.try{margin-top:22px;border:1px solid var(--line);border-radius:9px;background:var(--panel);padding:10px 12px}
.try h3{margin:0 0 4px;font-size:13px;text-transform:uppercase;letter-spacing:.04em;color:var(--muted)}
.try-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(15rem,1fr));gap:10px 12px;margin:10px 0;align-items:end}
.try label{display:flex;flex-direction:column;justify-content:flex-end;gap:4px;min-width:0;font-size:12px;color:var(--muted)}
.try-label{display:flex;align-items:center;gap:4px;height:1.25rem;min-width:0;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}
.try-label .mono{overflow:hidden;text-overflow:ellipsis}
.try input,.try select{width:100%;box-sizing:border-box;height:2.25rem;padding:0 10px;border:1px solid var(--field);border-radius:6px;background:var(--bg);color:var(--fg);font:inherit;font-size:12.5px;line-height:1.25rem}
.try select{appearance:auto}
.try input::placeholder{color:var(--muted);opacity:.85}
.try textarea{width:100%;box-sizing:border-box;min-height:7rem;padding:8px 10px;border:1px solid var(--field);border-radius:6px;background:var(--bg);color:var(--fg);font:inherit;font-size:12.5px;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;resize:vertical}
.try-body{display:flex;flex-direction:column;gap:4px;margin:10px 0}
.try-actions{display:flex;justify-content:flex-end;margin-top:6px}
.try-send{height:2.25rem;padding:0 14px;border:1px solid transparent;border-radius:6px;background:var(--accent);color:#fff;font:inherit;font-size:12.5px;font-weight:600;cursor:pointer}
.try-send:disabled{opacity:.6;cursor:wait}
.try-result{margin-top:10px}
.try-status{display:inline-block;border-radius:999px;padding:2px 9px;font-size:12px;font-weight:700}
.try-status--ok{background:color-mix(in oklab,#16a34a 18%,transparent);color:#16a34a}
.try-status--err{background:color-mix(in oklab,#dc2626 18%,transparent);color:#dc2626}
.try-error{margin:0;color:#dc2626;font-size:12.5px}
.try-body-text{max-height:26rem;overflow:auto}
@media (max-width:820px){main{flex-direction:column}aside{width:100%;flex:1 1 auto;border-right:0;border-bottom:1px solid var(--line);position:static;max-height:none}}
</style>
</head>
<body>
<header class="topbar">
<h1 id="doc-title">API</h1>
<span class="version" id="doc-version"></span>
<button type="button" class="theme-toggle" id="theme-toggle" aria-label="Toggle light/dark theme | 切换明暗主题">System | 跟随系统</button>
<span class="description" id="doc-description"></span>
</header>
<main>
<aside>
<input id="filter" type="search" placeholder="Filter routes | 过滤路由" autocomplete="off" aria-label="Filter routes">
<nav id="route-list"></nav>
</aside>
<section class="detail" id="detail">
<p class="hint">Select an entry on the left. | 请在左侧选择一个条目。</p>
</section>
</main>
<noscript>Enable JavaScript to browse the document. | 请启用 JavaScript 以浏览文档。</noscript>
<script type="application/json" id="openapi-data">__YD_OPENAPI_DATA__</script>
<script src="__YD_VIEWER_SCRIPT__" defer></script>
</html>
`

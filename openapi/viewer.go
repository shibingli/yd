package openapi

import (
	"html"
	"strings"
)

// Viewer template placeholders. | 查看器模板占位符。
const (
	viewerTitlePlaceholder = "__YD_OPENAPI_TITLE__"
	viewerDataPlaceholder  = "__YD_OPENAPI_DATA__"
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
	page := strings.Replace(viewerTemplate, viewerTitlePlaceholder, html.EscapeString(o.title(doc.Info)), 1)
	page = strings.Replace(page, viewerDataPlaceholder, escapeJSONForScript(raw), 1)
	return []byte(page), nil
}

// escapeJSONForScript neutralises the characters that could break out of a script
// element. | escapeJSONForScript 中和可能突破 script 元素的字符。
//
// The framework JSON layer already escapes them; the substitution keeps the guarantee
// independent of the active backend. | 框架 JSON 层已做转义；本次替换让该保证不依赖具体后端。
func escapeJSONForScript(raw []byte) string {
	s := string(raw)
	s = strings.ReplaceAll(s, "<", `<`)
	s = strings.ReplaceAll(s, ">", `>`)
	s = strings.ReplaceAll(s, "&", `&`)
	return s
}

// viewerTemplate is the built-in single page viewer. | viewerTemplate 是内置的单页查看器。
//
// Layout: a route list on the left, the operation detail on the right (parameters,
// request body, responses, and the collapsible raw JSON). | 布局：左侧路由列表，右侧操作详情
// （参数、请求体、响应，以及可折叠的 JSON 原文）。
//
// Every dynamic value is HTML escaped by the esc() helper before injection. | 所有动态值在注入前
// 均由 esc() 辅助函数做 HTML 转义。
const viewerTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__YD_OPENAPI_TITLE__</title>
<style>
:root{--bg:#fff;--fg:#1f2430;--muted:#6b7280;--line:#e5e7eb;--panel:#f7f9fc;--code:#f3f4f6;--accent:#2563eb}
@media (prefers-color-scheme:dark){:root{--bg:#0f1115;--fg:#e6e8eb;--muted:#9aa4b2;--line:#262b33;--panel:#151922;--code:#11151c;--accent:#60a5fa}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,"PingFang SC","Microsoft YaHei",sans-serif}
header.topbar{display:flex;align-items:baseline;gap:10px;flex-wrap:wrap;padding:13px 18px;border-bottom:1px solid var(--line);background:var(--panel);position:sticky;top:0;z-index:3}
header.topbar h1{margin:0;font-size:17px;font-weight:600}
header.topbar .version{color:var(--muted);font-size:12px;border:1px solid var(--line);border-radius:999px;padding:1px 8px}
header.topbar .description{color:var(--muted);font-size:12.5px;flex:1 1 100%}
main{display:flex;align-items:flex-start}
aside{width:330px;flex:0 0 330px;border-right:1px solid var(--line);padding:12px;position:sticky;top:52px;max-height:calc(100vh - 52px);overflow:auto}
input[type=search]{width:100%;padding:7px 9px;border:1px solid var(--line);border-radius:7px;background:var(--bg);color:var(--fg);margin-bottom:10px;font:inherit}
.route{display:flex;gap:8px;align-items:center;width:100%;text-align:left;background:none;border:0;border-radius:7px;padding:6px 8px;cursor:pointer;color:inherit;font:inherit}
.route:hover{background:var(--panel)}
.route.active{background:var(--panel);outline:1px solid var(--line)}
.route .path{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:12.5px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.method{flex:0 0 auto;font-size:11px;font-weight:700;text-transform:uppercase;border-radius:5px;padding:1px 6px;color:#fff}
.method.get{background:#0ea5e9}.method.post{background:#22c55e}.method.put{background:#f59e0b}.method.patch{background:#a855f7}
.method.delete{background:#ef4444}.method.head{background:#64748b}.method.options{background:#14b8a6}
section.detail{flex:1 1 auto;min-width:0;padding:18px 22px 60px}
h2{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin:0 0 6px;font-size:16px;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
h3{margin:22px 0 8px;font-size:13px;text-transform:uppercase;letter-spacing:.04em;color:var(--muted)}
.summary{margin:6px 0 0;font-weight:600}
.description{margin:6px 0 0;white-space:pre-wrap}
.meta{margin:4px 0 0;color:var(--muted);font-size:12.5px}
.hint{color:var(--muted)}
.mono{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
table{border-collapse:collapse;width:100%;font-size:13px}
th,td{border:1px solid var(--line);padding:6px 9px;text-align:left;vertical-align:top}
th{background:var(--panel);font-weight:600;color:var(--muted);font-size:12px}
pre.code{background:var(--code);border:1px solid var(--line);border-radius:7px;padding:10px;overflow:auto;font-size:12.5px;font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;margin:6px 0}
.media{margin:8px 0}
.response{border:1px solid var(--line);border-radius:7px;padding:8px 10px;margin:8px 0}
.resp-head{display:flex;gap:8px;align-items:baseline}
.code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-weight:700;color:var(--accent)}
details.raw{margin-top:24px}
details.raw summary{cursor:pointer;color:var(--muted);font-size:13px}
noscript{display:block;padding:14px 18px;color:var(--muted)}
@media (max-width:820px){main{flex-direction:column}aside{width:100%;flex:1 1 auto;border-right:0;border-bottom:1px solid var(--line);position:static;max-height:none}}
</style>
</head>
<body>
<header class="topbar">
<h1 id="doc-title">API</h1>
<span class="version" id="doc-version"></span>
<span class="description" id="doc-description"></span>
</header>
<main>
<aside>
<input id="filter" type="search" placeholder="Filter routes | 过滤路由" autocomplete="off" aria-label="Filter routes">
<nav id="route-list"></nav>
</aside>
<section class="detail" id="detail">
<p class="hint">Select a route on the left. | 请在左侧选择一条路由。</p>
</section>
</main>
<noscript>Enable JavaScript to browse the document. | 请启用 JavaScript 以浏览文档。</noscript>
<script type="application/json" id="openapi-data">__YD_OPENAPI_DATA__</script>
<script>
(function () {
  var node = document.getElementById('openapi-data');
  var doc = {};
  try { doc = JSON.parse((node && (node.textContent || node.innerText)) || '{}'); } catch (err) { doc = {}; }
  var info = doc.info || {};
  document.getElementById('doc-title').textContent = info.title || 'API';
  document.getElementById('doc-version').textContent = info.version ? 'v' + info.version : '';
  var descriptionEl = document.getElementById('doc-description');
  descriptionEl.textContent = info.description || '';

  var METHODS = ['get', 'post', 'put', 'patch', 'delete', 'head', 'options'];
  var routes = [];
  var paths = doc.paths || {};
  Object.keys(paths).sort().forEach(function (path) {
    var item = paths[path] || {};
    METHODS.forEach(function (method) {
      if (item[method]) { routes.push({ path: path, method: method, op: item[method] }); }
    });
  });

  var listEl = document.getElementById('route-list');
  var detailEl = document.getElementById('detail');
  var filterEl = document.getElementById('filter');
  var active = -1;

  function esc(value) {
    if (value === null || value === undefined) { return ''; }
    return String(value).replace(/[&<>"']/g, function (ch) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch];
    });
  }

  function schemaLabel(schema) {
    if (!schema) { return ''; }
    if (schema.$ref) { return schema.$ref; }
    if (schema.type === 'array') { return 'array<' + (schema.items ? schemaLabel(schema.items) : 'any') + '>'; }
    if (!schema.type) { return ''; }
    return schema.format ? schema.type + ' (' + schema.format + ')' : schema.type;
  }

  function renderContent(content) {
    if (!content) { return '<p class="hint">None | 无</p>'; }
    var html = '';
    Object.keys(content).sort().forEach(function (media) {
      var schema = (content[media] || {}).schema;
      html += '<div class="media"><span class="mono">' + esc(media) + '</span>';
      if (schema) { html += '<pre class="code">' + esc(JSON.stringify(schema, null, 2)) + '</pre>'; }
      html += '</div>';
    });
    return html || '<p class="hint">None | 无</p>';
  }

  function renderList(filter) {
    var needle = (filter || '').toLowerCase();
    var html = '';
    routes.forEach(function (route, index) {
      if (needle && (route.method + ' ' + route.path).toLowerCase().indexOf(needle) < 0) { return; }
      html += '<button type="button" class="route' + (index === active ? ' active' : '') + '" data-index="' + index + '">' +
        '<span class="method ' + esc(route.method) + '">' + esc(route.method) + '</span>' +
        '<span class="path">' + esc(route.path) + '</span></button>';
    });
    listEl.innerHTML = html || '<p class="hint">No matching routes. | 没有匹配的路由。</p>';
  }

  function renderDetail(index) {
    var route = routes[index];
    if (!route) { return; }
    active = index;
    var op = route.op || {};
    var html = '<h2><span class="method ' + esc(route.method) + '">' + esc(route.method) + '</span>' + esc(route.path) + '</h2>';
    var meta = [];
    if (op.operationId) { meta.push('operationId: ' + op.operationId); }
    if (op.tags && op.tags.length) { meta.push('tags: ' + op.tags.join(', ')); }
    if (meta.length) { html += '<p class="meta">' + esc(meta.join('  |  ')) + '</p>'; }
    if (op.summary) { html += '<p class="summary">' + esc(op.summary) + '</p>'; }
    if (op.description) { html += '<p class="description">' + esc(op.description) + '</p>'; }

    var params = op.parameters || [];
    html += '<h3>Parameters | 参数</h3>';
    if (!params.length) {
      html += '<p class="hint">None | 无</p>';
    } else {
      html += '<table><thead><tr><th>Name | 名称</th><th>In | 位置</th><th>Required | 必填</th><th>Schema</th><th>Description | 说明</th></tr></thead><tbody>';
      params.forEach(function (param) {
        html += '<tr><td class="mono">' + esc(param.name) + '</td><td>' + esc(param.in) + '</td>' +
          '<td>' + (param.required ? 'yes' : 'no') + '</td>' +
          '<td class="mono">' + esc(schemaLabel(param.schema)) + '</td>' +
          '<td>' + esc(param.description) + '</td></tr>';
      });
      html += '</tbody></table>';
    }

    html += '<h3>Request Body | 请求体</h3>';
    if (op.requestBody) {
      if (op.requestBody.required) { html += '<p class="meta">required</p>'; }
      html += renderContent(op.requestBody.content);
    } else {
      html += '<p class="hint">None | 无</p>';
    }

    var responses = op.responses || {};
    var codes = Object.keys(responses).sort();
    html += '<h3>Responses | 响应</h3>';
    if (!codes.length) {
      html += '<p class="hint">None | 无</p>';
    } else {
      codes.forEach(function (code) {
        var res = responses[code] || {};
        html += '<div class="response"><div class="resp-head"><span class="code">' + esc(code) + '</span>' + esc(res.description || '') + '</div>';
        html += renderContent(res.content);
        html += '</div>';
      });
    }

    html += '<details class="raw"><summary>Raw JSON | JSON 原文</summary><pre class="code">' +
      esc(JSON.stringify(op, null, 2)) + '</pre></details>';
    detailEl.innerHTML = html;
    renderList(filterEl.value);
  }

  listEl.addEventListener('click', function (event) {
    var target = event.target;
    while (target && target !== listEl && !target.getAttribute('data-index')) { target = target.parentNode; }
    if (!target || target === listEl || !target.getAttribute) { return; }
    var index = parseInt(target.getAttribute('data-index'), 10);
    if (!isNaN(index)) { renderDetail(index); }
  });
  filterEl.addEventListener('input', function () { renderList(filterEl.value); });

  renderList('');
  if (routes.length) { renderDetail(0); }
  else { detailEl.innerHTML = '<p class="hint">No routes. | 暂无路由。</p>'; }
})();
</script>
</body>
</html>
`

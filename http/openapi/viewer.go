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
	return renderViewerWithJSON(doc, o, raw)
}

// renderViewerWithJSON renders the viewer from an already serialized document, so the handler
// can share one encoding between the JSON endpoint and the page. | renderViewerWithJSON 使用已
// 序列化的文档渲染查看器，使处理器能在 JSON 端点与页面之间共用一次编码。
func renderViewerWithJSON(doc Document, o Options, raw []byte) ([]byte, error) {
	page := strings.Replace(viewerTemplate, viewerTitlePlaceholder, html.EscapeString(o.title(doc.Info)), 1)
	page = strings.Replace(page, viewerDataPlaceholder, escapeJSONForScript(raw), 1)
	return []byte(page), nil
}

// escapeJSONForScript neutralises the characters that could break out of a script
// element. | escapeJSONForScript 中和可能突破 script 元素的字符。
//
// The replacement uses JSON unicode escapes, not HTML entities: script elements are raw text,
// so entities would be parsed literally and corrupt string values. | 替换使用 JSON 的 Unicode
// 转义而不是 HTML 实体：script 元素是原始文本，实体会被原样解析并破坏字符串内容。
func escapeJSONForScript(raw []byte) string {
	s := string(raw)
	s = strings.ReplaceAll(s, "<", `<`)
	s = strings.ReplaceAll(s, ">", `>`)
	s = strings.ReplaceAll(s, "&", `&`)
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
:root{--bg:#fff;--fg:#1f2430;--muted:#6b7280;--line:#e5e7eb;--panel:#f7f9fc;--code:#f3f4f6;--accent:#2563eb;--warn:#b45309}
@media (prefers-color-scheme:dark){:root{--bg:#0f1115;--fg:#e6e8eb;--muted:#9aa4b2;--line:#262b33;--panel:#151922;--code:#11151c;--accent:#60a5fa;--warn:#fbbf24}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.65 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,"PingFang SC","Microsoft YaHei",sans-serif}
a{color:var(--accent);text-decoration:none}a:hover{text-decoration:underline}
header.topbar{display:flex;align-items:baseline;gap:10px;flex-wrap:wrap;padding:13px 18px;border-bottom:1px solid var(--line);background:var(--panel);position:sticky;top:0;z-index:3}
header.topbar h1{margin:0;font-size:17px;font-weight:600}
header.topbar .version{color:var(--muted);font-size:12px;border:1px solid var(--line);border-radius:999px;padding:1px 8px}
header.topbar .description{color:var(--muted);font-size:12.5px;flex:1 1 100%;white-space:pre-wrap}
main{display:flex;align-items:flex-start}
aside{width:340px;flex:0 0 340px;border-right:1px solid var(--line);padding:12px;position:sticky;top:52px;max-height:calc(100vh - 52px);overflow:auto}
input[type=search]{width:100%;padding:7px 9px;border:1px solid var(--line);border-radius:7px;background:var(--bg);color:var(--fg);margin-bottom:10px;font:inherit}
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
<p class="hint">Select an entry on the left. | 请在左侧选择一个条目。</p>
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
  var components = doc.components || {};
  var paths = doc.paths || {};
  var webhooks = doc.webhooks || {};
  var METHODS = ['get', 'post', 'put', 'patch', 'delete', 'head', 'options', 'trace'];

  document.getElementById('doc-title').textContent = info.title || 'API';
  document.getElementById('doc-version').textContent = info.version ? 'v' + info.version : '';
  document.getElementById('doc-description').textContent = info.description || info.summary || '';

  var listEl = document.getElementById('route-list');
  var detailEl = document.getElementById('detail');
  var filterEl = document.getElementById('filter');
  var active = -1;
  var items = [];

  function esc(value) {
    if (value === null || value === undefined) { return ''; }
    return String(value).replace(/[&<>"']/g, function (ch) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch];
    });
  }

  function json(value) {
    if (value === undefined) { return ''; }
    return esc(JSON.stringify(value, null, 2));
  }

  function safeHref(url) {
    var text = String(url || '');
    return /^(https?:|mailto:)/i.test(text) ? esc(text) : '';
  }

  function link(url, label) {
    var href = safeHref(url);
    var text = esc(label || url || '');
    return href ? '<a href="' + href + '" target="_blank" rel="noopener noreferrer">' + text + '</a>' : text;
  }

  function eachSorted(object, fn) {
    Object.keys(object || {}).sort().forEach(fn);
  }

  function schemaLabel(schema) {
    if (!schema) { return 'any'; }
    if (schema.$ref) { return String(schema.$ref).split('/').pop(); }
    var type = schema.type;
    if (Array.isArray(type)) { type = type.join('|'); }
    if (type === 'array') {
      var inner = schema.items || (schema.prefixItems && schema.prefixItems[0]);
      return 'array<' + schemaLabel(inner) + '>';
    }
    if (!type) {
      if (schema.properties) { type = 'object'; }
      else if (schema.allOf) { type = 'allOf'; }
      else if (schema.oneOf) { type = 'oneOf'; }
      else if (schema.anyOf) { type = 'anyOf'; }
      else if (schema.not) { type = 'not'; }
      else { type = 'any'; }
    }
    return schema.format ? type + ' (' + schema.format + ')' : type;
  }

  function isComplex(schema) {
    return !!(schema && (schema.$ref || schema.properties || schema.items || schema.prefixItems ||
      schema.allOf || schema.oneOf || schema.anyOf || schema.not ||
      schema.additionalProperties !== undefined || schema.unevaluatedProperties !== undefined));
  }

  function renderExtensions(object) {
    var keys = Object.keys(object || {}).filter(function (key) { return key.indexOf('x-') === 0; }).sort();
    if (!keys.length) { return ''; }
    var html = '<div class="ext-title">Extensions | 扩展</div><dl class="kv">';
    keys.forEach(function (key) {
      html += '<dt class="mono">' + esc(key) + '</dt><dd><pre class="code">' + json(object[key]) + '</pre></dd>';
    });
    return html + '</dl>';
  }

  function constraintFacts(schema) {
    var facts = [];
    if (schema.title) { facts.push(['title', esc(schema.title)]); }
    if (schema.default !== undefined) { facts.push(['default', '<code>' + json(schema.default) + '</code>']); }
    if (schema.const !== undefined) { facts.push(['const', '<code>' + json(schema.const) + '</code>']); }
    if (schema.enum) { facts.push(['enum', '<code>' + json(schema.enum) + '</code>']); }
    if (schema.minimum !== undefined) { facts.push([schema.exclusiveMinimum === true ? 'exclusiveMinimum' : 'minimum', esc(schema.minimum)]); }
    if (typeof schema.exclusiveMinimum === 'number') { facts.push(['exclusiveMinimum', esc(schema.exclusiveMinimum)]); }
    if (schema.maximum !== undefined) { facts.push([schema.exclusiveMaximum === true ? 'exclusiveMaximum' : 'maximum', esc(schema.maximum)]); }
    if (typeof schema.exclusiveMaximum === 'number') { facts.push(['exclusiveMaximum', esc(schema.exclusiveMaximum)]); }
    if (schema.multipleOf !== undefined) { facts.push(['multipleOf', esc(schema.multipleOf)]); }
    if (schema.minLength !== undefined) { facts.push(['minLength', esc(schema.minLength)]); }
    if (schema.maxLength !== undefined) { facts.push(['maxLength', esc(schema.maxLength)]); }
    if (schema.minItems !== undefined) { facts.push(['minItems', esc(schema.minItems)]); }
    if (schema.maxItems !== undefined) { facts.push(['maxItems', esc(schema.maxItems)]); }
    if (schema.uniqueItems) { facts.push(['uniqueItems', 'true']); }
    if (schema.minProperties !== undefined) { facts.push(['minProperties', esc(schema.minProperties)]); }
    if (schema.maxProperties !== undefined) { facts.push(['maxProperties', esc(schema.maxProperties)]); }
    if (schema.pattern) { facts.push(['pattern', '<code>' + esc(schema.pattern) + '</code>']); }
    if (schema.contentEncoding) { facts.push(['contentEncoding', esc(schema.contentEncoding)]); }
    if (schema.contentMediaType) { facts.push(['contentMediaType', esc(schema.contentMediaType)]); }
    return facts;
  }

  function renderSchema(schema, depth) {
    if (!schema) { return '<p class="hint">any | 任意</p>'; }
    if (depth > 6) { return '<pre class="code">' + json(schema) + '</pre>'; }
    var html = '<div class="schema"><div class="schema-head"><span class="mono">' + esc(schemaLabel(schema)) + '</span>';
    if (schema.format) { html += '<span class="chip">' + esc(schema.format) + '</span>'; }
    if (schema.nullable) { html += '<span class="chip">nullable</span>'; }
    if (schema.readOnly) { html += '<span class="chip">readOnly</span>'; }
    if (schema.writeOnly) { html += '<span class="chip">writeOnly</span>'; }
    if (schema.deprecated) { html += '<span class="chip">deprecated</span>'; }
    html += '</div>';
    if (schema.description) { html += '<div class="description">' + esc(schema.description) + '</div>'; }
    var facts = constraintFacts(schema);
    if (facts.length) {
      html += '<dl class="kv">';
      facts.forEach(function (fact) { html += '<dt>' + esc(fact[0]) + '</dt><dd>' + fact[1] + '</dd>'; });
      html += '</dl>';
    }
    if (schema.properties) {
      var required = {};
      (schema.required || []).forEach(function (name) { required[name] = true; });
      eachSorted(schema.properties, function (name) {
        var prop = schema.properties[name] || {};
        html += '<div class="prop"><div class="prop-head"><span class="prop-name mono">' + esc(name) + '</span>' +
          (required[name] ? '<span class="req">required</span>' : '') +
          '<span class="hint mono">' + esc(schemaLabel(prop)) + '</span></div>';
        if (prop.description) { html += '<div class="prop-desc">' + esc(prop.description) + '</div>'; }
        if (isComplex(prop)) {
          html += '<details class="schema-details"><summary>schema</summary>' + renderSchema(prop, depth + 1) + '</details>';
        } else {
          html += renderExtensions(prop);
        }
        html += '</div>';
      });
    }
    if (schema.items) { html += '<div class="hint">items</div>' + renderSchema(schema.items, depth + 1); }
    if (schema.prefixItems) {
      html += '<div class="hint">prefixItems</div>';
      schema.prefixItems.forEach(function (item, index) { html += '<div class="meta">#' + index + '</div>' + renderSchema(item, depth + 1); });
    }
    ['allOf', 'oneOf', 'anyOf'].forEach(function (keyword) {
      if (!schema[keyword]) { return; }
      html += '<div class="hint">' + keyword + '</div>';
      schema[keyword].forEach(function (part) { html += renderSchema(part, depth + 1); });
    });
    if (schema.not) { html += '<div class="hint">not</div>' + renderSchema(schema.not, depth + 1); }
    if (schema.additionalProperties !== undefined && typeof schema.additionalProperties === 'object') {
      html += '<div class="hint">additionalProperties</div>' + renderSchema(schema.additionalProperties, depth + 1);
    } else if (schema.additionalProperties !== undefined) {
      html += '<div class="meta">additionalProperties: ' + esc(schema.additionalProperties) + '</div>';
    }
    if (schema.unevaluatedProperties !== undefined && typeof schema.unevaluatedProperties === 'object') {
      html += '<div class="hint">unevaluatedProperties</div>' + renderSchema(schema.unevaluatedProperties, depth + 1);
    }
    if (schema.propertyNames) { html += '<div class="hint">propertyNames</div>' + renderSchema(schema.propertyNames, depth + 1); }
    if (schema.contains) { html += '<div class="hint">contains</div>' + renderSchema(schema.contains, depth + 1); }
    if (schema.if) { html += '<div class="hint">if</div>' + renderSchema(schema.if, depth + 1); }
    if (schema.then) { html += '<div class="hint">then</div>' + renderSchema(schema.then, depth + 1); }
    if (schema.else) { html += '<div class="hint">else</div>' + renderSchema(schema.else, depth + 1); }
    if (schema.discriminator) {
      html += '<div class="meta">discriminator: <span class="mono">' + esc(schema.discriminator.propertyName) + '</span>';
      eachSorted(schema.discriminator.mapping, function (key) {
        html += '<div class="mono">' + esc(key) + ' → ' + esc(schema.discriminator.mapping[key]) + '</div>';
      });
      html += '</div>';
    }
    if (schema.xml) { html += '<div class="meta">xml: <span class="mono">' + esc(schema.xml.name || '') + '</span></div>'; }
    if (schema.externalDocs) { html += '<div class="meta">docs: ' + link(schema.externalDocs.url, schema.externalDocs.description) + '</div>'; }
    html += renderExtensions(schema);
    return html + '</div>';
  }

  function renderExamples(examples) {
    var html = '';
    eachSorted(examples, function (name) {
      var example = examples[name] || {};
      html += '<div class="meta mono">' + esc(name) + '</div>';
      if (example.summary) { html += '<div>' + esc(example.summary) + '</div>'; }
      if (example.description) { html += '<div class="prop-desc">' + esc(example.description) + '</div>'; }
      if (example.value !== undefined) { html += '<pre class="code">' + json(example.value) + '</pre>'; }
      if (example.externalValue) { html += '<div>' + link(example.externalValue, example.externalValue) + '</div>'; }
      html += renderExtensions(example);
    });
    return html;
  }

  function renderEncoding(encoding) {
    if (!encoding || !Object.keys(encoding).length) { return ''; }
    var html = '<div class="ext-title">Encoding | 编码</div><table><thead><tr><th>Property | 属性</th><th>Content-Type</th><th>Style</th><th>Flags</th><th>Headers</th></tr></thead><tbody>';
    eachSorted(encoding, function (name) {
      var item = encoding[name] || {};
      var flags = [];
      if (item.explode !== undefined) { flags.push('explode=' + item.explode); }
      if (item.allowReserved) { flags.push('allowReserved'); }
      var headers = item.headers ? Object.keys(item.headers).sort().join(', ') : '';
      html += '<tr><td class="mono">' + esc(name) + '</td><td class="mono">' + esc(item.contentType || '') + '</td><td>' +
        esc(item.style || '') + '</td><td>' + esc(flags.join(', ')) + '</td><td class="mono">' + esc(headers) + '</td></tr>';
    });
    return html + '</tbody></table>';
  }

  function renderContent(content) {
    if (!content || !Object.keys(content).length) { return '<p class="hint">None | 无</p>'; }
    var html = '';
    eachSorted(content, function (media) {
      var item = content[media] || {};
      html += '<div class="media"><span class="mono">' + esc(media) + '</span>';
      if (item.schema) { html += renderSchema(item.schema, 0); }
      if (item.example !== undefined) { html += '<pre class="code">' + json(item.example) + '</pre>'; }
      if (item.examples) { html += renderExamples(item.examples); }
      html += renderEncoding(item.encoding);
      html += renderExtensions(item);
      html += '</div>';
    });
    return html;
  }

  function mergeParameters(shared, own) {
    var byKey = {}, order = [];
    (shared || []).concat(own || []).forEach(function (param) {
      var key = (param.in || '') + ' ' + (param.name || '');
      if (!(key in byKey)) { order.push(key); }
      byKey[key] = param;
    });
    return order.map(function (key) { return byKey[key]; });
  }

  function renderParameters(params) {
    if (!params || !params.length) { return '<p class="hint">None | 无</p>'; }
    var html = '<table><thead><tr><th>Name | 名称</th><th>In | 位置</th><th>Required | 必填</th><th>Schema</th><th>Flags | 标记</th><th>Description | 说明</th></tr></thead><tbody>';
    params.forEach(function (param) {
      var flags = [];
      if (param.$ref) { flags.push('$ref'); }
      if (param.style) { flags.push('style=' + param.style); }
      if (param.explode !== undefined) { flags.push('explode=' + param.explode); }
      if (param.deprecated) { flags.push('deprecated'); }
      if (param.allowEmptyValue) { flags.push('allowEmptyValue'); }
      if (param.allowReserved) { flags.push('allowReserved'); }
      var extra = esc(param.description || '');
      if (param.example !== undefined) { extra += '<pre class="code">' + json(param.example) + '</pre>'; }
      if (param.examples) { extra += renderExamples(param.examples); }
      if (param.content) { extra += renderContent(param.content); }
      extra += renderExtensions(param);
      html += '<tr><td class="mono">' + esc(param.name || '') + '</td><td>' + esc(param.in || '') + '</td>' +
        '<td>' + (param.required ? 'yes' : 'no') + '</td>' +
        '<td class="mono">' + esc(schemaLabel(param.schema)) + '</td>' +
        '<td>' + esc(flags.join(', ')) + '</td><td>' + extra + '</td></tr>';
    });
    return html + '</tbody></table>';
  }

  function renderHeaders(headers) {
    if (!headers || !Object.keys(headers).length) { return ''; }
    var html = '<table><thead><tr><th>Header | 响应头</th><th>Schema</th><th>Flags | 标记</th><th>Description | 说明</th></tr></thead><tbody>';
    eachSorted(headers, function (name) {
      var header = headers[name] || {};
      var flags = [];
      if (header.required) { flags.push('required'); }
      if (header.deprecated) { flags.push('deprecated'); }
      if (header.style) { flags.push('style=' + header.style); }
      if (header.explode !== undefined) { flags.push('explode=' + header.explode); }
      if (header.allowEmptyValue) { flags.push('allowEmptyValue'); }
      if (header.allowReserved) { flags.push('allowReserved'); }
      var extra = esc(header.description || '');
      if (header.example !== undefined) { extra += '<pre class="code">' + json(header.example) + '</pre>'; }
      if (header.examples) { extra += renderExamples(header.examples); }
      if (header.content) { extra += renderContent(header.content); }
      extra += renderExtensions(header);
      html += '<tr><td class="mono">' + esc(name) + '</td><td class="mono">' + esc(schemaLabel(header.schema)) + '</td><td>' +
        esc(flags.join(', ')) + '</td><td>' + extra + '</td></tr>';
    });
    return html + '</tbody></table>';
  }

  function renderLinks(links) {
    if (!links || !Object.keys(links).length) { return ''; }
    var html = '<table><thead><tr><th>Link | 链接</th><th>Target</th><th>Description | 说明</th></tr></thead><tbody>';
    eachSorted(links, function (name) {
      var item = links[name] || {};
      var target = item.operationId ? 'operationId: ' + item.operationId : (item.operationRef ? 'operationRef: ' + item.operationRef : '');
      html += '<tr><td class="mono">' + esc(name) + '</td><td class="mono">' + esc(target) + '</td><td>' +
        esc(item.description || '') + renderExtensions(item) + '</td></tr>';
    });
    return html + '</tbody></table>';
  }

  function renderResponses(responses) {
    if (!responses || !Object.keys(responses).length) { return '<p class="hint">None | 无</p>'; }
    var html = '';
    eachSorted(responses, function (code) {
      var res = responses[code] || {};
      html += '<div class="response"><div class="resp-head"><span class="code">' + esc(code) + '</span><span>' + esc(res.description || '') + '</span></div>';
      if (res.$ref) { html += '<div class="mono">' + esc(res.$ref) + '</div>'; }
      html += renderHeaders(res.headers);
      html += renderLinks(res.links);
      html += renderContent(res.content);
      html += renderExtensions(res);
      html += '</div>';
    });
    return html;
  }

  function renderCallbacks(callbacks) {
    if (!callbacks || !Object.keys(callbacks).length) { return ''; }
    var html = '<h3>Callbacks | 回调</h3>';
    eachSorted(callbacks, function (name) {
      html += '<div class="response"><div class="resp-head"><span class="code">' + esc(name) + '</span></div>';
      eachSorted(callbacks[name], function (expression) {
        var item = callbacks[name][expression] || {};
        METHODS.forEach(function (method) {
          if (!item[method]) { return; }
          html += '<div class="meta mono">' + esc(method.toUpperCase() + ' ' + expression) + '</div>';
          if (item[method].summary) { html += '<div>' + esc(item[method].summary) + '</div>'; }
          if (item[method].description) { html += '<div class="prop-desc">' + esc(item[method].description) + '</div>'; }
        });
      });
      html += renderExtensions(callbacks[name]);
      html += '</div>';
    });
    return html;
  }

  function renderServers(servers) {
    if (!servers || !servers.length) { return '<p class="hint">None | 无</p>'; }
    var html = '<ul class="plain">';
    servers.forEach(function (server) {
      html += '<li><span class="mono">' + esc(server.url || '') + '</span>';
      if (server.description) { html += ' — ' + esc(server.description); }
      if (server.variables) {
        html += '<dl class="kv">';
        eachSorted(server.variables, function (name) {
          var variable = server.variables[name] || {};
          var text = 'default=' + (variable.default === undefined ? '' : variable.default);
          if (variable.enum) { text += ', enum=' + variable.enum.join('|'); }
          if (variable.description) { text += ' — ' + variable.description; }
          html += '<dt class="mono">' + esc(name) + '</dt><dd>' + esc(text) + '</dd>';
        });
        html += '</dl>';
      }
      html += renderExtensions(server);
      html += '</li>';
    });
    return html + '</ul>';
  }

  function renderSecurityRequirement(requirements, schemes) {
    if (!requirements || !requirements.length) { return '<p class="hint">None | 无</p>'; }
    var html = '';
    requirements.forEach(function (requirement) {
      var parts = Object.keys(requirement || {}).sort().map(function (name) {
        var scopes = requirement[name] || [];
        var known = schemes && schemes[name];
        var text = name + (known && known.type ? ' (' + known.type + ')' : '');
        if (scopes.length) { text += ' [' + scopes.join(', ') + ']'; }
        return text;
      });
      html += '<span class="chip mono">' + esc(parts.join(' && ')) + '</span>';
    });
    return html;
  }

  function renderSecuritySchemes(schemes) {
    if (!schemes || !Object.keys(schemes).length) { return '<p class="hint">None | 无</p>'; }
    var html = '';
    eachSorted(schemes, function (name) {
      var scheme = schemes[name] || {};
      html += '<div class="response"><div class="resp-head"><span class="code">' + esc(name) + '</span><span class="chip">' + esc(scheme.type || '') + '</span></div>';
      var facts = [];
      if (scheme.description) { facts.push(['description', esc(scheme.description)]); }
      if (scheme.scheme) { facts.push(['scheme', esc(scheme.scheme)]); }
      if (scheme.bearerFormat) { facts.push(['bearerFormat', esc(scheme.bearerFormat)]); }
      if (scheme.name) { facts.push(['name', esc(scheme.name)]); }
      if (scheme.in) { facts.push(['in', esc(scheme.in)]); }
      if (scheme.openIdConnectUrl) { facts.push(['openIdConnectUrl', link(scheme.openIdConnectUrl, scheme.openIdConnectUrl)]); }
      if (facts.length) {
        html += '<dl class="kv">';
        facts.forEach(function (fact) { html += '<dt>' + esc(fact[0]) + '</dt><dd>' + fact[1] + '</dd>'; });
        html += '</dl>';
      }
      if (scheme.flows) {
        ['implicit', 'password', 'clientCredentials', 'authorizationCode'].forEach(function (flowName) {
          var flow = scheme.flows[flowName];
          if (!flow) { return; }
          html += '<div class="ext-title">' + esc(flowName) + '</div><dl class="kv">';
          if (flow.authorizationUrl) { html += '<dt>authorizationUrl</dt><dd>' + link(flow.authorizationUrl, flow.authorizationUrl) + '</dd>'; }
          if (flow.tokenUrl) { html += '<dt>tokenUrl</dt><dd>' + link(flow.tokenUrl, flow.tokenUrl) + '</dd>'; }
          if (flow.refreshUrl) { html += '<dt>refreshUrl</dt><dd>' + link(flow.refreshUrl, flow.refreshUrl) + '</dd>'; }
          eachSorted(flow.scopes, function (scope) {
            html += '<dt class="mono">' + esc(scope) + '</dt><dd>' + esc(flow.scopes[scope]) + '</dd>';
          });
          html += '</dl>';
        });
      }
      html += renderExtensions(scheme);
      html += '</div>';
    });
    return html;
  }

  function renderTags(tags) {
    if (!tags || !tags.length) { return '<p class="hint">None | 无</p>'; }
    var html = '';
    tags.forEach(function (tag) {
      html += '<div class="response"><div class="resp-head"><span class="code">' + esc(tag.name || '') + '</span></div>';
      if (tag.description) { html += '<div class="description">' + esc(tag.description) + '</div>'; }
      if (tag.externalDocs) { html += '<div class="meta">' + link(tag.externalDocs.url, tag.externalDocs.description || tag.externalDocs.url) + '</div>'; }
      html += renderExtensions(tag);
      html += '</div>';
    });
    return html;
  }

  function componentCounts() {
    var names = ['schemas', 'securitySchemes', 'parameters', 'responses', 'requestBodies', 'headers', 'examples', 'links', 'callbacks', 'pathItems'];
    var html = '<table><thead><tr><th>Section</th><th>Count</th><th>Names</th></tr></thead><tbody>';
    names.forEach(function (name) {
      var section = components[name] || {};
      var keys = Object.keys(section).sort();
      if (!keys.length) { return; }
      html += '<tr><td class="mono">' + esc(name) + '</td><td>' + keys.length + '</td><td class="mono">' + esc(keys.join(', ')) + '</td></tr>';
    });
    html += '</tbody></table>';
    return html;
  }

  function renderOverview() {
    var html = '<h2>' + esc(info.title || 'API') + '</h2>';
    if (info.summary) { html += '<p class="summary">' + esc(info.summary) + '</p>'; }
    if (info.description) { html += '<p class="description">' + esc(info.description) + '</p>'; }
    var facts = [];
    if (info.version) { facts.push(['version', esc(info.version)]); }
    if (info.termsOfService) { facts.push(['termsOfService', link(info.termsOfService, info.termsOfService)]); }
    if (info.contact) {
      var contact = [];
      if (info.contact.name) { contact.push(esc(info.contact.name)); }
      if (info.contact.email) { contact.push(link('mailto:' + info.contact.email, info.contact.email)); }
      if (info.contact.url) { contact.push(link(info.contact.url, info.contact.url)); }
      if (contact.length) { facts.push(['contact', contact.join(' · ')]); }
    }
    if (info.license) {
      var license = esc(info.license.name || info.license.identifier || '');
      if (info.license.url) { license += ' ' + link(info.license.url, info.license.url); }
      if (license) { facts.push(['license', license]); }
    }
    if (doc.externalDocs) { facts.push(['externalDocs', link(doc.externalDocs.url, doc.externalDocs.description || doc.externalDocs.url)]); }
    if (doc.jsonSchemaDialect) { facts.push(['jsonSchemaDialect', '<span class="mono">' + esc(doc.jsonSchemaDialect) + '</span>']); }
    if (facts.length) {
      html += '<dl class="kv">';
      facts.forEach(function (fact) { html += '<dt>' + esc(fact[0]) + '</dt><dd>' + fact[1] + '</dd>'; });
      html += '</dl>';
    }
    html += '<h3>Servers | 服务器</h3>' + renderServers(doc.servers);
    html += '<h3>Tags | 标签</h3>' + renderTags(doc.tags);
    html += '<h3>Global Security | 全局安全</h3>' + renderSecurityRequirement(doc.security, components.securitySchemes);
    html += '<h3>Security Schemes | 安全方案</h3>' + renderSecuritySchemes(components.securitySchemes);
    if (Object.keys(webhooks).length) {
      html += '<h3>Webhooks | Webhook</h3><ul class="plain">';
      eachSorted(webhooks, function (name) {
        METHODS.forEach(function (method) {
          if (webhooks[name] && webhooks[name][method]) {
            html += '<li><span class="method ' + esc(method) + '">' + esc(method) + '</span> <span class="mono">' + esc(name) + '</span></li>';
          }
        });
      });
      html += '</ul>';
    }
    html += '<h3>Components | 组件</h3>' + componentCounts();
    html += renderExtensions(doc);
    return html;
  }

  function renderOperation(item) {
    var op = item.op || {};
    var shared = item.pathItem || {};
    var servers = (op.servers && op.servers.length) ? op.servers : shared.servers;
    var html = '<h2><span class="method ' + esc(item.method) + '">' + esc(item.method) + '</span>' + esc(item.path) +
      (item.kind === 'webhook' ? '<span class="badge">webhook</span>' : '') + '</h2>';
    var meta = [];
    if (op.operationId) { meta.push('operationId: ' + op.operationId); }
    if (op.tags && op.tags.length) { meta.push('tags: ' + op.tags.join(', ')); }
    if (op.deprecated) { meta.push('deprecated | 已废弃'); }
    var security = op.security || doc.security;
    if (security && security.length) {
      meta.push('security: ' + security.map(function (requirement) {
        return Object.keys(requirement || {}).sort().map(function (name) {
          var scopes = requirement[name] || [];
          return scopes.length ? name + ' [' + scopes.join(', ') + ']' : name;
        }).join(' && ');
      }).join(' || '));
    }
    if (servers && servers.length) { meta.push('servers: ' + servers.map(function (s) { return s.url; }).join(', ')); }
    if (meta.length) { html += '<p class="meta">' + esc(meta.join('  |  ')) + '</p>'; }
    if (shared.summary) { html += '<p class="meta">path: ' + esc(shared.summary) + '</p>'; }
    if (shared.description) { html += '<p class="meta">' + esc(shared.description) + '</p>'; }
    if (op.summary) { html += '<p class="summary">' + esc(op.summary) + '</p>'; }
    if (op.description) { html += '<p class="description">' + esc(op.description) + '</p>'; }
    if (op.externalDocs) { html += '<p class="meta">docs: ' + link(op.externalDocs.url, op.externalDocs.description || op.externalDocs.url) + '</p>'; }
    if (servers && servers.length) {
      html += '<h3>Servers | 服务器</h3>' + renderServers(servers);
    }
    html += '<h3>Parameters | 参数</h3>' + renderParameters(mergeParameters(shared.parameters, op.parameters));
    html += '<h3>Request Body | 请求体</h3>';
    if (op.requestBody) {
      if (op.requestBody.$ref) { html += '<p class="mono">' + esc(op.requestBody.$ref) + '</p>'; }
      if (op.requestBody.required) { html += '<p class="meta">required | 必填</p>'; }
      if (op.requestBody.description) { html += '<p class="description">' + esc(op.requestBody.description) + '</p>'; }
      html += renderContent(op.requestBody.content);
      html += renderExtensions(op.requestBody);
    } else {
      html += '<p class="hint">None | 无</p>';
    }
    html += '<h3>Responses | 响应</h3>' + renderResponses(op.responses);
    html += renderCallbacks(op.callbacks);
    html += renderExtensions(shared);
    html += renderExtensions(op);
    html += '<details class="raw"><summary>Raw JSON | JSON 原文</summary><pre class="code">' + json(op) + '</pre></details>';
    return html;
  }

  function renderDetail(index) {
    var item = items[index];
    if (!item) { return; }
    active = index;
    var html;
    if (item.kind === 'overview') { html = renderOverview(); }
    else if (item.kind === 'schema') {
      html = '<h2>Schema · ' + esc(item.name) + '</h2>' + renderSchema(item.schema, 0) +
        '<details class="raw"><summary>Raw JSON | JSON 原文</summary><pre class="code">' + json(item.schema) + '</pre></details>';
    } else if (item.kind === 'security') {
      var schemes = {}; schemes[item.name] = item.scheme;
      html = '<h2>Security · ' + esc(item.name) + '</h2>' + renderSecuritySchemes(schemes) +
        '<details class="raw"><summary>Raw JSON | JSON 原文</summary><pre class="code">' + json(item.scheme) + '</pre></details>';
    } else { html = renderOperation(item); }
    detailEl.innerHTML = html;
    renderList(filterEl.value);
  }

  function itemText(item) {
    if (item.kind === 'overview') { return 'overview 概览'; }
    if (item.kind === 'schema') { return 'schema ' + item.name; }
    if (item.kind === 'security') { return 'security ' + item.name; }
    return item.method + ' ' + item.path + (item.kind === 'webhook' ? ' webhook' : '');
  }

  function renderList(filter) {
    var needle = (filter || '').toLowerCase();
    var html = '';
    items.forEach(function (item, index) {
      if (needle && itemText(item).toLowerCase().indexOf(needle) < 0) { return; }
      var badge;
      if (item.kind === 'operation' || item.kind === 'webhook') {
        badge = '<span class="method ' + esc(item.method) + '">' + esc(item.method) + '</span>';
      } else if (item.kind === 'overview') {
        badge = '<span class="badge">doc</span>';
      } else {
        badge = '<span class="badge">' + esc(item.kind) + '</span>';
      }
      var label = item.kind === 'operation' || item.kind === 'webhook' ? item.path : (item.name || 'Overview | 概览');
      html += '<button type="button" class="route' + (index === active ? ' active' : '') + '" data-index="' + index + '">' +
        badge + '<span class="path">' + esc(label) + '</span></button>';
    });
    listEl.innerHTML = html || '<p class="hint">No matching entries. | 没有匹配的条目。</p>';
  }

  items.push({ kind: 'overview', name: 'Overview | 概览' });
  eachSorted(paths, function (path) {
    var item = paths[path] || {};
    METHODS.forEach(function (method) {
      if (item[method]) { items.push({ kind: 'operation', path: path, method: method, op: item[method], pathItem: item }); }
    });
  });
  eachSorted(webhooks, function (name) {
    var item = webhooks[name] || {};
    METHODS.forEach(function (method) {
      if (item[method]) { items.push({ kind: 'webhook', path: name, method: method, op: item[method], pathItem: item }); }
    });
  });
  eachSorted(components.schemas, function (name) {
    items.push({ kind: 'schema', name: name, schema: components.schemas[name] });
  });
  eachSorted(components.securitySchemes, function (name) {
    items.push({ kind: 'security', name: name, scheme: components.securitySchemes[name] });
  });

  listEl.addEventListener('click', function (event) {
    var target = event.target;
    while (target && target !== listEl && !target.getAttribute('data-index')) { target = target.parentNode; }
    if (!target || target === listEl || !target.getAttribute) { return; }
    var index = parseInt(target.getAttribute('data-index'), 10);
    if (!isNaN(index)) { renderDetail(index); }
  });
  filterEl.addEventListener('input', function () { renderList(filterEl.value); });

  renderList('');
  renderDetail(0);
})();
</script>
</body>
</html>
`

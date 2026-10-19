/*!
 * yd-openapi-viewer.js — built-in OpenAPI viewer behaviour, loaded as a same-origin module.
 * yd-openapi-viewer.js — 内置 OpenAPI 查看器脚本，以同源脚本加载。
 *
 * Why it is a file rather than an inline <script> | 为什么是文件而不是内联 <script>：
 *   Generated projects ship a Content-Security-Policy with script-src 'self' and no 'unsafe-inline'.
 *   An inline viewer script is therefore blocked by the browser, and /docs renders an empty page.
 *   This file keeps the same origin, so the policy stays strict and the viewer keeps working.
 *   生成项目的内容安全策略是 script-src 'self' 且不含 'unsafe-inline'，内联查看器脚本会被浏览器直接拦截，
 *   /docs 因而显示空白页。本文件保持同源，策略继续严格，查看器照常工作。
 *
 * The document is read from <script type="application/json" id="openapi-data">, which the server renders
 * escaped; this script never fetches anything.
 * 文档从服务端转义渲染的 <script type="application/json" id="openapi-data"> 读取；本脚本不发起任何请求。
 */

(function () {
  /* Theme: share the application's yd_theme choice, and let the viewer switch it too.
     The button cycles light -> dark -> auto; auto leaves data-theme off so the CSS media query
     follows the operating system. | 主题：共享应用的 yd_theme 选择，并允许查看器自行切换。
     按钮按浅色 -> 深色 -> 跟随系统循环；跟随系统时移除 data-theme，由 CSS 媒体查询跟随操作系统。 */
  var THEME_KEY = 'yd_theme';
  var THEMES = ['light', 'dark', 'auto'];

  function readTheme() {
    try {
      var saved = window.localStorage.getItem(THEME_KEY);
      return THEMES.indexOf(saved) >= 0 ? saved : 'auto';
    } catch (err) {
      return 'auto';
    }
  }

  function applyTheme(choice) {
    if (choice === 'auto') {
      document.documentElement.removeAttribute('data-theme');
    } else {
      document.documentElement.setAttribute('data-theme', choice);
    }
    var button = document.getElementById('theme-toggle');
    if (button) {
      button.setAttribute('data-theme-choice', choice);
      button.textContent = choice === 'light' ? 'Light | 浅色' : (choice === 'dark' ? 'Dark | 深色' : 'System | 跟随系统');
    }
  }

  applyTheme(readTheme());
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

  function renderOperation(item, index) {
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
    html += renderTryIt(item, index);
    html += '<details class="raw"><summary>Raw JSON | JSON 原文</summary><pre class="code">' + json(op) + '</pre></details>';
    return html;
  }

  /* schemaSample builds a small request-body example from a schema. | schemaSample 从 schema 生成一个简单的请求体示例。 */
  function schemaSample(schema, depth) {
    if (!schema || depth > 4) { return {}; }
    if (schema.example !== undefined) { return schema.example; }
    if (schema.default !== undefined) { return schema.default; }
    if (schema.enum && schema.enum.length) { return schema.enum[0]; }
    if (schema.$ref) { return {}; }
    if (schema.type === 'array') { return [schemaSample(schema.items || {}, depth + 1)]; }
    if (schema.type === 'object' || schema.properties) {
      var out = {};
      eachSorted(schema.properties || {}, function (name) {
        out[name] = schemaSample(schema.properties[name] || {}, depth + 1);
      });
      return out;
    }
    if (schema.type === 'string') { return ''; }
    if (schema.type === 'integer' || schema.type === 'number') { return 0; }
    if (schema.type === 'boolean') { return false; }
    return null;
  }

  /* bodyMediaType returns the media type the form should submit. | bodyMediaType 返回表单应提交的媒体类型。 */
  function bodyMediaType(requestBody) {
    if (!requestBody || !requestBody.content) { return ''; }
    var keys = Object.keys(requestBody.content);
    if (!keys.length) { return ''; }
    if (requestBody.content['application/json']) { return 'application/json'; }
    return keys[0];
  }

  /* bodyExample returns the JSON example of a request body. | bodyExample 返回请求体的 JSON 示例。 */
  function bodyExample(requestBody) {
    if (!requestBody || !requestBody.content) { return ''; }
    var content = requestBody.content;
    var media = content['application/json'] || content[Object.keys(content)[0]];
    if (!media) { return ''; }
    if (media.example !== undefined) { return JSON.stringify(media.example, null, 2); }
    if (media.examples) {
      var names = Object.keys(media.examples);
      if (names.length && media.examples[names[0]] && media.examples[names[0]].value !== undefined) {
        return JSON.stringify(media.examples[names[0]].value, null, 2);
      }
    }
    if (media.schema) { return JSON.stringify(schemaSample(media.schema, 0), null, 2); }
    return '';
  }

  /* renderTryIt renders the online request builder for one operation. Requests are same-origin
     only: the page's CSP carries connect-src 'self', so a cross-origin target is reported instead
     of being sent. Same-origin cookies are included automatically.
     | renderTryIt 渲染单个操作的在线请求构造器。请求只允许同源：页面 CSP 的 connect-src 是 'self'，
     跨源目标会直接报告而不是发送；同源 cookie 会自动携带。 */
  function renderTryIt(item, index) {
    var op = item.op || {};
    var shared = item.pathItem || {};
    var params = mergeParameters(shared.parameters, op.parameters);
    var servers = (op.servers && op.servers.length) ? op.servers :
      ((shared.servers && shared.servers.length) ? shared.servers : (doc.servers || []));
    var mediaType = bodyMediaType(op.requestBody);
    var html = '<section class="try" data-try data-index="' + index + '" data-try-media="' + esc(mediaType) + '">' +
      '<h3>Try it | 在线测试</h3>' +
      '<p class="hint">Same-origin only; the session cookie is sent automatically. | 仅同源请求；会话 cookie 会自动携带。</p>' +
      '<div class="try-grid">' +
      '<label><span class="try-label">Server | 服务器</span><select data-try-server>';
    var seen = {};
    function addServer(value, label) {
      if (!value || seen[value]) { return; }
      seen[value] = true;
      html += '<option value="' + esc(value) + '">' + esc(label) + '</option>';
    }
    addServer(window.location.origin, 'This origin | 当前源');
    (servers || []).forEach(function (server) {
      var raw = String(server.url || '');
      var resolved = raw;
      try { resolved = new URL(raw, window.location.origin).toString(); } catch (err) { resolved = raw; }
      var cross = false;
      try { cross = new URL(resolved, window.location.origin).origin !== window.location.origin; } catch (err) { cross = false; }
      addServer(resolved, raw + (cross ? ' (cross-origin blocked | 跨源被拦截)' : ''));
    });
    html += '</select></label>' +
      '<label><span class="try-label">Authorization</span><input data-try-auth placeholder="Bearer …" autocomplete="off"></label>';
    params.forEach(function (param) {
      if (!param || !param.name || ['path', 'query', 'header'].indexOf(param.in) < 0) { return; }
      html += '<label><span class="try-label">' + esc(param.in) + ' · <span class="mono">' + esc(param.name) + '</span>' +
        (param.required ? ' <span class="req">required</span>' : '') + '</span>' +
        '<input data-try-param="' + esc(param.in + ':' + param.name) + '" autocomplete="off" placeholder="' + esc(param.description || '') + '"></label>';
    });
    html += '</div>';
    if (op.requestBody) {
      html += '<label class="try-body"><span class="try-label">Body | 请求体 <span class="hint">' + esc(mediaType) + '</span></span>' +
        '<textarea data-try-body rows="8" spellcheck="false">' + esc(bodyExample(op.requestBody)) + '</textarea></label>';
    }
    html += '<div class="try-actions"><button type="button" class="try-send" data-try-send>Send | 发送</button></div>' +
      '<div class="try-result" data-try-result hidden></div></section>';
    return html;
  }

  /* formEncode turns the JSON example into an application/x-www-form-urlencoded body, falling back
     to the raw text when it is not JSON. | formEncode 把 JSON 示例转成表单编码正文；不是 JSON 时原样返回。 */
  function formEncode(raw) {
    try {
      var object = JSON.parse(raw);
      if (!object || typeof object !== 'object' || Array.isArray(object)) { return raw; }
      var params = new URLSearchParams();
      Object.keys(object).forEach(function (key) {
        var value = object[key];
        if (value === null || value === undefined) { return; }
        params.append(key, typeof value === 'object' ? JSON.stringify(value) : String(value));
      });
      return params.toString();
    } catch (err) {
      return raw;
    }
  }

  /* multipartBody turns the JSON example into a FormData body; the browser supplies the boundary. | multipartBody 把 JSON 示例转成 FormData，边界由浏览器提供。 */
  function multipartBody(raw) {
    var form = new FormData();
    try {
      var object = JSON.parse(raw);
      if (object && typeof object === 'object' && !Array.isArray(object)) {
        Object.keys(object).forEach(function (key) {
          var value = object[key];
          if (value === null || value === undefined) { return; }
          form.append(key, typeof value === 'object' ? JSON.stringify(value) : String(value));
        });
      }
    } catch (err) {
      /* Not JSON: send the fields the user typed as one text part. | 不是 JSON：把用户输入作为一个文本字段发送。 */
      form.append('body', raw);
    }
    return form;
  }

  /* sendTry executes the request described by one try-it form. | sendTry 执行一个在线测试表单描述的请求。 */
  function sendTry(button) {
    var box = button.closest('[data-try]');
    if (!box) { return; }
    var index = parseInt(box.getAttribute('data-index'), 10);
    var item = items[index];
    if (!item || (item.kind !== 'operation' && item.kind !== 'webhook')) { return; }
    var result = box.querySelector('[data-try-result]');
    var server = box.querySelector('[data-try-server]');
    var auth = box.querySelector('[data-try-auth]');
    var bodyEl = box.querySelector('[data-try-body]');
    var base = (server && server.value ? server.value : window.location.origin).trim();
    var origin;
    try { origin = new URL(base, window.location.origin).origin; } catch (err) { origin = ''; }
    if (!origin || (origin !== window.location.origin && origin !== 'null')) {
      result.hidden = false;
      result.innerHTML = '<p class="try-error">Cross-origin requests are blocked by this page\'s CSP; use the current origin. | 跨源请求会被本页 CSP 拦截，请使用当前源。</p>';
      return;
    }
    function paramValue(where, name) {
      var inputs = box.querySelectorAll('[data-try-param]');
      for (var i = 0; i < inputs.length; i++) {
        if (inputs[i].getAttribute('data-try-param') === where + ':' + name) { return inputs[i].value; }
      }
      return '';
    }
    var method = String(item.method || 'get').toUpperCase();
    var path = String(item.path || '').replace(/\{([^}]+)\}/g, function (_, name) {
      return encodeURIComponent(paramValue('path', name));
    });
    var url;
    try {
      url = new URL((base.replace(/\/$/, '') || '') + path, window.location.origin);
    } catch (err) {
      result.hidden = false;
      result.innerHTML = '<p class="try-error">' + esc(String(err)) + '</p>';
      return;
    }
    var headers = { 'Accept': 'application/json, text/html' };
    var params = mergeParameters((item.pathItem || {}).parameters, item.op.parameters);
    params.forEach(function (param) {
      if (!param || !param.name) { return; }
      var value = paramValue(param.in, param.name);
      if (value === '') { return; }
      if (param.in === 'query') { url.searchParams.set(param.name, value); }
      if (param.in === 'header') { headers[param.name] = value; }
    });
    var body;
    if (method !== 'GET' && method !== 'HEAD' && bodyEl && bodyEl.value.trim() !== '') {
      var mediaType = box.getAttribute('data-try-media') || 'application/json';
      var raw = bodyEl.value;
      if (mediaType === 'application/x-www-form-urlencoded') {
        body = formEncode(raw);
        headers['Content-Type'] = 'application/x-www-form-urlencoded';
      } else if (mediaType === 'multipart/form-data') {
        body = multipartBody(raw);
        /* The browser sets the multipart boundary itself. | 边界由浏览器自行设置。 */
      } else {
        body = raw;
        headers['Content-Type'] = mediaType || 'application/json';
      }
    }
    if (auth && auth.value.trim() !== '') { headers['Authorization'] = auth.value.trim(); }
    result.hidden = false;
    result.innerHTML = '<p class="hint">Sending… | 发送中…</p>';
    button.disabled = true;
    var started = (window.performance && performance.now) ? performance.now() : Date.now();
    fetch(url.toString(), {
      method: method,
      headers: headers,
      body: body,
      credentials: 'same-origin'
    }).then(function (response) {
      return response.text().then(function (text) {
        var elapsed = Math.round(((window.performance && performance.now) ? performance.now() : Date.now()) - started);
        var headerText = '';
        response.headers.forEach(function (value, name) { headerText += name + ': ' + value + '\n'; });
        var pretty = text;
        try { pretty = JSON.stringify(JSON.parse(text), null, 2); } catch (err) { pretty = text; }
        var ok = response.status >= 200 && response.status < 400;
        result.innerHTML = '<div class="try-status ' + (ok ? 'try-status--ok' : 'try-status--err') + '">' +
          esc(response.status + ' ' + response.statusText) + ' · ' + elapsed + ' ms</div>' +
          '<p class="meta mono">' + esc(response.url) + '</p>' +
          '<details open><summary>Response headers | 响应头</summary><pre class="code">' + esc(headerText) + '</pre></details>' +
          '<pre class="code try-body-text">' + esc(pretty) + '</pre>';
        button.disabled = false;
      });
    }).catch(function (err) {
      result.innerHTML = '<p class="try-error">' + esc(String(err)) + '</p>';
      button.disabled = false;
    });
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
    } else { html = renderOperation(item, index); }
    detailEl.innerHTML = html;
    renderList(filterEl.value);
  }

  function itemText(item) {
    if (item.kind === 'overview') { return 'overview 概览'; }
    if (item.kind === 'schema') { return 'schema ' + item.name; }
    if (item.kind === 'security') { return 'security ' + item.name; }
    return item.method + ' ' + item.path + (item.kind === 'webhook' ? ' webhook' : '');
  }

  /* Groups keep the left navigation readable: operations are grouped by their first tag
     (doc.tags order first, then any undeclared tag alphabetically), and webhooks, schemas
     and security schemes get groups of their own.
     | 分组让左侧导航保持可读：操作按第一个 tag 归类（先按 doc.tags 的顺序，再按字母序列出
     未声明的 tag），webhooks、数据模型与安全方案各自成组。 */
  function groupLabel(item) {
    if (item.kind === 'overview') { return 'Overview | 概览'; }
    if (item.kind === 'webhook') { return 'Webhooks | Webhook'; }
    if (item.kind === 'schema') { return 'Schemas | 数据模型'; }
    if (item.kind === 'security') { return 'Security schemes | 安全方案'; }
    return item.group || 'Untagged | 未分组';
  }

  function renderList(filter) {
    var needle = (filter || '').toLowerCase();
    var html = '';
    var openGroup = '';
    var group = '';
    var groupHtml = '';
    var groupCount = 0;
    function flush() {
      if (!group) { return; }
      html += '<details class="route-group" open' + (group === openGroup ? ' data-active-group' : '') + '>' +
        '<summary><span class="route-group__name">' + esc(group) + '</span>' +
        '<span class="route-group__count">' + groupCount + '</span></summary>' +
        '<div class="route-group__body">' + groupHtml + '</div></details>';
      groupHtml = '';
      groupCount = 0;
    }
    items.forEach(function (item, index) {
      if (needle && itemText(item).toLowerCase().indexOf(needle) < 0) { return; }
      var label = groupLabel(item);
      if (label !== group) {
        flush();
        group = label;
      }
      if (index === active) { openGroup = label; }
      var badge;
      if (item.kind === 'operation' || item.kind === 'webhook') {
        badge = '<span class="method ' + esc(item.method) + '">' + esc(item.method) + '</span>';
      } else if (item.kind === 'overview') {
        badge = '<span class="badge">doc</span>';
      } else {
        badge = '<span class="badge">' + esc(item.kind) + '</span>';
      }
      var routeLabel = item.kind === 'operation' || item.kind === 'webhook' ? item.path : (item.name || 'Overview | 概览');
      groupHtml += '<button type="button" class="route' + (index === active ? ' active' : '') + '" data-index="' + index + '">' +
        badge + '<span class="path">' + esc(routeLabel) + '</span></button>';
      groupCount++;
    });
    flush();
    listEl.innerHTML = html || '<p class="hint">No matching entries. | 没有匹配的条目。</p>';
    /* Keep the selected entry visible when the list is rebuilt after a click. | 选中条目后在列表重建时保持可见。 */
    var current = listEl.querySelector('.route.active');
    if (current && current.scrollIntoView) {
      current.scrollIntoView({ block: 'nearest' });
    }
  }

  /* Build the grouped order: overview, operations by tag, webhooks, schemas, security. | 构造分组顺序：
     概览、按 tag 归类的操作、webhooks、数据模型、安全方案。 */
  var declaredTags = (doc.tags || []).map(function (tag) { return tag.name; }).filter(Boolean);
  var extraTags = {};
  var operations = [];
  eachSorted(paths, function (path) {
    var pathItem = paths[path] || {};
    METHODS.forEach(function (method) {
      var op = pathItem[method];
      if (!op) { return; }
      var tag = (op.tags && op.tags[0]) || '';
      if (tag && declaredTags.indexOf(tag) < 0) { extraTags[tag] = true; }
      operations.push({ kind: 'operation', path: path, method: method, op: op, pathItem: pathItem, group: tag || 'Untagged | 未分组' });
    });
  });
  var tagOrder = declaredTags.concat(Object.keys(extraTags).sort());
  operations.sort(function (a, b) {
    var ai = tagOrder.indexOf(a.group);
    var bi = tagOrder.indexOf(b.group);
    if (ai < 0) { ai = tagOrder.length; }
    if (bi < 0) { bi = tagOrder.length; }
    if (ai !== bi) { return ai - bi; }
    if (a.group !== b.group) { return a.group < b.group ? -1 : 1; }
    if (a.path !== b.path) { return a.path < b.path ? -1 : 1; }
    return a.method < b.method ? -1 : (a.method > b.method ? 1 : 0);
  });

  items.push({ kind: 'overview', name: 'Overview | 概览', group: 'Overview | 概览' });
  operations.forEach(function (item) { items.push(item); });
  eachSorted(webhooks, function (name) {
    var item = webhooks[name] || {};
    METHODS.forEach(function (method) {
      if (item[method]) { items.push({ kind: 'webhook', path: name, method: method, op: item[method], pathItem: item, group: 'Webhooks | Webhook' }); }
    });
  });
  eachSorted(components.schemas, function (name) {
    items.push({ kind: 'schema', name: name, schema: components.schemas[name], group: 'Schemas | 数据模型' });
  });
  eachSorted(components.securitySchemes, function (name) {
    items.push({ kind: 'security', name: name, scheme: components.securitySchemes[name], group: 'Security schemes | 安全方案' });
  });

  listEl.addEventListener('click', function (event) {
    var target = event.target;
    while (target && target !== listEl && !target.getAttribute('data-index')) { target = target.parentNode; }
    if (!target || target === listEl || !target.getAttribute) { return; }
    var index = parseInt(target.getAttribute('data-index'), 10);
    if (!isNaN(index)) { renderDetail(index); }
  });
  detailEl.addEventListener('click', function (event) {
    var button = event.target && event.target.closest ? event.target.closest('[data-try-send]') : null;
    if (button && detailEl.contains(button)) { sendTry(button); }
  });
  filterEl.addEventListener('input', function () { renderList(filterEl.value); });

  var themeButton = document.getElementById('theme-toggle');
  if (themeButton) {
    themeButton.addEventListener('click', function () {
      var current = readTheme();
      var next = THEMES[(THEMES.indexOf(current) + 1) % THEMES.length];
      try {
        window.localStorage.setItem(THEME_KEY, next);
      } catch (err) {
        /* A blocked store only costs persistence, not the current switch. | 存储被禁用只影响持久化，不影响本次切换。 */
      }
      applyTheme(next);
    });
  }

  renderList('');
  renderDetail(0);
})();

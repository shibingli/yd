/*!
 * yd-guomi-htmx.js — htmx integration for the pure-JS guomi sandbox. | 纯 JS 国密沙箱的 htmx 集成。
 *
 * Load it after htmx and after yd-guomi.js; the sample below is language neutral. | 请在 htmx 与
 * yd-guomi.js 之后引入本文件，下面的代码示例与语言无关。
 *
 *   <script src="/static/js/vendor/htmx.min.js"></script>
 *   <script src="/static/js/yd-guomi.js"></script>
 *   <script src="/static/js/yd-guomi-htmx.js"></script>
 *   <script>ydGuomiHtmx.install();</script>
 *
 * Declarative usage: mark the form with the algorithm and the public key URL, then mark the fields
 * that must not travel in clear text. | 声明式用法：在表单上标注算法与公钥地址，再标注不得明文传输的字段。
 *
 *   <form hx-post="/login"
 *         data-yd-encrypt="sm2"
 *         data-yd-pubkey="/api/guomi/pubkey">
 *     <input name="username" value="alice">
 *     <input name="password" type="password" data-yd-sensitive>
 *     <button>Sign in</button>
 *   </form>
 *
 * Every field carrying data-yd-sensitive is replaced, in the outgoing request body, by the
 * standard Base64 of its SM2 ciphertext in the raw guomi.EncryptRaw layout
 * X1(32)||Y1(32)||C3(32)||C2(len) with no 0x04 prefix, and the request gains the header
 * `X-YD-Encrypted: sm2`. | 请求体中所有带 data-yd-sensitive 的字段都会被替换为其 SM2 密文（原始
 * guomi.EncryptRaw 布局 X1(32)||Y1(32)||C3(32)||C2(len)，无 0x04 前缀）的**标准 Base64**，
 * 并在请求头加上 `X-YD-Encrypted: sm2`。
 *
 *   - Wire format: standard Base64 (RFC 4648 with padding), never URL-safe Base64, so the Go side
 *     decodes it with b64x.StdEncoding and guomi.DecryptRaw. | 传输格式：标准 Base64（RFC 4648，带填充），
 *     不使用 URL 安全变体，Go 侧用 b64x.StdEncoding 与 guomi.DecryptRaw 解码。
 *   - Empty values are left untouched: there is nothing to protect and the field keeps its meaning
 *     as "not submitted". | 空值保持原样：没有需要保护的内容，同时保留「未填写」的语义。
 *   - The public key is prefetched and cached per URL, because htmx runs the
 *     htmx:config:request handlers synchronously while SM2 encryption needs the key up front. | 公钥按
 *     URL 预取并缓存，因为 htmx 会同步执行 htmx:config:request 处理器，而 SM2 加密必须先有公钥。
 *   - Fail closed: if the key cannot be obtained or used, the request is cancelled instead of being
 *     sent in clear text. | 失败即关闭：无法取得或使用公钥时取消该请求，绝不降级为明文发送。
 *
 * Integrity helper attached to the core module | 挂载在核心模块上的完整性校验辅助函数：
 *
 *   ydGuomi.verifyIntegrity(url, expectSm3Hex) -> Promise<boolean>
 *
 * Fetches url, hashes the body with SM3 and compares it, in constant time, with the expected
 * lowercase hex digest. | 取回 url，对响应体做 SM3 摘要，并以恒定时间与期望的小写十六进制摘要比对。
 * It throws synchronously with code `invalid-hex` when the expectation is malformed, and rejects
 * with code `fetch-failed` when the response is not ok. | 期望值格式非法时同步抛出 `invalid-hex`，
 * 响应非 OK 时以 `fetch-failed` 拒绝。
 */
(function (root, factory) {
  'use strict';
  var core = null;
  // Prefer the sibling module so a bundler or Node resolves the exact same instance. | 优先使用同级模块，使打包器或 Node 解析到同一个实例。
  if (typeof require === 'function') {
    try {
      core = require('./yd-guomi.js');
    } catch (err) {
      core = null;
    }
  }
  if (!core && root && root.ydGuomi) {
    core = root.ydGuomi;
  }
  var api = factory(core);
  if (typeof module === 'object' && module && module.exports) {
    module.exports = api;
  }
  if (root) {
    root.ydGuomiHtmx = api;
  }
})(typeof globalThis !== 'undefined' ? globalThis : typeof self !== 'undefined' ? self : this, function (core) {
  'use strict';

  // Defaults | 默认值
  var DEFAULTS = {
    // configEvent is the htmx event that runs before a request is issued. | configEvent 是 htmx 发起请求前触发的事件。
    configEvent: 'htmx:config:request',
    // loadEvents trigger a public key prefetch after new content is swapped in. | loadEvents 是新内容换入后触发公钥预取的事件。
    loadEvents: ['htmx:load', 'htmx:after:swap'],
    // encryptAttribute marks a form for encryption and selects the algorithm. | encryptAttribute 标记需要加密的表单并选择算法。
    encryptAttribute: 'data-yd-encrypt',
    // sensitiveAttribute marks the fields to encrypt. | sensitiveAttribute 标记需要加密的字段。
    sensitiveAttribute: 'data-yd-sensitive',
    // pubkeyAttribute carries the public key URL. | pubkeyAttribute 携带公钥地址。
    pubkeyAttribute: 'data-yd-pubkey',
    // headerName is the request header that flags an encrypted body. | headerName 是标记加密请求体的请求头。
    headerName: 'X-YD-Encrypted',
    // headerValue is the value written into that header. | headerValue 是写入该请求头的值。
    headerValue: 'sm2',
    // mode is the only supported algorithm identifier. | mode 是唯一支持的算法标识。
    mode: 'sm2',
    // sensitiveSelector selects the fields inside a form. | sensitiveSelector 选取表单内的敏感字段。
    sensitiveSelector: '[data-yd-sensitive]',
    // pubkeySelector selects the forms whose keys are prefetched. | pubkeySelector 选取需要预取公钥的表单。
    pubkeySelector: 'form[data-yd-pubkey]',
    // publicKey is an optional static public key used when a form declares no URL. | publicKey 是可选的内置公钥，供未声明地址的表单使用。
    publicKey: null,
    // prefetch enables the background public key prefetch. | prefetch 控制后台公钥预取。
    prefetch: true,
    // eventTarget is the object the listener is attached to. | eventTarget 是监听器挂载的对象。
    eventTarget: null,
  };

  // state is the mutable module state: resolved options, caches and the active listener. | state 是模块可变状态：已解析配置、缓存与当前监听器。
  var state = {
    options: null,
    keys: Object.create(null),
    pending: Object.create(null),
    listeners: [],
    target: null,
  };

  // hookError builds an Error with a stable code and a bilingual message. | hookError 构造带稳定错误码与双语文案的 Error。
  function hookError(code, en, zh) {
    var err = new Error('ydGuomiHtmx: ' + en + ' | ydGuomiHtmx：' + zh);
    err.name = 'YdGuomiHtmxError';
    err.code = code;
    return err;
  }

  // requireCore returns the sandbox or throws a documented error. | requireCore 返回沙箱实例，缺失时抛出文档化错误。
  function requireCore() {
    if (!core || typeof core.sm2 !== 'object') {
      throw hookError('core-missing', 'yd-guomi.js must be loaded before this module', '必须先加载 yd-guomi.js 再加载本模块');
    }
    return core;
  }

  // warn reports a bilingual warning without throwing. | warn 输出双语警告，不抛错。
  function warn(message) {
    if (typeof console !== 'undefined' && console && typeof console.warn === 'function') {
      console.warn('ydGuomiHtmx: ' + message);
    }
  }

  // resolveOptions merges caller options over the defaults. | resolveOptions 把调用方配置合并到默认值之上。
  function resolveOptions(options) {
    var merged = {};
    var key;
    for (key in DEFAULTS) {
      if (Object.prototype.hasOwnProperty.call(DEFAULTS, key)) {
        merged[key] = DEFAULTS[key];
      }
    }
    if (options && typeof options === 'object') {
      for (key in options) {
        if (Object.prototype.hasOwnProperty.call(options, key) && options[key] !== undefined) {
          merged[key] = options[key];
        }
      }
    }
    return merged;
  }

  // setPublicKey caches a public key for one URL, skipping the network round trip. | setPublicKey 为某个地址缓存公钥，省去网络往返。
  function setPublicKey(url, publicKeyHex) {
    requireCore();
    var text = typeof publicKeyHex === 'string' ? publicKeyHex.trim() : '';
    if (!text) {
      throw hookError('invalid-key', 'public key must be a non-empty hex string', '公钥必须是非空十六进制字符串');
    }
    // Validate the shape eagerly so a malformed key fails here instead of at submit time. | 立即校验形状，使非法公钥在此处而非提交时才失败。
    var raw = text.replace(/^0[xX]/, '');
    if (!(raw.length === 130 || raw.length === 128) || !/^[0-9a-fA-F]+$/.test(raw)) {
      throw hookError('invalid-key', 'public key must be 130 or 128 hex characters', '公钥必须是 130 或 128 个十六进制字符');
    }
    state.keys[url] = text;
    return text;
  }

  // cachedPublicKey returns the cached key for a URL, or null. | cachedPublicKey 返回某个地址已缓存的公钥，缺失时返回 null。
  function cachedPublicKey(url) {
    if (Object.prototype.hasOwnProperty.call(state.keys, url)) {
      return state.keys[url];
    }
    return null;
  }

  // publicKeyFor resolves to the public key of a URL, fetching and caching it once. | publicKeyFor 解析为某个地址的公钥，首次获取后缓存。
  //
  // Concurrent callers share one request through state.pending. | 并发调用通过 state.pending 共享同一次请求。
  function publicKeyFor(url) {
    var sandbox = requireCore();
    var cached = cachedPublicKey(url);
    if (cached) {
      return Promise.resolve(cached);
    }
    var options = state.options || DEFAULTS;
    if (!url) {
      if (options.publicKey) {
        return Promise.resolve(options.publicKey);
      }
      return Promise.reject(hookError('no-public-key', 'the form declares no data-yd-pubkey and no default key is set', '表单未声明 data-yd-pubkey 且未设置默认公钥'));
    }
    if (state.pending[url]) {
      return state.pending[url];
    }
    var fetcher = typeof options.fetchPublicKey === 'function' ? options.fetchPublicKey : fetchPublicKeyHex;
    var promise = Promise.resolve()
      .then(function () {
        return fetcher(url, sandbox);
      })
      .then(function (hex) {
        setPublicKey(url, hex);
        delete state.pending[url];
        return state.keys[url];
      })
      .catch(function (err) {
        delete state.pending[url];
        throw err;
      });
    state.pending[url] = promise;
    return promise;
  }

  // fetchPublicKeyHex fetches a public key, accepting hex text or a JSON envelope. | fetchPublicKeyHex 获取公钥，同时接受纯十六进制文本与 JSON 包装。
  //
  // Recognised JSON shapes: "hex", {"publicKey": "..."} and {"data": {"publicKey": "..."}}, which
  // covers the usual framework response envelopes. | 可识别的 JSON 形态："hex"、{"publicKey": "..."}
  // 与 {"data": {"publicKey": "..."}}，覆盖框架常见的响应包装。
  function fetchPublicKeyHex(url, sandbox) {
    if (typeof fetch !== 'function') {
      throw hookError('fetch-unavailable', 'fetch is unavailable, call ydGuomiHtmx.setPublicKey first', '当前环境没有 fetch，请先调用 ydGuomiHtmx.setPublicKey');
    }
    return fetch(url, { headers: { Accept: 'text/plain, application/json' }, credentials: 'same-origin' })
      .then(function (response) {
        if (!response || !response.ok) {
          throw hookError('fetch-failed', 'public key request to ' + url + ' failed with status ' + (response && response.status), '获取公钥 ' + url + ' 失败，状态码 ' + (response && response.status));
        }
        return response.text();
      })
      .then(function (text) {
        var candidate = extractPublicKey(text);
        if (!candidate) {
          throw hookError('invalid-key', 'the response of ' + url + ' carries no public key', url + ' 的响应中没有公钥');
        }
        return candidate;
      });
  }

  // extractPublicKey pulls a public key out of plain text or a JSON envelope. | extractPublicKey 从纯文本或 JSON 包装中取出公钥。
  function extractPublicKey(text) {
    var trimmed = typeof text === 'string' ? text.trim() : '';
    if (!trimmed) {
      return null;
    }
    if (trimmed.charAt(0) === '{' || trimmed.charAt(0) === '"') {
      var parsed;
      try {
        parsed = JSON.parse(trimmed);
      } catch (err) {
        return null;
      }
      if (typeof parsed === 'string') {
        return parsed;
      }
      if (parsed && typeof parsed === 'object') {
        if (typeof parsed.publicKey === 'string') {
          return parsed.publicKey;
        }
        if (parsed.data && typeof parsed.data.publicKey === 'string') {
          return parsed.data.publicKey;
        }
      }
      return null;
    }
    return trimmed;
  }

  // encryptValue encrypts one field value into standard Base64. | encryptValue 把一个字段值加密为标准 Base64。
  function encryptValue(sandbox, publicKeyHex, value) {
    var ciphertext = sandbox.sm2.encrypt(publicKeyHex, value);
    return sandbox.util.bytesToBase64(ciphertext);
  }

  // fieldName reads the (possibly absent) name attribute of a form control. | fieldName 读取表单控件的 name 属性（可能不存在）。
  function fieldName(field) {
    if (typeof field.name === 'string' && field.name) {
      return field.name;
    }
    if (typeof field.getAttribute === 'function') {
      var attribute = field.getAttribute('name');
      if (attribute) {
        return attribute;
      }
    }
    return '';
  }

  // fieldValue reads the current value of a form control. | fieldValue 读取表单控件的当前值。
  function fieldValue(field) {
    if (typeof field.value === 'string') {
      return field.value;
    }
    if (typeof field.getAttribute === 'function') {
      var attribute = field.getAttribute('value');
      return attribute === null || attribute === undefined ? '' : String(attribute);
    }
    return '';
  }

  // requestBody returns the outgoing body as a FormData instance, if there is one. | requestBody 以 FormData 返回待发送请求体，不存在时返回 null。
  function requestBody(request) {
    var body = request && request.body;
    if (!body) {
      return null;
    }
    // URLSearchParams based bodies are supported too, so htmx versions that pre-encode do not
    // silently bypass encryption. | 同时支持基于 URLSearchParams 的请求体，避免某些 htmx 版本提前编码后静默跳过加密。
    if (typeof FormData === 'function' && body instanceof FormData) {
      return body;
    }
    if (typeof URLSearchParams === 'function' && body instanceof URLSearchParams) {
      return body;
    }
    if (typeof body.set === 'function' && typeof body.get === 'function') {
      return body;
    }
    return null;
  }

  // encryptForm encrypts every sensitive field of a form in place and returns the count. | encryptForm 就地加密表单内全部敏感字段并返回条数。
  function encryptForm(sandbox, form, publicKeyHex, body) {
    if (!form || typeof form.querySelectorAll !== 'function' || !body) {
      return 0;
    }
    var options = state.options || DEFAULTS;
    var fields = form.querySelectorAll(options.sensitiveSelector);
    var encrypted = 0;
    for (var i = 0; i < fields.length; i++) {
      var field = fields[i];
      var name = fieldName(field);
      var value = fieldValue(field);
      if (!name || !value) {
        continue;
      }
      body.set(name, encryptValue(sandbox, publicKeyHex, value));
      encrypted++;
    }
    return encrypted;
  }

  // formValues renders a body back into the plain object htmx.ajax expects on a retry. | formValues 把请求体还原为 htmx.ajax 重试所需的普通对象。
  function formValues(body) {
    var values = {};
    if (!body || typeof body.forEach !== 'function') {
      return values;
    }
    body.forEach(function (value, name) {
      values[name] = value;
    });
    return values;
  }

  // retryWithHtmx re-issues a cancelled request once the public key is available. | retryWithHtmx 在取得公钥后重新发起被取消的请求。
  function retryWithHtmx(form, request, body) {
    var htmx = getHtmx();
    if (!htmx || typeof htmx.ajax !== 'function') {
      throw hookError('htmx-missing', 'htmx.ajax is unavailable, cannot resend the encrypted request', '当前没有 htmx.ajax，无法重发加密请求');
    }
    var context = {
      source: form,
      values: formValues(body),
    };
    if (typeof form.getAttribute === 'function') {
      context.target = form.getAttribute('hx-target') || form.getAttribute('data-hx-target') || undefined;
      context.swap = form.getAttribute('hx-swap') || form.getAttribute('data-hx-swap') || undefined;
    }
    var method = (request.method || 'post').toUpperCase();
    htmx.ajax(method, request.action, context);
  }

  // getHtmx returns the global htmx object, or null. | getHtmx 返回全局 htmx 对象，缺失时返回 null。
  function getHtmx() {
    if (typeof globalThis !== 'undefined' && globalThis.htmx) {
      return globalThis.htmx;
    }
    return null;
  }

  // handleConfigRequest is the htmx:config:request handler that encrypts sensitive fields. | handleConfigRequest 是加密敏感字段的 htmx:config:request 处理器。
  //
  // It is exported so applications can call it directly when they drive htmx programmatically. | 该函数对外导出，便于应用在程序化驱动 htmx 时直接调用。
  function handleConfigRequest(evt) {
    var sandbox = requireCore();
    var options = state.options || DEFAULTS;
    var ctx = evt && evt.detail ? evt.detail.ctx : null;
    var request = ctx && ctx.request ? ctx.request : null;
    if (!request) {
      return;
    }
    var form = request.form || request.sourceElement || null;
    if (!form || typeof form.getAttribute !== 'function') {
      return;
    }
    var mode = form.getAttribute(options.encryptAttribute);
    if (!mode) {
      // No opt-in attribute: the form is deliberately sent in clear text. | 未声明选择加入的属性：该表单按明文发送。
      return;
    }
    if (String(mode).toLowerCase() !== options.mode) {
      cancel(evt);
      warn('unsupported ' + options.encryptAttribute + '="' + mode + '"; only "' + options.mode + '" is implemented, request cancelled | 不支持的 ' + options.encryptAttribute + '="' + mode + '"，当前仅实现 "' + options.mode + '"，请求已取消');
      return;
    }

    var body = requestBody(request);
    if (!body) {
      cancel(evt);
      warn('the request body is not a FormData or URLSearchParams instance, request cancelled | 请求体既不是 FormData 也不是 URLSearchParams，请求已取消');
      return;
    }

    var url = form.getAttribute(options.pubkeyAttribute) || '';
    var publicKeyHex = cachedPublicKey(url) || (url ? null : options.publicKey);
    if (!publicKeyHex) {
      // htmx runs this handler synchronously, so the key must already be cached; when it is not,
      // the request is cancelled, the key is fetched and htmx resends it encrypted. | htmx 同步执行本处理器，
      // 因此公钥必须已缓存；若尚未缓存则取消本次请求，取回公钥后再由 htmx 以加密方式重发。
      cancel(evt);
      publicKeyFor(url).then(
        function () {
          retryWithHtmx(form, request, body);
        },
        function (err) {
          warn('public key for ' + (url || '(default)') + ' unavailable, request cancelled: ' + (err && err.message) + ' | 公钥 ' + (url || '(默认)') + ' 不可用，请求已取消：' + (err && err.message));
        }
      );
      return;
    }

    encryptForm(sandbox, form, publicKeyHex, body);
    request.headers = request.headers || {};
    request.headers[options.headerName] = options.headerValue;
  }

  // cancel stops the current request, preferring the event API over the request state. | cancel 中止当前请求，优先使用事件 API 而非请求状态。
  function cancel(evt) {
    if (!evt) {
      return;
    }
    if (typeof evt.preventDefault === 'function') {
      evt.preventDefault();
    }
    if (typeof evt.stopPropagation === 'function') {
      evt.stopPropagation();
    }
  }

  // prefetchPublicKeys warms the cache for every form that declares a key URL. | prefetchPublicKeys 为所有声明了公钥地址的表单预热缓存。
  function prefetchPublicKeys(scope) {
    var options = state.options || DEFAULTS;
    if (!options.prefetch) {
      return;
    }
    var container = scope && typeof scope.querySelectorAll === 'function' ? scope : target();
    if (!container || typeof container.querySelectorAll !== 'function') {
      return;
    }
    var forms = container.querySelectorAll(options.pubkeySelector);
    for (var i = 0; i < forms.length; i++) {
      var form = forms[i];
      var url = typeof form.getAttribute === 'function' ? form.getAttribute(options.pubkeyAttribute) : null;
      if (url && !cachedPublicKey(url)) {
        publicKeyFor(url).catch(function (err) {
          warn('prefetch failed: ' + (err && err.message) + ' | 预取公钥失败：' + (err && err.message));
        });
      }
    }
  }

  // target returns the event target, defaulting to the global document. | target 返回事件目标，默认为全局 document。
  function target() {
    if (state.target) {
      return state.target;
    }
    if (typeof document !== 'undefined') {
      return document;
    }
    return null;
  }

  // attach registers one listener and remembers it for uninstall. | attach 注册一个监听器并记录以便卸载。
  function attach(name, handler) {
    var entry = target();
    if (!entry || typeof entry.addEventListener !== 'function') {
      return;
    }
    entry.addEventListener(name, handler);
    state.listeners.push({ name: name, handler: handler });
  }

  // onLoad refreshes the prefetch cache whenever htmx swaps content in. | onLoad 在 htmx 换入内容后刷新预取缓存。
  function onLoad(evt) {
    var scope = evt && evt.detail && evt.detail.elt ? evt.detail.elt : null;
    prefetchPublicKeys(scope);
  }

  // install wires the hook into htmx and is safe to call more than once. | install 把钩子接入 htmx，可安全地重复调用。
  function install(options) {
    requireCore();
    uninstall();
    state.options = resolveOptions(options);
    if (state.options.eventTarget) {
      state.target = state.options.eventTarget;
    }
    if (typeof document !== 'undefined' && !state.target) {
      state.target = document;
    }
    attach(state.options.configEvent, handleConfigRequest);
    for (var i = 0; i < state.options.loadEvents.length; i++) {
      attach(state.options.loadEvents[i], onLoad);
    }
    prefetchPublicKeys(null);
    return api;
  }

  // uninstall removes every listener registered by install. | uninstall 移除 install 注册的全部监听器。
  function uninstall() {
    var entry = target();
    if (entry && typeof entry.removeEventListener === 'function') {
      for (var i = 0; i < state.listeners.length; i++) {
        entry.removeEventListener(state.listeners[i].name, state.listeners[i].handler);
      }
    }
    state.listeners = [];
    return api;
  }

  // verifyIntegrity fetches a URL and compares its SM3 digest with the expected hex value. | verifyIntegrity 取回 URL 并以期望的十六进制值比对 SM3 摘要。
  //
  // The expectation is verified synchronously, so a malformed digest throws before any network
  // access; the returned promise resolves to true or false. | 期望值同步校验，因此摘要格式非法时会在联网前抛出；
  // 返回的 Promise 解析为 true 或 false。
  function verifyIntegrity(url, expectSm3Hex) {
    var sandbox = requireCore();
    var expected = typeof expectSm3Hex === 'string' ? expectSm3Hex.trim().toLowerCase().replace(/^0[xX]/, '') : '';
    if (!/^[0-9a-f]{64}$/.test(expected)) {
      throw hookError('invalid-hex', 'the expected digest must be 64 hex characters', '期望摘要必须是 64 个十六进制字符');
    }
    if (typeof fetch !== 'function') {
      return Promise.reject(hookError('fetch-failed', 'fetch is unavailable in this environment', '当前环境没有 fetch'));
    }
    return fetch(url, { credentials: 'same-origin' })
      .then(function (response) {
        if (!response || !response.ok) {
          throw hookError('fetch-failed', 'request to ' + url + ' failed with status ' + (response && response.status), '请求 ' + url + ' 失败，状态码 ' + (response && response.status));
        }
        return response.arrayBuffer();
      })
      .then(function (buffer) {
        var actual = sandbox.sm3Hex(new Uint8Array(buffer));
        return sandbox.util.bytesEqual(sandbox.util.hexToBytes(actual), sandbox.util.hexToBytes(expected));
      })
      .catch(function (err) {
        if (err && err.code === 'fetch-failed') {
          throw err;
        }
        throw hookError('fetch-failed', 'integrity check of ' + url + ' failed: ' + (err && err.message), url + ' 的完整性校验失败：' + (err && err.message));
      });
  }

  // Attach the integrity helper to the sandbox, so applications only import one namespace. | 把完整性辅助函数挂到沙箱上，使应用只需引入一个命名空间。
  if (core && typeof core === 'object') {
    core.verifyIntegrity = verifyIntegrity;
  }

  // api is the object exposed as `window.ydGuomiHtmx` / `module.exports`. | api 即暴露为 `window.ydGuomiHtmx` / `module.exports` 的对象。
  var api = {
    // install wires the hook, optionally overriding the documented defaults. | install 接入钩子，可用可选配置覆盖文档化的默认值。
    install: install,

    // uninstall removes the listeners registered by install. | uninstall 移除 install 注册的监听器。
    uninstall: uninstall,

    // handleConfigRequest is the raw htmx:config:request handler. | handleConfigRequest 是原始的 htmx:config:request 处理器。
    handleConfigRequest: handleConfigRequest,

    // setPublicKey caches a public key for a URL. | setPublicKey 为某个地址缓存公钥。
    setPublicKey: setPublicKey,

    // publicKeyFor resolves to the cached or fetched public key of a URL. | publicKeyFor 解析为某个地址已缓存或已获取的公钥。
    publicKeyFor: publicKeyFor,

    // verifyIntegrity compares the SM3 digest of a fetched asset with an expected hex value. | verifyIntegrity 比对取出资产的 SM3 摘要与期望十六进制值。
    verifyIntegrity: verifyIntegrity,

    // prefetch warms the public key cache for the given scope. | prefetch 为给定范围预热公钥缓存。
    prefetch: prefetchPublicKeys,

    // defaults exposes the documented default options as a frozen copy. | defaults 以冻结副本暴露文档化的默认配置。
    defaults: Object.freeze(resolveOptions(null)),

    // state reports the cached public keys, which is useful in diagnostics. | state 汇报已缓存的公钥，便于诊断。
    cachedKeys: function () {
      var out = {};
      for (var key in state.keys) {
        if (Object.prototype.hasOwnProperty.call(state.keys, key)) {
          out[key] = state.keys[key];
        }
      }
      return out;
    },
  };

  return api;
});

/*!
 * yd-i18n.js — client-side internationalisation bound to htmx 4 swaps.
 * yd-i18n.js — 与 htmx 4 交换绑定的前端国际化。
 *
 * Public surface on window.ydI18n | 暴露在 window.ydI18n 上的接口:
 *   init({lang, fallback, catalogs})  initialise from an envelope, or read the injected catalog
 *                                     | 从信封初始化，或读取服务端注入的目录
 *   t(key, args)                      translate, falling back to the fallback catalog then to key
 *                                     | 翻译，缺失时回退 fallback 语言，再回退到 key 本身
 *   t.n(value, opts)                  format a number or a date with Intl | 用 Intl 格式化数字或日期
 *   setLang(lang)                     switch, persist to localStorage + yd_lang cookie + <html lang>,
 *                                     then refetch #yd-workspace through htmx
 *                                     | 切换语言，并持久化到 localStorage、yd_lang cookie 与 <html lang>，
 *                                       随后经 htmx 重新取回 #yd-workspace
 *   current()                         the active language tag | 当前语言标签
 *   on(fn)                            subscribe to language changes, returns an unsubscribe function
 *                                     | 订阅语言变化，返回取消订阅函数
 *   bind(root, force)                 re-apply the declarative bindings under root | 重新应用 root 下的声明式绑定
 *
 * Declarative bindings | 声明式绑定:
 *   <span data-yd-i18n="user.title"></span>                text content | 文本内容
 *   <input data-yd-i18n-attr="placeholder:user.search">    attributes, comma separated, "attr:key"
 *                                                          | 属性绑定，逗号分隔，"attr:key"
 *   <span data-yd-i18n="k" data-yd-i18n-args='{"n":1}'>    interpolation arguments as JSON
 *                                                          | 以 JSON 给出的插值参数
 *   <button data-yd-lang="zh-CN">                           language switch, aria-pressed is kept in sync
 *                                                          | 语言切换按钮，会同步 aria-pressed
 *
 * Hard rules | 硬规则:
 *   1. The catalog is injected by the server, so nothing is ever fetched over the network.
 *      目录由服务端注入，因此不会发起任何网络请求。
 *   2. htmx:after:swap only translates new nodes: every node keeps a signature, and unchanged
 *      nodes are skipped, so morph swaps never duplicate work or fight the user's edits.
 *      htmx:after:swap 只翻译新节点：每个节点记录签名，未变化的节点直接跳过，
 *      因此 morph 交换不会重复劳动，也不会覆盖用户的修改。
 *   3. No dependency, no build step: plain ES5-compatible UMD that also runs under node:test.
 *      无依赖、无构建：纯 UMD，同时可在 node:test 下运行。
 *   4. A switch is immediate *and* server-accurate: the in-place rewrite below repaints the page
 *      with the catalog already in memory (no blank frame), and setLang then asks the server for
 *      the current URL again and swaps the answer into #yd-workspace, so every server-rendered
 *      string — pages, partials, flash messages — follows the new language too. Standalone pages
 *      without a #yd-workspace (the sign-in card) keep the in-place rewrite only.
 *      切换既即时又与服务端一致：下面的就地重写用已在内存中的目录重绘页面（不出现空白帧），
 *      随后 setLang 以当前 URL 向服务端重新取回片段并换入 #yd-workspace，
 *      使每一处服务端渲染的文案（页面、局部片段、闪现消息）同样跟随新语言。
 *      没有 #yd-workspace 的独立页面（登录卡片）只做就地重写。
 */
(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) {
    // Node / bundlers | Node 与打包器
    module.exports = factory();
  } else {
    // Browser globals | 浏览器全局
    root.ydI18n = factory();
  }
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  var STORAGE_KEY = 'yd_lang'; // localStorage key | localStorage 键名
  var COOKIE_KEY = 'yd_lang'; // cookie name read by the server | 服务端读取的 cookie 名
  var CATALOG_ID = 'yd-i18n-catalog'; // id of the injected envelope | 注入信封的 id
  var BIND_SELECTOR = '[data-yd-i18n],[data-yd-i18n-attr],[data-yd-lang]';
  var WORKSPACE_SELECTOR = '#yd-workspace'; // the region the server re-renders on a language switch | 语言切换时由服务端重新渲染的区域
  var ONE_YEAR = 31536000; // cookie max-age in seconds | cookie 有效期（秒）

  // state holds the resolved language, the catalogs and the subscribers.
  // state 保存已解析的语言、目录与订阅者。
  var state = {
    lang: '',
    fallback: '',
    catalogs: {},
    listeners: [],
    boundDoc: null
  };

  // env returns the global object, which keeps the module usable in node:test.
  // env 返回全局对象，使模块在 node:test 下同样可用。
  function env() {
    return typeof globalThis !== 'undefined' ? globalThis : {};
  }

  // doc returns the document when one exists, otherwise null.
  // doc 在存在文档时返回它，否则返回 null。
  function doc() {
    var e = env();
    return e.document || null;
  }

  function text(value) {
    return value === undefined || value === null ? '' : String(value);
  }

  function lower(value) {
    return text(value).toLowerCase();
  }

  // getAttr reads an attribute defensively so a minimal DOM stub keeps working.
  // getAttr 防御式读取属性，使最小化的 DOM 桩也能工作。
  function getAttr(el, name) {
    if (!el || typeof el.getAttribute !== 'function') {
      return null;
    }
    var value = el.getAttribute(name);
    return value === undefined || value === null ? null : value;
  }

  // tryParse returns the parsed value, or null when the text is not JSON at all.
  // tryParse 返回解析结果；文本完全不是 JSON 时返回 null。
  function tryParse(raw) {
    try {
      return JSON.parse(raw);
    } catch (err) {
      return null;
    }
  }

  // parseJSON parses the injected envelope and survives the two shapes html/template can produce:
  //   1. a raw JSON object, when the handler passes template.JS (the supported contract);
  //   2. a quoted JSON *string*, because html/template treats <script type="application/json"> as a
  //      JavaScript container and json.Marshal's every other type, template.HTML included.
  // Entity escaping (a text-context injection) is undone as a last resort.
  // parseJSON 解析注入的信封，并兼容 html/template 可能产生的两种形态：
  //   1. 原始 JSON 对象——处理函数传 template.JS 时（受支持的契约）；
  //   2. 带引号的 JSON *字符串*——因为 html/template 把 <script type="application/json">
  //      当作 JavaScript 容器，对包括 template.HTML 在内的其它类型一律 json.Marshal；
  // 最后再兜底还原一次实体转义（文本上下文注入时的产物）。
  function parseJSON(raw) {
    if (!raw) {
      return null;
    }
    var value = tryParse(raw);
    if (value === null) {
      var unescaped = text(raw)
        .replace(/&(?:quot|#34);/g, '"')
        .replace(/&(?:apos|#39);/g, "'")
        .replace(/&lt;/g, '<')
        .replace(/&gt;/g, '>')
        .replace(/&amp;/g, '&');
      value = tryParse(unescaped);
    }
    if (typeof value === 'string') {
      var inner = tryParse(value);
      if (inner !== null) {
        return inner;
      }
    }
    return value;
  }

  // readInjected reads and parses <script id="yd-i18n-catalog" type="application/json">.
  // readInjected 读取并解析 <script id="yd-i18n-catalog" type="application/json">。
  function readInjected(d) {
    if (!d || typeof d.getElementById !== 'function') {
      return null;
    }
    var el = d.getElementById(CATALOG_ID);
    if (!el) {
      return null;
    }
    return parseJSON(text(el.textContent));
  }

  // normalizeInjected accepts either the full envelope {"lang","fallback","catalogs"} or a bare
  // catalog object, which is then treated as the catalog of options.lang.
  // normalizeInjected 既接受完整信封 {"lang","fallback","catalogs"}，也接受裸目录对象，
  // 后者会被当作 options.lang 的目录。
  function normalizeInjected(injected, options) {
    var out = { lang: '', fallback: '', catalogs: {} };
    if (!injected || typeof injected !== 'object') {
      return out;
    }
    if (injected.catalogs && typeof injected.catalogs === 'object') {
      out.catalogs = injected.catalogs;
      out.lang = text(injected.lang);
      out.fallback = text(injected.fallback);
      return out;
    }
    var lang = text(options && options.lang) || 'default';
    out.catalogs[lang] = injected;
    out.lang = lang === 'default' ? '' : lang;
    return out;
  }

  // catalogKeys returns the language tags that actually carry a catalog.
  // catalogKeys 返回真正带有目录的语言标签。
  function catalogKeys() {
    return Object.keys(state.catalogs || {});
  }

  // resolveKey maps a language tag onto a catalog key, ignoring case and separators.
  // resolveKey 把语言标签映射到目录键，忽略大小写与分隔符差异。
  function resolveKey(lang) {
    var want = lower(lang);
    if (!want) {
      return '';
    }
    var keys = catalogKeys();
    for (var i = 0; i < keys.length; i++) {
      if (lower(keys[i]) === want) {
        return keys[i];
      }
    }
    var base = want.split('-')[0];
    for (var j = 0; j < keys.length; j++) {
      if (lower(keys[j]).split('-')[0] === base) {
        return keys[j];
      }
    }
    return '';
  }

  function hasCatalog(lang) {
    return resolveKey(lang) !== '';
  }

  function catalogFor(lang) {
    var key = resolveKey(lang);
    return key ? state.catalogs[key] : null;
  }

  // drill walks a dotted key inside a catalog, trying the exact segment first and its lower-case
  // form second, mirroring the case-insensitive lookup of the Go i18n bundle.
  // drill 在目录里按点分键逐层深入，先精确匹配再尝试小写，
  // 与 Go i18n 包大小写不敏感的查找保持一致。
  function drill(catalog, key) {
    if (!catalog || typeof catalog !== 'object' || !key) {
      return undefined;
    }
    var parts = text(key).split('.');
    var node = catalog;
    for (var i = 0; i < parts.length; i++) {
      if (node === null || typeof node !== 'object') {
        return undefined;
      }
      var part = parts[i];
      var next = Object.prototype.hasOwnProperty.call(node, part) ? node[part] : node[lower(part)];
      if (next === undefined) {
        return undefined;
      }
      node = next;
    }
    return node;
  }

  // interpolate fills named placeholders ({{.name}} and {name}) and positional verbs (%s/%d).
  // interpolate 填充具名占位符（{{.name}} 与 {name}）以及位置动词（%s/%d）。
  function interpolate(message, args) {
    var out = text(message);
    if (!args || !out) {
      return out;
    }
    if (Array.isArray(args)) {
      var index = 0;
      out = out.replace(/\{\{\s*\.?([\w.-]+)\s*\}\}|\{([\w.-]+)\}|%[sd]/g, function (match, namedA, namedB) {
        var name = namedA || namedB;
        if (name !== undefined) {
          return Object.prototype.hasOwnProperty.call(args, name) ? text(args[name]) : match;
        }
        var value = index < args.length ? args[index] : '';
        index++;
        return text(value);
      });
      return out;
    }
    if (typeof args === 'object') {
      return out.replace(/\{\{\s*\.?([\w.-]+)\s*\}\}|\{([\w.-]+)\}/g, function (match, namedA, namedB) {
        var name = namedA || namedB;
        return Object.prototype.hasOwnProperty.call(args, name) ? text(args[name]) : match;
      });
    }
    return out;
  }

  // parseArgs reads the optional JSON argument object of one node.
  // parseArgs 读取单个节点可选的 JSON 参数对象。
  function parseArgs(el) {
    var raw = getAttr(el, 'data-yd-i18n-args');
    if (!raw) {
      return null;
    }
    return parseJSON(raw);
  }

  // translate looks the key up in the active catalog, then in the fallback catalog, then returns
  // the key itself, which is what the Go bundle does on the server side.
  // translate 先在当前目录查找，再到 fallback 目录查找，最后返回 key 本身，
  // 与服务端 i18n 包的行为一致。
  function translate(key, args) {
    if (!key) {
      return '';
    }
    var value = drill(catalogFor(state.lang), key);
    if (value === undefined || value === null) {
      value = drill(catalogFor(state.fallback), key);
    }
    if (value === undefined || value === null) {
      return text(key);
    }
    return interpolate(value, args);
  }

  function intlRef() {
    var e = env();
    return e.Intl && typeof e.Intl.NumberFormat === 'function' ? e.Intl : null;
  }

  function asDate(value) {
    if (typeof Date === 'function' && value instanceof Date) {
      return value;
    }
    return new Date(value);
  }

  // formatValue backs t.n: dates and numbers are formatted with Intl in the active language.
  // formatValue 支撑 t.n：日期与数字都按当前语言用 Intl 格式化。
  function formatValue(value, opts) {
    var options = opts || {};
    var intl = intlRef();
    if (!intl) {
      return text(value);
    }
    var locale = state.lang || state.fallback || undefined;
    var kind = text(options.kind);
    var isDate = (typeof Date === 'function' && value instanceof Date) ||
      kind === 'date' || kind === 'time' || kind === 'datetime' ||
      options.dateStyle !== undefined || options.timeStyle !== undefined;

    var pass = {};
    Object.keys(options).forEach(function (name) {
      if (name !== 'kind' && name !== 'currency') {
        pass[name] = options[name];
      }
    });

    if (isDate) {
      if (kind === 'date' && pass.dateStyle === undefined) {
        pass.dateStyle = 'medium';
      }
      if (kind === 'time' && pass.timeStyle === undefined) {
        pass.timeStyle = 'short';
      }
      if (kind === 'datetime' && pass.dateStyle === undefined && pass.timeStyle === undefined) {
        pass.dateStyle = 'medium';
        pass.timeStyle = 'short';
      }
      return new intl.DateTimeFormat(locale, pass).format(asDate(value));
    }
    if (options.currency) {
      pass.style = 'currency';
      pass.currency = options.currency;
    }
    return new intl.NumberFormat(locale, pass).format(Number(value));
  }

  // applyAttrSpec applies data-yd-i18n-attr="attr:key, attr2:key2".
  // applyAttrSpec 应用 data-yd-i18n-attr="attr:key, attr2:key2"。
  function applyAttrSpec(el, spec, force) {
    var parts = text(spec).split(',');
    var touched = false;
    for (var i = 0; i < parts.length; i++) {
      var pair = parts[i].trim();
      if (!pair) {
        continue;
      }
      var colon = pair.indexOf(':');
      if (colon < 0) {
        continue;
      }
      var name = pair.slice(0, colon).trim();
      var key = pair.slice(colon + 1).trim();
      if (!name || !key || typeof el.setAttribute !== 'function') {
        continue;
      }
      var signature = 'attr:' + name + '=' + key;
      if (!force && getAttr(el, 'data-yd-attr-signature-' + name) === signature) {
        continue;
      }
      el.setAttribute(name, translate(key, parseArgs(el)));
      el.setAttribute('data-yd-attr-signature-' + name, signature);
      touched = true;
    }
    return touched;
  }

  // applyNode translates one node and returns whether it changed anything; unchanged nodes are
  // skipped so an htmx swap only ever pays for what it added.
  // applyNode 翻译单个节点并返回是否发生变化；未变化的节点会被跳过，
  // 因此 htmx 交换只需要为新内容付出代价。
  function applyNode(el, force) {
    if (!el || el.nodeType === 3) {
      return false;
    }
    var key = getAttr(el, 'data-yd-i18n');
    var attrSpec = getAttr(el, 'data-yd-i18n-attr');
    var langSwitch = getAttr(el, 'data-yd-lang');
    if (key === null && attrSpec === null && langSwitch === null) {
      return false;
    }

    var signature = state.lang + '|' + text(key) + '|' + text(attrSpec) + '|' + text(langSwitch);
    if (!force && el.__ydI18nSignature === signature) {
      return false;
    }

    if (key) {
      var translated = translate(key, parseArgs(el));
      el.textContent = translated;
    }
    applyAttrSpec(el, attrSpec, force);

    if (langSwitch !== null && typeof el.setAttribute === 'function') {
      var active = lower(langSwitch) === lower(state.lang);
      el.setAttribute('aria-pressed', active ? 'true' : 'false');
      if (active) {
        el.setAttribute('data-yd-lang-active', 'true');
      } else if (typeof el.removeAttribute === 'function') {
        el.removeAttribute('data-yd-lang-active');
      }
    }

    el.__ydI18nSignature = signature;
    return true;
  }

  // collect gathers root itself plus every bound descendant.
  // collect 收集 root 自身以及所有带绑定的后代节点。
  function collect(root) {
    var nodes = [];
    if (!root) {
      return nodes;
    }
    if (typeof root.matches === 'function' && root.matches(BIND_SELECTOR)) {
      nodes.push(root);
    }
    if (typeof root.querySelectorAll === 'function') {
      var found = root.querySelectorAll(BIND_SELECTOR);
      for (var i = 0; i < found.length; i++) {
        nodes.push(found[i]);
      }
    }
    return nodes;
  }

  // bind re-applies the declarative bindings under root; it returns how many nodes changed.
  // bind 重新应用 root 下的声明式绑定，返回发生变化的节点数。
  function bind(root, force) {
    var target = root || doc();
    if (!target) {
      return 0;
    }
    var nodes = collect(target);
    var changed = 0;
    for (var i = 0; i < nodes.length; i++) {
      if (applyNode(nodes[i], force === true)) {
        changed++;
      }
    }
    return changed;
  }

  function applyDocumentLang(lang) {
    var d = doc();
    if (d && d.documentElement) {
      d.documentElement.lang = lang;
    }
  }

  function readCookie(name) {
    var d = doc();
    if (!d || typeof d.cookie !== 'string' || !d.cookie) {
      return '';
    }
    var parts = d.cookie.split(';');
    for (var i = 0; i < parts.length; i++) {
      var pair = parts[i].trim();
      var eq = pair.indexOf('=');
      if (eq < 0) {
        continue;
      }
      if (pair.slice(0, eq).trim() === name) {
        return decodeURIComponent(pair.slice(eq + 1).trim());
      }
    }
    return '';
  }

  function readStorage(name) {
    var e = env();
    try {
      if (e.localStorage && typeof e.localStorage.getItem === 'function') {
        return text(e.localStorage.getItem(name));
      }
    } catch (err) {
      return '';
    }
    return '';
  }

  function writeStorage(name, value) {
    var e = env();
    try {
      if (e.localStorage && typeof e.localStorage.setItem === 'function') {
        e.localStorage.setItem(name, value);
      }
    } catch (err) {
      /* storage may be unavailable in private mode | 隐私模式下存储可能不可用 */
    }
  }

  // writeCookie exports the choice to the server, which then renders the right catalog.
  // writeCookie 把选择导出给服务端，服务端据此渲染正确的目录。
  function writeCookie(name, value) {
    var d = doc();
    if (!d) {
      return;
    }
    try {
      d.cookie = name + '=' + encodeURIComponent(value) + '; path=/; max-age=' + ONE_YEAR + '; samesite=lax';
    } catch (err) {
      /* a sandboxed document may reject cookie writes | 沙箱文档可能拒绝写 cookie */
    }
  }

  // storedLang is the remembered preference: localStorage first, cookie second.
  // storedLang 是记住的偏好：先 localStorage，后 cookie。
  function storedLang() {
    var remembered = readStorage(STORAGE_KEY) || readCookie(COOKIE_KEY);
    return hasCatalog(remembered) ? remembered : '';
  }

  function persist(lang) {
    writeStorage(STORAGE_KEY, lang);
    writeCookie(COOKIE_KEY, lang);
  }

  function emit() {
    var payload = { lang: state.lang, fallback: state.fallback };
    for (var i = 0; i < state.listeners.length; i++) {
      try {
        state.listeners[i](payload);
      } catch (err) {
        /* a broken subscriber must not stop the others | 单个订阅者出错不影响其它订阅者 */
      }
    }
  }

  // swapRoot picks the element htmx just swapped; the whole document is scanned as well because
  // out-of-band fragments and <hx-partial> blocks can land far away from the target.
  // swapRoot 取 htmx 刚交换的元素；同时也会扫描整篇文档，
  // 因为带外片段与 <hx-partial> 区块可能落在离目标很远的位置。
  function onAfterSwap(event) {
    var detail = (event && event.detail) || {};
    var ctx = detail.ctx || {};
    var root = ctx.target || detail.target || ctx.elt || ctx.sourceElement || null;
    if (root && root.nodeType && root.nodeType !== 1) {
      root = null;
    }
    if (root) {
      bind(root);
    }
    bind(doc());
  }

  // listen registers the DOM level hook once per document; it is a no-op without one.
  // listen 为每个文档只注册一次 DOM 钩子；没有文档时为空操作。
  function listen() {
    var d = doc();
    if (!d || typeof d.addEventListener !== 'function' || state.boundDoc === d) {
      return;
    }
    d.addEventListener('htmx:after:swap', onAfterSwap);
    state.boundDoc = d;
  }

  // init resolves the catalogs and the active language, then translates the current DOM.
  // init 解析目录与当前语言，然后翻译当前 DOM。
  function init(options) {
    var opts = options || {};
    var injected = opts.catalogs ? null : readInjected(doc());
    var normalized = normalizeInjected(injected, opts);

    state.catalogs = opts.catalogs && typeof opts.catalogs === 'object' ? opts.catalogs : normalized.catalogs;

    var keys = catalogKeys();
    var lang = text(opts.lang) || normalized.lang;
    var fallback = text(opts.fallback) || normalized.fallback;
    var remembered = storedLang();

    if (!hasCatalog(lang) && keys.length === 1) {
      lang = keys[0];
    }
    if (remembered) {
      lang = remembered;
    }
    if (!lang) {
      lang = fallback || keys[0] || 'en';
    }
    if (!hasCatalog(fallback)) {
      fallback = hasCatalog(lang) ? lang : (keys[0] || lang);
    }

    state.lang = lang;
    state.fallback = fallback;

    applyDocumentLang(lang);
    listen();
    bind(doc(), true);
    emit();
    return api;
  }

  // refetchWorkspace asks htmx to fetch the current URL again and swap the answer into
  // #yd-workspace with innerHTML. The server sees HX-Request and answers with the workspace
  // fragment rendered in the language of the freshly written yd_lang cookie, which is how
  // server-side copy, flash messages and every server-rendered partial follow a hot switch
  // without a full page load.
  // History is explicitly left alone: the URL never changes, so a push would only add a
  // duplicate entry to the back stack. It returns false when there is nothing to refetch —
  // no htmx, or a standalone page such as the sign-in card that has no workspace at all.
  // refetchWorkspace 让 htmx 重新请求当前 URL，并把结果以 innerHTML 换入 #yd-workspace。
  // 服务端看到 HX-Request 后，会按刚写入的 yd_lang cookie 渲染工作区片段，
  // 服务端文案、闪现消息以及每一处服务端渲染的局部片段因此都能跟随热切换，而无需整页加载。
  // 历史记录被显式放过：URL 从未变化，push 只会给后退栈增加一条重复记录。
  // 无 htmx、或登录卡片这类没有工作区的独立页面会返回 false（无事可做）。
  function refetchWorkspace() {
    var e = env();
    var api = e.htmx;
    var d = doc();
    if (!api || typeof api.ajax !== 'function' || !d || typeof d.querySelector !== 'function') {
      return false;
    }
    if (!d.querySelector(WORKSPACE_SELECTOR)) {
      return false;
    }
    var location = d.location || {};
    var url = text(location.pathname) || '/';
    if (location.search) {
      url += text(location.search);
    }
    api.ajax('GET', url, {
      target: WORKSPACE_SELECTOR,
      swap: 'innerHTML',
      push: false
    });
    return true;
  }

  // setLang switches the language: it persists the choice, updates <html lang>, rewrites the page
  // in place so the switch is visible immediately, and finally refetches the workspace so the
  // server-rendered copy agrees with the new language. The page itself never reloads.
  // setLang 切换语言：持久化选择、更新 <html lang>、就地重写页面使切换立刻可见，
  // 最后重新取回工作区，使服务端渲染的文案与新语言一致。页面本身从不刷新。
  function setLang(lang) {
    var next = text(lang);
    if (!next) {
      return state.lang;
    }
    state.lang = next;
    persist(next);
    applyDocumentLang(next);
    bind(doc(), true);
    emit();
    refetchWorkspace();
    return next;
  }

  var api = {
    init: init,
    setLang: setLang,
    current: function () {
      return state.lang;
    },
    fallback: function () {
      return state.fallback;
    },
    // on subscribes to language changes and returns the unsubscribe function.
    // on 订阅语言变化，返回取消订阅函数。
    on: function (fn) {
      if (typeof fn !== 'function') {
        return function () {};
      }
      state.listeners.push(fn);
      return function off() {
        var index = state.listeners.indexOf(fn);
        if (index >= 0) {
          state.listeners.splice(index, 1);
        }
      };
    },
    bind: bind,
    // refetch re-reads the workspace from the server in the active language.
    // refetch 以当前语言从服务端重新取回工作区。
    refetch: refetchWorkspace,
    available: catalogKeys,
    // t is the translation function; t.n formats numbers and dates.
    // t 是翻译函数；t.n 负责数字与日期格式化。
    t: translate
  };
  api.t.n = formatValue;

  return api;
});

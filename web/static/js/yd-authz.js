/*!
 * yd-authz.js — the browser side of the capability registry: names, decisions and declarative
 * guards. | yd-authz.js —— 能力注册表的浏览器端：名字、判定与声明式守卫。
 *
 * Public surface on window.yd | 暴露在 window.yd 上的接口:
 *   yd.PERM            capability script key -> token | 能力脚本键 -> 标识
 *   yd.ROLE            role script key -> name      | 角色脚本键 -> 名字
 *   yd.can(ref)        does the subject hold this capability | 主体是否持有该能力
 *   yd.hasRole(ref)    does the subject hold this role       | 主体是否持有该角色
 *   yd.perms()         the capabilities held, sorted         | 持有的能力列表，已排序
 *   yd.roles()         the roles held, sorted                | 持有的角色列表，已排序
 *   yd.subject()       a snapshot of the whole payload       | 整份载荷的快照
 *   yd.bind(root)      re-apply the declarative guards under root | 重新应用 root 之下的声明式守卫
 *
 * Declarative guards | 声明式守卫:
 *   <button data-yd-perm="{{ .Perm.UserCreate }}">              hidden while not held | 未持有时隐藏
 *   <button data-yd-perm="PermUserCreate" data-yd-perm-mode="disable">
 *                                                               greyed out instead of hidden | 变灰而不隐藏
 *   <tr data-yd-role="RoleAdmin" data-yd-role-mode="disable">    the same for a role | 角色同理
 *
 * A reference may be written in any of the four spellings the registry owns - the token
 * (user:create), the Go constant (PermUserCreate), the template data key (UserCreate) or the script
 * key (USER_CREATE) - because the Go side accepts all four, and a script that accepted fewer would
 * force every reader to know which language they are standing in. | 引用可以写成注册表拥有的四种写法
 * 中的任何一种——标识（user:create）、Go 常量（PermUserCreate）、模板数据键（UserCreate）或脚本键
 * （USER_CREATE）——因为 Go 侧四种全收，而少收一种的脚本会逼着每个读者先弄清自己站在哪种语言里。
 *
 * Hard rules | 硬规则:
 *   1. A FRONT-END CHECK IS NOT A SECURITY BOUNDARY. Everything here exists so that a page does not
 *      offer an action the server would refuse. Whoever receives the page can call the endpoint
 *      behind a hidden button directly, so every protected route still carries its own server-side
 *      guard (authz.Require / authz.Allow). Hiding a button without guarding its route is the defect
 *      this file cannot prevent, and these comments exist to name it.
 *      前端判定不是安全边界。这里的一切都只是为了让页面不提供「服务端本来就会拒绝」的操作。
 *      拿到页面的人可以直接调用隐藏按钮背后的端点，因此每个受保护路由仍然必须有自己的服务端守卫
 *      （authz.Require / authz.Allow）。只隐藏按钮而不守卫路由，是本文件无法阻止、只能在此点名的缺陷。
 *   2. The payload is injected by the server into <script id="yd-authz" type="application/json">
 *      (authz.AuthzScriptID), so nothing is fetched over the network: zero extra requests, and the
 *      page still works offline. | 载荷由服务端注入到该脚本元素中（authz.AuthzScriptID），
 *      因此不发起任何网络请求：零额外请求，页面离线也能工作。
 *   3. Fail closed. A missing, empty or malformed payload grants nothing, so a broken injection
 *      hides the guarded controls instead of opening them. | 失败关闭。载荷缺失、为空或格式错误时
 *      不授予任何能力，因此注入坏掉时受守卫的控件会被隐藏，而不是被放开。
 *   4. No dependency, no build step: plain ES5-compatible UMD that also runs under node:test.
 *      无依赖、无构建：纯 UMD，同时可在 node:test 下运行。
 */
(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) {
    // Node / bundlers | Node 与打包器
    module.exports = factory();
  } else {
    // Browser global | 浏览器全局
    root.ydAuthz = factory();
  }
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  // PAYLOAD_ID is the id of the injected element, equal to authz.AuthzScriptID. | PAYLOAD_ID 是注入元素的 id，与 authz.AuthzScriptID 相同。
  var PAYLOAD_ID = 'yd-authz';

  // GUARD_SELECTOR matches every element carrying either declarative guard. | GUARD_SELECTOR 匹配带有任一声明式守卫的所有元素。
  var GUARD_SELECTOR = '[data-yd-perm],[data-yd-role]';

  // The two guard modes, and the two markers this script leaves behind. The markers are what let a
  // later bind tell "the server hid this" from "we hid this", which is the difference between
  // restoring a control and overriding the server. | 两种守卫模式，以及本脚本留下的两个标记。
  // 正是这两个标记让之后的绑定能区分「服务端隐藏了它」与「我们隐藏了它」，
  // 也就是「恢复控件」与「覆盖服务端」的区别。
  var MODE_HIDE = 'hide';
  var MODE_DISABLE = 'disable';
  var STATE_ATTR = 'data-yd-authz-state';
  var PREV_ATTR = 'data-yd-authz-prev';

  // state holds the resolved payload: the two name maps, the held sets, and one lookup per kind that
  // accepts every spelling of an entry. | state 保存解析后的载荷：两张名字表、持有集合，
  // 以及每种条目一张接受其全部写法的查找表。
  var state = {
    perms: {}, // script key -> token | 脚本键 -> 标识
    roles: {}, // script key -> name | 脚本键 -> 名字
    granted: [], // tokens held | 持有的标识
    mine: [], // roles held | 持有的角色
    held: {}, // token -> true | 标识 -> true
    owned: {}, // role name -> true | 角色名 -> true
    permBySpelling: {}, // any spelling -> token | 任意写法 -> 标识
    roleBySpelling: {}, // any spelling -> role name | 任意写法 -> 角色名
    ready: false, // true once a payload was read | 读到载荷后为 true
    boundDoc: null // the document the swap hook is registered on | 已注册交换钩子的文档
  };

  // doc returns the document, or null outside a browser. | doc 返回 document，非浏览器环境返回 null。
  function doc() {
    return typeof document !== 'undefined' ? document : null;
  }

  // text normalises any value into a trimmed string; null and undefined become ''. | text 把任意值归一化为去空白字符串；null 与 undefined 变成 ''。
  function text(value) {
    if (value === null || value === undefined) {
      return '';
    }
    return String(value).trim();
  }

  // camel turns a script key back into a template data key: USER_CREATE -> UserCreate. The Go side
  // derives the same key from the token (authz.DataKey), so the two agree without asking anybody.
  // camel 把脚本键还原为模板数据键：USER_CREATE -> UserCreate。Go 侧按同一规则由标识派生该键
  // （authz.DataKey），因此两者无需询问任何人就能对上。
  function camel(key) {
    var parts = text(key).toLowerCase().split('_');
    var out = '';
    for (var i = 0; i < parts.length; i++) {
      if (!parts[i]) {
        continue;
      }
      out += parts[i].charAt(0).toUpperCase() + parts[i].slice(1);
    }
    return out;
  }

  // index records every spelling of one entry, so a reference may arrive in any of them.
  // index 记录一条条目的每种写法，使引用可以以其中任意一种抵达。
  function index(map, prefix, scriptKey, value) {
    var data = camel(scriptKey);
    map[text(value)] = value; // the token or the role name itself | 标识或角色名本身
    map[text(scriptKey)] = value; // USER_CREATE
    map[data] = value; // UserCreate
    map[prefix + data] = value; // PermUserCreate / RoleAdmin
  }

  // empty removes every own key of a map in place. The two name maps keep their identity across a
  // re-read, because they are handed to the page as yd.PERM and yd.ROLE: replacing the object would
  // leave an application holding a stale map that no longer answers. | empty 就地移除映射的全部自有键。
  // 两张名字表在重读之间保持同一身份，因为它们是以 yd.PERM 与 yd.ROLE 交给页面的：
  // 替换整个对象会让应用握住一张再也不更新的旧表。
  function empty(map) {
    for (var key in map) {
      if (Object.prototype.hasOwnProperty.call(map, key)) {
        delete map[key];
      }
    }
    return map;
  }

  // reset clears every table, so a re-read cannot leave a stale grant behind. | reset 清空全部表，使重读不会留下过期的授权。
  function reset() {
    empty(state.perms);
    empty(state.roles);
    empty(state.held);
    empty(state.owned);
    empty(state.permBySpelling);
    empty(state.roleBySpelling);
    state.granted = [];
    state.mine = [];
    state.ready = false;
  }

  // apply parses one payload object. Anything malformed is treated as empty, which is the
  // fail-closed direction: the page loses its buttons rather than offering forbidden ones.
  // apply 解析一份载荷对象。任何畸形内容都按空处理，这就是失败关闭的方向：
  // 页面失去按钮，而不是提供被禁止的按钮。
  function apply(payload) {
    reset();
    if (!payload || typeof payload !== 'object') {
      return state;
    }
    var key;
    if (payload.perms && typeof payload.perms === 'object') {
      for (key in payload.perms) {
        if (Object.prototype.hasOwnProperty.call(payload.perms, key)) {
          state.perms[key] = text(payload.perms[key]);
          index(state.permBySpelling, 'Perm', key, state.perms[key]);
        }
      }
    }
    if (payload.roles && typeof payload.roles === 'object') {
      for (key in payload.roles) {
        if (Object.prototype.hasOwnProperty.call(payload.roles, key)) {
          state.roles[key] = text(payload.roles[key]);
          index(state.roleBySpelling, 'Role', key, state.roles[key]);
        }
      }
    }
    if (payload.granted && payload.granted.length) {
      for (var i = 0; i < payload.granted.length; i++) {
        var token = text(payload.granted[i]);
        if (token) {
          state.granted.push(token);
          state.held[token] = true;
        }
      }
    }
    if (payload.mine && payload.mine.length) {
      for (var j = 0; j < payload.mine.length; j++) {
        var name = text(payload.mine[j]);
        if (name) {
          state.mine.push(name);
          state.owned[name] = true;
        }
      }
    }
    state.granted.sort();
    state.mine.sort();
    state.ready = true;
    return state;
  }

  // warn reports a problem without ever throwing: a broken payload must not break the page.
  // warn 报告问题且从不抛出异常：坏掉的载荷不该把页面弄坏。
  function warn(message) {
    var g = typeof globalThis !== 'undefined' ? globalThis : {};
    if (g.console && typeof g.console.warn === 'function') {
      g.console.warn(message);
    }
  }

  // readInjected finds and parses the injected payload element, and returns null when there is none.
  // readInjected 找到并解析注入的载荷元素，没有时返回 null。
  function readInjected(d) {
    if (!d || typeof d.getElementById !== 'function') {
      return null;
    }
    var el = d.getElementById(PAYLOAD_ID);
    if (!el) {
      return null;
    }
    var raw = typeof el.textContent === 'string' ? el.textContent : '';
    if (!text(raw)) {
      return null;
    }
    try {
      return JSON.parse(raw);
    } catch (err) {
      warn('yd-authz: the injected payload is not valid JSON, denying every capability | yd-authz：注入载荷不是合法 JSON，将拒绝全部能力');
      return null;
    }
  }

  // resolvePerm maps any spelling of a capability to its token. | resolvePerm 把能力的任意写法映射为标识。
  function resolvePerm(ref) {
    var key = text(ref);
    if (!key) {
      return '';
    }
    if (state.held[key]) {
      return key; // already the token itself | 本身就是标识
    }
    return state.permBySpelling[key] || '';
  }

  // resolveRole maps any spelling of a role to its name. | resolveRole 把角色的任意写法映射为名字。
  function resolveRole(ref) {
    var key = text(ref);
    if (!key) {
      return '';
    }
    if (state.owned[key]) {
      return key; // already the role name itself | 本身就是角色名
    }
    return state.roleBySpelling[key] || '';
  }

  // can reports whether the subject holds a capability. An unknown reference answers false and never
  // throws, so a decision inside an Alpine expression can never take the page down.
  // can 报告主体是否持有某能力。未知引用回答 false 且从不抛出，
  // 因此 Alpine 表达式里的判定绝不会把页面弄挂。
  function can(ref) {
    var token = resolvePerm(ref);
    return token !== '' && state.held[token] === true;
  }

  // hasRole reports whether the subject holds a role. | hasRole 报告主体是否持有某角色。
  function hasRole(ref) {
    var name = resolveRole(ref);
    return name !== '' && state.owned[name] === true;
  }

  // perms returns the capabilities held, as a copy. | perms 返回持有的能力列表副本。
  function perms() {
    return state.granted.slice();
  }

  // roles returns the roles held, as a copy. | roles 返回持有的角色列表副本。
  function roles() {
    return state.mine.slice();
  }

  // subject returns a snapshot of what the page knows. | subject 返回页面已知内容的快照。
  function subject() {
    return {
      perms: state.perms,
      roles: state.roles,
      granted: state.granted.slice(),
      mine: state.mine.slice(),
      ready: state.ready
    };
  }

  // guardAttr returns the attribute guarding this node, or '' when there is none. | guardAttr 返回守卫该节点的属性，没有时返回 ''。
  function guardAttr(el) {
    if (el && typeof el.hasAttribute === 'function') {
      if (el.hasAttribute('data-yd-perm')) {
        return 'data-yd-perm';
      }
      if (el.hasAttribute('data-yd-role')) {
        return 'data-yd-role';
      }
    }
    return '';
  }

  // modeOf reads the guard mode of a node, defaulting to hide. | modeOf 读取节点的守卫模式，缺省为隐藏。
  function modeOf(el, attr) {
    var name = attr === 'data-yd-role' ? 'data-yd-role-mode' : 'data-yd-perm-mode';
    var value = typeof el.getAttribute === 'function' ? text(el.getAttribute(name)) : '';
    return value === MODE_DISABLE ? MODE_DISABLE : MODE_HIDE;
  }

  // holdFor reads the decision for one guarded node. An attribute that is present but empty is a
  // reference the server failed to fill in, and it is refused like any other unknown reference.
  // holdFor 读取某个受守卫节点的判定结果。属性存在但为空，说明服务端没填上这个引用，
  // 它和别的未知引用一样被拒绝。
  function holdFor(el, attr) {
    var ref = typeof el.getAttribute === 'function' ? el.getAttribute(attr) : '';
    return attr === 'data-yd-role' ? hasRole(ref) : can(ref);
  }

  // hide conceals a node without destroying what the server rendered: the inline display is kept, so
  // a later bind that grants the capability puts the node back exactly as it was.
  // hide 隐藏节点而不破坏服务端渲染的内容：行内 display 会被保留，
  // 使之后授予该能力的绑定能把节点原样放回。
  function hide(el) {
    if (typeof el.setAttribute !== 'function') {
      return;
    }
    // 已经隐藏的节点不再写样式与属性：每次交换都会重新判定，重复写入只会制造无谓的样式失效。 |
    // A node already hidden gets no further style or attribute writes: every swap re-evaluates, and repeated
    // writes only cause pointless style invalidation.
    if (el.getAttribute(STATE_ATTR) === 'hidden') {
      return;
    }
    el.setAttribute(PREV_ATTR, el.style ? text(el.style.display) : '');
    if (el.style) {
      el.style.display = 'none';
    }
    el.setAttribute(STATE_ATTR, 'hidden');
  }

  // unhide restores a node this script hid, and only such a node: one the server hid stays hidden.
  // unhide 恢复本脚本隐藏过的节点，且只恢复这类节点：服务端隐藏的节点保持隐藏。
  function unhide(el) {
    if (typeof el.getAttribute !== 'function' || el.getAttribute(STATE_ATTR) !== 'hidden') {
      return;
    }
    var prev = el.getAttribute(PREV_ATTR);
    if (el.style) {
      el.style.display = prev === null || prev === undefined ? '' : prev;
    }
    el.removeAttribute(STATE_ATTR);
    el.removeAttribute(PREV_ATTR);
  }

  // disable greys a node out instead of removing it, which keeps the layout stable and tells a
  // screen reader why the control is unavailable. | disable 把节点变灰而不是移除它，
  // 既保持版式稳定，也让屏幕阅读器知道该控件为何不可用。
  function disable(el) {
    if (typeof el.setAttribute !== 'function') {
      return;
    }
    if (el.getAttribute(STATE_ATTR) === 'disabled') {
      return;
    }
    el.setAttribute('disabled', 'disabled');
    el.setAttribute('aria-disabled', 'true');
    el.setAttribute(STATE_ATTR, 'disabled');
    if ('disabled' in el) {
      el.disabled = true;
    }
  }

  // enable undoes disable, and only that. | enable 撤销 disable，且只撤销它。
  function enable(el) {
    if (typeof el.getAttribute !== 'function' || el.getAttribute(STATE_ATTR) !== 'disabled') {
      return;
    }
    el.removeAttribute('disabled');
    el.removeAttribute('aria-disabled');
    el.removeAttribute(STATE_ATTR);
    if ('disabled' in el) {
      el.disabled = false;
    }
  }

  // bind applies the declarative guards under root - and only under root, so a swap repaints its own
  // fragment instead of walking the whole document again. Binding is idempotent: a node this script
  // already hid is left alone, and one it hid that may now be shown again is restored.
  // bind 应用 root 之下的声明式守卫，且只处理 root 之下：这样一次交换只重绘自己的片段，
  // 而不必再遍历整篇文档。绑定是幂等的：本脚本已隐藏的节点保持原样，
  // 之前被隐藏而如今可能该显示的节点则被恢复。
  function bind(root) {
    if (!root || typeof root.querySelectorAll !== 'function') {
      return;
    }
    var nodes = root.querySelectorAll(GUARD_SELECTOR);
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      var attr = guardAttr(el);
      if (!attr) {
        continue;
      }
      if (holdFor(el, attr)) {
        unhide(el);
        enable(el);
        continue;
      }
      if (modeOf(el, attr) === MODE_DISABLE) {
        disable(el);
        continue;
      }
      hide(el);
    }
  }

  // onAfterSwap handles htmx:after:swap: the fragment htmx just inserted has never been decided, so
  // it is bound here. Out-of-band fragments may land away from the target, hence the whole document
  // is bound too - unlike i18n there is no signature to keep, because binding is idempotent.
  // onAfterSwap 处理 htmx:after:swap：htmx 刚插入的片段从未被判定过，因此在这里绑定。
  // 带外片段可能落在远离目标的位置，所以整篇文档也会被绑定——与 i18n 不同，这里没有签名要记，
  // 因为绑定是幂等的。
  // badgeSwap reports a swap of one unread badge: those arrive four times per realtime event and can
  // carry no out-of-band fragment, so binding the small root is enough.
  // badgeSwap 报告一次未读角标交换：实时事件一次会推四个，它们不可能携带带外片段，绑定这个小根节点即可。
  function badgeSwap(root) {
    if (!root || !root.id) {
      return false;
    }
    return root.id === 'yd-unread-badge' ||
      root.id === 'yd-unread-menu-badge' ||
      root.id === 'yd-sidebar-unread' ||
      root.id === 'yd-sidebar-friend-requests';
  }

  function onAfterSwap(event) {
    var detail = (event && event.detail) || {};
    var ctx = detail.ctx || {};
    var root = ctx.target || detail.target || ctx.elt || ctx.sourceElement || null;
    if (root && root.nodeType && root.nodeType !== 1) {
      root = null; // a document or a text node is not a fragment to bind | 文档或文本节点不是可绑定的片段
    }
    if (root) {
      bind(root);
    }
    if (badgeSwap(root)) {
      return;
    }
    bind(doc());
  }

  // listen registers the htmx hook once per document; without a document it does nothing.
  // listen 为每个文档只注册一次 htmx 钩子；没有文档时什么都不做。
  function listen() {
    var d = doc();
    if (!d || typeof d.addEventListener !== 'function' || state.boundDoc === d) {
      return;
    }
    d.addEventListener('htmx:after:swap', onAfterSwap);
    state.boundDoc = d;
  }

  // api is what the browser sees. It is created before init runs so that init can refresh it. PERM
  // and ROLE point at the live name maps: a re-read empties them in place, so a holder of the map
  // never needs to be handed a new one. | api 就是浏览器看到的东西。它先于 init 执行被创建，以便 init 刷新它。
  // PERM 与 ROLE 指向实时的名字表：重读会就地清空它们，因此持有该表的人永远不需要换一张新的。
  var api = {
    PERM: state.perms,
    ROLE: state.roles,
    can: can,
    hasRole: hasRole,
    perms: perms,
    roles: roles,
    subject: subject,
    bind: bind,
    init: init
  };

  // init reads the payload, binds the document and publishes the api. Calling it more than once is
  // safe: a re-read replaces the grants rather than piling them up, and publishing is idempotent.
  // Passing options.payload skips the injected element, which is what the node test uses.
  // init 读取载荷、绑定文档并发布 api。重复调用是安全的：重读会替换授权而不是把它们堆起来，
  // 而发布是幂等的。传入 options.payload 可跳过注入元素，node 测试正是这么做的。
  function init(options) {
    var opts = options || {};
    var payload = opts.payload;
    if (payload === undefined) {
      payload = readInjected(doc());
    }
    apply(payload);
    listen();
    bind(doc());
    publish();
    return api;
  }

  // publish merges the api into window.yd, creating it when no other script ran first.
  // publish 把 api 并入 window.yd，其它脚本尚未运行时则新建。
  function publish() {
    var g = typeof globalThis !== 'undefined' ? globalThis : {};
    if (!g.yd || typeof g.yd !== 'object') {
      g.yd = {};
    }
    for (var key in api) {
      if (Object.prototype.hasOwnProperty.call(api, key)) {
        g.yd[key] = api[key];
      }
    }
    return g.yd;
  }

  // boot is the automatic start inside a browser: a deferred script runs after the document is
  // parsed, so the payload element is already there. A document that is still loading is waited for,
  // which keeps a non-deferred include working as well. | boot 是浏览器中的自动启动：
  // defer 脚本在文档解析完毕后运行，因此载荷元素已经就位。仍在加载中的文档则等它加载完，
  // 这使非 defer 的引入方式同样可用。
  function boot() {
    init();
  }

  (function autoStart() {
    var d = doc();
    if (!d) {
      return; // node:test drives init() explicitly | node:test 显式驱动 init()
    }
    if (d.readyState === 'loading' && typeof d.addEventListener === 'function') {
      d.addEventListener('DOMContentLoaded', boot);
      return;
    }
    boot();
  })();

  return api;
});

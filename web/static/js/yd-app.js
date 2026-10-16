/*!
 * yd-app.js — behaviour of the yd admin shell: theme, sidebar, dialogs, flash, realtime banner.
 * yd-app.js — yd 管理后台外壳的行为层：主题、侧边栏、对话框、闪现消息、实时连接提示。
 *
 * Hard rules | 硬规则:
 *   1. No dependency, no build step: everything below is plain DOM and event delegation.
 *      无依赖、无构建：以下全部是纯 DOM 与事件委托。
 *   2. Delegated listeners only, so content swapped in by htmx needs no re-wiring.
 *      只用委托监听，htmx 交换进来的内容无需重新绑定。
 *   3. Translation itself belongs to yd-i18n.js; this file only calls into it.
 *      翻译本身归 yd-i18n.js 负责；本文件只调用它。
 *   4. Storage contract | 存储契约:
 *        localStorage "yd_theme"   light | dark | system, also mirrored into the yd_theme cookie
 *                                  同时镜像到 yd_theme cookie，供服务端预渲染 <html data-theme>
 *        localStorage "yd_sidebar" "true" while the left navigation is collapsed
 *                                  左侧导航折叠时为 "true"
 *
 * Hooks it owns | 本文件负责的钩子:
 *   [data-yd-theme-toggle]     switch between light and dark | 在浅色与深色之间切换
 *   [data-yd-theme-set]        <select> holding light | dark | system | 选择 light | dark | system 的下拉
 *   [data-yd-sidebar]          the collapsible left navigation | 可折叠的左侧导航
 *   [data-yd-sidebar-toggle]   collapse / expand it | 折叠或展开
 *   [data-yd-modal-open="id"]  open the dialog with that id | 打开该 id 的对话框
 *   [data-yd-modal-close]      close the surrounding dialog | 关闭所在对话框
 *   [data-yd-modal-confirm]    run the pending action, then close | 执行待确认动作后关闭
 *   [data-yd-modal-action="…"] selector clicked on confirm, e.g. the real delete button
 *                              确认时被点击的选择器，例如真正的删除按钮
 *   [data-yd-modal-message-key] i18n key copied onto [data-yd-modal-body] | 复制到正文的翻译键
 *   [data-yd-flash-dismiss]    remove the surrounding flash item | 移除所在闪现消息
 *   [data-yd-reconnect]        retry the realtime streams | 重试实时连接
 *   [data-yd-retry]            retry the failed page | 重试失败的页面
 *   [data-yd-lang]             switch the language through ydI18n.setLang | 经 ydI18n.setLang 切换语言
 *   [data-yd-format]           number / percent / currency / date / datetime cell | 数字或日期单元格
 *   [data-yd-nav]              left navigation link, marked active on navigation | 左侧导航链接，导航时标记为当前项
 *
 * Realtime | 实时:
 *   hx-sse / hx-ws fire htmx:sse:* and htmx:ws:* events; the banner #yd-connection is shown
 *   while a stream is down and [data-yd-connection-state] carries the pill state.
 *   hx-sse 与 hx-ws 会派发 htmx:sse:* 与 htmx:ws:* 事件：数据流断开时显示 #yd-connection 提示条，
 *   状态标签由 [data-yd-connection-state] 承载。
 */
(function () {
  'use strict';

  var THEME_KEY = 'yd_theme'; // localStorage key, mirrored into the same cookie | localStorage 键名，同时镜像为同名 cookie
  var SIDEBAR_KEY = 'yd_sidebar'; // localStorage key of the collapse state | 折叠状态的 localStorage 键名
  var ONE_YEAR = 31536000; // cookie max-age in seconds | cookie 有效期（秒）
  var FLASH_TIMEOUT = 6000; // auto dismiss delay of info/success flashes | info/success 闪现消息的自动消失延时
  var CURRENCY = 'CNY'; // default currency of [data-yd-format="currency"] | [data-yd-format="currency"] 的默认币种
  var VALID_THEMES = { light: true, dark: true, system: true };

  // win returns the global object so the file stays testable outside a browser.
  // win 返回全局对象，使本文件在浏览器之外也可测试。
  function win() {
    return typeof globalThis !== 'undefined' ? globalThis : {};
  }

  // ready runs fn once the DOM is parsed, whichever moment we are called in.
  // ready 在 DOM 解析完成后执行 fn，无论何时被调用。
  function ready(fn) {
    var d = win().document;
    if (!d) {
      return;
    }
    if (d.readyState === 'loading') {
      d.addEventListener('DOMContentLoaded', fn, { once: true });
    } else {
      fn();
    }
  }

  function all(selector, root) {
    var scope = root || win().document;
    if (!scope || typeof scope.querySelectorAll !== 'function') {
      return [];
    }
    return Array.prototype.slice.call(scope.querySelectorAll(selector));
  }

  function one(selector, root) {
    var scope = root || win().document;
    return scope && typeof scope.querySelector === 'function' ? scope.querySelector(selector) : null;
  }

  // closest walks up from a target, tolerating a text node or a missing document.
  // closest 从目标向上查找，兼容文本节点或缺少文档的情况。
  function closest(node, selector) {
    var current = node;
    while (current && current.nodeType === 1) {
      if (typeof current.matches === 'function' && current.matches(selector)) {
        return current;
      }
      if (typeof current.closest === 'function') {
        return current.closest(selector);
      }
      current = current.parentNode;
    }
    return null;
  }

  function getAttr(node, name) {
    return node && typeof node.getAttribute === 'function' ? node.getAttribute(name) : null;
  }

  function storage() {
    var store = win().localStorage;
    return store && typeof store.getItem === 'function' ? store : null;
  }

  function read(name) {
    var store = storage();
    try {
      return store ? store.getItem(name) || '' : '';
    } catch (err) {
      return ''; // private mode may throw on read | 隐私模式下读取可能抛错
    }
  }

  function write(name, value) {
    var store = storage();
    try {
      if (store) {
        store.setItem(name, value);
      }
    } catch (err) {
      /* ignore: the cookie below stays the server-side copy | 忽略：下面的 cookie 仍是服务端副本 */
    }
  }

  function readCookie(name) {
    var d = win().document;
    if (!d || typeof d.cookie !== 'string' || !d.cookie) {
      return '';
    }
    var parts = d.cookie.split(';');
    for (var i = 0; i < parts.length; i++) {
      var pair = parts[i].trim();
      var eq = pair.indexOf('=');
      if (eq > 0 && pair.slice(0, eq) === name) {
        return decodeURIComponent(pair.slice(eq + 1));
      }
    }
    return '';
  }

  // writeCookie mirrors a preference so the next server render already agrees with the client.
  // writeCookie 镜像偏好，使下一次服务端渲染与客户端保持一致。
  function writeCookie(name, value) {
    var d = win().document;
    if (!d) {
      return;
    }
    try {
      d.cookie = name + '=' + encodeURIComponent(value) + '; path=/; max-age=' + ONE_YEAR + '; samesite=lax';
    } catch (err) {
      /* a sandboxed document may reject cookie writes | 沙箱文档可能拒绝写 cookie */
    }
  }

  function i18n() {
    var api = win().ydI18n;
    return api && typeof api.t === 'function' ? api : null;
  }

  // applyKey translates a hook-added node without ever showing a bare key: while the catalog
  // has no entry the previous text survives, and the attribute stays for a later rebind.
  // applyKey 翻译由钩子新增的节点，且绝不显示裸键：目录缺失时保留原文，
  // 同时保留属性以便之后重新绑定。
  function applyKey(node, key) {
    if (!node || !key) {
      return;
    }
    node.setAttribute('data-yd-i18n', key);
    var api = i18n();
    if (!api) {
      return;
    }
    var text = api.t(key);
    if (text && text !== key) {
      node.textContent = text;
    }
  }

  /* ---------------------------------------------------------------- theme | 主题 */

  function systemTheme() {
    var mq = win().matchMedia;
    if (typeof mq !== 'function') {
      return 'light';
    }
    try {
      return mq('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    } catch (err) {
      return 'light';
    }
  }

  function storedTheme() {
    var theme = read(THEME_KEY) || readCookie(THEME_KEY);
    return VALID_THEMES[theme] ? theme : '';
  }

  // applyTheme sets <html data-theme> to the resolved value and keeps every switch in sync.
  // applyTheme 把 <html data-theme> 设为解析后的值，并同步所有切换控件。
  function applyTheme(theme, persist) {
    var choice = VALID_THEMES[theme] ? theme : 'system';
    var resolved = choice === 'system' ? systemTheme() : choice;
    var d = win().document;
    if (d && d.documentElement) {
      d.documentElement.setAttribute('data-theme', resolved);
    }
    all('[data-yd-theme-toggle]').forEach(function (button) {
      button.setAttribute('aria-pressed', resolved === 'dark' ? 'true' : 'false');
      button.setAttribute('data-yd-theme-state', choice);
    });
    all('[data-yd-theme-set]').forEach(function (select) {
      if (select.value !== choice) {
        select.value = choice;
      }
    });
    if (persist) {
      write(THEME_KEY, choice);
      writeCookie(THEME_KEY, choice);
    }
    return resolved;
  }

  function currentTheme() {
    var select = one('[data-yd-theme-set]');
    var fromSelect = select && select.value;
    return VALID_THEMES[fromSelect] ? fromSelect : storedTheme() || 'system';
  }

  // toggleTheme flips light and dark; from "system" it flips whatever is on screen now.
  // toggleTheme 在浅色与深色之间切换；当前为 system 时按屏幕上正在显示的主题翻转。
  function toggleTheme() {
    var resolved = applyTheme(currentTheme(), false);
    return applyTheme(resolved === 'dark' ? 'light' : 'dark', true);
  }

  /* ---------------------------------------------------------------- sidebar | 侧边栏 */

  function sidebar() {
    return one('[data-yd-sidebar]');
  }

  function applySidebar(collapsed) {
    var aside = sidebar();
    if (!aside) {
      return;
    }
    aside.setAttribute('data-collapsed', collapsed ? 'true' : 'false');
    all('[data-yd-sidebar-toggle]', aside).forEach(function (button) {
      button.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
    });
  }

  function toggleSidebar() {
    var aside = sidebar();
    if (!aside) {
      return;
    }
    var collapsed = aside.getAttribute('data-collapsed') === 'true';
    var next = !collapsed;
    applySidebar(next);
    write(SIDEBAR_KEY, next ? 'true' : 'false');
  }

  // syncActiveNav marks the link matching the current URL, which htmx navigation changes
  // without ever re-rendering the persistent sidebar.
  // syncActiveNav 标记与当前 URL 匹配的链接：htmx 导航不会重新渲染常驻侧边栏，因此需要这里同步。
  function syncActiveNav() {
    var d = win().document;
    if (!d || !d.location) {
      return;
    }
    var path = d.location.pathname || '';
    all('[data-yd-nav]').forEach(function (link) {
      var href = getAttr(link, 'href') || '';
      var active = !!href && href !== '/' && (path === href || path.indexOf(href + '/') === 0);
      if (active) {
        link.setAttribute('data-active', 'true');
        link.setAttribute('aria-current', 'page');
      } else {
        link.removeAttribute('data-active');
        link.removeAttribute('aria-current');
      }
    });
  }

  /* ---------------------------------------------------------------- modal | 对话框 */

  // modalState remembers which control opened the dialog and what confirming it should click.
  // modalState 记住打开对话框的控件，以及确认时应当点击的元素。
  var modalState = { opener: null, action: null };

  function openModal(opener) {
    var id = getAttr(opener, 'data-yd-modal-open');
    var d = win().document;
    var modal = id && d && typeof d.getElementById === 'function' ? d.getElementById(id) : null;
    if (!modal) {
      return null;
    }
    modalState.opener = opener;
    var actionSelector = getAttr(opener, 'data-yd-modal-action');
    modalState.action = actionSelector ? one(actionSelector) : null;

    var body = one('[data-yd-modal-body]', modal);
    if (body) {
      applyKey(body, getAttr(opener, 'data-yd-modal-message-key'));
    }
    modal.hidden = false;
    modal.setAttribute('data-open', 'true');
    var focusable = one('input, select, textarea, button', modal);
    if (focusable && typeof focusable.focus === 'function') {
      focusable.focus();
    }
    return modal;
  }

  // closeModal hides the surrounding dialog and gives focus back to its opener.
  // closeModal 隐藏所在对话框，并把焦点还给打开它的控件。
  function closeModal(modal) {
    if (!modal) {
      return;
    }
    modal.hidden = true;
    modal.removeAttribute('data-open');
    var opener = modalState.opener;
    modalState.opener = null;
    modalState.action = null;
    if (opener && typeof opener.focus === 'function') {
      opener.focus();
    }
  }

  function openModalElement() {
    var dialogs = all('[data-yd-modal][data-open]');
    return dialogs.length > 0 ? dialogs[dialogs.length - 1] : null;
  }

  // confirmModal clicks the element named by data-yd-modal-action and closes the dialog; without
  // one it emits yd:modal:confirm so application code can take over.
  // confirmModal 点击 data-yd-modal-action 指定的元素并关闭对话框；未指定时派发
  // yd:modal:confirm 事件，交由应用代码处理。
  function confirmModal(modal) {
    var action = modalState.action;
    var opener = modalState.opener;
    closeModal(modal);
    if (action) {
      if (typeof action.requestSubmit === 'function' && action.tagName === 'FORM') {
        action.requestSubmit();
      } else if (typeof action.click === 'function') {
        action.click();
      }
      return;
    }
    var d = win().document;
    var CustomEventCtor = win().CustomEvent;
    if (d && typeof CustomEventCtor === 'function' && typeof d.dispatchEvent === 'function') {
      d.dispatchEvent(new CustomEventCtor('yd:modal:confirm', { detail: { opener: opener, modal: modal }, bubbles: true }));
    }
  }

  /* ---------------------------------------------------------------- flash | 闪现消息 */

  function dismissFlash(item) {
    if (item && item.parentNode) {
      item.parentNode.removeChild(item);
    }
  }

  // autoDismissFlashes removes informational toasts after a while; errors and warnings stay.
  // autoDismissFlashes 让提示类闪现消息自动消失；错误与警告保留。
  function autoDismissFlashes(root) {
    all('[data-yd-flash-item]', root).forEach(function (item) {
      var level = getAttr(item, 'class') || '';
      var dismissible = level.indexOf('--success') >= 0 || level.indexOf('--info') >= 0;
      if (!dismissible || item.getAttribute('data-yd-flash-timer') === 'set') {
        return;
      }
      item.setAttribute('data-yd-flash-timer', 'set');
      win().setTimeout(function () {
        dismissFlash(item);
      }, FLASH_TIMEOUT);
    });
  }

  /* ---------------------------------------------------------------- realtime | 实时 */

  // setConnection reflects a stream state on the banner and on the status pill.
  // setConnection 把数据流状态反映到提示条与状态标签上。
  function setConnection(state) {
    var banner = one('#yd-connection');
    if (banner) {
      banner.hidden = state === 'open';
    }
    var pill = one('[data-yd-connection-state]');
    if (!pill) {
      return;
    }
    pill.setAttribute('data-yd-connection-state', state);
    var keys = {
      open: 'realtime.status.open',
      connecting: 'realtime.status.connecting',
      closed: 'realtime.status.closed',
      idle: 'realtime.status.idle'
    };
    applyKey(pill, keys[state] || keys.idle);
  }

  // reconnect reloads the page: the streams are opened on load, so a reload is the only
  // contract-safe way to reconnect without duplicating sockets.
  // reconnect 会重载页面：数据流在加载时打开，重载是唯一不会重复建立连接的安全做法。
  function reconnect() {
    var banner = one('#yd-connection');
    if (banner) {
      banner.hidden = true;
    }
    var d = win().document;
    if (d && d.location && typeof d.location.reload === 'function') {
      d.location.reload();
    }
  }

  /* ---------------------------------------------------------------- formatting | 格式化 */

  // formatCells renders every [data-yd-format] cell through ydI18n.t.n, so numbers and dates
  // follow the active language.
  // formatCells 通过 ydI18n.t.n 渲染每个 [data-yd-format] 单元格，使数字与日期跟随当前语言。
  function formatCells(root) {
    var api = i18n();
    var scope = root || win().document;
    if (api) {
      all('[data-yd-format]', scope).forEach(function (cell) {
        var raw = getAttr(cell, 'data-yd-format-value');
        if (raw === null) {
          return;
        }
        var kind = getAttr(cell, 'data-yd-format') || 'number';
        var options = { kind: kind };
        var currency = getAttr(cell, 'data-yd-format-currency');
        if (kind === 'percent') {
          options.style = 'percent';
        }
        if (kind === 'currency') {
          options.currency = currency || CURRENCY;
        }
        try {
          cell.textContent = api.t.n(
            kind === 'date' || kind === 'datetime' ? raw : Number(raw),
            options
          );
        } catch (err) {
          cell.textContent = raw; // never lose the value on a formatting failure | 格式化失败时绝不丢值
        }
      });
    }
    all('[data-yd-lang-current]', scope).forEach(function (node) {
      node.textContent = api ? api.current() : '';
    });
  }

  /* ---------------------------------------------------------------- wiring | 事件绑定 */

  function onDocument(type, handler) {
    var d = win().document;
    if (d && typeof d.addEventListener === 'function') {
      d.addEventListener(type, handler);
    }
  }

  function onClick(event) {
    var target = event.target || event.srcElement;

    var opener = closest(target, '[data-yd-modal-open]');
    if (opener) {
      event.preventDefault();
      openModal(opener);
      return;
    }
    var closer = closest(target, '[data-yd-modal-close]');
    if (closer) {
      event.preventDefault();
      closeModal(closest(closer, '[data-yd-modal]'));
      return;
    }
    var confirm = closest(target, '[data-yd-modal-confirm]');
    if (confirm) {
      event.preventDefault();
      confirmModal(closest(confirm, '[data-yd-modal]'));
      return;
    }
    if (closest(target, '[data-yd-flash-dismiss]')) {
      event.preventDefault();
      dismissFlash(closest(target, '[data-yd-flash-item]'));
      return;
    }
    if (closest(target, '[data-yd-theme-toggle]')) {
      event.preventDefault();
      toggleTheme();
      return;
    }
    if (closest(target, '[data-yd-sidebar-toggle]')) {
      event.preventDefault();
      toggleSidebar();
      return;
    }
    if (closest(target, '[data-yd-reconnect]')) {
      event.preventDefault();
      reconnect();
      return;
    }
    if (closest(target, '[data-yd-retry]')) {
      event.preventDefault();
      reconnect();
      return;
    }
    var langButton = closest(target, '[data-yd-lang]');
    if (langButton) {
      event.preventDefault();
      var api = i18n();
      var lang = getAttr(langButton, 'data-yd-lang');
      if (api && typeof api.setLang === 'function' && lang) {
        api.setLang(lang);
        formatCells(win().document);
      }
    }
  }

  function onChange(event) {
    var target = event.target || event.srcElement;
    var select = closest(target, '[data-yd-theme-set]');
    if (select) {
      applyTheme(select.value, true);
    }
  }

  function onKeyDown(event) {
    if (event.key !== 'Escape') {
      return;
    }
    closeModal(openModalElement());
  }

  function onAfterSwap(event) {
    var detail = (event && event.detail) || {};
    var ctx = detail.ctx || {};
    var root = ctx.target || detail.target || null;
    if (!root || root.nodeType !== 1) {
      root = win().document;
    }
    formatCells(root);
    autoDismissFlashes(root);
    syncActiveNav();
  }

  function bindShellEvents() {
    onDocument('click', onClick);
    onDocument('change', onChange);
    onDocument('keydown', onKeyDown);
    onDocument('htmx:after:swap', onAfterSwap);
    onDocument('htmx:sse:before:connection', function () {
      setConnection('connecting');
    });
    onDocument('htmx:sse:after:connection', function () {
      setConnection('open');
    });
    onDocument('htmx:sse:close', function () {
      setConnection('closed');
    });
    onDocument('htmx:sse:error', function () {
      setConnection('closed');
    });
    onDocument('htmx:ws:before:connection', function () {
      setConnection('connecting');
    });
    onDocument('htmx:ws:after:connection', function () {
      setConnection('open');
    });
    onDocument('htmx:ws:close', function () {
      setConnection('closed');
    });
    onDocument('htmx:ws:error', function () {
      setConnection('closed');
    });
    onDocument('popstate', syncActiveNav);
  }

  // watchSystem keeps a "system" preference honest when the OS theme changes.
  // watchSystem 在系统主题变化时保持 system 偏好正确。
  function watchSystem() {
    var mq = win().matchMedia;
    if (typeof mq !== 'function') {
      return;
    }
    try {
      var query = mq('(prefers-color-scheme: dark)');
      var listener = function () {
        if (currentTheme() === 'system') {
          applyTheme('system', false);
        }
      };
      if (typeof query.addEventListener === 'function') {
        query.addEventListener('change', listener);
      } else if (typeof query.addListener === 'function') {
        query.addListener(listener);
      }
    } catch (err) {
      /* matchMedia is optional; the server rendered theme stays in charge | matchMedia 可选，此时以服务端主题为准 */
    }
  }

  // boot initialises the shell: i18n catalogs, guomi readiness and every owned hook.
  // boot 初始化外壳：i18n 目录、国密就绪状态以及本文件负责的全部钩子。
  function boot() {
    var api = i18n();
    if (api && typeof api.init === 'function') {
      api.init();
      // Language changes must re-render the formatted cells too. | 语言变化时同样要重新渲染格式化单元格。
      if (typeof api.on === 'function') {
        api.on(function () {
          formatCells(win().document);
        });
      }
    }

    // ydGuomi builds its constants eagerly; ready() only keeps the bootstrap awaitable.
    // ydGuomi 会立即构建常量；ready() 只是让引导流程可等待。
    var guomi = win().ydGuomi;
    if (guomi && typeof guomi.ready === 'function') {
      try {
        guomi.ready();
      } catch (err) {
        /* the browser without WebCrypto falls back to the htmx extension | 缺 WebCrypto 的浏览器回退到 htmx 扩展 */
      }
    }

    applyTheme(storedTheme() || 'system', false);
    applySidebar(read(SIDEBAR_KEY) === 'true');
    syncActiveNav();
    formatCells(win().document);
    autoDismissFlashes(win().document);
    watchSystem();
    bindShellEvents();
  }

  win().ydApp = {
    boot: boot,
    theme: applyTheme,
    setTheme: function (theme) {
      return applyTheme(theme, true);
    },
    toggleTheme: toggleTheme,
    sidebar: applySidebar,
    toggleSidebar: toggleSidebar,
    formatCells: formatCells,
    setConnection: setConnection
  };

  ready(boot);
})();

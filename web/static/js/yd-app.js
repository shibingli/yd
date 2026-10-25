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
 *   [data-yd-sidebar-open]     open the off-canvas rail below 64rem | 在 64rem 以下打开抽屉式侧栏
 *   [data-yd-sidebar-backdrop] close it by clicking the scrim | 点击遮罩关闭抽屉
 *   [data-yd-modal-open="id"]  open the dialog with that id | 打开该 id 的对话框
 *   [data-yd-modal-close]      close the surrounding dialog | 关闭所在对话框
 *   [data-yd-modal-confirm]    run the pending action, then close | 执行待确认动作后关闭
 *   [data-yd-modal-action="…"] selector clicked on confirm, e.g. the real delete button
 *                              确认时被点击的选择器，例如真正的删除按钮
 *   [data-yd-modal-message-key] i18n key copied onto [data-yd-modal-body] | 复制到正文的翻译键
 *   [data-yd-flash-dismiss]    remove the surrounding flash item | 移除所在闪现消息
 *   [data-yd-reconnect]        retry the realtime streams | 重试实时连接
 *   [data-yd-retry]            retry the failed page | 重试失败的页面
 *   [data-yd-lang]             switch to a specific language through ydI18n.setLang | 经 ydI18n.setLang 切到指定语言
 *   [data-yd-lang-toggle]      one stateful button that cycles to the other language | 在两种语言间循环的单一按钮
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
    // The switch markup spells the system choice "auto" so one CSS rule can drive both the label and
    // the glyph; the stored preference keeps the three-state names. | 切换控件用 "auto" 表示跟随系统，
    // 使一条 CSS 规则同时驱动标签与图标；存储偏好仍使用三态名称。
    var uiChoice = choice === 'system' ? 'auto' : choice;
    all('[data-yd-theme-toggle]').forEach(function (button) {
      button.setAttribute('aria-pressed', resolved === 'dark' ? 'true' : 'false');
      button.setAttribute('data-yd-theme-state', uiChoice);
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
    // Tell components that own theme-dependent internals (the Markdown editor) that the resolved
    // theme changed. It is a plain DOM event, so no component holds a reference to another.
    // 通知拥有主题相关内部状态的组件（Markdown 编辑器）：解析后的主题已变化。它是普通 DOM 事件，
    // 因此组件之间不持有彼此的引用。
    if (d && typeof d.dispatchEvent === 'function' && typeof win().CustomEvent === 'function') {
      d.dispatchEvent(new win().CustomEvent('yd:theme', { detail: { theme: resolved, choice: choice } }));
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

  // applyMobileSidebar opens the off-canvas rail below the 64rem breakpoint and keeps the
  // backdrop, body scroll lock and aria-expanded state in sync. | applyMobileSidebar 在 64rem
  // 断点以下打开抽屉式侧栏，并同步遮罩、页面滚动锁与 aria-expanded。
  function applyMobileSidebar(open) {
    var aside = sidebar();
    var backdrop = one('[data-yd-sidebar-backdrop]');
    var d = win().document;
    if (aside) {
      aside.classList.toggle('is-open', !!open);
      all('[data-yd-sidebar-open]').forEach(function (button) {
        button.setAttribute('aria-expanded', open ? 'true' : 'false');
      });
    }
    if (backdrop) {
      backdrop.classList.toggle('is-open', !!open);
    }
    if (d && d.body && d.body.classList) {
      d.body.classList.toggle('yd-sidebar-open', !!open);
    }
  }

  function toggleMobileSidebar() {
    var aside = sidebar();
    if (!aside) {
      return;
    }
    applyMobileSidebar(!aside.classList.contains('is-open'));
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

  var RECOVERY_NOTICE_TIMEOUT = 3000; // how long the restored-connection notice stays | 恢复提示停留时长
  var realtimeState = {
    hadDrop: false,
    noticeTimer: null,
    noticeItem: null
  };

  // realtimeElements returns every element that owns an SSE or WebSocket stream. | realtimeElements
  // 返回所有承载 SSE 或 WebSocket 数据流的元素。
  function realtimeElements() {
    return all('[hx-sse\\:connect], [hx-ws\\:connect]');
  }

  // realtimeChannel returns the per-element state kept off the DOM. The element itself is
  // replaced on navigation, so this state naturally follows the connection's lifetime. |
  // realtimeChannel 返回保存在元素上的单通道状态；元素随导航被替换，状态因此天然随连接生命周期结束。
  function realtimeChannel(element) {
    if (!element.__ydRealtime) {
      element.__ydRealtime = {
        kind: getAttr(element, 'hx-sse:connect') !== null ? 'sse' : 'ws',
        state: 'connecting',
        attempt: 0,
        wasDown: false,
        lastEventId: ''
      };
    }
    return element.__ydRealtime;
  }

  function realtimeOffline() {
    var nav = win().navigator;
    return !!(nav && nav.onLine === false);
  }

  // setConnection paints the aggregate state on the status pill only; the banner has its own
  // lifecycle below so two channels cannot produce two banners. | setConnection 只把聚合状态画到
  // 状态标签上；横幅由下方独立生命周期管理，两条通道因此不会产生两个横幅。
  function setConnection(state) {
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

  function connectionBanner() {
    return one('#yd-connection');
  }

  function hideConnectionBanner() {
    var banner = connectionBanner();
    if (banner) {
      banner.hidden = true;
    }
    var attempt = one('[data-yd-connection-attempt]');
    if (attempt) {
      attempt.hidden = true;
      attempt.textContent = '';
    }
  }

  // showConnectionBanner updates the single banner in place. The attempt count is a separate
  // node because it changes on every retry while the sentence stays translated. |
  // showConnectionBanner 就地更新唯一横幅；重试次数单独成节点，因为每次重试都会变化，而句子本身仍走翻译。
  function showConnectionBanner(state, attempt) {
    var banner = connectionBanner();
    if (!banner) {
      return;
    }
    banner.hidden = false;
    var text = one('[data-yd-connection-text]', banner) || one('.yd-flash__text', banner);
    if (text) {
      var key = state === 'offline'
        ? 'realtime.status.offline'
        : (state === 'closed' ? 'realtime.status.failed' : 'realtime.status.reconnecting');
      applyKey(text, key);
    }
    var badge = one('[data-yd-connection-attempt]', banner);
    if (badge) {
      var numbered = attempt > 0 && state !== 'offline' && state !== 'closed';
      badge.hidden = !numbered;
      badge.textContent = numbered ? '#' + attempt : '';
    }
  }

  function clearConnectionNotice() {
    if (realtimeState.noticeTimer) {
      var clear = win().clearTimeout;
      if (typeof clear === 'function') {
        clear(realtimeState.noticeTimer);
      }
      realtimeState.noticeTimer = null;
    }
    // Removing the node as well matters on navigation: the flash container lives in the shell
    // and would otherwise keep a stale "restored" notice on a page with no realtime elements. |
    // 同时移除节点才能覆盖导航场景：提示容器在外壳里，否则会在没有实时元素的页面上留下过期的「已恢复」。
    if (realtimeState.noticeItem && realtimeState.noticeItem.parentNode) {
      realtimeState.noticeItem.parentNode.removeChild(realtimeState.noticeItem);
    }
    realtimeState.noticeItem = null;
  }

  // showRecoveredNotice emits one transient success toast. The banner hides as soon as every
  // channel is open, so the toast is the only thing left to time out. | showRecoveredNotice 发出
  // 一条短暂的恢复提示；横幅在所有通道恢复时立即隐藏，因此只剩这条提示需要计时清理。
  function showRecoveredNotice() {
    var flash = one('#yd-flash');
    if (!flash) {
      return;
    }
    var d = win().document;
    var item = d.createElement('div');
    item.className = 'yd-flash__item yd-flash__item--success';
    item.setAttribute('role', 'status');
    item.setAttribute('data-yd-flash-item', '');
    var text = d.createElement('span');
    text.className = 'yd-flash__text';
    applyKey(text, 'realtime.status.recovered');
    item.appendChild(text);
    var close = d.createElement('button');
    close.type = 'button';
    close.className = 'yd-flash__close';
    close.setAttribute('data-yd-flash-dismiss', '');
    applyKey(close, 'action.close');
    item.appendChild(close);
    var banner = connectionBanner();
    clearConnectionNotice();
    flash.insertBefore(item, banner || null);
    realtimeState.noticeItem = item;
    realtimeState.noticeTimer = win().setTimeout(function () {
      realtimeState.noticeTimer = null;
      realtimeState.noticeItem = null;
      dismissFlash(item);
    }, RECOVERY_NOTICE_TIMEOUT);
  }

  // refreshConnection aggregates every live channel. A channel that has never dropped keeps the
  // banner hidden during the initial handshake, so a fast first connection never flashes a
  // warning. A page without realtime elements resets the manager entirely. |
  // refreshConnection 聚合所有仍在文档中的通道。从未掉线的通道在首次握手期间保持横幅隐藏，
  // 因此快速的首连不会闪一下警告；没有实时元素的页面则完全重置管理器。
  function refreshConnection() {
    var elements = realtimeElements();
    var open = 0;
    var down = 0;
    var failed = 0;
    var attempt = 0;
    elements.forEach(function (element) {
      var channel = realtimeChannel(element);
      if (channel.state === 'open') {
        open++;
        return;
      }
      down++;
      if (channel.state === 'failed') {
        failed++;
      }
      if (channel.attempt > attempt) {
        attempt = channel.attempt;
      }
    });

    if (elements.length === 0) {
      setConnection('idle');
      hideConnectionBanner();
      realtimeState.hadDrop = false;
      clearConnectionNotice();
      return;
    }

    // A browser that reports itself offline shows that state even while the sockets look open;
    // the transport may not have noticed yet, and "reconnecting" would be misleading. | 浏览器报告
    // 离线时即使套接字看起来仍开着也显示离线；传输层可能还没察觉，「重连中」会误导用户。
    if (realtimeOffline()) {
      // Being offline is itself a drop; this is what makes the online transition announce
      // recovery exactly once. | 离线本身就是一次掉线；恢复时据此只提示一次。
      realtimeState.hadDrop = true;
      clearConnectionNotice();
      setConnection('connecting');
      showConnectionBanner('offline', 0);
      return;
    }

    if (open === elements.length) {
      setConnection('open');
      hideConnectionBanner();
      if (realtimeState.hadDrop) {
        realtimeState.hadDrop = false;
        showRecoveredNotice();
      }
      return;
    }

    var state = failed > 0 ? 'closed' : 'connecting';
    setConnection(state);
    if (realtimeState.hadDrop || failed > 0 || attempt > 0) {
      showConnectionBanner(state, attempt);
    } else {
      hideConnectionBanner();
    }
  }

  // resyncRealtime performs the declarative recovery fetch: an element may name a fragment to
  // reload after its channel resumes. It never targets the element that owns the stream by
  // default, because swapping that element would tear the connection down again. |
  // resyncRealtime 执行声明式恢复请求：元素可以指定通道恢复后要重取的片段。默认目标不是承载
  // 数据流的元素本身——交换它会再次拆掉连接。
  function resyncRealtime(element, detail) {
    var url = getAttr(element, 'data-yd-resync');
    if (!url) {
      return false;
    }
    var api = win().htmx;
    if (!api || typeof api.ajax !== 'function') {
      return false;
    }
    var target = getAttr(element, 'data-yd-resync-target') || '#yd-workspace';
    var swap = getAttr(element, 'data-yd-resync-swap') || 'innerMorph';
    try {
      api.ajax('GET', url, { source: element, target: target, swap: swap });
      return true;
    } catch (err) {
      return false;
    }
  }

  function announceRecovered(element, channel, detail) {
    var CustomEventCtor = win().CustomEvent;
    if (typeof CustomEventCtor !== 'function' || !element || typeof element.dispatchEvent !== 'function') {
      return;
    }
    var connection = (detail && detail.connection) || {};
    element.dispatchEvent(new CustomEventCtor('yd:realtime:recovered', {
      bubbles: true,
      detail: {
        channel: channel.kind,
        element: element,
        attempt: connection.attempt || 0,
        lastEventId: connection.lastEventId || channel.lastEventId || '',
        resynced: resyncRealtime(element, detail)
      }
    }));
  }

  function markChannelOpen(element, detail) {
    var channel = realtimeChannel(element);
    var connection = (detail && detail.connection) || {};
    var resumed = channel.wasDown;
    channel.state = 'open';
    channel.attempt = 0;
    channel.lastEventId = connection.lastEventId || channel.lastEventId || '';
    if (resumed) {
      channel.wasDown = false;
      announceRecovered(element, channel, detail);
    }
    refreshConnection();
  }

  function markChannelDown(element, state, detail) {
    if (!element || element.nodeType !== 1 || !element.isConnected) {
      return;
    }
    var channel = realtimeChannel(element);
    var connection = (detail && detail.connection) || {};
    channel.state = state;
    channel.wasDown = true;
    realtimeState.hadDrop = true;
    // A newer drop invalidates the "restored" notice immediately. | 新的掉线立即作废「已恢复」提示。
    clearConnectionNotice();
    if (connection.attempt > channel.attempt) {
      channel.attempt = connection.attempt;
    }
    refreshConnection();
  }

  function realtimeEventTarget(event) {
    var target = event && event.target;
    return target && target.nodeType === 1 ? target : null;
  }

  function realtimeEventDetail(event) {
    return (event && event.detail) || {};
  }

  function onSSEBefore(event) {
    var element = realtimeEventTarget(event);
    if (!element) {
      return;
    }
    var detail = realtimeEventDetail(event);
    var channel = realtimeChannel(element);
    channel.state = 'connecting';
    channel.attempt = (detail.connection && detail.connection.attempt) || 0;
    if (channel.attempt > 0) {
      channel.wasDown = true;
      realtimeState.hadDrop = true;
    }
    refreshConnection();
  }

  function onSSEAfter(event) {
    var element = realtimeEventTarget(event);
    if (element) {
      markChannelOpen(element, realtimeEventDetail(event));
    }
  }

  function onSSEClose(event) {
    var element = realtimeEventTarget(event);
    if (!element) {
      return;
    }
    var detail = realtimeEventDetail(event);
    var reason = detail.reason || '';
    if (reason === 'removed' || reason === 'cleanup' || reason === 'cancelled' || reason === 'message' || !element.isConnected) {
      delete element.__ydRealtime;
      refreshConnection();
      return;
    }
    // The extension turns a server-side end into another attempt; keep the banner in its
    // reconnecting state rather than raising a permanent failure. | 扩展会把服务端结束流变成下一次
    // 尝试；横幅因此保持「重连中」，而不是报成永久失败。
    markChannelDown(element, 'connecting', detail);
  }

  function onSSEError(event) {
    var element = realtimeEventTarget(event);
    if (!element) {
      return;
    }
    var detail = realtimeEventDetail(event);
    var status = detail.status || 0;
    // An error without a connection object is the initial request failing: the extension has no
    // retry loop for that path, so the honest state is "reconnection stopped", not "retrying". |
    // 没有连接对象的错误是首次请求失败：扩展在该路径上没有重试循环，因此应报「重连已停止」而不是「重连中」。
    var terminal = !detail.connection || status === 401 || status === 403 || status === 404;
    if (terminal && detail.connection && detail.connection.config) {
      // The extension would otherwise keep retrying a terminal status forever. | 否则扩展会对
      // 终止性状态码无限重试。
      detail.connection.config.reconnect = false;
    }
    markChannelDown(element, terminal ? 'failed' : 'connecting', detail);
  }

  function onWSBefore(event) {
    var element = realtimeEventTarget(event);
    if (!element) {
      return;
    }
    var detail = realtimeEventDetail(event);
    var channel = realtimeChannel(element);
    channel.state = 'connecting';
    channel.attempt = (detail.connection && detail.connection.attempt) || 0;
    if (channel.attempt > 0) {
      channel.wasDown = true;
      realtimeState.hadDrop = true;
    }
    refreshConnection();
  }

  function onWSAfter(event) {
    var element = realtimeEventTarget(event);
    if (element) {
      markChannelOpen(element, realtimeEventDetail(event));
    }
  }

  function onWSClose(event) {
    var element = realtimeEventTarget(event);
    if (!element) {
      return;
    }
    var detail = realtimeEventDetail(event);
    var reason = detail.reason || '';
    if (reason === 'removed' || reason === 'cancelled' || !element.isConnected) {
      delete element.__ydRealtime;
      refreshConnection();
      return;
    }
    var code = detail.code;
    var retryable = code === null || code === undefined || [1001, 1005, 1006, 1011, 1012, 1013, 1014].indexOf(code) >= 0;
    if (!retryable && detail.connection && detail.connection.config) {
      detail.connection.config.reconnect = false;
    }
    markChannelDown(element, retryable ? 'connecting' : 'failed', detail);
  }

  // onWebSocketError ignores a send failure while the socket is still open: htmx:ws:error also
  // fires when a composer cannot reach the socket, and that must not raise a disconnect alarm. |
  // onWebSocketError 会忽略套接字仍打开时的发送失败：发送区找不到套接字也会触发 htmx:ws:error，
  // 这不能报成断开。
  function onWebSocketError(event) {
    var detail = realtimeEventDetail(event);
    var connection = detail.connection || null;
    if (connection && connection.socket && connection.socket.readyState === 1) {
      return;
    }
    var element = realtimeEventTarget(event);
    if (element) {
      markChannelDown(element, 'connecting', detail);
    }
  }

  // retryRealtime forces the extensions to reconnect now instead of waiting for the next
  // backoff slot. The connection objects are the extensions' documented per-element state; when
  // they are absent (the channel was given up) a reload is the only safe recovery. |
  // retryRealtime 让扩展立刻重连，而不是等下一次退避。连接对象是扩展记录在元素上的状态；
  // 它们不存在时（通道已被放弃）重载页面是唯一安全的重连方式。
  function retryRealtime() {
    var retried = false;
    var failed = false;

    all('[hx-sse\\:connect]').forEach(function (element) {
      if (element.__ydRealtime && element.__ydRealtime.state === 'failed') {
        failed = true;
      }
    });
    all('[hx-ws\\:connect]').forEach(function (element) {
      if (element.__ydRealtime && element.__ydRealtime.state === 'failed') {
        failed = true;
      }
    });

    all('[hx-sse\\:connect]').forEach(function (element) {
      var connection = element._htmx && element._htmx.sse;
      if (!connection) {
        return;
      }
      retried = true;
      if (typeof connection.delayCanceller === 'function') {
        connection.delayCanceller();
      }
      if (connection.abortController && typeof connection.abortController.abort === 'function') {
        connection.abortController.abort();
      }
    });

    all('[hx-ws\\:connect]').forEach(function (element) {
      var connection = element._htmx && element._htmx.ws && element._htmx.ws.connection;
      if (!connection || !connection.socket) {
        return;
      }
      retried = true;
      try {
        // No status code: browsers only allow 1000 and 3000-4999 here, and a bare close
        // surfaces as 1005, which the extension reconnects on. | 不传状态码：浏览器在这里只允许
        // 1000 与 3000-4999，而无状态码的关闭会以 1005 上报，扩展正是据此重连。
        connection.socket.close();
      } catch (err) {
        /* a socket already leaving has nothing left to retry | 已在关闭的套接字无需再重试 */
      }
    });

    if (!retried || failed) {
      var d = win().document;
      if (d && d.location && typeof d.location.reload === 'function') {
        d.location.reload();
      }
      return;
    }

    realtimeState.hadDrop = true;
    refreshConnection();
  }

  function onRealtimeOnline() {
    var elements = realtimeElements();
    if (!elements.length) {
      return;
    }
    var down = false;
    elements.forEach(function (element) {
      if (realtimeChannel(element).state !== 'open') {
        down = true;
      }
    });
    if (!down) {
      refreshConnection();
      return;
    }
    realtimeState.hadDrop = true;
    retryRealtime();
    refreshConnection();
  }

  function onRealtimeOffline() {
    if (!realtimeElements().length) {
      return;
    }
    realtimeState.hadDrop = true;
    clearConnectionNotice();
    refreshConnection();
  }

  // pruneRealtime drops state for elements a swap removed, then repaints the aggregate. | pruneRealtime
  // 清掉已被交换移除的元素状态，再重绘聚合结果。
  function pruneRealtime() {
    var replaced = false;
    realtimeElements().forEach(function (element) {
      if (!element.__ydRealtime) {
        replaced = true;
      }
      if (!element.isConnected && element.__ydRealtime) {
        delete element.__ydRealtime;
      }
    });
    // New stream elements after a swap belong to a new page; a drop on the old page must not
    // produce a recovery notice on the new one. | 交换后的新流元素属于新页面；旧页面的掉线不得
    // 在新页面上产生恢复提示。
    if (replaced) {
      realtimeState.hadDrop = false;
    }
    refreshConnection();
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
    if (closest(target, '[data-yd-sidebar-open]')) {
      event.preventDefault();
      toggleMobileSidebar();
      return;
    }
    if (closest(target, '[data-yd-sidebar-backdrop]')) {
      event.preventDefault();
      applyMobileSidebar(false);
      return;
    }
    if (closest(target, '[data-yd-sidebar] a')) {
      applyMobileSidebar(false);
    }
    if (closest(target, '[data-yd-reconnect]')) {
      event.preventDefault();
      retryRealtime();
      return;
    }
    if (closest(target, '[data-yd-retry]')) {
      event.preventDefault();
      retryRealtime();
      return;
    }
    if (closest(target, '[data-yd-lang-toggle]')) {
      event.preventDefault();
      toggleLanguage();
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

  // toggleLanguage switches to the other shipped catalogue. Only zh-CN and en exist, so the next
  // language is derived from <html lang>; a server-rendered "next" attribute would go stale after
  // the workspace refetch, which does not rebuild the top bar.
  // toggleLanguage 切到另一种随包发布的语言。项目只有 zh-CN 与 en，目标语言因此从 <html lang>
  // 推导；服务端渲染的「下一个语言」属性会在工作区重取后过期，因为顶栏并不会随之重建。
  function toggleLanguage() {
    var d = win().document;
    var current = (d && d.documentElement && d.documentElement.lang) || '';
    var next = /^zh/i.test(current) ? 'en' : 'zh-CN';
    var api = i18n();
    if (api && typeof api.setLang === 'function') {
      api.setLang(next);
      formatCells(d);
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
    applyMobileSidebar(false);
  }

  // swapReplacedRail reports whether a swap replaced the shell itself. A metrics poll swaps a
  // node inside #yd-workspace and must leave an open rail alone; only a swap whose target
  // contains the rail (typically #yd-shell) needs the overlay reset. | swapReplacedRail 报告一次
  // 交换是否替换了外壳本身。指标轮询只交换 #yd-workspace 内的节点，必须保持已打开的侧栏；
  // 只有目标包含侧栏（通常是 #yd-shell）的交换才需要重置覆盖层。
  function swapReplacedRail(root) {
    var aside = sidebar();
    if (!aside || !root || root.nodeType !== 1) {
      return true;
    }
    return root.contains(aside);
  }

  // syncDocumentTitle keeps the tab title in step with a shell-only navigation. A shell swap
  // replaces the top bar but never reloads the document, so the browser would otherwise keep the
  // previous page's title. The project name comes from a body data attribute, not from parsing the
  // old title. | syncDocumentTitle 让标签页标题跟随只换外壳的导航。外壳交换只替换顶栏、不重载文档，
  // 不处理的话浏览器会一直显示上一页标题。项目名取自 body 上的数据属性，而不是解析旧标题。
  function syncDocumentTitle() {
    var heading = document.querySelector('.yd-topbar__title');
    if (!heading) {
      return;
    }
    var text = (heading.textContent || '').trim();
    if (!text) {
      return;
    }
    var name = (document.body.getAttribute('data-yd-app-name') || '').trim();
    document.title = name ? text + ' · ' + name : text;
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
    syncDocumentTitle();
    // A swap may replace the elements that own the streams; recompute the aggregate and drop
    // stale states so a navigation never leaves an orphaned reconnect banner behind. | 交换可能替换
    // 承载数据流的元素；重算聚合状态并清掉旧状态，导航因此不会留下无主的重连横幅。
    pruneRealtime();
    // A shell swap replaces the rail; the overlay state and scroll lock must not outlive it. |
    // 外壳交换会替换侧栏，覆盖层状态与滚动锁不得残留。
    if (swapReplacedRail(root)) {
      applyMobileSidebar(false);
    }
  }

  function bindShellEvents() {
    onDocument('click', onClick);
    // A viewport that grows past the drawer breakpoint must close the overlay, otherwise the
    // body scroll lock would survive a rotation or a window resize. | 视口放大到抽屉断点以上时
    // 必须关闭覆盖层，否则旋转或缩放后页面滚动锁会残留。
    win().addEventListener('resize', function () {
      var media = win().matchMedia;
      if (typeof media === 'function' && media('(min-width: 64rem)').matches) {
        applyMobileSidebar(false);
      }
    });
    onDocument('change', onChange);
    onDocument('keydown', onKeyDown);
    onDocument('htmx:after:swap', onAfterSwap);
    onDocument('htmx:sse:before:connection', onSSEBefore);
    onDocument('htmx:sse:after:connection', onSSEAfter);
    onDocument('htmx:sse:close', onSSEClose);
    onDocument('htmx:sse:error', onSSEError);
    onDocument('htmx:ws:before:connection', onWSBefore);
    onDocument('htmx:ws:after:connection', onWSAfter);
    onDocument('htmx:ws:close', onWSClose);
    onDocument('htmx:ws:error', onWebSocketError);
    win().addEventListener('online', onRealtimeOnline);
    win().addEventListener('offline', onRealtimeOffline);
    // Timers and half-open channels must not outlive the document. | 定时器与半开通道不得比文档活得更久。
    win().addEventListener('pagehide', clearConnectionNotice);
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
    refreshConnection();
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
    applyMobileSidebar(false);
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

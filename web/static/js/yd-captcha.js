/*!
 * yd-captcha.js — mounts the ydx captcha widget on every [data-yd-captcha] host.
 * yd-captcha.js — 在每个 [data-yd-captcha] 挂载点上初始化 ydx 验证码组件。
 *
 * The script owns only the glue: it loads the same-origin SDK on demand, writes the one-time token into the hidden
 * input, blocks a submit until the widget has verified, resets the widget after a refused request and follows the
 * application's theme and language. All user-facing copy comes from the template's data attributes and the SDK's own
 * catalogue, so this file contains no business text. | 本脚本只负责粘合：按需加载同源 SDK、把一次性令牌写入隐藏字段、
 * 在完成验证前阻止提交、请求被拒后重置组件，并跟随应用的主题与语言。所有面向用户的文案来自模板的 data 属性与
 * SDK 自带目录，因此本文件不包含任何业务文案。
 *
 * Hard rules | 硬规则:
 *   1. No dependency and no build step; the SDK is loaded from /captcha/assets on the same origin.
 *      无依赖、无构建；SDK 从同源的 /captcha/assets 加载。
 *   2. No inline script, no eval and no CDN: the generated CSP is script-src 'self'.
 *      不使用内联脚本、eval 或 CDN：生成项目的 CSP 是 script-src 'self'。
 *   3. A page without a captcha host loads only this small adapter; the SDK and its stylesheet are requested only when
 *      a host exists. | 没有验证码挂载点的页面只加载这个小适配器；SDK 与样式表仅在存在挂载点时才请求。
 */
(function () {
  'use strict';

  var doc = typeof document !== 'undefined' ? document : null;
  var win = typeof window !== 'undefined' ? window : null;
  if (!doc || !win) {
    return;
  }

  var SDK_URL = '/captcha/assets/yd-captcha.js';
  var CSS_URL = '/captcha/assets/yd-captcha.css';
  var HOST_SELECTOR = '[data-yd-captcha]';
  var MOUNT_SELECTOR = '[data-yd-captcha-host]';
  var ROOT_SELECTOR = '.yd-captcha';
  var STAGE_CONTROL_SELECTOR = '.yd-captcha__stage input, .yd-captcha__stage select, .yd-captcha__stage textarea';
  var TOKEN_SELECTOR = 'input[name="captcha_token"]';

  var records = [];
  var pendingResets = [];
  var resetTimer = 0;
  var sdkWaiters = [];
  var sdkLoading = false;
  var sdkFailed = false;
  var cssLoaded = false;

  /* ---------------------------------------------------------------- assets | 资产 */

  // loadCSS injects the SDK stylesheet once; a failure only costs the default styling. | loadCSS 只注入一次 SDK 样式表；
  // 加载失败只影响默认外观。
  function loadCSS() {
    if (cssLoaded || !doc.head) {
      return;
    }
    cssLoaded = true;
    var link = doc.createElement('link');
    link.rel = 'stylesheet';
    link.href = CSS_URL;
    link.setAttribute('data-yd-captcha-css', 'true');
    doc.head.appendChild(link);
  }

  // loadSDK injects the same-origin SDK once and fans the result out to every waiter. | loadSDK 只注入一次同源 SDK，
  // 并把结果分发给全部等待者。
  function loadSDK(done) {
    if (win.YdCaptcha && typeof win.YdCaptcha.mount === 'function') {
      done(null);
      return;
    }
    if (sdkFailed) {
      // The same-origin asset is embedded in the binary; a failure means the route is not mounted. Retrying on every
      // swap would only add duplicate script tags. | 同源资产随二进制内嵌；加载失败说明路由未挂载。每次交换都重试
      // 只会重复插入脚本标签。
      done(new Error('captcha SDK unavailable'));
      return;
    }
    sdkWaiters.push(done);
    if (sdkLoading) {
      return;
    }
    sdkLoading = true;
    var script = doc.createElement('script');
    script.src = SDK_URL;
    script.defer = true;
    script.setAttribute('data-yd-captcha-sdk', 'true');
    script.onload = function () {
      // A 200 response that does not publish YdCaptcha is just as unusable as a failed request; retrying on every
      // swap would stack one script tag per swap. | 返回 200 却没有发布 YdCaptcha 与加载失败同样不可用；每次交换
      // 都重试只会按交换次数堆叠脚本标签。
      if (!win.YdCaptcha || typeof win.YdCaptcha.mount !== 'function') {
        sdkFailed = true;
        flushSDK(new Error('captcha SDK unavailable'));
        return;
      }
      flushSDK(null);
    };
    script.onerror = function () {
      sdkFailed = true;
      flushSDK(new Error('captcha SDK unavailable'));
    };
    (doc.head || doc.documentElement).appendChild(script);
  }

  // flushSDK resolves every queued waiter exactly once. | flushSDK 恰好一次地完成全部排队的等待者。
  function flushSDK(err) {
    sdkLoading = false;
    var waiters = sdkWaiters;
    sdkWaiters = [];
    for (var i = 0; i < waiters.length; i++) {
      try {
        waiters[i](err);
      } catch (ignored) {
        /* one failed mount must not starve the others | 一个挂载失败不得阻塞其它挂载 */
      }
    }
  }

  /* ---------------------------------------------------------------- helpers | 辅助 */

  function all(selector, root) {
    var scope = root || doc;
    if (!scope || typeof scope.querySelectorAll !== 'function') {
      return [];
    }
    return Array.prototype.slice.call(scope.querySelectorAll(selector));
  }

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

  function connected(node) {
    return !!(node && doc.documentElement && doc.documentElement.contains(node));
  }

  function currentLocale() {
    var lang = doc.documentElement ? doc.documentElement.getAttribute('lang') : '';
    if (!lang && win.navigator) {
      lang = win.navigator.language || '';
    }
    return lang || 'zh-CN';
  }

  function currentTheme() {
    var theme = doc.documentElement ? doc.documentElement.getAttribute('data-theme') : '';
    theme = (theme || 'auto').toLowerCase();
    if (theme === 'system') {
      return 'auto';
    }
    return theme === 'light' || theme === 'dark' ? theme : 'auto';
  }

  function tokenInput(host) {
    var id = host.getAttribute('data-yd-captcha-input');
    if (id) {
      var byID = doc.getElementById(id);
      if (byID) {
        return byID;
      }
    }
    var form = closest(host, 'form');
    return form ? form.querySelector(TOKEN_SELECTOR) : null;
  }

  function errorNode(host) {
    return host.querySelector('.yd-field__error');
  }

  function sdkTarget(host) {
    return (host && host.querySelector(MOUNT_SELECTOR)) || host;
  }

  function sdkRoot(host) {
    var target = sdkTarget(host);
    if (!target || typeof target.querySelector !== 'function') {
      return null;
    }
    return target.querySelector(ROOT_SELECTOR);
  }

  // purgeRoots removes SDK roots that were cloned from a history snapshot; mounting on top of them would leave two
  // widgets in one host. | purgeRoots 移除历史快照克隆出来的 SDK 根节点；在它上面再挂载会让一个挂载点出现两个组件。
  function purgeRoots(host) {
    var target = sdkTarget(host);
    if (!target || typeof target.querySelectorAll !== 'function') {
      return;
    }
    var roots = target.querySelectorAll(ROOT_SELECTOR);
    for (var i = 0; i < roots.length; i++) {
      if (roots[i].parentNode) {
        roots[i].parentNode.removeChild(roots[i]);
      }
    }
  }

  function stageControl(host) {
    var target = sdkTarget(host);
    if (!target || typeof target.querySelector !== 'function') {
      return null;
    }
    return target.querySelector(STAGE_CONTROL_SELECTOR);
  }

  function labelText(host) {
    var id = host.getAttribute('data-yd-captcha-input');
    var label = id ? doc.getElementById(id + '-label') : null;
    return label && label.textContent ? String(label.textContent).replace(/\s+/g, ' ').trim() : '';
  }

  // labelRoot replaces the SDK's hard-coded English aria-label with the page's localized label. |
  // labelRoot 用页面本地化的标签替换 SDK 硬编码的英文 aria-label。
  function labelRoot(host) {
    var root = sdkRoot(host);
    if (!root) {
      return;
    }
    var label = labelText(host);
    if (label) {
      root.setAttribute('aria-label', label);
    } else {
      root.removeAttribute('aria-label');
    }
  }

  // describeControl links the SDK-created answer control to the visible hint/error and marks it required. |
  // describeControl 把 SDK 创建的作答控件关联到可见提示/错误，并标记为必填。
  function describeControl(host) {
    var control = stageControl(host);
    if (!control) {
      return;
    }
    control.setAttribute('aria-required', 'true');
    var base = host.getAttribute('data-yd-captcha-input');
    if (!base) {
      return;
    }
    var ids = [];
    if (doc.getElementById(base + '-hint')) {
      ids.push(base + '-hint');
    }
    if (doc.getElementById(base + '-error')) {
      ids.push(base + '-error');
    }
    if (ids.length) {
      control.setAttribute('aria-describedby', ids.join(' '));
    }
    if (host.getAttribute('data-invalid')) {
      control.setAttribute('aria-invalid', 'true');
    }
  }

  function setInvalid(host, invalid) {
    var control = stageControl(host);
    if (!control) {
      return;
    }
    if (invalid) {
      control.setAttribute('aria-invalid', 'true');
    } else {
      control.removeAttribute('aria-invalid');
    }
  }

  function showError(host, message) {
    if (!host || !message) {
      return;
    }
    var node = errorNode(host);
    if (node) {
      node.textContent = message;
    }
    host.setAttribute('data-invalid', 'true');
    setInvalid(host, true);
  }

  function clearError(host) {
    if (!host) {
      return;
    }
    var node = errorNode(host);
    if (node) {
      node.textContent = '';
    }
    host.removeAttribute('data-invalid');
    setInvalid(host, false);
  }

  function dispatch(node, name, detail) {
    if (!node || typeof win.CustomEvent !== 'function') {
      return;
    }
    try {
      node.dispatchEvent(new win.CustomEvent(name, { bubbles: true, detail: detail || {} }));
    } catch (ignored) {
      /* an old engine without CustomEvent simply skips the notification | 没有 CustomEvent 的旧引擎跳过通知 */
    }
  }

  function recordFor(host) {
    for (var i = 0; i < records.length; i++) {
      if (records[i].host === host) {
        return records[i];
      }
    }
    return null;
  }

  function removeRecord(record) {
    var index = records.indexOf(record);
    if (index >= 0) {
      records.splice(index, 1);
    }
  }

  /* ---------------------------------------------------------------- mount | 挂载 */

  function mountHost(host) {
    if (!host || recordFor(host)) {
      return;
    }
    // A restored history snapshot can carry a cloned SDK root; mounting on top of it would show two widgets. A host
    // without a record has no trusted token either, so any value left in its hidden field is a snapshot leftover. |
    // 恢复的历史快照可能带着克隆的 SDK 根节点；在它上面再次挂载会显示两个组件。没有记录的新宿主也没有可信令牌，
    // 隐藏字段里残留的值只可能来自快照。
    purgeRoots(host);
    var orphan = hostToken(host);
    if (orphan && orphan.value) {
      orphan.value = '';
      host.removeAttribute('data-yd-captcha-verified');
    }
    var target = sdkTarget(host);
    var action = host.getAttribute('data-yd-captcha-action') || 'default';
    var endpoint = host.getAttribute('data-yd-captcha-endpoint') || '/captcha';
    var locale = currentLocale();
    var theme = currentTheme();
    var previousFocus = doc.activeElement;
    // record is created before the SDK callbacks so they can follow an hx-preserve adoption to the new host instead of
    // writing the token and dispatching events on the detached old one. | record 在 SDK 回调之前创建，使回调能跟随
    // hx-preserve 归入新宿主，而不是把令牌与事件写到已脱离文档的旧宿主上。
    var record = {
      host: host,
      target: target,
      widget: null,
      action: action,
      locale: locale,
      theme: theme,
      closed: false
    };
    try {
      record.widget = win.YdCaptcha.mount(target, {
        endpoint: endpoint,
        action: action,
        locale: locale,
        theme: theme,
        onSuccess: function (token) {
          if (record.closed || (record.widget && record.widget.destroyed)) {
            return;
          }
          var liveInput = hostToken(record.host);
          if (liveInput) {
            liveInput.value = token;
          }
          clearError(record.host);
          record.host.setAttribute('data-yd-captcha-verified', 'true');
          dispatch(record.host, 'yd:captcha:verified', { token: token, action: action });
        },
        onError: function () {
          if (record.closed || (record.widget && record.widget.destroyed)) {
            return;
          }
          var liveInput = hostToken(record.host);
          if (liveInput) {
            liveInput.value = '';
          }
          record.host.removeAttribute('data-yd-captcha-verified');
        },
        onReady: function () {
          if (record.closed || (record.widget && record.widget.destroyed)) {
            return;
          }
          // The SDK creates the answer control while rendering the challenge; label and required semantics can only be
          // attached after that. | SDK 在渲染挑战时创建作答控件；标签与必填语义只能在那之后挂上。
          describeControl(record.host);
          labelRoot(record.host);
        }
      });
    } catch (err) {
      showError(host, host.getAttribute('data-yd-captcha-unavailable') || '');
      return;
    }
    records.push(record);
    describeControl(host);
    labelRoot(host);
    // The SDK focuses its answer input while mounting. If that steals focus from the user name or another field the
    // user was typing in, give it back; a fresh page (body focused) keeps the SDK focus. |
    // SDK 在挂载时会聚焦作答输入框。若它抢走了用户名或用户正在输入的字段，就把焦点还回去；全新页面（焦点在 body）
    // 保留 SDK 的聚焦。
    var focused = doc.activeElement;
    if (previousFocus && previousFocus !== doc.body && previousFocus.tagName &&
        (previousFocus.tagName === 'INPUT' || previousFocus.tagName === 'TEXTAREA' || previousFocus.tagName === 'SELECT') &&
        !host.contains(previousFocus) && focused && target.contains(focused)) {
      try {
        previousFocus.focus();
      } catch (ignored) {
        /* a removed field cannot take focus back | 已移除的字段无法收回焦点 */
      }
    }
    // The text challenge focuses its answer only after the challenge request returns, so the synchronous restore
    // above is too early. Watch the first focus inside the target for a short window; a user click there also ends the
    // guard, so a later intentional focus is never stolen. | 文本题在挑战请求返回后才聚焦作答框，上面的同步恢复太早。
    // 在短窗口内观察目标里的第一次聚焦；用户自己点进去也会结束守卫，之后的有意聚焦不会被抢走。
    if (previousFocus && previousFocus !== doc.body && previousFocus.tagName &&
        (previousFocus.tagName === 'INPUT' || previousFocus.tagName === 'TEXTAREA' || previousFocus.tagName === 'SELECT') &&
        !host.contains(previousFocus) && typeof target.addEventListener === 'function') {
      var restore = function (event) {
        var el = event && event.target;
        if (!el || (typeof target.contains === 'function' && !target.contains(el))) {
          return;
        }
        target.removeEventListener('focusin', restore, true);
        try {
          previousFocus.focus();
        } catch (ignored) {
          /* a removed field cannot take focus back | 已移除的字段无法收回焦点 */
        }
      };
      target.addEventListener('focusin', restore, true);
      setTimeout(function () {
        target.removeEventListener('focusin', restore, true);
      }, 1500);
    }
  }

  // mountAll mounts every host in the document; the SDK load is shared by all of them. | mountAll 挂载文档中的每个
  // 挂载点；SDK 加载由全部挂载点共享。
  function mountAll() {
    var hosts = all(HOST_SELECTOR);
    if (!hosts.length) {
      return;
    }
    loadCSS();
    loadSDK(function (err) {
      var current = all(HOST_SELECTOR);
      if (err) {
        for (var i = 0; i < current.length; i++) {
          showError(current[i], current[i].getAttribute('data-yd-captcha-unavailable') || '');
        }
        return;
      }
      for (var j = 0; j < current.length; j++) {
        if (connected(current[j])) {
          mountHost(current[j]);
        }
      }
    });
  }

  function destroyRecord(record) {
    record.closed = true;
    try {
      record.widget.destroy();
    } catch (ignored) {
      /* a destroyed widget may already have removed its DOM | 已销毁的组件可能已经移除自己的 DOM */
    }
    removeRecord(record);
  }

  // resetHostState destroys the widget and clears its token without mounting a replacement. htmx swaps call it before
  // mountAll, so one swap produces exactly one fresh challenge. | resetHostState 销毁组件并清掉令牌，但不立即重建。
  // htmx 交换会先调用它、再由 mountAll 统一挂载，因此一次交换只产生一道新题。
  function resetHostState(host) {
    if (!host) {
      return;
    }
    var record = recordFor(host);
    if (record) {
      destroyRecord(record);
    }
    purgeRoots(host);
    var input = hostToken(host);
    if (input) {
      input.value = '';
    }
    host.removeAttribute('data-yd-captcha-verified');
    clearError(host);
  }

  // resetHost destroys and re-mounts one host: the one-time token was consumed or refused. | resetHost 销毁并重新挂载
  // 一个挂载点：一次性令牌已被消费或被拒绝。
  function resetHost(host) {
    if (!host) {
      return;
    }
    resetHostState(host);
    if (connected(host)) {
      mountHost(host);
    }
  }

  // queueReset defers the reset until htmx has performed the swap. htmx:after:request fires *before* the swap, so
  // resetting immediately would open a challenge on a host that the swap is about to remove. | queueReset 把重置推迟到
  // htmx 完成交换之后。htmx:after:request 在交换之前触发；立即重置会在马上被移除的挂载点上开一道无用的题。
  function queueReset(host) {
    if (!host) {
      return;
    }
    if (pendingResets.indexOf(host) < 0) {
      pendingResets.push(host);
    }
    if (resetTimer) {
      return;
    }
    resetTimer = setTimeout(function () {
      resetTimer = 0;
      var pending = pendingResets;
      pendingResets = [];
      for (var i = 0; i < pending.length; i++) {
        var live = liveHostFor(pending[i]);
        if (live && hostToken(live)) {
          resetHost(live);
        }
      }
    }, 0);
  }

  // liveHostFor finds the host that now owns a queued request's preserved mount point. A login 422 replaces the shell
  // but hx-preserve moves the mount point into the new host, so the old host is disconnected while the token and widget
  // live on under the same input id. | liveHostFor 找到排队请求的保留挂载点现在所属的宿主。登录 422 会替换外壳，
  // 但 hx-preserve 会把挂载点移进新宿主：旧宿主已脱离文档，令牌与组件却在相同 input id 下继续存在。
  function liveHostFor(host) {
    if (!host) {
      return null;
    }
    var id = host.getAttribute('data-yd-captcha-input');
    if (!id) {
      return connected(host) ? host : null;
    }
    var fallback = null;
    var hosts = all(HOST_SELECTOR);
    for (var i = 0; i < hosts.length; i++) {
      if (hosts[i].getAttribute('data-yd-captcha-input') !== id) {
        continue;
      }
      // Prefer the host that actually owns the token field: after hx-preserve moved the mount point, the old host may
      // still be connected for a moment while the input already lives in the new one. | 优先选择真正持有令牌字段的宿主：
      // hx-preserve 移动挂载点后，旧宿主可能还短暂留在文档里，而输入字段已经在新宿主中。
      if (hostToken(hosts[i])) {
        return hosts[i];
      }
      if (!fallback) {
        fallback = hosts[i];
      }
    }
    if (hostToken(host)) {
      return host;
    }
    return fallback;
  }

  // hostToken returns the token input that belongs to this host, not merely one that shares its id. | hostToken 返回
  // 属于该挂载点的令牌字段，而不是仅凭 id 命中的另一个表单字段。
  function hostToken(host) {
    var input = tokenInput(host);
    if (!input || !host.contains(input)) {
      return null;
    }
    return input;
  }

  // adopt keeps a widget whose host was replaced but whose hx-preserve mount point moved into the new host. A language
  // switch replaces the whole shell; the preserved mount point carries the solved token and the typed answer, so the
  // record follows it instead of being destroyed. | adopt 让组件在宿主被替换、但 hx-preserve 挂载点被移动到新宿主后继续
  // 存活。语言切换会替换整块外壳；保留的挂载点带着已完成的令牌与已输入的答案，因此记录跟随它而不是被销毁。
  function adopt(record) {
    if (!record || connected(record.host)) {
      return true;
    }
    if (!connected(record.target)) {
      return false;
    }
    var newHost = closest(record.target, HOST_SELECTOR);
    if (!newHost) {
      return false;
    }
    record.host = newHost;
    record.target = sdkTarget(newHost);
    return true;
  }

  // sweep drops records whose host and preserved mount point both left the document after an htmx swap. |
  // sweep 清理宿主与保留挂载点都已离开文档的 htmx 交换记录。
  function sweep() {
    for (var i = records.length - 1; i >= 0; i--) {
      if (!connected(records[i].host) && !adopt(records[i])) {
        destroyRecord(records[i]);
      }
    }
  }

  function refreshTheme() {
    var theme = currentTheme();
    for (var i = 0; i < records.length; i++) {
      var record = records[i];
      if (!connected(record.host) || record.theme === theme) {
        continue;
      }
      record.theme = theme;
      try {
        record.widget.setTheme(theme);
      } catch (ignored) {
        /* a torn-down widget must not break the theme switch | 已拆除的组件不得中断主题切换 */
      }
    }
  }

  function refreshLocale() {
    var locale = currentLocale();
    for (var i = 0; i < records.length; i++) {
      var record = records[i];
      if (!connected(record.host)) {
        continue;
      }
      var input = hostToken(record.host);
      if (record.locale === locale) {
        labelRoot(record.host);
        continue;
      }
      record.locale = locale;
      // A verified token stays valid across a language switch; only an unsolved widget restarts. | 已完成的令牌在语言
      // 切换后仍然有效；只有尚未完成的组件重新出题。
      if (input && input.value) {
        labelRoot(record.host);
        continue;
      }
      try {
        record.widget.setLocale(locale);
      } catch (ignored) {
        /* the next swap remounts it with the new locale | 下一次交换会以新语言重新挂载 */
      }
      labelRoot(record.host);
    }
  }

  function refreshLabels() {
    for (var i = 0; i < records.length; i++) {
      if (connected(records[i].host)) {
        labelRoot(records[i].host);
      }
    }
  }

  function syncVerified(host) {
    if (!host) {
      return;
    }
    var input = hostToken(host);
    if (input && input.value) {
      host.setAttribute('data-yd-captcha-verified', 'true');
    } else {
      host.removeAttribute('data-yd-captcha-verified');
    }
  }

  /* ---------------------------------------------------------------- forms | 表单 */

  // onSubmit blocks a captcha-protected form until the widget produced a token. The listener sits on window capture,
  // which runs before htmx, the confirm dialog and the form's own listeners. | onSubmit 在组件产出令牌之前阻止受保护表单
  // 提交。监听器挂在 window 捕获阶段，先于 htmx、确认对话框与表单自身的监听器执行。
  function onSubmit(event) {
    var form = event.target;
    if (!form || !form.querySelector) {
      return;
    }
    var host = form.querySelector(HOST_SELECTOR);
    if (!host) {
      return;
    }
    // Validate the ordinary required fields first: the server checks the captcha before the password, so submitting a
    // blank user name would otherwise burn a solved challenge. | 先校验普通必填字段：服务端在口令之前校验验证码，
    // 否则空用户名提交会白白消耗一道已完成的题。
    if (typeof form.checkValidity === 'function' && !form.checkValidity()) {
      event.preventDefault();
      event.stopImmediatePropagation();
      if (typeof form.reportValidity === 'function') {
        form.reportValidity();
      }
      return;
    }
    var input = hostToken(host);
    if (input && input.value) {
      return;
    }
    event.preventDefault();
    event.stopImmediatePropagation();
    // A failed SDK load is an outage, not an unsolved challenge; use the matching localized copy. | SDK 加载失败是
    // 服务不可用而不是尚未完成验证，使用对应的本地化文案。
    var message = host.getAttribute('data-yd-captcha-required') || '';
    if (sdkFailed) {
      message = host.getAttribute('data-yd-captcha-unavailable') || message;
    }
    showError(host, message);
    // Focus the answer control or the verify button, never the refresh button in the action row. |
    // 聚焦作答控件或验证按钮，绝不聚焦操作行里的“换一张”。
    var control = stageControl(host);
    if (!control) {
      var target = sdkTarget(host);
      control = target && typeof target.querySelector === 'function'
        ? target.querySelector('.yd-captcha__actions button:last-child')
        : null;
    }
    if (!control) {
      control = host.querySelector('button, input, select, textarea');
    }
    if (control && typeof control.focus === 'function') {
      control.focus();
    }
  }

  // onAfterRequest schedules a reset after a refused request: the token was consumed or rejected. The reset waits for
  // the swap so a host that htmx is about to replace never opens a throw-away challenge. | onAfterRequest 在请求被拒后
  // 安排重置：令牌已被消费或拒绝。重置会等待交换完成，因此即将被 htmx 替换的挂载点不会白开一道题。
  function onAfterRequest(event) {
    var detail = event.detail || {};
    var ctx = detail.ctx || {};
    var status = typeof ctx.status === 'number' ? ctx.status : (ctx.response && ctx.response.status);
    if (typeof status !== 'number') {
      status = detail.xhr && detail.xhr.status;
    }
    if (typeof status !== 'number' || status < 400) {
      return;
    }
    var elt = ctx.sourceElement || detail.elt || (detail.requestConfig && detail.requestConfig.elt);
    if (!elt || !elt.querySelector) {
      return;
    }
    var host = elt.querySelector(HOST_SELECTOR) || closest(elt, HOST_SELECTOR);
    if (host && connected(host)) {
      queueReset(host);
    }
  }

  // remountStale resets only a record whose mount point or SDK root actually disappeared. A live unsolved widget is
  // left alone: an unrelated swap elsewhere on the page must not throw away the answer the user is working on. |
  // remountStale 只重置挂载点或 SDK 根节点确实消失的记录。仍然存活的未完成组件保持原样：页面上其它位置的无关交换
  // 不得丢掉用户正在作答的答案。
  function remountStale() {
    var hosts = all(HOST_SELECTOR);
    for (var i = 0; i < hosts.length; i++) {
      var host = hosts[i];
      if (!connected(host)) {
        continue;
      }
      var record = recordFor(host);
      if (!record) {
        continue;
      }
      var input = hostToken(host);
      if (input && input.value) {
        continue;
      }
      var target = sdkTarget(host);
      if (!connected(target) || record.target !== target || !sdkRoot(host)) {
        resetHostState(host);
      }
    }
  }

  function onAfterSwap() {
    // sweep first: it adopts a preserved mount point into its new host before pending resets map old hosts by id. |
    // 先做 sweep：它把保留挂载点归到新宿主，随后待重置列表才能按 id 找到旧请求对应的新宿主。
    sweep();
    // A refused request consumed or rejected the token even when htmx kept the form in place, so those hosts must be
    // cleared before remountStale decides whether a token still counts as solved. | 被拒的请求已经消费或拒绝了令牌，
    // 即使 htmx 保留了表单也必须先清掉；随后 remountStale 才能正确判断令牌是否仍然有效。
    var pending = pendingResets;
    pendingResets = [];
    for (var i = 0; i < pending.length; i++) {
      var host = liveHostFor(pending[i]);
      if (host && hostToken(host)) {
        resetHostState(host);
      }
    }
    remountStale();
    mountAll();
    var hosts = all(HOST_SELECTOR);
    for (var j = 0; j < hosts.length; j++) {
      if (connected(hosts[j])) {
        syncVerified(hosts[j]);
      }
    }
    refreshTheme();
    refreshLocale();
    refreshLabels();
  }

  // onHistoryRestore rebuilds every captcha after the history-cache extension restores a snapshot. The snapshot holds a
  // cloned SDK root and the hidden token; mounting over the clone would show two widgets and letting the token survive
  // would pass the submit guard with a token that may already be consumed. |
  // onHistoryRestore 在历史缓存扩展恢复快照后重建每个验证码。快照带着克隆的 SDK 根节点与隐藏令牌；在克隆上再挂载会
  // 显示两个组件，保留旧令牌则会让提交守卫用一枚可能已被消费的令牌放行。
  function onHistoryRestore() {
    pendingResets = [];
    sweep();
    var hosts = all(HOST_SELECTOR);
    for (var i = 0; i < hosts.length; i++) {
      var host = hosts[i];
      var record = recordFor(host);
      if (record) {
        destroyRecord(record);
      }
      purgeRoots(host);
      resetHostState(host);
    }
    mountAll();
    refreshTheme();
    refreshLocale();
    refreshLabels();
  }

  /* ---------------------------------------------------------------- boot | 启动 */

  function ready() {
    mountAll();
    if (win.MutationObserver && doc.documentElement) {
      new win.MutationObserver(function () {
        refreshTheme();
        refreshLocale();
      }).observe(doc.documentElement, { attributes: true, attributeFilter: ['data-theme', 'lang'] });
    }
    doc.addEventListener('yd:theme', refreshTheme);
  }

  win.addEventListener('submit', onSubmit, true);
  doc.addEventListener('htmx:after:request', onAfterRequest);
  doc.addEventListener('htmx:afterRequest', onAfterRequest);
  doc.addEventListener('htmx:after:swap', onAfterSwap);
  doc.addEventListener('htmx:afterSwap', onAfterSwap);
  doc.addEventListener('htmx:history:cache:after:restore', onHistoryRestore);

  if (doc.readyState === 'loading') {
    doc.addEventListener('DOMContentLoaded', ready, { once: true });
  } else {
    ready();
  }

  win.ydCaptcha = {
    mountAll: mountAll,
    reset: resetHost
  };
})();

/*!
 * yd-captcha.js — mounts the ydx captcha widget on every [data-yd-captcha] host.
 * yd-captcha.js — 在每个 [data-yd-captcha] 挂载点上初始化 ydx 验证码组件。
 *
 * The script owns only the glue: it loads the same-origin SDK on demand, mounts the widget lazily when a dialog opens,
 * writes the one-time token into the hidden input, closes the dialog and re-submits the original form after a
 * successful verification, resets the widget after a refused request and follows the application's theme and
 * language. All user-facing copy comes from the template's data attributes and the SDK's own catalogue, so this file
 * contains no business text. | 本脚本只负责粘合：按需加载同源 SDK、在对话框打开时惰性挂载组件、把一次性令牌写入
 * 隐藏字段、验证成功后关闭对话框并重新提交原表单、请求被拒后重置组件，并跟随应用的主题与语言。所有面向用户的
 * 文案来自模板的 data 属性与 SDK 自带目录，因此本文件不包含任何业务文案。
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
  var DIALOG_SELECTOR = 'dialog[data-yd-captcha-dialog]';
  var MOUNT_SELECTOR = '[data-yd-captcha-host]';
  var ROOT_SELECTOR = '.yd-captcha';
  var STAGE_CONTROL_SELECTOR = '.yd-captcha__stage input, .yd-captcha__stage select, .yd-captcha__stage textarea';
  var VERIFY_SELECTOR = '.yd-captcha__actions .yd-captcha__button--primary';
  var TOKEN_SELECTOR = 'input[name="captcha_token"]';

  var records = [];
  var pendingResets = [];
  var resetTimer = 0;
  var sdkWaiters = [];
  var sdkLoading = false;
  var sdkFailed = false;
  var cssLoaded = false;
  // pendingSubmission is the form that opened a captcha dialog. It is re-submitted after the SDK reports a token. |
  // pendingSubmission 是打开验证码对话框的那张表单；SDK 产出令牌后会重新提交它。
  var pendingSubmission = null;

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

  // dialogFor returns the native captcha dialog that owns a host. Older inline hosts have none, which keeps the
  // fallback path (and projects generated before the dialog existed) working unchanged. | dialogFor 返回拥有该挂载点的
  // 原生验证码对话框。旧的页内挂载点没有对话框，因此回退路径（以及对话框出现前生成的项目）保持原样。
  function dialogFor(host) {
    if (!host) {
      return null;
    }
    if (host.getAttribute && host.getAttribute('data-yd-captcha-dialog') !== null) {
      return host;
    }
    return closest(host, DIALOG_SELECTOR);
  }

  function dialogOpen(dialog) {
    return !!(dialog && (dialog.open || (dialog.hasAttribute && dialog.hasAttribute('open'))));
  }

  // openDialog shows the native modal. If showModal is unavailable the open attribute still exposes the dialog in
  // normal flow; the submit guard never treats that failure as verification. | openDialog 显示原生模态框。若 showModal
  // 不可用，open 属性仍让对话框按普通流显示；提交守卫绝不会把这种失败当成验证通过。
  function openDialog(dialog) {
    if (!dialog) {
      return false;
    }
    if (dialogOpen(dialog)) {
      return true;
    }
    try {
      if (typeof dialog.showModal === 'function') {
        dialog.showModal();
      } else {
        dialog.setAttribute('open', '');
      }
    } catch (err) {
      dialog.setAttribute('open', '');
    }
    return dialogOpen(dialog);
  }

  function closeDialog(dialog) {
    if (!dialog) {
      return;
    }
    try {
      if (typeof dialog.close === 'function' && dialog.open) {
        dialog.close();
        return;
      }
    } catch (ignored) {
      /* fall through to the attribute fallback | 继续走属性回退 */
    }
    dialog.removeAttribute('open');
  }

  // focusCaptcha puts focus on the answer control, then the SDK verify button and finally the close control. This is
  // what makes the modal usable with a keyboard as soon as it opens. | focusCaptcha 依次把焦点放在作答控件、SDK
  // 验证按钮与关闭控件上；对话框一打开就可以用键盘操作。
  function focusCaptcha(host) {
    var target = sdkTarget(host);
    var control = stageControl(host);
    if (!control && target && typeof target.querySelector === 'function') {
      control = target.querySelector(VERIFY_SELECTOR);
    }
    if (!control && typeof host.querySelector === 'function') {
      control = host.querySelector('.yd-captcha-dialog__close');
    }
    if (control && typeof control.focus === 'function') {
      control.focus();
    }
  }

  // cancelDialog closes without submitting and remembers the challenge: reopening the dialog keeps the widget and a
  // typed answer, so the user does not have to solve a second challenge after a mis-click. | cancelDialog 关闭对话框
  // 但不提交，并保留挑战：再次打开时组件与已输入的答案仍在，用户误触后不需要重做一道题。
  function cancelDialog(dialog) {
    var pending = pendingSubmission;
    pendingSubmission = null;
    closeDialog(dialog);
    // Focus the element that opened the dialog (the username field after Enter, or the submit button after a click),
    // after close(): the browser restores focus while closing and would otherwise overwrite a focus call made before
    // close() and leave the user on <body>. | 在 close() 之后把焦点还给打开对话框的元素（回车时的用户名输入，或点击时
    // 的提交按钮），因为浏览器会在关闭过程中恢复焦点；在 close() 之前聚焦会被覆盖，用户会停在 <body>。
    var focusTarget = null;
    if (pending && pending.dialog === dialog) {
      if (pending.opener && connected(pending.opener)) {
        focusTarget = pending.opener;
      } else if (pending.submitter && connected(pending.submitter)) {
        focusTarget = pending.submitter;
      }
    }
    if (focusTarget && typeof focusTarget.focus === 'function') {
      try {
        focusTarget.focus();
      } catch (ignored) {
        /* a removed opener cannot take focus back | 已移除的打开者无法收回焦点 */
      }
    }
  }

  // bindDialog attaches the cancel/backdrop/close handlers once per dialog. The handlers live on the dialog instead of
  // the form because hx-preserve may move the mount point into a replacement dialog after a language switch. |
  // bindDialog 为每个对话框只绑定一次取消、遮罩与关闭处理器。处理器挂在对话框而不是表单上：语言切换后
  // hx-preserve 可能把挂载点移进新的对话框。
  function bindDialog(host) {
    var dialog = dialogFor(host);
    if (!dialog || dialog.__ydCaptchaBound || typeof dialog.addEventListener !== 'function') {
      return;
    }
    // Use an expando rather than an attribute: history-cache snapshots clone the dialog and its attributes but not
    // listeners, so an attribute would tell the next mount that handlers already existed when the clone has none. |
    // 用 expando 而不是属性：历史缓存会克隆对话框与其属性，却不会克隆监听器；属性会让下一次挂载误以为处理器
    // 已经存在，而克隆节点实际上没有任何处理器。
    dialog.__ydCaptchaBound = true;
    dialog.addEventListener('cancel', function (event) {
      if (event && typeof event.preventDefault === 'function') {
        event.preventDefault();
      }
      cancelDialog(dialog);
    });
    dialog.addEventListener('click', function (event) {
      var target = event && event.target;
      if (target === dialog) {
        cancelDialog(dialog);
        return;
      }
      var closer = target ? closest(target, '[data-yd-captcha-cancel]') : null;
      if (closer && dialog.contains && dialog.contains(closer)) {
        if (typeof event.preventDefault === 'function') {
          event.preventDefault();
        }
        cancelDialog(dialog);
      }
    });
    dialog.addEventListener('close', function () {
      if (pendingSubmission && pendingSubmission.dialog === dialog) {
        pendingSubmission = null;
      }
    });
  }

  function closeOpenDialogs() {
    pendingSubmission = null;
    var dialogs = all(DIALOG_SELECTOR);
    for (var i = 0; i < dialogs.length; i++) {
      closeDialog(dialogs[i]);
    }
  }

  // deferMount keeps a closed dialog from fetching a challenge at page load: the default two-minute challenge TTL
  // would otherwise expire while the user is still filling in the form, and an invisible challenge would waste a
  // round trip. | deferMount 阻止关闭的对话框在页面加载时请求挑战：默认两分钟的挑战 TTL 会在用户填写表单期间过期，
  // 隐藏的挑战还会白白消耗一次往返。
  function deferMount(host) {
    var dialog = dialogFor(host);
    return !!dialog && !dialogOpen(dialog) && !recordFor(host);
  }

  // ensureMounted mounts a dialog's widget on first use, loading the SDK if page load has not finished yet. |
  // ensureMounted 在首次使用时挂载对话框组件；页面加载尚未完成时先等待 SDK。
  function ensureMounted(host) {
    if (!host) {
      return;
    }
    bindDialog(host);
    if (recordFor(host)) {
      return;
    }
    loadCSS();
    if (sdkFailed) {
      showError(host, host.getAttribute('data-yd-captcha-unavailable') || '');
      return;
    }
    if (win.YdCaptcha && typeof win.YdCaptcha.mount === 'function') {
      mountHost(host);
      return;
    }
    loadSDK(function (err) {
      if (err) {
        showError(host, host.getAttribute('data-yd-captcha-unavailable') || '');
        return;
      }
      // The user may have cancelled while the SDK was loading; mount only when the dialog is still open. |
      // 用户在 SDK 加载期间可能已取消；只有对话框仍打开时才挂载。
      if (connected(host) && dialogOpen(dialogFor(host))) {
        mountHost(host);
      }
    });
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

  // hasUserAnswer reports whether a closed widget already holds input. Pointer challenges expose no stage control,
  // so they are treated as potentially in progress and preserved. | hasUserAnswer 报告关闭的组件是否已有输入。
  // 指针题没有作答控件，因此按可能已有进度处理并保留。
  function hasUserAnswer(host) {
    var control = stageControl(host);
    if (!control) {
      return true;
    }
    if (typeof control.value !== 'string') {
      return true;
    }
    return control.value.trim() !== '';
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
    if (!host) {
      return;
    }
    bindDialog(host);
    if (recordFor(host)) {
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
    // A dialog is mounted after showModal(), while focus already belongs inside the top layer. Restoring the field
    // that was active before the dialog opened would move focus behind the modal, so only legacy inline hosts keep
    // the old focus-restoration path. | 对话框在 showModal() 之后挂载，焦点已属于顶层；把焦点还给打开对话框前的字段
    // 会让焦点跑到模态框背后，因此只有旧的页内挂载点保留原来的焦点恢复逻辑。
    var previousFocus = dialogFor(host) ? null : doc.activeElement;
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
          // completeSubmission captures the pending submit before it clears it and closes the dialog. Closing first
          // would let a synchronous `close` event clear pendingSubmission and silently drop the submit. If nothing is
          // pending (for example a solve triggered by a restored widget), close the solved modal afterwards so the
          // user does not have to dismiss it manually. | completeSubmission 会先捕获待提交状态再清空并关闭；先关闭
          // 会让同步的 `close` 事件清掉 pendingSubmission，静默丢掉提交。若没有待提交（例如恢复组件后完成验证），
          // 之后再关闭已完成的对话框，避免用户手动关闭。
          completeSubmission(record.host);
          var solvedDialog = dialogFor(record.host);
          if (dialogOpen(solvedDialog)) {
            closeDialog(solvedDialog);
          }
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
          // If the dialog is already open, move focus from the close button to the freshly rendered answer control. |
          // 对话框已经打开时，把焦点从关闭按钮移到刚渲染好的作答控件。
          if (dialogOpen(dialogFor(record.host))) {
            focusCaptcha(record.host);
          }
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
    // Bind dialogs even while they stay closed so Esc/cancel/backdrop work the moment a dialog opens; only the widget
    // mount itself is deferred. | 对话框保持关闭时也绑定取消处理器，使打开瞬间 Esc/取消/遮罩立即生效；只有组件挂载
    // 本身被延后。
    for (var i = 0; i < hosts.length; i++) {
      bindDialog(hosts[i]);
    }
    loadCSS();
    loadSDK(function (err) {
      var current = all(HOST_SELECTOR);
      if (err) {
        for (var j = 0; j < current.length; j++) {
          showError(current[j], current[j].getAttribute('data-yd-captcha-unavailable') || '');
        }
        return;
      }
      for (var k = 0; k < current.length; k++) {
        if (connected(current[k]) && !deferMount(current[k])) {
          mountHost(current[k]);
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
    if (connected(host) && !deferMount(host)) {
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
    // The old dialog is detached and can no longer be a modal; close it before following the preserved mount point. |
    // 旧对话框已经脱离文档、不再是模态框；跟随保留挂载点前先关闭它。
    if (dialogOpen(record.host)) {
      closeDialog(record.host);
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
    // Iterate backwards because a closed dialog record is destroyed instead of restarting its challenge below. |
    // 倒序遍历，因为下面会销毁关闭对话框的记录，而不是重新请求挑战。
    for (var i = records.length - 1; i >= 0; i--) {
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
      // A closed dialog must not fetch a hidden challenge with the new locale. With input already typed, keep the
      // widget and answer (the internal challenge text updates on the next challenge); without input, destroy it so
      // the next open mounts a fresh, correctly localized widget. | 关闭的对话框不得用新语言偷偷请求挑战。已经输入
      // 内容时保留组件与答案（内部挑战文案在下次出题时更新）；没有输入时销毁，等下次打开再挂载正确语言的新组件。
      var hostDialog = dialogFor(record.host);
      if (hostDialog && !dialogOpen(hostDialog)) {
        if (!hasUserAnswer(record.host)) {
          resetHostState(record.host);
        } else {
          labelRoot(record.host);
        }
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
    var dialog = dialogFor(host);
    if (dialog && dialogOpen(dialog)) {
      // Enter inside the modal would otherwise submit the login/password form again. Treat it as the SDK's own verify
      // action instead, which is what the user expects while the dialog is open. | 对话框打开时在输入框按回车原本会再次
      // 提交登录/改密表单；这里改为触发 SDK 的验证按钮，符合用户在对话框内按回车的预期。
      event.preventDefault();
      event.stopImmediatePropagation();
      var verify = typeof dialog.querySelector === 'function' ? dialog.querySelector(VERIFY_SELECTOR) : null;
      if (verify && !verify.disabled && typeof verify.click === 'function') {
        verify.click();
      } else {
        focusCaptcha(host);
      }
      return;
    }
    event.preventDefault();
    event.stopImmediatePropagation();
    if (dialog) {
      // The dialog owns the challenge. The token was already checked above, so this always opens, never submits. |
      // 挑战由对话框承载；上面已确认没有令牌，因此这里只打开对话框、绝不提交。
      // SubmitEvent.submitter is null for an implicit Enter submission; fall back to the form's default submit control
      // so a named button keeps its value exactly as the browser would have used it. | 隐式回车提交时
      // SubmitEvent.submitter 为 null；回退到表单的默认提交控件，使带名字的按钮保留浏览器本来会发送的取值。
      var submitter = event.submitter || null;
      if (!submitter && typeof form.querySelector === 'function') {
        submitter = form.querySelector('button[type="submit"], button:not([type]), input[type="submit"]');
      }
      var opener = doc.activeElement;
      if (!opener || opener === doc.body || opener === dialog || !connected(opener)) {
        opener = submitter;
      }
      pendingSubmission = {
        form: form,
        submitter: submitter,
        opener: opener,
        host: host,
        dialog: dialog
      };
      if (!openDialog(dialog)) {
        pendingSubmission = null;
        showError(host, host.getAttribute('data-yd-captcha-unavailable') || '');
        return;
      }
      if (sdkFailed) {
        showError(host, host.getAttribute('data-yd-captcha-unavailable') || '');
      }
      ensureMounted(host);
      focusCaptcha(host);
      return;
    }
    // Legacy inline host (a project generated before the dialog existed): keep the original fail-closed behaviour. |
    // 旧的页内挂载点（对话框出现前生成的项目）：保留原有的失败关闭行为。
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

  // completeSubmission closes the dialog and re-submits the original form after the SDK wrote its token. The submitter
  // is passed back to requestSubmit so a named button keeps its value (tenant switches and multi-button forms rely on
  // this); failing to pass it would silently change what the server receives. | completeSubmission 在 SDK 写入令牌后关闭
  // 对话框并重新提交原表单。提交者要原样交回 requestSubmit，带名字的按钮才能保留取值（租户切换与多按钮表单依赖
  // 这一点）；漏传会悄悄改变服务端收到的内容。
  function completeSubmission(host) {
    var pending = pendingSubmission;
    if (!pending) {
      return;
    }
    var form = pending.form;
    var dialog = pending.dialog;
    if (!form || !connected(form)) {
      pendingSubmission = null;
      closeDialog(dialog);
      return;
    }
    // Re-check what the user is about to submit: a form whose captcha host/dialog was swapped away while the dialog
    // was open must not send a token into a stale form. | 提交前复查用户将要提交的表单：对话框打开期间验证码挂载点或
    // 对话框被换掉的旧表单，不得带着令牌发出。
    var formHost = form.querySelector(HOST_SELECTOR);
    if (!formHost || dialogFor(formHost) !== dialog) {
      pendingSubmission = null;
      closeDialog(dialog);
      return;
    }
    // The host may have been adopted after an hx-preserve move; the record belongs to the live form, not to the
    // detached dialog that opened it. | 挂载点可能被 hx-preserve 移到新表单；记录跟随当前表单，而不是当初打开
    // 对话框的已脱离节点。
    pending.host = host;
    pendingSubmission = null;
    closeDialog(dialog);
    var submitter = pending.submitter;
    var belongs = !!submitter && (submitter.form === form || (typeof form.contains === 'function' && form.contains(submitter)));
    try {
      if (belongs && typeof form.requestSubmit === 'function') {
        form.requestSubmit(submitter);
      } else if (typeof form.requestSubmit === 'function') {
        form.requestSubmit();
      } else if (belongs && typeof submitter.click === 'function') {
        submitter.click();
      } else if (typeof form.submit === 'function') {
        form.submit();
      }
    } catch (ignored) {
      // A disabled submitter or a detached form is not a reason to lose the token; try the native fallbacks before
      // giving up. | 提交按钮被禁用或表单已脱离文档，不应因此丢掉令牌；先尝试原生回退再放弃。
      try {
        if (belongs && typeof submitter.click === 'function' && !submitter.disabled) {
          submitter.click();
        } else if (typeof form.requestSubmit === 'function') {
          form.requestSubmit();
        } else if (typeof form.submit === 'function') {
          form.submit();
        }
      } catch (ignoredAgain) {
        /* the server remains the final gate | 服务端仍是最终防线 */
      }
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
    // A swap that removed the form also cancels any verification that was still open for it. |
    // 交换移除了表单，说明为它打开的验证也应一并作废。
    if (pendingSubmission && (!connected(pendingSubmission.form) || !connected(pendingSubmission.dialog))) {
      pendingSubmission = null;
    }
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
    closeOpenDialogs();
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

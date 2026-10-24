/*!
 * yd-progress.js — the global request progress bar at the top edge of the viewport.
 * yd-progress.js — 固定在视口顶沿的全局请求进度条。
 *
 * What it watches | 它观察什么
 *   1. htmx requests: htmx:before:request starts the bar and htmx:finally:request
 *      ends it, so partial navigation, form posts and every other hx-* request
 *      share one indicator;
 *   2. full-document navigation: a same-origin link click (or a plain form submit)
 *      starts it after the event settles, so a handler that cancels the default
 *      action never leaves the bar running.
 *   1. htmx 请求：htmx:before:request 启动、htmx:finally:request 收尾，局部导航、
 *      表单提交与其它 hx-* 请求因此共用一个指示器；
 *   2. 整页导航：同源链接点击（或普通表单提交）在事件结算后启动，被其它处理器
 *      取消默认行为的操作不会留下一条空转的进度条。
 *
 * Why it looks the way it does | 外观为什么是这样
 *   The bar is a fixed 2px rule coloured with the theme's accent token, so it
 *   follows the light/dark switch without any JavaScript: [data-theme] rewrites
 *   --color-accent and the bar repaints immediately. Requests shorter than the
 *   show delay never flash; longer ones trickle toward 90% and complete to 100%
 *   before fading. | 进度条是固定定位的 2px 细线，颜色取自主题的强调色令牌，
 *   因此明暗切换无需任何 JavaScript：[data-theme] 重写 --color-accent 后进度条
 *   立即换色。短于显示延迟的请求不会闪一下；更长的请求缓慢逼近 90%，收尾时补满
 *   100% 再淡出。
 *
 * Public surface | 对外接口
 *   yd.progress.start()             start / end a manual request | 手工请求的起止
 *   yd.progress.done()
 *   yd.progress.create(options)     a detached controller, for tests | 独立的控制器，供测试使用
 *   yd.progress.install(window)     bind the rendered bar to a window | 把已渲染的进度条绑定到窗口
 *   yd.progress.instance            the controller bound at load time | 加载时绑定的控制器
 *
 * Markup contract | 标记契约
 *   <div data-yd-progress class="yd-progress"><div class="yd-progress__bar"></div></div>
 *   The element is rendered by both shells so it exists before the first request;
 *   without it the module stays inert instead of inventing DOM. | 该元素由两套外壳
 *   渲染，因此首次请求之前就已存在；缺少它时模块保持惰性，而不是自行创建 DOM。
 *
 * Not a security boundary | 不是安全边界
 *   The bar reflects requests the page already started. It adds no retry, no
 *   request and no privilege: hiding or breaking it changes feedback only.
 *   进度条只反映页面已经发起的请求，不添加重试、不发起请求、不改变权限：
 *   隐藏或破坏它只影响反馈。
 */
(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.yd = root.yd || {};
    root.yd.progress = factory();
  }
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  // The markup hook and the state class are shared with the templates and app.css. | 标记钩子与状态类由模板与 app.css 共用。
  var SELECTOR = '[data-yd-progress]';
  var BAR_SELECTOR = '.yd-progress__bar';
  var ACTIVE_CLASS = 'yd-progress--active';

  // DEFAULTS keeps every magic number in one place; tests override them through create(). | DEFAULTS 把全部魔法数字集中在一处；测试经 create() 覆盖。
  var DEFAULTS = {
    delay: 120,             // below this a request finishes without a flash | 低于此值的请求不会闪现
    trickle: 200,           // interval between increments | 每次前进的间隔
    fade: 220,              // fade-out duration, mirrored in app.css | 淡出时长，与 app.css 保持一致
    max: 0.9,               // the ceiling while requests are still pending | 仍有请求时的上限
    start: 0.08,            // the first painted value | 首次绘制的取值
    step: 0.12,             // share of the remaining distance per tick | 每次前进剩余距离的比例
    navigationTimeout: 15000 // a full navigation that never unloads still finishes | 未卸载的整页导航仍会收尾
  };

  function numberOr(value, fallback) {
    return typeof value === 'number' && isFinite(value) ? value : fallback;
  }

  // systemClock is the fallback scheduler when no window is handed in. | systemClock 是未传入窗口时的兜底调度器。
  function systemClock() {
    var g = typeof globalThis !== 'undefined' ? globalThis : {};
    return {
      setTimeout: function (fn, ms) { return g.setTimeout(fn, ms); },
      clearTimeout: function (id) { g.clearTimeout(id); },
      setInterval: function (fn, ms) { return g.setInterval(fn, ms); },
      clearInterval: function (id) { g.clearInterval(id); }
    };
  }

  // windowClock borrows the window's timers, so a bfcache-frozen page and the test stub behave alike. | windowClock 借用窗口的计时器，使 bfcache 冻结的页面与测试桩行为一致。
  function windowClock(w) {
    var fallback = systemClock();
    return {
      setTimeout: typeof w.setTimeout === 'function' ? function (fn, ms) { return w.setTimeout(fn, ms); } : fallback.setTimeout,
      clearTimeout: typeof w.clearTimeout === 'function' ? function (id) { w.clearTimeout(id); } : fallback.clearTimeout,
      setInterval: typeof w.setInterval === 'function' ? function (fn, ms) { return w.setInterval(fn, ms); } : fallback.setInterval,
      clearInterval: typeof w.clearInterval === 'function' ? function (id) { w.clearInterval(id); } : fallback.clearInterval
    };
  }

  // Progress is the state machine behind one bar. It knows nothing about htmx or links; install() feeds it. | Progress 是单条进度条背后的状态机，不认识 htmx 与链接，由 install() 喂给它事件。
  function Progress(options) {
    options = options || {};
    this.element = options.element || null;
    this.bar = options.bar || this.element;
    this.clock = options.clock || systemClock();
    this.delay = numberOr(options.delay, DEFAULTS.delay);
    this.trickleEvery = numberOr(options.trickle, DEFAULTS.trickle);
    this.fade = numberOr(options.fade, DEFAULTS.fade);
    this.max = numberOr(options.max, DEFAULTS.max);
    this.start = numberOr(options.start, DEFAULTS.start);
    this.step = numberOr(options.step, DEFAULTS.step);
    this.navigationTimeout = numberOr(options.navigationTimeout, DEFAULTS.navigationTimeout);

    this.pending = 0;
    this.shown = false;
    this.current = 0;
    this.showTimer = null;
    this.stepTimer = null;
    this.hideTimer = null;
    this.resetTimer = null;
  }

  // begin registers one more pending request and reveals the bar after the anti-flicker delay. | begin 记入一个未完成请求，并在防闪烁延迟之后显示进度条。
  Progress.prototype.begin = function () {
    this.pending += 1;
    this.clearHide();

    if (this.shown) {
      // A request that arrives while the bar is already visible keeps the current position;
      // one that arrives while it fades at 100% restarts from the beginning. | 进度条已可见时新请求沿用当前位置；
      // 已在 100% 淡出时则从头开始。
      if (this.current >= 1) {
        this.current = this.start;
        this.render();
      }
      return;
    }

    if (this.showTimer === null) {
      var self = this;
      this.showTimer = this.clock.setTimeout(function () { self.reveal(); }, this.delay);
    }
  };

  // end releases one pending request; the bar completes once none are left. | end 释放一个未完成请求；全部结束后进度条补满。
  Progress.prototype.end = function () {
    if (this.pending === 0) {
      return;
    }
    this.pending -= 1;
    if (this.pending > 0) {
      return;
    }

    // Never shown means the request beat the delay: cancel the reveal instead of flashing. | 从未显示说明请求快于延迟：取消显示，不要闪一下。
    if (this.showTimer !== null) {
      this.clock.clearTimeout(this.showTimer);
      this.showTimer = null;
      return;
    }
    if (!this.shown) {
      return;
    }

    this.stopTrickle();
    this.current = 1;
    this.render();
    var self = this;
    this.hideTimer = this.clock.setTimeout(function () { self.hide(); }, this.fade);
  };

  // resetNow returns to the idle state immediately, for bfcache restores and page changes. | resetNow 立即回到空闲状态，用于 bfcache 恢复与页面切换。
  Progress.prototype.resetNow = function () {
    this.clearTimers();
    this.pending = 0;
    this.shown = false;
    this.current = 0;
    this.applyActive(false);
    this.render();
  };

  Progress.prototype.visible = function () {
    return this.shown;
  };

  Progress.prototype.value = function () {
    return this.current;
  };

  // reveal is the delayed transition from hidden to visible. | reveal 是延迟之后由隐藏转为可见的入口。
  Progress.prototype.reveal = function () {
    this.showTimer = null;
    this.shown = true;
    if (this.current < this.start) {
      this.current = this.start;
    }
    this.applyActive(true);
    this.render();

    var self = this;
    this.stepTimer = this.clock.setInterval(function () { self.trickle(); }, this.trickleEvery);
  };

  // trickle approaches the ceiling asymptotically and never passes it. | trickle 渐进逼近上限且绝不越过。
  Progress.prototype.trickle = function () {
    if (!this.shown || this.current >= this.max) {
      return;
    }
    var next = this.current + (this.max - this.current) * this.step;
    this.current = next > this.max ? this.max : next;
    this.render();
  };

  // hide removes the active class after completion; reset then clears the transform while invisible. | hide 在补满后移除激活类；随后 reset 在不可见时清空位移。
  Progress.prototype.hide = function () {
    this.hideTimer = null;
    this.applyActive(false);
    var self = this;
    this.resetTimer = this.clock.setTimeout(function () { self.reset(); }, this.fade);
  };

  Progress.prototype.reset = function () {
    this.resetTimer = null;
    this.shown = false;
    this.current = 0;
    this.pending = 0;
    this.render();
  };

  // applyActive toggles the visible state. The bar stays aria-hidden on purpose: a nameless
  // indeterminate progress bar adds noise to every navigation without telling a screen reader
  // anything the request itself does not. | applyActive 切换可见状态。进度条刻意保持 aria-hidden：
  // 一条没有名字的不确定进度条只会给每次导航增加噪音，却不会告诉读屏软件任何请求本身之外的信息。
  Progress.prototype.applyActive = function (on) {
    if (!this.element) {
      return;
    }
    if (on) {
      this.element.classList.add(ACTIVE_CLASS);
    } else {
      this.element.classList.remove(ACTIVE_CLASS);
    }
  };

  // render writes the scale; four decimals are finer than a pixel on any display. | render 写入缩放值；四位小数在任何屏幕上都细于一个像素。
  Progress.prototype.render = function () {
    if (!this.bar || !this.bar.style) {
      return;
    }
    var rounded = Math.round(this.current * 10000) / 10000;
    this.bar.style.transform = 'scaleX(' + rounded + ')';
  };

  Progress.prototype.stopTrickle = function () {
    if (this.stepTimer !== null) {
      this.clock.clearInterval(this.stepTimer);
      this.stepTimer = null;
    }
  };

  Progress.prototype.clearHide = function () {
    if (this.hideTimer !== null) {
      this.clock.clearTimeout(this.hideTimer);
      this.hideTimer = null;
    }
    if (this.resetTimer !== null) {
      this.clock.clearTimeout(this.resetTimer);
      this.resetTimer = null;
    }
  };

  Progress.prototype.clearTimers = function () {
    if (this.showTimer !== null) {
      this.clock.clearTimeout(this.showTimer);
      this.showTimer = null;
    }
    this.stopTrickle();
    this.clearHide();
  };

  // create builds a detached controller. | create 构建一个独立的控制器。
  function create(options) {
    return new Progress(options);
  }

  // isHtmxTarget reports whether htmx already owns the element's request. It walks the ancestry
  // because htmx 4 inherits only through explicit ":inherited" attributes, so a boosted rail spells
  // hx-boost:inherited on an ancestor and never on the link itself. | isHtmxTarget 报告 htmx 是否
  // 已经接管该元素的请求。它沿祖先链检查，因为 htmx 4 只通过显式的 ":inherited" 属性继承：
  // 被 boost 的导航栏把 hx-boost:inherited 写在祖先上，链接本身没有该属性。
  var HTMX_ATTRIBUTES = ['hx-get', 'hx-post', 'hx-put', 'hx-patch', 'hx-delete', 'hx-boost'];

  function isHtmxTarget(el) {
    var node = el;
    while (node && node.nodeType === 1) {
      for (var i = 0; i < HTMX_ATTRIBUTES.length; i++) {
        if (node.hasAttribute(HTMX_ATTRIBUTES[i]) || node.hasAttribute(HTMX_ATTRIBUTES[i] + ':inherited')) {
          return true;
        }
      }
      node = node.parentNode;
    }
    return false;
  }

  // awaitsConfirm reports a change action still waiting for its confirmation dialog. | awaitsConfirm 报告仍在等待确认对话框的变更动作。
  function awaitsConfirm(el) {
    var holder = el.closest ? el.closest('[data-yd-confirm]') : null;
    return !!holder && holder.getAttribute('data-yd-confirmed') !== '1';
  }

  // originOf returns the current document's origin, deriving it when the browser lacks location.origin. | originOf 返回当前文档的来源，浏览器缺少 location.origin 时自行推导。
  function originOf(w) {
    if (w.location && w.location.origin) {
      return w.location.origin;
    }
    try {
      return new (w.URL || URL)(w.location.href).origin;
    } catch (err) {
      return '';
    }
  }

  // sameDocumentHref reports a link that would load another document in this window. | sameDocumentHref 报告某个链接会在本窗口加载另一份文档。
  function sameDocumentHref(link, w) {
    if (link.hasAttribute('download')) {
      return false;
    }
    var target = (link.getAttribute('target') || '').toLowerCase();
    if (target && target !== '_self') {
      return false;
    }
    var href = link.getAttribute('href');
    if (!href) {
      return false;
    }
    var Ctor = w.URL || URL;
    var url;
    var current;
    try {
      url = new Ctor(href, w.location.href);
      current = new Ctor(w.location.href);
    } catch (err) {
      return false;
    }
    if (url.protocol !== 'http:' && url.protocol !== 'https:') {
      return false;
    }
    if (url.origin !== originOf(w)) {
      return false;
    }
    // Same URL or a pure fragment: no second document is fetched. | 同一 URL 或纯锚点：不会请求第二份文档。
    return url.href.split('#')[0] !== current.href.split('#')[0];
  }

  // sameOriginAction reports a form that posts back to this origin. | sameOriginAction 报告表单会提交回本来源。
  function sameOriginAction(form, w) {
    var action = form.getAttribute('action') || w.location.href;
    try {
      return new (w.URL || URL)(action, w.location.href).origin === originOf(w);
    } catch (err) {
      return false;
    }
  }

  // navigationIntent extracts the element a click or submit would navigate away with, or null. | navigationIntent 取出点击或提交将带着离开的元素，没有则返回 null。
  function navigationIntent(event, w) {
    var target = event.target;
    if (!target || typeof target.closest !== 'function') {
      return null;
    }
    if (event.type === 'submit') {
      var form = target.closest('form');
      if (!form || isHtmxTarget(form) || awaitsConfirm(form) || !sameOriginAction(form, w)) {
        return null;
      }
      // A dialog form closes a dialog; it performs no request. | 对话框表单只关闭对话框，不发请求。
      var method = (form.getAttribute('method') || 'get').toLowerCase();
      if (method === 'dialog') {
        return null;
      }
      var formTarget = (form.getAttribute('target') || '').toLowerCase();
      return formTarget && formTarget !== '_self' ? null : form;
    }

    if (event.button !== undefined && event.button !== 0) {
      return null;
    }
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return null;
    }
    var link = target.closest('a[href], area[href]');
    if (!link || isHtmxTarget(link) || awaitsConfirm(link) || !sameDocumentHref(link, w)) {
      return null;
    }
    return link;
  }

  // install binds the rendered bar to htmx and to full-document navigation. | install 把已渲染的进度条绑定到 htmx 与整页导航。
  function install(win) {
    var w = win || (typeof window !== 'undefined' ? window : null);
    if (!w || !w.document || typeof w.document.querySelector !== 'function') {
      return null;
    }
    var doc = w.document;
    var element = doc.querySelector(SELECTOR);
    if (!element) {
      return null;
    }
    var bar = typeof element.querySelector === 'function' ? element.querySelector(BAR_SELECTOR) : null;
    var progress = new Progress({
      element: element,
      bar: bar || element,
      clock: windowClock(w)
    });

    doc.addEventListener('htmx:before:request', function () { progress.begin(); });
    doc.addEventListener('htmx:finally:request', function () { progress.end(); });

    function onIntent(event) {
      if (!event || event.defaultPrevented) {
        return;
      }
      if (navigationIntent(event, w) === null) {
        return;
      }
      // Defer one task: a later listener may cancel the default action, and a canceled
      // navigation must not leave the bar running. | 延后一个任务：后续监听器可能取消默认行为，
      // 被取消的导航不得留下一条空转的进度条。
      w.setTimeout(function () {
        if (event.defaultPrevented) {
          return;
        }
        progress.begin();
        // The document normally unloads before this fires; if it does not, the bar
        // still finishes instead of sitting at the ceiling forever. | 正常情况下文档会先卸载；
        // 若没有卸载，进度条仍会收尾，而不是永远停在上限。
        w.setTimeout(function () { progress.end(); }, progress.navigationTimeout);
      }, 0);
    }

    doc.addEventListener('click', onIntent);
    doc.addEventListener('submit', onIntent);
    if (typeof w.addEventListener === 'function') {
      w.addEventListener('pageshow', function () { progress.resetNow(); });
    }
    return progress;
  }

  var api = {
    create: create,
    install: install,
    instance: null,
    start: function () {
      if (api.instance) {
        api.instance.begin();
      }
    },
    done: function () {
      if (api.instance) {
        api.instance.end();
      }
    }
  };

  // Auto-install in a browser; Node's test runner has no document and therefore binds nothing. | 浏览器中自动安装；Node 测试环境没有 document，因此不绑定任何东西。
  if (typeof document !== 'undefined' && document && typeof document.querySelector === 'function' &&
      typeof window !== 'undefined' && window.document === document) {
    api.instance = install(window);
  }

  return api;
});

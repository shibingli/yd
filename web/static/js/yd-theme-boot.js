/*!
 * yd-theme-boot.js — first-paint theme resolution, loaded from <head> before the stylesheet.
 * yd-theme-boot.js — 首屏主题解析：在 <head> 中先于样式表同步加载。
 *
 * Why a separate synchronous script | 为什么需要一段独立的同步脚本:
 *   The layout already pre-renders <html data-theme> from the yd_theme cookie, so an explicit
 *   light or dark choice is correct on the very first byte. The one case the server cannot know
 *   is "system": whether the visitor's operating system asks for dark is only visible to the
 *   browser. Resolving it after the deferred scripts have run would paint one light frame and
 *   then flip to dark — the flash this file exists to remove.
 *   布局已依据 yd_theme cookie 预渲染 <html data-theme>，因此明确的浅色/深色选择在首字节就已正确；
 *   服务端唯一无法得知的是 "system"：访客的操作系统是否偏好深色只有浏览器知道。
 *   若等到 defer 脚本执行后才解析，就会先画出一帧浅色再翻成深色——本文件要消除的正是这次闪烁。
 *
 * Hard rules | 硬规则:
 *   1. Synchronous and dependency-free: it runs while <head> is still being parsed, therefore it
 *      must stay tiny and must never touch the DOM beyond the <html> attribute.
 *      同步且无依赖：它在 <head> 解析过程中执行，因此必须足够小，且除 <html> 属性外绝不触碰 DOM。
 *   2. It only reads localStorage and the yd_theme cookie; persisting a new choice belongs to
 *      yd-app.js, which runs later and keeps every switch control in sync.
 *      它只读 localStorage 与 yd_theme cookie；写入新偏好由随后执行的 yd-app.js 负责，
 *      由它同步全部切换控件。
 *   3. Every read is wrapped, so a private-mode localStorage or a sandboxed document degrades to
 *      the server-rendered value instead of throwing inside <head>.
 *      所有读取都被包裹：隐私模式下的 localStorage 或沙箱文档只会退化成服务端预渲染值，
 *      绝不会在 <head> 里抛错。
 *
 * Precedence | 优先级: localStorage -> yd_theme cookie -> server-rendered <html data-theme> -> system.
 */
(function () {
  'use strict';

  // THEME_KEY is the localStorage key, mirrored into a cookie of the same name. | THEME_KEY 是 localStorage 键名，同时镜像为同名 cookie。
  var THEME_KEY = 'yd_theme';
  // DARK_QUERY is the media query that answers "does the system ask for dark?" | DARK_QUERY 回答「系统是否偏好深色」的媒体查询。
  var DARK_QUERY = '(prefers-color-scheme: dark)';
  // CHOICES is the three-state whitelist; "auto" is the generated projects' spelling of "system"
  // and is kept so an older stored preference keeps working. | CHOICES 是三态白名单；
  // "auto" 是生成项目对 "system" 的写法，保留它是为了让旧偏好继续生效。
  var CHOICES = { light: true, dark: true, system: true, auto: true };

  // env returns the global object, which keeps the file testable outside a browser.
  // env 返回全局对象，使本文件在浏览器之外也可测试。
  function env() {
    return typeof globalThis !== 'undefined' ? globalThis : {};
  }

  // doc returns the document when one exists, otherwise null.
  // doc 在存在文档时返回它，否则返回 null。
  function doc() {
    var e = env();
    return e.document || null;
  }

  // choice normalizes an arbitrary value onto the three-state whitelist.
  // choice 把任意取值归一化到三态白名单上。
  function choice(value) {
    var raw = value === undefined || value === null ? '' : String(value);
    return CHOICES[raw] ? raw : '';
  }

  // readCookie reads one cookie defensively; a sandboxed document yields an empty string.
  // readCookie 防御式读取单个 cookie；沙箱文档返回空串。
  function readCookie(name) {
    var d = doc();
    if (!d || typeof d.cookie !== 'string' || !d.cookie) {
      return '';
    }
    var parts = d.cookie.split(';');
    for (var i = 0; i < parts.length; i++) {
      var pair = parts[i].trim();
      var eq = pair.indexOf('=');
      if (eq > 0 && pair.slice(0, eq) === name) {
        return decodeURIComponent(pair.slice(eq + 1).trim());
      }
    }
    return '';
  }

  // storedChoice is the remembered preference: localStorage first, cookie second.
  // storedChoice 是记住的偏好：先 localStorage，后 cookie。
  function storedChoice() {
    var value = '';
    try {
      var store = env().localStorage;
      if (store && typeof store.getItem === 'function') {
        value = choice(store.getItem(THEME_KEY));
      }
    } catch (err) {
      value = ''; // private mode may throw on read | 隐私模式下读取可能抛错
    }
    return value || choice(readCookie(THEME_KEY));
  }

  // systemResolved reports the theme the operating system asks for, defaulting to light.
  // systemResolved 返回操作系统要求的主题，缺省为浅色。
  function systemResolved() {
    var matchMedia = env().matchMedia;
    if (typeof matchMedia !== 'function') {
      return 'light';
    }
    try {
      return matchMedia(DARK_QUERY).matches ? 'dark' : 'light';
    } catch (err) {
      return 'light';
    }
  }

  // serverChoice reads the value the layout pre-rendered onto <html>.
  // serverChoice 读取布局预渲染在 <html> 上的取值。
  function serverChoice() {
    var d = doc();
    if (!d || !d.documentElement || typeof d.documentElement.getAttribute !== 'function') {
      return '';
    }
    return choice(d.documentElement.getAttribute('data-theme'));
  }

  // resolve turns a three-state choice into the concrete theme actually painted.
  // resolve 把三态偏好解析为实际绘制的具体主题。
  function resolve(value) {
    var picked = choice(value) || 'system';
    return picked === 'system' || picked === 'auto' ? systemResolved() : picked;
  }

  // apply writes the resolved theme onto <html>; it returns the resolved value.
  // apply 把解析后的主题写到 <html> 上，并返回该取值。
  function apply(value) {
    var resolved = resolve(value);
    var d = doc();
    if (d && d.documentElement && typeof d.documentElement.setAttribute === 'function') {
      d.documentElement.setAttribute('data-theme', resolved);
      d.documentElement.setAttribute('data-yd-theme-choice', choice(value) || 'system');
    }
    return resolved;
  }

  // boot resolves the first paint: memory first, cookie second, server render third, system last.
  // boot 解析首屏：内存优先、cookie 次之、服务端渲染再次、系统最后。
  function boot() {
    var remembered = storedChoice();
    if (remembered) {
      return apply(remembered);
    }
    var rendered = serverChoice();
    return apply(rendered || 'system');
  }

  var resolved = boot();

  // ydTheme is the tiny surface other scripts may read; yd-app.js owns the interactive switch.
  // ydTheme 是其它脚本可读取的极小接口；交互式切换由 yd-app.js 负责。
  env().ydTheme = {
    apply: apply,
    resolve: resolve,
    stored: storedChoice,
    current: function () {
      return doc() && doc().documentElement
        ? doc().documentElement.getAttribute('data-theme')
        : resolved;
    },
    choices: function () {
      return Object.keys(CHOICES);
    }
  };
})();

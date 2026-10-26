/*
 * yd-captcha.test.mjs — behaviour of the captcha glue on a small fake DOM.
 * yd-captcha.test.mjs — 在小型伪 DOM 上验证验证码胶水层的行为。
 *
 * Run with | 运行方式:
 *   node web/static/js/yd-captcha.test.mjs
 *
 * The suite loads the shipped glue with a fake same-origin SDK, then drives the three paths that
 * matter: mount + token hand-off, the submit guard, and the 4xx reset. It also proves the widget
 * follows <html data-theme> and <html lang>. | 本套件用伪同源 SDK 加载随包发布的胶水层，覆盖三条
 * 关键路径：挂载与令牌交接、提交守卫、4xx 重置；同时证明组件跟随 <html data-theme> 与 <html lang>。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

function makeElement(attrs = {}) {
  const element = {
    nodeType: 1,
    attrs: { ...attrs },
    value: '',
    textContent: '',
    style: {},
    className: '',
    children: [],
    parentNode: null,
    listeners: [],
    classList: {
      add() {},
      remove() {},
      toggle() {},
      contains() { return false; }
    },
    setAttribute(name, value) { this.attrs[name] = String(value); },
    getAttribute(name) {
      return Object.prototype.hasOwnProperty.call(this.attrs, name) ? this.attrs[name] : null;
    },
    removeAttribute(name) { delete this.attrs[name]; },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(this.attrs, name); },
    matches() { return false; },
    closest() { return null; },
    querySelector(selector) {
      const matches = this.querySelectorAll(selector);
      return matches.length ? matches[0] : null;
    },
    querySelectorAll(selector) {
      if (selector === '.yd-captcha') {
        return this.children.filter((child) => /\byd-captcha\b/.test(child.className || ''));
      }
      return [];
    },
    appendChild(child) {
      child.parentNode = this;
      this.children.push(child);
      if (child && typeof child.onload === 'function') {
        child.onload();
      }
      return child;
    },
    removeChild(child) {
      const index = this.children.indexOf(child);
      if (index >= 0) {
        this.children.splice(index, 1);
      }
      child.parentNode = null;
      return child;
    },
    addEventListener(name, fn) { this.listeners.push({ name, fn }); },
    removeEventListener() {},
    dispatchEvent() { return true; },
    focus() {
      this.focused = true;
      if (globalThis.document) {
        globalThis.document.activeElement = this;
      }
    },
    contains() { return true; }
  };
  return element;
}

function installEnvironment(withSDK = true) {
  const html = makeElement({ 'data-theme': 'light', lang: 'zh-CN' });
  html.contains = (node) => !node || node.detached !== true;
  const head = makeElement();
  const body = makeElement();
  const token = makeElement({ id: 'captcha-token', name: 'captcha_token' });
  const mountTarget = makeElement();
  const errorNode = makeElement();
  const control = makeElement();
  const host = makeElement({
    'data-yd-captcha': '',
    'data-yd-captcha-action': 'login',
    'data-yd-captcha-endpoint': '/captcha',
    'data-yd-captcha-input': 'captcha-token',
    'data-yd-captcha-required': '请先完成人机验证',
    'data-yd-captcha-unavailable': '人机验证暂时不可用'
  });
  const form = makeElement();

  host.querySelector = (selector) => {
    if (selector === '[data-yd-captcha-host]') return mountTarget;
    if (selector === '.yd-field__error') return errorNode;
    if (selector === 'button, input, select, textarea') return control;
    return null;
  };
  host.closest = (selector) => (selector === 'form' ? form : null);
  form.querySelector = (selector) => {
    if (selector === '[data-yd-captcha]') return host;
    if (selector === 'input[name="captcha_token"]') return token;
    return null;
  };

  const docListeners = [];
  const windowListeners = [];
  const observerCallbacks = [];
  const hosts = [host];
  const doc = {
    readyState: 'complete',
    documentElement: html,
    head,
    body,
    createElement: () => makeElement(),
    getElementById: (id) => (id === 'captcha-token' ? token : null),
    querySelectorAll: (selector) => (selector === '[data-yd-captcha]' ? hosts.filter((item) => !item.detached) : []),
    addEventListener: (name, fn) => { docListeners.push({ name, fn }); },
    removeEventListener: () => {},
    dispatchEvent: () => true,
    contains: (node) => !node || node.detached !== true
  };

  const widgets = [];
  const scripts = [];
  doc.createElement = (tag) => {
    const element = makeElement();
    element.tag = tag;
    if (tag === 'script') {
      scripts.push(element);
    }
    return element;
  };
  head.appendChild = (child) => {
    if (child && child.tag === 'script' && typeof child.onerror === 'function') {
      child.onerror();
    } else if (child && typeof child.onload === 'function') {
      child.onload();
    }
    return child;
  };
  globalThis.document = doc;
  globalThis.window = globalThis;
  globalThis.addEventListener = (name, fn) => { windowListeners.push({ name, fn }); };
  globalThis.removeEventListener = () => {};
  globalThis.CustomEvent = class {
    constructor(type, init) {
      this.type = type;
      this.detail = (init && init.detail) || null;
    }
  };
  globalThis.MutationObserver = class {
    constructor(fn) { observerCallbacks.push(fn); }
    observe() {}
  };
  if (withSDK) {
    globalThis.YdCaptcha = {
    mount(target, options) {
      const root = makeElement();
      root.className = 'yd-captcha';
      target.appendChild(root);
      const widget = {
        target,
        root,
        options,
        destroyed: false,
        destroy() {
          if (this.root && this.root.parentNode) {
            this.root.parentNode.removeChild(this.root);
          }
          this.destroyed = true;
        },
        setTheme(theme) { this.theme = theme; },
        setLocale(locale) { this.locale = locale; }
      };
      widgets.push(widget);
      return widget;
    }
    };
  } else {
    delete globalThis.YdCaptcha;
  }

  return { html, head, host, hosts, form, token, mountTarget, errorNode, control, widgets, scripts, docListeners, windowListeners, observerCallbacks };
}

const env = installEnvironment();
const require = createRequire(import.meta.url);
require('./yd-captcha.js');

function docListener(name) {
  return env.docListeners.find((listener) => listener.name === name);
}

function windowListener(name) {
  return env.windowListeners.find((listener) => listener.name === name);
}

function submitEvent() {
  return {
    target: env.form,
    defaultPrevented: false,
    stopped: false,
    preventDefault() { this.defaultPrevented = true; },
    stopImmediatePropagation() { this.stopped = true; }
  };
}

test('mounts one widget with the template contract and writes the token on success', () => {
  assert.ok(globalThis.ydCaptcha, 'ydCaptcha must be published');
  assert.equal(env.widgets.length, 1, 'one host must produce one widget');
  const widget = env.widgets[0];
  assert.equal(widget.target, env.mountTarget);
  assert.equal(widget.options.action, 'login');
  assert.equal(widget.options.endpoint, '/captcha');
  assert.equal(widget.options.locale, 'zh-CN');
  assert.equal(widget.options.theme, 'light');

  widget.options.onSuccess('token-1');
  assert.equal(env.token.value, 'token-1');
  assert.equal(env.host.getAttribute('data-yd-captcha-verified'), 'true');
  assert.equal(env.errorNode.textContent, '');
});

test('blocks a submit without a token and allows one with a token', () => {
  const submit = windowListener('submit');
  assert.ok(submit, 'the submit guard must be registered on the window capture phase');

  env.token.value = '';
  const blocked = submitEvent();
  submit.fn(blocked);
  assert.equal(blocked.defaultPrevented, true);
  assert.equal(blocked.stopped, true);
  assert.equal(env.errorNode.textContent, '请先完成人机验证');
  assert.equal(env.control.focused, true);

  env.token.value = 'token-2';
  const allowed = submitEvent();
  submit.fn(allowed);
  assert.equal(allowed.defaultPrevented, false);
});

test('validates ordinary required fields before touching the captcha token', () => {
  env.errorNode.textContent = '';
  const form = makeElement();
  form.querySelector = (selector) => (selector === '[data-yd-captcha]' ? env.host : null);
  form.checkValidity = () => false;
  let reported = false;
  form.reportValidity = () => { reported = true; };
  const submit = windowListener('submit');
  const event = {
    target: form,
    defaultPrevented: false,
    stopped: false,
    preventDefault() { this.defaultPrevented = true; },
    stopImmediatePropagation() { this.stopped = true; }
  };
  submit.fn(event);
  assert.equal(event.defaultPrevented, true);
  assert.equal(event.stopped, true);
  assert.equal(reported, true, 'the browser must be allowed to show the native validation message');
  assert.equal(env.errorNode.textContent, '', 'a blank user name must not consume a solved captcha');
});

test('defers a 4xx reset until the swap, then clears the consumed token exactly once', () => {
  env.token.value = 'token-3';
  const before = env.widgets.length;
  const afterRequest = docListener('htmx:after:request');
  assert.ok(afterRequest, 'the 4xx reset must listen to htmx:after:request');
  afterRequest.fn({ detail: { ctx: { status: 422, sourceElement: env.form } } });

  assert.equal(env.widgets.length, before, 'no challenge may be opened before the swap');
  assert.equal(env.widgets[before - 1].destroyed, false, 'the old widget is still in place before the swap');

  const afterSwap = docListener('htmx:after:swap');
  assert.ok(afterSwap, 'the swap handler must flush pending resets');
  afterSwap.fn({});

  assert.equal(env.widgets[before - 1].destroyed, true, 'the consumed widget must be destroyed');
  assert.equal(env.widgets.length, before + 1, 'exactly one fresh widget must be mounted');
  assert.equal(env.token.value, '');
  assert.equal(env.host.getAttribute('data-yd-captcha-verified'), null);
  assert.equal(env.host.getAttribute('data-invalid'), null);
  assert.equal(env.errorNode.textContent, '');
});

test('keeps a solved token across a swap that leaves the form in place', () => {
  env.token.value = 'token-solved';
  const before = env.widgets.length;
  const afterSwap = docListener('htmx:after:swap');
  afterSwap.fn({});
  assert.equal(env.widgets.length, before, 'a solved widget must not be rebuilt');
  assert.equal(env.token.value, 'token-solved');
});

test('rebuilds exactly one widget when a morph swap keeps the host but removes its widget', () => {
  env.token.value = '';
  const before = env.widgets.length;
  for (const root of env.mountTarget.querySelectorAll('.yd-captcha')) {
    env.mountTarget.removeChild(root);
  }
  const afterSwap = docListener('htmx:after:swap');
  afterSwap.fn({});
  assert.equal(env.widgets[before - 1].destroyed, true, 'the stale widget must be destroyed');
  assert.equal(env.widgets.length, before + 1, 'the stale record must be replaced by one widget');
  assert.equal(env.mountTarget.querySelectorAll('.yd-captcha').length, 1);
});

test('an unrelated swap keeps a live unsolved widget', () => {
  env.token.value = '';
  const before = env.widgets.length;
  const widget = env.widgets[before - 1];
  const afterSwap = docListener('htmx:after:swap');
  afterSwap.fn({});
  assert.equal(widget.destroyed, false, 'a live widget must not be reset by an unrelated swap');
  assert.equal(env.widgets.length, before, 'no challenge may be opened for an unrelated swap');
});

test('a history restore removes the cloned widget and the stale token, then mounts one', () => {
  env.token.value = 'stale-token';
  const clone = makeElement();
  clone.className = 'yd-captcha';
  env.mountTarget.appendChild(clone);
  const before = env.widgets.length;
  const restore = docListener('htmx:history:cache:after:restore');
  assert.ok(restore, 'the glue must observe the history-cache restore');
  restore.fn({});
  assert.equal(env.mountTarget.querySelectorAll('.yd-captcha').length, 1, 'exactly one SDK root may survive');
  assert.equal(env.token.value, '', 'a snapshot token must not pass the submit guard');
  assert.equal(env.widgets[before - 1].destroyed, true, 'the snapshot widget must be replaced');
  assert.equal(env.widgets.length, before + 1, 'one fresh widget must be mounted');
});

test('a language shell swap adopts the preserved mount point and keeps the solved token', () => {
  env.token.value = 'language-token';
  const widget = env.widgets[env.widgets.length - 1];
  const newHost = makeElement({
    'data-yd-captcha': '',
    'data-yd-captcha-input': 'captcha-token',
    'data-yd-captcha-required': '请先完成人机验证',
    'data-yd-captcha-unavailable': '人机验证暂时不可用'
  });
  newHost.querySelector = (selector) => (selector === '[data-yd-captcha-host]' ? env.mountTarget : null);
  newHost.contains = () => true;
  env.mountTarget.closest = (selector) => (selector === '[data-yd-captcha]' ? newHost : null);
  env.host.detached = true;
  newHost.appendChild(env.mountTarget);
  env.hosts.push(newHost);

  const before = env.widgets.length;
  docListener('htmx:after:swap').fn({});
  assert.equal(env.widgets.length, before, 'the preserved widget must not be rebuilt');
  assert.equal(widget.destroyed, false, 'the preserved widget must not be destroyed');
  assert.equal(env.token.value, 'language-token', 'the solved token must survive the shell swap');
  assert.equal(newHost.getAttribute('data-yd-captcha-verified'), 'true', 'the new host must reflect the token');

  widget.options.onSuccess('post-language-token');
  assert.equal(env.token.value, 'post-language-token', 'a late solve must write the preserved token field');
  assert.equal(newHost.getAttribute('data-yd-captcha-verified'), 'true', 'a late solve must mark the adopted host');
});

test('a 4xx that preserves the mount point clears the stale token exactly once', () => {
  const currentHost = env.hosts.find((item) => !item.detached);
  env.token.value = 'stale-token';
  const sourceForm = makeElement();
  sourceForm.querySelector = (selector) => (selector === '[data-yd-captcha]' ? currentHost : null);
  const afterRequest = docListener('htmx:after:request');
  afterRequest.fn({ detail: { ctx: { status: 422, sourceElement: sourceForm } } });

  const nextHost = makeElement({
    'data-yd-captcha': '',
    'data-yd-captcha-input': 'captcha-token',
    'data-yd-captcha-required': '请先完成人机验证',
    'data-yd-captcha-unavailable': '人机验证暂时不可用'
  });
  nextHost.querySelector = (selector) => (selector === '[data-yd-captcha-host]' ? env.mountTarget : null);
  nextHost.contains = () => true;
  env.mountTarget.closest = (selector) => (selector === '[data-yd-captcha]' ? nextHost : null);
  currentHost.detached = true;
  nextHost.appendChild(env.mountTarget);
  env.hosts.push(nextHost);

  const before = env.widgets.length;
  const staleWidget = env.widgets[before - 1];
  docListener('htmx:after:swap').fn({});
  assert.equal(staleWidget.destroyed, true, 'the consumed widget must be destroyed');
  assert.equal(env.token.value, '', 'the preserved stale token must be cleared');
  assert.equal(env.widgets.length, before + 1, 'exactly one fresh widget must be mounted');
  assert.equal(env.mountTarget.querySelectorAll('.yd-captcha').length, 1);
  assert.equal(nextHost.getAttribute('data-yd-captcha-verified'), null);
});

test('follows theme changes and restarts an unsolved challenge on a language change', () => {
  const widget = env.widgets[env.widgets.length - 1];
  env.token.value = '';
  env.html.setAttribute('data-theme', 'dark');
  env.html.setAttribute('lang', 'en');
  for (const callback of env.observerCallbacks) {
    callback();
  }
  assert.equal(widget.theme, 'dark');
  assert.equal(widget.locale, 'en');
});

test('a solved token survives a language change', () => {
  env.token.value = 'token-4';
  const widget = env.widgets[env.widgets.length - 1];
  const previousLocale = widget.locale;
  env.html.setAttribute('lang', 'zh-CN');
  for (const callback of env.observerCallbacks) {
    callback();
  }
  assert.equal(widget.locale, previousLocale, 'setLocale must not restart a solved challenge');
});


test('a failed SDK load shows the unavailable copy and is not retried', () => {
  const failure = installEnvironment(false);
  delete require.cache[require.resolve('./yd-captcha.js')];
  require('./yd-captcha.js');

  assert.equal(failure.scripts.length, 1, 'the glue must request the SDK once');
  assert.equal(failure.errorNode.textContent, '人机验证暂时不可用');

  const submit = failure.windowListeners.find((listener) => listener.name === 'submit');
  const event = submitEventFor(failure);
  submit.fn(event);
  assert.equal(event.defaultPrevented, true);
  assert.equal(failure.errorNode.textContent, '人机验证暂时不可用');

  globalThis.ydCaptcha.mountAll();
  assert.equal(failure.scripts.length, 1, 'a failed SDK must not be requested again');
});

test('a 200 response without YdCaptcha is treated as a load failure and not retried', () => {
  const failure = installEnvironment(false);
  failure.head.appendChild = (child) => {
    if (child && child.tag === 'script' && typeof child.onload === 'function') {
      child.onload();
    }
    return child;
  };
  delete require.cache[require.resolve('./yd-captcha.js')];
  require('./yd-captcha.js');

  assert.equal(failure.scripts.length, 1, 'the glue must request the SDK once');
  assert.equal(failure.errorNode.textContent, '人机验证暂时不可用');
  globalThis.ydCaptcha.mountAll();
  assert.equal(failure.scripts.length, 1, 'a 200 response without the SDK must not be retried');
});

function submitEventFor(failure) {
  return {
    target: failure.form,
    defaultPrevented: false,
    stopped: false,
    preventDefault() { this.defaultPrevented = true; },
    stopImmediatePropagation() { this.stopped = true; }
  };
}

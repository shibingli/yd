/*
 * yd-app.test.mjs — boot smoke test for the framework shell script.
 * yd-app.test.mjs — 框架外壳脚本的启动冒烟测试。
 *
 * Run with | 运行方式:
 *   node web/static/js/yd-app.test.mjs
 *
 * The script boots synchronously at load, so a constructor typo in the theme path can kill every
 * delegated listener on the page. This suite supplies the small DOM surface the boot touches and
 * asserts that loading the real file completes and that the theme path still writes the attribute.
 * 脚本在加载时同步启动，因此主题路径里一个构造函数写法错误就会让页面上所有委托监听失效。
 * 本套件提供启动所需的少量 DOM 表面，断言真实文件能够完成加载，并且主题路径仍会写入属性。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

function fakeElement() {
  const attrs = {};
  return {
    nodeType: 1,
    attrs,
    classList: {
      add() {},
      remove() {},
      toggle() {},
      contains() { return false; }
    },
    style: {},
    setAttribute(name, value) { attrs[name] = String(value); },
    getAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name) ? attrs[name] : null; },
    removeAttribute(name) { delete attrs[name]; },
    hasAttribute(name) { return Object.prototype.hasOwnProperty.call(attrs, name); },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    addEventListener() {},
    removeEventListener() {},
    focus() {},
    dispatchEvent() { return true; }
  };
}

function installFakeDOM() {
  const html = fakeElement();
  const body = fakeElement();
  const listeners = [];
  const doc = {
    readyState: 'complete',
    documentElement: html,
    body,
    cookie: '',
    location: { pathname: '/' },
    addEventListener(name) { listeners.push(name); },
    removeEventListener() {},
    querySelector() { return null; },
    querySelectorAll() { return []; },
    getElementById() { return null; },
    createElement() { return fakeElement(); },
    dispatchEvent() { return true; }
  };
  const storage = {
    values: {},
    getItem(name) { return Object.prototype.hasOwnProperty.call(this.values, name) ? this.values[name] : null; },
    setItem(name, value) { this.values[name] = String(value); }
  };
  const media = {
    matches: false,
    addEventListener() {},
    removeEventListener() {}
  };
  const windowListeners = [];
  globalThis.document = doc;
  globalThis.localStorage = storage;
  globalThis.addEventListener = (name) => { windowListeners.push(name); };
  globalThis.removeEventListener = () => {};
  globalThis.matchMedia = () => media;
  globalThis.CustomEvent = class {
    constructor(type, init) {
      this.type = type;
      this.detail = (init && init.detail) || null;
    }
  };
  globalThis.window = globalThis;
  return { doc, html, listeners, storage };
}

const env = installFakeDOM();
const require = createRequire(import.meta.url);
require('./yd-app.js');
// The script publishes itself on the global object; require() returns an empty module object.
// 脚本把自身发布到全局对象上；require() 返回的是空模块对象。
const ydApp = globalThis.ydApp;

test('yd-app boots without throwing and exposes its API', () => {
  const api = globalThis.ydApp;
  assert.ok(api, 'ydApp must be published to the global object');
  for (const name of ['boot', 'theme', 'setTheme', 'toggleTheme', 'sidebar', 'formatCells']) {
    assert.equal(typeof api[name], 'function', `missing ${name}`);
  }
  // Boot registers the delegated listeners; aborting in the theme path would leave this list empty.
  for (const name of ['click', 'change', 'keydown', 'htmx:after:swap', 'htmx:ws:close', 'popstate']) {
    assert.ok(env.listeners.includes(name), `boot did not register ${name}`);
  }
  assert.equal(env.html.getAttribute('data-theme'), 'light', 'system preference resolves to light');
});

test('theme switches still write the attribute and the stored preference', () => {
  ydApp.theme('dark', false);
  assert.equal(env.html.getAttribute('data-theme'), 'dark');
  ydApp.setTheme('light');
  assert.equal(env.html.getAttribute('data-theme'), 'light');
  assert.equal(env.storage.getItem('yd_theme'), 'light');
});

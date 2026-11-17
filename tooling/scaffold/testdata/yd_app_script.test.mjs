/*
 * yd_app_script.test.mjs — behaviour tests for the generated project's shell script.
 * yd_app_script.test.mjs — 生成项目外壳脚本的行为测试。
 *
 * Run through `go test ./tooling/scaffold`, which copies templates/_name_/web/static/js/yd-app.js.tmpl
 * to a temporary .js file and passes its path as argv[2]. The suite supplies the small DOM surface the
 * script touches at load, then drives the htmx lifecycle events the shell owns:
 *
 *   1. a swap into a page carrying #yd-page-assets must preload that page's styles and scripts without
 *      throwing — the generated script once called an undefined win() helper here, so every navigation
 *      into the message centre, the component gallery or the moderation page lost its anti-flash
 *      preloading and painted unstyled content first;
 *   2. clicking a drawer trigger must not open an empty panel — the drawer opens on the htmx swap that
 *      brings its fragment, in the very task that paints it.
 *
 * 由 `go test ./tooling/scaffold` 调用：它把模板复制成临时 .js 文件并作为 argv[2] 传入。本套件提供脚本
 * 在加载期触及的少量 DOM 表面，然后驱动外壳负责的 htmx 生命周期事件：
 *
 *   1. 换入带 #yd-page-assets 的页面时必须预载该页样式与脚本且不得抛错——生成脚本曾在这里调用未定义的
 *      win()，导致每次进入消息中心、组件示例或治理页时的防闪预载全部失效、先绘制无样式内容；
 *   2. 点击抽屉触发器不得打开空面板——抽屉只在片段到达的那次 htmx 交换中打开，与内容同一次绘制。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const scriptPath = process.argv[2];
if (!scriptPath) {
  throw new Error('usage: node yd_app_script.test.mjs <path-to-generated-app.js>');
}

function fakeElement(tag) {
  const attrs = new Map();
  const el = {
    nodeType: 1,
    tagName: String(tag || 'div').toUpperCase(),
    children: [],
    attrs,
    classList: {
      add() {},
      remove() {},
      toggle() {},
      contains() { return false; }
    },
    style: { setProperty() {}, removeProperty() {} },
    setAttribute(name, value) { attrs.set(name, String(value)); },
    getAttribute(name) { return attrs.has(name) ? attrs.get(name) : null; },
    hasAttribute(name) { return attrs.has(name); },
    removeAttribute(name) { attrs.delete(name); },
    matches() { return false; },
    closest() { return null; },
    contains() { return false; },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    appendChild(child) { el.children.push(child); return child; },
    addEventListener() {},
    removeEventListener() {},
    focus() {},
    remove() {},
    getBoundingClientRect() {
      return { width: 0, height: 0, top: 0, right: 0, bottom: 0, left: 0 };
    }
  };
  return el;
}

function installFakeDOM() {
  const html = fakeElement('html');
  const body = fakeElement('body');
  const head = fakeElement('head');
  const listeners = new Map();
  const drawer = {
    open: false,
    shown: 0,
    showModal() { this.open = true; this.shown += 1; },
    close() { this.open = false; },
    addEventListener() {},
    querySelector() { return null; }
  };
  const drawerBody = fakeElement('div');
  drawerBody.id = 'yd-drawer-body';
  drawerBody.contains = (node) => node === drawerBody;
  const drawerErrors = fakeElement('div');
  drawerErrors.innerHTML = '';
  const byId = {
    'yd-drawer': drawer,
    'yd-drawer-body': drawerBody,
    'yd-drawer-errors': drawerErrors
  };

  const alpineNodes = [];
  const doc = {
    readyState: 'complete',
    documentElement: html,
    body,
    head,
    cookie: '',
    location: { pathname: '/' },
    addEventListener(name, fn) {
      if (!listeners.has(name)) { listeners.set(name, []); }
      listeners.get(name).push(fn);
    },
    removeEventListener() {},
    querySelector(selector) {
      if (selector === '#yd-drawer') { return drawer; }
      return null;
    },
    querySelectorAll(selector) {
      if (selector === '[x-data]') { return alpineNodes; }
      return [];
    },
    getElementById(id) { return byId[id] || null; },
    createElement(tag) { return fakeElement(tag); },
    dispatchEvent() { return true; }
  };

  const assetNode = fakeElement('div');
  assetNode.setAttribute('data-yd-styles', '/static/css/page.css');
  assetNode.setAttribute('data-yd-scripts', '/static/js/page-a.js /static/js/page-b.js');
  const drawerVendor = '/static/js/vendored/drawer-editor.js';
  const drawerWrapper = fakeElement('div');
  drawerWrapper.setAttribute('data-yd-editor-vendor', drawerVendor);

  globalThis.document = doc;
  globalThis.addEventListener = () => {};
  globalThis.removeEventListener = () => {};
  globalThis.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  globalThis.CustomEvent = class {
    constructor(type, init) {
      this.type = type;
      this.detail = (init && init.detail) || null;
    }
  };
  globalThis.DOMParser = class {
    parseFromString(text) {
      const page = String(text).indexOf('id="yd-page-assets"') >= 0;
      return {
        querySelector(selector) {
          if (page && selector === '#yd-page-assets') { return assetNode; }
          if (!page && selector === '[data-yd-editor-vendor]') { return drawerWrapper; }
          return null;
        }
      };
    }
  };
  globalThis.window = globalThis;

  return { doc, html, body, head, listeners, drawer, drawerBody, assetNode, alpineNodes, drawerVendor };
}

const env = installFakeDOM();
createRequire(import.meta.url)(scriptPath);

function dispatch(name, event) {
  const handlers = env.listeners.get(name) || [];
  for (const handler of handlers) {
    handler(event);
  }
}

test('a swap into a page with #yd-page-assets preloads its assets without throwing', () => {
  const text = '<div id="yd-page-assets" data-yd-styles="/static/css/page.css" data-yd-scripts="/static/js/page-a.js /static/js/page-b.js"></div>';
  assert.doesNotThrow(() => {
    dispatch('htmx:before:swap', { detail: { ctx: { text, target: env.body } } });
  }, 'preloading must not throw; an undefined helper used to break it');

  const appended = env.head.children;
  const styles = appended.filter((node) => node.rel === 'stylesheet' && node.href === '/static/css/page.css');
  const preloads = appended.filter((node) => node.rel === 'preload' && node.as === 'script');
  assert.equal(styles.length, 1, 'the page stylesheet is inserted before the swap');
  assert.deepEqual(
    preloads.map((node) => node.href),
    ['/static/js/page-a.js', '/static/js/page-b.js'],
    'both page scripts are preloaded before the swap'
  );
});

test('a drawer fragment with only an editor wrapper preloads its vendor', () => {
  dispatch('htmx:before:swap', {
    detail: { ctx: { text: '<div data-yd-editor-vendor="' + env.drawerVendor + '"></div>', target: env.body } }
  });
  const preloads = env.head.children
    .filter((node) => node.rel === 'preload' && node.as === 'script')
    .map((node) => node.href);
  assert.ok(
    preloads.includes(env.drawerVendor),
    'the editor wrapper without a page-asset declaration still preloads its vendor'
  );
});

test('clicking a drawer trigger does not open an empty drawer', () => {
  const trigger = fakeElement('button');
  trigger.closest = (selector) => selector === '[data-yd-drawer-open]' ? trigger : null;
  const click = { target: trigger, preventDefault() {}, stopImmediatePropagation() {} };
  dispatch('click', click);
  assert.equal(env.drawer.shown, 0, 'the drawer must stay closed until its fragment is present');
});

test('the drawer opens once its fragment is in the DOM, before settlement', () => {
  dispatch('htmx:before:settle', { detail: { task: { target: env.drawerBody } } });
  assert.equal(env.drawer.shown, 1, 'the drawer opens together with its content');
});

test('a settlement outside the drawer does not open it', () => {
  env.drawer.open = false;
  env.drawer.shown = 0;
  const elsewhere = fakeElement('div');
  dispatch('htmx:before:settle', { detail: { task: { target: elsewhere } } });
  assert.equal(env.drawer.shown, 0, 'only a swap into the drawer body opens the drawer');
});

test('the history snapshot is repaired before the morph clones the scope stack', () => {
  const scope = fakeElement('div');
  scope.setAttribute('data-has-alpine-state', 'true');
  scope._x_dataStack = [];
  env.alpineNodes.push(scope);
  let initialised = 0;
  globalThis.Alpine = {
    initTree(node) {
      initialised += 1;
      node._x_dataStack = [{}];
    }
  };

  dispatch('htmx:after:history:update', { detail: {} });

  assert.equal(initialised, 1, 'the emptied Alpine scope is rebuilt before morph copies it');
  assert.equal(scope.hasAttribute('data-has-alpine-state'), false, 'the destroyed marker is cleared');
  assert.equal(scope._x_dataStack.length, 1, 'the scope stack is no longer empty');
  delete globalThis.Alpine;
});
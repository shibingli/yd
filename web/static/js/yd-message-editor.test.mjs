/*
 * yd-message-editor.test.mjs — idle warm-up test for the message editor loader.
 * yd-message-editor.test.mjs — 消息编辑器加载器的空闲预热测试。
 *
 * Run with | 运行方式:
 *   node web/static/js/yd-message-editor.test.mjs
 *
 * The compact composer is the message centre's primary input. It used to load EasyMDE only when the
 * composer appeared, so opening a conversation painted a bare textarea and then replaced it with the
 * toolbar a moment later — the visible flicker. The loader now fetches the vendor during the browser's
 * idle time without upgrading any editor; this suite supplies the small DOM surface and asserts that
 * warm-up appends exactly one same-origin script and that settling the load upgrades nothing.
 * 紧凑撰写区是消息中心的主输入区。过去只有撰写区出现时才加载 EasyMDE，打开会话会先画出裸文本域、
 * 片刻后才换成工具栏——也就是用户看到的闪动。现在加载器在浏览器空闲时取脚本且不升级任何编辑器；本套件
 * 提供少量 DOM 表面，断言预热只追加一个同源脚本，且加载结算时不会升级编辑器。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

function fakeElement(tag) {
  const attrs = new Map();
  const el = {
    nodeType: 1,
    tagName: String(tag || 'div').toUpperCase(),
    attrs,
    children: [],
    setAttribute(name, value) { attrs.set(name, String(value)); },
    getAttribute(name) { return attrs.has(name) ? attrs.get(name) : null; },
    hasAttribute(name) { return attrs.has(name); },
    removeAttribute(name) { attrs.delete(name); },
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    addEventListener() {},
    appendChild(child) { el.children.push(child); return child; }
  };
  return el;
}

const vendorURL = '/static/js/vendored/easymde.min.js?v=test';
const wrapper = fakeElement('div');
wrapper.setAttribute('data-yd-editor-vendor', vendorURL);

let idleCallback = null;
let wrapperQueries = 0;
const editorWrappers = [];
const head = fakeElement('head');
const doc = {
  readyState: 'complete',
  documentElement: fakeElement('html'),
  body: fakeElement('body'),
  head,
  addEventListener() {},
  removeEventListener() {},
  querySelector(selector) {
    if (selector === '[data-yd-editor-vendor]') { return wrapper; }
    return null;
  },
  querySelectorAll(selector) {
    return selector === '[data-yd-editor]' ? editorWrappers : [];
  },
  createElement(tag) { return fakeElement(tag); }
};

// A compact wrapper is the only shape initOne upgrades without an explicit intent; if a warm-up
// accidentally calls init(document), this wrapper records the query.
// | 紧凑包装是 initOne 在没有显式意图时会升级的唯一形态；若预热误调 init(document)，这个包装会记录查询。
function compactWrapper() {
  const el = fakeElement('div');
  el.setAttribute('data-yd-editor-compact', '');
  el.setAttribute('data-yd-editor-vendor', vendorURL);
  const textarea = fakeElement('textarea');
  el.querySelector = (selector) => {
    wrapperQueries += 1;
    return selector === 'textarea' ? textarea : null;
  };
  return el;
}

globalThis.document = doc;
globalThis.window = globalThis;
globalThis.MutationObserver = undefined;
globalThis.requestIdleCallback = (callback) => { idleCallback = callback; };
globalThis.matchMedia = () => ({ matches: false, addEventListener() {} });
globalThis.CustomEvent = class {
  constructor(type, init) {
    this.type = type;
    this.detail = (init && init.detail) || null;
  }
};

createRequire(import.meta.url)('./yd-message-editor.js');

test('the page declares a vendor URL and warm-up waits for idle time', () => {
  assert.equal(typeof idleCallback, 'function', 'the loader schedules an idle warm-up');
  assert.equal(head.children.length, 0, 'nothing is fetched before the browser is idle');
});

test('idle warm-up appends one same-origin script and upgrades nothing', () => {
  editorWrappers.push(compactWrapper());
  idleCallback();
  idleCallback = null;
  const scripts = head.children.filter((node) => node.tagName === 'SCRIPT');
  assert.equal(scripts.length, 1, 'exactly one vendor script is appended');
  assert.equal(scripts[0].src, vendorURL, 'the declared same-origin URL is used');
  assert.equal(scripts[0].async, false, 'the script keeps execution order');

  // Settling the load without a real EasyMDE must not scan or upgrade the page: a warm-up is not an
  // editor intent. | 在没有真实 EasyMDE 的情况下结算加载不得扫描或升级页面：预热不是编辑意图。
  assert.doesNotThrow(() => scripts[0].onload());
  assert.equal(wrapperQueries, 0, 'the warm-up never queries the editor wrapper');
});

test('a failed warm-up does not poison a later editor intent', () => {
  const scripts = head.children.filter((node) => node.tagName === 'SCRIPT');
  assert.equal(scripts.length, 1, 'the warm-up attempt is the only script so far');

  // The idle fetch fails; the next real intent must start a fresh request instead of hitting a
  // permanently settled state. | 空闲请求失败；下一次真实意图必须重新发起请求，而不是撞上永久结算状态。
  scripts[0].onerror();
  globalThis.yd.messageEditor.init(doc);
  const afterRetry = head.children.filter((node) => node.tagName === 'SCRIPT');
  assert.equal(afterRetry.length, 2, 'the intent retries the vendor after a failed warm-up');
  assert.equal(afterRetry[1].src, vendorURL, 'the retry uses the declared URL');
  assert.ok(wrapperQueries > 0, 'the intent reached the wrapper');
});
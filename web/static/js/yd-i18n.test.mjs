/*
 * yd-i18n.test.mjs — node:test suite for yd-i18n.js.
 * yd-i18n.test.mjs — yd-i18n.js 的 node:test 测试集。
 *
 * Run with | 运行方式:
 *   node web/static/js/yd-i18n.test.mjs
 *
 * The module is loaded through createRequire so the very same UMD file the browser gets is the
 * one under test; the browser globals it needs (document, localStorage) are stubbed below.
 * 通过 createRequire 加载模块，保证被测代码与浏览器拿到的 UMD 文件完全一致；
 * 它需要的浏览器全局对象（document、localStorage）在下方以桩实现提供。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const ydI18n = require('./yd-i18n.js');

/* ------------------------------------------------------------------ *
 * Minimal DOM stub | 最小 DOM 桩
 * ------------------------------------------------------------------ */

const BIND_ATTRIBUTES = ['data-yd-i18n', 'data-yd-i18n-attr', 'data-yd-lang'];

// matchesSelector understands the small subset of selectors the module actually uses:
// a tag name or an [attribute] test, comma separated. | 仅支持模块真正使用的小型选择器子集：
// 标签名或 [属性] 测试，逗号分隔。
function matchesSelector(node, selector) {
  return String(selector)
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
    .some((part) => {
      if (part.startsWith('[')) {
        return node.hasAttribute(part.slice(1, -1));
      }
      return node.tagName === part.toUpperCase();
    });
}

function collect(node, selector, out) {
  if (!node) {
    return out;
  }
  const children = node.children || [];
  for (const child of children) {
    if (child.nodeType === 1 && matchesSelector(child, selector)) {
      out.push(child);
    }
    collect(child, selector, out);
  }
  return out;
}

function el(tag, attrs = {}, children = []) {
  const node = {
    nodeType: 1,
    tagName: tag.toUpperCase(),
    attributes: { ...attrs },
    children: [],
    parentNode: null,
    text: '',
    getAttribute(name) {
      return Object.prototype.hasOwnProperty.call(this.attributes, name) ? this.attributes[name] : null;
    },
    setAttribute(name, value) {
      this.attributes[name] = String(value);
    },
    removeAttribute(name) {
      delete this.attributes[name];
    },
    hasAttribute(name) {
      return Object.prototype.hasOwnProperty.call(this.attributes, name);
    },
    get textContent() {
      if (this.children.length > 0) {
        return this.children.map((child) => child.textContent).join('');
      }
      return this.text;
    },
    set textContent(value) {
      this.children = [];
      this.text = String(value);
    },
    appendChild(child) {
      child.parentNode = this;
      this.children.push(child);
      return child;
    },
    remove() {
      if (this.parentNode) {
        const index = this.parentNode.children.indexOf(this);
        if (index >= 0) {
          this.parentNode.children.splice(index, 1);
        }
      }
    },
    matches(selector) {
      return matchesSelector(this, selector);
    },
    querySelectorAll(selector) {
      return collect(this, selector, []);
    }
  };
  for (const child of children) {
    node.appendChild(child);
  }
  return node;
}

function findById(node, id) {
  if (!node) {
    return null;
  }
  for (const child of node.children || []) {
    if (child.nodeType === 1) {
      if (child.getAttribute('id') === id) {
        return child;
      }
      const found = findById(child, id);
      if (found) {
        return found;
      }
    }
  }
  return null;
}

function createDocument() {
  const documentElement = el('html', { lang: 'en' });
  const body = el('body');
  documentElement.appendChild(body);
  return {
    nodeType: 9,
    documentElement,
    body,
    cookie: '',
    listeners: {},
    getElementById(id) {
      return findById(this.body, id);
    },
    querySelectorAll(selector) {
      return collect(this.body, selector, []);
    },
    addEventListener(type, handler) {
      (this.listeners[type] = this.listeners[type] || []).push(handler);
    },
    removeEventListener(type, handler) {
      this.listeners[type] = (this.listeners[type] || []).filter((fn) => fn !== handler);
    },
    // dispatch is a test helper that mimics htmx's custom events. | dispatch 是模拟 htmx 自定义事件的测试辅助函数。
    dispatch(type, detail) {
      for (const handler of this.listeners[type] || []) {
        handler({ type, detail });
      }
    }
  };
}

const doc = createDocument();
globalThis.document = doc;

// localStorage stub shared by every test. | 所有测试共用的 localStorage 桩。
const store = new Map();
globalThis.localStorage = {
  getItem: (key) => (store.has(key) ? store.get(key) : null),
  setItem: (key, value) => store.set(key, String(value)),
  removeItem: (key) => store.delete(key),
  clear: () => store.clear()
};

const CATALOGS = {
  'zh-CN': {
    user: { title: '用户管理', greeting: '你好，{{.name}}！' },
    lang: { 'zh-CN': '简体中文', en: 'English' }
  },
  en: {
    user: { title: 'Users', greeting: 'Hello, {name}!' },
    lang: { 'zh-CN': '简体中文', en: 'English' },
    only: { english: 'English only' }
  }
};

// reset clears the tree and the persisted preference; the registered listeners stay in place,
// exactly like a document that keeps living between swaps.
// reset 清空树与已持久化的偏好；已注册的监听器保留，与真实文档在多次交换之间持续存在一致。
function reset() {
  doc.body.children = [];
  doc.cookie = '';
  store.clear();
  globalThis.localStorage.clear();
}

/* ------------------------------------------------------------------ *
 * Translation | 翻译
 * ------------------------------------------------------------------ */

test('t translates in the active language and interpolates named arguments', () => {
  reset();
  ydI18n.init({ lang: 'zh-CN', fallback: 'en', catalogs: CATALOGS });

  assert.equal(ydI18n.current(), 'zh-CN');
  assert.equal(ydI18n.t('user.title'), '用户管理');
  // Server style braces and client style braces both interpolate. | 服务端花括号与客户端花括号都能插值。
  assert.equal(ydI18n.t('user.greeting', { name: 'YunDev' }), '你好，YunDev！');

  ydI18n.setLang('en');
  assert.equal(ydI18n.t('user.greeting', { name: 'YunDev' }), 'Hello, YunDev!');
});

test('t falls back to the fallback catalog, then to the key itself', () => {
  reset();
  ydI18n.init({ lang: 'zh-CN', fallback: 'en', catalogs: CATALOGS });

  // Missing in zh-CN, present in en. | zh-CN 缺失、en 存在。
  assert.equal(ydI18n.t('only.english'), 'English only');
  // Missing everywhere: the key is returned, exactly like the Go bundle. | 全部缺失时返回 key，与 Go 包一致。
  assert.equal(ydI18n.t('nope.missing'), 'nope.missing');
});

test('lookup is case insensitive and tolerates a bare language tag', () => {
  reset();
  ydI18n.init({ lang: 'zh', fallback: 'en', catalogs: CATALOGS });

  // "zh" resolves onto the zh-CN catalog. | "zh" 会解析到 zh-CN 目录。
  assert.equal(ydI18n.t('USER.TITLE'), '用户管理');
});

/* ------------------------------------------------------------------ *
 * Persistence | 持久化
 * ------------------------------------------------------------------ */

test('setLang persists to localStorage, to the yd_lang cookie and to <html lang>', () => {
  reset();
  ydI18n.init({ lang: 'en', fallback: 'en', catalogs: CATALOGS });

  const seen = [];
  const off = ydI18n.on((payload) => seen.push(payload.lang));

  ydI18n.setLang('zh-CN');

  assert.equal(ydI18n.current(), 'zh-CN');
  assert.equal(store.get('yd_lang'), 'zh-CN');
  assert.match(doc.cookie, /yd_lang=zh-CN/);
  assert.equal(doc.documentElement.lang, 'zh-CN');
  assert.deepEqual(seen, ['zh-CN']);

  off();
  ydI18n.setLang('en');
  assert.deepEqual(seen, ['zh-CN'], 'unsubscribed listeners must not be called | 取消订阅后不应再被调用');

  // A later init picks the remembered preference back up. | 之后 init 会读回记住的偏好。
  ydI18n.init({ lang: 'en', fallback: 'en', catalogs: CATALOGS });
  assert.equal(ydI18n.current(), 'en');
  ydI18n.setLang('zh-CN');
  ydI18n.init({ lang: 'en', fallback: 'en', catalogs: CATALOGS });
  assert.equal(ydI18n.current(), 'zh-CN');
});

test('setLang falls back to the cookie when localStorage is empty', () => {
  reset();
  ydI18n.init({ lang: 'en', fallback: 'en', catalogs: CATALOGS });
  ydI18n.setLang('zh-CN');
  store.delete('yd_lang');

  ydI18n.init({ lang: 'en', fallback: 'en', catalogs: CATALOGS });
  assert.equal(ydI18n.current(), 'zh-CN');
});

/* ------------------------------------------------------------------ *
 * Declarative bindings | 声明式绑定
 * ------------------------------------------------------------------ */

test('init translates data-yd-i18n text and data-yd-i18n-attr attributes', () => {
  reset();
  doc.body.appendChild(el('span', { 'data-yd-i18n': 'user.title' }));
  doc.body.appendChild(
    el('input', {
      'data-yd-i18n-attr': 'placeholder:user.title, aria-label:user.title',
      placeholder: 'stale',
      'aria-label': 'stale'
    })
  );

  ydI18n.init({ lang: 'zh-CN', fallback: 'en', catalogs: CATALOGS });

  const span = doc.body.children[0];
  const input = doc.body.children[1];
  assert.equal(span.textContent, '用户管理');
  assert.equal(input.getAttribute('placeholder'), '用户管理');
  assert.equal(input.getAttribute('aria-label'), '用户管理');
});

test('data-yd-i18n-args and [data-yd-lang] are applied', () => {
  reset();
  const greeting = el('span', {
    'data-yd-i18n': 'user.greeting',
    'data-yd-i18n-args': '{"name":"YunDev"}'
  });
  const zh = el('button', { 'data-yd-lang': 'zh-CN' });
  const en = el('button', { 'data-yd-lang': 'en' });
  doc.body.appendChild(greeting);
  doc.body.appendChild(zh);
  doc.body.appendChild(en);

  ydI18n.init({ lang: 'zh-CN', fallback: 'en', catalogs: CATALOGS });
  assert.equal(greeting.textContent, '你好，YunDev！');
  assert.equal(zh.getAttribute('aria-pressed'), 'true');
  assert.equal(en.getAttribute('aria-pressed'), 'false');

  ydI18n.setLang('en');
  assert.equal(greeting.textContent, 'Hello, YunDev!');
  assert.equal(zh.getAttribute('aria-pressed'), 'false');
  assert.equal(en.getAttribute('aria-pressed'), 'true');
});

test('setLang rewrites the page in place, without a reload', () => {
  reset();
  const title = el('h1', { 'data-yd-i18n': 'user.title' });
  doc.body.appendChild(title);

  ydI18n.init({ lang: 'en', fallback: 'en', catalogs: CATALOGS });
  assert.equal(title.textContent, 'Users');

  ydI18n.setLang('zh-CN');
  assert.equal(title.textContent, '用户管理');
});

/* ------------------------------------------------------------------ *
 * htmx integration | htmx 集成
 * ------------------------------------------------------------------ */

test('htmx:after:swap translates new nodes only', () => {
  reset();
  const existing = el('span', { 'data-yd-i18n': 'user.title' });
  doc.body.appendChild(existing);

  ydI18n.init({ lang: 'en', fallback: 'en', catalogs: CATALOGS });
  assert.equal(existing.textContent, 'Users');

  // Simulate a morph swap that rewrote the text server side. | 模拟 morph 交换在服务端重写了文本。
  existing.textContent = 'Users (server rendered)';

  const added = el('span', { 'data-yd-i18n': 'user.title' });
  doc.body.appendChild(added);

  const touched = ydI18n.bind(doc, false);
  assert.equal(touched, 1, 'only the new node may be translated | 只允许翻译新增节点');
  assert.equal(added.textContent, 'Users');
  assert.equal(existing.textContent, 'Users (server rendered)', 'existing nodes must be left alone | 已有节点不应被改动');
});

test('htmx:after:swap listener binds fragments added by a swap', () => {
  reset();
  ydI18n.init({ lang: 'zh-CN', fallback: 'en', catalogs: CATALOGS });

  const fragment = el('div', {}, [el('span', { 'data-yd-i18n': 'user.title' })]);
  doc.body.appendChild(fragment);

  doc.dispatch('htmx:after:swap', { ctx: { target: fragment } });
  assert.equal(fragment.children[0].textContent, '用户管理');

  // A malformed event must not throw. | 结构不完整的事件不应抛错。
  doc.dispatch('htmx:after:swap', {});
});

test('the swap hook is registered against the document it is initialised with', () => {
  reset();
  const other = createDocument();
  globalThis.document = other;
  try {
    ydI18n.init({ lang: 'en', fallback: 'en', catalogs: CATALOGS });
    const added = el('span', { 'data-yd-i18n': 'user.title' });
    other.body.appendChild(added);

    other.dispatch('htmx:after:swap', { ctx: { target: other.body } });
    assert.equal(added.textContent, 'Users');
  } finally {
    globalThis.document = doc;
  }
});

/* ------------------------------------------------------------------ *
 * Intl formatting | Intl 格式化
 * ------------------------------------------------------------------ */

test('t.n formats numbers, percentages, currencies and dates', () => {
  reset();
  ydI18n.init({ lang: 'en', fallback: 'en', catalogs: CATALOGS });

  assert.equal(ydI18n.t.n(1234.5, { minimumFractionDigits: 2 }), '1,234.50');
  assert.equal(ydI18n.t.n(0.2567, { style: 'percent', maximumFractionDigits: 1 }), '25.7%');
  assert.match(ydI18n.t.n(9876.5, { currency: 'CNY' }), /9,876\.50/);

  const when = new Date('2026-10-16T09:30:00Z');
  assert.match(ydI18n.t.n(when, { kind: 'date' }), /2026/);
  assert.match(ydI18n.t.n(when, { kind: 'datetime' }), /2026/);

  // The locale follows the active language. | 区域设置跟随当前语言。
  ydI18n.setLang('zh-CN');
  assert.match(ydI18n.t.n(when, { kind: 'date' }), /2026/);
});

/* ------------------------------------------------------------------ *
 * Server injected catalog | 服务端注入的目录
 * ------------------------------------------------------------------ */

test('init reads <script id="yd-i18n-catalog"> when no catalogs are passed', () => {
  reset();
  const envelope = {
    lang: 'en',
    fallback: 'en',
    catalogs: { en: { user: { title: 'Users' } } }
  };
  const script = el('script', { id: 'yd-i18n-catalog', type: 'application/json' });
  script.textContent = JSON.stringify(envelope);
  doc.body.appendChild(script);

  ydI18n.init();
  assert.equal(ydI18n.current(), 'en');
  assert.equal(ydI18n.t('user.title'), 'Users');
});

test('a quoted catalog envelope (template.HTML) is unwrapped', () => {
  reset();
  // html/template treats <script type="application/json"> as a JavaScript container and
  // json.Marshal's any value that is not template.JS, so the envelope arrives double encoded.
  // html/template 把 <script type="application/json"> 当作 JavaScript 容器，
  // 非 template.JS 的值都会被 json.Marshal，信封因此是双重编码的。
  const envelope = {
    lang: 'en',
    fallback: 'en',
    catalogs: { en: { user: { title: 'Users' } } }
  };
  const script = el('script', { id: 'yd-i18n-catalog', type: 'application/json' });
  script.textContent = JSON.stringify(JSON.stringify(envelope));
  doc.body.appendChild(script);

  ydI18n.init();
  assert.equal(ydI18n.current(), 'en');
  assert.equal(ydI18n.t('user.title'), 'Users');
});

test('an entity escaped catalog envelope is still parsed', () => {
  reset();
  // html/template escapes quotes when the value is a plain string, not template.HTML.
  // 当值是普通字符串而非 template.HTML 时，html/template 会转义引号。
  const escaped = JSON.stringify({
    lang: 'zh-CN',
    fallback: 'zh-CN',
    catalogs: { 'zh-CN': { user: { title: '用户管理' } } }
  })
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&#34;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');

  const script = el('script', { id: 'yd-i18n-catalog', type: 'application/json' });
  script.textContent = escaped;
  doc.body.appendChild(script);

  ydI18n.init();
  assert.equal(ydI18n.t('user.title'), '用户管理');
});

test('init without an injected catalog and without catalogs still returns keys', () => {
  reset();
  ydI18n.init();
  assert.equal(ydI18n.t('user.title'), 'user.title');
  assert.equal(ydI18n.available().length, 0);
});

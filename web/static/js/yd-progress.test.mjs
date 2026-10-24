/*
 * yd-progress.test.mjs — node:test suite for yd-progress.js.
 * yd-progress.test.mjs — yd-progress.js 的 node:test 测试集。
 *
 * Run with | 运行方式:
 *   node web/static/js/yd-progress.test.mjs
 *
 * The suite loads the very UMD file the browser receives. The clock and the
 * handful of DOM APIs the module touches are stubbed below, so every timing
 * rule (delay, trickle, fade, reset) is asserted without real waits.
 * 本套件加载的正是浏览器收到的 UMD 文件。计时器与模块真正用到的少量 DOM API
 * 在下方以桩实现提供，因此每条计时规则（延迟、逼近、淡出、复位）都无需真实等待。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const ydProgress = require('./yd-progress.js');

/* ------------------------------------------------------------------ *
 * Fake clock | 假计时器
 * ------------------------------------------------------------------ */

function fakeClock() {
  let now = 0;
  let nextId = 1;
  const timers = new Map();

  function schedule(fn, ms, every) {
    const id = nextId++;
    const delay = Number(ms) || 0;
    timers.set(id, { fn, at: now + delay, every });
    return id;
  }

  return {
    setTimeout(fn, ms) {
      return schedule(fn, ms, null);
    },
    clearTimeout(id) {
      timers.delete(id);
    },
    setInterval(fn, ms) {
      return schedule(fn, ms, Math.max(1, Number(ms) || 1));
    },
    clearInterval(id) {
      timers.delete(id);
    },
    advance(ms) {
      const target = now + ms;
      for (;;) {
        let picked = null;
        for (const [id, timer] of timers) {
          if (timer.at > target) {
            continue;
          }
          if (!picked || timer.at < picked.timer.at || (timer.at === picked.timer.at && id < picked.id)) {
            picked = { id, timer };
          }
        }
        if (!picked) {
          break;
        }
        now = picked.timer.at;
        if (picked.timer.every) {
          picked.timer.at = now + picked.timer.every;
        } else {
          timers.delete(picked.id);
        }
        picked.timer.fn();
      }
      now = target;
    },
    pending() {
      return timers.size;
    }
  };
}

/* ------------------------------------------------------------------ *
 * Minimal DOM stub | 最小 DOM 桩
 * ------------------------------------------------------------------ */

// matches understands the small selector subset the module uses: a tag name,
// a [attribute] test, a .class test, or tag[attribute]. | matches 只理解模块
// 使用的小型选择器子集：标签名、[属性]、.类名，或 tag[属性]。
function matchOne(node, part) {
  if (!part) {
    return false;
  }
  if (part.startsWith('.')) {
    return !!(node.classList && node.classList.contains(part.slice(1)));
  }
  if (part.startsWith('[') && part.endsWith(']')) {
    return node.hasAttribute(part.slice(1, -1));
  }
  const bracket = part.indexOf('[');
  const tag = (bracket === -1 ? part : part.slice(0, bracket)).toUpperCase();
  if (tag && node.tagName !== tag) {
    return false;
  }
  if (bracket !== -1) {
    return node.hasAttribute(part.slice(bracket + 1, part.lastIndexOf(']')));
  }
  return true;
}

function matches(node, selector) {
  return String(selector)
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
    .some((part) => matchOne(node, part));
}

function walk(node, selector, out) {
  for (const child of node.children || []) {
    if (child.nodeType === 1) {
      if (matches(child, selector)) {
        out.push(child);
      }
      walk(child, selector, out);
    }
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
    style: {},
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
    appendChild(child) {
      child.parentNode = this;
      this.children.push(child);
      return child;
    },
    querySelector(selector) {
      return walk(this, selector, [])[0] || null;
    },
    querySelectorAll(selector) {
      return walk(this, selector, []);
    },
    matches(selector) {
      return matches(this, selector);
    },
    closest(selector) {
      let cur = this;
      while (cur && cur.nodeType === 1) {
        if (matches(cur, selector)) {
          return cur;
        }
        cur = cur.parentNode;
      }
      return null;
    }
  };

  const classes = new Set(String(attrs.class || '').split(/\s+/).filter(Boolean));
  node.classList = {
    add(...names) {
      for (const name of names) {
        classes.add(name);
      }
      node.attributes.class = [...classes].join(' ');
    },
    remove(...names) {
      for (const name of names) {
        classes.delete(name);
      }
      node.attributes.class = [...classes].join(' ');
    },
    contains(name) {
      return classes.has(name);
    }
  };

  for (const child of children) {
    node.appendChild(child);
  }
  return node;
}

function progressDom() {
  const bar = el('div', { class: 'yd-progress__bar' });
  const root = el('div', { id: 'yd-progress', class: 'yd-progress', 'data-yd-progress': '', 'aria-hidden': 'true' }, [bar]);
  const body = el('body', {}, [root]);
  const document = {
    documentElement: el('html'),
    body,
    listeners: Object.create(null),
    addEventListener(type, fn) {
      (this.listeners[type] || (this.listeners[type] = [])).push(fn);
    },
    dispatch(type, event) {
      for (const fn of this.listeners[type] || []) {
        fn(event);
      }
    },
    querySelector(selector) {
      return matches(root, selector) ? root : walk(body, selector, [])[0] || null;
    }
  };
  return { root, bar, body, document };
}

function fakeWindow(document, clock) {
  const listeners = Object.create(null);
  return {
    document,
    location: { href: 'https://example.test/app', origin: 'https://example.test' },
    URL,
    listeners,
    addEventListener(type, fn) {
      (listeners[type] || (listeners[type] = [])).push(fn);
    },
    dispatch(type, event) {
      for (const fn of listeners[type] || []) {
        fn(event);
      }
    },
    setTimeout: clock.setTimeout,
    clearTimeout: clock.clearTimeout,
    setInterval: clock.setInterval,
    clearInterval: clock.clearInterval
  };
}

function clickEvent(target, extra = {}) {
  return {
    type: 'click',
    target,
    button: 0,
    metaKey: false,
    ctrlKey: false,
    shiftKey: false,
    altKey: false,
    defaultPrevented: false,
    preventDefault() {
      this.defaultPrevented = true;
    },
    ...extra
  };
}

function submitEvent(target, extra = {}) {
  return {
    type: 'submit',
    target,
    defaultPrevented: false,
    preventDefault() {
      this.defaultPrevented = true;
    },
    ...extra
  };
}

function harness() {
  const clock = fakeClock();
  const dom = progressDom();
  const win = fakeWindow(dom.document, clock);
  const progress = ydProgress.install(win);
  return { clock, dom, win, progress };
}

/* ------------------------------------------------------------------ *
 * Controller | 控制器
 * ------------------------------------------------------------------ */

test('create keeps the bar hidden below the show delay | 短于显示延迟的请求不显示进度条', () => {
  const clock = fakeClock();
  const dom = progressDom();
  const progress = ydProgress.create({ element: dom.root, bar: dom.bar, clock });

  progress.begin();
  clock.advance(100);
  assert.equal(progress.visible(), false);
  assert.equal(dom.root.classList.contains('yd-progress--active'), false);

  progress.end();
  clock.advance(1000);
  assert.equal(progress.visible(), false);
  assert.equal(progress.value(), 0);
});

test('create reveals at 8% and trickles toward the ceiling | 显示后从 8% 起缓慢逼近上限', () => {
  const clock = fakeClock();
  const dom = progressDom();
  const progress = ydProgress.create({ element: dom.root, bar: dom.bar, clock });

  progress.begin();
  clock.advance(120);
  assert.equal(progress.visible(), true);
  assert.equal(dom.root.classList.contains('yd-progress--active'), true);
  assert.equal(dom.root.getAttribute('aria-hidden'), 'true', 'the decorative bar must stay hidden from assistive technology | 装饰性进度条必须对辅助技术保持隐藏');
  assert.ok(progress.value() >= 0.08, `expected at least 0.08, got ${progress.value()}`);
  assert.match(dom.bar.style.transform, /^scaleX\(/);

  const before = progress.value();
  clock.advance(400);
  assert.ok(progress.value() > before, 'the bar must advance while the request is pending | 请求未完成时进度必须继续前进');
  clock.advance(60000);
  const ceiling = progress.value();
  assert.ok(ceiling <= 0.9, `the bar must never pass the ceiling, got ${ceiling}`);
});

test('create completes, fades and resets after the last request | 最后一个请求结束后补满、淡出并复位', () => {
  const clock = fakeClock();
  const dom = progressDom();
  const progress = ydProgress.create({ element: dom.root, bar: dom.bar, clock });

  progress.begin();
  clock.advance(120);
  progress.end();

  assert.equal(progress.value(), 1);
  assert.equal(dom.bar.style.transform, 'scaleX(1)');
  assert.equal(dom.root.classList.contains('yd-progress--active'), true);

  clock.advance(220);
  assert.equal(dom.root.classList.contains('yd-progress--active'), false);
  assert.equal(dom.root.getAttribute('aria-hidden'), 'true');

  clock.advance(220);
  assert.equal(progress.visible(), false);
  assert.equal(progress.value(), 0);
  assert.equal(dom.bar.style.transform, 'scaleX(0)');
});

test('create stays visible until every concurrent request finishes | 并发请求全部结束前保持可见', () => {
  const clock = fakeClock();
  const dom = progressDom();
  const progress = ydProgress.create({ element: dom.root, bar: dom.bar, clock });

  progress.begin();
  progress.begin();
  clock.advance(120);
  assert.equal(progress.visible(), true);

  progress.end();
  clock.advance(1000);
  assert.equal(progress.visible(), true);
  assert.ok(progress.value() < 1);

  progress.end();
  assert.equal(progress.value(), 1);
});

test('create restarts from 8% when a request arrives during the fade | 淡出期间的新请求从 8% 重新开始', () => {
  const clock = fakeClock();
  const dom = progressDom();
  const progress = ydProgress.create({ element: dom.root, bar: dom.bar, clock });

  progress.begin();
  clock.advance(120);
  progress.end();
  clock.advance(100); // inside the fade | 处于淡出中

  progress.begin();
  assert.equal(progress.visible(), true);
  assert.equal(progress.value(), 0.08);
  assert.equal(dom.root.classList.contains('yd-progress--active'), true);

  progress.end();
  assert.equal(progress.value(), 1);
});

/* ------------------------------------------------------------------ *
 * install: htmx and navigation | install：htmx 与导航
 * ------------------------------------------------------------------ */

test('install follows htmx request events | 跟随 htmx 请求事件', () => {
  const { clock, dom, win, progress } = harness();
  assert.ok(progress, 'install must bind the rendered bar | install 必须绑定已渲染的进度条');

  dom.document.dispatch('htmx:before:request', {});
  clock.advance(120);
  assert.equal(progress.visible(), true);

  dom.document.dispatch('htmx:finally:request', {});
  assert.equal(progress.value(), 1);
  clock.advance(440);
  assert.equal(progress.visible(), false);
  assert.equal(win.listeners.pageshow.length, 1);
});

test('install follows a plain same-origin link after the event settles | 同源普通链接在事件结算后启动', () => {
  const { clock, dom } = harness();
  const link = el('a', { href: '/users' });
  const event = clickEvent(link);

  dom.document.dispatch('click', event);
  assert.ok(clock.pending() > 0, 'the navigation intent must schedule a deferred check | 导航意图必须排期一次延后判定');
  clock.advance(0);
  clock.advance(120);
  assert.equal(dom.root.classList.contains('yd-progress--active'), true);
});

test('install ignores htmx links, modified clicks and non-navigating targets | 忽略 htmx 链接、修饰键点击与非导航目标', () => {
  const { clock, dom } = harness();

  const htmxLink = el('a', { href: '/users', 'hx-get': '/users' });
  dom.document.dispatch('click', clickEvent(htmxLink));

  const boosted = el('a', { href: '/users', 'hx-boost': 'true' });
  dom.document.dispatch('click', clickEvent(boosted));

  const inheritedBoost = el('nav', { 'hx-boost:inherited': 'true' });
  inheritedBoost.appendChild(el('a', { href: '/users' }));
  dom.document.dispatch('click', clickEvent(inheritedBoost.children[0]));

  const modified = el('a', { href: '/users' });
  dom.document.dispatch('click', clickEvent(modified, { ctrlKey: true }));

  const external = el('a', { href: 'https://elsewhere.test/x' });
  dom.document.dispatch('click', clickEvent(external));

  const download = el('a', { href: '/report.pdf', download: '' });
  dom.document.dispatch('click', clickEvent(download));

  const hashOnly = el('a', { href: '#section' });
  dom.document.dispatch('click', clickEvent(hashOnly));

  const blank = el('a', { href: '/docs', target: '_blank' });
  dom.document.dispatch('click', clickEvent(blank));

  clock.advance(1000);
  assert.equal(dom.root.classList.contains('yd-progress--active'), false);
});

test('install skips a click another handler cancels | 其它处理器取消的点击不启动', () => {
  const { clock, dom } = harness();
  const link = el('a', { href: '/users' });
  const event = clickEvent(link);

  dom.document.dispatch('click', event);
  event.preventDefault();
  clock.advance(1000);
  assert.equal(dom.root.classList.contains('yd-progress--active'), false);
});

test('install follows plain forms, skips htmx and unconfirmed ones | 普通表单启动，htmx 与未确认表单跳过', () => {
  const { clock, dom } = harness();

  const plain = el('form', { method: 'post', action: '/save' });
  dom.document.dispatch('submit', submitEvent(plain));
  clock.advance(0);
  clock.advance(120);
  assert.equal(dom.root.classList.contains('yd-progress--active'), true);

  // Finish the first run before probing the skipped ones. | 先结束第一轮，再检查被跳过的表单。
  dom.document.dispatch('htmx:finally:request', {});
  clock.advance(440);

  const htmxForm = el('form', { 'hx-post': '/save' });
  dom.document.dispatch('submit', submitEvent(htmxForm));

  const pendingConfirm = el('form', { action: '/delete', 'data-yd-confirm': 'sure?' });
  dom.document.dispatch('submit', submitEvent(pendingConfirm));

  clock.advance(1000);
  assert.equal(dom.root.classList.contains('yd-progress--active'), false);

  const confirmed = el('form', { action: '/delete', 'data-yd-confirm': 'sure?', 'data-yd-confirmed': '1' });
  dom.document.dispatch('submit', submitEvent(confirmed));
  clock.advance(0);
  clock.advance(120);
  assert.equal(dom.root.classList.contains('yd-progress--active'), true);
});

test('install ignores dialog forms | 忽略 method=dialog 的表单', () => {
  const { clock, dom } = harness();
  const dialogForm = el('form', { method: 'dialog' });

  dom.document.dispatch('submit', submitEvent(dialogForm));
  clock.advance(1000);
  assert.equal(dom.root.classList.contains('yd-progress--active'), false);
});

test('install resets a restored page | 恢复的页面会复位进度条', () => {
  const { clock, dom, win } = harness();

  dom.document.dispatch('htmx:before:request', {});
  clock.advance(120);
  assert.equal(dom.root.classList.contains('yd-progress--active'), true);

  win.dispatch('pageshow', { persisted: true });
  assert.equal(dom.root.classList.contains('yd-progress--active'), false);
  assert.equal(dom.bar.style.transform, 'scaleX(0)');
  assert.equal(clock.pending(), 0);
});

/*
 * yd-authz.test.mjs — node:test suite for yd-authz.js.
 * yd-authz.test.mjs — yd-authz.js 的 node:test 测试集。
 *
 * Run with | 运行方式:
 *   node web/static/js/yd-authz.test.mjs
 *
 * The module is loaded through createRequire, so the very same UMD file the browser gets is the one
 * under test; the browser globals it needs (document) are stubbed below. The stub is deliberately
 * strict about the difference between the two things a guard must never confuse: "we hid this node"
 * is undone, "the server hid it" is left alone.
 * 通过 createRequire 加载模块，保证被测代码与浏览器拿到的 UMD 文件完全一致；
 * 它需要的浏览器全局对象（document）在下方以桩实现提供。该桩刻意严格区分守卫绝不能混淆的两件事：
 * 「我们隐藏了这个节点」要撤销，「服务端隐藏了它」则保持不动。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);

/* ------------------------------------------------------------------ *
 * Minimal DOM stub | 最小 DOM 桩
 * ------------------------------------------------------------------ */

const GUARD_ATTRIBUTES = ['data-yd-perm', 'data-yd-role'];

// matchesSelector understands the small subset of selectors the module actually uses:
// an [attribute] test, comma separated. | 仅支持模块真正使用的小型选择器子集：
// [属性] 测试，逗号分隔。
function matchesSelector(node, selector) {
  return String(selector)
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
    .some((part) => (part.startsWith('[') ? node.hasAttribute(part.slice(1, -1)) : false));
}

function collect(node, out) {
  for (const child of node.children || []) {
    if (child.nodeType === 1 && matchesSelector(child, '[data-yd-perm],[data-yd-role]')) {
      out.push(child);
    }
    collect(child, out);
  }
  return out;
}

// el builds one element. `style` and `disabled` exist because the module writes both, and both must
// behave like the real thing: display is a string, disabled is a boolean on form controls only.
// el 构建一个元素。style 与 disabled 的存在是因为模块会写它们，而两者都必须与真实行为一致：
// display 是字符串，disabled 只存在于表单控件上且是布尔值。
function el(tag, attrs = {}, children = []) {
  const node = {
    nodeType: 1,
    tagName: tag.toUpperCase(),
    attributes: { ...attrs },
    style: { display: '' },
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
    querySelectorAll() {
      return collect(this, []);
    }
  };
  if (['button', 'input', 'select', 'textarea'].includes(tag)) {
    node.disabled = false;
  }
  for (const child of children) {
    node.appendChild(child);
  }
  return node;
}

function findById(node, id) {
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
    readyState: 'complete',
    documentElement,
    body,
    listeners: {},
    getElementById(id) {
      return findById(this.body, id);
    },
    querySelectorAll() {
      return collect(this.body, []);
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

// The module is required after the document stub exists: its automatic start reads the document at
// load time. | 模块在文档桩就位后才加载：它的自动启动在加载时就会读取文档。
const ydAuthz = require('./yd-authz.js');

/* ------------------------------------------------------------------ *
 * Payload fixtures | 载荷夹具
 * ------------------------------------------------------------------ */

// ENVELOPE mirrors what the server injects: two name maps plus the two sets the subject holds.
// ENVELOPE 与服务端注入的内容一致：两张名字表，加上主体实际持有的两个集合。
const ENVELOPE = {
  perms: {
    USER_CREATE: 'user:create',
    USER_DELETE: 'user:delete',
    ROLE_MANAGE: 'role:manage'
  },
  roles: {
    ADMIN: 'admin',
    MEMBER: 'member'
  },
  granted: ['user:create'],
  mine: ['admin']
};

// clone returns a deep copy of the envelope, so one test cannot leak into the next.
// clone 返回信封的深拷贝，使一个测试不会渗进下一个。
function clone(payload = ENVELOPE) {
  return JSON.parse(JSON.stringify(payload));
}

// inject replaces the payload element of the document. | inject 替换文档中的载荷元素。
function inject(payload) {
  const existing = findById(doc.body, 'yd-authz');
  if (existing) {
    existing.remove();
  }
  if (payload === null) {
    return;
  }
  const script = el('script', { id: 'yd-authz', type: 'application/json' });
  script.textContent = typeof payload === 'string' ? payload : JSON.stringify(payload);
  doc.body.appendChild(script);
}

// reset clears the tree, injects the given payload and re-initialises the module. Every test starts
// from here, so a stale grant can never make a later assertion pass by accident.
// reset 清空树、注入给定载荷并重新初始化模块。每个测试都从这里开始，
// 因此过期的授权绝不可能让之后的断言意外通过。
function reset(payload = ENVELOPE) {
  doc.body.children = [];
  inject(payload);
  ydAuthz.init();
}

/* ------------------------------------------------------------------ *
 * Names and decisions | 名字与判定
 * ------------------------------------------------------------------ */

test('the injected payload becomes yd.PERM and yd.ROLE', () => {
  reset();
  assert.equal(ydAuthz.PERM.USER_CREATE, 'user:create');
  assert.equal(ydAuthz.PERM.ROLE_MANAGE, 'role:manage');
  assert.equal(ydAuthz.ROLE.ADMIN, 'admin');
  assert.equal(ydAuthz.ROLE.MEMBER, 'member');
  // The documented spelling of a decision reads a map the server built. | 文档写定的判定写法读取的是服务端构建的表。
  assert.equal(ydAuthz.can(ydAuthz.PERM.USER_CREATE), true);
  assert.equal(ydAuthz.hasRole(ydAuthz.ROLE.ADMIN), true);
});

test('can accepts all four spellings and refuses everything unheld', () => {
  reset();
  for (const ref of ['user:create', 'USER_CREATE', 'UserCreate', 'PermUserCreate']) {
    assert.equal(ydAuthz.can(ref), true, `${ref} must resolve to user:create`);
    assert.equal(ydAuthz.can(` ${ref} `), true, `${ref} must survive surrounding whitespace`);
  }
  // Registered but not held, unregistered (a typo) and absent answers all refuse. | 已注册但未持有、未注册（拼写错误）与空引用一律拒绝。
  assert.equal(ydAuthz.can('user:delete'), false);
  assert.equal(ydAuthz.can('user:creat'), false);
  assert.equal(ydAuthz.can(''), false);
  assert.equal(ydAuthz.can(null), false);
  assert.equal(ydAuthz.can(undefined), false);
});

test('hasRole accepts all four spellings of a role', () => {
  reset();
  for (const ref of ['admin', 'ADMIN', 'Admin', 'RoleAdmin']) {
    assert.equal(ydAuthz.hasRole(ref), true, `${ref} must resolve to admin`);
  }
  assert.equal(ydAuthz.hasRole('member'), false);
  assert.equal(ydAuthz.hasRole('RoleOwner'), false);
  assert.equal(ydAuthz.hasRole(''), false);
});

test('perms and roles return sorted copies', () => {
  reset({ ...clone(), granted: ['user:delete', 'role:manage', 'user:create'], mine: ['member', 'admin'] });
  assert.deepEqual(ydAuthz.perms(), ['role:manage', 'user:create', 'user:delete']);
  assert.deepEqual(ydAuthz.roles(), ['admin', 'member']);

  // A caller that sorts or empties the returned array must not touch the module's own state.
  // 调用方排序或清空返回的数组，绝不能影响模块自己的状态。
  const held = ydAuthz.perms();
  held.length = 0;
  assert.deepEqual(ydAuthz.perms(), ['role:manage', 'user:create', 'user:delete']);
});

test('subject reports the payload and whether a payload was read at all', () => {
  reset();
  const snapshot = ydAuthz.subject();
  assert.equal(snapshot.ready, true);
  assert.deepEqual(snapshot.granted, ['user:create']);
  assert.deepEqual(snapshot.mine, ['admin']);
  assert.equal(snapshot.perms.USER_CREATE, 'user:create');
});

/* ------------------------------------------------------------------ *
 * Fail closed | 失败关闭
 * ------------------------------------------------------------------ */

test('a missing payload grants nothing', () => {
  reset(null);
  assert.equal(ydAuthz.subject().ready, false);
  assert.deepEqual(ydAuthz.perms(), []);
  assert.deepEqual(ydAuthz.roles(), []);
  assert.equal(ydAuthz.can('user:create'), false);
  assert.equal(ydAuthz.hasRole('admin'), false);
});

test('a malformed payload grants nothing', () => {
  reset('{not json');
  assert.equal(ydAuthz.can('user:create'), false);
  assert.deepEqual(ydAuthz.perms(), []);

  // An empty body is the same as none. | 空正文与没有正文等价。
  reset('');
  assert.equal(ydAuthz.can('user:create'), false);

  // Well formed JSON of the wrong shape is refused as well: a name map alone never grants anything.
  // 形状不对的合法 JSON 同样拒绝：只有一张名字表绝不授予任何东西。
  reset({ perms: ENVELOPE.perms, roles: ENVELOPE.roles });
  assert.equal(ydAuthz.can('user:create'), false);
  assert.equal(ydAuthz.PERM.USER_CREATE, 'user:create');
});

test('a re-read replaces the grants instead of piling them up', () => {
  reset();
  assert.equal(ydAuthz.can('user:create'), true);

  const next = clone();
  next.granted = ['user:delete'];
  next.mine = [];
  ydAuthz.init({ payload: next });

  assert.equal(ydAuthz.can('user:create'), false, 'the old grant must be gone | 旧授权必须消失');
  assert.equal(ydAuthz.can('user:delete'), true);
  assert.deepEqual(ydAuthz.roles(), []);
});

/* ------------------------------------------------------------------ *
 * Declarative guards | 声明式守卫
 * ------------------------------------------------------------------ */

test('guards hide an unheld control and leave a held one alone', () => {
  reset();
  const denied = el('button', { 'data-yd-perm': 'user:delete' });
  const allowed = el('button', { 'data-yd-perm': 'user:create' });
  const plain = el('button', {});
  doc.body.appendChild(denied);
  doc.body.appendChild(allowed);
  doc.body.appendChild(plain);

  ydAuthz.init();

  assert.equal(denied.style.display, 'none');
  assert.equal(denied.getAttribute('data-yd-authz-state'), 'hidden');
  assert.equal(allowed.style.display, '');
  assert.equal(allowed.getAttribute('data-yd-authz-state'), null);
  assert.equal(plain.style.display, '');
});

test('data-yd-perm-mode="disable" greys a control out instead of removing it', () => {
  reset();
  const denied = el('button', { 'data-yd-perm': 'user:delete', 'data-yd-perm-mode': 'disable' });
  doc.body.appendChild(denied);

  ydAuthz.init();

  assert.equal(denied.getAttribute('data-yd-authz-state'), 'disabled');
  assert.equal(denied.getAttribute('disabled'), 'disabled');
  assert.equal(denied.getAttribute('aria-disabled'), 'true');
  assert.equal(denied.disabled, true);
  assert.equal(denied.style.display, '', 'a disabled control keeps its place | 变灰的控件保留自己的位置');
});

test('a role guard works like a capability guard', () => {
  reset();
  const denied = el('tr', { 'data-yd-role': 'RoleMember' });
  const allowed = el('tr', { 'data-yd-role': 'RoleAdmin' });
  const disabled = el('button', { 'data-yd-role': 'RoleMember', 'data-yd-role-mode': 'disable' });
  doc.body.appendChild(denied);
  doc.body.appendChild(allowed);
  doc.body.appendChild(disabled);

  ydAuthz.init();

  assert.equal(denied.style.display, 'none');
  assert.equal(allowed.style.display, '');
  assert.equal(disabled.disabled, true);
});

test('an empty reference is refused, never treated as a grant', () => {
  reset();
  const empty = el('button', { 'data-yd-perm': '' });
  const spaced = el('button', { 'data-yd-perm': '   ' });
  doc.body.appendChild(empty);
  doc.body.appendChild(spaced);

  ydAuthz.init();

  assert.equal(empty.style.display, 'none');
  assert.equal(spaced.style.display, 'none');
});

test('htmx:after:swap binds the fragment that was just inserted', () => {
  reset();
  assert.equal(ydAuthz.can('user:create'), true);

  const fragment = el('div', {}, [
    el('button', { 'data-yd-perm': 'user:create' }),
    el('button', { 'data-yd-perm': 'role:manage' })
  ]);
  doc.body.appendChild(fragment);

  doc.dispatch('htmx:after:swap', { ctx: { target: fragment } });

  assert.equal(fragment.children[0].style.display, '', 'a new held control stays visible | 新增的已授权控件保持可见');
  assert.equal(fragment.children[1].style.display, 'none', 'a new unheld control is hidden | 新增的未授权控件被隐藏');

  // A malformed event must not throw. | 结构不完整的事件不应抛错。
  doc.dispatch('htmx:after:swap', {});
});

test('a swap that grants the capability restores a control this script hid', () => {
  reset();
  const button = el('button', { 'data-yd-perm': 'user:delete' });
  button.style.display = 'inline-flex';
  doc.body.appendChild(button);
  ydAuthz.init();
  assert.equal(button.style.display, 'none');

  // The server answers with a subject that now holds the capability, and the very same node comes
  // back through a morph swap: our own hiding is undone, and the original display returns.
  // 服务端返回的主体已持有该能力，而这同一个节点经 morph 交换回来：
  // 我们自己造成的隐藏被撤销，原始 display 复原。
  const granted = clone();
  granted.granted.push('user:delete');
  ydAuthz.init({ payload: granted });
  doc.dispatch('htmx:after:swap', { ctx: { target: doc.body } });

  assert.equal(button.style.display, 'inline-flex');
  assert.equal(button.getAttribute('data-yd-authz-state'), null);
  assert.equal(button.getAttribute('data-yd-authz-prev'), null);
});

test('a control the server disabled is never re-enabled by this script', () => {
  reset({ ...clone(), granted: ['user:create', 'user:delete'] });
  // The server rendered the button disabled for a reason of its own; the guard holds the capability
  // and must therefore leave the node exactly as the server wrote it.
  // 服务端出于自己的原因把这个按钮渲染为禁用；守卫持有该能力，因此必须让节点保持服务端写下的样子。
  const button = el('button', { 'data-yd-perm': 'user:delete', disabled: 'disabled' });
  doc.body.appendChild(button);

  ydAuthz.init();

  assert.equal(button.getAttribute('disabled'), 'disabled');
  assert.equal(button.style.display, '');
  assert.equal(button.getAttribute('data-yd-authz-state'), null);
});

test('binding twice is idempotent and keeps the original display', () => {
  reset();
  const button = el('button', { 'data-yd-perm': 'user:delete' });
  button.style.display = 'flex';
  doc.body.appendChild(button);

  ydAuthz.init();
  ydAuthz.bind(doc);
  ydAuthz.bind(doc.body);

  assert.equal(button.style.display, 'none');
  assert.equal(button.getAttribute('data-yd-authz-prev'), 'flex', 'the first display read is the one kept | 保留的是第一次读到的 display');

  // And once granted, the very same node is restored to it. | 一旦授予，同一个节点就复原为它。
  const granted = clone();
  granted.granted.push('user:delete');
  ydAuthz.init({ payload: granted });
  assert.equal(button.style.display, 'flex');
});

test('bind only touches the subtree it is given', () => {
  reset();
  const inside = el('div', {}, [el('button', { 'data-yd-perm': 'user:delete' })]);
  const outside = el('button', { 'data-yd-perm': 'user:delete' });
  doc.body.appendChild(inside);
  doc.body.appendChild(outside);

  ydAuthz.bind(inside);

  assert.equal(inside.children[0].style.display, 'none');
  assert.equal(outside.style.display, '', 'a node outside the bound subtree is not decided yet | 绑定子树之外的节点此时不判定');
});

/* ------------------------------------------------------------------ *
 * Publishing | 发布
 * ------------------------------------------------------------------ */

test('the api is merged into window.yd without clobbering the rest of it', () => {
  // Another script may have created yd already; its own entries must survive. | 别的脚本可能已创建 yd：它自己的条目必须保留。
  globalThis.yd = { other: 1 };
  try {
    ydAuthz.init();
    assert.equal(globalThis.yd.can, ydAuthz.can);
    assert.equal(globalThis.yd.PERM.USER_CREATE, 'user:create');
    assert.equal(globalThis.yd.other, 1);

    // A later init refreshes the published names, so a swap cannot leave yd.PERM stale.
    // 之后的 init 会刷新已发布的名字，因此交换不会让 yd.PERM 过期。
    const next = clone();
    next.perms = { USER_DELETE: 'user:delete' };
    next.granted = ['user:delete'];
    ydAuthz.init({ payload: next });
    assert.equal(globalThis.yd.PERM.USER_DELETE, 'user:delete');
    assert.equal(globalThis.yd.PERM.USER_CREATE, undefined);
  } finally {
    delete globalThis.yd;
  }
});

test('the swap hook is registered against the document it was initialised with', () => {
  const other = createDocument();
  globalThis.document = other;
  try {
    other.body.appendChild(el('script', { id: 'yd-authz', type: 'application/json' }));
    other.body.children[0].textContent = JSON.stringify(ENVELOPE);
    ydAuthz.init();

    const fragment = el('div', {}, [el('button', { 'data-yd-perm': 'user:delete' })]);
    other.body.appendChild(fragment);
    other.dispatch('htmx:after:swap', { detail: { target: fragment } });

    assert.equal(fragment.children[0].style.display, 'none');
  } finally {
    globalThis.document = doc;
    ydAuthz.init();
  }
});

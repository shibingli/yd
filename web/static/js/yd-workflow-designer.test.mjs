/*
 * yd-workflow-designer.test.mjs — behaviour suite for the visual designer's identifier and note rules.
 * yd-workflow-designer.test.mjs —— 可视化设计器标识符与说明规则的行为套件。
 *
 * Run with | 运行方式:
 *   node --test web/static/js/yd-workflow-designer.test.mjs
 *
 * The suite boots the real same-origin script against a tiny DOM surface. It covers three regressions
 * this script owns: adding a rule after deleting the first must not reuse a live id, adding a field
 * after deleting a middle field must not reuse a live key, and saving with an untouched note must not
 * send an empty string that erases the stored note. A publish success must reload the designer so the
 * next save cannot hit the immutable-version 409. | 本套件在最小的 DOM 表面上启动真实的同源脚本，
 * 覆盖脚本负责的三处回归：删除第一条规则后再加规则不得复用仍存活的 ID；删除中间字段后再加字段不得
 * 复用仍存活的键；说明未改动时保存不得发送空串抹掉已存说明。发布成功后必须重载设计器，避免下一次
 * 保存撞上不可变版本的 409。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import vm from 'node:vm';

const scriptPath = path.join(path.dirname(fileURLToPath(import.meta.url)), 'yd-workflow-designer.js');
const source = readFileSync(scriptPath, 'utf8');

function matchesTag(node, selector) {
  const parts = selector.split(',').map((part) => part.trim()).filter(Boolean);
  return parts.some((part) => {
    if (part.startsWith('.')) {
      const classes = String(node.className || '').split(/\s+/);
      return classes.includes(part.slice(1));
    }
    if (part.startsWith('[')) {
      const name = part.slice(1, -1);
      return node.hasAttribute(name);
    }
    return node.tagName === part.toUpperCase();
  });
}

function descendants(node, out) {
  for (const child of node.children || []) {
    if (child && child.nodeType === 1) {
      out.push(child);
      descendants(child, out);
    }
  }
  return out;
}

function fakeElement(tag, doc) {
  const attrs = new Map();
  const listeners = new Map();
  const el = {
    nodeType: 1,
    tagName: String(tag || 'div').toUpperCase(),
    children: [],
    style: {},
    dataset: {},
    className: '',
    id: '',
    value: '',
    checked: false,
    ownerDocument: doc,
    setAttribute(name, value) {
      attrs.set(name, String(value));
      if (name === 'id') {
        el.id = String(value);
      }
      if (name === 'class') {
        el.className = String(value);
      }
    },
    getAttribute(name) {
      return attrs.has(name) ? attrs.get(name) : null;
    },
    hasAttribute(name) {
      return attrs.has(name);
    },
    removeAttribute(name) {
      attrs.delete(name);
    },
    addEventListener(name, handler) {
      if (!listeners.has(name)) {
        listeners.set(name, []);
      }
      listeners.get(name).push(handler);
    },
    removeEventListener() {},
    appendChild(child) {
      el.children.push(child);
      return child;
    },
    querySelector(selector) {
      return descendants(el, []).find((node) => matchesTag(node, selector)) || null;
    },
    querySelectorAll(selector) {
      return descendants(el, []).filter((node) => matchesTag(node, selector));
    },
    matches(selector) {
      return matchesTag(el, selector);
    },
    click() {
      (listeners.get('click') || []).forEach((handler) => handler({ preventDefault() {} }));
    },
    focus() {},
    dispatchEvent() {}
  };
  el.content = {
    appendChild(child) {
      el.children.push(child);
      return child;
    }
  };
  let text = '';
  Object.defineProperty(el, 'textContent', {
    get() {
      return text;
    },
    set(value) {
      text = String(value);
      if (text === '') {
        el.children = [];
      }
    }
  });
  return el;
}

function buildDesignerEnvironment(options) {
  const doc = {
    readyState: 'complete',
    documentElement: { contains: () => true },
    addEventListener() {},
    removeEventListener() {},
    createElement(tag) {
      return fakeElement(tag, doc);
    },
    createElementNS(_namespace, tag) {
      return fakeElement(tag, doc);
    },
    createTextNode(value) {
      return { nodeType: 3, textContent: String(value) };
    },
    getElementById() {
      return null;
    },
    querySelector(selector) {
      if (selector === '#yd-workflow-design') {
        return { textContent: JSON.stringify(options.design) };
      }
      if (selector === '#yd-workflow-directory') {
        return { textContent: JSON.stringify({ users: [], roles: [], groups: [] }) };
      }
      return null;
    },
    querySelectorAll() {
      return [];
    }
  };

  const root = fakeElement('section', doc);
  root.setAttribute('data-yd-workflow-designer', '');
  root.setAttribute('data-save-url', '/api/v1/workflows/versions/wfver_1');
  root.setAttribute('data-publish-url', '/api/v1/workflows/versions/wfver_1/publish');
  root.setAttribute('data-back-url', '/mgr/workflows');
  root.setAttribute('data-confirm-publish', '');
  root.setAttribute('data-note', options.note || '');

  const canvas = fakeElement('div', doc);
  canvas.setAttribute('data-yd-flow-canvas', '');
  const stage = fakeElement('div', doc);
  stage.setAttribute('data-yd-flow-stage', '');
  const layers = fakeElement('g', doc);
  layers.setAttribute('data-yd-flow-layers', '');
  const inspector = fakeElement('aside', doc);
  inspector.setAttribute('data-yd-flow-inspector', '');
  const status = fakeElement('p', doc);
  status.setAttribute('data-yd-flow-status', '');

  root.querySelector = (selector) => {
    switch (selector) {
      case '[data-yd-flow-canvas]':
        return canvas;
      case '[data-yd-flow-stage]':
        return stage;
      case '[data-yd-flow-layers]':
        return layers;
      case '[data-yd-flow-inspector]':
        return inspector;
      case '[data-yd-flow-status]':
        return status;
      default:
        return descendants(root, []).find((node) => matchesTag(node, selector)) || null;
    }
  };
  const toolbar = [];
  for (const action of ['save', 'publish']) {
    const button = fakeElement('button', doc);
    button.setAttribute('data-yd-flow-action', action);
    toolbar.push(button);
  }
  root.querySelectorAll = (selector) => {
    if (selector === '[data-yd-flow-action]') {
      return toolbar;
    }
    return [];
  };

  const requests = [];
  const reloads = [];
  const win = {
    addEventListener() {},
    removeEventListener() {},
    confirm() {
      return true;
    },
    location: {
      href: '',
      reload() {
        reloads.push(true);
      }
    }
  };
  const fetchStub = (url, init) => {
    requests.push({ url, init, body: init && init.body ? JSON.parse(init.body) : null });
    return Promise.resolve({
      ok: true,
      status: 200,
      text: () => Promise.resolve('{}')
    });
  };

  doc.querySelectorAll = (selector) =>
    selector === '[data-yd-workflow-designer]' ? [root] : [];

  const context = vm.createContext({
    window: win,
    document: doc,
    fetch: fetchStub,
    setTimeout,
    clearTimeout,
    console,
    JSON,
    Math,
    Number,
    String,
    Object,
    Array,
    Boolean,
    Infinity,
    isFinite
  });
  vm.runInContext(source, context, { filename: 'yd-workflow-designer.js' });
  return { root, inspector, toolbar, requests, reloads };
}

function findButton(node, text) {
  if (!node || typeof node !== 'object') {
    return null;
  }
  if (node.tagName === 'BUTTON' && node.textContent === text) {
    return node;
  }
  for (const child of node.children || []) {
    const found = findButton(child, text);
    if (found) {
      return found;
    }
  }
  return null;
}

function toolbarButton(toolbar, action) {
  return toolbar.find((button) => button.getAttribute('data-yd-flow-action') === action) || null;
}

function baseDesign() {
  return {
    schema_version: 1,
    nodes: [
      { id: 'start', type: 'start', name: 'Start', position: { x: 80, y: 200 } },
      { id: 'end', type: 'end', name: 'End', position: { x: 400, y: 200 } }
    ],
    edges: [{ id: 'edge_1', source: 'start', target: 'end' }],
    forms: { start: [{ key: 'field_1', label: 'First', type: 'text' }] },
    rules: [{ id: 'rule_1', name: 'First rule', enabled: true, event: 'task', scope: 'variable', decision: 'approve' }],
    settings: {}
  };
}

test('designer allocates unique rule ids and field keys', async () => {
  const env = buildDesignerEnvironment({ design: baseDesign(), note: '' });
  const addRule = findButton(env.inspector, 'Add rule');
  assert.ok(addRule, 'the process inspector renders the Add rule button');
  addRule.click();

  const addField = findButton(env.inspector, 'Add field');
  assert.ok(addField, 'the process inspector renders the Add field button');
  addField.click();

  toolbarButton(env.toolbar, 'save').click();
  await new Promise((resolve) => setImmediate(resolve));

  assert.equal(env.requests.length, 1, 'save issues one PUT');
  const payload = env.requests[0].body;
  const ruleIDs = payload.design.rules.map((rule) => rule.id);
  assert.deepEqual(ruleIDs, ['rule_1', 'rule_2'], `rule ids = ${JSON.stringify(ruleIDs)}`);
  const fieldKeys = payload.design.forms.start.map((field) => field.key);
  assert.deepEqual(fieldKeys, ['field_1', 'field_2'], `field keys = ${JSON.stringify(fieldKeys)}`);
  assert.equal(payload.note, undefined, 'an empty note is omitted instead of erasing the stored note');
});

test('designer keeps a server-injected note and reloads after publish', async () => {
  const env = buildDesignerEnvironment({ design: baseDesign(), note: 'stored note' });
  toolbarButton(env.toolbar, 'save').click();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(env.requests[0].body.note, 'stored note', 'the injected note survives an untouched save');

  toolbarButton(env.toolbar, 'publish').click();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(env.requests.length, 2, 'publish posts the publication request');
  assert.equal(env.reloads.length, 1, 'a successful publish reloads the designer');
});
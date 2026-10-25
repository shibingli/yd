/*
 * yd-ui.test.mjs — node:test suite for the yd UI kit kernels and component registry.
 * yd-ui.test.mjs — yd UI 组件层内核与组件注册表的 node:test 测试集。
 *
 * Run with | 运行方式:
 *   node web/static/js/yd-ui.test.mjs
 *
 * The suite loads the very UMD file the browser receives. No DOM is required: the validators,
 * the RMB capitalisation, the date presets and the registry are plain functions, so the rules
 * that are easiest to get quietly wrong are asserted without a browser.
 * 本套件加载的正是浏览器收到的 UMD 文件。不需要 DOM：校验器、人民币大写、日期预设与注册表都是
 * 纯函数，因此最容易悄悄写错的规则都在无浏览器环境下得到断言。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const api = require('./yd-ui.js');
const CN = api.CN;

test('phone accepts only the mainland 11-digit form', () => {
  assert.equal(CN.phone('13800138000'), true);
  assert.equal(CN.phone('138 0013 8000'), true);
  assert.equal(CN.phone('19912345678'), true);
  assert.equal(CN.phone('12800138000'), false, 'second digit must be 3-9');
  assert.equal(CN.phone('1380013800'), false, 'ten digits is not a mobile number');
  assert.equal(CN.phone('138001380000'), false, 'twelve digits is not a mobile number');
  assert.equal(CN.phone(''), false);
});

test('phoneFormatted groups 3-4-4 and stays partial while typing', () => {
  assert.equal(CN.phoneFormatted('13800138000'), '138 0013 8000');
  assert.equal(CN.phoneFormatted('138'), '138');
  assert.equal(CN.phoneFormatted('1380'), '138 0');
  assert.equal(CN.phoneFormatted('1380013'), '138 0013');
  assert.equal(CN.phoneFormatted(''), '');
});

test('idCard checks the GB 11643 checksum and derives birth, sex and age', () => {
  const id = '11010519491231002X';
  assert.equal(CN.idCard(id), true);
  assert.equal(CN.idCard(id.toLowerCase()), true, 'the X is case-insensitive');
  assert.equal(CN.idCard('110105194912310021'), false, 'wrong check digit');
  assert.equal(CN.idCard('11010519491231002'), false, '17 digits is the old form, not accepted');
  assert.equal(CN.idCard('11010519491331002X'), false, 'month 13 is not a date');

  const info = CN.idCardInfo(id);
  assert.equal(info.birth, '1949-12-31');
  assert.equal(info.sex, 'female', 'the 17th digit is even');
  assert.ok(info.age >= 70, 'a 1949 birth date must be an adult');
});

test('luhn and bankCard validate a number and identify a known issuer', () => {
  assert.equal(CN.bankCard('6222021234567894'), true);
  assert.equal(CN.bankCard('6222021234567895'), false);
  assert.equal(CN.bankCard('123'), false, 'too short');
  assert.equal(CN.bankName('6222021234567894'), '中国工商银行');
  assert.equal(CN.bankName('6228481234567894'), '中国农业银行');
  assert.equal(CN.bankName('4111111111111111'), '', 'an unknown BIN names no issuer');
  assert.equal(CN.bankCardFormatted('6222021234567894'), '6222 0212 3456 7894');
});

test('creditCode checks the GB 32100 checksum', () => {
  assert.equal(CN.creditCode('9144030071526726XG'), true);
  assert.equal(CN.creditCode('9144030071526726Xg'), true, 'lower case is normalised');
  assert.equal(CN.creditCode('9144030071526726XH'), false, 'wrong check character');
  assert.equal(CN.creditCode('9144030071526726XI'), false, 'I is not in the charset');
  assert.equal(CN.creditCode('9144030071526726X'), false, '17 characters is not a credit code');
});

test('sanitizeAmount keeps one dot and at most two decimals', () => {
  assert.equal(CN.sanitizeAmount('1,234.567'), '1234.56');
  assert.equal(CN.sanitizeAmount('0012'), '12');
  assert.equal(CN.sanitizeAmount('12.'), '12.');
  assert.equal(CN.sanitizeAmount('12..3'), '12.3');
  assert.equal(CN.sanitizeAmount('abcdef'), '');
  assert.equal(CN.sanitizeAmount('-5'), '-5', 'a leading minus is preserved, not flipped to positive');
  assert.equal(CN.sanitizeAmount('--5'), '-5');
});

test('amountToChinese renders the capitalised RMB form', () => {
  const cases = [
    ['0', '零元整'],
    ['0.05', '零元零伍分'],
    ['1', '壹元整'],
    ['10', '壹拾元整'],
    ['1001', '壹仟零壹元整'],
    ['10000', '壹万元整'],
    ['10001', '壹万零壹元整'],
    ['100000', '壹拾万元整'],
    ['100000000', '壹亿元整'],
    ['100100010', '壹亿零壹拾万零壹拾元整'],
    ['123456789.01', '壹亿贰仟叁佰肆拾伍万陆仟柒佰捌拾玖元零壹分'],
    ['10000.5', '壹万元伍角'],
    ['12.34', '壹拾贰元叁角肆分']
  ];
  for (const [input, want] of cases) {
    assert.equal(CN.amountToChinese(input), want, `amount ${input}`);
  }
  assert.equal(CN.amountToChinese(''), '');
  assert.equal(CN.amountToChinese('-1'), '', 'negative amounts are rejected');
  assert.equal(CN.amountToChinese('1.234'), '', 'more than two decimals is rejected');
});

test('province fallback has the 34 first-level divisions', () => {
  assert.equal(CN.provinces.length, 34);
  assert.ok(CN.provinces.some((p) => p.code === '110000' && p.name === '北京市'));
  assert.ok(CN.provinces.some((p) => p.code === '820000' && p.name === '澳门特别行政区'));
});

test('decorateEditorToolbar swaps legacy icon classes for cloned Lucide SVGs', () => {
  const icon = { cloneNode: () => ({ kind: 'lucide-bold' }) };
  const holder = { querySelector: () => icon };
  const palette = { querySelector: (selector) => (selector.includes('lucide-bold') ? holder : null) };
  const root = { querySelector: () => palette };
  const button = {
    className: 'fa fa-bold',
    innerHTML: '<i class="fa"></i>',
    children: [],
    attributes: {},
    title: 'Bold',
    setAttribute(name, value) { this.attributes[name] = value; },
    getAttribute(name) { return name === 'title' ? this.title : this.attributes[name]; },
    appendChild(node) { this.children.push(node); }
  };
  api.decorateEditorToolbar({ toolbarElements: { bold: button } }, root);
  assert.equal(button.className, 'yd-editor-tool');
  assert.equal(button.innerHTML, '', 'the legacy glyph is cleared');
  assert.deepEqual(button.children, [{ kind: 'lucide-bold' }]);
  assert.equal(button.attributes['aria-label'], 'Bold');
});

test('decorateEditorToolbar keeps no-disable and semantic classes', () => {
  const icon = { cloneNode: () => ({ kind: 'lucide-eye' }) };
  const palette = { querySelector: (selector) => (selector.includes('lucide-eye') ? { querySelector: () => icon } : null) };
  const root = { querySelector: () => palette };
  const button = {
    className: 'fa fa-eye no-disable',
    innerHTML: '',
    children: [],
    attributes: {},
    setAttribute(name, value) { this.attributes[name] = value; },
    getAttribute() { return ''; },
    appendChild(node) { this.children.push(node); }
  };
  api.decorateEditorToolbar({ toolbarElements: { preview: button } }, root);
  assert.equal(button.className, 'yd-editor-tool no-disable');
  assert.deepEqual(button.children, [{ kind: 'lucide-eye' }]);
});

test('register installs every component once and is idempotent', () => {
  const names = [];
  const fake = {
    data(name) {
      names.push(name);
    }
  };
  api.register(fake);
  api.register(fake);
  assert.deepEqual(names.sort(), api.components.slice().sort());
  for (const name of ['ydPhone', 'ydRegion', 'ydAmount', 'ydSelect', 'ydDateTime', 'ydRows', 'ydEmoji', 'ydSignature', 'ydResizable']) {
    assert.ok(api.components.includes(name), `missing ${name}`);
  }
  assert.equal(api.components.length, names.length, 'a second register call adds nothing');
});

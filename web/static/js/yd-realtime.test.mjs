/*
 * yd-realtime.test.mjs — node:test suite for yd-realtime.js.
 * yd-realtime.test.mjs — yd-realtime.js 的 node:test 测试集。
 *
 * Run with | 运行方式:
 *   node web/static/js/yd-realtime.test.mjs
 *
 * The suite pins the wire format shared with the Go side (uint32 big-endian
 * length, zero-length heartbeats) and the transport ladder's decision table.
 * The suite loads the very UMD file the browser receives.
 * 本套件固定与 Go 侧共享的线格式（大端 uint32 长度、零长度心跳）以及传输阶梯的判定表。
 * 套件加载的正是浏览器收到的那个 UMD 文件。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const realtime = require('./yd-realtime.js');

test('encodeFrame matches the Go wire format | 帧格式与 Go 一致', () => {
  const frame = realtime.encodeFrame('hi');
  assert.equal(frame.length, 6);
  assert.deepEqual(Array.from(frame.slice(0, 4)), [0, 0, 0, 2]);
  assert.equal(new TextDecoder().decode(frame.slice(4)), 'hi');
});

test('FrameDecoder parses split chunks | 解析被切分的块', () => {
  const decoder = new realtime.FrameDecoder();
  const frame = realtime.encodeFrame('hello');
  assert.deepEqual(decoder.push(frame.slice(0, 2)), []);
  assert.deepEqual(decoder.push(frame.slice(2, 5)), []);
  assert.deepEqual(decoder.push(frame.slice(5)), ['hello']);
});

test('FrameDecoder returns several frames at once | 一次返回多帧', () => {
  const decoder = new realtime.FrameDecoder();
  const a = realtime.encodeFrame('a');
  const b = realtime.encodeFrame('b');
  const merged = new Uint8Array(a.length + b.length);
  merged.set(a, 0);
  merged.set(b, a.length);
  assert.deepEqual(decoder.push(merged), ['a', 'b']);
});

test('FrameDecoder skips heartbeats | 跳过心跳帧', () => {
  const decoder = new realtime.FrameDecoder();
  const heartbeat = new Uint8Array([0, 0, 0, 0]);
  const frame = realtime.encodeFrame('after');
  const merged = new Uint8Array(4 + frame.length);
  merged.set(heartbeat, 0);
  merged.set(frame, 4);
  assert.deepEqual(decoder.push(merged), ['after']);
  assert.deepEqual(decoder.push(heartbeat), []);
});

test('FrameDecoder keeps UTF-8 intact | 保持 UTF-8 完整', () => {
  const decoder = new realtime.FrameDecoder();
  const text = '中文|emoji 😀';
  const frame = realtime.encodeFrame(text);
  const cut = 4 + 3; // split in the middle of a multi-byte rune | 在多字节字符中间切分
  assert.deepEqual(decoder.push(frame.slice(0, cut)), []);
  assert.deepEqual(decoder.push(frame.slice(cut)), [text]);
});

test('chooseTransport follows the ladder | 阶梯选择', () => {
  const choose = realtime.chooseTransport;
  assert.equal(choose({ webtransport: true, websocket: true, sse: true, secure: true }), 'webtransport');
  assert.equal(choose({ webtransport: true, websocket: true, sse: true, secure: false }), 'websocket');
  assert.equal(choose({ websocket: false, sse: true, secure: true }), 'sse');
  assert.equal(choose({ websocket: true, sse: false, secure: true }), 'websocket');
  assert.equal(choose({}), null);
  assert.equal(choose({ webtransport: true, websocket: true, secure: true, force: 'sse' }), 'sse');
  assert.equal(choose({ webtransport: false, websocket: false, sse: true, force: 'sse' }), 'sse');
});

test('Channel rejects transports it cannot use | 拒绝不可用传输', () => {
  const ch = new realtime.Channel({ webtransportURL: 'https://x/wt', websocketURL: 'wss://x/ws' });
  assert.equal(ch.available('sse', { sse: false }), false);
  assert.equal(ch.available('webtransport', { webtransport: true, secure: false }), false);
  assert.equal(ch.available('webtransport', { webtransport: true, secure: true }), true);
  assert.equal(ch.available('websocket', { websocket: true }), true);
});

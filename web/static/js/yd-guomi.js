/*!
 * yd-guomi.js — dependency-free SM2 / SM3 / SM4 sandbox that is byte-compatible with the Go package
 * github.com/yun-dev/yd/guomi. | 无依赖的 SM2/SM3/SM4 沙箱，与 Go 包 github.com/yun-dev/yd/guomi 字节级互操作。
 *
 * Design rules | 设计约束：
 *   - Pure ES2017+ JavaScript: no npm package, no CDN, no WebAssembly, no build step. | 纯 ES2017+ JavaScript：不依赖 npm 包、不引用 CDN、不使用 WebAssembly、无构建步骤。
 *   - UMD: usable as a classic <script> (global `ydGuomi`) or as a CommonJS module. | UMD：既可作为经典 <script> 使用（全局 `ydGuomi`），也可作为 CommonJS 模块使用。
 *   - Every primitive is implemented here from the GB/T specifications; only BigInt, typed arrays and
 *     crypto.getRandomValues are assumed to exist. | 所有原语均按 GB/T 标准在本文件内实现，仅假设存在 BigInt、类型化数组与 crypto.getRandomValues。
 *
 * Interoperability contract with the Go side | 与 Go 侧的互操作契约：
 *   - ydGuomi.sm3 / sm3Hex: GB/T 32905-2016, 32-byte digest, lowercase hex output. | ydGuomi.sm3 / sm3Hex：GB/T 32905-2016，32 字节摘要，输出小写十六进制。
 *   - ydGuomi.sm2 keys: privateKey = 64 hex chars (D), publicKey = 130 hex chars, uncompressed point
 *     04||X||Y, both lowercase, matching guomi.PrivateKey.Hex and guomi.PublicKeyToHex. | SM2 密钥：私钥为 64 个十六进制字符（D），公钥为 130 个字符的未压缩点 04||X||Y，均小写，对应 guomi.PrivateKey.Hex 与 guomi.PublicKeyToHex。
 *   - ydGuomi.sm2.encrypt produces the RAW layout of guomi.EncryptRaw, 96+len bytes:
 *     C1 = X1(32)||Y1(32) WITHOUT the 0x04 marker byte, C3 = SM3(x2||msg||y2) 32 bytes,
 *     C2 = msg XOR KDF(x2||y2) with the GB/T 32918.4 counter-based KDF. | sm2.encrypt 产出 guomi.EncryptRaw 的原始布局，共 96+len 字节：
 *     C1 = X1(32)||Y1(32)，**不带 0x04 标识字节**；C3 = SM3(x2||msg||y2) 共 32 字节；C2 = 明文异或 KDF(x2||y2)，KDF 为 GB/T 32918.4 计数器构造。
 *     Pass opts.c1Prefix = true to emit the gmsm-style 0x04-prefixed variant instead; decrypt() auto-detects it. | 传入 opts.c1Prefix = true 可改用 gmsm 风格的 0x04 前缀变体；decrypt() 会自动识别。
 *   - ydGuomi.sm2.sign / verify: GB/T 32918.2 with the default user ID "1234567812345678" and DER
 *     SEQUENCE { INTEGER r, INTEGER s }, identical to guomi.Sign / guomi.Verify. | sm2.sign / verify：GB/T 32918.2，默认用户标识 "1234567812345678"，DER 结构 SEQUENCE { INTEGER r, INTEGER s }，与 guomi.Sign / guomi.Verify 一致。
 *   - ydGuomi.sm4: GB/T 32907-2016 block cipher; encryptCBC/decryptCBC use PKCS#7 padding and
 *     encryptGCM/decryptGCM emit ciphertext||tag with a 16-byte tag, matching crypto/cipher GCM,
 *     including nonces that are not 12 bytes long (GHASH-derived counter). | SM4：GB/T 32907-2016 分组密码；encryptCBC/decryptCBC 使用 PKCS#7 填充，encryptGCM/decryptGCM 产出 密文||16 字节标签，与 crypto/cipher 的 GCM 一致，包含非 12 字节随机数（走 GHASH 派生计数器）的情形。
 *
 * Errors | 错误：
 *   Every failure throws an Error whose `code` is a stable identifier, so callers never match on
 *   message text: | 任何失败都抛出带稳定 `code` 的 Error，调用方无需匹配文案：
 *   invalid-hex, invalid-length, invalid-input, invalid-key, invalid-iv, invalid-nonce,
 *   invalid-ciphertext, invalid-signature, invalid-padding, decrypt-failed, gcm-auth-failed,
 *   random-unavailable, fetch-failed. | 上述错误码即为全部取值。
 */
(function (root, factory) {
  'use strict';
  var api = factory();
  // CommonJS: hand the API to the module system as well. | CommonJS：同时把 API 交给模块系统。
  if (typeof module === 'object' && module && module.exports) {
    module.exports = api;
  }
  // Browser: register the documented global. | 浏览器：注册文档化的全局变量。
  if (root) {
    root.ydGuomi = api;
  }
})(typeof globalThis !== 'undefined' ? globalThis : typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  // ---------------------------------------------------------------- errors | 错误

  // guomiError builds an Error with a stable code and a bilingual message. | guomiError 构造带稳定错误码与双语文案的 Error。
  function guomiError(code, en, zh) {
    var err = new Error('ydGuomi: ' + en + ' | ydGuomi：' + zh);
    err.name = 'YdGuomiError';
    err.code = code;
    return err;
  }

  // ---------------------------------------------------------------- utilities | 工具

  // rotl rotates a 32-bit word left, masking the shift count. | rotl 对 32 位字循环左移，并自动取移位计数模 32。
  function rotl(x, n) {
    n &= 31;
    return ((x << n) | (x >>> (32 - n))) >>> 0;
  }

  // utf8Encode converts a JavaScript string into UTF-8 bytes, handling surrogate pairs. | utf8Encode 把 JS 字符串转为 UTF-8 字节，正确处理代理对。
  function utf8Encode(text) {
    var out = [];
    for (var i = 0; i < text.length; i++) {
      var code = text.charCodeAt(i);
      if (code >= 0xd800 && code <= 0xdbff && i + 1 < text.length) {
        var low = text.charCodeAt(i + 1);
        if (low >= 0xdc00 && low <= 0xdfff) {
          code = 0x10000 + ((code - 0xd800) << 10) + (low - 0xdc00);
          i++;
        }
      }
      if (code < 0x80) {
        out.push(code);
      } else if (code < 0x800) {
        out.push(0xc0 | (code >> 6), 0x80 | (code & 0x3f));
      } else if (code < 0x10000) {
        out.push(0xe0 | (code >> 12), 0x80 | ((code >> 6) & 0x3f), 0x80 | (code & 0x3f));
      } else {
        out.push(0xf0 | (code >> 18), 0x80 | ((code >> 12) & 0x3f), 0x80 | ((code >> 6) & 0x3f), 0x80 | (code & 0x3f));
      }
    }
    return Uint8Array.from(out);
  }

  // utf8Decode converts UTF-8 bytes back into a string, replacing malformed sequences with U+FFFD. | utf8Decode 把 UTF-8 字节还原为字符串，非法序列以 U+FFFD 替换。
  function utf8Decode(bytes) {
    var data = toBytes(bytes);
    var out = '';
    var i = 0;
    while (i < data.length) {
      var byte = data[i++];
      var code;
      var need;
      if (byte < 0x80) {
        code = byte;
        need = 0;
      } else if ((byte & 0xe0) === 0xc0) {
        code = byte & 0x1f;
        need = 1;
      } else if ((byte & 0xf0) === 0xe0) {
        code = byte & 0x0f;
        need = 2;
      } else if ((byte & 0xf8) === 0xf0) {
        code = byte & 0x07;
        need = 3;
      } else {
        out += '�';
        continue;
      }
      if (i + need > data.length) {
        out += '�';
        break;
      }
      for (var j = 0; j < need; j++) {
        code = (code << 6) | (data[i++] & 0x3f);
      }
      if (code > 0xffff) {
        code -= 0x10000;
        out += String.fromCharCode(0xd800 + (code >> 10), 0xdc00 + (code & 0x3ff));
      } else {
        out += String.fromCharCode(code);
      }
    }
    return out;
  }

  // toBytes normalises Uint8Array, ArrayBuffer, byte arrays and strings into a fresh Uint8Array copy. | toBytes 把 Uint8Array、ArrayBuffer、字节数组与字符串统一为新的 Uint8Array 副本。
  function toBytes(data) {
    if (data instanceof Uint8Array) {
      return Uint8Array.from(data);
    }
    if (typeof data === 'string') {
      return utf8Encode(data);
    }
    if (typeof ArrayBuffer !== 'undefined' && data instanceof ArrayBuffer) {
      return new Uint8Array(data.slice(0));
    }
    if (Array.isArray(data)) {
      for (var i = 0; i < data.length; i++) {
        if (!Number.isInteger(data[i]) || data[i] < 0 || data[i] > 255) {
          throw guomiError('invalid-input', 'byte array holds a value outside 0..255', '字节数组中存在 0..255 之外的值');
        }
      }
      return Uint8Array.from(data);
    }
    throw guomiError('invalid-input', 'expected a Uint8Array, ArrayBuffer, byte array or string', '期望 Uint8Array、ArrayBuffer、字节数组或字符串');
  }

  // bytesToHex renders bytes as lowercase hex, matching Go's hex.EncodeToString. | bytesToHex 把字节渲染为小写十六进制，与 Go 的 hex.EncodeToString 一致。
  function bytesToHex(data) {
    var bytes = toBytes(data);
    var out = '';
    for (var i = 0; i < bytes.length; i++) {
      out += (bytes[i] < 16 ? '0' : '') + bytes[i].toString(16);
    }
    return out;
  }

  // hexToBytes parses hex, tolerating an 0x prefix, whitespace and mixed case. | hexToBytes 解析十六进制，容忍 0x 前缀、空白与大小写混用。
  function hexToBytes(text) {
    if (typeof text !== 'string') {
      throw guomiError('invalid-hex', 'hex input must be a string', '十六进制输入必须是字符串');
    }
    var clean = text.trim().replace(/^0[xX]/, '');
    if (clean.length % 2 !== 0) {
      throw guomiError('invalid-hex', 'hex input has an odd number of digits: ' + clean.length, '十六进制位数为奇数：' + clean.length);
    }
    if (!/^[0-9a-fA-F]*$/.test(clean)) {
      throw guomiError('invalid-hex', 'hex input holds a non-hexadecimal character', '十六进制输入包含非十六进制字符');
    }
    var out = new Uint8Array(clean.length / 2);
    for (var i = 0; i < out.length; i++) {
      out[i] = parseInt(clean.substr(i * 2, 2), 16);
    }
    return out;
  }

  // bytesToBigIntBE converts big-endian bytes into a non-negative BigInt. | bytesToBigIntBE 把大端字节转为非负 BigInt。
  function bytesToBigIntBE(data) {
    var bytes = toBytes(data);
    var out = 0n;
    for (var i = 0; i < bytes.length; i++) {
      out = (out << 8n) | BigInt(bytes[i]);
    }
    return out;
  }

  // bigIntToBytesBE renders a non-negative BigInt as fixed width big-endian bytes, rejecting overflow. | bigIntToBytesBE 把非负 BigInt 渲染为定长大端字节，溢出即拒绝。
  function bigIntToBytesBE(value, size) {
    if (!Number.isInteger(size) || size < 0) {
      throw guomiError('invalid-length', 'byte length must be a non-negative integer', '字节长度必须是非负整数');
    }
    if (typeof value !== 'bigint' || value < 0n) {
      throw guomiError('invalid-length', 'value must be a non-negative BigInt', '数值必须是非负 BigInt');
    }
    var out = new Uint8Array(size);
    var rest = value;
    for (var i = size - 1; i >= 0; i--) {
      out[i] = Number(rest & 0xffn);
      rest >>= 8n;
    }
    if (rest !== 0n) {
      throw guomiError('invalid-length', 'value does not fit in ' + size + ' bytes', '数值无法装入 ' + size + ' 字节');
    }
    return out;
  }

  // concatBytes joins several byte sources into one Uint8Array. | concatBytes 把多个字节来源拼接为一个 Uint8Array。
  function concatBytes() {
    var parts = [];
    var total = 0;
    for (var i = 0; i < arguments.length; i++) {
      var part = toBytes(arguments[i]);
      parts.push(part);
      total += part.length;
    }
    var out = new Uint8Array(total);
    var offset = 0;
    for (var j = 0; j < parts.length; j++) {
      out.set(parts[j], offset);
      offset += parts[j].length;
    }
    return out;
  }

  // bytesEqual compares two byte sequences without an early exit. | bytesEqual 比较两段字节，不做提前返回。
  function bytesEqual(left, right) {
    if (left.length !== right.length) {
      return false;
    }
    var diff = 0;
    for (var i = 0; i < left.length; i++) {
      diff |= left[i] ^ right[i];
    }
    return diff === 0;
  }

  // randomBytes returns cryptographically secure random bytes or throws. | randomBytes 返回密码学安全随机字节，不可用时抛错。
  function randomBytes(size) {
    var out = new Uint8Array(size);
    var webCrypto = typeof crypto !== 'undefined' && crypto && typeof crypto.getRandomValues === 'function' ? crypto : null;
    if (webCrypto) {
      webCrypto.getRandomValues(out);
      return out;
    }
    // Node.js before v19 exposes the same primitive only through node:crypto. | Node.js v19 之前只能通过 node:crypto 使用同一原语。
    if (typeof require === 'function') {
      try {
        var nodeCrypto = require('crypto');
        var buf = nodeCrypto.randomBytes(size);
        out.set(buf);
        return out;
      } catch (err) {
        // Fall through to the explicit failure below. | 落入下方的显式失败分支。
      }
    }
    throw guomiError('random-unavailable', 'no cryptographically secure random source is available', '当前环境不提供密码学安全随机源');
  }

  // bytesToBase64 encodes bytes as standard Base64 with padding, like b64x.StdEncoding. | bytesToBase64 按标准 Base64（带填充）编码，与 b64x.StdEncoding 一致。
  function bytesToBase64(data) {
    var bytes = toBytes(data);
    if (typeof btoa === 'function') {
      var chunked = '';
      for (var i = 0; i < bytes.length; i++) {
        chunked += String.fromCharCode(bytes[i]);
      }
      return btoa(chunked);
    }
    if (typeof Buffer !== 'undefined') {
      return Buffer.from(bytes).toString('base64');
    }
    throw guomiError('invalid-input', 'Base64 encoding is unavailable in this environment', '当前环境不支持 Base64 编码');
  }

  // base64ToBytes decodes standard Base64 into bytes. | base64ToBytes 把标准 Base64 解码为字节。
  function base64ToBytes(text) {
    if (typeof text !== 'string') {
      throw guomiError('invalid-input', 'Base64 input must be a string', 'Base64 输入必须是字符串');
    }
    if (typeof atob === 'function') {
      var binary = atob(text);
      var out = new Uint8Array(binary.length);
      for (var i = 0; i < binary.length; i++) {
        out[i] = binary.charCodeAt(i);
      }
      return out;
    }
    if (typeof Buffer !== 'undefined') {
      return Uint8Array.from(Buffer.from(text, 'base64'));
    }
    throw guomiError('invalid-input', 'Base64 decoding is unavailable in this environment', '当前环境不支持 Base64 解码');
  }

  // ---------------------------------------------------------------- SM3 | SM3 摘要

  // SM3 initial value, GB/T 32905-2016 section 4.1. | SM3 初始值，GB/T 32905-2016 第 4.1 节。
  var SM3_IV = [0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600, 0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e];

  // p0 is the SM3 permutation P0(x) = x ^ x<<<9 ^ x<<<17. | p0 是 SM3 置换 P0(x) = x ^ x<<<9 ^ x<<<17。
  function sm3P0(x) {
    return (x ^ rotl(x, 9) ^ rotl(x, 17)) >>> 0;
  }

  // sm3P1 is the SM3 message expansion permutation P1(x) = x ^ x<<<15 ^ x<<<23. | sm3P1 是 SM3 消息扩展置换 P1(x) = x ^ x<<<15 ^ x<<<23。
  function sm3P1(x) {
    return (x ^ rotl(x, 15) ^ rotl(x, 23)) >>> 0;
  }

  // sm3FF is the SM3 boolean function FF, XOR below round 16 and majority from round 16. | sm3FF 是 SM3 布尔函数 FF：前 16 轮异或，之后取多数。
  function sm3FF(round, x, y, z) {
    return round < 16 ? (x ^ y ^ z) >>> 0 : ((x & y) | (x & z) | (y & z)) >>> 0;
  }

  // sm3GG is the SM3 boolean function GG, XOR below round 16 and choice from round 16. | sm3GG 是 SM3 布尔函数 GG：前 16 轮异或，之后为选择函数。
  function sm3GG(round, x, y, z) {
    return round < 16 ? (x ^ y ^ z) >>> 0 : ((x & y) | (~x & z)) >>> 0;
  }

  // sm3Compress applies one SM3 compression round to the state word array v. | sm3Compress 对状态字数组 v 执行一轮 SM3 压缩。
  function sm3Compress(v, block, offset) {
    var w = new Uint32Array(68);
    var w1 = new Uint32Array(64);
    var i;
    for (i = 0; i < 16; i++) {
      w[i] = ((block[offset + 4 * i] << 24) | (block[offset + 4 * i + 1] << 16) | (block[offset + 4 * i + 2] << 8) | block[offset + 4 * i + 3]) >>> 0;
    }
    for (i = 16; i < 68; i++) {
      w[i] = (sm3P1((w[i - 16] ^ w[i - 9] ^ rotl(w[i - 3], 15)) >>> 0) ^ rotl(w[i - 13], 7) ^ w[i - 6]) >>> 0;
    }
    for (i = 0; i < 64; i++) {
      w1[i] = (w[i] ^ w[i + 4]) >>> 0;
    }
    var a = v[0];
    var b = v[1];
    var c = v[2];
    var d = v[3];
    var e = v[4];
    var f = v[5];
    var g = v[6];
    var h = v[7];
    for (i = 0; i < 64; i++) {
      var tj = i < 16 ? 0x79cc4519 : 0x7a879d8a;
      var ss1 = rotl(((rotl(a, 12) + e + rotl(tj, i)) >>> 0), 7);
      var ss2 = (ss1 ^ rotl(a, 12)) >>> 0;
      var tt1 = (((sm3FF(i, a, b, c) + d) >>> 0) + ((ss2 + w1[i]) >>> 0)) >>> 0;
      var tt2 = (((sm3GG(i, e, f, g) + h) >>> 0) + ((ss1 + w[i]) >>> 0)) >>> 0;
      d = c;
      c = rotl(b, 9);
      b = a;
      a = tt1;
      h = g;
      g = rotl(f, 19);
      f = e;
      e = sm3P0(tt2);
    }
    v[0] = (v[0] ^ a) >>> 0;
    v[1] = (v[1] ^ b) >>> 0;
    v[2] = (v[2] ^ c) >>> 0;
    v[3] = (v[3] ^ d) >>> 0;
    v[4] = (v[4] ^ e) >>> 0;
    v[5] = (v[5] ^ f) >>> 0;
    v[6] = (v[6] ^ g) >>> 0;
    v[7] = (v[7] ^ h) >>> 0;
  }

  // sm3Stream returns an incremental SM3 hasher with update / digest / reset. | sm3Stream 返回可增量的 SM3 散列器，提供 update / digest / reset。
  //
  // digest() follows the hash.Hash contract of the Go wrapper: it never mutates the state, so the
  // same instance can keep absorbing data afterwards. | digest() 遵循 Go 包装的 hash.Hash 约定：不改变状态，因此同一实例之后仍可继续吸收数据。
  function sm3Stream() {
    var v = Int32Array.from(SM3_IV.map(function (word) { return word | 0; }));
    var buffer = new Uint8Array(64);
    var buffered = 0;
    var total = 0;

    function absorb(block, offset) {
      var copy = Uint32Array.from(v.map(function (word) { return word >>> 0; }));
      sm3Compress(copy, block, offset);
      for (var i = 0; i < 8; i++) {
        v[i] = copy[i] | 0;
      }
    }

    return {
      // update absorbs more bytes; strings are converted as UTF-8. | update 继续吸收字节；字符串按 UTF-8 转换。
      update: function (data) {
        var bytes = toBytes(data);
        total += bytes.length;
        var offset = 0;
        if (buffered > 0) {
          var take = Math.min(64 - buffered, bytes.length);
          buffer.set(bytes.subarray(0, take), buffered);
          buffered += take;
          offset = take;
          if (buffered === 64) {
            absorb(buffer, 0);
            buffered = 0;
          }
        }
        while (offset + 64 <= bytes.length) {
          absorb(bytes, offset);
          offset += 64;
        }
        if (offset < bytes.length) {
          buffer.set(bytes.subarray(offset), 0);
          buffered = bytes.length - offset;
        }
        return this;
      },
      // digest finalises the padding and returns the 32-byte digest. | digest 完成填充并返回 32 字节摘要。
      digest: function () {
        var state = Uint32Array.from(v.map(function (word) { return word >>> 0; }));
        var bitLength = total * 8;
        var padLength = (56 - ((buffered + 1) % 64) + 64) % 64;
        var tail = new Uint8Array(buffered + 1 + padLength + 8);
        tail.set(buffer.subarray(0, buffered), 0);
        tail[buffered] = 0x80;
        var high = Math.floor(bitLength / 4294967296);
        var low = bitLength % 4294967296;
        tail[tail.length - 8] = (high >>> 24) & 0xff;
        tail[tail.length - 7] = (high >>> 16) & 0xff;
        tail[tail.length - 6] = (high >>> 8) & 0xff;
        tail[tail.length - 5] = high & 0xff;
        tail[tail.length - 4] = (low >>> 24) & 0xff;
        tail[tail.length - 3] = (low >>> 16) & 0xff;
        tail[tail.length - 2] = (low >>> 8) & 0xff;
        tail[tail.length - 1] = low & 0xff;
        for (var offset = 0; offset < tail.length; offset += 64) {
          sm3Compress(state, tail, offset);
        }
        var out = new Uint8Array(32);
        for (var i = 0; i < 8; i++) {
          out[4 * i] = (state[i] >>> 24) & 0xff;
          out[4 * i + 1] = (state[i] >>> 16) & 0xff;
          out[4 * i + 2] = (state[i] >>> 8) & 0xff;
          out[4 * i + 3] = state[i] & 0xff;
        }
        return out;
      },
      // reset clears the state so the instance can be reused. | reset 复位状态以便复用实例。
      reset: function () {
        for (var i = 0; i < 8; i++) {
          v[i] = SM3_IV[i] | 0;
        }
        buffer = new Uint8Array(64);
        buffered = 0;
        total = 0;
        return this;
      },
    };
  }

  // sm3 hashes a Uint8Array or string once and returns the 32-byte digest. | sm3 一次性计算 Uint8Array 或字符串的摘要，返回 32 字节结果。
  function sm3(data) {
    var stream = sm3Stream();
    stream.update(data);
    return stream.digest();
  }

  // sm3Hex returns the lowercase hex SM3 digest of a Uint8Array or string. | sm3Hex 返回 Uint8Array 或字符串的小写十六进制 SM3 摘要。
  function sm3Hex(data) {
    return bytesToHex(sm3(data));
  }

  // ---------------------------------------------------------------- SM2 | SM2 公钥密码

  // SM2 recommended curve parameters, GB/T 32918.5-2017. | SM2 推荐曲线参数，GB/T 32918.5-2017。
  var CURVE = {
    p: BigInt('0xfffffffeffffffffffffffffffffffffffffffff00000000ffffffffffffffff'),
    a: BigInt('0xfffffffeffffffffffffffffffffffffffffffff00000000fffffffffffffffc'),
    b: BigInt('0x28e9fa9e9d9f5e344d5a9e4bcf6509a7f39789f515ab8f92ddbcbd414d940e93'),
    n: BigInt('0xfffffffeffffffffffffffffffffffff7203df6b21c6052b53bbf40939d54123'),
    gx: BigInt('0x32c4ae2c1f1981195f9904466a39c9948fe30bbff2660be1715a4589334c74c7'),
    gy: BigInt('0xbc3736a2f4f6779c59bdcee36b692153d0a9877cc62a474002df32e52139f0a0'),
  };

  // SM2_DEFAULT_UID is the default user identifier used by ZA, matching gmsm and guomi. | SM2_DEFAULT_UID 是 ZA 使用的默认用户标识，与 gmsm 及 guomi 一致。
  var SM2_DEFAULT_UID = '1234567812345678';

  // INFINITY is the Jacobian point at infinity. | INFINITY 是 Jacobian 坐标下的无穷远点。
  var INFINITY = { x: 0n, y: 1n, z: 0n };

  // mod reduces a BigInt into [0, m), so negative intermediates never leak out. | mod 把 BigInt 归约到 [0, m)，避免负中间值外泄。
  function mod(value, m) {
    var out = value % m;
    return out < 0n ? out + m : out;
  }

  // modInv computes the modular inverse through the extended Euclidean algorithm. | modInv 用扩展欧几里得算法求模逆。
  function modInv(value, m) {
    var a = mod(value, m);
    if (a === 0n) {
      throw guomiError('invalid-key', 'value has no inverse modulo the curve order', '该值在曲线阶下不存在模逆');
    }
    var g = m;
    var x = 0n;
    var y = 1n;
    while (a !== 0n) {
      var q = g / a;
      var t = g - q * a;
      g = a;
      a = t;
      t = x - q * y;
      x = y;
      y = t;
    }
    if (g !== 1n) {
      throw guomiError('invalid-key', 'value is not invertible modulo the curve order', '该值在曲线阶下不可逆');
    }
    return mod(x, m);
  }

  // jacobianDouble doubles a Jacobian point on the SM2 curve, whose a equals -3. | jacobianDouble 在 SM2 曲线（a 为 -3）上对 Jacobian 点做倍点。
  function jacobianDouble(point) {
    if (point.z === 0n || point.y === 0n) {
      return INFINITY;
    }
    var p = CURVE.p;
    var delta = mod(point.z * point.z, p);
    var gamma = mod(point.y * point.y, p);
    var beta = mod(point.x * gamma, p);
    var alpha = mod(3n * (point.x - delta) * (point.x + delta), p);
    var x3 = mod(alpha * alpha - 8n * beta, p);
    var z3 = mod((point.y + point.z) * (point.y + point.z) - gamma - delta, p);
    var y3 = mod(alpha * (4n * beta - x3) - 8n * gamma * gamma, p);
    return { x: x3, y: y3, z: z3 };
  }

  // jacobianAdd adds two Jacobian points on the SM2 curve. | jacobianAdd 在 SM2 曲线上做 Jacobian 点加法。
  function jacobianAdd(left, right) {
    if (left.z === 0n) {
      return right;
    }
    if (right.z === 0n) {
      return left;
    }
    var p = CURVE.p;
    var z1z1 = mod(left.z * left.z, p);
    var z2z2 = mod(right.z * right.z, p);
    var u1 = mod(left.x * z2z2, p);
    var u2 = mod(right.x * z1z1, p);
    var s1 = mod(left.y * right.z * z2z2, p);
    var s2 = mod(right.y * left.z * z1z1, p);
    if (u1 === u2) {
      if (s1 !== s2) {
        return INFINITY;
      }
      return jacobianDouble(left);
    }
    var h = mod(u2 - u1, p);
    var i = mod(4n * h * h, p);
    var j = mod(h * i, p);
    var r = mod(2n * (s2 - s1), p);
    var v = mod(u1 * i, p);
    var x3 = mod(r * r - j - 2n * v, p);
    var y3 = mod(r * (v - x3) - 2n * s1 * j, p);
    var z3 = mod(((left.z + right.z) * (left.z + right.z) - z1z1 - z2z2) * h, p);
    return { x: x3, y: y3, z: z3 };
  }

  // jacobianToAffine converts a Jacobian point back to affine coordinates, returning null at infinity. | jacobianToAffine 把 Jacobian 点转回仿射坐标，无穷远点返回 null。
  function jacobianToAffine(point) {
    if (point.z === 0n) {
      return null;
    }
    var p = CURVE.p;
    var zinv = modInv(point.z, p);
    var zinv2 = mod(zinv * zinv, p);
    return { x: mod(point.x * zinv2, p), y: mod(point.y * zinv2 * zinv, p) };
  }

  // scalarMult multiplies an affine point by a scalar with the double-and-add ladder. | scalarMult 用倍点-加点阶梯对仿射点做标量乘。
  function scalarMult(scalar, point) {
    var k = mod(scalar, CURVE.n);
    var result = INFINITY;
    var addend = { x: point.x, y: point.y, z: 1n };
    while (k > 0n) {
      if (k & 1n) {
        result = jacobianAdd(result, addend);
      }
      addend = jacobianDouble(addend);
      k >>= 1n;
    }
    return jacobianToAffine(result);
  }

  // scalarBaseMult multiplies the generator G by a scalar. | scalarBaseMult 对基点 G 做标量乘。
  function scalarBaseMult(scalar) {
    return scalarMult(scalar, { x: CURVE.gx, y: CURVE.gy });
  }

  // isOnCurve reports whether an affine point satisfies y^2 = x^3 + a*x + b over F_p. | isOnCurve 判断仿射点是否满足 F_p 上的 y^2 = x^3 + a*x + b。
  function isOnCurve(point) {
    if (!point) {
      return false;
    }
    var p = CURVE.p;
    if (point.x < 0n || point.x >= p || point.y < 0n || point.y >= p) {
      return false;
    }
    var left = mod(point.y * point.y, p);
    var right = mod(point.x * point.x * point.x + CURVE.a * point.x + CURVE.b, p);
    return left === right;
  }

  // parseScalar reads a 64-character private scalar and range-checks it against [1, n-1]. | parseScalar 解析 64 字符私钥标量并校验其落在 [1, n-1]。
  function parseScalar(value, label) {
    var text = typeof value === 'string' ? value.trim().replace(/^0[xX]/, '') : '';
    if (text.length !== 64) {
      throw guomiError('invalid-key', label + ' must be 64 hex characters, got ' + text.length, label + ' 必须是 64 个十六进制字符，实际为 ' + text.length);
    }
    var scalar = bytesToBigIntBE(hexToBytes(text));
    if (scalar < 1n || scalar >= CURVE.n) {
      throw guomiError('invalid-key', label + ' is outside the range [1, n-1]', label + ' 超出 [1, n-1] 范围');
    }
    return scalar;
  }

  // parsePublicKey accepts 130 hex characters (04||X||Y) or 128 hex characters (X||Y). | parsePublicKey 接受 130 个十六进制字符（04||X||Y）或 128 个字符（X||Y）。
  function parsePublicKey(value) {
    var text = typeof value === 'string' ? value.trim().replace(/^0[xX]/, '') : '';
    if (text.length === 130 && /^04/i.test(text)) {
      text = text.slice(2);
    }
    if (text.length !== 128) {
      throw guomiError('invalid-key', 'public key must be 130 or 128 hex characters, got ' + text.length, '公钥必须是 130 或 128 个十六进制字符，实际为 ' + text.length);
    }
    var raw = hexToBytes(text);
    var point = { x: bytesToBigIntBE(raw.subarray(0, 32)), y: bytesToBigIntBE(raw.subarray(32, 64)) };
    if (!isOnCurve(point)) {
      throw guomiError('invalid-key', 'public key point is not on the SM2 curve', '公钥点不在 SM2 曲线上');
    }
    return point;
  }

  // derivePublicKey returns the uncompressed point 04||X||Y for a private scalar. | derivePublicKey 由私钥标量返回未压缩点 04||X||Y。
  function derivePublicKey(privateKeyHex) {
    var point = scalarBaseMult(parseScalar(privateKeyHex, 'private key | 私钥'));
    return '04' + bytesToHex(bigIntToBytesBE(point.x, 32)) + bytesToHex(bigIntToBytesBE(point.y, 32));
  }

  // randomScalar draws a uniform scalar in [1, n-1] by rejection sampling. | randomScalar 用拒绝采样生成 [1, n-1] 内的均匀标量。
  function randomScalar() {
    for (var attempt = 0; attempt < 64; attempt++) {
      var candidate = bytesToBigIntBE(randomBytes(32));
      if (candidate >= 1n && candidate < CURVE.n) {
        return candidate;
      }
    }
    throw guomiError('random-unavailable', 'failed to draw a scalar in [1, n-1]', '无法生成 [1, n-1] 内的标量');
  }

  // sm2KDF is the GB/T 32918.4 section 5.4.3 key derivation function over SM3. | sm2KDF 是 GB/T 32918.4 第 5.4.3 节的 SM3 密钥派生函数。
  //
  // It returns the derived stream plus the all-zero flag the standard requires callers to react to. | 返回派生流以及标准要求调用方处理的全零标志。
  function sm2KDF(x2, y2, length) {
    var out = new Uint8Array(length);
    if (length === 0) {
      return { stream: out, allZero: true };
    }
    var counter = new Uint8Array(4);
    var offset = 0;
    var ct = 1;
    while (offset < length) {
      counter[0] = (ct >>> 24) & 0xff;
      counter[1] = (ct >>> 16) & 0xff;
      counter[2] = (ct >>> 8) & 0xff;
      counter[3] = ct & 0xff;
      var block = sm3(concatBytes(x2, y2, counter));
      var take = Math.min(32, length - offset);
      out.set(block.subarray(0, take), offset);
      offset += take;
      ct++;
    }
    var allZero = true;
    for (var i = 0; i < out.length; i++) {
      if (out[i] !== 0) {
        allZero = false;
        break;
      }
    }
    return { stream: out, allZero: allZero };
  }

  // parseRandomiserOption normalises opts.k, accepting hex text or 32 bytes. | parseRandomiserOption 归一化 opts.k，接受十六进制文本或 32 字节。
  //
  // A pinned randomiser is a test affordance: it makes ciphertexts and signatures reproducible, and
  // it must still be a valid scalar. | 固定随机数是测试用的能力：它让密文与签名可复现，同时必须仍是合法标量。
  function parseRandomiserOption(value) {
    if (value === undefined || value === null) {
      return null;
    }
    var text = typeof value === 'string' ? value : bytesToHex(toBytes(value));
    return parseScalar(text, 'randomiser | 随机数');
  }

  // sm2Encrypt encrypts bytes into the raw C1C3C2 layout used by guomi.EncryptRaw. | sm2Encrypt 把字节加密为 guomi.EncryptRaw 使用的原始 C1C3C2 布局。
  //
  // opts.k pins the randomiser for reproducible vectors; opts.c1Prefix prepends the 0x04 marker. | opts.k 固定随机数以产出可复现向量；opts.c1Prefix 前置 0x04 标识字节。
  function sm2Encrypt(publicKeyHex, data, opts) {
    var options = opts || {};
    var point = parsePublicKey(publicKeyHex);
    var message = toBytes(data);
    var fixed = parseRandomiserOption(options.k);
    for (var attempt = 0; attempt < 64; attempt++) {
      var k = fixed === null ? randomScalar() : fixed;
      var c1 = scalarBaseMult(k);
      var shared = scalarMult(k, point);
      if (!shared) {
        throw guomiError('invalid-key', 'the shared point is at infinity', '共享点为无穷远点');
      }
      var x2 = bigIntToBytesBE(shared.x, 32);
      var y2 = bigIntToBytesBE(shared.y, 32);
      var derived = sm2KDF(x2, y2, message.length);
      if (message.length > 0 && derived.allZero) {
        // GB/T 32918.4 requires a fresh randomiser when the stream is all zero. | GB/T 32918.4 要求密钥流全零时更换随机数。
        if (fixed !== null) {
          throw guomiError('invalid-key', 'the fixed randomiser derives an all-zero keystream', '固定随机数派生出全零密钥流');
        }
        continue;
      }
      var c2 = new Uint8Array(message.length);
      for (var i = 0; i < message.length; i++) {
        c2[i] = message[i] ^ derived.stream[i];
      }
      var c3 = sm3(concatBytes(x2, message, y2));
      var body = concatBytes(bigIntToBytesBE(c1.x, 32), bigIntToBytesBE(c1.y, 32), c3, c2);
      return options.c1Prefix ? concatBytes(new Uint8Array([0x04]), body) : body;
    }
    throw guomiError('random-unavailable', 'failed to draw a usable randomiser', '无法生成可用的随机数');
  }

  // sm2DecryptOne attempts a decryption with one concrete layout. | sm2DecryptOne 以某一种具体布局尝试解密。
  function sm2DecryptOne(privateKeyHex, ciphertext) {
    if (ciphertext.length < 96) {
      throw guomiError('invalid-ciphertext', 'ciphertext is shorter than 96 bytes: ' + ciphertext.length, '密文短于 96 字节：' + ciphertext.length);
    }
    var x1 = bytesToBigIntBE(ciphertext.subarray(0, 32));
    var y1 = bytesToBigIntBE(ciphertext.subarray(32, 64));
    var c1 = { x: x1, y: y1 };
    if (!isOnCurve(c1)) {
      throw guomiError('invalid-ciphertext', 'C1 is not on the SM2 curve', 'C1 不在 SM2 曲线上');
    }
    var scalar = parseScalar(privateKeyHex, 'private key | 私钥');
    var shared = scalarMult(scalar, c1);
    if (!shared) {
      throw guomiError('invalid-ciphertext', 'C1 multiplied by the private key is at infinity', 'C1 与私钥相乘得到无穷远点');
    }
    var x2 = bigIntToBytesBE(shared.x, 32);
    var y2 = bigIntToBytesBE(shared.y, 32);
    var c3 = ciphertext.subarray(64, 96);
    var c2 = ciphertext.subarray(96);
    var derived = sm2KDF(x2, y2, c2.length);
    if (c2.length > 0 && derived.allZero) {
      throw guomiError('decrypt-failed', 'key derivation produced an all-zero stream', '密钥派生结果全为零');
    }
    var message = new Uint8Array(c2.length);
    for (var i = 0; i < c2.length; i++) {
      message[i] = c2[i] ^ derived.stream[i];
    }
    if (!bytesEqual(sm3(concatBytes(x2, message, y2)), c3)) {
      throw guomiError('decrypt-failed', 'C3 verification failed: wrong key or tampered ciphertext', 'C3 校验失败：密钥错误或密文被篡改');
    }
    return message;
  }

  // ciphertextBytes normalises a ciphertext argument, decoding a hex dump as hex instead of text. | ciphertextBytes 规范化密文参数：十六进制转储按十六进制解码，而不按文本处理。
  //
  // A binary ciphertext has no meaningful text form, so a pure-hex string that is long enough to hold the
  // 96-byte C1||C3 header is always a hex dump (this is how the Go vectors hand ciphertexts over). | 二进制密文没有
  // 有意义的文本形式，因此凡是能容纳 96 字节 C1||C3 头的纯十六进制字符串必定是转储（Go 向量即以此方式交付密文）。
  function ciphertextBytes(ciphertext) {
    if (typeof ciphertext === 'string' && ciphertext.length >= 192 && ciphertext.length % 2 === 0 && /^[0-9a-fA-F]+$/.test(ciphertext)) {
      return hexToBytes(ciphertext);
    }
    return toBytes(ciphertext);
  }

  // sm2Decrypt decrypts the raw layout, auto-detecting the optional 0x04 marker. | sm2Decrypt 解密原始布局，并自动识别可选的 0x04 标识字节。
  function sm2Decrypt(privateKeyHex, ciphertext, opts) {
    var options = opts || {};
    var bytes = ciphertextBytes(ciphertext);
    var prefixed = options.c1Prefix;
    if (prefixed === true) {
      if (bytes.length === 0 || bytes[0] !== 0x04) {
        throw guomiError('invalid-ciphertext', 'c1Prefix was requested but the 0x04 marker is missing', '要求 c1Prefix 但缺少 0x04 标识字节');
      }
      return sm2DecryptOne(privateKeyHex, bytes.subarray(1));
    }
    try {
      return sm2DecryptOne(privateKeyHex, bytes);
    } catch (err) {
      // Auto mode: fall back to the gmsm-style 0x04-prefixed layout only when it is well formed. | 自动模式：仅在结构合法时回退到 gmsm 风格的 0x04 前缀布局。
      if (prefixed === undefined && bytes.length > 96 && bytes[0] === 0x04) {
        return sm2DecryptOne(privateKeyHex, bytes.subarray(1));
      }
      throw err;
    }
  }

  // sm2Digest computes e = SM3(ZA || data) with the default user identifier. | sm2Digest 以默认用户标识计算 e = SM3(ZA || data)。
  function sm2Digest(point, data) {
    var uid = utf8Encode(SM2_DEFAULT_UID);
    var entl = new Uint8Array([(uid.length * 8) >>> 8, (uid.length * 8) & 0xff]);
    var za = sm3(concatBytes(
      entl,
      uid,
      bigIntToBytesBE(CURVE.a, 32),
      bigIntToBytesBE(CURVE.b, 32),
      bigIntToBytesBE(CURVE.gx, 32),
      bigIntToBytesBE(CURVE.gy, 32),
      bigIntToBytesBE(point.x, 32),
      bigIntToBytesBE(point.y, 32)
    ));
    return bytesToBigIntBE(sm3(concatBytes(za, data)));
  }

  // sm2DerInteger encodes one positive INTEGER in DER form. | sm2DerInteger 以 DER 形式编码一个正整数 INTEGER。
  function sm2DerInteger(value) {
    var raw = bigIntToBytesBE(value, 32);
    var start = 0;
    while (start < raw.length - 1 && raw[start] === 0) {
      start++;
    }
    var body = raw.subarray(start);
    if (body[0] & 0x80) {
      body = concatBytes(new Uint8Array([0x00]), body);
    }
    return concatBytes(new Uint8Array([0x02, body.length]), body);
  }

  // sm2DerEncode renders (r, s) as SEQUENCE { INTEGER r, INTEGER s }. | sm2DerEncode 把 (r, s) 渲染为 SEQUENCE { INTEGER r, INTEGER s }。
  function sm2DerEncode(r, s) {
    var body = concatBytes(sm2DerInteger(r), sm2DerInteger(s));
    if (body.length > 127) {
      throw guomiError('invalid-signature', 'derived signature body exceeds the short-form length', '派生签名的内容超出短形式长度');
    }
    return concatBytes(new Uint8Array([0x30, body.length]), body);
  }

  // sm2ReadDerLength reads one DER length field, supporting short and long forms. | sm2ReadDerLength 读取一个 DER 长度字段，支持短形式与长形式。
  function sm2ReadDerLength(bytes, offset) {
    if (offset >= bytes.length) {
      return null;
    }
    var first = bytes[offset];
    if ((first & 0x80) === 0) {
      return { length: first, next: offset + 1 };
    }
    var count = first & 0x7f;
    if (count === 0 || count > 4 || offset + 1 + count > bytes.length) {
      return null;
    }
    var length = 0;
    for (var i = 0; i < count; i++) {
      length = length * 256 + bytes[offset + 1 + i];
    }
    return { length: length, next: offset + 1 + count };
  }

  // sm2ReadDerInteger reads one DER INTEGER as a signed BigInt. | sm2ReadDerInteger 读取一个 DER INTEGER 并按有符号 BigInt 返回。
  function sm2ReadDerInteger(bytes, offset) {
    if (offset >= bytes.length || bytes[offset] !== 0x02) {
      return null;
    }
    var header = sm2ReadDerLength(bytes, offset + 1);
    if (!header || header.length === 0 || header.next + header.length > bytes.length) {
      return null;
    }
    var body = bytes.subarray(header.next, header.next + header.length);
    var value = bytesToBigIntBE(body);
    if (body[0] & 0x80) {
      value -= 1n << BigInt(8 * body.length);
    }
    return { value: value, next: header.next + header.length };
  }

  // sm2DerDecode parses SEQUENCE { INTEGER r, INTEGER s }, returning null when malformed. | sm2DerDecode 解析 SEQUENCE { INTEGER r, INTEGER s }，结构非法时返回 null。
  function sm2DerDecode(bytes) {
    if (bytes.length < 8 || bytes[0] !== 0x30) {
      return null;
    }
    var header = sm2ReadDerLength(bytes, 1);
    if (!header || header.next + header.length > bytes.length) {
      return null;
    }
    var r = sm2ReadDerInteger(bytes, header.next);
    if (!r) {
      return null;
    }
    var s = sm2ReadDerInteger(bytes, r.next);
    if (!s) {
      return null;
    }
    return { r: r.value, s: s.value };
  }

  // sm2Sign signs data per GB/T 32918.2 and returns the DER signature. | sm2Sign 按 GB/T 32918.2 对数据签名，返回 DER 签名。
  //
  // opts.k pins the randomiser, which is what makes reproducible vectors possible. | opts.k 固定随机数，从而可以产出可复现的向量。
  function sm2Sign(privateKeyHex, data, opts) {
    var options = opts || {};
    var scalar = parseScalar(privateKeyHex, 'private key | 私钥');
    var publicPoint = scalarBaseMult(scalar);
    var e = sm2Digest(publicPoint, toBytes(data));
    var fixed = parseRandomiserOption(options.k);
    var dInv = modInv(1n + scalar, CURVE.n);
    for (var attempt = 0; attempt < 64; attempt++) {
      var k = fixed === null ? randomScalar() : fixed;
      var kPoint = scalarBaseMult(k);
      var r = mod(e + kPoint.x, CURVE.n);
      if (r === 0n || mod(r + k, CURVE.n) === 0n) {
        if (fixed !== null) {
          throw guomiError('invalid-signature', 'the fixed randomiser yields r = 0 or r + k = n', '固定随机数导致 r = 0 或 r + k = n');
        }
        continue;
      }
      var s = mod(dInv * mod(k - r * scalar, CURVE.n), CURVE.n);
      if (s === 0n) {
        if (fixed !== null) {
          throw guomiError('invalid-signature', 'the fixed randomiser yields s = 0', '固定随机数导致 s = 0');
        }
        continue;
      }
      return sm2DerEncode(r, s);
    }
    throw guomiError('random-unavailable', 'failed to draw a usable randomiser', '无法生成可用的随机数');
  }

  // sm2Verify checks a DER signature, returning false for every malformed or mismatching input. | sm2Verify 校验 DER 签名，结构非法或不匹配一律返回 false。
  function sm2Verify(publicKeyHex, data, signature) {
    var point = parsePublicKey(publicKeyHex);
    var der;
    if (typeof signature === 'string') {
      try {
        der = hexToBytes(signature);
      } catch (err) {
        return false;
      }
    } else {
      try {
        der = toBytes(signature);
      } catch (err) {
        return false;
      }
    }
    var parsed = sm2DerDecode(der);
    if (!parsed) {
      return false;
    }
    var r = parsed.r;
    var s = parsed.s;
    if (r < 1n || r >= CURVE.n || s < 1n || s >= CURVE.n) {
      return false;
    }
    var e = sm2Digest(point, toBytes(data));
    var t = mod(r + s, CURVE.n);
    if (t === 0n) {
      return false;
    }
    var sG = scalarBaseMult(s);
    var tP = scalarMult(t, point);
    if (!sG || !tP) {
      return false;
    }
    var affine = jacobianToAffine(jacobianAdd({ x: sG.x, y: sG.y, z: 1n }, { x: tP.x, y: tP.y, z: 1n }));
    if (!affine) {
      return false;
    }
    return mod(e + affine.x, CURVE.n) === r;
  }

  // sm2GenerateKeyPair resolves to a fresh lowercase hex key pair. | sm2GenerateKeyPair 解析为一条全新的小写十六进制密钥对。
  function sm2GenerateKeyPair() {
    return Promise.resolve().then(function () {
      var scalar = randomScalar();
      return {
        publicKey: derivePublicKey(bytesToHex(bigIntToBytesBE(scalar, 32))),
        privateKey: bytesToHex(bigIntToBytesBE(scalar, 32)),
      };
    });
  }

  // ---------------------------------------------------------------- SM4 | SM4 分组密码

  // SM4_SBOX is the GB/T 32907-2016 substitution box. | SM4_SBOX 是 GB/T 32907-2016 的 S 盒。
  var SM4_SBOX = new Uint8Array([
    0xd6, 0x90, 0xe9, 0xfe, 0xcc, 0xe1, 0x3d, 0xb7, 0x16, 0xb6, 0x14, 0xc2, 0x28, 0xfb, 0x2c, 0x05,
    0x2b, 0x67, 0x9a, 0x76, 0x2a, 0xbe, 0x04, 0xc3, 0xaa, 0x44, 0x13, 0x26, 0x49, 0x86, 0x06, 0x99,
    0x9c, 0x42, 0x50, 0xf4, 0x91, 0xef, 0x98, 0x7a, 0x33, 0x54, 0x0b, 0x43, 0xed, 0xcf, 0xac, 0x62,
    0xe4, 0xb3, 0x1c, 0xa9, 0xc9, 0x08, 0xe8, 0x95, 0x80, 0xdf, 0x94, 0xfa, 0x75, 0x8f, 0x3f, 0xa6,
    0x47, 0x07, 0xa7, 0xfc, 0xf3, 0x73, 0x17, 0xba, 0x83, 0x59, 0x3c, 0x19, 0xe6, 0x85, 0x4f, 0xa8,
    0x68, 0x6b, 0x81, 0xb2, 0x71, 0x64, 0xda, 0x8b, 0xf8, 0xeb, 0x0f, 0x4b, 0x70, 0x56, 0x9d, 0x35,
    0x1e, 0x24, 0x0e, 0x5e, 0x63, 0x58, 0xd1, 0xa2, 0x25, 0x22, 0x7c, 0x3b, 0x01, 0x21, 0x78, 0x87,
    0xd4, 0x00, 0x46, 0x57, 0x9f, 0xd3, 0x27, 0x52, 0x4c, 0x36, 0x02, 0xe7, 0xa0, 0xc4, 0xc8, 0x9e,
    0xea, 0xbf, 0x8a, 0xd2, 0x40, 0xc7, 0x38, 0xb5, 0xa3, 0xf7, 0xf2, 0xce, 0xf9, 0x61, 0x15, 0xa1,
    0xe0, 0xae, 0x5d, 0xa4, 0x9b, 0x34, 0x1a, 0x55, 0xad, 0x93, 0x32, 0x30, 0xf5, 0x8c, 0xb1, 0xe3,
    0x1d, 0xf6, 0xe2, 0x2e, 0x82, 0x66, 0xca, 0x60, 0xc0, 0x29, 0x23, 0xab, 0x0d, 0x53, 0x4e, 0x6f,
    0xd5, 0xdb, 0x37, 0x45, 0xde, 0xfd, 0x8e, 0x2f, 0x03, 0xff, 0x6a, 0x72, 0x6d, 0x6c, 0x5b, 0x51,
    0x8d, 0x1b, 0xaf, 0x92, 0xbb, 0xdd, 0xbc, 0x7f, 0x11, 0xd9, 0x5c, 0x41, 0x1f, 0x10, 0x5a, 0xd8,
    0x0a, 0xc1, 0x31, 0x88, 0xa5, 0xcd, 0x7b, 0xbd, 0x2d, 0x74, 0xd0, 0x12, 0xb8, 0xe5, 0xb4, 0xb0,
    0x89, 0x69, 0x97, 0x4a, 0x0c, 0x96, 0x77, 0x7e, 0x65, 0xb9, 0xf1, 0x09, 0xc5, 0x6e, 0xc6, 0x84,
    0x18, 0xf0, 0x7d, 0xec, 0x3a, 0xdc, 0x4d, 0x20, 0x79, 0xee, 0x5f, 0x3e, 0xd7, 0xcb, 0x39, 0x48,
  ]);

  // SM4_FK is the fixed key parameter of the SM4 key schedule. | SM4_FK 是 SM4 密钥编排的固定参数。
  var SM4_FK = new Uint32Array([0xa3b1bac6, 0x56aa3350, 0x677d9197, 0xb27022dc]);

  // SM4_CK is the constant key parameter of the SM4 key schedule. | SM4_CK 是 SM4 密钥编排的常量参数。
  var SM4_CK = new Uint32Array([
    0x00070e15, 0x1c232a31, 0x383f464d, 0x545b6269,
    0x70777e85, 0x8c939aa1, 0xa8afb6bd, 0xc4cbd2d9,
    0xe0e7eef5, 0xfc030a11, 0x181f262d, 0x343b4249,
    0x50575e65, 0x6c737a81, 0x888f969d, 0xa4abb2b9,
    0xc0c7ced5, 0xdce3eaf1, 0xf8ff060d, 0x141b2229,
    0x30373e45, 0x4c535a61, 0x686f767d, 0x848b9299,
    0xa0a7aeb5, 0xbcc3cad1, 0xd8dfe6ed, 0xf4fb0209,
    0x10171e25, 0x2c333a41, 0x484f565d, 0x646b7279,
  ]);

  // sm4Tau applies the SM4 non-linear substitution to the four bytes of a word. | sm4Tau 对字的四个字节施加 SM4 非线性替换。
  function sm4Tau(word) {
    return ((SM4_SBOX[(word >>> 24) & 0xff] << 24) | (SM4_SBOX[(word >>> 16) & 0xff] << 16) | (SM4_SBOX[(word >>> 8) & 0xff] << 8) | SM4_SBOX[word & 0xff]) >>> 0;
  }

  // sm4L is the linear transformation L(B) used by the round function. | sm4L 是轮函数使用的线性变换 L(B)。
  function sm4L(word) {
    return (word ^ rotl(word, 2) ^ rotl(word, 10) ^ rotl(word, 18) ^ rotl(word, 24)) >>> 0;
  }

  // sm4LPrime is the linear transformation L'(B) used by the key schedule. | sm4LPrime 是密钥编排使用的线性变换 L'(B)。
  function sm4LPrime(word) {
    return (word ^ rotl(word, 13) ^ rotl(word, 23)) >>> 0;
  }

  // sm4KeySchedule expands a 16-byte key into the 32 round keys. | sm4KeySchedule 把 16 字节密钥扩展为 32 个轮密钥。
  function sm4KeySchedule(key) {
    if (key.length !== 16) {
      throw guomiError('invalid-key', 'SM4 key must be 16 bytes, got ' + key.length, 'SM4 密钥必须是 16 字节，实际为 ' + key.length);
    }
    var k = new Uint32Array(4);
    for (var i = 0; i < 4; i++) {
      k[i] = (((key[4 * i] << 24) | (key[4 * i + 1] << 16) | (key[4 * i + 2] << 8) | key[4 * i + 3]) >>> 0) ^ SM4_FK[i];
    }
    var roundKeys = new Uint32Array(32);
    for (var round = 0; round < 32; round++) {
      var mixed = (k[1] ^ k[2] ^ k[3] ^ SM4_CK[round]) >>> 0;
      var next = (k[0] ^ sm4LPrime(sm4Tau(mixed))) >>> 0;
      roundKeys[round] = next;
      k[0] = k[1];
      k[1] = k[2];
      k[2] = k[3];
      k[3] = next;
    }
    return roundKeys;
  }

  // sm4BlockCrypt applies the SM4 round function with the given round key order. | sm4BlockCrypt 按给定轮密钥顺序执行 SM4 轮函数。
  function sm4BlockCrypt(roundKeys, input) {
    var x = new Uint32Array(4);
    for (var i = 0; i < 4; i++) {
      x[i] = ((input[4 * i] << 24) | (input[4 * i + 1] << 16) | (input[4 * i + 2] << 8) | input[4 * i + 3]) >>> 0;
    }
    var word = 0;
    for (var round = 0; round < 32; round++) {
      var mixed = (x[1] ^ x[2] ^ x[3] ^ roundKeys[round]) >>> 0;
      word = (x[0] ^ sm4L(sm4Tau(mixed))) >>> 0;
      x[0] = x[1];
      x[1] = x[2];
      x[2] = x[3];
      x[3] = word;
    }
    var out = new Uint8Array(16);
    for (var j = 0; j < 4; j++) {
      var value = x[3 - j];
      out[4 * j] = (value >>> 24) & 0xff;
      out[4 * j + 1] = (value >>> 16) & 0xff;
      out[4 * j + 2] = (value >>> 8) & 0xff;
      out[4 * j + 3] = value & 0xff;
    }
    return out;
  }

  // sm4ReverseRoundKeys returns the round keys in decryption order. | sm4ReverseRoundKeys 返回解密顺序的轮密钥。
  function sm4ReverseRoundKeys(roundKeys) {
    var out = new Uint32Array(32);
    for (var i = 0; i < 32; i++) {
      out[i] = roundKeys[31 - i];
    }
    return out;
  }

  // pkcs7Pad pads plaintext to a positive multiple of 16 bytes, always adding a block. | pkcs7Pad 把明文填充到 16 字节的正整数倍，且总是追加一个整块。
  function pkcs7Pad(plaintext) {
    var pad = 16 - (plaintext.length % 16);
    var out = new Uint8Array(plaintext.length + pad);
    out.set(plaintext, 0);
    out.fill(pad, plaintext.length);
    return out;
  }

  // pkcs7Unpad validates and removes the PKCS#7 padding. | pkcs7Unpad 校验并移除 PKCS#7 填充。
  function pkcs7Unpad(padded) {
    if (padded.length === 0 || padded.length % 16 !== 0) {
      throw guomiError('invalid-ciphertext', 'ciphertext length is not a positive multiple of 16', '密文长度不是 16 的正整数倍');
    }
    var pad = padded[padded.length - 1];
    if (pad === 0 || pad > 16 || pad > padded.length) {
      throw guomiError('invalid-padding', 'PKCS#7 padding byte is invalid: 0x' + pad.toString(16), 'PKCS#7 填充字节非法：0x' + pad.toString(16));
    }
    for (var i = padded.length - pad; i < padded.length; i++) {
      if (padded[i] !== pad) {
        throw guomiError('invalid-padding', 'PKCS#7 padding content is inconsistent', 'PKCS#7 填充内容与填充字节不一致');
      }
    }
    return padded.slice(0, padded.length - pad);
  }

  // sm4ValidateKey normalises and checks the 16-byte SM4 key. | sm4ValidateKey 归一化并校验 16 字节 SM4 密钥。
  function sm4ValidateKey(key) {
    var bytes = toBytes(key);
    if (bytes.length !== 16) {
      throw guomiError('invalid-key', 'SM4 key must be 16 bytes, got ' + bytes.length, 'SM4 密钥必须是 16 字节，实际为 ' + bytes.length);
    }
    return bytes;
  }

  // sm4EncryptCBC encrypts with CBC mode and PKCS#7 padding, matching guomi.EncryptCBC. | sm4EncryptCBC 使用 CBC 模式与 PKCS#7 填充加密，与 guomi.EncryptCBC 一致。
  function sm4EncryptCBC(key, iv, data) {
    var keyBytes = sm4ValidateKey(key);
    var ivBytes = toBytes(iv);
    if (ivBytes.length !== 16) {
      throw guomiError('invalid-iv', 'SM4-CBC iv must be 16 bytes, got ' + ivBytes.length, 'SM4-CBC 的 iv 必须是 16 字节，实际为 ' + ivBytes.length);
    }
    var roundKeys = sm4KeySchedule(keyBytes);
    var padded = pkcs7Pad(toBytes(data));
    var out = new Uint8Array(padded.length);
    var previous = ivBytes;
    for (var offset = 0; offset < padded.length; offset += 16) {
      var block = new Uint8Array(16);
      for (var i = 0; i < 16; i++) {
        block[i] = padded[offset + i] ^ previous[i];
      }
      block = sm4BlockCrypt(roundKeys, block);
      out.set(block, offset);
      previous = block;
    }
    return out;
  }

  // sm4DecryptCBC decrypts CBC ciphertext and strips the PKCS#7 padding. | sm4DecryptCBC 解密 CBC 密文并去除 PKCS#7 填充。
  function sm4DecryptCBC(key, iv, data) {
    var keyBytes = sm4ValidateKey(key);
    var ivBytes = toBytes(iv);
    if (ivBytes.length !== 16) {
      throw guomiError('invalid-iv', 'SM4-CBC iv must be 16 bytes, got ' + ivBytes.length, 'SM4-CBC 的 iv 必须是 16 字节，实际为 ' + ivBytes.length);
    }
    var ciphertext = toBytes(data);
    if (ciphertext.length === 0 || ciphertext.length % 16 !== 0) {
      throw guomiError('invalid-ciphertext', 'CBC ciphertext length ' + ciphertext.length + ' is not a positive multiple of 16', 'CBC 密文长度 ' + ciphertext.length + ' 不是 16 的正整数倍');
    }
    var roundKeys = sm4ReverseRoundKeys(sm4KeySchedule(keyBytes));
    var out = new Uint8Array(ciphertext.length);
    var previous = ivBytes;
    for (var offset = 0; offset < ciphertext.length; offset += 16) {
      var block = ciphertext.subarray(offset, offset + 16);
      var decrypted = sm4BlockCrypt(roundKeys, block);
      for (var i = 0; i < 16; i++) {
        out[offset + i] = decrypted[i] ^ previous[i];
      }
      previous = block;
    }
    return pkcs7Unpad(out);
  }

  // GCM_R is the GHASH reduction constant x^128 = x^7 + x^2 + x + 1. | GCM_R 是 GHASH 的约简常量 x^128 = x^7 + x^2 + x + 1。
  var GCM_R = 0xe1000000000000000000000000000000n;

  // gcmMul multiplies two field elements in GF(2^128) per NIST SP 800-38D algorithm 1. | gcmMul 按 NIST SP 800-38D 算法 1 在 GF(2^128) 中相乘。
  function gcmMul(left, right) {
    var z = 0n;
    var v = right;
    for (var i = 127; i >= 0; i--) {
      if ((left >> BigInt(i)) & 1n) {
        z ^= v;
      }
      v = (v & 1n) ? ((v >> 1n) ^ GCM_R) : (v >> 1n);
    }
    return z;
  }

  // gcmHashData folds a byte string into the running GHASH value with zero padding. | gcmHashData 把字节串以零填充折叠进 GHASH 累加值。
  function gcmHashData(state, hashKey, data) {
    var value = state;
    for (var offset = 0; offset < data.length; offset += 16) {
      var block = new Uint8Array(16);
      block.set(data.subarray(offset, Math.min(offset + 16, data.length)), 0);
      value = gcmMul(value ^ bytesToBigIntBE(block), hashKey);
    }
    return value;
  }

  // gcmLengthBlock renders the trailing 64-bit big-endian lengths of AAD and ciphertext. | gcmLengthBlock 渲染尾部 64 位大端的 AAD 与密文长度。
  //
  // Both fields are full 64-bit values: a 64-byte ciphertext already exceeds one byte of length. | 两个字段都是完整 64 位：64 字节密文的位长就已超过一个字节的容量。
  function gcmLengthBlock(aadLength, ciphertextLength) {
    var out = new Uint8Array(16);
    out.set(bigIntToBytesBE(BigInt(aadLength) * 8n, 8), 0);
    out.set(bigIntToBytesBE(BigInt(ciphertextLength) * 8n, 8), 8);
    return out;
  }

  // gcmDeriveCounter builds the initial counter block J0 from the nonce. | gcmDeriveCounter 由 nonce 构造初始计数块 J0。
  //
  // For a non-96-bit nonce the counter is GHASH_H(nonce || [0^64 || len(nonce)*8]_64); the nonce and
  // the 16-byte length block are concatenated first and zero-padded afterwards, which is what
  // crypto/cipher does, so the block boundaries match Go for every nonce length. | 随机数非 96 位时，
  // 计数块为 GHASH_H(nonce || [0^64 || len(nonce)*8]_64)；nonce 与 16 字节长度块先拼接、末尾再补零，
  // 与 crypto/cipher 一致，因此任意长度 nonce 的分块边界都与 Go 相同。
  function gcmDeriveCounter(hashKey, nonce) {
    if (nonce.length === 12) {
      return bytesToBigIntBE(concatBytes(nonce, new Uint8Array([0x00, 0x00, 0x00, 0x01])));
    }
    var lengthBlock = new Uint8Array(16);
    lengthBlock.set(bigIntToBytesBE(BigInt(nonce.length) * 8n, 8), 8);
    return gcmHashData(0n, hashKey, concatBytes(nonce, lengthBlock));
  }

  // gcmInc32 increments the low 32 bits of the counter block. | gcmInc32 递增计数块的低 32 位。
  function gcmInc32(counter) {
    var low = counter & 0xffffffffn;
    var high = counter >> 32n;
    return (high << 32n) | ((low + 1n) & 0xffffffffn);
  }

  // gcmKeystream produces length bytes of SM4-CTR keystream starting at inc32(J0). | gcmKeystream 从 inc32(J0) 开始产出 length 字节的 SM4-CTR 密钥流。
  function gcmKeystream(roundKeys, counter, length) {
    var out = new Uint8Array(length);
    var current = gcmInc32(counter);
    for (var offset = 0; offset < length; offset += 16) {
      var mask = sm4BlockCrypt(roundKeys, bigIntToBytesBE(current, 16));
      var take = Math.min(16, length - offset);
      for (var i = 0; i < take; i++) {
        out[offset + i] = mask[i];
      }
      current = gcmInc32(current);
    }
    return out;
  }

  // gcmTag computes the 16-byte authentication tag over aad and ciphertext. | gcmTag 计算 aad 与密文的 16 字节认证标签。
  function gcmTag(roundKeys, hashKey, counter, aad, ciphertext) {
    var state = gcmHashData(0n, hashKey, aad);
    state = gcmHashData(state, hashKey, ciphertext);
    state = gcmMul(state ^ bytesToBigIntBE(gcmLengthBlock(aad.length, ciphertext.length)), hashKey);
    var mask = sm4BlockCrypt(roundKeys, bigIntToBytesBE(counter, 16));
    return Uint8Array.from(bigIntToBytesBE(state, 16)).map(function (byte, index) {
      return byte ^ mask[index];
    });
  }

  // sm4ValidateGCMInputs normalises the key and nonce shared by GCM calls. | sm4ValidateGCMInputs 归一化 GCM 调用共用的密钥与 nonce。
  function sm4ValidateGCMInputs(key, nonce) {
    var keyBytes = sm4ValidateKey(key);
    var nonceBytes = toBytes(nonce);
    if (nonceBytes.length === 0 || nonceBytes.length > 64) {
      throw guomiError('invalid-nonce', 'SM4-GCM nonce must be 1..64 bytes, got ' + nonceBytes.length, 'SM4-GCM 的 nonce 必须为 1..64 字节，实际为 ' + nonceBytes.length);
    }
    return { key: keyBytes, nonce: nonceBytes };
  }

  // sm4EncryptGCM seals plaintext into ciphertext||tag, matching guomi.SealGCM. | sm4EncryptGCM 把明文封装为 密文||标签，与 guomi.SealGCM 一致。
  function sm4EncryptGCM(key, nonce, data, aad) {
    var inputs = sm4ValidateGCMInputs(key, nonce);
    var roundKeys = sm4KeySchedule(inputs.key);
    var hashKey = bytesToBigIntBE(sm4BlockCrypt(roundKeys, new Uint8Array(16)));
    var counter = gcmDeriveCounter(hashKey, inputs.nonce);
    var plaintext = toBytes(data);
    var additional = toBytes(aad === undefined || aad === null ? new Uint8Array(0) : aad);
    var stream = gcmKeystream(roundKeys, counter, plaintext.length);
    var ciphertext = new Uint8Array(plaintext.length);
    for (var i = 0; i < plaintext.length; i++) {
      ciphertext[i] = plaintext[i] ^ stream[i];
    }
    return concatBytes(ciphertext, gcmTag(roundKeys, hashKey, counter, additional, ciphertext));
  }

  // sm4DecryptGCM verifies the tag and opens ciphertext||tag, matching guomi.OpenGCM. | sm4DecryptGCM 校验标签并解开 密文||标签，与 guomi.OpenGCM 一致。
  function sm4DecryptGCM(key, nonce, data, aad) {
    var inputs = sm4ValidateGCMInputs(key, nonce);
    var sealed = toBytes(data);
    if (sealed.length < 16) {
      throw guomiError('invalid-ciphertext', 'GCM input is shorter than the 16-byte tag: ' + sealed.length, 'GCM 输入短于 16 字节标签：' + sealed.length);
    }
    var roundKeys = sm4KeySchedule(inputs.key);
    var hashKey = bytesToBigIntBE(sm4BlockCrypt(roundKeys, new Uint8Array(16)));
    var counter = gcmDeriveCounter(hashKey, inputs.nonce);
    var additional = toBytes(aad === undefined || aad === null ? new Uint8Array(0) : aad);
    var ciphertext = sealed.subarray(0, sealed.length - 16);
    var tag = sealed.subarray(sealed.length - 16);
    if (!bytesEqual(gcmTag(roundKeys, hashKey, counter, additional, ciphertext), tag)) {
      throw guomiError('gcm-auth-failed', 'GCM authentication failed: wrong key, nonce, aad or tampered data', 'GCM 认证失败：密钥、nonce、aad 错误或数据被篡改');
    }
    var stream = gcmKeystream(roundKeys, counter, ciphertext.length);
    var plaintext = new Uint8Array(ciphertext.length);
    for (var i = 0; i < ciphertext.length; i++) {
      plaintext[i] = ciphertext[i] ^ stream[i];
    }
    return plaintext;
  }

  // ---------------------------------------------------------------- public API | 公开 API

  // api is the object exposed as `window.ydGuomi` / `module.exports`. | api 即暴露为 `window.ydGuomi` / `module.exports` 的对象。
  var api = {
    // version is the sandbox version string. | version 是沙箱版本号。
    version: '1.0.0',

    // ready resolves once the module is usable; constants are built eagerly today, so it only
    // exists to keep call sites future-proof and awaitable. | ready 在模块可用时解析；当前常量是立即构建的，该函数仅用于让调用点面向未来且可 await。
    ready: function () {
      return Promise.resolve();
    },

    // sm3 returns the 32-byte GB/T 32905-2016 digest of a Uint8Array or string. | sm3 返回 Uint8Array 或字符串的 32 字节 GB/T 32905-2016 摘要。
    sm3: sm3,

    // sm3Hex returns the lowercase hex SM3 digest of a Uint8Array or string. | sm3Hex 返回 Uint8Array 或字符串的小写十六进制 SM3 摘要。
    sm3Hex: sm3Hex,

    // sm3Stream returns an incremental hasher with update / digest / reset. | sm3Stream 返回带 update / digest / reset 的增量散列器。
    sm3Stream: sm3Stream,

    // sm2 groups the SM2 public key cryptography API. | sm2 汇总 SM2 公钥密码 API。
    sm2: {
      // constants exposes the curve parameters, matching GB/T 32918.5. | constants 暴露曲线参数，与 GB/T 32918.5 一致。
      constants: { p: CURVE.p, a: CURVE.a, b: CURVE.b, n: CURVE.n, gx: CURVE.gx, gy: CURVE.gy },

      // defaultUid is the ZA user identifier shared with guomi and gmsm. | defaultUid 是与 guomi、gmsm 共享的 ZA 用户标识。
      defaultUid: SM2_DEFAULT_UID,

      // generateKeyPair resolves to { publicKey, privateKey } in lowercase hex. | generateKeyPair 解析为小写十六进制的 { publicKey, privateKey }。
      generateKeyPair: sm2GenerateKeyPair,

      // derivePublicKey returns 04||X||Y (130 hex chars) for a 64 hex character private scalar. | derivePublicKey 由 64 字符私钥返回 04||X||Y（130 个十六进制字符）。
      derivePublicKey: derivePublicKey,

      // encrypt returns the raw C1C3C2 ciphertext used by guomi.EncryptRaw. | encrypt 返回 guomi.EncryptRaw 使用的原始 C1C3C2 密文。
      encrypt: sm2Encrypt,

      // decrypt opens the raw layout and auto-detects a 0x04 prefix. | decrypt 解开原始布局并自动识别 0x04 前缀。
      decrypt: sm2Decrypt,

      // sign returns the DER SEQUENCE { r, s } signature. | sign 返回 DER 的 SEQUENCE { r, s } 签名。
      sign: sm2Sign,

      // verify checks a DER signature given as bytes or hex, returning false on any mismatch. | verify 校验字节或十六进制形式的 DER 签名，任何不匹配都返回 false。
      verify: sm2Verify,

      // signHex returns the DER signature as lowercase hex. | signHex 以小写十六进制返回 DER 签名。
      signHex: function (privateKeyHex, data, opts) {
        return bytesToHex(sm2Sign(privateKeyHex, data, opts));
      },

      // verifyHex checks a hex encoded DER signature. | verifyHex 校验十六进制编码的 DER 签名。
      verifyHex: function (publicKeyHex, data, signatureHex) {
        return sm2Verify(publicKeyHex, data, signatureHex);
      },
    },

    // sm4 groups the SM4 block cipher API. | sm4 汇总 SM4 分组密码 API。
    sm4: {
      // encryptCBC returns PKCS#7 padded CBC ciphertext. | encryptCBC 返回带 PKCS#7 填充的 CBC 密文。
      encryptCBC: sm4EncryptCBC,

      // decryptCBC validates the PKCS#7 padding and returns the plaintext. | decryptCBC 校验 PKCS#7 填充并返回明文。
      decryptCBC: sm4DecryptCBC,

      // encryptGCM returns ciphertext||tag with a 16-byte tag. | encryptGCM 返回 密文||16 字节标签。
      encryptGCM: sm4EncryptGCM,

      // decryptGCM verifies the tag before returning any plaintext. | decryptGCM 先校验标签再返回明文。
      decryptGCM: sm4DecryptGCM,
    },

    // util exposes the byte conversion helpers used by the tests and the htmx hook. | util 暴露测试与 htmx 钩子使用的字节转换辅助函数。
    util: {
      // toBytes normalises any supported input into a Uint8Array copy. | toBytes 把任一受支持入参归一化为 Uint8Array 副本。
      toBytes: toBytes,

      // bytesToHex renders bytes as lowercase hex. | bytesToHex 把字节渲染为小写十六进制。
      bytesToHex: bytesToHex,

      // hexToBytes parses hex into bytes. | hexToBytes 把十六进制解析为字节。
      hexToBytes: hexToBytes,

      // bytesToBigIntBE interprets big-endian bytes as a BigInt. | bytesToBigIntBE 把大端字节解释为 BigInt。
      bytesToBigIntBE: bytesToBigIntBE,

      // bigIntToBytesBE renders a BigInt as fixed width big-endian bytes. | bigIntToBytesBE 把 BigInt 渲染为定长大端字节。
      bigIntToBytesBE: bigIntToBytesBE,

      // utf8Encode converts a string into UTF-8 bytes. | utf8Encode 把字符串转为 UTF-8 字节。
      utf8Encode: utf8Encode,

      // utf8Decode converts UTF-8 bytes into a string. | utf8Decode 把 UTF-8 字节转为字符串。
      utf8Decode: utf8Decode,

      // concatBytes joins byte sources into one Uint8Array. | concatBytes 把多个字节来源拼接为一个 Uint8Array。
      concatBytes: concatBytes,

      // bytesEqual compares two byte sequences. | bytesEqual 比较两段字节。
      bytesEqual: bytesEqual,

      // randomBytes returns cryptographically secure random bytes. | randomBytes 返回密码学安全随机字节。
      randomBytes: randomBytes,

      // bytesToBase64 encodes bytes as standard Base64 with padding. | bytesToBase64 以标准 Base64（带填充）编码字节。
      bytesToBase64: bytesToBase64,

      // base64ToBytes decodes standard Base64 into bytes. | base64ToBytes 把标准 Base64 解码为字节。
      base64ToBytes: base64ToBytes,
    },
  };

  return api;
});

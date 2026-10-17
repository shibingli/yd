// Node test suite for the pure-JS guomi sandbox (SM2 / SM3 / SM4). | 纯 JS 国密沙箱（SM2/SM3/SM4）的 Node 测试套件。
// Run: node web/static/js/yd-guomi.test.mjs | 运行：node web/static/js/yd-guomi.test.mjs
//
// This file is also executed by web/guomi_vector_test.go, which injects three paths through the
// environment so that every artefact stays inside the temporary directory that test owns: | 本文件同时由
// web/guomi_vector_test.go 驱动执行，该测试通过环境变量注入三个路径，使全部产物都留在它持有的临时目录内：
//   - YD_GUOMI_VECTORS: the Go vectors this suite consumes; | YD_GUOMI_VECTORS：本套件消费的 Go 向量；
//   - YD_GUOMI_JS_VECTORS: where the full JavaScript vector file is written for Go to decrypt and
//     verify, random-k entries included; | YD_GUOMI_JS_VECTORS：完整 JS 向量文件的写出路径，供 Go 侧解密与验签，含随机 k 条目；
//   - YD_GUOMI_JS_FIXTURE: where the deterministic subset is written, which Go compares byte for
//     byte with the checked-in web/testdata/guomi_js_vectors.json. | YD_GUOMI_JS_FIXTURE：确定性子集的写出路径，
//     Go 会将其与签入的 web/testdata/guomi_js_vectors.json 逐字节比对。
//
// The suite therefore covers both directions of the interoperability contract: | 因此本套件覆盖互操作契约的双向：
//   - JS consumes Go vectors: SM3 digests, SM2 ciphertexts (fixed k included) and signatures, SM4-CBC/GCM. | JS 消费 Go 向量：SM3 摘要、SM2 密文（含固定 k）与签名、SM4-CBC/GCM。
//   - JS writes the vectors for the Go side to decrypt / verify; the entries encrypted with a fresh
//     random k never leave the temporary file, so no run can dirty the repository. | JS 写出向量供 Go 侧解密/验签；
//     使用全新随机 k 加密的条目绝不离开临时文件，因此任何运行都不会弄脏仓库。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import ydGuomi from './yd-guomi.js';
import ydGuomiHtmx from './yd-guomi-htmx.js';

const scriptDir = dirname(fileURLToPath(import.meta.url));

// Official GB/T 32905-2016 vectors. | GB/T 32905-2016 官方向量。
const SM3_ABC = '66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0';
// The GB/T 32905 example message is "abcd" repeated 16 times, that is 64 bytes. | GB/T 32905 的算例消息是 "abcd" 重复 16 次，即 64 字节。
const SM3_ABCD_X64 = 'debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732';
const SM3_EMPTY = '1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b';
// "abcd" repeated 64 times, that is 256 bytes or four blocks; cross-checked with openssl dgst -sm3. | "abcd" 重复 64 次即 256 字节共四块；已用 openssl dgst -sm3 交叉校验。
const SM3_ABCD_X256 = 'b965764c8bebb091c7602b74afd34eefb531dccb4e0076d9b7cd813199b45971';
const ABCD_X64 = 'abcd'.repeat(16);
const ABCD_X256 = 'abcd'.repeat(64);
// GB/T 32918 recommended curve parameters: H = 1*G and N*G = infinity. | GB/T 32918 推荐曲线参数：1*G 与 N*G 为无穷远点。
const GENERATOR_HEX = '04' +
  '32c4ae2c1f1981195f9904466a39c9948fe30bbff2660be1715a4589334c74c7' +
  'bc3736a2f4f6779c59bdcee36b692153d0a9877cc62a474002df32e52139f0a0';
const CURVE_ORDER_HEX = 'fffffffeffffffffffffffffffffffff7203df6b21c6052b53bbf40939d54123';
const CURVE_P_HEX = 'fffffffeffffffffffffffffffffffffffffffff00000000ffffffffffffffff';

// Fixed material reused when the Go vectors are absent (standalone run). | Go 向量缺失（独立运行）时复用的固定材料。
const FALLBACK_PRIVATE_KEY = '3945208f7b2144b13f36e38ac6d39f95889393692860b51a42fb81ef4df7c5b8';
const FALLBACK_SM4_KEY = '0123456789abcdeffedcba9876543210';
const FALLBACK_SM4_IV = '000102030405060708090a0b0c0d0e0f';
const FALLBACK_SM4_NONCE = '000102030405060708090a0b';
const FALLBACK_FIXED_K = 'a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90';

// Locate the checked-in fixture directory of a standalone run. | 定位独立运行时签入夹具所在目录。
function locateVectorDir() {
  const candidates = [
    resolve(scriptDir, '..', '..', 'testdata'),
    resolve(process.cwd(), 'testdata'),
    resolve(process.cwd(), 'web', 'testdata'),
  ];
  for (const dir of candidates) {
    if (existsSync(join(dir, 'guomi_vectors.json'))) return dir;
  }
  return candidates[0];
}

function loadJsonIfExists(path) {
  if (!existsSync(path)) return null;
  return JSON.parse(readFileSync(path, 'utf8'));
}

// Vector paths. The Go test injects all three through the environment; a standalone run reads the
// checked-in fixture and writes into the operating system temporary directory, so neither way
// touches the repository. | 向量路径。Go 测试通过环境变量注入全部三个路径；独立运行时读取签入夹具、
// 写入操作系统临时目录，两种方式都不触碰仓库。
const ENV_GO_VECTORS = 'YD_GUOMI_VECTORS';
const ENV_JS_VECTORS = 'YD_GUOMI_JS_VECTORS';
const ENV_JS_FIXTURE = 'YD_GUOMI_JS_FIXTURE';
const goVectorsPath = process.env[ENV_GO_VECTORS] || join(locateVectorDir(), 'guomi_vectors.json');
const jsVectorsPath = process.env[ENV_JS_VECTORS] || join(tmpdir(), 'yd-guomi-js-vectors.json');
const jsFixturePath = process.env[ENV_JS_FIXTURE] || join(tmpdir(), 'yd-guomi-js-fixture.json');

const util = ydGuomi.util;
const goVectors = loadJsonIfExists(goVectorsPath);
const bytes = (data) => util.toBytes(data);
const hex = (data) => util.bytesToHex(bytes(data));

// The material used for the JS -> Go direction: reuse the Go side keys when available, | JS -> Go 方向使用的材料：Go 侧密钥存在时优先复用，
// so TestJSVectorsAcceptedByGo proves both sides share one key pair. | 从而让 TestJSVectorsAcceptedByGo 证明确实是同一份密钥对。
const privateKeyHex = goVectors ? goVectors.sm2.privateKeyHex : FALLBACK_PRIVATE_KEY;
const sm4KeyHex = goVectors ? goVectors.sm4.keyHex : FALLBACK_SM4_KEY;
const publicKeyHex = ydGuomi.sm2.derivePublicKey(privateKeyHex);

const jsVectors = {
  version: 1,
  generatedBy: 'js',
  note: 'produced by web/static/js/yd-guomi.test.mjs; consumed by web/guomi_vector_test.go | 由 web/static/js/yd-guomi.test.mjs 生成，由 web/guomi_vector_test.go 消费',
  sm3: [],
  sm2: { privateKeyHex, publicKeyHex, encryptions: [], signatures: [] },
  sm4: { keyHex: sm4KeyHex, cbc: [], gcm: [] },
  htmxTransport: [],
};

test('ready() resolves and the exported surface is complete | ready() 可解析且导出面完整', async () => {
  await ydGuomi.ready();
  assert.equal(typeof ydGuomi.sm3, 'function');
  assert.equal(typeof ydGuomi.sm3Hex, 'function');
  assert.equal(typeof ydGuomi.sm3Stream, 'function');
  assert.equal(typeof ydGuomi.sm2.generateKeyPair, 'function');
  assert.equal(typeof ydGuomi.sm2.encrypt, 'function');
  assert.equal(typeof ydGuomi.sm2.decrypt, 'function');
  assert.equal(typeof ydGuomi.sm2.sign, 'function');
  assert.equal(typeof ydGuomi.sm2.verify, 'function');
  assert.equal(typeof ydGuomi.sm2.derivePublicKey, 'function');
  assert.equal(typeof ydGuomi.sm4.encryptCBC, 'function');
  assert.equal(typeof ydGuomi.sm4.decryptCBC, 'function');
  assert.equal(typeof ydGuomi.sm4.encryptGCM, 'function');
  assert.equal(typeof ydGuomi.sm4.decryptGCM, 'function');
  assert.equal(typeof ydGuomi.verifyIntegrity, 'function');
  assert.equal(typeof ydGuomiHtmx.install, 'function');
});

test('SM3 official vectors | SM3 官方向量', () => {
  assert.equal(ydGuomi.sm3Hex('abc'), SM3_ABC);
  assert.equal(ydGuomi.sm3Hex(ABCD_X64), SM3_ABCD_X64);
  assert.equal(ydGuomi.sm3Hex(ABCD_X256), SM3_ABCD_X256);
  assert.equal(ydGuomi.sm3Hex(''), SM3_EMPTY);
  assert.equal(hex(ydGuomi.sm3('abc')), SM3_ABC);
  // Both inputs must produce identical bytes: string and Uint8Array. | 字符串与 Uint8Array 两种入参必须产出完全相同的字节。
  assert.deepEqual(Array.from(ydGuomi.sm3(new TextEncoder().encode('abc'))), Array.from(ydGuomi.sm3('abc')));
  for (const [name, dataHex, digestHex] of [
    ['abc', util.bytesToHex(util.utf8Encode('abc')), SM3_ABC],
    ['empty', '', SM3_EMPTY],
    ['abcd-x16-64B', util.bytesToHex(util.utf8Encode(ABCD_X64)), SM3_ABCD_X64],
    ['abcd-x64-256B', util.bytesToHex(util.utf8Encode(ABCD_X256)), SM3_ABCD_X256],
  ]) {
    jsVectors.sm3.push({ name: `${name} (js)`, dataHex, digestHex });
  }
});

test('SM3 incremental updates match the one-shot digest | SM3 增量更新与一次性摘要一致', () => {
  const message = ABCD_X256;
  const oneShot = hex(ydGuomi.sm3(message));
  for (const chunk of [1, 7, 63, 64, 65, 100]) {
    const stream = ydGuomi.sm3Stream();
    for (let i = 0; i < message.length; i += chunk) stream.update(message.slice(i, i + chunk));
    assert.equal(hex(stream.digest()), oneShot, `chunk=${chunk}`);
  }
  const reused = ydGuomi.sm3Stream();
  reused.update('abc');
  assert.equal(hex(reused.digest()), SM3_ABC);
  reused.reset();
  reused.update('');
  assert.equal(hex(reused.digest()), SM3_EMPTY);
});

test('byte utilities round-trip explicitly | 字节工具显式往返', () => {
  assert.equal(util.bytesToHex(new Uint8Array([0x00, 0x0f, 0xff])), '000fff');
  assert.deepEqual(Array.from(util.hexToBytes('000FFF')), [0x00, 0x0f, 0xff]);
  assert.deepEqual(Array.from(util.hexToBytes('0x000fff')), [0x00, 0x0f, 0xff]);
  assert.equal(util.bytesToHex(util.hexToBytes('00'.repeat(32))), '00'.repeat(32));
  assert.throws(() => util.hexToBytes('abc'), (err) => err.code === 'invalid-hex');
  assert.throws(() => util.hexToBytes('zz'), (err) => err.code === 'invalid-hex');
  // Big-endian BigInt conversion must be left-padded and must reject overflow. | 大端 BigInt 转换必须左补零，并拒绝溢出。
  assert.equal(util.bytesToBigIntBE(new Uint8Array([1, 0])), 256n);
  assert.equal(util.bytesToBigIntBE(new Uint8Array(0)), 0n);
  assert.equal(util.bytesToHex(util.bigIntToBytesBE(256n, 4)), '00000100');
  assert.equal(util.bytesToHex(util.bigIntToBytesBE(0n, 2)), '0000');
  assert.throws(() => util.bigIntToBytesBE(0x10000n, 2), (err) => err.code === 'invalid-length');
  assert.throws(() => util.bigIntToBytesBE(-1n, 2), (err) => err.code === 'invalid-length');
  // UTF-8 must survive CJK, emoji (surrogate pairs) and NUL bytes. | UTF-8 必须能往返中文、emoji（代理对）与 NUL 字节。
  for (const text of ['国密沙箱', 'a\u0000b', '🔐 SM2/SM3/SM4 ✅', '']) {
    assert.equal(util.utf8Decode(util.utf8Encode(text)), text);
  }
  assert.deepEqual(Array.from(util.utf8Encode('国')), [0xe5, 0x9b, 0xbd]);
  assert.equal(util.bytesToHex(util.concatBytes(new Uint8Array([1]), new Uint8Array([2, 3]))), '010203');
  assert.equal(util.bytesToBase64(new Uint8Array([0x00, 0xff, 0x10])), 'AP8Q');
  assert.deepEqual(Array.from(util.base64ToBytes('AP8Q')), [0x00, 0xff, 0x10]);
});

test('SM2 curve constants agree with GB/T 32918 | SM2 曲线常量与 GB/T 32918 一致', () => {
  // d = 1 must yield the published generator point, which pins the curve parameters. | d = 1 必须得到公开的基点，从而锁定曲线参数。
  assert.equal(ydGuomi.sm2.derivePublicKey('0'.repeat(63) + '1'), GENERATOR_HEX);
  // An 0x prefix and mixed case are tolerated and must not change the result. | 容忍 0x 前缀与大小写混用，且不得改变结果。
  assert.equal(ydGuomi.sm2.derivePublicKey('0X' + '0'.repeat(62) + '01'), GENERATOR_HEX);
  assert.equal(util.bytesToHex(util.bigIntToBytesBE(ydGuomi.sm2.constants.p, 32)), CURVE_P_HEX);
  assert.equal(util.bytesToHex(util.bigIntToBytesBE(ydGuomi.sm2.constants.n, 32)), CURVE_ORDER_HEX);
  // Scalars that are too short, too long or outside [1, n-1] must be rejected. | 过短、过长或超出 [1, n-1] 的标量必须被拒绝。
  assert.throws(() => ydGuomi.sm2.derivePublicKey('01'), (err) => err.code === 'invalid-key');
  assert.throws(() => ydGuomi.sm2.derivePublicKey('0'.repeat(65) + '1'), (err) => err.code === 'invalid-key');
  assert.throws(() => ydGuomi.sm2.derivePublicKey('00'.repeat(32)), (err) => err.code === 'invalid-key');
  assert.throws(() => ydGuomi.sm2.derivePublicKey(CURVE_ORDER_HEX), (err) => err.code === 'invalid-key');
  assert.throws(() => ydGuomi.sm2.derivePublicKey('zz'.repeat(32)), (err) => err.code === 'invalid-hex');
});

test('SM2 key generation, signing and encryption round-trip | SM2 密钥生成、签名与加解密往返', async () => {
  const pair = await ydGuomi.sm2.generateKeyPair();
  assert.equal(pair.publicKey.length, 130);
  assert.equal(pair.publicKey.slice(0, 2), '04');
  assert.equal(pair.privateKey.length, 64);
  assert.equal(ydGuomi.sm2.derivePublicKey(pair.privateKey), pair.publicKey);

  const message = bytes('前端登录凭证 | login credential');
  const signature = ydGuomi.sm2.sign(pair.privateKey, message);
  assert.equal(ydGuomi.sm2.verify(pair.publicKey, message, signature), true);
  assert.equal(ydGuomi.sm2.verify(pair.publicKey, bytes('tampered'), signature), false);
  assert.equal(ydGuomi.sm2.verify(pair.publicKey, message, signature.slice(0, signature.length - 3)), false);

  const ciphertext = ydGuomi.sm2.encrypt(pair.publicKey, message);
  assert.equal(ciphertext.length, message.length + 96);
  assert.deepEqual(Array.from(ydGuomi.sm2.decrypt(pair.privateKey, ciphertext)), Array.from(message));

  // Empty plaintext round-trips into a 96-byte ciphertext. | 空明文往返后得到 96 字节密文。
  const empty = ydGuomi.sm2.encrypt(pair.publicKey, new Uint8Array(0));
  assert.equal(empty.length, 96);
  assert.equal(ydGuomi.sm2.decrypt(pair.privateKey, empty).length, 0);

  // A wrong key must fail with the documented error code. | 错误密钥必须以文档化的错误码失败。
  const other = await ydGuomi.sm2.generateKeyPair();
  assert.throws(() => ydGuomi.sm2.decrypt(other.privateKey, ciphertext), (err) => err.code === 'decrypt-failed');
  assert.throws(() => ydGuomi.sm2.decrypt(pair.privateKey, ciphertext.slice(0, 95)), (err) => err.code === 'invalid-ciphertext');
});

test('SM3 vectors produced by Go match the JS implementation | Go 生成的 SM3 向量与 JS 实现一致', (t) => {
  if (!goVectors) return t.skip(`Go vectors missing at ${goVectorsPath}; run go test ./web/ first | 缺少 ${goVectorsPath} 处的 Go 向量，请先运行 go test ./web/`);
  assert.ok(goVectors.sm3.length >= 4);
  for (const vector of goVectors.sm3) {
    const data = util.hexToBytes(vector.dataHex);
    assert.equal(hex(ydGuomi.sm3(data)), vector.digestHex, `${vector.name} (bytes)`);
    if (typeof vector.dataUtf8 === 'string') {
      assert.equal(ydGuomi.sm3Hex(vector.dataUtf8), vector.digestHex, `${vector.name} (utf8)`);
    }
    jsVectors.sm3.push({ name: vector.name, dataHex: vector.dataHex, digestHex: vector.digestHex });
  }
});

test('SM2: JS decrypts Go ciphertexts and reproduces the fixed-k ciphertext | SM2：JS 解 Go 密文并复现固定 k 密文', (t) => {
  if (!goVectors) return t.skip(`Go vectors missing at ${goVectorsPath} | 缺少 ${goVectorsPath} 处的 Go 向量`);
  const { sm2 } = goVectors;
  // The Go private key must derive the very same public key point in JS. | Go 私钥在 JS 侧必须推导出完全相同的公钥点。
  assert.equal(ydGuomi.sm2.derivePublicKey(sm2.privateKeyHex), sm2.publicKeyHex);
  assert.ok(sm2.encryptions.length >= 4);

  for (const vector of sm2.encryptions) {
    const ciphertext = util.hexToBytes(vector.cipherHex);
    const plaintext = ydGuomi.sm2.decrypt(sm2.privateKeyHex, ciphertext);
    assert.equal(util.bytesToHex(plaintext), vector.plainHex, `${vector.name} decrypt`);
    if (vector.fixedKHex) {
      // Byte-level proof: C1 || C3 || C2 and the KDF must match Go exactly. | 字节级证据：C1 || C3 || C2 与 KDF 必须与 Go 完全一致。
      const reproduced = ydGuomi.sm2.encrypt(sm2.publicKeyHex, util.hexToBytes(vector.plainHex), { k: vector.fixedKHex });
      assert.equal(util.bytesToHex(reproduced), vector.cipherHex, `${vector.name} fixed-k`);
      // The reproduced bytes are deterministic, so they belong in the checked-in fixture that Go
      // decrypts: a fixed-k entry recorded here can never drift. | 复现出的字节是确定性的，
      // 因此写入由 Go 解密的签入夹具：此处记录的固定 k 条目绝不会漂移。
      jsVectors.sm2.encryptions.push({
        name: `${vector.name} (js fixed-k)`,
        plainHex: vector.plainHex,
        cipherHex: util.bytesToHex(reproduced),
        fixedKHex: vector.fixedKHex,
      });
    }
  }

  // A ciphertext read straight out of the vector JSON as a hex dump must decrypt as bytes, not as text. | 直接从向量 JSON 以十六进制转储读入的密文必须按字节解密，而不是按文本。
  for (const vector of sm2.encryptions) {
    assert.equal(util.bytesToHex(ydGuomi.sm2.decrypt(sm2.privateKeyHex, vector.cipherHex)), vector.plainHex, `${vector.name} hex-string decrypt`);
  }
  // Below the 96-byte header a pure-hex string is never a ciphertext dump, so the text path stays in force. | 不足 96 字节头时纯十六进制字符串不可能是密文转储，因此文本路径保持生效。
  assert.throws(() => ydGuomi.sm2.decrypt(sm2.privateKeyHex, 'abcd'.repeat(10)), (err) => err.code === 'invalid-ciphertext');

  assert.ok(sm2.signatures.length >= 1);

  for (const vector of sm2.signatures) {
    const data = util.hexToBytes(vector.dataHex);
    assert.equal(ydGuomi.sm2.verify(sm2.publicKeyHex, data, vector.signatureHex), true, `${vector.name} verify`);
    assert.equal(ydGuomi.sm2.verify(sm2.publicKeyHex, util.utf8Encode('tampered | 篡改'), vector.signatureHex), false, `${vector.name} negative`);
    if (vector.fixedKHex) {
      // Byte-level proof: ZA, e = SM3(ZA||data), r, s and the DER encoding must all match Go. | 字节级证据：ZA、e = SM3(ZA||data)、r、s 与 DER 编码必须与 Go 完全一致。
      const reproduced = util.bytesToHex(ydGuomi.sm2.sign(sm2.privateKeyHex, data, { k: vector.fixedKHex }));
      assert.equal(reproduced, vector.signatureHex, `${vector.name} fixed-k`);
      // Recorded for the checked-in fixture: Go verifies this DER signature with its own primitives. | 记入签入夹具：Go 会用自己的原语验签这条 DER 签名。
      jsVectors.sm2.signatures.push({
        name: `${vector.name} (js fixed-k)`,
        dataHex: vector.dataHex,
        signatureHex: reproduced,
        fixedKHex: vector.fixedKHex,
      });
    }
  }
  const fixedKCount = sm2.encryptions.filter((vector) => vector.fixedKHex).length;
  t.diagnostic(`decrypted ${sm2.encryptions.length} Go ciphertext(s), ${fixedKCount} of them fixed-k, and verified ${sm2.signatures.length} Go signature(s) | 解密 ${sm2.encryptions.length} 条 Go 密文（其中 ${fixedKCount} 条为固定 k），并验签 ${sm2.signatures.length} 条 Go 签名`);
});

test('SM2: JS signs with the Go key for the Go side to verify | SM2：JS 用 Go 密钥签名供 Go 侧验签', () => {
  // JS re-encrypts with a fresh random k and hands the result to Go. | JS 用全新随机 k 重新加密，把结果交给 Go。
  for (const plainHex of ['', util.bytesToHex(util.utf8Encode('sandbox login | 沙箱登录')), 'ff00ff00a5a5']) {
    const ciphertext = ydGuomi.sm2.encrypt(publicKeyHex, util.hexToBytes(plainHex));
    assert.equal(util.bytesToHex(ydGuomi.sm2.decrypt(privateKeyHex, ciphertext)), plainHex);
    jsVectors.sm2.encryptions.push({
      name: `js-random-k ${plainHex.length / 2}B`,
      plainHex,
      cipherHex: util.bytesToHex(ciphertext),
    });
  }

  for (const data of [util.utf8Encode('yd login | 登录'), new Uint8Array(0), new Uint8Array([0xff, 0x00, 0xff, 0x00])]) {
    const signature = ydGuomi.sm2.sign(privateKeyHex, data);
    assert.equal(ydGuomi.sm2.verify(publicKeyHex, data, signature), true);
    assert.equal(ydGuomi.sm2.verifyHex(publicKeyHex, data, util.bytesToHex(signature)), true);
    jsVectors.sm2.signatures.push({
      name: `js ${data.length}B`,
      dataHex: util.bytesToHex(data),
      signatureHex: util.bytesToHex(signature),
    });
  }
  // The DER structure must be SEQUENCE { INTEGER r, INTEGER s }. | DER 结构必须是 SEQUENCE { INTEGER r, INTEGER s }。
  const der = ydGuomi.sm2.sign(privateKeyHex, bytes('der'));
  assert.equal(der[0], 0x30);
  assert.equal(der[1], der.length - 2);
  assert.equal(der[2], 0x02);
  // A non-hex signature string must return false, never throw. | 非法的十六进制签名字符串返回 false，绝不抛错。
  assert.equal(ydGuomi.sm2.verify(publicKeyHex, bytes('der'), 'not-hex!'), false);
});

test('SM4-CBC matches the Go vectors | SM4-CBC 与 Go 向量一致', (t) => {
  const key = util.hexToBytes(sm4KeyHex);
  const iv = util.hexToBytes(FALLBACK_SM4_IV);
  if (goVectors) {
    assert.equal(goVectors.sm4.keyHex, sm4KeyHex);
    for (const vector of goVectors.sm4.cbc) {
      const plaintext = util.hexToBytes(vector.plainHex);
      const ciphertext = ydGuomi.sm4.encryptCBC(key, util.hexToBytes(vector.ivHex), plaintext);
      assert.equal(util.bytesToHex(ciphertext), vector.cipherHex, `${vector.name} encrypt`);
      assert.equal(util.bytesToHex(ydGuomi.sm4.decryptCBC(key, util.hexToBytes(vector.ivHex), ciphertext)), vector.plainHex, `${vector.name} decrypt`);
      jsVectors.sm4.cbc.push({ name: `${vector.name} (js)`, ivHex: vector.ivHex, plainHex: vector.plainHex, cipherHex: util.bytesToHex(ciphertext) });
    }
  } else {
    t.diagnostic('Go vectors missing; only self round-trip is checked | 缺少 Go 向量，仅做自身往返校验');
  }
  // JS-produced ciphertext for the Go side: one full block, one empty, one odd length. | 交给 Go 的 JS 密文：一个整块、一个空明文、一个非对齐长度。
  for (const plainHex of ['', '00', '0123456789abcdeffedcba9876543210', util.bytesToHex(util.utf8Encode('国密沙箱'))]) {
    const ciphertext = ydGuomi.sm4.encryptCBC(key, iv, util.hexToBytes(plainHex));
    assert.equal(ciphertext.length % 16, 0);
    assert.ok(ciphertext.length > util.hexToBytes(plainHex).length);
    jsVectors.sm4.cbc.push({ name: `js ${plainHex.length / 2}B`, ivHex: FALLBACK_SM4_IV, plainHex, cipherHex: util.bytesToHex(ciphertext) });
  }
  // Tampered padding or a wrong key must be rejected. | 填充被破坏或密钥错误必须被拒绝。
  const sealed = ydGuomi.sm4.encryptCBC(key, iv, bytes('padding | 填充'));
  sealed[sealed.length - 1] ^= 0xff;
  assert.throws(() => ydGuomi.sm4.decryptCBC(key, iv, sealed), (err) => err.code === 'invalid-padding');
  assert.throws(() => ydGuomi.sm4.decryptCBC(key, iv, new Uint8Array(15)), (err) => err.code === 'invalid-ciphertext');
  assert.throws(() => ydGuomi.sm4.decryptCBC(key, iv, new Uint8Array(0)), (err) => err.code === 'invalid-ciphertext');
  assert.throws(() => ydGuomi.sm4.encryptCBC(new Uint8Array(15), iv, bytes('x')), (err) => err.code === 'invalid-key');
  assert.throws(() => ydGuomi.sm4.encryptCBC(key, new Uint8Array(15), bytes('x')), (err) => err.code === 'invalid-iv');
});

test('SM4-GCM matches the Go vectors | SM4-GCM 与 Go 向量一致', (t) => {
  const key = util.hexToBytes(sm4KeyHex);
  if (goVectors) {
    for (const vector of goVectors.sm4.gcm) {
      const plaintext = util.hexToBytes(vector.plainHex);
      const nonce = util.hexToBytes(vector.nonceHex);
      const aad = util.hexToBytes(vector.aadHex);
      const sealed = ydGuomi.sm4.encryptGCM(key, nonce, plaintext, aad);
      assert.equal(util.bytesToHex(sealed), vector.cipherHex, `${vector.name} encrypt`);
      assert.equal(util.bytesToHex(ydGuomi.sm4.decryptGCM(key, nonce, sealed, aad)), vector.plainHex, `${vector.name} decrypt`);
      jsVectors.sm4.gcm.push({ name: `${vector.name} (js)`, nonceHex: vector.nonceHex, aadHex: vector.aadHex, plainHex: vector.plainHex, cipherHex: util.bytesToHex(sealed) });
    }
  } else {
    t.diagnostic('Go vectors missing; only self round-trip is checked | 缺少 Go 向量，仅做自身往返校验');
  }
  const nonce12 = util.hexToBytes(FALLBACK_SM4_NONCE);
  for (const plainHex of ['', '00ff', 'a5'.repeat(37)]) {
    const sealed = ydGuomi.sm4.encryptGCM(key, nonce12, util.hexToBytes(plainHex), new Uint8Array(0));
    assert.equal(sealed.length, util.hexToBytes(plainHex).length + 16);
    assert.equal(util.bytesToHex(ydGuomi.sm4.decryptGCM(key, nonce12, sealed, new Uint8Array(0))), plainHex);
    jsVectors.sm4.gcm.push({ name: `js ${plainHex.length / 2}B`, nonceHex: FALLBACK_SM4_NONCE, aadHex: '', plainHex, cipherHex: util.bytesToHex(sealed) });
  }
  // A 16-byte nonce exercises the GHASH-derived initial counter block. | 16 字节 nonce 走 GHASH 派生初始计数块的分支。
  const nonce16 = util.hexToBytes(FALLBACK_SM4_IV);
  const sealed16 = ydGuomi.sm4.encryptGCM(key, nonce16, bytes('non-96-bit nonce | 非 96 位随机数'), bytes('aad'));
  assert.equal(util.utf8Decode(ydGuomi.sm4.decryptGCM(key, nonce16, sealed16, bytes('aad'))), 'non-96-bit nonce | 非 96 位随机数');
  jsVectors.sm4.gcm.push({ name: 'js 16-byte-nonce', nonceHex: FALLBACK_SM4_IV, aadHex: util.bytesToHex(util.utf8Encode('aad')), plainHex: util.bytesToHex(util.utf8Encode('non-96-bit nonce | 非 96 位随机数')), cipherHex: util.bytesToHex(sealed16) });
  // Authentication failures must throw instead of returning data. | 认证失败必须抛错，而不是返回数据。
  const sealed = ydGuomi.sm4.encryptGCM(key, nonce12, bytes('authenticated | 认证'), bytes('aad'));
  sealed[sealed.length - 1] ^= 0x01;
  assert.throws(() => ydGuomi.sm4.decryptGCM(key, nonce12, sealed, bytes('aad')), (err) => err.code === 'gcm-auth-failed');
  const good = ydGuomi.sm4.encryptGCM(key, nonce12, bytes('authenticated | 认证'), bytes('aad'));
  assert.throws(() => ydGuomi.sm4.decryptGCM(key, nonce12, good, bytes('other')), (err) => err.code === 'gcm-auth-failed');
  assert.throws(() => ydGuomi.sm4.decryptGCM(key, nonce12, new Uint8Array(8), new Uint8Array(0)), (err) => err.code === 'invalid-ciphertext');
  assert.throws(() => ydGuomi.sm4.encryptGCM(key, new Uint8Array(0), bytes('x'), new Uint8Array(0)), (err) => err.code === 'invalid-nonce');
});

test('SM2 deterministic vector with a fixed k equals the Go layout | 固定 k 的 SM2 确定性向量与 Go 布局一致', () => {
  const plaintext = bytes('fixed-k | 固定随机数');
  const first = ydGuomi.sm2.encrypt(publicKeyHex, plaintext, { k: FALLBACK_FIXED_K });
  const second = ydGuomi.sm2.encrypt(publicKeyHex, plaintext, { k: FALLBACK_FIXED_K });
  assert.deepEqual(Array.from(first), Array.from(second));
  // Layout: C1(64, no 0x04) || C3(32) || C2(len). | 布局：C1(64，无 0x04) || C3(32) || C2(明文长度)。
  assert.equal(first.length, plaintext.length + 96);
  const withPrefix = ydGuomi.sm2.encrypt(publicKeyHex, plaintext, { k: FALLBACK_FIXED_K, c1Prefix: true });
  assert.equal(withPrefix.length, first.length + 1);
  assert.equal(withPrefix[0], 0x04);
  assert.deepEqual(Array.from(withPrefix.slice(1)), Array.from(first));
  // The 0x04-prefixed variant is auto-detected on decrypt. | 带 0x04 前缀的变体在解密时会被自动识别。
  assert.deepEqual(Array.from(ydGuomi.sm2.decrypt(privateKeyHex, withPrefix)), Array.from(plaintext));
  // The public key is accepted with or without the 0x04 marker. | 公钥接受带或不带 0x04 标识两种写法。
  assert.deepEqual(Array.from(ydGuomi.sm2.encrypt(publicKeyHex.slice(2), plaintext, { k: FALLBACK_FIXED_K })), Array.from(first));
});

test('SM2 tampering is rejected | SM2 篡改被拒绝', () => {
  const ciphertext = ydGuomi.sm2.encrypt(publicKeyHex, bytes('integrity | 完整性'));
  const tampered = Uint8Array.from(ciphertext);
  tampered[97] ^= 0x01;
  // Pin the raw layout: auto-detection retries a 0x04-prefixed parse when C1 starts with 0x04 and
  // reports a different error code, which is unrelated to the tampering under test. | 固定原始布局：
  // 当 C1 以 0x04 开头时自动识别会重试带前缀的解析并报出另一种错误码，这与被测的篡改无关。
  assert.throws(() => ydGuomi.sm2.decrypt(privateKeyHex, tampered, { c1Prefix: false }), (err) => err.code === 'decrypt-failed');
  const offCurve = Uint8Array.from(ciphertext);
  offCurve[0] = 0x00;
  offCurve[1] = 0x01;
  assert.throws(() => ydGuomi.sm2.decrypt(privateKeyHex, offCurve), (err) => err.code === 'invalid-ciphertext');
});

test('htmx hook encrypts sensitive fields and flags the request | htmx 钩子加密敏感字段并标记请求', async () => {
  const url = '/api/guomi/pubkey';
  ydGuomiHtmx.setPublicKey(url, publicKeyHex);
  await ydGuomiHtmx.publicKeyFor(url);

  const fields = [
    { name: 'username', value: 'alice', sensitive: false },
    { name: 'password', value: 'p@ssw0rd | 口令', sensitive: true },
    { name: 'captcha', value: '', sensitive: true },
  ];
  const form = {
    getAttribute: (key) => ({ 'data-yd-encrypt': 'sm2', 'data-yd-pubkey': url }[key] ?? null),
    closest: () => form,
    querySelectorAll: () => fields
      .filter((field) => field.sensitive)
      .map((field) => ({
        name: field.name,
        value: field.value,
        getAttribute: () => '',
      })),
  };

  const body = new FormData();
  for (const field of fields) body.set(field.name, field.value);
  const ctx = { request: { method: 'POST', action: '/login', headers: {}, body, form, sourceElement: form } };

  // Minimal document stub so the hook can register its listener outside a browser. | 极简 document 桩，使钩子在浏览器之外也能注册监听器。
  const listeners = new Map();
  globalThis.document = {
    addEventListener: (name, fn) => {
      if (!listeners.has(name)) listeners.set(name, []);
      listeners.get(name).push(fn);
    },
    removeEventListener: () => {},
    querySelectorAll: () => [],
  };
  ydGuomiHtmx.install();
  assert.ok(listeners.has('htmx:config:request'));

  for (const listener of listeners.get('htmx:config:request')) listener({ detail: { ctx }, preventDefault() {}, stopPropagation() {} });

  assert.equal(ctx.request.headers['X-YD-Encrypted'], 'sm2');
  assert.equal(body.get('username'), 'alice');
  const encrypted = body.get('password');
  assert.ok(encrypted && encrypted !== 'p@ssw0rd | 口令');
  // The wire format is standard Base64 over the raw C1C3C2 ciphertext. | 传输格式为原始 C1C3C2 密文的**标准 Base64**。
  const plaintext = ydGuomi.sm2.decrypt(privateKeyHex, util.base64ToBytes(encrypted));
  assert.equal(util.utf8Decode(plaintext), 'p@ssw0rd | 口令');
  // Empty sensitive values are left untouched. | 空的敏感字段保持原样。
  assert.equal(body.get('captcha'), '');
  jsVectors.htmxTransport.push({
    name: 'js login password | JS 登录口令',
    fieldBase64: encrypted,
    plainHex: util.bytesToHex(plaintext),
  });
});

test('verifyIntegrity compares the SM3 digest of a fetched asset | verifyIntegrity 比对取出资产的 SM3 摘要', async () => {
  const payload = new TextEncoder().encode('yd static asset | 静态资产');
  const expected = util.bytesToHex(ydGuomi.sm3(payload));
  globalThis.fetch = async () => ({ ok: true, status: 200, arrayBuffer: async () => payload.buffer.slice(0) });
  assert.equal(await ydGuomi.verifyIntegrity('/static/js/yd-guomi.js', expected), true);
  assert.equal(await ydGuomi.verifyIntegrity('/static/js/yd-guomi.js', SM3_ABCD_X64), false);
  // Invalid hex throws synchronously: no network round trip for a malformed expectation. | 非法十六进制同步抛错：期望值格式错误时不发起网络请求。
  assert.throws(() => ydGuomi.verifyIntegrity('/x', 'not-hex'), (err) => err.code === 'invalid-hex');
  // A non-OK response is a hard error, never a silent "true". | 响应非 OK 一律硬错误，绝不静默返回 true。
  globalThis.fetch = async () => ({ ok: false, status: 404, arrayBuffer: async () => new ArrayBuffer(0) });
  await assert.rejects(() => ydGuomi.verifyIntegrity('/missing.js', expected), (err) => err.code === 'fetch-failed');
});

test('JS vectors are exported for the Go side | 导出 JS 向量供 Go 侧消费', () => {
  assert.ok(jsVectors.sm3.length >= 3);
  assert.ok(jsVectors.sm4.cbc.length >= 4);
  assert.ok(jsVectors.sm4.gcm.length >= 3);
  // The random-k coverage is asserted on its own, so fixed-k entries cannot mask a lost case. | 随机 k 覆盖单独断言，固定 k 条目无法掩盖用例的丢失。
  const randomEncryptions = jsVectors.sm2.encryptions.filter((vector) => !vector.fixedKHex);
  const randomSignatures = jsVectors.sm2.signatures.filter((vector) => !vector.fixedKHex);
  assert.ok(randomEncryptions.length >= 3, `random-k encryptions: ${randomEncryptions.length}`);
  assert.ok(randomSignatures.length >= 3, `random-k signatures: ${randomSignatures.length}`);
  assert.ok(jsVectors.htmxTransport.length >= 1);

  // The deterministic subset: the checked-in fixture must never carry an entry whose randomiser
  // came from crypto, because such an entry differs on every run. | 确定性子集：签入夹具绝不能带有随机数
  // 来自 crypto 的条目，因为此类条目每次运行都不同。
  const fixture = {
    version: jsVectors.version,
    generatedBy: jsVectors.generatedBy,
    note: jsVectors.note,
    sm3: jsVectors.sm3,
    sm2: {
      privateKeyHex: jsVectors.sm2.privateKeyHex,
      publicKeyHex: jsVectors.sm2.publicKeyHex,
      encryptions: jsVectors.sm2.encryptions.filter((vector) => vector.fixedKHex),
      signatures: jsVectors.sm2.signatures.filter((vector) => vector.fixedKHex),
    },
    sm4: jsVectors.sm4,
    htmxTransport: [],
  };
  assert.ok(fixture.sm2.encryptions.length >= 4, `fixed-k encryptions: ${fixture.sm2.encryptions.length}`);
  assert.ok(fixture.sm2.signatures.length >= 1, `fixed-k signatures: ${fixture.sm2.signatures.length}`);

  for (const [path, payload] of [[jsVectorsPath, jsVectors], [jsFixturePath, fixture]]) {
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, `${JSON.stringify(payload, null, 2)}\n`, 'utf8');
    assert.ok(existsSync(path), `${path} was written`);
  }
});

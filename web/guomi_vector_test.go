// Package web tests the browser assets shipped by the framework. | Package web 测试框架提供的前端资产。
//
// This file owns the SM2/SM3/SM4 interoperability contract between the Go package
// github.com/yun-dev/yd/guomi and the dependency-free JavaScript sandbox in
// web/static/js/yd-guomi.js: | 本文件承载 Go 包 github.com/yun-dev/yd/guomi 与无依赖 JS 沙箱
// web/static/js/yd-guomi.js 之间的 SM2/SM3/SM4 互操作契约：
//
//   - TestGuomiJSVectors writes testdata/guomi_vectors.json with a fixed private key and a
//     fixed SM2 randomiser, then runs the Node suite that consumes it; | TestGuomiJSVectors 用固定
//     私钥与固定随机数写出 testdata/guomi_vectors.json，随后运行消费该文件的 Node 套件；
//   - TestJSVectorsAcceptedByGo reads testdata/guomi_js_vectors.json, produced by that Node
//     suite, and verifies every JavaScript artefact with the Go primitives. | TestJSVectorsAcceptedByGo
//     读取该 Node 套件产出的 testdata/guomi_js_vectors.json，用 Go 原语校验全部 JS 产物。
//
// Node is optional: both tests skip with an explicit reason when it is absent. | Node 是可选的：
// 不存在时两个测试都会带明确原因跳过。
package web

import (
	"encoding/asn1"
	"encoding/hex"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tjfoc/gmsm/sm2"
	"github.com/yun-dev/yd/b64x"
	"github.com/yun-dev/yd/errx"
	"github.com/yun-dev/yd/guomi"
	"github.com/yun-dev/yd/jsonx"
)

var (
	// errNodeScriptMissing reports a missing JavaScript test entry point. | errNodeScriptMissing 表示缺少 JS 测试入口。
	errNodeScriptMissing = errx.New("web: the javascript guomi test script is missing | web：缺少 JS 国密测试脚本")

	// errNodeRunFailed reports a failing Node run. | errNodeRunFailed 表示 Node 运行失败。
	errNodeRunFailed = errx.New("web: the javascript guomi test suite failed | web：JS 国密测试套件执行失败")

	// errVectorIO reports a failed vector file read or write. | errVectorIO 表示向量文件读写失败。
	errVectorIO = errx.New("web: guomi vector file i/o failed | web：国密向量文件读写失败")

	// errVectorDecode reports a malformed vector file. | errVectorDecode 表示向量文件格式非法。
	errVectorDecode = errx.New("web: guomi vector file is not valid JSON | web：国密向量文件不是合法 JSON")

	// errVectorMismatch reports a Go/JS divergence detected while verifying JS vectors. | errVectorMismatch 表示校验 JS 向量时发现两侧不一致。
	errVectorMismatch = errx.New("web: javascript vector rejected by the go implementation | web：JS 向量未被 Go 实现接受")
)

// Fixed material for the cross-language vectors. | 跨语言向量使用的固定材料。
//
// The private key is the GB/T 32918.5-2017 worked example, so the derived public key is
// checkable against the published values and not only against this repository. | 私钥取自
// GB/T 32918.5-2017 的算例，因此推导出的公钥可对照公开数值校验，而不仅是对照本仓库。
const (
	// vectorPrivateKeyHex is the fixed SM2 private scalar D. | vectorPrivateKeyHex 是固定的 SM2 私钥标量 D。
	vectorPrivateKeyHex = "3945208f7b2144b13f36e38ac6d39f95889393692860b51a42fb81ef4df7c5b8"

	// vectorPublicXHex is the published X coordinate of the example public key. | vectorPublicXHex 是算例公钥 X 坐标的公开值。
	vectorPublicXHex = "09f9df311e5421a150dd7d161e4bc5c672179fad1833fc076bb08ff356f35020"

	// vectorPublicYHex is the published Y coordinate of the example public key. | vectorPublicYHex 是算例公钥 Y 坐标的公开值。
	vectorPublicYHex = "ccea490ce26775a52dc6ea718cc1aa600aed05fbf35e084a6632f6072da9ad13"

	// vectorFixedKHex is the fixed SM2 randomiser, which makes C1 and C3 deterministic. | vectorFixedKHex 是固定的 SM2 随机数，使 C1 与 C3 可复现。
	vectorFixedKHex = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

	// vectorSM4KeyHex is the GB/T 32907-2016 sample key, reused for every SM4 vector. | vectorSM4KeyHex 是 GB/T 32907-2016 的示例密钥，用于全部 SM4 向量。
	vectorSM4KeyHex = "0123456789abcdeffedcba9876543210"

	// vectorSM4IVHex is the fixed SM4-CBC initialisation vector. | vectorSM4IVHex 是固定的 SM4-CBC 初始向量。
	vectorSM4IVHex = "000102030405060708090a0b0c0d0e0f"

	// vectorSM4NonceHex is the recommended 12-byte SM4-GCM nonce. | vectorSM4NonceHex 是推荐的 12 字节 SM4-GCM 随机数。
	vectorSM4NonceHex = "000102030405060708090a0b"

	// ciphertextLayout describes the raw SM2 layout shared with JavaScript. | ciphertextLayout 描述与 JS 共享的 SM2 原始布局。
	ciphertextLayout = "C1=x1||y1 64B(no 0x04) | C3=SM3(x2||msg||y2) 32B | C2=msg xor KDF(x2||y2) len(msg) | C1=x1||y1 共 64 字节（无 0x04） | C3=SM3(x2||msg||y2) 32 字节 | C2=明文异或 KDF(x2||y2)，长度与明文一致"

	// signatureAlgorithm documents the SM2 signature construction shared with JavaScript. | signatureAlgorithm 描述与 JS 共享的 SM2 签名构造。
	signatureAlgorithm = "SM3(ZA||data) with uid 1234567812345678, DER SEQUENCE{INTEGER r, INTEGER s} | SM3(ZA||data)，用户标识 1234567812345678，DER SEQUENCE{INTEGER r, INTEGER s}"

	// goVectorFileName is the vector file consumed by the Node suite. | goVectorFileName 是 Node 套件消费的向量文件。
	goVectorFileName = "guomi_vectors.json"

	// jsVectorFileName is the vector file produced by the Node suite. | jsVectorFileName 是 Node 套件产出的向量文件。
	jsVectorFileName = "guomi_js_vectors.json"

	// jsTestScriptName is the Node test entry point relative to the repository root. | jsTestScriptName 是相对仓库根目录的 Node 测试入口。
	jsTestScriptName = "web/static/js/yd-guomi.test.mjs"
)

// sm3Vector is one SM3 digest vector. | sm3Vector 是一条 SM3 摘要向量。
type sm3Vector struct {
	// Name identifies the vector in failure messages. | Name 在失败信息中标识该向量。
	Name string `json:"name"`

	// DataHex is the message in lowercase hex. | DataHex 是消息的小写十六进制。
	DataHex string `json:"dataHex"`

	// DataUtf8 is the message as text when it is valid UTF-8, for the string API. | DataUtf8 是消息为合法 UTF-8 时的文本形式，供字符串 API 使用。
	DataUtf8 string `json:"dataUtf8,omitempty"`

	// DigestHex is the expected lowercase SM3 digest. | DigestHex 是期望的小写 SM3 摘要。
	DigestHex string `json:"digestHex"`
}

// sm2EncryptionVector is one raw C1C3C2 ciphertext vector. | sm2EncryptionVector 是一条原始 C1C3C2 密文向量。
type sm2EncryptionVector struct {
	// Name identifies the vector. | Name 标识该向量。
	Name string `json:"name"`

	// PlainHex is the plaintext in lowercase hex. | PlainHex 是小写十六进制明文。
	PlainHex string `json:"plainHex"`

	// CipherHex is the raw ciphertext in lowercase hex. | CipherHex 是小写十六进制的原始密文。
	CipherHex string `json:"cipherHex"`

	// FixedKHex is the randomiser used to build the ciphertext; empty means crypto/rand. | FixedKHex 是构造密文所用的随机数；为空表示来自 crypto/rand。
	FixedKHex string `json:"fixedKHex,omitempty"`

	// Layout documents the byte layout. | Layout 记录字节布局。
	Layout string `json:"layout,omitempty"`
}

// sm2SignatureVector is one DER encoded SM2 signature vector. | sm2SignatureVector 是一条 DER 编码的 SM2 签名向量。
type sm2SignatureVector struct {
	// Name identifies the vector. | Name 标识该向量。
	Name string `json:"name"`

	// DataHex is the signed message in lowercase hex. | DataHex 是被签名消息的小写十六进制。
	DataHex string `json:"dataHex"`

	// SignatureHex is the DER signature in lowercase hex. | SignatureHex 是小写十六进制的 DER 签名。
	SignatureHex string `json:"signatureHex"`

	// Algorithm documents the signature construction. | Algorithm 记录签名构造方式。
	Algorithm string `json:"algorithm,omitempty"`
}

// sm2Vectors is the SM2 section shared by both directions. | sm2Vectors 是两个方向共用的 SM2 段。
type sm2Vectors struct {
	// PrivateKeyHex is the 64-character private scalar. | PrivateKeyHex 是 64 字符的私钥标量。
	PrivateKeyHex string `json:"privateKeyHex"`

	// PublicKeyHex is the uncompressed point 04||X||Y. | PublicKeyHex 是未压缩点 04||X||Y。
	PublicKeyHex string `json:"publicKeyHex"`

	// PublicKeyRawHex is the same point without the 0x04 marker. | PublicKeyRawHex 是去掉 0x04 标识的同一公钥点。
	PublicKeyRawHex string `json:"publicKeyRawHex,omitempty"`

	// Layout documents the ciphertext byte layout. | Layout 记录密文字节布局。
	Layout string `json:"layout,omitempty"`

	// Encryptions are the ciphertext vectors. | Encryptions 是密文向量。
	Encryptions []sm2EncryptionVector `json:"encryptions"`

	// Signatures are the signature vectors. | Signatures 是签名向量。
	Signatures []sm2SignatureVector `json:"signatures"`
}

// sm4CBCVector is one SM4-CBC vector with PKCS#7 padding. | sm4CBCVector 是一条带 PKCS#7 填充的 SM4-CBC 向量。
type sm4CBCVector struct {
	// Name identifies the vector. | Name 标识该向量。
	Name string `json:"name"`

	// IVHex is the 16-byte initialisation vector. | IVHex 是 16 字节初始向量。
	IVHex string `json:"ivHex"`

	// PlainHex is the plaintext in lowercase hex. | PlainHex 是小写十六进制明文。
	PlainHex string `json:"plainHex"`

	// CipherHex is the ciphertext in lowercase hex. | CipherHex 是小写十六进制密文。
	CipherHex string `json:"cipherHex"`
}

// sm4GCMVector is one SM4-GCM vector, ciphertext||tag with a 16-byte tag. | sm4GCMVector 是一条 SM4-GCM 向量，密文||16 字节标签。
type sm4GCMVector struct {
	// Name identifies the vector. | Name 标识该向量。
	Name string `json:"name"`

	// NonceHex is the nonce, 12 bytes for the fast path. | NonceHex 是随机数，走快速路径时为 12 字节。
	NonceHex string `json:"nonceHex"`

	// AADHex is the additional authenticated data. | AADHex 是附加认证数据。
	AADHex string `json:"aadHex"`

	// PlainHex is the plaintext in lowercase hex. | PlainHex 是小写十六进制明文。
	PlainHex string `json:"plainHex"`

	// CipherHex is ciphertext||tag in lowercase hex. | CipherHex 是小写十六进制的密文||标签。
	CipherHex string `json:"cipherHex"`
}

// sm4Vectors is the SM4 section shared by both directions. | sm4Vectors 是两个方向共用的 SM4 段。
type sm4Vectors struct {
	// KeyHex is the 16-byte SM4 key. | KeyHex 是 16 字节 SM4 密钥。
	KeyHex string `json:"keyHex"`

	// CBC are the CBC vectors. | CBC 是 CBC 向量。
	CBC []sm4CBCVector `json:"cbc"`

	// GCM are the GCM vectors. | GCM 是 GCM 向量。
	GCM []sm4GCMVector `json:"gcm"`
}

// htmxTransportVector is one Base64 field payload as sent by the htmx hook. | htmxTransportVector 是 htmx 钩子发送的单条 Base64 字段负载。
type htmxTransportVector struct {
	// Name identifies the vector. | Name 标识该向量。
	Name string `json:"name"`

	// FieldBase64 is the standard Base64 of the raw C1C3C2 ciphertext, matching b64x.StdEncoding. | FieldBase64 是原始 C1C3C2 密文的标准 Base64，与 b64x.StdEncoding 一致。
	FieldBase64 string `json:"fieldBase64"`

	// PlainHex is the plaintext the field carried before encryption. | PlainHex 是字段加密前的明文。
	PlainHex string `json:"plainHex"`
}

// guomiVectorFile is the Go to JavaScript vector file. | guomiVectorFile 是 Go 到 JS 的向量文件。
type guomiVectorFile struct {
	// Version is the schema version. | Version 是模式版本。
	Version int `json:"version"`

	// GeneratedBy names the producer. | GeneratedBy 记录产出方。
	GeneratedBy string `json:"generatedBy"`

	// Note documents how to regenerate the file. | Note 记录如何重新生成该文件。
	Note string `json:"note"`

	// SM3 are the digest vectors. | SM3 是摘要向量。
	SM3 []sm3Vector `json:"sm3"`

	// SM2 is the public key cryptography section. | SM2 是公钥密码段。
	SM2 sm2Vectors `json:"sm2"`

	// SM4 is the block cipher section. | SM4 是分组密码段。
	SM4 sm4Vectors `json:"sm4"`
}

// jsVectorFile is the JavaScript to Go vector file. | jsVectorFile 是 JS 到 Go 的向量文件。
type jsVectorFile struct {
	// Version is the schema version. | Version 是模式版本。
	Version int `json:"version"`

	// GeneratedBy names the producer. | GeneratedBy 记录产出方。
	GeneratedBy string `json:"generatedBy"`

	// Note documents how to regenerate the file. | Note 记录如何重新生成该文件。
	Note string `json:"note"`

	// SM3 are the digests computed by JavaScript. | SM3 是 JS 计算的摘要。
	SM3 []sm3Vector `json:"sm3"`

	// SM2 is the public key cryptography section. | SM2 是公钥密码段。
	SM2 sm2Vectors `json:"sm2"`

	// SM4 is the block cipher section. | SM4 是分组密码段。
	SM4 sm4Vectors `json:"sm4"`

	// HtmxTransport are the Base64 field payloads of the htmx hook. | HtmxTransport 是 htmx 钩子的 Base64 字段负载。
	HtmxTransport []htmxTransportVector `json:"htmxTransport"`
}

// derSignature mirrors the ASN.1 shape gmsm uses for SM2 signatures, so a fixed randomiser
// produces a reproducible byte sequence. | derSignature 与 gmsm 的 SM2 签名 ASN.1 形状一致，
// 因此配合固定随机数可产出可复现的字节序列。
type derSignature struct {
	// R is the first signature integer. | R 是第一个签名整数。
	R *big.Int

	// S is the second signature integer. | S 是第二个签名整数。
	S *big.Int
}

// fixedReader always returns the same bytes, which pins the SM2 randomiser. | fixedReader 永远返回相同字节，用于固定 SM2 随机数。
type fixedReader struct {
	// buf is the byte sequence handed to every Read call. | buf 是每次 Read 调用返回的字节序列。
	buf []byte
}

// Read copies the fixed buffer into p and never fails. | Read 把固定缓冲区拷入 p，永不失败。
func (r *fixedReader) Read(p []byte) (int, error) {
	copy(p, r.buf)
	return len(p), nil
}

// TestGuomiJSVectors writes the Go side vectors and runs the Node suite over them. | TestGuomiJSVectors 写出 Go 侧向量并对其运行 Node 套件。
func TestGuomiJSVectors(t *testing.T) {
	writeGoVectors(t)

	out, err := runNodeTest(t)
	if err != nil {
		t.Fatalf("node %s failed | node %s 执行失败: %v\n%s", jsTestScriptName, jsTestScriptName, err, out)
	}
	t.Logf("node %s output | 输出:\n%s", jsTestScriptName, out)
}

// TestJSVectorsAcceptedByGo verifies every JavaScript artefact with the Go primitives. | TestJSVectorsAcceptedByGo 用 Go 原语校验全部 JS 产物。
//
// The vector file is regenerated through Node when it is absent, so a single `go test ./web/`
// is enough on a machine that has Node installed. | 文件缺失时通过 Node 重新生成，
// 因此在装有 Node 的机器上仅需一次 `go test ./web/`。
func TestJSVectorsAcceptedByGo(t *testing.T) {
	writeGoVectors(t)

	path := filepath.Join("testdata", jsVectorFileName)
	if _, err := os.Stat(path); err != nil {
		out, runErr := runNodeTest(t)
		if runErr != nil {
			t.Fatalf("node %s failed | node %s 执行失败: %v\n%s", jsTestScriptName, jsTestScriptName, runErr, out)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: %v", errVectorIO, err)
	}
	var vectors jsVectorFile
	if err := jsonx.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("%v: %v", errVectorDecode, err)
	}
	if vectors.GeneratedBy != "js" {
		t.Fatalf("%v: generatedBy is %q, want \"js\" | generatedBy 为 %q，期望 \"js\"", errVectorMismatch, vectors.GeneratedBy, vectors.GeneratedBy)
	}

	privateKey, err := guomi.PrivateKeyFromHex(vectors.SM2.PrivateKeyHex)
	if err != nil {
		t.Fatalf("%v: private key | 私钥: %v", errVectorMismatch, err)
	}
	publicKey, err := guomi.PublicKeyFromHex(vectors.SM2.PublicKeyHex)
	if err != nil {
		t.Fatalf("%v: public key | 公钥: %v", errVectorMismatch, err)
	}
	// The public key must be the one Go derives from the same private key, | 该公钥必须与 Go 由同一私钥推导出的公钥一致，
	// so a self-consistent but wrong key pair cannot slip through. | 从而避免「自洽但错误」的密钥对蒙混过关。
	if derived := guomi.PublicKeyToHex(privateKey.Public()); derived != strings.ToLower(vectors.SM2.PublicKeyHex) {
		t.Fatalf("%v: derived public key %s != javascript public key %s | 推导公钥 %s 与 JS 公钥 %s 不一致", errVectorMismatch, derived, vectors.SM2.PublicKeyHex, derived, vectors.SM2.PublicKeyHex)
	}
	if expected := vectorPublicXHex + vectorPublicYHex; !strings.HasSuffix(publicKey.Hex(), expected) {
		t.Fatalf("%v: public key %s does not embed the GB/T 32918.5 example point | 公钥 %s 未包含 GB/T 32918.5 算例点", errVectorMismatch, publicKey.Hex(), publicKey.Hex())
	}

	for _, vector := range vectors.SM3 {
		data, err := hex.DecodeString(vector.DataHex)
		if err != nil {
			t.Fatalf("%v: sm3 %s data | sm3 %s 数据: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		if got := guomi.SumHex(data); got != strings.ToLower(vector.DigestHex) {
			t.Fatalf("%v: sm3 %s = %s, want %s | sm3 %s 为 %s，期望 %s", errVectorMismatch, vector.Name, got, vector.DigestHex, vector.Name, got, vector.DigestHex)
		}
	}

	for _, vector := range vectors.SM2.Encryptions {
		ciphertext, err := hex.DecodeString(vector.CipherHex)
		if err != nil {
			t.Fatalf("%v: sm2 %s cipher | sm2 %s 密文: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		plaintext, err := guomi.DecryptRaw(privateKey, ciphertext)
		if err != nil {
			t.Fatalf("%v: sm2 %s decrypt | sm2 %s 解密: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		if got := hex.EncodeToString(plaintext); got != strings.ToLower(vector.PlainHex) {
			t.Fatalf("%v: sm2 %s = %s, want %s | sm2 %s 为 %s，期望 %s", errVectorMismatch, vector.Name, got, vector.PlainHex, vector.Name, got, vector.PlainHex)
		}
	}

	for _, vector := range vectors.SM2.Signatures {
		data, err := hex.DecodeString(vector.DataHex)
		if err != nil {
			t.Fatalf("%v: sm2 signature %s data | sm2 签名 %s 数据: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		ok, err := guomi.VerifyHex(publicKey, data, vector.SignatureHex)
		if err != nil {
			t.Fatalf("%v: sm2 signature %s | sm2 签名 %s: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		if !ok {
			t.Fatalf("%v: sm2 signature %s rejected | sm2 签名 %s 未通过验签", errVectorMismatch, vector.Name, vector.Name)
		}
	}

	key, err := hex.DecodeString(vectors.SM4.KeyHex)
	if err != nil {
		t.Fatalf("%v: sm4 key | sm4 密钥: %v", errVectorMismatch, err)
	}
	for _, vector := range vectors.SM4.CBC {
		iv, err := hex.DecodeString(vector.IVHex)
		if err != nil {
			t.Fatalf("%v: sm4-cbc %s iv | sm4-cbc %s 的 iv: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		ciphertext, err := hex.DecodeString(vector.CipherHex)
		if err != nil {
			t.Fatalf("%v: sm4-cbc %s cipher | sm4-cbc %s 密文: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		plaintext, err := guomi.DecryptCBC(key, iv, ciphertext)
		if err != nil {
			t.Fatalf("%v: sm4-cbc %s decrypt | sm4-cbc %s 解密: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		if got := hex.EncodeToString(plaintext); got != strings.ToLower(vector.PlainHex) {
			t.Fatalf("%v: sm4-cbc %s = %s, want %s | sm4-cbc %s 为 %s，期望 %s", errVectorMismatch, vector.Name, got, vector.PlainHex, vector.Name, got, vector.PlainHex)
		}
	}
	for _, vector := range vectors.SM4.GCM {
		nonce, err := hex.DecodeString(vector.NonceHex)
		if err != nil {
			t.Fatalf("%v: sm4-gcm %s nonce | sm4-gcm %s 的 nonce: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		aad, err := hex.DecodeString(vector.AADHex)
		if err != nil {
			t.Fatalf("%v: sm4-gcm %s aad | sm4-gcm %s 的 aad: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		ciphertext, err := hex.DecodeString(vector.CipherHex)
		if err != nil {
			t.Fatalf("%v: sm4-gcm %s cipher | sm4-gcm %s 密文: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		plaintext, err := guomi.OpenGCM(key, nonce, ciphertext, aad)
		if err != nil {
			t.Fatalf("%v: sm4-gcm %s decrypt | sm4-gcm %s 解密: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		if got := hex.EncodeToString(plaintext); got != strings.ToLower(vector.PlainHex) {
			t.Fatalf("%v: sm4-gcm %s = %s, want %s | sm4-gcm %s 为 %s，期望 %s", errVectorMismatch, vector.Name, got, vector.PlainHex, vector.Name, got, vector.PlainHex)
		}
	}

	// The htmx hook ships the raw ciphertext as standard Base64, decoded here through b64x. | htmx 钩子以标准 Base64 传输原始密文，这里用 b64x 解码。
	if len(vectors.HtmxTransport) == 0 {
		t.Fatalf("%v: htmxTransport is empty | htmxTransport 为空", errVectorMismatch)
	}
	for _, vector := range vectors.HtmxTransport {
		ciphertext, err := b64x.StdEncoding.DecodeString(vector.FieldBase64)
		if err != nil {
			t.Fatalf("%v: htmx %s base64 | htmx %s 的 base64: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		plaintext, err := guomi.DecryptRaw(privateKey, ciphertext)
		if err != nil {
			t.Fatalf("%v: htmx %s decrypt | htmx %s 解密: %v", errVectorMismatch, vector.Name, vector.Name, err)
		}
		if got := hex.EncodeToString(plaintext); got != strings.ToLower(vector.PlainHex) {
			t.Fatalf("%v: htmx %s = %s, want %s | htmx %s 为 %s，期望 %s", errVectorMismatch, vector.Name, got, vector.PlainHex, vector.Name, got, vector.PlainHex)
		}
	}
}

// writeGoVectors builds and writes testdata/guomi_vectors.json, asserting Go self-consistency
// on the way so a divergent vector never reaches the JavaScript side. | writeGoVectors 构建并写出
// testdata/guomi_vectors.json，并在过程中断言 Go 侧自洽，避免不一致的向量流入 JS 侧。
func writeGoVectors(t *testing.T) guomiVectorFile {
	t.Helper()

	privateKey, err := guomi.PrivateKeyFromHex(vectorPrivateKeyHex)
	if err != nil {
		t.Fatalf("private key | 私钥: %v", err)
	}
	publicKey := privateKey.Public()
	publicKeyHex := guomi.PublicKeyToHex(publicKey)
	if publicKeyHex != "04"+vectorPublicXHex+vectorPublicYHex {
		t.Fatalf("derived public key %s does not match the GB/T 32918.5 example point | 推导公钥 %s 与 GB/T 32918.5 算例点不一致", publicKeyHex, publicKeyHex)
	}

	vectors := guomiVectorFile{
		Version:     1,
		GeneratedBy: "go",
		Note: "regenerate with: go test ./web/ -run TestGuomi -v | 重新生成：go test ./web/ -run TestGuomi -v",
		SM3:         goSM3Vectors(),
		SM2: sm2Vectors{
			PrivateKeyHex:   vectorPrivateKeyHex,
			PublicKeyHex:    publicKeyHex,
			PublicKeyRawHex: publicKeyHex[2:],
			Layout:          ciphertextLayout,
			Encryptions:     goSM2Encryptions(t, publicKey, privateKey),
			Signatures:      goSM2Signatures(t, privateKey, publicKey),
		},
		SM4: goSM4Vectors(t),
	}

	raw, err := jsonx.MarshalIndent(vectors, "", "  ")
	if err != nil {
		t.Fatalf("%v: %v", errVectorIO, err)
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("%v: %v", errVectorIO, err)
	}
	path := filepath.Join("testdata", goVectorFileName)
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("%v: %v", errVectorIO, err)
	}
	t.Logf("wrote %s with %d SM3, %d SM2 ciphertext and %d SM4 vectors | 已写出 %s：%d 条 SM3、%d 条 SM2 密文与 %d 条 SM4 向量",
		path, len(vectors.SM3), len(vectors.SM2.Encryptions), len(vectors.SM4.CBC)+len(vectors.SM4.GCM), path)
	return vectors
}

// goSM3Vectors returns the SM3 digest vectors, covering the official examples and the
// padding boundaries at 55, 56, 63, 64 and 65 bytes. | goSM3Vectors 返回 SM3 摘要向量，
// 覆盖官方向量与 55、56、63、64、65 字节的填充边界。
func goSM3Vectors() []sm3Vector {
	repeated := strings.Repeat("abcd", 64)
	inputs := []struct{ name, text string }{
		{"abc GB/T 32905", "abc"},
		{"empty", ""},
		{"abcd-x64 GB/T 32905", repeated},
		{"utf8 CJK | 中文", "国密 SM3 沙箱 | guomi sandbox"},
		{"len-55", strings.Repeat("a", 55)},
		{"len-56", strings.Repeat("a", 56)},
		{"len-63", strings.Repeat("a", 63)},
		{"len-64", strings.Repeat("a", 64)},
		{"len-65", strings.Repeat("a", 65)},
		{"len-1000-ascii", strings.Repeat("yd", 500)},
	}
	vectors := make([]sm3Vector, 0, len(inputs)+1)
	for _, input := range inputs {
		data := []byte(input.text)
		vectors = append(vectors, sm3Vector{
			Name:      input.name,
			DataHex:   hex.EncodeToString(data),
			DataUtf8:  input.text,
			DigestHex: guomi.SumHex(data),
		})
	}
	// A binary vector covers bytes that are invalid UTF-8 and exercises the byte API only. | 二进制向量覆盖非法 UTF-8 字节，仅走字节 API。
	binary := make([]byte, 1000)
	for i := range binary {
		binary[i] = byte(i*7 + 1)
	}
	vectors = append(vectors, sm3Vector{Name: "len-1000-binary", DataHex: hex.EncodeToString(binary), DigestHex: guomi.SumHex(binary)})
	return vectors
}

// goSM2Encryptions returns raw C1C3C2 ciphertexts, deterministic for the fixed randomiser and
// random through guomi.EncryptRaw for the framework path. | goSM2Encryptions 返回原始 C1C3C2 密文：
// 固定随机数的确定性样本，以及走 guomi.EncryptRaw 的随机样本。
func goSM2Encryptions(t *testing.T, publicKey *guomi.PublicKey, privateKey *guomi.PrivateKey) []sm2EncryptionVector {
	t.Helper()

	plaintexts := []struct{ name, text string }{
		{"ascii", "yd sandbox login"},
		{"empty", ""},
		{"utf8 CJK | 中文", "国密前端沙箱 | guomi sandbox"},
	}
	var vectors []sm2EncryptionVector
	for _, plaintext := range plaintexts {
		vectors = append(vectors, deterministicEncryption(t, publicKey, plaintext.name, []byte(plaintext.text)))
	}
	binary := []byte{0xff, 0x00, 0xff, 0x00, 0xa5, 0x5a, 0x00, 0x01}
	vectors = append(vectors, deterministicEncryption(t, publicKey, "binary", binary))
	// 100 bytes cross the 32-byte KDF block boundary, which is where implementations diverge. | 100 字节跨越 32 字节 KDF 分块边界，正是各实现容易分歧之处。
	long := make([]byte, 100)
	for i := range long {
		long[i] = byte(i)
	}
	vectors = append(vectors, deterministicEncryption(t, publicKey, "kdf-multi-block-100B", long))

	for _, plaintext := range []struct{ name, text string }{
		{"framework-encryptraw", "guomi.EncryptRaw | 框架原始密文"},
		{"framework-encryptraw-empty", ""},
	} {
		ciphertext, err := guomi.EncryptRaw(publicKey, []byte(plaintext.text))
		if err != nil {
			t.Fatalf("EncryptRaw(%s) | EncryptRaw(%s): %v", plaintext.name, plaintext.name, err)
		}
		// Round-trip here so the JavaScript side only ever sees verified bytes. | 此处先做往返校验，确保 JS 侧只看到已验证的字节。
		if back, err := guomi.DecryptRaw(privateKey, ciphertext); err != nil || string(back) != plaintext.text {
			t.Fatalf("EncryptRaw round-trip %s | EncryptRaw 往返 %s: %v", plaintext.name, plaintext.name, err)
		}
		vectors = append(vectors, sm2EncryptionVector{
			Name:      plaintext.name,
			PlainHex:  hex.EncodeToString([]byte(plaintext.text)),
			CipherHex: hex.EncodeToString(ciphertext),
			Layout:    ciphertextLayout,
		})
	}
	return vectors
}

// deterministicEncryption builds one raw ciphertext with the fixed randomiser and asserts that
// Go accepts it, so the layout claim is proven on the Go side first. | deterministicEncryption
// 使用固定随机数构造一条原始密文，并断言 Go 能接受它，从而先在 Go 侧坐实布局声明。
func deterministicEncryption(t *testing.T, publicKey *guomi.PublicKey, name string, plaintext []byte) sm2EncryptionVector {
	t.Helper()

	k, ok := new(big.Int).SetString(vectorFixedKHex, 16)
	if !ok {
		t.Fatalf("fixed randomiser %q is not hex | 固定随机数 %q 不是合法十六进制", vectorFixedKHex, vectorFixedKHex)
	}
	reader := &fixedReader{buf: paddedBytes(k, guomi.SM2CoordinateSize)}
	// gmsm emits one leading 0x04 marker byte that guomi.EncryptRaw does not use. | gmsm 会多输出一个前导 0x04 标识字节，而 guomi.EncryptRaw 不使用它。
	ciphertext, err := sm2.Encrypt(publicKey.Raw(), plaintext, reader, sm2.C1C3C2)
	if err != nil {
		t.Fatalf("gmsm Encrypt(%s) | gmsm Encrypt(%s): %v", name, name, err)
	}
	if len(ciphertext) == 0 || ciphertext[0] != 0x04 {
		t.Fatalf("gmsm Encrypt(%s) has no uncompressed point marker | gmsm Encrypt(%s) 缺少未压缩点标识", name, name)
	}
	raw := ciphertext[1:]
	if len(raw) != 2*guomi.SM2CoordinateSize+guomi.SM3Size+len(plaintext) {
		t.Fatalf("raw ciphertext length %d, want %d | 原始密文长度 %d，期望 %d", len(raw), 2*guomi.SM2CoordinateSize+guomi.SM3Size+len(plaintext), len(raw), 2*guomi.SM2CoordinateSize+guomi.SM3Size+len(plaintext))
	}
	back, err := guomi.DecryptRaw(privateKeyFromPublic(t, publicKey), raw)
	if err != nil {
		t.Fatalf("guomi.DecryptRaw(%s) | guomi.DecryptRaw(%s): %v", name, name, err)
	}
	if string(back) != string(plaintext) {
		t.Fatalf("guomi.DecryptRaw(%s) = %q, want %q | guomi.DecryptRaw(%s) 为 %q，期望 %q", name, back, plaintext, name, back, plaintext)
	}
	return sm2EncryptionVector{
		Name:      name,
		PlainHex:  hex.EncodeToString(plaintext),
		CipherHex: hex.EncodeToString(raw),
		FixedKHex: vectorFixedKHex,
		Layout:    ciphertextLayout,
	}
}

// privateKeyFromPublic re-derives the framework private key from the fixed scalar. | privateKeyFromPublic 由固定标量重新推导框架私钥。
func privateKeyFromPublic(t *testing.T, _ *guomi.PublicKey) *guomi.PrivateKey {
	t.Helper()
	privateKey, err := guomi.PrivateKeyFromHex(vectorPrivateKeyHex)
	if err != nil {
		t.Fatalf("private key | 私钥: %v", err)
	}
	return privateKey
}

// goSM2Signatures returns one deterministic DER signature plus one produced by guomi.Sign. | goSM2Signatures 返回一条确定性 DER 签名和一条由 guomi.Sign 产生的签名。
func goSM2Signatures(t *testing.T, privateKey *guomi.PrivateKey, publicKey *guomi.PublicKey) []sm2SignatureVector {
	t.Helper()

	message := []byte("yd sandbox signature | 沙箱签名")
	k, ok := new(big.Int).SetString(vectorFixedKHex, 16)
	if !ok {
		t.Fatalf("fixed randomiser %q is not hex | 固定随机数 %q 不是合法十六进制", vectorFixedKHex, vectorFixedKHex)
	}
	r, s, err := sm2.Sm2Sign(privateKey.Raw(), message, nil, &fixedReader{buf: paddedBytes(k, guomi.SM2CoordinateSize)})
	if err != nil {
		t.Fatalf("gmsm Sm2Sign | gmsm Sm2Sign: %v", err)
	}
	der, err := asn1.Marshal(derSignature{R: r, S: s})
	if err != nil {
		t.Fatalf("der signature | DER 签名: %v", err)
	}
	if ok, err := guomi.Verify(publicKey, message, der); err != nil || !ok {
		t.Fatalf("deterministic signature rejected by guomi.Verify | 确定性签名未被 guomi.Verify 接受: ok=%v err=%v", ok, err)
	}

	randomHex, err := guomi.SignHex(privateKey, message)
	if err != nil {
		t.Fatalf("guomi.SignHex | guomi.SignHex: %v", err)
	}
	if ok, err := guomi.VerifyHex(publicKey, message, randomHex); err != nil || !ok {
		t.Fatalf("framework signature rejected by guomi.Verify | 框架签名未被 guomi.Verify 接受: ok=%v err=%v", ok, err)
	}

	empty := []byte{}
	emptyHex, err := guomi.SignHex(privateKey, empty)
	if err != nil {
		t.Fatalf("guomi.SignHex(empty) | guomi.SignHex(空): %v", err)
	}

	return []sm2SignatureVector{
		{Name: "deterministic-fixed-k", DataHex: hex.EncodeToString(message), SignatureHex: hex.EncodeToString(der), Algorithm: signatureAlgorithm},
		{Name: "framework-Sign", DataHex: hex.EncodeToString(message), SignatureHex: randomHex, Algorithm: signatureAlgorithm},
		{Name: "framework-Sign-empty", DataHex: "", SignatureHex: emptyHex, Algorithm: signatureAlgorithm},
	}
}

// goSM4Vectors returns CBC and GCM vectors, including a non-12-byte GCM nonce. | goSM4Vectors 返回 CBC 与 GCM 向量，含非 12 字节 GCM 随机数。
func goSM4Vectors(t *testing.T) sm4Vectors {
	t.Helper()

	key, err := hex.DecodeString(vectorSM4KeyHex)
	if err != nil {
		t.Fatalf("sm4 key | SM4 密钥: %v", err)
	}
	iv, err := hex.DecodeString(vectorSM4IVHex)
	if err != nil {
		t.Fatalf("sm4 iv | SM4 初始向量: %v", err)
	}
	nonce, err := hex.DecodeString(vectorSM4NonceHex)
	if err != nil {
		t.Fatalf("sm4 nonce | SM4 随机数: %v", err)
	}

	cbcPlaintexts := []struct{ name string; data []byte }{
		{"empty", []byte{}},
		{"block-16B", []byte("0123456789abcdef")},
		{"utf8 CJK | 中文", []byte("国密沙箱")},
		{"long-100B", make([]byte, 100)},
	}
	for i := range cbcPlaintexts[3].data {
		cbcPlaintexts[3].data[i] = byte(0xff - i)
	}
	vectors := sm4Vectors{KeyHex: vectorSM4KeyHex}
	for _, plaintext := range cbcPlaintexts {
		ciphertext, err := guomi.EncryptCBC(key, iv, plaintext.data)
		if err != nil {
			t.Fatalf("EncryptCBC(%s) | EncryptCBC(%s): %v", plaintext.name, plaintext.name, err)
		}
		if back, err := guomi.DecryptCBC(key, iv, ciphertext); err != nil || string(back) != string(plaintext.data) {
			t.Fatalf("DecryptCBC round-trip %s | DecryptCBC 往返 %s: %v", plaintext.name, plaintext.name, err)
		}
		vectors.CBC = append(vectors.CBC, sm4CBCVector{
			Name:      plaintext.name,
			IVHex:     vectorSM4IVHex,
			PlainHex:  hex.EncodeToString(plaintext.data),
			CipherHex: hex.EncodeToString(ciphertext),
		})
	}

	gcmInputs := []struct {
		name           string
		nonce          []byte
		aad            []byte
		plaintext      []byte
	}{
		{"nonce-12-aad-empty-plain-empty", nonce, []byte{}, []byte{}},
		{"nonce-12-aad-5B-plain-16B", nonce, []byte{0x01, 0x23, 0x45, 0x67, 0x89}, []byte("0123456789abcdef")},
		{"nonce-12-aad-16B-plain-100B", nonce, []byte("0123456789abcdef"), make([]byte, 100)},
		{"nonce-12-aad-utf8-plain-37B", nonce, []byte("附加认证 | aad"), make([]byte, 37)},
		{"nonce-16-aad-utf8-plain-utf8", iv, []byte("aad"), []byte("non-96-bit nonce | 非 96 位随机数")},
	}
	for i := range gcmInputs[2].plaintext {
		gcmInputs[2].plaintext[i] = byte(i * 3)
	}
	for i := range gcmInputs[3].plaintext {
		gcmInputs[3].plaintext[i] = byte(i + 0x40)
	}
	for _, input := range gcmInputs {
		sealed, err := guomi.SealGCM(key, input.nonce, input.plaintext, input.aad)
		if err != nil {
			t.Fatalf("SealGCM(%s) | SealGCM(%s): %v", input.name, input.name, err)
		}
		if len(sealed) != len(input.plaintext)+guomi.SM4GCMTagSize {
			t.Fatalf("SealGCM(%s) length %d | SealGCM(%s) 长度 %d", input.name, len(sealed), input.name, len(sealed))
		}
		if back, err := guomi.OpenGCM(key, input.nonce, sealed, input.aad); err != nil || string(back) != string(input.plaintext) {
			t.Fatalf("OpenGCM round-trip %s | OpenGCM 往返 %s: %v", input.name, input.name, err)
		}
		vectors.GCM = append(vectors.GCM, sm4GCMVector{
			Name:      input.name,
			NonceHex:  hex.EncodeToString(input.nonce),
			AADHex:    hex.EncodeToString(input.aad),
			PlainHex:  hex.EncodeToString(input.plaintext),
			CipherHex: hex.EncodeToString(sealed),
		})
	}
	return vectors
}

// paddedBytes renders a big integer as a fixed width big-endian buffer. | paddedBytes 把大整数渲染为定长大端缓冲区。
func paddedBytes(x *big.Int, size int) []byte {
	out := make([]byte, size)
	raw := x.Bytes()
	if len(raw) >= size {
		copy(out, raw[len(raw)-size:])
		return out
	}
	copy(out[size-len(raw):], raw)
	return out
}

// runNodeTest executes the JavaScript suite, skipping the test when Node is unavailable. | runNodeTest 执行 JS 套件，Node 不可用时跳过测试。
func runNodeTest(t *testing.T) ([]byte, error) {
	t.Helper()

	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("skipping: node is not available on PATH (%v); install Node.js to run the javascript guomi suite | 跳过：PATH 中没有 node（%v），请安装 Node.js 以运行 JS 国密套件", err)
		return nil, nil
	}

	script := ""
	for _, candidate := range []string{jsTestScriptName, "static/js/yd-guomi.test.mjs"} {
		if _, err := os.Stat(candidate); err == nil {
			script = candidate
			break
		}
	}
	if script == "" {
		t.Fatalf("%v: %s | 缺少 %s", errNodeScriptMissing, jsTestScriptName, jsTestScriptName)
		return nil, nil
	}

	cmd := exec.Command("node", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, errx.WrapDetail(err, errNodeRunFailed, "node %s exited with an error | node %s 以错误退出", script, script)
	}
	return out, nil
}

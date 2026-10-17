package jwt

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/http/router"
)

// ---------------------------------------------------------------- Shared fixtures | 共享夹具

const (
	// rfc7519Token is the exact compact JWS published in RFC 7519 §3.1. | rfc7519Token 是 RFC 7519 §3.1 公布的紧凑 JWS 原文。
	rfc7519Token = "eyJ0eXAiOiJKV1QiLA0KICJhbGciOiJIUzI1NiJ9." +
		"eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ." +
		"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// rfc7519KeyB64 is the base64url encoding of the HMAC key given in RFC 7519 §3.1. | rfc7519KeyB64 是 RFC 7519 §3.1 给出的 HMAC 密钥的 base64url 编码。
	rfc7519KeyB64 = "AyM1SysPpbyDfgZld3umj1qzKObwVMkoqQ-EstJQLr_T-1qS0gZH75aKtMN3Yj0iPS4hcgUuTwjAzZr1Z9CAow"
	// rfc7519SigB64 is the signature segment of the RFC 7519 §3.1 token. | rfc7519SigB64 是 RFC 7519 §3.1 token 的签名段。
	rfc7519SigB64 = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
)

// rfc7519KeyOctets is the HMAC key octet sequence printed byte by byte in RFC 7519 §3.1. | rfc7519KeyOctets 是 RFC 7519 §3.1 逐字节列出的 HMAC 密钥序列。
var rfc7519KeyOctets = []byte{
	3, 35, 53, 75, 43, 15, 165, 188, 131, 126, 6, 101,
	119, 123, 166, 143, 90, 179, 40, 230, 240, 84, 201, 40,
	169, 15, 132, 178, 210, 80, 46, 191, 211, 251, 90, 146,
	210, 6, 71, 239, 150, 138, 180, 195, 119, 98, 61, 34,
	61, 46, 33, 114, 5, 46, 79, 8, 192, 205, 154, 245,
	103, 208, 128, 163,
}

// testSecret is the shared HMAC secret of these tests. | testSecret 是这些测试共用的 HMAC 密钥。
var testSecret = []byte("yd-framework-jwt-test-secret-0123456789")

// fixedNow is the deterministic clock used by every time-dependent test. | fixedNow 是全部依赖时间的测试使用的确定性时钟。
var fixedNow = time.Unix(1_700_000_000, 0).UTC()

// rfcNow is the deterministic clock of the RFC 7519 §3.1 vector, whose exp is 1300819380 (2011-03-22). | rfcNow 是 RFC 7519 §3.1 向量的确定性时钟，该向量的 exp 为 1300819380（2011-03-22）。
var rfcNow = time.Unix(1_300_819_300, 0).UTC()

// buildToken assembles a compact JWS from a raw JSON header, a raw JSON payload and raw signature bytes. | buildToken 用原始 JSON 头部、原始 JSON 载荷与原始签名字节拼装紧凑 JWS。
func buildToken(header, payload string, sig []byte) string {
	return b64x.RawURLEncoding.EncodeToString([]byte(header)) + "." +
		b64x.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		b64x.RawURLEncoding.EncodeToString(sig)
}

// buildSegments assembles a compact JWS from three already encoded segments. | buildSegments 用三个已编码的段拼装紧凑 JWS。
func buildSegments(header, payload, sig string) string {
	return header + "." + payload + "." + sig
}

// b64 wraps EncodeToString for readability. | b64 包装 EncodeToString 以便阅读。
func b64(s string) string { return b64x.RawURLEncoding.EncodeToString([]byte(s)) }

// hmacKeyFunc returns a KeyFunc that always yields key. | hmacKeyFunc 返回恒定给出 key 的 KeyFunc。
func hmacKeyFunc(key []byte) KeyFunc {
	return func(Header) (any, error) { return key, nil }
}

// mustSign signs claims with the given key and fails the test on error. | mustSign 用给定密钥签发 claims，出错即判定测试失败。
func mustSign(t *testing.T, claims map[string]any, key any, alg Alg, o SignOptions) string {
	t.Helper()
	raw, err := Sign(claims, key, alg, o)
	if err != nil {
		t.Fatalf("Sign(%s) 失败: %v", alg, err)
	}
	return raw
}

// mustParse parses a token and fails the test on error. | mustParse 解析 token，出错即判定测试失败。
func mustParse(t *testing.T, raw string, keyFn KeyFunc, o ParseOptions) *Token {
	t.Helper()
	tok, err := Parse(raw, keyFn, o)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	return tok
}

// resign recomputes the signature of a compact JWS head with testSecret. | resign 用 testSecret 重算紧凑 JWS 头的签名。
func resign(t *testing.T, headerSegment, payloadSegment string) string {
	t.Helper()
	input := headerSegment + "." + payloadSegment
	mac := hmac.New(sha256.New, testSecret)
	mac.Write([]byte(input))
	return input + "." + b64x.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ---------------------------------------------------------------- RFC 7519 official vector | RFC 7519 官方向量

func TestHS256AgainstRFC7519Vector(t *testing.T) {
	key, err := b64x.RawURLEncoding.DecodeString(rfc7519KeyB64)
	if err != nil {
		t.Fatalf("解码 RFC 7519 密钥失败: %v", err)
	}
	if !bytes.Equal(key, rfc7519KeyOctets) {
		t.Fatalf("RFC 7519 密钥字节不一致:\n got %v\nwant %v", key, rfc7519KeyOctets)
	}
	wantSig, err := b64x.RawURLEncoding.DecodeString(rfc7519SigB64)
	if err != nil {
		t.Fatalf("解码 RFC 7519 签名失败: %v", err)
	}

	opts := ParseOptions{
		ValidAlgs:  []Alg{HS256},
		Issuer:     "joe",
		RequireExp: true,
		Now:        func() time.Time { return rfcNow },
	}
	tok := mustParse(t, rfc7519Token, hmacKeyFunc(rfc7519KeyOctets), opts)

	// Check the header field by field. | 头部逐字段核对。
	if tok.Header.Alg != HS256 {
		t.Errorf("Header.Alg = %q，期望 %q", tok.Header.Alg, HS256)
	}
	if tok.Header.Typ != "JWT" {
		t.Errorf("Header.Typ = %q，期望 %q", tok.Header.Typ, "JWT")
	}
	if tok.Header.Kid != "" {
		t.Errorf("Header.Kid = %q，期望空串", tok.Header.Kid)
	}
	if tok.Header.Extra != nil {
		t.Errorf("Header.Extra = %v，期望 nil", tok.Header.Extra)
	}

	// The signature must equal the published one byte for byte. | 签名必须与 RFC 公布的签名逐字节相等。
	if !bytes.Equal(tok.Signature, wantSig) {
		t.Fatalf("Signature = %x，期望 %x", tok.Signature, wantSig)
	}

	// Recompute with the standard library on the raw signing input. | 用标准库 HMAC 在原始 signing input 上重算，逐字节复核。
	dot := strings.LastIndexByte(rfc7519Token, '.')
	mac := hmac.New(sha256.New, rfc7519KeyOctets)
	mac.Write([]byte(rfc7519Token[:dot]))
	if !hmac.Equal(mac.Sum(nil), tok.Signature) {
		t.Fatalf("HMAC-SHA256 重算结果与 Signature 不一致")
	}

	// Payload fields are read by byte scanning, never through a map. | 载荷字段走字节扫描，不经过 map。
	if got := tok.Claims.Issuer(); got != "joe" {
		t.Errorf("Claims.Issuer() = %q，期望 %q", got, "joe")
	}
	if got := tok.Claims.Subject(); got != "" {
		t.Errorf("Claims.Subject() = %q，期望空串", got)
	}
	if got := tok.Claims.ID(); got != "" {
		t.Errorf("Claims.ID() = %q，期望空串", got)
	}
	exp, ok := tok.Claims.ExpiresAt()
	if !ok || !exp.Equal(time.Unix(1300819380, 0).UTC()) {
		t.Errorf("Claims.ExpiresAt() = (%v, %v)，期望 1300819380", exp, ok)
	}
	if _, ok := tok.Claims.NotBefore(); ok {
		t.Errorf("Claims.NotBefore() 报告存在 nbf，但 RFC 载荷没有该声明")
	}
	if v, ok := tok.Claims.GetBool("http://example.com/is_root"); !ok || !v {
		t.Errorf("Claims.GetBool(is_root) = (%v, %v)，期望 (true, true)", v, ok)
	}
	if !tok.Claims.Has("iss") || tok.Claims.Has("nope") {
		t.Errorf("Claims.Has 判定错误: Has(iss)=%v Has(nope)=%v", tok.Claims.Has("iss"), tok.Claims.Has("nope"))
	}
	if got := tok.Claims.Keys(); len(got) != 3 {
		t.Errorf("Claims.Keys() = %v，期望 3 个键", got)
	}
	wantPayload, _ := b64x.RawURLEncoding.DecodeString(strings.Split(rfc7519Token, ".")[1])
	if !bytes.Equal(tok.Claims.Raw(), wantPayload) {
		t.Errorf("Claims.Raw() = %q，期望 %q", tok.Claims.Raw(), wantPayload)
	}

	// An issuer mismatch must be rejected. | 签发方不匹配必须被拒绝。
	if _, err := Parse(rfc7519Token, hmacKeyFunc(rfc7519KeyOctets),
		ParseOptions{Issuer: "someone-else", Now: func() time.Time { return rfcNow }}); !errors.Is(err, ErrIssuerMismatch) {
		t.Errorf("Issuer 不匹配的 err = %v，期望 ErrIssuerMismatch", err)
	}

	// An algorithm outside the allow list must be rejected. | 算法白名单之外必须被拒绝。
	if _, err := Parse(rfc7519Token, hmacKeyFunc(rfc7519KeyOctets),
		ParseOptions{ValidAlgs: []Alg{RS256, ES256}, Now: func() time.Time { return rfcNow }}); !errors.Is(err, ErrAlgNotAllowed) {
		t.Errorf("算法白名单外的 err = %v，期望 ErrAlgNotAllowed", err)
	}

	// A missing exp must be rejected when exp is required. | 缺少 exp 且要求 exp 时必须被拒绝。
	noExp := resign(t, b64(`{"alg":"HS256"}`), b64(`{"iss":"joe"}`))
	if _, err := Parse(noExp, hmacKeyFunc(testSecret), ParseOptions{RequireExp: true}); !errors.Is(err, ErrMissingExp) {
		t.Errorf("缺少 exp 的 err = %v，期望 ErrMissingExp", err)
	}

	// Flipping the last signature character must fail. | 篡改签名末位必须失败。
	tampered := rfc7519Token[:len(rfc7519Token)-1] + "A"
	if _, err := Parse(tampered, hmacKeyFunc(rfc7519KeyOctets), ParseOptions{}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("篡改签名的 err = %v，期望 ErrSignatureInvalid", err)
	}

	// Tampering with the payload must fail: the signature covers the encoded segments. | 篡改载荷必须失败：签名覆盖编码后的段。
	// The tampered payload stays valid JSON, so the failure is reported by the signature check and not by the
	// structural check. | 篡改后的载荷仍是合法 JSON，因此失败由签名校验而非结构校验报出。
	segments := strings.Split(rfc7519Token, ".")
	tamperedPayload := buildSegments(segments[0],
		b64(`{"iss":"joh",`+"\r\n"+` "exp":1300819380,`+"\r\n"+` "http://example.com/is_root":true}`), segments[2])
	if _, err := Parse(tamperedPayload, hmacKeyFunc(rfc7519KeyOctets),
		ParseOptions{Now: func() time.Time { return rfcNow }}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("篡改载荷的 err = %v，期望 ErrSignatureInvalid", err)
	}

	// Surrounding whitespace is tolerated (common in cookies and config files). | 两侧空白应被容忍（cookie / 配置文件常见）。
	if _, err := Parse("  "+rfc7519Token+"\n", hmacKeyFunc(rfc7519KeyOctets),
		ParseOptions{Now: func() time.Time { return rfcNow }}); err != nil {
		t.Errorf("容忍首尾空白的 err = %v", err)
	}
}

// ---------------------------------------------------------------- Asymmetric round trips | 非对称算法往返

func TestRS256AndES256AndPS256RoundTrip(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成 RSA 密钥失败: %v", err)
	}
	otherRSA, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成备用 RSA 密钥失败: %v", err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成 ECDSA 密钥失败: %v", err)
	}
	otherEC, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成备用 ECDSA 密钥失败: %v", err)
	}

	claims := map[string]any{
		"sub": "u-42",
		"n":   int64(7),
		"f":   1.5,
		"b":   true,
		"exp": fixedNow.Add(time.Hour).Unix(),
	}

	cases := []struct {
		name     string
		alg      Alg
		priv     any
		pub      any
		wrongPub any
		sigLen   int
	}{
		{"RS256", RS256, rsaKey, &rsaKey.PublicKey, &otherRSA.PublicKey, 256},
		{"PS256", PS256, rsaKey, &rsaKey.PublicKey, &otherRSA.PublicKey, 256},
		{"ES256", ES256, ecKey, &ecKey.PublicKey, &otherEC.PublicKey, 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mustSign(t, claims, tc.priv, tc.alg, SignOptions{Kid: "key-2026"})
			pubKeyFn := func(Header) (any, error) { return tc.pub, nil }
			tok := mustParse(t, raw, pubKeyFn, ParseOptions{
				ValidAlgs: []Alg{tc.alg},
				Now:       func() time.Time { return fixedNow },
			})

			if tok.Header.Alg != tc.alg {
				t.Errorf("Header.Alg = %q，期望 %q", tok.Header.Alg, tc.alg)
			}
			if tok.Header.Kid != "key-2026" {
				t.Errorf("Header.Kid = %q，期望 %q", tok.Header.Kid, "key-2026")
			}
			if tok.Header.Typ != "JWT" {
				t.Errorf("Header.Typ = %q，期望 %q", tok.Header.Typ, "JWT")
			}
			if len(tok.Signature) != tc.sigLen {
				t.Errorf("len(Signature) = %d，期望 %d", len(tok.Signature), tc.sigLen)
			}
			if got := tok.Claims.Subject(); got != "u-42" {
				t.Errorf("Claims.Subject() = %q，期望 %q", got, "u-42")
			}
			if got, ok := tok.Claims.GetInt64("n"); !ok || got != 7 {
				t.Errorf("Claims.GetInt64(n) = (%d, %v)，期望 (7, true)", got, ok)
			}
			if got, ok := tok.Claims.GetFloat64("f"); !ok || got != 1.5 {
				t.Errorf("Claims.GetFloat64(f) = (%v, %v)，期望 (1.5, true)", got, ok)
			}
			if got, ok := tok.Claims.GetBool("b"); !ok || !got {
				t.Errorf("Claims.GetBool(b) = (%v, %v)，期望 (true, true)", got, ok)
			}

			// Verifying with another key of the same kind must fail. | 换成另一把同类型密钥必须验签失败。
			if _, err := Parse(raw, func(Header) (any, error) { return tc.wrongPub, nil }, ParseOptions{}); !errors.Is(err, ErrSignatureInvalid) {
				t.Errorf("错误公钥的 err = %v，期望 ErrSignatureInvalid", err)
			}

			// A tampered signature must fail. | 篡改签名必须失败。
			bad := bytes.Clone(tok.Signature)
			bad[0] ^= 0xFF
			tampered := buildToken(`{"alg":"`+string(tc.alg)+`"}`, `{"sub":"u-42"}`, bad)
			if _, err := Parse(tampered, pubKeyFn, ParseOptions{}); !errors.Is(err, ErrSignatureInvalid) {
				t.Errorf("篡改签名的 err = %v，期望 ErrSignatureInvalid", err)
			}
		})
	}
}

func TestEdDSARoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 Ed25519 密钥失败: %v", err)
	}
	raw := mustSign(t, map[string]any{"sub": "ed"}, priv, EdDSA, SignOptions{})
	tok := mustParse(t, raw, func(Header) (any, error) { return pub, nil }, ParseOptions{ValidAlgs: []Alg{EdDSA}})
	if len(tok.Signature) != ed25519.SignatureSize {
		t.Errorf("len(Signature) = %d，期望 %d", len(tok.Signature), ed25519.SignatureSize)
	}
	if got := tok.Claims.Subject(); got != "ed" {
		t.Errorf("Claims.Subject() = %q，期望 %q", got, "ed")
	}
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成备用 Ed25519 密钥失败: %v", err)
	}
	if _, err := Parse(raw, func(Header) (any, error) { return otherPub, nil }, ParseOptions{}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("错误 Ed25519 公钥的 err = %v，期望 ErrSignatureInvalid", err)
	}
}

// ---------------------------------------------------------------- Time claims and leeway | 时间声明与容差

func TestExpiredAndNotYetValidWithLeeway(t *testing.T) {
	keyFn := hmacKeyFunc(testSecret)
	base := fixedNow
	at := func(d time.Duration) func() time.Time {
		return func() time.Time { return base.Add(d) }
	}

	// exp equal to now is not expired yet. | exp 恰好是当前时刻：不算过期。
	expiring := mustSign(t, map[string]any{"exp": base.Unix()}, testSecret, HS256, SignOptions{})
	if _, err := Parse(expiring, keyFn, ParseOptions{Now: at(0)}); err != nil {
		t.Fatalf("exp == now 时应通过，err = %v", err)
	}

	// Expired one second later. | 1 秒后过期。
	if _, err := Parse(expiring, keyFn, ParseOptions{Now: at(time.Second)}); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("过期 token 的 err = %v，期望 ErrTokenExpired", err)
	}

	// A one-second leeway covers exactly that lateness. | 容差恰好覆盖 1 秒的迟到。
	if _, err := Parse(expiring, keyFn, ParseOptions{Now: at(time.Second), Leeway: time.Second}); err != nil {
		t.Errorf("容差内的过期 token 应通过，err = %v", err)
	}
	// A one-second leeway does not cover two seconds. | 容差不足以覆盖 2 秒的迟到。
	if _, err := Parse(expiring, keyFn, ParseOptions{Now: at(2 * time.Second), Leeway: time.Second}); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("超出容差的 err = %v，期望 ErrTokenExpired", err)
	}

	// nbf in the future is rejected as not yet valid. | nbf 尚未到达：判定为未生效。
	notYet := mustSign(t, map[string]any{"nbf": base.Add(time.Minute).Unix(), "exp": base.Add(time.Hour).Unix()},
		testSecret, HS256, SignOptions{})
	if _, err := Parse(notYet, keyFn, ParseOptions{Now: at(0)}); !errors.Is(err, ErrTokenNotValidYet) {
		t.Fatalf("未生效 token 的 err = %v，期望 ErrTokenNotValidYet", err)
	}
	// A 60-second leeway covers exactly that earliness. | 容差恰好覆盖 60 秒的提前量。
	if _, err := Parse(notYet, keyFn, ParseOptions{Now: at(0), Leeway: time.Minute}); err != nil {
		t.Errorf("容差内的未生效 token 应通过，err = %v", err)
	}
	// It passes once nbf is reached. | nbf 到达后通过。
	if _, err := Parse(notYet, keyFn, ParseOptions{Now: at(time.Minute)}); err != nil {
		t.Errorf("nbf 到达后应通过，err = %v", err)
	}

	// With both out of range, nbf is reported first. | exp 与 nbf 同时存在且都已越界：nbf 优先报未生效。
	both := mustSign(t, map[string]any{"nbf": base.Add(time.Minute).Unix(), "exp": base.Add(-time.Minute).Unix()},
		testSecret, HS256, SignOptions{})
	if _, err := Parse(both, keyFn, ParseOptions{Now: at(0)}); !errors.Is(err, ErrTokenNotValidYet) {
		t.Errorf("nbf 与 exp 同时越界的 err = %v，期望 ErrTokenNotValidYet", err)
	}

	// iat is informational: a future iat is readable and does not block. | iat 属于信息性声明：未来时间不影响通过，但可被读取。
	future := mustSign(t, map[string]any{"iat": base.Add(time.Hour).Unix()}, testSecret, HS256, SignOptions{})
	tok := mustParse(t, future, keyFn, ParseOptions{Now: at(0)})
	if got, ok := tok.Claims.IssuedAt(); !ok || !got.Equal(base.Add(time.Hour)) {
		t.Errorf("Claims.IssuedAt() = (%v, %v)，期望 %v", got, ok, base.Add(time.Hour))
	}

	// A non-numeric exp is a structural error. | 非数字的 exp 视为结构性错误。
	badExp := resign(t, b64(`{"alg":"HS256"}`), b64(`{"exp":"tomorrow"}`))
	if _, err := Parse(badExp, keyFn, ParseOptions{}); !errors.Is(err, ErrMalformed) {
		t.Errorf("字符串 exp 的 err = %v，期望 ErrMalformed", err)
	}
	// An out-of-range numeric exp is rejected as well. | 越界数值的 exp 同样拒绝。
	hugeExp := resign(t, b64(`{"alg":"HS256"}`), b64(`{"exp":1e300}`))
	if _, err := Parse(hugeExp, keyFn, ParseOptions{}); !errors.Is(err, ErrMalformed) {
		t.Errorf("超大 exp 的 err = %v，期望 ErrMalformed", err)
	}

	// A fractional NumericDate is supported with sub-second precision. | 小数的 NumericDate 支持亚秒精度。
	frac := resign(t, b64(`{"alg":"HS256"}`), b64(`{"exp":1700000000.5}`))
	tok = mustParse(t, frac, keyFn, ParseOptions{Now: at(0)})
	if got, ok := tok.Claims.ExpiresAt(); !ok || !got.Equal(time.Unix(1700000000, 500_000_000).UTC()) {
		t.Errorf("小数 exp = (%v, %v)，期望 1700000000.5", got, ok)
	}
	// A fractional exp that has just passed. | 小数 exp 恰好过期。
	if _, err := Parse(frac, keyFn, ParseOptions{Now: func() time.Time { return time.Unix(1700000001, 0) }}); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("小数 exp 过期的 err = %v，期望 ErrTokenExpired", err)
	}
}

// ---------------------------------------------------------------- Algorithm downgrade and confusion | 算法降级与混淆

func TestAlgNoneRejected(t *testing.T) {
	for _, name := range []string{"none", "None", "NONE"} {
		raw := buildToken(`{"alg":"`+name+`","typ":"JWT"}`, `{"sub":"attacker"}`, nil)
		if _, err := Parse(raw, hmacKeyFunc(testSecret), ParseOptions{}); !errors.Is(err, ErrAlgNotAllowed) {
			t.Errorf("alg=%q 的 err = %v，期望 ErrAlgNotAllowed", name, err)
		}
		if _, err := Parse(raw, hmacKeyFunc(testSecret), ParseOptions{ValidAlgs: []Alg{HS256, RS256}}); !errors.Is(err, ErrAlgNotAllowed) {
			t.Errorf("alg=%q（带白名单）的 err = %v，期望 ErrAlgNotAllowed", name, err)
		}
	}

	// A none token carrying a non-empty signature is rejected too. | 带非空签名的 none token 同样拒绝。
	if _, err := Parse(buildToken(`{"alg":"none"}`, `{"sub":"attacker"}`, []byte("garbage-signature")),
		hmacKeyFunc(testSecret), ParseOptions{}); !errors.Is(err, ErrAlgNotAllowed) {
		t.Errorf("alg=none（带签名）的 err = %v，期望 ErrAlgNotAllowed", err)
	}

	// Any unknown algorithm is rejected. | 未知算法一律拒绝。
	if _, err := Parse(buildToken(`{"alg":"HS255"}`, `{"sub":"x"}`, nil), hmacKeyFunc(testSecret), ParseOptions{}); !errors.Is(err, ErrAlgNotAllowed) {
		t.Errorf("未知算法的 err = %v，期望 ErrAlgNotAllowed", err)
	}
	// A non-string alg is rejected as well. | 算法不是字符串时同样拒绝。
	if _, err := Parse(buildToken(`{"alg":256}`, `{"sub":"x"}`, nil), hmacKeyFunc(testSecret), ParseOptions{}); !errors.Is(err, ErrAlgNotAllowed) {
		t.Errorf("数字 alg 的 err = %v，期望 ErrAlgNotAllowed", err)
	}
	// A header without alg is rejected as well. | 头部缺少 alg 时同样拒绝。
	if _, err := Parse(buildToken(`{"typ":"JWT"}`, `{"sub":"x"}`, nil), hmacKeyFunc(testSecret), ParseOptions{}); !errors.Is(err, ErrAlgNotAllowed) {
		t.Errorf("缺少 alg 的 err = %v，期望 ErrAlgNotAllowed", err)
	}

	// The signing side must refuse none as well. | 签发侧也不得接受 none。
	if _, err := Sign(map[string]any{"sub": "x"}, testSecret, Alg("none"), SignOptions{}); !errors.Is(err, ErrAlgNotAllowed) {
		t.Errorf("Sign(none) 的 err = %v，期望 ErrAlgNotAllowed", err)
	}
}

func TestAlgConfusionRejected(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成 RSA 密钥失败: %v", err)
	}
	pub := &rsaKey.PublicKey
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("编码 RSA 公钥失败: %v", err)
	}

	// The attacker uses the public RSA key as an HMAC secret and forges an HS256 token. | 攻击者把公开的 RSA 公钥当作 HMAC 密钥，伪造 alg=HS256 的 token。
	forged := mustSign(t, map[string]any{"sub": "attacker"}, der, HS256, SignOptions{})
	pubKeyFn := func(Header) (any, error) { return pub, nil }

	// First line of defence: the algorithm allow list. | 第一道防线：算法白名单。
	if _, err := Parse(forged, pubKeyFn, ParseOptions{ValidAlgs: []Alg{RS256, RS384, RS512, PS256, PS384, PS512}}); !errors.Is(err, ErrAlgNotAllowed) {
		t.Errorf("白名单拦不住的 err = %v，期望 ErrAlgNotAllowed", err)
	}

	// Second line of defence: even without an allow list, the HMAC path accepts only []byte/string keys. | 第二道防线：即使没有白名单，HMAC 路径也只接受 []byte/string 密钥。
	if _, err := Parse(forged, pubKeyFn, ParseOptions{}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("混淆攻击的 err = %v，期望 ErrSignatureInvalid", err)
	}

	// Reverse confusion: an RS256 token verified with a symmetric key. | 反向混淆：RS256 token 用对称密钥验证。
	legit := mustSign(t, map[string]any{"sub": "u1"}, rsaKey, RS256, SignOptions{})
	if _, err := Parse(legit, hmacKeyFunc([]byte("public-key-as-secret")), ParseOptions{}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("反向混淆的 err = %v，期望 ErrSignatureInvalid", err)
	}

	// ECDSA and Ed25519 public keys cannot act as HMAC keys either. | ECDSA / Ed25519 的公钥同样不能充当 HMAC 密钥。
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成 ECDSA 密钥失败: %v", err)
	}
	ecDER, err := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	if err != nil {
		t.Fatalf("编码 ECDSA 公钥失败: %v", err)
	}
	ecForged := mustSign(t, map[string]any{"sub": "attacker"}, ecDER, HS256, SignOptions{})
	if _, err := Parse(ecForged, func(Header) (any, error) { return &ecKey.PublicKey, nil }, ParseOptions{}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("ECDSA 公钥混淆的 err = %v，期望 ErrSignatureInvalid", err)
	}

	// ES256 verification with a mismatched curve must fail. | 曲线不匹配的 ES256 验签必须失败。
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("生成 P-384 密钥失败: %v", err)
	}
	if _, err := Parse(mustSign(t, map[string]any{"sub": "u1"}, ecKey, ES256, SignOptions{}),
		func(Header) (any, error) { return &p384.PublicKey, nil }, ParseOptions{}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("曲线不匹配的 err = %v，期望 ErrSignatureInvalid", err)
	}

	// Signing with a mismatched key type fails as well. | 密钥类型不匹配的签发请求同样失败。
	if _, err := Sign(map[string]any{"sub": "u1"}, []byte("secret"), RS256, SignOptions{}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("Sign(RS256, []byte) 的 err = %v，期望 ErrSignatureInvalid", err)
	}
	if _, err := Sign(map[string]any{"sub": "u1"}, pub, HS256, SignOptions{}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("Sign(HS256, *rsa.PublicKey) 的 err = %v，期望 ErrSignatureInvalid", err)
	}
	if _, err := Sign(map[string]any{"sub": "u1"}, nil, HS256, SignOptions{}); err == nil {
		t.Errorf("Sign(HS256, nil) 竟然成功")
	}
}

// ---------------------------------------------------------------- kid rotation | kid 轮换

func TestKeyFuncKidRotation(t *testing.T) {
	keys := map[string][]byte{
		"2024-01": []byte("rotation-secret-2024-01-abcdefghij"),
		"2025-01": []byte("rotation-secret-2025-01-abcdefghij"),
	}
	var seenKids []string
	keyFn := func(h Header) (any, error) {
		seenKids = append(seenKids, h.Kid)
		switch h.Kid {
		case "2024-01":
			return keys["2024-01"], nil
		case "2025-01":
			return keys["2025-01"], nil
		}
		return nil, errUnknownKid
	}
	opts := ParseOptions{ValidAlgs: []Alg{HS256}, Now: func() time.Time { return fixedNow }}

	for kid, k := range keys {
		raw := mustSign(t, map[string]any{"sub": "u1"}, k, HS256, SignOptions{Kid: kid})
		tok := mustParse(t, raw, keyFn, opts)
		if tok.Header.Kid != kid {
			t.Errorf("Header.Kid = %q，期望 %q", tok.Header.Kid, kid)
		}
	}

	// The KeyFunc must see exactly the kid claimed by the token. | KeyFunc 必须确切看到 token 声明的 kid。
	if len(seenKids) != 2 {
		t.Fatalf("KeyFunc 调用次数 = %d，期望 2", len(seenKids))
	}

	// During rotation the old kid keeps working. | 轮换期间旧 kid 仍然可用。
	old := mustSign(t, map[string]any{"sub": "legacy"}, keys["2024-01"], HS256, SignOptions{Kid: "2024-01"})
	if tok := mustParse(t, old, keyFn, opts); tok.Claims.Subject() != "legacy" {
		t.Errorf("Claims.Subject() = %q，期望 %q", tok.Claims.Subject(), "legacy")
	}

	// Verifying the old-kid token with the new key must fail. | 用当前密钥校验旧 kid 的 token 必须失败。
	if _, err := Parse(old, hmacKeyFunc(keys["2025-01"]), ParseOptions{}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("换发密钥后的 err = %v，期望 ErrSignatureInvalid", err)
	}

	// KeyFunc errors must surface unchanged and stay errors.Is-able. | KeyFunc 的错误必须原样透出（errors.Is 可判定）。
	unknown := mustSign(t, map[string]any{"sub": "u1"}, keys["2025-01"], HS256, SignOptions{Kid: "2026-01"})
	_, err := Parse(unknown, keyFn, opts)
	if !errors.Is(err, errUnknownKid) {
		t.Errorf("未知 kid 的 err = %v，期望 errUnknownKid", err)
	}

	// A missing kid reaches the KeyFunc as an empty string. | kid 缺失时 KeyFunc 看到空串。
	seenKids = nil
	noKid := mustSign(t, map[string]any{"sub": "u1"}, testSecret, HS256, SignOptions{})
	if _, err := Parse(noKid, func(h Header) (any, error) {
		seenKids = append(seenKids, h.Kid)
		return testSecret, nil
	}, opts); err != nil {
		t.Fatalf("无 kid token 解析失败: %v", err)
	}
	if len(seenKids) != 1 || seenKids[0] != "" {
		t.Errorf("KeyFunc 看到的 kid = %v，期望 [\"\"]", seenKids)
	}

	// Custom header parameters round-trip and never override alg/typ/kid. | 自定义头部参数必须原样回传，且不得覆盖 alg/typ/kid。
	extra := mustSign(t, map[string]any{"sub": "u1"}, testSecret, HS256, SignOptions{
		Kid:    "2025-01",
		Header: map[string]string{"cty": "JWT", "x-trace-id": "t-9", "alg": "none", "typ": "evil", "kid": "evil"},
	})
	tok := mustParse(t, extra, hmacKeyFunc(testSecret), opts)
	if tok.Header.Kid != "2025-01" || tok.Header.Typ != "JWT" || tok.Header.Alg != HS256 {
		t.Errorf("保留字段被覆盖: %+v", tok.Header)
	}
	if tok.Header.Extra["cty"] != "JWT" || tok.Header.Extra["x-trace-id"] != "t-9" {
		t.Errorf("Header.Extra = %v，期望含 cty 与 x-trace-id", tok.Header.Extra)
	}
	if _, ok := tok.Header.Extra["alg"]; ok {
		t.Errorf("Header.Extra 不应含 alg: %v", tok.Header.Extra)
	}
}

// errUnknownKid is the sentinel returned by the rotation KeyFunc for an unknown kid. | errUnknownKid 是轮换 KeyFunc 对未知 kid 返回的哨兵错误。
var errUnknownKid = errors.New("unknown kid")

// ---------------------------------------------------------------- Allocation-free scanning | 零分配扫描

func TestClaimsScanDoesNotAllocate(t *testing.T) {
	raw := mustSign(t, map[string]any{
		"iss": "yd",
		"sub": "u-1",
		"aud": []string{"yd-web", "yd-api"},
		"exp": int64(4102444800),
		"nbf": int64(1),
		"n":   int64(42),
	}, testSecret, HS256, SignOptions{Kid: "alloc-kid"})

	keyFn := func(Header) (any, error) { return testSecret, nil }
	opts := ParseOptions{
		ValidAlgs:  []Alg{HS256},
		Issuer:     "yd",
		Audience:   "yd-api",
		RequireExp: true,
		Now:        func() time.Time { return fixedNow },
	}

	var (
		tok *Token
		err error
	)
	parse := func() { tok, err = Parse(raw, keyFn, opts) }
	got := testing.AllocsPerRun(500, parse)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	// The bound is the measured steady-state value plus headroom, and the measured value is what the log line
	// reports. It breaks down as four allocations this package cannot pool. | 上界取实测稳态值加上余量，日志行报告的
	// 就是实测值。实测值由本包无法池化的四次分配构成：
	//
	//   - the decoded payload and the decoded signature, both handed to the caller in the *Token; | 解码后的载荷与
	//     解码后的签名，二者都通过 *Token 交给调用方；
	//   - the *Token itself; | *Token 本身；
	//   - the kid string, an arbitrary caller-visible value; | kid 字符串，取值由调用方决定且对调用方可见；
	//   - five more from crypto/hmac.New, which allocates its state and its two inner/outer hashers internally. | 以及
	//     crypto/hmac.New 内部的五次分配：它要分配自身状态与内、外层两个哈希器。
	//
	// Buffer handling on this path is allocation-free, which the per-accessor assertions below prove one
	// accessor at a time: the header, the signing input and the MAC output all live in pooled buffers. | 该路径上的缓冲
	// 处理是零分配的，下面的逐访问器断言一一证明了这一点：头部、签名输入与 MAC 输出都位于池化缓冲中。
	const maxParseAllocs = 12
	t.Logf("jwt Parse allocations/op = %v (bound %d)", got, maxParseAllocs)
	if got > maxParseAllocs {
		t.Errorf("Parse 分配次数 = %v，期望 <= %d", got, maxParseAllocs)
	}

	// The pure scanning path must not allocate at all. | 纯扫描路径必须零分配。
	c := tok.Claims
	if n := testing.AllocsPerRun(500, func() { _, _ = c.ExpiresAt() }); n != 0 {
		t.Errorf("Claims.ExpiresAt 分配次数 = %v，期望 0", n)
	}
	if n := testing.AllocsPerRun(500, func() { _, _ = c.NotBefore() }); n != 0 {
		t.Errorf("Claims.NotBefore 分配次数 = %v，期望 0", n)
	}
	if n := testing.AllocsPerRun(500, func() { _, _ = c.IssuedAt() }); n != 0 {
		t.Errorf("Claims.IssuedAt 分配次数 = %v，期望 0", n)
	}
	if n := testing.AllocsPerRun(500, func() { _, _ = c.GetInt64("n") }); n != 0 {
		t.Errorf("Claims.GetInt64 分配次数 = %v，期望 0", n)
	}
	if n := testing.AllocsPerRun(500, func() { _, _ = c.GetFloat64("n") }); n != 0 {
		t.Errorf("Claims.GetFloat64 分配次数 = %v，期望 0", n)
	}
	if n := testing.AllocsPerRun(500, func() { _ = c.Has("iss") }); n != 0 {
		t.Errorf("Claims.Has 分配次数 = %v，期望 0", n)
	}

	// Two measurements must agree (reused buffers do not drift). | 两次测量的分配次数必须一致（复用缓冲不产生漂移）。
	first := testing.AllocsPerRun(200, parse)
	if second := testing.AllocsPerRun(200, parse); second != first {
		t.Errorf("两次测量的分配次数不一致: %v vs %v", first, second)
	}
}

// ---------------------------------------------------------------- Concurrency | 并发

// TestParseIsConcurrencySafe hammers one token from several goroutines at the same time. | TestParseIsConcurrencySafe 用多个 goroutine 同时施压同一个 token。
//
// Run under -race it proves that the shared scratch pool never hands one buffer to two callers at once, and that
// no field of a returned Token aliases pooled memory. | 在 -race 下运行可证明共享的 scratch 池不会同时把同一缓冲
// 交给两个调用方，且返回的 Token 中没有任何字段指向池化内存。
func TestParseIsConcurrencySafe(t *testing.T) {
	raw := mustSign(t, map[string]any{
		"sub": "u-conc",
		"exp": int64(4102444800),
	}, testSecret, HS256, SignOptions{Kid: "conc"})

	keyFn := hmacKeyFunc(testSecret)
	opts := ParseOptions{ValidAlgs: []Alg{HS256}, Now: func() time.Time { return fixedNow }}

	const (
		workers = 8
		rounds  = 200
	)
	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				tok, err := Parse(raw, keyFn, opts)
				if err != nil {
					failures <- err
					return
				}
				if got := tok.Claims.Subject(); got != "u-conc" {
					failures <- errors.New("Claims.Subject = " + got)
					return
				}
				if tok.Header.Kid != "conc" {
					failures <- errors.New("Header.Kid = " + tok.Header.Kid)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------- Claims accessors | 声明访问器

func TestClaimsAccessorsAndJSON(t *testing.T) {
	payload := `{"iss":"yd","sub":"u-9","jti":"id-1","aud":"single","exp":1700000100,` +
		`"n":-12,"f":2.75,"b":false,"obj":{"k":[1,2]},"nil":null,"dup":1,"dup":2}`
	raw := resign(t, b64(`{"alg":"HS256"}`), b64(payload))

	tok := mustParse(t, raw, hmacKeyFunc(testSecret), ParseOptions{Now: func() time.Time { return fixedNow }})
	c := tok.Claims

	if got := c.Issuer(); got != "yd" {
		t.Errorf("Issuer = %q", got)
	}
	if got := c.Subject(); got != "u-9" {
		t.Errorf("Subject = %q", got)
	}
	if got := c.ID(); got != "id-1" {
		t.Errorf("ID = %q", got)
	}
	// A string aud is read as a single element. | aud 为字符串时按单元素读取。
	if got := c.Audience(); len(got) != 1 || got[0] != "single" {
		t.Errorf("Audience = %v，期望 [single]", got)
	}
	if got, ok := c.GetInt64("n"); !ok || got != -12 {
		t.Errorf("GetInt64(n) = (%d, %v)", got, ok)
	}
	if got, ok := c.GetInt64("f"); ok {
		t.Errorf("GetInt64(f) = (%d, %v)，整数访问器不应接受小数", got, ok)
	}
	if got, ok := c.GetFloat64("f"); !ok || got != 2.75 {
		t.Errorf("GetFloat64(f) = (%v, %v)", got, ok)
	}
	if got, ok := c.GetBool("b"); !ok || got {
		t.Errorf("GetBool(b) = (%v, %v)", got, ok)
	}
	if _, ok := c.Get("n"); ok {
		t.Errorf("Get(n) 不应把数字当作字符串")
	}
	if got, ok := c.Get("iss"); !ok || got != "yd" {
		t.Errorf("Get(iss) = (%q, %v)", got, ok)
	}
	// Duplicate keys resolve to the last occurrence, matching jsonx map decoding. | 重复键按最后一次出现取值，与 jsonx 解到 map 的语义一致。
	if got, ok := c.GetInt64("dup"); !ok || got != 2 {
		t.Errorf("GetInt64(dup) = (%d, %v)，期望 (2, true)", got, ok)
	}
	// A null value exists but is neither string, number nor bool. | null 值视为键存在，但不是字符串/数字/布尔。
	if !c.Has("nil") {
		t.Errorf("Has(nil) 应为 true")
	}
	if _, ok := c.Get("nil"); ok {
		t.Errorf("Get(nil) 不应成功")
	}

	var obj struct {
		K []int `json:"k"`
	}
	if err := c.GetJSON("obj", &obj); err != nil {
		t.Fatalf("GetJSON(obj) 失败: %v", err)
	}
	if len(obj.K) != 2 || obj.K[0] != 1 || obj.K[1] != 2 {
		t.Errorf("GetJSON(obj).K = %v，期望 [1 2]", obj.K)
	}
	if err := c.GetJSON("missing", &obj); !errors.Is(err, ErrClaimNotFound) {
		t.Errorf("GetJSON(missing) 的 err = %v，期望 ErrClaimNotFound", err)
	}

	// Keys returns the de-duplicated keys in document order. | Keys 按文档顺序返回去重后的键。
	keys := c.Keys()
	want := []string{"iss", "sub", "jti", "aud", "exp", "n", "f", "b", "obj", "nil", "dup"}
	if len(keys) != len(want) {
		t.Fatalf("Keys() = %v，期望 %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("Keys()[%d] = %q，期望 %q（完整结果 %v）", i, keys[i], want[i], keys)
		}
	}

	// An escaped key must be equivalent to its unescaped spelling. | 编码后的键名（\uXXXX）必须与未编码形式等价。
	// The Go raw string keeps the backslash, so the payload really carries the JSON escape. | Go 原始字符串保留反斜杠，因此载荷确实带有 JSON 转义。
	esc := resign(t, b64(`{"alg":"HS256"}`), b64(`{"exp":1700000200}`))
	etok := mustParse(t, esc, hmacKeyFunc(testSecret), ParseOptions{Now: func() time.Time { return fixedNow }})
	if got, ok := etok.Claims.ExpiresAt(); !ok || !got.Equal(time.Unix(1700000200, 0).UTC()) {
		t.Errorf("转义键 exp 的 ExpiresAt = (%v, %v)", got, ok)
	}

	// An array aud is validated by membership. | aud 为数组时按成员关系校验。
	arr := resign(t, b64(`{"alg":"HS256"}`), b64(`{"aud":["a","b"]}`))
	if _, err := Parse(arr, hmacKeyFunc(testSecret), ParseOptions{Audience: "b"}); err != nil {
		t.Errorf("aud 数组成员匹配应通过，err = %v", err)
	}
	if _, err := Parse(arr, hmacKeyFunc(testSecret), ParseOptions{Audience: "c"}); !errors.Is(err, ErrAudienceMismatch) {
		t.Errorf("aud 不匹配的 err = %v，期望 ErrAudienceMismatch", err)
	}
	// Validation fails when aud is absent. | 缺少 aud 时校验必须失败。
	noAud := resign(t, b64(`{"alg":"HS256"}`), b64(`{"sub":"u1"}`))
	if _, err := Parse(noAud, hmacKeyFunc(testSecret), ParseOptions{Audience: "a"}); !errors.Is(err, ErrAudienceMismatch) {
		t.Errorf("缺少 aud 的 err = %v，期望 ErrAudienceMismatch", err)
	}
}

// TestScanHelpersMatchJSONSemantics exercises the byte-level scanner directly. | TestScanHelpersMatchJSONSemantics 直接验证字节级扫描器。
func TestScanHelpersMatchJSONSemantics(t *testing.T) {
	cases := []struct {
		name string
		json string
		key  string
		want string
		ok   bool
	}{
		{"flat", `{"a":1,"b":"x"}`, "b", `"x"`, true},
		{"nested", `{"a":{"b":{"c":1}},"d":2}`, "d", `2`, true},
		{"array value", `{"a":[1,{"b":2}],"c":3}`, "c", `3`, true},
		{"escaped quote", `{"a":"say \"hi\"","b":2}`, "b", `2`, true},
		{"missing", `{"a":1}`, "z", "", false},
		{"last wins", `{"a":1,"a":2}`, "a", `2`, true},
		{"escaped key", `{"a":1}`, "a", `1`, true},
		{"escaped key with surrogate-free escape", `{"abc":1}`, "abc", `1`, true},
		{"whitespace", "{\n\t\"a\" : 1 ,\n \"b\": 2\n}", "b", `2`, true},
		{"empty object", `{}`, "a", "", false},
		{"malformed", `{"a":1`, "a", "", false},
		{"not object", `[1,2]`, "a", "", false},
		{"trailing garbage", `{"a":1}x`, "a", "", false},
		{"double comma", `{"a":1,,"b":2}`, "b", "", false},
		{"missing colon", `{"a" 1}`, "a", "", false},
		{"unquoted key", `{a:1}`, "a", "", false},
		{"nan value", `{"a":NaN}`, "a", "", false},
		{"bare word", `{"a":nope}`, "a", "", false},
		{"good literals", `{"a":true,"b":false,"c":null}`, "c", `null`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := scanField([]byte(tc.json), tc.key)
			if ok != tc.ok {
				t.Fatalf("scanField ok = %v，期望 %v", ok, tc.ok)
			}
			if ok && string(got) != tc.want {
				t.Fatalf("scanField = %q，期望 %q", got, tc.want)
			}
		})
	}

	ints := []struct {
		in   string
		ok   bool
		want int64
	}{
		{"1300819380", true, 1300819380},
		{"-12", true, -12},
		{"0", true, 0},
		{"1700000000.5", false, 0},
		{"1e3", false, 0},
		{`"42"`, false, 0},
		{"", false, 0},
		{"-", false, 0},
		{"9223372036854775808", false, 0},
		{"-9223372036854775808", true, math.MinInt64},
	}
	for _, tc := range ints {
		got, ok := scanInt64([]byte(tc.in))
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("scanInt64(%q) = (%d, %v)，期望 (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	floats := []struct {
		in   string
		want float64
	}{
		{"1300819380", 1300819380},
		{"-12", -12},
		{"1700000000.5", 1700000000.5},
		{"1e3", 1000},
		{"1.25e-2", 0.0125},
		{"-0.5", -0.5},
	}
	for _, tc := range floats {
		got, ok := scanFloat64([]byte(tc.in))
		if !ok {
			t.Errorf("scanFloat64(%q) 失败", tc.in)
			continue
		}
		if diff := math.Abs(got - tc.want); diff > 1e-9*math.Max(1, math.Abs(tc.want)) {
			t.Errorf("scanFloat64(%q) = %v，期望 %v", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{"NaN", "nope", "", "-", `"1"`, "1.2.3", "1e"} {
		if f, ok := scanFloat64([]byte(in)); ok {
			t.Errorf("scanFloat64(%q) = %v，期望失败", in, f)
		}
	}
}

// ---------------------------------------------------------------- Middleware | 中间件

func TestMiddlewareExtractsBearerAndSetsContext(t *testing.T) {
	raw := mustSign(t, map[string]any{"sub": "u-7", "exp": int64(4102444800)}, testSecret, HS256, SignOptions{})

	var seen *Token
	handler := func(c *router.Ctx) {
		tok, ok := FromContext(c)
		if !ok {
			_ = c.String(http.StatusInternalServerError, "no token in context")
			return
		}
		seen = tok
		_ = c.String(http.StatusOK, tok.Claims.Subject())
	}
	r := router.New()
	r.GET("/me", handler, Middleware(MiddlewareOptions{
		KeyFunc:      hmacKeyFunc(testSecret),
		ParseOptions: ParseOptions{ValidAlgs: []Alg{HS256}, RequireExp: true, Now: func() time.Time { return fixedNow }},
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，正文 %q", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "u-7" {
		t.Fatalf("正文 = %q，期望 %q", rec.Body.String(), "u-7")
	}
	if seen == nil || seen.Header.Alg != HS256 {
		t.Fatalf("上下文中的 token 不正确: %+v", seen)
	}

	// A lower-case bearer scheme is accepted too. | 小写的 bearer 前缀同样接受。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "bearer "+raw)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("小写 bearer 的状态码 = %d，期望 200", rec.Code)
	}

	// A missing Authorization header yields 401 with a short body. | 缺少 Authorization 头 → 401 且带简短正文。
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("缺少 token 的状态码 = %d，期望 401", rec.Code)
	}
	if rec.Body.String() == "" {
		t.Errorf("缺少 token 时正文为空，期望简短文本")
	}

	// An invalid token yields 401 and the route handler must not run. | 非法 token → 401，且业务处理器不得执行。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer not.a.token")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("非法 token 的状态码 = %d，期望 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "u-7") {
		t.Errorf("非法 token 下业务处理器仍被调用: %q", rec.Body.String())
	}

	// A mismatched scheme prefix yields 401. | 前缀不匹配 → 401。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Basic "+raw)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("前缀不匹配的状态码 = %d，期望 401", rec.Code)
	}

	// An expired token yields 401. | 过期 token → 401。
	expired := mustSign(t, map[string]any{"exp": int64(1)}, testSecret, HS256, SignOptions{})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("过期 token 的状态码 = %d，期望 401", rec.Code)
	}

	// A custom CtxKey and ErrorHandler. | 自定义 CtxKey 与 ErrorHandler。
	var handlerErr error
	r2 := router.New()
	r2.GET("/custom", func(c *router.Ctx) {
		v, ok := c.Get("custom.token")
		tok, isToken := v.(*Token)
		if !ok || !isToken {
			_ = c.String(http.StatusInternalServerError, "missing")
			return
		}
		_ = c.String(http.StatusOK, tok.Header.Kid)
	}, Middleware(MiddlewareOptions{
		KeyFunc:      hmacKeyFunc(testSecret),
		ParseOptions: ParseOptions{ValidAlgs: []Alg{HS256}, Now: func() time.Time { return fixedNow }},
		CtxKey:       "custom.token",
		ErrorHandler: func(c *router.Ctx, err error) {
			handlerErr = err
			_ = c.String(http.StatusTeapot, "custom error")
		},
	}))
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/custom", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	r2.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("自定义 CtxKey 的状态码 = %d，期望 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/custom", nil)
	req.Header.Set("Authorization", "Bearer ")
	r2.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot || handlerErr == nil {
		t.Fatalf("自定义 ErrorHandler 未被调用: code=%d err=%v", rec.Code, handlerErr)
	}
	if !errors.Is(handlerErr, ErrTokenNotFound) {
		t.Errorf("自定义 ErrorHandler 的 err = %v，期望 ErrTokenNotFound", handlerErr)
	}

	// A nil KeyFunc must not panic; it takes the error path instead. | KeyFunc 为 nil 时不得 panic，而是走错误分支。
	r3 := router.New()
	r3.GET("/nil", handler, Middleware(MiddlewareOptions{ParseOptions: ParseOptions{ValidAlgs: []Alg{HS256}}}))
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/nil", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	r3.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("KeyFunc 为 nil 的状态码 = %d，期望 401", rec.Code)
	}
}

func TestMiddlewareTokenLookupQueryAndCookie(t *testing.T) {
	raw := mustSign(t, map[string]any{"sub": "u-q", "exp": int64(4102444800)}, testSecret, HS256, SignOptions{})
	keyFn := hmacKeyFunc(testSecret)
	parseOpts := ParseOptions{ValidAlgs: []Alg{HS256}, Now: func() time.Time { return fixedNow }}

	// Query lookup. | query 查找。
	rq := router.New()
	rq.GET("/q", echoSubject, Middleware(MiddlewareOptions{
		KeyFunc: keyFn, ParseOptions: parseOpts, TokenLookup: "query:token",
	}))
	rec := httptest.NewRecorder()
	rq.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/q?token="+raw+"&x=1", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "u-q" {
		t.Errorf("query 查找: code=%d body=%q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	rq.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/q?token=", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("query 为空: code=%d，期望 401", rec.Code)
	}

	// Cookie lookup. | cookie 查找。
	rc := router.New()
	rc.GET("/c", echoSubject, Middleware(MiddlewareOptions{
		KeyFunc: keyFn, ParseOptions: parseOpts, TokenLookup: "cookie:jwt",
	}))
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/c", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: raw})
	rc.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "u-q" {
		t.Errorf("cookie 查找: code=%d body=%q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	rc.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/c", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("无 cookie: code=%d，期望 401", rec.Code)
	}

	// A custom header with an empty prefix. | 自定义头 + 空前缀 "header:X-Token:"。
	rh := router.New()
	rh.GET("/h", echoSubject, Middleware(MiddlewareOptions{
		KeyFunc: keyFn, ParseOptions: parseOpts, TokenLookup: "header:X-Token:",
	}))
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/h", nil)
	req.Header.Set("X-Token", raw)
	rh.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "u-q" {
		t.Errorf("header:X-Token: 查找: code=%d body=%q", rec.Code, rec.Body.String())
	}
	// A bare header name without a kind prefix is treated as a header. | 裸头名（无 kind 前缀）按 header 处理。
	rb := router.New()
	rb.GET("/h", echoSubject, Middleware(MiddlewareOptions{
		KeyFunc: keyFn, ParseOptions: parseOpts, TokenLookup: "X-Token",
	}))
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/h", nil)
	req.Header.Set("X-Token", raw)
	rb.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "u-q" {
		t.Errorf("裸头名查找: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// The default spec only inspects the Authorization header. | 默认规则只看 Authorization 头。
	rd := router.New()
	rd.GET("/d", echoSubject, Middleware(MiddlewareOptions{KeyFunc: keyFn, ParseOptions: parseOpts}))
	rec = httptest.NewRecorder()
	rd.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/d?token="+raw, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("默认规则下的 query token: code=%d，期望 401", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/d", nil)
	req.AddCookie(&http.Cookie{Name: "jwt", Value: raw})
	rd.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("默认规则下的 cookie token: code=%d，期望 401", rec.Code)
	}
}

// echoSubject writes the verified subject, or 500 when the token is missing. | echoSubject 输出已校验的 sub；token 缺失时输出 500。
func echoSubject(c *router.Ctx) {
	tok, ok := FromContext(c)
	if !ok {
		_ = c.String(http.StatusInternalServerError, "missing token")
		return
	}
	_ = c.String(http.StatusOK, tok.Claims.Subject())
}

// ---------------------------------------------------------------- Malformed tokens | 畸形 token

func TestMalformedTokensTable(t *testing.T) {
	hline := b64(`{"alg":"HS256","typ":"JWT"}`)
	goodPayload := b64(`{"sub":"u1"}`)
	goodSig := b64x.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	notJSON := b64(`nope`)
	notObject := b64(`[1,2]`)

	cases := []struct {
		name  string
		token string
		want  error
	}{
		{"empty", "", ErrMalformed},
		{"blank", "   ", ErrMalformed},
		{"one segment", "abc", ErrMalformed},
		{"two segments", hline + "." + goodPayload, ErrMalformed},
		{"four segments", buildSegments(hline, goodPayload, goodSig) + ".extra", ErrMalformed},
		{"empty header", buildSegments("", goodPayload, goodSig), ErrMalformed},
		{"empty payload", buildSegments(hline, "", goodSig), ErrMalformed},
		{"empty signature", buildSegments(hline, goodPayload, ""), ErrMalformed},
		{"bad base64 header", buildSegments("@@@@", goodPayload, goodSig), ErrMalformed},
		{"bad base64 payload", buildSegments(hline, "@@@@", goodSig), ErrMalformed},
		{"bad base64 signature", buildSegments(hline, goodPayload, "@@@@"), ErrMalformed},
		{"header not json", buildSegments(notJSON, goodPayload, goodSig), ErrMalformed},
		{"header not object", buildSegments(notObject, goodPayload, goodSig), ErrMalformed},
		{"payload not json", buildSegments(hline, notJSON, goodSig), ErrMalformed},
		{"payload not object", buildSegments(hline, notObject, goodSig), ErrMalformed},
		{"payload truncated", buildSegments(hline, b64(`{"sub":`), goodSig), ErrMalformed},
		{"signature too short", buildSegments(hline, goodPayload, b64x.RawURLEncoding.EncodeToString([]byte("short"))), ErrSignatureInvalid},
		{"signature too long", buildSegments(hline, goodPayload, b64x.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 128))), ErrSignatureInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.token, hmacKeyFunc(testSecret), ParseOptions{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Parse(%q) 的 err = %v，期望 %v", tc.token, err, tc.want)
			}
		})
	}

	// Non-object payloads are always rejected (array, string, scalar, null). | 载荷不是对象时一律拒绝（数组、字符串、标量、null）。
	for _, p := range []string{`"str"`, `42`, `true`, `null`} {
		if _, err := Parse(buildSegments(hline, b64(p), goodSig), hmacKeyFunc(testSecret), ParseOptions{}); !errors.Is(err, ErrMalformed) {
			t.Errorf("载荷 %s 的 err = %v，期望 ErrMalformed", p, err)
		}
	}

	// Illegal JSON numbers such as NaN and Infinity must fail structural validation. | 非法 JSON 数值（NaN/Infinity）必须被结构校验拦下。
	for _, h := range []string{`{"alg":"HS256","n":NaN}`, `{"alg":"HS256","n":Infinity}`, `{"alg":"HS256","n":01}`} {
		if _, err := Parse(buildSegments(b64(h), goodPayload, goodSig), hmacKeyFunc(testSecret), ParseOptions{}); !errors.Is(err, ErrMalformed) {
			t.Errorf("头部 %s 的 err = %v，期望 ErrMalformed", h, err)
		}
	}

	// Two segments where the second is empty. | 只有两段且第二段为空串。
	if _, err := Parse(buildSegments(hline, "", ""), hmacKeyFunc(testSecret), ParseOptions{}); !errors.Is(err, ErrMalformed) {
		t.Errorf("两段空载荷的 err = %v，期望 ErrMalformed", err)
	}
}

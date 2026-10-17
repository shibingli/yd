package jwt

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"
)

// benchKey is the HMAC secret used by the symmetric benchmarks; it is longer than one SHA-256 block
// so the key setup follows the normal (non-optimized) HMAC path. | benchKey 是对称基准使用的 HMAC
// 密钥；它长于一个 SHA-256 块，因此密钥处理走普通（非优化）的 HMAC 路径。
var benchKey = []byte("yd-benchmark-secret-key-0123456789abcdef")

// benchTokenSink keeps parsed tokens observable so the compiler cannot elide the verification. | benchTokenSink
// 让解析结果保持可观测，避免编译器消除验签工作。
var benchTokenSink *Token

// benchClaims returns the six-claim payload shared by every benchmark. | benchClaims 返回所有基准共用的六项声明载荷。
func benchClaims(now time.Time) map[string]any {
	return map[string]any{
		"sub": "user-42",
		"iss": "yd-bench",
		"aud": "api",
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
		"jti": "7f3c1a9e-42",
	}
}

// benchParseOptions enables the recommended checks (algorithm allow list, issuer, audience, exp). | benchParseOptions
// 启用建议的校验项（算法白名单、签发者、受众、过期时间）。
func benchParseOptions(alg Alg, now time.Time) ParseOptions {
	return ParseOptions{
		ValidAlgs:  []Alg{alg},
		Issuer:     "yd-bench",
		Audience:   "api",
		RequireExp: true,
		Now:        func() time.Time { return now },
	}
}

// BenchmarkSignHS256 measures login-time token creation with the symmetric algorithm. | BenchmarkSignHS256
// 测量登录期使用对称算法创建 token 的开销。
func BenchmarkSignHS256(b *testing.B) {
	claims := benchClaims(time.Now())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Sign(claims, benchKey, HS256, SignOptions{Kid: "k1"}); err != nil {
			b.Fatalf("Sign failed | Sign 失败: %v", err)
		}
	}
}

// BenchmarkParseHS256 measures the per-request verification path with HMAC. | BenchmarkParseHS256
// 测量使用 HMAC 的每请求验签路径。
func BenchmarkParseHS256(b *testing.B) {
	now := time.Now()
	token, err := Sign(benchClaims(now), benchKey, HS256, SignOptions{Kid: "k1"})
	if err != nil {
		b.Fatalf("Sign failed | Sign 失败: %v", err)
	}
	// Box the key once: a KeyFunc that returns a []byte from a closure re-boxes the slice on every
	// call, which would add a benchmark-induced allocation per verification. | 密钥只装箱一次：
	// 若 KeyFunc 在闭包里返回 []byte，每次调用都会重新装箱切片，给验签凭空增加一次分配。
	var keyAny any = benchKey
	keyFn := func(Header) (any, error) { return keyAny, nil }
	opts := benchParseOptions(HS256, now)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tok, parseErr := Parse(token, keyFn, opts)
		if parseErr != nil {
			b.Fatalf("Parse failed | Parse 失败: %v", parseErr)
		}
		benchTokenSink = tok
	}
}

// BenchmarkParseES256 measures the per-request verification path with ECDSA P-256. | BenchmarkParseES256
// 测量使用 ECDSA P-256 的每请求验签路径。
func BenchmarkParseES256(b *testing.B) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		b.Fatalf("GenerateKey failed | GenerateKey 失败: %v", err)
	}
	now := time.Now()
	token, err := Sign(benchClaims(now), priv, ES256, SignOptions{Kid: "k1"})
	if err != nil {
		b.Fatalf("Sign failed | Sign 失败: %v", err)
	}
	pub := &priv.PublicKey
	var keyAny any = pub
	keyFn := func(Header) (any, error) { return keyAny, nil }
	opts := benchParseOptions(ES256, now)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tok, parseErr := Parse(token, keyFn, opts)
		if parseErr != nil {
			b.Fatalf("Parse failed | Parse 失败: %v", parseErr)
		}
		benchTokenSink = tok
	}
}

// BenchmarkParseHS256Parallel measures HMAC verification under concurrent load. | BenchmarkParseHS256Parallel
// 测量并发负载下的 HMAC 验签。
func BenchmarkParseHS256Parallel(b *testing.B) {
	now := time.Now()
	token, err := Sign(benchClaims(now), benchKey, HS256, SignOptions{Kid: "k1"})
	if err != nil {
		b.Fatalf("Sign failed | Sign 失败: %v", err)
	}
	opts := benchParseOptions(HS256, now)
	var keyAny any = benchKey
	keyFn := func(Header) (any, error) { return keyAny, nil }

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Parsing performs cryptography, so the call can never be eliminated; no shared sink is needed. | 解析包含密码学运算，
			// 调用不会被消除，因此无需共享槽位。
			_, _ = Parse(token, keyFn, opts)
		}
	})
}

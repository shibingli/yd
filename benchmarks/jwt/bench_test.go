package jwtbench

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	stdjwt "github.com/golang-jwt/jwt/v5"

	"github.com/shibingli/ydx/json"
	ourjwt "github.com/shibingli/ydx/jwt"
)

// selected reports whether name passes the YD_BENCH_ONLY filter. Running each
// implementation in its own process removes same-process ordering and GC bias. | selected
// 判断 name 是否通过 YD_BENCH_ONLY 过滤器。每个实现独立进程运行，消除同进程顺序与 GC 偏差。
func selected(name string) bool {
	want := os.Getenv("YD_BENCH_ONLY")
	if want == "" {
		return true
	}
	for _, part := range strings.Split(want, ",") {
		if strings.TrimSpace(part) == name {
			return true
		}
	}
	return false
}

// benchKey is the HMAC secret shared by both implementations and longer than
// one SHA-256 block, so the normal HMAC key schedule is exercised. | benchKey
// 是两个实现共用的 HMAC 密钥，长于一个 SHA-256 块，因此会走常规 HMAC 密钥调度。
var benchKey = []byte("yd-benchmark-secret-key-0123456789abcdef")

// benchSinks keep results observable. | benchSinks 让结果保持可观测。
var (
	benchStringSink string
	benchOurSink    *ourjwt.Token
	benchStdSink    *stdjwt.Token
)

// benchClaims returns the six-claim payload shared by every benchmark. | benchClaims
// 返回所有基准共用的六项声明载荷。
func benchClaims(now time.Time) map[string]any {
	return map[string]any{
		"sub": "user-42",
		"iss": "yd-bench",
		"aud": []string{"api"},
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
		"jti": "7f3c1a9e-42",
	}
}

// ourClaims is the struct form of benchClaims; signing a struct avoids map
// reflection. | ourClaims 是 benchClaims 的结构体形式；签名结构体可避开 map 反射。
type ourClaims struct {
	Subject   string   `json:"sub"`
	Issuer    string   `json:"iss"`
	Audience  []string `json:"aud"`
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp"`
	ID        string   `json:"jti"`
}

// ourStructClaims returns the struct form of benchClaims. | ourStructClaims 返回
// benchClaims 的结构体形式。
func ourStructClaims(now time.Time) ourClaims {
	return ourClaims{
		Subject:   "user-42",
		Issuer:    "yd-bench",
		Audience:  []string{"api"},
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Hour).Unix(),
		ID:        "7f3c1a9e-42",
	}
}

// ourOptions enables the recommended checks (algorithm allow list, issuer,
// audience and a required exp). | ourOptions 启用建议的校验项（算法白名单、签发者、
// 受众与必须包含 exp）。
func ourOptions(alg ourjwt.Alg, now time.Time) ourjwt.ParseOptions {
	return ourjwt.ParseOptions{
		ValidAlgs:  []ourjwt.Alg{alg},
		Issuer:     "yd-bench",
		Audience:   "api",
		RequireExp: true,
		Now:        func() time.Time { return now },
	}
}

// ourKey returns a KeyFunc whose key is boxed once. | ourKey 返回密钥只装箱一次的 KeyFunc。
func ourKey() ourjwt.KeyFunc {
	var key any = benchKey
	return func(ourjwt.Header) (any, error) { return key, nil }
}

// stdOptions mirrors ourOptions with golang-jwt's functional options. | stdOptions
// 用 golang-jwt 的函数式选项镜像 ourOptions。
func stdOptions(now time.Time) []stdjwt.ParserOption {
	return []stdjwt.ParserOption{
		stdjwt.WithValidMethods([]string{"HS256"}),
		stdjwt.WithIssuer("yd-bench"),
		stdjwt.WithAudience("api"),
		stdjwt.WithExpirationRequired(),
		stdjwt.WithTimeFunc(func() time.Time { return now }),
	}
}

// stdKey returns the verification key for golang-jwt. | stdKey 返回 golang-jwt 的校验密钥。
func stdKey(*stdjwt.Token) (any, error) { return benchKey, nil }

// mustSignOur signs claims with this project's implementation. | mustSignOur 用本项目实现签发声明。
func mustSignOur(tb testing.TB, claims map[string]any, key any, alg ourjwt.Alg, now time.Time) string {
	tb.Helper()
	token, err := ourjwt.Sign(claims, key, alg, ourjwt.SignOptions{Kid: "k1"})
	if err != nil {
		tb.Fatalf("our Sign failed | 本项目签名失败: %v", err)
	}
	return token
}

// BenchmarkSign measures login-time HMAC signing. | BenchmarkSign 测量登录期的 HMAC 签发。
func BenchmarkSign(b *testing.B) {
	now := time.Now()
	claims := benchClaims(now)

	if selected("our") {
		b.Run("our/HS256", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				token, err := ourjwt.Sign(claims, benchKey, ourjwt.HS256, ourjwt.SignOptions{Kid: "k1"})
				if err != nil {
					b.Fatalf("Sign failed | Sign 失败: %v", err)
				}
				benchStringSink = token
			}
		})
	}
	if selected("our") {
		structClaims := ourStructClaims(now)
		b.Run("our-struct/HS256", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				token, err := ourjwt.Sign(structClaims, benchKey, ourjwt.HS256, ourjwt.SignOptions{Kid: "k1"})
				if err != nil {
					b.Fatalf("Sign failed | Sign 失败: %v", err)
				}
				benchStringSink = token
			}
		})
		payload, err := json.Marshal(structClaims)
		if err != nil {
			b.Fatalf("Marshal claims failed | 序列化声明失败: %v", err)
		}
		b.Run("our-json/HS256", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				token, err := ourjwt.SignJSON(payload, benchKey, ourjwt.HS256, ourjwt.SignOptions{Kid: "k1"})
				if err != nil {
					b.Fatalf("SignJSON failed | SignJSON 失败: %v", err)
				}
				benchStringSink = token
			}
		})
	}
	if selected("golang-jwt") {
		b.Run("golang-jwt/HS256", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				token, err := stdjwt.NewWithClaims(stdjwt.SigningMethodHS256, stdjwt.MapClaims(claims)).SignedString(benchKey)
				if err != nil {
					b.Fatalf("Sign failed | Sign 失败: %v", err)
				}
				benchStringSink = token
			}
		})
	}
}

// BenchmarkParse measures per-request verification with HMAC (HS256) and ECDSA
// P-256 (ES256). Both implementations parse the same token bytes. | BenchmarkParse
// 测量每请求验签：HMAC（HS256）与 ECDSA P-256（ES256）。两个实现解析同一份 token 字节。
func BenchmarkParse(b *testing.B) {
	now := time.Now()
	claims := benchClaims(now)
	hsToken := mustSignOur(b, claims, benchKey, ourjwt.HS256, now)

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		b.Fatalf("GenerateKey failed | 生成密钥失败: %v", err)
	}
	esToken := mustSignOur(b, claims, priv, ourjwt.ES256, now)

	if selected("our") {
		b.Run("our/HS256", func(b *testing.B) {
			keyFn := ourKey()
			opts := ourOptions(ourjwt.HS256, now)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tok, parseErr := ourjwt.Parse(hsToken, keyFn, opts)
				if parseErr != nil {
					b.Fatalf("Parse failed | Parse 失败: %v", parseErr)
				}
				benchOurSink = tok
			}
		})
		b.Run("our/ES256", func(b *testing.B) {
			keyFn := func(ourjwt.Header) (any, error) { return &priv.PublicKey, nil }
			opts := ourOptions(ourjwt.ES256, now)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tok, parseErr := ourjwt.Parse(esToken, keyFn, opts)
				if parseErr != nil {
					b.Fatalf("Parse failed | Parse 失败: %v", parseErr)
				}
				benchOurSink = tok
			}
		})
	}
	if selected("golang-jwt") {
		opts := stdOptions(now)
		b.Run("golang-jwt/HS256", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tok, parseErr := stdjwt.Parse(hsToken, stdKey, opts...)
				if parseErr != nil {
					b.Fatalf("Parse failed | Parse 失败: %v", parseErr)
				}
				benchStdSink = tok
			}
		})
		b.Run("golang-jwt/ES256", func(b *testing.B) {
			stdOpts := append([]stdjwt.ParserOption{stdjwt.WithValidMethods([]string{"ES256"})},
				stdjwt.WithIssuer("yd-bench"), stdjwt.WithAudience("api"),
				stdjwt.WithExpirationRequired(), stdjwt.WithTimeFunc(func() time.Time { return now }))
			keyFn := func(*stdjwt.Token) (any, error) { return &priv.PublicKey, nil }
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				tok, parseErr := stdjwt.Parse(esToken, keyFn, stdOpts...)
				if parseErr != nil {
					b.Fatalf("Parse failed | Parse 失败: %v", parseErr)
				}
				benchStdSink = tok
			}
		})
	}
}

// BenchmarkParseParallel measures aggregate verification throughput under
// concurrent load. The our-reuse mode returns Tokens to the pool; the other
// modes keep the default ownership model. | BenchmarkParseParallel 测量并发负载下的
// 聚合验签吞吐。our-reuse 模式把 Token 归还池；其余模式保持默认所有权模型。
func BenchmarkParseParallel(b *testing.B) {
	now := time.Now()
	hsToken := mustSignOur(b, benchClaims(now), benchKey, ourjwt.HS256, now)

	if selected("our") {
		keyFn := ourKey()
		opts := ourOptions(ourjwt.HS256, now)
		b.Run("our/HS256", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := ourjwt.Parse(hsToken, keyFn, opts); err != nil {
						b.Fatalf("Parse failed | Parse 失败: %v", err)
					}
				}
			})
		})
		b.Run("our-reuse/HS256", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					tok, err := ourjwt.Parse(hsToken, keyFn, opts)
					if err != nil {
						b.Fatalf("Parse failed | Parse 失败: %v", err)
					}
					tok.Release()
				}
			})
		})
	}
	if selected("golang-jwt") {
		stdOpts := stdOptions(now)
		b.Run("golang-jwt/HS256", func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := stdjwt.Parse(hsToken, stdKey, stdOpts...); err != nil {
						b.Fatalf("Parse failed | Parse 失败: %v", err)
					}
				}
			})
		})
	}
}

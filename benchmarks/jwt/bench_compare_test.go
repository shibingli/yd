package jwtbench

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	cristal "github.com/cristalhq/jwt/v5"
	kjwt "github.com/kataras/jwt"

	ourjwt "github.com/shibingli/yd/security/jwt"
)

// cristalHS builds the cristalhq signer, verifier and registered claims for
// HS256. | cristalHS 构造 cristalhq 的 HS256 签发器、校验器与登记声明。
func cristalHS(now time.Time) (*cristal.Builder, cristal.Verifier, cristal.RegisteredClaims, error) {
	signer, err := cristal.NewSignerHS(cristal.HS256, benchKey)
	if err != nil {
		return nil, nil, cristal.RegisteredClaims{}, err
	}
	verifier, err := cristal.NewVerifierHS(cristal.HS256, benchKey)
	if err != nil {
		return nil, nil, cristal.RegisteredClaims{}, err
	}
	claims := cristal.RegisteredClaims{
		Subject:   "user-42",
		Issuer:    "yd-bench",
		Audience:  cristal.Audience{"api"},
		ExpiresAt: cristal.NewNumericDate(now.Add(time.Hour)),
		IssuedAt:  cristal.NewNumericDate(now),
		ID:        "7f3c1a9e-42",
	}
	return cristal.NewBuilder(signer, cristal.WithKeyID("k1")), verifier, claims, nil
}

// cristalES builds the cristalhq signer, verifier and registered claims for
// ES256. | cristalES 构造 cristalhq 的 ES256 签发器、校验器与登记声明。
func cristalES(now time.Time, priv *ecdsa.PrivateKey) (*cristal.Builder, cristal.Verifier, cristal.RegisteredClaims, error) {
	signer, err := cristal.NewSignerES(cristal.ES256, priv)
	if err != nil {
		return nil, nil, cristal.RegisteredClaims{}, err
	}
	verifier, err := cristal.NewVerifierES(cristal.ES256, &priv.PublicKey)
	if err != nil {
		return nil, nil, cristal.RegisteredClaims{}, err
	}
	claims := cristal.RegisteredClaims{
		Subject:   "user-42",
		Issuer:    "yd-bench",
		Audience:  cristal.Audience{"api"},
		ExpiresAt: cristal.NewNumericDate(now.Add(time.Hour)),
		IssuedAt:  cristal.NewNumericDate(now),
		ID:        "7f3c1a9e-42",
	}
	return cristal.NewBuilder(signer, cristal.WithKeyID("k1")), verifier, claims, nil
}

// BenchmarkSignCristal measures cristalhq's HMAC signing. | BenchmarkSignCristal
// 测量 cristalhq 的 HMAC 签发。
func BenchmarkSignCristal(b *testing.B) {
	if !selected("cristal") {
		return
	}
	now := time.Now()
	builder, _, claims, err := cristalHS(now)
	if err != nil {
		b.Fatalf("cristal setup: %v", err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		token, err := builder.Build(claims)
		if err != nil {
			b.Fatalf("Build: %v", err)
		}
		benchStringSink = token.String()
	}
}

// BenchmarkParseCristal measures cristalhq's full verify + claims path for
// HS256 and ES256 using the shared token bytes. | BenchmarkParseCristal 测量
// cristalhq 在 HS256/ES256 上的完整验签 + 声明路径，输入为共用 token 字节。
func BenchmarkParseCristal(b *testing.B) {
	if !selected("cristal") {
		return
	}
	now := time.Now()
	hsToken := mustSignOur(b, benchClaims(now), benchKey, ourjwt.HS256, now)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		b.Fatalf("GenerateKey: %v", err)
	}
	esToken := mustSignOur(b, benchClaims(now), priv, ourjwt.ES256, now)

	b.Run("HS256", func(b *testing.B) {
		_, verifier, _, err := cristalHS(now)
		if err != nil {
			b.Fatalf("cristal setup: %v", err)
		}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var claims cristal.RegisteredClaims
			if err := cristal.ParseClaims([]byte(hsToken), verifier, &claims); err != nil {
				b.Fatalf("ParseClaims: %v", err)
			}
			if claims.ExpiresAt == nil || !claims.IsValidAt(now) || !claims.IsIssuer("yd-bench") || !claims.IsForAudience("api") {
				b.Fatal("claim validation failed | 声明校验失败")
			}
		}
	})
	b.Run("ES256", func(b *testing.B) {
		_, verifier, _, err := cristalES(now, priv)
		if err != nil {
			b.Fatalf("cristal setup: %v", err)
		}
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var claims cristal.RegisteredClaims
			if err := cristal.ParseClaims([]byte(esToken), verifier, &claims); err != nil {
				b.Fatalf("ParseClaims: %v", err)
			}
			if claims.ExpiresAt == nil || !claims.IsValidAt(now) || !claims.IsIssuer("yd-bench") || !claims.IsForAudience("api") {
				b.Fatal("claim validation failed | 声明校验失败")
			}
		}
	})
}

// BenchmarkParseParallelCristal measures cristalhq's HS256 verification under
// concurrent load. | BenchmarkParseParallelCristal 测量 cristalhq 的 HS256 并发验签。
func BenchmarkParseParallelCristal(b *testing.B) {
	if !selected("cristal") {
		return
	}
	now := time.Now()
	hsToken := mustSignOur(b, benchClaims(now), benchKey, ourjwt.HS256, now)
	_, verifier, _, err := cristalHS(now)
	if err != nil {
		b.Fatalf("cristal setup: %v", err)
	}
	raw := []byte(hsToken)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var claims cristal.RegisteredClaims
			if err := cristal.ParseClaims(raw, verifier, &claims); err != nil {
				b.Fatalf("ParseClaims: %v", err)
			}
			if !claims.IsValidAt(now) {
				b.Fatal("claim validation failed | 声明校验失败")
			}
		}
	})
}

// katarasClaims is the custom-claim shape used by the kataras benchmarks. | katarasClaims
// 是 kataras 基准使用的自定义声明形状。
type katarasClaims struct {
	Subject string `json:"sub"`
	ID      string `json:"jti"`
}

// katarasSign signs the shared claims with kataras' recommended options. | katarasSign
// 用 kataras 的推荐选项签发共用声明。
func katarasSign(tb testing.TB, key any, alg kjwt.Alg) []byte {
	tb.Helper()
	token, err := kjwt.Sign(alg, key, katarasClaims{Subject: "user-42", ID: "7f3c1a9e-42"},
		kjwt.MaxAge(time.Hour),
		kjwt.Claims{Issuer: "yd-bench", Audience: kjwt.Audience{"api"}})
	if err != nil {
		tb.Fatalf("kataras Sign: %v", err)
	}
	return token
}

// katarasVerify verifies a token and extracts the custom claims. | katarasVerify
// 校验 token 并提取自定义声明。
func katarasVerify(tb testing.TB, key any, alg kjwt.Alg, token []byte) {
	tb.Helper()
	verified, err := kjwt.Verify(alg, key, token,
		kjwt.Expected{Issuer: "yd-bench", Audience: kjwt.Audience{"api"}},
		kjwt.RequireExpiry)
	if err != nil {
		tb.Fatalf("kataras Verify: %v", err)
	}
	var claims katarasClaims
	if err := verified.Claims(&claims); err != nil {
		tb.Fatalf("kataras Claims: %v", err)
	}
}

// BenchmarkSignKataras measures kataras' HMAC signing. | BenchmarkSignKataras
// 测量 kataras 的 HMAC 签发。
func BenchmarkSignKataras(b *testing.B) {
	if !selected("kataras") {
		return
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		token := katarasSign(b, benchKey, kjwt.HS256)
		benchStringSink = string(token)
	}
}

// BenchmarkParseKataras measures kataras' full verify + claims path for HS256
// and ES256 using the shared token bytes. | BenchmarkParseKataras 测量 kataras 在
// HS256/ES256 上的完整验签 + 声明路径，输入为共用 token 字节。
func BenchmarkParseKataras(b *testing.B) {
	if !selected("kataras") {
		return
	}
	// kataras pins the exact canonical header bytes, so it must verify a token
	// it minted itself (its header carries no kid). | kataras 会精确比对规范头部
	// 字节，因此必须校验它自己签发的 token（其头部不含 kid）。
	hsToken := katarasSign(b, benchKey, kjwt.HS256)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		b.Fatalf("GenerateKey: %v", err)
	}
	esToken := katarasSign(b, priv, kjwt.ES256)

	b.Run("HS256", func(b *testing.B) {
		token := []byte(hsToken)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			katarasVerify(b, benchKey, kjwt.HS256, token)
		}
	})
	b.Run("ES256", func(b *testing.B) {
		token := []byte(esToken)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			katarasVerify(b, &priv.PublicKey, kjwt.ES256, token)
		}
	})
}

// BenchmarkParseParallelKataras measures kataras' HS256 verification under
// concurrent load. | BenchmarkParseParallelKataras 测量 kataras 的 HS256 并发验签。
func BenchmarkParseParallelKataras(b *testing.B) {
	if !selected("kataras") {
		return
	}
	token := []byte(katarasSign(b, benchKey, kjwt.HS256))
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			verified, err := kjwt.Verify(kjwt.HS256, benchKey, token,
				kjwt.Expected{Issuer: "yd-bench", Audience: kjwt.Audience{"api"}},
				kjwt.RequireExpiry)
			if err != nil {
				b.Fatalf("kataras Verify: %v", err)
			}
			var claims katarasClaims
			if err := verified.Claims(&claims); err != nil {
				b.Fatalf("kataras Claims: %v", err)
			}
		}
	})
}

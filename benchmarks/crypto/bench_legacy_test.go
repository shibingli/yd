package cryptobench

import (
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"fmt"
	"testing"

	tsm2 "github.com/tjfoc/gmsm/sm2"
	tsm3 "github.com/tjfoc/gmsm/sm3"
	tsm4 "github.com/tjfoc/gmsm/sm4"
	"golang.org/x/crypto/pbkdf2"
)

// The legacy benchmarks pin the previous backend (tjfoc/gmsm v1.4.1) so the
// impact of the switch to the maintained implementation stays measurable.
// | 遗留基准锁定旧后端（tjfoc/gmsm v1.4.1），使切换到维护版实现的效果始终可量化。

// BenchmarkSM3Legacy measures the previous SM3 backend. | BenchmarkSM3Legacy
// 测量旧版 SM3 后端。
func BenchmarkSM3Legacy(b *testing.B) {
	if !selected("tjfoc") {
		return
	}
	for _, size := range []int{4 << 10, 64 << 10} {
		data := benchData(size)
		b.Run(sizeName(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				copy(benchDigest[:], tsm3.Sm3Sum(data))
			}
		})
	}
}

// BenchmarkHMACLegacy measures the previous HMAC-SM3 path with a precomputed
// key schedule. | BenchmarkHMACLegacy 用预计算密钥编排测量旧版 HMAC-SM3 路径。
func BenchmarkHMACLegacy(b *testing.B) {
	if !selected("tjfoc") {
		return
	}
	key := benchData(32)
	data := benchData(256)
	mac := hmac.New(tsm3.New, key)
	buf := make([]byte, 0, mac.Size())
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mac.Reset()
		_, _ = mac.Write(data)
		buf = mac.Sum(buf[:0])
	}
	benchBytes = buf
}

// BenchmarkPBKDF2Legacy measures PBKDF2-HMAC-SM3 with the previous backend.
// | BenchmarkPBKDF2Legacy 用旧后端测量 PBKDF2-HMAC-SM3。
func BenchmarkPBKDF2Legacy(b *testing.B) {
	if !selected("tjfoc") {
		return
	}
	password := benchData(16)
	salt := benchData(16)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchBytes = pbkdf2.Key(password, salt, 1000, 32, tsm3.New)
	}
}

// BenchmarkSM4GCMLegacy measures the previous SM4-GCM backend on a reused AEAD.
// | BenchmarkSM4GCMLegacy 在复用 AEAD 上测量旧版 SM4-GCM 后端。
func BenchmarkSM4GCMLegacy(b *testing.B) {
	if !selected("tjfoc") {
		return
	}
	key := benchData(16)
	nonce := benchData(12)
	aad := benchData(32)
	for _, size := range []int{256, 4 << 10} {
		plain := benchData(size)
		block, err := tsm4.NewCipher(key)
		if err != nil {
			b.Fatalf("tsm4.NewCipher: %v", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			b.Fatalf("cipher.NewGCM: %v", err)
		}
		ct := aead.Seal(nil, nonce, plain, aad)
		b.Run(sizeName(size)+"/seal", func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchBytes = aead.Seal(nil, nonce, plain, aad)
			}
		})
		b.Run(sizeName(size)+"/open", func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, openErr := aead.Open(nil, nonce, ct, aad)
				if openErr != nil {
					b.Fatalf("open: %v", openErr)
				}
				benchBytes = out
			}
		})
	}
}

// BenchmarkSM2Legacy measures the previous SM2 backend for signing,
// verification and encryption. | BenchmarkSM2Legacy 测量旧版 SM2 后端的签名、
// 验签与加密。
func BenchmarkSM2Legacy(b *testing.B) {
	if !selected("tjfoc") {
		return
	}
	data := benchData(32)
	priv, err := tsm2.GenerateKey(rand.Reader)
	if err != nil {
		b.Fatalf("tsm2.GenerateKey: %v", err)
	}
	pub := &priv.PublicKey
	sig, err := priv.Sign(rand.Reader, data, nil)
	if err != nil {
		b.Fatalf("tsm2.Sign: %v", err)
	}
	ct, err := tsm2.Encrypt(pub, data, rand.Reader, tsm2.C1C3C2)
	if err != nil {
		b.Fatalf("tsm2.Encrypt: %v", err)
	}

	b.Run("sign", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out, signErr := priv.Sign(rand.Reader, data, nil)
			if signErr != nil {
				b.Fatalf("sign: %v", signErr)
			}
			benchBytes = out
		}
	})
	b.Run("verify", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if !pub.Verify(data, sig) {
				b.Fatal("verify failed | 验签失败")
			}
		}
	})
	b.Run("encrypt", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out, encErr := tsm2.Encrypt(pub, data, rand.Reader, tsm2.C1C3C2)
			if encErr != nil {
				b.Fatalf("encrypt: %v", encErr)
			}
			benchBytes = out
		}
	})
	b.Run("decrypt", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out, decErr := tsm2.Decrypt(priv, ct, tsm2.C1C3C2)
			if decErr != nil {
				b.Fatalf("decrypt: %v", decErr)
			}
			benchBytes = out
		}
	})
}

// sizeName renders a payload size for a benchmark name. | sizeName 把载荷大小渲染为基准名称。
func sizeName(size int) string {
	if size >= 1<<10 {
		return fmt.Sprintf("%dKiB", size>>10)
	}
	return fmt.Sprintf("%dB", size)
}

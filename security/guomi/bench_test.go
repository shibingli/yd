package guomi

import (
	"fmt"
	"testing"
)

// benchSink keeps benchmark outputs observable. | benchSink 让基准输出保持可观测。
var benchSink []byte

// benchSum keeps a fixed-size digest observable without forcing it to escape to the heap. | benchSum
// 让定长摘要保持可观测，同时不会迫使它逃逸到堆上。
var benchSum [SM3Size]byte

// benchData returns n deterministic non-zero bytes. | benchData 返回 n 个确定性的非零字节。
func benchData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i%251 + 1)
	}
	return b
}

// BenchmarkSM3Sum measures the one-shot SM3 digest. | BenchmarkSM3Sum 测量一次性 SM3 摘要。
func BenchmarkSM3Sum(b *testing.B) {
	for _, size := range []int{64, 4 << 10, 64 << 10} {
		data := benchData(size)
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSum = Sum(data)
			}
		})
	}
}

// BenchmarkHMACSM3 measures HMAC-SM3 over a short message. | BenchmarkHMACSM3 测量短消息上的 HMAC-SM3。
func BenchmarkHMACSM3(b *testing.B) {
	key := benchData(32)
	data := benchData(256)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSum = HMAC(key, data)
	}
}

// BenchmarkPBKDF2 measures PBKDF2-HMAC-SM3 with 1000 iterations (the default password hash uses
// 100000; the lower count keeps the benchmark fast while exposing per-iteration allocations). | BenchmarkPBKDF2
// 测量 1000 次迭代的 PBKDF2-HMAC-SM3（默认口令散列使用 100000 次；较低次数让基准更快，
// 同时暴露每轮分配）。
func BenchmarkPBKDF2(b *testing.B) {
	password := []byte("correct horse battery staple")
	salt := benchData(16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSink = PBKDF2(password, salt, 1000, SM3Size)
	}
}

// BenchmarkSM4CBC measures SM4-CBC encryption and decryption at two payload sizes. | BenchmarkSM4CBC
// 测量两种载荷大小下的 SM4-CBC 加解密。
func BenchmarkSM4CBC(b *testing.B) {
	key := benchData(SM4KeySize)
	iv := benchData(SM4IVSize)
	for _, size := range []int{256, 4 << 10} {
		plain := benchData(size)
		b.Run(fmt.Sprintf("encrypt/plain=%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := EncryptCBC(key, iv, plain)
				if err != nil {
					b.Fatalf("EncryptCBC failed | EncryptCBC 失败: %v", err)
				}
				benchSink = out
			}
		})
		ct, err := EncryptCBC(key, iv, plain)
		if err != nil {
			b.Fatalf("setup failed | 准备失败: %v", err)
		}
		b.Run(fmt.Sprintf("decrypt/plain=%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, decErr := DecryptCBC(key, iv, ct)
				if decErr != nil {
					b.Fatalf("DecryptCBC failed | DecryptCBC 失败: %v", decErr)
				}
				benchSink = out
			}
		})
	}
}

// BenchmarkSM4GCM measures SM4-GCM Seal and Open with the recommended 12-byte nonce. | BenchmarkSM4GCM
// 测量推荐 12 字节 nonce 下的 SM4-GCM Seal 与 Open。
func BenchmarkSM4GCM(b *testing.B) {
	key := benchData(SM4KeySize)
	nonce := benchData(SM4GCMNonceSize)
	aad := benchData(32)
	for _, size := range []int{256, 4 << 10} {
		plain := benchData(size)
		b.Run(fmt.Sprintf("seal/plain=%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := SealGCM(key, nonce, plain, aad)
				if err != nil {
					b.Fatalf("SealGCM failed | SealGCM 失败: %v", err)
				}
				benchSink = out
			}
		})
		ct, err := SealGCM(key, nonce, plain, aad)
		if err != nil {
			b.Fatalf("setup failed | 准备失败: %v", err)
		}
		aead, err := NewGCM(key)
		if err != nil {
			b.Fatalf("NewGCM failed | NewGCM 失败: %v", err)
		}
		b.Run(fmt.Sprintf("seal_reused/plain=%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchSink = aead.Seal(nil, nonce, plain, aad)
			}
		})
		b.Run(fmt.Sprintf("open_reused/plain=%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, openErr := aead.Open(nil, nonce, ct, aad)
				if openErr != nil {
					b.Fatalf("aead.Open failed | aead.Open 失败: %v", openErr)
				}
				benchSink = out
			}
		})
		b.Run(fmt.Sprintf("open/plain=%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, openErr := OpenGCM(key, nonce, ct, aad)
				if openErr != nil {
					b.Fatalf("OpenGCM failed | OpenGCM 失败: %v", openErr)
				}
				benchSink = out
			}
		})
	}
}

// BenchmarkSM2 measures key generation, signing and verification. | BenchmarkSM2 测量密钥生成、签名与验签。
func BenchmarkSM2(b *testing.B) {
	priv, err := GenerateKey()
	if err != nil {
		b.Fatalf("GenerateKey failed | GenerateKey 失败: %v", err)
	}
	pub := priv.Public()
	data := benchData(32)
	sig, err := Sign(priv, data)
	if err != nil {
		b.Fatalf("Sign failed | Sign 失败: %v", err)
	}

	b.Run("keygen", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, genErr := GenerateKey(); genErr != nil {
				b.Fatalf("GenerateKey failed | GenerateKey 失败: %v", genErr)
			}
		}
	})
	b.Run("sign", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			out, signErr := Sign(priv, data)
			if signErr != nil {
				b.Fatalf("Sign failed | Sign 失败: %v", signErr)
			}
			benchSink = out
		}
	})
	b.Run("verify", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ok, verifyErr := Verify(pub, data, sig)
			if verifyErr != nil || !ok {
				b.Fatalf("Verify failed | Verify 失败: ok=%v err=%v", ok, verifyErr)
			}
		}
	})
}

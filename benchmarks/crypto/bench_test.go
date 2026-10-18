package cryptobench

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	esm2 "github.com/emmansun/gmsm/sm2"
	esm3 "github.com/emmansun/gmsm/sm3"
	esm4 "github.com/emmansun/gmsm/sm4"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/pbkdf2"

	"github.com/shibingli/yd/security/guomi"
)

// selected reports whether name passes the YD_BENCH_ONLY filter. | selected
// 判断 name 是否通过 YD_BENCH_ONLY 过滤器。
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

// benchData returns n deterministic non-zero bytes. | benchData 返回 n 个确定性的非零字节。
func benchData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i%251 + 1)
	}
	return b
}

var (
	benchBytes  []byte
	benchDigest [32]byte
	benchOK     bool
)

// BenchmarkSM3 compares SM3 with SHA-256 across payload sizes. | BenchmarkSM3
// 在不同载荷大小下对比 SM3 与 SHA-256。
func BenchmarkSM3(b *testing.B) {
	for _, size := range []int{64, 4 << 10, 64 << 10} {
		data := benchData(size)
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			if selected("our") {
				b.Run("our", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchDigest = guomi.Sum(data)
					}
				})
			}
			if selected("emmansun") {
				b.Run("emmansun", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchDigest = esm3.Sum(data)
					}
				})
			}
			if selected("stdlib") {
				b.Run("sha256", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchDigest = sha256.Sum256(data)
					}
				})
			}
		})
	}
}

// BenchmarkHMAC compares HMAC-SM3 wrappers and HMAC-SHA256. | BenchmarkHMAC
// 对比 HMAC-SM3 封装与 HMAC-SHA256。
func BenchmarkHMAC(b *testing.B) {
	key := benchData(32)
	data := benchData(256)
	b.SetBytes(int64(len(data)))
	if selected("our") {
		b.Run("our-sm3", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchDigest = guomi.HMAC(key, data)
			}
		})
		// The reusable form precomputes the key schedule, matching the way the
		// upstream benchmarks call crypto/hmac. | 可复用形式会预计算密钥编排，与上游基准
		// 调用 crypto/hmac 的方式一致。
		b.Run("our-reuse", func(b *testing.B) {
			mac := hmac.New(guomi.New, key)
			buf := make([]byte, 0, mac.Size())
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				mac.Reset()
				_, _ = mac.Write(data)
				buf = mac.Sum(buf[:0])
			}
			benchBytes = buf
		})
	}
	if selected("emmansun") {
		b.Run("emmansun-sm3", func(b *testing.B) {
			mac := hmac.New(esm3.New, key)
			buf := make([]byte, 0, mac.Size())
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				mac.Reset()
				_, _ = mac.Write(data)
				buf = mac.Sum(buf[:0])
			}
			benchBytes = buf
		})
	}
	if selected("stdlib") {
		b.Run("sha256", func(b *testing.B) {
			mac := hmac.New(sha256.New, key)
			buf := make([]byte, 0, mac.Size())
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				mac.Reset()
				_, _ = mac.Write(data)
				buf = mac.Sum(buf[:0])
			}
			benchBytes = buf
		})
	}
}

// BenchmarkPBKDF2 compares password key derivation with 1000 iterations.
// | BenchmarkPBKDF2 以 1000 次迭代对比口令密钥派生。
func BenchmarkPBKDF2(b *testing.B) {
	password := benchData(16)
	salt := benchData(16)
	const iter = 1000
	const keyLen = 32

	if selected("our") {
		b.Run("our-sm3", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchBytes = guomi.PBKDF2(password, salt, iter, keyLen)
			}
		})
	}
	if selected("emmansun") {
		b.Run("emmansun-sm3", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchBytes = pbkdf2.Key(password, salt, iter, keyLen, esm3.New)
			}
		})
	}
	if selected("stdlib") {
		b.Run("sha256", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchBytes = pbkdf2.Key(password, salt, iter, keyLen, sha256.New)
			}
		})
	}
}

// BenchmarkSM4GCM compares SM4-GCM with AES-GCM and ChaCha20-Poly1305 on a
// reused AEAD. | BenchmarkSM4GCM 在复用 AEAD 上对比 SM4-GCM、AES-GCM 与
// ChaCha20-Poly1305。
func BenchmarkSM4GCM(b *testing.B) {
	key16 := benchData(guomi.SM4KeySize)
	key32 := benchData(32)
	nonce := benchData(guomi.SM4GCMNonceSize)
	aad := benchData(32)
	for _, size := range []int{256, 4 << 10} {
		plain := benchData(size)
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.SetBytes(int64(size))
			if selected("our") {
				aead, err := guomi.NewGCM(key16)
				if err != nil {
					b.Fatalf("guomi.NewGCM: %v", err)
				}
				ct := aead.Seal(nil, nonce, plain, aad)
				b.Run("our-seal", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchBytes = aead.Seal(nil, nonce, plain, aad)
					}
				})
				b.Run("our-open", func(b *testing.B) {
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
			if selected("emmansun") {
				block, err := esm4.NewCipher(key16)
				if err != nil {
					b.Fatalf("esm4.NewCipher: %v", err)
				}
				aead, err := cipher.NewGCM(block)
				if err != nil {
					b.Fatalf("cipher.NewGCM: %v", err)
				}
				ct := aead.Seal(nil, nonce, plain, aad)
				b.Run("emmansun-seal", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchBytes = aead.Seal(nil, nonce, plain, aad)
					}
				})
				b.Run("emmansun-open", func(b *testing.B) {
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
			if selected("stdlib") {
				block, err := aes.NewCipher(key16)
				if err != nil {
					b.Fatalf("aes.NewCipher: %v", err)
				}
				aead, err := cipher.NewGCM(block)
				if err != nil {
					b.Fatalf("cipher.NewGCM: %v", err)
				}
				ct := aead.Seal(nil, nonce, plain, aad)
				b.Run("aes-gcm-seal", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchBytes = aead.Seal(nil, nonce, plain, aad)
					}
				})
				b.Run("aes-gcm-open", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						out, openErr := aead.Open(nil, nonce, ct, aad)
						if openErr != nil {
							b.Fatalf("open: %v", openErr)
						}
						benchBytes = out
					}
				})
				chacha, err := chacha20poly1305.New(key32)
				if err != nil {
					b.Fatalf("chacha20poly1305.New: %v", err)
				}
				ct = chacha.Seal(nil, nonce, plain, aad)
				b.Run("chacha20-seal", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchBytes = chacha.Seal(nil, nonce, plain, aad)
					}
				})
				b.Run("chacha20-open", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						out, openErr := chacha.Open(nil, nonce, ct, aad)
						if openErr != nil {
							b.Fatalf("open: %v", openErr)
						}
						benchBytes = out
					}
				})
			}
		})
	}
}

// BenchmarkSM4CBC compares SM4-CBC encryption and decryption at 4 KiB. The
// framework wrapper includes PKCS#7 padding; the emmansun path is raw CBC of
// an already aligned buffer, so compare throughput, not exact bytes.
// | BenchmarkSM4CBC 在 4 KiB 上对比 SM4-CBC 加解密。框架封装包含 PKCS#7 填充；
// emmansun 路径对已对齐缓冲做原始 CBC，因此只比较吞吐而非逐字节结果。
func BenchmarkSM4CBC(b *testing.B) {
	key := benchData(guomi.SM4KeySize)
	iv := benchData(guomi.SM4IVSize)
	const size = 4 << 10
	plain := benchData(size)
	b.SetBytes(size)

	if selected("our") {
		ct, err := guomi.EncryptCBC(key, iv, plain)
		if err != nil {
			b.Fatalf("EncryptCBC: %v", err)
		}
		b.Run("our-encrypt", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, encErr := guomi.EncryptCBC(key, iv, plain)
				if encErr != nil {
					b.Fatalf("encrypt: %v", encErr)
				}
				benchBytes = out
			}
		})
		b.Run("our-decrypt", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, decErr := guomi.DecryptCBC(key, iv, ct)
				if decErr != nil {
					b.Fatalf("decrypt: %v", decErr)
				}
				benchBytes = out
			}
		})
	}
	if selected("emmansun") {
		block, err := esm4.NewCipher(key)
		if err != nil {
			b.Fatalf("esm4.NewCipher: %v", err)
		}
		dst := make([]byte, size)
		b.Run("emmansun-encrypt", func(b *testing.B) {
			enc := benchCBCEncrypter(block, iv)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				enc.CryptBlocks(dst, plain)
			}
			benchBytes = dst
		})
		enc := benchCBCEncrypter(block, iv)
		ct := make([]byte, size)
		enc.CryptBlocks(ct, plain)
		dec := benchCBCDecrypter(block, iv)
		b.Run("emmansun-decrypt", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				dec.CryptBlocks(dst, ct)
			}
			benchBytes = dst
		})
	}
}

// benchCBCEncrypter prefers the cipher's own CBC mode, mirroring what
// security/guomi does. | benchCBCEncrypter 优先使用底层密码自有的 CBC 模式，与
// security/guomi 的行为一致。
func benchCBCEncrypter(block cipher.Block, iv []byte) cipher.BlockMode {
	if b, ok := block.(interface{ NewCBCEncrypter([]byte) cipher.BlockMode }); ok {
		return b.NewCBCEncrypter(iv)
	}
	return cipher.NewCBCEncrypter(block, iv)
}

// benchCBCDecrypter is the decryption counterpart of benchCBCEncrypter. | benchCBCDecrypter
// 是 benchCBCEncrypter 的解密对应物。
func benchCBCDecrypter(block cipher.Block, iv []byte) cipher.BlockMode {
	if b, ok := block.(interface{ NewCBCDecrypter([]byte) cipher.BlockMode }); ok {
		return b.NewCBCDecrypter(iv)
	}
	return cipher.NewCBCDecrypter(block, iv)
}

// BenchmarkSM2 compares SM2 key generation, signing and verification with the
// maintained gmsm implementation and with ECDSA P-256 as the standard
// reference. | BenchmarkSM2 对比 SM2 密钥生成、签名与验签在维护版 gmsm 与
// ECDSA P-256（常密参照）下的表现。
func BenchmarkSM2(b *testing.B) {
	data := benchData(32)
	digest := sha256.Sum256(data)

	if selected("our") {
		priv, err := guomi.GenerateKey()
		if err != nil {
			b.Fatalf("guomi.GenerateKey: %v", err)
		}
		pub := priv.Public()
		sig, err := guomi.Sign(priv, data)
		if err != nil {
			b.Fatalf("guomi.Sign: %v", err)
		}
		b.Run("our-keygen", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, genErr := guomi.GenerateKey(); genErr != nil {
					b.Fatalf("keygen: %v", genErr)
				}
			}
		})
		b.Run("our-sign", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, signErr := guomi.Sign(priv, data)
				if signErr != nil {
					b.Fatalf("sign: %v", signErr)
				}
				benchBytes = out
			}
		})
		b.Run("our-verify", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				ok, verifyErr := guomi.Verify(pub, data, sig)
				if verifyErr != nil || !ok {
					b.Fatalf("verify: ok=%v err=%v", ok, verifyErr)
				}
			}
		})
	}
	if selected("emmansun") {
		priv, err := esm2.GenerateKey(rand.Reader)
		if err != nil {
			b.Fatalf("esm2.GenerateKey: %v", err)
		}
		sig, err := priv.SignMessage(rand.Reader, data, esm2.DefaultSM2SignerOpts)
		if err != nil {
			b.Fatalf("esm2.SignMessage: %v", err)
		}
		b.Run("emmansun-keygen", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, genErr := esm2.GenerateKey(rand.Reader); genErr != nil {
					b.Fatalf("keygen: %v", genErr)
				}
			}
		})
		b.Run("emmansun-sign", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, signErr := priv.SignMessage(rand.Reader, data, esm2.DefaultSM2SignerOpts)
				if signErr != nil {
					b.Fatalf("sign: %v", signErr)
				}
				benchBytes = out
			}
		})
		b.Run("emmansun-verify", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchOK = esm2.VerifyASN1WithSM2(&priv.PublicKey, nil, data, sig)
				if !benchOK {
					b.Fatal("verify failed | 验签失败")
				}
			}
		})
	}
	if selected("stdlib") {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			b.Fatalf("ecdsa.GenerateKey: %v", err)
		}
		sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
		if err != nil {
			b.Fatalf("ecdsa.SignASN1: %v", err)
		}
		b.Run("ecdsa-keygen", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, genErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader); genErr != nil {
					b.Fatalf("keygen: %v", genErr)
				}
			}
		})
		b.Run("ecdsa-sign", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, signErr := ecdsa.SignASN1(rand.Reader, priv, digest[:])
				if signErr != nil {
					b.Fatalf("sign: %v", signErr)
				}
				benchBytes = out
			}
		})
		b.Run("ecdsa-verify", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchOK = ecdsa.VerifyASN1(&priv.PublicKey, digest[:], sig)
				if !benchOK {
					b.Fatal("verify failed | 验签失败")
				}
			}
		})

		edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			b.Fatalf("ed25519.GenerateKey: %v", err)
		}
		edSig := ed25519.Sign(edPriv, data)
		b.Run("ed25519-keygen", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, _, genErr := ed25519.GenerateKey(rand.Reader); genErr != nil {
					b.Fatalf("keygen: %v", genErr)
				}
			}
		})
		b.Run("ed25519-sign", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchBytes = ed25519.Sign(edPriv, data)
			}
		})
		b.Run("ed25519-verify", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchOK = ed25519.Verify(edPub, data, edSig)
				if !benchOK {
					b.Fatal("verify failed | 验签失败")
				}
			}
		})
	}
}

// BenchmarkSM2Encrypt compares SM2 encryption and decryption of a 32-byte
// message. | BenchmarkSM2Encrypt 对比 32 字节消息的 SM2 加解密。
func BenchmarkSM2Encrypt(b *testing.B) {
	msg := benchData(32)
	if selected("our") {
		priv, err := guomi.GenerateKey()
		if err != nil {
			b.Fatalf("guomi.GenerateKey: %v", err)
		}
		pub := priv.Public()
		ct, err := guomi.EncryptRaw(pub, msg)
		if err != nil {
			b.Fatalf("guomi.EncryptRaw: %v", err)
		}
		b.Run("our-encrypt", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, encErr := guomi.EncryptRaw(pub, msg)
				if encErr != nil {
					b.Fatalf("encrypt: %v", encErr)
				}
				benchBytes = out
			}
		})
		b.Run("our-decrypt", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, decErr := guomi.DecryptRaw(priv, ct)
				if decErr != nil {
					b.Fatalf("decrypt: %v", decErr)
				}
				benchBytes = out
			}
		})
	}
	if selected("emmansun") {
		priv, err := esm2.GenerateKey(rand.Reader)
		if err != nil {
			b.Fatalf("esm2.GenerateKey: %v", err)
		}
		ct, err := esm2.Encrypt(rand.Reader, &priv.PublicKey, msg,
			esm2.NewPlainEncrypterOpts(esm2.MarshalUncompressed, esm2.C1C3C2))
		if err != nil {
			b.Fatalf("esm2.Encrypt: %v", err)
		}
		b.Run("emmansun-encrypt", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, encErr := esm2.Encrypt(rand.Reader, &priv.PublicKey, msg,
					esm2.NewPlainEncrypterOpts(esm2.MarshalUncompressed, esm2.C1C3C2))
				if encErr != nil {
					b.Fatalf("encrypt: %v", encErr)
				}
				benchBytes = out
			}
		})
		b.Run("emmansun-decrypt", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				out, decErr := esm2.Decrypt(priv, ct)
				if decErr != nil {
					b.Fatalf("decrypt: %v", decErr)
				}
				benchBytes = out
			}
		})
	}
}

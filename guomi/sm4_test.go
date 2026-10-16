package guomi

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
)

// TestSM4GBStandardVector 校验 GB/T 32907-2016 附录 A.1 的单分组标准向量。
func TestSM4GBStandardVector(t *testing.T) {
	key, err := hex.DecodeString("0123456789abcdeffedcba9876543210")
	if err != nil {
		t.Fatalf("测试向量密钥解码失败: %v", err)
	}
	plaintext, err := hex.DecodeString("0123456789abcdeffedcba9876543210")
	if err != nil {
		t.Fatalf("测试向量明文解码失败: %v", err)
	}
	const want = "681edf34d206965e86b3e94f536e4246"

	block, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher 返回错误: %v", err)
	}
	if block.BlockSize() != SM4BlockSize {
		t.Fatalf("BlockSize = %d, want %d", block.BlockSize(), SM4BlockSize)
	}
	out := make([]byte, SM4BlockSize)
	block.Encrypt(out, plaintext)
	if got := hex.EncodeToString(out); got != want {
		t.Fatalf("SM4 加密 = %s, want %s", got, want)
	}

	back := make([]byte, SM4BlockSize)
	block.Decrypt(back, out)
	if !bytes.Equal(back, plaintext) {
		t.Fatalf("SM4 单分组解密 = %x, want %x", back, plaintext)
	}
}

// TestSM4KeyLengthValidated 校验 16 字节密钥强制约束在各入口生效。
func TestSM4KeyLengthValidated(t *testing.T) {
	iv := bytes.Repeat([]byte{0x01}, SM4IVSize)
	nonce := bytes.Repeat([]byte{0x02}, SM4GCMNonceSize)

	for _, n := range []int{0, 1, 8, 15, 17, 24, 32} {
		key := make([]byte, n)
		if _, err := NewCipher(key); !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("NewCipher(%d 字节密钥) 的错误 = %v, want ErrInvalidKeyLength", n, err)
		}
		if _, err := EncryptCBC(key, iv, []byte("data")); !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("EncryptCBC(%d 字节密钥) 的错误 = %v, want ErrInvalidKeyLength", n, err)
		}
		if _, err := DecryptCBC(key, iv, make([]byte, SM4BlockSize)); !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("DecryptCBC(%d 字节密钥) 的错误 = %v, want ErrInvalidKeyLength", n, err)
		}
		if _, err := SealGCM(key, nonce, []byte("data"), nil); !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("SealGCM(%d 字节密钥) 的错误 = %v, want ErrInvalidKeyLength", n, err)
		}
		if _, err := OpenGCM(key, nonce, make([]byte, SM4GCMTagSize), nil); !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("OpenGCM(%d 字节密钥) 的错误 = %v, want ErrInvalidKeyLength", n, err)
		}
		if _, err := EncryptECB(key, []byte("data")); !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("EncryptECB(%d 字节密钥) 的错误 = %v, want ErrInvalidKeyLength", n, err)
		}
		if _, err := DecryptECB(key, make([]byte, SM4BlockSize)); !errors.Is(err, ErrInvalidKeyLength) {
			t.Fatalf("DecryptECB(%d 字节密钥) 的错误 = %v, want ErrInvalidKeyLength", n, err)
		}
	}

	key, err := RandomKey()
	if err != nil {
		t.Fatalf("RandomKey 返回错误: %v", err)
	}
	if len(key) != SM4KeySize {
		t.Fatalf("RandomKey 长度 = %d, want %d", len(key), SM4KeySize)
	}
	if _, err := NewCipher(key); err != nil {
		t.Fatalf("NewCipher(合法密钥) 返回错误: %v", err)
	}
}

// TestSM4CBCRoundTripWithPKCS7 覆盖 PKCS#7 填充下 CBC 的往返（含非 16 字节倍数长度）。
func TestSM4CBCRoundTripWithPKCS7(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, SM4KeySize)
	iv := bytes.Repeat([]byte{0x22}, SM4IVSize)

	for _, n := range []int{0, 1, 15, 16, 17, 31, 32, 33, 255, 1000} {
		t.Run(fmt.Sprintf("len=%d", n), func(t *testing.T) {
			plaintext := randBytes(t, n)
			ct, err := EncryptCBC(key, iv, plaintext)
			if err != nil {
				t.Fatalf("EncryptCBC 返回错误: %v", err)
			}
			wantLen := (n/SM4BlockSize + 1) * SM4BlockSize
			if len(ct) != wantLen {
				t.Fatalf("密文长度 = %d, want %d（含整块 PKCS#7 填充）", len(ct), wantLen)
			}
			// 相同密钥与 IV 必须得到确定性的密文。
			again, err := EncryptCBC(key, iv, plaintext)
			if err != nil {
				t.Fatalf("第二次 EncryptCBC 返回错误: %v", err)
			}
			if !bytes.Equal(ct, again) {
				t.Fatal("CBC 加密结果不确定")
			}
			back, err := DecryptCBC(key, iv, ct)
			if err != nil {
				t.Fatalf("DecryptCBC 返回错误: %v", err)
			}
			if !bytes.Equal(back, plaintext) {
				t.Fatalf("CBC 往返不一致: got %d 字节, want %d 字节", len(back), len(plaintext))
			}
		})
	}

	// IV 长度校验。
	if _, err := EncryptCBC(key, make([]byte, SM4IVSize-1), []byte("x")); !errors.Is(err, ErrInvalidIVLength) {
		t.Fatalf("EncryptCBC(15 字节 IV) 的错误 = %v, want ErrInvalidIVLength", err)
	}
	if _, err := DecryptCBC(key, make([]byte, SM4IVSize+1), make([]byte, SM4BlockSize)); !errors.Is(err, ErrInvalidIVLength) {
		t.Fatalf("DecryptCBC(17 字节 IV) 的错误 = %v, want ErrInvalidIVLength", err)
	}
	// 密文长度非分组整数倍。
	if _, err := DecryptCBC(key, iv, make([]byte, SM4BlockSize+1)); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("DecryptCBC(17 字节密文) 的错误 = %v, want ErrInvalidCiphertext", err)
	}
	if _, err := DecryptCBC(key, iv, nil); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("DecryptCBC(空密文) 的错误 = %v, want ErrInvalidCiphertext", err)
	}
	// 篡改填充：把最后一字节改成非法填充值必然失败。
	ct, err := EncryptCBC(key, iv, []byte("padding-probe"))
	if err != nil {
		t.Fatalf("EncryptCBC 返回错误: %v", err)
	}
	for _, pad := range []byte{0x00, 0x11} {
		broken := append([]byte{}, ct...)
		broken[len(broken)-1] = pad
		if _, err := DecryptCBC(key, iv, broken); !errors.Is(err, ErrInvalidPadding) {
			t.Fatalf("DecryptCBC(填充字节 0x%02x) 的错误 = %v, want ErrInvalidPadding", pad, err)
		}
	}
}

// TestSM4GCMRoundTripAndTamperFails 覆盖 GCM 的往返与 AEAD 完整性校验。
func TestSM4GCMRoundTripAndTamperFails(t *testing.T) {
	key := bytes.Repeat([]byte{0x33}, SM4KeySize)
	nonce := bytes.Repeat([]byte{0x44}, SM4GCMNonceSize)
	aad := []byte("aad-context|guomi")
	plaintext := []byte("SM4-GCM 载荷|国密|2026")

	ct, err := SealGCM(key, nonce, plaintext, aad)
	if err != nil {
		t.Fatalf("SealGCM 返回错误: %v", err)
	}
	if len(ct) != len(plaintext)+SM4GCMTagSize {
		t.Fatalf("密文长度 = %d, want %d（明文 + 16 字节认证标签）", len(ct), len(plaintext)+SM4GCMTagSize)
	}
	back, err := OpenGCM(key, nonce, ct, aad)
	if err != nil {
		t.Fatalf("OpenGCM 返回错误: %v", err)
	}
	if !bytes.Equal(back, plaintext) {
		t.Fatalf("GCM 往返不一致: %x, want %x", back, plaintext)
	}

	// 篡改密文 / 认证标签 / AAD 必须失败。
	tamper := func(mutate func([]byte)) {
		t.Helper()
		broken := append([]byte{}, ct...)
		mutate(broken)
		if _, err := OpenGCM(key, nonce, broken, aad); err == nil {
			t.Fatal("篡改后的密文未通过完整性校验")
		}
	}
	tamper(func(b []byte) { b[0] ^= 0x01 })
	tamper(func(b []byte) { b[len(b)-1] ^= 0x01 })
	tamper(func(b []byte) { b[len(b)/2] ^= 0x80 })
	if _, err := OpenGCM(key, nonce, ct, []byte("other-aad")); err == nil {
		t.Fatal("AAD 不一致时 OpenGCM 未返回错误")
	}
	if _, err := OpenGCM(key, []byte("other-nonce"), ct, aad); err == nil {
		t.Fatal("nonce 不一致时 OpenGCM 未返回错误")
	}
	if _, err := OpenGCM(bytes.Repeat([]byte{0x34}, SM4KeySize), nonce, ct, aad); err == nil {
		t.Fatal("密钥不一致时 OpenGCM 未返回错误")
	}
	if _, err := OpenGCM(key, nonce, ct[:len(ct)-1], aad); err == nil {
		t.Fatal("密文被截断时 OpenGCM 未返回错误")
	}

	// 空明文/空 AAD 也应可往返。
	empty, err := SealGCM(key, nonce, nil, nil)
	if err != nil {
		t.Fatalf("SealGCM(空明文) 返回错误: %v", err)
	}
	if out, err := OpenGCM(key, nonce, empty, nil); err != nil || len(out) != 0 {
		t.Fatalf("OpenGCM(空明文) = (%d 字节, %v), want (0 字节, nil)", len(out), err)
	}
}

// TestSM4GCMNonceLengths 校验 nonce 长度策略：12 字节推荐，其他长度允许但必须校验。
func TestSM4GCMNonceLengths(t *testing.T) {
	key := bytes.Repeat([]byte{0x55}, SM4KeySize)
	plaintext := []byte("nonce-length-probe")

	for _, n := range []int{8, 12, 13, 16, 32, 64} {
		nonce := randBytes(t, n)
		ct, err := SealGCM(key, nonce, plaintext, nil)
		if err != nil {
			t.Fatalf("SealGCM(nonce=%d 字节) 返回错误: %v", n, err)
		}
		back, err := OpenGCM(key, nonce, ct, nil)
		if err != nil || !bytes.Equal(back, plaintext) {
			t.Fatalf("OpenGCM(nonce=%d 字节) = (%d 字节, %v), want (%d 字节, nil)", n, len(back), err, len(plaintext))
		}
	}
	for _, n := range []int{0, 65, 128} {
		if _, err := SealGCM(key, make([]byte, n), plaintext, nil); !errors.Is(err, ErrInvalidNonceLength) {
			t.Fatalf("SealGCM(nonce=%d 字节) 的错误 = %v, want ErrInvalidNonceLength", n, err)
		}
		if _, err := OpenGCM(key, make([]byte, n), make([]byte, SM4GCMTagSize), nil); !errors.Is(err, ErrInvalidNonceLength) {
			t.Fatalf("OpenGCM(nonce=%d 字节) 的错误 = %v, want ErrInvalidNonceLength", n, err)
		}
	}
}

// TestSM4ECBRoundTrip 覆盖 ECB（仅协议兼容）的往返。
func TestSM4ECBRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x66}, SM4KeySize)

	for _, n := range []int{0, 1, 15, 16, 17, 32, 4096} {
		plaintext := randBytes(t, n)
		ct, err := EncryptECB(key, plaintext)
		if err != nil {
			t.Fatalf("EncryptECB(len=%d) 返回错误: %v", n, err)
		}
		if len(ct)%SM4BlockSize != 0 || len(ct) == 0 {
			t.Fatalf("ECB 密文长度 = %d, want 16 的正整数倍", len(ct))
		}
		back, err := DecryptECB(key, ct)
		if err != nil {
			t.Fatalf("DecryptECB(len=%d) 返回错误: %v", n, err)
		}
		if !bytes.Equal(back, plaintext) {
			t.Fatalf("ECB 往返不一致: got %d 字节, want %d 字节", len(back), len(plaintext))
		}
	}

	// 相同明文块必须产生相同密文块（ECB 的固有特性，仅用于协议兼容）。
	block := []byte("0123456789abcdef")
	ct, err := EncryptECB(key, append(append([]byte{}, block...), block...))
	if err != nil {
		t.Fatalf("EncryptECB 返回错误: %v", err)
	}
	if !bytes.Equal(ct[:SM4BlockSize], ct[SM4BlockSize:2*SM4BlockSize]) {
		t.Fatal("ECB 下相同明文块应产生相同密文块")
	}
	if _, err := DecryptECB(key, make([]byte, SM4BlockSize+1)); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("DecryptECB(17 字节) 的错误 = %v, want ErrInvalidCiphertext", err)
	}
}

// TestSM4RandomKeyAndIV 校随机密钥/IV 的长度与随机性。
func TestSM4RandomKeyAndIV(t *testing.T) {
	k1, err := RandomKey()
	if err != nil {
		t.Fatalf("RandomKey 返回错误: %v", err)
	}
	k2, err := RandomKey()
	if err != nil {
		t.Fatalf("第二次 RandomKey 返回错误: %v", err)
	}
	if len(k1) != SM4KeySize || len(k2) != SM4KeySize {
		t.Fatalf("RandomKey 长度 = %d/%d, want %d", len(k1), len(k2), SM4KeySize)
	}
	if bytes.Equal(k1, k2) {
		t.Fatal("两次 RandomKey 结果相同")
	}

	iv1, err := RandomIV()
	if err != nil {
		t.Fatalf("RandomIV 返回错误: %v", err)
	}
	iv2, err := RandomIV()
	if err != nil {
		t.Fatalf("第二次 RandomIV 返回错误: %v", err)
	}
	if len(iv1) != SM4IVSize || len(iv2) != SM4IVSize {
		t.Fatalf("RandomIV 长度 = %d/%d, want %d", len(iv1), len(iv2), SM4IVSize)
	}
	if bytes.Equal(iv1, iv2) {
		t.Fatal("两次 RandomIV 结果相同")
	}
}

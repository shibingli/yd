package guomi

import (
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/tjfoc/gmsm/sm4"
)

// maxGCMNonceSize 是 SealGCM/OpenGCM 允许的最大 nonce 长度。
const maxGCMNonceSize = 64

var (
	// ErrInvalidKeyLength 表示 SM4 密钥长度不是 16 字节。
	ErrInvalidKeyLength = errors.New("guomi: SM4 密钥长度必须为 16 字节")

	// ErrInvalidIVLength 表示 SM4-CBC 的 IV 长度不是 16 字节。
	ErrInvalidIVLength = errors.New("guomi: SM4 IV 长度必须为 16 字节")

	// ErrInvalidNonceLength 表示 SM4-GCM 的 nonce 为空或超过 64 字节。
	ErrInvalidNonceLength = errors.New("guomi: SM4-GCM nonce 长度非法")

	// ErrInvalidPadding 表示解密后的 PKCS#7 填充不合法（密文被篡改或密钥错误）。
	ErrInvalidPadding = errors.New("guomi: PKCS#7 填充非法")
)

// NewCipher 使用 16 字节密钥创建 SM4 分组密码（GB/T 32907-2016），
// 返回的 cipher.Block 分组长度为 16 字节，可直接用于 crypto/cipher 的各种模式。
//
// 密钥长度不等于 16 字节时返回包装了 ErrInvalidKeyLength 的错误。
func NewCipher(key []byte) (cipher.Block, error) {
	if len(key) != SM4KeySize {
		return nil, fmt.Errorf("%w: 实际 %d 字节", ErrInvalidKeyLength, len(key))
	}
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("guomi: 创建 SM4 分组密码失败: %w", err)
	}
	return block, nil
}

// EncryptCBC 使用 CBC 模式与 PKCS#7 填充加密 plaintext，iv 必须为 16 字节。
//
// 输出长度恒为 SM4BlockSize 的正整数倍（空明文也会填充为一个整块），
// 因此密文长度不会泄露明文长度模 16 的余数。
func EncryptCBC(key, iv, plaintext []byte) ([]byte, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != SM4IVSize {
		return nil, fmt.Errorf("%w: 实际 %d 字节", ErrInvalidIVLength, len(iv))
	}

	padded := pkcs7Pad(plaintext, SM4BlockSize)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out, nil
}

// DecryptCBC 使用 CBC 模式解密并去除 PKCS#7 填充，iv 必须为 16 字节。
//
// 密文长度不是 16 的正整数倍时返回 ErrInvalidCiphertext；
// 填充不合法（密钥或密文错误、密文被篡改）时返回 ErrInvalidPadding。
func DecryptCBC(key, iv, ciphertext []byte) ([]byte, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != SM4IVSize {
		return nil, fmt.Errorf("%w: 实际 %d 字节", ErrInvalidIVLength, len(iv))
	}
	if len(ciphertext) == 0 || len(ciphertext)%SM4BlockSize != 0 {
		return nil, fmt.Errorf("%w: CBC 密文长度 %d 不是 %d 的正整数倍", ErrInvalidCiphertext, len(ciphertext), SM4BlockSize)
	}

	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ciphertext)
	return pkcs7Unpad(out, SM4BlockSize)
}

// SealGCM 使用 GCM（认证加密）模式加密 plaintext 并附加 aad 作为附加认证数据，
// 返回 ciphertext||tag（认证标签长度 16 字节）。
//
// nonce 推荐 12 字节（SM4GCMNonceSize，走最优实现路径）；也允许 1~64 字节的
// 其他长度（通过 GHASH 派生初始计数器，效率略低）。同一密钥下 nonce 绝不可重复，
// 否则会破坏机密性与完整性，建议每条消息使用 RandomIV 生成随机 nonce。
func SealGCM(key, nonce, plaintext, aad []byte) ([]byte, error) {
	aead, err := newGCM(key, nonce)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, plaintext, aad), nil
}

// OpenGCM 校验并解密 SealGCM 的输出，aad 必须与加密时完全一致。
//
// nonce 长度校验规则同 SealGCM；密文、认证标签或 aad 任一被篡改，
// 都返回认证失败错误且不返回任何明文。
func OpenGCM(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	aead, err := newGCM(key, nonce)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("guomi: SM4-GCM 认证失败: %w", err)
	}
	return plaintext, nil
}

// newGCM 构造 SM4-GCM AEAD，并做密钥与 nonce 长度校验。
func newGCM(key, nonce []byte) (cipher.AEAD, error) {
	if len(nonce) == 0 || len(nonce) > maxGCMNonceSize {
		return nil, fmt.Errorf("%w: 实际 %d 字节（推荐 %d 字节，允许 1~%d）",
			ErrInvalidNonceLength, len(nonce), SM4GCMNonceSize, maxGCMNonceSize)
	}
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) == SM4GCMNonceSize {
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("guomi: 创建 SM4-GCM 失败: %w", err)
		}
		return aead, nil
	}
	// 非 12 字节 nonce：使用标准库提供的可定制 nonce 长度构造器（deprecated 但语义稳定）。
	aead, err := cipher.NewGCMWithNonceSize(block, len(nonce))
	if err != nil {
		return nil, fmt.Errorf("guomi: 创建 SM4-GCM 失败: %w", err)
	}
	return aead, nil
}

// EncryptECB 使用 ECB 模式与 PKCS#7 填充加密。
//
// 注意：ECB 模式下相同明文块产生相同密文块，会泄露明文结构，**仅用于兼容
// 既有协议**；新代码请使用 SealGCM（认证加密）或 EncryptCBC。
func EncryptECB(key, plaintext []byte) ([]byte, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, SM4BlockSize)
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += SM4BlockSize {
		block.Encrypt(out[i:i+SM4BlockSize], padded[i:i+SM4BlockSize])
	}
	return out, nil
}

// DecryptECB 解密 EncryptECB 的输出并去除 PKCS#7 填充。
//
// 注意：与 EncryptECB 一样，**仅用于兼容既有协议**；新代码请使用 OpenGCM。
func DecryptECB(key, ciphertext []byte) ([]byte, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%SM4BlockSize != 0 {
		return nil, fmt.Errorf("%w: ECB 密文长度 %d 不是 %d 的正整数倍", ErrInvalidCiphertext, len(ciphertext), SM4BlockSize)
	}
	out := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += SM4BlockSize {
		block.Decrypt(out[i:i+SM4BlockSize], ciphertext[i:i+SM4BlockSize])
	}
	return pkcs7Unpad(out, SM4BlockSize)
}

// RandomKey 使用 crypto/rand 生成 16 字节 SM4 密钥。
func RandomKey() ([]byte, error) {
	key := make([]byte, SM4KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("guomi: 生成 SM4 密钥失败: %w", err)
	}
	return key, nil
}

// RandomIV 使用 crypto/rand 生成 16 字节初始向量，
// 可用于 SM4-CBC 的 iv 或切分为 SM4-GCM 的 12 字节 nonce（取前 12 字节）。
func RandomIV() ([]byte, error) {
	iv := make([]byte, SM4IVSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("guomi: 生成 SM4 IV 失败: %w", err)
	}
	return iv, nil
}

// pkcs7Pad 按 PKCS#7 将 src 填充到 blockSize 的正整数倍长度。
func pkcs7Pad(src []byte, blockSize int) []byte {
	pad := blockSize - len(src)%blockSize
	out := make([]byte, len(src)+pad)
	copy(out, src)
	for i := len(src); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// pkcs7Unpad 校验并去除 PKCS#7 填充。
func pkcs7Unpad(src []byte, blockSize int) ([]byte, error) {
	n := len(src)
	if n == 0 || n%blockSize != 0 {
		return nil, fmt.Errorf("%w: 长度 %d 不是 %d 的正整数倍", ErrInvalidPadding, n, blockSize)
	}
	pad := int(src[n-1])
	if pad == 0 || pad > blockSize || pad > n {
		return nil, fmt.Errorf("%w: 填充字节 0x%02x 非法", ErrInvalidPadding, src[n-1])
	}
	for _, b := range src[n-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("%w: 填充内容与填充字节不一致", ErrInvalidPadding)
		}
	}
	return src[:n-pad], nil
}

package guomi

import (
	"crypto/cipher"
	"crypto/rand"

	"github.com/shibingli/yd/core/errx"
	"github.com/tjfoc/gmsm/sm4"
)

// maxGCMNonceSize is the longest nonce SealGCM and OpenGCM accept. | maxGCMNonceSize 是 SealGCM/OpenGCM 允许的最大 nonce 长度。
const maxGCMNonceSize = 64

var (
	// ErrInvalidKeyLength reports an SM4 key whose length is not 16 bytes. | ErrInvalidKeyLength 表示 SM4 密钥长度不是 16 字节。
	ErrInvalidKeyLength = errx.New("guomi: SM4 key length must be 16 bytes | guomi：SM4 密钥长度必须为 16 字节")

	// ErrInvalidIVLength reports an SM4-CBC IV whose length is not 16 bytes. | ErrInvalidIVLength 表示 SM4-CBC 的 IV 长度不是 16 字节。
	ErrInvalidIVLength = errx.New("guomi: SM4 IV length must be 16 bytes | guomi：SM4 IV 长度必须为 16 字节")

	// ErrInvalidNonceLength reports an SM4-GCM nonce that is empty or longer than 64 bytes. | ErrInvalidNonceLength 表示 SM4-GCM 的 nonce 为空或超过 64 字节。
	ErrInvalidNonceLength = errx.New("guomi: invalid SM4-GCM nonce length | guomi：SM4-GCM nonce 长度非法")

	// ErrInvalidPadding reports PKCS#7 padding that does not decode, caused by tampering or a wrong key. | ErrInvalidPadding 表示解密后的 PKCS#7 填充不合法（密文被篡改或密钥错误）。
	ErrInvalidPadding = errx.New("guomi: invalid PKCS#7 padding | guomi：PKCS#7 填充非法")

	// ErrGCMAuth reports a failed SM4-GCM authentication check. | ErrGCMAuth 表示 SM4-GCM 认证校验失败。
	ErrGCMAuth = errx.New("guomi: SM4-GCM authentication failed | guomi：SM4-GCM 认证失败")

	// ErrCipherCreate reports a failure while constructing an SM4 cipher or a mode over it. | ErrCipherCreate 表示构造 SM4 分组密码或其工作模式失败。
	ErrCipherCreate = errx.New("guomi: failed to create SM4 cipher or mode | guomi：创建 SM4 分组密码或工作模式失败")
)

// NewCipher creates an SM4 block cipher per GB/T 32907-2016 from a 16-byte key. | NewCipher 使用 16 字节密钥创建 SM4 分组密码（GB/T 32907-2016）。
// The returned cipher.Block has a block size of 16 bytes and works with every mode of crypto/cipher. | 返回的 cipher.Block 分组长度为 16 字节，可直接用于 crypto/cipher 的各种模式。
//
// A key length other than 16 bytes returns an error wrapping ErrInvalidKeyLength. | 密钥长度不等于 16 字节时返回包装了 ErrInvalidKeyLength 的错误。
func NewCipher(key []byte) (cipher.Block, error) {
	if len(key) != SM4KeySize {
		return nil, errx.Detail(ErrInvalidKeyLength, "got %[1]d bytes | 实际 %[1]d 字节", len(key))
	}
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, errx.Wrap(err, ErrCipherCreate)
	}
	return block, nil
}

// EncryptCBC encrypts plaintext in CBC mode with PKCS#7 padding, where iv must be 16 bytes. | EncryptCBC 使用 CBC 模式与 PKCS#7 填充加密 plaintext，iv 必须为 16 字节。
//
// The output is always a positive multiple of SM4BlockSize, since even an empty plaintext is padded into one full block. | 输出长度恒为 SM4BlockSize 的正整数倍（空明文也会填充为一个整块）。
// The ciphertext length therefore never leaks the plaintext length modulo 16. | 因此密文长度不会泄露明文长度模 16 的余数。
func EncryptCBC(key, iv, plaintext []byte) ([]byte, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != SM4IVSize {
		return nil, errx.Detail(ErrInvalidIVLength, "got %[1]d bytes | 实际 %[1]d 字节", len(iv))
	}

	// Pad straight into the output buffer and encrypt in place: cipher.BlockMode and
	// cipher.Block both allow dst and src to overlap exactly, so no intermediate copy is needed.
	// | 直接填充进输出缓冲并就地加密：cipher.BlockMode 与 cipher.Block 都允许 dst 与 src 完全重叠，
	// 因此无需中间副本。
	out := pkcs7PadInto(make([]byte, 0, len(plaintext)+SM4BlockSize), plaintext, SM4BlockSize)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, out)
	return out, nil
}

// DecryptCBC decrypts CBC ciphertext and strips the PKCS#7 padding, where iv must be 16 bytes. | DecryptCBC 使用 CBC 模式解密并去除 PKCS#7 填充，iv 必须为 16 字节。
//
// A ciphertext length that is not a positive multiple of 16 returns ErrInvalidCiphertext. | 密文长度不是 16 的正整数倍时返回 ErrInvalidCiphertext。
// Invalid padding, caused by a wrong key or ciphertext, returns ErrInvalidPadding. | 填充不合法（密钥或密文错误、密文被篡改）时返回 ErrInvalidPadding。
func DecryptCBC(key, iv, ciphertext []byte) ([]byte, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != SM4IVSize {
		return nil, errx.Detail(ErrInvalidIVLength, "got %[1]d bytes | 实际 %[1]d 字节", len(iv))
	}
	if len(ciphertext) == 0 || len(ciphertext)%SM4BlockSize != 0 {
		return nil, errx.Detail(ErrInvalidCiphertext, "CBC ciphertext length %[1]d is not a positive multiple of %[2]d | CBC 密文长度 %[1]d 不是 %[2]d 的正整数倍", len(ciphertext), SM4BlockSize)
	}

	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ciphertext)
	return pkcs7Unpad(out, SM4BlockSize)
}

// SealGCM encrypts plaintext with GCM authenticated encryption and authenticates aad as additional data. | SealGCM 使用 GCM（认证加密）模式加密 plaintext 并附加 aad 作为附加认证数据。
// It returns ciphertext||tag, where the authentication tag is 16 bytes. | 返回 ciphertext||tag（认证标签长度 16 字节）。
//
// A 12-byte nonce (SM4GCMNonceSize) is recommended and takes the fastest code path. | nonce 推荐 12 字节（SM4GCMNonceSize，走最优实现路径）。
// Other lengths from 1 to 64 bytes are allowed and derive the initial counter through GHASH, at slightly lower speed. | 也允许 1~64 字节的其他长度（通过 GHASH 派生初始计数器，效率略低）。
// A nonce must never repeat under one key, which would break both confidentiality and integrity; generate a random nonce per message with RandomIV. | 同一密钥下 nonce 绝不可重复，否则会破坏机密性与完整性，建议每条消息使用 RandomIV 生成随机 nonce。
func SealGCM(key, nonce, plaintext, aad []byte) ([]byte, error) {
	aead, err := newGCM(key, nonce)
	if err != nil {
		return nil, err
	}
	return aead.Seal(nil, nonce, plaintext, aad), nil
}

// OpenGCM verifies and decrypts the output of SealGCM, where aad must match the encryption call exactly. | OpenGCM 校验并解密 SealGCM 的输出，aad 必须与加密时完全一致。
//
// The nonce length rules match SealGCM. | nonce 长度校验规则同 SealGCM。
// Tampering with the ciphertext, the authentication tag or the aad returns an authentication error and no plaintext at all. | 密文、认证标签或 aad 任一被篡改，都返回认证失败错误且不返回任何明文。
func OpenGCM(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	aead, err := newGCM(key, nonce)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, errx.Wrap(err, ErrGCMAuth)
	}
	return plaintext, nil
}

// NewGCM creates a reusable SM4-GCM AEAD for the recommended 12-byte nonce. | NewGCM 创建可复用的 SM4-GCM AEAD（推荐 12 字节 nonce）。
//
// The returned AEAD is stateless and may be shared by any number of goroutines for Seal and Open.
// Reusing it avoids rebuilding the SM4 key schedule and the GHASH table on every message, which
// dominates the cost of encrypting many small messages; the one-shot SealGCM / OpenGCM build a
// fresh AEAD per call by design. | 返回的 AEAD 无状态，可被任意多个协程共享用于 Seal 与 Open。
// 复用它避免每条消息重建 SM4 密钥编排与 GHASH 表——这正是高频加密小消息时的主要开销；
// 一次性的 SealGCM / OpenGCM 按设计每次调用都会新建 AEAD。
func NewGCM(key []byte) (cipher.AEAD, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errx.Wrap(err, ErrCipherCreate)
	}
	return aead, nil
}

// newGCM builds the SM4-GCM AEAD and validates the key and nonce lengths. | newGCM 构造 SM4-GCM AEAD，并做密钥与 nonce 长度校验。
func newGCM(key, nonce []byte) (cipher.AEAD, error) {
	if len(nonce) == 0 || len(nonce) > maxGCMNonceSize {
		return nil, errx.Detail(ErrInvalidNonceLength, "got %[1]d bytes (recommended %[2]d, allowed 1..%[3]d) | 实际 %[1]d 字节（推荐 %[2]d 字节，允许 1~%[3]d）",
			len(nonce), SM4GCMNonceSize, maxGCMNonceSize)
	}
	if len(nonce) == SM4GCMNonceSize {
		return NewGCM(key)
	}
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	// A non-12-byte nonce uses the standard library constructor with a custom nonce size, deprecated but stable in semantics. | 非 12 字节 nonce：使用标准库提供的可定制 nonce 长度构造器（deprecated 但语义稳定）。
	aead, err := cipher.NewGCMWithNonceSize(block, len(nonce))
	if err != nil {
		return nil, errx.Wrap(err, ErrCipherCreate)
	}
	return aead, nil
}

// EncryptECB encrypts in ECB mode with PKCS#7 padding. | EncryptECB 使用 ECB 模式与 PKCS#7 填充加密。
//
// Note that identical plaintext blocks produce identical ciphertext blocks in ECB mode, which leaks plaintext structure. | 注意：ECB 模式下相同明文块产生相同密文块，会泄露明文结构。
// It exists only for compatibility with existing protocols; new code should use SealGCM or EncryptCBC. | 仅用于兼容既有协议；新代码请使用 SealGCM（认证加密）或 EncryptCBC。
func EncryptECB(key, plaintext []byte) ([]byte, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7PadInto(make([]byte, 0, len(plaintext)+SM4BlockSize), plaintext, SM4BlockSize)
	for i := 0; i < len(padded); i += SM4BlockSize {
		block.Encrypt(padded[i:i+SM4BlockSize], padded[i:i+SM4BlockSize])
	}
	return padded, nil
}

// DecryptECB decrypts the output of EncryptECB and strips the PKCS#7 padding. | DecryptECB 解密 EncryptECB 的输出并去除 PKCS#7 填充。
//
// As with EncryptECB, it exists only for compatibility with existing protocols; new code should use OpenGCM. | 与 EncryptECB 一样，仅用于兼容既有协议；新代码请使用 OpenGCM。
func DecryptECB(key, ciphertext []byte) ([]byte, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%SM4BlockSize != 0 {
		return nil, errx.Detail(ErrInvalidCiphertext, "ECB ciphertext length %[1]d is not a positive multiple of %[2]d | ECB 密文长度 %[1]d 不是 %[2]d 的正整数倍", len(ciphertext), SM4BlockSize)
	}
	out := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += SM4BlockSize {
		block.Decrypt(out[i:i+SM4BlockSize], ciphertext[i:i+SM4BlockSize])
	}
	return pkcs7Unpad(out, SM4BlockSize)
}

// RandomKey generates a 16-byte SM4 key from crypto/rand. | RandomKey 使用 crypto/rand 生成 16 字节 SM4 密钥。
func RandomKey() ([]byte, error) {
	key := make([]byte, SM4KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, errx.Wrap(err, ErrRandomSource)
	}
	return key, nil
}

// RandomIV generates a 16-byte initialization vector from crypto/rand. | RandomIV 使用 crypto/rand 生成 16 字节初始向量。
// It serves as the SM4-CBC iv directly, or as an SM4-GCM nonce by taking the first 12 bytes. | 可用于 SM4-CBC 的 iv 或切分为 SM4-GCM 的 12 字节 nonce（取前 12 字节）。
func RandomIV() ([]byte, error) {
	iv := make([]byte, SM4IVSize)
	if _, err := rand.Read(iv); err != nil {
		return nil, errx.Wrap(err, ErrRandomSource)
	}
	return iv, nil
}

// pkcs7PadInto appends src to dst and pads the result with PKCS#7 to a positive multiple of blockSize.
// | pkcs7PadInto 把 src 追加到 dst 并按 PKCS#7 填充到 blockSize 的正整数倍。
func pkcs7PadInto(dst, src []byte, blockSize int) []byte {
	pad := blockSize - len(src)%blockSize
	dst = append(dst, src...)
	for i := 0; i < pad; i++ {
		dst = append(dst, byte(pad))
	}
	return dst
}

// pkcs7Unpad validates and strips PKCS#7 padding. | pkcs7Unpad 校验并去除 PKCS#7 填充。
func pkcs7Unpad(src []byte, blockSize int) ([]byte, error) {
	n := len(src)
	if n == 0 || n%blockSize != 0 {
		return nil, errx.Detail(ErrInvalidPadding, "length %[1]d is not a positive multiple of %[2]d | 长度 %[1]d 不是 %[2]d 的正整数倍", n, blockSize)
	}
	pad := int(src[n-1])
	if pad == 0 || pad > blockSize || pad > n {
		return nil, errx.Detail(ErrInvalidPadding, "invalid padding byte 0x%[1]02x | 填充字节 0x%[1]02x 非法", src[n-1])
	}
	for _, b := range src[n-pad:] {
		if int(b) != pad {
			return nil, errx.Detail(ErrInvalidPadding, "padding contents do not match the padding byte | 填充内容与填充字节不一致")
		}
	}
	return src[:n-pad], nil
}

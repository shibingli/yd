// Package guomi provides the GB/T national cryptography suite: SM2 public key cryptography, SM3 hashing and the SM4 block cipher. | Package guomi 提供符合国密标准的 SM2/SM3/SM4 密码套件。
// Typical uses are data encryption, integrity verification and password protection. | 用于数据加密、完整性校验与口令保护。
//
// Implementation notes | 实现约定：
//
//   - Primitives come from github.com/tjfoc/gmsm, which is pure Go and cgo-free. | 原语来源：github.com/tjfoc/gmsm（纯 Go，无 cgo）。
//   - gmsm supplies the SM2 curve arithmetic and signing, the SM3 compression function and the SM4 block cipher. | gmsm 提供 SM2 曲线运算与签名验签、SM3 压缩函数、SM4 分组密码。
//   - No other third-party crypto library is imported, and there is no OpenSSL binding or cgo code. | 不引入任何其他第三方密码库，也不依赖 OpenSSL 绑定或任何 cgo 代码。
//   - All randomness comes from crypto/rand. | 随机数一律来自 crypto/rand。
//   - SM2 encryption and decryption, including the GB/T 32918.4 key derivation function, are composed in this package. | SM2 加密/解密（含 GB/T 32918.4 密钥派生函数 KDF）在本包内组合实现。
//   - Composing them here pins the ciphertext byte layout and handles empty plaintext correctly. | 如此可严格定义密文字节布局并正确处理空明文。
//   - SM2 signing and verification follow GB/T 32918.2 through gmsm, using the default user ID "1234567812345678". | SM2 签名/验签按 GB/T 32918.2 使用 gmsm 实现，用户标识为默认值 "1234567812345678"。
//   - SM4 modes (CBC, GCM and ECB) build on crypto/cipher over the SM4 block cipher. | SM4 的工作模式（CBC/GCM/ECB）基于标准库 crypto/cipher 组合。
//   - Base64 encoding goes through the framework wrapper github.com/shibingli/yd/core/b64x. | Base64 编解码统一走框架自封装层 github.com/shibingli/yd/core/b64x。
//
// Standards | 标准依据：
//
//   - SM3: GB/T 32905-2016. | SM3：GB/T 32905-2016。
//   - SM4: GB/T 32907-2016. | SM4：GB/T 32907-2016。
//   - SM2: GB/T 32918 parts 1 to 4. | SM2：GB/T 32918（第 1~4 部分）。
//
// Typical usage | 典型用法：
//
//	sum := guomi.SumHex([]byte("abc"))                 // SM3 digest, lowercase hex | SM3 摘要（小写十六进制）
//	priv, err := guomi.GenerateKey()                   // SM2 key pair | SM2 密钥对
//	ct, err := guomi.EncryptRaw(priv.Public(), msg)    // raw C1C3C2 ciphertext for JS interop | 原始 C1C3C2 密文（前端互操作）
//	key, err := guomi.RandomKey()                      // SM4 key | SM4 密钥
//	sealed, err := guomi.SealGCM(key, nonce, msg, aad) // SM4-GCM sealed box | SM4-GCM 加密并认证
package guomi

import "github.com/shibingli/yd/core/errx"

// Fixed algorithm-related lengths in bytes. | 算法相关的固定长度常量（单位：字节）。
const (
	// SM2CoordinateSize is the byte length of an SM2 curve coordinate (X/Y) and of the private scalar D. | SM2CoordinateSize 是 SM2 椭圆曲线坐标（X/Y）与私钥标量 D 的字节长度。
	SM2CoordinateSize = 32

	// SM3Size is the output length of an SM3 digest. | SM3Size 是 SM3 摘要的输出长度。
	SM3Size = 32

	// SM3BlockSize is the SM3 block size. | SM3BlockSize 是 SM3 的分组长度。
	SM3BlockSize = 64

	// SM4KeySize is the SM4 key length, which must match exactly. | SM4KeySize 是 SM4 密钥长度（必须精确等于该值）。
	SM4KeySize = 16

	// SM4BlockSize is the SM4 block size and the alignment unit of CBC ciphertext. | SM4BlockSize 是 SM4 的分组长度，也是 CBC 密文长度的对齐单位。
	SM4BlockSize = 16

	// SM4IVSize is the length of the SM4-CBC initialization vector. | SM4IVSize 是 SM4-CBC 初始化向量的长度。
	SM4IVSize = 16

	// SM4GCMNonceSize is the recommended, and fastest, SM4-GCM nonce length. | SM4GCMNonceSize 是 SM4-GCM 推荐（也是最优）的 nonce 长度。
	SM4GCMNonceSize = 12

	// SM4GCMTagSize is the length of the SM4-GCM authentication tag. | SM4GCMTagSize 是 SM4-GCM 认证标签的长度。
	SM4GCMTagSize = 16
)

var (
	// ErrInvalidCiphertext reports a ciphertext whose length or structure is invalid. | ErrInvalidCiphertext 表示密文长度或结构非法。
	// It covers raw and ASN.1 SM2 ciphertext as well as SM4 CBC and ECB ciphertext. | 涵盖 SM2 原始/ASN.1 密文与 SM4 CBC/ECB 密文。
	ErrInvalidCiphertext = errx.New("guomi: invalid ciphertext | guomi：非法的密文")

	// ErrRandomSource reports a failure while reading cryptographically secure randomness. | ErrRandomSource 表示读取密码学安全随机数失败。
	ErrRandomSource = errx.New("guomi: failed to read random source | guomi：读取随机源失败")
)

// CipherSuiteInfo describes the algorithms, modes and implementation sources in use. | CipherSuiteInfo 返回当前加密套件的算法、工作模式与实现来源描述。
// It is meant for /health probes and audit output. | 供 /health 探测与审计输出使用。
//
// The returned map is a fresh mutable copy, so caller edits never touch package state. | 返回的 map 是每次调用新建的可变副本，调用方自行修改不会影响包内状态。
func CipherSuiteInfo() map[string]string {
	return map[string]string{
		"sm2":     "gmsm/sm2 C1C3C2",
		"sm3":     "GB/T 32905-2016",
		"sm4":     "gmsm/sm4 CBC/GCM",
		"pbkdf2":  "PBKDF2-HMAC-SM3 (RFC 8018)",
		"rand":    "crypto/rand",
		"backend": "github.com/tjfoc/gmsm v1.4.1",
		"cgo":     "disabled",
	}
}

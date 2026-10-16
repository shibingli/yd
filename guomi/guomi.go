// Package guomi 提供符合国密标准的 SM2/SM3/SM4 密码套件，用于数据加密、
// 完整性校验与口令保护。
//
// 实现约定：
//
//   - 原语来源：github.com/tjfoc/gmsm（纯 Go，无 cgo）提供 SM2 曲线运算与签名验签、
//     SM3 压缩函数、SM4 分组密码；本包不引入任何其他第三方密码库，
//     也不依赖 OpenSSL 绑定或任何 cgo 代码；
//   - 随机数：一律来自 crypto/rand；
//   - SM2 加密/解密（含密钥派生函数 KDF）按 GB/T 32918.4 在本包内组合实现，
//     以便严格定义密文字节布局并正确处理空明文；SM2 签名/验签按 GB/T 32918.2
//     使用 gmsm 实现（用户标识为默认值 "1234567812345678"）；
//   - SM4 的工作模式（CBC/GCM/ECB）基于标准库 crypto/cipher 组合，分组密码为 SM4；
//   - Base64 编解码统一走框架自封装层 github.com/yun-dev/yd/b64x。
//
// 标准依据：SM3 —— GB/T 32905-2016；SM4 —— GB/T 32907-2016；
// SM2 —— GB/T 32918（第 1~4 部分）。
//
// 典型用法：
//
//	sum := guomi.SumHex([]byte("abc"))                 // SM3 摘要（小写十六进制）
//	priv, err := guomi.GenerateKey()                   // SM2 密钥对
//	ct, err := guomi.EncryptRaw(priv.Public(), msg)    // 原始 C1C3C2 密文（前端互操作）
//	key, err := guomi.RandomKey()                      // SM4 密钥
//	sealed, err := guomi.SealGCM(key, nonce, msg, aad) // SM4-GCM 加密并认证
package guomi

import "errors"

// 算法相关的固定长度常量（单位：字节）。
const (
	// SM2CoordinateSize 是 SM2 椭圆曲线坐标（X/Y）与私钥标量 D 的字节长度。
	SM2CoordinateSize = 32

	// SM3Size 是 SM3 摘要的输出长度。
	SM3Size = 32

	// SM3BlockSize 是 SM3 的分组长度。
	SM3BlockSize = 64

	// SM4KeySize 是 SM4 密钥长度（必须精确等于该值）。
	SM4KeySize = 16

	// SM4BlockSize 是 SM4 的分组长度，也是 IV 长度与 CBC 密文长度的对齐单位。
	SM4BlockSize = 16

	// SM4IVSize 是 SM4-CBC 初始化向量的长度。
	SM4IVSize = 16

	// SM4GCMNonceSize 是 SM4-GCM 推荐（也是最优）的 nonce 长度。
	SM4GCMNonceSize = 12

	// SM4GCMTagSize 是 SM4-GCM 认证标签的长度。
	SM4GCMTagSize = 16
)

// ErrInvalidCiphertext 表示密文长度或结构非法（SM2 原始/ASN.1 密文、SM4 CBC 密文等）。
var ErrInvalidCiphertext = errors.New("guomi: 非法的密文")

// CipherSuiteInfo 返回当前加密套件的算法、工作模式与实现来源描述，
// 供 /health 探测与审计输出使用。
//
// 返回的 map 是每次调用新建的可变副本，调用方自行修改不会影响包内状态。
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

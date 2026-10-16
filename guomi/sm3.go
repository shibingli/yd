package guomi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"

	"github.com/tjfoc/gmsm/sm3"
	"github.com/yun-dev/yd/b64x"
)

const (
	// DefaultSaltLen 是 HashPassword 在 PasswordOptions.SaltLen <= 0 时使用的盐长度。
	DefaultSaltLen = 16

	// DefaultIterations 是 HashPassword 在 PasswordOptions.Iterations <= 0 时使用的迭代次数。
	DefaultIterations = 100000

	// PasswordPrefix 是口令散列编码串的前缀，完整格式见 HashPassword。
	PasswordPrefix = "sm3pbkdf2"

	// maxPasswordIterations 是 VerifyPassword 接受的最大迭代次数，用于抵御
	// 恶意编码串构造的超大迭代导致的拒绝服务。
	maxPasswordIterations = 10000000

	// maxPasswordSaltLen 是 VerifyPassword 接受的最大盐长度。
	maxPasswordSaltLen = 64
)

// ErrInvalidPasswordHash 表示口令散列编码串不是合法的
// sm3pbkdf2$<iter>$<saltB64>$<hashB64> 格式。
var ErrInvalidPasswordHash = errors.New("guomi: 非法的口令散列编码")

// Sum 计算 data 的 SM3 摘要（GB/T 32905-2016），返回定长 32 字节数组。
func Sum(data []byte) [32]byte {
	var out [32]byte
	copy(out[:], sm3.Sm3Sum(data))
	return out
}

// SumHex 计算 data 的 SM3 摘要并返回小写十六进制字符串。
func SumHex(data []byte) string {
	sum := Sum(data)
	return hex.EncodeToString(sum[:])
}

// New 返回一个可增量写入的 SM3 散列对象，实现 hash.Hash 接口
// （Size 为 32，BlockSize 为 64），可直接用于 crypto/hmac 等标准库设施。
func New() hash.Hash { return sm3.New() }

// HMAC 计算 HMAC-SM3（RFC 2104 构造，伪随机函数为 SM3），返回定长 32 字节摘要。
func HMAC(key, data []byte) [32]byte {
	mac := hmac.New(New, key)
	mac.Write(data)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// PBKDF2 按 RFC 8018 §5.2 使用 HMAC-SM3 作伪随机函数推导 keyLen 字节密钥。
//
// iter 小于 1 时按 1 处理，keyLen 小于 0 时按 0 处理；盐可以为空。
func PBKDF2(password, salt []byte, iter, keyLen int) []byte {
	if iter < 1 {
		iter = 1
	}
	if keyLen < 0 {
		keyLen = 0
	}

	prf := hmac.New(New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	t := make([]byte, hashLen)
	var counter [4]byte
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		counter[0] = byte(block >> 24)
		counter[1] = byte(block >> 16)
		counter[2] = byte(block >> 8)
		counter[3] = byte(block)
		prf.Write(counter[:])
		u = prf.Sum(u[:0])
		copy(t, u)
		for n := 1; n < iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for x := range t {
				t[x] ^= u[x]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

// PasswordOptions 配置口令散列的随机盐长度与 PBKDF2 迭代次数。
//
// 零值即默认参数（DefaultSaltLen 与 DefaultIterations）：
//
//	guomi.PasswordOptions{}                            // 16 字节盐、100000 次迭代
//	guomi.PasswordOptions{SaltLen: 8, Iterations: 1e5} // 自定义
type PasswordOptions struct {
	// SaltLen 是随机盐长度（字节），小于等于 0 时使用 DefaultSaltLen。
	SaltLen int

	// Iterations 是 PBKDF2 迭代次数，小于等于 0 时使用 DefaultIterations。
	Iterations int
}

// HashPassword 使用 PBKDF2-HMAC-SM3 对口令做加盐散列，返回可持久化的编码串：
//
//	sm3pbkdf2$<iter>$<saltB64>$<hashB64>
//
// 其中 <iter> 为十进制迭代次数，<saltB64> 与 <hashB64> 为 b64x 标准 Base64
// 编码的盐与 32 字节摘要。盐由 crypto/rand 生成，因此同一口令每次散列结果不同。
//
// 返回的错误仅可能来自随机数读取失败或参数越界。
func HashPassword(password string, o PasswordOptions) (string, error) {
	saltLen := o.SaltLen
	if saltLen <= 0 {
		saltLen = DefaultSaltLen
	}
	if saltLen > maxPasswordSaltLen {
		return "", fmt.Errorf("guomi: 盐长度 %d 超出上限 %d", saltLen, maxPasswordSaltLen)
	}
	iter := o.Iterations
	if iter <= 0 {
		iter = DefaultIterations
	}
	if iter > maxPasswordIterations {
		return "", fmt.Errorf("guomi: 迭代次数 %d 超出上限 %d", iter, maxPasswordIterations)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("guomi: 生成口令盐失败: %w", err)
	}
	sum := PBKDF2([]byte(password), salt, iter, SM3Size)

	var b strings.Builder
	b.Grow(len(PasswordPrefix) + 8 + b64x.StdEncoding.EncodedLen(saltLen) + b64x.StdEncoding.EncodedLen(SM3Size) + 3)
	b.WriteString(PasswordPrefix)
	b.WriteByte('$')
	b.WriteString(strconv.Itoa(iter))
	b.WriteByte('$')
	b.WriteString(b64x.StdEncoding.EncodeToString(salt))
	b.WriteByte('$')
	b.WriteString(b64x.StdEncoding.EncodeToString(sum))
	return b.String(), nil
}

// VerifyPassword 校验 password 是否与 encoded 匹配。
//
// 编码串格式非法时返回 (false, error)，错误可用 errors.Is 与
// ErrInvalidPasswordHash 判定；编码串合法但口令不匹配时返回 (false, nil)。
// 摘要比较使用 crypto/subtle.ConstantTimeCompare 做常量时间比较，避免计时侧信道。
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != PasswordPrefix {
		return false, fmt.Errorf("%w: 前缀或分段非法", ErrInvalidPasswordHash)
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > maxPasswordIterations {
		return false, fmt.Errorf("%w: 迭代次数 %q 非法", ErrInvalidPasswordHash, parts[1])
	}
	salt, err := b64x.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, fmt.Errorf("%w: 盐不是合法 Base64: %v", ErrInvalidPasswordHash, err)
	}
	if len(salt) == 0 || len(salt) > maxPasswordSaltLen {
		return false, fmt.Errorf("%w: 盐长度 %d 非法", ErrInvalidPasswordHash, len(salt))
	}
	want, err := b64x.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, fmt.Errorf("%w: 摘要不是合法 Base64: %v", ErrInvalidPasswordHash, err)
	}
	if len(want) != SM3Size {
		return false, fmt.Errorf("%w: 摘要长度 %d, want %d", ErrInvalidPasswordHash, len(want), SM3Size)
	}

	got := PBKDF2([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

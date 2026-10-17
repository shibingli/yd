package guomi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"hash"
	"strconv"
	"strings"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
	"github.com/tjfoc/gmsm/sm3"
)

const (
	// DefaultSaltLen is the salt length HashPassword uses when PasswordOptions.SaltLen <= 0. | DefaultSaltLen 是 HashPassword 在 PasswordOptions.SaltLen <= 0 时使用的盐长度。
	DefaultSaltLen = 16

	// DefaultIterations is the iteration count HashPassword uses when PasswordOptions.Iterations <= 0. | DefaultIterations 是 HashPassword 在 PasswordOptions.Iterations <= 0 时使用的迭代次数。
	DefaultIterations = 100000

	// PasswordPrefix is the prefix of the encoded password hash, whose full format is documented on HashPassword. | PasswordPrefix 是口令散列编码串的前缀，完整格式见 HashPassword。
	PasswordPrefix = "sm3pbkdf2"

	// maxPasswordIterations is the highest iteration count VerifyPassword accepts. | maxPasswordIterations 是 VerifyPassword 接受的最大迭代次数。
	// The cap defends against denial of service through a maliciously encoded hash. | 该上限用于抵御恶意编码串构造的超大迭代导致的拒绝服务。
	maxPasswordIterations = 10000000

	// maxPasswordSaltLen is the longest salt VerifyPassword accepts. | maxPasswordSaltLen 是 VerifyPassword 接受的最大盐长度。
	maxPasswordSaltLen = 64
)

var (
	// ErrInvalidPasswordHash reports an encoded hash that is not a valid sm3pbkdf2$<iter>$<saltB64>$<hashB64> string. | ErrInvalidPasswordHash 表示口令散列编码串不是合法的 sm3pbkdf2$<iter>$<saltB64>$<hashB64> 格式。
	ErrInvalidPasswordHash = errx.New("guomi: invalid password hash encoding | guomi：非法的口令散列编码")

	// ErrPasswordOptions reports password hashing options outside the supported range. | ErrPasswordOptions 表示口令散列参数超出支持范围。
	ErrPasswordOptions = errx.New("guomi: invalid password hashing options | guomi：非法的口令散列参数")
)

// Sum computes the SM3 digest of data per GB/T 32905-2016 and returns the fixed 32-byte array. | Sum 计算 data 的 SM3 摘要（GB/T 32905-2016），返回定长 32 字节数组。
func Sum(data []byte) [32]byte {
	var out [32]byte
	copy(out[:], sm3.Sm3Sum(data))
	return out
}

// SumHex computes the SM3 digest of data and returns it as a lowercase hex string. | SumHex 计算 data 的 SM3 摘要并返回小写十六进制字符串。
func SumHex(data []byte) string {
	sum := Sum(data)
	return hex.EncodeToString(sum[:])
}

// New returns an incrementally writable SM3 hash implementing hash.Hash, with Size 32 and BlockSize 64. | New 返回一个可增量写入的 SM3 散列对象，实现 hash.Hash 接口（Size 为 32，BlockSize 为 64）。
// It can be passed directly to standard library facilities such as crypto/hmac. | 可直接用于 crypto/hmac 等标准库设施。
//
// The returned value repairs a defect in gmsm. | 返回值修正了 gmsm 的实现缺陷。
// gmsm's (*SM3).Sum writes its argument in as message data and does not append the digest to in as hash.Hash requires, returning a fixed 32-byte slice instead. | gmsm 的 (*SM3).Sum 会把入参 in 当作消息写入散列状态，并且不按 hash.Hash 约定把摘要追加到 in 之后（返回定长 32 字节切片）。
// Using it inside this package stalls the key derivation loop, so the wrapper below follows the hash.Hash contract strictly. | 在本包内使用会导致密钥派生循环无法推进，因此下面的包装严格遵守 hash.Hash 契约。
func New() hash.Hash { return &sm3Hash{inner: sm3.New()} }

// sm3Hash wraps gmsm's SM3 so that Sum follows the hash.Hash contract. | sm3Hash 包装 gmsm 的 SM3，使其 Sum 行为符合 hash.Hash 契约。
type sm3Hash struct {
	inner hash.Hash
}

// Write appends data to the hash state and never returns an error. | Write 追加数据到散列状态，永不返回错误。
func (h *sm3Hash) Write(p []byte) (int, error) { return h.inner.Write(p) }

// Sum appends the current digest to in as hash.Hash requires and leaves the hash state untouched. | Sum 按 hash.Hash 约定把当前摘要追加到 in 之后并返回结果，不改变散列状态。
func (h *sm3Hash) Sum(in []byte) []byte {
	var digest [SM3Size]byte
	copy(digest[:], h.inner.Sum(nil))
	return append(in, digest[:]...)
}

// Reset clears the hash state so the same value can be reused. | Reset 复位散列状态，便于复用同一对象。
func (h *sm3Hash) Reset() { h.inner.Reset() }

// Size returns the digest length of 32 bytes. | Size 返回摘要长度（32 字节）。
func (h *sm3Hash) Size() int { return SM3Size }

// BlockSize returns the block length of 64 bytes. | BlockSize 返回分组长度（64 字节）。
func (h *sm3Hash) BlockSize() int { return SM3BlockSize }

// HMAC computes HMAC-SM3 per RFC 2104, using SM3 as the pseudo-random function, and returns the fixed 32-byte digest. | HMAC 计算 HMAC-SM3（RFC 2104 构造，伪随机函数为 SM3），返回定长 32 字节摘要。
func HMAC(key, data []byte) [32]byte {
	mac := hmac.New(New, key)
	mac.Write(data)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// PBKDF2 derives a keyLen byte key with HMAC-SM3 as pseudo-random function, per RFC 8018 §5.2. | PBKDF2 按 RFC 8018 §5.2 使用 HMAC-SM3 作伪随机函数推导 keyLen 字节密钥。
//
// An iter below 1 is treated as 1 and a keyLen below 0 as 0. | iter 小于 1 时按 1 处理，keyLen 小于 0 时按 0 处理。
// The salt may be empty. | 盐可以为空。
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

// PasswordOptions configures the random salt length and the PBKDF2 iteration count of a password hash. | PasswordOptions 配置口令散列的随机盐长度与 PBKDF2 迭代次数。
//
// The zero value means the defaults, DefaultSaltLen and DefaultIterations. | 零值即默认参数（DefaultSaltLen 与 DefaultIterations）。
//
//	guomi.PasswordOptions{}                            // 16-byte salt, 100000 iterations | 16 字节盐、100000 次迭代
//	guomi.PasswordOptions{SaltLen: 8, Iterations: 1e5} // custom values | 自定义参数
type PasswordOptions struct {
	// SaltLen is the random salt length in bytes; a value of 0 or less selects DefaultSaltLen. | SaltLen 是随机盐长度（字节），小于等于 0 时使用 DefaultSaltLen。
	SaltLen int

	// Iterations is the PBKDF2 iteration count; a value of 0 or less selects DefaultIterations. | Iterations 是 PBKDF2 迭代次数，小于等于 0 时使用 DefaultIterations。
	Iterations int
}

// HashPassword hashes a password with salted PBKDF2-HMAC-SM3 and returns a storable encoded string. | HashPassword 使用 PBKDF2-HMAC-SM3 对口令做加盐散列，返回可持久化的编码串：
//
//	sm3pbkdf2$<iter>$<saltB64>$<hashB64>
//
// Here <iter> is the decimal iteration count and <saltB64> and <hashB64> are the salt and the 32-byte digest in b64x standard Base64. | 其中 <iter> 为十进制迭代次数，<saltB64> 与 <hashB64> 为 b64x 标准 Base64 编码的盐与 32 字节摘要。
// The salt comes from crypto/rand, so hashing the same password twice gives different results. | 盐由 crypto/rand 生成，因此同一口令每次散列结果不同。
//
// The only possible errors are a failed random read and out-of-range parameters. | 返回的错误仅可能来自随机数读取失败或参数越界。
func HashPassword(password string, o PasswordOptions) (string, error) {
	saltLen := o.SaltLen
	if saltLen <= 0 {
		saltLen = DefaultSaltLen
	}
	if saltLen > maxPasswordSaltLen {
		return "", errx.Detail(ErrPasswordOptions, "salt length %[1]d exceeds the limit %[2]d | 盐长度 %[1]d 超出上限 %[2]d", saltLen, maxPasswordSaltLen)
	}
	iter := o.Iterations
	if iter <= 0 {
		iter = DefaultIterations
	}
	if iter > maxPasswordIterations {
		return "", errx.Detail(ErrPasswordOptions, "iteration count %[1]d exceeds the limit %[2]d | 迭代次数 %[1]d 超出上限 %[2]d", iter, maxPasswordIterations)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", errx.Wrap(err, ErrRandomSource)
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

// VerifyPassword checks whether password matches encoded. | VerifyPassword 校验 password 是否与 encoded 匹配。
//
// A malformed encoded string returns (false, error), where errors.Is reports ErrInvalidPasswordHash. | 编码串格式非法时返回 (false, error)，错误可用 errors.Is 与 ErrInvalidPasswordHash 判定。
// A well-formed string with a wrong password returns (false, nil). | 编码串合法但口令不匹配时返回 (false, nil)。
// Digests are compared with crypto/subtle.ConstantTimeCompare to avoid timing side channels. | 摘要比较使用 crypto/subtle.ConstantTimeCompare 做常量时间比较，避免计时侧信道。
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != PasswordPrefix {
		return false, errx.Detail(ErrInvalidPasswordHash, "bad prefix or field count | 前缀或分段非法")
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > maxPasswordIterations {
		return false, errx.Detail(ErrInvalidPasswordHash, "invalid iteration count %[1]q | 迭代次数 %[1]q 非法", parts[1])
	}
	salt, err := b64x.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, errx.WrapDetail(err, ErrInvalidPasswordHash, "salt is not valid Base64 | 盐不是合法 Base64")
	}
	if len(salt) == 0 || len(salt) > maxPasswordSaltLen {
		return false, errx.Detail(ErrInvalidPasswordHash, "invalid salt length %[1]d | 盐长度 %[1]d 非法", len(salt))
	}
	want, err := b64x.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, errx.WrapDetail(err, ErrInvalidPasswordHash, "digest is not valid Base64 | 摘要不是合法 Base64")
	}
	if len(want) != SM3Size {
		return false, errx.Detail(ErrInvalidPasswordHash, "digest length %[1]d, want %[2]d | 摘要长度 %[1]d，期望 %[2]d", len(want), SM3Size)
	}

	got := PBKDF2([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

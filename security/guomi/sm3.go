package guomi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"hash"
	"strconv"
	"strings"
	"sync"

	esm3 "github.com/emmansun/gmsm/sm3"

	"github.com/shibingli/ydx/base64"
	"github.com/shibingli/ydx/errs"
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
	ErrInvalidPasswordHash = errs.New("guomi: invalid password hash encoding | guomi：非法的口令散列编码")

	// ErrPasswordOptions reports password hashing options outside the supported range. | ErrPasswordOptions 表示口令散列参数超出支持范围。
	ErrPasswordOptions = errs.New("guomi: invalid password hashing options | guomi：非法的口令散列参数")
)

// Sum computes the SM3 digest of data per GB/T 32905-2016 and returns the fixed 32-byte array. | Sum 计算 data 的 SM3 摘要（GB/T 32905-2016），返回定长 32 字节数组。
func Sum(data []byte) [32]byte {
	return esm3.Sum(data)
}

// SumHex computes the SM3 digest of data and returns it as a lowercase hex string. | SumHex 计算 data 的 SM3 摘要并返回小写十六进制字符串。
func SumHex(data []byte) string {
	sum := Sum(data)
	return hex.EncodeToString(sum[:])
}

// New returns an incrementally writable SM3 hash implementing hash.Hash, with Size 32 and BlockSize 64. | New 返回一个可增量写入的 SM3 散列对象，实现 hash.Hash 接口（Size 为 32，BlockSize 为 64）。
// It can be passed directly to standard library facilities such as crypto/hmac. | 可直接用于 crypto/hmac 等标准库设施。
func New() hash.Hash { return esm3.New() }

// HMAC computes HMAC-SM3 per RFC 2104, using SM3 as the pseudo-random function, and returns the fixed 32-byte digest. | HMAC 计算 HMAC-SM3（RFC 2104 构造，伪随机函数为 SM3），返回定长 32 字节摘要。
//
// Both hash states come from a sync.Pool and are reset on reuse, so the steady-state
// hot path allocates nothing. The per-call HMAC construction in crypto/hmac would
// allocate a new key schedule on every invocation. | 两个散列状态取自 sync.Pool 并在复用时
// 复位，因此稳态热路径零分配；crypto/hmac 的逐调用构造每次都会新建密钥编排。
func HMAC(key, data []byte) [32]byte {
	st := hmacSM3Pool.Get().(*hmacSM3State)

	// K' is the key padded to one block, or SM3(key) when it is longer. | K' 是补零到一个分组的
	// 密钥；密钥超过一个分组时先做一次 SM3。
	clear(st.key[:])
	if len(key) > SM3BlockSize {
		sum := esm3.Sum(key)
		copy(st.key[:], sum[:])
	} else {
		copy(st.key[:], key)
	}

	for i := range st.pad {
		st.pad[i] = st.key[i] ^ 0x36
	}
	st.inner.Reset()
	_, _ = st.inner.Write(st.pad[:])
	_, _ = st.inner.Write(data)
	innerSum := st.inner.Sum(st.innerSum[:0])

	for i := range st.pad {
		st.pad[i] = st.key[i] ^ 0x5c
	}
	st.outer.Reset()
	_, _ = st.outer.Write(st.pad[:])
	_, _ = st.outer.Write(innerSum)
	_ = st.outer.Sum(st.out[:0])

	out := st.out
	// Drop key-derived material before the state returns to the pool. | 归还池前清除密钥派生材料。
	clear(st.key[:])
	clear(st.pad[:])
	clear(st.innerSum[:])
	clear(st.out[:])
	st.inner.Reset()
	st.outer.Reset()
	hmacSM3Pool.Put(st)
	return out
}

// hmacSM3State bundles two SM3 states with their scratch buffers. Keeping the
// buffers in the pooled object avoids the heap escape a local array would incur
// when passed through the hash.Hash interface. | hmacSM3State 把两个 SM3 状态与
// 暂存缓冲放在一起。缓冲位于池化对象上，避免局部数组经 hash.Hash 接口传参时逃逸到堆。
type hmacSM3State struct {
	inner    hash.Hash
	outer    hash.Hash
	key      [SM3BlockSize]byte
	pad      [SM3BlockSize]byte
	innerSum [SM3Size]byte
	out      [SM3Size]byte
}

// hmacSM3Pool recycles the paired SM3 states used by HMAC. | hmacSM3Pool 回收 HMAC 使用的成对 SM3 状态。
var hmacSM3Pool = sync.Pool{New: func() any {
	return &hmacSM3State{inner: esm3.New(), outer: esm3.New()}
}}

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
// Here <iter> is the decimal iteration count and <saltB64> and <hashB64> are the salt and the 32-byte digest in base64 standard Base64. | 其中 <iter> 为十进制迭代次数，<saltB64> 与 <hashB64> 为 base64 标准 Base64 编码的盐与 32 字节摘要。
// The salt comes from crypto/rand, so hashing the same password twice gives different results. | 盐由 crypto/rand 生成，因此同一口令每次散列结果不同。
//
// The only possible errors are a failed random read and out-of-range parameters. | 返回的错误仅可能来自随机数读取失败或参数越界。
func HashPassword(password string, o PasswordOptions) (string, error) {
	saltLen := o.SaltLen
	if saltLen <= 0 {
		saltLen = DefaultSaltLen
	}
	if saltLen > maxPasswordSaltLen {
		return "", errs.Detail(ErrPasswordOptions, "salt length %[1]d exceeds the limit %[2]d | 盐长度 %[1]d 超出上限 %[2]d", saltLen, maxPasswordSaltLen)
	}
	iter := o.Iterations
	if iter <= 0 {
		iter = DefaultIterations
	}
	if iter > maxPasswordIterations {
		return "", errs.Detail(ErrPasswordOptions, "iteration count %[1]d exceeds the limit %[2]d | 迭代次数 %[1]d 超出上限 %[2]d", iter, maxPasswordIterations)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", errs.Wrap(err, ErrRandomSource)
	}
	sum := PBKDF2([]byte(password), salt, iter, SM3Size)

	var b strings.Builder
	b.Grow(len(PasswordPrefix) + 8 + base64.StdEncoding.EncodedLen(saltLen) + base64.StdEncoding.EncodedLen(SM3Size) + 3)
	b.WriteString(PasswordPrefix)
	b.WriteByte('$')
	b.WriteString(strconv.Itoa(iter))
	b.WriteByte('$')
	b.WriteString(base64.StdEncoding.EncodeToString(salt))
	b.WriteByte('$')
	b.WriteString(base64.StdEncoding.EncodeToString(sum))
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
		return false, errs.Detail(ErrInvalidPasswordHash, "bad prefix or field count | 前缀或分段非法")
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > maxPasswordIterations {
		return false, errs.Detail(ErrInvalidPasswordHash, "invalid iteration count %[1]q | 迭代次数 %[1]q 非法", parts[1])
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, errs.WrapDetail(err, ErrInvalidPasswordHash, "salt is not valid Base64 | 盐不是合法 Base64")
	}
	if len(salt) == 0 || len(salt) > maxPasswordSaltLen {
		return false, errs.Detail(ErrInvalidPasswordHash, "invalid salt length %[1]d | 盐长度 %[1]d 非法", len(salt))
	}
	want, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, errs.WrapDetail(err, ErrInvalidPasswordHash, "digest is not valid Base64 | 摘要不是合法 Base64")
	}
	if len(want) != SM3Size {
		return false, errs.Detail(ErrInvalidPasswordHash, "digest length %[1]d, want %[2]d | 摘要长度 %[1]d，期望 %[2]d", len(want), SM3Size)
	}

	got := PBKDF2([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

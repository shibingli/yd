package guomi

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/tjfoc/gmsm/sm3"
	"github.com/shibingli/yd/b64x"
)

// sm3EmptyHex is the standard vector for the empty message from GB/T 32905-2016. | sm3EmptyHex 是 GB/T 32905-2016 附录给出的空串示例向量。
const sm3EmptyHex = "1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b"

// sm3LongMsgData builds a deterministic byte stream of n bytes, data[i] = i mod 251. | sm3LongMsgData 构造 n 字节的确定性数据流，data[i] = i mod 251。
// Every digest asserted over it was produced independently by OpenSSL 3.x EVP_sm3. | 基于该数据流断言的所有摘要均由 OpenSSL 3.x 的 EVP_sm3 独立生成。
func sm3LongMsgData(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}

// TestSM3StandardVector checks the official GB/T 32905-2016 vector for SM3("abc"). | TestSM3StandardVector 校验 SM3("abc") 的 GB/T 32905-2016 官方向量。
func TestSM3StandardVector(t *testing.T) {
	const want = "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"

	sum := Sum([]byte("abc"))
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("Sum([]byte(\"abc\")) = %s, want %s", got, want)
	}
	if got := SumHex([]byte("abc")); got != want {
		t.Fatalf("SumHex([]byte(\"abc\")) = %s, want %s", got, want)
	}
}

// TestSM3EmptyAndOneBlockVector checks the empty message and the official 512-bit message SM3("abcd" repeated 16 times). | TestSM3EmptyAndOneBlockVector 校验空串与官方的 512 比特消息 SM3("abcd" 重复 16 次)。
//
// GB/T 32905-2016 example 2 uses "abcd" repeated 16 times, which is 64 bytes or 512 bits. | GB/T 32905-2016 示例 2 的消息是 "abcd" 重复 16 次，即 64 字节（512 比特）。
func TestSM3EmptyAndOneBlockVector(t *testing.T) {
	if got := SumHex(nil); got != sm3EmptyHex {
		t.Fatalf("SumHex(nil) = %s, want %s", got, sm3EmptyHex)
	}
	if got := SumHex([]byte{}); got != sm3EmptyHex {
		t.Fatalf("SumHex([]byte{}) = %s, want %s", got, sm3EmptyHex)
	}

	const want = "debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732"
	msg := bytes.Repeat([]byte("abcd"), 16)
	if got := SumHex(msg); got != want {
		t.Fatalf("SumHex(\"abcd\"*16) = %s, want %s", got, want)
	}
}

// TestSM3LongVector checks messages around the 64-byte block boundary and a longer one. | TestSM3LongVector 校验 64 字节分组边界附近的消息与更长消息。
//
// The first group below is the GB/T 32905-2016 example 2 message, 64 bytes; note that "abcd" repeated 64 times is 256 bytes, not the 512-bit standard vector. | 下面第一组是 GB/T 32905-2016 示例 2 的 64 字节消息；注意 "abcd" 重复 64 次是 256 字节，并非该 512 比特标准向量。
// The remaining digests were generated independently with OpenSSL 3.x EVP_sm3 over sm3LongMsgData, and lock the padding boundaries 55/56/63/64/65/66. | 其余摘要由 OpenSSL 3.x EVP_sm3 对 sm3LongMsgData 独立生成，锁定 55/56/63/64/65/66 这些填充边界。
func TestSM3LongVector(t *testing.T) {
	if got := SumHex(bytes.Repeat([]byte("abcd"), 16)); got != "debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732" {
		t.Fatalf("SumHex(\"abcd\"*16) = %s, want debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732", got)
	}
	if got := SumHex(bytes.Repeat([]byte("abcd"), 64)); got != "b965764c8bebb091c7602b74afd34eefb531dccb4e0076d9b7cd813199b45971" {
		t.Fatalf("SumHex(\"abcd\"*64) = %s, want b965764c8bebb091c7602b74afd34eefb531dccb4e0076d9b7cd813199b45971", got)
	}

	vectors := []struct {
		n    int
		want string
	}{
		{55, "a79cf9dcee3404abf7f769698201647fd9d3ff61d629d0f58bb4b5579a427db8"},
		{56, "62f7363b15f4de76dd925c493b9d6d00d4ba0ef2a1f334c1d0f13b293aeb40d1"},
		{63, "6165e4cbb15cde01c6226e0015a47f710f8f8e1f2c296700033bb34d9212109c"},
		{64, "93566f236d157aae078d1ddb5cebdbba1520b5142e22a8915564345ba2ae1d63"},
		{65, "c886e6814be748285a10b28ae62ddacd85db830cd2cf3a2bfa2f729c15f63618"},
		{66, "e844f6e566c8e40ca5e650b938c4ea3c1ebb3f16a714f51f3303a089c09f9e62"},
		{128, "a9e7985473ca09df1510d83b572f72375430756c4a661b00724afeb8b75dd0a5"},
		{1000, "b38fc481302b502c3f2f6608d060c47c5b6bd8fd65e148b7cd3af4988245f48a"},
	}
	for _, v := range vectors {
		if got := SumHex(sm3LongMsgData(v.n)); got != v.want {
			t.Fatalf("SumHex(sm3LongMsgData(%d)) = %s, want %s", v.n, got, v.want)
		}
	}
}

// TestSM3IncrementalMatchesOneShot checks that chunked writes agree with a one-shot digest, against an independent OpenSSL digest. | TestSM3IncrementalMatchesOneShot 校验分块 Write 与一次性计算一致，并以独立的 OpenSSL 摘要为基准。
func TestSM3IncrementalMatchesOneShot(t *testing.T) {
	data := sm3LongMsgData(1 << 20) // 1 MiB, hundreds of blocks | 1 MiB，覆盖数百个分组
	const want = "2acebf826f0afabf4023a18152b047faa1b838dfe57626e79e9351592aaf2e57"

	if got := SumHex(data); got != want {
		t.Fatalf("SumHex(1 MiB) = %s, want %s (OpenSSL 3.x EVP_sm3 独立校验值)", got, want)
	}

	h := New()
	if h.Size() != SM3Size || h.BlockSize() != SM3BlockSize {
		t.Fatalf("New() 的 Size/BlockSize = %d/%d, want %d/%d", h.Size(), h.BlockSize(), SM3Size, SM3BlockSize)
	}

	// Drive Write with a set of distinct chunk sizes covering the boundaries: 1, an exact block, and a crossing block. | 用一组互不相同的块长驱动 Write，覆盖边界（1、分组整除、跨分组）。
	chunks := []int{1, 3, 7, 63, 64, 65, 127, 128, 1000, 4096}
	off, i := 0, 0
	for off < len(data) {
		n := chunks[i%len(chunks)]
		if off+n > len(data) {
			n = len(data) - off
		}
		if _, err := h.Write(data[off : off+n]); err != nil {
			t.Fatalf("Write 返回错误: %v", err)
		}
		off += n
		i++
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Fatalf("分块 Sum = %s, want %s", got, want)
	}

	// After Reset the same hash.Hash must be reusable. | Reset 后应可复用同一个 hash.Hash。
	h.Reset()
	if _, err := h.Write(data); err != nil {
		t.Fatalf("Reset 后 Write 返回错误: %v", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Fatalf("Reset 后 Sum = %s, want %s", got, want)
	}
}

// TestSM3HMAC cross-checks HMAC-SM3 against a hand written RFC 2104 construction. | TestSM3HMAC 用手写的 RFC 2104 构造交叉校验 HMAC-SM3。
func TestSM3HMAC(t *testing.T) {
	key := []byte("yd-guomi-hmac-key")
	longKey := randBytes(t, 100) // longer than a block, so it is hashed first | 超过分组长度，必须先做一次 SM3
	msg := []byte("国密 HMAC-SM3 载荷|hmac")

	manual := func(key, msg []byte) [32]byte {
		const block = 64
		k := make([]byte, block)
		if len(key) > block {
			s := sm3.Sm3Sum(key)
			copy(k, s)
		} else {
			copy(k, key)
		}
		ipad := make([]byte, block)
		opad := make([]byte, block)
		for i := range k {
			ipad[i] = k[i] ^ 0x36
			opad[i] = k[i] ^ 0x5c
		}
		inner := sm3.New()
		inner.Write(ipad)
		inner.Write(msg)
		outer := sm3.New()
		outer.Write(opad)
		outer.Write(inner.Sum(nil))
		var out [32]byte
		copy(out[:], outer.Sum(nil))
		return out
	}

	for _, k := range [][]byte{key, longKey, nil, []byte{}} {
		got := HMAC(k, msg)
		want := manual(k, msg)
		if got != want {
			t.Fatalf("HMAC(%x, msg) = %x, want %x", k, got, want)
		}
	}
	if HMAC(key, msg) == HMAC(key, []byte("other")) {
		t.Fatal("不同消息的 HMAC 不应相同")
	}
	if HMAC(key, msg) == HMAC([]byte("other-key"), msg) {
		t.Fatal("不同密钥的 HMAC 不应相同")
	}
}

// TestPBKDF2RFC8018 checks the block index concatenation and output length of PBKDF2-SM3 per RFC 8018 §5.2. | TestPBKDF2RFC8018 校验 PBKDF2-SM3 的分组序号拼接与输出长度（RFC 8018 §5.2）。
func TestPBKDF2RFC8018(t *testing.T) {
	password := []byte("password")
	salt := []byte("salt")

	// With iter=1, T_1 = PRF(password, salt || INT(1)). | iter=1 时 T_1 = PRF(password, salt || INT(1))。
	one := PBKDF2(password, salt, 1, SM3Size)
	want := HMAC(password, append(append([]byte{}, salt...), 0x00, 0x00, 0x00, 0x01))
	if !bytes.Equal(one, want[:]) {
		t.Fatalf("PBKDF2(iter=1) = %x, want %x", one, want)
	}
	if got := PBKDF2(password, salt, 2, SM3Size); bytes.Equal(got, one) {
		t.Fatal("不同迭代次数的 PBKDF2 输出不应相同")
	}
	if got := PBKDF2(password, []byte("other-salt"), 1, SM3Size); bytes.Equal(got, one) {
		t.Fatal("不同盐的 PBKDF2 输出不应相同")
	}
	if got := PBKDF2(password, salt, 3, 1); len(got) != 1 {
		t.Fatalf("keyLen=1 时输出长度 = %d, want 1", len(got))
	}
	if got := PBKDF2(password, salt, 3, SM3Size+16); len(got) != SM3Size+16 {
		t.Fatalf("keyLen=48 时输出长度 = %d, want 48", len(got))
	}
}

// TestPasswordHashAndVerify checks the encoding format, the default parameters and the verification semantics of password hashing. | TestPasswordHashAndVerify 校验口令散列的编码格式、默认参数与校验语义。
func TestPasswordHashAndVerify(t *testing.T) {
	const password = "P@ssw0rd-口令-国密"

	encoded, err := HashPassword(password, PasswordOptions{})
	if err != nil {
		t.Fatalf("HashPassword 返回错误: %v", err)
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 {
		t.Fatalf("编码格式 = %q, want 4 段以 $ 分隔", encoded)
	}
	if parts[0] != PasswordPrefix {
		t.Fatalf("编码前缀 = %q, want %q", parts[0], PasswordPrefix)
	}
	if parts[1] != "100000" {
		t.Fatalf("默认迭代次数 = %q, want 100000", parts[1])
	}
	salt, err := b64x.StdEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("盐不是合法 Base64: %v", err)
	}
	if len(salt) != DefaultSaltLen {
		t.Fatalf("默认盐长度 = %d, want %d", len(salt), DefaultSaltLen)
	}
	sum, err := b64x.StdEncoding.DecodeString(parts[3])
	if err != nil {
		t.Fatalf("散列不是合法 Base64: %v", err)
	}
	if len(sum) != SM3Size {
		t.Fatalf("散列长度 = %d, want %d", len(sum), SM3Size)
	}

	ok, err := VerifyPassword(encoded, password)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword(正确口令) = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = VerifyPassword(encoded, password+"x")
	if err != nil || ok {
		t.Fatalf("VerifyPassword(错误口令) = (%v, %v), want (false, nil)", ok, err)
	}
	ok, err = VerifyPassword(encoded, "")
	if err != nil || ok {
		t.Fatalf("VerifyPassword(空口令) = (%v, %v), want (false, nil)", ok, err)
	}

	// Hashing the same password twice must differ because of the random salt, while both encodings still verify. | 相同口令两次散列：盐不同 → 编码不同，但都能校验通过。
	encoded2, err := HashPassword(password, PasswordOptions{})
	if err != nil {
		t.Fatalf("第二次 HashPassword 返回错误: %v", err)
	}
	if encoded2 == encoded {
		t.Fatal("同一口令两次散列应因随机盐而不同")
	}
	for _, e := range []string{encoded, encoded2} {
		if ok, err := VerifyPassword(e, password); err != nil || !ok {
			t.Fatalf("VerifyPassword(%q) = (%v, %v), want (true, nil)", e, ok, err)
		}
	}

	// Custom parameters must be carried in the encoding. | 自定义参数：编码中必须携带实际使用的迭代数与盐长。
	custom, err := HashPassword("short", PasswordOptions{SaltLen: 8, Iterations: 1000})
	if err != nil {
		t.Fatalf("HashPassword(自定义参数) 返回错误: %v", err)
	}
	cparts := strings.Split(custom, "$")
	if cparts[1] != "1000" {
		t.Fatalf("自定义迭代次数 = %q, want 1000", cparts[1])
	}
	csalt, err := b64x.StdEncoding.DecodeString(cparts[2])
	if err != nil || len(csalt) != 8 {
		t.Fatalf("自定义盐 = %d 字节 (err=%v), want 8", len(csalt), err)
	}
	if ok, err := VerifyPassword(custom, "short"); err != nil || !ok {
		t.Fatalf("VerifyPassword(自定义参数) = (%v, %v), want (true, nil)", ok, err)
	}

	// The empty password must hash and verify as well. | 空口令也应可散列与校验。
	empty, err := HashPassword("", PasswordOptions{Iterations: 1})
	if err != nil {
		t.Fatalf("HashPassword(空口令) 返回错误: %v", err)
	}
	if ok, err := VerifyPassword(empty, ""); err != nil || !ok {
		t.Fatalf("VerifyPassword(空口令) = (%v, %v), want (true, nil)", ok, err)
	}

	// Malformed encodings must return an error rather than panic. | 非法编码必须报错而不是 panic。
	bad := []string{
		"",
		"x",
		PasswordPrefix,
		PasswordPrefix + "$",
		PasswordPrefix + "$100000$",
		PasswordPrefix + "$100000$AAAA",
		PasswordPrefix + "$abc$AAAA$AAAA",
		PasswordPrefix + "$100000$!!!!$AAAA",
		PasswordPrefix + "$100000$AAAA$!!!!",
		PasswordPrefix + "$0$AAAA$AAAA",
		PasswordPrefix + "$-1$AAAA$AAAA",
		"bcrypt$100000$AAAA$AAAA",
		PasswordPrefix + "$100000$AAAA$c2hvcnQ=",
	}
	for _, e := range bad {
		ok, err := VerifyPassword(e, password)
		if err == nil {
			t.Fatalf("VerifyPassword(%q) 未返回错误", e)
		}
		if ok {
			t.Fatalf("VerifyPassword(%q) = true, want false", e)
		}
		if !errors.Is(err, ErrInvalidPasswordHash) {
			t.Fatalf("VerifyPassword(%q) 的错误 %v 不是 ErrInvalidPasswordHash", e, err)
		}
	}
}

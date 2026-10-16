package guomi

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/tjfoc/gmsm/sm3"
	"github.com/yun-dev/yd/b64x"
)

// sm3Hex 是 GB/T 32905-2016 标准附录给出的示例向量。
const sm3EmptyHex = "1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b"

// TestSM3StandardVector 校验 SM3("abc") 的 GB/T 32905-2016 官方向量。
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

// TestSM3EmptyAndOneBlockVector 校验空串与"abcd"重复 64 次（消息扩展超过 16 字）的向量。
func TestSM3EmptyAndOneBlockVector(t *testing.T) {
	if got := SumHex(nil); got != sm3EmptyHex {
		t.Fatalf("SumHex(nil) = %s, want %s", got, sm3EmptyHex)
	}
	if got := SumHex([]byte{}); got != sm3EmptyHex {
		t.Fatalf("SumHex([]byte{}) = %s, want %s", got, sm3EmptyHex)
	}

	const want = "debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732"
	data := bytes.Repeat([]byte("abcd"), 64)
	if got := SumHex(data); got != want {
		t.Fatalf("SumHex(\"abcd\"*64) = %s, want %s", got, want)
	}
}

// TestSM3LongVector 校验标准向量 SM3("abcd"*64)。
func TestSM3LongVector(t *testing.T) {
	const want = "debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732"
	if got := SumHex(bytes.Repeat([]byte("abcd"), 64)); got != want {
		t.Fatalf("SumHex(\"abcd\"*64) = %s, want %s", got, want)
	}
}

// TestSM3IncrementalMatchesOneShot 校验分块 Write 与一次性计算的结果一致。
func TestSM3IncrementalMatchesOneShot(t *testing.T) {
	data := randBytes(t, 1<<20) // 1 MiB，覆盖数百个分组
	want := SumHex(data)

	h := New()
	if h.Size() != SM3Size || h.BlockSize() != SM3BlockSize {
		t.Fatalf("New() 的 Size/BlockSize = %d/%d, want %d/%d", h.Size(), h.BlockSize(), SM3Size, SM3BlockSize)
	}

	// 用一组互不相同的块长驱动 Write，覆盖边界（1、分组整除、跨分组）。
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

	// Reset 后应可复用同一个 hash.Hash。
	h.Reset()
	if _, err := h.Write(data); err != nil {
		t.Fatalf("Reset 后 Write 返回错误: %v", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		t.Fatalf("Reset 后 Sum = %s, want %s", got, want)
	}
}

// TestSM3HMAC 用手写的 RFC 2104 构造交叉校验 HMAC-SM3。
func TestSM3HMAC(t *testing.T) {
	key := []byte("yd-guomi-hmac-key")
	longKey := randBytes(t, 100) // 超过分组长度，必须先做一次 SM3
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

// TestPBKDF2RFC8018 校验 PBKDF2-SM3 的分组序号拼接与输出长度（RFC 8018 §5.2）。
func TestPBKDF2RFC8018(t *testing.T) {
	password := []byte("password")
	salt := []byte("salt")

	// iter=1 时 T_1 = PRF(password, salt || INT(1))。
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

// TestPasswordHashAndVerify 校验口令散列的编码格式、默认参数与校验语义。
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

	// 相同口令两次散列：盐不同 → 编码不同，但都能校验通过。
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

	// 自定义参数：编码中必须携带实际使用的迭代数与盐长。
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

	// 空口令也应可散列与校验。
	empty, err := HashPassword("", PasswordOptions{Iterations: 1})
	if err != nil {
		t.Fatalf("HashPassword(空口令) 返回错误: %v", err)
	}
	if ok, err := VerifyPassword(empty, ""); err != nil || !ok {
		t.Fatalf("VerifyPassword(空口令) = (%v, %v), want (true, nil)", ok, err)
	}

	// 非法编码必须报错而不是 panic。
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

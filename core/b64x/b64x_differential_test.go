package b64x

// This file holds differential, safety and round-trip tests shared by every encoding variant.
// 本文件包含所有编码变体共享的差分、安全与往返测试。

import (
	"bytes"
	stdb64 "encoding/base64"
	"errors"
	"strings"
	"testing"
)

// codecVariant pairs one yd encoding with the standard library encoding it must match.
// | codecVariant 把 yd 编码器与它必须对齐的标准库编码器配成一对。
type codecVariant struct {
	name   string
	enc    *Encoding
	std    *stdb64.Encoding
	padded bool
	url    bool
}

// codecVariants returns the four RFC 4648 variants under test. | codecVariants 返回参与测试的
// 四个 RFC 4648 变体。
func codecVariants() []codecVariant {
	return []codecVariant{
		{"StdEncoding", StdEncoding, stdb64.StdEncoding, true, false},
		{"RawStdEncoding", RawStdEncoding, stdb64.RawStdEncoding, false, false},
		{"URLEncoding", URLEncoding, stdb64.URLEncoding, true, true},
		{"RawURLEncoding", RawURLEncoding, stdb64.RawURLEncoding, false, true},
	}
}

// edgeBytes builds n deterministic bytes spanning every interesting class: NUL, DEL, the
// high-bit boundary, 0xFF, ASCII punctuation and ordinary text. | edgeBytes 构造 n 个确定性
// 字节，覆盖所有关键类别：NUL、DEL、最高位边界、0xFF、ASCII 标点与普通文本。
func edgeBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		switch i % 7 {
		case 0:
			b[i] = 0x00
		case 1:
			b[i] = 0x7F
		case 2:
			b[i] = 0x80
		case 3:
			b[i] = 0xFF
		case 4:
			b[i] = byte(0x20 + i%0x5F)
		case 5:
			b[i] = byte(i * 31)
		default:
			b[i] = byte(i)
		}
	}
	return b
}

// checkDecodeDifferential decodes encoded with both implementations and requires identical
// results and identical written lengths. The destination is exactly DecodedLen long, so any
// write past it would panic through the Go bounds checks.
// | checkDecodeDifferential 用两个实现解码 encoded，要求结果与写入长度完全一致。目标缓冲区
// 长度恰为 DecodedLen，任何越界写都会被 Go 的边界检查捕获。
func checkDecodeDifferential(t *testing.T, v codecVariant, encoded []byte) {
	t.Helper()
	need := v.enc.DecodedLen(len(encoded))
	ydOut := make([]byte, need)
	ydN, ydErr := v.enc.Decode(ydOut, encoded)
	if ydErr != nil {
		t.Fatalf("%s: Decode(%q) 返回错误: %v", v.name, encoded, ydErr)
	}
	stdOut := make([]byte, v.std.DecodedLen(len(encoded)))
	stdN, stdErr := v.std.Decode(stdOut, encoded)
	if stdErr != nil {
		t.Fatalf("%s: 标准库解码失败（对拍前提不成立）: %v", v.name, stdErr)
	}
	if ydN != stdN || !bytes.Equal(ydOut[:ydN], stdOut[:stdN]) {
		t.Fatalf("%s: 解码结果不一致（yd n=%d，标准库 n=%d）", v.name, ydN, stdN)
	}
}

// TestDifferentialAllLengths cross-checks encoding, decoding and round-trips against
// encoding/base64 for every length 0..1024 and every alphabet/padding combination.
// | TestDifferentialAllLengths 对 0..1024 的每个长度以及每种字母表/填充组合，与
// encoding/base64 对拍编码、解码与往返结果。
func TestDifferentialAllLengths(t *testing.T) {
	for _, v := range codecVariants() {
		v := v
		t.Run(v.name, func(t *testing.T) {
			for n := 0; n <= 1024; n++ {
				src := edgeBytes(n)
				want := v.std.EncodeToString(src)
				if got := v.enc.EncodeToString(src); got != want {
					t.Fatalf("n=%d: EncodeToString = %q，期望 %q", n, got, want)
				}
				dst := make([]byte, v.enc.EncodedLen(n))
				v.enc.Encode(dst, src)
				if string(dst) != want {
					t.Fatalf("n=%d: Encode = %q，期望 %q", n, dst, want)
				}
				if v.padded {
					wantPads := (3 - n%3) % 3
					if got := strings.Count(string(dst), "="); got != wantPads && n > 0 {
						t.Fatalf("n=%d: 填充符数量 = %d，期望 %d（%q）", n, got, wantPads, dst)
					}
				} else if strings.ContainsRune(string(dst), '=') {
					t.Fatalf("n=%d: 无填充变体不应出现 '=': %q", n, dst)
				}
				checkDecodeDifferential(t, v, []byte(want))

				back, err := v.enc.DecodeString(want)
				if err != nil || !bytes.Equal(back, src) {
					t.Fatalf("n=%d: 往返失败（err=%v，got %d 字节，期望 %d 字节）", n, err, len(back), n)
				}
			}
		})
	}
}

// complexSamples returns inputs that mix Chinese, emoji, ASCII punctuation, whitespace,
// CR/LF, URL characters and raw byte edge cases. | complexSamples 返回混合中文、emoji、
// ASCII 标点、空白、CR/LF、URL 字符与原始字节边界的输入。
func complexSamples() []string {
	return []string{
		"云开发框架 YunDev：全栈 Web 框架！",
		"你好，世界 🌏🚀 — emoji + 中文 + ASCII + / _ - =",
		"line1\r\nline2\nline3\r\n",
		"a+b/c=d?e-f_g~h & (query)=1",
		" \t 空白 whitespace \r\n\t 混排 ",
		string([]byte{0x00, 0x7F, 0x80, 0xFF, 'A', '+', '/', '-', '_', '=', '=', '\r', '\n'}),
		"简体中文繁體中文日本語한국어 العربية русский Ελληνικά",
		strings.Repeat("云", 10) + strings.Repeat("a+b/", 5) + "🌏",
	}
}

// TestComplexInputDifferential encodes and decodes mixed-language, emoji and punctuation
// samples of every variant, comparing byte-for-byte with encoding/base64 and round-tripping.
// | TestComplexInputDifferential 对每个变体编码并解码中英混排、emoji 与标点样本，与
// encoding/base64 逐字节对拍并验证往返。
func TestComplexInputDifferential(t *testing.T) {
	for _, v := range codecVariants() {
		v := v
		t.Run(v.name, func(t *testing.T) {
			for _, s := range complexSamples() {
				src := []byte(s)
				want := v.std.EncodeToString(src)
				if got := v.enc.EncodeToString(src); got != want {
					t.Fatalf("样本 %q: EncodeToString = %q，期望 %q", s, got, want)
				}
				checkDecodeDifferential(t, v, []byte(want))
				back, err := v.enc.DecodeString(want)
				if err != nil || !bytes.Equal(back, src) {
					t.Fatalf("样本 %q: 往返失败（err=%v）", s, err)
				}
				// Also round-trip through the append-style API. | 同时经追加式接口往返。
				appended := v.enc.AppendEncode(nil, src)
				if string(appended) != want {
					t.Fatalf("样本 %q: AppendEncode = %q，期望 %q", s, appended, want)
				}
				decoded, err := v.enc.AppendDecode(nil, appended)
				if err != nil || !bytes.Equal(decoded, src) {
					t.Fatalf("样本 %q: AppendDecode 往返失败（err=%v）", s, err)
				}
			}
		})
	}
}

// TestThirtyTwoByteBoundary focuses on 31, 32 and 33 bytes, where the native AVX2 path
// switches between tail handling and the exact 32-byte kernel. | TestThirtyTwoByteBoundary
// 聚焦 31、32、33 字节：自研 AVX2 路径在这些长度上会在尾部处理与 32 字节直达内核之间切换。
func TestThirtyTwoByteBoundary(t *testing.T) {
	samples := complexSamples()
	for _, v := range codecVariants() {
		v := v
		t.Run(v.name, func(t *testing.T) {
			for n := 29; n <= 35; n++ {
				for _, src := range [][]byte{edgeBytes(n), randomBytes(n), []byte(strings.Repeat("云", n)[:n])} {
					if len(src) != n {
						continue
					}
					want := v.std.EncodeToString(src)
					if got := v.enc.EncodeToString(src); got != want {
						t.Fatalf("n=%d: EncodeToString = %q，期望 %q", n, got, want)
					}
					if n == 32 {
						if v.padded && len(want) != 44 {
							t.Fatalf("n=32: 填充编码长度 = %d，期望 44", len(want))
						}
						if !v.padded && len(want) != 43 {
							t.Fatalf("n=32: 无填充编码长度 = %d，期望 43", len(want))
						}
					}
					checkDecodeDifferential(t, v, []byte(want))
					back, err := v.enc.DecodeString(want)
					if err != nil || !bytes.Equal(back, src) {
						t.Fatalf("n=%d: 往返失败（err=%v）", n, err)
					}
				}
			}
			// Explicit 32-byte payloads built from the complex samples. | 用复杂样本显式
			// 构造 32 字节负载。
			for _, s := range samples {
				src := make([]byte, 32)
				copy(src, s)
				want := v.std.EncodeToString(src)
				if got := v.enc.EncodeToString(src); got != want {
					t.Fatalf("32 字节样本 %q: EncodeToString = %q，期望 %q", s, got, want)
				}
				checkDecodeDifferential(t, v, []byte(want))
			}
		})
	}
}

// TestDecodeInvalidDifferential compares the error behaviour with encoding/base64 for
// illegal characters, misplaced or excessive padding, data after padding, wrong alphabets
// and truncated input. An error from the standard library must always be an error here;
// success must produce identical bytes. | TestDecodeInvalidDifferential 对非法字符、错位或
// 过量填充、填充后仍有数据、错误字母表与截断输入，与 encoding/base64 比较错误行为：标准库
// 报错的输入这里必须报错；标准库成功的输入必须产出相同字节。
func TestDecodeInvalidDifferential(t *testing.T) {
	inputs := []string{
		"", "A", "AA", "AAA", "AAAAA", "AAAAAA", "AAAAAAA",
		"!", "!!!!", "AA!A", "AAAA!", "AA\x00A", "\x80\x80\x80\x80",
		"AA=A", "A=AA", "=AAA", "AA==AA", "AAAA=AAA", "AA=====", "A===",
		"AABB=", "AABB==", "AB==", "ABC=", "ABCD=",
		"AA==", "AAA=", "AAAA=", "AAAA==", "AA\r\n==", "AA=\n=", "AA==\r\n",
		"AA\r\nAA", "AAAA\n", "\nAAAA", "\r\n", "AA\r\n!",
		"++==", "//==", "--==", "__==", "AA+/", "AA-_",
	}
	for _, v := range codecVariants() {
		v := v
		t.Run(v.name, func(t *testing.T) {
			for _, in := range inputs {
				src := []byte(in)
				need := v.enc.DecodedLen(len(src))
				ydOut := make([]byte, need)
				ydN, ydErr := v.enc.Decode(ydOut, src)
				stdNeed := v.std.DecodedLen(len(src))
				stdOut := make([]byte, stdNeed)
				stdN, stdErr := v.std.Decode(stdOut, src)
				if stdErr != nil {
					if ydErr == nil {
						t.Fatalf("%q: 标准库报错 %v，自研实现却成功（n=%d）", in, stdErr, ydN)
					}
					if !errors.Is(ydErr, ErrInvalidData) {
						t.Fatalf("%q: 错误应可用 ErrInvalidData 判定，实际 %v", in, ydErr)
					}
					if ydN != 0 {
						t.Fatalf("%q: 解码失败时 n 应为 0，实际 %d", in, ydN)
					}
					if got, err := v.enc.DecodeString(in); err == nil || got != nil {
						t.Fatalf("%q: DecodeString 应返回 (nil, err)，实际 (%d 字节, %v)", in, len(got), err)
					}
					continue
				}
				if ydErr != nil {
					t.Fatalf("%q: 标准库成功，自研实现报错 %v", in, ydErr)
				}
				if ydN != stdN || !bytes.Equal(ydOut[:ydN], stdOut[:stdN]) {
					t.Fatalf("%q: 解码结果不一致（yd n=%d，标准库 n=%d）", in, ydN, stdN)
				}
			}
		})
	}
}

// TestDecodeNeverWritesPastDecodedLen decodes into a destination whose length is exactly
// DecodedLen for every length 0..512 and all variants; an out-of-bounds write would panic
// through the Go bounds checks. A second pass uses a destination with 8 spare bytes (the
// widest 256-bit SIMD store overhang) and verifies they stay untouched.
// | TestDecodeNeverWritesPastDecodedLen 对 0..512 的每个长度与所有变体，把数据解码到长度恰为
// DecodedLen 的目标缓冲区；越界写会被 Go 的边界检查捕获。第二遍使用多 8 字节（256 位 SIMD
// 存储的最大盈余）的缓冲区，并验证这些字节保持不变。
func TestDecodeNeverWritesPastDecodedLen(t *testing.T) {
	const simdGranularity = 8
	for _, v := range codecVariants() {
		v := v
		t.Run(v.name, func(t *testing.T) {
			for n := 0; n <= 512; n++ {
				src := edgeBytes(n)
				encoded := v.enc.EncodeToString(src)
				need := v.enc.DecodedLen(len(encoded))
				got, err := v.enc.Decode(make([]byte, need), []byte(encoded))
				if err != nil {
					t.Fatalf("n=%d: Decode 失败: %v", n, err)
				}
				if got != n {
					t.Fatalf("n=%d: 解码字节数 = %d", n, got)
				}
				buf := make([]byte, need+2*simdGranularity)
				for i := need; i < len(buf); i++ {
					buf[i] = 0x5A
				}
				if _, err := v.enc.Decode(buf, []byte(encoded)); err != nil {
					t.Fatalf("n=%d: 带余量 Decode 失败: %v", n, err)
				}
				for i := need + simdGranularity; i < len(buf); i++ {
					if buf[i] != 0x5A {
						t.Fatalf("n=%d: 写入超过 DecodedLen+%d（偏移 %d）", n, simdGranularity, i-need)
					}
				}
				// Decoding into a buffer shorter than DecodedLen must fail cleanly. | 目标
				// 缓冲区短于 DecodedLen 时必须干净失败。
				if need > 0 {
					if _, err := v.enc.Decode(make([]byte, need-1), []byte(encoded)); !errors.Is(err, ErrShortBuffer) {
						t.Fatalf("n=%d: 应返回 ErrShortBuffer，实际 %v", n, err)
					}
				}
			}
		})
	}
}

// checkDecodeLikeStdlib compares one decode with encoding/base64: whenever the standard
// library fails this implementation must fail with ErrInvalidData, and whenever it succeeds
// both byte sequences and lengths must match. | checkDecodeLikeStdlib 把一次解码与
// encoding/base64 比较：标准库失败时本实现必须以 ErrInvalidData 失败；标准库成功时两者的
// 字节序列与长度必须一致。
func checkDecodeLikeStdlib(t *testing.T, enc *Encoding, std *stdb64.Encoding, in []byte) {
	t.Helper()
	stdOut := make([]byte, std.DecodedLen(len(in)))
	stdN, stdErr := std.Decode(stdOut, in)
	out := make([]byte, enc.DecodedLen(len(in)))
	n, err := enc.Decode(out, in)
	if stdErr != nil {
		if err == nil {
			t.Fatalf("%q: 标准库报错 %v，自研实现却成功（n=%d）", in, stdErr, n)
		}
		if !errors.Is(err, ErrInvalidData) {
			t.Fatalf("%q: 错误应可用 ErrInvalidData 判定，实际 %v", in, err)
		}
		if n != 0 {
			t.Fatalf("%q: 解码失败时 n 应为 0，实际 %d", in, n)
		}
		return
	}
	if err != nil {
		t.Fatalf("%q: 标准库成功，自研实现报错 %v", in, err)
	}
	if n != stdN || !bytes.Equal(out[:n], stdOut[:stdN]) {
		t.Fatalf("%q: 解码结果不一致（yd n=%d，标准库 n=%d）", in, n, stdN)
	}
}

// validAlphabetText returns n characters that are valid in both the standard and the URL
// alphabets. | validAlphabetText 返回 n 个在标准与 URL 字母表中都合法的字符。
func validAlphabetText(n int) []byte {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[i%len(alphabet)]
	}
	return b
}

// TestRawRejectsPaddingOver16Chars covers raw decoding of padded input with 16 or more
// characters. On AVX-512 VBMI2 hosts the standard alphabet takes the 16..64-character tail
// loop of decodeSIMD512VBMI2, which must hand any '=' to the scalar path instead of decoding
// it as a data character; raw alphabets must reject it outright.
// | TestRawRejectsPaddingOver16Chars 覆盖 16 字符及以上带填充输入的 raw 解码。在支持
// AVX-512 VBMI2 的主机上，标准字母表会进入 decodeSIMD512VBMI2 的 16..64 字符尾部循环，
// 该循环必须把 '=' 交给标量路径，而不是当成数据字符解码；raw 字母表必须直接拒绝。
func TestRawRejectsPaddingOver16Chars(t *testing.T) {
	variants := []struct {
		name string
		enc  *Encoding
		std  *stdb64.Encoding
	}{
		{"RawStdEncoding", RawStdEncoding, stdb64.RawStdEncoding},
		{"RawURLEncoding", RawURLEncoding, stdb64.RawURLEncoding},
	}
	for _, v := range variants {
		v := v
		t.Run(v.name, func(t *testing.T) {
			for n := 16; n <= 96; n += 4 {
				base := validAlphabetText(n)
				for _, pos := range []int{0, 15, n / 2, 31, 47, 63, n - 2, n - 1} {
					if pos < 0 || pos >= n {
						continue
					}
					in := append([]byte(nil), base...)
					in[pos] = '='
					checkDecodeLikeStdlib(t, v.enc, v.std, in)
				}
			}
			// Valid padded text fed to a raw decoder must follow the standard library:
			// reject when it carries padding, decode identically otherwise.
			// | 把合法的带填充文本交给 raw 解码器：含填充时必须与标准库一样报错，
			// 不含填充时结果必须一致。
			for n := 10; n <= 70; n++ {
				src := edgeBytes(n)
				checkDecodeLikeStdlib(t, v.enc, v.std, []byte(StdEncoding.EncodeToString(src)))
				checkDecodeLikeStdlib(t, v.enc, v.std, []byte(URLEncoding.EncodeToString(src)))
			}
		})
	}
}

// TestPaddingOnlyAtEnd verifies that padded decoders reject '=' anywhere except the final
// one or two characters, including padding followed by data and padding on a 64-character
// window boundary. | TestPaddingOnlyAtEnd 验证带填充解码器拒绝除末尾一两个字符之外的
// '='，覆盖填充之后仍有数据以及 64 字符窗口边界上的填充。
func TestPaddingOnlyAtEnd(t *testing.T) {
	variants := []struct {
		name string
		enc  *Encoding
		std  *stdb64.Encoding
	}{
		{"StdEncoding", StdEncoding, stdb64.StdEncoding},
		{"URLEncoding", URLEncoding, stdb64.URLEncoding},
	}
	for _, v := range variants {
		v := v
		t.Run(v.name, func(t *testing.T) {
			for n := 16; n <= 128; n += 4 {
				base := validAlphabetText(n)
				for _, pos := range []int{0, 15, 16, 31, 47, 63, 64, n / 2, n - 3, n - 2, n - 1} {
					if pos < 0 || pos >= n {
						continue
					}
					in := append([]byte(nil), base...)
					in[pos] = '='
					checkDecodeLikeStdlib(t, v.enc, v.std, in)
				}
			}
			// A valid padded encoding followed by a data character must be rejected. | 合法
			// 填充编码之后追加一个数据字符必须被拒绝。
			for n := 10; n <= 70; n++ {
				for _, enc := range []*Encoding{StdEncoding, URLEncoding} {
					padded := []byte(enc.EncodeToString(edgeBytes(n)))
					if !bytes.Contains(padded, []byte("=")) {
						continue
					}
					in := append(append([]byte(nil), padded...), 'A')
					checkDecodeLikeStdlib(t, v.enc, v.std, in)
				}
			}
		})
	}
}

// TestRoundTripMixedContent verifies Encode -> Decode restores mixed Chinese/emoji/symbol
// strings byte-for-byte in every variant. | TestRoundTripMixedContent 验证每个变体中
// Encode -> Decode 都能逐字节还原中英/emoji/符号混排字符串。
func TestRoundTripMixedContent(t *testing.T) {
	samples := []string{
		"中文🚀English mixed 123",
		"emoji 👨‍👩‍👧‍👦 family and 中文标点，。！？",
		"URL 参数 a=1&b=2+c=d/e-f_g",
		"control \x00\x01\x7F and high \x80\xFF",
		strings.Repeat("混合Mixed🌏+/-_=", 40),
	}
	for _, v := range codecVariants() {
		v := v
		t.Run(v.name, func(t *testing.T) {
			for _, s := range samples {
				src := []byte(s)
				encoded := v.enc.EncodeToString(src)
				back, err := v.enc.DecodeString(encoded)
				if err != nil {
					t.Fatalf("样本 %q: 解码失败: %v", s, err)
				}
				if !bytes.Equal(back, src) {
					t.Fatalf("样本 %q: 往返结果不一致", s)
				}
				// The same payload must produce the standard library's text. | 同一负载必须
				// 产出标准库的文本。
				if want := v.std.EncodeToString(src); encoded != want {
					t.Fatalf("样本 %q: 编码与标准库不一致", s)
				}
			}
		})
	}
}

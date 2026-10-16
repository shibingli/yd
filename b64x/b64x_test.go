package b64x

import (
	"bytes"
	stdb64 "encoding/base64"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

// randomBytes returns n bytes of deterministic pseudo-random data (fixed seed, guaranteeing reproducible tests). | randomBytes 返回 n 字节确定性伪随机数据（固定种子，保证测试可复现）。
func randomBytes(n int) []byte {
	b := make([]byte, n)
	rnd := rand.New(rand.NewSource(int64(n) + 1))
	for i := range b {
		b[i] = byte(rnd.Intn(256))
	}
	return b
}

// TestStdEncodingMatchesStdlib cross-checks inputs of length 0..256 one by one against encoding/base64: | TestStdEncodingMatchesStdlib 对长度 0..256 的输入逐一与 encoding/base64 对拍：
// encoding, decoding and append-style codec are all identical, and the error cases containing undecodable characters are covered. | 编码、解码、追加式编解码全部一致，并覆盖含不可解码字符的错误情形。
func TestStdEncodingMatchesStdlib(t *testing.T) {
	for n := 0; n <= 256; n++ {
		src := randomBytes(n)
		wantEnc := stdb64.StdEncoding.EncodeToString(src)

		if got := StdEncoding.EncodeToString(src); got != wantEnc {
			t.Fatalf("n=%d: EncodeToString = %q，期望 %q", n, got, wantEnc)
		}

		dst := make([]byte, StdEncoding.EncodedLen(n))
		StdEncoding.Encode(dst, src)
		if string(dst) != wantEnc {
			t.Fatalf("n=%d: Encode = %q，期望 %q", n, dst, wantEnc)
		}
		if got := string(StdEncoding.AppendEncode(nil, src)); got != wantEnc {
			t.Fatalf("n=%d: AppendEncode = %q，期望 %q", n, got, wantEnc)
		}

		// The decode path is cross-checked against the standard library (length, content and error all identical). | 解码路径与标准库对拍（长度、内容、错误三者一致）。
		stdOut := make([]byte, stdb64.StdEncoding.DecodedLen(len(wantEnc)))
		stdN, stdErr := stdb64.StdEncoding.Decode(stdOut, []byte(wantEnc))
		out := make([]byte, StdEncoding.DecodedLen(len(wantEnc)))
		gotN, gotErr := StdEncoding.Decode(out, []byte(wantEnc))
		if stdErr != nil || gotErr != nil {
			t.Fatalf("n=%d: 合法输入解码失败，标准库 err=%v，b64x err=%v", n, stdErr, gotErr)
		}
		if gotN != stdN || !bytes.Equal(out[:gotN], stdOut[:stdN]) || !bytes.Equal(out[:gotN], src) {
			t.Fatalf("n=%d: Decode 结果不一致（b64x n=%d，标准库 n=%d）", n, gotN, stdN)
		}

		back, err := StdEncoding.DecodeString(wantEnc)
		if err != nil {
			t.Fatalf("n=%d: DecodeString 失败: %v", n, err)
		}
		if !bytes.Equal(back, src) {
			t.Fatalf("n=%d: DecodeString 结果不一致", n)
		}

		const prefix = "pre:"
		appended, err := StdEncoding.AppendDecode([]byte(prefix), []byte(wantEnc))
		if err != nil {
			t.Fatalf("n=%d: AppendDecode 失败: %v", n, err)
		}
		if string(appended[:len(prefix)]) != prefix || !bytes.Equal(appended[len(prefix):], src) {
			t.Fatalf("n=%d: AppendDecode 未正确追加（got %d 字节）", n, len(appended))
		}

		// Newline tolerance: encoding/base64 ignores \r and \n, so the semantics of both backends must be identical. | 换行容错：encoding/base64 会忽略 \r 与 \n，两个后端语义必须一致。
		var wrapped strings.Builder
		for i, c := range wantEnc {
			if i > 0 && i%4 == 0 {
				wrapped.WriteString("\r\n")
			}
			wrapped.WriteRune(c)
		}
		wrappedEnc := wrapped.String()
		stdWrapped, stdWrappedErr := stdb64.StdEncoding.DecodeString(wrappedEnc)
		if stdWrappedErr != nil {
			t.Fatalf("n=%d: 标准库应忽略换行，却报错 %v", n, stdWrappedErr)
		}
		gotWrapped, err := StdEncoding.DecodeString(wrappedEnc)
		if err != nil {
			t.Fatalf("n=%d: 含换行的输入解码失败: %v", n, err)
		}
		if !bytes.Equal(gotWrapped, stdWrapped) {
			t.Fatalf("n=%d: 含换行输入的解码结果与标准库不一致", n)
		}
		wrappedOut := make([]byte, StdEncoding.DecodedLen(len(wrappedEnc)))
		wn, err := StdEncoding.Decode(wrappedOut, []byte(wrappedEnc))
		if err != nil || !bytes.Equal(wrappedOut[:wn], src) {
			t.Fatalf("n=%d: 含换行输入的 Decode 失败或结果不一致: n=%d err=%v", n, wn, err)
		}

		if n == 0 {
			continue
		}

		// Undecodable character: replacing one character in the middle of the encoded string with '!' must make both implementations report an error. | 不可解码字符：把编码串中间一个字符替换为 '!'，两个实现都必须报错。
		bad := []byte(wantEnc)
		bad[len(bad)/2] = '!'
		if _, err := StdEncoding.Decode(make([]byte, StdEncoding.DecodedLen(len(bad))), bad); err == nil {
			t.Fatalf("n=%d: 含 '!' 的输入应返回解码错误", n)
		}
		if _, err := stdb64.StdEncoding.Decode(make([]byte, stdb64.StdEncoding.DecodedLen(len(bad))), bad); err == nil {
			t.Fatalf("n=%d: 标准库同样应拒绝该输入（对拍前提不成立）", n)
		}
		if got, err := StdEncoding.DecodeString(string(bad)); err == nil || got != nil {
			t.Fatalf("n=%d: DecodeString 对非法输入应返回 (nil, err)，实际 (%d 字节, %v)", n, len(got), err)
		}
	}
}

// TestURLAndRawVariants covers the URL-safe and unpadded variants: outputs are cross-checked against the standard library one by one, | TestURLAndRawVariants 覆盖 URL 安全与无填充两类变体：输出与标准库逐一对拍，
// and the differences in alphabet and padding character are verified. | 并校验字母表与填充符的差异。
func TestURLAndRawVariants(t *testing.T) {
	variants := []struct {
		name    string
		enc     *Encoding
		std     *stdb64.Encoding
		padded  bool
		urlSafe bool
	}{
		{"StdEncoding", StdEncoding, stdb64.StdEncoding, true, false},
		{"RawStdEncoding", RawStdEncoding, stdb64.RawStdEncoding, false, false},
		{"URLEncoding", URLEncoding, stdb64.URLEncoding, true, true},
		{"RawURLEncoding", RawURLEncoding, stdb64.RawURLEncoding, false, true},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			for n := 0; n <= 64; n++ {
				src := randomBytes(n)
				want := v.std.EncodeToString(src)
				got := v.enc.EncodeToString(src)
				if got != want {
					t.Fatalf("n=%d: EncodeToString = %q，期望 %q", n, got, want)
				}
				// The padding character only appears in padded encodings whose final group is shorter than 3 bytes. | 填充符只在最后不足 3 字节且带填充的编码中出现。
				wantPad := v.padded && n%3 != 0
				if strings.ContainsRune(got, '=') != wantPad {
					t.Fatalf("n=%d: 填充符不符合预期（期望填充=%v）: %q", n, wantPad, got)
				}
				if v.urlSafe && strings.ContainsAny(got, "+/") {
					t.Fatalf("n=%d: URL 安全字母表不应出现 '+' 或 '/': %q", n, got)
				}
				if !v.urlSafe && strings.ContainsAny(got, "-_") {
					t.Fatalf("n=%d: 标准字母表不应出现 '-' 或 '_': %q", n, got)
				}

				out := make([]byte, v.enc.DecodedLen(len(want)))
				nn, err := v.enc.Decode(out, []byte(want))
				if err != nil {
					t.Fatalf("n=%d: Decode 失败: %v", n, err)
				}
				stdOut := make([]byte, v.std.DecodedLen(len(want)))
				stdN, stdErr := v.std.Decode(stdOut, []byte(want))
				if stdErr != nil || nn != stdN || !bytes.Equal(out[:nn], src) {
					t.Fatalf("n=%d: Decode 与标准库不一致（b64x n=%d err=%v，标准库 n=%d err=%v）", n, nn, err, stdN, stdErr)
				}

				back, err := v.enc.DecodeString(want)
				if err != nil || !bytes.Equal(back, src) {
					t.Fatalf("n=%d: DecodeString 失败或结果不一致: %v", n, err)
				}
			}

			// Deterministic sample of the alphabet difference: 0xFB 0xEF 0xBE is "++++" under the standard alphabet and "----" under the URL alphabet. | 字母表差异的确定性样例：0xFB 0xEF 0xBE 在标准字母表下为 "++++"、URL 下为 "----"。
			sample := []byte{0xFB, 0xEF, 0xBE}
			if got, want := v.enc.EncodeToString(sample), v.std.EncodeToString(sample); got != want {
				t.Fatalf("样例编码 = %q，期望 %q", got, want)
			}
			switch v.name {
			case "StdEncoding", "RawStdEncoding":
				if got := v.enc.EncodeToString(sample); !strings.HasPrefix(got, "++++") {
					t.Fatalf("标准字母表样例 = %q，期望以 \"++++\" 开头", got)
				}
			default:
				if got := v.enc.EncodeToString(sample); !strings.HasPrefix(got, "----") {
					t.Fatalf("URL 字母表样例 = %q，期望以 \"----\" 开头", got)
				}
			}
		})
	}
}

// TestAppendEncodePreallocates verifies that AppendEncode reuses the underlying array when capacity is sufficient (the &dst[0] pointer stays unchanged). | TestAppendEncodePreallocates 验证容量充足时 AppendEncode 复用底层数组（&dst[0] 指针不变）。
func TestAppendEncodePreallocates(t *testing.T) {
	src := randomBytes(200)
	want := StdEncoding.EncodeToString(src)

	dst := make([]byte, 8, 1024)
	copy(dst, "prefix!!")
	first := &dst[0]

	out := StdEncoding.AppendEncode(dst, src)
	if &out[0] != first {
		t.Fatal("容量充足时 AppendEncode 不应重新分配底层数组")
	}
	if len(out) != 8+StdEncoding.EncodedLen(len(src)) {
		t.Fatalf("结果长度 = %d，期望 %d", len(out), 8+StdEncoding.EncodedLen(len(src)))
	}
	if string(out[:8]) != "prefix!!" {
		t.Fatalf("原有前缀被破坏: %q", out[:8])
	}
	if string(out[8:]) != want {
		t.Fatalf("追加内容 = %q，期望 %q", out[8:], want)
	}

	// When capacity is insufficient it must grow, and must not corrupt the existing data. | 容量不足时必须扩容，且不能破坏原有数据。
	small := make([]byte, 8, 8)
	copy(small, "prefix!!")
	out = StdEncoding.AppendEncode(small, src)
	if string(out[:8]) != "prefix!!" || string(out[8:]) != want {
		t.Fatal("容量不足时 AppendEncode 结果不正确")
	}
}

// TestAppendDecodeReusesBuffer verifies that AppendDecode reuses the underlying array when capacity is sufficient and keeps append semantics. | TestAppendDecodeReusesBuffer 验证容量充足时 AppendDecode 复用底层数组并保持追加语义。
func TestAppendDecodeReusesBuffer(t *testing.T) {
	src := randomBytes(200)
	encoded := StdEncoding.EncodeToString(src)

	dst := make([]byte, 4, 1024)
	copy(dst, "pre:")
	first := &dst[0]

	out, err := StdEncoding.AppendDecode(dst, []byte(encoded))
	if err != nil {
		t.Fatalf("AppendDecode 失败: %v", err)
	}
	if &out[0] != first {
		t.Fatal("容量充足时 AppendDecode 不应重新分配底层数组")
	}
	if string(out[:4]) != "pre:" {
		t.Fatalf("原有前缀被破坏: %q", out[:4])
	}
	if !bytes.Equal(out[4:], src) {
		t.Fatalf("解码结果不一致（got %d 字节，期望 %d 字节）", len(out)-4, len(src))
	}

	// Consecutive appends: the second call still reuses the same underlying array. | 连续追加：第二次调用仍复用同一底层数组。
	out, err = StdEncoding.AppendDecode(out, []byte(encoded))
	if err != nil {
		t.Fatalf("第二次 AppendDecode 失败: %v", err)
	}
	if &out[0] != first {
		t.Fatal("容量充足时连续 AppendDecode 不应重新分配底层数组")
	}
	if !bytes.Equal(out[4+len(src):], src) {
		t.Fatal("连续追加结果不一致")
	}
}

// TestAppendDecodeKeepsCapacity verifies that decoding does not write past the available capacity: sentinel bytes beyond the result are touched by at most 1 byte of the internal write margin. | TestAppendDecodeKeepsCapacity 验证解码不会越过可用容量写入：结果之外的哨兵字节至多被内部写余量触碰 1 字节。
func TestAppendDecodeKeepsCapacity(t *testing.T) {
	src := randomBytes(300)
	encoded := StdEncoding.EncodeToString(src)

	const sentinel = 0xAA
	raw := make([]byte, 4096)
	for i := range raw {
		raw[i] = sentinel
	}

	out, err := StdEncoding.AppendDecode(raw[:0], []byte(encoded))
	if err != nil {
		t.Fatalf("AppendDecode 失败: %v", err)
	}
	if !bytes.Equal(out, src) {
		t.Fatal("解码结果不一致")
	}
	for i := len(out) + 1; i < len(raw); i++ {
		if raw[i] != sentinel {
			t.Fatalf("索引 %d（结果末尾之后 %d 字节）被越界写入", i, i-len(out))
		}
	}
}

// TestDecodeErrorOnInvalid covers the error semantics of invalid input: it returns (0, err), and the error can be classified with errors.Is. | TestDecodeErrorOnInvalid 覆盖非法输入的错误语义：返回 (0, err)，错误可用 errors.Is 判定。
func TestDecodeErrorOnInvalid(t *testing.T) {
	cases := []struct {
		name string
		enc  *Encoding
		std  *stdb64.Encoding
		in   string
	}{
		{"StdEncoding/全非法字符", StdEncoding, stdb64.StdEncoding, "!!!!"},
		{"StdEncoding/中间非法字符", StdEncoding, stdb64.StdEncoding, "AA!A"},
		{"StdEncoding/填充符错位", StdEncoding, stdb64.StdEncoding, "AA=A"},
		{"StdEncoding/长度非法", StdEncoding, stdb64.StdEncoding, "AAAAA"},
		{"StdEncoding/空字节", StdEncoding, stdb64.StdEncoding, "AA\x00A"},
		{"RawStdEncoding/非法字符", RawStdEncoding, stdb64.RawStdEncoding, "AA!A"},
		{"RawStdEncoding/不允许填充", RawStdEncoding, stdb64.RawStdEncoding, "AA=="},
		{"RawStdEncoding/长度为 1", RawStdEncoding, stdb64.RawStdEncoding, "A"},
		{"URLEncoding/标准字母表非法", URLEncoding, stdb64.URLEncoding, "++=="},
		{"RawURLEncoding/标准字母表非法", RawURLEncoding, stdb64.RawURLEncoding, "AA/A"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.std.Decode(make([]byte, c.std.DecodedLen(len(c.in))), []byte(c.in)); err == nil {
				t.Fatal("标准库同样应拒绝该输入（对拍前提不成立）")
			}

			dst := make([]byte, c.enc.DecodedLen(len(c.in)))
			n, err := c.enc.Decode(dst, []byte(c.in))
			if err == nil {
				t.Fatalf("Decode 应返回错误，实际成功（n=%d）", n)
			}
			if n != 0 {
				t.Fatalf("解码失败时 n 应为 0，实际 %d", n)
			}
			if !errors.Is(err, ErrInvalidData) {
				t.Fatalf("errors.Is(err, ErrInvalidData) 应为 true，实际 err=%v", err)
			}

			if got, err := c.enc.DecodeString(c.in); err == nil || got != nil {
				t.Fatalf("DecodeString 应返回 (nil, err)，实际 (%d 字节, %v)", len(got), err)
			}

			const prefix = "keep"
			out, err := c.enc.AppendDecode([]byte(prefix), []byte(c.in))
			if err == nil {
				t.Fatal("AppendDecode 应返回错误")
			}
			if string(out) != prefix {
				t.Fatalf("解码失败时 AppendDecode 不应改动 dst，实际 %q", out)
			}
			if !errors.Is(err, ErrInvalidData) {
				t.Fatalf("AppendDecode 错误应可用 ErrInvalidData 判定，实际 err=%v", err)
			}
		})
	}

	t.Run("目标缓冲区过短", func(t *testing.T) {
		if _, err := StdEncoding.Decode(make([]byte, 1), []byte("AAAAAA==")); !errors.Is(err, ErrShortBuffer) {
			t.Fatalf("应返回 ErrShortBuffer，实际 %v", err)
		}
		if _, err := StdEncoding.Decode(nil, []byte("AAAAAA==")); !errors.Is(err, ErrShortBuffer) {
			t.Fatalf("nil 目标缓冲区应返回 ErrShortBuffer，实际 %v", err)
		}
	})

	t.Run("合法输入不报错", func(t *testing.T) {
		back, err := StdEncoding.DecodeString("AAAA")
		if err != nil || !bytes.Equal(back, []byte{0, 0, 0}) {
			t.Fatalf("合法输入解码失败: %v（%v）", err, back)
		}
	})
}

// TestEncodedLenDecodedLenMatchStdlib verifies that the length functions match encoding/base64 exactly. | TestEncodedLenDecodedLenMatchStdlib 校验长度函数与 encoding/base64 完全一致。
func TestEncodedLenDecodedLenMatchStdlib(t *testing.T) {
	variants := []struct {
		name string
		enc  *Encoding
		std  *stdb64.Encoding
	}{
		{"StdEncoding", StdEncoding, stdb64.StdEncoding},
		{"RawStdEncoding", RawStdEncoding, stdb64.RawStdEncoding},
		{"URLEncoding", URLEncoding, stdb64.URLEncoding},
		{"RawURLEncoding", RawURLEncoding, stdb64.RawURLEncoding},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			for n := 0; n <= 512; n++ {
				if got, want := v.enc.EncodedLen(n), v.std.EncodedLen(n); got != want {
					t.Fatalf("n=%d: EncodedLen = %d，期望 %d", n, got, want)
				}
				if got, want := v.enc.DecodedLen(n), v.std.DecodedLen(n); got != want {
					t.Fatalf("n=%d: DecodedLen = %d，期望 %d", n, got, want)
				}
			}
		})
	}
}

// TestBackendNameIsKnown verifies that Backend returns only one of the two agreed names. | TestBackendNameIsKnown 校验 Backend 只返回两个约定名称之一。
func TestBackendNameIsKnown(t *testing.T) {
	switch got := Backend(); got {
	case "base64x", "encoding/base64":
		t.Logf("当前 Base64 后端：%s", got)
	default:
		t.Fatalf("Backend() = %q，期望 \"base64x\" 或 \"encoding/base64\"", got)
	}
}

// TestEncodingsAreUsable confirms that all four encoding variants are initialized and can work independently. | TestEncodingsAreUsable 确认四个编码变体均已初始化且可独立工作。
func TestEncodingsAreUsable(t *testing.T) {
	src := []byte("yun-dev yd framework")
	for name, enc := range map[string]*Encoding{
		"StdEncoding":    StdEncoding,
		"RawStdEncoding": RawStdEncoding,
		"URLEncoding":    URLEncoding,
		"RawURLEncoding": RawURLEncoding,
	} {
		if enc == nil {
			t.Fatalf("%s 为 nil", name)
		}
		s := enc.EncodeToString(src)
		back, err := enc.DecodeString(s)
		if err != nil || !bytes.Equal(back, src) {
			t.Fatalf("%s 往返失败: %v", name, err)
		}
	}
}

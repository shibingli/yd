package cachex

import (
	"bytes"
	"testing"
)

func TestWireRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		version uint64
		flags   uint8
		data    []byte
	}{
		{name: "plain | 普通", version: 1, flags: 0, data: []byte("hello")},
		{name: "empty data | 空数据", version: 7, flags: FlagTombstone, data: nil},
		{name: "negative | 负缓存", version: 42, flags: FlagNegative, data: []byte("null")},
		{name: "large version | 大版本号", version: 1 << 53, flags: 0, data: bytes.Repeat([]byte{0xAB}, 128)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := WireEncode(tc.version, tc.flags, tc.data)
			if len(raw) != WireHeaderSize+len(tc.data) {
				t.Fatalf("encoded length = %d, want %d | 编码长度不符", len(raw), WireHeaderSize+len(tc.data))
			}
			version, flags, data, ok := WireDecode(raw)
			if !ok {
				t.Fatal("WireDecode reported a malformed envelope | WireDecode 判定信封畸形")
			}
			if version != tc.version || flags != tc.flags || !bytes.Equal(data, tc.data) {
				t.Fatalf("decoded = (%d,%d,%q), want (%d,%d,%q) | 解码结果不符",
					version, flags, data, tc.version, tc.flags, tc.data)
			}
		})
	}
}

func TestWireDecodeAliasesInput(t *testing.T) {
	raw := WireEncode(3, 0, []byte("abc"))
	_, _, data, ok := WireDecode(raw)
	if !ok || string(data) != "abc" {
		t.Fatalf("decode failed: ok=%v data=%q | 解码失败", ok, data)
	}
	raw[WireHeaderSize] = 'z'
	if string(data) != "zbc" {
		t.Fatalf("data does not alias raw: %q | data 未与 raw 共享底层数组", data)
	}
}

func TestWireDecodeRejectsMalformed(t *testing.T) {
	valid := WireEncode(1, 0, []byte("x"))
	for n := 0; n < WireHeaderSize; n++ {
		if _, _, _, ok := WireDecode(valid[:n]); ok {
			t.Fatalf("accepted a %d-byte prefix | 接受了 %d 字节的截断载荷", n, n)
		}
	}
	badMagic := append([]byte(nil), valid...)
	badMagic[0] = 'X'
	if _, _, _, ok := WireDecode(badMagic); ok {
		t.Fatal("accepted foreign magic | 接受了外来 magic")
	}
	if _, _, _, ok := WireDecode(nil); ok {
		t.Fatal("accepted nil | 接受了 nil")
	}
}

func TestWireAppendReusesDestination(t *testing.T) {
	prefix := []byte("head:")
	out := WireAppend(prefix, 9, FlagNegative, []byte("v"))
	if !bytes.HasPrefix(out, []byte("head:YD")) {
		t.Fatalf("prefix missing: %q | 前缀丢失", out)
	}
	version, flags, data, ok := WireDecode(out[len(prefix):])
	if !ok || version != 9 || flags != FlagNegative || string(data) != "v" {
		t.Fatalf("appended envelope mismatch: %d %d %q %v | 追加信封不符", version, flags, data, ok)
	}
}

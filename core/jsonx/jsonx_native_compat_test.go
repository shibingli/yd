//go:build amd64 && !jsonx_std

package jsonx_test

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/core/jsonx"
)

// nativeInner is the nested payload used by the compatibility tests. | nativeInner 是兼容性测试使用的嵌套载荷。
type nativeInner struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// nativeOuter exercises every type shape the native backend must decode. | nativeOuter 覆盖原生后端必须解码的所有类型形态。
type nativeOuter struct {
	When  time.Time          `json:"when"`
	Any   any                `json:"any"`
	Raw   stdjson.RawMessage `json:"raw"`
	Ptr   *nativeInner       `json:"ptr"`
	List  []nativeInner      `json:"list"`
	Blob  []byte             `json:"blob"`
	ID    int64              `json:"id"`
	U     uint64             `json:"u"`
	F     float64            `json:"f"`
	Inner nativeInner        `json:"inner"`
	Name  string             `json:"name"`
	Empty string             `json:"empty"`
}

// nativeWide has more than eight fields, selecting the map-based field lookup path. | nativeWide
// 的字段数超过八个，用于覆盖基于映射的字段查找路径。
type nativeWide struct {
	S int
	A int
	B int
	C int
	D int
	E int
	F int
	G int
	H int
}

// nativeEmbedBase is the value-embedded struct. | nativeEmbedBase 是按值嵌入的结构体。
type nativeEmbedBase struct {
	Base   string `json:"base"`
	Shared int    `json:"shared"`
}

// nativeEmbedPtr is the pointer-embedded struct. | nativeEmbedPtr 是按指针嵌入的结构体。
type nativeEmbedPtr struct {
	Ptr string `json:"ptr"`
}

// nativeEmbedded mixes value and pointer embedding. | nativeEmbedded 混合了值嵌入与指针嵌入。
type nativeEmbedded struct {
	nativeEmbedBase
	*nativeEmbedPtr
	ID int `json:"id"`
}

// NativeEmbedValue is an exported value-embedded struct. | NativeEmbedValue 是按值嵌入的导出结构体。
type NativeEmbedValue struct {
	Base   string `json:"base"`
	Shared int    `json:"shared"`
}

// NativeEmbedPointer is an exported pointer-embedded struct. | NativeEmbedPointer 是按指针嵌入的导出结构体。
type NativeEmbedPointer struct {
	Ptr string `json:"ptr"`
}

// NativeEmbeddedExported is the normal embedding case: both value and pointer embedding of
// exported struct types must decode and encode. | NativeEmbeddedExported 是常规嵌入场景：导出
// 结构体的值嵌入与指针嵌入都必须可编解码。
type NativeEmbeddedExported struct {
	NativeEmbedValue
	*NativeEmbedPointer
	ID int `json:"id"`
}

// nativeUpper is a custom (Un)Marshaler that upper-cases on encode and lower-cases on decode. | nativeUpper
// 是自定义 (Un)Marshaler：编码转大写、解码转小写。
type nativeUpper struct {
	S string
}

// MarshalJSON encodes the value upper-cased. | MarshalJSON 以大写形式编码。
func (u nativeUpper) MarshalJSON() ([]byte, error) {
	return stdjson.Marshal(strings.ToUpper(u.S))
}

// UnmarshalJSON decodes the value lower-cased. | UnmarshalJSON 以小写形式解码。
func (u *nativeUpper) UnmarshalJSON(b []byte) error {
	var s string
	if err := stdjson.Unmarshal(b, &s); err != nil {
		return errors.New("nativeUpper expects a JSON string | nativeUpper 期望 JSON 字符串")
	}
	u.S = strings.ToLower(s)
	return nil
}

// nativeBadMarshaler always returns invalid JSON. | nativeBadMarshaler 永远返回非法 JSON。
type nativeBadMarshaler struct{}

// MarshalJSON returns a syntactically invalid object. | MarshalJSON 返回语法非法的对象。
func (nativeBadMarshaler) MarshalJSON() ([]byte, error) { return []byte(`{"x":}`), nil }

// nativeText is a TextMarshaler / TextUnmarshaler pair. | nativeText 是一对 TextMarshaler / TextUnmarshaler。
type nativeText struct {
	V string
}

// MarshalText encodes the value with a fixed prefix. | MarshalText 以固定前缀编码。
func (n nativeText) MarshalText() ([]byte, error) { return []byte("txt:" + n.V), nil }

// UnmarshalText decodes the value with the fixed prefix stripped. | UnmarshalText 解码并去掉固定前缀。
func (n *nativeText) UnmarshalText(b []byte) error {
	n.V = strings.TrimPrefix(string(b), "txt:")
	return nil
}

// nativeNode is a self-referential type used for cycle detection. | nativeNode 是用于环检测的自引用类型。
type nativeNode struct {
	Next *nativeNode `json:"next"`
}

// nativeDecodeCase is one differential decode case. | nativeDecodeCase 是一个差分解码用例。
type nativeDecodeCase struct {
	newTarget func() any
	doc       string
	name      string
}

// nativeDecodeCases is the shared differential decode table. | nativeDecodeCases 是共用的差分解码表。
func nativeDecodeCases() []nativeDecodeCase {
	outer := `{
		"when":"2026-10-18T20:00:00.123456789+08:00",
		"any":{"nested":[1,2.5,true,null,"x",{"k":"v"}]},
		"raw":{"keep":[1,2,3]},
		"ptr":{"name":"p","tags":["a","b"]},
		"list":[{"name":"l1","tags":[]},{"name":"l2","tags":null}],
		"blob":"aGVsbG8=",
		"id":-9223372036854775808,
		"u":18446744073709551615,
		"f":1.7976931348623157e308,
		"inner":{"name":"中文😀","tags":["<","&"]},
		"name":"name"
	}`
	ptrTarget := func() any { return &nativeOuter{} }
	return []nativeDecodeCase{
		{name: "rich_object", doc: outer, newTarget: ptrTarget},
		{name: "case_insensitive_keys", doc: `{"ID":7,"INNER":{"NAME":"n","TAGS":["t"]},"Name":"a","NAME":"b"}`, newTarget: ptrTarget},
		{name: "wide_struct_fold_ascii", doc: `{"s":6}`, newTarget: func() any { return &nativeWide{} }},
		{name: "wide_struct_fold_unicode", doc: "{\"ſ\":5}", newTarget: func() any { return &nativeWide{} }},
		{name: "wide_struct_exact_wins", doc: `{"S":1,"s":2}`, newTarget: func() any { return &nativeWide{} }},
		{name: "embedded_fields", doc: `{"base":"b","shared":1,"ptr":"p","id":7}`, newTarget: func() any { return &nativeEmbedded{} }},
		{name: "embedded_pointer_created", doc: `{"ptr":"p"}`, newTarget: func() any { return &nativeEmbedded{} }},
		{name: "embedded_duplicate_keys", doc: `{"base":"a","base":"b"}`, newTarget: func() any { return &nativeEmbedded{} }},
		{name: "embedded_exported_types", doc: `{"base":"b","shared":1,"ptr":"p","id":7}`, newTarget: func() any { return &NativeEmbeddedExported{} }},
		{name: "embedded_exported_pointer_alloc", doc: `{"ptr":"p"}`, newTarget: func() any { return &NativeEmbeddedExported{} }},
		{name: "duplicate_keys_last_wins", doc: `{"id":1,"id":2,"id":-3}`, newTarget: ptrTarget},
		{name: "escaped_keys", doc: `{"id":9,"name":"esc"}`, newTarget: ptrTarget},
		{name: "nulls", doc: `{"any":null,"raw":null,"ptr":null,"list":null,"blob":null,"inner":null,"name":null}`, newTarget: ptrTarget},
		{name: "empty_object", doc: `{}`, newTarget: ptrTarget},
		{name: "any_document", doc: outer, newTarget: func() any { return new(any) }},
		{name: "map_any", doc: `{"a":{"b":[1,2,{"c":null}]},"":"empty"}`, newTarget: func() any { return new(map[string]any) }},
		{name: "slice_any", doc: `[1,2.5,true,null,"x",[1],{"k":"v"}]`, newTarget: func() any { return new([]any) }},
		{name: "nested_structs", doc: `{"ptr":{"name":"p","tags":["x"]},"list":[{"name":"l","tags":["y"]},{"name":"m","tags":null}]}`, newTarget: ptrTarget},
		{name: "unicode_escapes", doc: `{"name":"中文😀"}`, newTarget: ptrTarget},
		{name: "lone_high_surrogate", doc: `{"name":"a\ud800b"}`, newTarget: ptrTarget},
		{name: "lone_low_surrogate", doc: `{"name":"\udc00"}`, newTarget: ptrTarget},
		{name: "double_high_surrogate", doc: `{"name":"\ud800\ud800"}`, newTarget: ptrTarget},
		{name: "raw_u2028", doc: "{\"name\":\"a b c\"}", newTarget: ptrTarget},
		{name: "escaped_u2028", doc: `{"name":"  "}`, newTarget: ptrTarget},
		{name: "invalid_utf8", doc: "{\"name\":\"a\xffb\xc3\"}", newTarget: ptrTarget},
		{name: "html_chars", doc: `{"name":"<a href=\"x\">&</a>"}`, newTarget: ptrTarget},
		{name: "int64_bounds", doc: `{"id":9223372036854775807}`, newTarget: ptrTarget},
		{name: "int64_overflow", doc: `{"id":9223372036854775808}`, newTarget: ptrTarget},
		{name: "uint64_max", doc: `{"u":18446744073709551615}`, newTarget: ptrTarget},
		{name: "uint64_overflow", doc: `{"u":18446744073709551616}`, newTarget: ptrTarget},
		{name: "uint_negative", doc: `{"u":-1}`, newTarget: ptrTarget},
		{name: "int_fraction", doc: `{"id":1.5}`, newTarget: ptrTarget},
		{name: "int_exponent", doc: `{"id":1e3}`, newTarget: ptrTarget},
		{name: "float_exponent", doc: `{"f":1.25e-3}`, newTarget: ptrTarget},
		{name: "float_overflow", doc: `{"f":1e999}`, newTarget: ptrTarget},
		{name: "float_big_integer", doc: `{"f":100000000000000000000}`, newTarget: ptrTarget},
		{name: "negative_zero_float", doc: `{"f":-0}`, newTarget: ptrTarget},
		{name: "time_null", doc: `{"when":null}`, newTarget: ptrTarget},
		{name: "time_zulu", doc: `{"when":"2026-10-18T12:00:00Z"}`, newTarget: ptrTarget},
		{name: "time_epoch", doc: `{"when":"1970-01-01T00:00:00Z"}`, newTarget: ptrTarget},
		{name: "time_pre_epoch", doc: `{"when":"1969-12-31T23:59:59Z"}`, newTarget: ptrTarget},
		{name: "time_leap_day", doc: `{"when":"2000-02-29T23:59:59Z"}`, newTarget: ptrTarget},
		{name: "time_year_zero", doc: `{"when":"0000-01-01T00:00:00Z"}`, newTarget: ptrTarget},
		{name: "time_year_max", doc: `{"when":"9999-12-31T23:59:59Z"}`, newTarget: ptrTarget},
		{name: "time_bad_month", doc: `{"when":"2026-13-01T00:00:00Z"}`, newTarget: ptrTarget},
		{name: "time_bad_day", doc: `{"when":"2026-02-30T00:00:00Z"}`, newTarget: ptrTarget},
		{name: "time_bad_leap", doc: `{"when":"1900-02-29T00:00:00Z"}`, newTarget: ptrTarget},
		{name: "time_bad_hour", doc: `{"when":"2026-01-01T24:00:00Z"}`, newTarget: ptrTarget},
		{name: "time_bad_minute", doc: `{"when":"2026-01-01T00:60:00Z"}`, newTarget: ptrTarget},
		{name: "time_bad_second", doc: `{"when":"2026-01-01T00:00:60Z"}`, newTarget: ptrTarget},
		{name: "time_lowercase", doc: `{"when":"2026-01-01t00:00:00z"}`, newTarget: ptrTarget},
		{name: "time_nano", doc: `{"when":"2026-10-18T12:00:00.123456789Z"}`, newTarget: ptrTarget},
		{name: "time_invalid", doc: `{"when":"not-a-time"}`, newTarget: ptrTarget},
		{name: "custom_unmarshaler", doc: `"HELLO"`, newTarget: func() any { return &nativeUpper{} }},
		{name: "custom_unmarshaler_bad", doc: `1`, newTarget: func() any { return &nativeUpper{} }},
		{name: "raw_message_preserved", doc: `{"raw": { "a" : [1, 2] } }`, newTarget: ptrTarget},
		{name: "raw_message_invalid", doc: `{"raw":{"a":}}`, newTarget: ptrTarget},
		{name: "base64_bad", doc: `{"blob":"!!!"}`, newTarget: ptrTarget},
		{name: "trailing_data", doc: `{} {}`, newTarget: ptrTarget},
		{name: "trailing_garbage", doc: `{}x`, newTarget: ptrTarget},
		{name: "leading_whitespace", doc: " \t\r\n {\"id\":1} \n", newTarget: ptrTarget},
		{name: "bom_rejected", doc: "\xef\xbb\xbf{}", newTarget: ptrTarget},
		{name: "empty_input", doc: ``, newTarget: ptrTarget},
		{name: "whitespace_only", doc: "  \n\t", newTarget: ptrTarget},
		{name: "truncated_object", doc: `{"id":`, newTarget: ptrTarget},
		{name: "truncated_array", doc: `[1,2`, newTarget: func() any { return new([]any) }},
		{name: "bad_escape", doc: `{"name":"\x"}`, newTarget: ptrTarget},
		{name: "short_unicode_escape", doc: `{"name":"\u12"}`, newTarget: ptrTarget},
		{name: "bare_control_char", doc: "{\"name\":\"a\x01b\"}", newTarget: ptrTarget},
		{name: "wrong_type_object", doc: `[1,2]`, newTarget: ptrTarget},
		{name: "wrong_type_array", doc: `{"id":1}`, newTarget: func() any { return new([]any) }},
		{name: "string_into_int", doc: `{"id":"1"}`, newTarget: ptrTarget},
		{name: "nan_literal", doc: `{"f":NaN}`, newTarget: ptrTarget},
		{name: "leading_zero", doc: `{"id":01}`, newTarget: ptrTarget},
	}
}

// TestNativeDecodeDifferential compares the native decoder with encoding/json on a broad table. | TestNativeDecodeDifferential
// 在广泛用例表上把原生解码器与 encoding/json 做对比。
func TestNativeDecodeDifferential(t *testing.T) {
	for _, tc := range nativeDecodeCases() {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.newTarget()
			gotErr := jsonx.Unmarshal([]byte(tc.doc), got)
			want := tc.newTarget()
			wantErr := stdjson.Unmarshal([]byte(tc.doc), want)

			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("错误行为不一致: jsonx err=%v, stdlib err=%v", gotErr, wantErr)
			}
			if gotErr != nil {
				// Parser-originated errors must be classified, exactly like the other backends.
				// Errors raised by user callbacks (time.Time, custom UnmarshalJSON) are propagated
				// verbatim and are not reclassified. | 解析器自身的错误必须归类，与其他后端一致；
				// 用户回调（time.Time、自定义 UnmarshalJSON）返回的错误原样透传，不做重新归类。
				var syn *stdjson.SyntaxError
				var typ *stdjson.UnmarshalTypeError
				if errors.As(wantErr, &syn) || errors.As(wantErr, &typ) {
					if !errors.Is(gotErr, jsonx.ErrSyntax) && !errors.Is(gotErr, jsonx.ErrTypeMismatch) {
						t.Fatalf("错误未归类为 ErrSyntax / ErrTypeMismatch: %v", gotErr)
					}
				}
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("解码结果与标准库不一致\njsonx : %#v\nstdlib: %#v", got, want)
			}
			if v := jsonx.Valid([]byte(tc.doc)); v != stdjson.Valid([]byte(tc.doc)) {
				t.Fatalf("Valid 与标准库不一致: jsonx=%v stdlib=%v", v, stdjson.Valid([]byte(tc.doc)))
			}
		})
	}
}

// TestNativeTruncatedPrefixesDifferential checks every prefix of a rich document against the standard library.
// | TestNativeTruncatedPrefixesDifferential 对一份复杂文档的每个前缀与标准库做对比。
func TestNativeTruncatedPrefixesDifferential(t *testing.T) {
	doc := []byte(`{"when":"2026-10-18T12:00:00Z","inner":{"name":"中文😀","tags":["a","b"]},"list":[{"name":"x"}],"any":{"k":[1,2,null]}}`)
	for i := 0; i < len(doc); i++ {
		prefix := doc[:i]
		var got any
		gotErr := jsonx.Unmarshal(prefix, &got)
		var want any
		wantErr := stdjson.Unmarshal(prefix, &want)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("前缀 %d (%q) 错误行为不一致: jsonx err=%v, stdlib err=%v", i, prefix, gotErr, wantErr)
		}
		if gotErr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("前缀 %d (%q) 结果不一致", i, prefix)
		}
		if got, want := jsonx.Valid(prefix), stdjson.Valid(prefix); got != want {
			t.Fatalf("前缀 %d (%q) Valid 不一致: jsonx=%v stdlib=%v", i, prefix, got, want)
		}
	}
}

// nativeMarshalCase is one differential encode case. | nativeMarshalCase 是一个差分编码用例。
type nativeMarshalCase struct {
	value any
	name  string
}

// TestNativeMarshalDifferential compares the native encoder byte-for-byte with encoding/json. | TestNativeMarshalDifferential
// 把原生编码器与 encoding/json 做逐字节对比。
func TestNativeMarshalDifferential(t *testing.T) {
	zero := 0.0
	negZero := math.Copysign(0, -1)
	inner := nativeInner{Name: "n", Tags: []string{"a"}}
	node := &nativeNode{}
	node.Next = node
	cases := []nativeMarshalCase{
		{name: "rich_struct", value: nativeOuter{
			When:  time.Date(2026, 10, 18, 12, 0, 0, 123456789, time.UTC),
			Any:   map[string]any{"z": nil, "a": []any{1, "x", true}},
			Raw:   stdjson.RawMessage(` { "a" : [1, 2] } `),
			Ptr:   &inner,
			List:  []nativeInner{inner, {Name: "m", Tags: nil}},
			Blob:  []byte("hello"),
			ID:    math.MinInt64,
			U:     math.MaxUint64,
			F:     1.25,
			Inner: nativeInner{Name: "中文😀", Tags: []string{"<", "&", " "}},
			Name:  "a<b>c&d",
		}},
		{name: "nil", value: nil},
		{name: "scalars", value: []any{true, false, 0, -1, 1.5, "s", nil}},
		{name: "empty_containers", value: map[string]any{"s": []string{}, "m": map[string]int{}, "b": []byte{}}},
		{name: "nil_containers", value: map[string]any{"s": []string(nil), "m": map[string]int(nil), "b": []byte(nil)}},
		{name: "float_zero", value: zero},
		{name: "float_negative_zero", value: negZero},
		{name: "float_integral", value: []float64{0, 3, -7, 123456789, 1e20}},
		{name: "float_scientific", value: []float64{1e21, 1e-7, math.MaxFloat64, math.SmallestNonzeroFloat64}},
		{name: "float32", value: []float32{1.5, -0.25}},
		{name: "int_bounds", value: []any{int8(math.MinInt8), int16(math.MinInt16), int32(math.MinInt32), int64(math.MinInt64), uint64(math.MaxUint64)}},
		{name: "strings_escapes", value: []string{"", "plain", "a<b>c&d", "中文😀", "  ", "tab\tnewline\nquote\"backslash\\", "a\xffb"}},
		{name: "map_sorted", value: map[string]int{"b": 2, "a": 1, "": 0}},
		{name: "raw_message_compacted", value: stdjson.RawMessage(` { "a" : "x" , "b" : [ 1 , 2 ] } `)},
		{name: "raw_message_html", value: stdjson.RawMessage(`"<a>&</a>"`)},
		{name: "raw_message_invalid", value: stdjson.RawMessage(`{"x":}`)},
		{name: "bad_marshaler", value: nativeBadMarshaler{}},
		{name: "custom_marshaler", value: nativeUpper{S: "hello"}},
		{name: "text_marshaler", value: nativeText{V: "v"}},
		{name: "pointer_values", value: []any{&inner, (*nativeInner)(nil)}},
		{name: "embedded_value", value: nativeEmbedded{nativeEmbedBase{"b", 1}, &nativeEmbedPtr{"p"}, 7}},
		{name: "embedded_nil_pointer", value: nativeEmbedded{nativeEmbedBase{"b", 1}, nil, 7}},
		{name: "embedded_exported_types", value: NativeEmbeddedExported{NativeEmbedValue{"b", 1}, &NativeEmbedPointer{"p"}, 7}},
		{name: "embedded_exported_nil_pointer", value: NativeEmbeddedExported{NativeEmbedValue{"b", 1}, nil, 7}},
		{name: "time_zones", value: []time.Time{
			time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
			time.Date(2026, 10, 18, 12, 0, 0, 123456789, time.FixedZone("", 8*3600)),
		}},
		{name: "cycle", value: node},
		{name: "unsupported_func", value: func() {}},
		{name: "json_number_valid", value: stdjson.Number("123.5")},
		{name: "json_number_invalid", value: stdjson.Number("01")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotErr := jsonx.Marshal(tc.value)
			want, wantErr := stdjson.Marshal(tc.value)
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("错误行为不一致: jsonx err=%v, stdlib err=%v", gotErr, wantErr)
			}
			if gotErr != nil {
				return
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("编码结果与标准库不一致\njsonx : %s\nstdlib: %s", got, want)
			}
		})
	}
}

// TestNativeStringRoundTripByteExact verifies Chinese/emoji/HTML/invalid-UTF-8 strings survive encode-decode-encode
// byte-exactly and match the standard library. | TestNativeStringRoundTripByteExact 验证中英混排、emoji、HTML
// 与非法 UTF-8 字符串在「编码-解码-编码」后逐字节一致，且与标准库相同。
func TestNativeStringRoundTripByteExact(t *testing.T) {
	values := []string{
		"中文测试",
		"mixed 中文 and English",
		"emoji 😀👍🏽 family 👨‍👩‍👧‍👦",
		"<script>alert('x&y')</script>",
		"line separator paragraph",
		"quote\"backslash\\slash/",
		"control\t\r\n",
		"invalid \xff\xc3 bytes",
	}
	for _, s := range values {
		got, err := jsonx.Marshal(s)
		if err != nil {
			t.Fatalf("Marshal(%q): %v", s, err)
		}
		want, err := stdjson.Marshal(s)
		if err != nil {
			t.Fatalf("stdlib Marshal(%q): %v", s, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("编码不一致\njsonx : %q\nstdlib: %q", got, want)
		}
		var back string
		if err := jsonx.Unmarshal(got, &back); err != nil {
			t.Fatalf("Unmarshal(%q): %v", got, err)
		}
		var stdBack string
		if err := stdjson.Unmarshal(want, &stdBack); err != nil {
			t.Fatalf("stdlib Unmarshal(%q): %v", got, err)
		}
		if back != stdBack {
			t.Fatalf("解码结果不一致\njsonx : %q\nstdlib: %q", back, stdBack)
		}
		again, err := jsonx.Marshal(back)
		if err != nil {
			t.Fatalf("re-Marshal(%q): %v", back, err)
		}
		if !bytes.Equal(got, again) {
			t.Fatalf("二次编码漂移\nfirst : %q\nsecond: %q", got, again)
		}
	}
}

// TestNativeDepthLimit matches encoding/json's max nesting depth (10000) on decode and keeps the encoder bounded.
// | TestNativeDepthLimit 对齐 encoding/json 的最大嵌套深度（10000）解码语义，并保证编码器有界。
func TestNativeDepthLimit(t *testing.T) {
	nested := func(n int) []byte {
		return []byte(strings.Repeat("[", n) + strings.Repeat("]", n))
	}
	for _, n := range []int{1, 100, 9999, 10000} {
		doc := nested(n)
		var got any
		if err := jsonx.Unmarshal(doc, &got); err != nil {
			t.Fatalf("深度 %d 应可解码，实际 %v", n, err)
		}
		if !jsonx.Valid(doc) {
			t.Fatalf("深度 %d 的 Valid 应为 true", n)
		}
	}
	for _, n := range []int{10001, 20000} {
		doc := nested(n)
		var got any
		start := time.Now()
		err := jsonx.Unmarshal(doc, &got)
		if err == nil {
			t.Fatalf("深度 %d 必须返回错误", n)
		}
		if !errors.Is(err, jsonx.ErrSyntax) {
			t.Fatalf("深度 %d 的错误应归类为 ErrSyntax，实际 %v", n, err)
		}
		if jsonx.Valid(doc) {
			t.Fatalf("深度 %d 的 Valid 应为 false", n)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("深度 %d 拒绝耗时过长: %v", n, elapsed)
		}
	}

	t.Run("truncated_deep", func(t *testing.T) {
		doc := []byte(strings.Repeat("[", 20000))
		var got any
		if err := jsonx.Unmarshal(doc, &got); err == nil {
			t.Fatal("truncated deep input must return an error | 截断的超深输入必须返回错误")
		}
		if jsonx.Valid(doc) {
			t.Fatal("truncated deep input must be invalid | 截断的超深输入 Valid 必须为 false")
		}
	})

	t.Run("deep_objects", func(t *testing.T) {
		doc := []byte(strings.Repeat(`{"a":`, 10001) + "0" + strings.Repeat("}", 10001))
		var got any
		if err := jsonx.Unmarshal(doc, &got); err == nil {
			t.Fatal("deep object must return an error | 超深对象必须返回错误")
		}
	})

	t.Run("encode_cycle", func(t *testing.T) {
		node := &nativeNode{}
		node.Next = node
		if _, err := jsonx.Marshal(node); err == nil {
			t.Fatal("cyclic pointer must return an error | 循环指针必须返回错误而不是无限递归")
		}
	})

	t.Run("encode_deep_value", func(t *testing.T) {
		var v any = 0
		for i := 0; i < 10001; i++ {
			v = []any{v}
		}
		if _, err := jsonx.Marshal(v); err == nil {
			t.Fatal("value beyond the encode depth limit must return an error | 超过编码深度上限的值必须返回错误")
		}
	})
}

// TestNativeDecoderStreamSecurity verifies streaming decode rejects malformed values without panicking. | TestNativeDecoderStreamSecurity
// 验证流式解码拒绝畸形值且不 panic。
func TestNativeDecoderStreamSecurity(t *testing.T) {
	inputs := []string{
		`{"a":`,
		`[1,2`,
		`"unterminated`,
		``,
		`{`,
		`{"a":1} {"b":2}`,
		`[`,
		`null null`,
		"{\"a\":\"\xff\"}",
		strings.Repeat("[", 12000),
	}
	for _, in := range inputs {
		t.Run("", func(t *testing.T) {
			dec := jsonx.NewDecoder(strings.NewReader(in))
			var v any
			_ = dec.Decode(&v)
		})
	}
	var v any
	if err := jsonx.NewDecoder(strings.NewReader("{}")).Decode(v); err == nil {
		t.Fatal("non-pointer target must return an error | 非指针目标必须返回错误")
	}
	if err := jsonx.NewDecoder(nil).Decode(&v); !errors.Is(err, io.EOF) {
		t.Fatalf("nil reader Decode = %v, want io.EOF (no panic) | nil 读取器应返回 io.EOF 而不是 panic", err)
	}
}

// TestNativeMalformedInputsDoNotPanic drives every public decode entry point with hostile inputs and
// asserts that none of them panic. | TestNativeMalformedInputsDoNotPanic 用恶意输入驱动全部公开
// 解码入口，断言任何一个都不会 panic。
func TestNativeMalformedInputsDoNotPanic(t *testing.T) {
	inputs := [][]byte{
		nil,
		{},
		[]byte(`{`),
		[]byte(`[`),
		[]byte(`{"a":`),
		[]byte(`[1,2`),
		[]byte(`"`),
		[]byte(`"\`),
		[]byte(`"\x"`),
		[]byte(`"\u"`),
		[]byte(`"\u12"`),
		[]byte(`"\ud800"`),
		[]byte(`"\ud800\ud800"`),
		[]byte("{\"a\":\"\xff\xfe\"}"),
		[]byte("\xef\xbb\xbf{}"),
		[]byte(`{}x`),
		[]byte(`{} {}`),
		[]byte(`01`),
		[]byte(`1.`),
		[]byte(`1e`),
		[]byte(`-`),
		[]byte(`1e999999`),
		[]byte(`[1,]`),
		[]byte(`{"a":1,}`),
		[]byte(strings.Repeat("[", 10001)),
		[]byte(strings.Repeat("[", 10001) + strings.Repeat("]", 10001)),
		[]byte(strings.Repeat(`{"a":`, 10001)),
		[]byte(strings.Repeat("[", 10001) + "0" + strings.Repeat("]", 10001)),
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("输入 %q 触发 panic: %v", in, r)
				}
			}()
			var v any
			_ = jsonx.Unmarshal(in, &v)
			var st nativeOuter
			_ = jsonx.Unmarshal(in, &st)
			var wide nativeWide
			_ = jsonx.Unmarshal(in, &wide)
			_ = jsonx.Valid(in)
			dec := jsonx.NewDecoder(bytes.NewReader(in))
			var dv any
			_ = dec.Decode(&dv)
			_ = dec.Decode(&dv)
		}()
	}
}

// FuzzNativeUnmarshal asserts the native decoder never panics, stays differential with encoding/json and
// classifies errors. | FuzzNativeUnmarshal 断言原生解码器不 panic、与 encoding/json 保持差分一致并归类错误。
func FuzzNativeUnmarshal(f *testing.F) {
	seeds := []string{
		`{}`, `[]`, `null`, `true`, `"x"`, `0`, `-1.5e10`,
		`{"a":[1,{"b":null}],"c":"中文😀"}`,
		`{"when":"2026-10-18T12:00:00.123Z","id":-9223372036854775808}`,
		`{"a":1,"a":2}`, `{"a":1}`, `"😀"`, `"\ud800"`,
		`{"x":"a\xffb"}`, "{\"x\":\"\x01\"}", `{"a":}`, `[1,]`, `01`, `1e999`,
		strings.Repeat("[", 10001) + strings.Repeat("]", 10001),
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var got any
		gotErr := jsonx.Unmarshal(data, &got)
		var want any
		wantErr := stdjson.Unmarshal(data, &want)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("错误行为不一致: jsonx err=%v, stdlib err=%v, input=%q", gotErr, wantErr, data)
		}
		if gotErr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("结果不一致: jsonx=%#v stdlib=%#v input=%q", got, want, data)
		}
		if gotErr != nil && !errors.Is(gotErr, jsonx.ErrSyntax) && !errors.Is(gotErr, jsonx.ErrTypeMismatch) {
			t.Fatalf("错误未归类: %v input=%q", gotErr, data)
		}
		if got, want := jsonx.Valid(data), stdjson.Valid(data); got != want {
			t.Fatalf("Valid 不一致: jsonx=%v stdlib=%v input=%q", got, want, data)
		}
		var typed nativeOuter
		_ = jsonx.Unmarshal(data, &typed)
	})
}

// FuzzNativeMarshalRoundTrip asserts arbitrary decoded values re-encode identically to encoding/json. | FuzzNativeMarshalRoundTrip
// 断言任意可解码值重新编码后与 encoding/json 一致。
func FuzzNativeMarshalRoundTrip(f *testing.F) {
	seeds := []string{
		`{}`, `[]`, `null`, `true`, `"x"`, `0`, `-1.5e10`,
		`{"a":[1,{"b":null}],"c":"中文😀"}`,
		`{"a":1,"a":2}`, `"😀"`, `{"x":"a\xffb"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var v any
		if err := stdjson.Unmarshal(data, &v); err != nil {
			return
		}
		got, gotErr := jsonx.Marshal(v)
		want, wantErr := stdjson.Marshal(v)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("编码错误行为不一致: jsonx=%v stdlib=%v input=%q", gotErr, wantErr, data)
		}
		if gotErr == nil && !bytes.Equal(got, want) {
			t.Fatalf("编码不一致\njsonx : %s\nstdlib: %s", got, want)
		}
	})
}

//go:build jsonx_native

package jsonx_test

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/shibingli/yd/core/jsonx"
)

// quotedNamedString has no custom methods; its inner literal must be a JSON string. | quotedNamedString
// 没有自定义方法；其内层字面量必须是 JSON 字符串。
type quotedNamedString string

// quotedNamedInt8 is a named integer used through a pointer field. | quotedNamedInt8 是通过指针字段
// 使用的具名整数。
type quotedNamedInt8 int8

// quotedTextString implements TextMarshaler with a value receiver and TextUnmarshaler with a
// pointer receiver, exercising both indirect paths. | quotedTextString 用值接收者实现
// TextMarshaler、用指针接收者实现 TextUnmarshaler，覆盖两条 indirect 路径。
type quotedTextString string

// MarshalText encodes the value with a fixed prefix. | MarshalText 以固定前缀编码。
func (q quotedTextString) MarshalText() ([]byte, error) { return []byte("T:" + string(q)), nil }

// UnmarshalText decodes the value with the fixed prefix stripped. | UnmarshalText 解码并去掉固定前缀。
func (q *quotedTextString) UnmarshalText(b []byte) error {
	*q = quotedTextString(strings.TrimPrefix(string(b), "T:"))
	return nil
}

// quotedJSONString implements MarshalJSON with a value receiver and UnmarshalJSON with a pointer
// receiver. | quotedJSONString 用值接收者实现 MarshalJSON、用指针接收者实现 UnmarshalJSON。
type quotedJSONString string

// MarshalJSON encodes the value with a fixed prefix. | MarshalJSON 以固定前缀编码。
func (q quotedJSONString) MarshalJSON() ([]byte, error) {
	return stdjson.Marshal("J:" + string(q))
}

// UnmarshalJSON decodes the value with the fixed prefix stripped. | UnmarshalJSON 解码并去掉固定前缀。
func (q *quotedJSONString) UnmarshalJSON(b []byte) error {
	var s string
	if err := stdjson.Unmarshal(b, &s); err != nil {
		return err
	}
	*q = quotedJSONString(strings.TrimPrefix(s, "J:"))
	return nil
}

// quotedAddrJSON only has a pointer-receiver MarshalJSON, so encoding depends on addressability.
// | quotedAddrJSON 只有指针接收者 MarshalJSON，因此编码结果取决于字段值是否可寻址。
type quotedAddrJSON string

// MarshalJSON encodes the value with a fixed prefix. | MarshalJSON 以固定前缀编码。
func (q *quotedAddrJSON) MarshalJSON() ([]byte, error) {
	return stdjson.Marshal("A:" + string(*q))
}

// UnmarshalJSON decodes the value with the fixed prefix stripped. | UnmarshalJSON 解码并去掉固定前缀。
func (q *quotedAddrJSON) UnmarshalJSON(b []byte) error {
	var s string
	if err := stdjson.Unmarshal(b, &s); err != nil {
		return err
	}
	*q = quotedAddrJSON(strings.TrimPrefix(s, "A:"))
	return nil
}

// quotedFields covers every type shape eligible for the ,string option: primitives, pointers to
// primitives, json.Number, named strings, and custom Marshaler/TextMarshaler types.
// | quotedFields 覆盖所有适用 ,string 选项的类型形态：基础类型、指向基础类型的指针、json.Number、
// 具名字符串，以及自定义 Marshaler/TextMarshaler 类型。
type quotedFields struct {
	P   *int              `json:"p,string"`
	PP  *quotedNamedInt8  `json:"pp,string"`
	N   stdjson.Number    `json:"n,string"`
	B   bool              `json:"b,string"`
	I   int               `json:"i,string"`
	I8  int8              `json:"i8,string"`
	U   uint64            `json:"u,string"`
	F   float64           `json:"f,string"`
	F32 float32           `json:"f32,string"`
	S   string            `json:"s,string"`
	S2  string            `json:"s2,string"`
	NS  quotedNamedString `json:"ns,string"`
	NT  quotedTextString  `json:"nt,string"`
	PT  *quotedTextString `json:"pt,string"`
	NJ  quotedJSONString  `json:"nj,string"`
	NA  quotedAddrJSON    `json:"na,string"`
}

// quotedOmit mixes ,string with omitempty and omitzero. | quotedOmit 把 ,string 与 omitempty、
// omitzero 组合使用。
type quotedOmit struct {
	I int    `json:"i,string,omitempty"`
	S string `json:"s,string,omitempty"`
	Z int    `json:"z,string,omitzero"`
}

// TestNativeQuotedMarshalDifferential compares ,string encoding byte-for-byte with encoding/json,
// including nil pointers, escaping, and addressability-sensitive Marshaler fields.
// | TestNativeQuotedMarshalDifferential 对 ,string 编码与 encoding/json 做逐字节对比，覆盖 nil
// 指针、转义，以及依赖可寻址性的 Marshaler 字段。
func TestNativeQuotedMarshalDifferential(t *testing.T) {
	seven := 7
	negative := quotedNamedInt8(-8)
	pt := quotedTextString("pt")
	values := []any{
		quotedFields{},
		quotedFields{
			P: &seven, PP: &negative, N: "123.5", B: true, I: -42, I8: 127, U: math.MaxUint64,
			F: 1.25e3, F32: -0.5, S: "a<b>&\"\n\t\\", NS: "raw", NT: "nt", PT: &pt, NJ: "nj", NA: "na",
		},
		quotedFields{S: "中文😀", N: "-0.5", F: math.SmallestNonzeroFloat64},
		quotedFields{NA: "na"},
		quotedOmit{},
		quotedOmit{I: 3, S: "s", Z: 4},
		quotedOmit{Z: 1},
		quotedFields{N: "01"},
	}
	for _, value := range values {
		got, gotErr := jsonx.Marshal(value)
		want, wantErr := stdjson.Marshal(value)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("错误行为不一致 | error behavior differs: jsonx err=%v, stdlib err=%v", gotErr, wantErr)
		}
		if gotErr == nil && !bytes.Equal(got, want) {
			t.Fatalf("编码结果不一致 | encoded bytes differ\nvalue: %#v\njsonx : %s\nstdlib: %s", value, got, want)
		}
	}
	// A pointer root makes the Marshaler field addressable, so condAddrEncoder selects it.
	// | 指针根值使 Marshaler 字段可寻址，从而由 condAddrEncoder 选中它。
	value := quotedFields{NA: "na", I: 1}
	got, gotErr := jsonx.Marshal(&value)
	want, wantErr := stdjson.Marshal(&value)
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("指针错误行为不一致 | pointer error behavior differs: jsonx err=%v, stdlib err=%v", gotErr, wantErr)
	}
	if gotErr == nil && !bytes.Equal(got, want) {
		t.Fatalf("指针编码不一致 | pointer encoding differs\njsonx : %s\nstdlib: %s", got, want)
	}
}

// TestNativeQuotedUnmarshalDifferential compares ,string decoding results and error presence with
// encoding/json across valid and invalid literals. | TestNativeQuotedUnmarshalDifferential 在合法与
// 非法字面量上比较 ,string 解码结果与错误有无是否与 encoding/json 一致。
func TestNativeQuotedUnmarshalDifferential(t *testing.T) {
	docs := []string{
		`{}`,
		`{"b":"true","i":"-42","i8":"127","u":"18446744073709551615","f":"1.25e3","s":"\"hi\"","n":"123","p":"7","pp":"-8","ns":"\"raw\"","nt":"\"T:x\"","nj":"\"J:y\"","na":"\"A:z\""}`,
		`{"b":null,"i":null,"p":null,"s":null,"n":null}`,
		`{"i":"null","p":"null","n":"null","s":"null"}`,
		`{"s":"\"\""}`,
		`{"s":"\"a\\u0041\\n\\\"b\""}`,
		`{"s":"\"\ud800\""}`,
		`{"f":"-0","i":"0","u":"0"}`,
		`{"i":"+1","u":"+2"}`,
		`{"f":"+Inf","f32":"+Inf"}`,
		`{"f":"0x1p3","f32":"Infinity"}`,
		`{"s":"null","s2":"\"null\""}`,
		`{"n":"+1"}`,
		`{"n":"1e3"}`,
		`{"n":"01"}`,
		`{"n":"123.5","i":"1"}`,
		`{"n":"\"123.5\""}`,
		`{"n":"12x"}`,
		`{"n":""}`,
		`{"n":"null"}`,
		`{"n":123}`,
		`{"ns":"\"x\""}`,
		`{"p":"5","pp":"-128"}`,
		`{"i":"1e3"}`,
		`{"i":"abc"}`,
		`{"i":"1.5"}`,
		`{"i":123}`,
		`{"i":true}`,
		`{"i":[1,2]}`,
		`{"i":{"a":1}}`,
		`{"i":""}`,
		`{"i":" 1"}`,
		`{"i":"1 "}`,
		`{"i":"0x10"}`,
		`{"i8":"128"}`,
		`{"u":"-1"}`,
		`{"u":"18446744073709551616"}`,
		`{"f":"1e999"}`,
		`{"f":"Inf"}`,
		`{"f32":"3.5e38"}`,
		`{"b":"1"}`,
		`{"b":"TRUE"}`,
		`{"b":"truex"}`,
		`{"s":"hi"}`,
		`{"s":"123"}`,
		`{"s":"\"unterminated"}`,
		`{"p":"5x"}`,
		`{"pp":"128"}`,
		`{"nt":"abc"}`,
		`{"nt":"\"T:x\""}`,
		`{"pt":"T:z"}`,
		`{"pt":"null"}`,
		`{"pt":"null"}`,
		`{"na":"\"A:z\""}`,
	}
	for _, doc := range docs {
		t.Run(doc, func(t *testing.T) {
			got := new(quotedFields)
			gotErr := jsonx.Unmarshal([]byte(doc), got)
			want := new(quotedFields)
			wantErr := stdjson.Unmarshal([]byte(doc), want)
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("错误行为不一致 | error behavior differs: jsonx err=%v, stdlib err=%v doc=%s", gotErr, wantErr, doc)
			}
			if gotErr != nil {
				// Parser-originated errors must be classified; user callback errors propagate
				// verbatim and are not reclassified. | 解析器自身的错误必须归类；用户回调错误原样
				// 透传，不做重新归类。
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
				t.Fatalf("解码结果不一致 | decoded value differs\njsonx : %#v\nstdlib: %#v", got, want)
			}
		})
	}
}

// TestNativeQuotedErrorClassification asserts that misuse of the ,string option is reported as
// ErrTypeMismatch. | TestNativeQuotedErrorClassification 断言 ,string 选项的误用被归类为
// ErrTypeMismatch。
func TestNativeQuotedErrorClassification(t *testing.T) {
	docs := []string{
		`{"i":"abc"}`,
		`{"i":"1.5"}`,
		`{"i":123}`,
		`{"i":true}`,
		`{"i":""}`,
		`{"i":" 1"}`,
		`{"i8":"128"}`,
		`{"u":"-1"}`,
		`{"f":"1e999"}`,
		`{"f32":"3.5e38"}`,
		`{"b":"1"}`,
		`{"s":"hi"}`,
		`{"n":"\"abc\""}`,
		`{"n":"12x"}`,
		`{"n":123}`,
		`{"p":"5x"}`,
	}
	for _, doc := range docs {
		var v quotedFields
		err := jsonx.Unmarshal([]byte(doc), &v)
		if err == nil {
			t.Fatalf("输入 %s 必须返回错误 | input %s must fail", doc, doc)
		}
		if !errors.Is(err, jsonx.ErrTypeMismatch) && !errors.Is(err, jsonx.ErrSyntax) {
			t.Fatalf("输入 %s 的错误未归类: %v | error not classified", doc, err)
		}
	}
}

// TestNativeQuotedFloatNaN verifies the legacy quirk that a quoted NaN is accepted (it does not
// overflow) by both implementations. | TestNativeQuotedFloatNaN 验证旧版遗留行为：带引号的 NaN
// 不会溢出，两种实现都接受。
func TestNativeQuotedFloatNaN(t *testing.T) {
	doc := []byte(`{"f":"NaN"}`)
	var got quotedFields
	gotErr := jsonx.Unmarshal(doc, &got)
	var want quotedFields
	wantErr := stdjson.Unmarshal(doc, &want)
	if (gotErr == nil) != (wantErr == nil) {
		t.Fatalf("错误行为不一致 | error behavior differs: jsonx err=%v, stdlib err=%v", gotErr, wantErr)
	}
	if gotErr != nil {
		return
	}
	if !math.IsNaN(got.F) || !math.IsNaN(want.F) {
		t.Fatalf("NaN 语义不一致 | NaN semantics differ: jsonx=%v stdlib=%v", got.F, want.F)
	}
	got.F, want.F = 0, 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("其余字段不一致 | other fields differ\njsonx : %#v\nstdlib: %#v", got, want)
	}
}

// TestNativeQuotedCustomUnmarshaler verifies custom Unmarshaler / TextUnmarshaler fields receive
// the inner literal exactly like encoding/json. | TestNativeQuotedCustomUnmarshaler 验证自定义
// Unmarshaler / TextUnmarshaler 字段收到内层字面量的方式与 encoding/json 完全一致。
func TestNativeQuotedCustomUnmarshaler(t *testing.T) {
	docs := []string{
		`{"nj":"\"J:y\"","na":"\"A:z\"","nt":"\"T:x\""}`,
		`{"nj":"abc"}`,
		`{"na":"123"}`,
		`{"nt":"T:x"}`,
	}
	for _, doc := range docs {
		got := new(quotedFields)
		gotErr := jsonx.Unmarshal([]byte(doc), got)
		want := new(quotedFields)
		wantErr := stdjson.Unmarshal([]byte(doc), want)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("错误行为不一致 | error behavior differs: jsonx err=%v, stdlib err=%v doc=%s", gotErr, wantErr, doc)
		}
		if gotErr == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("解码结果不一致 | decoded value differs\njsonx : %#v\nstdlib: %#v", got, want)
		}
	}
}

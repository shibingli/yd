//go:build jsonx_native

package jsonx_test

import (
	"bytes"
	stdjson "encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/shibingli/yd/core/jsonx"
)

// omitZeroInner is a plain struct field with no IsZero method. | omitZeroInner 是带空值的普通结构体字段。
type omitZeroInner struct {
	A int    `json:"a"`
	B string `json:"b"`
}

// omitZeroValue implements IsZero with a value receiver. | omitZeroValue 用值接收者实现 IsZero。
type omitZeroValue struct {
	V int
}

// IsZero reports the custom zero state. | IsZero 报告自定义零值状态。
func (z omitZeroValue) IsZero() bool { return z.V == 42 }

// omitZeroPointer implements IsZero with a pointer receiver, exercising the boxing path.
// | omitZeroPointer 用指针接收者实现 IsZero，覆盖装箱路径。
type omitZeroPointer struct {
	V int
}

// IsZero reports the custom zero state. | IsZero 报告自定义零值状态。
func (z *omitZeroPointer) IsZero() bool { return z.V == 7 }

// omitZeroFields covers every zero shape: primitives, pointers, containers, arrays, structs,
// time.Time (which has its own IsZero), interfaces, and custom value/pointer receivers.
// | omitZeroFields 覆盖所有零值形态：基础类型、指针、容器、数组、结构体、自带 IsZero 的
// time.Time、接口，以及自定义值/指针接收者。
type omitZeroFields struct {
	Ptr       *int            `json:"ptr,omitzero"`
	Slice     []int           `json:"slice,omitzero"`
	Map       map[string]int  `json:"map,omitzero"`
	Iface     any             `json:"iface,omitzero"`
	Int       int             `json:"int,omitzero"`
	Str       string          `json:"str,omitzero"`
	Float     float64         `json:"float,omitzero"`
	Arr       [2]int          `json:"arr,omitzero"`
	Inner     omitZeroInner   `json:"inner,omitzero"`
	Time      time.Time       `json:"time,omitzero"`
	Custom    omitZeroValue   `json:"custom,omitzero"`
	CustomPtr omitZeroPointer `json:"custom_ptr,omitzero"`
	Bool      bool            `json:"bool,omitzero"`
}

// omitZeroCombo mixes omitempty and omitzero in both orders to prove they are independent.
// | omitZeroCombo 以两种顺序混用 omitempty 与 omitzero，证明二者相互独立。
type omitZeroCombo struct {
	SliceA []int  `json:"slice_a,omitempty,omitzero"`
	SliceB []int  `json:"slice_b,omitzero,omitempty"`
	Arr    [2]int `json:"arr,omitempty,omitzero"`
	Str    string `json:"str,omitempty,omitzero"`
	Ptr    *int   `json:"ptr,omitzero,omitempty"`
}

// omitZeroEmbedded promotes omitzero fields through embedding. | omitZeroEmbedded 通过嵌入提升 omitzero 字段。
type omitZeroEmbedded struct {
	omitZeroInner
	Extra int `json:"extra,omitzero"`
}

// TestNativeOmitZeroMarshalDifferential compares omitzero output byte-for-byte with encoding/json
// across zero and non-zero shapes. | TestNativeOmitZeroMarshalDifferential 对零值与非零值形态把
// omitzero 输出与 encoding/json 做逐字节对比。
func TestNativeOmitZeroMarshalDifferential(t *testing.T) {
	zeroInt := 0
	seven := 7
	cases := []struct {
		value any
		name  string
	}{
		{name: "all_zero", value: omitZeroFields{}},
		{name: "pointer_to_zero", value: omitZeroFields{Ptr: &zeroInt}},
		{name: "empty_non_nil_containers", value: omitZeroFields{Slice: []int{}, Map: map[string]int{}, Iface: 0}},
		{name: "non_zero_primitives", value: omitZeroFields{
			Int: 1, Str: "s", Float: 1.5, Bool: true,
			Ptr: &seven, Slice: []int{0}, Map: map[string]int{"k": 0},
		}},
		{name: "arrays", value: omitZeroFields{Arr: [2]int{0, 0}}},
		{name: "array_non_zero", value: omitZeroFields{Arr: [2]int{0, 1}}},
		{name: "struct_zero", value: omitZeroFields{Inner: omitZeroInner{}}},
		{name: "struct_non_zero", value: omitZeroFields{Inner: omitZeroInner{A: 0, B: "b"}}},
		{name: "time_zero", value: omitZeroFields{Time: time.Time{}}},
		{name: "time_non_zero", value: omitZeroFields{Time: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}},
		{name: "custom_value_zero", value: omitZeroFields{Custom: omitZeroValue{V: 42}}},
		{name: "custom_value_non_zero", value: omitZeroFields{Custom: omitZeroValue{V: 0}}},
		{name: "custom_pointer_zero", value: omitZeroFields{CustomPtr: omitZeroPointer{V: 7}}},
		{name: "custom_pointer_non_zero", value: omitZeroFields{CustomPtr: omitZeroPointer{V: 0}}},
		{name: "combo_all_zero", value: omitZeroCombo{}},
		{name: "combo_empty_containers", value: omitZeroCombo{SliceA: []int{}, SliceB: []int{}}},
		{name: "combo_array_zero", value: omitZeroCombo{Arr: [2]int{0, 0}}},
		{name: "combo_array_non_zero", value: omitZeroCombo{Arr: [2]int{1, 0}}},
		{name: "combo_pointer", value: omitZeroCombo{Ptr: &zeroInt}},
		{name: "embedded_zero", value: omitZeroEmbedded{}},
		{name: "embedded_non_zero", value: omitZeroEmbedded{omitZeroInner: omitZeroInner{A: 1}, Extra: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, gotErr := jsonx.Marshal(tc.value)
			want, wantErr := stdjson.Marshal(tc.value)
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("错误行为不一致 | error behavior differs: jsonx err=%v, stdlib err=%v", gotErr, wantErr)
			}
			if gotErr == nil && !bytes.Equal(got, want) {
				t.Fatalf("编码结果不一致 | encoded bytes differ\njsonx : %s\nstdlib: %s", got, want)
			}
		})
	}
}

// TestNativeOmitZeroPointerRoot verifies the custom IsZero boxing path works for a non-addressable
// root value and that a pointer root stays byte-identical. | TestNativeOmitZeroPointerRoot 验证
// 自定义 IsZero 的装箱路径在不可寻址根值上可用，且指针根值输出仍逐字节一致。
func TestNativeOmitZeroPointerRoot(t *testing.T) {
	value := omitZeroFields{Custom: omitZeroValue{V: 42}, CustomPtr: omitZeroPointer{V: 7}, Int: 3}
	got, err := jsonx.Marshal(value)
	if err != nil {
		t.Fatalf("值编码失败 | value marshal failed: %v", err)
	}
	want, err := stdjson.Marshal(value)
	if err != nil {
		t.Fatalf("标准库值编码失败 | stdlib value marshal failed: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("值编码不一致 | value encoding differs\njsonx : %s\nstdlib: %s", got, want)
	}
	gotPtr, err := jsonx.Marshal(&value)
	if err != nil {
		t.Fatalf("指针编码失败 | pointer marshal failed: %v", err)
	}
	wantPtr, err := stdjson.Marshal(&value)
	if err != nil {
		t.Fatalf("标准库指针编码失败 | stdlib pointer marshal failed: %v", err)
	}
	if !bytes.Equal(gotPtr, wantPtr) {
		t.Fatalf("指针编码不一致 | pointer encoding differs\njsonx : %s\nstdlib: %s", gotPtr, wantPtr)
	}
}

// omitZeroInt is a named integer with a custom IsZero method. | omitZeroInt 是带自定义 IsZero
// 方法的具名整数。
type omitZeroInt int

// IsZero reports 9 as the custom zero value. | IsZero 以 9 作为自定义零值。
func (z omitZeroInt) IsZero() bool { return z == 9 }

// omitZeroEdge covers the remaining shapes: a zero-length array, a pointer to a zero struct, an
// interface holding a typed nil pointer, and a named primitive with IsZero. | omitZeroEdge 覆盖
// 其余形态：零长数组、指向零值结构体的指针、持有类型化 nil 指针的接口，以及带 IsZero 的具名
// 基础类型。
type omitZeroEdge struct {
	PtrTime  *time.Time  `json:"ptr_time,omitzero"`
	IfacePtr any         `json:"iface_ptr,omitzero"`
	EmptyArr [0]int      `json:"empty_arr,omitzero"`
	NamedInt omitZeroInt `json:"named_int,omitzero"`
}

// TestNativeOmitZeroEdgeDifferential compares the less common zero shapes with encoding/json.
// | TestNativeOmitZeroEdgeDifferential 把较少见的零值形态与 encoding/json 做对比。
func TestNativeOmitZeroEdgeDifferential(t *testing.T) {
	var typedNil *int
	zeroTime := time.Time{}
	cases := []any{
		omitZeroEdge{},
		omitZeroEdge{PtrTime: &zeroTime, IfacePtr: typedNil, NamedInt: 0},
		omitZeroEdge{NamedInt: 9},
	}
	for _, value := range cases {
		got, gotErr := jsonx.Marshal(value)
		want, wantErr := stdjson.Marshal(value)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("错误行为不一致 | error behavior differs: jsonx err=%v, stdlib err=%v", gotErr, wantErr)
		}
		if gotErr == nil && !bytes.Equal(got, want) {
			t.Fatalf("编码结果不一致 | encoded bytes differ\nvalue: %#v\njsonx : %s\nstdlib: %s", value, got, want)
		}
	}
}

// TestNativeOmitZeroUnmarshalUnaffected verifies omitzero is encode-only and decoding still matches
// encoding/json. | TestNativeOmitZeroUnmarshalUnaffected 验证 omitzero 只影响编码，解码仍与
// encoding/json 一致。
func TestNativeOmitZeroUnmarshalUnaffected(t *testing.T) {
	docs := []string{
		`{"int":1,"str":"s","arr":[0,1],"inner":{"a":2,"b":"x"}}`,
		`{}`,
		`{"custom":{"V":42},"custom_ptr":{"V":7}}`,
	}
	for _, doc := range docs {
		got := new(omitZeroFields)
		gotErr := jsonx.Unmarshal([]byte(doc), got)
		want := new(omitZeroFields)
		wantErr := stdjson.Unmarshal([]byte(doc), want)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("解码错误行为不一致 | decode error behavior differs: jsonx=%v stdlib=%v doc=%s", gotErr, wantErr, doc)
		}
		if gotErr == nil && !reflect.DeepEqual(*got, *want) {
			t.Fatalf("解码结果不一致 | decoded value differs\njsonx : %#v\nstdlib: %#v", *got, *want)
		}
	}
}

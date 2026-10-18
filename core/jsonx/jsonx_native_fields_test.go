//go:build amd64 && !jsonx_std

package jsonx_test

import (
	"bytes"
	stdjson "encoding/json"
	"reflect"
	"testing"

	"github.com/shibingli/yd/core/jsonx"
)

// The types below cover encoding/json's embedded-field dominance rules: depth wins first, a JSON
// tag wins over no tag at the same depth, and equally ranked duplicates cancel the name.
// | 以下类型覆盖 encoding/json 的嵌入字段支配规则：先比深度，同深度下 JSON tag 优先于无 tag，
// 同等级重复项则抵消该名字。

// embDepthInner is the embedded struct whose field is shadowed by the outer one. | embDepthInner
// 是被外层同名字段遮蔽的嵌入结构体。
type embDepthInner struct {
	Value string `json:"value"`
	Deep  string `json:"deep"`
}

// embDepth shadows the deep value field at a shallower depth. | embDepth 以更浅的深度遮蔽深层 value 字段。
type embDepth struct {
	embDepthInner
	Value string `json:"value"`
}

// embDeepTagged holds a tagged Q field that is deeper than the outer untagged one. | embDeepTagged
// 持有带 tag 的 Q 字段，其深度大于外层无 tag 字段。
type embDeepTagged struct {
	Q string `json:"Q"`
}

// embShallowUntagged proves depth beats tag presence. | embShallowUntagged 证明深度优先于 tag。
type embShallowUntagged struct {
	embDeepTagged
	Q string
}

// embTag holds a tagged V field. | embTag 持有带 tag 的 V 字段。
type embTag struct {
	V string `json:"V"`
}

// embPlain holds an untagged V field. | embPlain 持有无 tag 的 V 字段。
type embPlain struct {
	V string
}

// embPlain2 is a second untagged V holder with a different type. | embPlain2 是另一个持有无 tag V
// 字段的不同类型。
type embPlain2 struct {
	V string
}

// embTagVsPlain scores a tagged field above an untagged one at the same depth. | embTagVsPlain
// 在同一深度下让带 tag 字段胜过无 tag 字段。
type embTagVsPlain struct {
	embPlain
	embTag
}

// embTagVsPlainRev is the reversed declaration order of embTagVsPlain. | embTagVsPlainRev 是
// embTagVsPlain 的反向声明顺序。
type embTagVsPlainRev struct {
	embTag
	embPlain
}

// doubleTaggedType builds, at run time, a struct with two embedded structs that both declare
// `json:"V"` at the same depth. It is built through reflect.StructOf rather than declared in source
// because go vet's structtag analyzer rejects same-level duplicate tags even though the dominance
// rules under test require exactly that shape. | doubleTaggedType 在运行时构造一个结构体，其两个
// 嵌入结构体在同一深度都声明 `json:"V"`。它经 reflect.StructOf 构造而非源码声明，因为 go vet 的
// structtag 分析器会拒绝同级重复 tag，而待测的支配规则恰恰需要这种形态。
func doubleTaggedType() reflect.Type {
	left := reflect.StructOf([]reflect.StructField{{
		Name: "V",
		Type: reflect.TypeFor[string](),
		Tag:  `json:"V"`,
	}})
	right := reflect.StructOf([]reflect.StructField{{
		Name: "W",
		Type: reflect.TypeFor[string](),
		Tag:  `json:"V"`,
	}})
	return reflect.StructOf([]reflect.StructField{
		{Name: "Left", Type: left, Anonymous: true},
		{Name: "Right", Type: right, Anonymous: true},
	})
}

// doubleTaggedValue fills both embedded structs of the dynamic type. | doubleTaggedValue 填充动态
// 类型的两个嵌入结构体。
func doubleTaggedValue() any {
	v := reflect.New(doubleTaggedType()).Elem()
	v.Field(0).Field(0).SetString("a")
	v.Field(1).Field(0).SetString("b")
	return v.Interface()
}

// embDoublePlain has two untagged fields with the same name. | embDoublePlain 有两个同名的无 tag 字段。
type embDoublePlain struct {
	embPlain
	embPlain2
}

// EmbValuePart is an exported value-embedded struct. | EmbValuePart 是导出的按值嵌入结构体。
type EmbValuePart struct {
	Shared    string `json:"shared"`
	OnlyValue string `json:"only_value"`
}

// EmbPtrPart is an exported pointer-embedded struct. | EmbPtrPart 是导出的按指针嵌入结构体。
type EmbPtrPart struct {
	Shared  string `json:"shared"`
	OnlyPtr string `json:"only_ptr"`
}

// embValueEmbed embeds EmbValuePart by value. | embValueEmbed 按值嵌入 EmbValuePart。
type embValueEmbed struct {
	EmbValuePart
	ID int `json:"id"`
}

// embPtrEmbed embeds EmbPtrPart by pointer. | embPtrEmbed 按指针嵌入 EmbPtrPart。
type embPtrEmbed struct {
	*EmbPtrPart
	ID int `json:"id"`
}

// embValuePtrConflict mixes value and pointer embedding with a conflicting name. | embValuePtrConflict
// 混合值嵌入与指针嵌入，并制造同名冲突。
type embValuePtrConflict struct {
	EmbValuePart
	*EmbPtrPart
}

// embHiddenInner is embedded through its unexported type; its exported subfield is promoted.
// | embHiddenInner 通过未导出类型被嵌入；其导出子字段仍会被提升。
type embHiddenInner struct {
	Visible string `json:"visible"`
}

// embHidden embeds an unexported struct type by value. | embHidden 按值嵌入未导出的结构体类型。
type embHidden struct {
	embHiddenInner
	Top string `json:"top"`
}

// embHiddenInt is an unexported non-struct type; embedding it must be ignored. | embHiddenInt 是
// 未导出的非结构体类型，嵌入它必须被忽略。
type embHiddenInt int

// embHiddenNonStruct embeds an unexported non-struct type. | embHiddenNonStruct 嵌入未导出的非结构体类型。
type embHiddenNonStruct struct {
	embHiddenInt
	Top string `json:"top"`
}

// embHiddenPtrTarget is the unexported struct behind an embedded pointer. | embHiddenPtrTarget 是
// 嵌入指针背后的未导出结构体。
type embHiddenPtrTarget struct {
	V string `json:"v"`
}

// embHiddenPtr embeds a pointer to an unexported struct; decoding a promoted field must fail.
// | embHiddenPtr 嵌入指向未导出结构体的指针；解码其提升字段必须失败。
type embHiddenPtr struct {
	*embHiddenPtrTarget
	ID int `json:"id"`
}

// embOmitTag supplies a tagged field with omitempty. | embOmitTag 提供带 omitempty 的 tag 字段。
type embOmitTag struct {
	Name string `json:"name,omitempty"`
}

// embOmitPlain supplies an untagged same-named field. | embOmitPlain 提供同名的无 tag 字段。
type embOmitPlain struct {
	Name string
}

// embOmit proves the tagged field wins and its omitempty still applies. | embOmit 证明带 tag 字段
// 胜出，且其 omitempty 仍然生效。
type embOmit struct {
	embOmitPlain
	embOmitTag
}

// embDashInner hides X with "-" while keeping Y. | embDashInner 用 "-" 隐藏 X，同时保留 Y。
type embDashInner struct {
	X string `json:"-"`
	Y string `json:"y"`
}

// embDashOuter adds a visible outer X; the hidden inner X must not conflict with it. | embDashOuter
// 增加一个可见的外层 X；被隐藏的内层 X 不得与之冲突。
type embDashOuter struct {
	embDashInner
	X string
}

// embDashDepth supplies a deep tagged z field. | embDashDepth 提供深层带 tag 的 z 字段。
type embDashDepth struct {
	Z string `json:"z"`
}

// embDashShallow hides only its own Z, so the deeper z survives. | embDashShallow 只隐藏自己的 Z，
// 因此深层的 z 仍然生效。
type embDashShallow struct {
	embDashDepth
	Z string `json:"-"`
}

// embCaseA supplies Foo. | embCaseA 提供 Foo。
type embCaseA struct {
	Foo string
}

// embCaseB supplies FOO. | embCaseB 提供 FOO。
type embCaseB struct {
	FOO string
}

// embCaseBoth keeps two differently-cased names that fold to the same lower-case key. | embCaseBoth
// 保留两个大小写不同、折叠后同键的名字。
type embCaseBoth struct {
	embCaseA
	embCaseB
}

// embSameInner is embedded twice at the same depth. | embSameInner 在同一深度被嵌入两次。
type embSameInner struct {
	S string `json:"s"`
}

// embSameA embeds embSameInner. | embSameA 嵌入 embSameInner。
type embSameA struct {
	embSameInner
}

// embSameB embeds embSameInner as well. | embSameB 同样嵌入 embSameInner。
type embSameB struct {
	embSameInner
}

// embSameTwice reaches the same type twice at the same depth, so its fields cancel. | embSameTwice
// 在同一深度两次到达同一类型，因此其字段被抵消。
type embSameTwice struct {
	embSameA
	embSameB
}

// embSameNest reaches embSameInner one level deeper than embSameDiff's direct embedding. | embSameNest
// 比 embSameDiff 的直接嵌入深一层到达 embSameInner。
type embSameNest struct {
	embSameA
}

// embSameDiff embeds the same type at different depths; only the shallow copy contributes. | embSameDiff
// 在不同深度嵌入同一类型；只有浅层副本贡献字段。
type embSameDiff struct {
	embSameInner
	embSameNest
}

// EmbRecursive embeds a pointer to itself, which must terminate during field collection. | EmbRecursive
// 嵌入指向自身的指针，字段收集必须能够终止。
type EmbRecursive struct {
	*EmbRecursive
	V string `json:"v"`
}

// EmbTaggedEmbed is embedded with an explicit JSON name, so it is not flattened. | EmbTaggedEmbed
// 带显式 JSON 名嵌入，因此不会被展平。
type EmbTaggedEmbed struct {
	Inner string `json:"inner"`
}

// embAnonTagged embeds a struct under an explicit name. | embAnonTagged 以显式名字嵌入结构体。
type embAnonTagged struct {
	EmbTaggedEmbed `json:"wrapped"`
}

// EmbNamedInt is a named non-struct type. | EmbNamedInt 是具名非结构体类型。
type EmbNamedInt int

// embAnonNonStruct embeds a named non-struct type with and without a tag. | embAnonNonStruct
// 以带 tag 与不带 tag 两种方式嵌入具名非结构体类型。
type embAnonNonStruct struct {
	EmbNamedInt `json:"n"`
	Plain       EmbNamedInt
}

// embFieldCase is one dominance differential case. | embFieldCase 是一个支配规则差分用例。
type embFieldCase struct {
	target func() any
	value  func() any
	name   string
	docs   []string
}

// embFieldCases is the shared dominance differential table. | embFieldCases 是共用的支配规则差分表。
func embFieldCases() []embFieldCase {
	return []embFieldCase{
		{
			name: "depth_shadowing",
			value: func() any {
				return embDepth{embDepthInner{Value: "deep", Deep: "d"}, "shallow"}
			},
			target: func() any { return new(embDepth) },
			docs:   []string{`{"value":"x","deep":"y"}`, `{"VALUE":"x"}`, `{}`},
		},
		{
			name: "depth_beats_tag",
			value: func() any {
				return embShallowUntagged{embDeepTagged{Q: "deep"}, "shallow"}
			},
			target: func() any { return new(embShallowUntagged) },
			docs:   []string{`{"Q":"x"}`, `{"q":"x"}`, `{}`},
		},
		{
			name: "same_depth_tag_wins",
			value: func() any {
				return embTagVsPlain{embPlain{V: "plain"}, embTag{V: "tag"}}
			},
			target: func() any { return new(embTagVsPlain) },
			docs:   []string{`{"V":"a"}`, `{"v":"a"}`, `{"V":"a","V":"b"}`, `{}`},
		},
		{
			name: "same_depth_tag_wins_reversed",
			value: func() any {
				return embTagVsPlainRev{embTag{V: "tag"}, embPlain{V: "plain"}}
			},
			target: func() any { return new(embTagVsPlainRev) },
			docs:   []string{`{"V":"a"}`, `{"v":"a"}`, `{}`},
		},
		{
			name:   "same_depth_double_tagged",
			value:  doubleTaggedValue,
			target: func() any { return reflect.New(doubleTaggedType()).Interface() },
			docs:   []string{`{"V":"a"}`, `{"v":"a"}`, `{}`},
		},
		{
			name: "same_depth_double_untagged",
			value: func() any {
				return embDoublePlain{embPlain{V: "a"}, embPlain2{V: "b"}}
			},
			target: func() any { return new(embDoublePlain) },
			docs:   []string{`{"V":"a"}`, `{"v":"a"}`, `{}`},
		},
		{
			name: "value_embedding",
			value: func() any {
				return embValueEmbed{EmbValuePart{Shared: "s", OnlyValue: "v"}, 7}
			},
			target: func() any { return new(embValueEmbed) },
			docs:   []string{`{"shared":"s","only_value":"v","id":7}`, `{"SHARED":"s"}`, `{"shared":null}`},
		},
		{
			name: "pointer_embedding_nil",
			value: func() any {
				return embPtrEmbed{nil, 7}
			},
			target: func() any { return new(embPtrEmbed) },
			docs:   []string{`{"id":7}`, `{}`},
		},
		{
			name: "pointer_embedding_allocated",
			value: func() any {
				return embPtrEmbed{&EmbPtrPart{Shared: "s", OnlyPtr: "p"}, 7}
			},
			target: func() any { return new(embPtrEmbed) },
			docs:   []string{`{"shared":"s","only_ptr":"p","id":7}`, `{"only_ptr":"p"}`, `{"shared":null}`},
		},
		{
			name: "value_pointer_conflict",
			value: func() any {
				return embValuePtrConflict{EmbValuePart{Shared: "vs", OnlyValue: "v"}, &EmbPtrPart{Shared: "ps", OnlyPtr: "p"}}
			},
			target: func() any { return new(embValuePtrConflict) },
			docs:   []string{`{"shared":"x","only_value":"v","only_ptr":"p"}`, `{"shared":"x"}`},
		},
		{
			name: "hidden_anonymous_struct",
			value: func() any {
				return embHidden{embHiddenInner{Visible: "v"}, "top"}
			},
			target: func() any { return new(embHidden) },
			docs:   []string{`{"visible":"v","top":"t"}`, `{"VISIBLE":"v"}`},
		},
		{
			name: "hidden_anonymous_non_struct",
			value: func() any {
				return embHiddenNonStruct{embHiddenInt(5), "top"}
			},
			target: func() any { return new(embHiddenNonStruct) },
			docs:   []string{`{"embHiddenInt":5,"top":"t"}`, `{"TOP":"t"}`},
		},
		{
			name: "hidden_pointer_nil",
			value: func() any {
				return embHiddenPtr{nil, 7}
			},
			target: func() any { return new(embHiddenPtr) },
			docs:   []string{`{"id":7}`, `{}`},
		},
		{
			name: "hidden_pointer_allocated",
			value: func() any {
				return embHiddenPtr{&embHiddenPtrTarget{V: "v"}, 7}
			},
			target: func() any { return new(embHiddenPtr) },
			docs:   []string{`{"id":7}`, `{"v":"x"}`, `{"V":"x"}`},
		},
		{
			name: "omitempty_after_dominance",
			value: func() any {
				return embOmit{embOmitPlain{Name: "plain"}, embOmitTag{Name: ""}}
			},
			target: func() any { return new(embOmit) },
			docs:   []string{`{"name":"x"}`, `{"NAME":"x"}`, `{}`},
		},
		{
			name: "dash_field_does_not_conflict",
			value: func() any {
				return embDashOuter{embDashInner{X: "hidden", Y: "y"}, "outer"}
			},
			target: func() any { return new(embDashOuter) },
			docs:   []string{`{"X":"outer","y":"y"}`, `{"x":"outer"}`, `{"y":"y"}`},
		},
		{
			name: "dash_shallow_keeps_deep",
			value: func() any {
				return embDashShallow{embDashDepth{Z: "deep"}, "hidden"}
			},
			target: func() any { return new(embDashShallow) },
			docs:   []string{`{"z":"x"}`, `{}`},
		},
		{
			name: "case_folded_duplicates",
			value: func() any {
				return embCaseBoth{embCaseA{Foo: "a"}, embCaseB{FOO: "b"}}
			},
			target: func() any { return new(embCaseBoth) },
			docs:   []string{`{"foo":"x"}`, `{"FOO":"y"}`, `{"Foo":"z","FOO":"w"}`},
		},
		{
			name: "same_type_same_depth",
			value: func() any {
				return embSameTwice{embSameA{embSameInner{S: "a"}}, embSameB{embSameInner{S: "b"}}}
			},
			target: func() any { return new(embSameTwice) },
			docs:   []string{`{"s":"x"}`, `{"S":"x"}`, `{}`},
		},
		{
			name: "same_type_different_depth",
			value: func() any {
				return embSameDiff{embSameInner{S: "shallow"}, embSameNest{embSameA{embSameInner{S: "deep"}}}}
			},
			target: func() any { return new(embSameDiff) },
			docs:   []string{`{"s":"x"}`, `{}`},
		},
		{
			name: "recursive_pointer_embedding",
			value: func() any {
				return EmbRecursive{nil, "v"}
			},
			target: func() any { return new(EmbRecursive) },
			docs:   []string{`{"v":"x"}`, `{"V":"x"}`},
		},
		{
			name: "anonymous_struct_with_tag",
			value: func() any {
				return embAnonTagged{EmbTaggedEmbed{Inner: "i"}}
			},
			target: func() any { return new(embAnonTagged) },
			docs:   []string{`{"wrapped":{"inner":"i"}}`, `{"WRAPPED":{"INNER":"i"}}`},
		},
		{
			name: "anonymous_non_struct_fields",
			value: func() any {
				return embAnonNonStruct{EmbNamedInt(5), EmbNamedInt(6)}
			},
			target: func() any { return new(embAnonNonStruct) },
			docs:   []string{`{"n":5,"Plain":6}`, `{"N":5,"plain":6}`},
		},
	}
}

// TestNativeEmbeddedFieldDominanceMarshal compares the native encoder byte-for-byte with encoding/json
// for every dominance shape. | TestNativeEmbeddedFieldDominanceMarshal 对每种支配形态把原生编码器
// 与 encoding/json 做逐字节对比。
func TestNativeEmbeddedFieldDominanceMarshal(t *testing.T) {
	for _, tc := range embFieldCases() {
		t.Run(tc.name, func(t *testing.T) {
			value := tc.value()
			got, gotErr := jsonx.Marshal(value)
			want, wantErr := stdjson.Marshal(value)
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("错误行为不一致 | error behavior differs: jsonx err=%v, stdlib err=%v", gotErr, wantErr)
			}
			if gotErr == nil && !bytes.Equal(got, want) {
				t.Fatalf("编码结果不一致 | encoded bytes differ\njsonx : %s\nstdlib: %s", got, want)
			}
		})
	}
}

// TestNativeEmbeddedFieldDominanceUnmarshal compares decoded values and error behavior with
// encoding/json for every dominance shape. | TestNativeEmbeddedFieldDominanceUnmarshal 对每种支配
// 形态比较解码结果与错误行为是否与 encoding/json 一致。
func TestNativeEmbeddedFieldDominanceUnmarshal(t *testing.T) {
	for _, tc := range embFieldCases() {
		for _, doc := range tc.docs {
			t.Run(tc.name+"/"+doc, func(t *testing.T) {
				got := tc.target()
				gotErr := jsonx.Unmarshal([]byte(doc), got)
				want := tc.target()
				wantErr := stdjson.Unmarshal([]byte(doc), want)
				if (gotErr == nil) != (wantErr == nil) {
					t.Fatalf("错误行为不一致 | error behavior differs: jsonx err=%v, stdlib err=%v", gotErr, wantErr)
				}
				if gotErr == nil && !reflect.DeepEqual(got, want) {
					t.Fatalf("解码结果不一致 | decoded value differs\njsonx : %#v\nstdlib: %#v", got, want)
				}
			})
		}
	}
}

// TestNativeEmbeddedFieldResolvedOnce verifies the encoder and decoder share one resolved field
// list: a repeated marshal/unmarshal cycle behaves exactly like encoding/json. | TestNativeEmbeddedFieldResolvedOnce
// 验证编码器与解码器共用同一份解析结果：重复编解码循环的行为与 encoding/json 完全一致。
func TestNativeEmbeddedFieldResolvedOnce(t *testing.T) {
	for _, tc := range embFieldCases() {
		t.Run(tc.name, func(t *testing.T) {
			first, err := jsonx.Marshal(tc.value())
			if err != nil {
				t.Fatalf("首次编码失败 | first marshal failed: %v", err)
			}
			stdTarget := tc.target()
			stdErr := stdjson.Unmarshal(first, stdTarget)
			got := tc.target()
			gotErr := jsonx.Unmarshal(first, got)
			if (gotErr == nil) != (stdErr == nil) {
				t.Fatalf("回读错误行为不一致 | decode error behavior differs: jsonx err=%v, stdlib err=%v", gotErr, stdErr)
			}
			if gotErr != nil {
				return
			}
			second, err := jsonx.Marshal(got)
			if err != nil {
				t.Fatalf("二次编码失败 | second marshal failed: %v", err)
			}
			want, err := stdjson.Marshal(stdTarget)
			if err != nil {
				t.Fatalf("标准库编码失败 | stdlib marshal failed: %v", err)
			}
			if !bytes.Equal(second, want) {
				t.Fatalf("循环编码漂移 | round-trip drift\njsonx : %s\nstdlib: %s", second, want)
			}
		})
	}
}

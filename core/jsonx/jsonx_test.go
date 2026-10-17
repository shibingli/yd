package jsonx_test

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/core/jsonx"
)

// address is the address struct nested inside profile, used to verify nesting and omitempty semantics. | address 是被 profile 嵌套引用的地址结构，用于验证嵌套与 omitempty 语义。
type address struct {
	City string `json:"city"`
	Zip  string `json:"zip,omitempty"`
}

// profile covers the standard types that jsonx must keep consistent with encoding/json: | profile 覆盖 jsonx 需要与 encoding/json 保持一致的各类标准类型：
// struct / map / slice / pointer / nesting / json.RawMessage / time.Time / | struct / map / slice / 指针 / 嵌套 / json.RawMessage / time.Time /
// omitempty / []byte (base64). | omitempty / []byte（base64）。
type profile struct {
	Name      string             `json:"name"`
	Age       int                `json:"age"`
	Zero      int                `json:"zero"`
	Ratio     float64            `json:"ratio"`
	Active    bool               `json:"active"`
	Nick      string             `json:"nick,omitempty"`
	Tags      []string           `json:"tags"`
	EmptyTags []string           `json:"empty_tags,omitempty"`
	Scores    map[string]float64 `json:"scores"`
	Home      *address           `json:"home"`
	Office    *address           `json:"office"`
	Extra     stdjson.RawMessage `json:"extra"`
	Joined    time.Time          `json:"joined"`
	Blob      []byte             `json:"blob"`
	EmptyBlob []byte             `json:"empty_blob"`
	HTML      string             `json:"html"`
}

// sampleProfile returns a sample value covering as many standard types as possible. | sampleProfile 返回一个尽量覆盖各类标准类型的样本值。
func sampleProfile() profile {
	return profile{
		Name:      "Ada <Lovelace> & Co",
		Age:       36,
		Zero:      0,
		Ratio:     1.5,
		Active:    true,
		Nick:      "",
		Tags:      []string{"go", "json"},
		EmptyTags: nil,
		Scores:    map[string]float64{"b": 2, "a": 1},
		Home:      &address{City: "Beijing", Zip: "100000"},
		Office:    nil,
		Extra:     stdjson.RawMessage(`{"k":[1,2,3],"s":"plain"}`),
		Joined:    time.Date(2026, 10, 16, 12, 30, 45, 123456789, time.UTC),
		Blob:      []byte("hello\x00world"),
		EmptyBlob: []byte{},
		HTML:      `<a href="x">&</a>`,
	}
}

// TestRoundTripStandardTypes verifies that the round-trip semantics of standard types (struct/map/slice/pointer/nesting/ | TestRoundTripStandardTypes 验证标准类型（struct/map/slice/指针/嵌套/
// json.RawMessage/time.Time/omitempty/[]byte base64) match encoding/json. | json.RawMessage/time.Time/omitempty/[]byte base64）的往返语义与 encoding/json 一致。
func TestRoundTripStandardTypes(t *testing.T) {
	in := sampleProfile()

	got, err := jsonx.Marshal(in)
	if err != nil {
		t.Fatalf("jsonx.Marshal: %v", err)
	}
	if !jsonx.Valid(got) {
		t.Fatalf("jsonx.Marshal 输出不是合法 JSON: %s", got)
	}

	want, err := stdjson.Marshal(in)
	if err != nil {
		t.Fatalf("encoding/json.Marshal: %v", err)
	}

	// Byte-for-byte alignment with the standard library: field order, map key sorting, HTML escaping, time format and base64 must all match. | 与标准库逐字节对齐：字段顺序、map key 排序、HTML 转义、时间格式、base64 均需一致。
	if !bytes.Equal(got, want) {
		t.Fatalf("Marshal 输出与标准库不一致\njsonx : %s\nstdlib: %s", got, want)
	}

	var back profile
	if err := jsonx.Unmarshal(got, &back); err != nil {
		t.Fatalf("jsonx.Unmarshal: %v", err)
	}
	var stdBack profile
	if err := stdjson.Unmarshal(want, &stdBack); err != nil {
		t.Fatalf("encoding/json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back, stdBack) {
		t.Fatalf("往返结果与标准库不一致\njsonx : %#v\nstdlib: %#v", back, stdBack)
	}

	t.Run("scalar_and_nested", func(t *testing.T) {
		if back.Name != in.Name || back.Age != in.Age || back.Zero != 0 || back.Ratio != in.Ratio || !back.Active {
			t.Fatalf("标量字段往返失败: %#v", back)
		}
		if back.Home == nil || *back.Home != *in.Home {
			t.Fatalf("嵌套指针字段往返失败: %#v", back.Home)
		}
		if back.Office != nil {
			t.Fatalf("nil 指针应保持 nil，实际 %#v", back.Office)
		}
		if !reflect.DeepEqual(back.Tags, in.Tags) || !reflect.DeepEqual(back.Scores, in.Scores) {
			t.Fatalf("slice/map 往返失败: %#v %#v", back.Tags, back.Scores)
		}
	})

	t.Run("omitempty", func(t *testing.T) {
		raw := string(got)
		for _, key := range []string{"nick", "empty_tags"} {
			if strings.Contains(raw, key) {
				t.Fatalf("omitempty 字段 %q 应被省略，实际输出 %s", key, raw)
			}
		}
		if !strings.Contains(raw, `"zero":0`) {
			t.Fatalf("非 omitempty 的零值必须保留，实际输出 %s", raw)
		}
	})

	t.Run("raw_message", func(t *testing.T) {
		if !bytes.Equal([]byte(back.Extra), []byte(in.Extra)) {
			t.Fatalf("json.RawMessage 往返被改写: %s", back.Extra)
		}
	})

	t.Run("time", func(t *testing.T) {
		if !back.Joined.Equal(in.Joined) {
			t.Fatalf("time.Time 往返时刻不一致: %v != %v", back.Joined, in.Joined)
		}
		if !strings.Contains(string(got), `"2026-10-16T12:30:45.123456789Z"`) {
			t.Fatalf("time.Time 应为 RFC3339Nano 文本，实际输出 %s", got)
		}
	})

	t.Run("bytes_base64", func(t *testing.T) {
		if !bytes.Equal(back.Blob, in.Blob) {
			t.Fatalf("[]byte 往返失败: %q != %q", back.Blob, in.Blob)
		}
		if !strings.Contains(string(got), `"blob":"aGVsbG8Ad29ybGQ="`) {
			t.Fatalf("[]byte 应编码为 base64（StdEncoding），实际输出 %s", got)
		}
		if back.EmptyBlob == nil || len(back.EmptyBlob) != 0 {
			t.Fatalf("空 []byte 应仍为空的非 nil 切片，实际 %#v", back.EmptyBlob)
		}
		if !strings.Contains(string(got), `"empty_blob":""`) {
			t.Fatalf("空 []byte 应编码为空字符串，实际输出 %s", got)
		}

		var direct struct {
			Blob []byte `json:"blob"`
		}
		if err := jsonx.Unmarshal([]byte(`{"blob":"aGVsbG8="}`), &direct); err != nil {
			t.Fatalf("base64 解码失败: %v", err)
		}
		if string(direct.Blob) != "hello" {
			t.Fatalf("base64 解码结果错误: %q", direct.Blob)
		}
	})

	t.Run("html_escaped", func(t *testing.T) {
		// The & < > inside the string must be escaped to & < >, and no bare character may appear. | 字符串中的 & < > 必须转义为 & < >，且不得出现裸字符。
		// Only the tail of the escape sequences is asserted here (avoiding the escape spelling itself), to prevent ambiguity in the test source. | 这里只断言转义序列的尾部（避开转义写法本身），避免测试源码歧义。
		raw := string(got)
		for _, esc := range []string{"u0026", "u003c", "u003e"} {
			if !strings.Contains(raw, esc) {
				t.Fatalf("缺少 HTML 转义序列 %q，实际输出 %s", esc, raw)
			}
		}
		if strings.Contains(raw, "<") || strings.Contains(raw, "&") {
			t.Fatalf("不应出现未转义的 HTML 字符，实际输出 %s", raw)
		}
	})
}

// TestUnmarshalIntoAnyMatchesStdlib verifies that the dynamic types when unmarshalling into any match the standard library. | TestUnmarshalIntoAnyMatchesStdlib 验证解析到 any 时的动态类型与标准库一致。
func TestUnmarshalIntoAnyMatchesStdlib(t *testing.T) {
	const doc = `{"i":1,"neg":-2,"f":3.5,"big":1000000,"s":"<&>","b":true,"n":null,` +
		`"arr":[1,2.5,true,null,"x"],"nest":{"deep":{"k":"v"}},"empty":{},"emptyArr":[]}`

	var got any
	if err := jsonx.Unmarshal([]byte(doc), &got); err != nil {
		t.Fatalf("jsonx.Unmarshal: %v", err)
	}
	var want any
	if err := stdjson.Unmarshal([]byte(doc), &want); err != nil {
		t.Fatalf("encoding/json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("解析到 any 的结果与标准库不一致\njsonx : %#v\nstdlib: %#v", got, want)
	}

	obj, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("顶层应为 map[string]any，实际 %T", got)
	}
	if _, ok := obj["i"].(float64); !ok {
		t.Fatalf("数字应解析为 float64，实际 %T", obj["i"])
	}
	if _, ok := obj["b"].(bool); !ok {
		t.Fatalf("布尔应解析为 bool，实际 %T", obj["b"])
	}
	if v, ok := obj["n"]; !ok || v != nil {
		t.Fatalf("null 应解析为 nil，实际 %#v", v)
	}
	if arr, ok := obj["arr"].([]any); !ok || len(arr) != 5 {
		t.Fatalf("数组应解析为 []any，实际 %#v", obj["arr"])
	}
	if _, ok := obj["empty"].(map[string]any); !ok {
		t.Fatalf("空对象应解析为 map[string]any，实际 %T", obj["empty"])
	}
	if _, ok := obj["emptyArr"].([]any); !ok {
		t.Fatalf("空数组应解析为 []any，实际 %T", obj["emptyArr"])
	}
}

// TestMarshalIndentMatchesStdlibShape verifies that the indented output matches the shape of encoding/json.MarshalIndent. | TestMarshalIndentMatchesStdlibShape 验证缩进输出与 encoding/json.MarshalIndent 形状一致。
func TestMarshalIndentMatchesStdlibShape(t *testing.T) {
	in := sampleProfile()

	cases := []struct {
		name   string
		prefix string
		indent string
	}{
		{name: "two_spaces", prefix: "", indent: "  "},
		{name: "tab_with_prefix", prefix: "//", indent: "\t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsonx.MarshalIndent(in, tc.prefix, tc.indent)
			if err != nil {
				t.Fatalf("jsonx.MarshalIndent: %v", err)
			}
			want, err := stdjson.MarshalIndent(in, tc.prefix, tc.indent)
			if err != nil {
				t.Fatalf("encoding/json.MarshalIndent: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("缩进输出与标准库不一致\njsonx : %q\nstdlib: %q", got, want)
			}
			if !bytes.Contains(got, []byte("\n")) {
				t.Fatalf("缩进输出应包含换行: %q", got)
			}
			// The first line is "{", carrying neither prefix nor indent; subsequent lines carry prefix+indent. | 首行是 "{"，不带前缀也不带缩进；后续行带 prefix+indent。
			if !bytes.HasPrefix(got, []byte("{")) {
				t.Fatalf("首行不应带前缀/缩进: %q", got)
			}
			if tc.indent != "" && !bytes.Contains(got, []byte("\n"+tc.prefix+tc.indent)) {
				t.Fatalf("后续行应以 prefix+indent 开头: %q", got)
			}
			// The indented output (when prefix is empty) must parse back into a semantically identical value. | 缩进输出（无 prefix 时）必须能解析回语义相同的值。
			// Note: with a non-empty prefix the standard library output contains comment-style prefixes and is not valid JSON itself. | 注意：带非空 prefix 时标准库输出含注释式前缀，本身不是合法 JSON。
			if tc.prefix == "" {
				var back profile
				if err := jsonx.Unmarshal(got, &back); err != nil {
					t.Fatalf("解析缩进输出失败: %v", err)
				}
				if back.Name != in.Name || back.Age != in.Age {
					t.Fatalf("缩进输出往返失败: %#v", back)
				}
			}
		})
	}
}

// TestEncoderDecoderStream verifies streaming encode/decode: newline separation, the HTML escaping switch, UseNumber and io.EOF. | TestEncoderDecoderStream 验证流式编解码：换行分隔、HTML 转义开关、UseNumber、io.EOF。
func TestEncoderDecoderStream(t *testing.T) {
	var buf bytes.Buffer
	enc := jsonx.NewEncoder(&buf)
	for _, v := range []any{map[string]int{"a": 1}, []int{2, 3}} {
		if err := enc.Encode(v); err != nil {
			t.Fatalf("Encoder.Encode: %v", err)
		}
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("每条消息应占一行，实际 %q", buf.String())
	}
	for _, line := range lines {
		if !jsonx.Valid([]byte(line)) {
			t.Fatalf("流中的行不是合法 JSON: %q", line)
		}
	}

	dec := jsonx.NewDecoder(bytes.NewReader(buf.Bytes()))
	var m map[string]int
	if err := dec.Decode(&m); err != nil || m["a"] != 1 {
		t.Fatalf("Decoder.Decode 第一个值失败: %v %#v", err, m)
	}
	var s []int
	if err := dec.Decode(&s); err != nil || len(s) != 2 || s[0] != 2 {
		t.Fatalf("Decoder.Decode 第二个值失败: %v %#v", err, s)
	}
	if err := dec.Decode(&m); !errors.Is(err, io.EOF) {
		t.Fatalf("流结束后应返回 io.EOF，实际 %v", err)
	}

	t.Run("escape_html_toggle", func(t *testing.T) {
		const html = "a<b>c&d"

		var on bytes.Buffer
		encOn := jsonx.NewEncoder(&on)
		if err := encOn.Encode(html); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if !strings.Contains(on.String(), "u003cb") || !strings.Contains(on.String(), "u0026") {
			t.Fatalf("默认应转义 HTML 字符，实际 %q", on.String())
		}
		if strings.Contains(on.String(), "<b>") || strings.Contains(on.String(), "&") {
			t.Fatalf("默认不应出现未转义的 HTML 字符，实际 %q", on.String())
		}

		var off bytes.Buffer
		encOff := jsonx.NewEncoder(&off)
		encOff.SetEscapeHTML(false)
		if err := encOff.Encode(html); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if !strings.Contains(off.String(), html) {
			t.Fatalf("SetEscapeHTML(false) 后不应转义，实际 %q", off.String())
		}
		// After escaping is turned off the output is still valid JSON and can be decoded back as-is. | 关闭转义后仍是合法 JSON，且能原样解回。
		var back string
		if err := jsonx.Unmarshal(bytes.TrimSpace(off.Bytes()), &back); err != nil || back != html {
			t.Fatalf("关闭转义后的输出解析失败: %v %q", err, back)
		}
	})

	t.Run("use_number", func(t *testing.T) {
		d := jsonx.NewDecoder(strings.NewReader(`{"n":12345678901234567890,"f":1.5}`))
		d.UseNumber()
		var obj map[string]any
		if err := d.Decode(&obj); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		n, ok := obj["n"].(stdjson.Number)
		if !ok {
			t.Fatalf("UseNumber 后数字应为 json.Number，实际 %T", obj["n"])
		}
		if n.String() != "12345678901234567890" {
			t.Fatalf("UseNumber 应保留原始数字文本，实际 %q", n)
		}
		if _, ok := obj["f"].(stdjson.Number); !ok {
			t.Fatalf("UseNumber 后浮点数应为 json.Number，实际 %T", obj["f"])
		}
	})
}

// TestValid verifies that Valid's judgement matches encoding/json.Valid, | TestValid 验证 Valid 的判定与 encoding/json.Valid 一致，
// and explicitly marks the known lenient region of the sonic backend on illegal escape sequences. | 并显式标注 sonic 后端在非法转义序列上的已知宽松区。
func TestValid(t *testing.T) {
	// Inputs that all three backends must judge as valid. | 三个后端都必须判为合法的输入。
	valid := []string{
		`{}`, `[]`, `null`, `true`, `false`, `0`, `-1.5e10`, `"str"`,
		`{"a":[1,{"b":null}]}`, `"<"`, "  {\"a\":1}  ", `[1,2,3]`,
		`{"a":1,"a":2}`, `"😀"`, `[[[]]]`, `{"":0}`,
	}
	// Structurally invalid inputs: all three backends must judge them invalid, consistent with the standard library. | 结构性非法的输入：三个后端都必须判为非法，且与标准库一致。
	invalid := []string{
		``, `   `, `{`, `{"a":1,}`, `[1,2`, `{'a':1}`, `{"a":1} extra`,
		`tru`, `01`, `[1 2]`, `nul`, `[1,]`, `{"a":}`, `1.`, `.1`, `+1`,
		`1e`, `--1`, `NaN`, `Infinity`, `[01]`, `tRue`,
	}

	for _, in := range valid {
		if got, want := jsonx.Valid([]byte(in)), stdjson.Valid([]byte(in)); !got || !want {
			t.Errorf("Valid(%q) = %v，标准库 = %v（两者都应为 true）", in, got, want)
		}
	}
	for _, in := range invalid {
		if got, want := jsonx.Valid([]byte(in)), stdjson.Valid([]byte(in)); got || want {
			t.Errorf("Valid(%q) = %v，标准库 = %v（两者都应为 false）", in, got, want)
		}
	}

	// Known lenient region: sonic's Valid only performs structural validation and does not validate illegal escape sequences | 已知宽松区：sonic 的 Valid 只做结构校验，不校验字符串内的非法转义序列
	// inside strings (\x, \u12, \q) or unescaped control characters, so it is more lenient than the standard library. Here we only require | （\x、\u12、\q）与未转义控制字符，因此比标准库宽松。这里只要求
	// that jsonx is never stricter than the standard library, avoiding treating a backend difference as a framework regression. | jsonx 永不比标准库更严格，避免把后端差异当成框架回归。
	backslash := string([]byte{92}) // a single backslash, avoiding escape ambiguity in the test source | 单个反斜杠，避免测试源码中的转义歧义
	lenient := []string{
		`"` + backslash + `x"`,
		`"` + backslash + `u12"`,
		`"` + backslash + `q"`,
		`"` + backslash + `a"`,
		`"` + string([]byte{1}) + `"`,
	}
	for _, in := range lenient {
		got, want := jsonx.Valid([]byte(in)), stdjson.Valid([]byte(in))
		if want && !got {
			t.Errorf("Valid(%q) = false，但标准库为 true：jsonx 不应比标准库更严格", in)
		}
		if !want && got {
			t.Logf("后端 %s 的宽松区：Valid(%q) = true，标准库 = false", jsonx.Backend(), in)
		}
	}
}

// TestBackendNameIsKnown verifies that Backend() returns a known name matching the running architecture. | TestBackendNameIsKnown 验证 Backend() 返回已知名称且与运行架构相符。
func TestBackendNameIsKnown(t *testing.T) {
	name := jsonx.Backend()
	t.Logf("GOARCH=%s 生效的 JSON 后端为 %q", runtime.GOARCH, name)

	known := map[string]bool{"sonic": true, "go-json": true, "encoding/json": true, "yd-native": true}
	if !known[name] {
		t.Fatalf("Backend() 返回未知后端 %q", name)
	}
	if name == "" {
		t.Fatal("Backend() must not be empty | Backend() 不应为空")
	}
	if again := jsonx.Backend(); again != name {
		t.Fatalf("Backend() 多次调用结果不一致: %q != %q", name, again)
	}
	switch runtime.GOARCH {
	case "amd64":
		if name == "go-json" {
			t.Fatalf("amd64 应使用 sonic 或 encoding/json，实际 %q", name)
		}
	default:
		if name == "sonic" {
			t.Fatalf("非 amd64 不得使用 sonic，实际 %q", name)
		}
	}
}

// TestUnmarshalErrorPropagates verifies that backend errors are propagated and can be classified across backends | TestUnmarshalErrorPropagates 验证后端错误会被传播，且可由 ErrSyntax /
// with ErrSyntax / ErrTypeMismatch. | ErrTypeMismatch 做跨后端判定。
func TestUnmarshalErrorPropagates(t *testing.T) {
	t.Run("syntax", func(t *testing.T) {
		var out map[string]any
		err := jsonx.Unmarshal([]byte(`{"n":`), &out)
		if err == nil {
			t.Fatal("a syntax error must return an error | 语法错误必须返回 error")
		}
		if !errors.Is(err, jsonx.ErrSyntax) {
			t.Fatalf("应可判定为 ErrSyntax，实际 %v", err)
		}
		if err.Error() == "" || !strings.Contains(err.Error(), "jsonx:") {
			t.Fatalf("the error text must keep the jsonx prefix and the backend detail, got %[1]q | 错误信息应保留 jsonx 前缀与后端详情，实际 %[1]q", err.Error())
		}
	})

	t.Run("type_mismatch", func(t *testing.T) {
		var out struct {
			N int `json:"n"`
		}
		err := jsonx.Unmarshal([]byte(`{"n":"not-a-number"}`), &out)
		if err == nil {
			t.Fatal("a type mismatch must return an error | 类型不匹配必须返回 error")
		}
		if !errors.Is(err, jsonx.ErrTypeMismatch) {
			t.Fatalf("应可判定为 ErrTypeMismatch，实际 %v", err)
		}
	})

	t.Run("invalid_argument", func(t *testing.T) {
		var out int
		if err := jsonx.Unmarshal([]byte(`123`), out); err == nil {
			t.Fatal("a non-pointer target must return an error | 非指针目标必须返回 error")
		}
	})

	t.Run("nil_target", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("nil 目标不得 panic，实际 panic: %v", r)
			}
		}()
		if err := jsonx.Unmarshal([]byte(`1`), nil); err == nil {
			t.Fatal("a nil target must return an error | nil 目标必须返回 error")
		}
	})

	t.Run("reader_error", func(t *testing.T) {
		dec := jsonx.NewDecoder(strings.NewReader(`{"a":`))
		var out map[string]any
		if err := dec.Decode(&out); err == nil {
			t.Fatal("invalid JSON in the stream must return an error | 流中非法 JSON 必须返回 error")
		}
	})

	t.Run("marshal_unsupported", func(t *testing.T) {
		if _, err := jsonx.Marshal(func() {}); err == nil {
			t.Fatal("an unencodable type must return an error | 不可编码类型必须返回 error")
		}
	})
}

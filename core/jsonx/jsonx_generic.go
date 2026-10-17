//go:build !amd64 && !jsonx_std

// This file is the jsonx implementation on non-amd64 architectures: it binds github.com/goccy/go-json. | 本文件是 jsonx 在非 amd64 架构上的实现：绑定 github.com/goccy/go-json。
//
// go-json is a pure-Go high-performance replacement for encoding/json (no JIT required, safe to use on | go-json 是纯 Go 实现的 encoding/json 高性能替代品（无需 JIT，可安全用于
// arm64 / 386 / riscv64 and other platforms), API- and semantics-compatible as a drop-in for the standard library. | arm64 / 386 / riscv64 等平台），API 与语义和标准库 drop-in 兼容。
//
// Build constraint note: this is not written as `!amd64 || jsonx_std`, because that expression has | 构建约束说明：这里没有写成 `!amd64 || jsonx_std`，因为该表达式与
// an intersection with `jsonx_std` of jsonx_std.go, which would compile both files at the same time and produce duplicate symbols; | jsonx_std.go 的 `jsonx_std` 存在交集，会同时编译两个文件而产生重复符号；
// therefore the constraints of the three implementation files are designed to be pairwise mutually exclusive, with a union covering every combination: | 因此三个实现文件的约束被设计为两两互斥、并集覆盖全部组合：
//
//	jsonx_amd64.go   : amd64 && !jsonx_std   → sonic
//	jsonx_generic.go : !amd64 && !jsonx_std  → go-json
//	jsonx_std.go     : jsonx_std             → encoding/json
//
// Under any (GOARCH, jsonx_std) combination, exactly one implementation file takes part in compilation. | 任一 (GOARCH, jsonx_std) 组合下都恰好只有一个实现文件参与编译。

package jsonx

import (
	"io"

	gojson "github.com/goccy/go-json"
)

// Backend returns the name of the currently active JSON backend; on non-amd64 without the jsonx_std tag it is "go-json". | Backend 返回当前生效的 JSON 后端名称；非 amd64 且未设置 jsonx_std 标签时为 "go-json"。
func Backend() string {
	return "go-json"
}

// Marshal returns the JSON encoding of v. | Marshal 返回 v 的 JSON 编码。
//
// The semantics match encoding/json.Marshal: HTML characters are escaped, and map keys are output in sorted order. | 语义与 encoding/json.Marshal 一致：HTML 字符被转义，map 的 key 排序输出。
func Marshal(v any) ([]byte, error) {
	return gojson.Marshal(v)
}

// Unmarshal parses JSON-encoded data and stores the result in the value pointed to by v. | Unmarshal 解析 JSON 编码的数据并将其存入 v 指向的值。
//
// The error returned on failure can be matched with errors.Is against ErrSyntax or ErrTypeMismatch. | 出错时返回的错误可用 errors.Is 判定为 ErrSyntax 或 ErrTypeMismatch。
func Unmarshal(data []byte, v any) error {
	return wrapDecodeError(data, gojson.Unmarshal(data, v))
}

// MarshalIndent is like Marshal but indents the output according to prefix and indent, | MarshalIndent 与 Marshal 类似，但按 prefix 与 indent 缩进输出，
// with a shape identical to encoding/json.MarshalIndent. | 形状与 encoding/json.MarshalIndent 一致。
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return gojson.MarshalIndent(v, prefix, indent)
}

// Valid reports whether data is valid JSON text. | Valid 报告 data 是否是合法的 JSON 文本。
func Valid(data []byte) bool {
	return gojson.Valid(data)
}

// Encoder streams JSON values to an io.Writer, terminating each message with a newline. | Encoder 将 JSON 值流式写入 io.Writer，每条消息以换行结尾。
type Encoder struct {
	enc *gojson.Encoder
}

// NewEncoder returns an Encoder writing to w, escaping HTML characters by default (consistent with encoding/json). | NewEncoder 返回写入 w 的 Encoder，默认转义 HTML 字符（与 encoding/json 一致）。
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{enc: gojson.NewEncoder(w)}
}

// Encode writes the JSON encoding of v to the underlying io.Writer and appends a newline. | Encode 将 v 的 JSON 编码写入底层 io.Writer，并追加一个换行符。
func (e *Encoder) Encode(v any) error {
	return e.enc.Encode(v)
}

// SetEscapeHTML specifies whether to escape HTML characters such as & < > inside JSON strings; | SetEscapeHTML 指定是否在 JSON 字符串中转义 & < > 等 HTML 字符；
// turning it off makes the output more readable, but unsuitable for embedding directly into HTML. | 关闭后输出更易读，但不适合直接嵌入 HTML。
func (e *Encoder) SetEscapeHTML(on bool) {
	e.enc.SetEscapeHTML(on)
}

// Decoder reads and parses JSON values from an io.Reader, and can read several values in a row from the stream. | Decoder 从 io.Reader 读取并解析 JSON 值，可连续读取流中的多个值。
type Decoder struct {
	dec *gojson.Decoder
}

// NewDecoder returns a Decoder that reads from r. | NewDecoder 返回从 r 读取的 Decoder。
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{dec: gojson.NewDecoder(r)}
}

// Decode reads the next JSON value from the input stream and stores it in the value pointed to by v; | Decode 从输入流读取下一个 JSON 值并存入 v 指向的值；
// it returns io.EOF when the stream is exhausted. | 流耗尽时返回 io.EOF。
func (d *Decoder) Decode(v any) error {
	return d.dec.Decode(v)
}

// UseNumber causes Decode to parse numbers as json.Number (preserving the original text) instead of float64. | UseNumber 使 Decode 把数字解析为 json.Number（保留原始文本）而不是 float64。
func (d *Decoder) UseNumber() {
	d.dec.UseNumber()
}

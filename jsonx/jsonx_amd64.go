//go:build amd64 && !jsonx_std

// This file is the jsonx implementation on amd64: it binds github.com/bytedance/sonic. | 本文件是 jsonx 在 amd64 上的实现：绑定 github.com/bytedance/sonic。
//
// sonic accelerates encoding and decoding through JIT compilation + SIMD (AVX2/SSE), and is configured as sonic.ConfigStd, | sonic 通过 JIT 编译 + SIMD（AVX2/SSE）加速编解码，并配置为 sonic.ConfigStd
// the configuration "compatible with encoding/json" (HTML escaping, map key sorting, json.Marshaler compaction), | ——「与 encoding/json 兼容」的配置（HTML 转义、map key 排序、json.Marshaler 压缩），
// guaranteeing an output shape identical to the standard library. Setting the jsonx_std build tag switches entirely to encoding/json. | 保证与标准库输出形状一致。设置 jsonx_std 构建标签可整体切换到 encoding/json。

package jsonx

import (
	"io"

	"github.com/bytedance/sonic"
)

// backend is sonic's standard-compatible configuration, equivalent to using the sonic.ConfigStd APIs directly. | backend 是 sonic 的标准兼容配置，等价于直接使用 sonic.ConfigStd 的各项 API。
var backend = sonic.ConfigStd

// Backend returns the name of the currently active JSON backend; on amd64 without the jsonx_std tag it is "sonic". | Backend 返回当前生效的 JSON 后端名称；amd64 且未设置 jsonx_std 标签时为 "sonic"。
func Backend() string {
	return "sonic"
}

// Marshal returns the JSON encoding of v. | Marshal 返回 v 的 JSON 编码。
//
// The semantics match encoding/json.Marshal: HTML characters are escaped, and map keys are output in sorted order. | 语义与 encoding/json.Marshal 一致：HTML 字符被转义，map 的 key 排序输出。
func Marshal(v any) ([]byte, error) {
	return backend.Marshal(v)
}

// Unmarshal parses JSON-encoded data and stores the result in the value pointed to by v. | Unmarshal 解析 JSON 编码的数据并将其存入 v 指向的值。
//
// The error returned on failure can be matched with errors.Is against ErrSyntax or ErrTypeMismatch. | 出错时返回的错误可用 errors.Is 判定为 ErrSyntax 或 ErrTypeMismatch。
func Unmarshal(data []byte, v any) error {
	return wrapDecodeError(data, backend.Unmarshal(data, v))
}

// MarshalIndent is like Marshal but indents the output according to prefix and indent, | MarshalIndent 与 Marshal 类似，但按 prefix 与 indent 缩进输出，
// with a shape identical to encoding/json.MarshalIndent. | 形状与 encoding/json.MarshalIndent 一致。
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return backend.MarshalIndent(v, prefix, indent)
}

// Valid reports whether data is valid JSON text. | Valid 报告 data 是否是合法的 JSON 文本。
//
// Note: sonic only performs structural validation, and is more lenient than encoding/json about illegal escape sequences | 注意：sonic 只做结构校验，对字符串中的非法转义序列（如 \x、\u12）与未转义控制
// inside strings (such as \x, \u12) and unescaped control characters (it treats them as valid); for strict validation use the jsonx_std build tag instead. | 字符比 encoding/json 宽松（会判为合法）；需要严格校验请改用 jsonx_std 构建标签。
func Valid(data []byte) bool {
	return backend.Valid(data)
}

// Encoder streams JSON values to an io.Writer, terminating each message with a newline. | Encoder 将 JSON 值流式写入 io.Writer，每条消息以换行结尾。
type Encoder struct {
	enc sonic.Encoder
}

// NewEncoder returns an Encoder writing to w, escaping HTML characters by default (consistent with encoding/json). | NewEncoder 返回写入 w 的 Encoder，默认转义 HTML 字符（与 encoding/json 一致）。
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{enc: backend.NewEncoder(w)}
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
	dec sonic.Decoder
}

// NewDecoder returns a Decoder that reads from r. | NewDecoder 返回从 r 读取的 Decoder。
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{dec: backend.NewDecoder(r)}
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

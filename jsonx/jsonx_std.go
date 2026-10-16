//go:build jsonx_std

// 本文件是 jsonx 的标准库实现：设置 jsonx_std 构建标签后整体切换到 encoding/json，
// 用于排障、兼容性对照与极端环境下的最小依赖构建。
//
// 构建约束 `jsonx_std` 与 jsonx_amd64.go 的 `amd64 && !jsonx_std`、
// jsonx_generic.go 的 `!amd64 && !jsonx_std` 互斥，因此不会出现重复符号。

package jsonx

import (
	stdjson "encoding/json"
	"io"
)

// Backend 返回当前生效的 JSON 后端名称；设置 jsonx_std 标签时恒为 "encoding/json"。
func Backend() string {
	return "encoding/json"
}

// Marshal 返回 v 的 JSON 编码（标准库 encoding/json 语义）。
func Marshal(v any) ([]byte, error) {
	return stdjson.Marshal(v)
}

// Unmarshal 解析 JSON 编码的数据并将其存入 v 指向的值。
//
// 出错时返回的错误可用 errors.Is 判定为 ErrSyntax 或 ErrTypeMismatch。
func Unmarshal(data []byte, v any) error {
	return wrapDecodeError(data, stdjson.Unmarshal(data, v))
}

// MarshalIndent 与 Marshal 类似，但按 prefix 与 indent 缩进输出。
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return stdjson.MarshalIndent(v, prefix, indent)
}

// Valid 报告 data 是否是合法的 JSON 文本。
func Valid(data []byte) bool {
	return stdjson.Valid(data)
}

// Encoder 将 JSON 值流式写入 io.Writer，每条消息以换行结尾。
type Encoder struct {
	enc *stdjson.Encoder
}

// NewEncoder 返回写入 w 的 Encoder，默认转义 HTML 字符。
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{enc: stdjson.NewEncoder(w)}
}

// Encode 将 v 的 JSON 编码写入底层 io.Writer，并追加一个换行符。
func (e *Encoder) Encode(v any) error {
	return e.enc.Encode(v)
}

// SetEscapeHTML 指定是否在 JSON 字符串中转义 & < > 等 HTML 字符；
// 关闭后输出更易读，但不适合直接嵌入 HTML。
func (e *Encoder) SetEscapeHTML(on bool) {
	e.enc.SetEscapeHTML(on)
}

// Decoder 从 io.Reader 读取并解析 JSON 值，可连续读取流中的多个值。
type Decoder struct {
	dec *stdjson.Decoder
}

// NewDecoder 返回从 r 读取的 Decoder。
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{dec: stdjson.NewDecoder(r)}
}

// Decode 从输入流读取下一个 JSON 值并存入 v 指向的值；
// 流耗尽时返回 io.EOF。
func (d *Decoder) Decode(v any) error {
	return d.dec.Decode(v)
}

// UseNumber 使 Decode 把数字解析为 json.Number（保留原始文本）而不是 float64。
func (d *Decoder) UseNumber() {
	d.dec.UseNumber()
}

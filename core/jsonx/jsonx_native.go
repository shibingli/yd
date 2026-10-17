//go:build jsonx_native

package jsonx

import (
	"bytes"
	"encoding"
	stdjson "encoding/json"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/shibingli/yd/core/errx"
)

// Backend returns the active backend name for this build. | Backend 返回当前构建使用的后端名称。
func Backend() string { return "yd-native" }

var (
	numberType          = reflect.TypeOf(stdjson.Number(""))
	rawMessageType      = reflect.TypeOf(stdjson.RawMessage(nil))
	timeType            = reflect.TypeOf(time.Time{})
	stringSliceType     = reflect.TypeOf([]string(nil))
	marshalerType       = reflect.TypeOf((*stdjson.Marshaler)(nil)).Elem()
	unmarshalerType     = reflect.TypeOf((*stdjson.Unmarshaler)(nil)).Elem()
	textMarshalerType   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// encoderPool reuses output buffers across Marshal calls. | encoderPool 在多次 Marshal 调用间复用输出缓冲。
var encoderPool = sync.Pool{
	New: func() any { return &encoder{buf: make([]byte, 0, 16<<10)} },
}

const encoderPoolMax = 1 << 20

func getEncoder(escapeHTML bool) *encoder {
	e := encoderPool.Get().(*encoder)
	e.buf = e.buf[:0]
	e.escapeHTML = escapeHTML
	return e
}

func putEncoder(e *encoder) {
	if cap(e.buf) <= encoderPoolMax {
		e.buf = e.buf[:0]
		encoderPool.Put(e)
	}
}

// Marshal returns the JSON encoding of v. | Marshal 返回 v 的 JSON 编码。
func Marshal(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if est := estimateValueSize(rv); est >= 64 {
		const maxHint = 4 << 20
		if est > maxHint {
			est = maxHint
		}
		e := &encoder{buf: make([]byte, 0, est), escapeHTML: true}
		if err := e.value(rv); err != nil {
			return nil, err
		}
		return e.buf, nil
	}
	e := getEncoder(true)
	if err := e.value(rv); err != nil {
		putEncoder(e)
		return nil, err
	}
	out := make([]byte, len(e.buf))
	copy(out, e.buf)
	putEncoder(e)
	return out, nil
}

// MarshalIndent is like Marshal but indents the output. | MarshalIndent 与 Marshal 类似，但缩进输出。
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := indentJSON(&out, b, prefix, indent); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Valid reports whether data is a single valid JSON value. | Valid 报告 data 是否是单个合法 JSON 值。
func Valid(data []byte) bool {
	d := &decoder{data: data}
	d.skipSpace()
	if err := d.skipValue(); err != nil {
		return false
	}
	d.skipSpace()
	return d.pos == len(data)
}

// Marshaler and Unmarshaler interfaces are the standard library's; this backend uses them directly. | Marshaler 与
// Unmarshaler 接口直接采用标准库定义，保证与 encoding/json 的扩展点一致。

// Encoder writes JSON values to an io.Writer. | Encoder 把 JSON 值写入 io.Writer。
type Encoder struct {
	w          io.Writer
	escapeHTML bool
}

// NewEncoder returns an Encoder writing to w. | NewEncoder 返回写入 w 的 Encoder。
func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w, escapeHTML: true} }

// Encode writes v followed by a newline. | Encode 写入 v 并追加换行。
func (e *Encoder) Encode(v any) error {
	enc := getEncoder(e.escapeHTML)
	if err := enc.value(reflect.ValueOf(v)); err != nil {
		putEncoder(enc)
		return err
	}
	enc.buf = append(enc.buf, '\n')
	_, err := e.w.Write(enc.buf)
	putEncoder(enc)
	return err
}

// SetEscapeHTML controls HTML escaping. | SetEscapeHTML 控制 HTML 转义。
func (e *Encoder) SetEscapeHTML(on bool) { e.escapeHTML = on }

// Decoder reads JSON values from an io.Reader. | Decoder 从 io.Reader 读取 JSON 值。
type Decoder struct {
	r         io.Reader
	data      []byte
	pos       int
	loaded    bool
	useNumber bool
}

// NewDecoder returns a Decoder reading from r. | NewDecoder 返回从 r 读取的 Decoder。
func NewDecoder(r io.Reader) *Decoder { return &Decoder{r: r} }

// UseNumber makes Decode store numbers as json.Number. | UseNumber 使 Decode 把数字存为 json.Number。
func (d *Decoder) UseNumber() { d.useNumber = true }

// Decode reads the next JSON value into v. | Decode 读取下一个 JSON 值并写入 v。
func (d *Decoder) Decode(v any) error {
	if !d.loaded {
		data, err := io.ReadAll(d.r)
		if err != nil {
			return err
		}
		d.data, d.loaded = data, true
	}
	dec := &decoder{data: d.data, pos: d.pos, useNumber: d.useNumber}
	dec.skipSpace()
	if dec.pos >= len(dec.data) {
		d.pos = dec.pos
		return io.EOF
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errx.Detail(ErrTypeMismatch, "jsonx: Decode target must be a non-nil pointer | jsonx：Decode 目标必须是非 nil 指针")
	}
	if err := dec.value(rv.Elem()); err != nil {
		return err
	}
	d.pos = dec.pos
	return nil
}

func isValidNumber(s string) bool {
	if s == "" {
		return false
	}
	d := &decoder{data: []byte(s)}
	if _, err := d.parseNumber(); err != nil {
		return false
	}
	return d.pos == len(s)
}

func indentJSON(out *bytes.Buffer, src []byte, prefix, indent string) error {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inString {
			out.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '"':
			inString = true
			out.WriteByte(c)
		case '{', '[':
			out.WriteByte(c)
			depth++
			if i+1 < len(src) && (src[i+1] == '}' || src[i+1] == ']') {
				continue
			}
			out.WriteByte('\n')
			out.WriteString(prefix)
			for j := 0; j < depth; j++ {
				out.WriteString(indent)
			}
		case '}', ']':
			depth--
			out.WriteByte('\n')
			out.WriteString(prefix)
			for j := 0; j < depth; j++ {
				out.WriteString(indent)
			}
			out.WriteByte(c)
		case ',':
			out.WriteByte(',')
			out.WriteByte('\n')
			out.WriteString(prefix)
			for j := 0; j < depth; j++ {
				out.WriteString(indent)
			}
		case ':':
			out.WriteString(": ")
		default:
			out.WriteByte(c)
		}
	}
	return nil
}

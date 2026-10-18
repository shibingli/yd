//go:build amd64 && !jsonx_std

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
	"github.com/shibingli/yd/core/sysinfo"
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

// encoderPoolMax bounds the buffer retained by one pooled encoder. It scales
// with the process memory ceiling and GOMAXPROCS instead of assuming a desktop
// host: a small container does not keep one oversized encoder per P. | encoderPoolMax
// 限制单个池化编码器保留的缓冲；它按进程内存上限与 GOMAXPROCS 缩放，而不是假定
// 桌面主机，避免小容器在每个 P 上各保留一份超大缓冲。
var encoderPoolMax = computeEncoderPoolMax()

func computeEncoderPoolMax() int {
	const def = 1 << 20
	limit := sysinfo.MemoryLimit()
	if limit == 0 {
		return def
	}
	per := limit / uint64(sysinfo.Parallelism()) / 1024
	if per < 64<<10 {
		per = 64 << 10
	}
	if per > def {
		per = def
	}
	return int(per)
}

// maxDirectHint caps one large Marshal output buffer. The ceiling follows the
// process memory limit so a small cgroup does not allow a 4 MiB hint per call. | maxDirectHint
// 限制单次大载荷 Marshal 的输出缓冲上限；上限随进程内存限额缩放，避免小 cgroup
// 下每次调用仍预留 4 MiB。
var maxDirectHint = computeMaxDirectHint()

func computeMaxDirectHint() int {
	const def = 4 << 20
	limit := sysinfo.MemoryLimit()
	if limit == 0 {
		return def
	}
	n := limit / 1024
	if n < 64<<10 {
		n = 64 << 10
	}
	if n > def {
		n = def
	}
	return int(n)
}

// directAllocThreshold is the estimated size above which Marshal allocates the output directly
// instead of encoding into the pooled buffer and copying out. Below it the copy is cheaper than a
// fresh allocation, so the pooled path wins. | directAllocThreshold 是 Marshal 直接在估算体积上
// 分配输出缓冲、而非先写入池化缓冲再复制的最小估算体积；更小的载荷复制比新分配更便宜。
const directAllocThreshold = 512

func getEncoder(escapeHTML bool) *encoder {
	e := encoderPool.Get().(*encoder)
	e.buf = e.buf[:0]
	e.escapeHTML = escapeHTML
	e.depth = 0
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
	if rv.IsValid() && rv.Kind() == reflect.Slice && rv.Len() >= 8 {
		// For multi-element slices the type/shape estimate is reliable enough to allocate the
		// output directly, so no final copy and no repeated growth are needed. Small payloads use
		// the pooled buffer because the copy is cheaper than a fresh allocation, and maps are
		// excluded because their encoded size depends on content the estimate cannot see. |
		// 多元素切片按类型/形状估算直接分配输出缓冲，省去尾部整块复制与多次扩容；小载荷仍走
		// 池化缓冲（复制比新分配便宜），map 的编码体积取决于估算看不到的内容，因此也走池化路径。
		if est := estimateValueSize(rv); est >= directAllocThreshold {
			if est > maxDirectHint {
				est = maxDirectHint
			}
			est += est/32 + 512
			// Single-core environments benefit from reusing the encoder and its time
			// cache through the pool. With real parallelism the pool's cross-P traffic
			// costs more than the one extra allocation, so each call gets its own
			// encoder. Both paths choose themselves from the current GOMAXPROCS. | 单核
			// 环境经池复用 encoder 与其时间缓存更划算；存在真实并行时，池的跨 P 往返
			// 比多一次分配更贵，因此每次调用使用独立 encoder。两条路径都由当前
			// GOMAXPROCS 自动选择。
			if sysinfo.IsMultiCore() {
				e := &encoder{escapeHTML: true, buf: make([]byte, 0, est)}
				if err := e.value(rv); err != nil {
					return nil, err
				}
				return e.buf, nil
			}
			e := getEncoder(true)
			pooled := e.buf
			e.buf = make([]byte, 0, est)
			if err := e.value(rv); err != nil {
				e.buf = pooled[:0]
				putEncoder(e)
				return nil, err
			}
			out := e.buf
			e.buf = pooled[:0]
			putEncoder(e)
			return out, nil
		}
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

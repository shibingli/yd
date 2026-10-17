//go:build jsonx_native

package jsonx

import (
	"encoding/binary"
	stdjson "encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"
)

func (d *decoder) valueAny() (any, error) {
	d.skipSpace()
	if d.pos >= len(d.data) {
		return nil, syntaxErr("unexpected end of JSON input | JSON 输入意外结束")
	}
	switch d.data[d.pos] {
	case 'n':
		return nil, d.literal("null")
	case 't', 'f':
		return d.parseBool()
	case '"':
		return d.parseString()
	case '{':
		return d.anyObject()
	case '[':
		return d.anyArray()
	default:
		b, err := d.parseNumberBytes()
		if err != nil {
			return nil, err
		}
		if d.useNumber {
			return stdjson.Number(string(b)), nil
		}
		f, err := strconv.ParseFloat(bytesString(b), 64)
		if err != nil {
			return nil, syntaxErr("invalid number %q | 非法数字 %q", bytesString(b), bytesString(b))
		}
		return f, nil
	}
}

func (d *decoder) anyObject() (any, error) {
	d.pos++
	out := make(map[string]any)
	d.skipSpace()
	if d.pos < len(d.data) && d.data[d.pos] == '}' {
		d.pos++
		return out, nil
	}
	for {
		d.skipSpace()
		if d.pos >= len(d.data) || d.data[d.pos] != '"' {
			return nil, syntaxErr("expected object key | 期望对象键")
		}
		key, err := d.parseString()
		if err != nil {
			return nil, err
		}
		d.skipSpace()
		if d.pos >= len(d.data) || d.data[d.pos] != ':' {
			return nil, syntaxErr("expected ':' after object key | 对象键之后期望 ':'")
		}
		d.pos++
		val, err := d.valueAny()
		if err != nil {
			return nil, err
		}
		out[key] = val
		d.skipSpace()
		if d.pos >= len(d.data) {
			return nil, syntaxErr("unexpected end of object | 对象意外结束")
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
		case '}':
			d.pos++
			return out, nil
		default:
			return nil, syntaxErr("expected ',' or '}' in object | 对象中期望 ',' 或 '}'")
		}
	}
}

func (d *decoder) anyArray() (any, error) {
	d.pos++
	out := make([]any, 0)
	d.skipSpace()
	if d.pos < len(d.data) && d.data[d.pos] == ']' {
		d.pos++
		return out, nil
	}
	for {
		val, err := d.valueAny()
		if err != nil {
			return nil, err
		}
		out = append(out, val)
		d.skipSpace()
		if d.pos >= len(d.data) {
			return nil, syntaxErr("unexpected end of array | 数组意外结束")
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
		case ']':
			d.pos++
			return out, nil
		default:
			return nil, syntaxErr("expected ',' or ']' in array | 数组中期望 ',' 或 ']'")
		}
	}
}

func (d *decoder) literal(s string) error {
	if len(d.data)-d.pos < len(s) || string(d.data[d.pos:d.pos+len(s)]) != s {
		return syntaxErr("invalid literal | 非法字面量")
	}
	d.pos += len(s)
	return nil
}

func (d *decoder) parseBool() (bool, error) {
	if d.pos < len(d.data) && d.data[d.pos] == 't' {
		if err := d.literal("true"); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := d.literal("false"); err != nil {
		return false, err
	}
	return false, nil
}

// scanStringBody finds the closing quote of a string body. It returns slow=true when an escape,
// control byte, or missing terminator requires the general parser. | scanStringBody 查找字符串体的
// 结束引号；遇到转义、控制字节或缺少终止符时返回 slow=true，交给通用解析器。
func scanStringBody(data []byte, start int) (end int, slow bool) {
	i := start
	for len(data)-i >= 8 {
		x := binary.LittleEndian.Uint64(unsafe.Slice(unsafe.SliceData(data[i:]), 8))
		if wordHasZero(x^wordQuote) || wordHasZero(x^wordBackslash) || wordHasControl(x) {
			for j := 0; j < 8; j++ {
				c := data[i+j]
				if c == '"' {
					return i + j, false
				}
				if c == '\\' || c < 0x20 {
					return 0, true
				}
			}
		}
		i += 8
	}
	for len(data)-i >= 4 {
		x := binary.LittleEndian.Uint32(unsafe.Slice(unsafe.SliceData(data[i:]), 4))
		if wordHasZero32(x^wordQuote32) || wordHasZero32(x^wordBackslash32) || wordHasControl32(x) {
			for j := 0; j < 4; j++ {
				c := data[i+j]
				if c == '"' {
					return i + j, false
				}
				if c == '\\' || c < 0x20 {
					return 0, true
				}
			}
		}
		i += 4
	}
	for ; i < len(data); i++ {
		c := data[i]
		if c == '"' {
			return i, false
		}
		if c == '\\' || c < 0x20 {
			return 0, true
		}
	}
	return 0, true
}

func (d *decoder) parseString() (string, error) {
	if d.pos >= len(d.data) || d.data[d.pos] != '"' {
		return "", syntaxErr("expected string | 期望字符串")
	}
	start := d.pos + 1
	if end, slow := scanStringBody(d.data, start); !slow {
		d.pos = end + 1
		return d.copyString(d.data[start:end]), nil
	}
	d.pos = start
	var b strings.Builder
	for d.pos < len(d.data) {
		c := d.data[d.pos]
		switch c {
		case '"':
			d.pos++
			return b.String(), nil
		case '\\':
			d.pos++
			if d.pos >= len(d.data) {
				return "", syntaxErr("unfinished escape sequence | 未完成的转义序列")
			}
			esc := d.data[d.pos]
			d.pos++
			switch esc {
			case '"', '\\', '/':
				b.WriteByte(esc)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, err := d.parseUnicodeEscape()
				if err != nil {
					return "", err
				}
				b.WriteRune(r)
			default:
				return "", syntaxErr("invalid escape character %q | 非法转义字符 %q", esc, esc)
			}
		default:
			if c < 0x20 {
				return "", syntaxErr("invalid control character in string | 字符串中包含非法控制字符")
			}
			r, size := utf8.DecodeRune(d.data[d.pos:])
			if r == utf8.RuneError && size == 1 {
				b.WriteRune('�')
				d.pos++
				continue
			}
			b.Write(d.data[d.pos : d.pos+size])
			d.pos += size
		}
	}
	return "", syntaxErr("unexpected end of string | 字符串意外结束")
}

// parseKey parses an object key without copying when it has no escapes. | parseKey 解析对象键；
// 没有转义时不复制。该视图只在解码期间使用，不会存入结果。
func (d *decoder) parseKey() (string, error) {
	if d.pos >= len(d.data) || d.data[d.pos] != '"' {
		return "", syntaxErr("expected string | 期望字符串")
	}
	start := d.pos + 1
	if end, slow := scanStringBody(d.data, start); !slow {
		d.pos = end + 1
		return unsafe.String(unsafe.SliceData(d.data[start:end]), end-start), nil
	}
	return d.parseString()
}

func (d *decoder) parseUnicodeEscape() (rune, error) {
	if d.pos+4 > len(d.data) {
		return 0, syntaxErr("unfinished unicode escape | 未完成的 Unicode 转义")
	}
	v, err := strconv.ParseUint(string(d.data[d.pos:d.pos+4]), 16, 32)
	if err != nil {
		return 0, syntaxErr("invalid unicode escape | 非法 Unicode 转义")
	}
	d.pos += 4
	r := rune(v)
	if r >= 0xD800 && r < 0xDC00 {
		if d.pos+6 > len(d.data) || d.data[d.pos] != '\\' || d.data[d.pos+1] != 'u' {
			return '�', nil
		}
		d.pos += 2
		v2, err := strconv.ParseUint(string(d.data[d.pos:d.pos+4]), 16, 32)
		if err != nil {
			return 0, syntaxErr("invalid unicode escape | 非法 Unicode 转义")
		}
		d.pos += 4
		r2 := rune(v2)
		if r2 < 0xDC00 || r2 > 0xDFFF {
			return '�', nil
		}
		return (r-0xD800)<<10 + (r2 - 0xDC00) + 0x10000, nil
	}
	if r >= 0xDC00 && r <= 0xDFFF {
		return '�', nil
	}
	return r, nil
}

func (d *decoder) parseNumber() (string, error) {
	b, err := d.parseNumberBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseNumberBytes scans a JSON number and returns a view into the input. | parseNumberBytes 扫描
// JSON 数字并返回输入中的视图，不复制。
func (d *decoder) parseNumberBytes() ([]byte, error) {
	start := d.pos
	if d.pos < len(d.data) && d.data[d.pos] == '-' {
		d.pos++
	}
	digits := d.pos
	for d.pos < len(d.data) && d.data[d.pos] >= '0' && d.data[d.pos] <= '9' {
		d.pos++
	}
	if d.pos == digits {
		return nil, syntaxErr("invalid number | 非法数字")
	}
	if d.data[digits] == '0' && d.pos-digits > 1 {
		return nil, syntaxErr("invalid leading zero | 非法前导零")
	}
	if d.pos < len(d.data) && d.data[d.pos] == '.' {
		d.pos++
		frac := d.pos
		for d.pos < len(d.data) && d.data[d.pos] >= '0' && d.data[d.pos] <= '9' {
			d.pos++
		}
		if d.pos == frac {
			return nil, syntaxErr("invalid number fraction | 非法数字小数部分")
		}
	}
	if d.pos < len(d.data) && (d.data[d.pos] == 'e' || d.data[d.pos] == 'E') {
		d.pos++
		if d.pos < len(d.data) && (d.data[d.pos] == '+' || d.data[d.pos] == '-') {
			d.pos++
		}
		exp := d.pos
		for d.pos < len(d.data) && d.data[d.pos] >= '0' && d.data[d.pos] <= '9' {
			d.pos++
		}
		if d.pos == exp {
			return nil, syntaxErr("invalid number exponent | 非法数字指数")
		}
	}
	return d.data[start:d.pos], nil
}

// bytesString returns a read-only string view of b without copying. | bytesString 返回 b 的只读字符串
// 视图，不复制。
func bytesString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(b), len(b))
}

func (d *decoder) captureValue() ([]byte, error) {
	view, err := d.captureValueView()
	if err != nil {
		return nil, err
	}
	raw := make([]byte, len(view))
	copy(raw, view)
	return raw, nil
}

// captureValueView returns the raw value as a view into the input. The caller must not retain it
// beyond the current call. | captureValueView 以输入视图返回原始值；调用方不得在本次调用之后保留它。
func (d *decoder) captureValueView() ([]byte, error) {
	start := d.pos
	if err := d.skipValue(); err != nil {
		return nil, err
	}
	return d.data[start:d.pos], nil
}

func (d *decoder) skipValue() error {
	d.skipSpace()
	if d.pos >= len(d.data) {
		return syntaxErr("unexpected end of JSON input | JSON 输入意外结束")
	}
	switch d.data[d.pos] {
	case 'n':
		return d.literal("null")
	case 't':
		return d.literal("true")
	case 'f':
		return d.literal("false")
	case '"':
		_, err := d.parseString()
		return err
	case '{':
		d.pos++
		d.skipSpace()
		if d.pos < len(d.data) && d.data[d.pos] == '}' {
			d.pos++
			return nil
		}
		for {
			d.skipSpace()
			if _, err := d.parseString(); err != nil {
				return err
			}
			d.skipSpace()
			if d.pos >= len(d.data) || d.data[d.pos] != ':' {
				return syntaxErr("expected ':' | 期望 ':'")
			}
			d.pos++
			if err := d.skipValue(); err != nil {
				return err
			}
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of object | 对象意外结束")
			}
			if d.data[d.pos] == ',' {
				d.pos++
				continue
			}
			if d.data[d.pos] == '}' {
				d.pos++
				return nil
			}
			return syntaxErr("expected ',' or '}' | 期望 ',' 或 '}'")
		}
	case '[':
		d.pos++
		d.skipSpace()
		if d.pos < len(d.data) && d.data[d.pos] == ']' {
			d.pos++
			return nil
		}
		for {
			if err := d.skipValue(); err != nil {
				return err
			}
			d.skipSpace()
			if d.pos >= len(d.data) {
				return syntaxErr("unexpected end of array | 数组意外结束")
			}
			if d.data[d.pos] == ',' {
				d.pos++
				continue
			}
			if d.data[d.pos] == ']' {
				d.pos++
				return nil
			}
			return syntaxErr("expected ',' or ']' | 期望 ',' 或 ']'")
		}
	default:
		_, err := d.parseNumber()
		return err
	}
}

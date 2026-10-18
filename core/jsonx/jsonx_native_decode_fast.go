//go:build jsonx_native

package jsonx

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

type fastFieldDec func(*decoder, unsafe.Pointer) error

// buildFastFieldDecoder returns a direct-memory decoder for common field types. | buildFastFieldDecoder
// 为常见字段类型返回直接写内存的解码器。
func buildFastFieldDecoder(t reflect.Type) fastFieldDec {
	// json.Number needs its own validation, and every other type with a custom unmarshal method
	// must go through the generic decoder so the method is called. time.Time and []string keep
	// their dedicated fast decoders. | json.Number 需要自身的校验；其余带自定义解码方法的类型必须
	// 走通用解码器以调用该方法；time.Time 与 []string 保留专用快速解码器。
	if t == numberType {
		return nil
	}
	if t != timeType && t != stringSliceType && hasUnmarshalMethod(t) {
		return nil
	}
	switch t.Kind() {
	case reflect.String:
		return func(d *decoder, p unsafe.Pointer) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			if d.data[d.pos] != '"' {
				return typeErr("cannot unmarshal non-string into %s | 无法把非字符串解析进 %s", t, t)
			}
			s, err := d.parseString()
			if err != nil {
				return err
			}
			*(*string)(p) = s
			return nil
		}
	case reflect.Bool:
		return func(d *decoder, p unsafe.Pointer) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			x, err := d.parseBool()
			if err != nil {
				return err
			}
			*(*bool)(p) = x
			return nil
		}
	case reflect.Int:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int)(p) = int(n)
			return nil
		}
	case reflect.Int8:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int8)(p) = int8(n)
			return nil
		}
	case reflect.Int16:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int16)(p) = int16(n)
			return nil
		}
	case reflect.Int32:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int32)(p) = int32(n)
			return nil
		}
	case reflect.Int64:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseIntField(t)
			if err != nil {
				return err
			}
			*(*int64)(p) = n
			return nil
		}
	case reflect.Uint:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint)(p) = uint(n)
			return nil
		}
	case reflect.Uint8:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint8)(p) = uint8(n)
			return nil
		}
	case reflect.Uint16:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint16)(p) = uint16(n)
			return nil
		}
	case reflect.Uint32:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint32)(p) = uint32(n)
			return nil
		}
	case reflect.Uint64:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uint64)(p) = n
			return nil
		}
	case reflect.Uintptr:
		return func(d *decoder, p unsafe.Pointer) error {
			n, err := d.parseUintField(t)
			if err != nil {
				return err
			}
			*(*uintptr)(p) = uintptr(n)
			return nil
		}
	case reflect.Float32:
		return func(d *decoder, p unsafe.Pointer) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			b, err := d.parseNumberBytes()
			if err != nil {
				return err
			}
			if f, ok := parseExactIntegerFloat(b); ok {
				*(*float32)(p) = float32(f)
				return nil
			}
			n, err := strconv.ParseFloat(bytesString(b), 32)
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", bytesString(b), t, bytesString(b), t)
			}
			*(*float32)(p) = float32(n)
			return nil
		}
	case reflect.Float64:
		return func(d *decoder, p unsafe.Pointer) error {
			if d.data[d.pos] == 'n' {
				return d.literal("null")
			}
			b, err := d.parseNumberBytes()
			if err != nil {
				return err
			}
			if f, ok := parseExactIntegerFloat(b); ok {
				*(*float64)(p) = f
				return nil
			}
			n, err := strconv.ParseFloat(bytesString(b), 64)
			if err != nil {
				return typeErr("number %s overflows %s | 数字 %s 超出 %s", bytesString(b), t, bytesString(b), t)
			}
			*(*float64)(p) = n
			return nil
		}
	}
	if t == timeType {
		return func(d *decoder, p unsafe.Pointer) error {
			var raw []byte
			if d.data[d.pos] == '"' {
				if end, slow, _ := scanStringBody(d.data, d.pos+1); !slow {
					raw = d.data[d.pos : end+1]
					d.pos = end + 1
				}
			}
			if raw == nil {
				var err error
				raw, err = d.captureValueView()
				if err != nil {
					return err
				}
			}
			if len(raw) == 4 && raw[0] == 'n' {
				return (*time.Time)(p).UnmarshalJSON(raw)
			}
			if int(d.timeLen) == len(raw) && len(raw) <= len(d.timeRaw) && bytes.Equal(d.timeRaw[:len(raw)], raw) {
				*(*time.Time)(p) = d.timeVal
				return nil
			}
			if len(raw) == 22 && raw[0] == '"' && raw[21] == '"' {
				if tt, ok := parseRFC3339Z(raw[1:21]); ok {
					copy(d.timeRaw[:], raw)
					d.timeLen = uint8(len(raw))
					d.timeVal = tt
					*(*time.Time)(p) = tt
					return nil
				}
			}
			tt := (*time.Time)(p)
			if err := tt.UnmarshalJSON(raw); err != nil {
				return err
			}
			if len(raw) <= len(d.timeRaw) {
				copy(d.timeRaw[:], raw)
				d.timeLen = uint8(len(raw))
				d.timeVal = *tt
			}
			return nil
		}
	}
	if t == stringSliceType {
		return fastDecodeStringSlice
	}
	return nil
}

// parseRFC3339Z parses the strict 20-byte "YYYY-MM-DDTHH:MM:SSZ" form. Anything else (fractions,
// numeric offsets, lowercase separators, invalid ranges) is left to time.Time.UnmarshalJSON, which
// keeps the accepted grammar and error behavior identical to encoding/json. | parseRFC3339Z 解析
// 严格的 20 字节 "YYYY-MM-DDTHH:MM:SSZ" 形式；其余情况（小数、数字时区、小写分隔符、越界值）
// 一律交给 time.Time.UnmarshalJSON，从而与 encoding/json 保持相同的接受范围与错误行为。
func parseRFC3339Z(b []byte) (time.Time, bool) {
	if len(b) != 20 || b[4] != '-' || b[7] != '-' || b[10] != 'T' || b[13] != ':' || b[16] != ':' || b[19] != 'Z' {
		return time.Time{}, false
	}
	yearHi, ok := parseDec2(b[0:2])
	yearLo, ok0 := parseDec2(b[2:4])
	if !ok || !ok0 {
		return time.Time{}, false
	}
	year := yearHi*100 + yearLo
	month, ok1 := parseDec2(b[5:7])
	day, ok2 := parseDec2(b[8:10])
	hour, ok3 := parseDec2(b[11:13])
	minute, ok4 := parseDec2(b[14:16])
	second, ok5 := parseDec2(b[17:19])
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || month < 1 || month > 12 || day < 1 ||
		hour > 23 || minute > 59 || second > 59 || day > daysInMonth(year, month) {
		return time.Time{}, false
	}
	secs := daysFromCivil(year, month, day)*86400 + int64(hour)*3600 + int64(minute)*60 + int64(second)
	return time.Unix(secs, 0).UTC(), true
}

// parseDec2 decodes two ASCII decimal digits. | parseDec2 解码两位 ASCII 十进制数字。
func parseDec2(b []byte) (int, bool) {
	d0 := b[0] - '0'
	d1 := b[1] - '0'
	if d0 > 9 || d1 > 9 {
		return 0, false
	}
	return int(d0)*10 + int(d1), true
}

// daysInMonth returns the number of days in y-m. | daysInMonth 返回 y 年 m 月的天数。
func daysInMonth(y, m int) int {
	switch m {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	}
	return 31
}

// daysFromCivil converts a civil date to days since 1970-01-01. | daysFromCivil 把公历日期转换为
// 距 1970-01-01 的天数。
func daysFromCivil(y, m, d int) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 {
		era = (y - 399) / 400
	}
	yoe := y - era*400
	mp := m + 9
	if m > 2 {
		mp = m - 3
	}
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return int64(era*146097+doe) - 719468
}

// parseExactIntegerFloat converts a plain integer literal of at most 15 digits to float64.
// Such integers are exactly representable, so the result matches strconv.ParseFloat while skipping
// its general decimal machinery. | parseExactIntegerFloat 把最多 15 位的纯整数字面量转换为
// float64；这类整数可被精确表示，因此结果与 strconv.ParseFloat 一致，但跳过其通用十进制处理。
func parseExactIntegerFloat(b []byte) (float64, bool) {
	if len(b) == 0 || len(b) > 16 {
		return 0, false
	}
	neg := false
	if b[0] == '-' {
		neg = true
		b = b[1:]
	}
	if len(b) == 0 || len(b) > 15 {
		return 0, false
	}
	var n uint64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	f := float64(n)
	if neg {
		f = -f
	}
	return f, true
}

func (d *decoder) parseIntField(t reflect.Type) (int64, error) {
	if d.data[d.pos] == 'n' {
		return 0, d.literal("null")
	}
	if d.data[d.pos] == '"' {
		return 0, typeErr("cannot unmarshal string into %s | 无法把字符串解析进 %s", t, t)
	}
	b, err := d.parseNumberBytes()
	if err != nil {
		return 0, err
	}
	if len(b) > 0 && b[0] != '-' {
		bits := t.Bits()
		max := uint64(1)<<63 - 1
		if bits < 64 {
			max = uint64(1)<<(bits-1) - 1
		}
		var n uint64
		ok := true
		for _, c := range b {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			digit := uint64(c - '0')
			if n > (max-digit)/10 {
				ok = false
				break
			}
			n = n*10 + digit
		}
		if ok {
			return int64(n), nil
		}
	}
	s := bytesString(b)
	if strings.ContainsAny(s, ".eE") {
		return 0, typeErr("cannot unmarshal number %s into %s | 无法把数字 %s 解析进 %s", s, t, s, t)
	}
	n, err := strconv.ParseInt(s, 10, t.Bits())
	if err != nil {
		return 0, typeErr("number %s overflows %s | 数字 %s 超出 %s", s, t, s, t)
	}
	return n, nil
}

func (d *decoder) parseUintField(t reflect.Type) (uint64, error) {
	if d.data[d.pos] == 'n' {
		return 0, d.literal("null")
	}
	if d.data[d.pos] == '"' {
		return 0, typeErr("cannot unmarshal string into %s | 无法把字符串解析进 %s", t, t)
	}
	b, err := d.parseNumberBytes()
	if err != nil {
		return 0, err
	}
	bits := t.Bits()
	max := ^uint64(0)
	if bits < 64 {
		max = uint64(1)<<bits - 1
	}
	var n uint64
	ok := true
	for _, c := range b {
		if c < '0' || c > '9' {
			ok = false
			break
		}
		digit := uint64(c - '0')
		if n > (max-digit)/10 {
			ok = false
			break
		}
		n = n*10 + digit
	}
	if ok {
		return n, nil
	}
	s := bytesString(b)
	if strings.ContainsAny(s, ".eE-") {
		return 0, typeErr("cannot unmarshal number %s into %s | 无法把数字 %s 解析进 %s", s, t, s, t)
	}
	parsed, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, typeErr("number %s overflows %s | 数字 %s 超出 %s", s, t, s, t)
	}
	return parsed, nil
}

func fastDecodeStringSlice(d *decoder, p unsafe.Pointer) error {
	sp := (*[]string)(p)
	if d.data[d.pos] == 'n' {
		if err := d.literal("null"); err != nil {
			return err
		}
		*sp = nil
		return nil
	}
	if d.data[d.pos] != '[' {
		return typeErr("cannot unmarshal non-array into []string | 无法把非数组解析进 []string")
	}
	d.pos++
	d.skipSpace()
	if d.pos < len(d.data) && d.data[d.pos] == ']' {
		d.pos++
		*sp = []string{}
		return nil
	}
	*sp = make([]string, 0, 4)
	i := 0
	for {
		if i >= len(*sp) {
			*sp = append(*sp, "")
		}
		if d.pos >= len(d.data) {
			return syntaxErr("unexpected end of array | 数组意外结束")
		}
		switch d.data[d.pos] {
		case '"':
			s, err := d.parseString()
			if err != nil {
				return err
			}
			(*sp)[i] = s
		case 'n':
			if err := d.literal("null"); err != nil {
				return err
			}
			(*sp)[i] = ""
		default:
			return typeErr("cannot unmarshal non-string into string | 无法把非字符串解析进字符串")
		}
		i++
		if d.pos < len(d.data) {
			switch d.data[d.pos] {
			case ',':
				d.pos++
				d.skipSpace()
				continue
			case ']':
				d.pos++
				if i < len(*sp) {
					*sp = (*sp)[:i]
				}
				return nil
			}
		}
		d.skipSpace()
		if d.pos >= len(d.data) {
			return syntaxErr("unexpected end of array | 数组意外结束")
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
			d.skipSpace()
		case ']':
			d.pos++
			if i < len(*sp) {
				*sp = (*sp)[:i]
			}
			return nil
		default:
			return syntaxErr("expected ',' or ']' in array | 数组中期望 ',' 或 ']'")
		}
	}
}

// This file defines the opaque keyset cursor. | 本文件定义不可变的键集游标。

package paging

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shibingli/ydx/base64"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/json"
)

// Kind is the value type stored in one cursor field. | Kind 是游标字段中保存的取值类型。
type Kind string

// The supported cursor value kinds. Every one has a deterministic text form, so a cursor survives a
// URL round trip without losing its database type. | 支持的游标取值类型。每种都有确定性的文本形式，
// 因此游标经 URL 往返不会丢失数据库类型。
const (
	// KindString is a string value. | KindString 是字符串取值。
	KindString Kind = "s"
	// KindInt is a signed integer. | KindInt 是有符号整数。
	KindInt Kind = "i"
	// KindUint is an unsigned integer. | KindUint 是无符号整数。
	KindUint Kind = "u"
	// KindTime is a UTC timestamp. | KindTime 是 UTC 时间戳。
	KindTime Kind = "t"
)

// maxCursorBytes bounds the encoded cursor before decoding, so a crafted URL cannot make the decoder
// allocate unbounded memory. | maxCursorBytes 限制解码前的游标长度，使构造的 URL 无法让解码器无限分配内存。
const maxCursorBytes = 512

// Cursor is the position of one row in a keyset order. | Cursor 是一行在键集排序中的位置。
//
// Value is the sort-column value and TieBreaker is the unique column value; both carry their own kind
// so the database receives the original type rather than a string that one dialect would coerce and
// another would reject. | Value 是排序列取值，TieBreaker 是唯一列取值；两者各自携带类型，使数据库收到
// 原始类型，而不是一个在某种方言里被隐式转换、在另一种方言里被拒绝的字符串。
type Cursor struct {
	// Kind is the sort value kind. | Kind 是排序值的类型。
	Kind Kind `json:"k"`
	// Value is the sort value in its canonical text form. | Value 是排序值的规范文本形式。
	Value string `json:"v"`
	// IDKind is the tie-breaker kind. | IDKind 是决胜列的类型。
	IDKind Kind `json:"ik"`
	// IDValue is the tie-breaker value in its canonical text form. | IDValue 是决胜列的规范文本形式。
	IDValue string `json:"iv"`
}

// NewCursor builds a cursor from the sort value and the unique tie-breaker. | NewCursor 依据排序值与唯一
// 决胜值构造游标。
func NewCursor(value, tieBreaker any) (Cursor, error) {
	kind, text, err := encodeValue(value)
	if err != nil {
		return Cursor{}, err
	}
	idKind, idText, err := encodeValue(tieBreaker)
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{Kind: kind, Value: text, IDKind: idKind, IDValue: idText}, nil
}

// encodeValue converts one Go value into its kind and canonical text form. | encodeValue 把一个 Go 取值
// 转换为其类型与规范文本形式。
func encodeValue(value any) (Kind, string, error) {
	switch v := value.(type) {
	case string:
		return KindString, v, nil
	case int:
		return KindInt, strconv.FormatInt(int64(v), 10), nil
	case int8:
		return KindInt, strconv.FormatInt(int64(v), 10), nil
	case int16:
		return KindInt, strconv.FormatInt(int64(v), 10), nil
	case int32:
		return KindInt, strconv.FormatInt(int64(v), 10), nil
	case int64:
		return KindInt, strconv.FormatInt(v, 10), nil
	case uint:
		return KindUint, strconv.FormatUint(uint64(v), 10), nil
	case uint8:
		return KindUint, strconv.FormatUint(uint64(v), 10), nil
	case uint16:
		return KindUint, strconv.FormatUint(uint64(v), 10), nil
	case uint32:
		return KindUint, strconv.FormatUint(uint64(v), 10), nil
	case uint64:
		return KindUint, strconv.FormatUint(v, 10), nil
	case time.Time:
		return KindTime, v.UTC().Format(time.RFC3339Nano), nil
	default:
		return "", "", errs.Detail(ErrCursor, "unsupported cursor value %T | 不支持的游标取值类型 %T", value, value)
	}
}

// SortValue decodes the sort value into the Go type the database column expects. | SortValue 把排序值解码为
// 数据库列期望的 Go 类型。
func (c Cursor) SortValue() (any, error) { return decodeValue(c.Kind, c.Value) }

// TieBreaker decodes the unique tie-breaker value. | TieBreaker 解码唯一决胜值。
func (c Cursor) TieBreaker() (any, error) { return decodeValue(c.IDKind, c.IDValue) }

// decodeValue converts a canonical text form back into its Go type. | decodeValue 把规范文本形式还原为
// 对应的 Go 类型。
func decodeValue(kind Kind, text string) (any, error) {
	switch kind {
	case KindString:
		return text, nil
	case KindInt:
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, errs.WrapDetail(err, ErrCursor, "integer %q | 整数 %q", text, text)
		}
		return value, nil
	case KindUint:
		value, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return nil, errs.WrapDetail(err, ErrCursor, "unsigned integer %q | 无符号整数 %q", text, text)
		}
		return value, nil
	case KindTime:
		value, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return nil, errs.WrapDetail(err, ErrCursor, "timestamp %q | 时间戳 %q", text, text)
		}
		return value.UTC(), nil
	default:
		return nil, errs.Detail(ErrCursor, "unsupported cursor kind %q | 不支持的游标类型 %q", kind, kind)
	}
}

// Encode returns the opaque URL-safe form of the cursor. | Encode 返回游标的不可变 URL 安全形式。
func (c Cursor) Encode() (string, error) {
	if _, err := c.SortValue(); err != nil {
		return "", err
	}
	if _, err := c.TieBreaker(); err != nil {
		return "", err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", errs.Wrap(err, ErrCursor)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// Decode parses an opaque cursor produced by Encode. | Decode 解析 Encode 产出的不可变游标。
func Decode(raw string) (Cursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Cursor{}, errs.Detail(ErrCursor, "empty cursor | 游标为空")
	}
	if len(raw) > maxCursorBytes {
		return Cursor{}, errs.Detail(ErrCursor, "cursor too long | 游标过长")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, errs.Wrap(err, ErrCursor)
	}
	var cursor Cursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return Cursor{}, errs.Wrap(err, ErrCursor)
	}
	if cursor.Kind == "" || cursor.IDKind == "" {
		return Cursor{}, errs.Detail(ErrCursor, "cursor lacks a kind | 游标缺少类型")
	}
	if _, err := cursor.SortValue(); err != nil {
		return Cursor{}, err
	}
	if _, err := cursor.TieBreaker(); err != nil {
		return Cursor{}, err
	}
	return cursor, nil
}

// String renders the cursor for debug logs without exposing the opaque payload. | String 为调试日志渲染游标，
// 不暴露完整载荷。
func (c Cursor) String() string {
	return fmt.Sprintf("Cursor{%s:%s,%s:%s}", c.Kind, c.Value, c.IDKind, c.IDValue)
}

package openapi

import (
	"bytes"
	"sort"
	"strings"

	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/json"
)

// ErrInvalidExtension reports an extension member that cannot be emitted or decoded. | ErrInvalidExtension
// 表示无法输出或解码的扩展成员。
var ErrInvalidExtension = errs.New("openapi: invalid specification extension | openapi：非法的规范扩展")

// marshalWithExtensions serializes base and appends its specification extensions. | marshalWithExtensions
// 序列化 base 并附加其规范扩展。
func marshalWithExtensions(base any, extensions map[string]any) ([]byte, error) {
	raw, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	return mergeExtensions(raw, extensions)
}

// mergeExtensions appends the x-* members of extensions to a serialized JSON object. | mergeExtensions
// 把 extensions 中的 x-* 成员追加到已序列化的 JSON 对象上。
//
// The base bytes are spliced rather than decoded and re-encoded, so the numeric precision and
// the field order of the object are preserved exactly. | 这里直接拼接原始字节而不是解码再编码，
// 因此对象的数值精度与字段顺序完全保持不变。
func mergeExtensions(raw []byte, extensions map[string]any) ([]byte, error) {
	if len(extensions) == 0 {
		return raw, nil
	}
	keys := make([]string, 0, len(extensions))
	for key := range extensions {
		if !strings.HasPrefix(key, "x-") {
			return nil, errs.Detail(ErrInvalidExtension,
				"extension name %q must start with x- | 扩展名 %q 必须以 x- 开头", key, key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	body := bytes.TrimSpace(raw)
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return nil, errs.Detail(ErrInvalidExtension, "extension target is not a JSON object | 扩展目标不是 JSON 对象")
	}
	inner := bytes.TrimSpace(body[1 : len(body)-1])

	var buf bytes.Buffer
	buf.Grow(len(body) + 24*len(keys))
	buf.WriteByte('{')
	wrote := len(inner) > 0
	if wrote {
		buf.Write(inner)
	}
	for _, key := range keys {
		if wrote {
			buf.WriteByte(',')
		}
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, errs.Wrap(err, ErrInvalidExtension)
		}
		valueJSON, err := json.Marshal(extensions[key])
		if err != nil {
			return nil, errs.WrapDetail(err, ErrInvalidExtension, "extension %q | 扩展 %q", key, key)
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		buf.Write(valueJSON)
		wrote = true
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// extractExtensions decodes the x-* members of a serialized JSON object. | extractExtensions 解码
// 已序列化 JSON 对象中的 x-* 成员。
func extractExtensions(data []byte) (map[string]any, error) {
	if !bytes.Contains(data, []byte(`"x-`)) {
		return nil, nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, errs.Wrap(err, ErrDecode)
	}
	var out map[string]any
	for key, value := range raw {
		if strings.HasPrefix(key, "x-") {
			if out == nil {
				out = map[string]any{}
			}
			out[key] = value
		}
	}
	return out, nil
}

// validateExtensions checks that every extension name carries the required x- prefix. | validateExtensions
// 检查每个扩展名都带必需的 x- 前缀。
func validateExtensions(extensions map[string]any, where string, add func(string, ...any)) {
	for key := range extensions {
		if !strings.HasPrefix(key, "x-") {
			add("%s has extension %q without the x- prefix | %s 的扩展 %q 缺少 x- 前缀", where, key, where, key)
		}
	}
}

// The MarshalJSON and UnmarshalJSON methods below keep specification extensions at the object
// level. Every document type with a struct representation is covered; Paths, Responses and
// Callback stay plain maps, so their extension members cannot be modelled without changing
// their public Go type. | 下面的 MarshalJSON/UnmarshalJSON 把规范扩展保留在对象层级。所有以结构体
// 表示的文档类型都已覆盖；Paths、Responses 与 Callback 仍是普通映射，要建模其扩展成员就必须改变
// 公开的 Go 类型，因此不在支持范围内。

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Document) MarshalJSON() ([]byte, error) {
	type alias Document
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Document) UnmarshalJSON(data []byte) error {
	type alias Document
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Contact) MarshalJSON() ([]byte, error) {
	type alias Contact
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Contact) UnmarshalJSON(data []byte) error {
	type alias Contact
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v License) MarshalJSON() ([]byte, error) {
	type alias License
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *License) UnmarshalJSON(data []byte) error {
	type alias License
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Server) MarshalJSON() ([]byte, error) {
	type alias Server
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Server) UnmarshalJSON(data []byte) error {
	type alias Server
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v ServerVariable) MarshalJSON() ([]byte, error) {
	type alias ServerVariable
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *ServerVariable) UnmarshalJSON(data []byte) error {
	type alias ServerVariable
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Tag) MarshalJSON() ([]byte, error) {
	type alias Tag
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Tag) UnmarshalJSON(data []byte) error {
	type alias Tag
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v ExternalDocs) MarshalJSON() ([]byte, error) {
	type alias ExternalDocs
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *ExternalDocs) UnmarshalJSON(data []byte) error {
	type alias ExternalDocs
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v SecurityScheme) MarshalJSON() ([]byte, error) {
	type alias SecurityScheme
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *SecurityScheme) UnmarshalJSON(data []byte) error {
	type alias SecurityScheme
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v OAuthFlows) MarshalJSON() ([]byte, error) {
	type alias OAuthFlows
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *OAuthFlows) UnmarshalJSON(data []byte) error {
	type alias OAuthFlows
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v OAuthFlow) MarshalJSON() ([]byte, error) {
	type alias OAuthFlow
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *OAuthFlow) UnmarshalJSON(data []byte) error {
	type alias OAuthFlow
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v PathItem) MarshalJSON() ([]byte, error) {
	type alias PathItem
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *PathItem) UnmarshalJSON(data []byte) error {
	type alias PathItem
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Operation) MarshalJSON() ([]byte, error) {
	type alias Operation
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Operation) UnmarshalJSON(data []byte) error {
	type alias Operation
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Example) MarshalJSON() ([]byte, error) {
	type alias Example
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Example) UnmarshalJSON(data []byte) error {
	type alias Example
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Parameter) MarshalJSON() ([]byte, error) {
	type alias Parameter
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Parameter) UnmarshalJSON(data []byte) error {
	type alias Parameter
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Header) MarshalJSON() ([]byte, error) {
	type alias Header
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Header) UnmarshalJSON(data []byte) error {
	type alias Header
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Response) MarshalJSON() ([]byte, error) {
	type alias Response
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Response) UnmarshalJSON(data []byte) error {
	type alias Response
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v MediaType) MarshalJSON() ([]byte, error) {
	type alias MediaType
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *MediaType) UnmarshalJSON(data []byte) error {
	type alias MediaType
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Encoding) MarshalJSON() ([]byte, error) {
	type alias Encoding
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Encoding) UnmarshalJSON(data []byte) error {
	type alias Encoding
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Link) MarshalJSON() ([]byte, error) {
	type alias Link
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Link) UnmarshalJSON(data []byte) error {
	type alias Link
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v RequestBody) MarshalJSON() ([]byte, error) {
	type alias RequestBody
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *RequestBody) UnmarshalJSON(data []byte) error {
	type alias RequestBody
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Components) MarshalJSON() ([]byte, error) {
	type alias Components
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Components) UnmarshalJSON(data []byte) error {
	type alias Components
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v Discriminator) MarshalJSON() ([]byte, error) {
	type alias Discriminator
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *Discriminator) UnmarshalJSON(data []byte) error {
	type alias Discriminator
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// MarshalJSON implements json.Marshaler. | MarshalJSON 实现 json.Marshaler。
func (v XML) MarshalJSON() ([]byte, error) {
	type alias XML
	return marshalWithExtensions((*alias)(&v), v.Extensions)
}

// UnmarshalJSON implements json.Unmarshaler. | UnmarshalJSON 实现 json.Unmarshaler。
func (v *XML) UnmarshalJSON(data []byte) error {
	type alias XML
	if err := json.Unmarshal(data, (*alias)(v)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	v.Extensions = ext
	return nil
}

// UnmarshalJSON implements json.Unmarshaler for Info. | UnmarshalJSON 实现 Info 的 json.Unmarshaler。
func (i *Info) UnmarshalJSON(data []byte) error {
	type alias Info
	if err := json.Unmarshal(data, (*alias)(i)); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	i.Extensions = ext
	return nil
}

// UnmarshalJSON implements json.Unmarshaler for Schema, accepting both the 3.0 string form and
// the 3.1 type array and numeric exclusive bounds. | UnmarshalJSON 实现 Schema 的 json.Unmarshaler，
// 同时接受 3.0 的字符串类型与 3.1 的类型数组、数值开界。
func (s *Schema) UnmarshalJSON(data []byte) error {
	type alias Schema
	aux := struct {
		*alias
		Type             any `json:"type,omitempty"`
		ExclusiveMinimum any `json:"exclusiveMinimum,omitempty"`
		ExclusiveMaximum any `json:"exclusiveMaximum,omitempty"`
	}{alias: (*alias)(s)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	s.Type = ""
	s.Types = nil
	switch value := aux.Type.(type) {
	case nil:
	case string:
		s.Type = value
	case []any:
		types := make([]string, 0, len(value))
		for _, item := range value {
			name, ok := item.(string)
			if !ok {
				return errs.Detail(ErrDecode, "schema type array must contain strings | Schema 类型数组只能包含字符串")
			}
			types = append(types, name)
		}
		s.Types = types
	default:
		return errs.Detail(ErrDecode, "schema type must be a string or an array | Schema 类型必须是字符串或数组")
	}
	var err error
	if s.ExclusiveMinimum, s.ExclusiveMinimumValue, err = decodeExclusiveBound(aux.ExclusiveMinimum); err != nil {
		return err
	}
	if s.ExclusiveMaximum, s.ExclusiveMaximumValue, err = decodeExclusiveBound(aux.ExclusiveMaximum); err != nil {
		return err
	}
	ext, err := extractExtensions(data)
	if err != nil {
		return err
	}
	s.Extensions = ext
	return nil
}

// decodeExclusiveBound decodes a 3.0 boolean or a 3.1 numeric exclusive bound. | decodeExclusiveBound
// 解码 3.0 的布尔开界或 3.1 的数值开界。
func decodeExclusiveBound(value any) (bool, *float64, error) {
	switch v := value.(type) {
	case nil:
		return false, nil, nil
	case bool:
		return v, nil, nil
	case float64:
		return false, &v, nil
	default:
		return false, nil, errs.Detail(ErrDecode, "exclusive bound must be a boolean or a number | 开界必须是布尔值或数字")
	}
}

package router

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
)

// Binding errors, declared once at package level. | 绑定错误，在包级声明一次。
var (
	// ErrUnsupportedMediaType indicates the request body cannot be bound according to the declared content type. | ErrUnsupportedMediaType 表示请求体无法按声明的内容类型绑定。
	ErrUnsupportedMediaType = errx.New("router: unsupported request content type | router：不支持的请求内容类型")
	// ErrBadBindTarget indicates the Bind target is nil, is not a pointer, or is neither a struct nor a map. | ErrBadBindTarget 表示 Bind 目标为 nil、不是指针，或既不是结构体也不是 map。
	ErrBadBindTarget = errx.New("router: invalid bind target | router：非法的绑定目标")
	// ErrBodyTooLarge indicates the request body exceeds the configured size limit. | ErrBodyTooLarge 表示请求体超出配置的大小限制。
	ErrBodyTooLarge = errx.New("router: request body too large | router：请求体超出大小限制")
	// ErrReadBody indicates the request body could not be read. | ErrReadBody 表示无法读取请求体。
	ErrReadBody = errx.New("router: failed to read request body | router：读取请求体失败")
	// ErrDecodeJSON indicates the JSON request body could not be decoded. | ErrDecodeJSON 表示 JSON 请求体无法解码。
	ErrDecodeJSON = errx.New("router: failed to decode JSON body | router：JSON 解码失败")
	// ErrParseForm indicates the request form could not be parsed. | ErrParseForm 表示无法解析请求表单。
	ErrParseForm = errx.New("router: failed to parse form | router：解析表单失败")
	// ErrBindField indicates a struct field could not be bound from its textual value. | ErrBindField 表示结构体字段无法从文本值绑定。
	ErrBindField = errx.New("router: failed to bind field | router：字段绑定失败")
	// ErrUnsupportedFieldType indicates the field type has no binding rule. | ErrUnsupportedFieldType 表示该字段类型没有对应的绑定规则。
	ErrUnsupportedFieldType = errx.New("router: unsupported field type | router：不支持的字段类型")
	// ErrBadTimeFormat indicates the time value matches none of the supported layouts. | ErrBadTimeFormat 表示时间值不匹配任何受支持的布局。
	ErrBadTimeFormat = errx.New("router: invalid time format | router：时间格式非法")
)

// Bind binds request parameters to v according to Content-Type. | Bind 依据 Content-Type 把请求参数绑定到 v。
//
//   - application/json → the framework's adaptive JSON layer (jsonx) decodes the request body; |   - application/json → 使用框架自适应 JSON 层（jsonx）解码请求体；
//   - application/x-www-form-urlencoded or multipart/form-data → form fields; |   - application/x-www-form-urlencoded 或 multipart/form-data → 表单字段；
//   - anything else → try the query string and then the form, in that order. |   - 其他 → 依次尝试 query 与表单。
//
// Struct fields are matched by their `json`, `form`, `query` tags, then by an identically named field ignoring case. | 结构体字段按 `json`、`form`、`query` 标签匹配，其次按大小写不敏感的同名字段匹配。
// Binding uses reflection, so it is only for request entry points; hot paths such as serialization and logging do not rely on it. | 绑定使用反射，因此仅用于请求入口；序列化与日志等热路径不依赖本函数。
func (c *Ctx) Bind(v any) error {
	if v == nil {
		return errx.Detail(ErrBadBindTarget, "target must not be nil | 目标不得为空")
	}
	ct := c.Request.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case ct == "application/json" || strings.HasSuffix(ct, "+json"):
		c.limitBody()
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, c.router.maxBodySize()+1))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return ErrBodyTooLarge
			}
			return errx.Wrap(err, ErrReadBody)
		}
		if int64(len(body)) > c.router.maxBodySize() {
			return ErrBodyTooLarge
		}
		if len(body) == 0 {
			return nil
		}
		if err := jsonx.Unmarshal(body, v); err != nil {
			return errx.Wrap(err, ErrDecodeJSON)
		}
		return nil
	case ct == "application/x-www-form-urlencoded" || ct == "multipart/form-data" || strings.HasPrefix(ct, "multipart/form-data"):
		c.limitBody()
		if err := c.Request.ParseMultipartForm(defaultMultipartMemory); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return ErrBodyTooLarge
			}
			if err := c.Request.ParseForm(); err != nil {
				return errx.Wrap(err, ErrParseForm)
			}
		}
		return bindValues(v, c.Request.Form)
	case ct == "":
		if len(c.QueryValues()) > 0 {
			return bindValues(v, c.QueryValues())
		}
		if err := c.Request.ParseForm(); err == nil && len(c.Request.Form) > 0 {
			return bindValues(v, c.Request.Form)
		}
		return nil
	default:
		return errx.Detail(ErrUnsupportedMediaType, "%s", ct)
	}
}

// bindValues binds url.Values to a struct or a map. | bindValues 把 url.Values 绑定到结构体/映射。
func bindValues(v any, values url.Values) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errx.Detail(ErrBadBindTarget, "target must be a non-nil pointer | 目标必须是非空指针")
	}
	rv = rv.Elem()
	switch rv.Kind() {
	case reflect.Struct:
		return bindStruct(rv, values)
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return errx.Detail(ErrBadBindTarget, "map key must be a string | 映射键必须是字符串")
		}
		if rv.IsNil() {
			rv.Set(reflect.MakeMap(rv.Type()))
		}
		for k, vs := range values {
			if len(vs) == 0 {
				continue
			}
			// Convert panics when the element type is not string-compatible (map[string]int is a
			// natural target). Build an element and let the scalar binder parse it, which also
			// reports overflow and unsupported types as errors instead of panicking. | 元素类型不可由
			// string 转换时 Convert 会 panic（map[string]int 是自然写法）。改为构造元素并交给标量
			// 绑定器解析，这样溢出与不支持的类型都会返回错误而不是 panic。
			elem := reflect.New(rv.Type().Elem()).Elem()
			if err := setFieldValue(elem, vs[0], "map key "+k); err != nil {
				return err
			}
			rv.SetMapIndex(reflect.ValueOf(k), elem)
		}
		return nil
	default:
		return errx.Detail(ErrBadBindTarget, "target must be a struct or map | 目标必须是结构体或映射")
	}
}

func bindStruct(rv reflect.Value, values url.Values) error {
	rt := rv.Type()
	var errs []error
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if !field.IsExported() {
			continue
		}
		fv := rv.Field(i)
		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			if err := bindStruct(fv, values); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		name := fieldName(field)
		if name == "-" {
			continue
		}
		raw, ok := lookupValue(values, name)
		if !ok {
			continue
		}
		if err := setFieldValue(fv, raw, field.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// fieldName determines the binding name in form → query → json tag order, defaulting to the field name. | fieldName 依 form → query → json 标签顺序确定绑定名，缺省用字段名。
func fieldName(field reflect.StructField) string {
	for _, tag := range []string{"form", "query", "json"} {
		if tagv, ok := field.Tag.Lookup(tag); ok {
			name := strings.Split(tagv, ",")[0]
			if name != "" {
				return name
			}
		}
	}
	return field.Name
}

func lookupValue(values url.Values, name string) (string, bool) {
	if vs, ok := values[name]; ok && len(vs) > 0 {
		return vs[0], true
	}
	// case-insensitive fallback | 大小写不敏感回退
	for k, vs := range values {
		if strings.EqualFold(k, name) && len(vs) > 0 {
			return vs[0], true
		}
	}
	return "", false
}

var timeType = reflect.TypeOf(time.Time{})

func setFieldValue(fv reflect.Value, raw, fieldName string) error {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return setFieldValue(fv.Elem(), raw, fieldName)
	}
	if fv.Type() == timeType {
		t, err := parseTime(raw)
		if err != nil {
			return errx.WrapDetail(err, ErrBindField, "field %[1]s | 字段 %[1]s", fieldName)
		}
		fv.Set(reflect.ValueOf(t))
		return nil
	}
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return errx.WrapDetail(err, ErrBindField, "field %[1]s | 字段 %[1]s", fieldName)
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Parse at the destination width so 200 into an int8 is an error, not -56. | 按目标位宽解析，
		// 使 200 写入 int8 时报错而不是变成 -56。
		n, err := strconv.ParseInt(raw, 10, fv.Type().Bits())
		if err != nil {
			return errx.WrapDetail(err, ErrBindField, "field %[1]s | 字段 %[1]s", fieldName)
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, fv.Type().Bits())
		if err != nil {
			return errx.WrapDetail(err, ErrBindField, "field %[1]s | 字段 %[1]s", fieldName)
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, fv.Type().Bits())
		if err != nil {
			return errx.WrapDetail(err, ErrBindField, "field %[1]s | 字段 %[1]s", fieldName)
		}
		fv.SetFloat(f)
	case reflect.Slice:
		if fv.Type().Elem().Kind() == reflect.String {
			fv.Set(reflect.ValueOf([]string{raw}))
			return nil
		}
		return errx.Detail(ErrUnsupportedFieldType, "field %[1]s: unsupported slice element type %[2]s | 字段 %[1]s：不支持的切片元素类型 %[2]s", fieldName, fv.Type().Elem())
	default:
		return errx.Detail(ErrUnsupportedFieldType, "field %[1]s: unsupported type %[2]s | 字段 %[1]s：不支持的类型 %[2]s", fieldName, fv.Type())
	}
	return nil
}

func parseTime(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errx.Detail(ErrBadTimeFormat, "%q", raw)
}

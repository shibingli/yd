package router

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/yun-dev/yd/jsonx"
)

// ErrUnsupportedMediaType 表示请求体无法按声明的内容类型绑定。
var ErrUnsupportedMediaType = errors.New("router: 不支持的请求内容类型")

// Bind 依据 Content-Type 把请求参数绑定到 v。
//
//   - application/json → 使用框架自适应 JSON 层（jsonx）解码请求体；
//   - application/x-www-form-urlencoded 或 multipart/form-data → 表单字段；
//   - 其他 → 依次尝试 query 与表单。
//
// 结构体字段按 `json`、`form`、`query` 标签匹配，其次按大小写不敏感的同名字段匹配。
// 绑定使用反射，因此仅用于请求入口；序列化与日志等热路径不依赖本函数。
func (c *Ctx) Bind(v any) error {
	if v == nil {
		return errors.New("router: Bind 目标不能为 nil")
	}
	ct := c.Request.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case ct == "application/json" || strings.HasSuffix(ct, "+json"):
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, c.router.maxBodySize()+1))
		if err != nil {
			return fmt.Errorf("router: 读取请求体失败: %w", err)
		}
		if int64(len(body)) > c.router.maxBodySize() {
			return errors.New("router: 请求体超出大小限制")
		}
		if len(body) == 0 {
			return nil
		}
		if err := jsonx.Unmarshal(body, v); err != nil {
			return fmt.Errorf("router: JSON 解码失败: %w", err)
		}
		return nil
	case ct == "application/x-www-form-urlencoded" || ct == "multipart/form-data" || strings.HasPrefix(ct, "multipart/form-data"):
		if err := c.Request.ParseMultipartForm(c.router.maxBodySize()); err != nil {
			if err := c.Request.ParseForm(); err != nil {
				return fmt.Errorf("router: 解析表单失败: %w", err)
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
		return fmt.Errorf("%w: %s", ErrUnsupportedMediaType, ct)
	}
}

// bindValues 把 url.Values 绑定到结构体/映射。
func bindValues(v any, values url.Values) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("router: Bind 目标必须是非 nil 指针")
	}
	rv = rv.Elem()
	switch rv.Kind() {
	case reflect.Struct:
		return bindStruct(rv, values)
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return errors.New("router: map 的键必须是字符串类型")
		}
		if rv.IsNil() {
			rv.Set(reflect.MakeMap(rv.Type()))
		}
		for k, vs := range values {
			if len(vs) == 0 {
				continue
			}
			rv.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(vs[0]).Convert(rv.Type().Elem()))
		}
		return nil
	default:
		return errors.New("router: Bind 目标必须是结构体或 map")
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

// fieldName 依 form → query → json 标签顺序确定绑定名，缺省用字段名。
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
	// 大小写不敏感回退
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
			return fmt.Errorf("字段 %s: %w", fieldName, err)
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
			return fmt.Errorf("字段 %s: %w", fieldName, err)
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("字段 %s: %w", fieldName, err)
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("字段 %s: %w", fieldName, err)
		}
		fv.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("字段 %s: %w", fieldName, err)
		}
		fv.SetFloat(f)
	case reflect.Slice:
		if fv.Type().Elem().Kind() == reflect.String {
			fv.Set(reflect.ValueOf([]string{raw}))
			return nil
		}
		return fmt.Errorf("字段 %s: 不支持的切片元素类型 %s", fieldName, fv.Type().Elem())
	default:
		return fmt.Errorf("字段 %s: 不支持的类型 %s", fieldName, fv.Type())
	}
	return nil
}

func parseTime(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("时间格式非法: %q", raw)
}

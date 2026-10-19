package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"html/template"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
)

// assetCache memoizes content hashes so templates do not re-read files. | assetCache 缓存内容哈希，避免模板重复读取文件。
type assetCache struct {
	seen map[string]string
	mu   sync.RWMutex
}

// Funcs returns the default template function map. | Funcs 返回默认模板函数表。
//
// The map is rebuilt on every call so callers can safely add their own
// entries without affecting other engines. | 每次调用都重建函数表，调用方可安全增补而不影响其它引擎。
//
// "tr" is included: it returns the message key itself until a translator is
// injected through the render data, see TranslatorKey and tr. | 其中包含 "tr"：
// 在通过渲染数据注入译者之前它返回消息键本身，见 TranslatorKey 与 tr。
//
// "can", "hasRole", "perm" and "role" are included too: they grant nothing and
// return their argument until an authority is injected through AuthzKey. | 其中还包含
// "can"、"hasRole"、"perm" 与 "role"：在通过 AuthzKey 注入判定主体之前，它们不授予任何能力并原样返回参数。
func Funcs(fsys afero.Fs) template.FuncMap {
	cache := &assetCache{seen: map[string]string{}}
	return template.FuncMap{
		"tr":        tr,
		"can":       can,
		"hasRole":   hasRole,
		"perm":      perm,
		"role":      role,
		"dict":      dict,
		"list":      list,
		"default":   dfault,
		"contains":  strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"lower":     strings.ToLower,
		"upper":     strings.ToUpper,
		"trim":      strings.TrimSpace,
		"join":      strings.Join,
		"json":      toJSON,
		"humanSize": humanSize,
		"humanTime": humanTime,
		"hx":        htmxAttrs,
		"asset":     cache.assetFunc(fsys),
	}
}

// dict builds a map from alternating key/value arguments. | dict 由交替的键值参数构造 map。
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, errx.Detail(ErrDictOddArgs, "got %[1]d args | 需要 %[1]d 个参数", len(kv))
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, errx.Detail(ErrDictKeyNotString, "index=%[1]d | 索引=%[1]d", i)
		}
		m[k] = kv[i+1]
	}
	return m, nil
}

// list builds a slice from its arguments. | list 由参数构造切片。
func list(v ...any) []any { return v }

// dfault returns def when v is empty (nil, zero value or empty string). | dfault 在 v 为空（nil、零值或空串）时返回 def。
func dfault(def, v any) any {
	if isEmpty(v) {
		return def
	}
	return v
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case int:
		return t == 0
	case int64:
		return t == 0
	case float64:
		return t == 0
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	default:
		return false
	}
}

// toJSON marshals v with the framework codec and marks it safe for scripting. | toJSON 用框架编解码器序列化 v，并标记为可安全嵌入脚本。
//
// jsonx escapes HTML-significant characters by default, so the result is safe
// inside a <script> block. | jsonx 默认转义 HTML 敏感字符，因此结果可安全嵌入 <script>。
func toJSON(v any) (template.JS, error) {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return "", errx.Wrap(err, ErrJSONMarshal)
	}
	return template.JS(b), nil
}

// humanSize renders a byte count in binary units. | humanSize 以二进制单位渲染字节数。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// humanTime renders a timestamp in a compact, locale-neutral layout. | humanTime 以紧凑且与语言无关的格式渲染时间。
func humanTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// htmxAttrs builds htmx attributes from alternating key/value arguments. | htmxAttrs 由交替的键值参数构造 htmx 属性。
//
// Keys may be given with or without the hx- prefix. The result is a
// template.HTMLAttr, which html/template inserts verbatim, so the helper escapes
// every value itself and rejects keys that are not plain attribute names. | 键可带可不带
// hx- 前缀。结果是 template.HTMLAttr，html/template 会原样插入，因此本函数自行转义每个值，
// 并拒绝不是普通属性名的键。
func htmxAttrs(kv ...any) (template.HTMLAttr, error) {
	if len(kv)%2 != 0 {
		return "", errx.Detail(ErrHtmxOddArgs, "got %[1]d args | 需要 %[1]d 个参数", len(kv))
	}
	pairs := make([]string, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return "", errx.Detail(ErrHtmxKeyNotString, "index=%[1]d | 索引=%[1]d", i)
		}
		if k == "" {
			return "", errx.Detail(ErrHtmxInvalidKey, "key=%[1]q | 键=%[1]q", k)
		}
		if !strings.HasPrefix(k, "hx-") {
			k = "hx-" + k
		}
		if !validAttrName(k) {
			return "", errx.Detail(ErrHtmxInvalidKey, "key=%[1]q | 键=%[1]q", k)
		}
		// hx-on:* attributes are JavaScript, not HTML: HTML escaping does not escape quotes inside
		// a JS string, so a computed value could break out and execute. | hx-on:* 属性是 JavaScript
		// 而不是 HTML：HTML 转义不会转义 JS 字符串里的引号，计算值可以逃逸并执行。
		if strings.HasPrefix(k, "hx-on") || strings.HasPrefix(k, "on") {
			return "", errx.Detail(ErrHtmxInvalidKey, "key=%[1]q is executable JavaScript and is not allowed | 键 %[1]q 是可执行 JavaScript，不允许使用", k)
		}
		pairs = append(pairs, fmt.Sprintf(`%s="%s"`, k, html.EscapeString(fmt.Sprint(kv[i+1]))))
	}
	sort.Strings(pairs)
	return template.HTMLAttr(strings.Join(pairs, " ")), nil
}

// validAttrName reports whether name is a plain HTML attribute name: letters,
// digits, underscores, dashes and colons only. | validAttrName 报告 name 是否为普通 HTML
// 属性名：仅允许字母、数字、下划线、连字符与冒号。
//
// Anything else (quotes, whitespace, "=", "<", ">", "/") could terminate the
// attribute or the tag, so it is refused rather than escaped: an attribute name
// has no legal escaped form. | 其它字符（引号、空白、"="、"<"、">"、"/"）可能终止属性或标签，
// 因此直接拒绝而不是转义：属性名没有合法的转义形态。
func validAttrName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == ':':
		default:
			return false
		}
	}
	return true
}

// assetFunc returns a function that appends a content hash to a URL path. | assetFunc 返回把内容哈希追加到 URL 路径的函数。
//
// The argument is a URL path such as "/static/css/yd.css"; the file is looked
// up in fsys without the leading slash. | 参数形如 "/static/css/yd.css"；文件在 fsys 中去掉前导斜杠后查找。
func (c *assetCache) assetFunc(fsys afero.Fs) func(string) (string, error) {
	return func(p string) (string, error) {
		if p == "" {
			return "", errx.Detail(ErrAssetNotFound, "empty path | 路径为空")
		}
		c.mu.RLock()
		h, ok := c.seen[p]
		c.mu.RUnlock()
		if !ok {
			file := strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "/")
			b, err := afero.ReadFile(fsys, file)
			if err != nil {
				return "", errx.WrapDetail(err, ErrAssetNotFound, "path=%[1]s | 路径=%[1]s", p)
			}
			sum := sha256.Sum256(b)
			h = hex.EncodeToString(sum[:4])
			c.mu.Lock()
			c.seen[p] = h
			c.mu.Unlock()
		}
		sep := "?"
		if strings.Contains(p, "?") {
			sep = "&"
		}
		return p + sep + "v=" + h, nil
	}
}

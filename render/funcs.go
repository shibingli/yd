package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/afero"

	"github.com/yun-dev/yd/errx"
	"github.com/yun-dev/yd/jsonx"
)

// assetCache memoizes content hashes so templates do not re-read files. | assetCache 缓存内容哈希，避免模板重复读取文件。
type assetCache struct {
	mu   sync.RWMutex
	seen map[string]string
}

// Funcs returns the default template function map. | Funcs 返回默认模板函数表。
//
// The map is rebuilt on every call so callers can safely add their own
// entries without affecting other engines. | 每次调用都重建函数表，调用方可安全增补而不影响其它引擎。
func Funcs(fsys afero.Fs) template.FuncMap {
	cache := &assetCache{seen: map[string]string{}}
	return template.FuncMap{
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
		return nil, errx.Detail(ErrDictOddArgs, "got %d args", len(kv))
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, errx.Detail(ErrDictKeyNotString, "index=%d", i)
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
// Keys may be given with or without the hx- prefix; values are HTML-escaped by
// html/template when the result is used as an attribute value. | 键可带可不带 hx- 前缀；
// 结果作为属性值时由 html/template 负责转义。
func htmxAttrs(kv ...any) (template.HTMLAttr, error) {
	if len(kv)%2 != 0 {
		return "", errx.Detail(ErrHtmxOddArgs, "got %d args", len(kv))
	}
	pairs := make([]string, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return "", errx.Detail(ErrHtmxKeyNotString, "index=%d", i)
		}
		if !strings.HasPrefix(k, "hx-") {
			k = "hx-" + k
		}
		pairs = append(pairs, fmt.Sprintf(`%s="%v"`, k, kv[i+1]))
	}
	sort.Strings(pairs)
	return template.HTMLAttr(strings.Join(pairs, " ")), nil
}

// assetFunc returns a function that appends a content hash to a URL path. | assetFunc 返回把内容哈希追加到 URL 路径的函数。
//
// The argument is a URL path such as "/static/css/yd.css"; the file is looked
// up in fsys without the leading slash. | 参数形如 "/static/css/yd.css"；文件在 fsys 中去掉前导斜杠后查找。
func (c *assetCache) assetFunc(fsys afero.Fs) func(string) (string, error) {
	return func(p string) (string, error) {
		if p == "" {
			return "", errx.Detail(ErrAssetNotFound, "empty path")
		}
		c.mu.RLock()
		h, ok := c.seen[p]
		c.mu.RUnlock()
		if !ok {
			file := strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "/")
			b, err := afero.ReadFile(fsys, file)
			if err != nil {
				return "", errx.WrapDetail(err, ErrAssetNotFound, "path=%s", p)
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

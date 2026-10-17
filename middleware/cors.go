package middleware

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shibingli/yd/http/router"
)

// CORSOptions configures the CORS middleware.
// | CORSOptions 配置 CORS 中间件。
type CORSOptions struct {
	// AllowOrigins lists the allowed origins. | AllowOrigins 列出允许的来源。
	// "*" matches every origin and "*.example.com" matches the subdomains of example.com. | "*" 匹配任意来源，"*.example.com" 匹配 example.com 的子域。
	// Any other value is matched literally, optionally prefixed with a scheme. | 其余取值按字面量匹配，可以带协议前缀。
	// An empty list allows no cross-origin caller at all. | 列表为空时不允许任何跨源调用。
	AllowOrigins []string
	// AllowMethods lists the methods echoed on preflight requests. | AllowMethods 列出预检请求回显的方法。
	AllowMethods []string
	// AllowHeaders lists the request headers echoed on preflight requests. | AllowHeaders 列出预检请求回显的请求头。
	AllowHeaders []string
	// ExposeHeaders lists the response headers exposed to the browser. | ExposeHeaders 列出暴露给浏览器的响应头。
	ExposeHeaders []string
	// AllowCredentials allows cookies and Authorization on cross-origin requests. | AllowCredentials 允许跨源请求携带 cookie 与 Authorization。
	AllowCredentials bool
	// MaxAge is sent as Access-Control-Max-Age on preflight responses. | MaxAge 作为 Access-Control-Max-Age 写在预检响应上。
	MaxAge time.Duration
}

// originPattern is a parsed entry of CORSOptions.AllowOrigins.
// | originPattern 是 CORSOptions.AllowOrigins 中一条解析后的规则。
type originPattern struct {
	scheme string // "" means any scheme. | 空串表示不限协议。
	host   string // lowercase; a leading dot means subdomain matching. | 小写；以点开头表示子域匹配。
	any    bool   // "*" matches any host. | "*" 匹配任意主机。
}

// parseOriginPattern parses one AllowOrigins entry. | parseOriginPattern 解析一条 AllowOrigins 规则。
func parseOriginPattern(raw string) (originPattern, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return originPattern{}, false
	}
	p := originPattern{}
	if i := strings.Index(s, "://"); i >= 0 {
		p.scheme = strings.ToLower(s[:i])
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.ToLower(s)
	switch {
	case s == "*":
		p.any = true
	case strings.HasPrefix(s, "*."):
		p.host = s[1:] // keep the leading dot: it marks subdomain matching. | 保留前导点：它标记子域匹配。
	default:
		p.host = s
	}
	if !p.any && p.host == "" {
		return originPattern{}, false
	}
	return p, true
}

// matchOrigin reports whether origin is allowed and which value the response header must carry.
// | matchOrigin 报告 origin 是否被允许，以及响应头应当携带的取值。
func matchOrigin(origin string, patterns []originPattern, credentials bool) (bool, string) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false, ""
	}
	host := strings.ToLower(u.Host)
	scheme := strings.ToLower(u.Scheme)
	for _, p := range patterns {
		if p.scheme != "" && p.scheme != scheme {
			continue
		}
		switch {
		case p.any:
			// Wildcards may never be combined with credentials. | 通配绝不可与凭证同时使用。
			if credentials {
				return true, origin
			}
			return true, "*"
		case strings.HasPrefix(p.host, "."):
			if len(host) > len(p.host) && strings.HasSuffix(host, p.host) {
				return true, origin
			}
		case p.host == host:
			return true, origin
		}
	}
	return false, ""
}

// CORS returns a middleware that applies the given cross-origin policy. | CORS 返回应用给定跨源策略的中间件。
// Three rules are enforced. | 该中间件强制三条硬规则。
// With AllowCredentials the response never carries "*" and echoes the concrete origin instead. | 开启 AllowCredentials 时绝不回 "*"，改为回显具体来源。
// Preflight requests — OPTIONS plus Access-Control-Request-Method — are answered with 204 and
// short-circuit the chain. | 预检请求（OPTIONS 且带 Access-Control-Request-Method）直接以 204 短路。
// An unmatched origin receives no CORS headers at all instead of an error. | 来源未匹配时不写任何 CORS 头，而不是返回错误。
func CORS(o CORSOptions) router.Middleware {
	patterns := make([]originPattern, 0, len(o.AllowOrigins))
	for _, raw := range o.AllowOrigins {
		if p, ok := parseOriginPattern(raw); ok {
			patterns = append(patterns, p)
		}
	}
	allowMethods := strings.Join(o.AllowMethods, ", ")
	allowHeaders := strings.Join(o.AllowHeaders, ", ")
	exposeHeaders := strings.Join(o.ExposeHeaders, ", ")
	maxAge := ""
	if o.MaxAge > 0 {
		maxAge = strconv.FormatInt(int64(o.MaxAge/time.Second), 10)
	}
	credentials := o.AllowCredentials

	return func(c *router.Ctx) {
		origin := headerValue(c.Request.Header, "Origin")
		preflight := c.Request.Method == http.MethodOptions &&
			headerValue(c.Request.Header, "Access-Control-Request-Method") != ""

		allowed := false
		value := ""
		if origin != "" {
			allowed, value = matchOrigin(origin, patterns, credentials)
		}

		h := c.Writer.Header()
		if allowed {
			h.Set("Access-Control-Allow-Origin", value)
			if value != "*" {
				// Caches must key on the origin whenever it is echoed. | 只要回显来源，缓存就必须按来源区分。
				appendVary(h, "Origin")
			}
			if credentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
		}

		if preflight {
			if allowed {
				methods := allowMethods
				if methods == "" {
					// Fall back to the requested method when no list is configured. | 未配置方法列表时回退到请求中的方法。
					methods = headerValue(c.Request.Header, "Access-Control-Request-Method")
				}
				if methods != "" {
					h.Set("Access-Control-Allow-Methods", methods)
				}
				headers := allowHeaders
				if headers == "" {
					headers = headerValue(c.Request.Header, "Access-Control-Request-Headers")
				}
				if headers != "" {
					h.Set("Access-Control-Allow-Headers", headers)
				}
				if maxAge != "" {
					h.Set("Access-Control-Max-Age", maxAge)
				}
			}
			c.Abort()
			_ = c.NoContent(http.StatusNoContent)
			return
		}

		if allowed && exposeHeaders != "" {
			h.Set("Access-Control-Expose-Headers", exposeHeaders)
		}
		c.Next()
	}
}

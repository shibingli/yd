package i18n

import (
	"strings"

	"github.com/shibingli/yd/http/router"
)

// CtxKey is the router context key holding the request translator. | CtxKey 是路由上下文中保存请求译者的键。
const CtxKey = "yd.i18n.translator"

// LanguageHeader is the response header carrying the negotiated language. | LanguageHeader 是承载协商结果的响应头名称。
const LanguageHeader = "Content-Language"

// AcceptLanguageHeader is the standard request header used as the last source. | AcceptLanguageHeader 是作为最后来源的标准请求头名称。
const AcceptLanguageHeader = "Accept-Language"

// Default sources used when Options leaves a field empty. | Options 字段留空时使用的默认来源。
const (
	// DefaultQueryParam is the default query parameter name. | DefaultQueryParam 是默认查询参数名。
	DefaultQueryParam = "lang"
	// DefaultCookieName is the default cookie name. | DefaultCookieName 是默认 cookie 名。
	DefaultCookieName = "yd_lang"
	// DefaultHeaderName is the default request header name. | DefaultHeaderName 是默认请求头名。
	DefaultHeaderName = "X-Lang"
)

// Options configures where the middleware looks for the requested language. | Options 配置中间件查找请求语言的来源。
type Options struct {
	// QueryParam is the query parameter name, default "lang". | QueryParam 是查询参数名，默认 "lang"。
	QueryParam string
	// CookieName is the cookie name, default "yd_lang". | CookieName 是 cookie 名，默认 "yd_lang"。
	CookieName string
	// HeaderName is the request header name, default "X-Lang". | HeaderName 是请求头名，默认 "X-Lang"。
	HeaderName string
}

// withDefaults fills the empty fields with the documented defaults. | withDefaults 用文档约定的默认值填充空字段。
func (o Options) withDefaults() Options {
	if o.QueryParam == "" {
		o.QueryParam = DefaultQueryParam
	}
	if o.CookieName == "" {
		o.CookieName = DefaultCookieName
	}
	if o.HeaderName == "" {
		o.HeaderName = DefaultHeaderName
	}
	return o
}

// source returns the first non-empty language hint of the request. | source 返回请求中首个非空的语种线索。
func (o Options) source(c *router.Ctx) string {
	if v := strings.TrimSpace(c.Query(o.QueryParam)); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Cookie(o.CookieName)); v != "" {
		return v
	}
	if v := strings.TrimSpace(c.Header(o.HeaderName)); v != "" {
		return v
	}
	return strings.TrimSpace(c.Header(AcceptLanguageHeader))
}

// Middleware resolves the request language and stores a translator in the context. | Middleware 解析请求语言并把译者放入上下文。
// Sources are consulted in order, first non-empty wins: query > cookie > custom header > Accept-Language. | 来源按顺序查询，首个非空者胜出：query > cookie > 自定义请求头 > Accept-Language。
// The winner is negotiated by Bundle.MatchLanguage, so q values, prefixes and the `*` wildcard are honoured, and an unknown or absent hint resolves to the default language. | 胜出值交由 Bundle.MatchLanguage 协商，因此 q 值、前缀与 `*` 通配都被支持，线索未知或缺失时解析为默认语言。
// The translator is stored under CtxKey and the resolved tag is written to the Content-Language response header before the chain continues. | 译者保存在 CtxKey 下，协商结果写入响应头 Content-Language，随后继续执行后续处理器。
// A nil bundle degrades to a pass-through middleware. | b 为 nil 时退化为直通中间件。
func Middleware(b *Bundle, o Options) router.Middleware {
	if b == nil {
		return func(c *router.Ctx) { c.Next() }
	}

	o = o.withDefaults()
	return func(c *router.Ctx) {
		translator := b.Translator(b.MatchLanguage(o.source(c)))
		c.Set(CtxKey, translator)
		c.SetHeader(LanguageHeader, translator.Lang())
		// The response body depends on request headers, so shared caches must key on them. A
		// query-selected language is already part of the cache key, but Accept-Language, the
		// custom header and the cookie are not. | 响应正文依赖请求头，共享缓存必须按它们区分。
		// 通过查询参数选择的语言已在缓存键里，但 Accept-Language、自定义头与 cookie 不在。
		c.SetHeader("Vary", strings.Join([]string{AcceptLanguageHeader, o.HeaderName, "Cookie"}, ", "))
		c.Next()
	}
}

// FromContext returns the translator installed by Middleware. | FromContext 返回 Middleware 装入的译者。
// It returns nil when the middleware is not installed, which callers can use to detect a missing wiring; T and Lang stay safe on the nil translator, so `FromContext(c).T("key")` never panics. | 未安装中间件时返回 nil，调用方可据此发现装配缺失；nil 译者上的 T 与 Lang 依旧安全，因此 `FromContext(c).T("key")` 不会 panic。
func FromContext(c *router.Ctx) *Translator {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(CtxKey); ok {
		if translator, ok := value.(*Translator); ok {
			return translator
		}
	}
	return nil
}

// TemplateFunc returns a `t` helper bound to the request translator. | TemplateFunc 返回绑定当前请求译者的 `t` 模板函数。
// Register it once during wiring, for example `render.New().Funcs(render.FuncMap{"t": i18n.TemplateFunc})`. | 在装配阶段注册一次即可，例如 `render.New().Funcs(render.FuncMap{"t": i18n.TemplateFunc})`。
// The helper is captured per request, so `{{ t "user.title" }}` follows the negotiated language. | 辅助函数按请求捕获，因此 `{{ t "user.title" }}` 会跟随协商出的语言。
func TemplateFunc(c *router.Ctx) func(key string, args ...any) string {
	translator := FromContext(c)
	return func(key string, args ...any) string {
		return translator.T(key, args...)
	}
}

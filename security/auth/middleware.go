package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/log"
	"github.com/shibingli/ydx/router"
)

// DefaultCtxKey is the router context key holding the authenticated *User. | DefaultCtxKey 是保存已认证 *User 的路由上下文键。
const DefaultCtxKey = "auth.user"

// DefaultTokenLookup is the lookup used when MiddlewareOptions.TokenLookup is empty. | DefaultTokenLookup 是 MiddlewareOptions.TokenLookup 为空时使用的提取方式。
const DefaultTokenLookup = "header:Authorization:Bearer "

// Context keys written by Middleware for RequirePermission. | Middleware 为 RequirePermission 写入的上下文键。
const (
	// serviceCtxKey holds the *Service of the issuer. | serviceCtxKey 保存签发器所属的 *Service。
	serviceCtxKey = "auth.service"
	// handlerCtxKey holds the error renderer chosen by Middleware. | handlerCtxKey 保存 Middleware 选定的错误渲染器。
	handlerCtxKey = "auth.error_handler"
)

// MiddlewareOptions configures Middleware. | MiddlewareOptions 配置 Middleware。
type MiddlewareOptions struct {
	// Issuer verifies tokens; nil makes every request fail instead of panicking. | Issuer 负责校验令牌；为空时每个请求都失败，而不是 panic。
	Issuer *TokenIssuer
	// ErrorHandler renders the failure; nil means the default JSON renderer. | ErrorHandler 负责输出失败响应；为空表示使用默认的 JSON 渲染器。
	//
	// It is reused by RequirePermission, so it must map ErrForbidden to 403;
	// the packaged default does. | 它会被 RequirePermission 复用，因此必须把 ErrForbidden 映射为 403；
	// 本包的默认实现已经如此。
	ErrorHandler func(*router.Ctx, error)
	// TokenLookup selects where the token is read from; empty selects DefaultTokenLookup. | TokenLookup 指定令牌的提取位置；为空时取 DefaultTokenLookup。
	TokenLookup string
	// CtxKey is the router context key holding the *User; empty selects DefaultCtxKey. | CtxKey 是保存 *User 的路由上下文键；为空时取 DefaultCtxKey。
	CtxKey string
	// Optional lets anonymous and invalid-token requests continue without a user instead of denying them. |
	// Optional 让匿名与无效令牌请求以「无用户」继续，而不是拒绝它们。
	//
	// It exists for public pages that only decorate the view when a session is present. It must never be used on
	// protected routes: there a missing user is the whole point of the check. |
	// 它用于「有会话就装饰视图」的公开页面；绝不能用于受保护路由——那里的检查正是要求用户存在。
	Optional bool
}

// Middleware verifies the bearer token of a request and stores the authenticated
// user in the router context. | Middleware 校验请求携带的 Bearer 令牌，并把已认证用户存入路由上下文。
//
// Verification resolves the per-user sec_key from the "kid" header parameter, so a
// token is only ever accepted by the very user it was issued to, and it also loads
// the user record, so a disabled user, a deleted user or a rotated sec_key takes
// effect on the next request. | 校验凭 "kid" 头部参数解析每用户 sec_key，因此令牌只会被它原本签发的
// 那个用户接受；校验过程还会读取用户记录，因此被禁用、被删除或 sec_key 已轮换都会在下一次请求立即生效。
//
// TokenLookup accepts these forms, defaulting to DefaultTokenLookup | TokenLookup 接受以下形式，默认为 DefaultTokenLookup：
//
//	"header:Authorization:Bearer "  the named header, with the scheme prefix removed | 指定头部，去掉 scheme 前缀
//	"header:X-Token:"               the named header read as-is | 指定头部，原样读取
//	"X-Token"                       a bare name is treated as a header | 裸名称按头部处理
//	"query:token"                   a query parameter | 查询参数
//	"cookie:jwt"                    a cookie | 从 Cookie 读取
//
// The chain is stopped with Ctx.Abort before the error handler runs, so a rejected
// request never reaches the business handler. | 错误处理器执行前，链路会通过 Ctx.Abort 终止，
// 因此被拒绝的请求绝不会到达业务处理器。
func Middleware(o MiddlewareOptions) router.Middleware {
	lookup, lookupErr := parseTokenLookup(o.TokenLookup)
	ctxKey := o.CtxKey
	if ctxKey == "" {
		ctxKey = DefaultCtxKey
	}
	handle := o.ErrorHandler
	if handle == nil {
		handle = defaultErrorHandler
	}
	issuer := o.Issuer
	return func(c *router.Ctx) {
		if lookupErr != nil {
			deny(c, handle, lookupErr)
			return
		}
		if issuer == nil {
			deny(c, handle, errs.Detail(ErrTokenInvalid, "middleware has no token issuer | 中间件未配置令牌签发器"))
			return
		}
		raw, ok := lookup(c)
		if !ok {
			if o.Optional {
				c.Next()
				return
			}
			deny(c, handle, errs.Detail(ErrTokenInvalid, "request carries no token | 请求未携带令牌"))
			return
		}
		u, err := issuer.Verify(c.Request.Context(), raw)
		if err != nil {
			if o.Optional {
				c.Next()
				return
			}
			deny(c, handle, err)
			return
		}
		// The user is always stored under DefaultCtxKey so that CurrentUser works
		// unchanged, and additionally under a custom key when one was configured. | 用户始终以
		// DefaultCtxKey 存入，使 CurrentUser 无需改动即可工作；配置了自定义键时再额外存一份。
		c.Set(DefaultCtxKey, u)
		if ctxKey != DefaultCtxKey {
			c.Set(ctxKey, u)
		}
		c.Set(serviceCtxKey, issuer.svc)
		c.Set(handlerCtxKey, handle)
		c.Next()
	}
}

// CurrentUser returns the authenticated user stored by Middleware. | CurrentUser 返回 Middleware 存入的已认证用户。
func CurrentUser(c *router.Ctx) (*User, bool) {
	v, ok := c.Get(DefaultCtxKey)
	if !ok {
		return nil, false
	}
	u, ok := v.(*User)
	return u, ok
}

// RequirePermission returns a middleware that admits only users holding p. | RequirePermission 返回只放行持有 p 的用户的时间件。
//
// The decision is taken on the user already stored in the context, and a rejection
// is ErrForbidden, which the renderer of Middleware maps to 403. The resource the
// handler would have touched is never looked up, so a 403 cannot betray whether it
// exists. | 判定完全基于上下文中已有的用户，拒绝理由为 ErrForbidden，由 Middleware 的渲染器映射为 403。
// 处理器本会触碰的资源从不被查询，因此 403 不会泄露其是否存在。
//
// A missing user or a missing Service is a wiring fault and is reported as
// ErrTokenInvalid, which renders as 401. | 缺少用户或缺少 Service 属于装配错误，以 ErrTokenInvalid 上报并渲染为 401。
func RequirePermission(p Permission) router.Middleware {
	return func(c *router.Ctx) {
		u, ok := CurrentUser(c)
		if !ok {
			deny(c, currentHandler(c), errs.Detail(ErrTokenInvalid, "no authenticated user in the context | 上下文中没有已认证用户"))
			return
		}
		svc, ok := currentService(c)
		if !ok {
			deny(c, currentHandler(c), errs.Detail(ErrTokenInvalid, "no auth service in the context | 上下文中没有 auth 服务"))
			return
		}
		if !svc.HasPermission(u, p) {
			deny(c, currentHandler(c), errs.Detail(ErrForbidden, "missing permission %[1]q | 缺少权限 %[1]q", string(p)))
			return
		}
		c.Next()
	}
}

// RequireAnyPermission returns a middleware that admits a user holding at least one of perms. |
// RequireAnyPermission 返回只放行持有 perms 中至少一项能力的用户的中间件。
//
// It exists for routes whose handler already implements an OR rule (a manager may act through the same
// endpoint as the owner), so the route guard and the view flags can agree without encoding a second
// copy of the rule. An empty list refuses every request instead of allowing them. |
// 它用于处理器本身已实现「或」规则的路由（管理员可以走与属主相同的端点），让路由守卫与视图标志
// 使用同一条规则，而不是各写一份。空列表拒绝所有请求，而不是放行。
func RequireAnyPermission(perms ...Permission) router.Middleware {
	return func(c *router.Ctx) {
		u, ok := CurrentUser(c)
		if !ok {
			deny(c, currentHandler(c), errs.Detail(ErrTokenInvalid, "no authenticated user in the context | 上下文中没有已认证用户"))
			return
		}
		svc, ok := currentService(c)
		if !ok {
			deny(c, currentHandler(c), errs.Detail(ErrTokenInvalid, "no auth service in the context | 上下文中没有 auth 服务"))
			return
		}
		for _, p := range perms {
			if svc.HasPermission(u, p) {
				c.Next()
				return
			}
		}
		deny(c, currentHandler(c), errs.Detail(ErrForbidden, "missing every permission in %v | 缺少 %v 中的全部权限", perms, perms))
	}
}

// currentService returns the Service stored by Middleware. | currentService 返回 Middleware 存入的 Service。
func currentService(c *router.Ctx) (*Service, bool) {
	v, ok := c.Get(serviceCtxKey)
	if !ok {
		return nil, false
	}
	svc, ok := v.(*Service)
	return svc, ok
}

// currentHandler returns the renderer stored by Middleware, or the default one. | currentHandler 返回 Middleware 存入的渲染器，没有时返回默认实现。
func currentHandler(c *router.Ctx) func(*router.Ctx, error) {
	if v, ok := c.Get(handlerCtxKey); ok {
		if h, ok := v.(func(*router.Ctx, error)); ok {
			return h
		}
	}
	return defaultErrorHandler
}

// deny aborts the chain and renders err through handle. | deny 终止链路并通过 handle 渲染 err。
func deny(c *router.Ctx, handle func(*router.Ctx, error), err error) {
	c.Abort()
	handle(c, err)
}

// defaultErrorHandler writes a short JSON body with the status that matches err. | defaultErrorHandler 按 err 对应的状态码写出简短 JSON 正文。
//
// The message is the sentinel text, which is bilingual and therefore carries no
// internal detail beyond the classification. | 正文即哨兵文案，本身中英同步，除分类外不携带任何内部细节。
func defaultErrorHandler(c *router.Ctx, err error) {
	status := statusFor(err)
	// Log the wrapped error server-side, but never echo it: errs keeps the underlying driver text
	// on the chain, so err.Error() can disclose a DB host, path or account state to an
	// unauthenticated caller. | 在服务端记录完整错误，但绝不回显：errs 会把底层驱动文案保留在
	// 错误链上，err.Error() 可能向未认证调用方泄露数据库主机、路径或账号状态。
	log.Default().Warn("auth: request rejected | auth：请求被拒绝",
		log.Int("status", status), log.Err(err))
	if writeErr := c.JSON(status, map[string]string{"error": authErrorMessage(status)}); writeErr != nil {
		log.Default().Warn("auth: failed to write the error response | auth：写出错误响应失败", log.Err(writeErr))
	}
}

// authErrorMessage returns the fixed, bilingual body of one status class. | authErrorMessage
// 返回某个状态类别固定的中英文正文。
func authErrorMessage(status int) string {
	// Use the declared sentinels: their text is static and bilingual, so it classifies the failure
	// without echoing any wrapped store/driver detail. | 使用已声明的哨兵：其文案静态且中英同步，
	// 既能说明失败类别，又不会回显任何被包装的存储/驱动细节。
	switch status {
	case http.StatusForbidden:
		return ErrForbidden.Error()
	case http.StatusNotFound:
		return ErrNotFound.Error()
	case http.StatusConflict:
		return ErrConflict.Error()
	case http.StatusBadRequest:
		return ErrInvalidInput.Error()
	default:
		return ErrTokenInvalid.Error()
	}
}

// statusFor maps a package sentinel onto an HTTP status code. | statusFor 把包级哨兵映射为 HTTP 状态码。
//
// Anything unrecognised is treated as an authentication failure, so an unexpected
// error can never be answered with a success status. | 未识别的错误按认证失败处理，
// 因此意外错误绝不会以成功状态码作答。
func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ErrWeakPassword), errors.Is(err, ErrInvalidInput), errors.Is(err, ErrTenantRequired):
		return http.StatusBadRequest
	default:
		return http.StatusUnauthorized
	}
}

// parseTokenLookup compiles a TokenLookup specification into an extractor. | parseTokenLookup 把 TokenLookup 配置编译成提取器。
//
// The scheme prefix of a header lookup is compared case-insensitively, so "bearer "
// and "Bearer " behave alike. | 头部提取的 scheme 前缀按大小写不敏感比较，因此 "bearer " 与 "Bearer " 行为一致。
func parseTokenLookup(spec string) (func(*router.Ctx) (string, bool), error) {
	if spec == "" {
		spec = DefaultTokenLookup
	}
	kind, rest := spec, ""
	if i := strings.IndexByte(spec, ':'); i >= 0 {
		kind, rest = spec[:i], spec[i+1:]
	}
	switch kind {
	case "header":
		name, prefix := rest, ""
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			name, prefix = rest[:i], rest[i+1:]
		}
		if name == "" {
			return nil, errs.Detail(ErrInvalidInput, "empty header name | 头部名称为空")
		}
		return func(c *router.Ctx) (string, bool) { return headerToken(c.Header(name), prefix) }, nil
	case "query":
		if rest == "" {
			return nil, errs.Detail(ErrInvalidInput, "empty query name | 查询参数名为空")
		}
		return func(c *router.Ctx) (string, bool) { return nonEmptyToken(c.Query(rest)) }, nil
	case "cookie":
		if rest == "" {
			return nil, errs.Detail(ErrInvalidInput, "empty cookie name | Cookie 名为空")
		}
		return func(c *router.Ctx) (string, bool) { return nonEmptyToken(c.Cookie(rest)) }, nil
	}
	// No recognised kind: the whole specification is a header name. | 未识别出类型：整个配置就是一个头部名称。
	return func(c *router.Ctx) (string, bool) { return headerToken(c.Header(spec), "") }, nil
}

// headerToken strips prefix from value and reports whether a token remains. | headerToken 从 value 中去掉 prefix，并报告是否还剩令牌。
func headerToken(value, prefix string) (string, bool) {
	if prefix != "" {
		if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
			return "", false
		}
		value = value[len(prefix):]
	}
	return nonEmptyToken(value)
}

// nonEmptyToken trims value and reports whether anything is left. | nonEmptyToken 去除 value 首尾空白并报告是否还有内容。
func nonEmptyToken(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != ""
}

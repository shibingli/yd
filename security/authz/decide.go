package authz

import (
	"context"
	"net/http"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/http/router"
	"github.com/shibingli/yd/security/auth"
)

// CtxKey is the router context key holding the *Subject of the request. | CtxKey 是保存本次请求判定主体的路由上下文键。
const CtxKey = "authz.subject"

// ctxKey is the private context key type used on plain context.Context values. | ctxKey 是普通 context.Context 上使用的私有键类型。
//
// The type is unexported on purpose: a key of an exported type could be forged by another package,
// and then a request could arrive carrying a subject nobody authenticated. | 该类型刻意不导出：
// 导出类型的键可以被别的包伪造，于是请求就可能带着一个没人认证过的主体抵达。
type ctxKey string

// subjectCtxKey is the key WithSubject writes and SubjectOf reads. | subjectCtxKey 是 WithSubject 写入、SubjectOf 读取的键。
const subjectCtxKey ctxKey = "authz.subject"

// Subject is the decision subject of one request: one user and the service holding the RBAC data.
// Subject 是单次请求的判定主体：一个用户，以及持有 RBAC 数据的服务。
//
// A nil *Subject is a valid anonymous subject: every method below answers "no" instead of
// panicking, because a page rendered without a sign-in is a normal page and not a fault. | 空的
// *Subject 是合法的匿名主体：下面每个方法都回答「否」而不是 panic，因为未登录时渲染页面是正常页面而非故障。
type Subject struct {
	// svc reads the roles of the tenant. | svc 读取租户的角色。
	svc *auth.Service
	// user is the authenticated user, or nil when the request is anonymous. | user 是已认证用户，匿名请求时为 nil。
	user *auth.User
}

// NewSubject pairs a user with the service that can decide for it. | NewSubject 把一个用户与能为它判定的服务配对。
//
// u is expected to come from auth.CurrentUser, i.e. from the token the request presented; a nil u
// yields an anonymous subject rather than an error. | u 应来自 auth.CurrentUser，即请求携带的令牌；
// u 为空时得到匿名主体而不是错误。
func NewSubject(svc *auth.Service, u *auth.User) *Subject {
	return &Subject{svc: svc, user: u}
}

// SubjectOf returns the subject stored in ctx. | SubjectOf 返回 ctx 中存放的判定主体。
//
// Both carriers are understood: a *router.Ctx keeps its subject under CtxKey, and any
// context.Context carries one written by WithSubject. | 两种载体都能识别：
// *router.Ctx 把主体放在 CtxKey 下，而任意 context.Context 携带的是 WithSubject 写入的那一个。
func SubjectOf(ctx context.Context) (*Subject, bool) {
	if ctx == nil {
		return nil, false
	}
	if getter, ok := ctx.(interface{ Get(string) (any, bool) }); ok {
		if v, ok := getter.Get(CtxKey); ok {
			if s, ok := v.(*Subject); ok {
				return s, true
			}
		}
	}
	if s, ok := ctx.Value(subjectCtxKey).(*Subject); ok {
		return s, true
	}
	return nil, false
}

// WithSubject returns a context carrying s. | WithSubject 返回携带 s 的上下文。
//
// It exists so that code which only sees a context.Context - a service method called from a
// handler, a queue worker - can still take the same decision as the handler that started it. |
// 它的存在是为了让只看得到 context.Context 的代码——处理器调用的服务方法、队列消费者——
// 仍能做出与发起它的处理器相同的判定。
func WithSubject(ctx context.Context, s *Subject) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, subjectCtxKey, s)
}

// Middleware stores the decision subject of every request. | Middleware 存入每个请求的判定主体。
//
// It reads the user the auth middleware put in the context, so it runs after it:
//
//	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: issuer}))
//	r.Use(authz.Middleware(svc))
//
// 它读取 auth 中间件放入上下文的用户，因此应排在其后运行：
//
//	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: issuer}))
//	r.Use(authz.Middleware(svc))
//
// A subject already stored by WithSubject wins, so a handler that builds its own subject - a
// background job, an impersonated request - is never overwritten by this middleware. | 若
// WithSubject 已存入主体，则以它为准，因此自行构建主体的处理器（后台任务、模拟登录的请求）
// 不会被本中间件覆盖。
func Middleware(svc *auth.Service) router.Middleware {
	return func(c *router.Ctx) {
		if _, ok := SubjectOf(c); ok {
			c.Next()
			return
		}
		u, _ := auth.CurrentUser(c)
		s := NewSubject(svc, u)
		c.Set(CtxKey, s)
		// The request context carries it too, so the subject survives a call that only receives a
		// context.Context. | 请求上下文也带一份，使主体在「只收到 context.Context」的调用中依然可用。
		if c.Request != nil {
			c.Request = c.Request.WithContext(WithSubject(c.Request.Context(), s))
		}
		c.Next()
	}
}

// Can reports whether the subject of ctx holds a capability. | Can 报告 ctx 的主体是否持有某能力。
//
// A missing subject - a page without the middleware in the chain - answers false, so the failure
// mode of a missing wiring is the same as that of a missing permission. | 缺少主体时
// （链上没有该中间件）回答 false，因此漏装配的失败形态与缺权限完全一致。
func Can(ctx context.Context, perm string) bool {
	s, ok := SubjectOf(ctx)
	if !ok {
		return false
	}
	return s.Can(perm)
}

// HasRole reports whether the subject of ctx holds a role. | HasRole 报告 ctx 的主体是否持有某角色。
func HasRole(ctx context.Context, role string) bool {
	s, ok := SubjectOf(ctx)
	if !ok {
		return false
	}
	return s.HasRole(role)
}

// Can reports whether the subject holds a capability. | Can 报告主体是否持有某能力。
//
// The reference may be a token, a constant name, a data key or a JavaScript key; an unknown one
// answers false, never an error, because a decision helper that can fail would have to be handled
// at every call site. | 引用可以是标识、常量名、数据键或 JavaScript 键；未知引用回答 false 而不是报错，
// 因为会失败的判定辅助函数必须在每个调用点被处理。
//
// A user that is disabled, deleted or stripped of its roles holds nothing: the answer comes from
// auth.Service.PermissionsOf, which reports an empty set for all three. | 被禁用、被删除或被摘掉角色的用户
// 不持有任何能力：答案来自 auth.Service.PermissionsOf，它对这三种情况都返回空集。
func (s *Subject) Can(perm string) bool {
	token, err := ResolvePermission(perm)
	if err != nil {
		return false
	}
	return s.canToken(token)
}

// HasRole reports whether the subject holds a role. | HasRole 报告主体是否持有某角色。
func (s *Subject) HasRole(role string) bool {
	name, err := ResolveRole(role)
	if err != nil {
		return false
	}
	if s == nil || s.user == nil {
		return false
	}
	for _, held := range s.user.Roles {
		if held == name {
			return true
		}
	}
	return false
}

// Permissions returns the capabilities the subject holds, sorted. | Permissions 返回主体持有的能力，已排序。
func (s *Subject) Permissions() []string {
	if s == nil || s.svc == nil {
		return nil
	}
	granted := s.svc.PermissionsOf(s.user)
	if len(granted) == 0 {
		return nil
	}
	out := make([]string, 0, len(granted))
	for _, p := range granted {
		out = append(out, string(p))
	}
	return out
}

// Roles returns the roles the subject holds, sorted. | Roles 返回主体持有的角色，已排序。
func (s *Subject) Roles() []string {
	if s == nil || s.svc == nil {
		return nil
	}
	return s.svc.RolesOf(s.user)
}

// User returns the authenticated user, or nil for an anonymous subject. | User 返回已认证用户，匿名主体返回 nil。
func (s *Subject) User() *auth.User {
	if s == nil {
		return nil
	}
	return s.user
}

// Anonymous reports whether the subject holds no user. | Anonymous 报告主体是否没有用户。
func (s *Subject) Anonymous() bool { return s == nil || s.user == nil }

// canToken takes the decision for an already resolved capability token. | canToken 对已解析的能力标识作出判定。
func (s *Subject) canToken(token string) bool {
	if s == nil || s.svc == nil || token == "" {
		return false
	}
	return s.svc.HasPermission(s.user, auth.Permission(token))
}

// Require returns a middleware that admits only a subject holding perm. | Require 返回只放行持有 perm 的主体的中间件。
//
// It resolves the reference against the registry and then delegates to auth.RequirePermission, so
// the rejection is the one the application already renders - 403 through the error handler the auth
// middleware installed - and the resource the handler would have touched is never looked up, which
// is what keeps a 403 from betraying whether it exists. | 它先按注册表解析引用，再委托给
// auth.RequirePermission，因此拒绝语与应用的既有渲染一致（经 auth 中间件装入的错误渲染器输出 403），
// 且处理器本会触碰的资源从不会被查询，这正是 403 不泄露资源是否存在的原因。
//
// The auth middleware must be upstream in the chain, because the user and the service are read
// from the context it fills. | auth 中间件必须位于链的上游，因为用户与服务都是从它填充的上下文中读取的。
//
// An unregistered reference is reported once here, at wiring time, and the route then refuses
// every request: a typo must be visible in the log and in `yd authz check`, never silently
// permissive. | 引用未注册时在这里（装配期）报告一次，随后该路由拒绝所有请求：
// 拼写错误必须出现在日志与 `yd authz check` 中，绝不能变得「静默放行」。
func Require(perm string) router.Middleware {
	token, err := ResolvePermission(perm)
	if err != nil {
		logx.Default().Warn("authz: a route is guarded by an unregistered permission, every request is refused | authz：某路由守着未注册的权限，所有请求都会被拒绝",
			logx.Str("permission", perm))
		// Deny unconditionally. Falling back to the raw token would make a typo an allow-list
		// entry: the auth store accepts arbitrary permission strings, so any role that happens to
		// contain the misspelled name would open the route. | 无条件拒绝。退回原始标识会让拼写
		// 错误变成白名单条目：auth 存储接受任意权限字符串，任何恰好含有该错误名称的角色都会
		// 打开这条路由。
		unregistered := perm
		return func(c *router.Ctx) {
			logx.Default().Warn("authz: refused a request guarded by an unregistered permission | authz：拒绝了守着未注册权限的请求",
				logx.Str("permission", unregistered))
			c.Abort()
			_ = c.JSON(http.StatusForbidden, map[string]string{"error": "forbidden | 禁止访问"})
		}
	}
	return auth.RequirePermission(auth.Permission(token))
}

// RequireAny guards a route with an OR of capabilities. Every reference is resolved at wiring time;
// one unknown reference makes the route refuse every request, for the same reason Require does. |
// RequireAny 用能力的「或」守卫路由。每个引用都在装配期解析；只要有一个未知引用，路由就拒绝所有请求，
// 理由与 Require 相同。
func RequireAny(perms ...string) router.Middleware {
	tokens := make([]auth.Permission, 0, len(perms))
	for _, perm := range perms {
		token, err := ResolvePermission(perm)
		if err != nil {
			logx.Default().Warn("authz: a route is guarded by an unregistered permission, every request is refused | authz：某路由守着未注册的权限，所有请求都会被拒绝",
				logx.Str("permission", perm))
			return func(c *router.Ctx) {
				c.Abort()
				_ = c.JSON(http.StatusForbidden, map[string]string{"error": "forbidden | 禁止访问"})
			}
		}
		tokens = append(tokens, auth.Permission(token))
	}
	return auth.RequireAnyPermission(tokens...)
}

// Allow takes the decision of one handler in a single line. | Allow 用一行完成处理器内的判定。
//
//	if err := authz.Allow(c, authz.PermUserUpdate); err != nil {
//		return err
//	}
//
// On refusal it writes 403 and returns ErrForbidden, and it writes the response only when nothing
// has been written yet, so a caller that is chained behind another writer cannot produce a second
// body on top of the first. | 拒绝时写出 403 并返回 ErrForbidden；
// 它只在尚未写出任何内容时才写响应，因此排在另一个写入者之后的调用方不会在已有正文上再叠一份。
//
// The body is the same {"error": …} shape the auth middleware answers with, and it names the
// permission rather than the resource, so it cannot leak whether the resource exists. | 正文与
// auth 中间件应答的 {"error": …} 同形，且它点出的是权限而不是资源，因此不会泄露资源是否存在。
//
// Use Require for a route that is guarded end to end: it renders the refusal through the
// application error handler, while Allow always answers JSON. | 需要端到端守卫的路由请用 Require：
// 它经应用的错误渲染器输出拒绝语，而 Allow 一律以 JSON 作答。
//
// A nil context - a handler called from a test without a request - is refused as well rather than
// panicking, which keeps the failure direction the same as the missing-subject case above. | 上下文为空时
// （测试中不使用请求直接调用的处理器）同样拒绝而不是 panic，这与上面「缺少主体」的失败方向一致。
func Allow(c *router.Ctx, perm string) error {
	if c != nil {
		if s, ok := SubjectOf(c); ok && s.Can(perm) {
			return nil
		}
	}
	err := errx.Detail(ErrForbidden, "missing permission %[1]q | 缺少权限 %[1]q", perm)
	if c != nil {
		c.Abort()
		if !c.Written() {
			if werr := c.JSON(http.StatusForbidden, map[string]string{"error": err.Error()}); werr != nil {
				logx.Default().Warn("authz: failed to write the refusal | authz：写出拒绝响应失败", logx.Err(werr))
			}
		}
	}
	return err
}

package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/router"
)

// errForbiddenWrapped returns an ErrForbidden that carries extra detail, which is
// how RequirePermission reports a denial. | errForbiddenWrapped 返回携带额外细节的 ErrForbidden，
// 这正是 RequirePermission 上报拒绝的方式。
func errForbiddenWrapped() error {
	return errx.Detail(ErrForbidden, "missing permission %[1]q | 缺少权限 %[1]q", "user:write")
}

// do sends one request through r and returns the recorded response. | do 通过 r 发送一次请求并返回记录到的响应。
func do(t *testing.T, r *router.Router, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// bearer returns the Authorization header of a token. | bearer 返回令牌对应的 Authorization 头部。
func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// usernameHandler writes the authenticated username, or fails the request. | usernameHandler 写出已认证用户名，未认证时让请求失败。
func usernameHandler(t *testing.T) router.HandlerFunc {
	t.Helper()
	return func(c *router.Ctx) {
		u, ok := CurrentUser(c)
		if !ok {
			if err := c.String(http.StatusUnauthorized, "no user"); err != nil {
				t.Errorf("write failed | 写响应失败: %v", err)
			}
			return
		}
		if err := c.String(http.StatusOK, u.Username); err != nil {
			t.Errorf("write failed | 写响应失败: %v", err)
		}
	}
}

// TestRequirePermissionReturns403 covers the 401/403/200 trio of a missing permission plus the response body. | TestRequirePermissionReturns403 覆盖缺失权限时的 401/403/200 三态与响应正文。
func TestRequirePermissionReturns403(t *testing.T) {
	f := newFixture(t)
	r := router.New()
	r.Use(Middleware(MiddlewareOptions{Issuer: f.issuer}))
	r.GET("/admin", usernameHandler(t), RequirePermission("user:write"))
	r.GET("/profile", usernameHandler(t), RequirePermission("user:read"))

	adminToken := mustIssue(t, f, f.admin)
	memberToken := mustIssue(t, f, f.member)

	// No token at all: the request never reaches the permission check. | 完全没有令牌：请求到不了权限判定。
	rec := do(t, r, http.MethodGet, "/admin", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token want 401, got %d | 无令牌期望 401，实际 %d", rec.Code, rec.Code)
	}

	// A valid token without the permission: 403, and the body says nothing about the
	// resource, because the resource was never looked up. | 令牌有效但权限不足：403，
	// 且正文不涉及任何资源，因为资源从未被查询。
	rec = do(t, r, http.MethodGet, "/admin", bearer(memberToken))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing permission want 403, got %d | 权限不足期望 403，实际 %d", rec.Code, rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ErrForbidden.Error()) {
		t.Fatalf("403 body want the ErrForbidden text, got %q | 403 正文期望 ErrForbidden 文案，实际 %q", body, body)
	}
	if strings.Contains(body, f.member.Username) {
		t.Fatalf("403 body leaked the authenticated username: %q | 403 正文泄露了已认证用户名：%q", body, body)
	}
	if strings.Contains(body, "admin") || strings.Contains(body, f.admin.ID) {
		t.Fatalf("403 body leaked the protected resource: %q | 403 正文泄露了受保护资源：%q", body, body)
	}

	// The same user on a route it does hold: 200. | 同一用户访问它确实拥有的权限路由：200。
	rec = do(t, r, http.MethodGet, "/profile", bearer(memberToken))
	if rec.Code != http.StatusOK || rec.Body.String() != f.member.Username {
		t.Fatalf("holding the permission want 200 %q, got %d %q | 持有权限时期望 200 %q，实际 %d %q",
			f.member.Username, rec.Code, rec.Body.String(), f.member.Username, rec.Code, rec.Body.String())
	}
	rec = do(t, r, http.MethodGet, "/admin", bearer(adminToken))
	if rec.Code != http.StatusOK || rec.Body.String() != f.admin.Username {
		t.Fatalf("admin want 200 %q, got %d %q | 管理员期望 200 %q，实际 %d %q",
			f.admin.Username, rec.Code, rec.Body.String(), f.admin.Username, rec.Code, rec.Body.String())
	}

	// Broken tokens are authentication failures, never authorisation ones. | 损坏的令牌属于认证失败，绝不是授权失败。
	badTokens := map[string]string{
		"garbage":        "not-a-token",
		"empty":          "",
		"foreign issuer": mustIssueWith(t, mustIssuer(t, f.svc, "somebody-else", f), f.admin),
	}
	for name, token := range badTokens {
		rec = do(t, r, http.MethodGet, "/admin", bearer(token))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s want 401, got %d | %s 期望 401，实际 %d", name, rec.Code, name, rec.Code)
		}
	}

	// A disabled user and a rotated key lose access immediately. | 被禁用的用户与已轮换的密钥立即失去访问权。
	requireNoError(t, f.svc.DisableUser(testCtx, f.tenantA.ID, f.admin.ID), "DisableUser")
	rec = do(t, r, http.MethodGet, "/admin", bearer(adminToken))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled user want 401, got %d | 被禁用的用户期望 401，实际 %d", rec.Code, rec.Code)
	}
	requireNoError(t, f.svc.RotateSecKey(testCtx, f.tenantA.ID, f.member.ID), "RotateSecKey")
	rec = do(t, r, http.MethodGet, "/profile", bearer(memberToken))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a rotated sec_key want 401, got %d | 已轮换 sec_key 期望 401，实际 %d", rec.Code, rec.Code)
	}
}

// mustIssuer builds an issuer with the given iss claim, failing the test on error. | mustIssuer 构建带指定 iss 声明的签发器，出错即终止测试。
func mustIssuer(t *testing.T, svc *Service, iss string, f *fixture) *TokenIssuer {
	t.Helper()
	issuer, err := NewTokenIssuer(svc, TokenOptions{Issuer: iss, TTL: DefaultTokenTTL, Now: f.clock.Now})
	if err != nil {
		t.Fatalf("NewTokenIssuer | NewTokenIssuer 失败: %v", err)
	}
	return issuer
}

// TestRequirePermissionUsesTheCustomErrorHandler checks that the permission check reuses a custom error renderer. | TestRequirePermissionUsesTheCustomErrorHandler 校验自定义错误渲染器被权限判定复用。
func TestRequirePermissionUsesTheCustomErrorHandler(t *testing.T) {
	f := newFixture(t)
	var seen []error
	handler := func(c *router.Ctx, err error) {
		seen = append(seen, err)
		status := http.StatusTeapot
		if errors.Is(err, ErrForbidden) {
			status = http.StatusForbidden
		}
		if writeErr := c.String(status, "custom:"+err.Error()); writeErr != nil {
			t.Errorf("write failed | 写响应失败: %v", writeErr)
		}
	}
	r := router.New()
	r.Use(Middleware(MiddlewareOptions{Issuer: f.issuer, ErrorHandler: handler}))
	r.GET("/admin", usernameHandler(t), RequirePermission("user:write"))

	memberToken := mustIssue(t, f, f.member)
	rec := do(t, r, http.MethodGet, "/admin", bearer(memberToken))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("custom handler want 403, got %d | 自定义渲染器期望 403，实际 %d", rec.Code, rec.Code)
	}
	if len(seen) != 1 || !errors.Is(seen[0], ErrForbidden) {
		t.Fatalf("custom handler saw %v | 自定义渲染器收到 %v", seen, seen)
	}
	if !strings.HasPrefix(rec.Body.String(), "custom:") {
		t.Fatalf("the custom body was not used: %q | 未使用自定义正文：%q", rec.Body.String(), rec.Body.String())
	}
	// The same renderer serves authentication failures. | 同一个渲染器也服务认证失败。
	seen = nil
	rec = do(t, r, http.MethodGet, "/admin", nil)
	if len(seen) != 1 || rec.Code != http.StatusTeapot {
		t.Fatalf("an unauthenticated request want the custom status, got %d with %v | 未认证请求期望自定义状态码，实际 %d 与 %v",
			rec.Code, seen, rec.Code, seen)
	}
}

// TestMiddlewareStoresTheCurrentUser checks that the authenticated user lands in the router context. | TestMiddlewareStoresTheCurrentUser 校验认证结果落入路由上下文。
func TestMiddlewareStoresTheCurrentUser(t *testing.T) {
	f := newFixture(t)
	const customKey = "auth.custom"
	r := router.New()
	r.Use(Middleware(MiddlewareOptions{Issuer: f.issuer, CtxKey: customKey}))
	r.GET("/me", func(c *router.Ctx) {
		standard, ok := CurrentUser(c)
		if !ok {
			t.Error("CurrentUser found no user under the default key | CurrentUser 在默认键下找不到用户")
		}
		custom, _ := c.Get(customKey)
		viaCustom, ok := custom.(*User)
		if !ok || viaCustom.ID != f.member.ID {
			t.Errorf("the custom context key holds %+v | 自定义上下文键保存的是 %+v", custom, custom)
		}
		if ok && standard != nil && standard.ID != viaCustom.ID {
			t.Errorf("the two keys disagree: %q vs %q | 两个键不一致：%q 与 %q", standard.ID, viaCustom.ID, standard.ID, viaCustom.ID)
		}
		if err := c.String(http.StatusOK, "ok"); err != nil {
			t.Errorf("write failed | 写响应失败: %v", err)
		}
	})
	rec := do(t, r, http.MethodGet, "/me", bearer(mustIssue(t, f, f.member)))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d | 期望 200，实际 %d", rec.Code, rec.Code)
	}

	// Outside the middleware nothing is stored. | 未经中间件时上下文中没有任何用户。
	bare := router.New()
	bare.GET("/me", func(c *router.Ctx) {
		if u, ok := CurrentUser(c); ok || u != nil {
			t.Errorf("CurrentUser reported %+v without the middleware | 未经中间件时 CurrentUser 报告了 %+v", u, u)
		}
		if err := c.String(http.StatusOK, "bare"); err != nil {
			t.Errorf("write failed | 写响应失败: %v", err)
		}
	})
	rec = do(t, bare, http.MethodGet, "/me", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d | 期望 200，实际 %d", rec.Code, rec.Code)
	}

	// A permission check without the authentication middleware is a wiring fault. | 缺少认证中间件时进行权限判定属于装配错误。
	orphan := router.New()
	orphan.GET("/admin", usernameHandler(t), RequirePermission("user:read"))
	rec = do(t, orphan, http.MethodGet, "/admin", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("RequirePermission without Middleware want 401, got %d | 缺少 Middleware 的 RequirePermission 期望 401，实际 %d", rec.Code, rec.Code)
	}

	// Middleware without an issuer refuses everything instead of panicking. | 未配置签发器的 Middleware 拒绝一切，而不是 panic。
	unwired := router.New()
	unwired.Use(Middleware(MiddlewareOptions{}))
	unwired.GET("/me", usernameHandler(t))
	if rec := do(t, unwired, http.MethodGet, "/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unwired Middleware want 401, got %d | 未装配的 Middleware 期望 401，实际 %d", rec.Code, rec.Code)
	}
	if rec := do(t, unwired, http.MethodGet, "/me", bearer(mustIssue(t, f, f.member))); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unwired Middleware want 401 even with a token, got %d | 未装配的 Middleware 即使带令牌也期望 401，实际 %d", rec.Code, rec.Code)
	}
}

// TestMiddlewareTokenLookupForms covers every source a TokenLookup can read a token from. | TestMiddlewareTokenLookupForms 覆盖 TokenLookup 的各种取值来源。
func TestMiddlewareTokenLookupForms(t *testing.T) {
	f := newFixture(t)
	token := mustIssue(t, f, f.member)

	cases := []struct {
		name    string
		lookup  string
		headers map[string]string
		target  string
		cookie  *http.Cookie
		want    int
	}{
		{name: "default scheme", lookup: "", headers: bearer(token), target: "/me", want: http.StatusOK},
		{name: "lowercase scheme", lookup: "", headers: map[string]string{"Authorization": "bearer " + token}, target: "/me", want: http.StatusOK},
		{name: "missing scheme", lookup: "", headers: map[string]string{"Authorization": token}, target: "/me", want: http.StatusUnauthorized},
		{name: "bare header", lookup: "X-Token", headers: map[string]string{"X-Token": token}, target: "/me", want: http.StatusOK},
		{name: "header without prefix", lookup: "header:X-Token:", headers: map[string]string{"X-Token": token}, target: "/me", want: http.StatusOK},
		{name: "header with scheme", lookup: "header:X-Token:Token ", headers: map[string]string{"X-Token": "Token " + token}, target: "/me", want: http.StatusOK},
		{name: "wrong header", lookup: "header:X-Token:", headers: bearer(token), target: "/me", want: http.StatusUnauthorized},
		{name: "query", lookup: "query:token", target: "/me?token=" + token, want: http.StatusOK},
		{name: "query missing", lookup: "query:token", target: "/me", want: http.StatusUnauthorized},
		{name: "cookie", lookup: "cookie:jwt", target: "/me", cookie: &http.Cookie{Name: "jwt", Value: token}, want: http.StatusOK},
		{name: "cookie missing", lookup: "cookie:jwt", target: "/me", want: http.StatusUnauthorized},
		// A malformed lookup specification is a wiring fault, so it is a 400. | 非法的提取配置属于装配错误，因此是 400。
		{name: "blank query name", lookup: "query:", target: "/me", want: http.StatusBadRequest},
		{name: "blank cookie name", lookup: "cookie:", target: "/me", want: http.StatusBadRequest},
		{name: "blank header name", lookup: "header:", target: "/me", want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		r := router.New()
		r.Use(Middleware(MiddlewareOptions{Issuer: f.issuer, TokenLookup: tc.lookup}))
		r.GET("/me", usernameHandler(t))
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		if tc.cookie != nil {
			req.AddCookie(tc.cookie)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: want %d, got %d (%q) | %s：期望 %d，实际 %d（%q）",
				tc.name, tc.want, rec.Code, rec.Body.String(), tc.name, tc.want, rec.Code, rec.Body.String())
		}
		if tc.want == http.StatusOK && rec.Body.String() != f.member.Username {
			t.Errorf("%s: body want %q, got %q | %s：正文期望 %q，实际 %q",
				tc.name, f.member.Username, rec.Body.String(), tc.name, f.member.Username, rec.Body.String())
		}
	}

	// The exported default lookup is the documented one. | 导出的默认提取方式即文档所述的那一种。
	if DefaultTokenLookup != "header:Authorization:Bearer " {
		t.Fatalf("DefaultTokenLookup changed to %q | DefaultTokenLookup 变为 %q", DefaultTokenLookup, DefaultTokenLookup)
	}
	if DefaultCtxKey != "auth.user" {
		t.Fatalf("DefaultCtxKey changed to %q | DefaultCtxKey 变为 %q", DefaultCtxKey, DefaultCtxKey)
	}
}

// TestDefaultErrorHandlerStatusMapping checks the mapping from an error onto a status code. | TestDefaultErrorHandlerStatusMapping 校验错误到状态码的映射。
func TestDefaultErrorHandlerStatusMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{ErrForbidden, http.StatusForbidden},
		{ErrNotFound, http.StatusNotFound},
		{ErrConflict, http.StatusConflict},
		{ErrWeakPassword, http.StatusBadRequest},
		{ErrInvalidInput, http.StatusBadRequest},
		{ErrTenantRequired, http.StatusBadRequest},
		{ErrInvalidCredentials, http.StatusUnauthorized},
		{ErrUserDisabled, http.StatusUnauthorized},
		{ErrSecKeyMissing, http.StatusUnauthorized},
		{ErrTokenInvalid, http.StatusUnauthorized},
		{ErrNilStore, http.StatusUnauthorized},
		{ErrMasterKeyRequired, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		if got := statusFor(tc.err); got != tc.want {
			t.Errorf("statusFor(%v) = %d, want %d | statusFor(%v) = %d，期望 %d", tc.err, got, tc.want, tc.err, got, tc.want)
		}
	}
	// A wrapped sentinel keeps its classification. | 被包装的哨兵保留其分类。
	if got := statusFor(errForbiddenWrapped()); got != http.StatusForbidden {
		t.Fatalf("a wrapped ErrForbidden want 403, got %d | 被包装的 ErrForbidden 期望 403，实际 %d", got, got)
	}
}

// TestCurrentUserCarriesTheStoredRecord checks that the user in the context carries the full stored record. | TestCurrentUserCarriesTheStoredRecord 校验上下文中的用户带有完整的库中字段。
func TestCurrentUserCarriesTheStoredRecord(t *testing.T) {
	f := newFixture(t)
	r := router.New()
	r.Use(Middleware(MiddlewareOptions{Issuer: f.issuer}))
	stashed := make(chan *User, 1)
	r.GET("/me", func(c *router.Ctx) {
		u, ok := CurrentUser(c)
		if !ok {
			t.Error("no user in the context | 上下文中没有用户")
			return
		}
		stashed <- u
		if err := c.String(http.StatusOK, "ok"); err != nil {
			t.Errorf("write failed | 写响应失败: %v", err)
		}
	})
	rec := do(t, r, http.MethodGet, "/me", bearer(mustIssue(t, f, f.member)))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d | 期望 200，实际 %d", rec.Code, rec.Code)
	}
	select {
	case u := <-stashed:
		if u.ID != f.member.ID || u.TenantID != f.tenantA.ID || u.Status != StatusActive {
			t.Fatalf("the stored user is %+v | 存入的用户是 %+v", u, u)
		}
		if len(u.Roles) != 1 || u.Roles[0] != "member" || len(u.EncryptedSecKey) == 0 {
			t.Fatalf("the stored user is missing stored fields: %+v | 存入的用户缺少库中字段：%+v", u, u)
		}
	default:
		t.Fatal("the handler did not run | 处理器未执行")
	}
}

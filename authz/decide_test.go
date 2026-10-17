package authz_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shibingli/yd/auth"
	"github.com/shibingli/yd/authz"
	"github.com/shibingli/yd/router"
)

// fixture is one tenant with one reader holding user:read and one bare member holding nothing. | fixture
// 是一个租户，内含一个持有 user:read 的读者与一个什么都不持有的普通成员。
//
// It is built from the exported API of the auth package only, so the test proves the decision helpers
// work against a service that was wired the way an application wires one. | 它只使用 auth 包的导出 API 构建，
// 因此测试证明的是：判定辅助函数在「按应用的方式装配起来的」服务上工作。
//
// The role is a *registered* one on purpose: the decision helpers resolve every reference against the
// registry first, so a role the registry does not know answers "not held" no matter what the store
// says. | 角色刻意选用「已注册」的那一个：判定辅助函数先把每个引用对着注册表解析，
// 因此注册表不认识的角色的答案必然是「未持有」，无论存储里写的是什么。
type fixture struct {
	svc         *auth.Service
	issuer      *auth.TokenIssuer
	tenantID    string
	reader      *auth.User
	member      *auth.User
	readerToken string
	memberToken string
}

// newFixture builds the tenant, the role, the two users and their tokens. | newFixture 构建租户、角色、两个用户及其令牌。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	store := auth.NewMemoryStore()
	svc, err := auth.NewService(store, auth.Options{MasterKey: []byte("0123456789abcdef0123456789abcdef")})
	if err != nil {
		t.Fatalf("NewService failed | 构建服务失败: %v", err)
	}
	tenant, err := svc.CreateTenant(ctx, "acme", "Acme | 弘毅")
	if err != nil {
		t.Fatalf("CreateTenant failed: %v", err)
	}
	if err := store.UpsertRole(ctx, &auth.Role{
		ID:          "rl_reader",
		TenantID:    tenant.ID,
		Name:        authz.RoleMember,
		Description: "Reads users | 读取用户",
		Permissions: []string{authz.PermUserRead, authz.PermRoleRead},
	}); err != nil {
		t.Fatalf("UpsertRole failed: %v", err)
	}
	const password = "correct horse battery"
	reader, err := svc.CreateUser(ctx, tenant.ID, auth.CreateUserInput{
		Username: "reader",
		Password: password,
		Roles:    []string{authz.RoleMember},
	})
	if err != nil {
		t.Fatalf("CreateUser(reader) failed: %v", err)
	}
	member, err := svc.CreateUser(ctx, tenant.ID, auth.CreateUserInput{Username: "member", Password: password})
	if err != nil {
		t.Fatalf("CreateUser(member) failed: %v", err)
	}
	issuer, err := auth.NewTokenIssuer(svc, auth.TokenOptions{})
	if err != nil {
		t.Fatalf("NewTokenIssuer failed: %v", err)
	}
	issue := func(username string) string {
		res, err := svc.Login(ctx, tenant.ID, username, password, issuer)
		if err != nil {
			t.Fatalf("Login(%s) failed: %v", username, err)
		}
		return res.Token
	}
	return &fixture{
		svc:         svc,
		issuer:      issuer,
		tenantID:    tenant.ID,
		reader:      reader,
		member:      member,
		readerToken: issue("reader"),
		memberToken: issue("member"),
	}
}

// do sends one request through r and returns the recorded response. | do 通过 r 发送一次请求并返回记录到的响应。
func do(t *testing.T, r *router.Router, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestSubjectOfAnonymous 要求缺少主体与匿名主体一律回答「否」，且从不 panic。
//
// A page rendered without a sign-in is a normal page, so the failure mode of missing wiring must be
// indistinguishable from that of a missing permission. | 未登录时渲染页面是正常页面，
// 因此漏装配的失败形态必须与缺权限完全一致。
func TestSubjectOfAnonymous(t *testing.T) {
	svc := mustService(t)
	if authz.Can(context.Background(), authz.PermUserRead) {
		t.Errorf("a bare context holds a permission | 空上下文持有权限")
	}
	if authz.HasRole(context.Background(), authz.RoleAdmin) {
		t.Errorf("a bare context holds a role | 空上下文持有角色")
	}
	var anonymous *authz.Subject
	if anonymous.Can(authz.PermUserRead) || anonymous.HasRole(authz.RoleAdmin) {
		t.Errorf("a nil subject holds something | 空主体持有内容")
	}
	if !anonymous.Anonymous() {
		t.Errorf("a nil subject is not anonymous | 空主体不是匿名")
	}
	if len(anonymous.Permissions()) != 0 || len(anonymous.Roles()) != 0 {
		t.Errorf("a nil subject lists permissions or roles | 空主体列出了权限或角色")
	}
	subject := authz.NewSubject(svc, nil)
	if !subject.Anonymous() || subject.Can(authz.PermUserRead) {
		t.Errorf("a subject without a user holds something | 没有用户的主体持有内容")
	}
	if _, ok := authz.SubjectOf(nil); ok {
		t.Errorf("SubjectOf(nil) reported a subject | SubjectOf(nil) 报告了主体")
	}
}

// TestWithSubjectRoundTrip requires a plain context to carry the subject too. | TestWithSubjectRoundTrip 要求普通 context 也能携带主体。
func TestWithSubjectRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := authz.WithSubject(context.Background(), authz.NewSubject(f.svc, f.reader))
	got, ok := authz.SubjectOf(ctx)
	if !ok {
		t.Fatalf("SubjectOf lost the subject | SubjectOf 丢失了主体")
	}
	if got.User() == nil || got.User().ID != f.reader.ID {
		t.Errorf("SubjectOf returned another user | SubjectOf 返回了另一个用户")
	}
	if !authz.Can(ctx, authz.PermUserRead) {
		t.Errorf("the reader does not hold user:read | 读者未持有 user:read")
	}
	if authz.Can(ctx, authz.PermUserDelete) {
		t.Errorf("the reader holds user:delete | 读者持有 user:delete")
	}
	if !authz.HasRole(ctx, authz.RoleMember) {
		t.Errorf("the member role is missing | 缺少 member 角色")
	}
	if authz.HasRole(ctx, authz.RoleAdmin) {
		t.Errorf("the reader holds the admin role | 读者持有 admin 角色")
	}
}

// TestSubjectReferencesInEverySpelling 要求四种写法给出同一个答案。
//
// A template passes the data key, JavaScript passes the JS key, Go passes the constant and the
// database stores the token; if they could disagree, the front end would hide a button the back end
// would allow. | 模板传数据键，JavaScript 传 JS 键，Go 传常量，数据库存标识；
// 若它们可能不一致，前端就会隐藏一个后端本会放行的按钮。
func TestSubjectReferencesInEverySpelling(t *testing.T) {
	f := newFixture(t)
	s := authz.NewSubject(f.svc, f.reader)
	for _, ref := range []string{
		authz.PermUserRead,
		authz.ConstantName(authz.PermUserRead),
		authz.DataKey(authz.PermUserRead),
		authz.JSKey(authz.PermUserRead),
	} {
		if !s.Can(ref) {
			t.Errorf("Can(%q) = false, want true", ref)
		}
	}
	for _, ref := range []string{authz.RoleMember, "RoleMember", "Member", "MEMBER"} {
		if !s.HasRole(ref) {
			t.Errorf("HasRole(%q) = false, want true", ref)
		}
	}
	if s.Can("user:creat") || s.Can("") {
		t.Errorf("an unknown or empty reference was granted | 未知或空引用被授予了")
	}
	if s.HasRole("nobody") || s.HasRole("") {
		t.Errorf("an unknown or empty role was granted | 未知或空角色被授予了")
	}
	if got := s.Permissions(); len(got) != 2 ||
		got[0] != authz.PermRoleRead || got[1] != authz.PermUserRead {
		t.Errorf("Permissions() = %v, want the two sorted capabilities", got)
	}
	if got := s.Roles(); len(got) != 1 || got[0] != authz.RoleMember {
		t.Errorf("Roles() = %v", got)
	}
}

// TestDisabledUserHoldsNothing requires a disabled user to lose every capability at once. | TestDisabledUserHoldsNothing 要求被禁用的用户立即失去一切能力。
func TestDisabledUserHoldsNothing(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.DisableUser(context.Background(), f.tenantID, f.reader.ID); err != nil {
		t.Fatalf("DisableUser failed: %v", err)
	}
	disabled, err := f.svc.GetUser(context.Background(), f.tenantID, f.reader.ID)
	if err != nil {
		t.Fatalf("GetUser failed: %v", err)
	}
	s := authz.NewSubject(f.svc, disabled)
	if s.Can(authz.PermUserRead) {
		t.Errorf("a disabled user still holds a capability | 被禁用的用户仍持有能力")
	}
}

// TestMiddlewareStoresTheSubject requires the middleware to turn the auth-context user into the decision subject. | TestMiddlewareStoresTheSubject 要求中间件把 auth 上下文里的用户装成判定主体。
func TestMiddlewareStoresTheSubject(t *testing.T) {
	f := newFixture(t)
	r := router.New()
	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: f.issuer}))
	r.Use(authz.Middleware(f.svc))
	var seen *authz.Subject
	r.GET("/who", func(c *router.Ctx) {
		seen, _ = authz.SubjectOf(c)
		if err := c.String(http.StatusOK, "ok | 好"); err != nil {
			t.Errorf("write failed: %v", err)
		}
	})

	if rec := do(t, r, "/who", f.readerToken); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %[1]d: %[2]s | 期望 200，实际 %[1]d：%[2]s", rec.Code, rec.Body.String())
	}
	if seen == nil || seen.User() == nil || seen.User().ID != f.reader.ID {
		t.Fatalf("the middleware stored %+[1]v | 中间件存入了 %+[1]v", seen)
	}
	if !seen.Can(authz.PermUserRead) {
		t.Errorf("the subject of the request holds nothing | 请求的主体不持有任何能力")
	}
}

// TestMiddlewareKeepsAnInjectedSubject 要求自行构建主体的处理器不被中间件覆盖。
//
// An impersonated request or a background job builds its own subject; the middleware running after it
// must not quietly replace the decision. | 模拟登录的请求或后台任务会自行构建主体；
// 在其后运行的中间件绝不能悄悄替换掉这个判定。
func TestMiddlewareKeepsAnInjectedSubject(t *testing.T) {
	f := newFixture(t)
	r := router.New()
	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: f.issuer}))
	r.Use(func(c *router.Ctx) {
		c.Set(authz.CtxKey, authz.NewSubject(f.svc, f.reader))
		c.Next()
	})
	r.Use(authz.Middleware(f.svc))

	var got bool
	r.GET("/who", func(c *router.Ctx) {
		got = authz.Can(c, authz.PermRoleRead)
		if err := c.String(http.StatusOK, "ok"); err != nil {
			t.Errorf("write failed: %v", err)
		}
	})
	// The member presents the token, the injected subject decides: the injected reader wins.
	if rec := do(t, r, "/who", f.memberToken); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !got {
		t.Errorf("the middleware replaced the injected subject | 中间件替换掉了注入的主体")
	}
}

// TestAllowRefusesAndDoesNotLeak requires Allow to refuse with 403, carry the sentinel and name the permission rather than the resource. | TestAllowRefusesAndDoesNotLeak 要求 Allow 的拒绝是 403、携带哨兵、且点的是权限而不是资源。
func TestAllowRefusesAndDoesNotLeak(t *testing.T) {
	f := newFixture(t)
	r := router.New()
	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: f.issuer}))
	r.Use(authz.Middleware(f.svc))
	var errSeen error
	r.GET("/users/42", func(c *router.Ctx) {
		if err := authz.Allow(c, authz.PermUserDelete); err != nil {
			errSeen = err
			return
		}
		if err := c.String(http.StatusOK, "gone"); err != nil {
			t.Errorf("write failed: %v", err)
		}
	})

	rec := do(t, r, "/users/42", f.readerToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %[1]d: %[2]s | 期望 403，实际 %[1]d：%[2]s", rec.Code, rec.Body.String())
	}
	if !errors.Is(errSeen, authz.ErrForbidden) {
		t.Fatalf("Allow returned %[1]v, want ErrForbidden | Allow 返回 %[1]v，期望 ErrForbidden", errSeen)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the refusal is not the {\"error\": …} shape: %v | 拒绝语不是 {\"error\": …} 形态：%v", err, rec.Body.String())
	}
	message := body["error"]
	if !strings.Contains(message, authz.PermUserDelete) {
		t.Errorf("the refusal does not name the missing permission: %q | 拒绝语未点出缺失的权限：%q", message, message)
	}
	if strings.Contains(message, "42") || strings.Contains(message, f.reader.Username) {
		t.Errorf("the refusal leaked the resource or the user: %q | 拒绝语泄露了资源或用户：%q", message, message)
	}

	// The member holds no role at all: refused just the same, with the same shape.
	rec = do(t, r, "/users/42", f.memberToken)
	if rec.Code != http.StatusForbidden {
		t.Errorf("member want 403, got %d", rec.Code)
	}
	// A request without a token never reaches the handler: the auth middleware upstream answers 401
	// first, which is the one refusal that is about the caller and not about the resource.
	// 不带令牌的请求根本到不了处理器：上游的 auth 中间件先答 401——这是唯一只关于调用方、
	// 与资源无关的那种拒绝。
	if rec = do(t, r, "/users/42", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous want 401 from the auth middleware, got %d", rec.Code)
	}
}

// TestAllowPassesWhenHeld requires Allow to pass while the capability is held and to write no refusal text. | TestAllowPassesWhenHeld 要求持有能力时 Allow 放行、不写任何拒绝语。
func TestAllowPassesWhenHeld(t *testing.T) {
	f := newFixture(t)
	r := router.New()
	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: f.issuer}))
	r.Use(authz.Middleware(f.svc))
	r.GET("/users", func(c *router.Ctx) {
		if err := authz.Allow(c, authz.PermUserRead); err != nil {
			t.Errorf("Allow refused a held permission: %[1]v | Allow 拒绝了本已持有的权限：%[1]v", err)
			return
		}
		if err := c.String(http.StatusOK, "list | 列表"); err != nil {
			t.Errorf("write failed: %v", err)
		}
	})
	rec := do(t, r, "/users", f.readerToken)
	if rec.Code != http.StatusOK || rec.Body.String() != "list | 列表" {
		t.Fatalf("want 200 list, got %d %q", rec.Code, rec.Body.String())
	}
}

// TestAllowDoesNotOverwriteAResponse 要求 Allow 只在尚未写出内容时才写拒绝语。
//
// A stream or an SSE handler may already have written a prefix when a later guard refuses; a second
// body on top of the first would corrupt the response. | 流式或 SSE 处理器在后面的守卫拒绝时可能已经写下了前缀；
// 在已有正文之上再叠一份会破坏响应。
func TestAllowDoesNotOverwriteAResponse(t *testing.T) {
	f := newFixture(t)
	r := router.New()
	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: f.issuer}))
	r.Use(authz.Middleware(f.svc))
	r.GET("/stream", func(c *router.Ctx) {
		if err := c.String(http.StatusOK, "chunk-1"); err != nil {
			t.Errorf("write failed: %v", err)
		}
		if err := authz.Allow(c, authz.PermUserDelete); !errors.Is(err, authz.ErrForbidden) {
			t.Errorf("Allow = %v, want ErrForbidden", err)
		}
	})
	rec := do(t, r, "/stream", f.readerToken)
	if rec.Body.String() != "chunk-1" {
		t.Fatalf("the body was overwritten: %q | 正文被覆盖：%q", rec.Body.String(), rec.Body.String())
	}
}

// TestAllowWithoutCtx requires Allow to return the sentinel instead of panicking on an empty context. | TestAllowWithoutCtx 要求 Allow 在上下文为空时仍然返回哨兵而不是 panic。
func TestAllowWithoutCtx(t *testing.T) {
	if err := authz.Allow(nil, authz.PermUserRead); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("Allow(nil) = %v, want ErrForbidden", err)
	}
}

// TestRequireGuardsTheRoute requires Require's refusal to render 403 through the error renderer, with the handler never running. | TestRequireGuardsTheRoute 要求 Require 的拒绝经错误渲染器输出 403，且处理器从未运行。
func TestRequireGuardsTheRoute(t *testing.T) {
	f := newFixture(t)
	r := router.New()
	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: f.issuer}))
	r.Use(authz.Middleware(f.svc))
	ran := false
	r.GET("/admin", func(c *router.Ctx) {
		ran = true
		if err := c.String(http.StatusOK, "secret"); err != nil {
			t.Errorf("write failed: %v", err)
		}
	}, authz.Require(authz.PermUserDelete))

	rec := do(t, r, "/admin", f.readerToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d %q", rec.Code, rec.Body.String())
	}
	if ran {
		t.Fatalf("the guarded handler ran anyway | 受守卫的处理器仍然运行了")
	}
	if rec = do(t, r, "/admin", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous want 401 from the auth middleware, got %d", rec.Code)
	}
}

// TestRequireWithAnUnregisteredReference 要求未注册的引用仍然守住路由：日志告警，且请求被拒。
//
// A typo must never become "silently permissive": failing closed is the only acceptable direction for
// a guard, and the warning is what points at the line to fix. | 拼写错误绝不能变成「静默放行」：
// 对守卫而言失败关闭是唯一可接受的方向，而那条日志告警正是要指出该改哪一行。
func TestRequireWithAnUnregisteredReference(t *testing.T) {
	f := newFixture(t)
	r := router.New()
	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: f.issuer}))
	r.Use(authz.Middleware(f.svc))
	r.GET("/typo", func(c *router.Ctx) {
		if err := c.String(http.StatusOK, "reached"); err != nil {
			t.Errorf("write failed: %v", err)
		}
	}, authz.Require("user:creat"))

	if rec := do(t, r, "/typo", f.readerToken); rec.Code != http.StatusForbidden {
		t.Fatalf("a route guarded by a typo want 403, got %[1]d: %[2]s | 由拼错权限守卫的路由期望 403，实际 %[1]d：%[2]s", rec.Code, rec.Body.String())
	}
}

// mustService returns a bare service for the tests that need one but no data. | mustService 返回一个仅供
// 需要服务但不需要数据的测试使用的空服务。
func mustService(t *testing.T) *auth.Service {
	t.Helper()
	svc, err := auth.NewService(auth.NewMemoryStore(), auth.Options{MasterKey: []byte("0123456789abcdef0123456789abcdef")})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	return svc
}

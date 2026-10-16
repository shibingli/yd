package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibingli/yd/jwt"
)

// ---------------------------------------------------------------- Shared fixtures | 共享夹具

const (
	// testMasterKey is the fixed ASCII master key of every fixture. | testMasterKey 是全部夹具使用的固定 ASCII 主密钥。
	testMasterKey = "yd-auth-test-master-key-0123456789"
	// testIterations keeps PBKDF2 cheap, so the suite stays fast under -race. | testIterations 让 PBKDF2 保持廉价，使整套测试在 -race 下依然快速。
	testIterations = 1000
	// testPassword is the sign-in password of every fixture user. | testPassword 是全部夹具用户的登录口令。
	testPassword = "yd-test-password"
	// testWrongPassword is well-formed but matches nobody. | testWrongPassword 格式合法，但不匹配任何人。
	testWrongPassword = "yd-test-password-wrong"
	// testIssuer is the iss claim of the fixture issuer. | testIssuer 是夹具签发器的 iss 声明。
	testIssuer = "yd-auth-test"
	// testGCMTagLen is the SM4-GCM authentication tag length of the sealed sec_key layout. | testGCMTagLen 是封装 sec_key 布局中 SM4-GCM 认证标签的长度。
	testGCMTagLen = 16
)

// testStart is the origin of the fixture clock. | testStart 是夹具时钟的起点。
var testStart = time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)

// testCtx is the context used by every fixture call. | testCtx 是全部夹具调用使用的上下文。
var testCtx = context.Background()

// testClock is a manually advanced clock, so that token expiry is deterministic. | testClock 是手动推进的时钟，使令牌过期可确定复现。
type testClock struct {
	now time.Time
	mu  sync.Mutex
}

// newTestClock returns a clock frozen at testStart. | newTestClock 返回冻结在 testStart 的时钟。
func newTestClock() *testClock { return &testClock{now: testStart} }

// Now returns the current instant. | Now 返回当前时刻。
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward and returns the new instant. | Advance 向前推进时钟并返回新的时刻。
func (c *testClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// fixture bundles the store, the service, the issuer and the entities shared by
// the tests of this package. | fixture 汇总本包测试共用的 Store、Service、签发器与实体。
//
// Tenant A owns the roles "admin" and "member"; tenant B owns a role that is also
// called "member" but grants something else, so a leak between the two tenants can
// never hide behind identical role names. | 租户 A 拥有 "admin" 与 "member" 两个角色；租户 B 也拥有
// 一个名为 "member" 的角色却授予不同权限，因此两个租户之间的泄漏无法借同名角色蒙混过关。
type fixture struct {
	clock       *testClock
	store       *memoryStore
	svc         *Service
	issuer      *TokenIssuer
	tenantA     *Tenant
	tenantB     *Tenant
	adminRole   *Role
	memberRole  *Role
	bMemberRole *Role
	admin       *User
	member      *User
	stranger    *User
}

// newFixture builds the shared fixture. | newFixture 构建共享夹具。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	clock := newTestClock()
	svc, store := newTestService(t, Options{Now: clock.Now})
	f := &fixture{clock: clock, store: store, svc: svc}
	f.tenantA = mustTenant(t, svc, "tenant-a", "Tenant A")
	f.tenantB = mustTenant(t, svc, "tenant-b", "Tenant B")
	f.adminRole = mustRole(t, svc, f.tenantA.ID, "admin", "user:read", "user:write", "role:write")
	f.memberRole = mustRole(t, svc, f.tenantA.ID, "member", "user:read")
	f.bMemberRole = mustRole(t, svc, f.tenantB.ID, "member", "billing:read")
	f.admin = mustUser(t, svc, f.tenantA.ID, "admin", "admin")
	f.member = mustUser(t, svc, f.tenantA.ID, "member", "member")
	f.stranger = mustUser(t, svc, f.tenantB.ID, "stranger", "member")
	issuer, err := NewTokenIssuer(svc, TokenOptions{Issuer: testIssuer, TTL: time.Hour, Now: clock.Now})
	if err != nil {
		t.Fatalf("NewTokenIssuer | NewTokenIssuer 失败: %v", err)
	}
	f.issuer = issuer
	return f
}

// ------------------------------------------------ Fixture builders | 夹具构建辅助

// newTestService returns a Service over a fresh memory store, filling in the
// fixture master key and a cheap iteration count. | newTestService 返回基于全新内存 Store 的
// Service，并补齐夹具主密钥与廉价的迭代次数。
func newTestService(t *testing.T, o Options) (*Service, *memoryStore) {
	t.Helper()
	if len(o.MasterKey) == 0 {
		o.MasterKey = []byte(testMasterKey)
	}
	if o.Iterations == 0 {
		o.Iterations = testIterations
	}
	store := newMemoryStore()
	svc, err := NewService(store, o)
	if err != nil {
		t.Fatalf("NewService | NewService 失败: %v", err)
	}
	return svc, store
}

// mustTenant creates a tenant. | mustTenant 创建租户。
func mustTenant(t *testing.T, svc *Service, slug, name string) *Tenant {
	t.Helper()
	tn, err := svc.CreateTenant(testCtx, slug, name)
	if err != nil {
		t.Fatalf("CreateTenant(%q) | CreateTenant(%q) 失败: %v", slug, slug, err)
	}
	return tn
}

// mustRole upserts a role with the given permissions. | mustRole 创建或更新带指定权限的角色。
func mustRole(t *testing.T, svc *Service, tenantID, name string, perms ...string) *Role {
	t.Helper()
	r, err := svc.UpsertRole(testCtx, tenantID, &Role{Name: name, Permissions: perms})
	if err != nil {
		t.Fatalf("UpsertRole(%q) | UpsertRole(%q) 失败: %v", name, name, err)
	}
	return r
}

// mustUser creates a user with testPassword. | mustUser 用 testPassword 创建用户。
func mustUser(t *testing.T, svc *Service, tenantID, username string, roles ...string) *User {
	t.Helper()
	u, err := svc.CreateUser(testCtx, tenantID, CreateUserInput{
		Username:    username,
		DisplayName: username,
		Password:    testPassword,
		Roles:       roles,
	})
	if err != nil {
		t.Fatalf("CreateUser(%q) | CreateUser(%q) 失败: %v", username, username, err)
	}
	return u
}

// mustIssue signs a token for u with the fixture issuer. | mustIssue 用夹具签发器为 u 签发令牌。
func mustIssue(t *testing.T, f *fixture, u *User) string {
	t.Helper()
	return mustIssueWith(t, f.issuer, u)
}

// mustIssueWith signs a token for u with issuer. | mustIssueWith 用 issuer 为 u 签发令牌。
func mustIssueWith(t *testing.T, issuer *TokenIssuer, u *User) string {
	t.Helper()
	token, _, err := issuer.Issue(testCtx, u)
	if err != nil {
		t.Fatalf("Issue(%q) | Issue(%q) 失败: %v", u.Username, u.Username, err)
	}
	return token
}

// mustSecKey returns the plaintext sec_key of u and fails the test on error. | mustSecKey 返回 u 的明文 sec_key，出错即终止测试。
func mustSecKey(t *testing.T, f *fixture, u *User) []byte {
	t.Helper()
	key, err := f.svc.SecKey(testCtx, u)
	if err != nil {
		t.Fatalf("SecKey(%q) | SecKey(%q) 失败: %v", u.Username, u.Username, err)
	}
	return key
}

// parseOptions mirrors the verification policy of TokenIssuer. | parseOptions 与 TokenIssuer 的校验策略保持一致。
func parseOptions(f *fixture) jwt.ParseOptions {
	return jwt.ParseOptions{
		ValidAlgs:  []jwt.Alg{jwt.HS256},
		Issuer:     testIssuer,
		RequireExp: true,
		Now:        f.clock.Now,
	}
}

// keyFuncFor returns a KeyFunc that always hands back key. | keyFuncFor 返回恒为 key 的 KeyFunc。
func keyFuncFor(key []byte) jwt.KeyFunc {
	return func(jwt.Header) (any, error) { return key, nil }
}

// ------------------------------------------------ Assertions | 断言辅助

// requireIs fails the test unless err is (or wraps) target. | requireIs 在 err 不是（或未包装）target 时终止测试。
func requireIs(t *testing.T, err, target error, what string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: want %v, got %v | %s：期望 %v，实际 %v", what, target, err, what, target, err)
	}
}

// requireNoError fails the test when err is non-nil. | requireNoError 在 err 非空时终止测试。
func requireNoError(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v | %s：意外错误 %v", what, err, what, err)
	}
}

// requireNoUser fails the test when a denied lookup still handed back a user. | requireNoUser 在拒绝的查询仍然交出用户时终止测试。
func requireNoUser(t *testing.T, u *User, what string) {
	t.Helper()
	if u != nil {
		t.Fatalf("%s: denied call leaked a user: %+v | %s：被拒绝的调用泄露了用户：%+v", what, u, what, u)
	}
}

// requireNoRole fails the test when a denied lookup still handed back a role. | requireNoRole 在拒绝的查询仍然交出角色时终止测试。
func requireNoRole(t *testing.T, r *Role, what string) {
	t.Helper()
	if r != nil {
		t.Fatalf("%s: denied call leaked a role: %+v | %s：被拒绝的调用泄露了角色：%+v", what, r, what, r)
	}
}

// ------------------------------------------------ Package invariants | 包级不变量

// TestEverySentinelIsBilingualAndDistinct 要求每个哨兵都中英同步且彼此可区分。
func TestEverySentinelIsBilingualAndDistinct(t *testing.T) {
	sentinels := map[string]error{
		"ErrNotFound":           ErrNotFound,
		"ErrForbidden":          ErrForbidden,
		"ErrConflict":           ErrConflict,
		"ErrInvalidInput":       ErrInvalidInput,
		"ErrInvalidCredentials": ErrInvalidCredentials,
		"ErrTenantRequired":     ErrTenantRequired,
		"ErrUserDisabled":       ErrUserDisabled,
		"ErrWeakPassword":       ErrWeakPassword,
		"ErrSecKeyMissing":      ErrSecKeyMissing,
		"ErrNilStore":           ErrNilStore,
		"ErrMasterKeyRequired":  ErrMasterKeyRequired,
		"ErrTokenInvalid":       ErrTokenInvalid,
	}
	seen := make(map[string]string, len(sentinels))
	for name, err := range sentinels {
		if err == nil {
			t.Fatalf("%s is nil | %s 为空", name, name)
		}
		msg := err.Error()
		if !strings.Contains(msg, " | ") {
			t.Errorf("%s is not bilingual: %q | %s 未中英同步：%q", name, msg, name, msg)
		}
		if !strings.Contains(msg, "auth") {
			t.Errorf("%s does not carry the package prefix: %q | %s 未带包名前缀：%q", name, msg, name, msg)
		}
		if other, dup := seen[msg]; dup {
			t.Errorf("%s and %s share the message %q | %s 与 %s 文案相同：%q", name, other, msg, name, other, msg)
		}
		seen[msg] = name
	}
}

// TestNewServiceValidatesOptions 校验构造函数对 Store 与主密钥的拒绝行为。
func TestNewServiceValidatesOptions(t *testing.T) {
	if _, err := NewService(nil, Options{}); !errors.Is(err, ErrNilStore) {
		t.Fatalf("NewService(nil) want ErrNilStore, got %v | NewService(nil) 期望 ErrNilStore，实际 %v", err, err)
	}
	if _, err := NewService(newMemoryStore(), Options{MasterKey: []byte("too-short")}); !errors.Is(err, ErrMasterKeyRequired) {
		t.Fatalf("short master key want ErrMasterKeyRequired, got %v | 短主密钥期望 ErrMasterKeyRequired，实际 %v", err, err)
	}
	// An empty master key is the documented development fallback: the Service is
	// still built, and its master key is random rather than shared. | 空主密钥是文档化的开发兜底：
	// Service 仍可构建，且其主密钥是随机的而非共享的。
	first, _ := newTestService(t, Options{MasterKey: nil, Iterations: testIterations})
	second, _ := newTestService(t, Options{MasterKey: nil, Iterations: testIterations})
	firstUser := mustUser(t, first, mustTenant(t, first, "ephemeral-a", "Ephemeral A").ID, "ephemeral")
	secondUser := mustUser(t, second, mustTenant(t, second, "ephemeral-b", "Ephemeral B").ID, "ephemeral")
	firstKey, err := first.SecKey(testCtx, firstUser)
	requireNoError(t, err, "SecKey(first)")
	secondKey, err := second.SecKey(testCtx, secondUser)
	requireNoError(t, err, "SecKey(second)")
	if bytes.Equal(firstKey, secondKey) {
		t.Fatal("two Services with a generated master key produced the same sec_key | 两个使用生成主密钥的 Service 产出了相同的 sec_key")
	}
}

// TestSecKeyLenIsClampedToTheFloor 校验 sec_key 长度既取默认值也不低于下限。
func TestSecKeyLenIsClampedToTheFloor(t *testing.T) {
	svc, _ := newTestService(t, Options{})
	if svc.secKeyLen != DefaultSecKeyLen {
		t.Fatalf("default secKeyLen want %d, got %d | 默认 secKeyLen 期望 %d，实际 %d", DefaultSecKeyLen, svc.secKeyLen, DefaultSecKeyLen, svc.secKeyLen)
	}
	svc, _ = newTestService(t, Options{SecKeyLen: 4})
	if svc.secKeyLen != MinSecKeyLen {
		t.Fatalf("clamped secKeyLen want %d, got %d | 被提升的 secKeyLen 期望 %d，实际 %d", MinSecKeyLen, svc.secKeyLen, MinSecKeyLen, svc.secKeyLen)
	}
	svc, _ = newTestService(t, Options{SecKeyLen: 64})
	if svc.secKeyLen != 64 {
		t.Fatalf("explicit secKeyLen want 64, got %d | 显式 secKeyLen 期望 64，实际 %d", svc.secKeyLen, svc.secKeyLen)
	}
}

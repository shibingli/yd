package auth

import (
	"bytes"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/security/guomi"
)

// sortedNames returns names sorted and joined with commas, so that a comparison
// never depends on the order a caller happened to supply. | sortedNames 返回排序后以逗号连接的名称，
// 使比较不依赖调用方恰好给出的顺序。
func sortedNames(names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// scanStore collects every string and byte slice the store holds, so that a test
// can search the whole database for a plaintext secret. | scanStore 收集库中保存的所有字符串与字节切片，
// 以便测试在整库范围内搜索明文机密。
func scanStore(s *memoryStore) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.users)*8)
	for _, tn := range s.tenants {
		out = append(out, tn.ID, tn.Slug, tn.Name, string(tn.Status))
	}
	for _, u := range s.users {
		out = append(out,
			u.ID, u.TenantID, u.DefaultTenantID, u.Username, u.Email, u.DisplayName,
			u.PasswordHash, string(u.Status), string(u.EncryptedSecKey))
	}
	// Per-tenant roles live on the memberships, not on the user row. | 各租户的角色存放在成员关系上，而不是用户行上。
	for _, byUser := range s.members {
		for _, membership := range byUser {
			out = append(out, membership.roles...)
		}
	}
	for _, byID := range s.roles {
		for _, r := range byID {
			out = append(out, r.ID, r.TenantID, r.Name, r.Description)
			out = append(out, r.Permissions...)
		}
	}
	// The uniqueness indexes are part of the stored state as well. | 唯一性索引同样属于库中状态。
	for name := range s.usernames {
		out = append(out, name)
	}
	for _, byName := range s.roleNames {
		for name := range byName {
			out = append(out, name)
		}
	}
	return out
}

// requirePresent fails the test when needle is nowhere to be found, which would
// make the following requireAbsent calls vacuous. | requirePresent 在 needle 完全找不到时终止测试，
// 否则随后的 requireAbsent 检查将毫无意义。
func requirePresent(t *testing.T, fields []string, needle []byte, what string) {
	t.Helper()
	for _, field := range fields {
		if bytes.Contains([]byte(field), needle) {
			return
		}
	}
	t.Fatalf("%s: %q is not in the scanned state, so the scan is broken | %s：扫描结果中没有 %q，说明扫描本身有问题",
		what, string(needle), what, string(needle))
}

// requireAbsent fails the test when needle occurs in any stored field. | requireAbsent 在任一存储字段出现 needle 时终止测试。
func requireAbsent(t *testing.T, fields []string, needle []byte, what string) {
	t.Helper()
	if len(needle) == 0 {
		t.Fatalf("%s: the needle is empty, so the check is vacuous | %s：待搜索内容为空，检查没有意义", what, what)
	}
	for i, field := range fields {
		if bytes.Contains([]byte(field), needle) {
			t.Fatalf("%s: found the plaintext secret in stored field #%d (%q) | %s：在库中第 %d 个字段（%q）中发现了明文机密",
				what, i, field, what, i, field)
		}
	}
}

// TestPasswordsAndSecKeysAreNeverStoredInPlaintext asserts that the database holds neither a plaintext password nor a plaintext sec_key or master key. | TestPasswordsAndSecKeysAreNeverStoredInPlaintext 断言数据库里既没有明文口令，也没有明文 sec_key 与主密钥。
func TestPasswordsAndSecKeysAreNeverStoredInPlaintext(t *testing.T) {
	f := newFixture(t)
	raw, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	key := mustSecKey(t, f, raw)

	// The stored value is a hash, not the password: it carries the guomi prefix and
	// only VerifyPassword accepts the plaintext. | 库存的是散列而非口令：它带 guomi 前缀，
	// 且只有 VerifyPassword 认得明文。
	if !strings.HasPrefix(raw.PasswordHash, guomi.PasswordPrefix) {
		t.Fatalf("PasswordHash %q is not a guomi password hash | PasswordHash %q 不是 guomi 口令散列", raw.PasswordHash, raw.PasswordHash)
	}
	if raw.PasswordHash == testPassword || strings.Contains(raw.PasswordHash, testPassword) {
		t.Fatal("PasswordHash contains the plaintext password | PasswordHash 含有明文口令")
	}
	matched, err := guomi.VerifyPassword(raw.PasswordHash, testPassword)
	requireNoError(t, err, "VerifyPassword")
	if !matched {
		t.Fatal("the stored hash does not verify the password it was created from | 库中散列无法校验其对应的口令")
	}

	// The stored sec_key is ciphertext: it is not the key, it does not contain the
	// key, and it has the documented nonce || ciphertext || tag layout. | 库中 sec_key 是密文：
	// 它不等于密钥、不含密钥，且符合文档化的 nonce || 密文 || 标签 布局。
	if bytes.Equal(raw.EncryptedSecKey, key) {
		t.Fatal("EncryptedSecKey equals the plaintext sec_key | EncryptedSecKey 等于明文 sec_key")
	}
	if len(raw.EncryptedSecKey) != guomi.SM4GCMNonceSize+len(key)+testGCMTagLen {
		t.Fatalf("EncryptedSecKey len want %d, got %d | EncryptedSecKey 长度期望 %d，实际 %d",
			guomi.SM4GCMNonceSize+len(key)+testGCMTagLen, len(raw.EncryptedSecKey),
			guomi.SM4GCMNonceSize+len(key)+testGCMTagLen, len(raw.EncryptedSecKey))
	}
	if len(key) != DefaultSecKeyLen {
		t.Fatalf("sec_key len want %d, got %d | sec_key 长度期望 %d，实际 %d", DefaultSecKeyLen, len(key), DefaultSecKeyLen, len(key))
	}

	// Nothing anywhere in the database holds the password, the sec_key or the
	// configured master key. | 库中任何位置都不含口令、sec_key 或配置的主密钥。
	fields := scanStore(f.store)
	requirePresent(t, fields, []byte(f.member.Username), "positive control")
	requireAbsent(t, fields, []byte(testPassword), "plaintext password")
	requireAbsent(t, fields, key, "plaintext sec_key")
	requireAbsent(t, fields, []byte(testMasterKey), "master key")

	// Every user owns a different key, and no two sealed blobs are equal. | 每个用户拥有不同密钥，任意两个密文也不相等。
	for _, u := range []*User{f.admin, f.member, f.stranger} {
		fresh, getErr := f.store.GetUser(testCtx, u.TenantID, u.ID)
		requireNoError(t, getErr, "GetUser("+u.Username+")")
		other := mustSecKey(t, f, fresh)
		if u.ID != f.member.ID && bytes.Equal(other, key) {
			t.Fatalf("%q shares the sec_key of %q | %q 与 %q 共用同一把 sec_key", u.Username, f.member.Username, u.Username, f.member.Username)
		}
	}
	admin, err := f.store.GetUser(testCtx, f.tenantA.ID, f.admin.ID)
	requireNoError(t, err, "GetUser(admin)")
	if bytes.Equal(admin.EncryptedSecKey, raw.EncryptedSecKey) {
		t.Fatal("two users share one sealed sec_key blob | 两个用户共用同一份封装 sec_key 密文")
	}
}

// TestSecKeyIsBoundToItsOwningRecord checks that the sealed sec_key is bound to its tenant and user, so moving it elsewhere cannot open it. | TestSecKeyIsBoundToItsOwningRecord 校验 sec_key 密文与租户、用户绑定，搬走后无法解开。
func TestSecKeyIsBoundToItsOwningRecord(t *testing.T) {
	f := newFixture(t)
	member, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser(member)")
	admin, err := f.store.GetUser(testCtx, f.tenantA.ID, f.admin.ID)
	requireNoError(t, err, "GetUser(admin)")

	// Move the ciphertext of one user onto another record. | 把一个用户的密文搬到另一个用户的记录上。
	admin.EncryptedSecKey = member.EncryptedSecKey
	requireNoError(t, f.store.UpdateUser(testCtx, f.tenantA.ID, admin), "UpdateUser(moved blob)")
	moved, err := f.store.GetUser(testCtx, f.tenantA.ID, f.admin.ID)
	requireNoError(t, err, "GetUser(moved)")
	if _, err := f.svc.SecKey(testCtx, moved); !errors.Is(err, ErrSecKeyMissing) {
		t.Fatalf("a moved sec_key must not open, got %v | 被搬走的 sec_key 必须无法解开，实际 %v", err, err)
	}

	// A truncated blob and a missing blob are refused as well. | 被截断与缺失的密文同样被拒绝。
	truncated := *moved
	truncated.EncryptedSecKey = member.EncryptedSecKey[:guomi.SM4GCMNonceSize+2]
	requireNoError(t, f.store.UpdateUser(testCtx, f.tenantA.ID, &truncated), "UpdateUser(truncated blob)")
	stored, err := f.store.GetUser(testCtx, f.tenantA.ID, f.admin.ID)
	requireNoError(t, err, "GetUser(truncated)")
	if _, err := f.svc.SecKey(testCtx, stored); !errors.Is(err, ErrSecKeyMissing) {
		t.Fatalf("a truncated sec_key must not open, got %v | 被截断的 sec_key 必须无法解开，实际 %v", err, err)
	}
	if _, err := f.svc.SecKey(testCtx, &User{ID: "us_none"}); !errors.Is(err, ErrSecKeyMissing) {
		t.Fatalf("a user without a sec_key want ErrSecKeyMissing, got %v | 没有 sec_key 的用户期望 ErrSecKeyMissing，实际 %v", err, err)
	}
	if _, err := f.svc.SecKey(testCtx, nil); !errors.Is(err, ErrSecKeyMissing) {
		t.Fatalf("a nil user want ErrSecKeyMissing, got %v | 空用户期望 ErrSecKeyMissing，实际 %v", err, err)
	}

	// A different master key cannot open the blob either. | 换一把主密钥同样无法解开该密文。
	other, err := NewService(f.store, Options{MasterKey: []byte("another-master-key-0123456789"), Iterations: testIterations})
	requireNoError(t, err, "NewService(other master key)")
	fresh, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser(fresh)")
	if _, err := other.SecKey(testCtx, fresh); !errors.Is(err, ErrSecKeyMissing) {
		t.Fatalf("a foreign master key must not open a sec_key, got %v | 外部主密钥必须无法解开 sec_key，实际 %v", err, err)
	}
}

// TestCreateUserValidation covers every kind of refusal when a user is created. | TestCreateUserValidation 覆盖创建用户时的每一类拒绝。
func TestCreateUserValidation(t *testing.T) {
	f := newFixture(t)
	before, err := f.svc.ListUsers(testCtx, f.tenantA.ID)
	requireNoError(t, err, "ListUsers")

	cases := []struct {
		name    string
		input   CreateUserInput
		tenant  string
		wantErr error
	}{
		{"empty tenant", CreateUserInput{Username: "u", Password: testPassword}, "", ErrTenantRequired},
		{"empty username", CreateUserInput{Username: "   ", Password: testPassword}, f.tenantA.ID, ErrInvalidInput},
		{"short password", CreateUserInput{Username: "u1", Password: "short"}, f.tenantA.ID, ErrWeakPassword},
		{"short multibyte password", CreateUserInput{Username: "u2", Password: "口令口令口令"}, f.tenantA.ID, ErrWeakPassword},
		{"unknown tenant", CreateUserInput{Username: "u3", Password: testPassword}, "tn_missing", ErrNotFound},
		{"duplicate username", CreateUserInput{Username: "member", Password: testPassword}, f.tenantA.ID, ErrConflict},
		{"case-variant duplicate", CreateUserInput{Username: "MEMBER", Password: testPassword}, f.tenantA.ID, ErrConflict},
		{"unknown role", CreateUserInput{Username: "u4", Password: testPassword, Roles: []string{"ghost"}}, f.tenantA.ID, ErrNotFound},
		{"role of another tenant", CreateUserInput{Username: "u5", Password: testPassword, Roles: []string{"admin"}}, f.tenantB.ID, ErrNotFound},
	}
	for _, tc := range cases {
		u, err := f.svc.CreateUser(testCtx, tc.tenant, tc.input)
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: want %v, got %v | %s：期望 %v，实际 %v", tc.name, tc.wantErr, err, tc.name, tc.wantErr, err)
		}
		requireNoUser(t, u, tc.name)
	}

	after, err := f.svc.ListUsers(testCtx, f.tenantA.ID)
	requireNoError(t, err, "ListUsers")
	if len(after) != len(before) {
		t.Fatalf("a refused CreateUser stored a user: %d -> %d | 被拒绝的 CreateUser 仍落库了用户：%d -> %d",
			len(before), len(after), len(before), len(after))
	}

	// A well-formed input is accepted and normalised. | 合法输入被接受并被归一化。
	created, err := f.svc.CreateUser(testCtx, f.tenantA.ID, CreateUserInput{
		Username:    "  carol  ",
		Email:       "  carol@example.com ",
		DisplayName: " Carol  ",
		Password:    testPassword,
		Roles:       []string{" member ", "member", "admin", "  "},
	})
	requireNoError(t, err, "CreateUser(carol)")
	if created.Username != "carol" || created.Email != "carol@example.com" || created.DisplayName != "Carol" {
		t.Fatalf("CreateUser did not trim its input: %+v | CreateUser 未去除首尾空白：%+v", created, created)
	}
	if got := sortedNames(created.Roles); got != "admin,member" {
		t.Fatalf("roles want \"admin,member\", got %q | 角色期望 \"admin,member\"，实际 %q", got, got)
	}
	if created.Status != StatusActive {
		t.Fatalf("a new user want %q, got %q | 新用户状态期望 %q，实际 %q", StatusActive, created.Status, StatusActive, created.Status)
	}
	if !strings.HasPrefix(created.ID, userIDPrefix) {
		t.Fatalf("user id %q does not carry the %q prefix | 用户 ID %q 未带 %q 前缀", created.ID, userIDPrefix, created.ID, userIDPrefix)
	}
	if created.TenantID != f.tenantA.ID {
		t.Fatalf("TenantID want %q, got %q | TenantID 期望 %q，实际 %q", f.tenantA.ID, created.TenantID, f.tenantA.ID, created.TenantID)
	}
	if len(created.EncryptedSecKey) == 0 {
		t.Fatal("a new user has no sealed sec_key | 新用户没有封装的 sec_key")
	}
	if created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("creation timestamps are inconsistent: %+v | 创建时间不一致：%+v", created, created)
	}

	// The username index is case-insensitive, so the fresh account is reachable
	// through any spelling. | 用户名索引大小写不敏感，因此新账号用任意大小写都可命中。
	byUpper, err := f.store.GetUserByUsername(testCtx, f.tenantA.ID, "CAROL")
	requireNoError(t, err, "GetUserByUsername(CAROL)")
	if byUpper.ID != created.ID {
		t.Fatalf("case-insensitive lookup returned %q | 大小写不敏感查询返回 %q", byUpper.ID, byUpper.ID)
	}
}

// TestAuthenticateDoesNotLeakUserExistence requires an unknown user and a wrong password to be wholly indistinguishable. | TestAuthenticateDoesNotLeakUserExistence 要求用户不存在与口令错误完全不可区分。
func TestAuthenticateDoesNotLeakUserExistence(t *testing.T) {
	f := newFixture(t)

	missing, errMissing := f.svc.Authenticate(testCtx, f.tenantA.ID, "nobody", testPassword)
	requireIs(t, errMissing, ErrInvalidCredentials, "Authenticate(unknown user)")
	requireNoUser(t, missing, "Authenticate(unknown user)")

	wrong, errWrong := f.svc.Authenticate(testCtx, f.tenantA.ID, f.member.Username, testWrongPassword)
	requireIs(t, errWrong, ErrInvalidCredentials, "Authenticate(wrong password)")
	requireNoUser(t, wrong, "Authenticate(wrong password)")

	if errMissing.Error() != errWrong.Error() {
		t.Fatalf("the two failures are distinguishable: %q vs %q | 两种失败可被区分：%q 与 %q",
			errMissing.Error(), errWrong.Error(), errMissing.Error(), errWrong.Error())
	}

	// A user of the tenant B is "missing" for tenant A, with the same answer. | 对租户 A 而言，租户 B 的用户就是「不存在」，答案一致。
	crossTenant, errCross := f.svc.Authenticate(testCtx, f.tenantA.ID, f.stranger.Username, testPassword)
	requireIs(t, errCross, ErrInvalidCredentials, "Authenticate(user of another tenant)")
	requireNoUser(t, crossTenant, "Authenticate(user of another tenant)")
	if errCross.Error() != errMissing.Error() {
		t.Fatalf("a cross-tenant probe is distinguishable: %q vs %q | 跨租户探测可被区分：%q 与 %q",
			errCross.Error(), errMissing.Error(), errCross.Error(), errMissing.Error())
	}

	// A disabled account only reveals itself after the password matched. | 被禁用的账号只有在口令正确后才暴露自己。
	requireNoError(t, f.svc.DisableUser(testCtx, f.tenantA.ID, f.member.ID), "DisableUser")
	disabledWrong, errDisabledWrong := f.svc.Authenticate(testCtx, f.tenantA.ID, f.member.Username, testWrongPassword)
	requireIs(t, errDisabledWrong, ErrInvalidCredentials, "Authenticate(disabled, wrong password)")
	requireNoUser(t, disabledWrong, "Authenticate(disabled, wrong password)")
	disabledRight, errDisabledRight := f.svc.Authenticate(testCtx, f.tenantA.ID, f.member.Username, testPassword)
	requireIs(t, errDisabledRight, ErrUserDisabled, "Authenticate(disabled, right password)")
	requireNoUser(t, disabledRight, "Authenticate(disabled, right password)")

	// An empty tenant performs a tenant-less sign-in against the globally unique
	// username and lands the user in its default tenant. | 空租户执行无租户登录：
	// 按全局唯一的登录名认证，并让用户落入其默认租户。
	global, errGlobal := f.svc.Authenticate(testCtx, "  ", f.admin.Username, testPassword)
	requireNoError(t, errGlobal, "Authenticate(empty tenant)")
	if global == nil || global.TenantID != f.admin.DefaultTenantID {
		t.Fatalf("tenant-less sign-in landed in %v, want default %q | 无租户登录落入 %v，期望默认租户 %q",
			global, f.admin.DefaultTenantID, global, f.admin.DefaultTenantID)
	}

	// Timing parity is structural: the dummy hash carries the same algorithm and
	// the same iteration count as a real hash, so the missing-user path burns the
	// same PBKDF2 work. | 耗时对齐是结构性的：占位散列与真实散列使用同一算法与同一迭代次数，
	// 因此「用户不存在」路径消耗同样的 PBKDF2 运算量。
	parts := strings.Split(f.svc.dummyHash, "$")
	if len(parts) != 4 || parts[0] != guomi.PasswordPrefix {
		t.Fatalf("dummyHash %q is not a guomi password hash | 占位散列 %q 不是 guomi 口令散列", f.svc.dummyHash, f.svc.dummyHash)
	}
	if parts[1] != strconv.Itoa(testIterations) || f.svc.iterations != testIterations {
		t.Fatalf("dummyHash iterations %q do not match the service's %d | 占位散列迭代次数 %q 与 Service 的 %d 不一致",
			parts[1], f.svc.iterations, parts[1], f.svc.iterations)
	}
	matched, err := guomi.VerifyPassword(f.svc.dummyHash, testPassword)
	requireNoError(t, err, "VerifyPassword(dummy)")
	if matched {
		t.Fatal("the dummy hash must never match a password | 占位散列绝不能匹配任何口令")
	}
}

// TestDisableUserIsImmediate checks that disabling takes effect at once and is idempotent. | TestDisableUserIsImmediate 校验禁用立即生效，且幂等。
func TestDisableUserIsImmediate(t *testing.T) {
	f := newFixture(t)
	token := mustIssue(t, f, f.member)
	// The clock moves on, so the refreshed UpdatedAt is observable. | 时钟向前推进，使被刷新的 UpdatedAt 可被观察到。
	f.clock.Advance(time.Minute)
	requireNoError(t, f.svc.DisableUser(testCtx, f.tenantA.ID, f.member.ID), "DisableUser")
	// Disabling again is idempotent. | 再次禁用是幂等的。
	requireNoError(t, f.svc.DisableUser(testCtx, f.tenantA.ID, f.member.ID), "DisableUser again")

	u, err := f.svc.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if u.Status != StatusDisabled {
		t.Fatalf("status want %q, got %q | 状态期望 %q，实际 %q", StatusDisabled, u.Status, StatusDisabled, u.Status)
	}
	// The Service clock is the only time source of the record. | Service 时钟是记录唯一的时间来源。
	if !u.UpdatedAt.Equal(testStart.Add(time.Minute)) {
		t.Fatalf("UpdatedAt want %v, got %v | UpdatedAt 期望 %v，实际 %v",
			testStart.Add(time.Minute), u.UpdatedAt, testStart.Add(time.Minute), u.UpdatedAt)
	}
	if perms := f.svc.PermissionsOf(u); len(perms) != 0 {
		t.Fatalf("a disabled user kept permissions: %v | 被禁用的用户仍保留权限：%v", perms, perms)
	}
	if _, err := f.issuer.Verify(testCtx, token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a disabled user's token must be refused with ErrTokenInvalid, got %v | 被禁用用户的令牌必须以 ErrTokenInvalid 拒绝，实际 %v", err, err)
	}
	if _, _, err := f.issuer.Issue(testCtx, u); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("issuing for a disabled user want ErrUserDisabled, got %v | 为被禁用用户签发期望 ErrUserDisabled，实际 %v", err, err)
	}
	if _, err := f.svc.Authenticate(testCtx, f.tenantA.ID, f.member.Username, testPassword); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("sign-in of a disabled user want ErrUserDisabled, got %v | 被禁用用户的登录期望 ErrUserDisabled，实际 %v", err, err)
	}
	if err := f.svc.DisableUser(testCtx, f.tenantA.ID, "us_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DisableUser(unknown) want ErrNotFound, got %v | DisableUser(未知用户) 期望 ErrNotFound，实际 %v", err, err)
	}
	if err := f.svc.DisableUser(testCtx, "", f.member.ID); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("DisableUser(no tenant) want ErrTenantRequired, got %v | DisableUser(无租户) 期望 ErrTenantRequired，实际 %v", err, err)
	}
}

// TestSetPasswordRotatesTheSecKey checks that changing a password also rotates the sec_key, so the old token lapses with it. | TestSetPasswordRotatesTheSecKey 校验改密同时换掉 sec_key，旧令牌随之失效。
func TestSetPasswordRotatesTheSecKey(t *testing.T) {
	f := newFixture(t)
	token := mustIssue(t, f, f.member)
	const newPassword = "yd-set-password-2"

	if err := f.svc.SetPassword(testCtx, f.tenantA.ID, f.member.ID, "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak new password want ErrWeakPassword, got %v | 弱新口令期望 ErrWeakPassword，实际 %v", err, err)
	}
	requireNoError(t, f.svc.SetPassword(testCtx, f.tenantA.ID, f.member.ID, newPassword), "SetPassword")

	if _, err := f.svc.Authenticate(testCtx, f.tenantA.ID, f.member.Username, testPassword); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("the old password must stop working, got %v | 旧口令必须失效，实际 %v", err, err)
	}
	if _, err := f.svc.Authenticate(testCtx, f.tenantA.ID, f.member.Username, newPassword); err != nil {
		t.Fatalf("the new password must work, got %v | 新口令必须可用，实际 %v", err, err)
	}
	if _, err := f.issuer.Verify(testCtx, token); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a token from before the password change must die, got %v | 改密前的令牌必须失效，实际 %v", err, err)
	}
	fresh, err := f.svc.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if _, _, err := f.issuer.Issue(testCtx, fresh); err != nil {
		t.Fatalf("a token issued after the password change must work, got %v | 改密后签发的令牌必须可用，实际 %v", err, err)
	}
	if err := f.svc.SetPassword(testCtx, f.tenantA.ID, "us_missing", newPassword); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetPassword(unknown user) want ErrNotFound, got %v | SetPassword(未知用户) 期望 ErrNotFound，实际 %v", err, err)
	}
}

// TestUpdateUserCannotOverwriteSecrets checks that UpdateUser can overwrite neither the hash nor the sec_key. | TestUpdateUserCannotOverwriteSecrets 校验 UpdateUser 无法覆盖散列与 sec_key。
func TestUpdateUserCannotOverwriteSecrets(t *testing.T) {
	f := newFixture(t)
	token := mustIssue(t, f, f.member)
	before, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	key := mustSecKey(t, f, before)

	payload := &User{
		ID:              before.ID,
		TenantID:        before.TenantID,
		Username:        "member-renamed",
		Email:           "member@example.com",
		DisplayName:     "Member Renamed",
		PasswordHash:    "sm3pbkdf2$1$AAAA$BBBB",
		EncryptedSecKey: []byte("attacker-controlled-key"),
		Status:          StatusActive,
		Roles:           []string{"admin"},
	}
	requireNoError(t, f.svc.UpdateUser(testCtx, f.tenantA.ID, payload), "UpdateUser")

	after, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if after.Username != "member-renamed" || after.Email != "member@example.com" || after.DisplayName != "Member Renamed" {
		t.Fatalf("mutable fields were not updated: %+v | 可变字段未被更新：%+v", after, after)
	}
	if after.PasswordHash != before.PasswordHash {
		t.Fatal("UpdateUser overwrote PasswordHash | UpdateUser 覆盖了 PasswordHash")
	}
	if !bytes.Equal(after.EncryptedSecKey, before.EncryptedSecKey) {
		t.Fatal("UpdateUser overwrote EncryptedSecKey | UpdateUser 覆盖了 EncryptedSecKey")
	}
	if got := strings.Join(after.Roles, ","); got != "admin" {
		t.Fatalf("roles want \"admin\", got %q | 角色期望 \"admin\"，实际 %q", got, got)
	}
	freshKey := mustSecKey(t, f, after)
	if !bytes.Equal(freshKey, key) {
		t.Fatal("the sec_key changed although only mutable fields were updated | 仅更新可变字段却改变了 sec_key")
	}
	if _, err := f.issuer.Verify(testCtx, token); err != nil {
		t.Fatalf("the old token must survive a profile update, got %v | 资料更新后旧令牌必须继续有效，实际 %v", err, err)
	}

	// A nil Roles keeps the stored list; an empty non-nil list clears it. | Roles 为 nil 时保留库中列表；非 nil 空列表表示清空。
	requireNoError(t, f.svc.UpdateUser(testCtx, f.tenantA.ID, &User{ID: after.ID, Username: after.Username}), "UpdateUser(nil roles)")
	kept, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if len(kept.Roles) != 1 {
		t.Fatalf("a nil Roles must keep the stored list, got %v | nil Roles 必须保留库中列表，实际 %v", kept.Roles, kept.Roles)
	}
	requireNoError(t, f.svc.UpdateUser(testCtx, f.tenantA.ID, &User{ID: after.ID, Username: after.Username, Roles: []string{}}), "UpdateUser(empty roles)")
	cleared, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if len(cleared.Roles) != 0 {
		t.Fatalf("an empty Roles must clear the list, got %v | 空 Roles 必须清空列表，实际 %v", cleared.Roles, cleared.Roles)
	}

	// An unknown role and an unknown user are refused without a partial write. | 未知角色与未知用户被拒绝，且不发生部分写入。
	if err := f.svc.UpdateUser(testCtx, f.tenantA.ID, &User{ID: after.ID, Roles: []string{"ghost"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown role want ErrNotFound, got %v | 未知角色期望 ErrNotFound，实际 %v", err, err)
	}
	if err := f.svc.UpdateUser(testCtx, f.tenantA.ID, &User{ID: "us_missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user want ErrNotFound, got %v | 未知用户期望 ErrNotFound，实际 %v", err, err)
	}
	if err := f.svc.UpdateUser(testCtx, "", &User{ID: after.ID}); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("empty tenant want ErrTenantRequired, got %v | 空租户期望 ErrTenantRequired，实际 %v", err, err)
	}
	if err := f.svc.UpdateUser(testCtx, f.tenantA.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil payload want ErrNotFound, got %v | 空载荷期望 ErrNotFound，实际 %v", err, err)
	}
	if err := f.svc.UpdateUser(testCtx, f.tenantA.ID, &User{ID: after.ID, TenantID: f.tenantB.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign tenant in the payload want ErrNotFound, got %v | 载荷指向其它租户时期望 ErrNotFound，实际 %v", err, err)
	}
}

// TestGetAndListUserErrors checks the boundary behaviour of reading users. | TestGetAndListUserErrors 校验读取用户时的边界行为。
func TestGetAndListUserErrors(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.GetUser(testCtx, f.tenantA.ID, "us_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user want ErrNotFound, got %v | 未知用户期望 ErrNotFound，实际 %v", err, err)
	}
	if _, err := f.svc.GetUser(testCtx, f.tenantA.ID, "  "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty user id want ErrNotFound, got %v | 空用户 ID 期望 ErrNotFound，实际 %v", err, err)
	}
	if _, err := f.svc.GetUser(testCtx, "", f.member.ID); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("empty tenant want ErrTenantRequired, got %v | 空租户期望 ErrTenantRequired，实际 %v", err, err)
	}
	users, err := f.svc.ListUsers(testCtx, f.tenantA.ID)
	requireNoError(t, err, "ListUsers")
	if len(users) != 2 {
		t.Fatalf("ListUsers(A) want 2 users, got %d | ListUsers(A) 期望 2 个用户，实际 %d", len(users), len(users))
	}
	for i := 1; i < len(users); i++ {
		if users[i-1].CreatedAt.After(users[i].CreatedAt) {
			t.Fatalf("ListUsers is not ordered by creation time | ListUsers 未按创建时间排序")
		}
	}
}

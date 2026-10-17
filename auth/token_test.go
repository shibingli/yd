package auth

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/shibingli/yd/guomi"
	"github.com/shibingli/yd/jwt"
)

// TestTokenSignedWithOneUsersKeyFailsWithAnother 断言令牌只被它原本签发的那个用户的密钥接受。
//
// All three users live in the same tenant, so the tenant is not the differentiator
// here: only the per-user sec_key is. | 三个用户同处一个租户，因此这里的区分因素不是租户，
// 只能是每用户独立的 sec_key。
func TestTokenSignedWithOneUsersKeyFailsWithAnother(t *testing.T) {
	f := newFixture(t)
	third := mustUser(t, f.svc, f.tenantA.ID, "third", "member")
	users := []*User{f.admin, f.member, third}

	keys := make(map[string][]byte, len(users))
	tokens := make(map[string]string, len(users))
	for _, u := range users {
		fresh, err := f.svc.GetUser(testCtx, u.TenantID, u.ID)
		requireNoError(t, err, "GetUser("+u.Username+")")
		keys[u.ID] = mustSecKey(t, f, fresh)
		tokens[u.ID] = mustIssue(t, f, fresh)
		// Every user owns a full-length key; an empty or shared key would make the
		// pairwise checks below meaningless. | 每个用户都拥有全长密钥；空密钥或共用密钥会让下面的
		// 两两比对失去意义。
		if len(keys[u.ID]) != f.svc.secKeyLen {
			t.Fatalf("%q has no key of its own: %d bytes | %q 没有自己的密钥：%d 字节", u.Username, len(keys[u.ID]), u.Username, len(keys[u.ID]))
		}
	}

	for _, a := range users {
		for _, b := range users {
			if a.ID == b.ID {
				continue
			}
			if bytes.Equal(keys[a.ID], keys[b.ID]) {
				t.Fatalf("%q and %q share one sec_key | %q 与 %q 共用同一把 sec_key", a.Username, b.Username, a.Username, b.Username)
			}
			// Positive control: the rightful key always verifies. | 正向对照：属于它的密钥一定验签通过。
			if _, err := jwt.Parse(tokens[a.ID], keyFuncFor(keys[a.ID]), parseOptions(f)); err != nil {
				t.Fatalf("token(%q) with its own key: %v | 令牌(%q) 配自己的密钥：%v", a.Username, err, a.Username, err)
			}
			// Any other user's key must fail the signature. | 任何其它用户的密钥都必须验签失败。
			_, err := jwt.Parse(tokens[a.ID], keyFuncFor(keys[b.ID]), parseOptions(f))
			if !errors.Is(err, jwt.ErrSignatureInvalid) {
				t.Fatalf("token(%q) verified with the key of %q: %v | 令牌(%q) 用 %q 的密钥通过了验签：%v",
					a.Username, b.Username, err, a.Username, b.Username, err)
			}
			// And through the issuer as well, where the key comes from the kid. | 经由签发器同样如此，那里密钥来自 kid。
			if _, err := f.issuer.Verify(testCtx, tokens[a.ID]); err != nil {
				t.Fatalf("token(%q) does not verify through its issuer: %v | 令牌(%q) 经签发器验签失败：%v", a.Username, err, a.Username, err)
			}
		}
	}

	// A token whose header names one user while its payload names another is
	// refused, so a stolen key cannot be used to impersonate a neighbour. | 头部指向一个用户、
	// 载荷却指向另一个用户的令牌会被拒绝，因此窃取的密钥无法用于冒充同租户的邻居。
	forged := mustSignRaw(t, f, jwt.SignOptions{
		Kid:    f.member.ID,
		Header: map[string]string{tenantKey: f.tenantA.ID},
	}, f.member, map[string]any{
		"sub":     f.admin.ID,
		tenantKey: f.tenantA.ID,
		"iss":     testIssuer,
		"iat":     f.clock.Now().Unix(),
		"exp":     f.clock.Now().Add(time.Hour).Unix(),
	})
	if _, err := f.issuer.Verify(testCtx, forged); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a token claiming another subject must be refused, got %v | 冒充其它主体的令牌必须被拒绝，实际 %v", err, err)
	}

	// The key is chosen by the kid header alone: the same claims signed with one
	// user's key but presented under another user's kid must fail the signature,
	// which is what makes the header, and not the payload, the key selector. | 密钥完全由 kid 头部决定：
	// 同一份声明用一个用户的密钥签发、却挂在另一个用户的 kid 之下，必然验签失败；
	// 这正是「头部而非载荷负责选择密钥」的含义。
	mislabelled := mustSignRaw(t, f, jwt.SignOptions{
		Kid:    f.admin.ID,
		Header: map[string]string{tenantKey: f.tenantA.ID},
	}, f.member, map[string]any{
		"sub":     f.member.ID,
		tenantKey: f.tenantA.ID,
		"iss":     testIssuer,
		"iat":     f.clock.Now().Unix(),
		"exp":     f.clock.Now().Add(time.Hour).Unix(),
	})
	if _, err := jwt.Parse(mislabelled, f.issuer.KeyFunc, parseOptions(f)); !errors.Is(err, jwt.ErrSignatureInvalid) {
		t.Fatalf("a kid naming another user must select that user's key, got %v | 指向其它用户的 kid 必须选中该用户的密钥，实际 %v", err, err)
	}
}

// TestNoGlobalSigningKeyFallback 断言不存在任何全局兜底密钥：主密钥及其派生值都签不了也验不了令牌。
//
// The master key is the only package-wide secret, and it wraps sec_keys rather than
// signing tokens; this test pins that down by trying it, and every value derived
// from it, as a verification key. | 主密钥是本包唯一的全局机密，它用于封装 sec_key 而不是签发令牌；
// 本测试把它以及由它派生的每个取值都当作校验密钥试一遍，从而把这一点钉死。
func TestNoGlobalSigningKeyFallback(t *testing.T) {
	f := newFixture(t)
	token := mustIssue(t, f, f.member)
	master := guomi.Sum([]byte(testMasterKey))
	candidates := []struct {
		name string
		key  []byte
	}{
		{"the configured master key", []byte(testMasterKey)},
		{"the SM3 digest of the master key", master[:]},
		{"the SM4 key derived from the master key", master[:guomi.SM4KeySize]},
		{"the package default issuer", []byte(DefaultIssuer)},
		{"the tenant id", []byte(f.tenantA.ID)},
		{"the user id", []byte(f.member.ID)},
		{"the username", []byte(f.member.Username)},
		{"the password", []byte(testPassword)},
		{"a plausibly configured secret", []byte("yd-jwt-secret-0123456789")},
	}
	for _, c := range candidates {
		if _, err := jwt.Parse(token, keyFuncFor(c.key), parseOptions(f)); !errors.Is(err, jwt.ErrSignatureInvalid) {
			t.Errorf("%s verified the token: %v | %s 通过了令牌验签：%v", c.name, err, c.name, err)
		}
	}
	// The stored ciphertext is not a signing key either, even though it is the only
	// key-like value the database holds. | 库中保存的密文同样不是签名密钥，
	// 尽管它是数据库里唯一的类密钥取值。
	stored, err := f.store.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	if _, err := jwt.Parse(token, keyFuncFor(stored.EncryptedSecKey), parseOptions(f)); !errors.Is(err, jwt.ErrSignatureInvalid) {
		t.Fatalf("the sealed sec_key verified the token: %v | 封装的 sec_key 通过了令牌验签：%v", err, err)
	}
	// Positive control: the per-user key does verify it, so the failures above are
	// about the key and not about a broken token. | 正向对照：该用户自己的密钥可以验通，
	// 因此上面的失败源于密钥本身，而非令牌损坏。
	if _, err := jwt.Parse(token, keyFuncFor(mustSecKey(t, f, stored)), parseOptions(f)); err != nil {
		t.Fatalf("the per-user key must verify the token: %v | 每用户密钥必须验通令牌：%v", err, err)
	}
	// The issuer's own KeyFunc resolves the sec_key from the store, never a fallback. | 签发器的 KeyFunc 从库中解析
	// sec_key，绝不使用兜底值。
	header := jwt.Header{Alg: jwt.HS256, Kid: f.member.ID, Extra: map[string]string{tenantKey: f.tenantA.ID}}
	key, err := f.issuer.KeyFunc(header)
	requireNoError(t, err, "KeyFunc")
	if bytes.Equal(key.([]byte), []byte(testMasterKey)) || bytes.Equal(key.([]byte), master[:guomi.SM4KeySize]) {
		t.Fatal("KeyFunc returned a package-wide key | KeyFunc 返回了包级密钥")
	}
	if !bytes.Equal(key.([]byte), mustSecKey(t, f, stored)) {
		t.Fatal("KeyFunc did not return the sec_key of the user named by kid | KeyFunc 未返回 kid 所指用户的 sec_key")
	}
}

// TestTokenHeaderCarriesKidAndTenant asserts that the kid and the tenant header both ship with the token. | TestTokenHeaderCarriesKidAndTenant 断言 kid 与租户头部参数都随令牌签发。
func TestTokenHeaderCarriesKidAndTenant(t *testing.T) {
	f := newFixture(t)
	token := mustIssue(t, f, f.admin)
	tok, err := jwt.Parse(token, f.issuer.KeyFunc, parseOptions(f))
	requireNoError(t, err, "Parse")
	if tok.Header.Kid != f.admin.ID {
		t.Fatalf("kid want %q, got %q | kid 期望 %q，实际 %q", f.admin.ID, tok.Header.Kid, f.admin.ID, tok.Header.Kid)
	}
	if tok.Header.Extra[tenantKey] != f.tenantA.ID {
		t.Fatalf("tid header want %q, got %q | tid 头部期望 %q，实际 %q",
			f.tenantA.ID, tok.Header.Extra[tenantKey], f.tenantA.ID, tok.Header.Extra[tenantKey])
	}
	if tok.Header.Alg != jwt.HS256 {
		t.Fatalf("alg want %q, got %q | alg 期望 %q，实际 %q", jwt.HS256, tok.Header.Alg, jwt.HS256, tok.Header.Alg)
	}
	if sub := tok.Claims.Subject(); sub != f.admin.ID {
		t.Fatalf("sub want %q, got %q | sub 期望 %q，实际 %q", f.admin.ID, sub, f.admin.ID, sub)
	}
	if iss, _ := tok.Claims.Get("iss"); iss != testIssuer {
		t.Fatalf("iss want %q, got %q | iss 期望 %q，实际 %q", testIssuer, iss, testIssuer, iss)
	}
	if exp, ok := tok.Claims.ExpiresAt(); !ok || !exp.Equal(testStart.Add(time.Hour)) {
		t.Fatalf("exp want %v, got %v (%v) | exp 期望 %v，实际 %v（%v）", testStart.Add(time.Hour), exp, ok, testStart.Add(time.Hour), exp, ok)
	}
	if _, ok := tok.Claims.Get("jti"); !ok {
		t.Fatal("the token has no jti | 令牌缺少 jti")
	}

	// KeyFunc consults the header alone: it refuses a header without kid or tid. | KeyFunc 只看头部：
	// 缺少 kid 或 tid 的头部一律拒绝。
	if _, err := f.issuer.KeyFunc(jwt.Header{Alg: jwt.HS256, Extra: map[string]string{tenantKey: f.tenantA.ID}}); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a header without kid want ErrTokenInvalid, got %v | 缺少 kid 的头部期望 ErrTokenInvalid，实际 %v", err, err)
	}
	if _, err := f.issuer.KeyFunc(jwt.Header{Alg: jwt.HS256, Kid: f.admin.ID}); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a header without tid want ErrTokenInvalid, got %v | 缺少 tid 的头部期望 ErrTokenInvalid，实际 %v", err, err)
	}
	if _, err := f.issuer.KeyFunc(jwt.Header{Alg: jwt.HS256, Kid: "us_missing", Extra: map[string]string{tenantKey: f.tenantA.ID}}); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("an unknown kid want ErrTokenInvalid, got %v | 未知 kid 期望 ErrTokenInvalid，实际 %v", err, err)
	}
	// A kid that exists in another tenant resolves to nothing, because the lookup is
	// tenant-scoped. | 存在于其它租户的 kid 解析不到任何东西，因为查询受租户约束。
	if _, err := f.issuer.KeyFunc(jwt.Header{Alg: jwt.HS256, Kid: f.admin.ID, Extra: map[string]string{tenantKey: f.tenantB.ID}}); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a cross-tenant kid want ErrTokenInvalid, got %v | 跨租户 kid 期望 ErrTokenInvalid，实际 %v", err, err)
	}
}

// TestDisabledUserTokenIsRejected asserts that a disabled user loses its token at once. | TestDisabledUserTokenIsRejected 断言被禁用的用户立刻失去令牌。
func TestDisabledUserTokenIsRejected(t *testing.T) {
	f := newFixture(t)
	token := mustIssue(t, f, f.member)
	if _, err := f.issuer.Verify(testCtx, token); err != nil {
		t.Fatalf("the token must work before the user is disabled: %v | 禁用之前令牌必须可用：%v", err, err)
	}
	requireNoError(t, f.svc.DisableUser(testCtx, f.tenantA.ID, f.member.ID), "DisableUser")

	if _, err := f.issuer.Verify(testCtx, token); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("Verify of a disabled user want ErrUserDisabled, got %v | 被禁用用户的 Verify 期望 ErrUserDisabled，实际 %v", err, err)
	}
	// The bare jwt.Parse is refused as well, because KeyFunc checks the status before
	// it hands out the key. | 裸的 jwt.Parse 也会被拒绝，因为 KeyFunc 在交出密钥前已检查状态。
	if _, err := jwt.Parse(token, f.issuer.KeyFunc, parseOptions(f)); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("Parse of a disabled user want ErrUserDisabled, got %v | 被禁用用户的 Parse 期望 ErrUserDisabled，实际 %v", err, err)
	}
	// Re-enabling the user revives the very same token, proving the key was intact. | 重新启用后同一个令牌恢复可用，说明密钥本身完好。
	u, err := f.svc.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	u.Status = StatusActive
	requireNoError(t, f.svc.UpdateUser(testCtx, f.tenantA.ID, u), "UpdateUser(reactivate)")
	if _, err := f.issuer.Verify(testCtx, token); err != nil {
		t.Fatalf("the token must work again after re-enabling: %v | 重新启用后令牌必须再次可用：%v", err, err)
	}
}

// TestSecKeyRotationInvalidatesOldToken asserts that rotating the sec_key makes the old token lapse on its own. | TestSecKeyRotationInvalidatesOldToken 断言轮换 sec_key 会让旧令牌自然失效。
func TestSecKeyRotationInvalidatesOldToken(t *testing.T) {
	f := newFixture(t)
	oldToken := mustIssue(t, f, f.member)
	oldKey := mustSecKey(t, f, f.member)
	requireNoError(t, f.svc.RotateSecKey(testCtx, f.tenantA.ID, f.member.ID), "RotateSecKey")

	fresh, err := f.svc.GetUser(testCtx, f.tenantA.ID, f.member.ID)
	requireNoError(t, err, "GetUser")
	newKey := mustSecKey(t, f, fresh)
	if bytes.Equal(oldKey, newKey) {
		t.Fatal("RotateSecKey did not change the key | RotateSecKey 未更换密钥")
	}
	if _, err := f.issuer.Verify(testCtx, oldToken); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("the old token want ErrTokenInvalid, got %v | 旧令牌期望 ErrTokenInvalid，实际 %v", err, err)
	}
	// The reason is cryptographic: the old signature no longer matches the key that
	// the kid resolves to. | 原因是密码学的：旧签名与 kid 解析出的密钥不再匹配。
	if _, err := jwt.Parse(oldToken, f.issuer.KeyFunc, parseOptions(f)); !errors.Is(err, jwt.ErrSignatureInvalid) {
		t.Fatalf("the old token must fail the signature check, got %v | 旧令牌必须验签失败，实际 %v", err, err)
	}
	newToken := mustIssue(t, f, fresh)
	if _, err := f.issuer.Verify(testCtx, newToken); err != nil {
		t.Fatalf("the new token must work: %v | 新令牌必须可用：%v", err, err)
	}

	// Rotating is destructive for the old key only: other users keep their tokens. | 轮换只影响旧密钥：其它用户的令牌不受影响。
	adminToken := mustIssue(t, f, f.admin)
	requireNoError(t, f.svc.RotateSecKey(testCtx, f.tenantA.ID, f.member.ID), "RotateSecKey again")
	if _, err := f.issuer.Verify(testCtx, adminToken); err != nil {
		t.Fatalf("rotating one user's key broke another user's token: %v | 轮换一个用户的密钥破坏了另一个用户的令牌：%v", err, err)
	}
	if err := f.svc.RotateSecKey(testCtx, f.tenantA.ID, "us_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RotateSecKey(unknown user) want ErrNotFound, got %v | RotateSecKey(未知用户) 期望 ErrNotFound，实际 %v", err, err)
	}
}

// TestTokenExpiryFollowsTheClock asserts that exp follows the clock of the issuer. | TestTokenExpiryFollowsTheClock 断言 exp 由签发时钟决定。
func TestTokenExpiryFollowsTheClock(t *testing.T) {
	f := newFixture(t)
	token := mustIssue(t, f, f.member)
	f.clock.Advance(time.Hour + time.Second)

	if _, err := f.issuer.Verify(testCtx, token); !errors.Is(err, jwt.ErrTokenExpired) {
		t.Fatalf("an expired token want jwt.ErrTokenExpired, got %v | 过期令牌期望 jwt.ErrTokenExpired，实际 %v", err, err)
	}
	if _, err := f.issuer.Verify(testCtx, "not-a-token"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a malformed token want ErrTokenInvalid, got %v | 畸形令牌期望 ErrTokenInvalid，实际 %v", err, err)
	}
	if _, err := f.issuer.Verify(testCtx, "  "); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("an empty token want ErrTokenInvalid, got %v | 空令牌期望 ErrTokenInvalid，实际 %v", err, err)
	}
	// A token of a foreign issuer is refused by the iss check. | 其它签发者的令牌会被 iss 检查拒绝。
	other, err := NewTokenIssuer(f.svc, TokenOptions{Issuer: "somebody-else", Now: f.clock.Now})
	requireNoError(t, err, "NewTokenIssuer(other)")
	foreign := mustIssueWith(t, other, f.member)
	if _, err := f.issuer.Verify(testCtx, foreign); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a foreign issuer must be refused, got %v | 外部签发者必须被拒绝，实际 %v", err, err)
	}
	if _, err := other.Verify(testCtx, foreign); err != nil {
		t.Fatalf("the foreign issuer must accept its own token: %v | 外部签发者必须接受自己的令牌：%v", err, err)
	}
}

// TestIssueAndIssuerValidation covers the boundary behaviour of issuing and of building an issuer. | TestIssueAndIssuerValidation 覆盖签发与签发器构造的边界行为。
func TestIssueAndIssuerValidation(t *testing.T) {
	f := newFixture(t)
	if _, err := NewTokenIssuer(nil, TokenOptions{}); !errors.Is(err, ErrNilStore) {
		t.Fatalf("NewTokenIssuer(nil) want ErrNilStore, got %v | NewTokenIssuer(nil) 期望 ErrNilStore，实际 %v", err, err)
	}
	// The defaults are applied when the options are zero. | 选项为零值时套用默认值。
	bare, err := NewTokenIssuer(f.svc, TokenOptions{})
	requireNoError(t, err, "NewTokenIssuer(defaults)")
	if bare.ttl != DefaultTokenTTL || bare.iss != DefaultIssuer {
		t.Fatalf("defaults want ttl=%v iss=%q, got ttl=%v iss=%q | 默认值期望 ttl=%v iss=%q，实际 ttl=%v iss=%q",
			DefaultTokenTTL, DefaultIssuer, bare.ttl, bare.iss, DefaultTokenTTL, DefaultIssuer, bare.ttl, bare.iss)
	}

	if _, _, err := f.issuer.Issue(testCtx, nil); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("Issue(nil) want ErrTokenInvalid, got %v | Issue(nil) 期望 ErrTokenInvalid，实际 %v", err, err)
	}
	if _, _, err := f.issuer.Issue(testCtx, &User{ID: "us_x", TenantID: f.tenantA.ID, Status: StatusActive}); !errors.Is(err, ErrSecKeyMissing) {
		t.Fatalf("Issue(no sec_key) want ErrSecKeyMissing, got %v | Issue(无 sec_key) 期望 ErrSecKeyMissing，实际 %v", err, err)
	}
	if _, _, err := f.issuer.Issue(testCtx, &User{ID: f.member.ID, Status: StatusActive}); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("Issue(no tenant) want ErrTenantRequired, got %v | Issue(无租户) 期望 ErrTenantRequired，实际 %v", err, err)
	}
	// The tenant of the token is taken from the user, so a user record from another
	// tenant cannot be signed into this tenant. | 令牌的租户取自用户，因此另一个租户的用户记录
	// 无法被签进本租户。
	foreign := *f.member
	foreign.TenantID = f.tenantB.ID
	if _, _, err := f.issuer.Issue(testCtx, &foreign); !errors.Is(err, ErrSecKeyMissing) {
		t.Fatalf("a record re-labelled to another tenant want ErrSecKeyMissing, got %v | 被改标为其它租户的记录期望 ErrSecKeyMissing，实际 %v", err, err)
	}
}

// mustSignRaw signs an arbitrary claim set with the sec_key of signer. | mustSignRaw 用 signer 的 sec_key
// 签发任意声明集合。
func mustSignRaw(t *testing.T, f *fixture, o jwt.SignOptions, signer *User, claims map[string]any) string {
	t.Helper()
	key := mustSecKey(t, f, signer)
	token, err := jwt.Sign(claims, key, jwt.HS256, o)
	if err != nil {
		t.Fatalf("jwt.Sign | jwt.Sign 失败: %v", err)
	}
	return token
}

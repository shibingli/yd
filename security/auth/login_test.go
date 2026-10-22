package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/shibingli/yd/security/guomi"
	"github.com/shibingli/ydx/base64"
)

// TestLoginIssuesATokenOnlyForTheRightPassword covers both the success and the failure end of the whole login path. | TestLoginIssuesATokenOnlyForTheRightPassword 覆盖完整登录链路的成功与失败两端。
func TestLoginIssuesATokenOnlyForTheRightPassword(t *testing.T) {
	f := newFixture(t)
	before := f.clock.Now()

	result, err := f.svc.Login(testCtx, f.tenantA.ID, f.member.Username, testPassword, f.issuer)
	requireNoError(t, err, "Login")
	if result.User == nil || result.User.ID != f.member.ID {
		t.Fatalf("Login returned the wrong user: %+v | Login 返回了错误的用户：%+v", result.User, result.User)
	}
	if result.Token == "" {
		t.Fatal("Login issued no token | Login 未签发令牌")
	}
	if !result.ExpiresAt.Equal(before.Add(time.Hour)) {
		t.Fatalf("ExpiresAt want %v, got %v | ExpiresAt 期望 %v，实际 %v", before.Add(time.Hour), result.ExpiresAt, before.Add(time.Hour), result.ExpiresAt)
	}
	// The token really is signed with the sec_key of the user that signed in, so it
	// verifies back into that very user. | 令牌确实用登录者的 sec_key 签发，因此能验回同一个用户。
	verified, err := f.issuer.Verify(testCtx, result.Token)
	requireNoError(t, err, "Verify")
	if verified.ID != f.member.ID || verified.TenantID != f.tenantA.ID {
		t.Fatalf("the issued token names %+v | 签发的令牌指向 %+v", verified, verified)
	}
	if result.User.EncryptedSecKey == nil {
		t.Fatal("the returned user has no sealed sec_key | 返回的用户没有封装的 sec_key")
	}

	// Wrong password, unknown user and a user of another tenant are one and the
	// same failure. | 口令错误、用户不存在与跨租户用户是同一种失败。
	for _, tc := range []struct{ name, tenant, username, password string }{
		{"wrong password", f.tenantA.ID, f.member.Username, testWrongPassword},
		{"unknown user", f.tenantA.ID, "nobody", testPassword},
		{"user of another tenant", f.tenantA.ID, f.stranger.Username, testPassword},
		{"unknown tenant", "tn_missing", f.member.Username, testPassword},
	} {
		res, err := f.svc.Login(testCtx, tc.tenant, tc.username, tc.password, f.issuer)
		requireIs(t, err, ErrInvalidCredentials, "Login("+tc.name+")")
		if res != nil {
			t.Fatalf("Login(%s) returned a result: %+v | Login(%s) 返回了结果：%+v", tc.name, res, tc.name, res)
		}
	}

	// A disabled user only fails after the password matched. | 被禁用的用户只有在口令正确后才失败。
	requireNoError(t, f.svc.DisableUser(testCtx, f.tenantA.ID, f.member.ID), "DisableUser")
	if _, err := f.svc.Login(testCtx, f.tenantA.ID, f.member.Username, testPassword, f.issuer); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("Login of a disabled user want ErrUserDisabled, got %v | 被禁用用户的 Login 期望 ErrUserDisabled，实际 %v", err, err)
	}
	if _, err := f.svc.Login(testCtx, f.tenantA.ID, f.member.Username, testWrongPassword, f.issuer); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login(disabled, wrong password) want ErrInvalidCredentials, got %v | Login(被禁用, 错口令) 期望 ErrInvalidCredentials，实际 %v", err, err)
	}
	// A missing issuer is a wiring fault, not a credential failure. | 缺少签发器属于装配错误，而非凭据失败。
	if _, err := f.svc.Login(testCtx, f.tenantA.ID, f.member.Username, testPassword, nil); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("Login without an issuer want ErrTokenInvalid, got %v | 无签发器的 Login 期望 ErrTokenInvalid，实际 %v", err, err)
	}
}

// TestDecryptLoginPayloadRoundTrip covers both entry points of the front-end SM2 transport protocol. | TestDecryptLoginPayloadRoundTrip 覆盖前端 SM2 传输协议的两条入口。
func TestDecryptLoginPayloadRoundTrip(t *testing.T) {
	priv, err := guomi.GenerateKey()
	requireNoError(t, err, "GenerateKey")

	ciphertext, err := guomi.EncryptRaw(priv.Public(), []byte(testPassword))
	requireNoError(t, err, "EncryptRaw")

	// The browser sends standard Base64 of the raw C1C3C2 blob. | 浏览器提交的是原始 C1C3C2 数据块的标准 Base64。
	encoded := base64.StdEncoding.EncodeToString(ciphertext)
	plain, err := DecryptLoginPayload([]byte(encoded), priv)
	requireNoError(t, err, "DecryptLoginPayload(base64)")
	if plain != testPassword {
		t.Fatalf("want %q, got %q | 期望 %q，实际 %q", testPassword, plain, testPassword, plain)
	}
	// A Go caller that already decoded the body hands over the raw bytes. | 已自行解码请求体的 Go 调用方直接提交原始字节。
	plain, err = DecryptLoginPayload(ciphertext, priv)
	requireNoError(t, err, "DecryptLoginPayload(raw)")
	if plain != testPassword {
		t.Fatalf("want %q, got %q | 期望 %q，实际 %q", testPassword, plain, testPassword, plain)
	}
	// Surrounding ASCII whitespace, as a text field or a form submission adds, is ignored. | 文本字段或表单提交引入的首尾 ASCII 空白会被忽略。
	plain, err = DecryptLoginPayload([]byte("  "+encoded+"\r\n"), priv)
	requireNoError(t, err, "DecryptLoginPayload(padded)")
	if plain != testPassword {
		t.Fatalf("want %q, got %q | 期望 %q，实际 %q", testPassword, plain, testPassword, plain)
	}

	// A nil private key, empty input, non-Base64 garbage and a payload encrypted for
	// someone else all fail with ErrInvalidCredentials and no plaintext. | 私钥为空、输入为空、
	// 非 Base64 的垃圾数据、以及为他人加密的载荷，全部以 ErrInvalidCredentials 失败且不返回明文。
	other, err := guomi.GenerateKey()
	requireNoError(t, err, "GenerateKey(other)")
	foreign, err := guomi.EncryptRaw(other.Public(), []byte(testPassword))
	requireNoError(t, err, "EncryptRaw(foreign)")
	for _, tc := range []struct {
		name string
		blob []byte
		key  *guomi.PrivateKey
	}{
		{"nil key", []byte(encoded), nil},
		{"empty payload", nil, priv},
		{"blank payload", []byte("   \n"), priv},
		{"not base64", []byte("!!!not-base64!!!"), priv},
		{"wrong key", foreign, priv},
		{"truncated", ciphertext[:40], priv},
	} {
		plain, err := DecryptLoginPayload(tc.blob, tc.key)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s: want ErrInvalidCredentials, got %v | %s：期望 ErrInvalidCredentials，实际 %v", tc.name, err, tc.name, err)
		}
		if plain != "" {
			t.Errorf("%s: leaked %q | %s：泄露了 %q", tc.name, plain, tc.name, plain)
		}
	}
}

// TestLoginEncryptedScheme checks how the X-YD-Encrypted marker is recognised. | TestLoginEncryptedScheme 校验 X-YD-Encrypted 标记的判定。
func TestLoginEncryptedScheme(t *testing.T) {
	if LoginEncryptedHeader != "X-YD-Encrypted" {
		t.Fatalf("the header name is part of the front-end contract, got %q | 头部名属于前端契约，实际 %q", LoginEncryptedHeader, LoginEncryptedHeader)
	}
	for _, value := range []string{"sm2", "SM2", " sm2 ", "Sm2"} {
		if !LoginEncryptedScheme(value) {
			t.Errorf("LoginEncryptedScheme(%q) = false | LoginEncryptedScheme(%q) = false", value, value)
		}
	}
	for _, value := range []string{"", "  ", "sm4", "sm2-extra", "plain", "rsa"} {
		if LoginEncryptedScheme(value) {
			t.Errorf("LoginEncryptedScheme(%q) = true | LoginEncryptedScheme(%q) = true", value, value)
		}
	}
}

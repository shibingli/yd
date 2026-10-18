package auth

import (
	"context"
	"strings"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/security/jwt"
)

// TokenOptions configures a TokenIssuer. | TokenOptions 配置 TokenIssuer。
type TokenOptions struct {
	// Now overrides the clock; nil means the clock of the Service. | Now 用于替换时钟；为空表示沿用 Service 的时钟。
	Now func() time.Time
	// Issuer is the "iss" claim; empty selects DefaultIssuer. | Issuer 是 "iss" 声明；为空时取 DefaultIssuer。
	Issuer string
	// TTL is the token lifetime; <= 0 selects DefaultTokenTTL. | TTL 是令牌有效期；<= 0 时取 DefaultTokenTTL。
	TTL time.Duration
}

// TokenIssuer signs and verifies tokens with the per-user sec_key. | TokenIssuer 用每用户 sec_key 签发与验签令牌。
//
// There is no signing secret on this type beyond the sec_key of the user being
// served: Issue seals a token with exactly one user's key, and KeyFunc resolves
// that key from the "kid" and the tenant header parameter alone. | 本类型除「被服务用户自己的 sec_key」
// 之外没有任何签名密钥：Issue 只用某一个用户的密钥签发，KeyFunc 也仅凭 "kid" 与租户头部参数解析该密钥。
//
// A TokenIssuer is safe for concurrent use. | TokenIssuer 可安全并发使用。
type TokenIssuer struct {
	// svc supplies the users, the sec_keys and the role table. | svc 提供用户、sec_key 与角色表。
	svc *Service
	// now is the clock used for iat and exp. | now 是用于 iat 与 exp 的时钟。
	now func() time.Time
	// iss is the issuer claim. | iss 是签发者声明。
	iss string
	// ttl is the token lifetime. | ttl 是令牌有效期。
	ttl time.Duration
}

// NewTokenIssuer builds a TokenIssuer over svc. | NewTokenIssuer 基于 svc 构建 TokenIssuer。
//
// A nil Service is refused with ErrNilStore, because without it no sec_key could
// ever be resolved. | Service 为空时以 ErrNilStore 拒绝，因为没有它就无法解析任何 sec_key。
func NewTokenIssuer(svc *Service, o TokenOptions) (*TokenIssuer, error) {
	if svc == nil {
		return nil, ErrNilStore
	}
	iss := strings.TrimSpace(o.Issuer)
	if iss == "" {
		iss = DefaultIssuer
	}
	ttl := o.TTL
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	now := o.Now
	if now == nil {
		now = svc.now
	}
	return &TokenIssuer{svc: svc, now: now, iss: iss, ttl: ttl}, nil
}

// Issue signs a token for u and returns it with its expiry. | Issue 为 u 签发令牌，并连同到期时间一起返回。
//
// The header carries kid = u.ID and tid = u.TenantID, and the HMAC key is exactly
// SecKey(u); no other key is ever consulted, so tokens of different users live in
// different key spaces. | 头部携带 kid = u.ID 与 tid = u.TenantID，HMAC 密钥恰好是 SecKey(u)；
// 绝不查询任何其它密钥，因此不同用户的令牌处于彼此独立的密钥空间。
//
// A user that is not active is refused with ErrUserDisabled. | 用户非激活时以 ErrUserDisabled 拒绝。
func (i *TokenIssuer) Issue(ctx context.Context, u *User) (string, time.Time, error) {
	if u == nil || u.ID == "" {
		return "", time.Time{}, errx.Detail(ErrTokenInvalid, "cannot issue a token without a user | 缺少用户，无法签发令牌")
	}
	if u.TenantID == "" {
		return "", time.Time{}, ErrTenantRequired
	}
	if u.Status != StatusActive {
		return "", time.Time{}, ErrUserDisabled
	}
	secKey, err := i.svc.SecKey(ctx, u)
	if err != nil {
		return "", time.Time{}, err
	}
	defer zeroBytes(secKey)

	jti, err := newID("")
	if err != nil {
		return "", time.Time{}, err
	}
	now := i.now()
	expiresAt := now.Add(i.ttl)
	claims := map[string]any{
		"sub":     u.ID,
		tenantKey: u.TenantID,
		"iss":     i.iss,
		"jti":     jti,
		"iat":     now.Unix(),
		"exp":     expiresAt.Unix(),
	}
	token, err := jwt.Sign(claims, secKey, jwt.HS256, jwt.SignOptions{
		Kid:    u.ID,
		Header: map[string]string{tenantKey: u.TenantID},
	})
	if err != nil {
		return "", time.Time{}, errx.Wrap(err, ErrTokenInvalid)
	}
	return token, expiresAt, nil
}

// KeyFunc resolves the verification key of a token. | KeyFunc 解析令牌的校验密钥。
//
// It reads the "kid" (user id) and the tid header parameter and loads that user,
// then decrypts the user's sec_key. The payload is never decoded here, so an
// unverified claim can never influence which key is used. | 它读取 "kid"（用户 ID）与 tid 头部参数，
// 载入该用户并解密其 sec_key。此处绝不解析载荷，因此未经校验的声明永远无法影响密钥选择。
//
// The context is a background one because jwt.KeyFunc carries none; the store
// lookup it performs is tenant-scoped by construction. | 由于 jwt.KeyFunc 不带上下文，这里使用后台上下文；
// 它执行的查询天然受租户约束。
//
// The errors it returns are those of jwt.KeyFunc: a wiring problem surfaces as
// itself, while a missing user, an unknown tenant or a user that is not active
// surfaces as ErrTokenInvalid or ErrUserDisabled. | 它返回的错误即 jwt.KeyFunc 的错误：装配问题原样上报，
// 用户不存在、租户未知或用户非激活则以 ErrTokenInvalid 或 ErrUserDisabled 上报。
func (i *TokenIssuer) KeyFunc(header jwt.Header) (any, error) {
	if header.Kid == "" {
		return nil, errx.Detail(ErrTokenInvalid, "token has no kid | 令牌缺少 kid")
	}
	tenantID := header.Extra[tenantKey]
	if tenantID == "" {
		return nil, errx.Detail(ErrTokenInvalid, "token has no %[1]s header | 令牌缺少 %[1]s 头部", tenantKey)
	}
	u, err := i.svc.store.GetUser(context.Background(), tenantID, header.Kid)
	if err != nil {
		return nil, errx.Wrap(err, ErrTokenInvalid)
	}
	if u.Status != StatusActive {
		return nil, ErrUserDisabled
	}
	secKey, err := i.svc.SecKey(context.Background(), u)
	if err != nil {
		return nil, err
	}
	// The buffer becomes the HMAC key, so it is not wiped here. | 该缓冲区会成为 HMAC 密钥，因此此处不清零。
	return secKey, nil
}

// Verify validates a token and returns the user it names. | Verify 校验令牌并返回它所指向的用户。
//
// The signature is checked with the sec_key that the "kid" resolves to; only then
// are the verified sub and tid claims required to equal the kid and tid header
// parameters, and the user is loaded from the store. A user that was disabled,
// deleted or whose sec_key was rotated therefore fails immediately. | 签名用
// "kid" 解析出的 sec_key 校验；此后才要求已验签的 sub 与 tid 声明等于头部的 kid 与 tid，
// 并从库中载入该用户。因此被禁用、被删除或已轮换 sec_key 的用户会立刻失败。
//
// Every failure of jwt.Parse is wrapped into ErrTokenInvalid while the underlying
// jwt sentinel stays reachable through errors.Is. | jwt.Parse 的每种失败都包装进 ErrTokenInvalid，
// 底层 jwt 哨兵仍可用 errors.Is 判定。
func (i *TokenIssuer) Verify(ctx context.Context, token string) (*User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errx.Detail(ErrTokenInvalid, "empty token | 令牌为空")
	}
	tok, err := jwt.Parse(token, i.KeyFunc, i.parseOptions())
	if err != nil {
		return nil, errx.Wrap(err, ErrTokenInvalid)
	}
	// The token does not escape Verify, so its pool storage can be reused as soon as
	// this call returns; this keeps the per-request JWT path allocation-free. | 该 token
	// 不会离开 Verify，因此调用返回后即可复用其池化存储；这使得每请求 JWT 路径保持零分配。
	defer tok.Release()
	userID := tok.Claims.Subject()
	tenantID, _ := tok.Claims.Get(tenantKey)
	if userID == "" || tenantID == "" {
		return nil, errx.Detail(ErrTokenInvalid, "token lacks sub or %[1]s | 令牌缺少 sub 或 %[1]s", tenantKey)
	}
	// The key that verified the signature must belong to the very user and tenant
	// the payload names; otherwise a token could be signed by one user's key while
	// claiming to be another. | 验签所用的密钥必须属于载荷所指的那个用户与租户；
	// 否则一个用户可以用自己的密钥签出「冒充他人」的令牌。
	if userID != tok.Header.Kid || tenantID != tok.Header.Extra[tenantKey] {
		return nil, errx.Detail(ErrTokenInvalid, "sub and %[1]s do not match the signing kid | sub 与 %[1]s 同签发用的 kid 不一致", tenantKey)
	}
	u, err := i.svc.store.GetUser(ctx, tenantID, userID)
	if err != nil {
		return nil, errx.Wrap(err, ErrTokenInvalid)
	}
	if u.Status != StatusActive {
		return nil, ErrUserDisabled
	}
	return u, nil
}

// parseOptions returns the verification policy of this issuer. | parseOptions 返回本签发器的校验策略。
//
// HS256 only, an explicit issuer and a mandatory exp: the allow list is the first
// line of defence against algorithm confusion. | 仅允许 HS256，显式校验签发者，且必须带 exp：
// 白名单是抵御算法混淆攻击的第一道防线。
func (i *TokenIssuer) parseOptions() jwt.ParseOptions {
	return jwt.ParseOptions{
		ValidAlgs:  []jwt.Alg{jwt.HS256},
		Issuer:     i.iss,
		RequireExp: true,
		Now:        i.now,
	}
}

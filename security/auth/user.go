package auth

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/security/guomi"
)

// User is one subject inside exactly one tenant. | User 是某一个租户内的一个主体。
//
// The struct never carries a plaintext password or a plaintext sec_key: PasswordHash
// is guomi.HashPassword output and EncryptedSecKey is SM4-GCM ciphertext sealed
// with the Service master key, bound to the tenant id and the user id as
// additional authenticated data. | 该结构从不携带明文口令或明文 sec_key：PasswordHash 是
// guomi.HashPassword 的结果，EncryptedSecKey 是用 Service 主密钥以 SM4-GCM 封装的密文，
// 并以租户 ID 与用户 ID 作为附加认证数据绑定。
type User struct {
	// CreatedAt is the creation time in the Service clock. | CreatedAt 是 Service 时钟下的创建时间。
	CreatedAt time.Time
	// UpdatedAt is the last mutation time in the Service clock. | UpdatedAt 是 Service 时钟下的最后修改时间。
	UpdatedAt time.Time
	// ID is the generated identifier, prefixed with "us_", and doubles as the JWT "kid". | ID 是生成的标识符，前缀为 "us_"，同时作为 JWT 的 "kid"。
	ID string
	// TenantID is the owning tenant; it is part of every lookup and every sec_key binding. | TenantID 是所属租户，参与每一次查询与每一次 sec_key 绑定。
	TenantID string
	// Username is the unique sign-in name inside the tenant. | Username 是租户内唯一的登录名。
	Username string
	// Email is the optional contact address; it is not a sign-in key. | Email 是可选的联络邮箱，不作为登录键。
	Email string
	// DisplayName is the optional human-readable name. | DisplayName 是可选的展示名称。
	DisplayName string
	// PasswordHash is the guomi password hash; the plaintext is never stored. | PasswordHash 是 guomi 口令散列；明文从不落库。
	PasswordHash string
	// Status is the lifecycle state; anything other than StatusActive fails Verify. | Status 是生命周期状态；非 StatusActive 时 Verify 必然失败。
	Status Status
	// EncryptedSecKey is the SM4-GCM sealed per-user JWT key; the plaintext key never leaves SecKey. | EncryptedSecKey 是 SM4-GCM 封装的每用户 JWT 密钥；明文密钥只从 SecKey 返回。
	EncryptedSecKey []byte
	// Roles holds role names of the tenant; a dangling name grants no permission. | Roles 保存租户内的角色名；悬空的角色名不授予任何权限。
	Roles []string
}

// CreateUserInput carries the fields CreateUser accepts. | CreateUserInput 承载 CreateUser 接受的字段。
//
// There is no sec_key and no status field: the Service generates the first and
// always activates the second. | 这里没有 sec_key 与状态字段：前者由 Service 生成，后者一律为激活。
type CreateUserInput struct {
	// Username is required and unique inside the tenant. | Username 必填，且在租户内唯一。
	Username string
	// Email is optional. | Email 可选。
	Email string
	// DisplayName is optional. | DisplayName 可选。
	DisplayName string
	// Password is required and must meet the strength policy. | Password 必填，且必须满足强度要求。
	Password string
	// Roles lists role names of the same tenant; every name must exist. | Roles 列出同租户的角色名，每个名字都必须存在。
	Roles []string
}

// CreateUser creates a user, hashing the password and sealing a fresh per-user
// sec_key. | CreateUser 创建用户，散列口令并封装一把全新的每用户 sec_key。
//
// The generated sec_key is randomBytes(SecKeyLen) and is only ever persisted as
// User.EncryptedSecKey; it is the sole signing and verification key of that user. | 生成的
// sec_key 为 randomBytes(SecKeyLen)，且只以 User.EncryptedSecKey 落库；它是该用户唯一的签发与验签密钥。
//
// An unknown tenant or an unknown role is reported as ErrNotFound. | 租户未知或角色未知时返回 ErrNotFound。
func (s *Service) CreateUser(ctx context.Context, tenantID string, in CreateUserInput) (*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	username := strings.TrimSpace(in.Username)
	if username == "" {
		return nil, errx.Detail(ErrInvalidInput, "username must not be empty | 用户名不得为空")
	}
	if err := validatePassword(in.Password); err != nil {
		return nil, err
	}
	// The tenant must exist; otherwise a user could be planted into a tenant that
	// nothing else knows about. | 租户必须存在，否则用户会被种进一个谁也不知道的租户。
	if _, err := s.store.GetTenant(ctx, tenantID); err != nil {
		return nil, err
	}
	roles, err := s.resolveRoles(ctx, tenantID, in.Roles)
	if err != nil {
		return nil, err
	}
	hash, err := guomi.HashPassword(in.Password, guomi.PasswordOptions{Iterations: s.iterations})
	if err != nil {
		return nil, err
	}
	secKey, err := randomBytes(s.secKeyLen)
	if err != nil {
		return nil, err
	}
	now := s.now()
	id, err := newID(userIDPrefix)
	if err != nil {
		return nil, err
	}
	sealed, err := sealSecKey(s.masterKey, tenantID, id, secKey)
	if err != nil {
		return nil, err
	}
	u := &User{
		ID:              id,
		TenantID:        tenantID,
		Username:        username,
		Email:           strings.TrimSpace(in.Email),
		DisplayName:     strings.TrimSpace(in.DisplayName),
		PasswordHash:    hash,
		EncryptedSecKey: sealed,
		Status:          StatusActive,
		Roles:           roles,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	return cloneUser(u), nil
}

// GetUser returns one user of the tenant, or ErrNotFound. | GetUser 返回该租户下的单个用户，不存在时返回 ErrNotFound。
//
// A user of another tenant is indistinguishable from a missing one. | 其它租户的用户与不存在的用户不可区分。
func (s *Service) GetUser(ctx context.Context, tenantID, userID string) (*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	if strings.TrimSpace(userID) == "" {
		return nil, errx.Detail(ErrNotFound, "empty user id | 用户 ID 为空")
	}
	return s.store.GetUser(ctx, tenantID, userID)
}

// ListUsers returns every user of the tenant, or ErrNotFound when the tenant is
// unknown. | ListUsers 返回该租户的全部用户；租户未知时返回 ErrNotFound。
func (s *Service) ListUsers(ctx context.Context, tenantID string) ([]*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	return s.store.ListUsers(ctx, tenantID)
}

// UpdateUser replaces the mutable fields of an existing user. | UpdateUser 覆盖既有用户的可变字段。
//
// Username, Email, DisplayName, Status and Roles are taken from u; PasswordHash,
// EncryptedSecKey, TenantID and CreatedAt are kept from the stored record, so a
// caller can never overwrite a hash or a sec_key by accident or by a crafted
// payload. An empty Username or Status keeps the stored value, and a nil Roles
// keeps the stored role list, while an empty non-nil Roles clears it. | Username、
// Email、DisplayName、Status 与 Roles 取自 u；PasswordHash、EncryptedSecKey、TenantID 与 CreatedAt
// 一律沿用库中记录，调用方无法凭借意外或构造的载荷覆盖散列与 sec_key。Username 或 Status 为空时保留库中取值，
// Roles 为 nil 时保留库中角色列表，非 nil 的空切片则表示清空。
//
// A tenant mismatch or an unknown tenant, user or role is reported as ErrNotFound. | 租户不匹配，或租户、用户、角色未知时，返回 ErrNotFound。
func (s *Service) UpdateUser(ctx context.Context, tenantID string, u *User) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	if u == nil || strings.TrimSpace(u.ID) == "" {
		return errx.Detail(ErrNotFound, "empty user id | 用户 ID 为空")
	}
	if u.TenantID != "" && u.TenantID != tenantID {
		// A payload naming another tenant is treated as a missing resource. | 载荷指向其它租户时按资源不存在处理。
		return ErrNotFound
	}
	stored, err := s.store.GetUser(ctx, tenantID, u.ID)
	if err != nil {
		return err
	}
	username := strings.TrimSpace(u.Username)
	if username == "" {
		username = stored.Username
	}
	status := u.Status
	if status == "" {
		status = stored.Status
	}
	stored.Username = username
	stored.Email = strings.TrimSpace(u.Email)
	stored.DisplayName = strings.TrimSpace(u.DisplayName)
	stored.Status = status
	if u.Roles != nil {
		roles, err := s.resolveRoles(ctx, tenantID, u.Roles)
		if err != nil {
			return err
		}
		stored.Roles = roles
	}
	stored.UpdatedAt = s.now()
	return s.store.UpdateUser(ctx, tenantID, stored)
}

// DisableUser flips a user to StatusDisabled. | DisableUser 把用户置为 StatusDisabled。
//
// Tokens that are already in the wild stop working immediately, because both
// KeyFunc and Verify refuse a user that is not active. | 已经流出的令牌会立即失效：KeyFunc 与 Verify
// 都会拒绝非激活用户。
func (s *Service) DisableUser(ctx context.Context, tenantID, userID string) error {
	u, err := s.store.GetUser(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	u.Status = StatusDisabled
	u.UpdatedAt = s.now()
	return s.store.UpdateUser(ctx, tenantID, u)
}

// SetPassword replaces the password hash and rotates the sec_key in one step. | SetPassword 一步完成口令散列替换与 sec_key 轮换。
//
// Rotating the sec_key is deliberate: a password change is a security event, so
// every token issued before it must die with the old key. | 轮换 sec_key 是刻意的：改密属于安全事件，
// 因此此前签发的所有令牌都必须随旧密钥一起失效。
func (s *Service) SetPassword(ctx context.Context, tenantID, userID, password string) error {
	if err := validatePassword(password); err != nil {
		return err
	}
	u, err := s.store.GetUser(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	hash, err := guomi.HashPassword(password, guomi.PasswordOptions{Iterations: s.iterations})
	if err != nil {
		return err
	}
	secKey, err := randomBytes(s.secKeyLen)
	if err != nil {
		return err
	}
	sealed, err := sealSecKey(s.masterKey, u.TenantID, u.ID, secKey)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	u.EncryptedSecKey = sealed
	u.UpdatedAt = s.now()
	return s.store.UpdateUser(ctx, tenantID, u)
}

// Authenticate verifies a password and returns the user. | Authenticate 校验口令并返回用户。
//
// A missing user and a wrong password return the very same sentinel,
// ErrInvalidCredentials, and cost the same PBKDF2 work, so neither the response
// nor the latency enumerates usernames. | 用户不存在与口令错误返回完全相同的哨兵 ErrInvalidCredentials，
// 且两者都执行同样代价的 PBKDF2 运算，因此响应与耗时都不会枚举用户名。
//
// A disabled user only learns about it after the password matched, so a stranger
// cannot use this call to probe for accounts either. | 被禁用的用户只有在口令正确后才会得知，
// 因此陌生人也无法借本调用探测账号。
func (s *Service) Authenticate(ctx context.Context, tenantID, username, password string) (*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	u, err := s.store.GetUserByUsername(ctx, tenantID, strings.TrimSpace(username))
	if err != nil {
		// Burn the same PBKDF2 work a real account would burn. | 消耗与真实账号相同的 PBKDF2 运算量。
		_, _ = guomi.VerifyPassword(s.dummyHash, password)
		return nil, ErrInvalidCredentials
	}
	matched, verifyErr := guomi.VerifyPassword(u.PasswordHash, password)
	if verifyErr != nil {
		return nil, errx.Wrap(verifyErr, ErrInvalidCredentials)
	}
	if !matched {
		return nil, ErrInvalidCredentials
	}
	if u.Status != StatusActive {
		return nil, ErrUserDisabled
	}
	return u, nil
}

// SecKey decrypts the per-user JWT key of u. | SecKey 解密 u 的每用户 JWT 密钥。
//
// The returned slice is a fresh buffer owned by the caller; the ciphertext is bound
// to the tenant id and the user id, so a key copied onto another record fails to
// open instead of being accepted. | 返回的切片是调用方持有的新缓冲区；密文与租户 ID、用户 ID 绑定，
// 因此被复制到其它记录上的密钥无法解开，而不会被接受。
func (s *Service) SecKey(_ context.Context, u *User) ([]byte, error) {
	if u == nil || len(u.EncryptedSecKey) == 0 {
		return nil, errx.Detail(ErrSecKeyMissing, "user has no encrypted sec_key | 用户没有加密的 sec_key")
	}
	return openSecKey(s.masterKey, u.TenantID, u.ID, u.EncryptedSecKey)
}

// RotateSecKey replaces the per-user JWT key with a fresh random one. | RotateSecKey 用新的随机密钥替换每用户 JWT 密钥。
//
// Every token signed with the previous key stops verifying at once, because
// verification resolves the key from the store on each request. | 用旧密钥签发的所有令牌会立即无法验签，
// 因为验签在每次请求时都会从库中解析密钥。
func (s *Service) RotateSecKey(ctx context.Context, tenantID, userID string) error {
	u, err := s.store.GetUser(ctx, tenantID, userID)
	if err != nil {
		return err
	}
	secKey, err := randomBytes(s.secKeyLen)
	if err != nil {
		return err
	}
	sealed, err := sealSecKey(s.masterKey, u.TenantID, u.ID, secKey)
	if err != nil {
		return err
	}
	u.EncryptedSecKey = sealed
	u.UpdatedAt = s.now()
	return s.store.UpdateUser(ctx, tenantID, u)
}

// resolveRoles validates that every listed role exists inside the tenant and
// returns the deduplicated names. | resolveRoles 校验列出的每个角色都存在于该租户，并返回去重后的名字。
//
// An unknown role is reported as ErrNotFound, so a caller cannot use role assignment
// to probe for role names of another tenant. | 角色未知时返回 ErrNotFound，调用方无法借角色分配探测其它租户的角色名。
func (s *Service) resolveRoles(ctx context.Context, tenantID string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		known, err := s.roles.exists(ctx, tenantID, name)
		if err != nil {
			return nil, err
		}
		if !known {
			return nil, errx.Detail(ErrNotFound, "unknown role %q | 角色 %q 不存在", name, name)
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// validatePassword enforces the strength policy shared by every entry point. | validatePassword 实施所有入口共用的强度策略。
func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLen {
		return errx.Detail(ErrWeakPassword, "password must be at least %[1]d characters | 口令至少需要 %[1]d 个字符", MinPasswordLen)
	}
	return nil
}

// cloneUser returns a deep copy of u, or nil when u is nil. | cloneUser 返回 u 的深拷贝，u 为空时返回 nil。
func cloneUser(u *User) *User {
	if u == nil {
		return nil
	}
	dup := *u
	if u.Roles != nil {
		dup.Roles = append([]string(nil), u.Roles...)
	}
	if u.EncryptedSecKey != nil {
		dup.EncryptedSecKey = append([]byte(nil), u.EncryptedSecKey...)
	}
	return &dup
}

// zeroBytes wipes a sensitive buffer in place, shrinking the window in which a
// plaintext key or password lives in memory. | zeroBytes 就地清零敏感缓冲区，缩小明文密钥或口令在内存中的存活窗口。
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

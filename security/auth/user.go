package auth

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/guomi"
)

// User is a global identity that may belong to several tenants. | User 是可以隶属多个租户的全局身份。
//
// TenantID is the tenant this value acts in for the request or token at hand,
// HomeTenantID is the immutable identity tenant and DefaultTenantID is where a
// tenant-less sign-in lands. | TenantID 是本次请求或令牌下活动的租户，
// HomeTenantID 是不可变的身份租户，DefaultTenantID 是无租户登录落入的租户。
//
// The struct never carries a plaintext password or a plaintext sec_key: PasswordHash
// is guomi.HashPassword output and EncryptedSecKey is SM4-GCM ciphertext sealed
// with the Service master key, bound to the home tenant id and the user id as
// additional authenticated data. | 该结构从不携带明文口令或明文 sec_key：PasswordHash 是
// guomi.HashPassword 的结果，EncryptedSecKey 是用 Service 主密钥以 SM4-GCM 封装的密文，
// 并以身份租户 ID 与用户 ID 作为附加认证数据绑定。
type User struct {
	// CreatedAt is the creation time in the Service clock. | CreatedAt 是 Service 时钟下的创建时间。
	CreatedAt time.Time
	// UpdatedAt is the last mutation time in the Service clock. | UpdatedAt 是 Service 时钟下的最后修改时间。
	UpdatedAt time.Time
	// ID is the generated identifier, prefixed with "us_", and doubles as the JWT "kid". | ID 是生成的标识符，前缀为 "us_"，同时作为 JWT 的 "kid"。
	ID string
	// TenantID is the tenant this user acts in for the current request or token. | TenantID 是当前请求或令牌下该用户实际所在的租户。
	//
	// A user may belong to several tenants; this field always carries the active one,
	// while HomeTenantID carries the immutable identity tenant. | 一个用户可以隶属于多个租户；
	// 本字段始终是当前生效的那个，HomeTenantID 则是不变的身份租户。
	TenantID string
	// HomeTenantID is the tenant that owns this identity and seals its sec_key; it never changes. | HomeTenantID 是拥有该身份并封装其 sec_key 的租户，永不改变。
	HomeTenantID string
	// DefaultTenantID is the tenant a sign-in without a tenant name lands in; the user may change it. | DefaultTenantID 是未指明租户时登录落入的租户，用户可以修改。
	DefaultTenantID string
	// TenantIDs lists every tenant this user belongs to. | TenantIDs 列出该用户隶属的全部租户。
	TenantIDs []string
	// Username is the globally unique sign-in name. | Username 是全局唯一的登录名。
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
		return nil, errs.Detail(ErrInvalidInput, "username must not be empty | 用户名不得为空")
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
		HomeTenantID:    tenantID,
		DefaultTenantID: tenantID,
		TenantIDs:       []string{tenantID},
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
		return nil, errs.Detail(ErrNotFound, "empty user id | 用户 ID 为空")
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

// userBatchStore is the optional Store capability that resolves or searches users without loading the whole
// tenant roster. | userBatchStore 是 Store 的可选能力：无需载入整个租户名册即可按 ID 解析或检索用户。
type userBatchStore interface {
	// GetUsers returns the users visible in tenantID for the given ids. | GetUsers 返回 tenantID 内给定 ID 可见的用户。
	GetUsers(ctx context.Context, tenantID string, ids []string) ([]*User, error)
	// SearchUsers returns active users whose name, sign-in name or e-mail contains the keyword. |
	// SearchUsers 返回名称、登录名或邮箱包含关键字的活跃用户。
	SearchUsers(ctx context.Context, tenantID, keyword, excludeID string, limit int) ([]*User, error)
}

// GetUsers resolves a batch of users visible in tenantID. Unknown ids and users of another tenant are omitted. |
// GetUsers 批量解析 tenantID 内可见的用户；未知 ID 与其它租户的用户都被省略。
//
// Without the batch capability it falls back to GetUser per id: the result is identical, only with more round
// trips. | 批量能力缺失时回退到逐个 GetUser：结果不变，只是多几次往返。
func (s *Service) GetUsers(ctx context.Context, tenantID string, ids []string) ([]*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return []*User{}, nil
	}
	if batch, ok := s.store.(userBatchStore); ok {
		return batch.GetUsers(ctx, tenantID, unique)
	}
	out := make([]*User, 0, len(unique))
	for _, id := range unique {
		user, err := s.store.GetUser(ctx, tenantID, id)
		if err == nil {
			out = append(out, user)
			continue
		}
		if errors.Is(err, ErrNotFound) {
			continue
		}
		return nil, err
	}
	return out, nil
}

// userPageStore is the optional Store capability behind a paged admin list. |
// userPageStore 是管理列表分页所依赖的可选 Store 能力。
type userPageStore interface {
	// ListUsersPage returns one page of tenant members matching the keyword, plus the filtered total. |
	// ListUsersPage 返回匹配关键字的租户成员的一页，以及过滤后的总数。
	ListUsersPage(ctx context.Context, tenantID, keyword string, limit, offset int) ([]*User, int, error)
}

// ListUsersPage returns one page of tenant members matching the keyword. The keyword is matched
// case-insensitively against the sign-in name, display name and e-mail; the total is the filtered count,
// not the page length. | ListUsersPage 返回匹配关键字的租户成员的一页。关键字对登录名、展示名与邮箱做
// 不区分大小写的匹配；total 是过滤后的总数，而不是本页条数。
func (s *Service) ListUsersPage(ctx context.Context, tenantID, keyword string, limit, offset int) ([]*User, int, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, 0, ErrTenantRequired
	}
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	if paged, ok := s.store.(userPageStore); ok {
		return paged.ListUsersPage(ctx, tenantID, keyword, limit, offset)
	}
	users, err := s.store.ListUsers(ctx, tenantID)
	if err != nil {
		return nil, 0, err
	}
	needle := strings.ToLower(strings.TrimSpace(keyword))
	matched := make([]*User, 0, len(users))
	for _, user := range users {
		if needle != "" && !userMatches(user, needle) {
			continue
		}
		matched = append(matched, user)
	}
	// ListUsers returns oldest first; the paged contract is newest first, matching the store implementation. |
	// ListUsers 按从旧到新返回；分页契约是从新到旧，与存储层实现一致。
	for left, right := 0, len(matched)-1; left < right; left, right = left+1, right-1 {
		matched[left], matched[right] = matched[right], matched[left]
	}
	total := len(matched)
	if offset >= total {
		return []*User{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return matched[offset:end], total, nil
}

// userRecentStore is the optional Store capability behind a dashboard's count and recent-user strip. |
// userRecentStore 是仪表盘计数与最近用户条带所依赖的可选 Store 能力。
type userRecentStore interface {
	// CountUsers returns the number of members of the tenant. | CountUsers 返回租户的成员数。
	CountUsers(ctx context.Context, tenantID string) (int, error)
	// RecentUsers returns the newest members of the tenant. | RecentUsers 返回租户最新的成员。
	RecentUsers(ctx context.Context, tenantID string, limit int) ([]*User, error)
}

// CountUsers returns the number of members of the tenant. | CountUsers 返回租户的成员数。
func (s *Service) CountUsers(ctx context.Context, tenantID string) (int, error) {
	if strings.TrimSpace(tenantID) == "" {
		return 0, ErrTenantRequired
	}
	if recent, ok := s.store.(userRecentStore); ok {
		return recent.CountUsers(ctx, tenantID)
	}
	users, err := s.store.ListUsers(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	return len(users), nil
}

// RecentUsers returns the newest members of the tenant, up to limit. | RecentUsers 返回租户最新的成员，最多 limit 个。
func (s *Service) RecentUsers(ctx context.Context, tenantID string, limit int) ([]*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	if limit <= 0 {
		limit = 5
	}
	if recent, ok := s.store.(userRecentStore); ok {
		return recent.RecentUsers(ctx, tenantID, limit)
	}
	users, err := s.store.ListUsers(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if len(users) > limit {
		users = users[:limit]
	}
	return users, nil
}

// SearchUsers returns active users of the tenant matching the keyword, ordered by display name. |
// SearchUsers 返回租户内匹配关键字的活跃用户，按展示名排序。
//
// 与 ListUsers 不同，这里把关键字与上限交给存储层过滤，避免为了二十条候选解码整张用户表。 |
// Unlike ListUsers, the keyword and limit are handed to the store, so twenty candidates do not decode the whole
// user table.
func (s *Service) SearchUsers(ctx context.Context, tenantID, keyword, excludeID string, limit int) ([]*User, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantRequired
	}
	if limit <= 0 {
		limit = 20
	}
	if batch, ok := s.store.(userBatchStore); ok {
		return batch.SearchUsers(ctx, tenantID, keyword, excludeID, limit)
	}
	users, err := s.store.ListUsers(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	out := make([]*User, 0, limit)
	for _, user := range users {
		if user.ID == excludeID || user.Status != StatusActive {
			continue
		}
		if keyword != "" && !userMatches(user, keyword) {
			continue
		}
		out = append(out, user)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(userDisplay(out[i])) < strings.ToLower(userDisplay(out[j]))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// userMatches reports whether the keyword appears in a searchable field. | userMatches 报告关键字是否出现在可检索字段中。
func userMatches(user *User, keyword string) bool {
	for _, value := range []string{user.Username, user.DisplayName, user.Email} {
		if strings.Contains(strings.ToLower(value), keyword) {
			return true
		}
	}
	return false
}

// userDisplay returns the display name, falling back to the sign-in name. | userDisplay 返回展示名，缺失时回退登录名。
func userDisplay(user *User) string {
	if strings.TrimSpace(user.DisplayName) != "" {
		return user.DisplayName
	}
	return user.Username
}

// UpdateUser replaces the mutable fields of an existing user. | UpdateUser 覆盖既有用户的可变字段。
//
// Username, Email, DisplayName, Status and Roles are taken from u; PasswordHash,
// EncryptedSecKey, Status, TenantID and CreatedAt are kept from the stored record, so a
// caller can never overwrite a hash, a sec_key or a lifecycle state by accident or by a crafted
// payload. An empty Username keeps the stored value, and a nil Roles keeps the stored role list,
// while an empty non-nil Roles clears it. Status changes go through SetStatus/DisableUser. |
// Username、Email、DisplayName 与 Roles 取自 u；Status、PasswordHash、EncryptedSecKey、TenantID 与
// CreatedAt 一律沿用库中记录，调用方无法凭借意外或构造的载荷覆盖状态、散列与 sec_key。
// Username 为空时保留库中取值，Roles 为 nil 时保留库中角色列表，非 nil 的空切片表示清空。
// 状态变更走 SetStatus/DisableUser。
//
// A tenant mismatch or an unknown tenant, user or role is reported as ErrNotFound. | 租户不匹配，或租户、用户、角色未知时，返回 ErrNotFound。
func (s *Service) UpdateUser(ctx context.Context, tenantID string, u *User) error {
	if strings.TrimSpace(tenantID) == "" {
		return ErrTenantRequired
	}
	if u == nil || strings.TrimSpace(u.ID) == "" {
		return errs.Detail(ErrNotFound, "empty user id | 用户 ID 为空")
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
	stored.Username = username
	stored.Email = strings.TrimSpace(u.Email)
	stored.DisplayName = strings.TrimSpace(u.DisplayName)
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
	return s.SetStatus(ctx, tenantID, userID, StatusDisabled)
}

// SetStatus changes only a user's lifecycle state. It never rewrites profile, password or key
// columns, so a concurrent profile edit cannot resurrect an old status. |
// SetStatus 只修改用户的生命周期状态，绝不重写资料、口令或密钥列，因此并发的资料编辑无法复活旧状态。
func (s *Service) SetStatus(ctx context.Context, tenantID, userID string, status Status) error {
	if status != StatusActive && status != StatusDisabled {
		return errs.Detail(ErrInvalidInput, "unknown user status %[1]q | 未知的用户状态 %[1]q", status)
	}
	return s.store.SetUserStatus(ctx, tenantID, userID, status, s.now())
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
	sealed, err := sealSecKey(s.masterKey, homeTenantOf(u), u.ID, secKey)
	if err != nil {
		return err
	}
	return s.store.SetUserCredentials(ctx, tenantID, userID, hash, sealed, s.now())
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
	// An empty tenant means a tenant-less sign-in: the username is globally unique
	// and the user lands in its default tenant. | 空租户表示不带租户的登录：登录名全局唯一，
	// 用户落入其默认租户。
	var (
		u   *User
		err error
	)
	if strings.TrimSpace(tenantID) == "" {
		u, err = s.store.GetUserByName(ctx, strings.TrimSpace(username))
	} else {
		u, err = s.store.GetUserByUsername(ctx, tenantID, strings.TrimSpace(username))
	}
	if err != nil {
		// Burn the same PBKDF2 work a real account would burn. | 消耗与真实账号相同的 PBKDF2 运算量。
		_, _ = guomi.VerifyPassword(s.dummyHash, password)
		return nil, ErrInvalidCredentials
	}
	matched, verifyErr := guomi.VerifyPassword(u.PasswordHash, password)
	if verifyErr != nil {
		return nil, errs.Wrap(verifyErr, ErrInvalidCredentials)
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
		return nil, errs.Detail(ErrSecKeyMissing, "user has no encrypted sec_key | 用户没有加密的 sec_key")
	}
	// The key is sealed with the immutable identity tenant, never with the tenant the
	// request happens to act in: switching tenants must not make the key unreadable.
	// | 密钥用不可变的身份租户封装，绝不用请求当前所在的租户：切换租户不能让密钥变得不可解。
	return openSecKey(s.masterKey, homeTenantOf(u), u.ID, u.EncryptedSecKey)
}

// homeTenantOf returns the tenant that seals the sec_key of u. | homeTenantOf 返回封装 u 的 sec_key 的租户。
//
// Records written before HomeTenantID existed carry the tenant in TenantID, so the
// fallback keeps them readable. | HomeTenantID 出现之前写入的记录把租户放在 TenantID 上，
// 因此这里回退到它，旧记录仍然可解。
func homeTenantOf(u *User) string {
	if u == nil {
		return ""
	}
	if u.HomeTenantID != "" {
		return u.HomeTenantID
	}
	return u.TenantID
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
	sealed, err := sealSecKey(s.masterKey, homeTenantOf(u), u.ID, secKey)
	if err != nil {
		return err
	}
	return s.store.SetUserSecKey(ctx, tenantID, userID, sealed, s.now())
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
			return nil, errs.Detail(ErrNotFound, "unknown role %q | 角色 %q 不存在", name, name)
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
		return errs.Detail(ErrWeakPassword, "password must be at least %[1]d characters | 口令至少需要 %[1]d 个字符", MinPasswordLen)
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
	if u.TenantIDs != nil {
		dup.TenantIDs = append([]string(nil), u.TenantIDs...)
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

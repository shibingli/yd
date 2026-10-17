// Package auth implements multi-tenancy, subjects (users) with role-based access
// control, and the complete sign-in flow, on top of per-user JWT keys. | Package auth 在
// 「每用户独立 JWT 密钥」的基础上，实现多租户、主体（用户）的角色权限（RBAC）与完整登录授权。
//
// The three ideas that shape the API | 塑造本 API 的三个要点：
//
//  1. Tenant isolation is a hard boundary. Every Store method that reads an
//     entity by id takes a tenantID and validates it; a cross-tenant read is
//     reported as ErrNotFound, never as ErrForbidden, so a probe learns nothing
//     about the existence of a foreign entity. | 租户是硬边界：Store 中任何按 ID 读取实体的方法
//     都带 tenantID 并校验；跨租户读取一律报 ErrNotFound 而非 ErrForbidden，探测者无法从错误里
//     推断外部实体是否存在。
//  2. The JWT signing key is private to each user. CreateUser generates a random
//     sec_key of SecKeyLen bytes and stores it only as SM4-GCM ciphertext
//     (User.EncryptedSecKey); signing uses jwt.Sign with that key and the user id
//     as "kid", and verification resolves the key from the "kid" (plus the tenant
//     header parameter) alone, without ever decoding an unverified payload.
//     There is no global fallback secret anywhere in this package. | JWT 密钥每用户独立：
//     CreateUser 生成 SecKeyLen 字节的随机 sec_key，且只以 SM4-GCM 密文（User.EncryptedSecKey）
//     落库；签发时以该密钥与用户 ID（"kid"）调用 jwt.Sign，验签时仅凭 "kid"（与租户头部参数）
//     取回并解密该用户的 sec_key，绝不解析未验签的载荷。本包不存在任何全局兜底密钥。
//  3. Passwords are stored only as guomi.HashPassword output and compared with
//     guomi.VerifyPassword, which is constant time. | 口令只存 guomi.HashPassword 的结果，
//     并以常量时间的 guomi.VerifyPassword 比对。
//
// Typical wiring | 典型装配：
//
//	store := auth.NewMemoryStore()
//	svc, err := auth.NewService(store, auth.Options{MasterKey: master})
//	issuer, err := auth.NewTokenIssuer(svc, auth.TokenOptions{TTL: 2 * time.Hour})
//	r.Use(auth.Middleware(auth.MiddlewareOptions{Issuer: issuer}))
//	r.GET("/users", listUsers, auth.RequirePermission("user:read"))
//
// The package emits no log output except a single warning when no master key is
// configured. | 本包除「未配置主密钥」的一条告警外不产生任何日志输出。
package auth

import (
	"crypto/rand"
	"strconv"
	"time"

	"github.com/shibingli/yd/core/b64x"
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/security/guomi"
)

// Status is the lifecycle state shared by tenants and users. | Status 是租户与用户共用的生命周期状态。
type Status string

const (
	// StatusActive marks a tenant or user that may sign in and be served. | StatusActive 表示租户或用户可登录、可被服务。
	StatusActive Status = "active"

	// StatusDisabled marks a tenant or user that must be refused. | StatusDisabled 表示租户或用户必须被拒绝。
	StatusDisabled Status = "disabled"
)

const (
	// DefaultSecKeyLen is the per-user sec_key length used when Options.SecKeyLen is not positive. | DefaultSecKeyLen 是 Options.SecKeyLen 非正时使用的每用户 sec_key 长度。
	DefaultSecKeyLen = 32

	// MinSecKeyLen is the shortest sec_key this package will generate. | MinSecKeyLen 是本包允许生成的最短 sec_key。
	//
	// HMAC-SHA-256 keys shorter than the 32-byte block would weaken the signature,
	// so a smaller Options.SecKeyLen is raised to this floor with a warning. | HMAC-SHA-256 的密钥短于 32 字节会削弱签名强度，
	// 因此更小的 Options.SecKeyLen 会被提升到本下限并输出告警。
	MinSecKeyLen = 16

	// MinMasterKeyLen is the shortest master key Options.MasterKey accepts. | MinMasterKeyLen 是 Options.MasterKey 允许的最短长度。
	MinMasterKeyLen = 16

	// MinPasswordLen is the shortest password CreateUser and SetPassword accept. | MinPasswordLen 是 CreateUser 与 SetPassword 接受的最短口令长度。
	MinPasswordLen = 8

	// DefaultTokenTTL is the token lifetime used when TokenOptions.TTL is not positive. | DefaultTokenTTL 是 TokenOptions.TTL 非正时使用的令牌有效期。
	DefaultTokenTTL = 2 * time.Hour

	// DefaultIssuer is the "iss" value used when TokenOptions.Issuer is empty. | DefaultIssuer 是 TokenOptions.Issuer 为空时使用的 "iss" 取值。
	DefaultIssuer = "yd"

	// tenantKey is the parameter name carrying the tenant id, both as a JOSE
	// header parameter and as a payload claim. | tenantKey 是承载租户 ID 的参数名，同时用于 JOSE 头部参数与载荷声明。
	//
	// The header copy lets KeyFunc find the user without decoding the payload; the
	// claim copy is the verified one. | 头部中的那份让 KeyFunc 无需解析载荷即可定位用户；载荷中的那份才是已验签的。
	tenantKey = "tid"

	// idBytes is the entropy of a generated identifier. | idBytes 是生成标识符使用的随机字节数。
	idBytes = 16
)

// Tenant ID, user ID and role ID prefixes. | 租户 ID、用户 ID 与角色 ID 的前缀。
const (
	tenantIDPrefix = "tn_"
	userIDPrefix   = "us_"
	roleIDPrefix   = "rl_"
)

// Options configures a Service. | Options 配置 Service。
type Options struct {
	// Now overrides the clock; nil means time.Now. | Now 用于替换时钟；为空表示 time.Now。
	Now func() time.Time
	// MasterKey encrypts every user sec_key with SM4-GCM. | MasterKey 用 SM4-GCM 加密每个用户的 sec_key。
	//
	// It must be at least MinMasterKeyLen bytes and should come from configuration
	// or an environment variable; an empty value makes NewService generate a
	// temporary key and log a warning, which is acceptable in development only,
	// because the stored sec_keys become unreadable after a restart. | 长度不得小于
	// MinMasterKeyLen，应来自配置或环境变量；为空时 NewService 会生成临时主密钥并输出告警，
	// 该模式仅限开发：进程重启后已入库的 sec_key 将无法解密。
	MasterKey []byte
	// SecKeyLen is the per-user sec_key length in bytes; a value <= 0 selects
	// DefaultSecKeyLen. | SecKeyLen 是每用户 sec_key 的字节长度；<= 0 时取 DefaultSecKeyLen。
	SecKeyLen int
	// Iterations is the password hashing iteration count; <= 0 selects
	// guomi.DefaultIterations. | Iterations 是口令散列的迭代次数；<= 0 时取 guomi.DefaultIterations。
	Iterations int
}

// Service owns tenants, users, roles and the password and sec_key lifecycle. | Service 管理租户、用户、角色以及口令与 sec_key 的生命周期。
//
// A Service is safe for concurrent use. | Service 可安全并发使用。
type Service struct {
	// store is the persistence boundary. | store 是持久化边界。
	store Store
	// log receives the master-key warning. | log 接收主密钥告警。
	log *logx.Logger
	// now is the clock used for CreatedAt and UpdatedAt. | now 是用于 CreatedAt 与 UpdatedAt 的时钟。
	now func() time.Time
	// roles caches role permissions for the RBAC hot path. | roles 缓存角色权限，服务于 RBAC 热路径。
	roles *roleCache
	// dummyHash is a well-formed hash of an unguessable password, verified when
	// the user does not exist so that the timing does not betray user existence. | dummyHash 是一个
	// 「格式合法但不可猜中」的口令散列；用户不存在时也执行一次校验，使耗时不会泄露用户是否存在。
	dummyHash string
	// secKeyLen is the per-user sec_key length in bytes. | secKeyLen 是每用户 sec_key 的字节长度。
	secKeyLen int
	// iterations is the password hashing iteration count. | iterations 是口令散列的迭代次数。
	iterations int
	// masterKey is the 16-byte SM4 key derived from Options.MasterKey with SM3. | masterKey 是由 Options.MasterKey 经 SM3 派生的 16 字节 SM4 密钥。
	masterKey [guomi.SM4KeySize]byte
}

// NewService builds a Service over store. | NewService 基于 store 构建 Service。
//
// A nil store is refused with ErrNilStore; a master key shorter than
// MinMasterKeyLen is refused with ErrMasterKeyRequired. | store 为空时以 ErrNilStore 拒绝；
// 主密钥短于 MinMasterKeyLen 时以 ErrMasterKeyRequired 拒绝。
func NewService(store Store, o Options) (*Service, error) {
	if store == nil {
		return nil, ErrNilStore
	}

	master := o.MasterKey
	if len(master) == 0 {
		// Development fallback: a random master key keeps the process runnable,
		// but every sec_key sealed with it dies with the process. | 开发兜底：随机主密钥让进程能跑起来，
		// 但用它封装的 sec_key 会随进程一起失效。
		generated, err := guomi.RandomKey()
		if err != nil {
			return nil, errx.Wrap(err, guomi.ErrRandomSource)
		}
		master = generated
		logx.Default().Warn("auth: no master key configured, using a temporary one, development only | auth：未配置主密钥，正在使用临时主密钥，仅限开发环境")
	} else if len(master) < MinMasterKeyLen {
		return nil, errx.Detail(ErrMasterKeyRequired, "master key length %[1]d is below the minimum %[2]d | 主密钥长度 %[1]d 小于最小长度 %[2]d", len(master), MinMasterKeyLen)
	}

	secKeyLen := o.SecKeyLen
	switch {
	case secKeyLen <= 0:
		secKeyLen = DefaultSecKeyLen
	case secKeyLen < MinSecKeyLen:
		logx.Default().Warn("auth: sec_key length raised to the minimum | auth：sec_key 长度已提升至下限",
			logx.Int("requested", secKeyLen), logx.Int("minimum", MinSecKeyLen))
		secKeyLen = MinSecKeyLen
	}

	now := o.Now
	if now == nil {
		now = time.Now
	}

	s := &Service{
		store:      store,
		log:        logx.Default(),
		now:        now,
		roles:      newRoleCache(store),
		dummyHash:  dummyPasswordHash(o.Iterations),
		secKeyLen:  secKeyLen,
		iterations: o.Iterations,
	}
	// The master key never signs a token; SM3 derivation pins it to the 16 bytes
	// SM4 requires and accepts any configured length. | 主密钥从不用于签发令牌；SM3 派生把它固定到
	// SM4 要求的 16 字节，并因此接受任意配置长度。
	sum := guomi.Sum(master)
	copy(s.masterKey[:], sum[:guomi.SM4KeySize])
	return s, nil
}

// dummyPasswordHash builds a syntactically valid guomi password hash whose digest
// is all zeros. | dummyPasswordHash 构造一个格式合法但摘要全零的 guomi 口令散列。
//
// No PBKDF2 work happens here: VerifyPassword reads the iteration count out of the
// string and does the hashing itself, which is exactly the cost-parity we want. | 这里不做
// 任何 PBKDF2 运算：VerifyPassword 会从字符串中读出迭代次数并自行散列，这正是我们要的耗时对齐。
func dummyPasswordHash(iterations int) string {
	if iterations <= 0 {
		iterations = guomi.DefaultIterations
	}
	salt := make([]byte, guomi.DefaultSaltLen)
	digest := make([]byte, guomi.SM3Size)
	return guomi.PasswordPrefix + "$" +
		strconv.Itoa(iterations) + "$" +
		b64x.StdEncoding.EncodeToString(salt) + "$" +
		b64x.StdEncoding.EncodeToString(digest)
}

// newID returns a random identifier carrying the given prefix. | newID 返回带指定前缀的随机标识符。
func newID(prefix string) (string, error) {
	var raw [idBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errx.Wrap(err, guomi.ErrRandomSource)
	}
	return prefix + b64x.RawURLEncoding.EncodeToString(raw[:]), nil
}

// randomBytes returns n cryptographically secure random bytes. | randomBytes 返回 n 个密码学安全的随机字节。
func randomBytes(n int) ([]byte, error) {
	out := make([]byte, n)
	if _, err := rand.Read(out); err != nil {
		return nil, errx.Wrap(err, guomi.ErrRandomSource)
	}
	return out, nil
}

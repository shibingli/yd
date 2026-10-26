// Package captcha wires ydx's human-verification module into a yd application. | Package captcha 把 ydx 的人机验证模块接入 yd 应用。
//
// The package owns the whole integration: configuration, secret resolution, the memory/Redis store choice, the
// manager lifecycle and the two verification points used by sign-in and password changes. Generated projects only
// call New, Register, Verify and Token; they never parse captcha configuration themselves. | 本包负责整条集成链路：
// 配置、密钥解析、内存/Redis 存储选择、管理器生命周期，以及登录与改密使用的两个校验点。生成项目只调用
// New、Register、Verify 与 Token，不自行解析验证码配置。
package captcha

import (
	"context"
	"errors"
	"strings"
	"sync"

	goredis "github.com/redis/go-redis/v9"

	ydcaptcha "github.com/shibingli/ydx/captcha"
	"github.com/shibingli/ydx/config"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/log"
	"github.com/shibingli/ydx/router"
)

// Actions and request constants shared by the server, templates and tests. | 服务端、模板与测试共用的动作与请求常量。
const (
	// ActionLogin is the verification action required by sign-in. | ActionLogin 是登录要求的验证动作。
	ActionLogin = "login"
	// ActionPasswordChange is the verification action required by a password change. | ActionPasswordChange 是修改口令要求的验证动作。
	ActionPasswordChange = "password_change"
	// Endpoint is the mount path of the captcha HTTP API and SDK assets. | Endpoint 是验证码 HTTP API 与 SDK 资产的挂载路径。
	Endpoint = "/captcha"
	// TokenField is the form field carrying a one-time verification token. | TokenField 是承载一次性验证令牌的表单字段。
	TokenField = "captcha_token"
	// maxTokenBytes bounds a submitted token before any parsing; real tokens are well under 1 KiB. |
	// maxTokenBytes 限制提交的令牌长度；真实令牌远小于 1 KiB。
	maxTokenBytes = 4096
)

// Store and fallback modes accepted by the configuration. | 配置接受的存储与降级模式。
const (
	// StoreAuto prefers Redis and falls back according to captcha.fallback. | StoreAuto 优先使用 Redis，并按 captcha.fallback 降级。
	StoreAuto = "auto"
	// StoreMemory keeps challenges in this process only. | StoreMemory 只在本进程内保存挑战。
	StoreMemory = "memory"
	// StoreRedis shares challenges through Redis. | StoreRedis 通过 Redis 共享挑战。
	StoreRedis = "redis"

	// FallbackMemory degrades to an in-process store when Redis is unavailable. | FallbackMemory 在 Redis 不可用时降级为进程内存储。
	FallbackMemory = "memory"
	// FallbackFail refuses to start when Redis is unavailable. | FallbackFail 在 Redis 不可用时拒绝启动。
	FallbackFail = "fail"
)

// DevSecret is the documented development signing key. | DevSecret 是文档化的开发签名密钥。
//
// It applies only while captcha is enabled, captcha.secret is empty and app.env is development; every other
// environment refuses to start instead. The value is public by design, exactly like the documented development
// master key and seed password. | 它只在验证码启用、captcha.secret 为空且 app.env 为 development 时生效；
// 其它环境一律拒绝启动。该取值按设计公开，与文档化的开发主密钥和种子口令完全一致。
const DevSecret = "yd-scaffold-development-captcha-secret-please-replace"

// Package-level sentinels, always matched with errors.Is. | 包级哨兵错误，一律用 errors.Is 判定。
var (
	// ErrConfig reports an unusable captcha configuration. | ErrConfig 表示验证码配置不可用。
	ErrConfig = errs.New("captcha: invalid configuration | captcha：配置不合法")
	// ErrSecret reports a missing signing secret outside development. | ErrSecret 表示非开发环境缺少签名密钥。
	ErrSecret = errs.New("captcha: signing secret is missing | captcha：签名密钥缺失")
	// ErrStore reports an unavailable shared store in strict mode. | ErrStore 表示严格模式下共享存储不可用。
	ErrStore = errs.New("captcha: shared store is unavailable | captcha：共享存储不可用")
	// ErrRequired reports a protected action submitted without a token. | ErrRequired 表示受保护动作没有携带令牌。
	ErrRequired = errs.New("captcha: verification is required | captcha：需要完成验证")
	// ErrInvalid reports a refused one-time token. | ErrInvalid 表示一次性令牌被拒绝。
	ErrInvalid = errs.New("captcha: verification failed | captcha：验证失败")
	// ErrUnknownAction reports a Verify call for an action the service does not know. It is a wiring bug, not a user
	// error, and must fail closed rather than silently skipping verification. | ErrUnknownAction 表示 Verify 收到了
	// 服务不认识的动作。它属于接线错误而非用户输入错误，必须失败关闭，而不是静默跳过校验。
	ErrUnknownAction = errs.New("captcha: unknown action | captcha：未知的动作")
)

// Service owns the captcha manager and the two action switches. | Service 持有验证码管理器与两个动作开关。
//
// A disabled service is valid and cheap: it holds no manager, no Redis client and no goroutine. | 关闭的服务是合法且轻量的：
// 它不持有管理器、Redis 客户端或协程。
type Service struct {
	// closeErr caches the joined close error of the first call. | closeErr 缓存第一次调用的聚合关闭错误。
	closeErr error
	// manager is nil while both actions are disabled. | 两个动作都关闭时 manager 为 nil。
	manager *ydcaptcha.Manager
	// closeStores releases the Redis client opened by New; nil for a caller-owned client. |
	// closeStores 释放 New 自行打开的 Redis 客户端；调用方持有的客户端为 nil。
	closeStores func() error
	// log receives verification warnings. | log 接收校验告警。
	log *log.Logger
	// closeOnce makes Close idempotent; the App may call it from a shutdown hook and a test may call it again. |
	// closeOnce 让 Close 幂等；App 可能在关停钩子里调用它，测试也可能再次调用。
	closeOnce sync.Once
	// login reports whether sign-in requires a token. | login 报告登录是否要求令牌。
	login bool
	// passwordChange reports whether a password change requires a token. | passwordChange 报告修改口令是否要求令牌。
	passwordChange bool
}

// New builds the service from the application configuration. | New 依据应用配置构建服务。
//
// Both action switches default to false, so a project that never enables captcha starts exactly as before. | 两个动作开关默认
// 为 false，因此从未启用验证码的项目启动行为与之前完全一致。
func New(cfg *config.Config, logger *log.Logger) (*Service, error) {
	return newService(cfg, logger, nil, true)
}

// NewWithClient builds the service on an existing Redis client. | NewWithClient 在既有 Redis 客户端上构建服务。
//
// The caller keeps ownership of sharedClient: it is probed but never closed by Service.Close, so a generated
// project can open one client for the cache, the message bus and captcha. A nil sharedClient means the caller
// already determined Redis is unavailable: the service never opens a second client and applies captcha.fallback
// directly. | 共享客户端由调用方持有：服务只探活、绝不关闭，因此生成项目可以让缓存、消息总线与验证码共用一个
// 客户端。sharedClient 为 nil 表示调用方已经判定 Redis 不可用：服务不会再次建连，而是直接应用 captcha.fallback。
func NewWithClient(cfg *config.Config, logger *log.Logger, sharedClient goredis.UniversalClient) (*Service, error) {
	return newService(cfg, logger, sharedClient, false)
}

// newService is the shared constructor; allowOpen lets the standalone New open its own Redis client. | newService 是
// 共享构造器；allowOpen 让独立的 New 自行打开 Redis 客户端。
func newService(cfg *config.Config, logger *log.Logger, sharedClient goredis.UniversalClient, allowOpen bool) (*Service, error) {
	if logger == nil {
		logger = log.Default()
	}
	opts, err := loadOptions(cfg, logger)
	if err != nil {
		return nil, err
	}

	service := &Service{
		log:            logger,
		login:          opts.login,
		passwordChange: opts.passwordChange,
	}
	if !opts.login && !opts.passwordChange {
		return service, nil
	}

	managerOptions := ydcaptcha.Options{
		Secret:         opts.secret,
		Logger:         logger,
		KeyPrefix:      opts.keyPrefix,
		DefaultLocale:  defaultLocale(cfg, logger),
		Types:          opts.types,
		TrustedProxies: opts.trustedProxies,
		Limits:         ydcaptcha.Limits{FailClosed: true},
	}
	closeStores, effectiveStore, err := applyStores(&managerOptions, cfg, opts, logger, sharedClient, allowOpen)
	if err != nil {
		return nil, err
	}
	manager, err := ydcaptcha.New(managerOptions)
	if err != nil {
		if closeStores != nil {
			if closeErr := closeStores(); closeErr != nil {
				logger.Warn("captcha store cleanup failed | 验证码存储清理失败", log.Err(closeErr))
			}
		}
		return nil, errs.Wrap(err, ErrConfig)
	}

	service.manager = manager
	service.closeStores = closeStores
	logger.Info("captcha enabled | 验证码已启用",
		log.Bool("login", opts.login),
		log.Bool("password_change", opts.passwordChange),
		log.Str("store", opts.store),
		log.Str("effective_store", effectiveStore),
	)
	return service, nil
}

// knownAction reports whether action is one of the two integration points. | knownAction 报告 action 是否属于两个集成点之一。
func knownAction(action string) bool {
	return action == ActionLogin || action == ActionPasswordChange
}

// Enabled reports whether action currently requires a verification token. | Enabled 报告 action 当前是否要求验证令牌。
func (s *Service) Enabled(action string) bool {
	if s == nil || s.manager == nil {
		return false
	}
	switch action {
	case ActionLogin:
		return s.login
	case ActionPasswordChange:
		return s.passwordChange
	default:
		return false
	}
}

// Verify consumes the one-time token for action. | Verify 消费 action 对应的一次性令牌。
//
// A disabled action returns nil; a missing token returns ErrRequired; any refusal from the manager returns
// ErrInvalid; an unknown action while the manager exists returns ErrUnknownAction. The failure copy deliberately stays
// generic and never includes the token or the underlying reason. | 关闭的动作返回 nil；缺少令牌返回 ErrRequired；管理器
// 拒绝返回 ErrInvalid；管理器存在时未知动作返回 ErrUnknownAction。失败文案刻意保持通用，不包含令牌或底层原因。
func (s *Service) Verify(ctx context.Context, token, action string) error {
	if s == nil {
		return nil
	}
	// A known-but-disabled action is intentionally a no-op; an unknown action while the manager exists is a wiring
	// bug and must fail closed instead of silently disabling the control for a new endpoint. | 已知但关闭的动作是
	// 有意的空操作；管理器存在时遇到未知动作属于接线错误，必须失败关闭，而不是让新端点的校验静默失效。
	if !knownAction(action) {
		if s.manager == nil {
			return nil
		}
		return errs.Detail(ErrUnknownAction, "%[1]q is not a captcha action | %[1]q 不是验证码动作", action)
	}
	if !s.Enabled(action) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrRequired
	}
	if len(token) > maxTokenBytes {
		if s.log != nil {
			s.log.Debug("captcha token is oversized | 验证码令牌过长", log.Str("action", action))
		}
		return ErrInvalid
	}
	if _, err := s.manager.ConsumeToken(ctx, token, action); err != nil {
		if s.log != nil {
			// A forged, expired or already-used token is expected attack noise; store and limiter failures are not.
			// Keeping the former at Debug leaves the access log as the single per-request record. | 伪造、过期或已使用的
			// 令牌是预期的攻击噪音，存储与限流故障不是；前者记 Debug，让访问日志成为每请求唯一的记录。
			if isTokenRefusal(err) {
				s.log.Debug("captcha token refused | 验证码令牌被拒绝",
					log.Str("action", action),
					log.Err(err),
				)
			} else {
				s.log.Warn("captcha verification failed | 验证码校验失败",
					log.Str("action", action),
					log.Err(err),
				)
			}
		}
		return errs.Wrap(err, ErrInvalid)
	}
	return nil
}

// isTokenRefusal reports the three expected one-time-token refusals. | isTokenRefusal 报告三种预期的一次性令牌拒绝。
func isTokenRefusal(err error) bool {
	return errors.Is(err, ydcaptcha.ErrTokenInvalid) ||
		errors.Is(err, ydcaptcha.ErrTokenExpired) ||
		errors.Is(err, ydcaptcha.ErrTokenUsed)
}

// Register mounts the captcha HTTP API and embedded SDK assets on r. | Register 在 r 上挂载验证码 HTTP API 与内嵌 SDK 资产。
//
// A disabled service mounts nothing, so /captcha stays a 404 in projects that never enabled it. | 关闭的服务不挂载任何路由，
// 因此从未启用验证码的项目里 /captcha 始终是 404。
func (s *Service) Register(r *router.Router) {
	if s == nil || s.manager == nil || r == nil {
		return
	}
	handler := s.manager.Handler(ydcaptcha.HandlerOptions{Prefix: Endpoint})
	if handler != nil {
		handler.Bind(r)
	}
}

// Close stops the manager and releases the Redis client only when New opened it. A caller-owned client passed to
// NewWithClient is never closed, and no policy refresh loop exists unless a PolicyProvider is configured. |
// Close 停止管理器，并且只在 New 自行打开 Redis 客户端时释放它。传给 NewWithClient 的调用方客户端绝不关闭；
// 未配置 PolicyProvider 时也不存在策略刷新循环。
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		var joined []error
		if s.manager != nil {
			joined = append(joined, s.manager.Close())
		}
		if s.closeStores != nil {
			joined = append(joined, s.closeStores())
		}
		s.closeErr = errors.Join(joined...)
	})
	return s.closeErr
}

// Token reads the one-time token from the POST body first and the X-Captcha-Token header second. | Token 先读 POST
// 正文里的表单字段，再读 X-Captcha-Token 请求头中的一次性令牌。
//
// The Authorization header is deliberately not consulted: on the password-change endpoint it carries the session
// token, and treating that as a captcha token would make the refusal path ambiguous. API clients use
// X-Captcha-Token. The URL query is not a token source either: query strings leak into access logs, browser history
// and Referer headers, so a token placed there would outlive its one-time use in more places than the header. |
// 刻意不读取 Authorization：改密端点上的它承载会话令牌，把它当作验证码令牌会让拒绝路径变得含糊。API 客户端使用
// X-Captcha-Token。URL 查询串也不是令牌来源：查询串会进入访问日志、浏览器历史与 Referer，放在那里的令牌会在
// 比请求头更多的地方留下副本，违背一次性语义。
func Token(ctx *router.Ctx) string {
	if ctx == nil || ctx.Request == nil {
		return ""
	}
	if value := strings.TrimSpace(ctx.Request.PostFormValue(TokenField)); value != "" {
		return value
	}
	return strings.TrimSpace(ctx.Request.Header.Get(ydcaptcha.TokenHeader))
}

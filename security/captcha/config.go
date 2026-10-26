package captcha

import (
	"context"
	"net/netip"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	ydcaptcha "github.com/shibingli/ydx/captcha"
	ydcore "github.com/shibingli/ydx/captcha/core"
	"github.com/shibingli/ydx/captcha/store/redisstore"
	"github.com/shibingli/ydx/config"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/log"
	"github.com/shibingli/ydx/middleware/ratelimit"
	yredis "github.com/shibingli/ydx/redis"

	"github.com/shibingli/yd/core/envx"
)

// Configuration keys and fixed values. | 配置键与固定取值。
const (
	// configEnv is the runtime environment key, shared with the application facade. | configEnv 是运行环境键，与应用门面共用。
	configEnv = "app.env"
	// configLogin enables sign-in verification. | configLogin 启用登录验证。
	configLogin = "captcha.login"
	// configPasswordChange enables password-change verification. | configPasswordChange 启用改密验证。
	configPasswordChange = "captcha.password_change"
	// configSecret is the HMAC signing secret. | configSecret 是 HMAC 签名密钥。
	configSecret = "captcha.secret"
	// configStore selects auto, memory or redis. | configStore 选择 auto、memory 或 redis。
	configStore = "captcha.store"
	// configFallback selects memory or fail when Redis is unavailable. | configFallback 选择 Redis 不可用时降级到内存还是失败。
	configFallback = "captcha.fallback"
	// configTypes lists the enabled visual challenge types. | configTypes 列出启用的视觉题型。
	configTypes = "captcha.types"
	// configKeyPrefix is the namespace shared by every captcha key. | configKeyPrefix 是所有验证码键共享的命名空间。
	configKeyPrefix = "captcha.key_prefix"
	// configTrustedProxies lists the reverse proxies allowed to supply the client address. | configTrustedProxies 列出允许提供客户端地址的反向代理。
	configTrustedProxies = "captcha.trusted_proxies"
	// configDefaultLang is the fallback prompt language. | configDefaultLang 是提示语的默认语言。
	configDefaultLang = "i18n.default_lang"

	// DefaultKeyPrefix is the captcha key namespace used when none is configured. | DefaultKeyPrefix 是未配置时使用的验证码键命名空间。
	DefaultKeyPrefix = "yd:captcha:"
	// redisProbeTimeout bounds the startup liveness probe. | redisProbeTimeout 限制启动期探活时长。
	redisProbeTimeout = 2 * time.Second
	// minimumSecretBytes is the shortest accepted signing secret. | minimumSecretBytes 是可接受的最短签名密钥长度。
	minimumSecretBytes = 16
)

// visualTypes maps the configuration spelling onto the module's challenge types. | visualTypes 把配置写法映射到模块的题型。
var visualTypes = map[string]ydcaptcha.Type{
	"text":       ydcaptcha.TypeText,
	"click_text": ydcaptcha.TypeClickText,
	"click_icon": ydcaptcha.TypeClickIcon,
	"semantic":   ydcaptcha.TypeSemantic,
	"slide":      ydcaptcha.TypeSlide,
	"scratch":    ydcaptcha.TypeScratch,
	"rotate":     ydcaptcha.TypeRotate,
}

// options is the subset of the configuration that decides how the manager is built. | options 是决定管理器如何构建的配置子集。
type options struct {
	// types lists the enabled visual types; nil means every built-in visual type. | types 列出启用的视觉题型；nil 表示全部内置视觉题型。
	types []ydcaptcha.Type
	// secret is the resolved HMAC signing key. | secret 是解析后的 HMAC 签名密钥。
	secret []byte
	// store is the normalized store mode. | store 是归一化后的存储模式。
	store string
	// fallback is the normalized fallback mode. | fallback 是归一化后的降级模式。
	fallback string
	// keyPrefix is the key namespace. | keyPrefix 是键命名空间。
	keyPrefix string
	// trustedProxies lists the reverse proxies trusted to supply the client address. | trustedProxies 列出允许提供客户端地址的反向代理。
	trustedProxies []netip.Prefix
	// login enables sign-in verification. | login 启用登录验证。
	login bool
	// passwordChange enables password-change verification. | passwordChange 启用改密验证。
	passwordChange bool
}

// loadOptions reads and validates the captcha configuration. | loadOptions 读取并校验验证码配置。
func loadOptions(cfg *config.Config, logger *log.Logger) (options, error) {
	if cfg == nil {
		return options{}, errs.Detail(ErrConfig, "nil configuration | 配置为空")
	}

	o := options{
		login:          cfg.GetBool(configLogin),
		passwordChange: cfg.GetBool(configPasswordChange),
	}

	// Both switches off means the feature is unused: leave every other captcha key alone so an upgraded project with
	// stale or invalid captcha settings still starts exactly as before. | 两个开关都关闭表示该功能未使用：不再读取其它
	// 验证码配置，使升级后仍保留陈旧或非法取值的项目照旧启动。
	if !o.login && !o.passwordChange {
		return o, nil
	}

	o.store = normalizeMode(cfg.GetString(configStore), StoreAuto)
	o.fallback = normalizeMode(cfg.GetString(configFallback), FallbackMemory)
	o.keyPrefix = strings.TrimSpace(cfg.GetString(configKeyPrefix))
	if o.keyPrefix == "" {
		o.keyPrefix = DefaultKeyPrefix
	}
	switch o.store {
	case StoreAuto, StoreMemory, StoreRedis:
	default:
		return options{}, errs.Detail(ErrConfig, "unknown captcha store %[1]q (want auto, memory or redis) | 未知的验证码存储 %[1]q（期望 auto、memory 或 redis）", o.store)
	}
	switch o.fallback {
	case FallbackMemory, FallbackFail:
	default:
		return options{}, errs.Detail(ErrConfig, "unknown captcha fallback %[1]q (want memory or fail) | 未知的验证码降级策略 %[1]q（期望 memory 或 fail）", o.fallback)
	}
	if strings.ContainsAny(o.keyPrefix, "\r\n") {
		return options{}, errs.Detail(ErrConfig, "captcha key prefix contains a line break | 验证码键前缀包含换行")
	}
	trustedProxies, err := parseTrustedProxies(cfg.GetStringSlice(configTrustedProxies))
	if err != nil {
		return options{}, err
	}
	o.trustedProxies = trustedProxies

	secret, err := resolveSecret(cfg, logger)
	if err != nil {
		return options{}, err
	}
	types, err := parseTypes(cfg.GetStringSlice(configTypes))
	if err != nil {
		return options{}, err
	}
	o.secret = secret
	o.types = types
	return o, nil
}

// resolveSecret returns the configured secret or the documented development fallback. | resolveSecret 返回配置的密钥或文档化的开发回退。
func resolveSecret(cfg *config.Config, logger *log.Logger) ([]byte, error) {
	secret := strings.TrimSpace(cfg.GetString(configSecret))
	env := envx.Normalize(cfg.GetString(configEnv))
	if env == "" {
		env = envx.Development
	}
	if secret != "" {
		if len(secret) < minimumSecretBytes {
			return nil, errs.Detail(ErrSecret, "captcha.secret is shorter than %[1]d bytes | captcha.secret 短于 %[1]d 字节", minimumSecretBytes)
		}
		// The documented development secret is public; using it outside development would let anyone forge
		// one-time tokens. | 文档化开发密钥是公开知识；在非开发环境使用它，任何人都能伪造一次性令牌。
		if env != envx.Development && secret == DevSecret {
			return nil, errs.Detail(ErrSecret, "the documented development captcha secret must not be used outside development | 文档化开发验证码密钥不得在非开发环境使用")
		}
		return []byte(secret), nil
	}

	if env == envx.Development {
		if logger != nil {
			logger.Warn("captcha.secret is not configured; using the documented development secret | captcha.secret 未配置，使用文档化的开发密钥")
		}
		return []byte(DevSecret), nil
	}
	return nil, errs.Detail(ErrSecret,
		"captcha.secret is required outside development; set YD_CAPTCHA_SECRET | 非开发环境必须配置 captcha.secret；请设置 YD_CAPTCHA_SECRET")
}

// parseTypes turns the configured names into the module's challenge types. | parseTypes 把配置的题型名转换为模块的题型。
//
// An empty list means every built-in visual type, which is the module's own default. Unknown names are refused at
// startup rather than silently ignored. | 空列表表示全部内置视觉题型，即模块自身的默认值；未知名称在启动期拒绝，而不是静默忽略。
func parseTypes(values []string) ([]ydcaptcha.Type, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]ydcaptcha.Type, 0, len(values))
	seen := make(map[ydcaptcha.Type]bool, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			name := strings.ToLower(strings.TrimSpace(part))
			if name == "" {
				continue
			}
			typ, ok := visualTypes[name]
			if !ok {
				return nil, errs.Detail(ErrConfig, "unsupported captcha type %[1]q | 不支持的验证码题型 %[1]q", name)
			}
			if seen[typ] {
				continue
			}
			seen[typ] = true
			out = append(out, typ)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// applyStores selects the memory or Redis store and reports the Redis close function together with the store that is
// actually in effect. | applyStores 选择内存或 Redis 存储，并返回 Redis 的关闭函数与实际生效的存储。
func applyStores(target *ydcaptcha.Options, cfg *config.Config, o options, logger *log.Logger, shared goredis.UniversalClient, allowOpen bool) (func() error, string, error) {
	if o.store == StoreMemory {
		warnMemoryInProduction(cfg, logger)
		return nil, StoreMemory, nil
	}

	client := shared
	owned := false
	if client == nil {
		if !allowOpen {
			// The caller already probed Redis and found it unavailable; opening a second client here would duplicate
			// both the connection attempt and the startup latency. | 调用方已经探活并确认 Redis 不可用；在这里再开一个
			// 客户端会重复连接尝试与启动延迟。
			return fallbackStore(errs.Detail(ErrStore, "shared Redis client is unavailable | 共享 Redis 客户端不可用"), cfg, o, logger)
		}
		opened, err := yredis.Open(yredis.OptionsFromConfig(cfg))
		if err != nil {
			return fallbackStore(errs.Wrap(err, ErrStore), cfg, o, logger)
		}
		client = opened
		owned = true
	}

	// The generated project already probed this client in service.NewRedis, but a direct NewWithClient caller may not
	// have; one startup round trip keeps the fallback decision honest for both. | 生成项目已在 service.NewRedis 探活过这个
	// 客户端，但直接的 NewWithClient 调用方未必；一次启动期往返让两种调用方的降级判定都可靠。
	probeCtx, cancel := context.WithTimeout(context.Background(), redisProbeTimeout)
	pingErr := client.Ping(probeCtx).Err()
	cancel()
	if pingErr != nil {
		closeOwnedStore(client, owned, logger)
		return fallbackStore(errs.Wrap(pingErr, ErrStore), cfg, o, logger)
	}

	store, images, limits, err := buildRedisStores(client, o.keyPrefix)
	if err != nil {
		closeOwnedStore(client, owned, logger)
		return fallbackStore(errs.Wrap(err, ErrStore), cfg, o, logger)
	}
	target.Store = store
	target.ImageStore = images
	target.Limits.Store = prefixedLimiter{base: limits, prefix: o.keyPrefix}
	target.Limits.FailClosed = true
	if owned {
		return func() error { return client.Close() }, StoreRedis, nil
	}
	return nil, StoreRedis, nil
}

// closeOwnedStore releases a client this package opened; a caller-owned client is never touched here. Close failures
// are logged rather than swallowed because a leaked pool is hardest to notice during a failed startup. |
// closeOwnedStore 释放本包自行打开的客户端；调用方持有的客户端绝不在这里关闭。关闭失败必须记录而不是吞掉，
// 因为启动失败的现场最难观察连接池泄漏。
func closeOwnedStore(client goredis.UniversalClient, owned bool, logger *log.Logger) {
	if !owned || client == nil {
		return
	}
	if err := client.Close(); err != nil && logger != nil {
		logger.Warn("captcha Redis client cleanup failed | 验证码 Redis 客户端清理失败", log.Err(err))
	}
}

// fallbackStore applies captcha.fallback after the shared store turned out to be unusable. | fallbackStore 在共享存储
// 不可用后应用 captcha.fallback。
func fallbackStore(err error, cfg *config.Config, o options, logger *log.Logger) (func() error, string, error) {
	if o.fallback == FallbackFail {
		return nil, "", err
	}
	if logger != nil {
		logger.Warn("captcha Redis store is unavailable; falling back to memory | 验证码 Redis 存储不可用，回退到内存",
			log.Err(err),
		)
	}
	warnMemoryInProduction(cfg, logger)
	return nil, "memory (fallback)", nil
}

// buildRedisStores builds the three shared stores from one client. | buildRedisStores 用同一个客户端构建三种共享存储。
func buildRedisStores(client goredis.UniversalClient, keyPrefix string) (ydcaptcha.Store, ydcaptcha.ImageStore, ratelimit.LimitStore, error) {
	store, err := redisstore.NewStore(redisstore.StoreOptions{Client: client})
	if err != nil {
		return nil, nil, nil, err
	}
	images, err := redisstore.NewImageStore(redisstore.ImageStoreOptions{Client: client})
	if err != nil {
		return nil, nil, nil, err
	}
	limits, err := redisstore.NewRateLimitStore(client, keyPrefix+"rl:")
	if err != nil {
		return nil, nil, nil, err
	}
	return store, images, limits, nil
}

// prefixedLimiter strips the manager's key prefix before handing the key to the shared limiter. | prefixedLimiter 在把键
// 交给共享限流器之前去掉管理器自己的键前缀，避免键前缀重复两层。
type prefixedLimiter struct {
	// base is the Redis-backed token bucket. | base 是 Redis 支撑的令牌桶。
	base ratelimit.LimitStore
	// prefix is the manager prefix already present in the key. | prefix 是键里已经存在的管理器前缀。
	prefix string
}

// Allow implements ratelimit.LimitStore. | Allow 实现 ratelimit.LimitStore。
func (p prefixedLimiter) Allow(ctx context.Context, key string, rate float64, burst int) (ratelimit.Decision, error) {
	if p.base == nil {
		return ratelimit.Decision{}, ErrStore
	}
	if p.prefix != "" {
		key = strings.TrimPrefix(key, p.prefix)
	}
	return p.base.Allow(ctx, key, rate, burst)
}

// warnMemoryInProduction makes a per-process store visible to operators. | warnMemoryInProduction 让运维看到进程内存储这一事实。
func warnMemoryInProduction(cfg *config.Config, logger *log.Logger) {
	if cfg == nil || logger == nil {
		return
	}
	env := envx.Normalize(cfg.GetString(configEnv))
	if env == "" {
		env = envx.Development
	}
	if env == envx.Production {
		logger.Warn("captcha state is held in memory; multiple replicas will not share challenges | 验证码状态保存在内存中；多副本不会共享挑战")
	}
}

// parseTrustedProxies parses CIDR prefixes (a bare IP is treated as a single host). | parseTrustedProxies 解析 CIDR
// 前缀（裸 IP 视为单主机）。
func parseTrustedProxies(values []string) ([]netip.Prefix, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]netip.Prefix, 0, len(values))
	seen := make(map[netip.Prefix]bool, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			entry := strings.TrimSpace(part)
			if entry == "" {
				continue
			}
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				addr, addrErr := netip.ParseAddr(entry)
				if addrErr != nil {
					return nil, errs.Detail(ErrConfig, "invalid captcha trusted proxy %[1]q | 非法的验证码可信代理 %[1]q", entry)
				}
				prefix = netip.PrefixFrom(addr, addr.BitLen())
			}
			prefix = prefix.Masked()
			if seen[prefix] {
				continue
			}
			seen[prefix] = true
			out = append(out, prefix)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// defaultLocale returns a prompt language the captcha catalog actually supports. The application may add its own
// i18n catalogue for a language the captcha SDK does not ship; refusing to start over that would take the whole
// application down for a missing prompt translation, so an unknown value falls back to zh-CN with one warning. |
// defaultLocale 返回验证码目录真正支持的提示语言。应用可能为自己的 i18n 新增了验证码 SDK 未内置的语言；因为缺少
// 一条提示翻译就让整个应用拒绝启动并不合理，因此未知取值回退到 zh-CN 并告警一次。
func defaultLocale(cfg *config.Config, logger *log.Logger) string {
	lang := "zh-CN"
	if cfg != nil {
		if configured := strings.TrimSpace(cfg.GetString(configDefaultLang)); configured != "" {
			lang = configured
		}
	}
	if _, err := ydcore.NewLocalizer(ydcore.Options{DefaultLocale: lang}); err != nil {
		if logger != nil {
			logger.Warn("captcha default language is unsupported; falling back to zh-CN | 验证码默认语言不受支持，回退到 zh-CN",
				log.Str("lang", lang),
			)
		}
		return "zh-CN"
	}
	return lang
}

// normalizeMode trims and lowers a mode, falling back to def when it is empty. | normalizeMode 去空格并小写化模式，
// 空值回退到 def。
func normalizeMode(value, def string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		return def
	}
	return mode
}

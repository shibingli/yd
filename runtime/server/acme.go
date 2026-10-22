package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/acme/autocert"

	"github.com/shibingli/ydx/errs"
)

// Sentinel errors of the ACME manager, all testable with errors.Is. | ACME 管理器的哨兵错误，均可通过 errors.Is 判定。
var (
	// ErrACMEDomainsRequired reports ACME enabled without any domain. | ErrACMEDomainsRequired 表示启用 ACME 却没有配置任何域名。
	ErrACMEDomainsRequired = errs.New("server: enabling ACME requires at least one domain | server：启用 ACME 必须配置至少一个域名")

	// ErrACMEHostMissing reports an empty host handed to the non-strict host policy. | ErrACMEHostMissing 表示非严格主机策略收到空主机名。
	ErrACMEHostMissing = errs.New("acme: missing server name | acme：缺少服务器名")

	// ErrACMEUninitialized reports a nil or uninitialized ACME manager. | ErrACMEUninitialized 表示 ACME 管理器为 nil 或未初始化。
	ErrACMEUninitialized = errs.New("server: ACME is not initialized | server：ACME 未初始化")
)

// acmeChallengePrefix is the well-known path served by the ACME HTTP-01 challenge. | acmeChallengePrefix 是 ACME HTTP-01 质询使用的 well-known 路径。
const acmeChallengePrefix = "/.well-known/acme-challenge/"

// acmeManager wraps autocert with an explicit, testable host policy. | acmeManager 包装 autocert，并提供可显式测试的主机策略。
type acmeManager struct {
	manager    *autocert.Manager
	hostPolicy autocert.HostPolicy
}

// defaultACMECacheDir returns where issued certificates are cached when the configuration does not
// say. | defaultACMECacheDir 返回配置未指定时已签发证书的缓存位置。
//
// The directory is absolute on purpose. A service manager starts the process in a fixed system
// directory (Windows SCM uses System32, systemd typically "/"), so a relative default would write
// certificates into a system directory or fail there for an unprivileged account. The user cache
// directory is per-account and writable; when the platform cannot report one, the executable's
// directory is the next best absolute location. | 该目录刻意取绝对路径。服务管理器在固定的系统目录
// 启动进程（Windows SCM 使用 System32，systemd 通常是 "/"），相对默认值会把证书写进系统目录，或让
// 非特权账号在那里失败。用户缓存目录按账号隔离且可写；平台无法给出时，可执行文件所在目录是下一个
// 绝对位置。
func defaultACMECacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "yd", "acme")
	}
	if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
		return filepath.Join(filepath.Dir(exe), ".yd", "acme-cache")
	}
	// No absolute location is available; keep the documented relative name rather than inventing
	// one. An operator who needs ACME behind a service manager should set server.acme.cache_dir. |
	// 没有任何绝对位置可用；保留文档中的相对名称，而不是凭空编造。在服务管理器后面使用 ACME 的运维
	// 应当显式配置 server.acme.cache_dir。
	return ".yd/acme-cache"
}

// newACMEManager builds the certificate manager. | newACMEManager 构造证书管理器。
//
// Certificates are cached on disk so renewals survive restarts; the host
// policy rejects unknown domains when strict mode is on, which prevents a
// misconfigured deployment from exhausting ACME rate limits. | 证书落盘缓存，
// 重启后仍可续期；严格模式下主机策略会拒绝未知域名，避免错误部署耗尽 ACME 配额。
func newACMEManager(cfg ACMEConfig) (*acmeManager, error) {
	if len(cfg.Domains) == 0 {
		return nil, ErrACMEDomainsRequired
	}
	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = defaultACMECacheDir()
	}
	policy := autocert.HostWhitelist(cfg.Domains...)
	if !cfg.HostPolicyStrict {
		// Non-strict mode: hosts outside the whitelist are allowed, but an empty host name is still rejected | 非严格模式：允许白名单之外的主机，但仍拒绝空主机名
		allowed := make(map[string]struct{}, len(cfg.Domains))
		for _, d := range cfg.Domains {
			allowed[strings.ToLower(d)] = struct{}{}
		}
		policy = func(_ context.Context, host string) error {
			if host == "" {
				return ErrACMEHostMissing
			}
			return nil
		}
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      cfg.Email,
		Cache:      autocert.DirCache(cacheDir),
		HostPolicy: policy,
	}
	return &acmeManager{manager: m, hostPolicy: policy}, nil
}

// HostPolicy exposes the effective policy for diagnostics and tests. | HostPolicy 暴露生效的策略，供诊断与测试使用。
func (a *acmeManager) HostPolicy(ctx context.Context, host string) error {
	if a == nil || a.hostPolicy == nil {
		return ErrACMEUninitialized
	}
	return a.hostPolicy(ctx, host)
}

// CertCacheDir reports where certificates are stored. | CertCacheDir 报告证书存放位置。
func (a *acmeManager) CertCacheDir() string {
	if a == nil || a.manager == nil {
		return ""
	}
	if c, ok := a.manager.Cache.(autocert.DirCache); ok {
		return string(c)
	}
	return ""
}

// String implements fmt.Stringer for logging. | String 实现 fmt.Stringer 便于日志输出。
func (a *acmeManager) String() string {
	if a == nil {
		return "acme(disabled)"
	}
	return fmt.Sprintf("acme(cache=%s)", a.CertCacheDir())
}

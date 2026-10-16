package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/acme/autocert"
)

// acmeChallengePrefix is the well-known path served by the ACME HTTP-01 challenge. | acmeChallengePrefix 是 ACME HTTP-01 质询使用的 well-known 路径。
const acmeChallengePrefix = "/.well-known/acme-challenge/"

// acmeManager wraps autocert with an explicit, testable host policy. | acmeManager 包装 autocert，并提供可显式测试的主机策略。
type acmeManager struct {
	manager    *autocert.Manager
	hostPolicy autocert.HostPolicy
}

// newACMEManager builds the certificate manager. | newACMEManager 构造证书管理器。
//
// Certificates are cached on disk so renewals survive restarts; the host
// policy rejects unknown domains when strict mode is on, which prevents a
// misconfigured deployment from exhausting ACME rate limits. | 证书落盘缓存，
// 重启后仍可续期；严格模式下主机策略会拒绝未知域名，避免错误部署耗尽 ACME 配额。
func newACMEManager(cfg ACMEConfig) (*acmeManager, error) {
	if len(cfg.Domains) == 0 {
		return nil, errors.New("server: 启用 ACME 必须配置至少一个域名")
	}
	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		cacheDir = ".yd/acme-cache"
	}
	policy := autocert.HostWhitelist(cfg.Domains...)
	if !cfg.HostPolicyStrict {
		// 非严格模式：允许白名单之外的主机，但仍拒绝空主机名
		allowed := make(map[string]struct{}, len(cfg.Domains))
		for _, d := range cfg.Domains {
			allowed[strings.ToLower(d)] = struct{}{}
		}
		policy = func(_ context.Context, host string) error {
			if host == "" {
				return errors.New("acme: 缺少服务器名")
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
		return errors.New("server: ACME 未初始化")
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

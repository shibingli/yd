package server

import (
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/shibingli/ydx/config"
	"github.com/shibingli/ydx/log"
)

// TLSConfig holds the certificate source for HTTPS. | TLSConfig 描述 HTTPS 的证书来源。
type TLSConfig struct {
	// CertFile is the PEM certificate path. | CertFile 是 PEM 证书路径。
	CertFile string
	// KeyFile is the PEM private key path. | KeyFile 是 PEM 私钥路径。
	KeyFile string
	// MinVersion is the minimum TLS version; 0 means TLS 1.2. | MinVersion 为最低 TLS 版本，0 表示 TLS 1.2。
	MinVersion uint16
	// Enabled turns on TLS for the HTTP listener. | Enabled 为 HTTP 监听开启 TLS。
	Enabled bool
}

// HTTP3Config describes the QUIC listener. | HTTP3Config 描述 QUIC 监听配置。
type HTTP3Config struct {
	// Enabled turns on the HTTP/3 (QUIC) listener. | Enabled 为 HTTP/3（QUIC）监听开关。
	Enabled bool
	// Port is the UDP port; 0 means "same as the TCP port". | Port 为 UDP 端口，0 表示与 TCP 端口相同。
	Port int
	// WebTransport enables WebTransport sessions on the same QUIC listener. It
	// requires Enabled and a TLS certificate. | WebTransport 在同一条 QUIC 监听上
	// 启用 WebTransport 会话；要求 Enabled 为真且已配置 TLS 证书。
	WebTransport bool
}

// ACMEConfig describes automatic certificate issuance. | ACMEConfig 描述自动证书签发配置。
type ACMEConfig struct {
	// Email is the ACME account contact. | Email 是 ACME 账户联系邮箱。
	Email string
	// CacheDir stores issued certificates. | CacheDir 用于存放已签发证书。
	CacheDir string
	// Domains is the host whitelist. | Domains 是主机白名单。
	Domains []string
	// Enabled turns on the ACME manager. | Enabled 为 ACME 管理器开关。
	Enabled bool
	// HostPolicyStrict rejects hosts outside Domains. | HostPolicyStrict 拒绝不在 Domains 中的主机。
	HostPolicyStrict bool
}

// Options configures a Server. | Options 是服务器的构造参数。
type Options struct {

	// Handler serves every request (usually the router). | Handler 处理全部请求（通常是路由）。
	Handler http.Handler
	// Logger records lifecycle events. | Logger 记录生命周期事件。
	Logger *log.Logger
	// Host is the bind address; empty means all interfaces. | Host 为绑定地址，空表示全部网卡。
	Host string

	// TLS is the certificate source. | TLS 为证书来源。
	TLS TLSConfig
	// ACME configures automatic certificates. | ACME 配置自动证书。
	ACME ACMEConfig

	// HTTP3 configures the QUIC listener. | HTTP3 配置 QUIC 监听。
	HTTP3 HTTP3Config
	// Port is the TCP port; 0 picks a free port. | Port 为 TCP 端口，0 表示自动选择空闲端口。
	Port int

	// ReadTimeout limits the whole request read. | ReadTimeout 限制整个请求读取时长。
	ReadTimeout time.Duration
	// ReadHeaderTimeout limits reading request headers. | ReadHeaderTimeout 限制请求头读取时长。
	ReadHeaderTimeout time.Duration
	// WriteTimeout limits the response write. | WriteTimeout 限制响应写入时长。
	WriteTimeout time.Duration
	// IdleTimeout limits keep-alive idle time. | IdleTimeout 限制长连接空闲时长。
	IdleTimeout time.Duration
	// ShutdownTimeout bounds graceful shutdown. | ShutdownTimeout 限定优雅退出的最长等待。
	ShutdownTimeout time.Duration

	// H2C enables cleartext HTTP/2 (h2c) on the same port. | H2C 在同端口开启明文 HTTP/2（h2c）。
	H2C bool
	// ForceHTTP2ExtendedConnect makes Start fail unless the process was started with | ForceHTTP2ExtendedConnect 使 Start 在进程未以
	// GODEBUG=http2xconnect=1 启动时失败（仅在 HTTP/2 可能启用时检查）。 | GODEBUG=http2xconnect=1 启动时返回错误（仅在 HTTP/2 可能启用时检查）。
	ForceHTTP2ExtendedConnect bool
}

// OptionsFromConfig reads server settings from the layered config. | OptionsFromConfig 从分层配置读取服务器设置。
//
// Every key can be overridden by an environment variable (prefix YD_) or a
// command-line flag, matching the framework-wide precedence rules. | 每个键都可被环境变量（前缀 YD_）
// 或命令行参数覆盖，与框架全局优先级规则一致。
func OptionsFromConfig(c *config.Config) Options {
	if c == nil {
		return Options{}
	}
	host := c.GetString("server.host")
	port := c.GetInt("server.port")
	o := Options{
		Host:                      host,
		Port:                      port,
		H2C:                       c.GetBool("server.h2c"),
		ForceHTTP2ExtendedConnect: c.GetBool("server.http2.extended_connect"),
		ReadTimeout:               c.Duration("server.read_timeout"),
		WriteTimeout:              c.Duration("server.write_timeout"),
		IdleTimeout:               c.Duration("server.idle_timeout"),
		ShutdownTimeout:           c.Duration("server.shutdown_timeout"),
		ReadHeaderTimeout:         c.Duration("server.read_header_timeout"),
	}
	o.TLS = TLSConfig{
		Enabled:  c.GetBool("server.tls.enabled"),
		CertFile: c.GetString("server.tls.cert_file"),
		KeyFile:  c.GetString("server.tls.key_file"),
	}
	// HTTP/3 may be written either as the object form (enabled/port/webtransport)
	// or as the legacy boolean scalar `server.http3: true`; the object wins when
	// present, and the scalar keeps old configurations working. | HTTP/3 既可以写成
	// 对象形式（enabled/port/webtransport），也可以写成旧的布尔标量
	// `server.http3: true`；对象存在时优先，标量让旧配置继续可用。
	h3Enabled := c.GetBool("server.http3.enabled")
	if !c.IsSet("server.http3.enabled") {
		if raw, ok := c.Get("server.http3").(bool); ok {
			h3Enabled = raw
		}
	}
	o.HTTP3 = HTTP3Config{
		Enabled:      h3Enabled,
		Port:         c.GetInt("server.http3.port"),
		WebTransport: c.GetBool("server.http3.webtransport"),
	}
	o.ACME = ACMEConfig{
		Enabled:          c.GetBool("server.acme.enabled"),
		Domains:          c.GetStringSlice("server.acme.domains"),
		Email:            c.GetString("server.acme.email"),
		CacheDir:         c.GetString("server.acme.cache_dir"),
		HostPolicyStrict: c.GetBool("server.acme.host_policy_strict"),
	}
	return o
}

// normalize fills safe defaults. | normalize 填充安全默认值。
func (o Options) normalize() Options {
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = 15 * time.Second
	}
	if o.ReadHeaderTimeout <= 0 {
		o.ReadHeaderTimeout = 10 * time.Second
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 15 * time.Second
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = 60 * time.Second
	}
	if o.ShutdownTimeout <= 0 {
		o.ShutdownTimeout = 10 * time.Second
	}
	return o
}

// addr builds a "host:port" address. | addr 拼接 "host:port" 地址。
func (o Options) addr() string {
	return net.JoinHostPort(o.Host, strconv.Itoa(o.Port))
}

// h3Addr builds the UDP address of the QUIC listener. | h3Addr 拼接 QUIC 监听的 UDP 地址。
func (o Options) h3Addr(tcpPort int) string {
	port := o.HTTP3.Port
	if port == 0 {
		port = tcpPort
	}
	return net.JoinHostPort(o.Host, strconv.Itoa(port))
}

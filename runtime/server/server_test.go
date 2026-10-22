package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/config"
	"github.com/spf13/afero"
)

// protoHandler echoes the negotiated protocol version. | protoHandler 回显协商出的协议版本。
func protoHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, r.Proto)
	})
}

// newTestServer builds a server bound to loopback with an ephemeral port. | newTestServer 构造绑定回环与随机端口的服务器。
func newTestServer(t *testing.T, mutate func(*Options)) *Server {
	t.Helper()
	o := Options{Host: "127.0.0.1", Port: 0, Handler: protoHandler()}
	if mutate != nil {
		mutate(&o)
	}
	srv := New(o)
	if err := srv.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv
}

func TestHTTP1AndH2CServeSameHandler(t *testing.T) {
	srv := newTestServer(t, func(o *Options) { o.H2C = true })
	base := "http://" + srv.Addr()

	// HTTP/1.1
	resp, err := http.Get(base + "/proto")
	if err != nil {
		t.Fatalf("HTTP/1.1 请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.ProtoMajor != 1 {
		t.Fatalf("默认请求应为 HTTP/1.1，实际 %s", resp.Proto)
	}
	if string(body) != "HTTP/1.1" {
		t.Fatalf("处理器看到的协议 = %q", body)
	}

	// h2c: enable cleartext HTTP/2 only, forcing h2c | h2c：仅启用明文 HTTP/2，强制走 h2c
	protos := new(http.Protocols)
	protos.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: protos}, Timeout: 5 * time.Second}
	resp2, err := client.Get(base + "/proto")
	if err != nil {
		t.Fatalf("h2c 请求失败: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if resp2.ProtoMajor != 2 {
		t.Fatalf("h2c 请求应为 HTTP/2，实际 %s", resp2.Proto)
	}
	if string(body2) != "HTTP/2.0" {
		t.Fatalf("h2c 下处理器看到的协议 = %q", body2)
	}
}

func TestH2CDisabledRejectsPriorKnowledgeHTTP2(t *testing.T) {
	srv := newTestServer(t, func(o *Options) { o.H2C = false })

	protos := new(http.Protocols)
	protos.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: protos}, Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + srv.Addr() + "/proto")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("未开启 h2c 时明文 HTTP/2 应失败，实际得到 %s", resp.Proto)
	}
}

func TestAltSvcHeaderAndHTTP3RoundTrip(t *testing.T) {
	srv := newTestServer(t, func(o *Options) { o.HTTP3 = HTTP3Config{Enabled: true} })

	if srv.HTTP3Addr() == "" {
		t.Fatal("HTTP3Addr must not be empty once HTTP/3 is enabled | 启用 HTTP/3 后 HTTP3Addr 不应为空")
	}
	if !strings.Contains(srv.AltSvc(), "h3=") {
		t.Fatalf("Alt-Svc = %q, 期望包含 h3=", srv.AltSvc())
	}

	// HTTP/1.1 over TLS: verify the Alt-Svc advertisement (an ephemeral self-signed certificate is used when HTTP/3 is enabled) | HTTP/1.1 over TLS：验证 Alt-Svc 通告（HTTP/3 启用时自动使用临时自签证书）
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   5 * time.Second,
	}
	resp, err := client.Get("https://" + srv.Addr() + "/proto")
	if err != nil {
		t.Fatalf("HTTPS 请求失败: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if got := resp.Header.Get("Alt-Svc"); !strings.Contains(got, "h3=") {
		t.Fatalf("Alt-Svc 响应头 = %q, 期望包含 h3=", got)
	}

	// Real HTTP/3 round trip | 真实 HTTP/3 往返
	tr := &http3.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer func() { _ = tr.Close() }()
	h3client := &http.Client{Transport: tr, Timeout: 8 * time.Second}
	resp3, err := h3client.Get("https://" + srv.HTTP3Addr() + "/proto")
	if err != nil {
		t.Fatalf("HTTP/3 请求失败: %v", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	_ = resp3.Body.Close()
	if resp3.ProtoMajor != 3 {
		t.Fatalf("HTTP/3 请求的协议 = %s, 期望 HTTP/3", resp3.Proto)
	}
	if string(body3) != "HTTP/3.0" {
		t.Fatalf("HTTP/3 下处理器看到的协议 = %q", body3)
	}
}

func TestHTTP3RequiresTLSConfiguration(t *testing.T) {
	// ACME 与证书文件都未配置时，HTTP/3 会退回临时自签证书（开发模式），
	// 因此这里断言的是「不会因为缺少证书而失败」以及确实生成了证书。
	srv := newTestServer(t, func(o *Options) { o.HTTP3 = HTTP3Config{Enabled: true} })
	if srv.Options().TLS.Enabled {
		t.Fatal("the test case must not enable explicit TLS | 测试用例不应启用显式 TLS")
	}
	if _, err := selfSignedCertForDev(); err != nil {
		t.Fatalf("开发自签证书生成失败: %v", err)
	}
}

func TestGracefulShutdownDrainsInflight(t *testing.T) {
	started := make(chan struct{})
	done := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "done")
		close(done)
	})

	srv := New(Options{Host: "127.0.0.1", Port: 0, Handler: h, ShutdownTimeout: 5 * time.Second})
	if err := srv.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	type result struct {
		body string
		code int
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + srv.Addr() + "/slow")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		resCh <- result{body: string(b), code: resp.StatusCode}
	}()

	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("优雅退出返回错误: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the in-flight request was not completed (not drained) | 进行中的请求未被完成（未排空）")
	}

	res := <-resCh
	if res.err != nil {
		t.Fatalf("进行中的请求失败: %v", res.err)
	}
	if res.code != http.StatusOK || res.body != "done" {
		t.Fatalf("进行中的请求结果 code=%d body=%q", res.code, res.body)
	}
}

func TestStartWithoutHandlerReturnsError(t *testing.T) {
	srv := New(Options{Host: "127.0.0.1", Port: 0})
	if err := srv.Start(); err != ErrNoHandler {
		t.Fatalf("错误 = %v, 期望 ErrNoHandler", err)
	}
}

func TestDoubleStartReturnsError(t *testing.T) {
	srv := newTestServer(t, nil)
	if err := srv.Start(); err != ErrAlreadyStarted {
		t.Fatalf("错误 = %v, 期望 ErrAlreadyStarted", err)
	}
}

func TestOptionsFromConfigOverriddenByEnv(t *testing.T) {
	t.Setenv("YD_SERVER_PORT", "9091")
	t.Setenv("YD_SERVER_H2C", "false")

	mem := afs.NewMem()
	if err := afero.WriteFile(mem, "config.yaml", []byte("server:\n  port: 8080\n  h2c: true\n"), 0o644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	cfg, err := config.Load(config.Options{FS: mem, ConfigDir: ".", ConfigName: "config"})
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	o := OptionsFromConfig(cfg)
	if o.Port != 9091 {
		t.Fatalf("端口 = %d, 期望环境变量覆盖为 9091", o.Port)
	}
	if o.H2C {
		t.Fatal("h2c must be overridden to false by the environment variable | h2c 应被环境变量覆盖为 false")
	}
	if o.ShutdownTimeout <= 0 {
		t.Fatal("the default timeout must come from config.Defaults | 默认超时值必须来自 config.Defaults")
	}
}

func TestACMEHostPolicyRejectsUnknownDomain(t *testing.T) {
	mgr, err := newACMEManager(ACMEConfig{
		Enabled:          true,
		Domains:          []string{"app.example.com"},
		Email:            "ops@example.com",
		CacheDir:         t.TempDir(),
		HostPolicyStrict: true,
	})
	if err != nil {
		t.Fatalf("构造 ACME 管理器失败: %v", err)
	}
	ctx := context.Background()
	if err := mgr.HostPolicy(ctx, "app.example.com"); err != nil {
		t.Fatalf("白名单内域名应通过: %v", err)
	}
	if err := mgr.HostPolicy(ctx, "evil.example.com"); err == nil {
		t.Fatal("a domain outside the whitelist must be rejected in strict mode | 严格模式下白名单外域名必须被拒绝")
	}
	if err := mgr.HostPolicy(ctx, ""); err == nil {
		t.Fatal("an empty host name must be rejected | 空主机名必须被拒绝")
	}
	if mgr.CertCacheDir() == "" {
		t.Fatal("the certificate cache directory must not be empty | 证书缓存目录不应为空")
	}
}

func TestACMERequiresDomains(t *testing.T) {
	if _, err := newACMEManager(ACMEConfig{Enabled: true}); err == nil {
		t.Fatal("a missing domain configuration must return an error | 未配置域名时必须返回错误")
	}
}

// TestDefaultACMECacheDirIsAbsolute proves the default cache does not depend on the process working
// directory, which is System32 under the Windows SCM and "/" under systemd. | TestDefaultACMECacheDirIsAbsolute
// 证明默认缓存不依赖进程工作目录——Windows SCM 下它是 System32，systemd 下它是 "/"。
func TestDefaultACMECacheDirIsAbsolute(t *testing.T) {
	if dir := defaultACMECacheDir(); !filepath.IsAbs(dir) {
		t.Fatalf("default ACME cache dir = %q，期望绝对路径 | want an absolute path", dir)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	srv := New(Options{Host: "127.0.0.1", Port: 0, Handler: protoHandler(), ShutdownTimeout: 3 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	// Wait for the listener to become ready | 等待监听就绪
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Addr() != "" {
			if c, err := net.DialTimeout("tcp", srv.Addr(), 200*time.Millisecond); err == nil {
				_ = c.Close()
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run 返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit when ctx was canceled | Run 未随 ctx 取消而退出")
	}
}

// TestStartRequiresHTTP2ExtendedConnect proves the force switch fails closed. | TestStartRequiresHTTP2ExtendedConnect 证明强制开关失败关闭。
func TestStartRequiresHTTP2ExtendedConnect(t *testing.T) {
	t.Setenv("GODEBUG", "")
	srv := New(Options{
		Host:                      "127.0.0.1",
		Port:                      0,
		Handler:                   protoHandler(),
		H2C:                       true,
		ForceHTTP2ExtendedConnect: true,
	})
	err := srv.Start()
	if !errors.Is(err, ErrHTTP2ExtendedConnectRequired) {
		t.Fatalf("Start = %v, want ErrHTTP2ExtendedConnectRequired | Start = %v，期望 ErrHTTP2ExtendedConnectRequired", err, err)
	}
}

// TestStartAllowsConfiguredHTTP2ExtendedConnect proves an already-set GODEBUG passes. | TestStartAllowsConfiguredHTTP2ExtendedConnect 证明已设置 GODEBUG 时启动成功。
func TestStartAllowsConfiguredHTTP2ExtendedConnect(t *testing.T) {
	t.Setenv("GODEBUG", "http2xconnect=1")
	_ = newTestServer(t, func(o *Options) {
		o.H2C = true
		o.ForceHTTP2ExtendedConnect = true
	})
}

// TestStartDoesNotRequireWhenDisabled proves the default path is unchanged. | TestStartDoesNotRequireWhenDisabled 证明默认路径不变。
func TestStartDoesNotRequireWhenDisabled(t *testing.T) {
	t.Setenv("GODEBUG", "")
	_ = newTestServer(t, func(o *Options) { o.H2C = true })
}

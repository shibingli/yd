package scaffold_test

import (
	"strings"
	"testing"

	"github.com/shibingli/yd/tooling/scaffold"
)

// TestGeneratedRealtimeRegistersTrackers asserts that every streaming endpoint registers its live
// connections with the shared tracker and that the application lifecycle closes them before the
// HTTP drain. Without this wiring an HTTP/1.1 WebSocket waits for the hijack force-close, an
// HTTP/2/3 WebSocket is never forced, and an SSE stream holds the drain to the deadline. |
// TestGeneratedRealtimeRegistersTrackers 断言每个流式端点都把活跃连接登记到共享追踪器，且应用生命周期
// 在 HTTP 排空之前关闭它们。缺少这层接线时，HTTP/1.1 WebSocket 只能等 hijack 强关，HTTP/2/3 WebSocket
// 根本不会被强关，SSE 则会把排空拖到宽限期。
func TestGeneratedRealtimeRegistersTrackers(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("realtime-trackers-app"))

	router := pr.read("internal/router/router.go")
	if !strings.Contains(router, "controller.TrackRealtime(a, broker)") {
		t.Error("router does not wire the realtime trackers before the routes can open a stream | router 未在路由打开数据流之前装配实时追踪器")
	}

	realtime := pr.read("internal/controller/realtime.go")
	for _, want := range []string{
		"websocket.NewConnSet()",
		"sse.NewWriterSet(broker)",
		"a.Lifecycle().Add(\"sse-streams\"",
		"a.Lifecycle().Add(\"websocket-connections\"",
		"websocket.CloseGoingAway",
		"Tracker:   sseTracker",
		"Options:      websocket.Options{Tracker: wsConnSet}",
	} {
		if !strings.Contains(realtime, want) {
			t.Errorf("realtime.go is missing %q | realtime.go 缺少 %q", want, want)
		}
	}

	for _, rel := range []string{
		"internal/controller/message_stream.go",
		"internal/controller/metrics_stream.go",
	} {
		source := pr.read(rel)
		if !strings.Contains(source, "Options:      websocket.Options{Tracker: wsConnSet}") {
			t.Errorf("%s does not register its WebSocket connections with the shared tracker | %s 未把 WebSocket 连接登记到共享追踪器", rel, rel)
		}
	}
}

// TestGeneratedSecurityEnvironmentUsesConfiguration asserts that the master-key gate and the
// session cookie derive production from the loaded configuration, not only from YD_APP_ENV. A
// deployment that writes `app.env: production` in configs/config.yaml must not fall back to the
// public development key or drop the Secure cookie attribute. |
// TestGeneratedSecurityEnvironmentUsesConfiguration 断言主密钥闸门与会话 Cookie 从已加载配置推导生产环境，
// 而不只看 YD_APP_ENV。只在 configs/config.yaml 写 `app.env: production` 的部署不得回退到公开开发密钥，
// 也不得丢掉 Cookie 的 Secure 属性。
func TestGeneratedSecurityEnvironmentUsesConfiguration(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("security-env-app"))

	metaSource := pr.read("internal/meta/meta.go")
	for _, want := range []string{"func RuntimeEnv(cfg *config.Config) string", "func Production(cfg *config.Config) bool", "func Development(cfg *config.Config) bool"} {
		if !strings.Contains(metaSource, want) {
			t.Errorf("meta.go is missing %q | meta.go 缺少 %q", want, want)
		}
	}

	router := pr.read("internal/router/router.go")
	if !strings.Contains(router, "meta.Development(a.Config())") {
		t.Error("master-key gate still reads the environment from YD_APP_ENV only | 主密钥闸门仍然只看 YD_APP_ENV")
	}
	auth := pr.read("internal/controller/auth_controller.go")
	if !strings.Contains(auth, "meta.Production(c.app.Config())") {
		t.Error("session cookie still derives Secure from YD_APP_ENV only | 会话 Cookie 仍然只从 YD_APP_ENV 推导 Secure")
	}
}

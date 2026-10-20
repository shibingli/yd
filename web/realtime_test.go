// realtime_test.go — reconnect, recovery and notice-lifecycle contract of the admin shell.
// realtime_test.go — 管理后台外壳的重连、恢复与提示生命周期契约。
//
// The htmx SSE and WebSocket extensions already retry with exponential backoff. What this file
// pins is the layer above them: one aggregated connection state, a single reconnect banner whose
// text follows the attempt count, a transient recovery notice, a declarative resync hook, and the
// cleanup that keeps a navigation from leaving stale prompts behind.
// htmx 的 SSE 与 WebSocket 扩展已经带指数退避重试；本文件固定的是它们之上的一层：
// 聚合后的单一连接状态、随重试次数更新文案的唯一重连横幅、短暂的恢复提示、声明式重新同步钩子，
// 以及导航时清理旧提示的生命周期。
package web_test

import (
	"os"
	"strings"
	"testing"

	"github.com/shibingli/yd/web"
)

// TestRealtimeReconnectAndRecoveryContract pins the reconnect manager in yd-app.js. | TestRealtimeReconnectAndRecoveryContract 固定 yd-app.js 中的重连管理器。
func TestRealtimeReconnectAndRecoveryContract(t *testing.T) {
	js := string(readEmbedded(t, web.FS(), appJSPath))
	for _, want := range []string{
		"function realtimeElements()",
		"function refreshConnection()",
		"function retryRealtime()",
		"function resyncRealtime(",
		"yd:realtime:recovered",
		"data-yd-resync",
		"data-yd-resync-target",
		"data-yd-connection-attempt",
		"noticeItem",
		"readyState",
		"pagehide",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("yd-app.js is missing %q | yd-app.js 缺少 %q", want, want)
		}
	}

	// A send failure can fire htmx:ws:error while the socket is still open; it must never be
	// treated as a disconnect. | 发送失败可能在套接字仍打开时触发 htmx:ws:error，绝不能当作断开。
	wsError := jsBody(t, js, "function onWebSocketError(event) {")
	if !strings.Contains(wsError, "readyState") {
		t.Error("the ws error handler does not check readyState | ws 错误处理没有检查 readyState")
	}

	// The shell swap replaces the elements that own the streams, so the aggregate state must be
	// recomputed and stale notices dropped. | 外壳交换会替换承载数据流的元素，因此必须重算聚合状态并清理旧提示。
	afterSwap := jsBody(t, js, "function onAfterSwap(event) {")
	if !strings.Contains(afterSwap, "pruneRealtime()") && !strings.Contains(afterSwap, "refreshConnection()") {
		t.Error("onAfterSwap does not refresh the realtime state | onAfterSwap 没有刷新实时状态")
	}
}

// TestRealtimeCatalogHasLifecycleCopy pins the three banner states in both catalogues. | TestRealtimeCatalogHasLifecycleCopy 固定两份目录中的三种横幅文案。
func TestRealtimeCatalogHasLifecycleCopy(t *testing.T) {
	for _, path := range []string{"i18n/zh-CN.yaml", "i18n/en.yaml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, want := range []string{"recovered:", "offline:", "failed:"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("%s is missing realtime.status.%s | %s 缺少 realtime.status.%s", path, strings.TrimSuffix(want, ":"), path, strings.TrimSuffix(want, ":"))
			}
		}
	}
}

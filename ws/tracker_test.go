package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// shutdownReason is the close reason used by the shutdown tests. | shutdownReason 是退出测试使用的关闭原因。
const shutdownReason = "server shutting down | 服务器正在退出"

// dialTracked connects a client to a test server and returns both sides' handles. | dialTracked 连接测试服务器并返回两侧句柄。
func dialTracked(t *testing.T, srv *httptest.Server) *Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), DialOptions{})
	if err != nil {
		t.Fatalf("客户端握手失败: %v", err)
	}
	t.Cleanup(func() { _ = cli.CloseNow() })
	return cli
}

// TestConnSetClosesLiveConnectionWith1001 端到端验证：关闭追踪集合时，活跃连接先收到 1001 关闭帧，
// 服务端处理协程随后退出；整个过程用同步信号断言，不依赖 sleep。
// TestConnSetClosesLiveConnectionWith1001 is an end-to-end check: closing the set delivers a 1001 close
// frame to the live peer and lets the server handler exit, asserted with synchronization signals only.
func TestConnSetClosesLiveConnectionWith1001(t *testing.T) {
	set := NewConnSet()
	opened := make(chan *Conn, 1)
	finished := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, Options{Tracker: set})
		if err != nil {
			return
		}
		// 读循环阻塞在这里，直到关闭帧把它唤醒。| The read loop blocks here until the close frame wakes it up.
		defer func() {
			_ = conn.CloseNow()
			close(finished)
		}()
		opened <- conn
		_, _, _ = conn.ReadMessage()
	}))
	defer srv.Close()

	cli := dialTracked(t, srv)

	select {
	case <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("服务端未在 3 秒内完成升级")
	}
	// `opened <- conn` 发生在 setTracker 之后，收到它即保证连接已登记。| The send happens after setTracker, so receiving it proves registration.
	if got := set.Len(); got != 1 {
		t.Fatalf("追踪连接数 = %d, 期望 1", got)
	}

	start := time.Now()
	set.CloseAll(CloseGoingAway, shutdownReason)

	if code := expectCloseCode(t, cli); code != CloseGoingAway {
		t.Fatalf("关闭码 = %d, 期望 1001（going away）", code)
	}

	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("关闭后服务端处理协程未在 3 秒内退出")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("关闭耗时 %v, 超出预期", elapsed)
	}
	if got := set.Len(); got != 0 {
		t.Fatalf("关闭后追踪连接数 = %d, 期望 0（不得泄漏）", got)
	}
}

// TestConnSetUntracksOnPeerDisconnect 验证对端断开后连接自行取消登记，追踪表不残留条目。
// TestConnSetUntracksOnPeerDisconnect verifies a disconnected peer leaves no entry behind in the tracker.
func TestConnSetUntracksOnPeerDisconnect(t *testing.T) {
	set := NewConnSet()
	finished := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, Options{Tracker: set})
		if err != nil {
			return
		}
		defer func() {
			_ = conn.CloseNow()
			close(finished)
		}()
		_, _, _ = conn.ReadMessage()
	}))
	defer srv.Close()

	cli := dialTracked(t, srv)

	_ = cli.CloseNow() // 客户端断开：服务端读循环收到 ErrUnexpectedEOF | The peer drops: the server read loop sees ErrUnexpectedEOF

	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("客户端断开后服务端未在 3 秒内退出")
	}
	if got := set.Len(); got != 0 {
		t.Fatalf("断开后追踪连接数 = %d, 期望 0", got)
	}
}

// TestConnSetCloseAllOnEmptyAndNilTracker 验证空集合与 nil 追踪器都是安全的无操作。
// TestConnSetCloseAllOnEmptyAndNilTracker verifies an empty set and a nil tracker are safe no-ops.
func TestConnSetCloseAllOnEmptyAndNilTracker(t *testing.T) {
	set := NewConnSet()
	set.CloseAll(CloseGoingAway, shutdownReason) // 空集合：不得 panic | empty set: must not panic
	if got := set.Len(); got != 0 {
		t.Fatalf("Len = %d, 期望 0", got)
	}

	if untrack := set.Track(nil); untrack == nil {
		t.Fatal("Track(nil) 必须返回可调用的取消登记函数")
	} else {
		untrack()
		untrack() // 幂等 | idempotent
	}

	// Tracker 为 nil 时 Upgrade 不得登记，也不得 panic。| With a nil tracker, Upgrade must neither register nor panic.
	var none Tracker
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, Options{Tracker: none})
		if err != nil {
			return
		}
		_ = conn.Close(CloseNormalClosure, "")
	}))
	defer srv.Close()
	_ = dialTracked(t, srv)
}

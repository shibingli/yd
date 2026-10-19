package sse

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// readEventFrame reads from br until the blank line that terminates an SSE frame. | readEventFrame 从 br 读到结束一个 SSE 帧的空行为止。
func readEventFrame(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	var sb strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("读取事件帧失败: %v (已读 %q)", err, sb.String())
		}
		if line == "\n" {
			return sb.String()
		}
		sb.WriteString(line)
	}
}

// newStreamServer starts an SSE endpoint that streams events from a broker until the writer is closed. | newStreamServer 启动一个 SSE 端点：
// 它持续把广播器事件写入事件流，直到写入器被关闭。
func newStreamServer(t *testing.T, set *WriterSet, broker *Broker, handlerReturned chan struct{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wr, err := NewWriter(w, Options{Heartbeat: 5 * time.Millisecond, Tracker: set})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer func() {
			_ = wr.Close()
			close(handlerReturned)
		}()

		if err := wr.WriteData("hello"); err != nil {
			return
		}

		ch, cancel := broker.Subscribe("topic")
		defer cancel()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-wr.Done():
				return
			case e := <-ch:
				if err := wr.WriteEvent(e); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWriterSetShutdownTerminatesStream 端到端验证：关闭写入器集合后事件流被终止，
// 客户端读到最终注释帧后连接结束，且关闭本身不阻塞。
// TestWriterSetShutdownTerminatesStream is an end-to-end check: closing the writer set terminates the
// stream, the client sees the final comment frame and then the end of the response, and the shutdown
// itself does not block.
func TestWriterSetShutdownTerminatesStream(t *testing.T) {
	broker := NewBroker(BrokerOptions{})
	set := NewWriterSet(broker)
	handlerReturned := make(chan struct{})

	srv := newStreamServer(t, set, broker, handlerReturned)

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("建立 SSE 流失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, 期望 text/event-stream", ct)
	}

	br := bufio.NewReader(resp.Body)
	first := readEventFrame(t, br)
	if !strings.Contains(first, "hello") {
		t.Fatalf("首帧 = %q, 期望包含 hello", first)
	}
	// The first frame proves NewWriter ran, hence the writer is tracked. | 首帧到达即证明 NewWriter 已执行，也就是写入器已登记。
	if got := set.Len(); got != 1 {
		t.Fatalf("追踪写入器数 = %d, 期望 1", got)
	}

	start := time.Now()
	set.Shutdown()
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("WriterSet.Shutdown 耗时 %v, 超出宽限期预期", elapsed)
	}

	select {
	case <-handlerReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler did not return within 3 seconds after the event stream closed | 事件流关闭后处理器未在 3 秒内返回")
	}

	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("读取事件流剩余内容失败: %v", err)
	}
	body := string(rest)
	idx := strings.Index(body, shutdownComment)
	if idx < 0 {
		t.Fatalf("事件流必须以最终注释帧 %q 结束，实际尾帧 = %q", shutdownComment, body)
	}
	if tail := body[idx+len(shutdownComment):]; strings.Contains(tail, heartbeatComment) {
		t.Fatalf("最终注释帧之后仍出现心跳帧，说明心跳未被停止: %q", tail)
	}
	if got := set.Len(); got != 0 {
		t.Fatalf("关闭后追踪写入器数 = %d, 期望 0（不得泄漏）", got)
	}
}

// TestWriterSetShutdownReleasesSubscribers 验证关闭会一并关闭广播器，释放订阅通道。
// TestWriterSetShutdownReleasesSubscribers verifies the shutdown also closes the broker and releases subscriber channels.
func TestWriterSetShutdownReleasesSubscribers(t *testing.T) {
	broker := NewBroker(BrokerOptions{})
	set := NewWriterSet(broker)

	ch, cancel := broker.Subscribe("topic")
	defer cancel()

	set.Shutdown()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("the subscription channel must close as soon as the broker closes; no further event may be delivered | 关闭广播器后订阅通道应立即关闭，不得再投递事件")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the subscription channel was not closed after the broker closed (the subscriber goroutine would hang) | 关闭广播器后订阅通道未关闭（订阅协程会悬挂）")
	}
	if got := set.Len(); got != 0 {
		t.Fatalf("Len = %d, 期望 0", got)
	}
}

// TestWriterSetNilBrokerAndUntrackOnClose 验证无广播器以及处理器主动关闭写入器的情况。
// TestWriterSetNilBrokerAndUntrackOnClose verifies the no-broker case and a handler closing its own writer.
func TestWriterSetNilBrokerAndUntrackOnClose(t *testing.T) {
	// A set without a broker must shut down without panicking; it is a separate set because a
	// shutdown closes the registration barrier and would make the tracking below a no-op. | 无广播器的集合
	// 关闭时不得 panic；这里用独立集合，因为关闭会合上登记屏障，让下面的登记变成空操作。
	empty := NewWriterSet(nil)
	empty.Shutdown()
	empty.Shutdown()

	set := NewWriterSet(nil)
	if untrack := set.Track(nil); untrack == nil {
		t.Fatal("Track(nil) must return a callable untrack function | Track(nil) 必须返回可调用的取消登记函数")
	} else {
		untrack()
		untrack() // 幂等 | idempotent
	}

	rec := newFlushRecorder()
	w, err := NewWriter(rec, Options{Tracker: set})
	if err != nil {
		t.Fatalf("NewWriter 失败: %v", err)
	}
	if got := set.Len(); got != 1 {
		t.Fatalf("NewWriter 未登记写入器，Len = %d", got)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close 返回错误: %v", err)
	}
	if got := set.Len(); got != 0 {
		t.Fatalf("Close 后 Len = %d, 期望 0", got)
	}
	select {
	case <-w.Done():
	default:
		t.Fatal("Done must already be closed after Close | Close 后 Done 必须已关闭")
	}
}

// TestWriterSetRejectsWritersAfterShutdown proves the shutdown barrier is closed: a stream that
// registers after Shutdown is terminated immediately instead of waiting for a sweep that will
// never come. | TestWriterSetRejectsWritersAfterShutdown 证明退出屏障是关闭的：Shutdown 之后才登记的
// 事件流会被立即终止，而不是等待一轮永远不会到来的清扫。
func TestWriterSetRejectsWritersAfterShutdown(t *testing.T) {
	set := NewWriterSet(NewBroker(BrokerOptions{}))
	set.Shutdown()

	w, err := NewWriter(httptest.NewRecorder(), Options{Tracker: set})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	select {
	case <-w.Done():
	default:
		t.Fatal("Shutdown 之后登记的写入器必须立即关闭 | a writer tracked after Shutdown must close immediately")
	}
	if got := set.Len(); got != 0 {
		t.Errorf("追踪写入器数 = %d，期望 0 | tracked writers = %d, want 0", got, got)
	}
	// A second shutdown must be a no-op rather than a panic or a second broker close. | 第二次关闭必须是无操作，而不是 panic 或重复关闭广播器。
	set.Shutdown()
}

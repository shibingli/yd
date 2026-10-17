package sse

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibingli/yd/router"
)

// ---------------------------------------------------------------- Test helpers | 测试辅助

// flushRecorder is a fake response writer that records the bytes written, is safe for concurrent access, and implements http.Flusher. | flushRecorder 是记录写入字节、可并发访问且实现 http.Flusher 的响应写入器假实现。
type flushRecorder struct {
	mu      sync.Mutex
	header  http.Header
	buf     bytes.Buffer
	code    int
	flushes int
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{header: make(http.Header)}
}

func (f *flushRecorder) Header() http.Header { return f.header }

func (f *flushRecorder) WriteHeader(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.code == 0 {
		f.code = code
	}
}

func (f *flushRecorder) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.code == 0 {
		f.code = http.StatusOK
	}
	return f.buf.Write(p)
}

func (f *flushRecorder) Flush() {
	f.mu.Lock()
	f.flushes++
	f.mu.Unlock()
}

// String returns a snapshot of the bytes written so far. | String 返回已写出字节的快照。
func (f *flushRecorder) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.String()
}

// Len returns the number of bytes written so far. | Len 返回已写出的字节数。
func (f *flushRecorder) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.Len()
}

// Flushes returns the number of times Flush was called. | Flushes 返回 Flush 被调用的次数。
func (f *flushRecorder) Flushes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flushes
}

// plainWriter implements only http.ResponseWriter and deliberately does not implement http.Flusher. | plainWriter 只实现 http.ResponseWriter，刻意不实现 http.Flusher。
type plainWriter struct {
	header http.Header
	buf    bytes.Buffer
	code   int
}

func (p *plainWriter) Header() http.Header { return p.header }

func (p *plainWriter) Write(b []byte) (int, error) {
	if p.code == 0 {
		p.code = http.StatusOK
	}
	return p.buf.Write(b)
}

func (p *plainWriter) WriteHeader(code int) {
	if p.code == 0 {
		p.code = code
	}
}

// waitFor polls until the condition holds, and fails the test on timeout. | waitFor 轮询等待条件成立，超时即判定失败。
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}

// recvEvent receives one event from ch within the timeout. | recvEvent 在超时内从 ch 收取一个事件。
func recvEvent(t *testing.T, ch <-chan Event, timeout time.Duration) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(timeout):
		t.Fatal("timed out waiting for an event | 等待事件超时")
		return Event{}
	}
}

// readUntil keeps reading from r until want appears or the timeout expires, returning a snapshot of what has been read. | readUntil 从 r 持续读取，直到出现 want 或超时，返回已读到的内容快照。
func readUntil(t *testing.T, r io.Reader, want string, timeout time.Duration) string {
	t.Helper()
	var (
		mu  sync.Mutex
		sb  strings.Builder
		out = make(chan string, 16)
	)
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				mu.Lock()
				sb.Write(buf[:n])
				s := sb.String()
				mu.Unlock()
				select {
				case out <- s:
				default:
				}
			}
			if err != nil {
				close(out)
				return
			}
		}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var last string
	for {
		select {
		case s, ok := <-out:
			if !ok {
				return last
			}
			last = s
			if strings.Contains(s, want) {
				return s
			}
		case <-timer.C:
			return last
		}
	}
}

// ---------------------------------------------------------------- Writer

func TestWriterExactWireFormat(t *testing.T) {
	rec := newFlushRecorder()
	w, err := NewWriter(rec, Options{})
	if err != nil {
		t.Fatalf("NewWriter 返回错误: %v", err)
	}

	if err := w.WriteEvent(Event{
		ID:    "1",
		Name:  "msg",
		Data:  []byte("hello\nworld"),
		Retry: 3 * time.Second,
	}); err != nil {
		t.Fatalf("WriteEvent 返回错误: %v", err)
	}

	const want = "id: 1\nevent: msg\nretry: 3000\ndata: hello\ndata: world\n\n"
	if got := rec.String(); got != want {
		t.Fatalf("线格式不匹配:\n got = %q\nwant = %q", got, want)
	}

	// The response headers must follow the SSE conventions. | 响应头必须符合 SSE 约定。
	wantHeaders := map[string]string{
		"Content-Type":      "text/event-stream; charset=utf-8",
		"Cache-Control":     "no-cache, no-transform",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no",
	}
	for k, v := range wantHeaders {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("响应头 %s = %q，期望 %q", k, got, v)
		}
	}
	if rec.Flushes() == 0 {
		t.Error("Flush must be called after WriteEvent | WriteEvent 之后必须调用 Flush")
	}
}

// TestWriterFieldOrderAndCRNormalization asserts the fixed field order | TestWriterFieldOrderAndCRNormalization 断言固定字段顺序
// (comment → id → event → retry → data lines → blank line) as well as CR/CRLF normalization. | （comment → id → event → retry → data 行 → 空行）以及 CR/CRLF 归一化。
func TestWriterFieldOrderAndCRNormalization(t *testing.T) {
	rec := newFlushRecorder()
	w, err := NewWriter(rec, Options{})
	if err != nil {
		t.Fatalf("NewWriter 返回错误: %v", err)
	}
	if err := w.WriteEvent(Event{
		Comment: "c1\nc2",
		ID:      "42",
		Name:    "upd",
		Retry:   1500 * time.Millisecond,
		Data:    []byte("a\r\nb\rc"),
	}); err != nil {
		t.Fatalf("WriteEvent 返回错误: %v", err)
	}

	const want = ": c1\n: c2\nid: 42\nevent: upd\nretry: 1500\ndata: a\ndata: b\ndata: c\n\n"
	if got := rec.String(); got != want {
		t.Fatalf("线格式不匹配:\n got = %q\nwant = %q", got, want)
	}
}

// TestWriterDataAndCommentOnlyFrames covers the two convenience entry points WriteData / Comment. | TestWriterDataAndCommentOnlyFrames 覆盖 WriteData / Comment 两个便捷入口。
func TestWriterDataAndCommentOnlyFrames(t *testing.T) {
	rec := newFlushRecorder()
	w, err := NewWriter(rec, Options{})
	if err != nil {
		t.Fatalf("NewWriter 返回错误: %v", err)
	}
	if err := w.WriteData("line1\nline2"); err != nil {
		t.Fatalf("WriteData 返回错误: %v", err)
	}
	if err := w.Comment("note"); err != nil {
		t.Fatalf("Comment 返回错误: %v", err)
	}

	const want = "data: line1\ndata: line2\n\n: note\n\n"
	if got := rec.String(); got != want {
		t.Fatalf("线格式不匹配:\n got = %q\nwant = %q", got, want)
	}

	// Newlines inside the id/event fields must be stripped, without breaking the single-line structure. | id/event 字段中的换行必须被清除，不得破坏单行结构。
	rec2 := newFlushRecorder()
	w2, err := NewWriter(rec2, Options{})
	if err != nil {
		t.Fatalf("NewWriter 返回错误: %v", err)
	}
	if err := w2.WriteEvent(Event{ID: "1\n2", Name: "a\rb"}); err != nil {
		t.Fatalf("WriteEvent 返回错误: %v", err)
	}
	if got, want := rec2.String(), "id: 12\nevent: ab\n\n"; got != want {
		t.Fatalf("线格式不匹配:\n got = %q\nwant = %q", got, want)
	}
}

// TestWriterRetryFromOptions asserts that Options.Retry serves as the default reconnect interval | TestWriterRetryFromOptions 断言 Options.Retry 作为默认重连间隔，
// and that Event.Retry takes precedence; no retry field may be emitted when Retry is 0. | 且 Event.Retry 优先；Retry 为 0 时不得输出 retry 字段。
func TestWriterRetryFromOptions(t *testing.T) {
	rec := newFlushRecorder()
	w, err := NewWriter(rec, Options{Retry: 2 * time.Second})
	if err != nil {
		t.Fatalf("NewWriter 返回错误: %v", err)
	}
	if err := w.WriteData("x"); err != nil {
		t.Fatalf("WriteData 返回错误: %v", err)
	}
	if err := w.WriteEvent(Event{Data: []byte("y"), Retry: 500 * time.Millisecond}); err != nil {
		t.Fatalf("WriteEvent 返回错误: %v", err)
	}
	const want = "retry: 2000\ndata: x\n\nretry: 500\ndata: y\n\n"
	if got := rec.String(); got != want {
		t.Fatalf("线格式不匹配:\n got = %q\nwant = %q", got, want)
	}
}

func TestWriterRequiresFlusher(t *testing.T) {
	w, err := NewWriter(&plainWriter{header: make(http.Header)}, Options{})
	if !errors.Is(err, ErrFlusherUnsupported) {
		t.Fatalf("err = %v，期望 ErrFlusherUnsupported", err)
	}
	if w != nil {
		t.Fatalf("不支持 Flush 时不得返回 Writer，得到 %v", w)
	}
}

func TestHeartbeatEmitsCommentAndStopsOnClose(t *testing.T) {
	rec := newFlushRecorder()
	w, err := NewWriter(rec, Options{})
	if err != nil {
		t.Fatalf("NewWriter 返回错误: %v", err)
	}
	w.Heartbeat(5 * time.Millisecond)

	waitFor(t, 2*time.Second, "心跳注释", func() bool {
		return strings.Contains(rec.String(), ": ping\n")
	})

	if err := w.Close(); err != nil {
		t.Fatalf("Close 返回错误: %v", err)
	}
	// Idempotence: a repeated Close must not panic. | 幂等：重复 Close 不得 panic。
	if err := w.Close(); err != nil {
		t.Fatalf("重复 Close 返回错误: %v", err)
	}

	after := rec.Len()
	time.Sleep(40 * time.Millisecond)
	if got := rec.Len(); got != after {
		t.Fatalf("Close 之后心跳仍在输出：字节数 %d → %d", after, got)
	}

	// Writes after closing are no-ops and must not panic. | 关闭后的写入为无操作，且不得 panic。
	if err := w.WriteEvent(Event{ID: "x"}); err != nil {
		t.Fatalf("关闭后写入返回错误: %v", err)
	}
}

// ---------------------------------------------------------------- Broker

func TestBrokerFanoutToMultipleSubscribers(t *testing.T) {
	b := NewBroker(BrokerOptions{QueueSize: 8})
	defer b.Close()

	ch1, cancel1 := b.Subscribe("room")
	defer cancel1()
	ch2, cancel2 := b.Subscribe("room")
	defer cancel2()

	if got := b.Clients("room"); got != 2 {
		t.Fatalf("Clients(room) = %d，期望 2", got)
	}

	b.Publish("room", Event{ID: "1", Data: []byte("a")})
	b.Publish("room", Event{ID: "2", Data: []byte("b")})
	b.Publish("other", Event{ID: "9"})

	for i, ch := range []<-chan Event{ch1, ch2} {
		e1 := recvEvent(t, ch, time.Second)
		e2 := recvEvent(t, ch, time.Second)
		if e1.ID != "1" || e2.ID != "2" {
			t.Errorf("订阅者 %d 收到 %q/%q，期望 1/2", i, e1.ID, e2.ID)
		}
	}
	// Other topics must not leak into this stream. | 其他 topic 不得串流。
	select {
	case e := <-ch1:
		t.Fatalf("收到非本 topic 的事件: %+v", e)
	case <-time.After(20 * time.Millisecond):
	}
	if got := b.Clients("other"); got != 0 {
		t.Errorf("Clients(other) = %d，期望 0", got)
	}
}

// TestBrokerSlowConsumerDoesNotBlock asserts that a subscriber which never reads cannot block the publisher, | TestBrokerSlowConsumerDoesNotBlock 断言从不读取的订阅者不会阻塞发布方，
// and that the healthy subscriber still receives frames, with the first one being the first published. | 且健康订阅者仍能收到事件，且首个事件就是最早发布的那一条。
//
// The test advances on channel signals only: it waits for the fast consumer's own signal before asserting, | 本用例仅以 channel 信号推进时序：断言之前先等待快消费者自己发出的信号，
// so the verdict does not depend on when the runtime happens to schedule that goroutine (polling a wall clock made it flaky). | 因此结论不依赖运行时何时调度该 goroutine（此前的墙钟轮询正是偶发失败的来源）。
func TestBrokerSlowConsumerDoesNotBlock(t *testing.T) {
	b := NewBroker(BrokerOptions{QueueSize: 1})
	defer b.Close()

	slow, slowCancel := b.Subscribe("t")
	defer slowCancel()
	fast, fastCancel := b.Subscribe("t")
	defer fastCancel()

	// firstRec carries the first frame the fast consumer takes off its channel; fastDone reports that the consumer loop has ended. | firstRec 承载快消费者从通道取走的第一帧；fastDone 表示消费循环已结束。
	firstRec := make(chan string, 1)
	fastDone := make(chan struct{})
	go func() {
		defer close(fastDone)
		for e := range fast {
			select {
			case firstRec <- e.ID:
			default: // only the first frame is signalled | 仅对首帧发信号
			}
		}
	}()

	const n = 200
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			b.Publish("t", Event{ID: strconv.Itoa(i)})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a slow consumer blocked Publish | 慢消费者阻塞了 Publish")
	}

	// The slow consumer reads not a single byte, so every frame after the first one is dropped: once done is closed | 慢消费者一个字节都不读，因此第一帧之后的每一帧都被丢弃：done 关闭后
	// (which happens-before the checks below) the drop counter cannot still be zero. | （对下述检查建立 happens-before）丢弃计数不可能仍为 0。
	if dropped := b.Dropped("t"); dropped == 0 {
		t.Fatalf("Dropped(t) = 0，期望 > 0")
	}
	// What the slow consumer has not read is never delivered: its single buffered frame stays queued, and later frames | 慢消费者未读取的事件不会被投递出来：它那唯一一帧缓冲始终留在通道里，
	// are neither queued in its place nor handed over. | 后续事件既不顶替它也不被交付。
	if queued := len(slow); queued != 1 {
		t.Fatalf("慢消费者待读事件数 = %d，期望 1", queued)
	}

	// Block on the fast consumer's own signal instead of polling the wall clock. | 阻塞等待快消费者自身的信号，而不是轮询墙钟。
	select {
	case first := <-firstRec:
		if first != "0" {
			t.Errorf("快消费者首个事件 = %q，期望 0", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the fast consumer received no event in time | 快消费者没有及时收到任何事件")
	}

	// Close must end the consumer loop. | Close 必须结束消费循环。
	b.Close()
	select {
	case <-fastDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the consume loop did not exit after Broker.Close | Broker.Close 之后消费循环未退出")
	}
}

func TestBrokerUnsubscribeIdempotentAndStopsDelivery(t *testing.T) {
	b := NewBroker(BrokerOptions{QueueSize: 4})
	defer b.Close()

	ch, cancel := b.Subscribe("t")
	b.Publish("t", Event{ID: "1"})
	if e := recvEvent(t, ch, time.Second); e.ID != "1" {
		t.Fatalf("收到 %q，期望 1", e.ID)
	}

	cancel()
	cancel() // idempotent: must not panic | 幂等：不得 panic

	if got := b.Clients("t"); got != 0 {
		t.Fatalf("取消后 Clients(t) = %d，期望 0", got)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("no event may arrive after cancel | 取消后不应再收到事件")
		}
	case <-time.After(time.Second):
		t.Fatal("the subscription channel must be closed after cancel | 取消后订阅通道应被关闭")
	}
	b.Publish("t", Event{ID: "2"}) // must not panic | 不得 panic
}

func TestBrokerCloseClosesAllSubscriptions(t *testing.T) {
	b := NewBroker(BrokerOptions{})
	ch, cancel := b.Subscribe("t")
	b.Close()
	b.Close() // idempotent | 幂等

	if got := b.Clients("t"); got != 0 {
		t.Fatalf("Close 后 Clients(t) = %d，期望 0", got)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("no event may arrive after Close | Close 后不应再收到事件")
		}
	default:
		t.Fatal("Close must close the subscription channel at once | Close 应立即关闭订阅通道")
	}
	cancel() // must not panic | 不得 panic

	// Publishing after closing is a no-op. | 关闭后的发布为无操作。
	b.Publish("t", Event{ID: "1"})
	if _, cancel2 := b.Subscribe("t"); cancel2 == nil {
		t.Fatal("a closed Broker must still hand out a callable cancel function | 已关闭的 Broker 仍需返回可调用的取消函数")
	}
}

func TestBrokerPublishHTMLLineFormat(t *testing.T) {
	b := NewBroker(BrokerOptions{QueueSize: 4})
	defer b.Close()

	ch, cancel := b.Subscribe("msgs")
	defer cancel()
	b.PublishHTML("msgs", "#list", "innerHTML", `<li class="x">hi</li>`)

	e := recvEvent(t, ch, time.Second)
	if e.Name != "msgs" {
		t.Errorf("Event.Name = %q，期望 msgs", e.Name)
	}
	want := `<hx-partial hx-target="#list" hx-swap="innerHTML"><li class="x">hi</li></hx-partial>`
	if string(e.Data) != want {
		t.Fatalf("Event.Data = %q，期望 %q", e.Data, want)
	}

	// The complete byte stream as sent over the wire. | 送上线后的完整字节流。
	rec := newFlushRecorder()
	w, err := NewWriter(rec, Options{})
	if err != nil {
		t.Fatalf("NewWriter 返回错误: %v", err)
	}
	if err := w.WriteEvent(e); err != nil {
		t.Fatalf("WriteEvent 返回错误: %v", err)
	}
	wantWire := "event: msgs\ndata: " + want + "\n\n"
	if got := rec.String(); got != wantWire {
		t.Fatalf("线格式不匹配:\n got = %q\nwant = %q", got, wantWire)
	}

	// Attributes must be escaped to prevent injection. | 属性必须转义，防止注入。
	b.PublishHTML("msgs", `" onload="x`, "outerHTML", "v")
	esc := recvEvent(t, ch, time.Second)
	if strings.Contains(string(esc.Data), `hx-target="" onload=`) {
		t.Fatalf("hx-target 属性未转义: %q", esc.Data)
	}
}

// ---------------------------------------------------------------- Handler

// sseServer starts an SSE endpoint mounted at /events. | sseServer 启动一个挂在 /events 上的 SSE 端点。
func sseServer(t *testing.T, b *Broker, topic string, o Options) (*httptest.Server, *router.Router) {
	t.Helper()
	r := router.New()
	r.GET("/events", Handler(b, topic, o))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, r
}

// getAsync issues a GET asynchronously and returns a response channel (an SSE long-lived connection does not deliver response headers before the first event). | getAsync 异步发起 GET，返回响应通道（SSE 长连接在首个事件前不会返回响应头）。
func getAsync(t *testing.T, url string, hdr map[string]string) <-chan *http.Response {
	t.Helper()
	out := make(chan *http.Response, 1)
	go func() {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			out <- nil
			return
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			out <- nil
			return
		}
		out <- resp
	}()
	return out
}

func TestHandlerSendsHtmxPartialEvent(t *testing.T) {
	b := NewBroker(BrokerOptions{QueueSize: 8})
	defer b.Close()
	srv, _ := sseServer(t, b, "msgs", Options{})

	respCh := getAsync(t, srv.URL+"/events", nil)
	waitFor(t, 2*time.Second, "订阅建立", func() bool { return b.Clients("msgs") == 1 })

	b.PublishHTML("msgs", "#list", "innerHTML", "<li>hi</li>")

	var resp *http.Response
	select {
	case resp = <-respCh:
	case <-time.After(2 * time.Second):
		t.Fatal("the response headers never arrived | 响应头未送达")
	}
	if resp == nil {
		t.Fatal("the request failed | 请求失败")
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q，期望 text/event-stream; charset=utf-8", ct)
	}
	for _, h := range [][2]string{
		{"Cache-Control", "no-cache, no-transform"},
		{"Connection", "keep-alive"},
		{"X-Accel-Buffering", "no"},
	} {
		if got := resp.Header.Get(h[0]); got != h[1] {
			t.Errorf("响应头 %s = %q，期望 %q", h[0], got, h[1])
		}
	}
	body := readUntil(t, resp.Body, "<hx-partial", 2*time.Second)
	if !strings.Contains(body, "event: msgs\n") {
		t.Errorf("未收到 event: msgs，实际 %q", body)
	}
	if !strings.Contains(body, "<hx-partial") {
		t.Errorf("未收到 hx-partial 片段，实际 %q", body)
	}
	wantData := `data: <hx-partial hx-target="#list" hx-swap="innerHTML"><li>hi</li></hx-partial>`
	if !strings.Contains(body, wantData) {
		t.Errorf("data 行不匹配:\n got = %q\nwant 含 %q", body, wantData)
	}
}

func TestHandlerCleansUpOnClientDisconnect(t *testing.T) {
	b := NewBroker(BrokerOptions{QueueSize: 8})
	defer b.Close()
	srv, _ := sseServer(t, b, "live", Options{})

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	respCh := make(chan *http.Response, 1)
	go func() {
		resp, err := client.Get(srv.URL + "/events")
		if err != nil {
			respCh <- nil
			return
		}
		respCh <- resp
	}()

	waitFor(t, 2*time.Second, "订阅建立", func() bool { return b.Clients("live") == 1 })

	// Trigger one write so that the client actually receives the response headers. | 触发一次写出，客户端才会拿到响应头。
	b.Publish("live", Event{ID: "1", Data: []byte("first")})
	var resp *http.Response
	select {
	case resp = <-respCh:
	case <-time.After(2 * time.Second):
		t.Fatal("the response headers never arrived | 响应头未送达")
	}
	if resp == nil {
		t.Fatal("the request failed | 请求失败")
	}
	if body := readUntil(t, resp.Body, "data: first", 2*time.Second); !strings.Contains(body, "data: first") {
		t.Fatalf("未收到首个事件，实际 %q", body)
	}

	// Client disconnect → the server must clean up the subscription. | 客户端断开 → 服务端必须清理订阅。
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("关闭响应体失败: %v", err)
	}
	waitFor(t, 3*time.Second, "客户端断开后订阅被清理", func() bool {
		return b.Clients("live") == 0
	})
}

func TestHandlerParsesLastEventID(t *testing.T) {
	b := NewBroker(BrokerOptions{QueueSize: 8})
	defer b.Close()

	seen := make(chan string, 1)
	r := router.New()
	r.Use(func(c *router.Ctx) {
		c.Next()
		seen <- LastEventID(c)
	})
	r.GET("/events", Handler(b, "live", Options{}))
	srv := httptest.NewServer(r)
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	respCh := make(chan *http.Response, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events", nil)
		req.Header.Set("Last-Event-ID", "42")
		resp, err := client.Do(req)
		if err != nil {
			respCh <- nil
			return
		}
		respCh <- resp
	}()

	waitFor(t, 2*time.Second, "订阅建立", func() bool { return b.Clients("live") == 1 })
	b.Publish("live", Event{ID: "43", Data: []byte("next")})
	select {
	case resp := <-respCh:
		if resp != nil {
			defer resp.Body.Close()
			readUntil(t, resp.Body, ": 43", time.Second)
			_ = resp.Body.Close()
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the response headers never arrived | 响应头未送达")
	}

	select {
	case got := <-seen:
		if got != "42" {
			t.Fatalf("LastEventID = %q，期望 42", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the middleware did not see Last-Event-ID after the request had finished | 中间件未在请求结束后读到 Last-Event-ID")
	}
}

func TestHandlerRejectsNonFlusher(t *testing.T) {
	b := NewBroker(BrokerOptions{})
	defer b.Close()

	pw := &plainWriter{header: make(http.Header)}
	c := &router.Ctx{
		Writer:  pw,
		Request: httptest.NewRequest(http.MethodGet, "/events", nil),
	}
	Handler(b, "t", Options{})(c)

	if pw.code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500", pw.code)
	}
	if got := b.Clients("t"); got != 0 {
		t.Fatalf("建立失败时不应留下订阅，Clients(t) = %d", got)
	}
}

// ---------------------------------------------------------------- Concurrency and leaks | 并发与泄漏

// TestWriterConcurrentWritesAreSerialized uses concurrent writes to verify that frames do not interleave (run with -race). | TestWriterConcurrentWritesAreSerialized 用并发写入验证帧不会交错（配合 -race）。
func TestWriterConcurrentWritesAreSerialized(t *testing.T) {
	rec := newFlushRecorder()
	w, err := NewWriter(rec, Options{})
	if err != nil {
		t.Fatalf("NewWriter 返回错误: %v", err)
	}

	const (
		writers = 8
		perLoop = 50
	)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perLoop; j++ {
				if err := w.WriteEvent(Event{ID: strconv.Itoa(id), Data: []byte("x")}); err != nil {
					t.Errorf("WriteEvent 返回错误: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	out := rec.String()
	const frame = "id: 1\ndata: x\n\n"
	if n := strings.Count(out, "\n\n"); n != writers*perLoop {
		t.Fatalf("帧数 = %d，期望 %d（出现交错写出）", n, writers*perLoop)
	}
	if n := strings.Count(out, "data: x\n"); n != writers*perLoop {
		t.Fatalf("data 行数 = %d，期望 %d", n, writers*perLoop)
	}
	if !strings.Contains(out, frame) {
		t.Fatalf("输出中缺少完整帧 %q", frame)
	}
}

// TestHeartbeatRestartDoesNotLeakGoroutines asserts that repeatedly starting and stopping the heartbeat leaves no goroutines behind. | TestHeartbeatRestartDoesNotLeakGoroutines 断言重复启停心跳不会残留 goroutine。
func TestHeartbeatRestartDoesNotLeakGoroutines(t *testing.T) {
	rec := newFlushRecorder()
	w, err := NewWriter(rec, Options{})
	if err != nil {
		t.Fatalf("NewWriter 返回错误: %v", err)
	}

	base := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		w.Heartbeat(time.Hour) // very long interval: no heartbeat is actually written in this round | 间隔很长：本轮不会真的写出心跳
		w.Heartbeat(0)         // stop immediately and wait for the goroutine to exit | 立即停止并等待 goroutine 退出
	}
	waitFor(t, time.Second, "goroutine 回落到基线", func() bool {
		return runtime.NumGoroutine() <= base
	})

	if err := w.Close(); err != nil {
		t.Fatalf("Close 返回错误: %v", err)
	}
	// No bytes may be written after Close. | Close 之后不得再写出任何字节。
	before := rec.Len()
	w.Heartbeat(time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if got := rec.Len(); got != before {
		t.Fatalf("Close 之后心跳仍在输出：字节数 %d → %d", before, got)
	}
}

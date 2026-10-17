package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/http/router"
)

// gateHandler blocks every call until open is called, so tests can pin down queue occupancy.
// | gateHandler 会阻塞每次调用直到 open 被调用，使测试能够精确控制队列占用。
type gateHandler struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	records []Record
}

// newGateHandler returns a handler with an open gate. | newGateHandler 返回一个闸门关闭的处理器。
func newGateHandler() *gateHandler {
	return &gateHandler{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

// handle records rec after the gate opens. | handle 在闸门打开后记录 rec。
func (h *gateHandler) handle(rec Record) {
	select {
	case h.started <- struct{}{}:
	default:
	}
	<-h.release
	h.mu.Lock()
	h.records = append(h.records, rec)
	h.mu.Unlock()
}

// open releases every current and future call. | open 释放当前与后续的所有调用。
func (h *gateHandler) open() { h.once.Do(func() { close(h.release) }) }

// snapshot returns a copy of the handled records. | snapshot 返回已处理记录的副本。
func (h *gateHandler) snapshot() []Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Record(nil), h.records...)
}

// ids returns the ID of every handled record in order. | ids 按顺序返回已处理记录的 ID。
func (h *gateHandler) ids() []string {
	recs := h.snapshot()
	out := make([]string, len(recs))
	for i, rec := range recs {
		out[i] = rec.ID
	}
	return out
}

// TestAsyncAfterCapturesRecordFields drives one request through AccessLogAsync and asserts every
// Record field matches the request. | TestAsyncAfterCapturesRecordFields 让一个请求经过
// AccessLogAsync，并断言 Record 的每个字段都与请求一致。
func TestAsyncAfterCapturesRecordFields(t *testing.T) {
	records := make(chan Record, 1)
	a := NewAsyncAfter(AsyncOptions{
		Workers:   2,
		QueueSize: 8,
		Handler:   func(rec Record) { records <- rec },
	})
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	r := router.New()
	r.Use(RequestID(""), AccessLogAsync(nil, a))
	r.GET("/users/42", func(c *router.Ctx) {
		_ = c.String(http.StatusCreated, "hello world")
	})

	res := doReqFrom(r, http.MethodGet, "/users/42?x=1", "203.0.113.7:54321",
		map[string]string{"User-Agent": "yd-test/1.0"})
	if res.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201", res.Code)
	}

	var got Record
	select {
	case got = <-records:
	case <-time.After(2 * time.Second):
		t.Fatal("no Record was delivered within 2s")
	}

	if want := res.Header().Get("X-Request-Id"); want == "" || got.ID != want {
		t.Errorf("ID = %q, want %q", got.ID, want)
	}
	if got.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", got.Method)
	}
	if got.Path != "/users/42" {
		t.Errorf("Path = %q, want /users/42", got.Path)
	}
	if got.RemoteIP != "203.0.113.7" {
		t.Errorf("RemoteIP = %q, want 203.0.113.7", got.RemoteIP)
	}
	if got.UA != "yd-test/1.0" {
		t.Errorf("UA = %q, want yd-test/1.0", got.UA)
	}
	if got.Status != http.StatusCreated {
		t.Errorf("Status = %d, want 201", got.Status)
	}
	if want := int64(len("hello world")); got.Bytes != want {
		t.Errorf("Bytes = %d, want %d", got.Bytes, want)
	}
	if got.Duration < 0 {
		t.Errorf("Duration = %v, want >= 0", got.Duration)
	}
}

// TestAsyncAfterHighConcurrency runs 1000 concurrent requests through a multi-worker pipeline and
// asserts every record is handled exactly once. Run it with -race. | TestAsyncAfterHighConcurrency
// 让 1000 个并发请求经过多 worker 管线，断言每条记录都恰好被处理一次；需在 -race 下运行。
func TestAsyncAfterHighConcurrency(t *testing.T) {
	const (
		total   = 1000
		workers = 8
	)
	var handled atomic.Int64
	a := NewAsyncAfter(AsyncOptions{
		Workers:   workers,
		QueueSize: 256,
		Overflow:  OverflowBlock,
		Handler:   func(Record) { handled.Add(1) },
	})
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	r := router.New()
	r.Use(AccessLogAsync(nil, a))
	r.GET("/concurrent", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res := doReq(r, http.MethodGet, "/concurrent", nil, ""); res.Code != http.StatusOK {
				t.Errorf("code = %d, want 200", res.Code)
			}
		}()
	}
	wg.Wait()

	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := handled.Load(); got != total {
		t.Fatalf("handled = %d, want %d", got, total)
	}
	if got := a.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
}

// TestAsyncAfterCloseDrainsQueuedRecords submits N records and closes immediately: Close must run
// Handler exactly N times. The checksum catches duplicate or lost records. | TestAsyncAfterCloseDrainsQueuedRecords
// 投递 N 条记录后立刻 Close：Handler 必须恰好执行 N 次；校验和可发现重复或丢失。
func TestAsyncAfterCloseDrainsQueuedRecords(t *testing.T) {
	const total = 500
	var (
		handled atomic.Int64
		sum     atomic.Int64
	)
	a := NewAsyncAfter(AsyncOptions{
		Workers:   1,
		QueueSize: total,
		Overflow:  OverflowBlock,
		Handler: func(rec Record) {
			handled.Add(1)
			sum.Add(rec.Bytes)
		},
	})

	for i := 1; i <= total; i++ {
		a.Submit(Record{ID: "queued", Bytes: int64(i)})
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := handled.Load(); got != total {
		t.Fatalf("handled = %d, want %d", got, total)
	}
	if want := int64(total * (total + 1) / 2); sum.Load() != want {
		t.Fatalf("sum = %d, want %d", sum.Load(), want)
	}
	if got := a.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
}

// TestAsyncAfterOverflowBlockDoesNotDrop fills the queue with one worker blocked, proves the next
// Submit blocks instead of dropping, then releases the gate and checks all records arrive.
// | TestAsyncAfterOverflowBlockDoesNotDrop 在单个 worker 阻塞时填满队列，证明下一次 Submit 会阻塞
// 而不是丢弃，随后打开闸门并检查全部记录都到达。
func TestAsyncAfterOverflowBlockDoesNotDrop(t *testing.T) {
	h := newGateHandler()
	a := NewAsyncAfter(AsyncOptions{
		Workers:   1,
		QueueSize: 1,
		Overflow:  OverflowBlock,
		Handler:   h.handle,
		OnDrop:    func(Record) { t.Error("OverflowBlock must never call OnDrop") },
	})
	t.Cleanup(func() {
		h.open()
		_ = a.Close(context.Background())
	})

	a.Submit(Record{ID: "a"})
	<-h.started
	a.Submit(Record{ID: "b"}) // occupies the only queue slot | 占满唯一的队列槽位

	done := make(chan struct{})
	go func() {
		a.Submit(Record{ID: "c"})
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Submit returned although the queue was full")
	case <-time.After(100 * time.Millisecond):
	}
	if got := a.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d while blocked, want 0", got)
	}

	h.open()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked Submit did not return after the gate opened")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := h.ids(); len(got) != 3 {
		t.Fatalf("handled = %v, want 3 records", got)
	}
}

// TestAsyncAfterOverflowDropNew pins the exact DropNew behavior: with the worker blocked and the
// queue full, the newest record is dropped, counted and reported. | TestAsyncAfterOverflowDropNew
// 精确验证 DropNew：worker 阻塞且队列已满时，新记录被丢弃、计数并上报。
func TestAsyncAfterOverflowDropNew(t *testing.T) {
	h := newGateHandler()
	dropped := make(chan Record, 4)
	a := NewAsyncAfter(AsyncOptions{
		Workers:   1,
		QueueSize: 1,
		Overflow:  OverflowDropNew,
		Handler:   h.handle,
		OnDrop:    func(rec Record) { dropped <- rec },
	})
	t.Cleanup(func() {
		h.open()
		_ = a.Close(context.Background())
	})

	a.Submit(Record{ID: "a"})
	<-h.started
	a.Submit(Record{ID: "b"})
	a.Submit(Record{ID: "c"})

	if got := a.Dropped(); got != 1 {
		t.Fatalf("Dropped = %d, want 1", got)
	}
	select {
	case rec := <-dropped:
		if rec.ID != "c" {
			t.Fatalf("OnDrop ID = %q, want c", rec.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("OnDrop was not called")
	}

	h.open()
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := h.ids(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("handled = %v, want [a b]", got)
	}
}

// TestAsyncAfterOverflowDropOldest pins the exact DropOldest behavior: the oldest queued record is
// evicted, reported to OnDrop, and the newest record takes its place. | TestAsyncAfterOverflowDropOldest
// 精确验证 DropOldest：最旧的排队记录被挤掉并上报 OnDrop，新记录取而代之。
func TestAsyncAfterOverflowDropOldest(t *testing.T) {
	h := newGateHandler()
	dropped := make(chan Record, 4)
	a := NewAsyncAfter(AsyncOptions{
		Workers:   1,
		QueueSize: 1,
		Overflow:  OverflowDropOldest,
		Handler:   h.handle,
		OnDrop:    func(rec Record) { dropped <- rec },
	})
	t.Cleanup(func() {
		h.open()
		_ = a.Close(context.Background())
	})

	a.Submit(Record{ID: "a"})
	<-h.started
	a.Submit(Record{ID: "b"})
	a.Submit(Record{ID: "c"})

	if got := a.Dropped(); got != 1 {
		t.Fatalf("Dropped = %d, want 1", got)
	}
	select {
	case rec := <-dropped:
		if rec.ID != "b" {
			t.Fatalf("OnDrop ID = %q, want b", rec.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("OnDrop was not called")
	}

	h.open()
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := h.ids(); len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("handled = %v, want [a c]", got)
	}
}

// TestAsyncAfterHandlerPanicKeepsWorkerAlive makes Handler panic on the first task and asserts the
// worker survives, the next task runs, and the panic is logged through logx. | TestAsyncAfterHandlerPanicKeepsWorkerAlive
// 让 Handler 在首个任务上 panic，断言 worker 存活、后续任务照常执行，且 panic 通过 logx 记录。
func TestAsyncAfterHandlerPanicKeepsWorkerAlive(t *testing.T) {
	old := logx.Default()
	testLog, buf := newTestLogger()
	logx.SetDefault(testLog)
	t.Cleanup(func() { logx.SetDefault(old) })

	var handled atomic.Int64
	a := NewAsyncAfter(AsyncOptions{
		Workers:   1,
		QueueSize: 8,
		Handler: func(rec Record) {
			handled.Add(1)
			if rec.ID == "boom" {
				panic("intentional handler panic")
			}
		},
	})
	a.Submit(Record{ID: "boom"})
	a.Submit(Record{ID: "after"})
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := handled.Load(); got != 2 {
		t.Fatalf("handled = %d, want 2", got)
	}

	found := false
	for _, entry := range decodeLogRecords(t, buf) {
		if entry["msg"] == "async-after handler panic | 响应后异步处理器 panic" {
			found = true
		}
	}
	if !found {
		t.Fatalf("panic log record missing, got %q", buf.String())
	}
}

// TestAsyncAfterSubmitAfterCloseRunsSynchronously documents and pins the post-Close contract:
// Submit executes Handler on the caller goroutine before returning and is not counted as a drop.
// | TestAsyncAfterSubmitAfterCloseRunsSynchronously 记录并验证关闭后的契约：Submit 在返回前于
// 调用方协程同步执行 Handler，且不计入丢弃。
func TestAsyncAfterSubmitAfterCloseRunsSynchronously(t *testing.T) {
	var handled atomic.Int64
	a := NewAsyncAfter(AsyncOptions{
		Workers: 2,
		Handler: func(Record) { handled.Add(1) },
	})
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	a.Submit(Record{ID: "late"})
	if got := handled.Load(); got != 1 {
		t.Fatalf("handled = %d immediately after Submit, want 1 (synchronous run)", got)
	}
	if got := a.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
}

// TestAsyncAfterCloseHonorsContextDeadlineWithoutLeaking blocks the worker past the Close deadline,
// asserts ErrCloseTimeout, and then proves the already-closed queue drains and the worker exits.
// | TestAsyncAfterCloseHonorsContextDeadlineWithoutLeaking 让 worker 阻塞超过 Close 期限，断言返回
// ErrCloseTimeout，随后证明已关闭的队列仍会排空且 worker 退出。
func TestAsyncAfterCloseHonorsContextDeadlineWithoutLeaking(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)

	a := NewAsyncAfter(AsyncOptions{
		Workers:   1,
		QueueSize: 2,
		Overflow:  OverflowBlock,
		Handler: func(Record) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		},
	})
	a.Submit(Record{ID: "a"})
	<-started
	a.Submit(Record{ID: "b"})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := a.Close(ctx)
	if !errors.Is(err, ErrCloseTimeout) {
		t.Fatalf("Close = %v, want ErrCloseTimeout", err)
	}

	open()
	select {
	case <-a.done:
	case <-time.After(5 * time.Second):
		t.Fatal("workers did not exit after the queue was drained")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestAsyncAfterConcurrentCloseUnderLoad crosses a request storm with a concurrent Close. With
// OverflowBlock the pipeline accepts every record, so the only correct outcomes are exactly one
// Handler call per request and zero drops: requests that lose the race run synchronously after
// Close, and records already queued are drained before the workers exit. | TestAsyncAfterConcurrentCloseUnderLoad
// 让请求风暴与并发的 Close 交叉。使用 OverflowBlock 时管线接收全部记录，因此唯一正确的结果是
// 每个请求恰好一次 Handler 调用且零丢弃：输掉竞态的请求在 Close 后同步执行，已入队的记录则在
// worker 退出前被排空。
func TestAsyncAfterConcurrentCloseUnderLoad(t *testing.T) {
	const (
		requests = 2000
		workers  = 8
	)
	var handled atomic.Int64
	a := NewAsyncAfter(AsyncOptions{
		Workers:   workers,
		QueueSize: 64,
		Overflow:  OverflowBlock,
		Handler:   func(Record) { handled.Add(1) },
	})
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	entered := make(chan struct{}, requests)
	release := make(chan struct{})
	r := router.New()
	r.Use(AccessLogAsync(nil, a))
	r.GET("/storm", func(c *router.Ctx) {
		entered <- struct{}{}
		<-release
		_ = c.String(http.StatusOK, "ok")
	})

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/storm", nil))
		}()
	}
	close(start)
	// Park every request inside the handler, so the Close below races with the actual Submits
	// rather than with request startup. | 让每个请求都停在处理器内，使下面的 Close 与真正的
	// Submit 竞态，而不是与请求启动竞态。
	for i := 0; i < requests; i++ {
		<-entered
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- a.Close(context.Background()) }()
	close(release)

	wg.Wait()
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := handled.Load(); got != requests {
		t.Fatalf("handled = %d, want %d", got, requests)
	}
	if got := a.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
}

// TestAsyncAfterCloseLeavesNoGoroutines asserts the worker and waiter goroutines are gone after a
// normal Close. | TestAsyncAfterCloseLeavesNoGoroutines 断言正常 Close 后 worker 与等待协程都已消失。
func TestAsyncAfterCloseLeavesNoGoroutines(t *testing.T) {
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	a := NewAsyncAfter(AsyncOptions{
		Workers:   4,
		QueueSize: 32,
		Handler:   func(Record) {},
	})
	for i := 0; i < 64; i++ {
		a.Submit(Record{ID: "x"})
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.GC()
		if got := runtime.NumGoroutine(); got <= baseline {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, baseline = %d after Close", runtime.NumGoroutine(), baseline)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAsyncAfterRecordSnapshotSurvivesRequestCtxReuse is the direct proof that cloning is
// necessary. The request's method, path, User-Agent and RemoteAddr all point into byte slices that
// this test overwrites after ServeHTTP returns (the *router.Ctx has already been returned to its
// pool). Without strings.Clone in Submit, the delivered Record would observe the overwritten bytes.
// | TestAsyncAfterRecordSnapshotSurvivesRequestCtxReuse 是「必须克隆」的直接证据。请求的方法、路径、
// User-Agent 与 RemoteAddr 都指向本测试在 ServeHTTP 返回后覆写的字节切片（此时 *router.Ctx 已归还
// 对象池）。若 Submit 没有用 strings.Clone，送达的 Record 就会看到被覆写的字节。
func TestAsyncAfterRecordSnapshotSurvivesRequestCtxReuse(t *testing.T) {
	gate := make(chan struct{})
	var gateOnce sync.Once
	open := func() { gateOnce.Do(func() { close(gate) }) }
	t.Cleanup(open)

	records := make(chan Record, 1)
	a := NewAsyncAfter(AsyncOptions{
		Workers:   1,
		QueueSize: 4,
		Handler: func(rec Record) {
			<-gate
			records <- rec
		},
	})
	t.Cleanup(func() {
		open()
		_ = a.Close(context.Background())
	})

	r := router.New()
	r.Use(RequestID(""), AccessLogAsync(nil, a))
	r.GET("/original", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	pathBytes := []byte("/original")
	methodBytes := []byte(http.MethodGet)
	uaBytes := []byte("snapshot-agent/1.0")
	remoteBytes := []byte("198.51.100.9:1234")

	req := httptest.NewRequest(http.MethodGet, "/original", nil)
	req.URL.Path = unsafe.String(&pathBytes[0], len(pathBytes))
	req.Method = unsafe.String(&methodBytes[0], len(methodBytes))
	req.Header.Set("User-Agent", unsafe.String(&uaBytes[0], len(uaBytes)))
	req.RemoteAddr = unsafe.String(&remoteBytes[0], len(remoteBytes))

	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", res.Code)
	}

	// The request is over and its Ctx is back in the pool; overwrite the bytes the request
	// strings pointed at. | 请求已结束，其 Ctx 已归还对象池；覆写请求字符串原本指向的字节。
	for i := range pathBytes {
		pathBytes[i] = 'x'
	}
	for i := range methodBytes {
		methodBytes[i] = 'X'
	}
	for i := range uaBytes {
		uaBytes[i] = 'y'
	}
	for i := range remoteBytes {
		remoteBytes[i] = 'z'
	}

	open()
	var got Record
	select {
	case got = <-records:
	case <-time.After(2 * time.Second):
		t.Fatal("no Record delivered within 2s")
	}
	if got.Path != "/original" {
		t.Errorf("Path = %q, want /original (snapshot was not cloned)", got.Path)
	}
	if got.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET (snapshot was not cloned)", got.Method)
	}
	if got.UA != "snapshot-agent/1.0" {
		t.Errorf("UA = %q, want snapshot-agent/1.0 (snapshot was not cloned)", got.UA)
	}
	if got.RemoteIP != "198.51.100.9" {
		t.Errorf("RemoteIP = %q, want 198.51.100.9 (snapshot was not cloned)", got.RemoteIP)
	}
}

// TestAccessLogAsyncNilPipelineFallsBackToSync proves the documented nil-pipeline fallback keeps
// the l parameter meaningful. | TestAccessLogAsyncNilPipelineFallsBackToSync 证明文档化的 nil 管线
// 回退行为，使 l 参数始终有意义。
func TestAccessLogAsyncNilPipelineFallsBackToSync(t *testing.T) {
	log, buf := newTestLogger()
	r := router.New()
	r.Use(AccessLogAsync(log, nil))
	r.GET("/sync", func(c *router.Ctx) { _ = c.String(http.StatusOK, "ok") })

	doReq(r, http.MethodGet, "/sync", nil, "")
	entry := onlyRecord(t, buf)
	if entry["path"] != "/sync" {
		t.Fatalf("path = %v, want /sync", entry["path"])
	}
}

// TestAccessLogAsyncSkipsUpgradeRequests keeps the WebSocket contract of AccessLog: an upgrade
// request is neither wrapped nor recorded. | TestAccessLogAsyncSkipsUpgradeRequests 保持 AccessLog
// 的 WebSocket 契约：升级请求既不包装也不记录。
func TestAccessLogAsyncSkipsUpgradeRequests(t *testing.T) {
	var handled atomic.Int64
	a := NewAsyncAfter(AsyncOptions{
		Workers: 1,
		Handler: func(Record) { handled.Add(1) },
	})
	t.Cleanup(func() { _ = a.Close(context.Background()) })

	hijackable := false
	r := router.New()
	r.Use(AccessLogAsync(nil, a))
	r.GET("/ws", func(c *router.Ctx) {
		_, hijackable = c.Writer.(http.Hijacker)
		_ = c.NoContent()
	})

	hj := &hijackRecorder{ResponseRecorder: newRecorder()}
	r.ServeHTTP(hj, newUpgradeRequest("/ws"))
	if !hijackable {
		t.Fatal("upgrade request lost http.Hijacker")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := handled.Load(); got != 0 {
		t.Fatalf("handled = %d, want 0 for upgrade requests", got)
	}
}

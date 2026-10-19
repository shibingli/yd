// Package sse implements a W3C Server-Sent Events (text/event-stream) writer and an in-process publish/subscribe broadcaster. | Package sse 实现 W3C Server-Sent Events（text/event-stream）写入器与进程内发布订阅广播器。
//
// The writer strictly follows the event stream format: one event consists of several field lines separated by LF, | 写入器严格遵循事件流格式：一个事件由若干字段行组成，字段行之间以 LF 分隔，
// and the event ends with a blank line; the field line order is fixed as comment → id → event → retry → data. | 事件以空行结束；字段行顺序固定为 comment → id → event → retry → data。
// The data field may span several lines (each `data:` line is joined by the client with LF), and CR / CRLF / LF inside a value | data 字段可跨多行（每条 `data:` 行由客户端以 LF 拼接），值中的 CR / CRLF / LF
// are all normalized into separate data lines, never appearing verbatim inside a single-line field. | 都会被规整为独立的数据行，绝不会原样出现在单行字段中。
//
// This package integrates seamlessly with router and can be used directly as the server endpoint of the Htmx SSE extension. | 本包与 router 无缝集成，可直接作为 Htmx SSE 扩展的服务端端点使用。
package sse

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shibingli/yd/core/errx"
)

// ErrFlusherUnsupported is returned by NewWriter when the http.ResponseWriter does not implement http.Flusher. | ErrFlusherUnsupported 在 http.ResponseWriter 未实现 http.Flusher 时由 NewWriter 返回。
//
// An event stream must be delivered frame by frame; a response writer lacking Flush capability (such as some middleware wrappers) | 事件流必须逐帧送达客户端，缺少 Flush 能力的响应写入器（如部分中间件包装器）
// would leave events sitting in a buffer, so this case is judged unusable outright. | 会把事件滞留在缓冲区，因此这种情况直接判定为不可用。
var ErrFlusherUnsupported = errx.New("sse: response writer does not support flushing | sse：响应写入器不支持 flush")

// ErrWriteStream is returned when the assembled event stream frame cannot be written to the response writer. | ErrWriteStream 在组装好的事件流帧无法写入响应写入器时返回。
//
// It keeps the underlying write error on the same chain, so errors.Is matches both the sentinel and that error | 它把底层写入错误保留在同一条链上，因此 errors.Is 既能匹配哨兵也能匹配该错误，
// (typical causes are a dropped client connection or a closed response body). | （常见原因是客户端断连或响应体已关闭）。
var ErrWriteStream = errx.New("sse: failed to write event stream | sse：写出事件流失败")

// Default values. | 默认值。
const (
	// defaultBufferSize is the initial capacity of the per-frame assembly buffer (in bytes). | defaultBufferSize 是单帧组装缓冲的初始容量（字节）。
	defaultBufferSize = 512
	// heartbeatComment is the comment text carried by heartbeat frames. | heartbeatComment 是心跳帧携带的注释文本。
	heartbeatComment = "ping"
	// maxRetainedBuffer caps the assembly buffer kept for reuse; a larger frame is not retained. | maxRetainedBuffer 限制为复用而保留的组装缓冲；更大的帧不会被保留。
	maxRetainedBuffer = 64 << 10
)

// Event describes one SSE event. | Event 描述一条 SSE 事件。
//
// Field lines corresponding to empty strings (or to an empty Data) are not written out; Data supports multi-line content | 空字符串（或空 Data）对应的字段行不会被输出；Data 支持多行内容，
// and is split into several `data:` lines when written. | 写出时会被拆成多条 `data:` 行。
type Event struct {
	// ID corresponds to the `id:` field; clients return it via the Last-Event-ID request header when reconnecting after a disconnect. | ID 对应 `id:` 字段；客户端断线重连时会通过 Last-Event-ID 请求头回传。
	ID string
	// Name corresponds to the `event:` field, i.e. the event type; empty means the default message event. | Name 对应 `event:` 字段，即事件类型；为空表示默认的 message 事件。
	Name string
	// Comment is a comment line (`: `); clients ignore its content, and it is commonly used for heartbeat keep-alive and debugging. | Comment 是注释行（`: `），客户端会忽略其内容，常用于心跳保活与调试。
	Comment string
	// Data corresponds to the `data:` field and may contain newlines (CR / CRLF / LF are all split into lines). | Data 对应 `data:` 字段，可包含换行（CR / CRLF / LF 均按行拆分）。
	Data []byte
	// Retry corresponds to the `retry:` field, i.e. the client reconnect interval; when >0 it is written in milliseconds. | Retry 对应 `retry:` 字段，即客户端重连间隔；>0 时以毫秒为单位输出。
	Retry time.Duration
}

// Options holds the behavior parameters of Writer and Handler. | Options 是 Writer 与 Handler 的行为参数。
type Options struct {
	// Tracker, when non-nil, registers every Writer built by NewWriter so that a shutdown can terminate all streams at once. | Tracker 非 nil 时登记 NewWriter 建立的每个 Writer，使退出流程能一次终止全部事件流。
	//
	// The writer unregisters itself from Close, so the set never leaks entries. | 写入器会在 Close 中自行取消登记，因此集合不会泄漏条目。
	Tracker *WriterSet
	// Heartbeat is the heartbeat interval; when >0, Handler periodically sends a `: ping` comment frame. | Heartbeat 是心跳间隔；>0 时 Handler 会周期性发送 `: ping` 注释帧。
	Heartbeat time.Duration
	// Retry is the default reconnect interval used when an event sets no Retry of its own (still written in milliseconds). | Retry 是事件自身未设置 Retry 时采用的默认重连间隔（仍以毫秒输出）。
	Retry time.Duration
	// BufferSize is the initial capacity of the per-frame assembly buffer; <=0 means 512. | BufferSize 是单帧组装缓冲的初始容量；<=0 时取 512。
	BufferSize int
}

// Writer writes events to an http.ResponseWriter in the W3C event stream format. | Writer 把事件按 W3C 事件流格式写入 http.ResponseWriter。
//
// The field output order is fixed as comment → id → event → retry → data lines → blank line. | 字段输出顺序固定为 comment → id → event → retry → data 行 → 空行。
// Writer serializes writes internally and may be called concurrently from several goroutines (for example the business handler and the heartbeat goroutine); | Writer 内部串行化写入，可被多个 goroutine 并发调用（例如业务处理器与心跳 goroutine）；
// all writes after Close are no-ops. | Close 之后的所有写入均为无操作。
type Writer struct {
	w       io.Writer
	flusher http.Flusher
	stop    chan struct{}
	stopped chan struct{}
	done    chan struct{}
	buf     []byte
	untrack func()

	options Options

	mu sync.Mutex

	hbMu    sync.Mutex
	trackMu sync.Mutex
	closed  bool
}

// NewWriter establishes an event stream: it sets the SSE response headers and checks the Flusher capability. | NewWriter 建立一条事件流：设置 SSE 响应头并校验 Flusher 能力。
//
// The response headers are fixed to `Content-Type: text/event-stream; charset=utf-8`, | 响应头固定为 `Content-Type: text/event-stream; charset=utf-8`、
// `Cache-Control: no-cache, no-transform`, `Connection: keep-alive`, and | `Cache-Control: no-cache, no-transform`、`Connection: keep-alive`、
// `X-Accel-Buffering: no` (which disables Nginx buffering so frames arrive one at a time). | `X-Accel-Buffering: no`（禁用 Nginx 缓冲，保证逐帧送达）。
// It returns ErrFlusherUnsupported when w does not implement http.Flusher. | 当 w 未实现 http.Flusher 时返回 ErrFlusherUnsupported。
func NewWriter(w http.ResponseWriter, o Options) (*Writer, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, ErrFlusherUnsupported
	}
	if o.BufferSize <= 0 {
		o.BufferSize = defaultBufferSize
	}
	// An event stream outlives the server's write timeout. net/http applies one absolute write
	// deadline to the whole response, so without clearing it here every stream dies when
	// server.write_timeout elapses (15s by default). ResponseController is the supported way to
	// reach the underlying writer through middleware wrappers; unsupported writers keep their
	// previous behaviour. | 事件流的寿命长于服务器的写超时。net/http 对整个响应只设置一个绝对写超时，
	// 不在此清除的话，server.write_timeout（默认 15s）一到所有事件流都会失败。ResponseController
	// 是穿透中间件包装器访问底层写入器的受支持方式；不支持时维持原有行为。
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	out := &Writer{
		w:       w,
		flusher: f,
		options: o,
		buf:     make([]byte, 0, o.BufferSize),
		done:    make(chan struct{}),
	}
	out.setTracker(o.Tracker)
	return out, nil
}

// Done returns a channel closed once this writer is closed. | Done 返回本写入器关闭后被关闭的通道。
//
// A streaming handler can select on it to return as soon as a shutdown closes
// the stream, instead of blocking on an event channel that may never receive
// another frame. | 流式处理器可以 select 监听它，在退出流程关闭事件流时立即返回，
// 而不必阻塞在一个可能永远收不到新帧的事件通道上。
func (w *Writer) Done() <-chan struct{} { return w.done }

// setTracker registers the writer with a writer set and arms automatic untracking. | setTracker 把写入器登记到写入器集合，并装配自动取消登记。
func (w *Writer) setTracker(s *WriterSet) {
	if s == nil {
		return
	}
	untrack := s.Track(w)
	w.trackMu.Lock()
	w.untrack = untrack
	w.trackMu.Unlock()
}

// detach removes the writer from its set; it is idempotent. | detach 把写入器从集合中移除；该方法是幂等的。
func (w *Writer) detach() {
	w.trackMu.Lock()
	untrack := w.untrack
	w.untrack = nil
	w.trackMu.Unlock()
	if untrack != nil {
		untrack()
	}
}

// WriteEvent writes one event and flushes it immediately. | WriteEvent 写出一条事件并立即刷新。
//
// The event ends with a blank line; newlines inside Data are split into several `data:` lines. | 事件以空行结束；Data 中的换行会被拆分为多条 `data:` 行。
// When Retry is 0 it falls back to Options.Retry. | Retry 为 0 时回退到 Options.Retry。
func (w *Writer) WriteEvent(e Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeEventLocked(e)
}

// WriteData writes an event carrying only the data field, with the default event type (message). | WriteData 以默认事件类型（message）写出一条仅含 data 字段的事件。
//
// It is equivalent to WriteEvent(Event{Data: []byte(s)}), except that it emits one `data: ` line even when s is empty. | 等价于 WriteEvent(Event{Data: []byte(s)})，但即使 s 为空也会输出一行 `data: `。
// The string is appended straight into the assembly buffer, so no []byte(s) conversion is made. | 字符串直接追加进组装缓冲，因此不会产生 []byte(s) 转换。
func (w *Writer) WriteData(s string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	buf := w.buf[:0]
	if ms := retryMillis(0, w.options.Retry); ms > 0 {
		buf = append(buf, "retry: "...)
		buf = strconv.AppendUint(buf, ms, 10)
		buf = append(buf, '\n')
	}
	buf = appendDataLinesString(buf, s)
	buf = append(buf, '\n')
	return w.flushLocked(buf)
}

// Comment writes a comment frame (`: <text>`), whose content clients ignore. | Comment 写出一条注释帧（`: <text>`），客户端会忽略其内容。
//
// Multi-line text gets a `: ` prefix added to every line; Heartbeat itself uses this method to send `: ping`. | 多行文本会逐行加上 `: ` 前缀；Heartbeat 内部即使用本方法发送 `: ping`。
func (w *Writer) Comment(text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	buf := appendCommentLines(w.buf[:0], text)
	buf = append(buf, '\n')
	return w.flushLocked(buf)
}

// Heartbeat starts a background heartbeat: it sends one `: ping` comment frame every d, to keep the connection alive and detect disconnects early. | Heartbeat 启动后台心跳：每隔 d 发送一帧 `: ping` 注释，用于保活并及早发现断线。
//
// Calling it again first stops the previous heartbeat and then restarts it with the new interval; d <= 0 means stopping the heartbeat only. | 重复调用会先停止上一次心跳再按新间隔重启；d <= 0 表示仅停止心跳。
// The heartbeat goroutine exits on Close or on a failed write. | 心跳 goroutine 在 Close 或写出失败时退出。
func (w *Writer) Heartbeat(d time.Duration) {
	w.hbMu.Lock()
	defer w.hbMu.Unlock()
	w.stopHeartbeatLocked()
	if d <= 0 {
		return
	}
	w.mu.Lock()
	closed := w.closed
	w.mu.Unlock()
	if closed {
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	w.stop, w.stopped = stop, done
	go w.heartbeatLoop(d, stop, done)
}

// Close stops the heartbeat, flushes the buffer, and closes the event stream; calling it repeatedly is safe. | Close 停止心跳、刷新缓冲并关闭事件流；重复调用是安全的。
//
// Writes after closing (WriteEvent / WriteData / Comment / Heartbeat) are all no-ops. | 关闭后的写入（WriteEvent / WriteData / Comment / Heartbeat）均为无操作。
func (w *Writer) Close() error {
	// Leave the tracked set first: a shutdown will no longer write into a stream that is closing. | 先离开追踪集合：退出流程不会再向一条正在关闭的事件流写帧。
	w.detach()
	w.stopHeartbeat()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	close(w.done)
	w.flusher.Flush()
	return nil
}

// ---------------------------------------------------------------- Internal implementation | 内部实现

// heartbeatLoop sends heartbeat comments periodically until stop is closed or a write fails. | heartbeatLoop 周期性发送心跳注释，直到 stop 关闭或写出失败。
func (w *Writer) heartbeatLoop(d time.Duration, stop, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(d)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if err := w.Comment(heartbeatComment); err != nil {
				return
			}
		}
	}
}

// stopHeartbeat stops the heartbeat goroutine and waits for it to exit. | stopHeartbeat 停止心跳 goroutine 并等待其退出。
func (w *Writer) stopHeartbeat() {
	w.hbMu.Lock()
	defer w.hbMu.Unlock()
	w.stopHeartbeatLocked()
}

// stopHeartbeatLocked stops and waits for the heartbeat goroutine while holding hbMu. | stopHeartbeatLocked 在持有 hbMu 的前提下停止并等待心跳 goroutine。
func (w *Writer) stopHeartbeatLocked() {
	stop, done := w.stop, w.stopped
	w.stop, w.stopped = nil, nil
	if stop == nil {
		return
	}
	close(stop)
	if done != nil {
		<-done
	}
}

// writeEventLocked assembles and writes one complete event frame. | writeEventLocked 组装并写出一个完整事件帧。
func (w *Writer) writeEventLocked(e Event) error {
	if w.closed {
		return nil
	}
	buf := w.buf[:0]
	if e.Comment != "" {
		buf = appendCommentLines(buf, e.Comment)
	}
	if e.ID != "" {
		buf = append(buf, "id: "...)
		buf = appendSingleLine(buf, e.ID)
		buf = append(buf, '\n')
	}
	if e.Name != "" {
		buf = append(buf, "event: "...)
		buf = appendSingleLine(buf, e.Name)
		buf = append(buf, '\n')
	}
	if ms := retryMillis(e.Retry, w.options.Retry); ms > 0 {
		buf = append(buf, "retry: "...)
		buf = strconv.AppendUint(buf, ms, 10)
		buf = append(buf, '\n')
	}
	if len(e.Data) > 0 {
		buf = appendDataLines(buf, e.Data)
	}
	buf = append(buf, '\n')
	return w.flushLocked(buf)
}

// flushLocked writes the assembled frame and flushes immediately; it must be called while holding w.mu. | flushLocked 写出组好的帧并立即刷新；必须在持有 w.mu 时调用。
//
// A one-off huge frame (a large backlog dump, say) must not pin its buffer for the lifetime of the
// stream, so the retained capacity is capped and a fresh buffer of the configured size is allocated. | 一次性的
// 超大帧（例如一次大 backlog 输出）不得让缓冲在整条流的生命周期内驻留，因此保留容量设有上限，
// 超出时换成配置大小的新缓冲。
func (w *Writer) flushLocked(buf []byte) error {
	if cap(buf) > maxRetainedBuffer {
		w.buf = make([]byte, 0, w.options.BufferSize)
	} else {
		w.buf = buf
	}
	if _, err := w.w.Write(buf); err != nil {
		return errx.Wrap(err, ErrWriteStream)
	}
	w.flusher.Flush()
	return nil
}

// retryMillis computes the retry milliseconds this event should emit; a return of 0 means the field is omitted. | retryMillis 计算本条事件应输出的 retry 毫秒数；返回 0 表示不输出该字段。
func retryMillis(event, fallback time.Duration) uint64 {
	d := event
	if d <= 0 {
		d = fallback
	}
	if d <= 0 {
		return 0
	}
	ms := uint64(d / time.Millisecond)
	if ms == 0 {
		ms = 1 // anything under 1ms is emitted as 1ms anyway, so that `retry: 0` never appears | 不足 1ms 也按 1ms 输出，避免出现 `retry: 0`
	}
	return ms
}

// appendCommentLines splits the comment text on CRLF / CR / LF and appends `: <line>` for every line. | appendCommentLines 把注释文本按 CRLF / CR / LF 拆行，逐行追加 `: <line>`。
//
// The split is done while scanning, without strings.ReplaceAll, so a multi-line comment allocates
// nothing beyond the assembly buffer. | 拆分在扫描过程中完成，不经过 strings.ReplaceAll，因此多行注释
// 除组装缓冲外不产生任何分配。
func appendCommentLines(buf []byte, text string) []byte {
	start := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			buf = appendCommentLine(buf, text[start:i])
			start = i + 1
		case '\r':
			buf = appendCommentLine(buf, text[start:i])
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	return appendCommentLine(buf, text[start:])
}

// appendCommentLine appends one `: <line>` line. | appendCommentLine 追加一行 `: <line>`。
func appendCommentLine(buf []byte, line string) []byte {
	buf = append(buf, ':')
	if line != "" {
		buf = append(buf, ' ')
		buf = append(buf, line...)
	}
	return append(buf, '\n')
}

// appendDataLines splits data on CRLF / CR / LF and appends `data: <line>` for every line. | appendDataLines 把 data 按 CRLF / CR / LF 拆行，逐行追加 `data: <line>`。
func appendDataLines(buf, data []byte) []byte {
	start := 0
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '\n':
			buf = appendDataLine(buf, data[start:i])
			start = i + 1
		case '\r':
			buf = appendDataLine(buf, data[start:i])
			if i+1 < len(data) && data[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	return appendDataLine(buf, data[start:])
}

// appendDataLine appends a single `data:` line. | appendDataLine 追加单行 `data:`。
func appendDataLine(buf, line []byte) []byte {
	buf = append(buf, "data: "...)
	buf = append(buf, line...)
	return append(buf, '\n')
}

// appendDataLinesString is appendDataLines for a string, avoiding a []byte conversion. | appendDataLinesString 是
// appendDataLines 的字符串版本，避免 []byte 转换。
func appendDataLinesString(buf []byte, data string) []byte {
	start := 0
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '\n':
			buf = appendDataLineString(buf, data[start:i])
			start = i + 1
		case '\r':
			buf = appendDataLineString(buf, data[start:i])
			if i+1 < len(data) && data[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	return appendDataLineString(buf, data[start:])
}

// appendDataLineString appends a single `data:` line from a string. | appendDataLineString 从字符串追加单行 `data:`。
func appendDataLineString(buf []byte, line string) []byte {
	buf = append(buf, "data: "...)
	buf = append(buf, line...)
	return append(buf, '\n')
}

// appendSingleLine appends s with any CR / LF stripped out (the id and event fields must be single-line). | appendSingleLine 追加 s 并剔除其中的 CR / LF（id、event 字段必须是单行）。
func appendSingleLine(buf []byte, s string) []byte {
	if !strings.ContainsAny(s, "\r\n") {
		return append(buf, s...)
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c != '\r' && c != '\n' {
			buf = append(buf, c)
		}
	}
	return buf
}

// Package sse 实现 W3C Server-Sent Events（text/event-stream）写入器与进程内发布订阅广播器。
//
// 写入器严格遵循事件流格式：一个事件由若干字段行组成，字段行之间以 LF 分隔，
// 事件以空行结束；字段行顺序固定为 comment → id → event → retry → data。
// data 字段可跨多行（每条 `data:` 行由客户端以 LF 拼接），值中的 CR / CRLF / LF
// 都会被规整为独立的数据行，绝不会原样出现在单行字段中。
//
// 本包与 router 无缝集成，可直接作为 Htmx SSE 扩展的服务端端点使用。
package sse

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrFlusherUnsupported 在 http.ResponseWriter 未实现 http.Flusher 时由 NewWriter 返回。
//
// 事件流必须逐帧送达客户端，缺少 Flush 能力的响应写入器（如部分中间件包装器）
// 会把事件滞留在缓冲区，因此这种情况直接判定为不可用。
var ErrFlusherUnsupported = errors.New(
	"sse: http.ResponseWriter 未实现 http.Flusher，无法推送事件流")

// 默认值。
const (
	// defaultBufferSize 是单帧组装缓冲的初始容量（字节）。
	defaultBufferSize = 512
	// heartbeatComment 是心跳帧携带的注释文本。
	heartbeatComment = "ping"
)

// Event 描述一条 SSE 事件。
//
// 空字符串（或空 Data）对应的字段行不会被输出；Data 支持多行内容，
// 写出时会被拆成多条 `data:` 行。
type Event struct {
	// ID 对应 `id:` 字段；客户端断线重连时会通过 Last-Event-ID 请求头回传。
	ID string
	// Name 对应 `event:` 字段，即事件类型；为空表示默认的 message 事件。
	Name string
	// Data 对应 `data:` 字段，可包含换行（CR / CRLF / LF 均按行拆分）。
	Data []byte
	// Retry 对应 `retry:` 字段，即客户端重连间隔；>0 时以毫秒为单位输出。
	Retry time.Duration
	// Comment 是注释行（`: `），客户端会忽略其内容，常用于心跳保活与调试。
	Comment string
}

// Options 是 Writer 与 Handler 的行为参数。
type Options struct {
	// Heartbeat 是心跳间隔；>0 时 Handler 会周期性发送 `: ping` 注释帧。
	Heartbeat time.Duration
	// Retry 是事件自身未设置 Retry 时采用的默认重连间隔（仍以毫秒输出）。
	Retry time.Duration
	// BufferSize 是单帧组装缓冲的初始容量；<=0 时取 512。
	BufferSize int
}

// Writer 把事件按 W3C 事件流格式写入 http.ResponseWriter。
//
// 字段输出顺序固定为 comment → id → event → retry → data 行 → 空行。
// Writer 内部串行化写入，可被多个 goroutine 并发调用（例如业务处理器与心跳 goroutine）；
// Close 之后的所有写入均为无操作。
type Writer struct {
	w       io.Writer
	flusher http.Flusher
	options Options
	buf     []byte

	mu     sync.Mutex
	closed bool

	hbMu    sync.Mutex
	stop    chan struct{}
	stopped chan struct{}
}

// NewWriter 建立一条事件流：设置 SSE 响应头并校验 Flusher 能力。
//
// 响应头固定为 `Content-Type: text/event-stream; charset=utf-8`、
// `Cache-Control: no-cache, no-transform`、`Connection: keep-alive`、
// `X-Accel-Buffering: no`（禁用 Nginx 缓冲，保证逐帧送达）。
// 当 w 未实现 http.Flusher 时返回 ErrFlusherUnsupported。
func NewWriter(w http.ResponseWriter, o Options) (*Writer, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, ErrFlusherUnsupported
	}
	if o.BufferSize <= 0 {
		o.BufferSize = defaultBufferSize
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	return &Writer{
		w:       w,
		flusher: f,
		options: o,
		buf:     make([]byte, 0, o.BufferSize),
	}, nil
}

// WriteEvent 写出一条事件并立即刷新。
//
// 事件以空行结束；Data 中的换行会被拆分为多条 `data:` 行。
// Retry 为 0 时回退到 Options.Retry。
func (w *Writer) WriteEvent(e Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeEventLocked(e, false)
}

// WriteData 以默认事件类型（message）写出一条仅含 data 字段的事件。
//
// 等价于 WriteEvent(Event{Data: []byte(s)})，但即使 s 为空也会输出一行 `data: `。
func (w *Writer) WriteData(s string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeEventLocked(Event{Data: []byte(s)}, true)
}

// Comment 写出一条注释帧（`: <text>`），客户端会忽略其内容。
//
// 多行文本会逐行加上 `: ` 前缀；Heartbeat 内部即使用本方法发送 `: ping`。
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

// Heartbeat 启动后台心跳：每隔 d 发送一帧 `: ping` 注释，用于保活并及早发现断线。
//
// 重复调用会先停止上一次心跳再按新间隔重启；d <= 0 表示仅停止心跳。
// 心跳 goroutine 在 Close 或写出失败时退出。
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

// Close 停止心跳、刷新缓冲并关闭事件流；重复调用是安全的。
//
// 关闭后的写入（WriteEvent / WriteData / Comment / Heartbeat）均为无操作。
func (w *Writer) Close() error {
	w.stopHeartbeat()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	w.flusher.Flush()
	return nil
}

// ---------------------------------------------------------------- 内部实现

// heartbeatLoop 周期性发送心跳注释，直到 stop 关闭或写出失败。
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

// stopHeartbeat 停止心跳 goroutine 并等待其退出。
func (w *Writer) stopHeartbeat() {
	w.hbMu.Lock()
	defer w.hbMu.Unlock()
	w.stopHeartbeatLocked()
}

// stopHeartbeatLocked 在持有 hbMu 的前提下停止并等待心跳 goroutine。
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

// writeEventLocked 组装并写出一个完整事件帧；forceData 为 true 时即使 Data 为空也输出 data 行。
func (w *Writer) writeEventLocked(e Event, forceData bool) error {
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
	if forceData || len(e.Data) > 0 {
		buf = appendDataLines(buf, e.Data)
	}
	buf = append(buf, '\n')
	return w.flushLocked(buf)
}

// flushLocked 写出组好的帧并立即刷新；必须在持有 w.mu 时调用。
func (w *Writer) flushLocked(buf []byte) error {
	w.buf = buf
	if _, err := w.w.Write(buf); err != nil {
		return fmt.Errorf("sse: 写出事件流失败: %w", err)
	}
	w.flusher.Flush()
	return nil
}

// retryMillis 计算本条事件应输出的 retry 毫秒数；返回 0 表示不输出该字段。
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
		ms = 1 // 不足 1ms 也按 1ms 输出，避免出现 `retry: 0`
	}
	return ms
}

// appendCommentLines 把注释文本按 CRLF / CR / LF 拆行，逐行追加 `: <line>`。
func appendCommentLines(buf []byte, text string) []byte {
	if strings.ContainsAny(text, "\r\n") {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
	}
	for {
		line, rest, more := strings.Cut(text, "\n")
		buf = append(buf, ':')
		if line != "" {
			buf = append(buf, ' ')
			buf = append(buf, line...)
		}
		buf = append(buf, '\n')
		if !more {
			return buf
		}
		text = rest
	}
}

// appendDataLines 把 data 按 CRLF / CR / LF 拆行，逐行追加 `data: <line>`。
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

// appendDataLine 追加单行 `data:`。
func appendDataLine(buf, line []byte) []byte {
	buf = append(buf, "data: "...)
	buf = append(buf, line...)
	return append(buf, '\n')
}

// appendSingleLine 追加 s 并剔除其中的 CR / LF（id、event 字段必须是单行）。
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

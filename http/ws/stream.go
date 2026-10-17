package ws

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// streamAddr is the address reported by a stream transport. | streamAddr 是流传输上报的地址。
type streamAddr struct {
	network string
	addr    string
}

// Network implements net.Addr. | Network 实现 net.Addr。
func (a streamAddr) Network() string { return a.network }

// String implements net.Addr. | String 实现 net.Addr。
func (a streamAddr) String() string { return a.addr }

// addrOrFallback keeps LocalAddr and RemoteAddr non-nil. | addrOrFallback 保证 LocalAddr 与 RemoteAddr 非 nil。
func addrOrFallback(addr net.Addr, network, fallback string) net.Addr {
	if addr != nil {
		return addr
	}
	return streamAddr{network: network, addr: fallback}
}

// streamConn adapts an HTTP/2 or HTTP/3 request/response stream to net.Conn, so
// the frame layer can reuse one code path on every transport. | streamConn 把
// HTTP/2 或 HTTP/3 的请求/响应流适配成 net.Conn，使帧层在三种传输上复用同一条路径。
//
// Server side: Read comes from r.Body and Write goes to the ResponseWriter; the
// start and end of the stream are owned by the HTTP handler. Client side: Read
// comes from the response body and Write goes to the request-body pipe; Close
// half-closes the stream and cancels the request. | 服务端：读来自 r.Body，写发往
// ResponseWriter，流的起止由 HTTP 处理器掌握。客户端：读来自响应体，写发往请求体管道；
// Close 会半关闭流并取消请求。
type streamConn struct {
	body   io.ReadCloser
	writer io.Writer
	local  net.Addr
	remote net.Addr
	flush  func() error
	rc     *http.ResponseController
	cancel context.CancelFunc
	// closeWrite half-closes the client write side; nil on the server. | closeWrite
	// 半关闭客户端写侧；服务端为 nil。
	closeWrite func(error) error

	readTimer  *time.Timer
	writeTimer *time.Timer

	deadlineMu sync.Mutex

	closed atomic.Bool
	// readExpired and writeExpired turn a deadline hit into a sticky transport error. | readExpired
	// 与 writeExpired 把截止时间命中变成粘性的传输错误。
	readExpired  atomic.Bool
	writeExpired atomic.Bool
}

// newStreamConnServer builds the server-side adapter. | newStreamConnServer 构造服务端适配器。
func newStreamConnServer(body io.ReadCloser, w http.ResponseWriter, rc *http.ResponseController, local, remote net.Addr) *streamConn {
	c := &streamConn{
		body:   body,
		writer: w,
		rc:     rc,
		local:  addrOrFallback(local, "http-stream", ""),
		remote: addrOrFallback(remote, "http-stream", ""),
	}
	if rc != nil {
		c.flush = rc.Flush
	}
	return c
}

// newStreamConnClient builds the client-side adapter. | newStreamConnClient 构造客户端适配器。
func newStreamConnClient(body io.ReadCloser, w *io.PipeWriter, cancel context.CancelFunc, local, remote net.Addr) *streamConn {
	c := &streamConn{
		body:   body,
		writer: w,
		cancel: cancel,
		local:  addrOrFallback(local, "ws-stream", ""),
		remote: addrOrFallback(remote, "ws-stream", ""),
	}
	if w != nil {
		c.closeWrite = w.CloseWithError
	}
	return c
}

// Read implements net.Conn. | Read 实现 net.Conn。
func (c *streamConn) Read(p []byte) (int, error) {
	if c.readExpired.Load() {
		return 0, os.ErrDeadlineExceeded
	}
	n, err := c.body.Read(p)
	if err != nil && c.readExpired.Load() {
		return n, os.ErrDeadlineExceeded
	}
	return n, err
}

// Write implements net.Conn. | Write 实现 net.Conn。
func (c *streamConn) Write(p []byte) (int, error) {
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	if c.writeExpired.Load() {
		return 0, os.ErrDeadlineExceeded
	}
	return c.writer.Write(p)
}

// Close implements net.Conn; it is idempotent. | Close 实现 net.Conn，且是幂等的。
func (c *streamConn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	c.deadlineMu.Lock()
	if c.readTimer != nil {
		c.readTimer.Stop()
		c.readTimer = nil
	}
	if c.writeTimer != nil {
		c.writeTimer.Stop()
		c.writeTimer = nil
	}
	c.deadlineMu.Unlock()

	var errs []error
	if c.body != nil {
		if err := c.body.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.closeWrite != nil {
		if err := c.closeWrite(nil); err != nil {
			errs = append(errs, err)
		}
	}
	if c.cancel != nil {
		c.cancel()
	}
	return errors.Join(errs...)
}

// LocalAddr implements net.Conn. | LocalAddr 实现 net.Conn。
func (c *streamConn) LocalAddr() net.Addr { return c.local }

// RemoteAddr implements net.Conn. | RemoteAddr 实现 net.Conn。
func (c *streamConn) RemoteAddr() net.Addr { return c.remote }

// SetDeadline implements net.Conn. | SetDeadline 实现 net.Conn。
func (c *streamConn) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}

// SetReadDeadline implements net.Conn. | SetReadDeadline 实现 net.Conn。
//
// Server streams forward to the ResponseController, which both net/http and
// quic-go support. Client streams arm a timer that closes the response body and
// cancels the request, because a blocking body read has no other interrupt. | 服务端
// 流转发给 ResponseController（net/http 与 quic-go 都支持）。客户端流用定时器关闭响应体
// 并取消请求，因为阻塞中的 body 读取没有别的中断方式。
func (c *streamConn) SetReadDeadline(t time.Time) error {
	if c.rc != nil {
		return c.rc.SetReadDeadline(t)
	}

	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if c.readTimer != nil {
		c.readTimer.Stop()
		c.readTimer = nil
	}
	if t.IsZero() {
		return nil
	}
	if d := time.Until(t); d <= 0 {
		c.expireRead()
		return nil
	} else {
		c.readTimer = time.AfterFunc(d, c.expireRead)
	}
	return nil
}

// SetWriteDeadline implements net.Conn. | SetWriteDeadline 实现 net.Conn。
func (c *streamConn) SetWriteDeadline(t time.Time) error {
	if c.rc != nil {
		return c.rc.SetWriteDeadline(t)
	}

	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	if c.writeTimer != nil {
		c.writeTimer.Stop()
		c.writeTimer = nil
	}
	if t.IsZero() {
		return nil
	}
	if d := time.Until(t); d <= 0 {
		c.expireWrite()
		return nil
	} else {
		c.writeTimer = time.AfterFunc(d, c.expireWrite)
	}
	return nil
}

// expireRead marks the read side expired and unblocks it. | expireRead 标记读侧已超时并解除阻塞。
func (c *streamConn) expireRead() {
	c.readExpired.Store(true)
	if c.body != nil {
		_ = c.body.Close()
	}
	if c.cancel != nil {
		c.cancel()
	}
}

// expireWrite marks the write side expired and unblocks it. | expireWrite 标记写侧已超时并解除阻塞。
func (c *streamConn) expireWrite() {
	c.writeExpired.Store(true)
	if c.closeWrite != nil {
		_ = c.closeWrite(os.ErrDeadlineExceeded)
	}
	if c.cancel != nil {
		c.cancel()
	}
}

// FlushError flushes one frame on a stream transport; it is a no-op on the
// client and on TCP (which never exposes this method). | FlushError 在流传输上刷新
// 一帧；客户端与 TCP 都是无操作（TCP 从不暴露该方法）。
func (c *streamConn) FlushError() error {
	if c.flush == nil {
		return nil
	}
	return c.flush()
}

var _ net.Conn = (*streamConn)(nil)

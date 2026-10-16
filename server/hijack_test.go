package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// baseWriter carries the three methods every http.ResponseWriter needs. | baseWriter 承载每个
// http.ResponseWriter 都需要的三个方法。
//
// Go 的方法集是静态的：一个类型要么实现某接口，要么不实现，无法用字段开关切换。
// 因此这里用「嵌入基类 + 各能力单独一层」的方式构造出能力不同的写入器。
// Go method sets are static: a type either implements an interface or it does not, and no field
// can switch that. The writers below are therefore built by embedding a base plus one layer per
// capability.
type baseWriter struct {
	header http.Header
	body   bytes.Buffer
}

// Header implements http.ResponseWriter. | Header 实现 http.ResponseWriter。
func (b *baseWriter) Header() http.Header { return b.header }

// Write implements http.ResponseWriter. | Write 实现 http.ResponseWriter。
func (b *baseWriter) Write(p []byte) (int, error) { return b.body.Write(p) }

// WriteHeader implements http.ResponseWriter. | WriteHeader 实现 http.ResponseWriter。
func (b *baseWriter) WriteHeader(int) {}

// plainWriter is a bare ResponseWriter: it cannot hijack, which is how an HTTP/2 or HTTP/3
// writer looks to the wrapper. | plainWriter 是裸的 ResponseWriter：它不能 hijack，
// 这正是 HTTP/2 或 HTTP/3 的写入器在包装器眼中的样子。
type plainWriter struct{ baseWriter }

// newPlainWriter builds a bare writer. | newPlainWriter 构建裸写入器。
func newPlainWriter() *plainWriter {
	return &plainWriter{baseWriter{header: make(http.Header)}}
}

// hijackable adds http.Hijacker on top of the base — but deliberately no Flush and no
// ReadFrom, so the generic copy path is exercised. | hijackable 在基类之上增加 http.Hijacker ——
// 但刻意不带 Flush 与 ReadFrom，以便覆盖通用拷贝路径。
type hijackable struct{ baseWriter }

// Hijack implements http.Hijacker and returns an inert connection. | Hijack 实现 http.Hijacker 并返回一个惰性连接。
func (h *hijackable) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return &fakeConn{}, bufio.NewReadWriter(bufio.NewReader(strings.NewReader("")), bufio.NewWriter(io.Discard)), nil
}

// newHijackable builds a hijackable writer without the optional fast paths. | newHijackable 构建不带可选快速路径的可 hijack 写入器。
func newHijackable() *hijackable {
	return &hijackable{baseWriter{header: make(http.Header)}}
}

// fullWriter is what a real HTTP/1.1 writer looks like: hijackable, flushable and able to take
// the io.ReaderFrom fast path. | fullWriter 是真实 HTTP/1.1 写入器的样子：可 hijack、可 flush，
// 并支持 io.ReaderFrom 快速路径。
type fullWriter struct {
	hijackable
	readFroms int
}

// Flush implements http.Flusher. | Flush 实现 http.Flusher。
func (f *fullWriter) Flush() {}

// ReadFrom implements io.ReaderFrom and counts calls, which is how the test proves the fast path
// was taken rather than the generic copy loop. | ReadFrom 实现 io.ReaderFrom 并统计调用次数，
// 测试据此证明走的是快速路径而不是通用拷贝循环。
func (f *fullWriter) ReadFrom(r io.Reader) (int64, error) {
	f.readFroms++
	return f.body.ReadFrom(r)
}

// newFullWriter builds a writer with every optional capability. | newFullWriter 构建具备全部可选能力的写入器。
func newFullWriter() *fullWriter {
	return &fullWriter{hijackable: hijackable{baseWriter{header: make(http.Header)}}}
}

// fakeConn is an inert net.Conn that only satisfies the Hijacker signature. | fakeConn 是惰性的 net.Conn，仅用于满足 Hijack 签名。
type fakeConn struct{ net.Conn }

// wrapForTest runs h through the hijack wrapper of a bare server. | wrapForTest 让 h 经过裸服务器的 hijack 包装器。
func wrapForTest(h http.HandlerFunc) http.Handler {
	return (&Server{}).trackHijack(h)
}

// TestReadFromKeepsUnderlyingFastPath 断言包装不吞掉底层写入器的 io.ReaderFrom 快速路径。
// TestReadFromKeepsUnderlyingFastPath asserts wrapping does not swallow the underlying io.ReaderFrom fast path.
//
// 这是热路径要求：包装器装在每一个 HTTP/1.1 请求上，若丢掉 ReaderFrom，
// 所有静态资源响应都会退回逐块拷贝。
// This is a hot-path requirement: the wrapper is installed on every HTTP/1.1 request, and losing
// ReaderFrom would send every static asset response back to the generic copy loop.
func TestReadFromKeepsUnderlyingFastPath(t *testing.T) {
	underlying := newFullWriter()

	var sawReaderFrom bool
	wrapForTest(func(w http.ResponseWriter, _ *http.Request) {
		rf, ok := w.(io.ReaderFrom)
		sawReaderFrom = ok
		if !ok {
			return
		}
		const payload = "static asset body | 静态资源响应体"
		n, err := rf.ReadFrom(strings.NewReader(payload))
		if err != nil {
			t.Errorf("ReadFrom 返回错误: %v", err)
		}
		if want := int64(len(payload)); n != want {
			t.Errorf("ReadFrom 写入字节数 = %d, 期望 %d", n, want)
		}
	}).ServeHTTP(underlying, &http.Request{})

	if !sawReaderFrom {
		t.Fatal("包装后的写入器不再实现 io.ReaderFrom，快速路径已丢失")
	}
	if underlying.readFroms != 1 {
		t.Errorf("底层 ReadFrom 调用次数 = %d, 期望 1（必须委托给底层）", underlying.readFroms)
	}
	if got, want := underlying.body.String(), "static asset body | 静态资源响应体"; got != want {
		t.Errorf("底层收到的内容 = %q, 期望 %q", got, want)
	}
}

// TestWrapperOverRealConnectionKeepsFastPath 在真实 HTTP/1.1 连接上断言快速路径依然存在。
// TestWrapperOverRealConnectionKeepsFastPath asserts the fast path survives on a real HTTP/1.1 connection.
//
// 上一条测试证明「底层支持时包装器会委托」，这一条证明「真实连接上的底层确实支持」，
// 两者合起来才构成完整的防回归护栏。
// The previous test proves the wrapper delegates when the underlying writer supports ReadFrom; this
// one proves the underlying writer of a real connection really does. Together they form the guard.
func TestWrapperOverRealConnectionKeepsFastPath(t *testing.T) {
	var sawHijacker, sawReaderFrom, sawFlusher bool

	srv := httptest.NewServer(wrapForTest(func(w http.ResponseWriter, _ *http.Request) {
		_, sawHijacker = w.(http.Hijacker)
		_, sawReaderFrom = w.(io.ReaderFrom)
		_, sawFlusher = w.(http.Flusher)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("真实 HTTP/1.1 请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if !sawHijacker {
		t.Fatal("真实 HTTP/1.1 处理器未拿到可 hijack 的写入器，包装器根本没装上")
	}
	if !sawReaderFrom {
		t.Error("真实 HTTP/1.1 连接上丢失了 io.ReaderFrom，静态资源将退回逐块拷贝")
	}
	if !sawFlusher {
		t.Error("真实 HTTP/1.1 连接上丢失了 http.Flusher，SSE 将无法推送")
	}
}

// TestReadFromWithoutFastPathDoesNotRecurse 断言底层没有快速路径时 io.Copy 不会递归回包装器自身。
// TestReadFromWithoutFastPathDoesNotRecurse asserts io.Copy does not recurse back into the wrapper when the underlying writer has no fast path.
func TestReadFromWithoutFastPathDoesNotRecurse(t *testing.T) {
	// 可 hijack 但没有 ReaderFrom：包装器会装上，而它自己声明了 ReaderFrom。
	// Hijackable but without ReaderFrom: the wrapper is installed and itself declares ReaderFrom.
	underlying := newHijackable()

	done := make(chan struct{})
	wrapForTest(func(w http.ResponseWriter, _ *http.Request) {
		defer close(done)
		const payload = "no fast path | 无快速路径"
		n, err := io.Copy(w, strings.NewReader(payload))
		if err != nil {
			t.Errorf("io.Copy 返回错误: %v", err)
		}
		if want := int64(len(payload)); n != want {
			t.Errorf("io.Copy 写入字节数 = %d, 期望 %d", n, want)
		}
	}).ServeHTTP(underlying, &http.Request{})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("io.Copy 未在 5 秒内返回，包装器的 ReadFrom 可能递归回了自身")
	}
	if got, want := underlying.body.String(), "no fast path | 无快速路径"; got != want {
		t.Errorf("底层收到的内容 = %q, 期望 %q", got, want)
	}
}

// TestWrapperIsInstalledOnlyWhenHijackable 断言包装只装在可 hijack 的写入器上。
// TestWrapperIsInstalledOnlyWhenHijackable asserts the wrapper is installed only on hijackable writers.
//
// 这是 HTTP/2 与 HTTP/3 的行为护栏：它们的写入器不支持 hijack，必须原样透传，
// 这样 ws 仍会得到 ErrHijackUnsupported。
// This guards HTTP/2 and HTTP/3 behaviour: their writers cannot hijack, so they must pass through
// untouched and ws must still observe ErrHijackUnsupported.
func TestWrapperIsInstalledOnlyWhenHijackable(t *testing.T) {
	cases := []struct {
		name     string
		newW     func() http.ResponseWriter
		wantType string
	}{
		{"可 hijack 时包装 | wrapped when hijackable", func() http.ResponseWriter { return newFullWriter() }, "*server.hijackWriter"},
		{"不可 hijack 时透传 | passed through when not hijackable", func() http.ResponseWriter { return newPlainWriter() }, "*server.plainWriter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			underlying := tc.newW()

			var got string
			var gotHijacker bool
			wrapForTest(func(w http.ResponseWriter, _ *http.Request) {
				got = fmt.Sprintf("%T", w)
				_, gotHijacker = w.(http.Hijacker)
			}).ServeHTTP(underlying, &http.Request{})

			if got != tc.wantType {
				t.Errorf("处理器收到的写入器类型 = %s, 期望 %s", got, tc.wantType)
			}
			_, wantHijacker := underlying.(http.Hijacker)
			if gotHijacker != wantHijacker {
				t.Errorf("包装后 Hijack 能力 = %v, 期望与底层一致 = %v", gotHijacker, wantHijacker)
			}
		})
	}
}

// TestWrapperKeepsFlushAndUnwrap 断言包装后仍暴露 Flush 与 Unwrap。
// TestWrapperKeepsFlushAndUnwrap asserts Flush and Unwrap survive the wrapping.
//
// SSE 依赖 Flush；http.ResponseController 依赖 Unwrap。
// SSE depends on Flush; http.ResponseController depends on Unwrap.
func TestWrapperKeepsFlushAndUnwrap(t *testing.T) {
	underlying := newFullWriter()

	wrapForTest(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("包装后的写入器不再实现 http.Flusher，SSE 将无法推送")
		}
		f.Flush()

		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			t.Fatal("包装后的写入器不再暴露 Unwrap，http.ResponseController 将拿不到底层写入器")
		}
		if u.Unwrap() != http.ResponseWriter(underlying) {
			t.Error("Unwrap 返回的不是底层写入器")
		}
	}).ServeHTTP(underlying, &http.Request{})
}

// TestWrapperWithoutFlushStaysSilent 断言底层不可 flush 时调用 Flush 不 panic。
// TestWrapperWithoutFlushStaysSilent asserts calling Flush is a no-op when the underlying writer cannot flush.
func TestWrapperWithoutFlushStaysSilent(t *testing.T) {
	underlying := newHijackable()

	wrapForTest(func(w http.ResponseWriter, _ *http.Request) {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}).ServeHTTP(underlying, &http.Request{})
}

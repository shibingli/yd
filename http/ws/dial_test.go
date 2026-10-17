package ws

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDialHonorsReadBufferSizeWithoutPipelinedBytes pins that a dial with an
// explicit read buffer keeps that size after the handshake; the handshake
// reader is only reused when it actually buffered pipelined bytes.
// | TestDialHonorsReadBufferSizeWithoutPipelinedBytes 固定「显式读缓冲在握手后
// 保持指定大小」：只有握手读取器确实缓存了流水线字节时才复用它。
func TestDialHonorsReadBufferSizeWithoutPipelinedBytes(t *testing.T) {
	upgraded := make(chan error, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, Options{})
		if err != nil {
			upgraded <- err
			return
		}
		defer func() { _ = c.CloseNow() }()
		upgraded <- nil
		<-release
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), DialOptions{ReadBufferSize: 64 << 10})
	if err != nil {
		t.Fatalf("Dial: %v | Dial：%v", err, err)
	}
	defer func() { _ = c.CloseNow() }()
	if err := <-upgraded; err != nil {
		t.Fatalf("server upgrade: %v | 服务端升级：%v", err, err)
	}
	if c.br == nil || c.br.Size() != 64<<10 {
		t.Fatalf("read buffer size = %d, want %d | 读缓冲大小 = %d，期望 %d", readerSize(c.br), 64<<10, readerSize(c.br), 64<<10)
	}
	close(release)
}

// readerSize reports a bufio.Reader size, or zero when absent. | readerSize 返回 bufio.Reader 的大小，缺失时返回 0。
func readerSize(r *bufio.Reader) int {
	if r == nil {
		return 0
	}
	return r.Size()
}

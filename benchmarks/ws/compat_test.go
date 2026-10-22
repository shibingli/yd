package wsbench

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gobwas/ws"
	"github.com/gorilla/websocket"
	"github.com/lxzan/gws"
	ourws "github.com/shibingli/ydx/websocket"
)

type compatCase struct {
	name string
	op   ourws.OpCode
	data []byte
}

func compatCases() []compatCase {
	return []compatCase{
		{"text", ourws.TextMessage, []byte(strings.Repeat("兼容性 compat ", 8))},
		{"binary-1KiB", ourws.BinaryMessage, bytes.Repeat([]byte{0, 1, 2, 3}, 256)},
		{"binary-64KiB", ourws.BinaryMessage, bytes.Repeat([]byte{0x10, 0x20, 0x30, 0x40}, 16<<10)},
	}
}

func ourServer(t *testing.T, comp bool, subs []string) (string, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := ourws.Upgrade(w, r, ourws.Options{EnableCompression: comp, Subprotocols: subs})
		if err != nil {
			t.Errorf("our upgrade: %v", err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			op, p, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(op, p); err != nil {
				return
			}
		}
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http"), srv.Close
}

func coderServer(t *testing.T, comp bool, subs []string) (string, func()) {
	t.Helper()
	mode := coderws.CompressionDisabled
	if comp {
		mode = coderws.CompressionContextTakeover
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := coderws.Accept(w, r, &coderws.AcceptOptions{Subprotocols: subs, CompressionMode: mode})
		if err != nil {
			t.Errorf("coder accept: %v", err)
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(1 << 20)
		for {
			typ, p, err := c.Read(context.Background())
			if err != nil {
				return
			}
			if err := c.Write(context.Background(), typ, p); err != nil {
				return
			}
		}
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http"), srv.Close
}

func gorillaServer(t *testing.T, comp bool, subs []string) (string, func()) {
	t.Helper()
	up := &websocket.Upgrader{Subprotocols: subs, EnableCompression: comp, CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("gorilla upgrade: %v", err)
			return
		}
		defer func() { _ = c.Close() }()
		for {
			mt, p, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(mt, p); err != nil {
				return
			}
		}
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http"), srv.Close
}

func gobwasServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go gobwasEcho(conn)
		}
	}()
	return "ws://" + ln.Addr().String(), func() { _ = ln.Close() }
}

func gobwasEcho(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if _, err := (ws.Upgrader{ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10}).Upgrade(conn); err != nil {
		return
	}
	bw := bufio.NewWriterSize(conn, 64<<10)
	var frag []byte
	var fragOp ws.OpCode
	write := func(op ws.OpCode, p []byte) bool {
		if err := ws.WriteFrame(bw, ws.NewFrame(op, true, p)); err != nil {
			return false
		}
		return bw.Flush() == nil
	}
	for {
		hdr, err := ws.ReadHeader(conn)
		if err != nil {
			return
		}
		p := make([]byte, hdr.Length)
		if _, err := io.ReadFull(conn, p); err != nil {
			return
		}
		if hdr.Masked {
			ws.Cipher(p, hdr.Mask, 0)
		}
		switch hdr.OpCode {
		case ws.OpPing:
			if !write(ws.OpPong, p) {
				return
			}
		case ws.OpClose:
			_ = write(ws.OpClose, p)
			return
		case ws.OpText, ws.OpBinary:
			if hdr.Fin {
				if !write(hdr.OpCode, p) {
					return
				}
				continue
			}
			fragOp, frag = hdr.OpCode, append(frag[:0], p...)
		case ws.OpContinuation:
			frag = append(frag, p...)
			if hdr.Fin && !write(fragOp, frag) {
				return
			}
		}
	}
}

type gwsEchoHandler struct{ gws.BuiltinEventHandler }

func (h *gwsEchoHandler) OnMessage(s *gws.Conn, m *gws.Message) {
	_ = s.WriteMessage(m.Opcode, m.Bytes())
	_ = m.Close()
}
func (h *gwsEchoHandler) OnPing(s *gws.Conn, p []byte) { _ = s.WritePong(p) }

func gwsServer(t *testing.T, comp bool, subs []string) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln = newGateListener(ln)
	opt := &gws.ServerOption{
		SubProtocols: subs, ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10,
		ReadMaxPayloadSize: 16 << 20, CheckUtf8Enabled: false, Logger: nopLogger{},
	}
	if comp {
		opt.PermessageDeflate = gws.PermessageDeflate{Enabled: true, ServerContextTakeover: true}
	}
	srv := gws.NewServer(&gwsEchoHandler{}, opt)
	go func() { _ = srv.RunListener(ln) }()
	return "ws://" + ln.Addr().String(), func() { _ = ln.Close() }
}

type gwsSink struct {
	gws.BuiltinEventHandler
	msgs  chan *gws.Message
	pongs chan []byte
}

func (h *gwsSink) OnMessage(_ *gws.Conn, m *gws.Message) { h.msgs <- m }
func (h *gwsSink) OnPong(_ *gws.Conn, p []byte) {
	select {
	case h.pongs <- p:
	default:
	}
}

func testCoder(t *testing.T, url string, comp bool, sub string) {
	t.Helper()
	mode := coderws.CompressionDisabled
	if comp {
		mode = coderws.CompressionContextTakeover
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := &coderws.DialOptions{CompressionMode: mode}
	if sub != "" {
		opts.Subprotocols = []string{sub}
	}
	c, _, err := coderws.Dial(ctx, url, opts)
	if err != nil {
		t.Fatalf("coder dial: %v", err)
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	if sub != "" && c.Subprotocol() != sub {
		t.Fatalf("coder subprotocol = %q", c.Subprotocol())
	}
	for _, tc := range compatCases() {
		typ := coderws.MessageText
		if tc.op == ourws.BinaryMessage {
			typ = coderws.MessageBinary
		}
		if err := c.Write(ctx, typ, tc.data); err != nil {
			t.Fatalf("coder write %s: %v", tc.name, err)
		}
		gotTyp, got, err := c.Read(ctx)
		if err != nil || gotTyp != typ || !bytes.Equal(got, tc.data) {
			t.Fatalf("coder echo %s: type=%v len=%d err=%v", tc.name, gotTyp, len(got), err)
		}
	}
	// coder's Ping waits for a concurrent Reader to consume the pong. | coder 的 Ping
	// 需要并发的 Reader 读取 pong。
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}()
	if err := c.Ping(ctx); err != nil {
		t.Fatalf("coder ping: %v", err)
	}
	if err := c.Close(coderws.StatusNormalClosure, "bye"); err != nil {
		t.Fatalf("coder close: %v", err)
	}
	<-readDone
}

func testGorilla(t *testing.T, url string, comp bool, sub string) {
	t.Helper()
	d := websocket.Dialer{EnableCompression: comp, ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10}
	var hdr http.Header
	if sub != "" {
		hdr = http.Header{"Sec-WebSocket-Protocol": []string{sub}}
	}
	c, _, err := d.Dial(url, hdr)
	if err != nil {
		t.Fatalf("gorilla dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	if sub != "" && c.Subprotocol() != sub {
		t.Fatalf("gorilla subprotocol = %q", c.Subprotocol())
	}
	for _, tc := range compatCases() {
		mt := websocket.TextMessage
		if tc.op == ourws.BinaryMessage {
			mt = websocket.BinaryMessage
		}
		if err := c.WriteMessage(mt, tc.data); err != nil {
			t.Fatalf("gorilla write %s: %v", tc.name, err)
		}
		gotTyp, got, err := c.ReadMessage()
		if err != nil || gotTyp != mt || !bytes.Equal(got, tc.data) {
			t.Fatalf("gorilla echo %s: type=%d len=%d err=%v", tc.name, gotTyp, len(got), err)
		}
	}
	pong := make(chan struct{}, 1)
	c.SetPongHandler(func(string) error { pong <- struct{}{}; return nil })
	if err := c.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("gorilla ping: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, _ = c.ReadMessage()
	select {
	case <-pong:
	case <-time.After(time.Second):
		t.Fatalf("gorilla pong not received")
	}
}

func gobwasWrite(bw *bufio.Writer, op ws.OpCode, fin bool, p []byte) error {
	buf := append([]byte(nil), p...)
	frame := ws.MaskFrameInPlaceWith(ws.NewFrame(op, fin, buf), [4]byte{1, 2, 3, 4})
	if err := ws.WriteFrame(bw, frame); err != nil {
		return err
	}
	return bw.Flush()
}

func gobwasRead(rd io.Reader) (ws.OpCode, []byte, error) {
	hdr, err := ws.ReadHeader(rd)
	if err != nil {
		return 0, nil, err
	}
	p := make([]byte, hdr.Length)
	if _, err := io.ReadFull(rd, p); err != nil {
		return 0, nil, err
	}
	if hdr.Masked {
		ws.Cipher(p, hdr.Mask, 0)
	}
	return hdr.OpCode, p, nil
}

func testGobwas(t *testing.T, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, br, _, err := ws.Dial(ctx, url)
	if err != nil {
		t.Fatalf("gobwas dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	var rd io.Reader = conn
	if br != nil {
		rd = br
	}
	bw := bufio.NewWriterSize(conn, 64<<10)
	for _, tc := range compatCases() {
		op := ws.OpText
		if tc.op == ourws.BinaryMessage {
			op = ws.OpBinary
		}
		if err := gobwasWrite(bw, op, true, tc.data); err != nil {
			t.Fatalf("gobwas write %s: %v", tc.name, err)
		}
		gotOp, got, err := gobwasRead(rd)
		if err != nil || gotOp != op || !bytes.Equal(got, tc.data) {
			t.Fatalf("gobwas echo %s: op=%v len=%d err=%v", tc.name, gotOp, len(got), err)
		}
	}
	if err := gobwasWrite(bw, ws.OpText, false, []byte("frag")); err != nil {
		t.Fatalf("gobwas frag start: %v", err)
	}
	if err := gobwasWrite(bw, ws.OpContinuation, true, []byte("ment")); err != nil {
		t.Fatalf("gobwas frag end: %v", err)
	}
	gotOp, got, err := gobwasRead(rd)
	if err != nil || gotOp != ws.OpText || string(got) != "fragment" {
		t.Fatalf("gobwas fragment echo: op=%v payload=%q err=%v", gotOp, got, err)
	}
	if err := gobwasWrite(bw, ws.OpPing, true, []byte("ping")); err != nil {
		t.Fatalf("gobwas ping: %v", err)
	}
	gotOp, got, err = gobwasRead(rd)
	if err != nil || gotOp != ws.OpPong || string(got) != "ping" {
		t.Fatalf("gobwas pong: op=%v payload=%q err=%v", gotOp, got, err)
	}
	if err := gobwasWrite(bw, ws.OpClose, true, []byte{0x03, 0xE8}); err != nil {
		t.Fatalf("gobwas close: %v", err)
	}
	gotOp, _, err = gobwasRead(rd)
	if err != nil || gotOp != ws.OpClose {
		t.Fatalf("gobwas close echo: op=%v err=%v", gotOp, err)
	}
}

func testGWS(t *testing.T, url string, comp bool) {
	t.Helper()
	h := &gwsSink{msgs: make(chan *gws.Message, 4), pongs: make(chan []byte, 1)}
	opt := &gws.ClientOption{
		Addr: url, ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10,
		ReadMaxPayloadSize: 16 << 20, CheckUtf8Enabled: false, Logger: nopLogger{},
	}
	if comp {
		opt.PermessageDeflate = gws.PermessageDeflate{Enabled: true, ClientContextTakeover: true}
	}
	c, _, err := gws.NewClient(h, opt)
	if err != nil {
		t.Fatalf("gws dial: %v", err)
	}
	readDone := make(chan struct{})
	go func() { c.ReadLoop(); close(readDone) }()
	defer func() {
		_ = c.NetConn().Close()
		select {
		case <-readDone:
		case <-time.After(2 * time.Second):
		}
	}()
	for _, tc := range compatCases() {
		op := gws.OpcodeText
		if tc.op == ourws.BinaryMessage {
			op = gws.OpcodeBinary
		}
		if err := c.WriteMessage(op, tc.data); err != nil {
			t.Fatalf("gws write %s: %v", tc.name, err)
		}
		select {
		case m := <-h.msgs:
			n := len(m.Bytes())
			ok := m.Opcode == op && bytes.Equal(m.Bytes(), tc.data)
			_ = m.Close()
			if !ok {
				t.Fatalf("gws echo %s: len=%d", tc.name, n)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("gws echo %s timed out", tc.name)
		}
	}
	if err := c.WritePing([]byte("ping")); err != nil {
		t.Fatalf("gws ping: %v", err)
	}
	select {
	case <-h.pongs:
	case <-time.After(time.Second):
		t.Fatalf("gws pong not received")
	}
	_ = c.WriteClose(1000, []byte("bye"))
}

func testOur(t *testing.T, url string, comp bool, sub string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := ourws.DialOptions{EnableWriteCompression: comp, ReadBufferSize: 64 << 10, HandshakeTimeout: 5 * time.Second}
	if sub != "" {
		opts.Subprotocols = []string{sub}
	}
	c, err := ourws.Dial(ctx, url, opts)
	if err != nil {
		t.Fatalf("our dial: %v", err)
	}
	if sub != "" && c.Subprotocol() != sub {
		_ = c.CloseNow()
		t.Fatalf("our subprotocol = %q", c.Subprotocol())
	}
	for _, tc := range compatCases() {
		if err := c.WriteMessage(tc.op, tc.data); err != nil {
			_ = c.CloseNow()
			t.Fatalf("our write %s: %v", tc.name, err)
		}
		op, got, err := c.ReadMessage()
		if err != nil || op != tc.op || !bytes.Equal(got, tc.data) {
			_ = c.CloseNow()
			t.Fatalf("our echo %s: op=%v len=%d err=%v", tc.name, op, len(got), err)
		}
	}
	pong := make(chan struct{}, 1)
	c.SetPongHandler(func(string) error { pong <- struct{}{}; return nil })
	readDone := make(chan struct{})
	go func() { _, _, _ = c.ReadMessage(); close(readDone) }()
	if err := c.WriteControl(ourws.PingMessage, []byte("ping"), time.Now().Add(time.Second)); err != nil {
		_ = c.CloseNow()
		t.Fatalf("our ping: %v", err)
	}
	select {
	case <-pong:
	case <-time.After(time.Second):
		_ = c.CloseNow()
		t.Fatalf("our pong not received")
	}
	_ = c.CloseNow()
	<-readDone

	c2, err := ourws.Dial(ctx, url, opts)
	if err != nil {
		t.Fatalf("our dial (close): %v", err)
	}
	if err := c2.Close(ourws.CloseNormalClosure, "bye"); err != nil {
		_ = c2.CloseNow()
		t.Fatalf("our close: %v", err)
	}
}

func TestCompatOurServer(t *testing.T) {
	url, stop := ourServer(t, false, []string{"yd.v1"})
	defer stop()
	t.Run("coder", func(t *testing.T) { testCoder(t, url, false, "yd.v1") })
	t.Run("gorilla", func(t *testing.T) { testGorilla(t, url, false, "yd.v1") })
	t.Run("gobwas", func(t *testing.T) { testGobwas(t, url) })
	t.Run("gws", func(t *testing.T) { testGWS(t, url, false) })

	compURL, compStop := ourServer(t, true, nil)
	defer compStop()
	t.Run("compression-coder", func(t *testing.T) { testCoder(t, compURL, true, "") })
	t.Run("compression-gorilla", func(t *testing.T) { testGorilla(t, compURL, true, "") })
	t.Run("compression-gws", func(t *testing.T) { testGWS(t, compURL, true) })
}

func TestCompatOurClient(t *testing.T) {
	t.Run("coder", func(t *testing.T) {
		url, stop := coderServer(t, false, []string{"yd.v1"})
		defer stop()
		testOur(t, url, false, "yd.v1")
	})
	t.Run("gorilla", func(t *testing.T) {
		url, stop := gorillaServer(t, false, []string{"yd.v1"})
		defer stop()
		testOur(t, url, false, "yd.v1")
	})
	t.Run("gobwas", func(t *testing.T) {
		url, stop := gobwasServer(t)
		defer stop()
		testOur(t, url, false, "")
	})
	t.Run("gws", func(t *testing.T) {
		url, stop := gwsServer(t, false, nil)
		defer stop()
		testOur(t, url, false, "")
	})
	t.Run("compression-coder", func(t *testing.T) {
		url, stop := coderServer(t, true, nil)
		defer stop()
		testOur(t, url, true, "")
	})
	t.Run("compression-gorilla", func(t *testing.T) {
		url, stop := gorillaServer(t, true, nil)
		defer stop()
		testOur(t, url, true, "")
	})
	t.Run("compression-gws", func(t *testing.T) {
		url, stop := gwsServer(t, true, nil)
		defer stop()
		testOur(t, url, true, "")
	})
}

// TestCompatCompressionPayload checks a compressible 4 KiB payload through the
// coder pair in both directions. | TestCompatCompressionPayload 用 4 KiB 可压缩载荷
// 双向检查 coder 组合。
func TestCompatCompressionPayload(t *testing.T) {
	unit := `{"id":12345,"name":"bench-user","active":true,"score":98.5},`
	payload := bytes.Repeat([]byte(unit), 4<<10/len(unit)+1)[:4<<10]
	for _, comp := range []bool{false, true} {
		t.Run(fmt.Sprintf("comp=%v/our-server", comp), func(t *testing.T) {
			url, stop := ourServer(t, comp, nil)
			defer stop()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			mode := coderws.CompressionDisabled
			if comp {
				mode = coderws.CompressionContextTakeover
			}
			c, _, err := coderws.Dial(ctx, url, &coderws.DialOptions{CompressionMode: mode})
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.CloseNow()
			c.SetReadLimit(1 << 20)
			if err := c.Write(ctx, coderws.MessageBinary, payload); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, got, err := c.Read(ctx)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("echo: err=%v len=%d", err, len(got))
			}
			_ = c.Close(coderws.StatusNormalClosure, "")
		})
		t.Run(fmt.Sprintf("comp=%v/our-client", comp), func(t *testing.T) {
			url, stop := coderServer(t, comp, nil)
			defer stop()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := ourws.Dial(ctx, url, ourws.DialOptions{EnableWriteCompression: comp, ReadBufferSize: 64 << 10})
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer func() { _ = c.CloseNow() }()
			if err := c.WriteMessage(ourws.BinaryMessage, payload); err != nil {
				t.Fatalf("write: %v", err)
			}
			_, got, err := c.ReadMessage()
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("echo: err=%v len=%d", err, len(got))
			}
			_ = c.Close(ourws.CloseNormalClosure, "")
		})
	}
}

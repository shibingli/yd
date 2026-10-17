package ws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shibingli/yd/jsonx"
	"github.com/shibingli/yd/router"
)

// ---------------------------------------------------------------- Test scaffolding | 测试脚手架

// htmxRecord collects the callbacks reported by one htmx endpoint. | htmxRecord 收集单个 htmx 端点回调的结果。
//
// All channels are buffered, so an endpoint never blocks on a test that has stopped reading. | 所有通道都带缓冲，端点不会因测试停止读取而阻塞。
type htmxRecord struct {
	opened   chan struct{}
	messages chan HtmxMessage
	closed   chan error
}

// newHtmxRecord creates an empty record. | newHtmxRecord 创建一个空记录。
func newHtmxRecord() *htmxRecord {
	return &htmxRecord{
		opened:   make(chan struct{}, 8),
		messages: make(chan HtmxMessage, 8),
		closed:   make(chan error, 8),
	}
}

// htmxHarness is a router-backed server exposing the endpoints used by the tests. | htmxHarness 是测试使用的、基于 router 的端点服务端。
type htmxHarness struct {
	srv    *httptest.Server
	echo   *htmxRecord
	chat   *htmxRecord
	strict *htmxRecord
}

// newHtmxHarness starts the three endpoints: /echo (OnMessage nil, so the handler echoes Body), | newHtmxHarness 启动三个端点：/echo（OnMessage 为 nil，处理器回显 Body）、
// /chat (full callbacks plus a subprotocol) and /strict (an OnClose observer for protocol errors). | /chat（回调齐全并声明子协议）与 /strict（只观察协议错误时的 OnClose）。
func newHtmxHarness(t *testing.T) *htmxHarness {
	t.Helper()
	h := &htmxHarness{srv: nil, echo: newHtmxRecord(), chat: newHtmxRecord(), strict: newHtmxRecord()}

	r := router.New()
	r.GET("/echo", HtmxHandler(HtmxOptions{}))
	r.GET("/chat", HtmxHandler(HtmxOptions{
		OnOpen: func(*Conn) { h.chat.opened <- struct{}{} },
		OnMessage: func(c *Conn, m HtmxMessage) {
			h.chat.messages <- m
			// write a fragment from the callback to prove WriteHtmxFragment works on a live endpoint | 回调里写出片段，验证 WriteHtmxFragment 在真实端点上可用
			_ = c.WriteHtmxFragment("#chat", "beforeend", "<p>"+m.Body+"</p>")
		},
		OnClose:      func(_ *Conn, err error) { h.chat.closed <- err },
		Subprotocols: []string{"htmx"},
	}))
	r.GET("/strict", HtmxHandler(HtmxOptions{
		OnClose:      func(_ *Conn, err error) { h.strict.closed <- err },
		Subprotocols: []string{"htmx"},
	}))

	h.srv = httptest.NewServer(r)
	t.Cleanup(h.srv.Close)
	return h
}

// dial opens a client connection to a path of the harness. | dial 打开到该测试服务端指定路径的客户端连接。
func (h *htmxHarness) dial(t *testing.T, path string, subs ...string) *Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, wsURL(t, h.srv, path), DialOptions{Subprotocols: subs})
	if err != nil {
		t.Fatalf("Dial %s 失败: %v", path, err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

// readOrFail reads one message with a deadline, failing the test on timeout or error. | readOrFail 带超时读取一条消息，超时或出错即判定测试失败。
func readOrFail(t *testing.T, c *Conn, what string) (OpCode, []byte) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	op, data, err := c.ReadMessage()
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", what, err)
	}
	return op, data
}

// ---------------------------------------------------------------- Wire format | 线格式

func TestHtmxFragmentWireFormat(t *testing.T) {
	cases := []struct {
		name   string
		target string
		swap   string
		body   string
		want   string
	}{
		{
			name:   "目标与交换策略齐全",
			target: "#messages",
			swap:   "beforeend",
			body:   "<p>你好 | hello</p>",
			want:   `<hx-partial hx-target="#messages" hx-swap="beforeend"><p>你好 | hello</p></hx-partial>`,
		},
		{
			name:   "属性值转义而正文原样输出",
			target: `#a"b&<c>'d`,
			swap:   `swap"&<`,
			body:   `<b class="x">t</b>`,
			want:   `<hx-partial hx-target="#a&#34;b&amp;&lt;c&gt;&#39;d" hx-swap="swap&#34;&amp;&lt;"><b class="x">t</b></hx-partial>`,
		},
		{
			name:   "省略 target",
			target: "",
			swap:   "innerMorph",
			body:   "x",
			want:   `<hx-partial hx-swap="innerMorph">x</hx-partial>`,
		},
		{
			name:   "省略 swap",
			target: "#log",
			swap:   "",
			body:   "x",
			want:   `<hx-partial hx-target="#log">x</hx-partial>`,
		},
		{
			name:   "两者皆省且正文为空",
			target: "",
			swap:   "",
			body:   "",
			want:   `<hx-partial></hx-partial>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cli := connPair(t, Options{})
			if err := srv.WriteHtmxFragment(tc.target, tc.swap, tc.body); err != nil {
				t.Fatalf("WriteHtmxFragment 失败: %v", err)
			}
			_ = cli.SetReadDeadline(time.Now().Add(3 * time.Second))
			op, got, err := cli.ReadMessage()
			if err != nil {
				t.Fatalf("读取片段失败: %v", err)
			}
			if op != TextMessage {
				t.Fatalf("操作码 = %v, 期望 TextMessage", op)
			}
			if string(got) != tc.want {
				t.Fatalf("线格式不一致:\n实际 = %s\n期望 = %s", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------- Endpoint behaviour | 端点行为

func TestHtmxHandlerEchoAndCallbacks(t *testing.T) {
	h := newHtmxHarness(t)

	t.Run("OnMessage 为 nil 时回显 BODY", func(t *testing.T) {
		c := h.dial(t, "/echo")

		// the request is a raw JSON literal, independently verifying the HEADERS / BODY field names | 请求直接用 JSON 字面量构造，独立校验 HEADERS / BODY 字段名
		const in = `{"HEADERS":{"hx-target":"#log","hx-swap":"beforeend"},"BODY":"raw json body | 原始 JSON 正文"}`
		if err := c.WriteMessage(TextMessage, []byte(in)); err != nil {
			t.Fatalf("写入信封失败: %v", err)
		}

		op, got := readOrFail(t, c, "回显")
		if op != TextMessage {
			t.Fatalf("操作码 = %v, 期望 TextMessage", op)
		}
		if !jsonx.Valid(got) {
			t.Fatalf("回显不是合法 JSON: %s", got)
		}

		var echo HtmxMessage
		if err := jsonx.Unmarshal(got, &echo); err != nil {
			t.Fatalf("回显解码失败: %v", err)
		}
		if echo.Body != "raw json body | 原始 JSON 正文" {
			t.Fatalf("回显 BODY = %q, 期望 %q", echo.Body, "raw json body | 原始 JSON 正文")
		}
		// the request-side HEADERS must not be echoed back as response directives | 请求侧的 HEADERS 不应被当作响应指令回送
		var shape map[string]any
		if err := jsonx.Unmarshal(got, &shape); err != nil {
			t.Fatalf("回显形状解析失败: %v", err)
		}
		if _, ok := shape["HEADERS"]; ok {
			t.Fatalf("回显不应携带 HEADERS: %s", got)
		}
		if _, ok := shape["BODY"]; !ok {
			t.Fatalf("回显必须携带 BODY: %s", got)
		}

		// a second message on the same connection proves the loop keeps running | 同一连接上继续第二条，证明读循环持续工作
		if err := c.WriteMessage(TextMessage, []byte(`{"HEADERS":{},"BODY":"second | 第二条"}`)); err != nil {
			t.Fatalf("写入第二条失败: %v", err)
		}
		_, got = readOrFail(t, c, "第二条回显")
		if err := jsonx.Unmarshal(got, &echo); err != nil {
			t.Fatalf("第二条回显解码失败: %v", err)
		}
		if echo.Body != "second | 第二条" {
			t.Fatalf("第二条回显 BODY = %q", echo.Body)
		}
	})

	t.Run("回调顺序与子协议", func(t *testing.T) {
		c := h.dial(t, "/chat", "htmx", "other")
		if got := c.Subprotocol(); got != "htmx" {
			t.Fatalf("子协议 = %q, 期望 htmx", got)
		}
		recvOrTimeout(t, h.chat.opened, 3*time.Second, "OnOpen 回调")

		for i := 0; i < 3; i++ {
			body := "hi-" + string(rune('0'+i))
			raw, err := jsonx.Marshal(HtmxMessage{
				Headers: map[string]string{"hx-target": "#chat", "HX-Source": "test"},
				Body:    body,
			})
			if err != nil {
				t.Fatalf("编码信封失败: %v", err)
			}
			if err := c.WriteMessage(TextMessage, raw); err != nil {
				t.Fatalf("写入信封 %d 失败: %v", i, err)
			}

			msg := recvOrTimeout(t, h.chat.messages, 3*time.Second, "OnMessage 回调")
			if msg.Body != body {
				t.Fatalf("OnMessage 第 %d 条 BODY = %q, 期望 %q", i, msg.Body, body)
			}
			if msg.Headers["hx-target"] != "#chat" || msg.Headers["HX-Source"] != "test" {
				t.Fatalf("OnMessage 第 %d 条 HEADERS = %v", i, msg.Headers)
			}

			// the fragment written from the callback must arrive byte for byte | 回调里写出的片段必须逐字节到达
			op, got := readOrFail(t, c, "回调片段")
			want := `<hx-partial hx-target="#chat" hx-swap="beforeend"><p>` + body + `</p></hx-partial>`
			if op != TextMessage || string(got) != want {
				t.Fatalf("片段第 %d 条不一致: 类型 %v, 实际 %s, 期望 %s", i, op, got, want)
			}
		}

		// after the client closes, OnClose must receive the peer's close code and reason | 客户端发起关闭后 OnClose 必须收到对端的关闭码与原因
		_ = c.Close(CloseNormalClosure, "client done | 客户端结束")
		closeErr := recvOrTimeout(t, h.chat.closed, 3*time.Second, "OnClose 回调")
		var ce *CloseError
		if !errors.As(closeErr, &ce) {
			t.Fatalf("OnClose 错误 = %v, 期望 *CloseError", closeErr)
		}
		if ce.Code != CloseNormalClosure || ce.Text != "client done | 客户端结束" {
			t.Fatalf("OnClose 关闭 = (%d, %q), 期望 (%d, %q)", ce.Code, ce.Text, CloseNormalClosure, "client done | 客户端结束")
		}
	})

	t.Run("非 JSON 文本以 1003 关闭", func(t *testing.T) {
		c := h.dial(t, "/strict", "htmx")
		if err := c.WriteMessage(TextMessage, []byte("this is not json | 这不是 JSON")); err != nil {
			t.Fatalf("写入非 JSON 文本失败: %v", err)
		}

		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _, err := c.ReadMessage()
		var ce *CloseError
		if !errors.As(err, &ce) {
			t.Fatalf("客户端错误 = %v, 期望 *CloseError", err)
		}
		if ce.Code != CloseUnsupportedData {
			t.Fatalf("关闭码 = %d, 期望 %d（不支持的数据类型）", ce.Code, CloseUnsupportedData)
		}
		if ce.Text == "" {
			t.Fatal("the close reason must not be empty | 关闭原因不应为空")
		}

		closeErr := recvOrTimeout(t, h.strict.closed, 3*time.Second, "OnClose 回调")
		if !errors.Is(closeErr, ErrHtmxDecode) {
			t.Fatalf("OnClose 错误 = %v, 期望 ErrHtmxDecode", closeErr)
		}
	})

	t.Run("二进制消息以 1003 关闭", func(t *testing.T) {
		c := h.dial(t, "/strict", "htmx")
		if err := c.WriteMessage(BinaryMessage, []byte{0x01, 0x02}); err != nil {
			t.Fatalf("写入二进制消息失败: %v", err)
		}

		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _, err := c.ReadMessage()
		var ce *CloseError
		if !errors.As(err, &ce) {
			t.Fatalf("客户端错误 = %v, 期望 *CloseError", err)
		}
		if ce.Code != CloseUnsupportedData {
			t.Fatalf("关闭码 = %d, 期望 %d（不支持的数据类型）", ce.Code, CloseUnsupportedData)
		}

		closeErr := recvOrTimeout(t, h.strict.closed, 3*time.Second, "OnClose 回调")
		if !errors.Is(closeErr, ErrHtmxMessageType) {
			t.Fatalf("OnClose 错误 = %v, 期望 ErrHtmxMessageType", closeErr)
		}
	})

	t.Run("非升级请求被拒绝", func(t *testing.T) {
		resp, err := http.Get(h.srv.URL + "/chat")
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("状态码 = %d, 期望 %d", resp.StatusCode, http.StatusBadRequest)
		}
		select {
		case <-h.chat.opened:
			t.Fatal("OnOpen must not fire when the upgrade fails | 升级失败时不得触发 OnOpen")
		default:
		}
	})
}

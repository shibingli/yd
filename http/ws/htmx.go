package ws

import (
	stdhtml "html"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/jsonx"
	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/http/router"
)

// ---------------------------------------------------------------- Errors | 错误

// Error variables of the htmx integration. | htmx 集成层的错误变量。
var (
	// ErrHtmxEncode indicates that an htmx envelope could not be encoded as JSON. | ErrHtmxEncode 表示 htmx 信封无法编码为 JSON。
	ErrHtmxEncode = errx.New("ws: failed to encode htmx message | ws：htmx 消息编码失败")
	// ErrHtmxDecode indicates that a received text message is not a valid htmx JSON envelope. | ErrHtmxDecode 表示收到的文本消息不是合法的 htmx JSON 信封。
	ErrHtmxDecode = errx.New("ws: failed to decode htmx message | ws：htmx 消息解码失败")
	// ErrHtmxWrite indicates that writing an htmx fragment to the connection failed. | ErrHtmxWrite 表示把 htmx 片段写入连接失败。
	ErrHtmxWrite = errx.New("ws: failed to write htmx fragment | ws：htmx 片段写出失败")
	// ErrHtmxMessageType indicates that an htmx endpoint received a non-text message. | ErrHtmxMessageType 表示 htmx 端点收到了非文本消息。
	ErrHtmxMessageType = errx.New("ws: htmx endpoint received a non-text message | ws：htmx 端点收到非文本消息")
)

// Close reasons used by HtmxHandler. | HtmxHandler 使用的关闭原因。
const (
	// closeReasonHtmxType is sent when a message other than text arrives. | closeReasonHtmxType 在收到非文本消息时发送。
	closeReasonHtmxType = "htmx endpoint accepts text messages only | htmx 端点仅接受文本消息"
	// closeReasonHtmxJSON is sent when text is not a valid htmx envelope. | closeReasonHtmxJSON 在文本不是合法 htmx 信封时发送。
	closeReasonHtmxJSON = "invalid htmx message | 非法的 htmx 消息"
	// closeReasonHtmxEncode is sent when the echo envelope cannot be encoded. | closeReasonHtmxEncode 在回显信封无法编码时发送。
	closeReasonHtmxEncode = "failed to encode htmx message | htmx 消息编码失败"
)

// ---------------------------------------------------------------- Messages | 消息

// HtmxMessage is one message exchanged by the htmx `hx-ws` extension. | HtmxMessage 是 htmx `hx-ws` 扩展收发的一条消息。
//
// htmx 4 sends a flat JSON object: every form value is a top-level key and the per-message request
// headers live under "headers", for example {"message":"hi","headers":{...}}. Older releases sent
// {"HEADERS":{...},"BODY":"..."}; both shapes are accepted on input. On output the extension reads
// "content" as the fragment to swap, so the built-in echo replies with {"content":"…"}. | htmx 4 发送
// 扁平 JSON 对象：每个表单字段都是顶层键，逐条消息的请求头放在 "headers" 下，例如
// {"message":"hi","headers":{...}}。旧版本发送 {"HEADERS":{...},"BODY":"..."}；输入时两种形态都接受。
// 输出方向扩展读取 "content" 作为要交换的片段，因此内建回显以 {"content":"…"} 应答。
//
// Both directions are encoded with the framework's adaptive JSON layer (jsonx), never with encoding/json directly. | 两个方向都使用框架自适应 JSON 层（jsonx）编解码，绝不直接使用 encoding/json。
type HtmxMessage struct {
	// Headers carries the per-message request headers, sent under "headers" by htmx 4 and under "HEADERS" by older releases. | Headers 承载逐条消息的请求头：htmx 4 放在 "headers"，旧版本放在 "HEADERS"。
	Headers map[string]string `json:"headers,omitempty"`
	// Body is the text payload; htmx 4 has no dedicated field, so it is derived from the single value or the conventional message/body/text field. | Body 是文本载荷；htmx 4 没有专门字段，因此由唯一值或约定的 message/body/text 字段推导。
	Body string `json:"BODY,omitempty"`
	// Content is the response fragment the htmx 4 extension swaps into its target; it is only set on replies. | Content 是 htmx 4 扩展交换进目标元素的响应片段，只在应答中设置。
	Content string `json:"content,omitempty"`
	// Values holds every non-header field of an inbound htmx 4 message, keyed by form field name. | Values 以表单字段名为键，保存入站 htmx 4 消息中除请求头外的全部字段。
	Values map[string]any `json:"-"`
}

// UnmarshalJSON accepts both the htmx 4 flat envelope and the legacy HEADERS/BODY envelope. | UnmarshalJSON
// 同时接受 htmx 4 的扁平信封与旧的 HEADERS/BODY 信封。
func (m *HtmxMessage) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := jsonx.Unmarshal(data, &raw); err != nil {
		return err
	}
	*m = HtmxMessage{Values: make(map[string]any, len(raw))}
	for key, value := range raw {
		switch {
		case strings.EqualFold(key, "headers"):
			m.Headers = htmxHeaderStrings(value, m.Headers)
		case strings.EqualFold(key, "BODY"), strings.EqualFold(key, "content"):
			m.Body = htmxScalarString(value)
		default:
			m.Values[key] = value
		}
	}
	if m.Body != "" {
		return nil
	}
	// htmx 4 has no body field: a single form value is the body, and a multi-field form falls back to
	// the conventional names in order. | htmx 4 没有正文字段：只有一个表单值时它就是正文，多字段表单按
	// 约定名称依次回退。
	if len(m.Values) == 1 {
		for _, value := range m.Values {
			m.Body = htmxScalarString(value)
		}
		return nil
	}
	for _, key := range []string{"message", "body", "text"} {
		if value, ok := m.Values[key]; ok {
			m.Body = htmxScalarString(value)
			return nil
		}
	}
	return nil
}

// htmxHeaderStrings converts the headers object of an inbound message to strings. | htmxHeaderStrings 把入站消息的请求头对象转换为字符串映射。
//
// htmx sends strings today, but a client is free to send numbers or arrays; converting them keeps the
// handler from failing the whole message over one header. | htmx 目前发送字符串，但客户端也可能发送数字或数组；
// 逐个转换可避免整条消息因为一个请求头而失败。
func htmxHeaderStrings(value any, dst map[string]string) map[string]string {
	src, ok := value.(map[string]any)
	if !ok {
		return dst
	}
	if dst == nil {
		dst = make(map[string]string, len(src))
	}
	for key, entry := range src {
		dst[key] = htmxScalarString(entry)
	}
	return dst
}

// htmxScalarString renders one JSON value as text for the echo path. | htmxScalarString 把单个 JSON 值渲染为文本，供回显路径使用。
func htmxScalarString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return ""
	}
	encoded, err := jsonx.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// ---------------------------------------------------------------- Endpoint | 端点

// HtmxOptions configures HtmxHandler. | HtmxOptions 配置 HtmxHandler。
type HtmxOptions struct {
	// OnOpen runs once after the handshake succeeded and before the read loop starts; it may be nil. | OnOpen 在握手成功之后、读循环开始之前调用一次；可为 nil。
	OnOpen func(*Conn)
	// OnMessage runs for every successfully decoded htmx message; when it is nil the handler echoes Body back. | OnMessage 对每条解码成功的 htmx 消息调用；为 nil 时处理器回显 Body。
	OnMessage func(*Conn, HtmxMessage)
	// OnClose runs once after the read loop ends (peer close frame, protocol error or read failure) and before the underlying | OnClose 在读循环结束（对端关闭帧、协议错误或读取失败）后调用一次，且在底层
	// connection is closed; it may be nil. The error is *CloseError for a peer close frame and ErrUnexpectedEOF for a dropped connection. | 连接关闭之前；可为 nil。对端关闭帧对应 *CloseError，连接异常断开对应 ErrUnexpectedEOF。
	OnClose func(*Conn, error)
	// Subprotocols are the subprotocols this endpoint advertises (for example "htmx"); | Subprotocols 是本端点声明的子协议（例如 "htmx"）；
	// when non-empty they take precedence over Options.Subprotocols. | 非空时优先于 Options.Subprotocols。
	Subprotocols []string
	// Options holds the underlying upgrade parameters (buffer sizes, per-message limit, Origin check, …). | Options 是底层升级参数（缓冲大小、单条消息上限、Origin 校验等）。
	Options Options
}

// HtmxHandler returns an out-of-the-box htmx WebSocket endpoint. | HtmxHandler 返回开箱即用的 htmx WebSocket 端点。
//
// The request is upgraded with o.Options (o.Subprotocols winning when non-empty); then o.OnOpen is called once, | 请求以 o.Options 升级（o.Subprotocols 非空时优先）；随后调用一次 o.OnOpen，
// every text message is decoded as an htmx JSON envelope and handed to o.OnMessage, and when o.OnMessage is nil | 每条文本消息都被解码为 htmx JSON 信封并交给 o.OnMessage，而 o.OnMessage 为 nil 时
// the handler echoes Body back as {"content":"…"}, the shape the htmx 4 extension swaps into its target | 处理器把 Body 以 {"content":"…"} 回显，这正是 htmx 4 扩展交换进目标元素的形态
// (the request-side Headers are deliberately not echoed, they are requests rather than response directives). | （请求侧的 Headers 刻意不回送，它们是请求指令而非响应指令）。
//
// Non-text messages and text that is not a valid envelope are protocol errors: the connection is closed with 1003 | 非文本消息、以及不是合法信封的文本都属于协议错误：连接以 1003（不支持的数据类型）
// (unsupported data) and the loop ends. o.OnClose observes the terminating error in every case and runs before the | 关闭并结束循环。任何情况下 o.OnClose 都能观察到终止错误，并在底层连接关闭之前执行。
// underlying connection is closed.
//
// Upgrade failures (a plain GET, a rejected Origin, an HTTP/2 request, …) have already produced the HTTP error | 升级失败（普通 GET、Origin 被拒、HTTP/2 请求等）时 Upgrade 已经写出 HTTP 错误响应；
// response through Upgrade; the failure is logged with logx and the handler returns. | 处理器用 logx 记录该失败后返回。
//
// This is a generic factory: concrete routes are registered by the application, so the OpenAPI annotation block | 本函数是通用工厂：具体路由由应用注册，因此 OpenAPI 注解块写在注册处。
// belongs at the registration site.
func HtmxHandler(o HtmxOptions) router.HandlerFunc {
	opts := o.Options
	if len(o.Subprotocols) > 0 {
		opts.Subprotocols = o.Subprotocols
	}
	return func(c *router.Ctx) {
		conn, err := Upgrade(c.Writer, c.Request, opts)
		if err != nil {
			// Upgrade has already written the HTTP error response; only an observable log record is added here | Upgrade 已写出 HTTP 错误响应，这里只补一条可观测日志
			logx.Default().Warn("ws: htmx endpoint upgrade failed | ws：htmx 端点升级失败",
				logx.Str("path", c.Path()), logx.Err(err))
			return
		}
		// the connection is unusable once the loop ends, so the underlying TCP is always closed on exit | 读循环结束后连接不再可用，统一在退出时关闭底层 TCP
		defer func() { _ = conn.CloseNow() }()

		loopErr := serveHtmx(conn, o)
		if o.OnClose != nil {
			o.OnClose(conn, loopErr)
		}
	}
}

// serveHtmx runs the htmx read loop and returns the error that ended it. | serveHtmx 运行 htmx 读循环并返回结束它的错误。
func serveHtmx(conn *Conn, o HtmxOptions) error {
	if o.OnOpen != nil {
		o.OnOpen(conn)
	}
	for {
		op, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if op != TextMessage {
			// htmx exchanges text envelopes only; any other type is 1003 unsupported data | htmx 只交换文本信封，其它类型按 1003 判定为不支持的数据
			_ = conn.Close(CloseUnsupportedData, closeReasonHtmxType)
			return ErrHtmxMessageType
		}

		var msg HtmxMessage
		if err := jsonx.Unmarshal(data, &msg); err != nil {
			_ = conn.Close(CloseUnsupportedData, closeReasonHtmxJSON)
			return errx.Wrap(err, ErrHtmxDecode)
		}

		if o.OnMessage != nil {
			o.OnMessage(conn, msg)
			continue
		}

		// htmx 4 reads "content" from the reply; a BODY field would be parsed but never swapped. | htmx 4 从应答中读取
		// "content"；BODY 字段虽然能被解析，却永远不会被交换。
		echo, err := encodeHtmxMessage(HtmxMessage{Content: msg.Body})
		if err != nil {
			_ = conn.Close(CloseInternalServerErr, closeReasonHtmxEncode)
			return err
		}
		if err := conn.WriteMessage(TextMessage, echo); err != nil {
			return errx.Wrap(err, ErrHtmxWrite)
		}
	}
}

// encodeHtmxMessage encodes an htmx envelope with the framework's adaptive JSON layer. | encodeHtmxMessage 用框架自适应 JSON 层编码 htmx 信封。
func encodeHtmxMessage(m HtmxMessage) ([]byte, error) {
	b, err := jsonx.Marshal(m)
	if err != nil {
		return nil, errx.Wrap(err, ErrHtmxEncode)
	}
	return b, nil
}

// ---------------------------------------------------------------- Fragments | 片段

// WriteHtmxFragment writes one Htmx 4 <hx-partial> fragment as a text message. | WriteHtmxFragment 以一条文本消息写出一个 Htmx 4 的 <hx-partial> 片段。
//
// The wire form is <hx-partial hx-target="…" hx-swap="…">…</hx-partial>: an empty target or swap omits the corresponding | 线格式为 <hx-partial hx-target="…" hx-swap="…">…</hx-partial>：target 或 swap 为空时省略对应属性，
// attribute, attribute values are HTML-escaped, and body is emitted verbatim because it is an already-rendered fragment | 属性值经 HTML 转义，body 原样输出（它已是渲染好的片段，永不转义）。
// and must never be escaped. A failed write is wrapped in ErrHtmxWrite. | 写出失败时错误以 ErrHtmxWrite 包装。
//
// The fragment leaves as a raw text message: the Htmx 4 ws extension falls back to the raw text whenever the payload is | 该片段以裸文本消息发出：Htmx 4 的 ws 扩展在载荷不是 JSON 时直接把它当作片段内容。
// not JSON. When the peer expects the JSON envelope instead, put this fragment into HtmxMessage.Body and send it through | 若对端要求 JSON 信封，则把该片段放进 HtmxMessage.Body，用 jsonx 编码后再由
// jsonx and WriteMessage. | WriteMessage 发送。
func (c *Conn) WriteHtmxFragment(target, swap, html string) error {
	buf := make([]byte, 0, len(html)+len(target)+len(swap)+32)
	buf = append(buf, "<hx-partial"...)
	if target != "" {
		buf = append(buf, ` hx-target="`...)
		buf = append(buf, stdhtml.EscapeString(target)...)
		buf = append(buf, '"')
	}
	if swap != "" {
		buf = append(buf, ` hx-swap="`...)
		buf = append(buf, stdhtml.EscapeString(swap)...)
		buf = append(buf, '"')
	}
	buf = append(buf, '>')
	buf = append(buf, html...)
	buf = append(buf, "</hx-partial>"...)

	if err := c.WriteMessage(TextMessage, buf); err != nil {
		return errx.Wrap(err, ErrHtmxWrite)
	}
	return nil
}

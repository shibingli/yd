// Package ws is a WebSocket implementation that strictly follows RFC 6455. | Package ws 是严格遵循 RFC 6455 的 WebSocket 实现。
//
// Design highlights: | 设计要点：
//
//   - Minimal dependencies: the handshake, frame encoding/decoding, masking and incremental UTF-8 validation are implemented here; |   - 依赖克制：握手、帧编解码、掩码与 UTF-8 增量校验均自行实现；
//     only the framework's b64x and the klauspost/compress DEFLATE codec are reused. |     仅复用框架自带的 b64x 与 klauspost/compress 的 DEFLATE 编解码。
//   - RFC 7692 permessage-deflate is opt-in on both endpoints: the first data frame of a message carries RSV1, |   - RFC 7692 permessage-deflate 在两端都需显式开启：消息首个数据帧置 RSV1，
//     control frames are never compressed, context takeover is honoured, and decompression output is capped |     控制帧永不压缩，支持上下文接管，且解压输出受 MaxMessageSize 限制
//     by MaxMessageSize so a compression bomb is closed with 1009 instead of exhausting memory. |     因此解压炸弹会以 1009 关闭而不是耗尽内存。
//   - Memory reuse: frame headers are written into a stack array and sent together with the payload in a single writev via net.Buffers, |   - 内存复用：帧头写入栈上数组并配合 net.Buffers 一次 writev 发出，
//     so the payload is never copied a second time; the read side reuses internal buffers and reads large messages straight into the destination slice. |     载荷不做二次拷贝；读取侧复用内部缓冲，大消息直接读入目标切片。
//   - Protocol strictness: client masking is enforced, control frames are constrained, RSV bits are validated, text is UTF-8 validated, |   - 协议严格性：强制客户端掩码、控制帧约束、RSV 校验、文本 UTF-8 校验、
//     oversized messages are closed, and the error codes fully follow RFC 6455 §7.4.1. |     超限消息关闭，错误码完全遵循 RFC 6455 §7.4.1。
//
// Concurrency model: one concurrent reader goroutine and one concurrent writer goroutine are allowed; control frames are answered | 并发模型：允许一个并发读协程与一个并发写协程；控制帧由内部自动应答，
// automatically by the implementation, requiring no extra involvement from the caller. | 不要求调用方额外参与。
package ws

import (
	"fmt"
	"net/http"
	"time"

	"github.com/shibingli/yd/core/errx"
)

// Protocol constants. | 协议常量。
const (
	// websocketGUID is the handshake magic value specified by RFC 6455 §1.3. | websocketGUID 是 RFC 6455 §1.3 规定的握手魔数。
	websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	// maxControlPayload is the control frame payload limit (RFC 6455 §5.5). | maxControlPayload 是控制帧载荷上限（RFC 6455 §5.5）。
	maxControlPayload = 125
	// maxHeaderSize is the maximum byte count of a masked client frame header (2 + 8 + 4). | maxHeaderSize 是客户端掩码帧头最大字节数（2 + 8 + 4）。
	maxHeaderSize = 14
	// defaultReadBufferSize / defaultWriteBufferSize are the default read and write buffer sizes. | defaultReadBufferSize / defaultWriteBufferSize 是默认读写缓冲大小。
	defaultReadBufferSize  = 4096
	defaultWriteBufferSize = 4096
	// defaultMaxMessageSize is the default per-message limit (32MB). | defaultMaxMessageSize 是默认单条消息上限（32MB）。
	defaultMaxMessageSize = 32 << 20
	// closeTimeout is the time to wait for the peer to acknowledge after a close frame is sent. | closeTimeout 是发送关闭帧后等待对端确认的时间。
	closeTimeout = 5 * time.Second
)

// OpCode is a WebSocket frame opcode (RFC 6455 §5.2). | OpCode 是 WebSocket 帧操作码（RFC 6455 §5.2）。
type OpCode byte

// Opcode values. | 操作码取值。
const (
	// ContinuationMessage denotes a subsequent frame of a fragmented message. | ContinuationMessage 表示分片消息的后续帧。
	ContinuationMessage OpCode = 0x0
	// TextMessage denotes a UTF-8 text message. | TextMessage 表示 UTF-8 文本消息。
	TextMessage OpCode = 0x1
	// BinaryMessage denotes a binary message. | BinaryMessage 表示二进制消息。
	BinaryMessage OpCode = 0x2
	// CloseMessage denotes a close frame. | CloseMessage 表示关闭帧。
	CloseMessage OpCode = 0x8
	// PingMessage denotes a ping frame. | PingMessage 表示 ping 帧。
	PingMessage OpCode = 0x9
	// PongMessage denotes a pong frame. | PongMessage 表示 pong 帧。
	PongMessage OpCode = 0xA
)

// IsControl reports whether this opcode denotes a control frame. | IsControl 报告该操作码是否为控制帧。
func (c OpCode) IsControl() bool { return c&0x8 != 0 }

// IsData reports whether this opcode denotes a data frame. | IsData 报告该操作码是否为数据帧。
func (c OpCode) IsData() bool { return c == TextMessage || c == BinaryMessage }

// TransportKind identifies the transport a Conn runs on. | TransportKind 标识 Conn 所在的传输。
type TransportKind uint8

// Transport values. | 传输取值。
const (
	// TransportTCP is an HTTP/1.1 connection obtained through http.Hijacker. | TransportTCP 是通过 http.Hijacker 得到的 HTTP/1.1 连接。
	TransportTCP TransportKind = iota
	// TransportHTTP2 is an RFC 8441 extended CONNECT stream. | TransportHTTP2 是 RFC 8441 的 extended CONNECT 流。
	TransportHTTP2
	// TransportHTTP3 is an RFC 9220 extended CONNECT stream. | TransportHTTP3 是 RFC 9220 的 extended CONNECT 流。
	TransportHTTP3
)

func (c OpCode) String() string {
	switch c {
	case ContinuationMessage:
		return "continuation"
	case TextMessage:
		return "text"
	case BinaryMessage:
		return "binary"
	case CloseMessage:
		return "close"
	case PingMessage:
		return "ping"
	case PongMessage:
		return "pong"
	default:
		return fmt.Sprintf("opcode(%d)", byte(c))
	}
}

// CloseCode is a close frame status code (RFC 6455 §7.4.1). | CloseCode 是关闭帧状态码（RFC 6455 §7.4.1）。
type CloseCode uint16

// Close code values. | 关闭码取值。
const (
	CloseNormalClosure           CloseCode = 1000
	CloseGoingAway               CloseCode = 1001
	CloseProtocolError           CloseCode = 1002
	CloseUnsupportedData         CloseCode = 1003
	CloseNoStatusReceived        CloseCode = 1005
	CloseAbnormalClosure         CloseCode = 1006
	CloseInvalidFramePayloadData CloseCode = 1007
	ClosePolicyViolation         CloseCode = 1008
	CloseMessageTooBig           CloseCode = 1009
	CloseMandatoryExtension      CloseCode = 1010
	CloseInternalServerErr       CloseCode = 1011
	CloseServiceRestart          CloseCode = 1012
	CloseTryAgainLater           CloseCode = 1013
	CloseTLSHandshake            CloseCode = 1015
)

func (c CloseCode) String() string {
	switch c {
	case CloseNormalClosure:
		return "正常关闭"
	case CloseGoingAway:
		return "端点离开"
	case CloseProtocolError:
		return "协议错误"
	case CloseUnsupportedData:
		return "不支持的数据类型"
	case CloseNoStatusReceived:
		return "未收到状态码"
	case CloseAbnormalClosure:
		return "异常关闭"
	case CloseInvalidFramePayloadData:
		return "载荷数据非法"
	case ClosePolicyViolation:
		return "策略违规"
	case CloseMessageTooBig:
		return "消息过大"
	case CloseMandatoryExtension:
		return "缺少强制扩展"
	case CloseInternalServerErr:
		return "服务端内部错误"
	case CloseServiceRestart:
		return "服务重启"
	case CloseTryAgainLater:
		return "稍后重试"
	case CloseTLSHandshake:
		return "TLS 握手失败"
	default:
		return fmt.Sprintf("关闭码(%d)", uint16(c))
	}
}

// validCloseCode reports whether a status code is allowed on the wire (1005/1006/1015 are reserved and must not be sent). | validCloseCode 报告状态码是否允许出现在线上（1005/1006/1015 保留，不得发送）。
func validCloseCode(c CloseCode) bool {
	if c >= 3000 && c <= 4999 {
		return true
	}
	switch c {
	case CloseNormalClosure, CloseGoingAway, CloseProtocolError, CloseUnsupportedData,
		CloseInvalidFramePayloadData, ClosePolicyViolation, CloseMessageTooBig,
		CloseMandatoryExtension, CloseInternalServerErr, CloseServiceRestart, CloseTryAgainLater:
		return true
	default:
		return false
	}
}

// Error variables. | 错误变量。
var (
	// ErrHandshake indicates that the WebSocket handshake failed. | ErrHandshake 表示 WebSocket 握手失败。
	ErrHandshake = errx.New("ws: handshake failed | ws：握手失败")
	// ErrHijackUnsupported indicates that the underlying connection does not support Hijack. | ErrHijackUnsupported 表示底层连接不支持 Hijack。
	// WebSocket cannot be obtained through an upgrade over HTTP/2 and HTTP/3; on those protocols use the RFC 8441 | HTTP/2 与 HTTP/3 无法通过升级获得 WebSocket；这些协议下应使用 RFC 8441 的
	// Extended CONNECT (not provided by this implementation), or switch to SSE. | Extended CONNECT（本实现未提供），或改用 SSE。
	ErrHijackUnsupported = errx.New("ws: underlying connection does not support Hijack (HTTP/2 and HTTP/3 cannot upgrade to WebSocket; use SSE instead) | ws：底层连接不支持 Hijack（HTTP/2 与 HTTP/3 无法升级为 WebSocket，请改用 SSE）")
	// ErrOriginNotAllowed indicates that the Origin check did not pass. | ErrOriginNotAllowed 表示 Origin 校验未通过。
	ErrOriginNotAllowed = errx.New("ws: Origin check failed | ws：Origin 校验未通过")
	// ErrProtocol indicates that the peer violated an RFC 6455 protocol constraint. | ErrProtocol 表示对端违反 RFC 6455 协议约束。
	ErrProtocol = errx.New("ws: protocol error | ws：协议错误")
	// ErrMessageTooBig indicates that a message exceeded MaxMessageSize. | ErrMessageTooBig 表示消息超过 MaxMessageSize。
	ErrMessageTooBig = errx.New("ws: message exceeds the size limit | ws：消息超过大小上限")
	// ErrInvalidUTF8 indicates that a text message is not valid UTF-8. | ErrInvalidUTF8 表示文本消息不是合法 UTF-8。
	ErrInvalidUTF8 = errx.New("ws: text message is not valid UTF-8 | ws：文本消息不是合法 UTF-8")
	// ErrCloseSent indicates that the close frame has been sent, so no further data frame may be written. | ErrCloseSent 表示关闭帧已发出，此后不得再写数据帧。
	ErrCloseSent = errx.New("ws: close frame already sent | ws：关闭帧已发出")
	// ErrUnexpectedEOF indicates that the connection dropped before a close frame arrived (corresponding to 1006). | ErrUnexpectedEOF 表示未收到关闭帧连接即断开（对应 1006）。
	ErrUnexpectedEOF = errx.New("ws: connection dropped before a close frame arrived | ws：连接异常断开")
	// ErrControlTooLarge indicates that a control frame payload exceeds 125 bytes. | ErrControlTooLarge 表示控制帧载荷超过 125 字节。
	ErrControlTooLarge = errx.New("ws: control frame payload exceeds 125 bytes | ws：控制帧载荷超过 125 字节")
	// ErrFragmentedControl indicates that a control frame was fragmented. | ErrFragmentedControl 表示控制帧被分片。
	ErrFragmentedControl = errx.New("ws: control frames must not be fragmented | ws：控制帧不得分片")
	// ErrReservedBits indicates that RSV bits were set while no extension had been negotiated. | ErrReservedBits 表示在未协商扩展时设置了 RSV 位。
	ErrReservedBits = errx.New("ws: RSV bits set without a negotiated extension | ws：未协商扩展却设置了 RSV 位")
	// ErrCompression indicates that the permessage-deflate codec itself failed (not a peer protocol violation). | ErrCompression 表示 permessage-deflate 编解码器自身失败（不是对端协议违规）。
	ErrCompression = errx.New("ws: permessage-deflate codec failed | ws：permessage-deflate 编解码失败")
	// ErrInvalidCompressedData indicates that a compressed message could not be inflated; the connection is closed with 1007. | ErrInvalidCompressedData 表示压缩消息无法解压；连接会以 1007 关闭。
	ErrInvalidCompressedData = errx.New("ws: compressed message is corrupt | ws：压缩消息已损坏")
	// ErrMaskMismatch indicates that the masking direction violates RFC 6455 (clients must mask, servers must not mask). | ErrMaskMismatch 表示掩码方向不符合 RFC 6455（客户端必须掩码、服务端必须不掩码）。
	ErrMaskMismatch = errx.New("ws: masking direction violates RFC 6455 | ws：掩码方向不符合 RFC 6455")
	// ErrBadOpCode indicates a reserved opcode. | ErrBadOpCode 表示保留操作码。
	ErrBadOpCode = errx.New("ws: reserved opcode | ws：保留了非法操作码")
	// ErrContinuationWithoutStart indicates that a continuation frame was received with no starting frame. | ErrContinuationWithoutStart 表示收到没有起始帧的续帧。
	ErrContinuationWithoutStart = errx.New("ws: continuation frame without a starting frame | ws：收到没有起始帧的续帧")
	// ErrNewDataDuringFragment indicates that a new message started before the fragmented one had finished. | ErrNewDataDuringFragment 表示分片消息未结束就开始了新消息。
	ErrNewDataDuringFragment = errx.New("ws: new data frame arrived before the fragmented message ended | ws：分片消息未结束即出现新的数据帧")
	// ErrFrameLengthMSB indicates that a 64-bit payload length has its most significant bit set, which RFC 6455 §5.2 forbids. | ErrFrameLengthMSB 表示 64 位载荷长度的最高位被置 1，RFC 6455 §5.2 禁止这种编码。
	ErrFrameLengthMSB = errx.New("ws: frame payload length has the most significant bit set | ws：帧载荷长度的最高位被置 1")
	// ErrWriteContinuation indicates that WriteMessage was given a bare continuation frame (fragmentation is not exposed by the write API). | ErrWriteContinuation 表示 WriteMessage 收到了没有起始帧的续帧（写 API 不暴露分片能力）。
	ErrWriteContinuation = errx.New("ws: a continuation frame cannot start a message; WriteMessage writes complete messages only | ws：续帧不能作为消息的起始帧；WriteMessage 只写完整消息")
	// ErrWriteMessageOpcode indicates that WriteMessage was given an opcode other than text or binary (control frames use WriteControl; 0x3-0x7 and 0xB-0xF are reserved). | ErrWriteMessageOpcode 表示 WriteMessage 收到的不是文本或二进制操作码（控制帧请用 WriteControl；0x3-0x7 与 0xB-0xF 为保留码）。
	ErrWriteMessageOpcode = errx.New("ws: WriteMessage accepts only text or binary opcodes | ws：WriteMessage 只接受文本或二进制操作码")
	// ErrWriteControlOpcode indicates that WriteControl was given an opcode other than close, ping or pong. | ErrWriteControlOpcode 表示 WriteControl 收到的不是 close、ping 或 pong 操作码。
	ErrWriteControlOpcode = errx.New("ws: WriteControl accepts only close, ping or pong opcodes | ws：WriteControl 只接受 close、ping 或 pong 操作码")
	// ErrInvalidCloseCode indicates that an outbound close frame carries a reserved or unassigned status code. | ErrInvalidCloseCode 表示出站关闭帧携带了保留或未分配的状态码。
	ErrInvalidCloseCode = errx.New("ws: close status code is reserved or unassigned and must not appear on the wire | ws：关闭状态码为保留或未分配，不得出现在线上")
	// ErrInvalidCloseReason indicates that an outbound close reason is not valid UTF-8. | ErrInvalidCloseReason 表示出站关闭原因不是合法 UTF-8。
	ErrInvalidCloseReason = errx.New("ws: close reason must be valid UTF-8 | ws：关闭原因必须是合法 UTF-8")
	// ErrUnrequestedSubprotocol indicates that the server selected a subprotocol the client never offered (RFC 6455 §4.1). | ErrUnrequestedSubprotocol 表示服务端选择了客户端未提议的子协议（RFC 6455 §4.1）。
	ErrUnrequestedSubprotocol = errx.New("ws: server selected a subprotocol the client did not offer | ws：服务端选择了客户端未提议的子协议")
	// ErrUnrequestedExtension indicates that the server enabled an extension the client never offered (RFC 6455 §4.1). | ErrUnrequestedExtension 表示服务端启用了客户端未提议的扩展（RFC 6455 §4.1）。
	ErrUnrequestedExtension = errx.New("ws: server enabled an extension the client did not offer | ws：服务端启用了客户端未提议的扩展")
)

// CloseError indicates that one side ended the connection with a close frame. | CloseError 表示对端或本端以关闭帧结束连接。
type CloseError struct {
	// Text is the close reason (UTF-8). | Text 为关闭原因（UTF-8）。
	Text string
	// Code is the close status code. | Code 为关闭状态码。
	Code CloseCode
}

func (e *CloseError) Error() string {
	if e.Text == "" {
		return fmt.Sprintf("ws: 连接关闭 (%d %s)", uint16(e.Code), e.Code)
	}
	return fmt.Sprintf("ws: 连接关闭 (%d %s): %s", uint16(e.Code), e.Code, e.Text)
}

// Options holds the server-side upgrade parameters. | Options 是服务端升级参数。
type Options struct {
	// CheckOrigin validates the Origin; when nil, the same-origin policy is used. | CheckOrigin 校验 Origin；为 nil 时使用同源策略。
	CheckOrigin func(*http.Request) bool
	// Tracker, when non-nil, registers every connection established by Upgrade, so that a shutdown can close them all at once. | Tracker 非 nil 时登记 Upgrade 建立的每条连接，使退出流程能一次关闭全部连接。
	//
	// The connection untracks itself from Close / CloseNow as well as from any terminating read error, so the
	// tracker never leaks entries. | 连接会在 Close / CloseNow 以及任何终止性读错误处自行取消登记，因此追踪表不会泄漏条目。
	Tracker Tracker
	// Subprotocols are the subprotocols the server supports, chosen by order of priority. | Subprotocols 为服务端支持的子协议，按顺序优先级选择。
	Subprotocols []string
	// ReadBufferSize is the read buffer size; <=0 means 4096. | ReadBufferSize 为读缓冲大小，<=0 时取 4096。
	ReadBufferSize int
	// WriteBufferSize is the write buffer size; <=0 means 4096. | WriteBufferSize 为写缓冲大小，<=0 时取 4096。
	WriteBufferSize int
	// HandshakeTimeout is the handshake timeout; <=0 means no limit. | HandshakeTimeout 为握手超时，<=0 表示不限制。
	HandshakeTimeout time.Duration
	// MaxMessageSize is the per-message limit; <=0 means 32MB. | MaxMessageSize 为单条消息上限，<=0 时取 32MB。
	MaxMessageSize int64
	// EnableCompression enables RFC 7692 permessage-deflate negotiation on the server side. | EnableCompression 在服务端启用 RFC 7692 permessage-deflate 协商。
	//
	// It defaults to false: compression costs CPU and only helps peers that understand the extension, | 默认值为 false：压缩消耗 CPU，且只对理解该扩展的对端有益，
	// so it must be switched on explicitly. When enabled, Upgrade parses the client's | 因此必须显式开启。启用后，Upgrade 会解析客户端
	// Sec-WebSocket-Extensions offer and echoes the parameters it can honour; an offer it cannot | 的 Sec-WebSocket-Extensions 提议并回写它能履行的参数；无法履行的提议
	// accept is declined (no extension is negotiated) rather than failing the handshake. | 会被拒绝（不协商扩展），而不是让握手失败。
	EnableCompression bool
}

// normalize fills in the default values. | normalize 填充默认值。
func (o Options) normalize() Options {
	if o.ReadBufferSize <= 0 {
		o.ReadBufferSize = defaultReadBufferSize
	}
	if o.WriteBufferSize <= 0 {
		o.WriteBufferSize = defaultWriteBufferSize
	}
	if o.MaxMessageSize <= 0 {
		o.MaxMessageSize = defaultMaxMessageSize
	}
	return o
}

// Package ws 是严格遵循 RFC 6455 的 WebSocket 实现。
//
// 设计要点：
//
//   - 零外部依赖：握手、帧编解码、掩码、UTF-8 增量校验全部自行实现，
//     仅复用框架自带的 b64x（Sec-WebSocket-Accept 计算）。
//   - 内存复用：帧头写入栈上数组并配合 net.Buffers 一次 writev 发出，
//     载荷不做二次拷贝；读取侧复用内部缓冲，大消息直接读入目标切片。
//   - 协议严格性：强制客户端掩码、控制帧约束、RSV 校验、文本 UTF-8 校验、
//     超限消息关闭，错误码完全遵循 RFC 6455 §7.4.1。
//
// 并发模型：允许一个并发读协程与一个并发写协程；控制帧由内部自动应答，
// 不要求调用方额外参与。
package ws

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// 协议常量。
const (
	// websocketGUID 是 RFC 6455 §1.3 规定的握手魔数。
	websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	// maxControlPayload 是控制帧载荷上限（RFC 6455 §5.5）。
	maxControlPayload = 125
	// maxHeaderSize 是客户端掩码帧头最大字节数（2 + 8 + 4）。
	maxHeaderSize = 14
	// defaultReadBufferSize / defaultWriteBufferSize 是默认读写缓冲大小。
	defaultReadBufferSize  = 4096
	defaultWriteBufferSize = 4096
	// defaultMaxMessageSize 是默认单条消息上限（32MB）。
	defaultMaxMessageSize = 32 << 20
	// closeTimeout 是发送关闭帧后等待对端确认的时间。
	closeTimeout = 5 * time.Second
)

// OpCode 是 WebSocket 帧操作码（RFC 6455 §5.2）。
type OpCode byte

// 操作码取值。
const (
	// ContinuationMessage 表示分片消息的后续帧。
	ContinuationMessage OpCode = 0x0
	// TextMessage 表示 UTF-8 文本消息。
	TextMessage OpCode = 0x1
	// BinaryMessage 表示二进制消息。
	BinaryMessage OpCode = 0x2
	// CloseMessage 表示关闭帧。
	CloseMessage OpCode = 0x8
	// PingMessage 表示 ping 帧。
	PingMessage OpCode = 0x9
	// PongMessage 表示 pong 帧。
	PongMessage OpCode = 0xA
)

// IsControl 报告该操作码是否为控制帧。
func (c OpCode) IsControl() bool { return c&0x8 != 0 }

// IsData 报告该操作码是否为数据帧。
func (c OpCode) IsData() bool { return c == TextMessage || c == BinaryMessage }

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

// CloseCode 是关闭帧状态码（RFC 6455 §7.4.1）。
type CloseCode uint16

// 关闭码取值。
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

// validCloseCode 报告状态码是否允许出现在线上（1005/1006/1015 保留，不得发送）。
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

// 错误变量。
var (
	// ErrHandshake 表示 WebSocket 握手失败。
	ErrHandshake = errors.New("ws: 握手失败")
	// ErrHijackUnsupported 表示底层连接不支持 Hijack。
	// HTTP/2 与 HTTP/3 无法通过升级获得 WebSocket；这些协议下应使用 RFC 8441 的
	// Extended CONNECT（本实现未提供），或改用 SSE。
	ErrHijackUnsupported = errors.New("ws: 底层连接不支持 Hijack（HTTP/2 与 HTTP/3 无法升级为 WebSocket，请改用 SSE）")
	// ErrOriginNotAllowed 表示 Origin 校验未通过。
	ErrOriginNotAllowed = errors.New("ws: Origin 校验未通过")
	// ErrProtocol 表示对端违反 RFC 6455 协议约束。
	ErrProtocol = errors.New("ws: 协议错误")
	// ErrMessageTooBig 表示消息超过 MaxMessageSize。
	ErrMessageTooBig = errors.New("ws: 消息超过大小上限")
	// ErrInvalidUTF8 表示文本消息不是合法 UTF-8。
	ErrInvalidUTF8 = errors.New("ws: 文本消息不是合法 UTF-8")
	// ErrCloseSent 表示关闭帧已发出，此后不得再写数据帧。
	ErrCloseSent = errors.New("ws: 关闭帧已发出")
	// ErrUnexpectedEOF 表示未收到关闭帧连接即断开（对应 1006）。
	ErrUnexpectedEOF = errors.New("ws: 连接异常断开")
	// ErrControlTooLarge 表示控制帧载荷超过 125 字节。
	ErrControlTooLarge = errors.New("ws: 控制帧载荷超过 125 字节")
	// ErrFragmentedControl 表示控制帧被分片。
	ErrFragmentedControl = errors.New("ws: 控制帧不得分片")
	// ErrReservedBits 表示在未协商扩展时设置了 RSV 位。
	ErrReservedBits = errors.New("ws: 未协商扩展却设置了 RSV 位")
	// ErrMaskMismatch 表示掩码方向不符合 RFC 6455（客户端必须掩码、服务端必须不掩码）。
	ErrMaskMismatch = errors.New("ws: 掩码方向不符合 RFC 6455")
	// ErrBadOpCode 表示保留操作码。
	ErrBadOpCode = errors.New("ws: 保留了非法操作码")
	// ErrContinuationWithoutStart 表示收到没有起始帧的续帧。
	ErrContinuationWithoutStart = errors.New("ws: 收到没有起始帧的续帧")
	// ErrNewDataDuringFragment 表示分片消息未结束就开始了新消息。
	ErrNewDataDuringFragment = errors.New("ws: 分片消息未结束即出现新的数据帧")
)

// CloseError 表示对端或本端以关闭帧结束连接。
type CloseError struct {
	// Code 为关闭状态码。
	Code CloseCode
	// Text 为关闭原因（UTF-8）。
	Text string
}

func (e *CloseError) Error() string {
	if e.Text == "" {
		return fmt.Sprintf("ws: 连接关闭 (%d %s)", uint16(e.Code), e.Code)
	}
	return fmt.Sprintf("ws: 连接关闭 (%d %s): %s", uint16(e.Code), e.Code, e.Text)
}

// Options 是服务端升级参数。
type Options struct {
	// ReadBufferSize 为读缓冲大小，<=0 时取 4096。
	ReadBufferSize int
	// WriteBufferSize 为写缓冲大小，<=0 时取 4096。
	WriteBufferSize int
	// HandshakeTimeout 为握手超时，<=0 表示不限制。
	HandshakeTimeout time.Duration
	// MaxMessageSize 为单条消息上限，<=0 时取 32MB。
	MaxMessageSize int64
	// Subprotocols 为服务端支持的子协议，按顺序优先级选择。
	Subprotocols []string
	// CheckOrigin 校验 Origin；为 nil 时使用同源策略。
	CheckOrigin func(*http.Request) bool
	// DisableCompression 保留字段：本实现未启用 permessage-deflate。
	DisableCompression bool
}

// normalize 填充默认值。
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

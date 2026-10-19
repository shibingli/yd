// Package grpcx integrates gRPC with the yd HTTP stack. | Package grpcx 把 gRPC 集成进 yd 的 HTTP 技术栈。
//
// gRPC always speaks HTTP/2, so the dual-channel mode multiplexes gRPC and
// HTML traffic on one port by inspecting the request protocol and content
// type. The same-port mode requires cleartext HTTP/2 (h2c) or TLS with ALPN;
// use it together with server.Options.H2C or server.Options.TLS. | gRPC 始终基于
// HTTP/2，因此双通道模式通过检查请求协议与内容类型，在同一端口上复用 gRPC 与 HTML 流量。
// 同端口模式要求明文 HTTP/2（h2c）或带 ALPN 的 TLS，请与 server.Options.H2C 或
// server.Options.TLS 搭配使用。
package grpcx

import (
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/shibingli/yd/core/config"
	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/core/logx"
)

// ErrSeparateModeUnsupported reports ModeSeparate, which has no listener implementation: accepting
// it silently would leave a healthy-looking process whose gRPC port refuses every connection.
// | ErrSeparateModeUnsupported 表示 ModeSeparate 尚无监听实现：静默接受它会让进程看起来健康，
// 而 gRPC 端口拒绝所有连接。
var ErrSeparateModeUnsupported = errx.New("grpcx: separate listener mode is not implemented; use same-port | grpcx：独立监听模式尚未实现；请使用同端口模式")

// Transport modes. | 传输模式常量。
const (
	// ModeSamePort multiplexes gRPC and HTTP on one listener. | ModeSamePort 在同一监听上复用 gRPC 与 HTTP。
	ModeSamePort = "same-port"
	// ModeSeparate runs gRPC on its own listener. | ModeSeparate 让 gRPC 使用独立监听。
	ModeSeparate = "separate"
)

// Options configures the gRPC channel. | Options 配置 gRPC 通道。
type Options struct {
	// Mode is ModeSamePort or ModeSeparate. | Mode 取 ModeSamePort 或 ModeSeparate。
	Mode string
	// Port is the listener port for ModeSeparate. | Port 是 ModeSeparate 下的监听端口。
	Port int
	// MaxRecvMsgSize limits inbound messages in bytes. | MaxRecvMsgSize 限制入站消息字节数。
	MaxRecvMsgSize int
	// MaxSendMsgSize limits outbound messages in bytes. | MaxSendMsgSize 限制出站消息字节数。
	MaxSendMsgSize int
	// Enabled turns gRPC on. | Enabled 开启 gRPC。
	Enabled bool
	// Reflection registers the server reflection service. | Reflection 注册服务端反射服务。
	Reflection bool
}

// withDefaults fills in the transport mode. | withDefaults 填充传输模式的默认值。
func (o Options) withDefaults() Options {
	if o.Mode == "" {
		o.Mode = ModeSamePort
	}
	return o
}

// NewServer builds a gRPC server from the options. | NewServer 依据参数构造 gRPC 服务端。
func NewServer(o Options, opts ...grpc.ServerOption) *grpc.Server {
	o = o.withDefaults()
	if o.MaxRecvMsgSize > 0 {
		opts = append(opts, grpc.MaxRecvMsgSize(o.MaxRecvMsgSize))
	}
	if o.MaxSendMsgSize > 0 {
		opts = append(opts, grpc.MaxSendMsgSize(o.MaxSendMsgSize))
	}
	srv := grpc.NewServer(opts...)
	if o.Reflection {
		reflection.Register(srv)
	}
	return srv
}

// DualHandler routes gRPC calls and plain HTTP requests to their handlers. | DualHandler 把 gRPC 调用与普通 HTTP 请求分流到各自处理器。
//
// A request is treated as gRPC when it arrives over HTTP/2 and its content
// type is application/grpc (with an optional suffix such as +proto). | 当请求经由
// HTTP/2 到达且内容类型为 application/grpc（可带 +proto 等后缀）时，按 gRPC 处理。
func DualHandler(grpcSrv *grpc.Server, httpHandler http.Handler) http.Handler {
	if grpcSrv == nil {
		return httpHandler
	}
	if httpHandler == nil {
		return grpcSrv
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsGRPCRequest(r) {
			grpcSrv.ServeHTTP(w, r)
			return
		}
		httpHandler.ServeHTTP(w, r)
	})
}

// IsGRPCRequest reports whether r is a gRPC call. | IsGRPCRequest 报告 r 是否为 gRPC 调用。
func IsGRPCRequest(r *http.Request) bool {
	if r == nil || r.ProtoMajor != 2 {
		return false
	}
	ct := r.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	return ct == "application/grpc" || strings.HasPrefix(ct, "application/grpc+")
}

// OptionsFromConfig reads gRPC settings from the layered config. | OptionsFromConfig 从分层配置读取 gRPC 设置。
func OptionsFromConfig(c *config.Config) Options {
	if c == nil {
		// Even without a config object we must still return a usable default mode, | 即使没有配置对象，也必须给出可用的默认模式，
		// otherwise a caller holding an empty mode cannot tell which transport to use. | 否则调用方拿到空模式后无法判断该用哪种传输方式。
		return Options{}.withDefaults()
	}
	return Options{
		Enabled:        c.GetBool("grpc.enabled"),
		Mode:           c.GetString("grpc.mode"),
		Port:           c.GetInt("grpc.port"),
		MaxRecvMsgSize: c.GetInt("grpc.max_recv_msg_size"),
		MaxSendMsgSize: c.GetInt("grpc.max_send_msg_size"),
		Reflection:     c.GetBool("grpc.reflection"),
	}.withDefaults()
}

// Validate checks that the transport mode has an implementation. | Validate 校验传输模式是否有实现。
func Validate(o Options) error {
	if o.withDefaults().Mode == ModeSeparate {
		return ErrSeparateModeUnsupported
	}
	return nil
}

// RegisterE is Register with the option error exposed. | RegisterE 是暴露选项错误的 Register。
func RegisterE(o Options, l *logx.Logger, opts ...grpc.ServerOption) (*grpc.Server, error) {
	if err := Validate(o); err != nil {
		return nil, err
	}
	return Register(o, l, opts...), nil
}

// Register wires a gRPC server into an application. | Register 把 gRPC 服务端接入应用。
//
// It returns the server so callers can register services on it. ModeSeparate is reported through
// the logger because this signature cannot return an error; use RegisterE to receive
// ErrSeparateModeUnsupported instead. | 返回服务端以便调用方注册服务。由于本签名无法返回错误，
// ModeSeparate 会经日志报告；需要拿到 ErrSeparateModeUnsupported 时请用 RegisterE。
func Register(o Options, l *logx.Logger, opts ...grpc.ServerOption) *grpc.Server {
	o = o.withDefaults()
	if err := Validate(o); err != nil && l != nil {
		l.Error("gRPC transport mode is not supported | gRPC 传输模式不受支持",
			logx.Str("mode", o.Mode), logx.Err(err))
	}
	srv := NewServer(o, opts...)
	if l != nil {
		l.Info("gRPC channel enabled | gRPC 通道已启用",
			logx.Str("mode", o.Mode),
			logx.Int("port", o.Port),
			logx.Bool("reflection", o.Reflection),
		)
	}
	return srv
}

package grpcx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/yun-dev/yd/server"
)

// rawCodec passes []byte payloads through untouched. | rawCodec 原样透传 []byte 载荷。
//
// Using a raw codec keeps the test independent of protoc-generated code, so we
// verify the transport wiring rather than the serialization library. | 使用原始
// 编解码器可让测试不依赖 protoc 生成的代码，从而验证传输层装配而非序列化库。
type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error) {
	b, ok := v.(*[]byte)
	if !ok {
		return nil, errors.New("rawCodec: 仅支持 *[]byte")
	}
	return *b, nil
}

func (rawCodec) Unmarshal(data []byte, v any) error {
	b, ok := v.(*[]byte)
	if !ok {
		return errors.New("rawCodec: 仅支持 *[]byte")
	}
	*b = append([]byte(nil), data...)
	return nil
}

func (rawCodec) Name() string { return "yd-raw" }

// echoService mirrors the payload back to the caller. | echoService 把载荷原样回显给调用方。
type echoService interface {
	Echo()
}

type echoImpl struct{}

func (echoImpl) Echo() {}

var echoServiceDesc = grpc.ServiceDesc{
	ServiceName: "yd.Echo",
	HandlerType: (*echoService)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Echo",
		Handler: func(_ any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			in := new([]byte)
			if err := dec(in); err != nil {
				return nil, err
			}
			return in, nil
		},
	}},
	Metadata: "yd/echo.proto",
}

func TestIsGRPCRequest(t *testing.T) {
	mk := func(protoMajor int, ct string) *http.Request {
		r := &http.Request{ProtoMajor: protoMajor, Header: http.Header{}}
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		return r
	}
	cases := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"h2 grpc", mk(2, "application/grpc"), true},
		{"h2 grpc+proto", mk(2, "application/grpc+proto"), true},
		{"h2 grpc with charset", mk(2, "application/grpc; charset=utf-8"), true},
		{"h1 grpc", mk(1, "application/grpc"), false},
		{"h2 json", mk(2, "application/json"), false},
		{"h2 form", mk(2, "application/x-www-form-urlencoded"), false},
		{"h2 no ct", mk(2, ""), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsGRPCRequest(tc.req); got != tc.want {
				t.Fatalf("IsGRPCRequest = %v, 期望 %v", got, tc.want)
			}
		})
	}
}

func TestDualHandlerRoutesGRPCAndHTTPOnSamePort(t *testing.T) {
	grpcSrv := NewServer(Options{Enabled: true, Mode: ModeSamePort, MaxRecvMsgSize: 4 << 20}, grpc.ForceServerCodec(rawCodec{}))
	grpcSrv.RegisterService(&echoServiceDesc, echoImpl{})

	httpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "http-ok:"+r.URL.Path)
	})

	// 同端口双通道需要 h2c（或 TLS+ALPN），这里用框架自带的 server 提供
	srv := server.New(server.Options{
		Host:    "127.0.0.1",
		Port:    0,
		H2C:     true,
		Handler: DualHandler(grpcSrv, httpHandler),
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	t.Cleanup(func() {
		grpcSrv.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	addr := srv.Addr()

	// 1) 普通 HTTP 请求仍然走 HTTP 处理器
	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("HTTP 请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "http-ok:/health" {
		t.Fatalf("HTTP 通道异常: code=%d body=%q", resp.StatusCode, body)
	}

	// 2) 同一端口上的真实 gRPC 调用
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	if err != nil {
		t.Fatalf("建立 gRPC 客户端失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	payload := []byte(strings.Repeat("yd-grpc-", 100))
	var out []byte
	if err := conn.Invoke(ctx, "/yd.Echo/Echo", &payload, &out); err != nil {
		t.Fatalf("gRPC 调用失败: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("gRPC 回显不一致: len(in)=%d len(out)=%d", len(payload), len(out))
	}

	// 3) 调用 gRPC 之后 HTTP 通道依然可用（互不干扰）
	resp2, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("第二次 HTTP 请求失败: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("gRPC 调用后 HTTP 通道异常: %d", resp2.StatusCode)
	}
}

func TestDualHandlerNilFallbacks(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "plain") })

	if got := DualHandler(nil, h); got == nil {
		t.Fatal("gRPC 服务端为 nil 时应返回 HTTP 处理器")
	}
	srv := grpc.NewServer()
	if got := DualHandler(srv, nil); got == nil {
		t.Fatal("HTTP 处理器为 nil 时应返回 gRPC 服务端")
	}
	srv.Stop()
}

func TestOptionsFromConfigDefaultsToSamePort(t *testing.T) {
	o := OptionsFromConfig(nil)
	if o.Enabled {
		t.Fatal("nil 配置下 gRPC 应默认关闭")
	}
	if o.Mode != ModeSamePort {
		t.Fatalf("默认模式 = %q, 期望 %q", o.Mode, ModeSamePort)
	}
}

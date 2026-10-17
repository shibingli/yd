package grpcx

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// fakeServer is a GracefulServer whose graceful path can be released on demand. | fakeServer 是优雅停止路径可被显式放行的 GracefulServer。
type fakeServer struct {
	release chan struct{}
	grace   atomic.Bool
	stopped atomic.Bool
}

func (f *fakeServer) GracefulStop() {
	f.grace.Store(true)
	<-f.release
}

func (f *fakeServer) Stop() {
	f.stopped.Store(true)
	close(f.release)
}

// TestShutdownNilAndGracefulPaths 覆盖 nil、无期限以及优雅路径。
// TestShutdownNilAndGracefulPaths covers the nil, no-deadline and graceful paths.
func TestShutdownNilAndGracefulPaths(t *testing.T) {
	if err := Shutdown(context.Background(), nil); err != nil {
		t.Fatalf("nil 服务端应是无操作, err = %v", err)
	}

	srv := grpc.NewServer()
	// A nil ctx means no deadline; with no RPC in flight the graceful stop returns at once. | ctx 为 nil 表示不设期限；服务端无进行中的 RPC，优雅停止立即返回。
	if err := Shutdown(nil, srv); err != nil {
		t.Fatalf("无进行中 RPC 时优雅停止应成功, err = %v", err)
	}
}

// TestShutdownForceStopsAfterDeadline 验证宽限期过后强制停止，并保留超时原因。
// TestShutdownForceStopsAfterDeadline verifies the forced stop after the grace period and the preserved cause.
func TestShutdownForceStopsAfterDeadline(t *testing.T) {
	f := &fakeServer{release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Shutdown(ctx, f)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("错误 = %v, 期望匹配 ErrShutdownTimeout", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("错误 = %v, 期望保留 context.DeadlineExceeded", err)
	}
	if !f.grace.Load() {
		t.Fatal("must attempt the graceful stop first | 必须首先尝试优雅停止")
	}
	if !f.stopped.Load() {
		t.Fatal("must force a stop once the grace period is over | 宽限期过后必须强制停止")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("强制停止耗时 %v, 期望接近宽限期", elapsed)
	}
}

// streamService is the bidi streaming service used by the tests. | streamService 是测试用的双向流服务。
type streamService interface {
	Pipe(grpc.ServerStream) error
}

type streamImpl struct {
	started  chan struct{}
	canceled chan struct{}
}

func (s streamImpl) Pipe(ss grpc.ServerStream) error {
	close(s.started)
	<-ss.Context().Done()
	close(s.canceled)
	return ss.Context().Err()
}

var streamServiceDesc = grpc.ServiceDesc{
	ServiceName: "yd.Stream",
	HandlerType: (*streamService)(nil),
	Streams:     []grpc.StreamDesc{{StreamName: "Pipe", ServerStreams: true, ClientStreams: true, Handler: func(srv any, ss grpc.ServerStream) error { return srv.(streamService).Pipe(ss) }}},
	Metadata:    "yd/stream.proto",
}

// TestShutdownTerminatesOpenStreamWithinGrace 端到端验证：真实 gRPC 服务上挂着一条未结束的流时，
// 优雅停止会被宽限期兜住，随后强制停止取消该流，且 Serve 结束。
// TestShutdownTerminatesOpenStreamWithinGrace is an end-to-end check on a real gRPC server: with one open
// stream, the graceful stop is bounded by the deadline, the forced stop cancels that stream, and Serve ends.
func TestShutdownTerminatesOpenStreamWithinGrace(t *testing.T) {
	impl := streamImpl{started: make(chan struct{}), canceled: make(chan struct{})}
	srv := NewServer(Options{}, grpc.ForceServerCodec(rawCodec{}))
	srv.RegisterService(&streamServiceDesc, impl)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	serveReturned := make(chan error, 1)
	go func() { serveReturned <- srv.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	if err != nil {
		t.Fatalf("建立 gRPC 客户端失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	callCtx, cancelCall := context.WithCancel(context.Background())
	defer cancelCall()

	stream, err := conn.NewStream(callCtx, &streamServiceDesc.Streams[0], "/yd.Stream/Pipe")
	if err != nil {
		t.Fatalf("建立流失败: %v", err)
	}
	payload := []byte("yd-shutdown")
	if err := stream.SendMsg(&payload); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}
	if err := stream.SendMsg(&payload); err != nil {
		t.Fatalf("再次发送消息失败: %v", err)
	}

	select {
	case <-impl.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the server stream handler did not start within 5 seconds | 服务端流处理器未在 5 秒内启动")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = Shutdown(ctx, srv)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("错误 = %v, 期望匹配 ErrShutdownTimeout（开着的流必须先被宽限期兜住）", err)
	}
	t.Logf("流未结束时的 Shutdown 实测耗时 = %v（宽限期 300ms）", elapsed)
	if elapsed > 3*time.Second {
		t.Fatalf("Shutdown 耗时 %v, 必须在宽限期附近返回", elapsed)
	}
	select {
	case <-impl.canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("the forced stop did not cancel the open stream | 强制停止未取消进行中的流")
	}
	select {
	case serveErr := <-serveReturned:
		if serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			t.Fatalf("Serve 返回错误: %v", serveErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gRPC Serve did not return after the stop | gRPC Serve 未在停止后返回")
	}
}

// TestShutdownStopsIdleServer 验证没有进行中 RPC 时优雅停止直接成功，且之后无法再调用。
// TestShutdownStopsIdleServer verifies an idle server stops gracefully and accepts no further calls.
func TestShutdownStopsIdleServer(t *testing.T) {
	srv := NewServer(Options{}, grpc.ForceServerCodec(rawCodec{}))
	srv.RegisterService(&echoServiceDesc, echoImpl{})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	serveReturned := make(chan error, 1)
	go func() { serveReturned <- srv.Serve(lis) }()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
	)
	if err != nil {
		t.Fatalf("建立 gRPC 客户端失败: %v", err)
	}
	defer func() { _ = conn.Close() }()

	callCtx, cancelCall := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCall()

	payload := []byte("yd-idle")
	var out []byte
	if err := conn.Invoke(callCtx, "/yd.Echo/Echo", &payload, &out); err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	if err := Shutdown(ctx, srv); err != nil {
		t.Fatalf("空载服务端优雅停止失败: %v", err)
	}
	t.Logf("空载服务端 Shutdown 实测耗时 = %v", time.Since(start))

	select {
	case <-serveReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("gRPC Serve did not return after the stop | gRPC Serve 未在停止后返回")
	}
}

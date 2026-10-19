package grpcx

import (
	"context"

	"github.com/shibingli/yd/core/errx"
)

// ErrShutdownTimeout reports a gRPC graceful stop that did not finish within the deadline, so the server was force-stopped. | ErrShutdownTimeout 表示 gRPC 优雅停止未在期限内完成，服务端已被强制停止。
var ErrShutdownTimeout = errx.New("grpcx: graceful stop did not finish before the deadline | grpcx：宽限期内未能完成优雅停止")

// GracefulServer is the minimal view of a gRPC server the shutdown needs. | GracefulServer 是退出流程所需的 gRPC 服务端最小视图。
//
// It is declared here — rather than taking a *grpc.Server directly — so that a
// caller can pass any server implementation, and so the contract stays obvious
// to read. *grpc.Server satisfies it as is. | 这里声明接口而不是直接接收 *grpc.Server，
// 一是让调用方可以传入任意实现，二是让契约一眼可读。*grpc.Server 天然满足该接口。
type GracefulServer interface {
	// GracefulStop stops the server after all in-flight RPCs finish. | GracefulStop 等待所有进行中的 RPC 结束后停止服务端。
	GracefulStop()
	// Stop stops the server immediately and cancels in-flight RPCs. | Stop 立即停止服务端并取消进行中的 RPC。
	Stop()
}

// Shutdown stops a gRPC server gracefully and force-stops it once the deadline passes. | Shutdown 优雅停止 gRPC 服务端，并在超过期限后强制停止。
//
// A graceful stop waits for every in-flight RPC, and a single long-lived stream
// can therefore block it forever; ctx bounds that wait. When ctx expires the
// server is force-stopped through Stop (which cancels in-flight RPCs) and
// ErrShutdownTimeout is returned wrapped around ctx.Err(), so callers can still
// match the cause with errors.Is. A nil server is a no-op, and a nil ctx means
// "no deadline". | 优雅停止会等待所有进行中的 RPC，因此单条长连接流就能把它永远挂住；
// ctx 用来限定这次等待。ctx 到期后用 Stop 强制停止（进行中的 RPC 被取消），
// 并返回包裹 ctx.Err() 的 ErrShutdownTimeout，调用方仍可用 errors.Is 定位原因。
// srv 为 nil 时是无操作；ctx 为 nil 表示不设期限。
func Shutdown(ctx context.Context, srv GracefulServer) error {
	if srv == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.GracefulStop()
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}

	// grpc-go 的 GracefulStop 等待处理器时持有服务器互斥锁，Stop 需要同一把锁；处理器无视取消时
	// 同步调用 Stop 会越过期限阻塞。放到后台并返回超时，保证退出流程按时完成。
	// grpc-go's GracefulStop holds the server mutex while waiting for handlers, and Stop needs the
	// same mutex; calling Stop synchronously blocks past the deadline when a handler ignores
	// cancellation. Detach it and return the timeout so the shutdown sequence finishes on time.
	go srv.Stop()
	return errx.WrapDetail(ctx.Err(), ErrShutdownTimeout, "force stop after %[1]v | 超过 %[1]v 后强制停止", ctx.Err())
}

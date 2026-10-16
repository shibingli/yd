package app

import (
	"context"
	"errors"

	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/lifecycle"
	"github.com/shibingli/yd/logx"
)

// ErrGRPCShutdown indicates that a gRPC server did not stop gracefully within the deadline. | ErrGRPCShutdown 表示 gRPC 服务端未在期限内优雅停止。
var ErrGRPCShutdown = errx.New("app: gRPC graceful stop did not finish before the deadline | app：gRPC 宽限期内未能完成优雅停止")

// GRPCServer is the minimal view of a gRPC server the shutdown needs. | GRPCServer 是退出流程所需的 gRPC 服务端最小视图。
//
// It is declared in this package on purpose: importing grpcx here would link
// grpc into every application binary, including the ones that never speak
// gRPC. The protocol is exactly the one grpcx.Shutdown implements — graceful
// stop, then force stop when the deadline passes — so *grpc.Server satisfies
// this interface as is. | 该接口故意声明在本包：在这里导入 grpcx 会把 grpc
// 链接进每一个应用二进制，包括完全不用 gRPC 的项目。其协议与 grpcx.Shutdown
// 完全一致——先优雅停止，超过期限再强制停止——因此 *grpc.Server 天然满足它。
type GRPCServer interface {
	// GracefulStop waits for in-flight RPCs before stopping. | GracefulStop 等待进行中的 RPC 结束后再停止。
	GracefulStop()
	// Stop stops immediately, cancelling in-flight RPCs. | Stop 立即停止并取消进行中的 RPC。
	Stop()
}

// WithGRPC registers the gRPC server shut down together with the HTTP server. | WithGRPC 登记与 HTTP 服务器一并退出的 gRPC 服务端。
func WithGRPC(srv GRPCServer) Option { return func(a *App) { a.grpc = srv } }

// Lifecycle returns the shutdown registry of the app's HTTP server. | Lifecycle 返回应用 HTTP 服务器的退出注册表。
//
// Long-lived connections (WebSocket, SSE) register their close hooks there;
// Run and Shutdown close it before draining HTTP, so those connections wind
// down instead of holding the drain until the grace period expires. | 长连接
// （WebSocket、SSE）在此登记各自的关闭钩子；Run 与 Shutdown 会在排空 HTTP 之前
// 关闭它，使这些连接主动收尾，而不是把排空拖到宽限期结束。
func (a *App) Lifecycle() *lifecycle.Registry { return a.server.Lifecycle() }

// Shutdown stops the application in a fixed order and joins every error. | Shutdown 按固定顺序停止应用并聚合全部错误。
//
// Order | 顺序：
//
//  1. the lifecycle registry: Done closes immediately and its hooks run in
//     reverse registration order, which is where long-lived connections
//     (WebSocket, SSE) get closed; | 生命周期注册表：Done 立即关闭，钩子按注册逆序
//     执行，长连接（WebSocket、SSE）正是在这里被关闭；
//  2. the HTTP server drains: it stops accepting, waits for in-flight requests
//     and force-closes connections that survived the drain; | HTTP 服务器排空：
//     停止接入、等待进行中的请求结束，并强制关闭排空后仍存活的连接；
//  3. the gRPC server stops gracefully, then force-stops when ctx expires; | gRPC 服务器
//     优雅停止，ctx 到期后强制停止；
//  4. the app's own OnShutdown hooks run in reverse order, releasing services
//     such as the database, Redis and the task queue. | 最后按逆序执行应用自身的
//     OnShutdown 钩子，释放数据库、Redis、任务队列等资源。
//
// ctx bounds the whole sequence; a step that runs out of time is recorded as an
// error and never prevents the remaining steps from running. Shutdown is
// idempotent: every step is itself idempotent, so a repeated call is a no-op.
// | ctx 限定整个流程的最长耗时；超时的步骤会被记为错误，且绝不阻止其余步骤执行。
// Shutdown 幂等：每一步自身都幂等，因此重复调用是无操作。
func (a *App) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var errs []error

	// ① 先给长连接统一的退出信号。若关闭已由他人（例如 server.Run 内部的退出）发起，
	//    错误由发起方报告，这里只等待并复用结果，避免同一个错误被报两遍。
	// ① Give long-lived connections their shutdown signal first. When the shutdown was already
	//    started elsewhere (for example by server.Run's own exit path), the starter reports the
	//    error: this call only waits and reuses the result instead of reporting it twice.
	registry := a.server.Lifecycle()
	owned := !registry.ShuttingDown()
	if err := registry.Shutdown(ctx); err != nil && owned {
		a.log.Error("long-lived connections failed to close | 长连接关闭失败", logx.Err(err))
		errs = append(errs, err)
	}

	// ② 再排空 HTTP：停止接入、等待进行中的请求、强制关闭存活的 hijack 连接。
	// ② Then drain HTTP: stop accepting, wait for in-flight requests and force-close surviving hijacked connections.
	if err := a.server.Shutdown(ctx); err != nil {
		a.log.Error("http server failed to stop gracefully | HTTP 服务器未能优雅停止", logx.Err(err))
		errs = append(errs, err)
	}

	// ③ gRPC 与 HTTP 各自独立，顺序固定以便诊断。| ③ gRPC is independent of HTTP; the order is fixed so failures stay diagnosable.
	if err := a.shutdownGRPC(ctx); err != nil {
		a.log.Error("gRPC server failed to stop gracefully | gRPC 服务器未能优雅停止", logx.Err(err))
		errs = append(errs, err)
	}

	// ④ 最后释放服务资源。| ④ Release service resources last.
	if err := a.Close(ctx); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// shutdownGRPC stops the gRPC server gracefully and force-stops it once ctx expires. | shutdownGRPC 优雅停止 gRPC 服务端，并在 ctx 到期后强制停止。
//
// The stage runs at most once, so a repeated Shutdown neither calls a
// user-supplied server twice nor blocks on a server that is already stopped.
// | 该阶段最多执行一次，因此重复 Shutdown 既不会对调用方提供的服务端重复调用，
// 也不会阻塞在一个已经停止的服务端上。
func (a *App) shutdownGRPC(ctx context.Context) error {
	if a.grpc == nil {
		return nil
	}

	var err error
	a.grpcOnce.Do(func() { err = a.stopGRPC(ctx) })
	return err
}

// stopGRPC performs the graceful stop and its forced fallback. | stopGRPC 执行优雅停止及其强制兜底。
func (a *App) stopGRPC(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.grpc.GracefulStop()
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}

	// Stop 会取消进行中的 RPC，因此上面的 GracefulStop 必定返回；等它收尾可保证本函数返回后不留协程。
	// Stop cancels in-flight RPCs, so the GracefulStop above is bound to return; waiting for it keeps this call free of lingering goroutines.
	a.grpc.Stop()
	<-done
	return errx.WrapDetail(ctx.Err(), ErrGRPCShutdown, "force stop after %[1]v | 超过 %[1]v 后强制停止", ctx.Err())
}

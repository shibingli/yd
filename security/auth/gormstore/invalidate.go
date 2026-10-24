package gormstore

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/shibingli/ydx/cache"
	ydlog "github.com/shibingli/ydx/log"
)

// Invalidator invalidates table generations after a transaction commits. The
// gormcache plugin implements it; another cache can implement the same contract. |
// Invalidator 在事务提交后使表代际失效。gormcache 插件实现了它，其它缓存也可以实现同一契约。
type Invalidator interface {
	// Invalidate bumps generations for the named tables. | Invalidate 推进指定表的代际。
	Invalidate(ctx context.Context, tables ...string) error
}

// invalidationTimeout bounds the post-commit publish so a cache outage never
// stalls a completed write. | invalidationTimeout 限制提交后的发布，使缓存故障不会拖住已经完成的
// 写入。
const invalidationTimeout = 3 * time.Second

// SetInvalidator attaches the post-commit invalidator. Call it during assembly,
// before serving requests. A nil invalidator disables the post-commit step. |
// SetInvalidator 绑定提交后失效器。应在装配期、开始服务前调用；nil 表示关闭提交后失效。
func (s *Store) SetInvalidator(inv Invalidator) {
	if s == nil {
		return
	}
	s.invalidatorMu.Lock()
	s.invalidator = inv
	s.invalidatorMu.Unlock()
}

// transaction runs fn in a transaction and invalidates tables after a successful
// commit. Write callbacks are marked deferred so they do not bump generations
// before commit, which would let a concurrent read cache stale rows under the new
// generation. | transaction 在事务中执行 fn，并在成功提交后使表失效。写回调被标记为延迟失效，
// 因此不会在提交前推进代际——否则并发读会把陈旧行缓存到新代际下。
func (s *Store) transaction(ctx context.Context, tables []string, fn func(*gorm.DB) error) error {
	// Only defer when an invalidator is actually attached; otherwise the plugin's
	// write callbacks stay responsible for invalidation, which is safer than
	// deferring to a step that will never run. | 只有确实绑定了失效器时才延迟；否则仍由
	// 插件写回调负责失效——把失效推迟给一个永远不会执行的步骤更危险。
	s.invalidatorMu.RLock()
	hasInvalidator := s.invalidator != nil
	s.invalidatorMu.RUnlock()
	txCtx := ctx
	if hasInvalidator {
		txCtx = cache.WithDeferredInvalidation(ctx)
	}
	if err := s.db.WithContext(txCtx).Transaction(fn); err != nil {
		return err
	}
	s.invalidate(ctx, tables...)
	return nil
}

// invalidate runs the late-bound invalidator with a context detached from the
// request, so a response that is already being written cannot cancel it. |
// invalidate 用与请求解绑的上下文运行后置失效器，已开始写出的响应不会取消它。
func (s *Store) invalidate(ctx context.Context, tables ...string) {
	s.invalidatorMu.RLock()
	inv := s.invalidator
	s.invalidatorMu.RUnlock()
	if inv == nil || len(tables) == 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	invCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), invalidationTimeout)
	defer cancel()
	if err := inv.Invalidate(invCtx, tables...); err != nil {
		s.log.Warn("auth cache invalidation failed | 鉴权缓存失效失败",
			ydlog.Err(err),
			ydlog.Strs("tables", tables),
		)
	}
}

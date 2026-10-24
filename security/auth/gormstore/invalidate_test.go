package gormstore

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/shibingli/ydx/cache"
)

// recordingInvalidator records post-commit invalidations. | recordingInvalidator 记录提交后失效。
type recordingInvalidator struct {
	mu     sync.Mutex
	tables []string
	err    error
}

func (r *recordingInvalidator) Invalidate(ctx context.Context, tables ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tables = append(r.tables, tables...)
	r.err = ctx.Err()
	return nil
}

func (r *recordingInvalidator) snapshot() ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tables...), r.err
}

// TestTransactionInvalidatesAfterCommit proves a successful transaction marks the
// context deferred and notifies the invalidator with every affected table. |
// TestTransactionInvalidatesAfterCommit 证明成功事务把上下文标记为延迟失效，并把全部受影响表
// 通知失效器。
func TestTransactionInvalidatesAfterCommit(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "auth.db"))
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store, err := New(db, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inv := &recordingInvalidator{}
	store.SetInvalidator(inv)

	deferred := false
	if err := store.transaction(context.Background(), []string{tableTenants, tableUsers}, func(tx *gorm.DB) error {
		deferred = cache.DeferredInvalidation(tx.Statement.Context)
		return tx.Create(&tenantRow{
			ID: "tn_1", Slug: "demo", SlugKey: "demo", Name: "Demo", Status: "active",
		}).Error
	}); err != nil {
		t.Fatalf("transaction: %v", err)
	}
	if !deferred {
		t.Fatal("transaction did not carry the deferred marker | 事务没有携带延迟失效标记")
	}
	tables, ctxErr := inv.snapshot()
	if ctxErr != nil {
		t.Fatalf("invalidator context error = %v | 失效器上下文错误 = %v", ctxErr, ctxErr)
	}
	if len(tables) != 2 || tables[0] != tableTenants || tables[1] != tableUsers {
		t.Fatalf("invalidated tables = %v | 失效表 = %v", tables, tables)
	}
}

// TestTransactionSkipsInvalidationOnRollback proves a failed transaction never
// invalidates. | TestTransactionSkipsInvalidationOnRollback 证明失败事务不会触发失效。
func TestTransactionSkipsInvalidationOnRollback(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "auth.db"))
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store, err := New(db, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inv := &recordingInvalidator{}
	store.SetInvalidator(inv)
	boom := errors.New("boom | 爆炸")
	if err := store.transaction(context.Background(), []string{tableTenants}, func(*gorm.DB) error {
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("transaction error = %v, want boom", err)
	}
	if tables, _ := inv.snapshot(); len(tables) != 0 {
		t.Fatalf("rollback invalidated %v | 回滚触发了失效 %v", tables, tables)
	}
}

// TestTransactionWithoutInvalidatorDoesNotDefer proves a store without an
// invalidator leaves invalidation to the plugin's write callbacks instead of
// deferring to a step that will never run. | TestTransactionWithoutInvalidatorDoesNotDefer
// 证明没有失效器的存储会让插件的写回调负责失效，而不是推迟给一个永远不会执行的步骤。
func TestTransactionWithoutInvalidatorDoesNotDefer(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "auth.db"))
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store, err := New(db, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	deferred := true
	if err := store.transaction(context.Background(), []string{tableTenants}, func(tx *gorm.DB) error {
		deferred = cache.DeferredInvalidation(tx.Statement.Context)
		return nil
	}); err != nil {
		t.Fatalf("transaction: %v", err)
	}
	if deferred {
		t.Fatal("transaction deferred without an invalidator | 没有失效器却设置了延迟失效")
	}
}

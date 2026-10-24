package gormstore

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/shibingli/ydx/cache"
)

// recordingInvalidator records message table invalidations. | recordingInvalidator 记录消息表失效。
type recordingInvalidator struct {
	mu     sync.Mutex
	tables []string
}

func (r *recordingInvalidator) Invalidate(_ context.Context, tables ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tables = append(r.tables, tables...)
	return nil
}

func (r *recordingInvalidator) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tables...)
}

// TestMessageTransactionInvalidatesAfterCommit proves a message transaction marks
// the context deferred and invalidates every affected table only after success. |
// TestMessageTransactionInvalidatesAfterCommit 证明消息事务把上下文标记为延迟失效，并且只在成功
// 后失效全部受影响表。
func TestMessageTransactionInvalidatesAfterCommit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "msg.db"))+"?_pragma=busy_timeout(5000)"),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inv := &recordingInvalidator{}
	store.SetInvalidator(inv)

	deferred := false
	tables := []string{"yd_msg_messages", "yd_msg_message_tags"}
	if err := store.transaction(context.Background(), tables, func(tx *gorm.DB) error {
		deferred = cache.DeferredInvalidation(tx.Statement.Context)
		return nil
	}); err != nil {
		t.Fatalf("transaction: %v", err)
	}
	if !deferred {
		t.Fatal("message transaction did not carry the deferred marker | 消息事务没有携带延迟失效标记")
	}
	got := inv.snapshot()
	if len(got) != len(tables) || got[0] != tables[0] || got[1] != tables[1] {
		t.Fatalf("invalidated tables = %v | 失效表 = %v", got, got)
	}
}

// TestMessageTransactionSkipsInvalidationOnRollback proves a failed message
// transaction never invalidates. | TestMessageTransactionSkipsInvalidationOnRollback
// 证明失败的消息事务不会触发失效。
func TestMessageTransactionSkipsInvalidationOnRollback(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "msg.db"))+"?_pragma=busy_timeout(5000)"),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	inv := &recordingInvalidator{}
	store.SetInvalidator(inv)
	boom := errors.New("boom | 爆炸")
	if err := store.transaction(context.Background(), []string{"yd_msg_messages"}, func(*gorm.DB) error {
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("transaction error = %v, want boom", err)
	}
	if got := inv.snapshot(); len(got) != 0 {
		t.Fatalf("rollback invalidated %v | 回滚触发了失效 %v", got, got)
	}
}

// TestMessageTransactionWithoutInvalidatorDoesNotDefer proves the message store
// leaves invalidation to the plugin when no invalidator is attached. |
// TestMessageTransactionWithoutInvalidatorDoesNotDefer 证明未绑定失效器时消息存储把失效交给插件。
func TestMessageTransactionWithoutInvalidatorDoesNotDefer(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "msg.db"))+"?_pragma=busy_timeout(5000)"),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store, err := New(db)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	deferred := true
	if err := store.transaction(context.Background(), []string{"yd_msg_messages"}, func(tx *gorm.DB) error {
		deferred = cache.DeferredInvalidation(tx.Statement.Context)
		return nil
	}); err != nil {
		t.Fatalf("transaction: %v", err)
	}
	if deferred {
		t.Fatal("message transaction deferred without an invalidator | 没有失效器却设置了延迟失效")
	}
}

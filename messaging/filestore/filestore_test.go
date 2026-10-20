package filestore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shibingli/yd/messaging/filestore"
)

// TestStoreRoundTripAndTraversal proves bytes round-trip and unsafe keys are refused. |
// TestStoreRoundTripAndTraversal 证明字节可往返且不安全键被拒绝。
func TestStoreRoundTripAndTraversal(t *testing.T) {
	store, err := filestore.New(t.TempDir())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	key := "tenant/att_123"
	if err := store.Put(ctx, key, []byte("payload")); err != nil {
		t.Fatalf("put: %v", err)
	}
	data, err := store.Get(ctx, key)
	if err != nil || string(data) != "payload" {
		t.Fatalf("get = %q err=%v | 读取 = %q err=%v", data, err, data, err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.Get(ctx, key); err == nil {
		t.Fatal("deleted key still readable | 已删除的键仍可读取")
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
	for _, bad := range []string{"../escape", "tenant/../../escape", "/absolute", "tenant//empty", ""} {
		if err := store.Put(ctx, bad, []byte("x")); !errors.Is(err, filestore.ErrKeyInvalid) {
			t.Fatalf("key %q err = %v, want ErrKeyInvalid | 键 %q err = %v，期望 ErrKeyInvalid", bad, err, bad, err)
		}
	}
}

// TestNewRejectsEmptyRoot refuses an empty root. | TestNewRejectsEmptyRoot 拒绝空根目录。
func TestNewRejectsEmptyRoot(t *testing.T) {
	if _, err := filestore.New("   "); !errors.Is(err, filestore.ErrRootInvalid) {
		t.Fatalf("err = %v, want ErrRootInvalid | err = %v，期望 ErrRootInvalid", err, err)
	}
}

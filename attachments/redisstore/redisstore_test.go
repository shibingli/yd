package redisstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/shibingli/yd/messaging/redisstore"
)

// newStore returns a store backed by a disposable miniredis. | newStore 返回由一次性 miniredis 支撑的存储。
func newStore(t *testing.T) (*redisstore.Store, redis.UniversalClient) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store, err := redisstore.New(client, "yd:test:blob:")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store, client
}

// TestStoreRoundTrip stores, reads and deletes bytes under the partitioned key. | TestStoreRoundTrip
// 在分区键下保存、读取并删除字节。
func TestStoreRoundTrip(t *testing.T) {
	store, client := newStore(t)
	ctx := context.Background()
	key := "0123456789abcdef/fedcba9876543210/2026/10/27/att_1"
	if err := store.Put(ctx, key, []byte("payload")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("get = %q", got)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("second delete must be idempotent: %v", err)
	}
	if _, err := store.Get(ctx, key); !errors.Is(err, redisstore.ErrRead) {
		t.Fatalf("missing get err = %v", err)
	}
	if n := client.Exists(ctx, "yd:test:blob:"+key).Val(); n != 0 {
		t.Fatalf("key still present: %d", n)
	}
}

// TestStoreRejectsUnsafeKeys prevents traversal and empty segments from reaching Redis. |
// TestStoreRejectsUnsafeKeys 阻止穿越与空段进入 Redis。
func TestStoreRejectsUnsafeKeys(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	for _, key := range []string{"", " ", "a//b", "../x", "a/../b", "a b", "a\nb"} {
		if err := store.Put(ctx, key, []byte("x")); !errors.Is(err, redisstore.ErrKeyInvalid) {
			t.Fatalf("put %q err = %v", key, err)
		}
	}
}

// TestNewValidatesArguments keeps an empty client and a malformed prefix from constructing a store. |
// TestNewValidatesArguments 阻止空客户端与非法前缀构造存储。
func TestNewValidatesArguments(t *testing.T) {
	if _, err := redisstore.New(nil, ""); !errors.Is(err, redisstore.ErrClientInvalid) {
		t.Fatalf("nil client err = %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = client.Close() })
	if _, err := redisstore.New(client, "bad prefix"); !errors.Is(err, redisstore.ErrPrefixInvalid) {
		t.Fatalf("bad prefix err = %v", err)
	}
	store, err := redisstore.New(client, " ")
	if err != nil {
		t.Fatalf("empty prefix should fall back: %v", err)
	}
	if store.Prefix() != redisstore.DefaultPrefix {
		t.Fatalf("prefix = %q", store.Prefix())
	}
}

package cachex

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newRedisTest builds a miniredis-backed remote. | newRedisTest 构造基于 miniredis 的远端。
func newRedisTest(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient, *RedisRemote) {
	t.Helper()
	mr := miniredis.RunT(t)
	cli := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cli.Close() })
	remote, err := NewRedisRemote(cli, RedisOptions{Prefix: "test:", HealthInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewRedisRemote: %v | 构造远端失败", err)
	}
	return mr, cli, remote.(*RedisRemote)
}

func TestRedisLoadStoreRoundTrip(t *testing.T) {
	_, _, rr := newRedisTest(t)
	ctx := context.Background()
	v1, err := rr.Store(ctx, "k", []byte("v1"), FlagNone, 0)
	if err != nil {
		t.Fatalf("Store: %v | 写入失败", err)
	}
	if v1 != 1 {
		t.Fatalf("first version = %d, want 1 | 首个版本不符", v1)
	}
	item, ok, err := rr.Load(ctx, "k")
	if err != nil || !ok {
		t.Fatalf("Load = %v,%v | 读取失败", ok, err)
	}
	version, flags, data, valid := WireDecode(item.Raw)
	if !valid || version != 1 || flags != FlagNone || string(data) != "v1" {
		t.Fatalf("decoded = %d,%d,%q,%v | 解码结果不符", version, flags, data, valid)
	}
	v2, err := rr.Store(ctx, "k", []byte("v2"), FlagNone, 0)
	if err != nil || v2 != 2 {
		t.Fatalf("second Store = %d,%v | 第二次写入不符", v2, err)
	}
	if _, ok, _ := rr.Load(ctx, "missing"); ok {
		t.Fatal("missing key reported as found | 缺失键被报告为存在")
	}
}

func TestRedisTTL(t *testing.T) {
	mr, _, rr := newRedisTest(t)
	ctx := context.Background()
	if _, err := rr.Store(ctx, "k", []byte("v"), FlagNone, time.Minute); err != nil {
		t.Fatalf("Store: %v | 写入失败", err)
	}
	item, ok, err := rr.Load(ctx, "k")
	if err != nil || !ok || item.TTL <= 0 {
		t.Fatalf("Load = %v,%v,ttl=%v | 读取或 TTL 不符", ok, err, item.TTL)
	}
	mr.FastForward(2 * time.Minute)
	if _, ok, _ := rr.Load(ctx, "k"); ok {
		t.Fatal("expired key still present | 过期键仍存在")
	}
}

func TestRedisCAS(t *testing.T) {
	_, _, rr := newRedisTest(t)
	ctx := context.Background()
	if v, stored, err := rr.StoreIf(ctx, "k", []byte("v1"), FlagNone, 0, 0); err != nil || !stored || v != 1 {
		t.Fatalf("CAS on missing = %d,%v,%v | 缺失键 CAS 不符", v, stored, err)
	}
	if v, stored, err := rr.StoreIf(ctx, "k", []byte("v2"), FlagNone, 0, 0); err != nil || stored || v != 0 {
		t.Fatalf("stale CAS = %d,%v,%v, want rejected | 陈旧 CAS 未被拒绝", v, stored, err)
	}
	if v, stored, err := rr.StoreIf(ctx, "k", []byte("v2"), FlagNone, 0, 1); err != nil || !stored || v != 2 {
		t.Fatalf("valid CAS = %d,%v,%v | 合法 CAS 不符", v, stored, err)
	}
}

func TestRedisRemoveAndFlags(t *testing.T) {
	_, _, rr := newRedisTest(t)
	ctx := context.Background()
	if _, err := rr.Store(ctx, "k", []byte("v"), FlagNone, 0); err != nil {
		t.Fatalf("Store: %v | 写入失败", err)
	}
	tomb, err := rr.Remove(ctx, "k", FlagTombstone, time.Minute)
	if err != nil || tomb != 2 {
		t.Fatalf("Remove = %d,%v | 删除标记不符", tomb, err)
	}
	item, ok, _ := rr.Load(ctx, "k")
	if !ok {
		t.Fatal("tombstone disappeared | 墓碑消失")
	}
	if _, flags, data, valid := WireDecode(item.Raw); !valid || flags != FlagTombstone || len(data) != 0 {
		t.Fatalf("tombstone decode = flags %d data %q valid %v | 墓碑解码不符", flags, data, valid)
	}
	neg, err := rr.Remove(ctx, "k", FlagNegative, time.Second)
	if err != nil || neg != 3 {
		t.Fatalf("negative Remove = %d,%v | 负缓存标记不符", neg, err)
	}
	item, _, _ = rr.Load(ctx, "k")
	if _, flags, _, _ := WireDecode(item.Raw); flags != FlagNegative {
		t.Fatalf("negative flags = %d | 负缓存标志不符", flags)
	}
}

func TestRedisLoadMany(t *testing.T) {
	_, _, rr := newRedisTest(t)
	ctx := context.Background()
	for _, k := range []string{"a", "b"} {
		if _, err := rr.Store(ctx, k, []byte(k), FlagNone, 0); err != nil {
			t.Fatalf("Store: %v | 写入失败", err)
		}
	}
	got, err := rr.LoadMany(ctx, []string{"a", "b", "a", "missing"})
	if err != nil {
		t.Fatalf("LoadMany: %v | 批量读取失败", err)
	}
	if len(got) != 2 {
		t.Fatalf("LoadMany returned %d keys, want 2 | 批量读取数量不符", len(got))
	}
	for _, k := range []string{"a", "b"} {
		item, ok := got[k]
		if !ok {
			t.Fatalf("missing key %q | 缺少键 %q", k, k)
		}
		if _, _, data, _ := WireDecode(item.Raw); string(data) != k {
			t.Fatalf("key %q data = %q | 键值不符", k, data)
		}
	}
}

func TestRedisPublishSubscribe(t *testing.T) {
	_, _, rr := newRedisTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 8)
	done := make(chan error, 1)
	go func() { done <- rr.Subscribe(ctx, func(ev Event) { events <- ev }) }()
	want := Event{Op: OpSet, Namespace: "ns", Keys: []string{"ns:k"}, Source: "node-1", At: time.Now().UTC()}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case got := <-events:
			if got.Op != OpSet || got.Namespace != "ns" || len(got.Keys) != 1 || got.Keys[0] != "ns:k" || got.Source != "node-1" {
				t.Fatalf("event = %+v | 事件内容不符", got)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for a published event | 等待发布事件超时")
		case <-time.After(20 * time.Millisecond):
			_ = rr.Publish(ctx, want)
		}
	}
}

func TestRedisCorruptPayloadIsReplaced(t *testing.T) {
	_, cli, rr := newRedisTest(t)
	m, err := NewManager(WithRemote(rr), WithLocalOptions(testLocal()), WithLogger(testLogger()))
	if err != nil {
		t.Fatalf("NewManager: %v | 构造管理器失败", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	r, _ := m.Raw("ns")
	ctx := context.Background()
	if err := cli.Set(ctx, "test:ns:bad", "garbage", 0).Err(); err != nil {
		t.Fatalf("seed: %v | 播种失败", err)
	}
	if _, ok, err := r.Get(ctx, "bad"); err != nil || ok {
		t.Fatalf("corrupt Get = %v,%v | 损坏载荷读取结果不符", ok, err)
	}
	raw, err := cli.Get(ctx, "test:ns:bad").Bytes()
	if err != nil {
		t.Fatalf("Get after repair: %v | 修复后读取失败", err)
	}
	if _, flags, _, valid := WireDecode(raw); !valid || flags != FlagTombstone {
		t.Fatalf("corrupt entry was not replaced by a tombstone: %q | 损坏条目未被墓碑替换", raw)
	}
}

func TestRedisNilClient(t *testing.T) {
	if _, err := NewRedisRemote(nil, RedisOptions{}); !errors.Is(err, ErrNilClient) {
		t.Fatalf("nil client error = %v | nil 客户端错误不符", err)
	}
}

func TestRedisCloseStopsSubscribe(t *testing.T) {
	_, _, rr := newRedisTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- rr.Subscribe(ctx, func(Event) {}) }()
	time.Sleep(20 * time.Millisecond)
	if err := rr.Close(); err != nil {
		t.Fatalf("Close: %v | 关闭失败", err)
	}
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, ErrClosed) && !errors.Is(err, ErrSubscribe) {
			t.Fatalf("Subscribe returned %v | 订阅返回错误不符", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe did not stop after Close | Close 后订阅未停止")
	}
}

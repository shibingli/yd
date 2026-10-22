package cachex

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/shibingli/yd/core/logx"
	"github.com/shibingli/yd/data/cachex/local"
)

// benchManager builds a manager with a small deterministic L1. | benchManager 构造小型确定性 L1 的管理器。
func benchManager(b *testing.B, remote Remote) *Manager {
	b.Helper()
	m, err := NewManager(
		WithRemote(remote),
		WithLocalOptions(local.Options{MaxCost: 64 << 20, Shards: 8, MaxEntrySize: 1 << 20, CleanupInterval: time.Hour}),
		WithLogger(logx.New(logx.Options{Writer: io.Discard, Level: logx.ErrorLevel})),
		WithLease(time.Minute),
	)
	if err != nil {
		b.Fatalf("NewManager: %v | 构造管理器失败", err)
	}
	b.Cleanup(func() { _ = m.Close() })
	return m
}

// benchKeys precomputes keys so formatting does not pollute the measurement. | benchKeys 预生成键，避免格式化污染测量。
func benchKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("key:%05d", i)
	}
	return keys
}

func BenchmarkRawGet(b *testing.B) {
	ctx := context.Background()
	m := benchManager(b, nil)
	r, _ := m.Raw("bench")
	keys := benchKeys(4096)
	for _, k := range keys {
		if err := r.Set(ctx, k, []byte("0123456789abcdef0123456789abcdef")); err != nil {
			b.Fatalf("Set: %v | 写入失败", err)
		}
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		_, _, _ = r.Get(ctx, keys[i&4095])
		i++
	}
}

func BenchmarkRawView(b *testing.B) {
	ctx := context.Background()
	m := benchManager(b, nil)
	r, _ := m.Raw("bench")
	keys := benchKeys(4096)
	for _, k := range keys {
		if err := r.Set(ctx, k, []byte("0123456789abcdef0123456789abcdef")); err != nil {
			b.Fatalf("Set: %v | 写入失败", err)
		}
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		_, _ = r.View(ctx, keys[i&4095], func([]byte) error { return nil })
		i++
	}
}

func BenchmarkRawSet(b *testing.B) {
	ctx := context.Background()
	m := benchManager(b, nil)
	r, _ := m.Raw("bench")
	keys := benchKeys(4096)
	value := []byte("0123456789abcdef0123456789abcdef")
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if err := r.Set(ctx, keys[i&4095], value); err != nil {
			b.Fatalf("Set: %v | 写入失败", err)
		}
		i++
	}
}

func BenchmarkRawGetTwoLevelL1Hit(b *testing.B) {
	ctx := context.Background()
	m := benchManager(b, newFakeRemote())
	r, _ := m.Raw("bench")
	keys := benchKeys(4096)
	for _, k := range keys {
		if err := r.Set(ctx, k, []byte("0123456789abcdef0123456789abcdef")); err != nil {
			b.Fatalf("Set: %v | 写入失败", err)
		}
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		_, _, _ = r.Get(ctx, keys[i&4095])
		i++
	}
}

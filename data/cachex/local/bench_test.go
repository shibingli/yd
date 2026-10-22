package local

import (
	"fmt"
	"testing"
	"time"
)

// benchCache builds a cache large enough to avoid eviction during read benchmarks. | benchCache 构造足够大的缓存，避免读基准期间发生驱逐。
func benchCache(b *testing.B) *Cache {
	b.Helper()
	c, err := New(Options{MaxCost: 64 << 20, Shards: 8, MaxEntrySize: 1 << 20, CleanupInterval: time.Hour})
	if err != nil {
		b.Fatalf("New: %v | 构造失败", err)
	}
	b.Cleanup(func() { _ = c.Close() })
	for i := 0; i < 10000; i++ {
		if err := c.Set(fmt.Sprintf("key:%05d", i), []byte("0123456789abcdef0123456789abcdef"), 0); err != nil {
			b.Fatalf("Set: %v | 写入失败", err)
		}
	}
	return c
}

func BenchmarkGet(b *testing.B) {
	c := benchCache(b)
	keys := make([]string, 1024)
	for i := range keys {
		keys[i] = fmt.Sprintf("key:%05d", i*7%10000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get(keys[i&1023])
	}
}

func BenchmarkGetAppend(b *testing.B) {
	c := benchCache(b)
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf, _ = c.GetAppend(buf[:0], "key:00001")
	}
}

func BenchmarkView(b *testing.B) {
	c := benchCache(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.View("key:00001", func([]byte) error { return nil })
	}
}

func BenchmarkSet(b *testing.B) {
	c := benchCache(b)
	value := []byte("0123456789abcdef0123456789abcdef")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Set(fmt.Sprintf("bench:%06d", i%4096), value, 0)
	}
}

func BenchmarkGetParallel(b *testing.B) {
	c := benchCache(b)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = c.Get(fmt.Sprintf("key:%05d", i%10000))
			i++
		}
	})
}

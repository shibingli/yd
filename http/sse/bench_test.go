package sse

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// benchWriter is a ResponseWriter + Flusher that only counts bytes, so the benchmarks measure
// event assembly rather than recorder growth. | benchWriter 是只统计字节数的 ResponseWriter +
// Flusher，使基准测量事件组装而不是 recorder 扩容。
type benchWriter struct {
	header http.Header
	n      int
}

func newBenchWriter() *benchWriter { return &benchWriter{header: make(http.Header, 4)} }

// Header implements http.ResponseWriter. | Header 实现 http.ResponseWriter。
func (w *benchWriter) Header() http.Header { return w.header }

// Write implements http.ResponseWriter. | Write 实现 http.ResponseWriter。
func (w *benchWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}

// WriteHeader implements http.ResponseWriter. | WriteHeader 实现 http.ResponseWriter。
func (w *benchWriter) WriteHeader(int) {}

// Flush implements http.Flusher. | Flush 实现 http.Flusher。
func (w *benchWriter) Flush() {}

func benchSSEWriter(b *testing.B, size int) *Writer {
	b.Helper()
	w, err := NewWriter(newBenchWriter(), Options{BufferSize: size})
	if err != nil {
		b.Fatalf("NewWriter failed | NewWriter 失败: %v", err)
	}
	b.Cleanup(func() { _ = w.Close() })
	return w
}

func BenchmarkWriteData(b *testing.B) {
	cases := []struct {
		name string
		data string
	}{
		{name: "small_32B", data: strings.Repeat("a", 32)},
		{name: "large_4KiB", data: strings.Repeat("a", 4<<10)},
		{name: "multiline_1KiB", data: strings.Repeat("line of data\n", 80)},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			w := benchSSEWriter(b, defaultBufferSize)
			b.SetBytes(int64(len(c.data)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := w.WriteData(c.data); err != nil {
					b.Fatalf("WriteData failed | WriteData 失败: %v", err)
				}
			}
		})
	}
}

func BenchmarkWriteEvent(b *testing.B) {
	w := benchSSEWriter(b, defaultBufferSize)
	e := Event{
		ID:      "evt-12345",
		Name:    "message",
		Comment: "keep-alive",
		Data:    []byte(strings.Repeat("payload line\n", 16)),
		Retry:   3_000_000_000,
	}
	b.SetBytes(int64(len(e.Data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := w.WriteEvent(e); err != nil {
			b.Fatalf("WriteEvent failed | WriteEvent 失败: %v", err)
		}
	}
}

func BenchmarkPublish(b *testing.B) {
	for _, n := range []int{1, 64} {
		b.Run("subscribers="+strconv.Itoa(n), func(b *testing.B) {
			br := NewBroker(BrokerOptions{QueueSize: 128})
			defer br.Close()
			chans := make([]<-chan Event, 0, n)
			for i := 0; i < n; i++ {
				ch, cancel := br.Subscribe("topic")
				defer cancel()
				chans = append(chans, ch)
			}
			e := Event{Name: "topic", Data: []byte("payload")}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				br.Publish("topic", e)
				for _, ch := range chans {
					<-ch
				}
			}
		})
	}
}

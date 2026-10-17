package jsonbench

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	gojson "github.com/goccy/go-json"
	jsoniter "github.com/json-iterator/go"

	"github.com/shibingli/yd/core/jsonx"
)

// benchSink keeps results observable. | benchSink 让结果保持可观测。
var benchSink int

// record is the shared payload shape. | record 是共用的载荷结构。
type record struct {
	Created time.Time `json:"created"`
	Tags    []string  `json:"tags"`
	ID      int       `json:"id"`
	Score   float64   `json:"score"`
	Name    string    `json:"name"`
	Email   string    `json:"email"`
	Active  bool      `json:"active"`
}

// jsoniterAPI is the standard-library-compatible jsoniter config. | jsoniterAPI 是与标准库兼容的 jsoniter 配置。
var jsoniterAPI = jsoniter.ConfigCompatibleWithStandardLibrary

// codec is one JSON implementation under comparison. | codec 是参与对比的一个 JSON 实现。
type codec struct {
	name      string
	marshal   func(any) ([]byte, error)
	unmarshal func([]byte, any) error
}

// codecs is the comparison set. | codecs 是参与对比的集合。
var codecs = []codec{
	{name: "stdlib", marshal: json.Marshal, unmarshal: json.Unmarshal},
	{name: "go-json", marshal: gojson.Marshal, unmarshal: gojson.Unmarshal},
	{name: "sonic-std", marshal: sonic.ConfigStd.Marshal, unmarshal: sonic.ConfigStd.Unmarshal},
	{name: "jsoniter-compat", marshal: jsoniterAPI.Marshal, unmarshal: jsoniterAPI.Unmarshal},
	{name: "yd-jsonx", marshal: jsonx.Marshal, unmarshal: jsonx.Unmarshal},
}

// dataset is one record count with its pre-encoded payload. | dataset 是一种记录数量及其预编码载荷。
type dataset struct {
	records []record
	raw     []byte
	name    string
}

// buildDatasets builds the shared payloads. | buildDatasets 构造共用载荷。
func buildDatasets(tb testing.TB) []dataset {
	tb.Helper()
	out := make([]dataset, 0, 3)
	for _, n := range []int{1, 100, 1000} {
		records := make([]record, n)
		for i := range records {
			records[i] = record{
				ID:      i,
				Name:    fmt.Sprintf("user-%d", i),
				Email:   fmt.Sprintf("user%d@example.com", i),
				Active:  i%2 == 0,
				Score:   float64(i) / 3,
				Tags:    []string{"alpha", "beta", "gamma"},
				Created: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
			}
		}
		raw, err := json.Marshal(records)
		if err != nil {
			tb.Fatalf("marshal fixture: %v", err)
		}
		out = append(out, dataset{name: fmt.Sprintf("n=%d", n), records: records, raw: raw})
	}
	return out
}

// BenchmarkMarshal compares JSON encoding across implementations. | BenchmarkMarshal 对比各实现的 JSON 编码。
func BenchmarkMarshal(b *testing.B) {
	for _, ds := range buildDatasets(b) {
		ds := ds
		for _, c := range codecs {
			c := c
			b.Run(ds.name+"/"+c.name, func(b *testing.B) {
				b.SetBytes(int64(len(ds.raw)))
				// Warm up lazily compiled codecs (sonic JIT, go-json caches) so no
				// implementation pays its one-time setup inside the timed loop. | 预热
				// 惰性编译的编解码器（sonic JIT、go-json 缓存），避免任何实现把一次性
				// 初始化成本计入计时循环。
				for i := 0; i < 3; i++ {
					if out, err := c.marshal(ds.records); err != nil {
						b.Fatalf("%s warmup: %v", c.name, err)
					} else {
						benchSink += len(out)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					out, err := c.marshal(ds.records)
					if err != nil {
						b.Fatalf("%s marshal: %v", c.name, err)
					}
					benchSink += len(out)
				}
			})
		}
	}
}

// BenchmarkUnmarshal compares JSON decoding across implementations. | BenchmarkUnmarshal 对比各实现的 JSON 解码。
func BenchmarkUnmarshal(b *testing.B) {
	for _, ds := range buildDatasets(b) {
		ds := ds
		for _, c := range codecs {
			c := c
			b.Run(ds.name+"/"+c.name, func(b *testing.B) {
				b.SetBytes(int64(len(ds.raw)))
				for i := 0; i < 3; i++ {
					var warm []record
					if err := c.unmarshal(ds.raw, &warm); err != nil {
						b.Fatalf("%s warmup: %v", c.name, err)
					}
					benchSink += len(warm)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var out []record
					if err := c.unmarshal(ds.raw, &out); err != nil {
						b.Fatalf("%s unmarshal: %v", c.name, err)
					}
					benchSink += len(out)
				}
			})
		}
	}
}

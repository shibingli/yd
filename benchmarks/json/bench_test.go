package jsonbench

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	gojson "github.com/goccy/go-json"
	jsoniter "github.com/json-iterator/go"

	ydjson "github.com/shibingli/ydx/json"
)

// benchSink keeps results observable. | benchSink 让结果保持可观测。
var benchSink int

// benchSinkAtomic is the concurrency-safe sink used by parallel benchmarks. | benchSinkAtomic
// 是并行基准使用的并发安全 sink。
var benchSinkAtomic atomic.Int64

// benchSelected reports whether name passes the YD_BENCH_ONLY filter. Set it to
// one comma-separated list of codec names to run each implementation in its own
// process and remove same-process ordering and GC bias. | benchSelected 判断
// name 是否通过 YD_BENCH_ONLY 过滤。设为逗号分隔的编解码器名可让每个实现独立
// 进程运行，消除同进程顺序与 GC 偏差。
func benchSelected(name string) bool {
	want := os.Getenv("YD_BENCH_ONLY")
	if want == "" {
		return true
	}
	for _, part := range strings.Split(want, ",") {
		if strings.TrimSpace(part) == name {
			return true
		}
	}
	return false
}

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
	{name: "yd-jsonx", marshal: ydjson.Marshal, unmarshal: ydjson.Unmarshal},
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
			if !benchSelected(c.name) {
				continue
			}
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
			if !benchSelected(c.name) {
				continue
			}
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

// BenchmarkMarshalParallel compares aggregate encoding throughput when every
// parallel goroutine encodes the same dataset independently. | BenchmarkMarshalParallel
// 对比每个并行协程独立编码同一数据集时的聚合吞吐。
func BenchmarkMarshalParallel(b *testing.B) {
	for _, ds := range buildDatasets(b) {
		ds := ds
		for _, c := range codecs {
			c := c
			if !benchSelected(c.name) {
				continue
			}
			b.Run(ds.name+"/"+c.name, func(b *testing.B) {
				b.SetBytes(int64(len(ds.raw)))
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					local := 0
					for pb.Next() {
						out, err := c.marshal(ds.records)
						if err != nil {
							b.Errorf("%s marshal: %v | 编码失败", c.name, err)
							return
						}
						local += len(out)
					}
					benchSinkAtomic.Add(int64(local))
				})
			})
		}
	}
}

// BenchmarkUnmarshalParallel compares aggregate decoding throughput when every
// parallel goroutine decodes into its own destination. | BenchmarkUnmarshalParallel
// 对比每个并行协程解码到各自目标时的聚合吞吐。
func BenchmarkUnmarshalParallel(b *testing.B) {
	for _, ds := range buildDatasets(b) {
		ds := ds
		for _, c := range codecs {
			c := c
			if !benchSelected(c.name) {
				continue
			}
			b.Run(ds.name+"/"+c.name, func(b *testing.B) {
				b.SetBytes(int64(len(ds.raw)))
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					local := 0
					for pb.Next() {
						var out []record
						if err := c.unmarshal(ds.raw, &out); err != nil {
							b.Errorf("%s unmarshal: %v | 解码失败", c.name, err)
							return
						}
						local += len(out)
					}
					benchSinkAtomic.Add(int64(local))
				})
			})
		}
	}
}

// sameRecords compares records semantically, ignoring time.Location identity. | sameRecords
// 按语义比较记录，忽略 time.Location 的指针差异。
func sameRecords(a, b []record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Name != b[i].Name || a[i].Email != b[i].Email ||
			a[i].Active != b[i].Active || a[i].Score != b[i].Score ||
			!slices.Equal(a[i].Tags, b[i].Tags) || !a[i].Created.Equal(b[i].Created) {
			return false
		}
	}
	return true
}

// TestCodecFixtures proves every compared codec produces the same JSON document
// and decodes the shared fixture back to the same records. | TestCodecFixtures
// 证明每个参与对比的编解码器产生相同的 JSON 文档，并能把共用夹具解回相同记录。
func TestCodecFixtures(t *testing.T) {
	for _, ds := range buildDatasets(t) {
		var wantAny any
		if err := json.Unmarshal(ds.raw, &wantAny); err != nil {
			t.Fatalf("%s fixture: %v | 夹具解析失败", ds.name, err)
		}
		for _, c := range codecs {
			out, err := c.marshal(ds.records)
			if err != nil {
				t.Fatalf("%s/%s marshal: %v | 编码失败", ds.name, c.name, err)
			}
			var gotAny any
			if err := json.Unmarshal(out, &gotAny); err != nil {
				t.Fatalf("%s/%s output is not valid JSON: %v | 输出不是合法 JSON", ds.name, c.name, err)
			}
			if !reflect.DeepEqual(gotAny, wantAny) {
				t.Fatalf("%s/%s output differs semantically from stdlib | 输出与标准库语义不一致", ds.name, c.name)
			}
			var got []record
			if err := c.unmarshal(ds.raw, &got); err != nil {
				t.Fatalf("%s/%s unmarshal: %v | 解码失败", ds.name, c.name, err)
			}
			if !sameRecords(got, ds.records) {
				t.Fatalf("%s/%s decoded records differ from fixture | 解码记录与夹具不一致", ds.name, c.name)
			}
		}
	}
}

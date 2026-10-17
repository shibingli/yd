package jsonx

import (
	"encoding/json"
	"testing"
)

// benchSinkBytes keeps serialized results observable. | benchSinkBytes 让序列化结果保持可观测。
var benchSinkBytes []byte

// benchUser is the fixed payload shape used by every case. | benchUser 是每个用例共用的固定载荷形态。
type benchUser struct {
	Name   string   `json:"name"`
	Tags   []string `json:"tags"`
	ID     int64    `json:"id"`
	Score  float64  `json:"score"`
	Active bool     `json:"active"`
}

// benchUsers returns n users with repetitive but realistic content. | benchUsers 返回 n 个内容重复但贴近真实的用户。
func benchUsers(n int) []benchUser {
	out := make([]benchUser, n)
	for i := range out {
		out[i] = benchUser{
			ID:     int64(i + 1),
			Name:   "bench-user",
			Active: true,
			Score:  98.5,
			Tags:   []string{"alpha", "beta", "gamma"},
		}
	}
	return out
}

// BenchmarkMarshal compares the adaptive layer with encoding/json. | BenchmarkMarshal 对比自适应层与 encoding/json。
func BenchmarkMarshal(b *testing.B) {
	small := benchUsers(1)[0]
	many := benchUsers(64)
	cases := []struct {
		name string
		v    any
	}{
		{name: "one", v: small},
		{name: "many", v: many},
	}
	for _, c := range cases {
		b.Run(c.name+"/jsonx", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := Marshal(c.v)
				if err != nil {
					b.Fatalf("Marshal failed | Marshal 失败: %v", err)
				}
				benchSinkBytes = out
			}
		})
		b.Run(c.name+"/stdlib", func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := json.Marshal(c.v)
				if err != nil {
					b.Fatalf("Marshal failed | Marshal 失败: %v", err)
				}
				benchSinkBytes = out
			}
		})
	}
}

// BenchmarkUnmarshal compares decoding into a fresh value. | BenchmarkUnmarshal 对比解码到新值。
func BenchmarkUnmarshal(b *testing.B) {
	small := benchUsers(1)[0]
	many := benchUsers(64)
	smallJSON, err := Marshal(small)
	if err != nil {
		b.Fatalf("setup failed | 准备失败: %v", err)
	}
	manyJSON, err := Marshal(many)
	if err != nil {
		b.Fatalf("setup failed | 准备失败: %v", err)
	}

	b.Run("one/jsonx", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var v benchUser
			if err := Unmarshal(smallJSON, &v); err != nil {
				b.Fatalf("Unmarshal failed | Unmarshal 失败: %v", err)
			}
		}
	})
	b.Run("one/stdlib", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var v benchUser
			if err := json.Unmarshal(smallJSON, &v); err != nil {
				b.Fatalf("Unmarshal failed | Unmarshal 失败: %v", err)
			}
		}
	})
	b.Run("many/jsonx", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var v []benchUser
			if err := Unmarshal(manyJSON, &v); err != nil {
				b.Fatalf("Unmarshal failed | Unmarshal 失败: %v", err)
			}
		}
	})
	b.Run("many/stdlib", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var v []benchUser
			if err := json.Unmarshal(manyJSON, &v); err != nil {
				b.Fatalf("Unmarshal failed | Unmarshal 失败: %v", err)
			}
		}
	})
}

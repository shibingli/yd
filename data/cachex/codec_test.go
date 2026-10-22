package cachex

import (
	"testing"
	"time"
)

func TestCodecRoundTrips(t *testing.T) {
	cases := []struct {
		name   string
		encode func() ([]byte, error)
		decode func([]byte) (any, error)
		want   any
	}{
		{
			name:   "int | 整数",
			encode: func() ([]byte, error) { return Int().Encode(-42) },
			decode: func(b []byte) (any, error) { return Int().Decode(b) },
			want:   -42,
		},
		{
			name:   "int64 | 长整数",
			encode: func() ([]byte, error) { return Int64().Encode(1 << 40) },
			decode: func(b []byte) (any, error) { return Int64().Decode(b) },
			want:   int64(1 << 40),
		},
		{
			name:   "uint64 | 无符号长整数",
			encode: func() ([]byte, error) { return Uint64().Encode(1 << 63) },
			decode: func(b []byte) (any, error) { return Uint64().Decode(b) },
			want:   uint64(1 << 63),
		},
		{
			name:   "float64 | 浮点数",
			encode: func() ([]byte, error) { return Float64().Encode(3.5) },
			decode: func(b []byte) (any, error) { return Float64().Decode(b) },
			want:   3.5,
		},
		{
			name:   "bool | 布尔",
			encode: func() ([]byte, error) { return Bool().Encode(true) },
			decode: func(b []byte) (any, error) { return Bool().Decode(b) },
			want:   true,
		},
		{
			name:   "string | 字符串",
			encode: func() ([]byte, error) { return String().Encode("hello") },
			decode: func(b []byte) (any, error) { return String().Decode(b) },
			want:   "hello",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tc.encode()
			if err != nil {
				t.Fatalf("encode: %v | 序列化失败", err)
			}
			got, err := tc.decode(data)
			if err != nil {
				t.Fatalf("decode: %v | 反序列化失败", err)
			}
			if got != tc.want {
				t.Fatalf("round trip = %v, want %v | 往返结果不符", got, tc.want)
			}
		})
	}
}

func TestTimeCodec(t *testing.T) {
	want := time.Date(2026, 10, 21, 12, 34, 56, 789, time.FixedZone("CST", 8*3600))
	data, err := Time().Encode(want)
	if err != nil {
		t.Fatalf("encode: %v | 序列化失败", err)
	}
	got, err := Time().Decode(data)
	if err != nil {
		t.Fatalf("decode: %v | 反序列化失败", err)
	}
	if !got.Equal(want) {
		t.Fatalf("time round trip = %v, want %v | 时间往返结果不符", got, want)
	}
}

func TestCodecRejectsInvalidInput(t *testing.T) {
	if _, err := Int().Decode([]byte("not-a-number")); err == nil {
		t.Fatal("Int accepted invalid input | Int 接受了非法输入")
	}
	if _, err := Uint64().Decode([]byte("-1")); err == nil {
		t.Fatal("Uint64 accepted a negative number | Uint64 接受了负数")
	}
	if _, err := Float64().Decode([]byte("x")); err == nil {
		t.Fatal("Float64 accepted invalid input | Float64 接受了非法输入")
	}
	if _, err := Bool().Decode([]byte("maybe")); err == nil {
		t.Fatal("Bool accepted invalid input | Bool 接受了非法输入")
	}
	if _, err := Time().Decode([]byte("not-a-time")); err == nil {
		t.Fatal("Time accepted invalid input | Time 接受了非法输入")
	}
}

func TestJSONCodec(t *testing.T) {
	type payload struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	codec := JSON[payload]()
	want := payload{Name: "x", Tags: []string{"a", "b"}}
	data, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("encode: %v | 序列化失败", err)
	}
	got, err := codec.Decode(data)
	if err != nil {
		t.Fatalf("decode: %v | 反序列化失败", err)
	}
	if got.Name != want.Name || len(got.Tags) != 2 || got.Tags[0] != "a" {
		t.Fatalf("JSON round trip = %+v | JSON 往返结果不符", got)
	}
}

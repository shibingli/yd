package asynqx_test

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/shibingli/yd/data/asynqx"
)

// TestQueueWeightsParsedFromString asserts the comma-separated queue grammar and its rejections. | TestQueueWeightsParsedFromString 断言逗号分隔的队列语法与各类非法输入。
func TestQueueWeightsParsedFromString(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    map[string]int
		wantErr error
	}{
		{"标准输入", "critical:3,default:2,low:1", map[string]int{"critical": 3, "default": 2, "low": 1}, nil},
		{"含空白", " critical : 3 , default:2 ", map[string]int{"critical": 3, "default": 2}, nil},
		{"单个队列", "default:1", map[string]int{"default": 1}, nil},
		{"空串", "", map[string]int{}, nil},
		{"纯空白", "   ", map[string]int{}, nil},
		{"缺冒号", "critical", nil, asynqx.ErrInvalidQueueSpec},
		{"缺队列名", ":3", nil, asynqx.ErrInvalidQueueSpec},
		{"空权重", "critical:", nil, asynqx.ErrInvalidQueueWeight},
		{"非数字权重", "critical:x", nil, asynqx.ErrInvalidQueueWeight},
		{"零权重", "critical:0", nil, asynqx.ErrInvalidQueueWeight},
		{"负权重", "critical:-2", nil, asynqx.ErrInvalidQueueWeight},
		{"重复队列", "a:1,a:2", nil, asynqx.ErrInvalidQueueSpec},
		{"逗号空段", "a:1,,b:2", nil, asynqx.ErrInvalidQueueSpec},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := asynqx.ParseQueues(c.in)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("ParseQueues(%q) 错误 = %v，期望 errors.Is(err, %v)", c.in, err, c.wantErr)
				}
				if got != nil {
					t.Fatalf("ParseQueues(%q) 返回 %v，期望 nil", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseQueues(%q) 失败: %v", c.in, err)
			}
			if !maps.Equal(got, c.want) {
				t.Fatalf("ParseQueues(%q) = %v，期望 %v", c.in, got, c.want)
			}
		})
	}
}

// TestExponentialBackoffMonotonicAndCapped asserts the delay grows exponentially, stays monotonic under jitter and never exceeds the cap. | TestExponentialBackoffMonotonicAndCapped 断言延迟按指数增长、在抖动下仍单调、且不超过上限。
func TestExponentialBackoffMonotonicAndCapped(t *testing.T) {
	const (
		base = 100 * time.Millisecond
		max  = time.Second
	)
	backoff := asynqx.ExponentialBackoff(base, max)
	task := asynq.NewTask("probe", nil)

	var prev time.Duration
	for n := 1; n <= 8; n++ {
		got := backoff(n, nil, task)
		want := base
		for i := 1; i < n && want < max; i++ {
			want *= 2
		}
		if want > max {
			want = max
		}
		if got < want || got > want+want/10 {
			t.Fatalf("n=%d 延迟 = %v，期望落在 [%v, %v] 内（base*2^(n-1)，含 10%% 抖动）", n, got, want, want+want/10)
		}
		if got < prev {
			t.Fatalf("n=%d 延迟 = %v 小于上一档 %v，单调性被破坏", n, got, prev)
		}
		prev = got
	}

	// Deep retries neither overflow nor exceed the cap. | 高位封顶：即使重试次数极大也不溢出、不超过上限。
	for _, n := range []int{9, 20, 64, 1024} {
		got := backoff(n, nil, task)
		if got < max || got > max+max/10 {
			t.Fatalf("n=%d 延迟 = %v，期望封顶在 [%v, %v]", n, got, max, max+max/10)
		}
	}

	// n <= 0 is treated as the first retry. | n <= 0 视为首次重试。
	if got := backoff(0, nil, task); got < base || got > base+base/10 {
		t.Fatalf("n=0 延迟 = %v，期望首次重试档 [%v, %v]", got, base, base+base/10)
	}

	// Jitter: repeated calls at one step must not all be identical. | 抖动：同一档多次取值不应完全相同。
	seen := make(map[time.Duration]struct{})
	for range 32 {
		seen[backoff(3, nil, task)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("n=3 的 32 次取值只有 %d 种，抖动缺失", len(seen))
	}

	// base > max clamps immediately; base <= 0 yields zero. | base > max 时立即封顶；base <= 0 时返回 0。
	if got := asynqx.ExponentialBackoff(10*time.Second, time.Second)(1, nil, task); got < max || got > max+max/10 {
		t.Fatalf("base>max 延迟 = %v，期望封顶在 %v 附近", got, max)
	}
	if got := asynqx.ExponentialBackoff(0, max)(3, nil, task); got != 0 {
		t.Fatalf("base=0 延迟 = %v，期望 0", got)
	}
}

// TestRegistryDeclarativeLookup asserts registration, chaining and the defensive snapshot. | TestRegistryDeclarativeLookup 断言注册、链式调用与防御性快照。
func TestRegistryDeclarativeLookup(t *testing.T) {
	reg := asynqx.NewRegistry()
	if got := reg.Handlers(); len(got) != 0 {
		t.Fatalf("空注册表 Handlers = %v，期望空", got)
	}

	h := func(context.Context, asynqx.Payload) error { return nil }
	if got := reg.Register("a", h); got != reg {
		t.Fatal("Register did not return the receiver, so chaining breaks | Register 未返回自身，链式调用失效")
	}
	reg.Register("b", h)
	if got := len(reg.Handlers()); got != 2 {
		t.Fatalf("Handlers 数量 = %d，期望 2", got)
	}
	if err := reg.Err(); err != nil {
		t.Fatalf("合法注册后 Err = %v，期望 nil", err)
	}

	// Handlers returns a copy, so caller mutations do not reach the registry. | Handlers 返回副本：调用方改动不影响注册表。
	snapshot := reg.Handlers()
	delete(snapshot, "a")
	snapshot["c"] = h
	if got := len(reg.Handlers()); got != 2 {
		t.Fatalf("改动快照后 Handlers 数量 = %d，期望仍为 2", got)
	}

	// An invalid registration does not panic; it is recorded as a startup error. | 非法注册不 panic，而是记录为启动期错误。
	reg.Register("", h)
	reg.Register("d", nil)
	if err := reg.Err(); !errors.Is(err, asynqx.ErrEmptyTaskType) {
		t.Fatalf("空类型注册 Err = %v，期望 ErrEmptyTaskType", err)
	}
	if got := len(reg.Handlers()); got != 2 {
		t.Fatalf("非法注册后 Handlers 数量 = %d，期望仍为 2", got)
	}
}

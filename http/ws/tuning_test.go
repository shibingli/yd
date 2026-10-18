package ws

import (
	"runtime"
	"testing"

	"github.com/shibingli/yd/core/sysinfo"
)

// TestNormalizeUsesTunedDefaults pins that unset options receive values derived
// from sysinfo and that explicit values are never overridden. | TestNormalizeUsesTunedDefaults
// 固定「未设置的选项取 sysinfo 推导值，显式值不被覆盖」。
func TestNormalizeUsesTunedDefaults(t *testing.T) {
	o := Options{}.normalize()
	if o.ReadBufferSize < defaultReadBufferSize || o.ReadBufferSize > maxTunedBufferSize {
		t.Fatalf("ReadBufferSize = %d, want [%d, %d] | ReadBufferSize = %d，期望 [%d, %d]",
			o.ReadBufferSize, defaultReadBufferSize, maxTunedBufferSize, o.ReadBufferSize, defaultReadBufferSize, maxTunedBufferSize)
	}
	if o.WriteBufferSize < defaultReadBufferSize || o.WriteBufferSize > maxTunedBufferSize {
		t.Fatalf("WriteBufferSize = %d, want [%d, %d] | WriteBufferSize = %d，期望 [%d, %d]",
			o.WriteBufferSize, defaultReadBufferSize, maxTunedBufferSize, o.WriteBufferSize, defaultReadBufferSize, maxTunedBufferSize)
	}
	if o.MaxMessageSize < minTunedMaxMessageSize || o.MaxMessageSize > maxTunedMaxMessageSize {
		t.Fatalf("MaxMessageSize = %d, want [%d, %d] | MaxMessageSize = %d，期望 [%d, %d]",
			o.MaxMessageSize, minTunedMaxMessageSize, maxTunedMaxMessageSize, o.MaxMessageSize, minTunedMaxMessageSize, maxTunedMaxMessageSize)
	}

	const (
		read  = 1234
		write = 5678
		max   = 9 << 20
	)
	explicit := Options{ReadBufferSize: read, WriteBufferSize: write, MaxMessageSize: max}.normalize()
	if explicit.ReadBufferSize != read || explicit.WriteBufferSize != write || explicit.MaxMessageSize != max {
		t.Fatalf("explicit options were overridden: %+v | 显式选项被覆盖：%+v", explicit, explicit)
	}
}

// TestTunedBufferSizeFollowsParallelism pins the GOMAXPROCS tiers and the
// memory-based cap of the per-connection buffer. | TestTunedBufferSizeFollowsParallelism
// 固定每连接缓冲的 GOMAXPROCS 分层与内存上限。
func TestTunedBufferSizeFollowsParallelism(t *testing.T) {
	prev := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(prev)

	limit := sysinfo.MemoryLimit()
	cases := []struct {
		procs int
		want  int
	}{
		{procs: 1, want: 4 << 10},
		{procs: 4, want: 4 << 10},
		{procs: 15, want: 4 << 10},
		{procs: 16, want: 8 << 10},
		{procs: 31, want: 8 << 10},
		{procs: 32, want: 16 << 10},
		{procs: 64, want: 32 << 10},
		{procs: 128, want: 32 << 10},
	}
	for _, tc := range cases {
		runtime.GOMAXPROCS(tc.procs)
		want := tc.want
		if limit > 0 {
			if perP := limit / uint64(tc.procs) / 256; perP < uint64(want) {
				want = int(perP)
				if want < defaultReadBufferSize {
					want = defaultReadBufferSize
				}
			}
		}
		if got := tunedBufferSize(); got != want {
			t.Fatalf("GOMAXPROCS=%d: buffer = %d, want %d | GOMAXPROCS=%d：缓冲 = %d，期望 %d", tc.procs, got, want, tc.procs, got, want)
		}
	}
}

// TestTunedMaxMessageSizeUsesMemoryCeiling pins the memory-derived message cap. | TestTunedMaxMessageSizeUsesMemoryCeiling
// 固定内存推导的单条消息上限。
func TestTunedMaxMessageSizeUsesMemoryCeiling(t *testing.T) {
	limit := sysinfo.MemoryLimit()
	want := int64(maxTunedMaxMessageSize)
	if limit > 0 {
		want = int64(limit / 64)
		if want < minTunedMaxMessageSize {
			want = minTunedMaxMessageSize
		}
		if want > maxTunedMaxMessageSize {
			want = maxTunedMaxMessageSize
		}
	}
	if got := tunedMaxMessageSize(); got != want {
		t.Fatalf("MaxMessageSize = %d, want %d | MaxMessageSize = %d，期望 %d", got, want, got, want)
	}
}

package toolchain

import (
	"os/exec"
	"strings"
	"testing"
)

// godedbugFromEnv returns the single GODEBUG entry in env, or "" when absent or duplicated. | godedbugFromEnv 返回 env 中的唯一 GODEBUG 条目；缺失或重复时返回空串。
func godedbugFromEnv(env []string) string {
	found := ""
	count := 0
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "GODEBUG") {
			found = kv
			count++
		}
	}
	if count != 1 {
		return ""
	}
	return found
}

// TestChildEnvMergesHTTP2ExtendedConnect pins the dev child environment. | TestChildEnvMergesHTTP2ExtendedConnect 固定开发子进程环境。
func TestChildEnvMergesHTTP2ExtendedConnect(t *testing.T) {
	cases := []struct {
		name    string
		godebug string
		enabled bool
		want    string
	}{
		{"missing | 缺失", "", true, "GODEBUG=http2xconnect=1"},
		{"existing | 已有其它项", "gctrace=1", true, "GODEBUG=gctrace=1,http2xconnect=1"},
		{"already present | 已包含", "gctrace=1,http2xconnect=1", true, "GODEBUG=gctrace=1,http2xconnect=1"},
		{"disabled | 未启用", "gctrace=1", false, "GODEBUG=gctrace=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("x")
			cmd.Env = []string{"PATH=/bin"}
			if tc.godebug != "" {
				cmd.Env = append(cmd.Env, "GODEBUG="+tc.godebug)
			}
			env := childEnv(cmd, 0, tc.enabled)
			if got := godedbugFromEnv(env); got != tc.want {
				t.Fatalf("GODEBUG entry = %q, want %q | GODEBUG 条目 = %q，期望 %q", got, tc.want, got, tc.want)
			}
		})
	}
}

// TestChildEnvDoesNotAddGODEBUGWhenDisabled proves the off path is untouched. | TestChildEnvDoesNotAddGODEBUGWhenDisabled 证明关闭时完全不添加。
func TestChildEnvDoesNotAddGODEBUGWhenDisabled(t *testing.T) {
	cmd := exec.Command("x")
	cmd.Env = []string{"PATH=/bin"}
	env := childEnv(cmd, 0, false)
	if got := godedbugFromEnv(env); got != "" {
		t.Fatalf("GODEBUG entry = %q, want none | GODEBUG 条目 = %q，期望不存在", got, got)
	}
}

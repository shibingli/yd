package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/shibingli/yd/runtime/server"
)

// TestIsTestBinary pins the re-exec guard. | TestIsTestBinary 固定重启护栏。
func TestIsTestBinary(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"app.test", true},
		{"app.test.exe", true},
		{"app", false},
		{"app.exe", false},
	}
	for _, tc := range cases {
		if got := isTestBinary(tc.path); got != tc.want {
			t.Errorf("isTestBinary(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestMergeEnvValue pins the environment replacement rules. | TestMergeEnvValue 固定环境变量替换规则。
func TestMergeEnvValue(t *testing.T) {
	got := mergeEnvValue([]string{"A=1"}, "GODEBUG", "http2xconnect=1")
	if !contains(got, "GODEBUG=http2xconnect=1") || !contains(got, "A=1") {
		t.Fatalf("mergeEnvValue = %v", got)
	}
	got = mergeEnvValue([]string{"GODEBUG=gctrace=1", "A=1"}, "GODEBUG", "gctrace=1,http2xconnect=1")
	if contains(got, "GODEBUG=gctrace=1") || !contains(got, "GODEBUG=gctrace=1,http2xconnect=1") {
		t.Fatalf("mergeEnvValue = %v, want the GODEBUG entry replaced | 期望替换 GODEBUG 条目", got)
	}
	got = mergeEnvValue([]string{"godebug=gctrace=1"}, "GODEBUG", "http2xconnect=1")
	if contains(got, "godebug=gctrace=1") {
		t.Fatalf("mergeEnvValue = %v, want case-insensitive replacement | 期望大小写不敏感替换", got)
	}
}

// contains reports whether env has the exact entry. | contains 报告 env 是否包含该精确条目。
func contains(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

// TestEnsureHTTP2ExtendedConnect pins the bootstrap decision table. | TestEnsureHTTP2ExtendedConnect 固定启动引导的决策表。
func TestEnsureHTTP2ExtendedConnect(t *testing.T) {
	original := isTestProcess
	isTestProcess = func() bool { return false }
	t.Cleanup(func() { isTestProcess = original })

	cases := []struct {
		name         string
		opts         server.Options
		godebug      string
		bootstrapped string
		wantCalls    int
		wantErr      error
	}{
		{
			name:      "force disabled | 未强制",
			opts:      server.Options{H2C: true},
			wantCalls: 0,
		},
		{
			name:      "no HTTP/2 surface | 无 HTTP/2 入口",
			opts:      server.Options{ForceHTTP2ExtendedConnect: true},
			wantCalls: 0,
		},
		{
			name:      "already enabled | 已启用",
			opts:      server.Options{H2C: true, ForceHTTP2ExtendedConnect: true},
			godebug:   "http2xconnect=1",
			wantCalls: 0,
		},
		{
			name:      "missing | 缺失",
			opts:      server.Options{H2C: true, ForceHTTP2ExtendedConnect: true},
			wantCalls: 1,
		},
		{
			name:         "loop guard | 防循环",
			opts:         server.Options{H2C: true, ForceHTTP2ExtendedConnect: true},
			bootstrapped: "1",
			wantCalls:    0,
			wantErr:      server.ErrHTTP2ExtendedConnectRequired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GODEBUG", tc.godebug)
			t.Setenv(bootstrapEnv, tc.bootstrapped)

			calls := 0
			var gotEnv []string
			reexec := func(env []string) error {
				calls++
				gotEnv = env
				return nil
			}
			err := ensureHTTP2ExtendedConnect(tc.opts, reexec)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if calls != tc.wantCalls {
				t.Fatalf("reexec calls = %d, want %d", calls, tc.wantCalls)
			}
			if calls > 0 {
				if !contains(gotEnv, "GODEBUG=http2xconnect=1") {
					t.Fatalf("env = %v, want merged GODEBUG | env = %v，期望合并后的 GODEBUG", gotEnv, gotEnv)
				}
				if !contains(gotEnv, bootstrapEnv+"=1") {
					t.Fatalf("env = %v, want the loop-guard sentinel | env = %v，期望防循环哨兵", gotEnv, gotEnv)
				}
			}
		})
	}
}

// TestMergeEnvValueKeepsUnrelatedEntries is a regression guard for accidental drops. | TestMergeEnvValueKeepsUnrelatedEntries 守卫无关条目不被误删。
func TestMergeEnvValueKeepsUnrelatedEntries(t *testing.T) {
	got := mergeEnvValue([]string{"PATH=/bin", "GODEBUG=x=1", "HOME=/root"}, "GODEBUG", "http2xconnect=1")
	if !contains(got, "PATH=/bin") || !contains(got, "HOME=/root") {
		t.Fatalf("unrelated entries dropped: %v | 无关条目被丢弃: %v", got, got)
	}
	if !contains(got, "GODEBUG=http2xconnect=1") {
		t.Fatalf("GODEBUG not merged: %v", got)
	}
	if strings.Count(strings.Join(got, "\n"), "GODEBUG=") != 1 {
		t.Fatalf("duplicate GODEBUG entries: %v | GODEBUG 条目重复: %v", got, got)
	}
}

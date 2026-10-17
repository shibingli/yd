package build

import (
	"errors"
	"reflect"
	"testing"
)

// TestParseTargets covers both accepted and rejected target lists. | TestParseTargets 覆盖可接受与被拒绝的目标列表。
func TestParseTargets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Target
	}{
		{
			name: "multi target",
			in:   "linux/amd64,linux/arm64,windows/amd64",
			want: []Target{
				{GOOS: "linux", GOARCH: "amd64"},
				{GOOS: "linux", GOARCH: "arm64"},
				{GOOS: "windows", GOARCH: "amd64"},
			},
		},
		{
			name: "surrounding whitespace",
			in:   " linux/amd64 ,  darwin/arm64 ",
			want: []Target{
				{GOOS: "linux", GOARCH: "amd64"},
				{GOOS: "darwin", GOARCH: "arm64"},
			},
		},
		{
			name: "upper case is normalized",
			in:   "Windows/AMD64",
			want: []Target{{GOOS: "windows", GOARCH: "amd64"}},
		},
		{
			name: "duplicates collapse",
			in:   "linux/amd64,linux/amd64,linux/amd64",
			want: []Target{{GOOS: "linux", GOARCH: "amd64"}},
		},
		{
			name: "trailing comma is tolerated",
			in:   "linux/amd64,",
			want: []Target{{GOOS: "linux", GOARCH: "amd64"}},
		},
		{
			name: "wasm and loong64",
			in:   "js/wasm,linux/loong64",
			want: []Target{
				{GOOS: "js", GOARCH: "wasm"},
				{GOOS: "linux", GOARCH: "loong64"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTargets(tc.in)
			if err != nil {
				t.Fatalf("ParseTargets(%q) 返回错误: %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseTargets(%q) = %v, 期望 %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseTargetsRejectsInvalidInput pins the sentinel returned for malformed input. | TestParseTargetsRejectsInvalidInput 固定非法输入返回的哨兵错误。
func TestParseTargetsRejectsInvalidInput(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		",",
		"linux",
		"linux/",
		"/amd64",
		"linux/amd64/extra",
		"linux/x86_64",
		"beos/amd64",
		"linux/amd64,broken",
	} {
		t.Run(in, func(t *testing.T) {
			got, err := ParseTargets(in)
			if err == nil {
				t.Fatalf("ParseTargets(%q) = %v, 期望错误", in, got)
			}
			if !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("ParseTargets(%q) 错误 = %v, 期望 errors.Is(err, ErrInvalidTarget)", in, err)
			}
		})
	}
}

// TestTargetString renders a target the way it is spelled on the command line. | TestTargetString 按命令行写法渲染目标平台。
func TestTargetString(t *testing.T) {
	if got := (Target{GOOS: "linux", GOARCH: "arm64"}).String(); got != "linux/arm64" {
		t.Fatalf("Target.String() = %q, 期望 %q", got, "linux/arm64")
	}
}

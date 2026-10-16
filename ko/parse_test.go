// This file tests the parser that picks the image references out of ko's merged output. | 本文件测试从 ko 合并输出中挑出镜像引用的解析器。

package ko

import (
	"slices"
	"strings"
	"testing"
)

// koTestDigest is a well-formed sha256 digest used by the fixtures. | koTestDigest 是夹具使用的合法 sha256 摘要。
var koTestDigest = "sha256:" + strings.Repeat("a", digestHexLen)

// TestParseImages covers the references ko prints and the progress lines that must never be mistaken
// for one. | TestParseImages 覆盖 ko 打印的引用，以及绝不能被误认为引用的进度行。
func TestParseImages(t *testing.T) {
	tests := []struct {
		name string
		logs []string
		want []string
	}{
		{
			name: "tag and digest | 标签与摘要",
			logs: []string{
				"2026/10/17 10:00:00 Building github.com/shibingli/yd/cmd/app for linux/amd64",
				"registry.example.com/team/app:v1.2.3@" + koTestDigest,
			},
			want: []string{"registry.example.com/team/app:v1.2.3@" + koTestDigest},
		},
		{
			name: "digest only when no tag was requested | 未请求标签时只有摘要",
			logs: []string{"registry.example.com/team/app@" + koTestDigest},
			want: []string{"registry.example.com/team/app@" + koTestDigest},
		},
		{
			name: "progress lines are ignored | 进度行被忽略",
			logs: []string{
				"Using base cgr.dev/chainguard/static:latest@" + koTestDigest + " for github.com/shibingli/yd/cmd/app",
				"error: failed to publish images: exit status 1",
				"http://registry.example.com/team/app:v1",
				"running go build -o /tmp/ko1234",
				"2026/10/17 10:00:00",
			},
			want: nil,
		},
		{
			name: "one line per image, order kept, duplicates dropped | 每个镜像一行，保序去重",
			logs: []string{
				"registry.example.com/team/api:v1@" + koTestDigest,
				"registry.example.com/team/worker:v1@" + koTestDigest,
				"registry.example.com/team/api:v1@" + koTestDigest,
			},
			want: []string{
				"registry.example.com/team/api:v1@" + koTestDigest,
				"registry.example.com/team/worker:v1@" + koTestDigest,
			},
		},
		{
			name: "a malformed digest is not a reference | 非法摘要不算引用",
			logs: []string{"registry.example.com/team/app:v1@sha256:short"},
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseImages(tc.logs)
			if !slices.Equal(got, tc.want) {
				t.Errorf("parseImages() = %q | parseImages() 得到 %q，期望 %q", got, got, tc.want)
			}
		})
	}
}

// TestDigestOf checks the digest extraction used to fill Result.Digest. | TestDigestOf 校验用于填充 Result.Digest 的摘要提取。
func TestDigestOf(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		want      string
	}{
		{name: "tag and digest | 标签与摘要", reference: "r/app:v1@" + koTestDigest, want: koTestDigest},
		{name: "digest only | 仅摘要", reference: "r/app@" + koTestDigest, want: koTestDigest},
		{name: "no digest | 无摘要", reference: "r/app:v1"},
		{name: "malformed digest | 非法摘要", reference: "r/app@sha256:xyz"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := digestOf(tc.reference); got != tc.want {
				t.Errorf("digestOf(%q) = %q | digestOf(%q) 得到 %q，期望 %q",
					tc.reference, got, tc.reference, got, tc.want)
			}
		})
	}
}

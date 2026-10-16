// This file tests the argv assembly and the option validation of the ko package. | 本文件测试 ko 包的 argv 组装与选项校验。

package ko

import (
	"errors"
	"slices"
	"testing"
)

// TestArgs pins the argv handed to the ko CLI: the verb, the fixed import-path naming, the always
// explicit push switch, the repeatable tag and platform flags in the order given (deduplicated), and
// the package list last. | TestArgs 固化交给 ko CLI 的 argv：子命令、固定的镜像命名方式、始终显式的推送
// 开关、按给定顺序（已去重）重复的标签与平台标志，以及位于末尾的包列表。
func TestArgs(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "defaults | 缺省",
			opts: Options{},
			want: []string{"build", "--base-import-paths", "--push=false", "./cmd/app"},
		},
		{
			name: "push and local | 推送与本地",
			opts: Options{Repository: "registry.example.com/team/app", Push: true, Local: true},
			want: []string{"build", "--base-import-paths", "--push=true", "--local", "./cmd/app"},
		},
		{
			name: "tags keep their order and drop duplicates | 标签保序去重",
			opts: Options{Tags: []string{" v1.2.3 ", "latest", "v1.2.3", "", "  "}},
			want: []string{
				"build", "--base-import-paths", "--push=false",
				"--tags", "v1.2.3", "--tags", "latest",
				"./cmd/app",
			},
		},
		{
			name: "platforms repeat the flag once per value | 平台逐个值重复标志",
			opts: Options{Platforms: []string{"linux/amd64, linux/arm64", "linux/amd64"}},
			want: []string{
				"build", "--base-import-paths", "--push=false",
				"--platform", "linux/amd64", "--platform", "linux/arm64",
				"./cmd/app",
			},
		},
		{
			name: "packages close the line | 包列表位于末尾",
			opts: Options{Packages: []string{" ./cmd/api ", "./cmd/worker", "./cmd/api"}},
			want: []string{"build", "--base-import-paths", "--push=false", "./cmd/api", "./cmd/worker"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.opts.Args()
			if !slices.Equal(got, tc.want) {
				t.Errorf("Args() = %q | Args() 得到 %q，期望 %q", got, got, tc.want)
			}
		})
	}
}

// TestValidate checks the two option combinations this package refuses before ko is ever started. | TestValidate
// 校验本包在启动 ko 之前就拒绝的两种选项组合。
func TestValidate(t *testing.T) {
	tests := []struct {
		sentinel error
		name     string
		opts     Options
		wantErr  bool
	}{
		{
			name: "local needs no repository | 本地模式无需仓库",
			opts: Options{Local: true},
		},
		{
			name: "repository present | 已给出仓库",
			opts: Options{Repository: "registry.example.com/team/app"},
		},
		{
			name:     "blank repository | 仓库为空",
			opts:     Options{Repository: "   "},
			wantErr:  true,
			sentinel: ErrNoRepository,
		},
		{
			name:     "no repository at all | 完全没有仓库",
			wantErr:  true,
			sentinel: ErrNoRepository,
		},
		{
			name:     "all with specific platforms | all 与具体平台混用",
			opts:     Options{Repository: "r/app", Platforms: []string{"all", "linux/amd64"}},
			wantErr:  true,
			sentinel: ErrInvalidOptions,
		},
		{
			name: "all alone is valid | 单独使用 all 合法",
			opts: Options{Repository: "r/app", Platforms: []string{"all"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.validate()
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("validate() = %v | validate() 得到 %v，期望 nil", err, err)
				}
				return
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("validate() = %v | validate() 得到 %v，期望哨兵 %v", err, err, tc.sentinel)
			}
		})
	}
}

// TestNormalizeList covers the trimming, comma splitting and order-preserving deduplication every
// list option goes through. | TestNormalizeList 覆盖所有列表型选项共用的去空白、逗号拆分与保序去重。
func TestNormalizeList(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nil | 空", in: nil, want: nil},
		{name: "blanks only | 只有空白", in: []string{"", "   ", ","}, want: nil},
		{name: "trim and split | 去空白并拆分", in: []string{" a , b ", "c"}, want: []string{"a", "b", "c"}},
		{name: "dedupe keeps first order | 去重保序", in: []string{"b", "a", "b", "a"}, want: []string{"b", "a"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeList(tc.in)
			if !slices.Equal(got, tc.want) {
				t.Errorf("normalizeList(%q) = %q | normalizeList(%q) 得到 %q，期望 %q", tc.in, got, tc.in, got, tc.want)
			}
		})
	}
}

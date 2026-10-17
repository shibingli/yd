// This file tests the ko executable resolution chain: flag, environment, project-local copy, PATH,
// and the not-found sentinel when every source misses. | 本文件测试 ko 可执行文件的解析链：标志、环境变量、
// 项目内副本、PATH，以及全部来源落空时的未找到哨兵。

package ko

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubLookup returns a PATH lookup backed by a name-to-path table; unknown names behave like a
// missing command. | stubLookup 返回一个由「名字 → 路径」表支撑的 PATH 查找函数，未知名字等同于命令缺失。
func stubLookup(paths map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if p, ok := paths[name]; ok {
			return p, nil
		}
		return "", exec.ErrNotFound
	}
}

// usePathLookup installs a lookup for one test and restores the package default afterwards. | usePathLookup
// 为一个测试安装查找函数，并在测试结束后恢复包级默认值。
func usePathLookup(t *testing.T, lookup func(string) (string, error)) {
	t.Helper()
	original := PathLookup
	PathLookup = lookup
	t.Cleanup(func() { PathLookup = original })
}

// writeLocalKo drops a fake ko into <dir>/.yd/tools and returns the expected path. | writeLocalKo 把假 ko
// 放入 <dir>/.yd/tools 并返回期望路径。
func writeLocalKo(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(LocalToolDir), DefaultBinary+exeSuffix())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建 .yd/tools 失败: %v", err)
	}
	if err := os.WriteFile(path, []byte("fake ko | 假 ko"), 0o755); err != nil {
		t.Fatalf("写入假 ko 失败: %v", err)
	}
	return path
}

// TestResolve checks the four sources in priority order plus the sentinel of a total miss. | TestResolve
// 校验四个来源的优先级顺序，以及全部落空时的哨兵。
func TestResolve(t *testing.T) {
	tests := []struct {
		name      string
		envBin    string
		bin       string
		paths     map[string]string
		want      string
		wantLocal bool
		wantErr   bool
	}{
		{
			name:   "flag wins over environment | 标志优先于环境变量",
			bin:    "ko-flag",
			envBin: "ko-env",
			paths:  map[string]string{"ko-flag": "/resolved/ko-flag", "ko-env": "/resolved/ko-env"},
			want:   "/resolved/ko-flag",
		},
		{
			name:   "environment wins over PATH | 环境变量优先于 PATH",
			envBin: "ko-env",
			paths:  map[string]string{"ko-env": "/resolved/ko-env", DefaultBinary: "/resolved/ko-path"},
			want:   "/resolved/ko-env",
		},
		{
			name: "project-local copy wins over PATH | 项目内副本优先于 PATH",
			paths: map[string]string{
				DefaultBinary: "/resolved/ko-path",
			},
			wantLocal: true,
		},
		{
			name:  "PATH is the last resort | PATH 是最后回退",
			paths: map[string]string{DefaultBinary: "/resolved/ko-path"},
			want:  "/resolved/ko-path",
		},
		{
			name:    "every source misses | 所有来源都落空",
			paths:   map[string]string{},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvBinary, tc.envBin)
			usePathLookup(t, stubLookup(tc.paths))

			dir := t.TempDir()
			want := tc.want
			if tc.wantLocal {
				want = writeLocalKo(t, dir)
			}

			got, err := Resolve(Options{Binary: tc.bin, WorkingDir: dir})
			if tc.wantErr {
				if !errors.Is(err, ErrBinaryNotFound) {
					t.Fatalf("Resolve() = %q, %v | Resolve() 得到 %q、%v，期望 ErrBinaryNotFound", got, err, got, err)
				}
				// The install hint must be bilingual and actionable. | 安装提示必须中英同步且可执行。
				for _, want := range []string{InstallHint, EnvBinary, LocalToolDir, "已检查"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("ErrBinaryNotFound 详情缺少 %q：%v | detail misses %q: %v", want, err, want, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() 失败: %v", err)
			}
			if got != want {
				t.Errorf("Resolve() = %q | Resolve() 得到 %q，期望 %q", got, got, want)
			}
		})
	}
}

// TestResolvePrefersExplicitPath checks that a value with a separator is used verbatim once it
// exists, without going through PATH. | TestResolvePrefersExplicitPath 校验含分隔符的取值在文件存在时被原样使用，
// 而不经过 PATH。
func TestResolvePrefersExplicitPath(t *testing.T) {
	t.Setenv(EnvBinary, "")
	usePathLookup(t, stubLookup(map[string]string{}))

	dir := t.TempDir()
	// 路径不是可执行文件也无妨：本包只要求它存在且不是目录——与 align 的解析链一致。
	// The path need not be executable: this package only requires it to exist and not be a
	// directory, matching the align resolution chain.
	bin := filepath.Join(dir, DefaultBinary+exeSuffix())
	if err := os.WriteFile(bin, []byte("fake ko | 假 ko"), 0o755); err != nil {
		t.Fatalf("写入假 ko 失败: %v", err)
	}

	got, err := Resolve(Options{Binary: bin, WorkingDir: dir})
	if err != nil {
		t.Fatalf("Resolve() 失败: %v", err)
	}
	if got != bin {
		t.Errorf("Resolve() = %q | Resolve() 得到 %q，期望 %q", got, got, bin)
	}

	if _, err := Resolve(Options{Binary: filepath.Join(dir, "missing-ko"), WorkingDir: dir}); !errors.Is(err, ErrBinaryNotFound) {
		t.Errorf("不存在的显式路径应返回 ErrBinaryNotFound，得到 %v | an explicit missing path must yield ErrBinaryNotFound, got %v", err, err)
	}
}

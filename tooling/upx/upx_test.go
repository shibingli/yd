// This file tests the UPX wrapper package: version checks, resolution precedence, embedded payloads,
// extraction, compression invocation and the KO_GO_PATH wrapper. | 本文件测试 UPX 包装包：版本检查、
// 解析优先级、嵌入 payload、解压、压缩调用与 KO_GO_PATH 包装器。
package upx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/shibingli/yd/tooling/payload"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// runCall records one invocation of the injected command runner. | runCall 记录一次注入命令执行器的调用。
type runCall struct {
	name string
	args []string
}

// fakeRun returns a runner that records calls and invokes onCall. | fakeRun 返回记录调用并执行 onCall 的执行器。
func fakeRun(calls *[]runCall, onCall func(call runCall) ([]byte, error)) RunCommandFunc {
	return func(_ context.Context, name string, args []string, _ string, _ []string) ([]byte, error) {
		call := runCall{name: name, args: append([]string(nil), args...)}
		*calls = append(*calls, call)
		if onCall != nil {
			return onCall(call)
		}
		return nil, nil
	}
}

// TestCheckVersion pins the 5.2.1 floor and the tolerant version parser. | TestCheckVersion 固定 5.2.1 下限与宽容的版本解析。
func TestCheckVersion(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr error
	}{
		{name: "exact minimum | 恰好最低版本", output: "upx 5.2.1\n"},
		{name: "newer patch | 更高补丁", output: "upx 5.2.2\n"},
		{name: "newer minor | 更高次版本", output: "upx 5.10.0\n"},
		{name: "suffix | 带后缀", output: "upx 5.2.1-git\n"},
		{name: "too old | 版本过旧", output: "upx 5.2.0\n", wantErr: ErrVersionTooOld},
		{name: "major too old | 主版本过旧", output: "upx 4.2.4\n", wantErr: ErrVersionTooOld},
		{name: "unparsable | 无法解析", output: "not upx\n", wantErr: ErrVersionTooOld},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckVersion(tc.output)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CheckVersion(%q) = %v | 期望 %v", tc.output, err, tc.wantErr)
			}
		})
	}
}

// TestVersionOfRunsTheBinary pins the --version probe. | TestVersionOfRunsTheBinary 固定 --version 探测。
func TestVersionOfRunsTheBinary(t *testing.T) {
	var calls []runCall
	out, err := VersionOf(context.Background(), "/opt/upx", fakeRun(&calls, func(runCall) ([]byte, error) {
		return []byte("upx 5.2.1\n"), nil
	}))
	if err != nil {
		t.Fatalf("VersionOf() = %v", err)
	}
	if out != "5.2.1" {
		t.Errorf("VersionOf() = %q | 期望 5.2.1", out)
	}
	if len(calls) != 1 || calls[0].name != "/opt/upx" || !slices.Equal(calls[0].args, []string{"--version"}) {
		t.Errorf("calls = %+v | 期望一次 --version 调用", calls)
	}
}

// TestResolvePrecedence pins explicit → environment → local copy → embedded → PATH. | TestResolvePrecedence
// 固定 显式 → 环境变量 → 本地副本 → 嵌入 → PATH 的优先级。
func TestResolvePrecedence(t *testing.T) {
	if _, _, ok := EmbeddedHost(); !ok {
		t.Skip("no embedded host payload on this platform | 当前平台没有嵌入的宿主 payload")
	}
	versionRun := func(calls *[]runCall) RunCommandFunc {
		return fakeRun(calls, func(runCall) ([]byte, error) {
			return []byte("upx 5.2.1\n"), nil
		})
	}
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit-upx")
	writeExecutable(t, explicit)
	local := filepath.Join(dir, LocalToolDir, upxBinaryName())
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeExecutable(t, local)

	t.Run("explicit wins | 显式优先", func(t *testing.T) {
		t.Setenv(EnvBin, filepath.Join(dir, "env-upx"))
		var calls []runCall
		bin, err := Resolve(Options{Binary: explicit, Dir: dir, ExecCommand: versionRun(&calls), Materialize: true})
		if err != nil {
			t.Fatalf("Resolve() = %v", err)
		}
		if bin.Path != explicit || bin.Embedded {
			t.Errorf("Resolve() = %+v | 期望显式路径", bin)
		}
	})

	t.Run("environment before local | 环境变量先于本地副本", func(t *testing.T) {
		envBin := filepath.Join(dir, "env-upx")
		writeExecutable(t, envBin)
		t.Setenv(EnvBin, envBin)
		var calls []runCall
		bin, err := Resolve(Options{Dir: dir, ExecCommand: versionRun(&calls), Materialize: true})
		if err != nil {
			t.Fatalf("Resolve() = %v", err)
		}
		if bin.Path != envBin {
			t.Errorf("Resolve() = %+v | 期望环境变量路径", bin)
		}
		t.Setenv(EnvBin, "")
	})

	t.Run("local before embedded | 本地副本先于嵌入", func(t *testing.T) {
		t.Setenv(EnvBin, "")
		var calls []runCall
		bin, err := Resolve(Options{Dir: dir, ExecCommand: versionRun(&calls), Materialize: true})
		if err != nil {
			t.Fatalf("Resolve() = %v", err)
		}
		if bin.Path != local || bin.Embedded {
			t.Errorf("Resolve() = %+v | 期望本地副本", bin)
		}
	})

	t.Run("embedded materialized when nothing else exists | 无其它来源时物化嵌入", func(t *testing.T) {
		t.Setenv(EnvBin, "")
		fresh := t.TempDir()
		var calls []runCall
		bin, err := Resolve(Options{Dir: fresh, ExecCommand: versionRun(&calls), Materialize: true})
		if err != nil {
			t.Fatalf("Resolve() = %v", err)
		}
		if !bin.Embedded || bin.Path == "" {
			t.Errorf("Resolve() = %+v | 期望嵌入物化", bin)
		}
		if st, statErr := os.Stat(bin.Path); statErr != nil || st.Size() == 0 {
			t.Errorf("物化文件不可用: %v | materialized file unavailable: %v", statErr, statErr)
		}
		if bin.Version != Version {
			t.Errorf("Version = %q | 期望 %q", bin.Version, Version)
		}
	})

	t.Run("probe skips the version probe | 探测不执行版本查询", func(t *testing.T) {
		var calls []runCall
		bin, err := Resolve(Options{Binary: explicit, Dir: dir, ExecCommand: fakeRun(&calls, nil), SkipVersionCheck: true})
		if err != nil {
			t.Fatalf("Resolve() = %v", err)
		}
		if bin.Path != explicit || len(calls) != 0 {
			t.Errorf("Resolve() = %+v calls=%d | 期望不执行任何命令", bin, len(calls))
		}
	})
}

// TestEmbeddedLinuxPayloads pins the two Linux target payloads. | TestEmbeddedLinuxPayloads 固定两个 Linux 目标 payload。
func TestEmbeddedLinuxPayloads(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		payload, ok := EmbeddedLinux(arch)
		if !ok || len(payload) == 0 {
			t.Fatalf("EmbeddedLinux(%q) 缺失", arch)
		}
	}
	if _, ok := EmbeddedLinux("mips"); ok {
		t.Error("不支持的架构不应返回 payload | an unsupported arch must not yield a payload")
	}
}

// TestMaterializeIsIdempotent pins the atomic extraction and its executable mode. | TestMaterializeIsIdempotent
// 固定原子解压与可执行权限。
func TestMaterializeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	first, err := Materialize(dir, "upx-test", []byte("first"))
	if err != nil {
		t.Fatalf("Materialize() = %v", err)
	}
	data, err := os.ReadFile(first)
	if err != nil || string(data) != "first" {
		t.Fatalf("内容 = %q, %v | content = %q, %v", data, err, data, err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(first); st.Mode()&0o111 == 0 {
			t.Errorf("mode = %v | 期望可执行位", st.Mode())
		}
	}
	second, err := Materialize(dir, "upx-test", []byte("second"))
	if err != nil {
		t.Fatalf("second Materialize() = %v", err)
	}
	data, _ = os.ReadFile(second)
	if string(data) != "first" {
		t.Errorf("已有文件不得被覆盖：%q | an existing copy must win: %q", data, data)
	}
}

// TestCompressInvokesBestLzma pins the compression argv and the size guard. | TestCompressInvokesBestLzma
// 固定压缩参数与体积门槛。
func TestCompressInvokesBestLzma(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app")
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	var calls []runCall
	if err := Compress(context.Background(), "/opt/upx", target, 1<<30, fakeRun(&calls, nil)); err != nil {
		t.Fatalf("Compress() = %v", err)
	}
	want := []string{"--best", "--lzma", target}
	if len(calls) != 1 || calls[0].name != "/opt/upx" || !slices.Equal(calls[0].args, want) {
		t.Errorf("calls = %+v | 期望 %v", calls, want)
	}
	if err := Compress(context.Background(), "/opt/upx", target, 3, fakeRun(&calls, nil)); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("超限文件应返回 ErrFileTooLarge，得到 %v", err)
	}
}

// TestRunWrapperPacksAndServesBuildInfo pins the whole KO_GO_PATH flow: pack after build, then answer
// go version -m from the pre-pack backup and remove it. | TestRunWrapperPacksAndServesBuildInfo 固定完整的
// KO_GO_PATH 流程：构建后压缩、go version -m 从压缩前备份读取并清理备份。
func TestRunWrapperPacksAndServesBuildInfo(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app")
	var goCalls []runCall
	run := func(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
		goCalls = append(goCalls, runCall{name: name, args: append([]string(nil), args...)})
		if len(args) > 0 && args[0] == "build" {
			return os.WriteFile(target, []byte("unpacked"), 0o755)
		}
		return nil
	}
	var packed []string
	pack := func(_ context.Context, path string) error {
		packed = append(packed, path)
		// UPX replaces the file (new inode); mimic that so the hard-link backup keeps the original bytes. |
		// UPX 以新 inode 替换文件；这里照做，使硬链接备份保留原始字节。
		temp := path + ".packed"
		if err := os.WriteFile(temp, []byte("packed"), 0o755); err != nil {
			return err
		}
		return os.Rename(temp, path)
	}
	o := WrapperOptions{GoBin: "/real/go", Run: run, Pack: pack, MaxBytes: 1 << 30}

	if code := RunWrapper(context.Background(), []string{"build", "-trimpath", "-o", target, "example.com/app"}, o, nil, io.Discard, io.Discard); code != 0 {
		t.Fatalf("build wrapper exit = %d", code)
	}
	if data, _ := os.ReadFile(target); string(data) != "packed" {
		t.Fatalf("构建产物未压缩：%q | the build output was not packed: %q", data, data)
	}
	if len(packed) != 1 || packed[0] != target {
		t.Fatalf("packed = %q | 期望压缩 %q", packed, target)
	}
	backup := target + backupSuffix
	if data, err := os.ReadFile(backup); err != nil || string(data) != "unpacked" {
		t.Fatalf("备份 = %q, %v | backup = %q, %v", data, err, data, err)
	}

	if code := RunWrapper(context.Background(), []string{"version", "-m", target}, o, nil, io.Discard, io.Discard); code != 0 {
		t.Fatalf("version wrapper exit = %d", code)
	}
	last := goCalls[len(goCalls)-1]
	if len(last.args) != 3 || last.args[0] != "version" || last.args[1] != "-m" || last.args[2] != backup {
		t.Errorf("version -m args = %q | 期望读取备份 %q", last.args, backup)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Errorf("读取后备份应被删除: %v | the backup must be removed after the read: %v", err, err)
	}
}

// TestRunWrapperLeavesFailuresUncompressed pins the best-effort policy. | TestRunWrapperLeavesFailuresUncompressed
// 固定尽力而为策略：压缩失败不得改变构建结果。
func TestRunWrapperLeavesFailuresUncompressed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app")
	run := func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
		return os.WriteFile(target, []byte("unpacked"), 0o755)
	}
	pack := func(context.Context, string) error { return errors.New("upx boom | upx 失败") }
	var stderr bytes.Buffer
	o := WrapperOptions{GoBin: "/real/go", Run: run, Pack: pack, MaxBytes: 1 << 30}
	if code := RunWrapper(context.Background(), []string{"build", "-o", target}, o, nil, io.Discard, &stderr); code != 0 {
		t.Fatalf("wrapper exit = %d，压缩失败不得影响构建", code)
	}
	if data, _ := os.ReadFile(target); string(data) != "unpacked" {
		t.Errorf("压缩失败后应保留原文件: %q", data)
	}
	if !strings.Contains(stderr.String(), "upx") {
		t.Errorf("stderr = %q | 应给出压缩失败提示", stderr.String())
	}
}

// TestRunWrapperRequiresGoBinary pins the environment contract. | TestRunWrapperRequiresGoBinary 固定环境契约。
func TestRunWrapperRequiresGoBinary(t *testing.T) {
	var stderr bytes.Buffer
	code := RunWrapper(context.Background(), []string{"env"}, WrapperOptions{}, nil, io.Discard, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "YD_KO_GO_REAL") {
		t.Fatalf("缺少 go 时应失败：code=%d stderr=%q", code, stderr.String())
	}
}

// TestEmbeddedPayloadsMatchLockedChecksums pins the exact bytes shipped inside yd. | TestEmbeddedPayloadsMatchLockedChecksums
// 固定 yd 内嵌的确切字节。
func TestEmbeddedPayloadsMatchLockedChecksums(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		want    string
	}{
		{name: "linux/amd64", payload: payloadLinuxAMD64, want: LinuxAMD64SHA256},
		{name: "linux/arm64", payload: payloadLinuxARM64, want: LinuxARM64SHA256},
		{name: "windows/amd64", payload: payloadWindowsAMD64, want: WindowsAMD64SHA256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum := sha256.Sum256(tc.payload)
			if got := hex.EncodeToString(sum[:]); got != tc.want {
				t.Errorf("SHA-256 = %s | 期望 %s（payload 漂移，请同步 README 与常量）", got, tc.want)
			}
		})
	}
}

// writeExecutable writes a fake executable file. | writeExecutable 写入一个假的可执行文件。
func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestMain isolates the payload cache used by Materialize. | TestMain 隔离 Materialize 使用的 payload 缓存。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "yd-upx-cache-*")
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	_ = os.Setenv(payload.CacheEnv, dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// TestResolveFallsBackToPathWithoutEmbeddedHost pins the macOS/Windows-arm64 route: a host without an
// embedded payload uses the Homebrew upx found on PATH. | TestResolveFallsBackToPathWithoutEmbeddedHost
// 固定 macOS/Windows-arm64 路径：没有宿主 payload 的机器使用 PATH 上的 upx。
func TestResolveFallsBackToPathWithoutEmbeddedHost(t *testing.T) {
	original := embeddedHost
	embeddedHost = func() ([]byte, string, bool) { return nil, "", false }
	t.Cleanup(func() { embeddedHost = original })

	path := filepath.Join(t.TempDir(), upxBinaryName())
	lookup := func(name string) (string, error) {
		if name == upxBinaryName() {
			return path, nil
		}
		return "", os.ErrNotExist
	}
	var calls []runCall
	bin, err := Resolve(Options{
		PathLookup: lookup,
		ExecCommand: fakeRun(&calls, func(runCall) ([]byte, error) {
			return []byte("upx 5.2.1\n"), nil
		}),
	})
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	if bin.Path != path || bin.Version != "5.2.1" || bin.Embedded {
		t.Errorf("Resolve() = %+v | 期望 PATH 上的 5.2.1", bin)
	}
	if len(calls) != 1 || calls[0].name != path {
		t.Errorf("calls = %+v | 期望仅执行一次 PATH 上的 upx", calls)
	}

	// A read-only probe must not execute the binary. | 只读探测不得执行二进制。
	calls = nil
	bin, err = Resolve(Options{PathLookup: lookup, SkipVersionCheck: true, ExecCommand: fakeRun(&calls, nil)})
	if err != nil || bin.Path != path {
		t.Fatalf("probe Resolve() = %+v, %v", bin, err)
	}
	if len(calls) != 0 {
		t.Errorf("probe executed %d commands | 只读探测执行了 %d 条命令", len(calls), len(calls))
	}

	_, err = Resolve(Options{PathLookup: func(string) (string, error) { return "", os.ErrNotExist }})
	if !errors.Is(err, ErrBinaryNotFound) {
		t.Errorf("Resolve() error = %v | 期望 ErrBinaryNotFound", err)
	}
}

// TestRunWrapperRestoresOriginalAfterDestructivePackFailure pins the safety net: a packer that
// replaces the output and then fails must not leave a partial binary behind. |
// TestRunWrapperRestoresOriginalAfterDestructivePackFailure 固定安全网：先替换产物再失败的压缩器不得留下残缺二进制。
func TestRunWrapperRestoresOriginalAfterDestructivePackFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app")
	run := func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
		return os.WriteFile(target, []byte("unpacked"), 0o755)
	}
	pack := func(_ context.Context, path string) error {
		temp := path + ".partial"
		if err := os.WriteFile(temp, []byte("partial"), 0o755); err != nil {
			return err
		}
		if err := os.Rename(temp, path); err != nil {
			return err
		}
		return errors.New("upx boom | upx 失败")
	}
	var stderr bytes.Buffer
	o := WrapperOptions{GoBin: "/real/go", Run: run, Pack: pack, MaxBytes: 1 << 30}
	if code := RunWrapper(context.Background(), []string{"build", "-o", target}, o, nil, io.Discard, &stderr); code != 0 {
		t.Fatalf("wrapper exit = %d，压缩失败不得影响构建", code)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "unpacked" {
		t.Fatalf("压缩失败后必须恢复原文件：%q, %v | the original must be restored: %q, %v", data, err, data, err)
	}
	if _, err := os.Stat(target + backupSuffix); !os.IsNotExist(err) {
		t.Errorf("恢复后备份应被删除：%v | the backup must be removed after a successful restore: %v", err, err)
	}
	if !strings.Contains(stderr.String(), "UPX") {
		t.Errorf("stderr = %q | 应给出压缩失败提示", stderr.String())
	}
}

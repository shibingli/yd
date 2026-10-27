// This file tests the UPX staging of the docker build context. | 本文件测试 docker 构建上下文的 UPX 暂存。
package docker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/payload"
	"github.com/shibingli/yd/tooling/upx"
	ydlog "github.com/shibingli/ydx/log"
)

// TestTargetArch pins the platform parsing used to select the Linux payload. | TestTargetArch 固定用于选择
// Linux payload 的平台解析。
func TestTargetArch(t *testing.T) {
	tests := []struct {
		platform string
		arch     string
		ok       bool
	}{
		{platform: "linux/amd64", arch: "amd64", ok: true},
		{platform: "linux/arm64", arch: "arm64", ok: true},
		{platform: "linux/arm64/v8", arch: "arm64", ok: true},
		{platform: "linux/386", ok: false},
		{platform: "windows/amd64", ok: false},
		{platform: "", arch: runtime.GOARCH, ok: true},
	}
	for _, tc := range tests {
		t.Run(tc.platform, func(t *testing.T) {
			arch, ok := targetArch(tc.platform)
			if ok != tc.ok || arch != tc.arch {
				t.Errorf("targetArch(%q) = %q,%v | 期望 %q,%v", tc.platform, arch, ok, tc.arch, tc.ok)
			}
		})
	}
}

// TestStageUPXStagesTargetArchAndCleansUp pins the staged payload, its removal after the build, and the
// architecture selection. | TestStageUPXStagesTargetArchAndCleansUp 固定暂存 payload、构建后清理与架构选择。
func TestStageUPXStagesTargetArchAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	writeDockerfile(t, dir, "FROM scratch\nARG YD_INCLUDE_UPX=false\n")

	want, ok := upx.EmbeddedLinux("arm64")
	if !ok {
		t.Fatal("没有 arm64 payload | no arm64 payload")
	}
	var matched bool
	fake := &fakeExec{probeStdout: "27.0.0\n", buildStdout: buildOutput}
	fake.onBuild = func([]string) {
		data, err := os.ReadFile(filepath.Join(dir, ".yd", "upx", "upx"))
		matched = err == nil && bytes.Equal(data, want)
	}

	o := Options{Dir: dir, Platform: "linux/arm64", UPX: true, ExecCommand: fake.exec}
	if _, err := BuildImage(context.Background(), o, afero.NewOsFs(), nil); err != nil {
		t.Fatalf("BuildImage() = %v", err)
	}
	if !matched {
		t.Error("构建阶段没有看到与目标架构匹配的 UPX | the build did not see the arch-matched UPX")
	}
	if _, err := os.Stat(filepath.Join(dir, ".yd", "upx", "upx")); !os.IsNotExist(err) {
		t.Errorf("构建结束后暂存 UPX 应被删除: %v | the staged UPX must be removed after the build: %v", err, err)
	}
}

// TestStageUPXSkipsDockerfileWithoutStep pins the compatibility path for projects generated before UPX
// support: nothing is staged and the build still succeeds. | TestStageUPXSkipsDockerfileWithoutStep 固定
// UPX 支持之前生成的项目：不暂存任何文件，构建仍然成功。
func TestStageUPXSkipsDockerfileWithoutStep(t *testing.T) {
	dir := t.TempDir()
	writeDockerfile(t, dir, "FROM scratch\n")

	var staged bool
	fake := &fakeExec{probeStdout: "27.0.0\n", buildStdout: buildOutput}
	fake.onBuild = func([]string) {
		_, err := os.Stat(filepath.Join(dir, ".yd", "upx", "upx"))
		staged = err == nil
	}
	o := Options{Dir: dir, Platform: "linux/amd64", UPX: true, ExecCommand: fake.exec}
	if _, err := BuildImage(context.Background(), o, afero.NewOsFs(), nil); err != nil {
		t.Fatalf("BuildImage() = %v", err)
	}
	if staged {
		t.Error("没有 UPX 步骤的 Dockerfile 不得暂存 UPX | a Dockerfile without the step must not stage UPX")
	}
}

// TestStageUPXSkipsUnsupportedPlatform pins the graceful skip for non-Linux or unknown architectures. |
// TestStageUPXSkipsUnsupportedPlatform 固定非 Linux 或未知架构的优雅跳过。
func TestStageUPXSkipsUnsupportedPlatform(t *testing.T) {
	dir := t.TempDir()
	writeDockerfile(t, dir, "FROM scratch\nARG YD_INCLUDE_UPX=false\n")
	o := Options{Dir: dir, Platform: "windows/amd64", UPX: true}
	if staged := stageUPX(o, logger()); staged != nil {
		t.Errorf("不支持的平台不得暂存: %+v | an unsupported platform must not stage: %+v", staged, staged)
	}
}

// writeDockerfile writes a project Dockerfile. | writeDockerfile 写入项目 Dockerfile。
func writeDockerfile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(content), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
}

// logger returns the package's default logger. | logger 返回包默认日志器。
func logger() *ydlog.Logger { return ydlog.Default() }

// TestMain isolates the payload cache used by UPX staging. | TestMain 隔离 UPX 暂存使用的 payload 缓存。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "yd-docker-cache-*")
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	_ = os.Setenv(payload.CacheEnv, dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

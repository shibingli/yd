// This file tests the temporary kodata staging that packages configs/config.yaml into a ko image. | 本文件测试
// 把 configs/config.yaml 打包进 ko 镜像的临时 kodata 暂存。

package ko

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeKodataFixture creates a project with a baseline configuration and one local package. | writeKodataFixture
// 创建一个带基线配置与一个本地包的项目。
func writeKodataFixture(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, DefaultConfigDir), 0o755); err != nil {
		t.Fatalf("创建配置目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultConfigDir, configFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("写入基线配置失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "app"), 0o755); err != nil {
		t.Fatalf("创建包目录失败: %v", err)
	}
	return dir
}

// TestBuildImageStagesConfigIntoKodata asserts that the baseline configuration is visible to the injected ko
// process and that the staged copy disappears afterwards. | TestBuildImageStagesConfigIntoKodata 断言注入的 ko
// 进程能看到基线配置，且暂存副本在构建后消失。
func TestBuildImageStagesConfigIntoKodata(t *testing.T) {
	stubResolvedKo(t)

	const content = "app:\n  env: production\n"
	dir := writeKodataFixture(t, content)
	stagedPath := filepath.Join(dir, "cmd", "app", kodataDir, DefaultConfigDir, configFileName)

	var seen string
	o := Options{
		Binary:     DefaultBinary,
		Repository: "registry.example.com/team/app",
		WorkingDir: dir,
		Packages:   []string{"./cmd/app"},
		Exec: func(_ context.Context, _ string, _ []string, _ string, _ []string) ([]byte, error) {
			data, err := os.ReadFile(stagedPath)
			if err != nil {
				return nil, err
			}
			seen = string(data)
			return []byte("registry.example.com/team/app:latest\n"), nil
		},
	}
	if _, err := BuildImage(context.Background(), o, nil); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	if seen != content {
		t.Errorf("ko 看到的配置 = %q，期望 %q", seen, content)
	}
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Errorf("构建结束后暂存文件仍然存在: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "app", kodataDir)); !os.IsNotExist(err) {
		t.Errorf("构建结束后暂存的 kodata 目录仍然存在: %v", err)
	}
}

// TestBuildImageKeepsExistingKodataConfig asserts that a project-owned kodata configuration wins and is never
// removed by cleanup. | TestBuildImageKeepsExistingKodataConfig 断言项目自有的 kodata 配置优先，且不会被清理删除。
func TestBuildImageKeepsExistingKodataConfig(t *testing.T) {
	stubResolvedKo(t)

	const baseline = "app:\n  env: production\n"
	const custom = "app:\n  env: staging\n"
	dir := writeKodataFixture(t, baseline)
	stagedPath := filepath.Join(dir, "cmd", "app", kodataDir, DefaultConfigDir, configFileName)
	if err := os.MkdirAll(filepath.Dir(stagedPath), 0o755); err != nil {
		t.Fatalf("创建 kodata 目录失败: %v", err)
	}
	if err := os.WriteFile(stagedPath, []byte(custom), 0o644); err != nil {
		t.Fatalf("写入自有 kodata 配置失败: %v", err)
	}

	var seen string
	o := Options{
		Binary:     DefaultBinary,
		Repository: "registry.example.com/team/app",
		WorkingDir: dir,
		Packages:   []string{"./cmd/app"},
		Exec: func(_ context.Context, _ string, _ []string, _ string, _ []string) ([]byte, error) {
			data, err := os.ReadFile(stagedPath)
			if err != nil {
				return nil, err
			}
			seen = string(data)
			return []byte("registry.example.com/team/app:latest\n"), nil
		},
	}
	if _, err := BuildImage(context.Background(), o, nil); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	if seen != custom {
		t.Errorf("ko 看到的配置 = %q，期望项目自有的 %q", seen, custom)
	}
	data, err := os.ReadFile(stagedPath)
	if err != nil {
		t.Fatalf("构建结束后自有配置被删除: %v", err)
	}
	if string(data) != custom {
		t.Errorf("构建结束后自有配置 = %q，期望 %q", data, custom)
	}
}

// TestBuildImageSkipsConfigStaging asserts that SkipConfig leaves no kodata tree behind. | TestBuildImageSkipsConfigStaging
// 断言 SkipConfig 不产生任何 kodata 树。
func TestBuildImageSkipsConfigStaging(t *testing.T) {
	stubResolvedKo(t)

	dir := writeKodataFixture(t, "app:\n  env: production\n")
	stagedPath := filepath.Join(dir, "cmd", "app", kodataDir, DefaultConfigDir, configFileName)

	o := Options{
		Binary:     DefaultBinary,
		Repository: "registry.example.com/team/app",
		WorkingDir: dir,
		Packages:   []string{"./cmd/app"},
		SkipConfig: true,
		Exec: func(_ context.Context, _ string, _ []string, _ string, _ []string) ([]byte, error) {
			if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
				return nil, errors.New("unexpected kodata file during a skipped build | 跳过构建时出现了 kodata 文件")
			}
			return []byte("registry.example.com/team/app:latest\n"), nil
		},
	}
	if _, err := BuildImage(context.Background(), o, nil); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
}

// TestBuildImageCleansUpAfterPartialStagingFailure asserts that a failure while staging a later package removes
// the files already created for an earlier one. | TestBuildImageCleansUpAfterPartialStagingFailure 断言后一个包暂存
// 失败时，前一个包已创建的文件会被清理。
func TestBuildImageCleansUpAfterPartialStagingFailure(t *testing.T) {
	stubResolvedKo(t)

	dir := writeKodataFixture(t, "app:\n  env: production\n")
	for _, pkg := range []string{"cmd/app", "cmd/other"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(pkg)), 0o755); err != nil {
			t.Fatalf("创建包目录失败: %v", err)
		}
	}
	// A regular file occupies the second package's kodata, so creating the configs child must fail. |
	// 第二个包的 kodata 被一个普通文件占住，创建 configs 子目录时必然失败。
	if err := os.WriteFile(filepath.Join(dir, "cmd", "other", kodataDir), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("写入冲突文件失败: %v", err)
	}

	o := Options{
		Binary:     DefaultBinary,
		Repository: "registry.example.com/team/app",
		WorkingDir: dir,
		Packages:   []string{"./cmd/app", "./cmd/other"},
		Exec: func(_ context.Context, _ string, _ []string, _ string, _ []string) ([]byte, error) {
			t.Error("暂存失败后不应启动 ko | ko must not start after a staging failure")
			return nil, nil
		},
	}
	if _, err := BuildImage(context.Background(), o, nil); !errors.Is(err, ErrConfigStage) {
		t.Fatalf("BuildImage 错误 = %v，期望 ErrConfigStage", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "app", kodataDir)); !os.IsNotExist(err) {
		t.Errorf("部分失败后第一个包的 kodata 目录仍然存在: %v", err)
	}
}

// TestBuildImageIgnoresMissingBaselineConfig asserts that a project without configs/config.yaml still builds,
// so a purely environment-driven project is not blocked. | TestBuildImageIgnoresMissingBaselineConfig 断言没有
// configs/config.yaml 的项目仍可构建，使完全由环境变量驱动的项目不被阻塞。
func TestBuildImageIgnoresMissingBaselineConfig(t *testing.T) {
	stubResolvedKo(t)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "app"), 0o755); err != nil {
		t.Fatalf("创建包目录失败: %v", err)
	}
	o := Options{
		Binary:     DefaultBinary,
		Repository: "registry.example.com/team/app",
		WorkingDir: dir,
		Packages:   []string{"./cmd/app"},
		Exec: func(_ context.Context, _ string, _ []string, _ string, _ []string) ([]byte, error) {
			return []byte("registry.example.com/team/app:latest\n"), nil
		},
	}
	if _, err := BuildImage(context.Background(), o, nil); err != nil {
		t.Fatalf("BuildImage 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "app", kodataDir)); !os.IsNotExist(err) {
		t.Errorf("缺少基线配置时不应创建 kodata: %v", err)
	}
}

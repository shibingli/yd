package cli

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// TestLoadConfigReadsProjectConfigsDir proves the CLI reads the same configs/config.yaml as the
// generated application. yd dev depends on server.http2.extended_connect and server.port from this
// file; reading the project root instead silently falls back to defaults, which makes a Windows
// child re-exec and leaves its grandchild holding the dev port. |
// TestLoadConfigReadsProjectConfigsDir 证明 CLI 与生成应用读取同一份 configs/config.yaml。
// yd dev 依赖其中的 server.http2.extended_connect 与 server.port；只读项目根目录会静默回退到默认值，
// 使 Windows 子进程重新执行，并让它的孙进程占住开发端口。
func TestLoadConfigReadsProjectConfigsDir(t *testing.T) {
	t.Setenv(configDirEnvKey, "")
	t.Setenv(koDataEnvKey, "")
	dir := "project"
	fsys := afero.NewMemMapFs()
	path := filepath.Join(dir, "configs", "config.yaml")
	if err := fsys.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建配置目录失败：%v | creating config dir: %v", err, err)
	}
	if err := afero.WriteFile(fsys, path, []byte("server:\n  port: 9090\n  http2:\n    extended_connect: true\n"), 0o644); err != nil {
		t.Fatalf("写入配置失败：%v | writing config: %v", err, err)
	}
	fs := pflag.NewFlagSet("yd", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerGlobalFlags(fs)

	cfg, err := loadConfig(&Env{WorkDir: dir, FS: fsys, Out: io.Discard}, fs)
	if err != nil {
		t.Fatalf("加载配置失败：%v | loading config: %v", err, err)
	}
	if got := cfg.GetInt("server.port"); got != 9090 {
		t.Errorf("server.port = %d，期望 9090 | want 9090", got)
	}
	if !cfg.GetBool("server.http2.extended_connect") {
		t.Error("server.http2.extended_connect = false，期望读取 configs/config.yaml 中的 true | want true from configs/config.yaml")
	}
}

// TestLoadConfigKeepsRootFileFallback locks the fallback for callers whose project has no configs
// directory: a root config file must still be read. |
// TestLoadConfigKeepsRootFileFallback 固化没有 configs 目录时的回退：项目根目录的配置文件仍须被读取。
func TestLoadConfigKeepsRootFileFallback(t *testing.T) {
	t.Setenv(configDirEnvKey, "")
	t.Setenv(koDataEnvKey, "")
	dir := "plain"
	fsys := afero.NewMemMapFs()
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建项目目录失败：%v | creating project dir: %v", err, err)
	}
	if err := afero.WriteFile(fsys, filepath.Join(dir, "config.yaml"), []byte("server:\n  port: 7070\n"), 0o644); err != nil {
		t.Fatalf("写入配置失败：%v | writing config: %v", err, err)
	}
	fs := pflag.NewFlagSet("yd", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerGlobalFlags(fs)

	cfg, err := loadConfig(&Env{WorkDir: dir, FS: fsys, Out: io.Discard}, fs)
	if err != nil {
		t.Fatalf("加载配置失败：%v | loading config: %v", err, err)
	}
	if got := cfg.GetInt("server.port"); got != 7070 {
		t.Errorf("server.port = %d，期望 7070 | want 7070", got)
	}
}

package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/tooling/icons"
)

// TestIconsCommandRegistered keeps the command discoverable and uniquely registered. | TestIconsCommandRegistered
// 保证命令可被发现且只注册一次。
func TestIconsCommandRegistered(t *testing.T) {
	registryMu.Lock()
	_, ok := registry[iconsCommandName]
	registryMu.Unlock()
	if !ok {
		t.Fatal("icons is not registered | icons 未注册")
	}
	if !strings.Contains(strings.Join(commandNames(), " "), iconsCommandName) {
		t.Fatal("icons is missing from the command list | icons 不在命令列表中")
	}
}

// TestIconsSyncCheckAndDefaultAction exercises the writer, the default check action and the drift
// sentinel. | TestIconsSyncCheckAndDefaultAction 覆盖写入、默认校验动作与漂移哨兵。
func TestIconsSyncCheckAndDefaultAction(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeIconTestFile(t, fsys, "web/templates/pages/a.html", `{{template "partials/icon" "lucide-log-in"}}`)
	te := newTestEnv(t, fsys, "", false)

	if err := Execute(context.Background(), []string{"icons", "sync"}, te.env); err != nil {
		t.Fatalf("icons sync failed: %v | icons sync 失败：%v\n%s", err, err, te.out.String())
	}
	sprite, err := afero.ReadFile(fsys, "web/static/img/icons.svg")
	if err != nil {
		t.Fatalf("sprite was not written: %[1]v | sprite 未写入：%[1]v", err)
	}
	if !strings.Contains(string(sprite), `id="i-lucide-log-in"`) {
		t.Errorf("sprite does not contain the referenced icon:\n%s", sprite)
	}
	if !strings.Contains(te.out.String(), "icons | 图标: 1") {
		t.Errorf("sync report does not count the icon:\n%s", te.out.String())
	}

	te = newTestEnv(t, fsys, "", false)
	if err := Execute(context.Background(), []string{"icons"}, te.env); err != nil {
		t.Fatalf("default check failed: %v | 默认校验失败：%v\n%s", err, err, te.out.String())
	}
	if !strings.Contains(te.out.String(), "ok | 通过") {
		t.Errorf("check report does not say ok:\n%s", te.out.String())
	}

	writeIconTestFile(t, fsys, "web/templates/pages/a.html", `{{template "partials/icon" "lucide-log-out"}}`)
	te = newTestEnv(t, fsys, "", false)
	err = Execute(context.Background(), []string{"icons", "check"}, te.env)
	if !errors.Is(err, icons.ErrDrift) {
		t.Fatalf("want icons.ErrDrift, got %v | 期望 icons.ErrDrift，实际 %v\n%s", err, err, te.out.String())
	}
	if !strings.Contains(te.errs.String(), "yd icons sync") {
		t.Errorf("drift error does not name the fix:\n%s", te.errs.String())
	}
}

// TestIconsExplicitTemplatesAndOutput pins the two overrides used by a generated project. |
// TestIconsExplicitTemplatesAndOutput 固定生成项目使用的两个覆盖标志。
func TestIconsExplicitTemplatesAndOutput(t *testing.T) {
	fsys := afero.NewMemMapFs()
	writeIconTestFile(t, fsys, "web/custom/pages/a.html", `{{template "partials/icon" "lucide-languages"}}`)
	te := newTestEnv(t, fsys, "", false)

	err := Execute(context.Background(), []string{
		"icons", "sync",
		"--templates", "web/custom",
		"--output", "web/custom/icons.svg",
	}, te.env)
	if err != nil {
		t.Fatalf("icons sync with overrides failed: %v | 带覆盖参数的 icons sync 失败：%v\n%s", err, err, te.out.String())
	}
	sprite, err := afero.ReadFile(fsys, "web/custom/icons.svg")
	if err != nil {
		t.Fatalf("custom sprite was not written: %[1]v | 自定义 sprite 未写入：%[1]v", err)
	}
	if !strings.Contains(string(sprite), `id="i-lucide-languages"`) {
		t.Errorf("custom sprite does not contain the icon:\n%s", sprite)
	}
}

func writeIconTestFile(t *testing.T, fsys afero.Fs, name, body string) {
	t.Helper()
	if dir := filepath.Dir(name); dir != "." {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := afero.WriteFile(fsys, name, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

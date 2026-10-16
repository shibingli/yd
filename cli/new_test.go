package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/shibingli/yd/assets"
	"github.com/shibingli/yd/scaffold"
)

// readMemFile reads a file from the in-memory file system, failing the test when it is missing. | readMemFile 从内存文件系统读取文件，缺失时让测试失败。
func readMemFile(t *testing.T, fs afero.Fs, path string) string {
	t.Helper()
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(data)
}

// TestNewCommandGeneratesProject 非 TTY 下 `yd new` 取默认值并生成完整项目。
// TestNewCommandGeneratesProject has `yd new` keep the defaults on a non-TTY and generate the full project.
func TestNewCommandGeneratesProject(t *testing.T) {
	// --local-replace 取绝对路径：相对路径会以工作目录为基准解析（另有用例覆盖）。
	// --local-replace takes an absolute path here; relative values resolve against the working directory (covered below).
	localRepo := t.TempDir()
	te := newTestEnv(t, nil, "", false)

	err := Execute(context.Background(), []string{
		newCommandName, "demo",
		"--module", "example.com/demo",
		"--redis",
		"-y",
		"--local-replace", localRepo,
	}, te.env)
	if err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, te.errs.String())
	}

	for _, path := range []string{
		"demo/go.mod",
		"demo/.gitignore",
		"demo/.env.template",
		"demo/.yd.json",
		"demo/CLAUDE.md",
		"demo/AGENTS.md",
		"demo/LICENSE",
		"demo/cmd/app/main.go",
		"demo/internal/router/router.go",
		"demo/web/assets.lock.json",
	} {
		if !existsMem(t, te.env.FS, path) {
			t.Errorf("缺少生成物 %s", path)
		}
	}

	gomod := filepath.ToSlash(readMemFile(t, te.env.FS, "demo/go.mod"))
	for _, want := range []string{"module example.com/demo", "replace github.com/shibingli/yd => " + filepath.ToSlash(localRepo)} {
		if !strings.Contains(gomod, want) {
			t.Errorf("go.mod 缺少 %q:\n%s", want, gomod)
		}
	}

	meta := readMemFile(t, te.env.FS, "demo/.yd.json")
	for _, want := range []string{`"name": "demo"`, `"module": "example.com/demo"`, `"db": "sqlite"`, `"redis": true`, `"tailwind": true`} {
		if !strings.Contains(meta, want) {
			t.Errorf(".yd.json 缺少 %q:\n%s", want, meta)
		}
	}

	out := te.out.String()
	for _, want := range []string{"demo", "cd demo && yd dev", "cd demo && yd assets update"} {
		if !strings.Contains(out, want) {
			t.Errorf("收尾提示缺少 %q:\n%s", want, out)
		}
	}
}

// TestNewCommandWithoutNameIsUsageError 非 TTY 且未给项目名时返回用法错误而不是 panic。
// TestNewCommandWithoutNameIsUsageError returns a usage error instead of panicking when the name is missing on a non-TTY.
func TestNewCommandWithoutNameIsUsageError(t *testing.T) {
	for _, args := range [][]string{
		{newCommandName},
		{newCommandName, "a", "b"},
	} {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), args, te.env)
		if !errors.Is(err, ErrNoCommand) {
			t.Errorf("args=%v 期望 ErrNoCommand，实际 %v", args, err)
		}
		if !strings.Contains(err.Error(), newUsageLine) {
			t.Errorf("错误信息应给出用法行：%v", err)
		}
	}
}

// TestNewCommandForceOverwritesConflicts 冲突文件在未传 --force 时阻断，传入后覆盖。
// TestNewCommandForceOverwritesConflicts blocks on conflicting files without --force and overwrites them with it.
func TestNewCommandForceOverwritesConflicts(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := afero.WriteFile(te.env.FS, "demo/README.md", []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("预置冲突文件失败: %v", err)
	}

	err := Execute(context.Background(), []string{newCommandName, "demo", "-y"}, te.env)
	if !errors.Is(err, scaffold.ErrConflict) {
		t.Fatalf("期望 ErrConflict，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "demo/README.md") {
		t.Errorf("冲突提示应列出路径：%v", err)
	}
	if got := readMemFile(t, te.env.FS, "demo/README.md"); got != "stale\n" {
		t.Errorf("冲突时不应改写文件，实际 %q", got)
	}

	if err := Execute(context.Background(), []string{newCommandName, "demo", "-y", "--force"}, te.env); err != nil {
		t.Fatalf("--force 生成失败: %v", err)
	}
	if got := readMemFile(t, te.env.FS, "demo/README.md"); !strings.Contains(got, "demo") {
		t.Errorf("--force 后 README.md 未覆盖:\n%s", got)
	}
}

// lineReader mimics a terminal in canonical mode: every Read delivers at most one line and keeps the rest
// for the following call. | lineReader 模仿终端的规范模式：每次 Read 最多交付一行，多余字节留到下一次。
//
// It models what the interactive helpers actually see on a terminal. Note that they build a fresh
// bufio.Reader per question (cli/interact.go), so a reader that hands out several lines in one Read — a
// pipe, or a pasted block — answers only the first question. | 它模拟交互辅助在终端上真正看到的行为。
// 请注意这些辅助每次提问都会新建一个 bufio.Reader（cli/interact.go），因此一次 Read 交付多行的输入源
// （管道、粘贴的整块文本）只能回答第一个问题。
type lineReader struct {
	lines   []string
	next    int
	pending []byte
}

// newLineReader splits input into lines, keeping each terminator. | newLineReader 把 input 按行切分并保留行尾换行。
func newLineReader(input string) *lineReader {
	lines := strings.SplitAfter(input, "\n")
	if last := lines[len(lines)-1]; last == "" {
		lines = lines[:len(lines)-1]
	}
	return &lineReader{lines: lines}
}

// Read hands out the next line, or io.EOF once the script is exhausted. | Read 交付下一行，脚本用尽后返回 io.EOF。
func (r *lineReader) Read(p []byte) (int, error) {
	if len(r.pending) == 0 {
		if r.next >= len(r.lines) {
			return 0, io.EOF
		}
		r.pending = []byte(r.lines[r.next])
		r.next++
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

// TestNewCommandTTYInterviewUsesAnswers TTY 下逐项询问并把回答写入生成物。
// TestNewCommandTTYInterviewUsesAnswers asks one question per option on a TTY and writes the answers into the project.
func TestNewCommandTTYInterviewUsesAnswers(t *testing.T) {
	// 询问顺序：项目名、模块路径、数据库、redis、asynq、grpc、i18n、tailwind、扩展、协议。
	// Question order: project name, module, database, redis, asynq, grpc, i18n, tailwind, extensions, licence.
	answers := strings.Join([]string{
		"shop",
		"",
		"2", // 2) postgres
		"y", "y", "n", "y",
		"", // Tailwind keeps its default (on) | Tailwind 保持默认（开启）
		"sse, ws",
		"",
	}, "\n") + "\n"
	te := newTestEnv(t, nil, "", true)
	te.env.In = newLineReader(answers)

	if err := Execute(context.Background(), []string{newCommandName}, te.env); err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, te.errs.String())
	}

	meta := readMemFile(t, te.env.FS, "shop/.yd.json")
	for _, want := range []string{
		`"module": "github.com/yun-dev/shop"`,
		`"db": "postgres"`,
		`"redis": true`,
		`"asynq": true`,
		`"grpc": false`,
		`"i18n": true`,
		`"sse"`,
		`"ws"`,
	} {
		if !strings.Contains(meta, want) {
			t.Errorf(".yd.json 缺少 %q:\n%s", want, meta)
		}
	}
	if strings.Contains(meta, "preload") {
		t.Errorf("问答选择了 sse,ws，preload 不应出现:\n%s", meta)
	}

	for _, want := range []string{"项目名 | Project name", "数据库驱动 | Database driver", "Htmx 扩展"} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("提示语缺少 %q:\n%s", want, te.out.String())
		}
	}
}

// TestNewCommandYesSkipsInterviewInTTY TTY 下传 --yes 时一个字节都不读。
// TestNewCommandYesSkipsInterviewInTTY reads not a single byte when --yes is passed on a TTY.
func TestNewCommandYesSkipsInterviewInTTY(t *testing.T) {
	te := newTestEnv(t, nil, "", true)
	te.env.In = failingReader{t: t}

	if err := Execute(context.Background(), []string{newCommandName, "ay", "-y"}, te.env); err != nil {
		t.Fatalf("yd new -y 失败: %v", err)
	}
	if strings.Contains(te.out.String(), "Project name") {
		t.Errorf("--yes 不应提问：\n%s", te.out.String())
	}
	if !existsMem(t, te.env.FS, "ay/.yd.json") {
		t.Error("--yes 应生成默认项目")
	}
}

// TestNewCommandResolvesWorkDirAndRelativeReplace 生成目标与相对 replace 都以 WorkDir 为基准。
// TestNewCommandResolvesWorkDirAndRelativeReplace resolves both the destination and a relative replace against WorkDir.
func TestNewCommandResolvesWorkDirAndRelativeReplace(t *testing.T) {
	fs := afero.NewMemMapFs()
	te := newTestEnv(t, fs, "", false)
	te.env.WorkDir = filepath.Join("sandbox", "dev")

	if err := Execute(context.Background(), []string{
		newCommandName, "app", "-y", "--local-replace", "../yd",
	}, te.env); err != nil {
		t.Fatalf("yd new 失败: %v", err)
	}

	if !existsMem(t, fs, filepath.Join("sandbox", "dev", "app", ".yd.json")) {
		t.Fatal("项目未落在 WorkDir 之下")
	}
	gomod := readMemFile(t, fs, filepath.Join("sandbox", "dev", "app", "go.mod"))
	want := "replace github.com/shibingli/yd => " + filepath.Join("sandbox", "dev", "..", "yd")
	if !strings.Contains(filepath.ToSlash(gomod), filepath.ToSlash(want)) {
		t.Errorf("相对 --local-replace 未以 WorkDir 为基准解析:\n%s\n期望包含 %s", gomod, filepath.ToSlash(want))
	}
}

// TestNewCommandGeneratedLockIsLoadableByAssets 生成的资产清单必须能被 assets 包直接加载。
// TestNewCommandGeneratedLockIsLoadableByAssets requires the generated lock to load through the assets package.
func TestNewCommandGeneratedLockIsLoadableByAssets(t *testing.T) {
	te := newTestEnv(t, nil, "", false)

	if err := Execute(context.Background(), []string{
		newCommandName, "lock", "-y", "--htmx-ext", "ws",
	}, te.env); err != nil {
		t.Fatalf("yd new 失败: %v", err)
	}

	path := filepath.Join("lock", assets.DefaultManifestPath)
	m, err := assets.LoadManifest(te.env.FS, path)
	if err != nil {
		t.Fatalf("生成清单无法加载: %v", err)
	}
	names := make([]string, 0, len(m.Assets))
	for i := range m.Assets {
		names = append(names, m.Assets[i].Name)
	}
	if !strings.Contains(strings.Join(names, ","), "hx-ws") {
		t.Errorf("所选扩展 hx-ws 不在清单中：%v", names)
	}
	if strings.Contains(strings.Join(names, ","), "hx-sse") {
		t.Errorf("未选中的 hx-sse 不应在清单中：%v", names)
	}
}

// existsMem reports whether path exists inside fs. | existsMem 报告 path 是否存在于 fs 中。
func existsMem(t *testing.T, fs afero.Fs, path string) bool {
	t.Helper()
	ok, err := afero.Exists(fs, path)
	if err != nil {
		t.Fatalf("判断 %s 是否存在失败: %v", path, err)
	}
	return ok
}

package cli

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shibingli/yd/tooling/assets"
	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/spf13/afero"
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
		"demo/.yd/config.baseline.yaml",
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
	for _, want := range []string{`"name": "demo"`, `"module": "example.com/demo"`, `"db": "sqlite"`, `"redis": true`, `"asynq": true`, `"tailwind": true`} {
		if !strings.Contains(meta, want) {
			t.Errorf(".yd.json 缺少 %q:\n%s", want, meta)
		}
	}

	// Redis 与 Asynq 默认内建：配置、消息总线与路由装配点都不需要任何标志。
	// Redis and Asynq are built in by default: configuration, message bus and router assembly points need no flag.
	cfg := readMemFile(t, te.env.FS, "demo/configs/config.yaml")
	for _, want := range []string{"redis:", "asynq:"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("configs/config.yaml 缺少 %q:\n%s", want, cfg)
		}
	}
	if baseline := readMemFile(t, te.env.FS, "demo/.yd/config.baseline.yaml"); baseline != cfg {
		t.Errorf("config baseline must equal the generated config | 配置基线必须与生成的配置一致")
	}
	bus := readMemFile(t, te.env.FS, "demo/internal/service/message_bus.go")
	for _, want := range []string{"asynq.NewClient", "asynq.NewServer"} {
		if !strings.Contains(bus, want) {
			t.Errorf("message_bus.go 缺少 %q:\n%s", want, bus)
		}
	}
	if strings.Contains(bus, "redis.Open") {
		t.Errorf("message_bus.go 不得自行打开第二个 Redis 客户端:\n%s", bus)
	}
	redisFile := readMemFile(t, te.env.FS, "demo/internal/service/redis.go")
	if !strings.Contains(redisFile, "redis.Open") {
		t.Errorf("internal/service/redis.go 必须打开唯一的 Redis 客户端:\n%s", redisFile)
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

// TestNewCommandRemovedFeatureFlagsAreRejected 断言 Redis、Asynq 与 Tailwind 的开关已移除：再传旧标志属于未知标志。
// TestNewCommandRemovedFeatureFlagsAreRejected asserts the Redis, Asynq and Tailwind switches are gone: the old flags are unknown.
func TestNewCommandRemovedFeatureFlagsAreRejected(t *testing.T) {
	for _, flag := range []string{"--redis", "--asynq", "--tailwind"} {
		te := newTestEnv(t, nil, "", false)
		err := Execute(context.Background(), []string{newCommandName, "shop", "-y", flag + "=false"}, te.env)
		if !errors.Is(err, ErrFlagParse) {
			t.Errorf("%s: err = %v, 期望 ErrFlagParse | err = %v, want ErrFlagParse", flag, err, err)
		}
	}
}

// TestNewCommandTTYInterviewUsesAnswers TTY 下逐项询问并把回答写入生成物。
// TestNewCommandTTYInterviewUsesAnswers asks one question per option on a TTY and writes the answers into the project.
func TestNewCommandTTYInterviewUsesAnswers(t *testing.T) {
	// 询问顺序：项目名、模块路径、数据库、grpc、验证码、WAF、协议；i18n、Htmx 扩展、Redis、Asynq、
	// Htmx、Tailwind 与 Alpine 是内建能力，不再提问。
	// Question order: project name, module, database, grpc, captcha, WAF, licence; i18n, Htmx extensions,
	// Redis, Asynq, Htmx, Tailwind and Alpine are built in and never asked.
	answers := strings.Join([]string{
		"shop",
		"",
		"2", // 2) postgres
		"n", // grpc
		"y", // captcha
		"y", // waf
		"",
	}, "\n") + "\n"
	te := newTestEnv(t, nil, "", true)
	te.env.In = newLineReader(answers)

	if err := Execute(context.Background(), []string{newCommandName}, te.env); err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, te.errs.String())
	}

	// Redis、Asynq、i18n、Tailwind 与 Alpine 已内建，不再出现在问答里。
	// Redis, Asynq, i18n, Tailwind and Alpine are built in and never appear in the interview.
	prompts := te.out.String()
	for _, unwanted := range []string{"启用 Redis", "启用 Asynq", "启用 Tailwind", "启用 i18n"} {
		if strings.Contains(prompts, unwanted) {
			t.Errorf("内建能力不应再出现在问答里 %q：\n%s", unwanted, prompts)
		}
	}

	meta := readMemFile(t, te.env.FS, "shop/.yd.json")
	for _, want := range []string{
		`"module": "github.com/yun-dev/shop"`,
		`"db": "postgres"`,
		`"redis": true`,
		`"asynq": true`,
		`"tailwind": true`,
		`"grpc": false`,
		`"captcha": true`,
		`"waf": true`,
		`"i18n": true`,
		`"sse"`,
		`"ws"`,
		`"preload"`,
		`"targets"`,
		`"alpine"`,
	} {
		if !strings.Contains(meta, want) {
			t.Errorf(".yd.json 缺少 %q:\n%s", want, meta)
		}
	}

	for _, want := range []string{"项目名 | Project name", "数据库驱动 | Database driver", "启用验证码", "启用 WAF"} {
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
		t.Error("--yes should have generated the default project | --yes 应生成默认项目")
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
		t.Fatal("the project did not land under WorkDir | 项目未落在 WorkDir 之下")
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

// TestNewCommandCaptchaFlag asserts --captcha flips both runtime switches and the manifest entry.
// | TestNewCommandCaptchaFlag 断言 --captcha 打开两个运行时开关并写入工程清单。
func TestNewCommandCaptchaFlag(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{newCommandName, "secure", "-y", "--captcha"}, te.env); err != nil {
		t.Fatalf("yd new --captcha 失败: %v\n%s", err, te.errs.String())
	}

	cfg := readMemFile(t, te.env.FS, "secure/configs/config.yaml")
	for _, want := range []string{"captcha:", "login: true", "password_change: true"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.yaml 缺少 %q:\n%s", want, cfg)
		}
	}
	meta := readMemFile(t, te.env.FS, "secure/.yd.json")
	if !strings.Contains(meta, `"captcha": true`) {
		t.Errorf(".yd.json 未标记 captcha 开启:\n%s", meta)
	}

	plain := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{newCommandName, "plain", "-y"}, plain.env); err != nil {
		t.Fatalf("yd new -y 失败: %v\n%s", err, plain.errs.String())
	}
	plainCfg := readMemFile(t, plain.env.FS, "plain/configs/config.yaml")
	if !strings.Contains(plainCfg, "login: false") || !strings.Contains(plainCfg, "password_change: false") {
		t.Errorf("默认生成不应开启验证码:\n%s", plainCfg)
	}
}

// TestNewCommandWAFDefaultOn asserts WAF ships with every project by default. |
// TestNewCommandWAFDefaultOn 断言 WAF 默认随每个项目生成。
func TestNewCommandWAFDefaultOn(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{newCommandName, "secure", "-y"}, te.env); err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, te.errs.String())
	}
	meta := readMemFile(t, te.env.FS, "secure/.yd.json")
	if !strings.Contains(meta, `"waf": true`) {
		t.Errorf(".yd.json 未标记 waf 开启:\n%s", meta)
	}
	if !strings.Contains(te.out.String(), "waf") {
		t.Errorf("报告缺少 waf:\n%s", te.out.String())
	}
}

// TestNewCommandWAFFlagOff asserts --waf=false records the opt-out in the manifest. |
// TestNewCommandWAFFlagOff 断言 --waf=false 在清单中记录关闭。
func TestNewCommandWAFFlagOff(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := Execute(context.Background(), []string{newCommandName, "secure", "-y", "--waf=false"}, te.env); err != nil {
		t.Fatalf("yd new 失败: %v\n%s", err, te.errs.String())
	}
	meta := readMemFile(t, te.env.FS, "secure/.yd.json")
	if !strings.Contains(meta, `"waf": false`) {
		t.Errorf(".yd.json 未标记 waf 关闭:\n%s", meta)
	}
}

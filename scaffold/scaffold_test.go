package scaffold_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/assets"
	"github.com/shibingli/yd/jsonx"
	"github.com/shibingli/yd/scaffold"
)

// demoName 是绝大多数用例使用的项目名（目标 FS 中的目录名）。
// demoName is the project name used by most cases (the directory inside the target FS).
const demoName = "demo-app"

// requiredFiles 是需求基线要求生成的项目相对路径清单（相对 <name>/，"/" 分隔）。
// requiredFiles is the project-relative list of files the requirements baseline demands
// (relative to <name>/, "/"-separated).
var requiredFiles = []string{
	".env.template",
	".gitattributes",
	".gitignore",
	".yd.json",
	"AGENTS.md",
	"CLAUDE.md",
	"Dockerfile",
	"LICENSE",
	"README.md",
	"build/README.md",
	"cmd/app/main.go",
	"configs/config.local.yaml.template",
	"configs/config.yaml",
	"docs/developer_guidelines.md",
	"go.mod",
	"internal/controller/user.go",
	"internal/middleware/recover.go",
	"internal/model/user.go",
	"internal/repository/user.go",
	"internal/router/router.go",
	"internal/service/user.go",
	"web/assets.lock.json",
	"web/static/css/app.css",
	"web/static/js/yd-app.js",
	"web/templates/layouts/base.html",
	"web/templates/pages/error.html",
	"web/templates/pages/home.html",
	"web/templates/partials/flash.html",
	"web/templates/partials/sidebar.html",
}

// specGitignore 是需求基线 §二.3 的 .gitignore 模板逐字内容（已去掉提示行）。
// specGitignore is the verbatim .gitignore template of the baseline §二.3
// (the advisory line has been dropped).
const specGitignore = "bin/\n" +
	"dist/\n" +
	"*.exe\n" +
	"*.exe~\n" +
	"*.dll\n" +
	"*.so\n" +
	"*.dylib\n" +
	"/app_main\n" +
	".env\n" +
	".env.*\n" +
	"!*.template\n" +
	"configs/config.local.yaml\n" +
	"logs/\n" +
	"*.log\n" +
	".idea/\n" +
	".vscode/\n" +
	"*.suo\n" +
	"*.sw?\n" +
	".tailwindcss/\n" +
	"node_modules/\n"

// specGitattributes 是需求基线 §二.3 的 .gitattributes 模板逐字内容。
// specGitattributes is the verbatim .gitattributes template of the baseline §二.3.
const specGitattributes = "* text=auto eol=lf\n" +
	"*.go text\n" +
	"*.html text\n" +
	"*.css text\n" +
	"*.js text\n" +
	"*.png binary\n" +
	"*.jpg binary\n" +
	"*.jpeg binary\n" +
	"*.gif binary\n" +
	"*.ico binary\n"

// project 包装一次生成结果：把项目相对路径翻译成目标 FS 上的路径。
// project wraps one generation result: it translates project-relative paths into target-FS paths.
type project struct {
	t    *testing.T
	fs   afero.Fs
	name string
}

// at 返回项目相对路径在目标 FS 上的完整路径。
// at returns the full target-FS path of a project-relative path.
func (p project) at(rel string) string {
	return p.name + "/" + rel
}

// read 读取项目文件，失败即终止用例。 | read reads a project file and fails the case on error.
func (p project) read(rel string) string {
	p.t.Helper()
	b, err := afero.ReadFile(p.fs, p.at(rel))
	if err != nil {
		p.t.Fatalf("读取 %s 失败: %v", p.at(rel), err)
	}
	return string(b)
}

// exists 报告项目文件是否存在。 | exists reports whether a project file exists.
func (p project) exists(rel string) bool {
	return afs.Exists(p.fs, p.at(rel))
}

// files 返回带项目前缀的路径清单，便于与 Result 比对。
// files returns the paths with the project prefix so they can be compared against the Result.
func (p project) files(rels []string) []string {
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		out = append(out, p.at(rel))
	}
	return out
}

// generateMem 在 MemFS 上生成项目并返回包装后的句柄与结果。
// generateMem generates the project on a MemFS and returns the wrapper together with the result.
func generateMem(t *testing.T, o scaffold.Options) (project, *scaffold.Result) {
	t.Helper()
	fs := afs.NewMem()
	res, err := scaffold.Generate(context.Background(), o, fs, nil)
	if err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	return project{t: t, fs: fs, name: o.Name}, res
}

// repoRoot 返回本仓库根目录（scaffold 包的上一级），供 --local-replace 使用。
// repoRoot returns the repository root (the parent of the scaffold package), used by --local-replace.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件路径")
	}
	return filepath.Dir(filepath.Dir(file))
}

// TestGenerateProducesAllRequiredFiles 断言生成物清单齐全且关键内容正确。
// TestGenerateProducesAllRequiredFiles asserts every required file exists with the right content.
func TestGenerateProducesAllRequiredFiles(t *testing.T) {
	o := scaffold.DefaultOptions(demoName)
	pr, res := generateMem(t, o)

	for _, rel := range requiredFiles {
		if !pr.exists(rel) {
			t.Errorf("缺少生成物: %s", pr.at(rel))
			continue
		}
		if content := pr.read(rel); content == "" {
			t.Errorf("生成物为空: %s", pr.at(rel))
		}
	}

	if res.ModulePath != "github.com/yun-dev/demo-app" {
		t.Errorf("ModulePath = %q, 期望 github.com/yun-dev/demo-app", res.ModulePath)
	}
	if !slices.Equal(res.Files, pr.files(requiredFiles)) {
		t.Errorf("Files 清单与期望不一致:\n实际=%v\n期望=%v", res.Files, pr.files(requiredFiles))
	}
	if len(res.Skipped) != 0 {
		t.Errorf("首次生成不应有 Skipped: %v", res.Skipped)
	}

	gomod := pr.read("go.mod")
	if !strings.Contains(gomod, "module github.com/yun-dev/demo-app") {
		t.Errorf("go.mod 缺少模块路径:\n%s", gomod)
	}
	if !strings.Contains(gomod, "go 1.27.2") {
		t.Errorf("go.mod 缺少 go 1.27.2:\n%s", gomod)
	}
	if strings.Contains(gomod, "\nreplace ") {
		t.Errorf("未指定 --local-replace 时不应出现 replace 指令:\n%s", gomod)
	}

	license := pr.read("LICENSE")
	for _, want := range []string{"Apache License", "Version 2.0, January 2004", "Copyright 2026 YunDev"} {
		if !strings.Contains(license, want) {
			t.Errorf("LICENSE 缺少 %q", want)
		}
	}
	if len(license) != 11961 {
		t.Errorf("LICENSE 长度 = %d, 期望与仓库同源的 11961 字节", len(license))
	}

	if got := pr.read(".gitignore"); got != specGitignore {
		t.Errorf(".gitignore 与需求基线不一致:\n实际=%q\n期望=%q", got, specGitignore)
	}
	if strings.Contains(pr.read(".gitignore"), "请谨慎使用此类代码") {
		t.Error(".gitignore 仍含提示行「请谨慎使用此类代码。」")
	}
	if got := pr.read(".gitattributes"); got != specGitattributes {
		t.Errorf(".gitattributes 与需求基线不一致:\n实际=%q\n期望=%q", got, specGitattributes)
	}
}

// TestGenerateHiddenFilesNotLostOnMemFS 断言隐藏文件不会因内嵌/遍历规则被漏写。
// TestGenerateHiddenFilesNotLostOnMemFS asserts dot-files survive embedding and walking.
func TestGenerateHiddenFilesNotLostOnMemFS(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("hidden-app"))

	for _, rel := range []string{".gitignore", ".gitattributes", ".env.template", ".yd.json"} {
		if !pr.exists(rel) {
			t.Fatalf("隐藏文件缺失: %s", pr.at(rel))
		}
	}

	env := pr.read(".env.template")
	for _, want := range []string{"YD_SERVER_PORT", "YD_DB_DRIVER", "YD_LOG_LEVEL"} {
		if !strings.Contains(env, want) {
			t.Errorf(".env.template 缺少 %q", want)
		}
	}
	if strings.Contains(env, "YUNGO_") {
		t.Error(".env.template 仍含旧前缀 YUNGO_")
	}

	var meta struct {
		Name      string         `json:"name"`
		Module    string         `json:"module"`
		Features  map[string]any `json:"features"`
		AssetRoot string         `json:"assetRoot"`
	}
	if err := jsonx.Unmarshal([]byte(pr.read(".yd.json")), &meta); err != nil {
		t.Fatalf(".yd.json 不是合法 JSON: %v", err)
	}
	if meta.Name != "hidden-app" || meta.Module != "github.com/yun-dev/hidden-app" {
		t.Errorf(".yd.json 记录错误: %+v", meta)
	}
	if meta.AssetRoot != "web" {
		t.Errorf(".yd.json assetRoot = %q, 期望 web", meta.AssetRoot)
	}
	if meta.Features["db"] != "sqlite" {
		t.Errorf(".yd.json features.db = %v, 期望 sqlite", meta.Features["db"])
	}
	if meta.Features["tailwind"] != true {
		t.Errorf(".yd.json features.tailwind = %v, 期望 true", meta.Features["tailwind"])
	}
}

// TestGenerateTailwindAddsCssEntry 断言 Tailwind 开关决定 CSS 入口内容。
// TestGenerateTailwindAddsCssEntry asserts the Tailwind switch drives the CSS entry.
func TestGenerateTailwindAddsCssEntry(t *testing.T) {
	on := scaffold.DefaultOptions("css-on")
	on.Tailwind = true
	prOn, _ := generateMem(t, on)
	css := prOn.read("web/static/css/app.css")
	for _, want := range []string{`@import "tailwindcss"`, "@theme", "--color-brand-600"} {
		if !strings.Contains(css, want) {
			t.Errorf("开启 Tailwind 时 app.css 缺少 %q:\n%s", want, css)
		}
	}

	off := scaffold.DefaultOptions("css-off")
	off.Tailwind = false
	prOff, _ := generateMem(t, off)
	cssOff := prOff.read("web/static/css/app.css")
	if strings.Contains(cssOff, `@import "tailwindcss"`) {
		t.Errorf("关闭 Tailwind 时 app.css 不应含 Tailwind 入口:\n%s", cssOff)
	}
	if cssOff == "" {
		t.Error("关闭 Tailwind 时 app.css 不应为空（需提供最小基础样式）")
	}
}

// TestGenerateFeaturesReflectedInConfigAndRoutes 断言特性开关落到配置与路由骨架。
// TestGenerateFeaturesReflectedInConfigAndRoutes asserts feature switches reach config and router code.
func TestGenerateFeaturesReflectedInConfigAndRoutes(t *testing.T) {
	o := scaffold.DefaultOptions("feat-app")
	o.DB = scaffold.DBPostgres
	o.Redis, o.Asynq, o.GRPC, o.I18n = true, true, true, true
	pr, _ := generateMem(t, o)

	cfg := pr.read("configs/config.yaml")
	for _, want := range []string{"postgres", "redis:", "asynq:", "grpc:", "i18n:"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("开启全部特性时 config.yaml 缺少 %q:\n%s", want, cfg)
		}
	}

	router := pr.read("internal/router/router.go")
	for _, want := range []string{"redis", "asynq", "grpc", "i18n"} {
		if !strings.Contains(router, want) {
			t.Errorf("开启全部特性时 internal/router/router.go 缺少 %q 注册代码:\n%s", want, router)
		}
	}

	plain := scaffold.DefaultOptions("plain-app")
	plain.DB = scaffold.DBNone
	prPlain, _ := generateMem(t, plain)
	cfgPlain := prPlain.read("configs/config.yaml")
	for _, unwanted := range []string{"redis:", "asynq:", "grpc:", "i18n:"} {
		if strings.Contains(cfgPlain, unwanted) {
			t.Errorf("未开启特性时 config.yaml 不应含 %q:\n%s", unwanted, cfgPlain)
		}
	}
	if strings.Contains(prPlain.read("internal/router/router.go"), "asynq") {
		t.Error("未开启 Asynq 时 router.go 不应出现 asynq")
	}
}

// TestGenerateDockerfileUsesScratchMultiStage 断言 Dockerfile 采用多阶段 + scratch。
// TestGenerateDockerfileUsesScratchMultiStage asserts the Dockerfile is multi-stage and scratch-based.
func TestGenerateDockerfileUsesScratchMultiStage(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("docker-app"))
	df := pr.read("Dockerfile")
	for _, want := range []string{
		"FROM golang:1.27-alpine AS builder",
		"FROM scratch",
		"CGO_ENABLED=0",
		"ca-certificates",
		"zoneinfo",
		`ENTRYPOINT ["/app"]`,
	} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile 缺少 %q:\n%s", want, df)
		}
	}
}

// TestGenerateMainGoIsMinimalCompilableSkeleton 断言 main.go 是可编译的最小骨架（app 包未就绪时的兜底形态）。
// TestGenerateMainGoIsMinimalCompilableSkeleton asserts the main.go fallback skeleton (the app package is not ready yet).
func TestGenerateMainGoIsMinimalCompilableSkeleton(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("main-app"))
	main := pr.read("cmd/app/main.go")
	for _, want := range []string{
		`"github.com/shibingli/yd/logx"`,
		"ListenAndServe",
		"http.Server",
		"signal.NotifyContext",
		"srv.Shutdown",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("cmd/app/main.go 缺少 %q:\n%s", want, main)
		}
	}
	if strings.Contains(main, "github.com/shibingli/yd/app") {
		t.Error("app 包尚不可用时不应 import 它")
	}
}

// TestGenerateIdempotentSkipsIdentical 断言重复生成内容相同的文件只记 Skipped。
// TestGenerateIdempotentSkipsIdentical asserts identical reruns only record Skipped entries.
func TestGenerateIdempotentSkipsIdentical(t *testing.T) {
	fs := afs.NewMem()
	o := scaffold.DefaultOptions("idem-app")

	first, err := scaffold.Generate(context.Background(), o, fs, nil)
	if err != nil {
		t.Fatalf("首次生成失败: %v", err)
	}
	pr := project{t: t, fs: fs, name: o.Name}

	second, err := scaffold.Generate(context.Background(), o, fs, nil)
	if err != nil {
		t.Fatalf("二次生成失败: %v", err)
	}
	if len(second.Files) != 0 {
		t.Errorf("二次生成不应写入文件: %v", second.Files)
	}
	if !slices.Equal(second.Skipped, first.Files) {
		t.Errorf("二次生成的 Skipped 应与首次 Files 一致:\nSkipped=%v\nFiles=%v", second.Skipped, first.Files)
	}
	if second.ModulePath != first.ModulePath {
		t.Errorf("模块路径不稳定: %q vs %q", second.ModulePath, first.ModulePath)
	}

	// 幂等性：内容不被改写，关键文件仍然正确。 | Idempotence: nothing is rewritten and the key files stay correct.
	if got := pr.read("go.mod"); !strings.Contains(got, "module github.com/yun-dev/idem-app") {
		t.Errorf("二次生成后 go.mod 内容异常:\n%s", got)
	}
}

// TestGenerateConflictsWithoutForce 断言冲突文件在未 Force 时阻断并列出。
// TestGenerateConflictsWithoutForce asserts conflicts block the run and are listed when Force is off.
func TestGenerateConflictsWithoutForce(t *testing.T) {
	fs := afs.NewMem()
	o := scaffold.DefaultOptions("conflict-app")
	pr := project{t: t, fs: fs, name: o.Name}
	for _, rel := range []string{"README.md", "internal/router/router.go"} {
		if err := afero.WriteFile(fs, pr.at(rel), []byte("stale\n"), 0o644); err != nil {
			t.Fatalf("预置冲突文件失败: %v", err)
		}
	}

	_, err := scaffold.Generate(context.Background(), o, fs, nil)
	if err == nil {
		t.Fatal("存在冲突且未 Force 时必须报错")
	}
	if !errors.Is(err, scaffold.ErrConflict) {
		t.Errorf("错误应可 errors.Is(ErrConflict) 判定，实际: %v", err)
	}
	for _, want := range []string{pr.at("README.md"), pr.at("internal/router/router.go")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误文案未列出冲突路径 %q: %v", want, err)
		}
	}
	if got := pr.read("README.md"); got != "stale\n" {
		t.Errorf("冲突时不应改写文件，实际: %q", got)
	}
	if pr.exists("go.mod") {
		t.Error("冲突时其余文件不应被写出（先校验后写入）")
	}

	o.Force = true
	res, err := scaffold.Generate(context.Background(), o, fs, nil)
	if err != nil {
		t.Fatalf("Force 生成失败: %v", err)
	}
	if !slices.Contains(res.Files, pr.at("README.md")) {
		t.Errorf("Force 时 README.md 应被覆盖并计入 Files: %v", res.Files)
	}
	if got := pr.read("README.md"); !strings.Contains(got, "conflict-app") {
		t.Errorf("Force 后 README.md 未更新:\n%s", got)
	}
}

// TestLocalReplaceAddsGoModReplace 断言 --local-replace 写入 require + replace。
// TestLocalReplaceAddsGoModReplace asserts --local-replace writes require + replace into go.mod.
func TestLocalReplaceAddsGoModReplace(t *testing.T) {
	o := scaffold.DefaultOptions("replace-app")
	o.LocalReplace = "/srv/yd"
	pr, _ := generateMem(t, o)

	gomod := pr.read("go.mod")
	if !strings.Contains(gomod, "require github.com/shibingli/yd") {
		t.Errorf("go.mod 缺少框架 require:\n%s", gomod)
	}
	if !strings.Contains(gomod, "replace github.com/shibingli/yd => /srv/yd") {
		t.Errorf("go.mod 缺少 replace 指向本地仓库:\n%s", gomod)
	}
}

// TestGenerateAssetsLockTrimmedToSelectedExtensions 断言资产清单来自框架清单并裁剪到所选扩展。
// TestGenerateAssetsLockTrimmedToSelectedExtensions asserts the asset lock comes from the framework manifest and is trimmed to the selection.
func TestGenerateAssetsLockTrimmedToSelectedExtensions(t *testing.T) {
	o := scaffold.DefaultOptions("lock-app")
	o.HtmxExts = []string{scaffold.ExtSSE}
	pr, _ := generateMem(t, o)

	raw := pr.read("web/assets.lock.json")
	manifest, err := assets.LoadManifest(memFSWith(t, "web/assets.lock.json", raw), "web/assets.lock.json")
	if err != nil {
		t.Fatalf("生成的资产清单无法被 assets 包加载: %v\n%s", err, raw)
	}
	if manifest.Version != assets.CurrentManifestVersion {
		t.Errorf("清单版本 = %d, 期望 %d", manifest.Version, assets.CurrentManifestVersion)
	}

	names := make([]string, 0, len(manifest.Assets))
	for _, a := range manifest.Assets {
		names = append(names, a.Name)
		if a.Version == "" || a.Path == "" || a.License == "" {
			t.Errorf("资产条目字段不全: %+v", a)
		}
		if len(a.SHA256) != 64 {
			t.Errorf("资产 %s 的 sha256 未随框架清单带过来: %q", a.Name, a.SHA256)
		}
		if _, err := assets.NormalizePath(a.Path); err != nil {
			t.Errorf("资产 %s 的路径不合法（应相对资产根 web）: %v", a.Name, err)
		}
	}

	for _, want := range []string{"htmx", "alpinejs", "idiomorph", "hx-sse"} {
		if !slices.Contains(names, want) {
			t.Errorf("资产清单缺少 %q: %v", want, names)
		}
	}
	for _, unwanted := range []string{"hx-ws", "hx-preload", "hx-alpine-compat", "hx-targets", "hx-head"} {
		if slices.Contains(names, unwanted) {
			t.Errorf("资产清单不应含未选中的扩展资产 %q: %v", unwanted, names)
		}
	}
}

// memFSWith 返回一个只放了单个文件的 MemFS，便于复用读取真实清单的代码路径。
// memFSWith returns a MemFS holding a single file, which reuses the code path that reads a real manifest.
func memFSWith(t *testing.T, path, content string) afero.Fs {
	t.Helper()
	fsys := afs.NewMem()
	if err := afero.WriteFile(fsys, path, []byte(content), 0o644); err != nil {
		t.Fatalf("准备 MemFS 失败: %v", err)
	}
	return fsys
}

// TestGenerateRejectsInvalidOptions 表驱动校验参数拦截。
// TestGenerateRejectsInvalidOptions is a table-driven check on option validation.
func TestGenerateRejectsInvalidOptions(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(o *scaffold.Options)
		want   error
	}{
		{"项目名为空", func(o *scaffold.Options) { o.Name = "" }, scaffold.ErrNameRequired},
		{"项目名非法", func(o *scaffold.Options) { o.Name = "1bad name" }, scaffold.ErrNameInvalid},
		{"模块路径非法", func(o *scaffold.Options) { o.Module = "not-a-module" }, scaffold.ErrModuleInvalid},
		{"数据库驱动不支持", func(o *scaffold.Options) { o.DB = "oracle" }, scaffold.ErrDBUnsupported},
		{"协议不支持", func(o *scaffold.Options) { o.License = "MIT" }, scaffold.ErrLicenseUnsupported},
		{"Htmx 扩展未知", func(o *scaffold.Options) { o.HtmxExts = []string{"nope"} }, scaffold.ErrHtmxExtUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := scaffold.DefaultOptions("bad-app")
			tc.mutate(&o)
			_, err := scaffold.Generate(context.Background(), o, afs.NewMem(), nil)
			if err == nil {
				t.Fatal("非法参数必须报错")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("错误类型不符: %v（期望 errors.Is %v）", err, tc.want)
			}
		})
	}
}

// TestGenerateCanceledContext 断言 ctx 取消即中止。
// TestGenerateCanceledContext asserts a cancelled ctx aborts the run.
func TestGenerateCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := scaffold.Generate(ctx, scaffold.DefaultOptions("cancel-app"), afs.NewMem(), nil)
	if !errors.Is(err, scaffold.ErrCanceled) {
		t.Errorf("应返回 ErrCanceled，实际: %v", err)
	}
}

// TestInteractiveNonTTYUsesDefaults 断言非 TTY 下不提问、直接返回默认值。
// TestInteractiveNonTTYUsesDefaults asserts non-TTY runs never prompt and just return the defaults.
func TestInteractiveNonTTYUsesDefaults(t *testing.T) {
	var out bytes.Buffer
	got, err := scaffold.Interactive(strings.NewReader(""), &out, scaffold.DefaultOptions("demo"), false)
	if err != nil {
		t.Fatalf("非 TTY 交互不应报错: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("非 TTY 不应输出提示，实际: %q", out.String())
	}
	want := scaffold.DefaultOptions("demo")
	if got.Name != want.Name || got.Module != want.Module || got.DB != want.DB ||
		got.License != want.License || got.Tailwind != want.Tailwind ||
		got.Redis != want.Redis || got.Asynq != want.Asynq || got.GRPC != want.GRPC || got.I18n != want.I18n ||
		!slices.Equal(got.HtmxExts, want.HtmxExts) {
		t.Errorf("非 TTY 应返回默认值:\n实际=%+v\n期望=%+v", got, want)
	}
}

// TestInteractiveTTYReadsAnswers 断言 TTY 下逐项读取回答，空行取默认值。
// TestInteractiveTTYReadsAnswers asserts TTY mode reads one answer per prompt and empty lines keep defaults.
func TestInteractiveTTYReadsAnswers(t *testing.T) {
	// 询问顺序：name、module、db、redis、asynq、grpc、i18n、tailwind、htmx-exts、license。
	// Prompt order: name, module, db, redis, asynq, grpc, i18n, tailwind, htmx-exts, license.
	answers := strings.Join([]string{
		"shop-app", // 项目名 | project name
		"",         // 模块路径 → 跟随新项目名 | module follows the new name
		"postgres", // 数据库 | database
		"y",        // redis
		"y",        // asynq
		"n",        // grpc
		"y",        // i18n
		"",         // tailwind → 默认开启 | keeps the default (on)
		"sse, ws",  // Htmx 扩展 | htmx extensions
		"",         // 协议 → 默认 Apache-2.0 | license keeps its default
	}, "\n") + "\n"

	var out bytes.Buffer
	got, err := scaffold.Interactive(strings.NewReader(answers), &out, scaffold.DefaultOptions("base-app"), true)
	if err != nil {
		t.Fatalf("交互失败: %v", err)
	}
	if got.Name != "shop-app" || got.Module != "github.com/yun-dev/shop-app" {
		t.Errorf("名称/模块解析错误: %+v", got)
	}
	if got.DB != scaffold.DBPostgres {
		t.Errorf("DB 解析错误: %q", got.DB)
	}
	if !got.Redis || !got.Asynq || got.GRPC || !got.I18n || !got.Tailwind {
		t.Errorf("特性开关解析错误: %+v", got)
	}
	if !slices.Equal(got.HtmxExts, []string{"sse", "ws"}) {
		t.Errorf("Htmx 扩展解析错误: %v", got.HtmxExts)
	}
	if !strings.Contains(out.String(), " | ") {
		t.Errorf("提示文案应为中英同步（含 \" | \"）: %q", out.String())
	}
}

// TestGeneratedGovernanceDocsStateConstraints 断言生成的治理文档覆盖硬性约束。
// TestGeneratedGovernanceDocsStateConstraints asserts the generated governance docs cover the hard constraints.
func TestGeneratedGovernanceDocsStateConstraints(t *testing.T) {
	o := scaffold.DefaultOptions("gov-app")
	o.Redis, o.Asynq = true, true
	pr, _ := generateMem(t, o)

	claude := pr.read("CLAUDE.md")
	for _, want := range []string{
		"gov-app",
		"github.com/yun-dev/gov-app",
		"Co-Authored-By: Eric Shi <shibingli@sina.com>",
		"中英文同步",
		"errx",
		"fmt.Errorf",
		"logx",
		"jsonx",
		"b64x",
		"YD_",
		// 技术栈版本（模板以粗体书写，逐字校验需求规定的版本号）。 | Stack versions (the template bolds them; the mandated version literals are checked verbatim).
		"Go **1.27.2**",
		"Htmx **4.0.0**",
		"Tailwind CSS **v4**",
		"redis",
		"asynq",
	} {
		if !strings.Contains(claude, want) {
			t.Errorf("CLAUDE.md 缺少 %q", want)
		}
	}
	lower := strings.ToLower(claude)
	if !strings.Contains(lower, "claude") || !strings.Contains(lower, "anthropic") {
		t.Error("CLAUDE.md 必须写明禁止提交信息出现 Claude/Anthropic 相关内容")
	}

	agents := pr.read("AGENTS.md")
	for _, want := range []string{"Test-and-Fix", "logx", "jsonx", "b64x", "errx", "YD_", "Co-Authored-By: Eric Shi <shibingli@sina.com>"} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md 缺少 %q", want)
		}
	}

	guide := pr.read("docs/developer_guidelines.md")
	for _, want := range []string{"hx-", "layouts/", "partials/", "pages/", "assets.lock.json", "YD_", "HX-Request"} {
		if !strings.Contains(guide, want) {
			t.Errorf("docs/developer_guidelines.md 缺少 %q", want)
		}
	}
}

// TestRealGenerateCompilesWithGoBuild 在真实临时目录生成项目并执行 `go build ./...`。
// TestRealGenerateCompilesWithGoBuild generates a project into a real temp dir and runs `go build ./...`.
//
// 这是「生成的骨架必须可编译」的端到端护栏：结合 --local-replace 指向本仓库，
// 由 `go build -mod=mod` 依据本地模块缓存补全 go.mod/go.sum（GOPROXY=off 时绝不触网）。
// This is the end-to-end guard proving the skeleton compiles: with --local-replace pointing at
// this repository, `go build -mod=mod` completes go.mod/go.sum from the local module cache
// (GOPROXY=off guarantees no network access).
func TestRealGenerateCompilesWithGoBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实编译 | skipped in short mode")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("未找到 go 工具链，跳过真实编译: %v", err)
	}

	dir, err := os.MkdirTemp("", "yd-scaffold-build-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	o := scaffold.DefaultOptions("yd-build-probe")
	o.DB = scaffold.DBSQLite
	o.Redis, o.Asynq, o.GRPC, o.I18n = true, true, true, true
	o.LocalReplace = filepath.ToSlash(repoRoot(t))

	res, err := scaffold.Generate(context.Background(), o, afs.NewOS(dir), nil)
	if err != nil {
		t.Fatalf("真实生成失败: %v", err)
	}
	if len(res.Files) != len(requiredFiles) {
		t.Fatalf("真实生成文件数 = %d, 期望 %d: %v", len(res.Files), len(requiredFiles), res.Files)
	}

	projDir := filepath.Join(dir, o.Name)
	output, err := runGo(goBin, projDir, true, "build", "-mod=mod", "./...")
	if err != nil {
		// 本地模块缓存不完整时回退到环境自带代理（离线环境仍会失败，如实报告）。
		// Incomplete module caches fall back to the ambient proxy (offline hosts still fail, reported as-is).
		t.Logf("GOPROXY=off 编译失败，回退到环境代理重试:\n%s", output)
		output, err = runGo(goBin, projDir, false, "build", "-mod=mod", "./...")
	}
	if err != nil {
		t.Fatalf("生成的骨架无法编译（go build ./...）: %v\n%s", err, output)
	}

	// `go build ./...` 命中多个包时只做编译校验、不落盘产物，因此再显式链接一次主包，
	// 证明骨架不仅能通过类型检查，也能真正产出可执行文件。
	// `go build ./...` only type-checks when several packages match and writes no artefact, so the main
	// package is linked explicitly once more to prove the skeleton yields a real executable.
	bin := filepath.Join(projDir, "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := runGo(goBin, projDir, true, "build", "-mod=mod", "-o", bin, "./cmd/app"); err != nil {
		t.Fatalf("主包链接失败（go build -o app ./cmd/app）: %v\n%s", err, out)
	}
	info, statErr := os.Stat(bin)
	if statErr != nil {
		t.Fatalf("未产出可执行文件 %s: %v", bin, statErr)
	}
	if info.Size() == 0 {
		t.Fatalf("产出的可执行文件为空: %s", bin)
	}
	t.Logf("真实生成 + go build 通过: %d 个文件，产物 %s（%d 字节）", len(res.Files), filepath.Base(bin), info.Size())
}

// runGo 在 dir 中执行 go 子命令，offline 为真时强制 GOPROXY=off 并清空 GOFLAGS、关闭工作区。
// runGo runs a go subcommand inside dir, forcing GOPROXY=off when offline is true and clearing GOFLAGS/GOWORK.
func runGo(goBin, dir string, offline bool, args ...string) (string, error) {
	cmd := exec.Command(goBin, args...)
	cmd.Dir = dir
	env := append(os.Environ(), "GOFLAGS=", "GOWORK=off")
	if offline {
		env = append(env, "GOPROXY=off")
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

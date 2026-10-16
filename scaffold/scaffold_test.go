package scaffold_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/assets"
	"github.com/shibingli/yd/b64x"
	"github.com/shibingli/yd/guomi"
	"github.com/shibingli/yd/jsonx"
	"github.com/shibingli/yd/scaffold"
	"github.com/shibingli/yd/ws"
	"github.com/spf13/afero"
	"go.yaml.in/yaml/v3"
)

// demoName 是绝大多数用例使用的项目名（目标 FS 中的目录名）。
// demoName is the project name used by most cases (the directory inside the target FS).
const demoName = "demo-app"

// requiredFiles 是需求基线要求生成的项目相对路径清单（相对 <name>/，"/" 分隔）。
// requiredFiles is the project-relative list of files the requirements baseline demands
// (relative to <name>/, "/"-separated).
//
// 顺序即 plan() 的排序结果，清单同时被 slices.Equal 逐项比对，因此新增或删除模板必须同步改这里。
// The order is the one plan() produces and the list is compared item by item with slices.Equal, so adding or removing a
// template must be mirrored here.
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
	"internal/controller/auth_controller.go",
	"internal/controller/controller.go",
	"internal/controller/home.go",
	"internal/controller/realtime.go",
	"internal/controller/role_controller.go",
	"internal/controller/tenant_controller.go",
	"internal/controller/user_controller.go",
	"internal/meta/meta.go",
	"internal/middleware/security.go",
	"internal/middleware/session.go",
	"internal/middleware/tenant.go",
	"internal/model/permission.go",
	"internal/model/role.go",
	"internal/model/tenant.go",
	"internal/model/user.go",
	"internal/repository/role_repo.go",
	"internal/repository/store.go",
	"internal/repository/tenant_repo.go",
	"internal/repository/user_repo.go",
	"internal/router/router.go",
	"internal/service/auth.go",
	"internal/service/realtime.go",
	"internal/service/role.go",
	"internal/service/tenant.go",
	"internal/service/user.go",
	"web/assets.lock.json",
	"web/embed.go",
	"web/i18n/en.yaml",
	"web/i18n/zh-CN.yaml",
	"web/static/css/app.css",
	"web/static/js/yd-app.js",
	"web/templates/layouts/base.html",
	"web/templates/pages/account.html",
	"web/templates/pages/error.html",
	"web/templates/pages/home.html",
	"web/templates/pages/login.html",
	"web/templates/pages/permissions.html",
	"web/templates/pages/realtime.html",
	"web/templates/pages/roles.html",
	"web/templates/pages/tenants.html",
	"web/templates/pages/users.html",
	"web/templates/partials/flash.html",
	"web/templates/partials/login.html",
	"web/templates/partials/roles/form.html",
	"web/templates/partials/roles/list.html",
	"web/templates/partials/sidebar.html",
	"web/templates/partials/tenants/form.html",
	"web/templates/partials/tenants/list.html",
	"web/templates/partials/users/form.html",
	"web/templates/partials/users/list.html",
	"web/templates/partials/users/row.html",
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
	// 鉴权不随开关：生成的项目一律登记 auth，开关关掉它等于把一个没有租户边界的骨架发出去。
	// Auth is not switchable: every generated project registers it, and switching it off would ship a skeleton with no tenant boundary.
	if meta.Features["auth"] != true {
		t.Errorf(".yd.json features.auth = %v, 期望 true", meta.Features["auth"])
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

// TestGenerateMainGoIsMinimalCompilableSkeleton 断言 main.go 只做装配、信号与退出三件事，业务一律不在入口里。
// TestGenerateMainGoIsMinimalCompilableSkeleton asserts main.go does exactly three things — assemble, react to signals,
// exit — and keeps every business concern out of the entry point.
func TestGenerateMainGoIsMinimalCompilableSkeleton(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("main-app"))
	main := pr.read("cmd/app/main.go")
	for _, want := range []string{
		`"` + scaffold.YdModule + `/app"`,
		`"` + scaffold.YdModule + `/errx"`,
		`"` + scaffold.YdModule + `/logx"`,
		`"` + scaffold.YdModule + `/middleware"`,
		"signal.NotifyContext",
		// 门面装配：本项目资产经 app.WithFS 交给渲染器与 /static，而不是退回框架内嵌资产。
		// Facade assembly: this project's assets reach the renderer and /static through app.WithFS instead of falling back
		// to the framework's embedded ones.
		"app.New(",
		"app.WithFS(web.Assets())",
		`StaticFS("/static"`,
		"router.Register(application)",
		"application.Run(ctx)",
		"os.Exit(1)",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("cmd/app/main.go 缺少 %q:\n%s", want, main)
		}
	}

	// 服务器生命周期归 app 门面：入口再手写监听与关闭就等于把框架的优雅退出绕过去了。
	// The server lifecycle belongs to the app facade: hand-written listeners and shutdowns in the entry point would
	// bypass the framework's graceful exit.
	for _, unwanted := range []string{"ListenAndServe", "http.Server", "srv.Shutdown"} {
		if strings.Contains(main, unwanted) {
			t.Errorf("cmd/app/main.go 不应再手写 %q（监听与优雅退出由 app.Run 接管）:\n%s", unwanted, main)
		}
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
		// 鉴权链路：多租户边界、能力守卫与会话顺序必须在助手的全局上下文里写明。
		// The authentication chain: the tenant boundary, the capability guards and the session order must be spelled out for the assistant.
		"auth.RequirePermission",
		"middleware.TenantID",
		"auth.ErrNotFound",
		"middleware.Promote",
		"auth.LoginEncryptedHeader",
		"SeedPasswordNotice",
		"web/i18n",
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
	for _, want := range []string{
		"Test-and-Fix", "logx", "jsonx", "b64x", "errx", "YD_", "Co-Authored-By: Eric Shi <shibingli@sina.com>",
		// 安全清单里的鉴权条目：租户隔离、能力守卫、会话顺序、文案双语。
		// The authentication items of the security checklist: tenant isolation, capability guards, session order and bilingual copy.
		"auth.RequirePermission",
		"middleware.TenantID",
		"ErrNotFound",
		"middleware.Promote",
		"SeedPasswordNotice",
		"web/i18n",
	} {
		if !strings.Contains(agents, want) {
			t.Errorf("AGENTS.md 缺少 %q", want)
		}
	}

	guide := pr.read("docs/developer_guidelines.md")
	for _, want := range []string{
		"hx-", "layouts/", "partials/", "pages/", "assets.lock.json", "YD_", "HX-Request",
		// 鉴权与多租户一节：链路顺序、越权即 404、能力守卫与国密载荷都在协同约束里。
		// The authentication and multi-tenancy section: the chain order, "escalation is a 404", the capability guards and the national-cryptography payload all live in the collaboration contract.
		"middleware.Promote",
		"auth.Middleware",
		"middleware.Tenant",
		"auth.ErrNotFound",
		"auth.RequirePermission",
		"sm2",
		"yd_lang",
		"data-theme",
		// 注解契约的小节号被生成的源码引用，缺了它源码里的 §9.2 就指向空气。
		// The annotation section number is referenced by the generated sources; without it the §9.2 in the code points at nothing.
		"§9.2",
	} {
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

// 真实服务验证用到的时间与体积上限 | Time and size bounds of the live-server checks
const (
	// startupTimeout 是等待生成的应用就绪的上限：首次启动要建连、装配模板并绑定端口。
	// startupTimeout bounds the wait for the generated application to become ready: the first start has to connect,
	// assemble its templates and bind the port.
	startupTimeout = 30 * time.Second
	// httpTimeout 是单条普通请求的上限。 | httpTimeout bounds one ordinary request.
	httpTimeout = 10 * time.Second
	// sseTimeout 是等待一帧 SSE 推送的上限（推送间隔 5 秒，留足心跳与调度余量）。
	// sseTimeout bounds the wait for one SSE frame (the push interval is five seconds, leaving room for heartbeats and scheduling).
	sseTimeout = 20 * time.Second
	// maxBodyBytes 限制单个响应体的读取量，避免断言把一条数据流整条读进内存。
	// maxBodyBytes caps how much of one response body is read so an assertion never pulls a whole stream into memory.
	maxBodyBytes = 1 << 20
)

// TestGeneratedAppServesFrameworkFeatures 端到端跑通「生成 → 编译 → 启动 → 访问」。
// TestGeneratedAppServesFrameworkFeatures runs the whole chain — generate, compile, start, request.
//
// 生成与编译沿用 TestRealGenerateCompilesWithGoBuild 的方式（真实临时目录 + --local-replace 指向本仓库），
// 随后把产物启在随机空闲端口上，用真实 HTTP 请求逐条断言：整页与片段、OpenAPI 文档与自包含的 /docs、
// 混合捕获路由、静态资产、SSE、WebSocket 以及表单校验的 422 契约。
// Generation and compilation follow TestRealGenerateCompilesWithGoBuild (a real temp directory plus --local-replace
// pointing at this repository); the artefact is then started on a random free port and every capability is asserted
// through real HTTP requests: whole page versus fragment, the OpenAPI document and the self-contained /docs, the
// mixed-capture routes, static assets, SSE, WebSocket and the 422 contract of form validation.
func TestGeneratedAppServesFrameworkFeatures(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实服务验证 | skipped in short mode")
	}

	_, base := startProbeApp(t, "yd-serve-probe")

	// 会话不再是可选项：/ 与 /users 都落在受保护组里，因此探针先用默认凭据登录，此后每条请求都带着那枚 cookie
	// （中间件 Promote 会把它提升为 Authorization，浏览器与 API 客户端因此走同一条校验路径）。
	// A session is no longer optional: / and /users live inside the protected group, so the probe signs in with the
	// default credentials first and every request afterwards carries that cookie (middleware.Promote lifts it into
	// Authorization, which is how browsers and API clients end up sharing one verification path).
	client := newProbeClient(t, httpTimeout)
	token := signIn(t, client, base, probeTenant, probeAdmin, probeAdminPassword)

	// 流式端点自己带令牌，因此另起一个客户端：它的超时按一帧推送的等待时间放宽。
	// The streaming endpoints carry the token themselves, so they get a client of their own with the timeout widened to
	// the wait for one pushed frame.
	streamClient := newProbeClient(t, sseTimeout)

	t.Run("健康检查", func(t *testing.T) {
		resp, body := getText(t, client, base+"/healthz", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /healthz 状态码 = %d, 期望 200", resp.StatusCode)
		}
		if strings.TrimSpace(body) != "ok" {
			t.Errorf("GET /healthz 响应体 = %q, 期望 ok", body)
		}
	})

	t.Run("整页渲染", func(t *testing.T) {
		resp, body := getText(t, client, base+"/", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET / 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		for _, want := range []string{"<html", `id="yd-workspace"`, "/static/css/yd.css"} {
			if !strings.Contains(body, want) {
				t.Errorf("GET / 缺少 %q:\n%s", want, body)
			}
		}
	})

	t.Run("htmx 局部请求只返回片段", func(t *testing.T) {
		resp, body := getText(t, client, base+"/", map[string]string{"HX-Request": "true"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("HX-Request GET / 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		if strings.Contains(body, "<html") {
			t.Errorf("HX-Request 请求不应返回整页 HTML:\n%s", body)
		}
		if !strings.Contains(body, `data-yd-page="home"`) {
			t.Errorf("HX-Request 响应不是首页片段:\n%s", body)
		}
	})

	t.Run("OpenAPI 文档与自包含 /docs", func(t *testing.T) {
		resp, doc := getText(t, client, base+"/openapi.json", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /openapi.json 状态码 = %d, 期望 200", resp.StatusCode)
		}
		for _, want := range []string{
			`"3.0`,
			`"/avatar/{id}.jpg"`,
			`"/report/{id}-xx.png"`,
			`"/users"`,
			`"/events"`,
			`"/ws"`,
			`"/login"`,
			`"/guomi/pubkey"`,
			`"/realtime"`,
			// 注解里的摘要：出现它就证明注解块被真正解析，而不是仅由路由树兜底推导（未解析的兜底操作没有 summary）。
			// A summary coming from an annotation proves the annotation blocks were really parsed instead of the
			// document being derived from the route tree alone (a fallback operation carries no summary).
			"Health probe",
			"WebSocket echo and push",
		} {
			if !strings.Contains(doc, want) {
				t.Errorf("GET /openapi.json 缺少 %q:\n%s", want, doc)
			}
		}

		assertAnnotatedOperations(t, doc)

		respDocs, docs := getText(t, client, base+"/docs", nil)
		if respDocs.StatusCode != http.StatusOK {
			t.Fatalf("GET /docs 状态码 = %d, 期望 200", respDocs.StatusCode)
		}
		if !strings.Contains(docs, "<html") {
			t.Errorf("GET /docs 不是完整页面:\n%s", docs)
		}
		// 文档页必须自包含：任何绝对 URL 都可能在内网/离线环境里挂住整个页面。
		// The viewer must be self-contained: any absolute URL could hang the whole page on an intranet or offline host.
		for _, unwanted := range []string{"http://", "https://", "//unpkg.com", "//cdn."} {
			if strings.Contains(docs, unwanted) {
				t.Errorf("GET /docs 含外部引用 %q（必须完全自包含）:\n%s", unwanted, docs)
			}
		}
	})

	t.Run("混合捕获路由", func(t *testing.T) {
		resp, _ := getText(t, client, base+"/avatar/42.jpg", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /avatar/42.jpg 状态码 = %d, 期望 200", resp.StatusCode)
		}
		// X-YD-Avatar 是控制器回显的捕获值：它等于 42 就说明 :id 与 .jpg 被正确切开。
		// X-YD-Avatar echoes the captured value: 42 proves the split between :id and the .jpg suffix.
		if got := resp.Header.Get("X-YD-Avatar"); got != "42" {
			t.Errorf("X-YD-Avatar = %q, 期望 42（混合捕获失败）", got)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "image/png") {
			t.Errorf("Content-Type = %q, 期望 image/png", ct)
		}

		// 后缀是模式的一部分：换一个后缀就不该再命中。
		// The suffix is part of the pattern: any other suffix must no longer match.
		respMiss, _ := getText(t, client, base+"/avatar/42.png", nil)
		if respMiss.StatusCode != http.StatusNotFound {
			t.Errorf("GET /avatar/42.png 状态码 = %d, 期望 404（后缀属于路由模式）", respMiss.StatusCode)
		}

		respReport, _ := getText(t, client, base+"/report/7-xx.png", nil)
		if respReport.StatusCode != http.StatusOK {
			t.Fatalf("GET /report/7-xx.png 状态码 = %d, 期望 200", respReport.StatusCode)
		}
		if got := respReport.Header.Get("X-YD-Report"); got != "7" {
			t.Errorf("X-YD-Report = %q, 期望 7（动态段 + 静态后缀被正确切开）", got)
		}
	})

	t.Run("静态资产", func(t *testing.T) {
		resp, body := getText(t, client, base+"/static/js/vendor/htmx.min.js", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /static/js/vendor/htmx.min.js 状态码 = %d, 期望 200", resp.StatusCode)
		}
		if len(body) < 1000 || !strings.Contains(body, "htmx") {
			t.Errorf("htmx.min.js 内容异常（长度 %d 字节）", len(body))
		}
	})

	t.Run("SSE 推送时钟片段", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, base+"/events", nil)
		if err != nil {
			t.Fatalf("构造 SSE 请求失败: %v", err)
		}
		// 流式端点同样在受保护组里：浏览器靠 cookie（Promote 提升），脚本客户端直接带请求头。
		// The streaming endpoint is protected as well: a browser relies on the cookie that Promote lifts while a scripted
		// client sends the header directly.
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := streamClient.Do(req)
		if err != nil {
			t.Fatalf("GET /events 失败: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /events 状态码 = %d, 期望 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
			t.Fatalf("GET /events Content-Type = %q, 期望 text/event-stream", ct)
		}

		// 推送器启动时立即发一帧，因此第一秒内就该读到带 hx-partial 信封的数据行；
		// 读取放在协程里，主协程用超时兜底，绝不因为一条挂住的流让整个用例卡死。
		// The publisher sends a frame the moment it starts, so a data line carrying an hx-partial envelope should arrive
		// within the first second. The read runs in a goroutine with a timeout on the main one, so a stuck stream can
		// never wedge the case.
		frames := make(chan string, 1)
		go func() {
			buf := bufio.NewReader(resp.Body)
			var acc strings.Builder
			for {
				line, err := buf.ReadString('\n')
				acc.WriteString(line)
				if err != nil {
					return
				}
				if strings.Contains(acc.String(), "<hx-partial") && strings.Contains(acc.String(), `hx-target="#yd-clock"`) {
					frames <- acc.String()
					return
				}
			}
		}()

		select {
		case frame := <-frames:
			for _, want := range []string{"event: yd.events", "<hx-partial", `hx-target="#yd-clock"`, "<time"} {
				if !strings.Contains(frame, want) {
					t.Errorf("SSE 帧缺少 %q:\n%s", want, frame)
				}
			}
		case <-time.After(sseTimeout):
			t.Fatalf("%s 内未收到 SSE 推送帧", sseTimeout)
		}
	})

	t.Run("WebSocket 回显与推送", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), sseTimeout)
		defer cancel()

		// 用框架自带的客户端握手：与浏览器相同的子协议，且会校验 Sec-WebSocket-Accept；
		// 升级请求也要带令牌，否则握手会被鉴权中间件挡在 401 上。
		// The handshake uses the framework's own client: the same subprotocol a browser asks for, with the
		// Sec-WebSocket-Accept response verified. The upgrade request has to carry the token too, otherwise the auth
		// middleware stops it at 401.
		conn, err := ws.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws", ws.DialOptions{
			Subprotocols: []string{"htmx"},
			Headers:      http.Header{"Authorization": []string{"Bearer " + token}},
		})
		if err != nil {
			t.Fatalf("WebSocket 握手失败: %v", err)
		}
		defer func() { _ = conn.CloseNow() }()

		// OnOpen 里的推送协程立刻写一帧，因此第一条消息就是时钟片段。
		// The push goroutine started in OnOpen writes one frame immediately, so the first message is the clock fragment.
		if err := conn.SetReadDeadline(time.Now().Add(sseTimeout)); err != nil {
			t.Fatalf("设置读超时失败: %v", err)
		}
		op, frame, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("读取服务端推送失败: %v", err)
		}
		if op != ws.TextMessage {
			t.Fatalf("推送帧类型 = %v, 期望文本帧", op)
		}
		for _, want := range []string{"<hx-partial", `hx-target="#yd-clock"`} {
			if !strings.Contains(string(frame), want) {
				t.Errorf("推送帧缺少 %q:\n%s", want, frame)
			}
		}

		// htmx 信封的 BODY 由默认回显实现原样送回；推送与回显可能交错，因此循环读到回显为止。
		// The envelope BODY is echoed verbatim by the default implementation; pushes and echoes can interleave, so the
		// loop reads until the echo shows up.
		if err := conn.WriteMessage(ws.TextMessage, []byte(`{"BODY":"ping"}`)); err != nil {
			t.Fatalf("写出 htmx 信封失败: %v", err)
		}
		echoed := false
		for deadline := time.Now().Add(sseTimeout); time.Now().Before(deadline); {
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatalf("设置读超时失败: %v", err)
			}
			_, got, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if strings.Contains(string(got), "ping") {
				echoed = true
				break
			}
		}
		if !echoed {
			t.Error("WebSocket 未把 htmx 信封的 BODY 回显回来")
		}
	})

	t.Run("表单校验与列表闭环", func(t *testing.T) {
		// 校验失败（htmx 请求）：422 + 错误片段，且片段必须只是错误槽位的内容，不能是整页。
		// 登录名与邮箱的校验发生在口令解密之前，因此这两条用例可以只发普通表单字段。
		// A validation failure on an htmx request: 422 plus the error fragment, which must be the error slot's content
		// only and never a whole page. The sign-in name and the address are checked before any password is decrypted,
		// which is why these two cases may post plain form fields.
		hx := map[string]string{"HX-Request": "true"}
		resp, body := postForm(t, client, base+"/users", map[string]string{"username": "", "email": "nobody@example.com"}, hx)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("空登录名 POST /users 状态码 = %d, 期望 422\n%s", resp.StatusCode, body)
		}
		for _, want := range []string{"yd-errors__body", "user name is required"} {
			if !strings.Contains(body, want) {
				t.Errorf("422 错误片段缺少 %q:\n%s", want, body)
			}
		}
		if strings.Contains(body, "<html") {
			t.Errorf("HX-Request 的 422 响应不应是整页（表单用 hx-status:422 直接换入 #yd-errors）:\n%s", body)
		}

		// 校验失败（普通表单提交）：同样是 422，但由布局包裹成整页——浏览器禁用脚本后仍要能读到错误。
		// A validation failure on a plain form post: the same 422, wrapped in the layout as a whole page, because a
		// browser with scripting disabled still has to be able to read the error.
		respPlain, bodyPlain := postForm(t, client, base+"/users", map[string]string{"username": "", "email": "nobody@example.com"}, nil)
		if respPlain.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("非 htmx 的空登录名 POST /users 状态码 = %d, 期望 422\n%s", respPlain.StatusCode, bodyPlain)
		}
		for _, want := range []string{"<html", "yd-errors__body", "user name is required"} {
			if !strings.Contains(bodyPlain, want) {
				t.Errorf("非 htmx 的 422 整页缺少 %q:\n%s", want, bodyPlain)
			}
		}

		// 邮箱非法同样是 422，但文案不同：状态码由哨兵映射，文案来自哨兵本身。
		// A malformed e-mail is a 422 too, with different copy: the status comes from the sentinel mapping and the copy
		// from the sentinel itself.
		respBad, bodyBad := postForm(t, client, base+"/users", map[string]string{"username": "Alice", "email": "not-an-email"}, hx)
		if respBad.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("非法邮箱 POST /users 状态码 = %d, 期望 422\n%s", respBad.StatusCode, bodyBad)
		}
		if !strings.Contains(bodyBad, "invalid e-mail address") {
			t.Errorf("422 错误片段缺少邮箱校验文案:\n%s", bodyBad)
		}

		// 成功：200 + 刷新后的列表片段 + HX-Trigger 下发的 yd:toast 事件。
		// 口令必须是 SM2 密文并附上 sm2 标记头：服务端拒绝任何明文口令链路，这条路径正是登录之外的第二个解密入口。
		// Success: 200 plus the refreshed list fragment plus the yd:toast event carried by HX-Trigger. The password must
		// arrive as SM2 ciphertext with the sm2 marker header: the server refuses any plaintext password path, and this is
		// the second decryption entry point next to sign-in.
		respOK, bodyOK := postFormValues(t, client, base+"/users", url.Values{
			"username":     {"alice"},
			"email":        {"alice@example.com"},
			"display_name": {"Alice"},
			"password":     {sm2Cipher(t, client, base, probeUserPassword)},
			"roles":        {"member"},
		}, map[string]string{"HX-Request": "true", "X-YD-Encrypted": "sm2"})
		if respOK.StatusCode != http.StatusOK {
			t.Fatalf("POST /users 状态码 = %d, 期望 200\n%s", respOK.StatusCode, bodyOK)
		}
		if trigger := respOK.Header.Get("HX-Trigger"); !strings.Contains(trigger, "yd:toast") {
			t.Errorf("HX-Trigger = %q, 期望包含 yd:toast", trigger)
		}
		if !strings.Contains(bodyOK, "alice@example.com") {
			t.Errorf("创建后返回的列表片段不含新用户:\n%s", bodyOK)
		}

		// 列表与关键字筛选都返回片段（HX-Request），无匹配时是 200 + 空状态而不是 404。
		// Both the list and its keyword filter answer with fragments (HX-Request), and a filter without a match is a 200
		// plus the empty state rather than a 404.
		respList, list := getText(t, client, base+"/users?q=alice", map[string]string{"HX-Request": "true"})
		if respList.StatusCode != http.StatusOK || !strings.Contains(list, "alice@example.com") {
			t.Errorf("GET /users?q=alice 未命中新用户（状态码 %d）:\n%s", respList.StatusCode, list)
		}
		respMiss, miss := getText(t, client, base+"/users?q=nobody-here", map[string]string{"HX-Request": "true"})
		if respMiss.StatusCode != http.StatusOK {
			t.Errorf("无匹配的筛选状态码 = %d, 期望 200（内容为空状态）", respMiss.StatusCode)
		}
		if !strings.Contains(miss, "no user matches") {
			t.Errorf("无匹配的筛选未返回空状态片段:\n%s", miss)
		}
	})
}

// 探针使用的种子凭据与契约常量：与生成项目的 internal/meta 同源，这里各留一份供断言使用，
// assertSeedCredentialsDocumented 会逐字回验一次，因此两边不会各自漂移。
// The seed credentials and contract constants the probes use: they share one source with the generated project's
// internal/meta and are kept here for the assertions, while assertSeedCredentialsDocumented verifies them verbatim so
// neither side can drift away on its own.
const (
	// probeTenant 是演示租户的标识（登录表单的租户字段）。 | probeTenant is the demo tenant's key (the sign-in form's tenant field).
	probeTenant = "demo"
	// probeAdmin 是演示租户的管理员登录名。 | probeAdmin is the demo tenant's administrator sign-in name.
	probeAdmin = "admin"
	// probeAdminPassword 是演示租户管理员的初始口令。 | probeAdminPassword is the demo administrator's initial password.
	probeAdminPassword = "Admin@123456"
	// probeOpsTenant 是第二个租户的标识，也是跨租户用例里「另一个租户」的名字。
	// probeOpsTenant is the second tenant's key and the name of "the other tenant" in the cross-tenant cases.
	probeOpsTenant = "acme"
	// probeOpsAdmin 是第二个租户的管理员登录名。 | probeOpsAdmin is the second tenant's administrator sign-in name.
	probeOpsAdmin = "acme-admin"
	// probeOpsPassword 是第二个租户管理员的初始口令。 | probeOpsPassword is the second tenant administrator's initial password.
	probeOpsPassword = "Acme@123456"
	// probeUserPassword 是探针自己建的账号所用的口令，长度满足口令强度下限。
	// probeUserPassword is the password of the accounts the probe creates itself, long enough to clear the strength floor.
	probeUserPassword = "Probe@123456"
	// tokenCookieName 是会话 cookie 名，与生成项目的 internal/meta.TokenCookie 一致。
	// tokenCookieName is the session cookie name, matching internal/meta.TokenCookie of the generated project.
	tokenCookieName = "yd_token"
	// forbiddenCopy 是越权兜底文案（controller.msgForbidden）的中文半句：出现它就说明 403 是文档化的拒绝。
	// forbiddenCopy is the Chinese half of the escalation fallback copy (controller.msgForbidden): its presence proves the
	// 403 is the documented refusal.
	forbiddenCopy = "没有权限执行该操作"
)

// assertSeedCredentialsDocumented 逐字回验种子凭据与首登改密提示确实落在生成项目的源码与 README 里。
// assertSeedCredentialsDocumented verifies verbatim that the seed credentials and the first-sign-in notice really land in
// the generated project's source and README.
func assertSeedCredentialsDocumented(t *testing.T, projDir string) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(projDir, "internal", "meta", "meta.go"))
	if err != nil {
		t.Fatalf("读取 internal/meta/meta.go 失败: %v", err)
	}
	for _, want := range []string{
		`SeedTenantSlug = "` + probeTenant + `"`,
		`SeedAdminUsername = "` + probeAdmin + `"`,
		`SeedAdminPassword = "` + probeAdminPassword + `"`,
		`SeedOpsSlug = "` + probeOpsTenant + `"`,
		`SeedOpsAdminUsername = "` + probeOpsAdmin + `"`,
		`SeedOpsAdminPassword = "` + probeOpsPassword + `"`,
		`SeedPasswordNotice = "`,
		`TokenCookie = "` + tokenCookieName + `"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("internal/meta/meta.go 缺少 %q", want)
		}
	}

	readme, err := os.ReadFile(filepath.Join(projDir, "README.md"))
	if err != nil {
		t.Fatalf("读取 README.md 失败: %v", err)
	}
	// README 是默认凭据与改密提醒唯一的对外落点：两个口令与那句提示都必须在这里出现。
	// The README is the one outward landing place of the default credentials and the password notice: both passwords and
	// that very sentence have to show up in it.
	for _, want := range []string{probeAdminPassword, probeOpsPassword, "SeedPasswordNotice", probeTenant, probeOpsTenant} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README.md 缺少 %q", want)
		}
	}
}

// assertNoUserData 断言响应里一行用户数据都没有：任何一条用户记录都会带上邮箱，因此域名出现即为泄漏。
// assertNoUserData asserts the response carries not a single user record: every one of them comes with an address, so the
// presence of the domain is the leak.
func assertNoUserData(t *testing.T, where, body string) {
	t.Helper()

	for _, leak := range []string{"@example.com", probeOpsAdmin, probeAdmin + "@"} {
		if strings.Contains(body, leak) {
			t.Errorf("%s 泄漏了用户数据 %q:\n%s", where, leak, body)
		}
	}
}

// TestGeneratedAppSupportsAuth 端到端验证登录鉴权链路：未登录被拒、默认凭据 + SM2 密文登录、令牌访问受保护片段，
// 以及两条拒绝路径——跨租户提权与能力不足。
// TestGeneratedAppSupportsAuth runs the authentication chain end to end: the anonymous refusal, sign-in with the default
// credentials and an SM2 payload, token access to a protected fragment, and the two refusal paths — cross-tenant
// escalation and a missing capability.
//
// 探针只用框架自己的 guomi 构造请求，不依赖浏览器：口令按前端的方式加密成标准 Base64 的 SM2 密文，
// 令牌从登录响应的 cookie 里取出来，此后既可以用 cookie（浏览器形态）也可以用 Authorization 请求头（API 形态）。
// The probe builds its requests with the framework's own guomi package and needs no browser: the password is encrypted the
// way the front end does it — standard Base64 over an SM2 ciphertext — and the token is read out of the sign-in response's
// cookie, after which it is usable both as a cookie (the browser shape) and as an Authorization header (the API shape).
func TestGeneratedAppSupportsAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实鉴权验证 | skipped in short mode")
	}

	projDir, base := startProbeApp(t, "yd-auth-probe")
	assertSeedCredentialsDocumented(t, projDir)

	t.Run("未登录被挡在受保护端点之外", func(t *testing.T) {
		anon := newProbeClient(t, httpTimeout)

		// 普通浏览请求：303 到登录页，而不是把 / 的内容直接吐出来。
		// A plain browsing request: a 303 to the sign-in page instead of the content of /.
		resp, body := getText(t, anon, base+"/", nil)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("未登录 GET / 状态码 = %d, 期望 303\n%s", resp.StatusCode, body)
		}
		if loc := resp.Header.Get("Location"); loc != "/login" {
			t.Errorf("未登录 GET / 的 Location = %q, 期望 /login", loc)
		}
		assertNoUserData(t, "未登录 GET /", body)

		// htmx 与 API 客户端拿到 401 片段：把它们丢到登录整页上毫无帮助。
		// htmx and API clients get the 401 fragment: dropping them on a whole sign-in page helps nobody.
		respHX, bodyHX := getText(t, anon, base+"/", map[string]string{"HX-Request": "true"})
		if respHX.StatusCode != http.StatusUnauthorized {
			t.Fatalf("未登录 HX-Request GET / 状态码 = %d, 期望 401\n%s", respHX.StatusCode, bodyHX)
		}
		if strings.Contains(bodyHX, "<html") {
			t.Errorf("未登录的 401 应是错误片段而不是整页:\n%s", bodyHX)
		}
		assertNoUserData(t, "未登录 HX-Request GET /", bodyHX)

		// 用户列表对匿名请求同样只有 401，一行用户都不给。
		// The user list answers an anonymous request with the same 401 and not one row.
		respUsers, users := getText(t, anon, base+"/users", map[string]string{"HX-Request": "true"})
		if respUsers.StatusCode != http.StatusUnauthorized {
			t.Fatalf("未登录 GET /users 状态码 = %d, 期望 401\n%s", respUsers.StatusCode, users)
		}
		assertNoUserData(t, "未登录 GET /users", users)

		// 登录页本身必须可达：拒绝要给出一条出路。
		// The sign-in page itself stays reachable: a refusal has to leave a way forward.
		respLogin, login := getText(t, anon, base+"/login", nil)
		if respLogin.StatusCode != http.StatusOK {
			t.Fatalf("GET /login 状态码 = %d, 期望 200\n%s", respLogin.StatusCode, login)
		}
		if !strings.Contains(login, `data-yd-page="login"`) {
			t.Errorf("GET /login 不是登录页:\n%s", login)
		}
	})

	t.Run("默认凭据加 SM2 密文登录拿到令牌", func(t *testing.T) {
		anon := newProbeClient(t, httpTimeout)

		// 明文口令没有 sm2 标记头：整条明文链路都必须被拒绝，否则国密前置加密就形同虚设。
		// A plaintext password without the sm2 marker header: the whole plaintext path has to be refused, otherwise the
		// front-loaded national-cryptography encryption would be decoration.
		respPlain, bodyPlain := postForm(t, anon, base+"/login", map[string]string{
			"tenant": probeTenant, "username": probeAdmin, "password": probeAdminPassword,
		}, nil)
		if respPlain.StatusCode != http.StatusBadRequest {
			t.Fatalf("明文口令 POST /login 状态码 = %d, 期望 400\n%s", respPlain.StatusCode, bodyPlain)
		}
		if token := sessionToken(t, anon, base); token != "" {
			t.Error("被拒绝的登录不应写下会话 cookie")
		}

		// 口令错误与租户不存在回同一句、同一个状态码：登录接口匿名可达，任何差异都是租户枚举的旁路。
		// A wrong password and an unknown tenant answer with one sentence and one status: the endpoint is anonymous, so any
		// difference would be a side channel for enumerating tenants.
		for name, form := range map[string]map[string]string{
			"口令错误":  {"tenant": probeTenant, "username": probeAdmin, "password": sm2Cipher(t, anon, base, "Wrong@123456")},
			"租户不存在": {"tenant": "no-such-tenant", "username": probeAdmin, "password": sm2Cipher(t, anon, base, probeAdminPassword)},
		} {
			resp, body := postForm(t, anon, base+"/login", form, map[string]string{"X-YD-Encrypted": "sm2"})
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s POST /login 状态码 = %d, 期望 401\n%s", name, resp.StatusCode, body)
			}
			if !strings.Contains(body, "Wrong tenant, user name or password") {
				t.Errorf("%s 的 401 未回显统一文案:\n%s", name, body)
			}
			if token := sessionToken(t, anon, base); token != "" {
				t.Errorf("%s 之后不应有会话 cookie", name)
			}
		}

		// 成功路径：默认凭据 + SM2 密文 → 303 到工作区 + HttpOnly 的会话 cookie。
		// The success path: the default credentials plus an SM2 payload → a 303 to the workspace and an HttpOnly session cookie.
		browser := newProbeClient(t, httpTimeout)
		resp, body := postForm(t, browser, base+"/login", map[string]string{
			"tenant": probeTenant, "username": probeAdmin, "password": sm2Cipher(t, browser, base, probeAdminPassword),
		}, map[string]string{"X-YD-Encrypted": "sm2"})
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("登录 POST /login 状态码 = %d, 期望 303\n%s", resp.StatusCode, body)
		}
		if loc := resp.Header.Get("Location"); loc != "/" {
			t.Errorf("登录成功后的 Location = %q, 期望 /", loc)
		}

		var session *http.Cookie
		for _, c := range resp.Cookies() {
			if c.Name == tokenCookieName {
				session = c
			}
		}
		if session == nil {
			t.Fatalf("登录响应没有写入 %s cookie", tokenCookieName)
		}
		if !session.HttpOnly {
			t.Error("会话 cookie 必须是 HttpOnly（脚本拿不走会话）")
		}
		if session.Value == "" {
			t.Error("会话 cookie 的令牌为空")
		}
		if token := sessionToken(t, browser, base); token != session.Value {
			t.Errorf("cookie jar 里的令牌 = %q, 响应里的令牌 = %q", token, session.Value)
		}
	})

	t.Run("带令牌访问受保护端点返回片段", func(t *testing.T) {
		browser := newProbeClient(t, httpTimeout)
		token := signIn(t, browser, base, probeTenant, probeAdmin, probeAdminPassword)

		// 浏览器形态：请求里只有 cookie，没有 Authorization，令牌由中间件 Promote 提升上来的。
		// The browser shape: the request carries the cookie and no Authorization at all, so the token must have been
		// lifted by middleware.Promote.
		resp, body := getText(t, browser, base+"/", map[string]string{"HX-Request": "true"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("已登录 HX-Request GET / 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		if strings.Contains(body, "<html") {
			t.Errorf("HX-Request 请求不应返回整页 HTML:\n%s", body)
		}
		if !strings.Contains(body, `data-yd-page="home"`) {
			t.Errorf("已登录的 HX-Request 响应不是首页片段:\n%s", body)
		}

		// API 形态：显式 Authorization、没有 cookie，走的是同一条校验路径。
		// The API shape: an explicit Authorization with no cookie, verified along the very same path.
		api := newProbeClient(t, httpTimeout)
		respAPI, bodyAPI := getText(t, api, base+"/users", map[string]string{
			"HX-Request":    "true",
			"Authorization": "Bearer " + token,
		})
		if respAPI.StatusCode != http.StatusOK {
			t.Fatalf("带令牌 GET /users 状态码 = %d, 期望 200\n%s", respAPI.StatusCode, bodyAPI)
		}
		if strings.Contains(bodyAPI, "<html") {
			t.Errorf("HX-Request 的用户列表不应是整页:\n%s", bodyAPI)
		}
		for _, want := range []string{`data-yd-page="users"`, probeAdmin + "@example.com"} {
			if !strings.Contains(bodyAPI, want) {
				t.Errorf("带令牌的用户列表片段缺少 %q:\n%s", want, bodyAPI)
			}
		}
	})

	t.Run("跨租户提权被拒绝且不泄漏数据", func(t *testing.T) {
		browser := newProbeClient(t, httpTimeout)
		token := signIn(t, browser, base, probeTenant, probeAdmin, probeAdminPassword)

		// demo 的令牌 + acme 的请求头：令牌声明优先，请求头一旦指向别的租户就是提权，一律 403。
		// The demo token with the acme header: the token's claim wins and a header naming another tenant is escalation, refused with 403.
		resp, body := getText(t, browser, base+"/users", map[string]string{
			"HX-Request": "true",
			"X-Tenant":   probeOpsTenant,
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("跨租户 GET /users 状态码 = %d, 期望 403\n%s", resp.StatusCode, body)
		}
		if !strings.Contains(body, forbiddenCopy) {
			t.Errorf("跨租户的 403 未回显越权文案:\n%s", body)
		}
		assertNoUserData(t, "跨租户 GET /users", body)

		// 同一枚令牌用 API 形态（显式请求头）提权，结果必须一模一样。
		// The same token escalating in the API shape (an explicit header) has to end exactly the same way.
		api := newProbeClient(t, httpTimeout)
		respAPI, bodyAPI := getText(t, api, base+"/users", map[string]string{
			"HX-Request":    "true",
			"Authorization": "Bearer " + token,
			"X-Tenant":      probeOpsTenant,
		})
		if respAPI.StatusCode != http.StatusForbidden {
			t.Fatalf("API 形态跨租户 GET /users 状态码 = %d, 期望 403\n%s", respAPI.StatusCode, bodyAPI)
		}
		assertNoUserData(t, "API 形态跨租户 GET /users", bodyAPI)

		// 反向确认：同一枚令牌指向自己的租户时数据照常返回，因此上面的 403 说的是「租户对不上」而不是「令牌坏了」。
		// The reverse check: the same token naming its own tenant returns data as usual, so the 403 above says "the tenant
		// does not match" and not "the token is broken".
		respOwn, own := getText(t, browser, base+"/users", map[string]string{
			"HX-Request": "true",
			"X-Tenant":   probeTenant,
		})
		if respOwn.StatusCode != http.StatusOK || !strings.Contains(own, probeAdmin+"@example.com") {
			t.Errorf("令牌指向自己租户时未返回数据（状态码 %d）:\n%s", respOwn.StatusCode, own)
		}

		// 另一个租户的令牌在自己租户里同样是好的，换成 demo 的请求头才被拒绝，且不泄漏 demo 侧的任何一行数据。
		// The other tenant's token works just as well inside its own tenant and is refused only when it names demo, without
		// leaking a single row of demo's data.
		ops := newProbeClient(t, httpTimeout)
		signIn(t, ops, base, probeOpsTenant, probeOpsAdmin, probeOpsPassword)
		respOps, opsBody := getText(t, ops, base+"/users", map[string]string{"HX-Request": "true"})
		if respOps.StatusCode != http.StatusOK || !strings.Contains(opsBody, probeOpsAdmin+"@example.com") {
			t.Fatalf("acme 令牌读自己的用户列表失败（状态码 %d）:\n%s", respOps.StatusCode, opsBody)
		}
		respCross, crossBody := getText(t, ops, base+"/users", map[string]string{
			"HX-Request": "true",
			"X-Tenant":   probeTenant,
		})
		if respCross.StatusCode != http.StatusForbidden {
			t.Fatalf("acme 令牌读 demo 用户列表状态码 = %d, 期望 403\n%s", respCross.StatusCode, crossBody)
		}
		assertNoUserData(t, "acme 令牌读 demo 用户列表", crossBody)
	})

	t.Run("缺少能力的令牌访问高权限端点被拒绝", func(t *testing.T) {
		// 用 demo 管理员建两个账号：member 没有任何能力，auditor 只有读能力。
		// Two accounts created by the demo administrator: member holds nothing and auditor holds read capabilities only.
		admin := newProbeClient(t, httpTimeout)
		adminToken := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
		createProbeUser(t, admin, base, adminToken, "probe-member", "member", "probe-member@example.com")
		createProbeUser(t, admin, base, adminToken, "probe-auditor", "auditor", "probe-auditor@example.com")

		hx := map[string]string{"HX-Request": "true"}

		// member：令牌本身有效（会话即可访问的首页给 200），但用户列表要 user:read → 403。
		// member: the token itself is valid (the session-only home page answers 200) while the user list needs user:read → 403.
		member := newProbeClient(t, httpTimeout)
		signIn(t, member, base, probeTenant, "probe-member", probeUserPassword)
		respHome, home := getText(t, member, base+"/", hx)
		if respHome.StatusCode != http.StatusOK {
			t.Fatalf("member 访问首页状态码 = %d, 期望 200（令牌有效）\n%s", respHome.StatusCode, home)
		}
		respMember, memberBody := getText(t, member, base+"/users", hx)
		if respMember.StatusCode != http.StatusForbidden {
			t.Fatalf("member GET /users 状态码 = %d, 期望 403\n%s", respMember.StatusCode, memberBody)
		}
		if !strings.Contains(memberBody, forbiddenCopy) {
			t.Errorf("member 的 403 未回显越权文案:\n%s", memberBody)
		}
		assertNoUserData(t, "member GET /users", memberBody)

		// auditor：读能力在（列表 200，且能看到自己），写能力不在（新建租户 403）。
		// auditor: the read capability is there (the list answers 200 and shows the account itself) and the write one is not
		// (creating a tenant is a 403).
		auditor := newProbeClient(t, httpTimeout)
		signIn(t, auditor, base, probeTenant, "probe-auditor", probeUserPassword)
		respAud, audBody := getText(t, auditor, base+"/users", hx)
		if respAud.StatusCode != http.StatusOK || !strings.Contains(audBody, "probe-auditor@example.com") {
			t.Fatalf("auditor GET /users 状态码 = %d, 期望 200 且能看到自己\n%s", respAud.StatusCode, audBody)
		}
		respWrite, writeBody := postForm(t, auditor, base+"/tenants", map[string]string{
			"name": "Escalation probe",
			"slug": "escalation-probe",
		}, hx)
		if respWrite.StatusCode != http.StatusForbidden {
			t.Fatalf("auditor POST /tenants 状态码 = %d, 期望 403\n%s", respWrite.StatusCode, writeBody)
		}
		if !strings.Contains(writeBody, forbiddenCopy) {
			t.Errorf("auditor 的 403 未回显越权文案:\n%s", writeBody)
		}

		// 越权写入确实没有发生：管理员再读一次租户列表，那个标识不在里面。
		// The refused write really did not happen: the administrator reads the tenant list once more and that key is not in it.
		respTenants, tenants := getText(t, admin, base+"/tenants", hx)
		if respTenants.StatusCode != http.StatusOK {
			t.Fatalf("管理员 GET /tenants 状态码 = %d, 期望 200\n%s", respTenants.StatusCode, tenants)
		}
		if strings.Contains(tenants, "escalation-probe") {
			t.Errorf("被拒绝的越权写入居然生效了:\n%s", tenants)
		}
	})
}

// TestGeneratedI18nCatalogueCoversEveryTrKey 断言模板用到的 tr 键与两份语言目录的键集合严格一致。
// TestGeneratedI18nCatalogueCoversEveryTrKey asserts the tr keys used by the templates and the key sets of both
// catalogues agree exactly.
//
// 少一个键，页面上出现的就是键名本身；多一个键，则是一句永远不会被读到的文案。
// A missing key makes the page show the key itself, and a stale one is a sentence nobody ever reads.
func TestGeneratedI18nCatalogueCoversEveryTrKey(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("i18n-app"))

	used := map[string]string{}
	for _, rel := range requiredFiles {
		if !strings.HasPrefix(rel, "web/templates/") || !strings.HasSuffix(rel, ".html") {
			continue
		}
		for _, match := range trKeyPattern.FindAllStringSubmatch(pr.read(rel), -1) {
			if _, seen := used[match[1]]; !seen {
				used[match[1]] = rel
			}
		}
	}
	if len(used) == 0 {
		t.Fatal("模板里没有找到任何 tr 键")
	}

	zh := readCatalogue(t, pr, "web/i18n/zh-CN.yaml")
	en := readCatalogue(t, pr, "web/i18n/en.yaml")

	for key, where := range used {
		if _, ok := zh[key]; !ok {
			t.Errorf("zh-CN.yaml 缺少 %q（%s 用到）", key, where)
		}
		if _, ok := en[key]; !ok {
			t.Errorf("en.yaml 缺少 %q（%s 用到）", key, where)
		}
	}
	for key := range zh {
		if _, ok := used[key]; !ok {
			t.Errorf("zh-CN.yaml 的 %q 没有任何模板引用", key)
		}
	}
	for key := range en {
		if _, ok := used[key]; !ok {
			t.Errorf("en.yaml 的 %q 没有任何模板引用", key)
		}
	}
	// 两份目录的键集合必须严格一致：一边多出键意味着另一边会显示出键名。
	// The two catalogues must agree exactly: a key living in only one of them means the other shows the key name.
	if len(zh) != len(en) {
		t.Errorf("语言目录键数不一致: zh-CN=%d, en=%d", len(zh), len(en))
	}
	t.Logf("i18n 覆盖: %d 个 tr 键在 zh-CN 与 en 两份目录里都能命中", len(used))
}

// trKeyPattern 抽取模板里的 tr 键名：`tr "键"`，键名不含引号。
// trKeyPattern extracts the tr key names used by a template: `tr "key"`, where the key holds no quote.
var trKeyPattern = regexp.MustCompile(`\btr\s+"([^"]+)"`)

// readCatalogue 读取一份语言目录并把嵌套结构摊平成点分键，规则与 i18n.drill 一致（每段小写）。
// readCatalogue reads one catalogue and flattens the nested structure into dotted keys with the same rule i18n.drill
// uses (every segment lower-cased).
func readCatalogue(t *testing.T, pr project, rel string) map[string]string {
	t.Helper()

	var raw map[string]any
	if err := yaml.Unmarshal([]byte(pr.read(rel)), &raw); err != nil {
		t.Fatalf("%s 不是合法 YAML: %v", rel, err)
	}

	flat := map[string]string{}
	var walk func(prefix string, node map[string]any)
	walk = func(prefix string, node map[string]any) {
		for key, value := range node {
			dotted := strings.ToLower(key)
			if prefix != "" {
				dotted = prefix + "." + strings.ToLower(key)
			}
			if nested, ok := value.(map[string]any); ok {
				walk(dotted, nested)
				continue
			}
			flat[dotted] = ""
		}
	}
	walk("", raw)
	if len(flat) == 0 {
		t.Fatalf("%s 里没有任何键", rel)
	}
	return flat
}

// startProbeApp 生成项目、编译它并在随机端口上启动，返回项目目录与基地址。
// startProbeApp generates the project, compiles it and starts it on a random free port, returning the project directory
// and the base URL.
func startProbeApp(t *testing.T, name string) (string, string) {
	t.Helper()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("未找到 go 工具链，跳过端到端验证: %v", err)
	}

	dir := t.TempDir()
	o := scaffold.DefaultOptions(name)
	// 与端到端验证无关的依赖一律不引入：模板、路由与鉴权链路都不需要数据库。
	// Anything unrelated to the end-to-end checks is left out: templates, routes and the authentication chain need no database.
	o.DB = scaffold.DBNone
	o.LocalReplace = filepath.ToSlash(repoRoot(t))
	if _, err := scaffold.Generate(context.Background(), o, afs.NewOS(dir), nil); err != nil {
		t.Fatalf("真实生成失败: %v", err)
	}

	projDir := filepath.Join(dir, o.Name)
	bin := filepath.Join(projDir, "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	output, err := runGo(goBin, projDir, true, "build", "-mod=mod", "-o", bin, "./cmd/app")
	if err != nil {
		t.Logf("GOPROXY=off 编译失败，回退到环境代理重试:\n%s", output)
		output, err = runGo(goBin, projDir, false, "build", "-mod=mod", "-o", bin, "./cmd/app")
	}
	if err != nil {
		t.Fatalf("生成的骨架无法编译: %v\n%s", err, output)
	}
	return projDir, startGeneratedApp(t, bin, projDir)
}

// newProbeClient 返回端到端探针使用的客户端：带 cookie jar（浏览器会话）、固定英文文案、且从不自行跟随重定向。
// newProbeClient returns the client the end-to-end probes use: it carries a cookie jar (the browser session), pins the
// copy to English and never follows a redirect on its own.
//
// 语言固定为英文的理由：断言咬住的是文案本身（空状态、错误提示），而默认语言是中文；
// Accept-Language 是 i18n 中间件四个来源里的最后一个，永不与模板里的 tr 键冲突。
// The language is pinned to English because the assertions bite on the copy itself (empty states, error hints) while the
// default language is Chinese; Accept-Language is the last of the i18n middleware's four sources and never clashes with a
// tr key written in a template.
func newProbeClient(t *testing.T, timeout time.Duration) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("构造 cookie jar 失败: %v", err)
	}
	return &http.Client{
		Timeout:   timeout,
		Jar:       jar,
		Transport: langTransport{},
		// 不跟随重定向：303 与它的 Location 本身就是被测契约的一部分。
		// Redirects are never followed: the 303 and its Location are part of the contract under test.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// langTransport 给每条请求补上 Accept-Language: en（已有取值时不动）。
// langTransport appends Accept-Language: en to every request that does not carry one.
type langTransport struct{}

// RoundTrip 实现 http.RoundTripper。 | RoundTrip implements http.RoundTripper.
func (langTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "en")
	}
	return http.DefaultTransport.RoundTrip(req)
}

// sm2Cipher 取服务端公钥并把明文口令加密成前端提交的形态：原始 C1C3C2 密文的标准 Base64。
// sm2Cipher fetches the server's public key and encrypts a clear-text password into the shape the front end submits:
// standard Base64 over the raw C1C3C2 ciphertext.
func sm2Cipher(t *testing.T, client *http.Client, base, plain string) string {
	t.Helper()

	resp, body := getText(t, client, base+"/guomi/pubkey", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /guomi/pubkey 状态码 = %d, 期望 200", resp.StatusCode)
	}
	pub, err := guomi.PublicKeyFromHex(strings.TrimSpace(body))
	if err != nil {
		t.Fatalf("解析 SM2 公钥失败: %v", err)
	}
	cipher, err := guomi.EncryptRaw(pub, []byte(plain))
	if err != nil {
		t.Fatalf("SM2 加密失败: %v", err)
	}
	return b64x.StdEncoding.EncodeToString(cipher)
}

// signIn 用默认凭据（或任意账号）完成一次登录，返回会话令牌。
// signIn performs one sign-in with the default credentials (or any account) and returns the session token.
//
// 口令按前端的方式加密后提交：登录接口不接受任何明文口令。
// The password is encrypted the way the front end does it: the endpoint accepts no clear-text password at all.
func signIn(t *testing.T, client *http.Client, base, tenant, username, password string) string {
	t.Helper()

	resp, body := postForm(t, client, base+"/login", map[string]string{
		"tenant":   tenant,
		"username": username,
		"password": sm2Cipher(t, client, base, password),
	}, map[string]string{"X-YD-Encrypted": "sm2"})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("登录 %s@%s 状态码 = %d, 期望 303\n%s", username, tenant, resp.StatusCode, body)
	}

	token := sessionToken(t, client, base)
	if token == "" {
		t.Fatalf("登录 %s@%s 成功后 cookie jar 里没有 %s", username, tenant, tokenCookieName)
	}
	return token
}

// sessionToken 从 cookie jar 里读出会话令牌，空串表示这个客户端还没有会话。
// sessionToken reads the session token out of the cookie jar; an empty string means the client holds no session.
func sessionToken(t *testing.T, client *http.Client, base string) string {
	t.Helper()

	if client.Jar == nil {
		t.Fatal("探针客户端必须带 cookie jar")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("解析基地址 %q 失败: %v", base, err)
	}
	for _, cookie := range client.Jar.Cookies(u) {
		if cookie.Name == tokenCookieName && cookie.Value != "" {
			return cookie.Value
		}
	}
	return ""
}

// createProbeUser 以管理员身份建一个账号（已登录的客户端 + 显式令牌），并断言新账号出现在返回的列表片段里。
// createProbeUser creates an account as the administrator (a signed-in client plus an explicit token) and asserts the new
// account shows up in the returned list fragment.
//
// 口令同样只能是 SM2 密文：建号是登录之外的第二个解密入口，明文一律拒绝。
// The password can only be SM2 ciphertext here as well: user creation is the second decryption entry point beside sign-in
// and plaintext is refused outright.
func createProbeUser(t *testing.T, client *http.Client, base, token, username, role, email string) {
	t.Helper()

	resp, body := postFormValues(t, client, base+"/users", url.Values{
		"username":     {username},
		"email":        {email},
		"display_name": {username},
		"password":     {sm2Cipher(t, client, base, probeUserPassword)},
		"roles":        {role},
	}, map[string]string{
		"HX-Request":     "true",
		"X-YD-Encrypted": "sm2",
		"Authorization":  "Bearer " + token,
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, email) {
		t.Fatalf("建号 %s 失败（状态码 %d）:\n%s", username, resp.StatusCode, body)
	}
}

// startGeneratedApp 在随机空闲端口上启动生成的应用，返回基地址，并在用例结束时终止进程。
// startGeneratedApp starts the generated application on a random free port, returns its base URL and kills the process
// when the case ends.
//
// 端口经 YD_SERVER_PORT 注入（映射到 server.port），因此不与开发机上任何常驻服务抢端口；
// 启动输出被收集起来，就绪等待超时时一并打印，排障不必再手动复现。
// The port is injected through YD_SERVER_PORT (mapped onto server.port), so no resident service on the developer machine
// can collide; the startup output is collected and printed when the readiness wait times out, which keeps debugging free
// of manual reproduction.
func startGeneratedApp(t *testing.T, bin, dir string) string {
	t.Helper()

	port := freePort(t)
	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOWORK=off",
		// 探针只从本机回环访问：绑定 127.0.0.1 既不依赖通配地址的绑定权限，也不会把端口暴露到局域网。
		// The probe only ever reaches the app over the loopback interface: binding 127.0.0.1 needs no wildcard-bind
		// permission and never exposes the port to the local network.
		"YD_SERVER_HOST=127.0.0.1",
		"YD_SERVER_PORT="+strconv.Itoa(port),
		"YD_LOG_FORMAT=console",
	)
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动生成的应用失败: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	probe := &http.Client{Timeout: httpTimeout}
	deadline := time.Now().Add(startupTimeout)
	for ready := false; !ready; {
		resp, err := probe.Get(base + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			ready = resp.StatusCode == http.StatusOK
		}
		if !ready && time.Now().After(deadline) {
			t.Fatalf("生成的应用未在 %s 内就绪:\n%s", startupTimeout, logs.String())
		}
		if !ready {
			time.Sleep(100 * time.Millisecond)
		}
	}

	t.Logf("生成的应用已就绪: %s", base)
	return base
}

// freePort 向内核申请一个空闲端口并立即释放，把它留给生成的应用独占监听。
// freePort asks the kernel for a free port and releases it at once, leaving it for the generated application to bind.
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请空闲端口失败: %v", err)
	}
	defer func() { _ = listener.Close() }()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("监听地址类型异常: %T", listener.Addr())
	}
	return addr.Port
}

// getText 发起 GET 请求并返回响应与响应体文本，附加请求头由 headers 给出。
// getText performs a GET request and returns the response together with its body as text, with extra headers applied.
func getText(t *testing.T, client *http.Client, url string, headers map[string]string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return doText(t, client, req)
}

// postForm 提交一份单值表单；headers 用来区分 htmx 请求（HX-Request: true）与普通表单提交，两者拿到的错误响应形态不同。
// postForm submits a single-valued form; headers tell an htmx request (HX-Request: true) apart from a plain form post,
// which get different shapes of error response.
func postForm(t *testing.T, client *http.Client, target string, values map[string]string, headers map[string]string) (*http.Response, string) {
	t.Helper()

	form := url.Values{}
	for name, value := range values {
		form.Set(name, value)
	}
	return postFormValues(t, client, target, form, headers)
}

// postFormValues 以 application/x-www-form-urlencoded 提交表单并返回响应与响应体文本。
// postFormValues submits a form as application/x-www-form-urlencoded and returns the response with its body as text.
//
// 与 postForm 的差别只有一处：它接受 url.Values，因此可以提交重复字段（建号表单的 roles 就是多值）。
// The only difference from postForm is that it takes url.Values, so repeated fields can be submitted (the roles field of
// the user-creation form is multi-valued).
func postFormValues(t *testing.T, client *http.Client, target string, values url.Values, headers map[string]string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("构造表单请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return doText(t, client, req)
}

// frameworkStaticPath 是框架静态资源挂载点：它由 app.StaticFS 自动登记，框架侧没有业务注解块。
// frameworkStaticPath is the framework's static asset mount: app.StaticFS registers it automatically and the framework
// side carries no business annotation block for it.
const frameworkStaticPath = "/static/{filepath}"

// httpMethodKeys 是 OpenAPI 路径条目里真正的操作键，其余键（parameters 等）属于路径级字段。
// httpMethodKeys are the real operation keys inside one OpenAPI path item; the remaining keys (parameters and the like)
// are path-level fields.
var httpMethodKeys = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true,
}

// assertAnnotatedOperations 校验每个业务操作都带着注解块产出的摘要与标签：只要有一个操作退回路由树兜底的裸操作，
// 就说明该处理器的注解块缺失或没被解析，docs/developer_guidelines.md §9.2 的覆盖率要求即被破坏。
// assertAnnotatedOperations checks that every business operation carries the summary and tags produced by its annotation
// block: as soon as one operation falls back to the bare route-tree operation, that handler's annotation block is either
// missing or unparsed and the coverage rule of docs/developer_guidelines.md §9.2 is broken.
//
// 唯一的例外是框架自带的静态挂载点：它不是业务端点，也不该由生成的项目写注解。
// The only exception is the framework's own static mount: it is no business endpoint and the generated project is not
// supposed to annotate it.
func assertAnnotatedOperations(t *testing.T, doc string) {
	t.Helper()

	var parsed struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("解析 /openapi.json 失败: %v\n%s", err, doc)
	}

	annotated, fallback := 0, 0
	for path, item := range parsed.Paths {
		for method, raw := range item {
			if !httpMethodKeys[method] {
				continue
			}
			if path == frameworkStaticPath {
				fallback++
				continue
			}

			var op struct {
				Summary string   `json:"summary"`
				Tags    []string `json:"tags"`
			}
			if err := json.Unmarshal(raw, &op); err != nil {
				t.Errorf("解析 %s %s 的操作对象失败: %v", method, path, err)
				continue
			}
			if op.Summary == "" || len(op.Tags) == 0 {
				t.Errorf("%s %s 缺少注解摘要或标签（注解未生效？summary=%q tags=%v）", method, path, op.Summary, op.Tags)
				continue
			}
			annotated++
		}
	}

	if annotated == 0 {
		t.Fatalf("OpenAPI 文档里没有任何带注解的操作:\n%s", doc)
	}
	if fallback != 1 {
		t.Errorf("框架静态挂载点数量 = %d, 期望 1（%s）", fallback, frameworkStaticPath)
	}
	t.Logf("OpenAPI 注解覆盖: %d 个业务操作全部带摘要与标签，%d 个框架挂载点由路由树兜底", annotated, fallback)
}

// doText 执行请求并读出响应体文本；读取量以 maxBodyBytes 封顶，响应体始终关闭。
// doText runs the request and reads its body as text, capped at maxBodyBytes, with the body always closed.
func doText(t *testing.T, client *http.Client, req *http.Request) (*http.Response, string) {
	t.Helper()

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", req.Method, req.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		t.Fatalf("读取 %s %s 响应失败: %v", req.Method, req.URL, err)
	}
	return resp, string(body)
}

// This file tests the service command: registration, help output, the action vocabulary, the flag
// and configuration precedence, the derivation of the target binary and the failure hints. The
// control point is injected in every test, so no test ever registers, starts or stops a real system
// service; the build point is injected as well, so no test compiles anything except the one case
// that runs the build package against a fake toolchain. | 本文件测试 service 命令：注册、帮助输出、
// 动作词表、标志与配置的优先级、目标程序的推导与失败提示。每个测试都注入控制点，因此任何测试都不会真实
// 注册、启动或停止系统服务；构建点同样被注入，因此除唯一一个用假工具链跑 build 包的用例外，没有任何测试
// 会执行真实编译。

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kservice "github.com/kardianos/service"
	"github.com/shibingli/yd/build"
	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/logx"
	"github.com/shibingli/yd/service"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// serviceProjectMeta is the .yd.json body the tests write: the project name the service name and
// the artifact name fall back to. | serviceProjectMeta 是测试写入的 .yd.json 内容：服务名与产物名回退所用的项目名。
const serviceProjectMeta = `{"name":"shop-admin"}`

// serviceProject is the project name of serviceProjectMeta. | serviceProject 是 serviceProjectMeta 的项目名。
const serviceProject = "shop-admin"

// serviceCall records the control point invocation and answers it with a canned result. | serviceCall
// 记录控制点的调用并以预置结果应答。
type serviceCall struct {
	// seen is the option set the command handed over. | seen 是命令交出的选项。
	seen service.Options
	// err is the canned failure, nil for success. | err 是预置失败，nil 表示成功。
	err error
	// result is the canned result. | result 是预置结果。
	result string
	// action is the action the command requested. | action 是命令请求的动作。
	action service.Action
	// calls counts the invocations. | calls 记录调用次数。
	calls int
}

// useServiceControl injects a recording control point that never touches a real service. | useServiceControl
// 注入记录型控制点，绝不触碰真实服务。
func useServiceControl(t *testing.T) *serviceCall {
	t.Helper()
	rec := &serviceCall{result: "installed | 已安装: shop-admin (linux-systemd)"}
	old := serviceControlFunc
	serviceControlFunc = func(o service.Options, a service.Action) (string, error) {
		rec.calls++
		rec.seen = o
		rec.action = a
		return rec.result, rec.err
	}
	t.Cleanup(func() { serviceControlFunc = old })
	return rec
}

// useServiceBuild injects a fake compilation that records its options and answers with the
// conventional artifact path. | useServiceBuild 注入假编译：记录选项并以约定的产物路径应答。
func useServiceBuild(t *testing.T, artifact string) *build.Options {
	t.Helper()
	seen := &build.Options{}
	old := serviceBuildFunc
	serviceBuildFunc = func(_ context.Context, o build.Options, _ *logx.Logger) (string, error) {
		*seen = o
		return artifact, nil
	}
	t.Cleanup(func() { serviceBuildFunc = old })
	return seen
}

// newServiceProject builds an Env whose working directory carries the project metadata, so the
// service name and the artifact name have something deterministic to fall back to. | newServiceProject
// 构造一个工作目录带项目元信息的 Env，使服务名与产物名有确定的回退值。
func newServiceProject(t *testing.T) (*testEnv, string) {
	t.Helper()
	dir := t.TempDir()
	te := newTestEnv(t, nil, "", false)
	te.env.WorkDir = dir
	writeServiceFile(t, te.env.FS, filepath.Join(dir, ".yd.json"), serviceProjectMeta)
	return te, dir
}

// writeServiceFile writes one file into the in-memory file system of a test. | writeServiceFile 往测试的内存文件系统写入一个文件。
func writeServiceFile(t *testing.T, fs afero.Fs, path, body string) {
	t.Helper()
	if err := afero.WriteFile(fs, path, []byte(body), 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// newServiceConfigEnv builds an Env whose configuration file carries a service section. | newServiceConfigEnv
// 构造一个配置文件带 service 段的 Env。
func newServiceConfigEnv(t *testing.T, yaml string) (*testEnv, string) {
	t.Helper()
	te, dir := newServiceProject(t)
	writeServiceFile(t, te.env.FS, filepath.Join(dir, "config.yaml"), yaml)
	return te, dir
}

// runServiceCommand executes one `yd service ...` line. | runServiceCommand 执行一条 `yd service ...` 命令行。
func runServiceCommand(t *testing.T, te *testEnv, args ...string) error {
	t.Helper()
	return Execute(context.Background(), append([]string{serviceCommandName}, args...), te.env)
}

// ---------------------------------------------------------------- Tests | 测试

// TestServiceCommandRegistered service 只注册一次、标志齐全且用法行固定。Register 对重名会 panic。
// TestServiceCommandRegistered requires the service command to stay registered exactly once with every flag: Register panics on duplicates.
func TestServiceCommandRegistered(t *testing.T) {
	count := 0
	for _, c := range Commands() {
		if c.Name != serviceCommandName {
			continue
		}
		count++
		if c.Run == nil || c.Flags == nil {
			t.Error("service 命令缺少 Run 或 Flags")
		}
		if c.Usage != serviceUsageLine {
			t.Errorf("Usage = %q，期望 %q", c.Usage, serviceUsageLine)
		}
	}
	if count != 1 {
		t.Fatalf("service 命令注册了 %d 次，期望 1 次", count)
	}

	fs := pflag.NewFlagSet(serviceCommandName, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	registerServiceFlags(fs)
	for _, name := range []string{
		flagServiceName, flagServiceDisplayName, flagServiceDescription, flagServiceBinary,
		flagServiceDir, flagServiceUser, flagServiceArgs,
	} {
		if fs.Lookup(name) == nil {
			t.Errorf("service 命令缺少标志 --%s", name)
		}
	}
}

// TestServiceCommandHelp service 命令的 --help 列出用法行与全部标志。
// TestServiceCommandHelp requires the usage line and every flag to appear in the help output.
func TestServiceCommandHelp(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	if err := runServiceCommand(t, te, "--help"); err != nil {
		t.Fatalf("service --help 不应返回错误: %v", err)
	}
	out := te.out.String()
	for _, want := range []string{"yd service", "install", "uninstall", "status", "--name", "--display-name", "--description", "--binary", "--dir", "--user", "--args"} {
		if !strings.Contains(out, want) {
			t.Errorf("service 帮助缺少 %q：\n%s", want, out)
		}
	}
}

// TestServiceActionMapsToTheServiceCall 每个动作词被映射到同一名字的 service.Action，且只调用一次控制点。
// TestServiceActionMapsToTheServiceCall maps every action word onto the equally named service.Action with exactly one control call.
func TestServiceActionMapsToTheServiceCall(t *testing.T) {
	cases := []struct {
		word   string
		action service.Action
	}{
		{"install", service.ActionInstall},
		{"uninstall", service.ActionUninstall},
		{"start", service.ActionStart},
		{"stop", service.ActionStop},
		{"restart", service.ActionRestart},
		{"status", service.ActionStatus},
		{"INSTALL", service.ActionInstall},
	}

	for _, tc := range cases {
		te, dir := newServiceProject(t)
		rec := useServiceControl(t)
		// Only an install registers the path, so the artifact only serves that action. | 只有安装会登记程序路径，这里给出产物只为让安装动作有东西可登记。
		writeServiceFile(t, te.env.FS, filepath.Join(dir, "bin", serviceProject), "artifact")

		if err := runServiceCommand(t, te, tc.word); err != nil {
			t.Fatalf("%s: 期望成功, 实际 %v", tc.word, err)
		}
		if rec.calls != 1 {
			t.Fatalf("%s: 控制点调用次数 = %d，期望 1", tc.word, rec.calls)
		}
		if rec.action != tc.action {
			t.Errorf("%s: 动作 = %q，期望 %q", tc.word, rec.action, tc.action)
		}
		out := te.out.String()
		for _, want := range []string{"service | 服务: " + serviceProject, "action | 动作: " + string(tc.action), "result | 结果: " + rec.result} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: 报告缺少 %q：\n%s", tc.word, want, out)
			}
		}
	}
}

// TestServiceInstallReportsInstalledArtifact expects the registered executable to be reported. | TestServiceInstallReportsInstalledArtifact 安装时报告登记的程序路径。
func TestServiceInstallReportsInstalledArtifact(t *testing.T) {
	te, dir := newServiceProject(t)
	useServiceControl(t)
	artifact := filepath.Join(dir, "bin", serviceProject)
	writeServiceFile(t, te.env.FS, artifact, "artifact")

	if err := runServiceCommand(t, te, "install"); err != nil {
		t.Fatalf("install 失败: %v", err)
	}
	if out := te.out.String(); !strings.Contains(out, "binary | 程序: "+artifact) {
		t.Errorf("安装报告缺少程序路径 %q：\n%s", artifact, out)
	}
}

// TestServiceUnknownActionErrors 未知动作返回 service.ErrUnknownAction、不调用控制点并打印用法。
// TestServiceUnknownActionErrors requires an unknown action to fail with service.ErrUnknownAction, print the usage and skip the control point.
func TestServiceUnknownActionErrors(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)

	err := runServiceCommand(t, te, "reload")
	if !errors.Is(err, service.ErrUnknownAction) {
		t.Fatalf("期望 service.ErrUnknownAction，实际 %v", err)
	}
	if rec.calls != 0 {
		t.Errorf("未知动作不应调用控制点，实际调用 %d 次", rec.calls)
	}
	if got := te.errs.String(); !strings.Contains(got, "unknown action") || !strings.Contains(got, serviceUsageLine) {
		t.Errorf("未知动作的提示不完整：\n%s", got)
	}
}

// TestServiceRunActionExplainsTheBinary run 动作被拒绝，并说明它属于应用二进制。
// TestServiceRunActionExplainsTheBinary rejects the run action and explains that the application binary owns it.
func TestServiceRunActionExplainsTheBinary(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)

	if err := runServiceCommand(t, te, "run"); !errors.Is(err, service.ErrUnknownAction) {
		t.Fatalf("run 动作应返回 service.ErrUnknownAction，实际 %v", err)
	}
	if rec.calls != 0 {
		t.Errorf("run 动作不应调用控制点，实际调用 %d 次", rec.calls)
	}
	if got := te.errs.String(); !strings.Contains(got, "-service run") {
		t.Errorf("run 动作缺少“属于应用二进制”的说明：\n%s", got)
	}
}

// TestServiceMissingActionErrors 缺少动作词时返回 ErrNoCommand 并打印用法。
// TestServiceMissingActionErrors requires a missing action word to fail with ErrNoCommand and print the usage.
func TestServiceMissingActionErrors(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)

	if err := runServiceCommand(t, te); !errors.Is(err, ErrNoCommand) {
		t.Fatalf("期望 ErrNoCommand，实际 %v", err)
	}
	if rec.calls != 0 {
		t.Errorf("缺少动作词时不应调用控制点，实际调用 %d 次", rec.calls)
	}
	if out := te.out.String(); !strings.Contains(out, serviceUsageLine) {
		t.Errorf("缺少动作词时应打印用法行：\n%s", out)
	}
}

// TestServiceOptionsComeFromConfig 服务名、显示名、描述、账号、参数与重启策略来自配置。
// TestServiceOptionsComeFromConfig requires name, display name, description, account, arguments and the restart policy to come from the configuration.
func TestServiceOptionsComeFromConfig(t *testing.T) {
	te, _ := newServiceConfigEnv(t, `
app:
  name: web
service:
  name: shop-service
  display_name: Shop Admin Service
  description: 商城后台常驻服务 | shop admin daemon
  user_name: shop
  arguments:
    - -service
    - run
  restart_on_exit: true
`)
	rec := useServiceControl(t)

	if err := runServiceCommand(t, te, "start"); err != nil {
		t.Fatalf("start 失败: %v", err)
	}
	if rec.seen.Name != "shop-service" {
		t.Errorf("服务名 = %q，期望 shop-service（配置优先于 .yd.json 与目录名）", rec.seen.Name)
	}
	if rec.seen.DisplayName != "Shop Admin Service" {
		t.Errorf("显示名 = %q，期望 Shop Admin Service", rec.seen.DisplayName)
	}
	if rec.seen.Description != "商城后台常驻服务 | shop admin daemon" {
		t.Errorf("描述 = %q，期望配置中的取值", rec.seen.Description)
	}
	if rec.seen.UserName != "shop" {
		t.Errorf("账号 = %q，期望 shop", rec.seen.UserName)
	}
	if len(rec.seen.Arguments) != 2 || rec.seen.Arguments[0] != "-service" || rec.seen.Arguments[1] != "run" {
		t.Errorf("参数 = %v，期望 [-service run]", rec.seen.Arguments)
	}
	if !rec.seen.RestartOnExit {
		t.Error("restart_on_exit 未映射到 Options.RestartOnExit")
	}
}

// TestServiceNameFallsBackToProjectMetadata 未配置 service.name 时服务名取 .yd.json 记录的项目名，其次目录名。
// TestServiceNameFallsBackToProjectMetadata falls back to the project name recorded in .yd.json and then to the directory name.
func TestServiceNameFallsBackToProjectMetadata(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)
	if err := runServiceCommand(t, te, "status"); err != nil {
		t.Fatalf("status 失败: %v", err)
	}
	if rec.seen.Name != serviceProject {
		t.Errorf("服务名 = %q，期望 .yd.json 记录的 %q（框架默认的 app.name 不得参与）", rec.seen.Name, serviceProject)
	}
	if rec.seen.DisplayName != serviceProject || rec.seen.Description != serviceProject {
		t.Errorf("显示名与描述应回退到服务名, 实际 %q / %q", rec.seen.DisplayName, rec.seen.Description)
	}

	// Without .yd.json in the working directory the directory name is the fallback. | 工作目录没有 .yd.json 时回退为目录名。
	bare := newTestEnv(t, nil, "", false)
	bare.env.WorkDir = te.env.WorkDir
	if err := runServiceCommand(t, bare, "status"); err != nil {
		t.Fatalf("status 失败: %v", err)
	}
	if want := filepath.Base(te.env.WorkDir); rec.seen.Name != want {
		t.Errorf("服务名 = %q，期望目录名 %q", rec.seen.Name, want)
	}
}

// TestServiceFlagsOverrideConfig 命令行标志压过配置与项目元信息。
// TestServiceFlagsOverrideConfig requires the command line flags to win over the configuration and the project metadata.
func TestServiceFlagsOverrideConfig(t *testing.T) {
	te, _ := newServiceConfigEnv(t, "service:\n  name: shop-service\n  display_name: From Config\n  description: from config\n  user_name: shop\n  arguments:\n    - -service\n    - run\n")
	rec := useServiceControl(t)

	err := runServiceCommand(t, te, "status",
		"--name", "other-service",
		"--display-name", "Other Service",
		"--description", "other description",
		"--user", "www",
		"--args", `-service run --config "/opt/app/config.yaml"`,
	)
	if err != nil {
		t.Fatalf("status 失败: %v", err)
	}
	if rec.seen.Name != "other-service" || rec.seen.DisplayName != "Other Service" || rec.seen.Description != "other description" {
		t.Errorf("标志未压过配置: %+v", rec.seen)
	}
	if rec.seen.UserName != "www" {
		t.Errorf("账号 = %q，期望 www", rec.seen.UserName)
	}
	want := []string{"-service", "run", "--config", "/opt/app/config.yaml"}
	if len(rec.seen.Arguments) != len(want) {
		t.Fatalf("参数 = %v，期望 %v", rec.seen.Arguments, want)
	}
	for i, arg := range want {
		if rec.seen.Arguments[i] != arg {
			t.Errorf("参数[%d] = %q，期望 %q", i, rec.seen.Arguments[i], arg)
		}
	}
}

// TestServiceBinaryDefaultsToBuildArtifact --binary 省略时取 bin/<项目名>，其次 bin/<项目名>.exe。
// TestServiceBinaryDefaultsToBuildArtifact derives bin/<project> and then bin/<project>.exe when --binary is omitted.
func TestServiceBinaryDefaultsToBuildArtifact(t *testing.T) {
	cases := []struct {
		name     string
		artifact string
		want     string
	}{
		{"无后缀 | no suffix", serviceProject, serviceProject},
		{"可执行后缀 | executable suffix", serviceProject + serviceExeSuffix, serviceProject + serviceExeSuffix},
	}

	for _, tc := range cases {
		te, dir := newServiceProject(t)
		rec := useServiceControl(t)
		writeServiceFile(t, te.env.FS, filepath.Join(dir, "bin", tc.artifact), "artifact")

		if err := runServiceCommand(t, te, "install"); err != nil {
			t.Fatalf("%s: install 失败: %v", tc.name, err)
		}
		want := filepath.Join(dir, "bin", tc.want)
		if rec.seen.Executable != want {
			t.Errorf("%s: 程序 = %q，期望 %q", tc.name, rec.seen.Executable, want)
		}
	}
}

// TestServiceBinaryDirFlagMovesTheSearch --dir 指定项目目录后产物在该目录下解析。
// TestServiceBinaryDirFlagMovesTheSearch resolves the artifact below the directory given by --dir.
func TestServiceBinaryDirFlagMovesTheSearch(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)
	other := filepath.Join(te.env.WorkDir, "checkout")
	writeServiceFile(t, te.env.FS, filepath.Join(other, ".yd.json"), serviceProjectMeta)
	artifact := filepath.Join(other, "bin", serviceProject)
	writeServiceFile(t, te.env.FS, artifact, "artifact")

	if err := runServiceCommand(t, te, "install", "--dir", "checkout"); err != nil {
		t.Fatalf("install --dir 失败: %v", err)
	}
	if rec.seen.Executable != artifact {
		t.Errorf("程序 = %q，期望 %q", rec.seen.Executable, artifact)
	}
}

// TestServiceBinaryBuildsMissingArtifact 产物缺失时用 build 包编一次，并编译约定入口 cmd/app。
// TestServiceBinaryBuildsMissingArtifact compiles once through the build package and targets the conventional cmd/app entry.
func TestServiceBinaryBuildsMissingArtifact(t *testing.T) {
	te, dir := newServiceProject(t)
	rec := useServiceControl(t)
	writeServiceFile(t, te.env.FS, filepath.Join(dir, "cmd", "app", "main.go"), "package main\n")
	artifact := filepath.Join(dir, "bin", serviceProject)
	seen := useServiceBuild(t, artifact)

	if err := runServiceCommand(t, te, "install"); err != nil {
		t.Fatalf("install 失败: %v", err)
	}
	if seen.Dir != dir {
		t.Errorf("构建目录 = %q，期望 %q", seen.Dir, dir)
	}
	if seen.Name != serviceProject {
		t.Errorf("产物基础名 = %q，期望 %q", seen.Name, serviceProject)
	}
	if seen.Package != "./"+serviceEntryDir {
		t.Errorf("构建入口 = %q，期望 ./%s", seen.Package, serviceEntryDir)
	}
	if rec.seen.Executable != artifact {
		t.Errorf("程序 = %q，期望构建产物 %q", rec.seen.Executable, artifact)
	}
}

// TestServiceBuildFailureCarriesAHint 编译失败时返回 ErrServiceBinary，并提示先构建或传入 --binary。
// TestServiceBuildFailureCarriesAHint reports ErrServiceBinary with a hint to build first or pass --binary.
func TestServiceBuildFailureCarriesAHint(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)
	failure := errors.New("go build: exit status 1")
	old := serviceBuildFunc
	serviceBuildFunc = func(context.Context, build.Options, *logx.Logger) (string, error) { return "", failure }
	t.Cleanup(func() { serviceBuildFunc = old })

	err := runServiceCommand(t, te, "install")
	if !errors.Is(err, ErrServiceBinary) {
		t.Fatalf("期望 ErrServiceBinary，实际 %v", err)
	}
	if !errors.Is(err, failure) {
		t.Errorf("原始错误应保留在错误链上: %v", err)
	}
	if rec.calls != 0 {
		t.Errorf("编译失败时不应调用控制点，实际调用 %d 次", rec.calls)
	}
	if msg := err.Error(); !strings.Contains(msg, "yd build") || !strings.Contains(msg, "--binary") {
		t.Errorf("失败详情缺少可操作提示: %v", msg)
	}
}

// TestServiceBinaryFlagMustExist --binary 指向不存在的文件时立即失败，不触碰控制点。
// TestServiceBinaryFlagMustExist fails immediately when --binary points at a missing file and never touches the control point.
func TestServiceBinaryFlagMustExist(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)

	err := runServiceCommand(t, te, "install", "--binary", "dist/shop-admin")
	if !errors.Is(err, ErrServiceBinary) {
		t.Fatalf("期望 ErrServiceBinary，实际 %v", err)
	}
	if rec.calls != 0 {
		t.Errorf("程序缺失时不应调用控制点，实际调用 %d 次", rec.calls)
	}

	writeServiceFile(t, te.env.FS, filepath.Join(te.env.WorkDir, "dist", "shop-admin"), "artifact")
	if err := runServiceCommand(t, te, "install", "--binary", "dist/shop-admin"); err != nil {
		t.Fatalf("显式 --binary 应被接受: %v", err)
	}
	if want := filepath.Join(te.env.WorkDir, "dist", "shop-admin"); rec.seen.Executable != want {
		t.Errorf("程序 = %q，期望 %q", rec.seen.Executable, want)
	}
}

// TestServiceOnlyInstallResolvesTheBinary 非安装动作不推导程序路径，也不触发编译。
// TestServiceOnlyInstallResolvesTheBinary skips the binary derivation and the compilation for the other actions.
func TestServiceOnlyInstallResolvesTheBinary(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)
	built := 0
	old := serviceBuildFunc
	serviceBuildFunc = func(context.Context, build.Options, *logx.Logger) (string, error) {
		built++
		return "artifact", nil
	}
	t.Cleanup(func() { serviceBuildFunc = old })

	for _, action := range []string{"uninstall", "start", "stop", "restart", "status"} {
		if err := runServiceCommand(t, te, action); err != nil {
			t.Fatalf("%s 失败: %v", action, err)
		}
		if rec.seen.Executable != "" {
			t.Errorf("%s: 程序 = %q，期望空（只有安装登记程序路径）", action, rec.seen.Executable)
		}
	}
	if built != 0 {
		t.Errorf("非安装动作不应触发编译，实际 %d 次", built)
	}
}

// TestServiceUnsupportedPlatformHint 平台不受支持时错误可判定，并提示缺少服务管理器。
// TestServiceUnsupportedPlatformHint keeps the unsupported-platform error matchable and hints at the missing service manager.
func TestServiceUnsupportedPlatformHint(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)
	rec.err = errx.Wrap(kservice.ErrNoServiceSystemDetected, service.ErrUnsupportedPlatform)

	err := runServiceCommand(t, te, "status")
	if !errors.Is(err, service.ErrUnsupportedPlatform) {
		t.Fatalf("期望 service.ErrUnsupportedPlatform，实际 %v", err)
	}
	if !errors.Is(err, kservice.ErrNoServiceSystemDetected) {
		t.Errorf("原始错误应保留在错误链上: %v", err)
	}
	if got := te.errs.String(); !strings.Contains(got, "no supported service manager") || !strings.Contains(got, "未检测到受支持的服务管理器") {
		t.Errorf("缺少平台不支持的提示：\n%s", got)
	}
	if out := te.out.String(); strings.Contains(out, "result | 结果") {
		t.Errorf("失败时不应打印结果行：\n%s", out)
	}
}

// TestServicePermissionHint 权限被拒时提示管理员或 sudo。
// TestServicePermissionHint hints at Administrator or sudo when the action is denied.
func TestServicePermissionHint(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)
	rec.err = service.ErrPermissionDenied

	if err := runServiceCommand(t, te, "start"); !errors.Is(err, service.ErrPermissionDenied) {
		t.Fatalf("期望 service.ErrPermissionDenied，实际 %v", err)
	}
	if got := te.errs.String(); !strings.Contains(got, "Administrator") || !strings.Contains(got, "sudo") {
		t.Errorf("缺少提权提示：\n%s", got)
	}
}

// TestServiceNotInstalledHint 服务未安装时提示先执行 yd service install。
// TestServiceNotInstalledHint hints at `yd service install` when the service is not installed.
func TestServiceNotInstalledHint(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)
	rec.err = service.ErrNotInstalled

	if err := runServiceCommand(t, te, "stop"); !errors.Is(err, service.ErrNotInstalled) {
		t.Fatalf("期望 service.ErrNotInstalled，实际 %v", err)
	}
	if got := te.errs.String(); !strings.Contains(got, "yd service install") {
		t.Errorf("缺少安装提示：\n%s", got)
	}
}

// TestServiceStatusPrintsTheStatusText status 动作把状态文本作为结果打印。
// TestServiceStatusPrintsTheStatusText prints the status text as the result of the status action.
func TestServiceStatusPrintsTheStatusText(t *testing.T) {
	te, _ := newServiceProject(t)
	rec := useServiceControl(t)
	rec.result = "running | 运行中"

	if err := runServiceCommand(t, te, "status"); err != nil {
		t.Fatalf("status 失败: %v", err)
	}
	if out := te.out.String(); !strings.Contains(out, "result | 结果: running | 运行中") {
		t.Errorf("状态文本未作为结果打印：\n%s", out)
	}
}

// TestSplitServiceArgs 参数串按未加引号的空白切分，引号成组且不解释转义。
// TestSplitServiceArgs splits on unquoted whitespace, groups quoted values and interprets no escape sequence.
func TestSplitServiceArgs(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  []string
	}{
		{"空串 | empty", "", nil},
		{"仅空白 | blanks only", "   \t", nil},
		{"简单切分 | plain split", "-service run", []string{"-service", "run"}},
		{"多余空白 | extra blanks", "  -service   run  ", []string{"-service", "run"}},
		{"双引号成组 | double quotes", `--config "/opt/app/my config.yaml"`, []string{"--config", "/opt/app/my config.yaml"}},
		{"单引号成组 | single quotes", `--name 'my service'`, []string{"--name", "my service"}},
		{"空参数 | empty argument", `--tag ""`, []string{"--tag", ""}},
		{"不解释转义 | no escape", `--pattern a\ b`, []string{"--pattern", `a\`, "b"}},
	}

	for _, tc := range cases {
		got := splitServiceArgs(tc.value)
		if len(got) != len(tc.want) {
			t.Errorf("%s: splitServiceArgs(%q) = %v，期望 %v", tc.name, tc.value, got, tc.want)
			continue
		}
		for i, arg := range tc.want {
			if got[i] != arg {
				t.Errorf("%s: 参数[%d] = %q，期望 %q", tc.name, i, got[i], arg)
			}
		}
	}
}

// TestServiceBuildsTheArtifactThroughTheBuildPackage 产物缺失时真的走一遍 build 包（工具链为假）。
// TestServiceBuildsTheArtifactThroughTheBuildPackage runs the real build package with a fake toolchain when the artifact is missing.
func TestServiceBuildsTheArtifactThroughTheBuildPackage(t *testing.T) {
	dir := newBuildDir(t)
	project := filepath.Base(dir)
	if err := os.MkdirAll(filepath.Join(dir, serviceEntryDir), 0o755); err != nil {
		t.Fatalf("创建入口目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, serviceEntryDir, serviceEntryFile), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("写入入口失败: %v", err)
	}
	fake := cliFakeGoBuild()
	useBuildExec(t, fake)

	te := newTestEnv(t, afero.NewOsFs(), "", false)
	te.env.WorkDir = dir
	rec := useServiceControl(t)

	if err := runServiceCommand(t, te, "install"); err != nil {
		t.Fatalf("install 失败: %v", err)
	}

	builds := fake.invocations("build")
	if len(builds) != 1 {
		t.Fatalf("go build 调用次数 = %d，期望 1", len(builds))
	}
	if got := builds[0][len(builds[0])-1]; got != "./"+serviceEntryDir {
		t.Errorf("构建入口 = %q，期望 ./%s", got, serviceEntryDir)
	}

	want := filepath.Join(dir, "bin", project+cliExeSuffix())
	if rec.seen.Executable != want {
		t.Errorf("程序 = %q，期望 %q", rec.seen.Executable, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("构建产物未落盘: %v", err)
	}
}

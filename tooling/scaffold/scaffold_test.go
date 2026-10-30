package scaffold_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	frameworkdocs "github.com/shibingli/yd/docs"
	"github.com/shibingli/yd/security/authz"
	"github.com/shibingli/yd/tooling/assets"
	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/shibingli/yd/web"
	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/base64"
	"github.com/shibingli/ydx/guomi"
	"github.com/shibingli/ydx/json"
	"github.com/shibingli/ydx/nodeid"
	"github.com/shibingli/ydx/protocol"
	"github.com/shibingli/ydx/regexpx"
	"github.com/shibingli/ydx/websocket"
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
	"data/.gitkeep",
	"docs/README.md",
	"docs/attachments.md",
	"docs/cache.md",
	"docs/captcha.md",
	"docs/cron.md",
	"docs/developer_guidelines.md",
	"docs/dictionary.md",
	"docs/frontend-components.md",
	"docs/frontend-ui.md",
	"docs/gorm-dialects.md",
	"docs/icon-usage.md",
	"docs/message-center.md",
	"docs/middleware-metrics.md",
	"docs/node-identity.md",
	"docs/realtime-transports.md",
	"docs/redis-asynq.md",
	"docs/render-performance.md",
	"docs/render-usage.md",
	"docs/runtime-gc.md",
	"docs/verification.md",
	"docs/waf.md",
	"docs/workflow.md",
	"docs/yd-usage.md",
	"go.mod",
	"internal/controller/attachment_controller.go",
	"internal/controller/auth_controller.go",
	"internal/controller/controller.go",
	"internal/controller/dictionary_api.go",
	"internal/controller/dictionary_api_types.go",
	"internal/controller/dictionary_controller.go",
	"internal/controller/dictionary_item_controller.go",
	"internal/controller/dictionary_system_api.go",
	"internal/controller/dictionary_system_controller.go",
	"internal/controller/dictionary_view.go",
	"internal/controller/friend_controller.go",
	"internal/controller/home.go",
	"internal/controller/message_admin.go",
	"internal/controller/message_api.go",
	"internal/controller/message_controller.go",
	"internal/controller/message_stream.go",
	"internal/controller/message_system_controller.go",
	"internal/controller/message_view.go",
	"internal/controller/metrics_stream.go",
	"internal/controller/middleware_metrics.go",
	"internal/controller/portal_root.go",
	"internal/controller/realtime.go",
	"internal/controller/role_controller.go",
	"internal/controller/tenant_controller.go",
	"internal/controller/ui_kit.go",
	"internal/controller/user_controller.go",
	"internal/controller/workflow_api.go",
	"internal/controller/workflow_api_types.go",
	"internal/controller/workflow_controller.go",
	"internal/controller/workflow_group_controller.go",
	"internal/controller/workflow_instance_controller.go",
	"internal/controller/workflow_runtime_api.go",
	"internal/controller/workflow_task_controller.go",
	"internal/meta/meta.go",
	"internal/middleware/cache.go",
	"internal/middleware/observability.go",
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
	"internal/service/attachment.go",
	"internal/service/auth.go",
	"internal/service/cache.go",
	"internal/service/cron.go",
	"internal/service/dictionary.go",
	"internal/service/message.go",
	"internal/service/message_bus.go",
	"internal/service/metrics.go",
	"internal/service/realtime.go",
	"internal/service/redis.go",
	"internal/service/role.go",
	"internal/service/tenant.go",
	"internal/service/user.go",
	"internal/service/workflow.go",
	"internal/waf/rules.go",
	"internal/waf/rules_test.go",
	"internal/waf/settings.go",
	"internal/waf/settings_test.go",
	"internal/waf/waf.go",
	"internal/waf/waf_test.go",
	"web/assets.lock.json",
	"web/bundle_generated.go",
	"web/embed.go",
	"web/i18n/en.yaml",
	"web/i18n/zh-CN.yaml",
	"web/static/css/app.css",
	"web/static/css/components.css",
	"web/static/css/ui.css",
	"web/static/img/icons.svg",
	"web/static/js/yd-app.js",
	"web/templates/components/chart.html",
	"web/templates/components/cn.html",
	"web/templates/components/data.html",
	"web/templates/components/display.html",
	"web/templates/components/display_advanced.html",
	"web/templates/components/display_business.html",
	"web/templates/components/display_interactive.html",
	"web/templates/components/feedback.html",
	"web/templates/components/feedback_advanced.html",
	"web/templates/components/form.html",
	"web/templates/components/form_advanced.html",
	"web/templates/components/layout.html",
	"web/templates/components/navigation.html",
	"web/templates/components/navigation_advanced.html",
	"web/templates/components/overlay.html",
	"web/templates/mgr/layouts/base.html",
	"web/templates/mgr/layouts/shell.html",
	"web/templates/mgr/pages/attachments.html",
	"web/templates/mgr/pages/dictionaries.html",
	"web/templates/mgr/pages/home.html",
	"web/templates/mgr/pages/message_admin.html",
	"web/templates/mgr/pages/message_settings.html",
	"web/templates/mgr/pages/permissions.html",
	"web/templates/mgr/pages/roles.html",
	"web/templates/mgr/pages/tenants.html",
	"web/templates/mgr/pages/users.html",
	"web/templates/mgr/pages/workflow_designer.html",
	"web/templates/mgr/pages/workflow_groups.html",
	"web/templates/mgr/pages/workflow_instances.html",
	"web/templates/mgr/pages/workflows.html",
	"web/templates/mgr/partials/attachments/cleanup.html",
	"web/templates/mgr/partials/attachments/delete.html",
	"web/templates/mgr/partials/dictionaries/item_form.html",
	"web/templates/mgr/partials/dictionaries/type_form.html",
	"web/templates/mgr/partials/home/live_metrics.html",
	"web/templates/mgr/partials/home/metrics.html",
	"web/templates/mgr/partials/home/metrics_fragment.html",
	"web/templates/mgr/partials/home/middleware.html",
	"web/templates/mgr/partials/home/middleware_fragment.html",
	"web/templates/mgr/partials/message_settings/body.html",
	"web/templates/mgr/partials/message_settings/tag_form.html",
	"web/templates/mgr/partials/message_settings/type_form.html",
	"web/templates/mgr/partials/messages/audit.html",
	"web/templates/mgr/partials/messages/pin.html",
	"web/templates/mgr/partials/roles/drawer.html",
	"web/templates/mgr/partials/roles/edit_drawer.html",
	"web/templates/mgr/partials/roles/form.html",
	"web/templates/mgr/partials/roles/list.html",
	"web/templates/mgr/partials/sidebar.html",
	"web/templates/mgr/partials/tenants/drawer.html",
	"web/templates/mgr/partials/tenants/form.html",
	"web/templates/mgr/partials/tenants/list.html",
	"web/templates/mgr/partials/users/create_drawer.html",
	"web/templates/mgr/partials/users/edit_drawer.html",
	"web/templates/mgr/partials/users/form.html",
	"web/templates/mgr/partials/users/list.html",
	"web/templates/mgr/partials/users/row.html",
	"web/templates/mgr/partials/workflows/definition_form.html",
	"web/templates/mgr/partials/workflows/definition_list.html",
	"web/templates/mgr/partials/workflows/group_form.html",
	"web/templates/mgr/partials/workflows/group_list.html",
	"web/templates/mgr/partials/workflows/group_members.html",
	"web/templates/mgr/partials/workflows/import.html",
	"web/templates/mgr/partials/workflows/instance_list.html",
	"web/templates/mgr/partials/workflows/versions.html",
	"web/templates/partials/app_shell.html",
	"web/templates/partials/base.html",
	"web/templates/partials/captcha.html",
	"web/templates/partials/editor-icons.html",
	"web/templates/partials/flash.html",
	"web/templates/partials/icon.html",
	"web/templates/partials/messages/message.html",
	"web/templates/partials/messages/sidebar_badges.html",
	"web/templates/partials/messages/unread_badge.html",
	"web/templates/partials/messages/unread_frames.html",
	"web/templates/partials/messages/unread_menu_badge.html",
	"web/templates/partials/pages/error.html",
	"web/templates/partials/shell-switches.html",
	"web/templates/partials/sidebar_brand.html",
	"web/templates/partials/sidebar_footer.html",
	"web/templates/partials/topbar.html",
	"web/templates/partials/workflows/instance_detail.html",
	"web/templates/partials/workflows/labels.html",
	"web/templates/partials/workflows/task.html",
	"web/templates/portal/layouts/base.html",
	"web/templates/portal/layouts/shell.html",
	"web/templates/portal/pages/account.html",
	"web/templates/portal/pages/attachments.html",
	"web/templates/portal/pages/friends.html",
	"web/templates/portal/pages/login.html",
	"web/templates/portal/pages/messages.html",
	"web/templates/portal/pages/realtime.html",
	"web/templates/portal/pages/ui_kit.html",
	"web/templates/portal/pages/workflow_tasks.html",
	"web/templates/portal/partials/friends/remark.html",
	"web/templates/portal/partials/friends/request.html",
	"web/templates/portal/partials/friends/search.html",
	"web/templates/portal/partials/friends/summary.html",
	"web/templates/portal/partials/login.html",
	"web/templates/portal/partials/messages/attachment_chip.html",
	"web/templates/portal/partials/messages/compose.html",
	"web/templates/portal/partials/messages/composer.html",
	"web/templates/portal/partials/messages/contacts.html",
	"web/templates/portal/partials/messages/conversations.html",
	"web/templates/portal/partials/messages/group_settings.html",
	"web/templates/portal/partials/messages/history.html",
	"web/templates/portal/partials/messages/i18n_keys.html",
	"web/templates/portal/partials/messages/notice.html",
	"web/templates/portal/partials/messages/search.html",
	"web/templates/portal/partials/messages/thread.html",
	"web/templates/portal/partials/sidebar.html",
	"web/templates/portal/partials/workflows/start.html",
	"web/templates/portal/partials/workflows/task_list.html",
}

// specGitignore 是需求基线 §二.3 的 .gitignore 模板逐字内容（已去掉提示行），
// 外加四处**有意偏离**：
//   - 基线未忽略 `.yd/`，而 `yd tools install` 会把 betteralign、ko 等外部工具二进制装进
//     项目内的 `.yd/tools/`，不忽略就会出现在 `git status` 里，甚至被误提交；
//     该偏离已记入 docs/superpowers/plans/2026-10-17-yd-tool-installation.md 的差异说明。
//   - 基线未忽略本地库文件，而 `yd new --db sqlite` 的示例 DSN 默认把库落在 `data/`，
//     WAL 模式还会生成 `-wal` / `-shm` 边车文件；它们属于本机运行数据，绝不入库。
//   - 基线未忽略运行期上传目录（消息附件），它们同样是本机运行数据。
//
// specGitignore is the verbatim .gitignore template of the baseline §二.3 (the advisory line has
// been dropped), plus four deliberate deviations:
//   - the baseline does not ignore `.yd/`, yet `yd tools install` puts betteralign, ko and friends
//     into the project-local `.yd/tools/`, where they would show up in `git status` and could be
//     committed by accident (recorded in the tool-installation plan's difference note);
//   - the baseline does not ignore local database files, yet the SQLite sample DSN of
//     `yd new --db sqlite` lands the database in `data/`, and WAL mode adds the -wal / -shm
//     sidecars; they are machine-local runtime data and never belong in version control.
//   - the baseline does not ignore the runtime upload directory (message attachments), which is
//     likewise machine-local runtime data;
//   - the baseline does not ignore the per-instance node identity file, which must never be
//     committed or shared between deployments.
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
	"node_modules/\n" +
	"# Project-local optional tool binaries installed by `yd tools install` | 由 `yd tools install` 安装到项目内的可选工具二进制\n" +
	".yd/\n" +
	"# Runtime uploads (message attachments) | 运行期上传（消息附件）\n" +
	"data/attachments/\n" +
	"# Node identity is per-instance runtime state; committing or sharing it duplicates the node | 节点身份是每实例运行期状态，入库或共享会造成节点重复\n" +
	"data/node-id\n" +
	"data/.node-id-*\n" +
	"# Local database files (the default landing spot of yd new --db sqlite, WAL sidecars included) | 本地数据库文件（yd new --db sqlite 的默认落点，含 WAL 边车文件）\n" +
	"data/*.db\n" +
	"data/*.db-wal\n" +
	"data/*.db-shm\n" +
	"\n" +
	"# `yd build` 生成的压缩资产 | minified assets produced by `yd build`\n" +
	"web/static/**/*.min.js\n" +
	"web/static/**/*.min.css\n"

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

// read reads a project file and fails the case on error. | read 读取项目文件，失败即终止用例。
func (p project) read(rel string) string {
	p.t.Helper()
	b, err := afero.ReadFile(p.fs, p.at(rel))
	if err != nil {
		p.t.Fatalf("读取 %s 失败: %v", p.at(rel), err)
	}
	return string(b)
}

// exists reports whether a project file exists. | exists 报告项目文件是否存在。
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

// repoRoot 返回本仓库根目录（tooling/scaffold 包向上两级），供 --local-replace 使用。
// repoRoot returns the repository root (two levels above the tooling/scaffold package), used by --local-replace.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file path | 无法定位测试文件路径")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
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
	if !strings.Contains(gomod, "go 1.27.3") {
		t.Errorf("go.mod 缺少 go 1.27.3:\n%s", gomod)
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
		t.Error("the .gitignore still carries the boilerplate hint line | .gitignore 仍含提示行「请谨慎使用此类代码。」")
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
	for _, want := range []string{"YD_SERVER_PORT", "YD_DB_DRIVER", "YD_LOG_LEVEL", "YD_SERVER_HTTP2_EXTENDED_CONNECT"} {
		if !strings.Contains(env, want) {
			t.Errorf(".env.template 缺少 %q", want)
		}
	}
	if strings.Contains(env, "YUNGO_") {
		t.Error("the .env.template still carries the retired YUNGO_ prefix | .env.template 仍含旧前缀 YUNGO_")
	}

	var meta struct {
		Name      string         `json:"name"`
		Module    string         `json:"module"`
		Features  map[string]any `json:"features"`
		AssetRoot string         `json:"assetRoot"`
	}
	if err := json.Unmarshal([]byte(pr.read(".yd.json")), &meta); err != nil {
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
	// Redis、Asynq 与 Cron 同样是内建能力：没有开关，生成项目一律登记为已接入。
	// Redis, Asynq and Cron are built in as well: there is no switch, and every generated project records them as wired in.
	for _, key := range []string{"redis", "asynq", "cron"} {
		if meta.Features[key] != true {
			t.Errorf(".yd.json features.%s = %v, 期望 true", key, meta.Features[key])
		}
	}
}

// TestGenerateTailwindAddsCssEntry 断言 Tailwind 开关决定 CSS 入口内容。
// TestGenerateTailwindAddsCssEntry asserts the Tailwind switch drives the CSS entry.
func TestGenerateTailwindAddsCssEntry(t *testing.T) {
	on := scaffold.DefaultOptions("css-on")
	on.Tailwind = true
	prOn, _ := generateMem(t, on)
	css := prOn.read("web/static/css/app.css")
	for _, want := range []string{`@import "tailwindcss" source(none)`, "@theme", "--color-brand-600"} {
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
		t.Error("app.css must not be empty while Tailwind is off (a minimal base stylesheet is required) | 关闭 Tailwind 时 app.css 不应为空（需提供最小基础样式）")
	}
}

// TestGenerateAlwaysIntegratesRedisAndAsynq 断言 Redis 与 Asynq 是内建能力：默认生成即带配置、接线与装配点，
// 不存在关闭它们的开关。
// TestGenerateAlwaysIntegratesRedisAndAsynq asserts Redis and Asynq are built in: the default generation carries their
// configuration, wiring and assembly points, and no switch can turn them off.
func TestGenerateAlwaysIntegratesRedisAndAsynq(t *testing.T) {
	o := scaffold.DefaultOptions("feat-app")
	o.DB = scaffold.DBPostgres
	o.GRPC, o.I18n = true, true
	pr, _ := generateMem(t, o)

	cfg := pr.read("configs/config.yaml")
	for _, want := range []string{
		"extended_connect: true",
		"enabled: false      # 开启 HTTP/3",
		"webtransport: false",
		"node:",
		"id_file: \"data/node-id\"",
		"require_persistent: false",
		"gc:",
		"strategy: balanced",
		"sensitive_tables:",
		"yd_auth_user_tenants",
		"sensitive_ttl: 5s",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.yaml 缺少 %q:\n%s", want, cfg)
		}
	}
	busSource := pr.read("internal/service/message_bus.go")
	for _, want := range []string{"nodeid.OptionsFromConfig", "nodeid.Resolve", "message.node_id"} {
		if !strings.Contains(busSource, want) {
			t.Errorf("message_bus.go is missing %q | message_bus.go 缺少 %q", want, want)
		}
	}
	routerSource := pr.read("internal/router/router.go")
	for _, want := range []string{"CacheBypass", "sensitivePath", "SetCacheInvalidator", "SetMessageCacheInvalidator"} {
		if !strings.Contains(routerSource, want) {
			t.Errorf("router.go is missing %q | router.go 缺少 %q", want, want)
		}
	}
	repositorySource := pr.read("internal/repository/store.go")
	for _, want := range []string{"SetCacheInvalidator", "SetMessageCacheInvalidator"} {
		if !strings.Contains(repositorySource, want) {
			t.Errorf("repository/store.go is missing %q | repository/store.go 缺少 %q", want, want)
		}
	}
	for _, want := range []string{"postgres", "redis:", "asynq:", "cron:", "grpc:", "i18n:", "default_lang: zh-CN"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config.yaml 缺少 %q:\n%s", want, cfg)
		}
	}

	router := pr.read("internal/router/router.go")
	for _, want := range []string{"redis", "asynq", "grpc", "i18n"} {
		if !strings.Contains(router, want) {
			t.Errorf("internal/router/router.go 缺少 %q 注册代码:\n%s", want, router)
		}
	}

	realtime := pr.read("internal/controller/realtime.go")
	if !strings.Contains(realtime, "必须阻塞到连接结束") {
		t.Errorf("realtime.go 缺少 HTTP/2/3 处理器生命周期说明:\n%s", realtime)
	}

	// Redis 与 Asynq 不受任何开关控制：关掉其余可选特性后，它们仍必须出现在配置、共享客户端与总线装配点里。
	// Redis and Asynq are not switchable: with every other optional feature off they must still appear in the
	// configuration, the shared client and the message-bus assembly points.
	plain := scaffold.DefaultOptions("plain-app")
	plain.DB = scaffold.DBNone
	plain.I18n = false
	prPlain, _ := generateMem(t, plain)
	cfgPlain := prPlain.read("configs/config.yaml")
	for _, want := range []string{"redis:", "asynq:"} {
		if !strings.Contains(cfgPlain, want) {
			t.Errorf("未开启其它特性时 config.yaml 仍应含 %q:\n%s", want, cfgPlain)
		}
	}
	for _, unwanted := range []string{"grpc:", "i18n:"} {
		if strings.Contains(cfgPlain, unwanted) {
			t.Errorf("未开启特性时 config.yaml 不应含 %q:\n%s", unwanted, cfgPlain)
		}
	}
	redisFile := prPlain.read("internal/service/redis.go")
	if !strings.Contains(redisFile, "redis.Open") {
		t.Errorf("进程级 Redis 客户端应始终在 internal/service/redis.go 装配:\n%s", redisFile)
	}
	bus := prPlain.read("internal/service/message_bus.go")
	for _, want := range []string{"asynq.NewClient", "asynq.NewServer"} {
		if !strings.Contains(bus, want) {
			t.Errorf("消息总线应始终装配 %q:\n%s", want, bus)
		}
	}
	if strings.Contains(bus, "redis.Open") {
		t.Errorf("消息总线必须复用共享 Redis 客户端，不得再次建连:\n%s", bus)
	}
	if routerPlain := prPlain.read("internal/router/router.go"); !strings.Contains(routerPlain, "asynq") {
		t.Error("router.go must always mention asynq | router.go 应始终包含 asynq 装配点")
	}
}

// TestGeneratedRealtimeWrapsWSSendControls pins the htmx 4 WebSocket contract of the generated demo. | TestGeneratedRealtimeWrapsWSSendControls
// 锁定生成项目里 htmx 4 WebSocket 的契约。
//
// The extension finds the connection with closest(), so the composer and the send button must be nested in the
// element carrying hx-ws:connect; otherwise a click reports "No WebSocket connection found" and sends nothing.
// Enter cannot use an hx-trigger filter because htmx compiles filters with new Function, which the CSP
// script-src 'self' blocks, so the app script clicks the same button instead. | 扩展用 closest() 查找连接，
// 因此输入区与发送按钮必须嵌在带 hx-ws:connect 的元素内部，否则点击只会报 "No WebSocket connection found"
// 而不会发出消息。回车不能用 hx-trigger 过滤器，因为 htmx 用 new Function 编译过滤器，会被 CSP
// script-src 'self' 拦截；改由应用脚本点击同一个按钮。
func TestGeneratedRealtimeWrapsWSSendControls(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("realtime-app"))

	page := pr.read("web/templates/portal/pages/realtime.html")
	connectAt := strings.Index(page, `hx-ws:connect="/ws"`)
	composerAt := strings.Index(page, `class="yd-composer" data-yd-ws-composer`)
	sendAt := strings.Index(page, `hx-ws:send="true"`)
	switch {
	case connectAt < 0:
		t.Fatal("realtime.html does not open a WebSocket connection | realtime.html 未打开 WebSocket 连接")
	case composerAt < 0 || sendAt < 0:
		t.Fatal("realtime.html lacks the composer or the send button | realtime.html 缺少输入区或发送按钮")
	case connectAt > composerAt || composerAt > sendAt:
		t.Errorf("hx-ws:connect must wrap the composer and send button (connect=%d composer=%d send=%d) | hx-ws:connect 必须包住输入区与发送按钮（connect=%d composer=%d send=%d）",
			connectAt, composerAt, sendAt, connectAt, composerAt, sendAt)
	}
	if strings.Contains(page, `hx-trigger="keyup`) {
		t.Error("the composer must not use an hx-trigger filter: the CSP blocks htmx's new Function compilation | 输入区不得使用 hx-trigger 过滤器：CSP 会拦截 htmx 的 new Function 编译")
	}

	app := pr.read("web/static/js/yd-app.js")
	for _, want := range []string{"data-yd-ws-enter", "window.htmx.process(document.body)"} {
		if !strings.Contains(app, want) {
			t.Errorf("yd-app.js lacks %q | yd-app.js 缺少 %q", want, want)
		}
	}
}

// TestGeneratedPersistenceFollowsDatabaseChoice 断言生成时的数据库选择决定持久化形态：
// 选了数据库的项目把连接池、迁移与 GORM 实现装进 repository，未选数据库的项目一行 ORM 依赖都不引入，
// 只在启动时如实告警「数据仅存于内存，重启即丢失」。
// TestGeneratedPersistenceFollowsDatabaseChoice asserts that the database choice made at generation time decides the
// persistence shape: a project with a database wires the pool, the migration and the GORM implementation into repository,
// while a project without one imports no ORM at all and only warns faithfully that the data lives in memory.
func TestGeneratedPersistenceFollowsDatabaseChoice(t *testing.T) {
	// ORM 依赖只允许出现在选了数据库的项目里：授权表由框架的 auth/gormstore 拥有，本项目只装配它。
	// ORM dependencies may only appear in a project that picked a database: the auth tables belong to the framework's
	// auth/gormstore and this project merely assembles it.
	ormImports := []string{
		`"github.com/shibingli/yd/security/auth/gormstore"`,
		`"github.com/shibingli/ydx/gorm"`,
		`"gorm.io/gorm"`,
	}

	withDB := scaffold.DefaultOptions("persist-app")
	withDB.DB = scaffold.DBSQLite
	prDB, _ := generateMem(t, withDB)

	store := prDB.read("internal/repository/store.go")
	for _, want := range []string{
		`"github.com/shibingli/yd/security/auth/gormstore"`,
		`"github.com/shibingli/ydx/gorm"`,
		"gormstore.Migrate(ctx, db)",
		"gormstore.New(db, gormstore.OptionsFromConfig(o.Config))",
		"attachmentsgorm.MigrateLegacy(ctx, gdb)",
		"attachmentsgorm.DropLegacy(ctx, gdb)",
		"auth data is stored in the database",
		"no database is configured, auth data lives in memory",
		"database unavailable, auth data falls back to memory",
		"func (s *Store) Close() error",
	} {
		if !strings.Contains(store, want) {
			t.Errorf("选了数据库的 store.go 缺少 %q", want)
		}
	}
	if !strings.Contains(prDB.read("internal/router/router.go"), "Config:") {
		t.Error("router.go does not hand the configuration to repository.Open, so Open cannot read the db section | router.go 未把配置交给 repository.Open，Open 就读不到 db 段")
	}

	cfgDB := prDB.read("configs/config.yaml")
	for _, want := range []string{"gorm:", "query_timeout", "maxconn: 100", "maxidle: 10"} {
		if !strings.Contains(cfgDB, want) {
			t.Errorf("选了数据库的 config.yaml 缺少 %q", want)
		}
	}
	// auth.gorm 必须留在 auth 段里：插在 captcha 段之后会让 query_timeout 变成 captcha.gorm，框架读不到。
	// auth.gorm must stay inside the auth block: placing it after captcha turns query_timeout into captcha.gorm,
	// a key the framework never reads.
	authAt, captchaAt := strings.Index(cfgDB, "\nauth:\n"), strings.Index(cfgDB, "\ncaptcha:\n")
	if authAt < 0 || captchaAt < 0 || authAt >= captchaAt {
		t.Fatalf("config.yaml 的 auth/captcha 段位置异常: auth=%d captcha=%d", authAt, captchaAt)
	}
	if !strings.Contains(cfgDB[authAt:captchaAt], "\n  gorm:\n") {
		t.Error("config.yaml 把 auth.gorm 放到了 auth 段之外 | config.yaml moved auth.gorm outside the auth block")
	}
	// ydx/gorm 读的是 db.maxconn：写成基线文档里的 db.max_conn 就是一个永远不会生效的死键。
	// ydx/gorm reads db.maxconn: the db.max_conn spelling from the baseline document is a dead key that never takes effect.
	if strings.Contains(cfgDB, "max_conn") {
		t.Error("config.yaml still spells the key ydx/gorm never reads as db.max_conn (it must be db.maxconn) | config.yaml 仍写着 ydx/gorm 不读的 db.max_conn（应为 db.maxconn）")
	}

	plain := scaffold.DefaultOptions("memory-app")
	plain.DB = scaffold.DBNone
	prNone, _ := generateMem(t, plain)

	// 全量扫描：未选数据库的项目里任何一个生成物都不得引入 ORM。
	// A wholesale scan: no artefact of a project without a database may import an ORM.
	for _, rel := range requiredFiles {
		content := prNone.read(rel)
		for _, imp := range ormImports {
			if strings.Contains(content, imp) {
				t.Errorf("未选数据库的项目里 %s 引入了 %s", rel, imp)
			}
		}
	}

	storeNone := prNone.read("internal/repository/store.go")
	for _, unwanted := range []string{"gormstore", "github.com/shibingli/ydx/gorm"} {
		if strings.Contains(storeNone, unwanted) {
			t.Errorf("未选数据库的 store.go 不应出现 %q", unwanted)
		}
	}
	for _, want := range []string{"generated without a database", "auth.NewMemoryStore()"} {
		if !strings.Contains(storeNone, want) {
			t.Errorf("未选数据库的 store.go 缺少 %q", want)
		}
	}
	if cfgNone := prNone.read("configs/config.yaml"); strings.Contains(cfgNone, "gorm:") ||
		strings.Contains(cfgNone, "maxconn") {
		t.Errorf("未选数据库的 config.yaml 不应出现数据库连接池配置:\n%s", cfgNone)
	}
	if envNone := prNone.read(".env.template"); strings.Contains(envNone, "YD_DB_DSN") {
		t.Errorf("未选数据库的 .env.template 不应出现 YD_DB_DSN:\n%s", envNone)
	}
	if keep := prNone.read("data/.gitkeep"); !strings.Contains(keep, "data") {
		t.Errorf("未选数据库的 data/.gitkeep 未说明目录用途:\n%s", keep)
	}
}

// TestGeneratedProjectEnablesGormCacheByDefault asserts that every project with a database gets
// the two-level cache and its GORM plugin enabled by default, while the auth tables stay
// uncached so disabling a user or rotating a sec_key still takes effect immediately. |
// TestGeneratedProjectEnablesGormCacheByDefault 断言每个带数据库的项目默认开启二级缓存及其
// GORM 插件，同时把鉴权表排除在缓存之外，使禁用用户或轮换 sec_key 仍然立即生效。
func TestGeneratedProjectEnablesGormCacheByDefault(t *testing.T) {
	withDB := scaffold.DefaultOptions("cached-app")
	withDB.DB = scaffold.DBSQLite
	pr, _ := generateMem(t, withDB)

	// The template is checked out with CRLF on Windows, so normalise before matching a block. |
	// 模板在 Windows 上以 CRLF 检出，匹配整块前先归一化换行。
	cfg := strings.ReplaceAll(pr.read("configs/config.yaml"), "\r\n", "\n")
	for _, want := range []string{
		"  gorm:\n    enabled: true",
		"ignore_tables:",
		"- yd_auth_users",
		"sensitive_tables:",
		"- yd_auth_tenants",
		"- yd_auth_user_tenants",
		"- yd_auth_roles",
		"- yd_auth_role_permissions",
		"sensitive_ttl: 5s",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("configs/config.yaml is missing %q | configs/config.yaml 缺少 %q", want, want)
		}
	}
	// Message tables are regular data now; they must not be excluded from the cache. Attachments
	// are the one exception: StoredKey is json:"-" and a JSON-backed cache would erase it. |
	// 消息表现在是常规数据，不得再排除在缓存之外。附件是唯一例外：StoredKey 带 json:"-",
	// 基于 JSON 的查询缓存会把它丢掉。
	if strings.Contains(cfg, "- yd_msg_messages") || strings.Contains(cfg, "- yd_msg_conversations") {
		t.Error("message tables must stay cached | 消息表必须保持缓存")
	}

	// The plugin only installs when the configuration enables it. | 只有配置开启时插件才会安装。
	router := pr.read("internal/router/router.go")
	for _, want := range []string{"gormcache.EnabledFromConfig", "gormcache.New", "gormDB.Use(cachePlugin)"} {
		if !strings.Contains(router, want) {
			t.Errorf("internal/router/router.go 缺少 %q:\n%s", want, router)
		}
	}

	// Without a database there is no GORM plugin to configure. | 未选数据库时没有可配置的 GORM 插件。
	plain := scaffold.DefaultOptions("uncached-app")
	plain.DB = scaffold.DBNone
	prPlain, _ := generateMem(t, plain)
	if cfg := prPlain.read("configs/config.yaml"); strings.Contains(cfg, "ignore_tables:") {
		t.Errorf("未选数据库的 config.yaml 不应出现 GORM 缓存配置:\n%s", cfg)
	}
}

// TestGeneratedProjectSharesOneCronScheduler proves the scaffold wires exactly one process-wide cron
// scheduler, mounts the workflow scan on it and gives the deployment a project-specific lock namespace.
// | TestGeneratedProjectSharesOneCronScheduler 证明脚手架只装配一个进程级 cron 调度器、把流程扫描
// 挂载其上，并为部署提供项目专属的锁命名空间。
func TestGeneratedProjectSharesOneCronScheduler(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("cron-app"))

	cfg := strings.ReplaceAll(pr.read("configs/config.yaml"), "\r\n", "\n")
	for _, want := range []string{
		"cron:\n",
		`namespace: "yd:cron-app:cron"`,
		"node_id:",
		"lock_ttl: 30s",
		"shutdown_timeout: 30s",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("configs/config.yaml is missing %q | configs/config.yaml 缺少 %q", want, want)
		}
	}

	// The shared scheduler must fall back to the project-scoped namespace rather than the generic
	// "cron" default, and it must never configure a Store: Reload would drop the in-memory scan job.
	// | 共享调度器必须回退到项目级命名空间而不是通用默认值 "cron"，且不得配置 Store：
	// Reload 会丢弃内存中的扫描任务。
	cronSvc := pr.read("internal/service/cron.go")
	for _, want := range []string{
		"cron.OptionsFromConfig(cfg)",
		"cronNamespacePrefix + meta.Name + cronNamespaceSuffix",
		"cronredis.NewLocker(redisClient)",
		"options.Locker = cronredis.NewLocker(redisClient)",
	} {
		if !strings.Contains(cronSvc, want) {
			t.Errorf("internal/service/cron.go 缺少 %q:\n%s", want, cronSvc)
		}
	}
	if strings.Contains(cronSvc, "Store:") || strings.Contains(cronSvc, "options.Store") {
		t.Error("the shared scheduler must not configure a Store | 共享调度器不得配置 Store")
	}

	workflowSvc := pr.read("internal/service/workflow.go")
	for _, want := range []string{
		"shared *cron.Scheduler",
		"workflow.WithCronScheduler(shared)",
	} {
		if !strings.Contains(workflowSvc, want) {
			t.Errorf("internal/service/workflow.go 缺少 %q", want)
		}
	}

	ctrl := pr.read("internal/controller/controller.go")
	for _, want := range []string{
		"service.NewCronService(",
		"cronSvc.Scheduler()",
		"func (c *Controller) CronScheduler()",
		"c.startCronRuntime(ctx)",
		"func (c *Controller) startCronRuntime(ctx context.Context)",
		"return c.cronSvc.Stop()",
	} {
		if !strings.Contains(ctrl, want) {
			t.Errorf("internal/controller/controller.go 缺少 %q", want)
		}
	}
	// The cron stop hook is registered before the workflow one, so the reverse-order shutdown
	// detaches the scan before the scheduler closes. | cron 的停止钩子先于流程钩子登记，逆序关停
	// 才会先卸载扫描、再关闭调度器。
	start := strings.Index(ctrl, "func (c *Controller) StartWorkflowRuntime")
	if start < 0 {
		t.Fatal("StartWorkflowRuntime is missing | 缺少 StartWorkflowRuntime")
	}
	body := ctrl[start:]
	if cronStart, workflowHook := strings.Index(body, "c.startCronRuntime(ctx)"), strings.Index(body, "c.workflow.SchedulerStop()"); cronStart < 0 || workflowHook < 0 || cronStart > workflowHook {
		t.Errorf("StartWorkflowRuntime must start cron before registering the workflow stop hook | StartWorkflowRuntime 必须先启动 cron 再登记流程停止钩子")
	}
}

// TestGeneratedPortalAndManagementTreesAreIsolated pins the portal/back-office split: the two template trees never
// include each other, the management endpoints live under /mgr and /api/v1 while member endpoints live under
// /api/portal/v1, and the controller selects the layout from view data. | TestGeneratedPortalAndManagementTreesAreIsolated
// 固定门户与后台的隔离：两棵模板树互不 include，管理端点在 /mgr 与 /api/v1 下，成员端点在 /api/portal/v1 下，
// 控制器按视图数据选择布局。
func TestGeneratedPortalAndManagementTreesAreIsolated(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("split-app"))

	// 模板树隔离：门户不得引用 mgr 树的片段，后台不得引用 portal 树的片段。
	// Template isolation: neither tree may include a fragment from the other.
	for _, rel := range requiredFiles {
		switch {
		case strings.HasPrefix(rel, "web/templates/portal/"):
			if body := pr.read(rel); strings.Contains(body, `"mgr/`) || strings.Contains(body, `"mgr/partials/`) {
				t.Errorf("%s 引用了后台模板树 | %s includes the management tree", rel, rel)
			}
		case strings.HasPrefix(rel, "web/templates/mgr/"):
			if body := pr.read(rel); strings.Contains(body, `"portal/`) || strings.Contains(body, `"portal/partials/`) {
				t.Errorf("%s 引用了门户模板树 | %s includes the portal tree", rel, rel)
			}
		}
	}
	for _, stale := range []string{
		"web/templates/layouts/base.html",
		"web/templates/pages/home.html",
		"web/templates/partials/sidebar.html",
	} {
		if pr.exists(stale) {
			t.Errorf("旧模板路径仍然存在: %s | legacy template path still exists: %s", stale, stale)
		}
	}

	// 布局选择：控制器用 render.LayoutKey 与两套布局/外壳常量选择各自入口；两侧共用 partials/base 与
	// partials/app_shell，角色差异只体现在数据（.Portal/.Mgr）选出的侧栏上。
	// Layout selection: the controller uses render.LayoutKey and two layout/shell constant sets to pick each entry;
	// both sides share partials/base and partials/app_shell, and the role difference exists only in the rail that
	// the .Portal / .Mgr data key selects.
	ctrl := pr.read("internal/controller/controller.go")
	for _, want := range []string{
		"render.LayoutKey",
		"LayoutPortal",
		"LayoutMgr",
		"ShellPortal",
		"ShellMgr",
	} {
		if !strings.Contains(ctrl, want) {
			t.Errorf("controller.go 缺少 %q | controller.go is missing %q", want, want)
		}
	}

	// 路由隔离：管理页面与管理 API 在 /mgr 与 /api/v1，成员 API 在 /api/portal/v1。
	// Route isolation: management pages and APIs live under /mgr and /api/v1, member APIs under /api/portal/v1.
	router := pr.read("internal/router/router.go")
	for _, want := range []string{
		`g.GET("/mgr", ctrl.ManagementHome`,
		`g.GET("/mgr/users"`,
		`g.GET("/mgr/messages"`,
		`g.GET("/api/portal/v1/messages"`,
		`g.GET("/api/v1/message-types"`,
	} {
		if !strings.Contains(router, want) {
			t.Errorf("router.go 缺少隔离后的路由 %q | router.go is missing %q", want, want)
		}
	}
	for _, unwanted := range []string{
		`g.GET("/users"`,
		`g.GET("/messages/admin"`,
		`g.GET("/api/v1/messages/conversations"`,
	} {
		if strings.Contains(router, unwanted) {
			t.Errorf("router.go 仍暴露旧路由 %q | router.go still exposes %q", unwanted, unwanted)
		}
	}
}

// TestGeneratedPortalAndManagementShareOneComponentLayer pins the single component layer both surfaces use.
// The document skeleton, application shell, top bar, confirmation dialog, drawer, flash, switches, badges and the
// message card exist once under web/templates/partials; the two trees keep only their own rail and pages, and neither
// tree includes the other. | TestGeneratedPortalAndManagementShareOneComponentLayer 固定门户与后台共用同一套组件层：
// 文档骨架、应用外壳、顶栏、确认框、抽屉、提示、切换按钮、角标与消息卡片都只在
// web/templates/partials 下存在一份；两棵树只保留自己的侧栏与页面，且互不 include。
func TestGeneratedPortalAndManagementShareOneComponentLayer(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("shared-components-app"))

	// Every shared component exists exactly once at the root of the template tree. | 共享组件只在树根存在一份。
	for _, rel := range []string{
		"web/templates/partials/base.html",
		"web/templates/partials/app_shell.html",
		"web/templates/partials/topbar.html",
		"web/templates/partials/flash.html",
		"web/templates/partials/shell-switches.html",
		"web/templates/partials/sidebar_brand.html",
		"web/templates/partials/sidebar_footer.html",
		"web/templates/partials/messages/message.html",
		"web/templates/partials/messages/unread_badge.html",
		"web/templates/partials/messages/unread_frames.html",
		"web/templates/partials/messages/unread_menu_badge.html",
		"web/templates/partials/messages/sidebar_badges.html",
		"web/templates/partials/pages/error.html",
	} {
		if !pr.exists(rel) {
			t.Errorf("共享组件缺失: %s | shared component missing: %s", rel, rel)
		}
	}

	// The two trees contain no copy of a shared component. | 两棵树里没有共享组件的副本。
	for _, stale := range []string{
		"web/templates/portal/partials/icon.html",
		"web/templates/mgr/partials/icon.html",
		"web/templates/portal/partials/flash.html",
		"web/templates/mgr/partials/flash.html",
		"web/templates/portal/partials/shell-switches.html",
		"web/templates/mgr/partials/shell-switches.html",
		"web/templates/portal/partials/messages/message.html",
		"web/templates/mgr/partials/messages/message.html",
		"web/templates/portal/partials/messages/unread_badge.html",
		"web/templates/portal/partials/messages/unread_menu_badge.html",
		"web/templates/portal/partials/messages/sidebar_badges.html",
		"web/templates/portal/pages/error.html",
		"web/templates/mgr/pages/error.html",
	} {
		if pr.exists(stale) {
			t.Errorf("共享组件仍有树内副本: %s | duplicated inside a tree: %s", stale, stale)
		}
	}

	// Both layout entries only delegate to the shared skeleton. | 两个布局入口只委托给共享骨架。
	for _, rel := range []string{"web/templates/portal/layouts/base.html", "web/templates/mgr/layouts/base.html"} {
		body := pr.read(rel)
		if !strings.Contains(body, `{{ template "partials/base" . }}`) {
			t.Errorf("%s 未调用共享骨架 partials/base | %s does not call the shared skeleton", rel, rel)
		}
		if strings.Contains(body, "<!doctype") || strings.Contains(body, "<html") {
			t.Errorf("%s 仍然自带整页骨架 | %s still carries its own document skeleton", rel, rel)
		}
	}

	// Both shell entries only delegate to the shared application shell. | 两个外壳入口只委托给共享应用外壳。
	portalShell := pr.read("web/templates/portal/layouts/shell.html")
	if !strings.Contains(portalShell, `{{ template "partials/app_shell" . }}`) {
		t.Error("门户外壳未调用共享应用外壳 | the portal shell does not call the shared application shell")
	}
	if !strings.Contains(portalShell, ".SignedIn") || !strings.Contains(portalShell, "yd-auth-shell") {
		t.Error("门户外壳丢失了唯一的匿名登录分支 | the portal shell lost its anonymous sign-in branch")
	}
	mgrShell := pr.read("web/templates/mgr/layouts/shell.html")
	if strings.TrimSpace(mgrShell[strings.LastIndex(mgrShell, "*/}}")+len("*/}}"):]) != `{{ template "partials/app_shell" . }}` {
		t.Errorf("后台外壳必须只调用共享应用外壳 | the management shell must only call the shared application shell")
	}

	// The shared shell chooses one rail from data, while each tree keeps its own rail. | 共享外壳按数据选择侧栏，
	// 两棵树的侧栏仍各自保留。
	appShell := pr.read("web/templates/partials/app_shell.html")
	for _, want := range []string{
		`template "portal/partials/sidebar"`, `template "mgr/partials/sidebar"`,
		`id="yd-attachment-preview"`, `data-yd-attachment-preview-image`, `tr "attachment.preview_title"`,
	} {
		if !strings.Contains(appShell, want) {
			t.Errorf("共享应用外壳缺少侧栏选择或附件预览 %q | the shared application shell lacks %q", want, want)
		}
	}
	for _, rel := range []string{"web/templates/portal/partials/sidebar.html", "web/templates/mgr/partials/sidebar.html"} {
		body := pr.read(rel)
		for _, want := range []string{`partials/sidebar_brand`, `partials/sidebar_footer`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s 未复用共享侧栏部件 %q | %s does not reuse %q", rel, want, rel, want)
			}
		}
	}

	// Both surfaces render the same message card, and its actions come from the view's BasePath. | 两侧渲染同一张
	// 消息卡片，动作前缀来自视图的 BasePath。
	card := pr.read("web/templates/partials/messages/message.html")
	if !strings.Contains(card, "{{ .BasePath }}") {
		t.Error("共享消息卡片必须从 .BasePath 取动作前缀 | the shared message card must take its action prefix from .BasePath")
	}
	for _, want := range []string{"data-yd-attachment-preview", "data-yd-attachment-src", "data-yd-attachment-download"} {
		if !strings.Contains(card, want) {
			t.Errorf("消息卡片附件缺少页内预览属性 %q | message-card attachments lack %q", want, want)
		}
	}
	if strings.Contains(card, `target="_blank"`) {
		t.Error("消息附件不得在新页面打开，必须使用共享预览对话框 | message attachments must not open in a new page; use the shared preview dialog")
	}
	composer := pr.read("web/templates/portal/partials/messages/composer.html")
	for _, want := range []string{
		"data-yd-upload-form", "data-yd-upload-file", "data-yd-upload-status-target", "data-yd-upload-status",
		"data-yd-upload-uploading", "data-yd-upload-uploaded", "data-yd-upload-failed", "data-yd-upload-error-target",
		`tr "message.uploading"`, `tr "message.uploaded"`, `tr "message.upload_failed"`,
	} {
		if !strings.Contains(composer, want) {
			t.Errorf("内联撰写区缺少自动上传接线 %q | the inline composer lacks %q", want, want)
		}
	}
	compose := pr.read("web/templates/portal/partials/messages/compose.html")
	for _, want := range []string{
		"data-yd-upload-form", "components.form.field", `"Type" "file"`, "data-yd-upload-submit", "data-yd-upload-status",
		"data-yd-upload-uploading", "data-yd-upload-uploaded", "data-yd-upload-failed", "data-yd-upload-error-target",
		"data-yd-attachment-preview", "data-yd-attachment-src", "data-yd-attachment-download",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("消息抽屉缺少自动上传或附件预览接线 %q | the message drawer lacks %q", want, want)
		}
	}
	if strings.Contains(compose, `target="_blank"`) {
		t.Error("抽屉附件不得在新页面打开，必须使用共享预览对话框 | drawer attachments must not open in a new page; use the shared preview dialog")
	}
	for _, rel := range []string{"web/templates/portal/partials/messages/history.html", "web/templates/mgr/partials/messages/audit.html"} {
		if !strings.Contains(pr.read(rel), `template "partials/messages/message"`) {
			t.Errorf("%s 未使用共享消息卡片 | %s does not use the shared message card", rel, rel)
		}
	}
	ctrl := pr.read("internal/controller/controller.go")
	for _, want := range []string{"MessagePortalBase", "MessageAdminBase"} {
		if !strings.Contains(ctrl, want) {
			t.Errorf("controller.go 缺少消息卡片动作前缀常量 %q | controller.go is missing %q", want, want)
		}
	}

	// The moderation page hosts the edit and announcement drawers, both of which contain an EasyMDE
	// editor. A shell swap never rewrites <head>, so the page must declare the lazy asset node that
	// yd-app.js reads; without it the drawer editor stays a bare textarea until a full reload. |
	// 治理页承载编辑与新建公告抽屉，两者都含 EasyMDE 编辑器。外壳交换不会重写 <head>，页面必须声明
	// yd-app.js 读取的惰性资产节点；缺少它时抽屉编辑器要整页刷新才会升级。
	adminPage := pr.read("web/templates/mgr/pages/message_admin.html")
	for _, want := range []string{`id="yd-page-assets"`, "data-yd-styles=", "data-yd-scripts="} {
		if !strings.Contains(adminPage, want) {
			t.Errorf("message_admin.html lacks %q; its drawer editors never initialise after an htmx navigation | message_admin.html 缺少 %q；htmx 导航后抽屉编辑器不会初始化", want, want)
		}
	}

	// A shell swap must carry the new page title into the tab. | 外壳交换必须把新页面标题带进标签页。
	base := pr.read("web/templates/partials/base.html")
	if !strings.Contains(base, `data-yd-app-name="{{ .Name }}"`) {
		t.Error("共享骨架缺少 data-yd-app-name | the shared skeleton lacks data-yd-app-name")
	}
	if !strings.Contains(pr.read("web/static/js/yd-app.js"), "syncDocumentTitle") {
		t.Error("yd-app.js 未在外壳交换后同步 document.title | yd-app.js does not sync document.title after a shell swap")
	}
}

// TestGeneratedTemplatesUseComponentFormControls pins the visible form contract: ordinary inputs, selects and date
// pickers come from components.form.* instead of hand-written native controls, and icon-bearing action-menu rows use
// the flex modifier that keeps the glyph on the same line. |
// TestGeneratedTemplatesUseComponentFormControls 固定可见表单契约：普通输入框、下拉框与日期选择器必须来自
// components.form.*，不得手写原生控件；带图标的操作菜单行使用保持同一行的弹性修饰类。
func TestGeneratedTemplatesUseComponentFormControls(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("component-form-controls"))

	// Scan every generated template outside components/: a single hand-written select/date/input is enough to
	// make one page drift from the component library. | 扫描 components/ 之外的每一个生成模板：只要有一个手写的
	// select、日期或输入框，就足以让某个页面偏离组件库。
	for _, rel := range requiredFiles {
		if !strings.HasPrefix(rel, "web/templates/") || strings.HasPrefix(rel, "web/templates/components/") {
			continue
		}
		body := pr.read(rel)
		for _, unwanted := range []string{
			"<select", `type="date"`, `type="datetime-local"`, `type="month"`, `type="time"`, `class="yd-input`,
		} {
			if strings.Contains(body, unwanted) {
				t.Errorf("%s 手写原生控件 %q；改用 components.form.field/select/date | %s hand-writes %q; use components.form.field/select/date", rel, unwanted, rel, unwanted)
			}
		}
	}

	for _, rel := range []string{
		"web/templates/portal/pages/attachments.html",
		"web/templates/mgr/pages/attachments.html",
	} {
		body := pr.read(rel)
		for _, want := range []string{
			`components.form.field`, `components.form.select`, `components.form.date`,
			`yd-menu__item--with-icon`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s 缺少组件化控件或图标菜单修饰类 %q | %s lacks %q", rel, want, rel, want)
			}
		}
	}

	if !strings.Contains(pr.read("web/templates/mgr/partials/messages/audit.html"), `components.form.field`) {
		t.Error("消息审计筛选栏仍手写搜索输入框 | the message audit filter still hand-writes its search inputs")
	}
	if !strings.Contains(pr.read("web/templates/partials/base.html"), `name="htmx-config"`) {
		t.Error("共享骨架缺少 htmx-config 的 hx-live 前缀配置 | the shared skeleton lacks the htmx-config hx-live prefix")
	}
	css := pr.read("web/static/css/components.css")
	if !strings.Contains(css, ".yd-menu__item.yd-menu__item--with-icon") {
		t.Error("components.css 缺少图标菜单行的弹性布局规则 | components.css lacks the flex rule for icon action rows")
	}

	// Every action menu shares one trigger and one item layout: an icon button opens a details panel, and
	// icon-bearing items use the flex modifier. A stray ghost button or text-only icon row makes two tables
	// look like they came from different products. | 所有操作菜单共用同一套触发器与菜单项布局：图标按钮打开
	// details 面板，带图标的菜单项使用弹性修饰类；混入 ghost 按钮或纯文字图标行会让两张表看起来不是同一套界面。
	menus := []string{
		"web/templates/mgr/partials/users/row.html",
		"web/templates/mgr/partials/roles/list.html",
		"web/templates/mgr/partials/message_settings/body.html",
		"web/templates/portal/pages/attachments.html",
		"web/templates/mgr/pages/attachments.html",
		"web/templates/portal/partials/friends/summary.html",
		"web/templates/portal/partials/messages/thread.html",
		"web/templates/partials/messages/message.html",
	}
	for _, rel := range menus {
		body := pr.read(rel)
		if strings.Contains(body, `<summary class="yd-btn`) {
			t.Errorf("%s 的菜单触发器仍是文字按钮，应统一为 yd-icon-btn | %s still uses a text-button menu trigger", rel, rel)
		}
		if !strings.Contains(body, "yd-menu__item--with-icon") {
			t.Errorf("%s 的菜单项未统一使用图标弹性行 | %s does not use the shared icon menu row", rel, rel)
		}
	}
	for _, rel := range []string{"web/templates/portal/pages/attachments.html", "web/templates/mgr/pages/attachments.html"} {
		if !strings.Contains(pr.read(rel), "yd-table__td--actions") {
			t.Errorf("%s 的操作列缺少统一的 yd-table__td--actions | %s lacks the shared action cell class", rel, rel)
		}
	}
}

// TestGeneratedProjectWiresAuthzRegistry 断言生成的项目真的接上了框架的授权注册表：路由挂 authz.Middleware 并用
// authz.Require(authz.PermXxx) 守卫每一段写路径、控制器把 authz.Data 注入视图数据、模板按数据键与常量名两种写法判定，
// 而种子数据的角色与能力一律取自注册表——因此 `yd authz check` 在生成物上零错误。
// TestGeneratedProjectWiresAuthzRegistry asserts a generated project really hooks up the framework authorization
// registry: the router mounts authz.Middleware and guards every write path with authz.Require(authz.PermXxx), the
// controllers inject authz.Data into the view data, the templates decide on both spellings (data key and constant name),
// and the seed takes every role and capability from the registry — so `yd authz check` reports zero errors on it.
func TestGeneratedProjectWiresAuthzRegistry(t *testing.T) {
	o := scaffold.DefaultOptions("authz-app")
	o.DB = scaffold.DBSQLite
	pr, _ := generateMem(t, o)

	// 路由：中间件在链上，守卫引用寄存器里的常量。 | Router: the middleware is on the chain and every guard cites a registry constant.
	router := pr.read("internal/router/router.go")
	for _, want := range []string{
		"authz.Middleware(ctrl.Auth())",
		"authz.Require(authz.PermTenantRead)",
		"authz.Require(authz.PermTenantCreate)",
		"authz.Require(authz.PermUserRead)",
		"authz.Require(authz.PermUserCreate)",
		"authz.Require(authz.PermUserUpdate)",
		"authz.Require(authz.PermRoleRead)",
		"authz.Require(authz.PermRoleManage)",
		"authz.Require(authz.PermRoleUpdate)",
		"authz.Require(authz.PermRoleDelete)",
		"authz.Require(authz.PermPermissionRead)",
	} {
		if !strings.Contains(router, want) {
			t.Errorf("internal/router/router.go 缺少守卫 %q", want)
		}
	}

	// 控制器：视图数据里必须有注入的能力表，否则模板里的 .Perm 与 base.html 的 .AuthzJSON 都取不到值（失败关闭）。
	// Controllers: the view data carries the injected capability map, without which .Perm in the templates and .AuthzJSON
	// in base.html resolve to nothing (fail closed).
	ctrl := pr.read("internal/controller/controller.go")
	for _, want := range []string{"authz.Data(", "authz.SubjectOf(", "authz.NewSubject("} {
		if !strings.Contains(ctrl, want) {
			t.Errorf("internal/controller/controller.go 缺少 %q", want)
		}
	}

	// 模板：两个可照抄的范例——声明式属性与条件判定各一，两者都读注册表派生的键。
	// Templates: the two copyable examples — one declarative attribute and one condition — both reading keys derived from
	// the registry.
	form := pr.read("web/templates/mgr/partials/users/form.html")
	if want := `data-yd-perm="{{ ` + authz.PermSelector + authz.DataKey(authz.PermUserCreate) + ` }}"`; !strings.Contains(form, want) {
		t.Errorf("users/form.html 的提交按钮缺少 %q", want)
	}
	sidebar := pr.read("web/templates/mgr/partials/sidebar.html")
	if want := `can "` + authz.ConstantName(authz.PermUserRead) + `"`; !strings.Contains(sidebar, want) {
		t.Errorf("sidebar.html 缺少常量名写法 %q", want)
	}
	if want := `can ` + authz.PermSelector + authz.DataKey(authz.PermTenantRead); !strings.Contains(sidebar, want) {
		t.Errorf("sidebar.html 缺少数据键写法 %q", want)
	}

	// 种子数据：角色与能力取自注册表，管理员拿的是注册表里的所有者角色。
	// The seed: roles and capabilities come from the registry, and the administrator holds the registry's owner role.
	store := pr.read("internal/repository/store.go")
	for _, want := range []string{
		"authz.BuiltinRoles()",
		"authz.BuiltinPermissions()",
		"authz.RoleOwner",
		"meta.DevSeedAdminPassword",
	} {
		if !strings.Contains(store, want) {
			t.Errorf("internal/repository/store.go 缺少 %q", want)
		}
	}

	// 零字面量：把 `yd authz check` 的判定直接跑在生成物上。
	// Zero literals: the verdict of `yd authz check` runs straight over the generated output.
	assertAuthzCheckPasses(t, pr.fs, o.Name, "MemFS 生成项目")
}

// TestGenerateDockerfileUsesScratchMultiStage 断言 Dockerfile 采用多阶段 + scratch。
// TestGenerateDockerfileUsesScratchMultiStage asserts the Dockerfile is multi-stage and scratch-based.
func TestGenerateDockerfileUsesScratchMultiStage(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("docker-app"))
	df := pr.read("Dockerfile")
	goTag := scaffold.GoVersion
	if parts := strings.SplitN(goTag, ".", 3); len(parts) >= 2 {
		goTag = parts[0] + "." + parts[1]
	}
	for _, want := range []string{
		"FROM golang:" + goTag + "-alpine AS builder",
		"FROM scratch",
		"CGO_ENABLED=0",
		"ca-certificates",
		"zoneinfo",
		"ARG YD_INCLUDE_CONFIG=true",
		"ENV YD_CONFIG_DIR=/configs",
		"COPY --from=builder /out/configs /configs",
		"COPY --from=builder /out/data /data",
		"ARG YD_INCLUDE_UPX=false",
		"/src/.yd/upx/upx",
		"--best --lzma",
		"1073741824",
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
		`"` + scaffold.YdModule + `/runtime/app"`,
		`"` + scaffold.YdxModule + `/errs"`,
		`"` + scaffold.YdxModule + `/log"`,
		`"` + scaffold.YdxModule + `/middleware"`,
		"signal.NotifyContext",
		"syscall.SIGTERM",
		"syscall.SIGQUIT",
		"syscall.SIGHUP",
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

	// Idempotence: nothing is rewritten and the key files stay correct. | 幂等性：内容不被改写，关键文件仍然正确。
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
		t.Fatal("a conflicting file must be an error while Force is off | 存在冲突且未 Force 时必须报错")
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
		t.Error("on a conflict no other file may be written (validate first, then write) | 冲突时其余文件不应被写出（先校验后写入）")
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
	if !strings.Contains(gomod, scaffold.YdModule+" v0.0.0") {
		t.Errorf("go.mod 缺少框架 require:\n%s", gomod)
	}
	if !strings.Contains(gomod, "replace github.com/shibingli/yd => /srv/yd") {
		t.Errorf("go.mod 缺少 replace 指向本地仓库:\n%s", gomod)
	}
	if !strings.Contains(gomod, scaffold.YdxModule+"/errs v0.0.0") {
		t.Errorf("go.mod 缺少 ydx require:\n%s", gomod)
	}
	if !strings.Contains(gomod, "replace "+scaffold.YdxModule+"/errs => /srv/ydx/errs") {
		t.Errorf("go.mod 缺少 ydx replace 指向同级仓库:\n%s", gomod)
	}
}

// TestLocalReplaceUNCIsQuoted proves a UNC-style replacement path is quoted: unquoted, the modfile
// lexer reads the leading "//" as a comment and drops the replacement target. | TestLocalReplaceUNCIsQuoted
// 证明 UNC 形态的替换路径会加引号：不加引号时 modfile 词法器把开头的 "//" 当作注释，替换目标会被丢掉。
func TestLocalReplaceUNCIsQuoted(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC paths are Windows-only | UNC 路径仅适用于 Windows")
	}
	o := scaffold.DefaultOptions("unc-app")
	o.LocalReplace = "//server/share/yd"
	pr, _ := generateMem(t, o)

	gomod := pr.read("go.mod")
	want := `replace github.com/shibingli/yd => "//server/share/yd"`
	if !strings.Contains(gomod, want) {
		t.Errorf("go.mod 未引用 UNC 路径:\n got:\n%s\nwant substring: %s", gomod, want)
	}
	wantYdx := `replace ` + scaffold.YdxModule + `/errs => "//server/share/ydx/errs"`
	if !strings.Contains(gomod, wantYdx) {
		t.Errorf("go.mod 未引用 ydx 的 UNC 路径:\n got:\n%s\nwant substring: %s", gomod, wantYdx)
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

	for _, want := range []string{"htmx", "alpinejs", "idiomorph", "hx-sse", "hx-alpine-compat"} {
		if !slices.Contains(names, want) {
			t.Errorf("资产清单缺少 %q: %v", want, names)
		}
	}
	for _, unwanted := range []string{"hx-ws", "hx-preload", "hx-targets", "hx-head"} {
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
				t.Fatal("an invalid argument must be an error | 非法参数必须报错")
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
		got.GRPC != want.GRPC || got.I18n != want.I18n ||
		!slices.Equal(got.HtmxExts, want.HtmxExts) {
		t.Errorf("非 TTY 应返回默认值:\n实际=%+v\n期望=%+v", got, want)
	}
}

// TestInteractiveTTYReadsAnswers 断言 TTY 下逐项读取回答，空行取默认值。
// TestInteractiveTTYReadsAnswers asserts TTY mode reads one answer per prompt and empty lines keep defaults.
func TestInteractiveTTYReadsAnswers(t *testing.T) {
	// 询问顺序：name、module、db、grpc、license；i18n、Htmx 扩展、Redis、Asynq、Htmx、Tailwind
	// 与 Alpine 是内建能力，不再提问。
	// Prompt order: name, module, db, grpc, license; i18n, Htmx extensions, Redis, Asynq, Htmx,
	// Tailwind and Alpine are built in and never asked.
	answers := strings.Join([]string{
		"shop-app", // 项目名 | project name
		"",         // 模块路径 → 跟随新项目名 | module follows the new name
		"postgres", // 数据库 | database
		"n",        // grpc
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
	if got.GRPC || !got.I18n || !got.Tailwind {
		t.Errorf("特性开关解析错误: %+v", got)
	}
	if !slices.Equal(got.HtmxExts, scaffold.DefaultExtensions()) {
		t.Errorf("Htmx 扩展应为全部安全扩展: %v", got.HtmxExts)
	}
	prompts := out.String()
	for _, unwanted := range []string{"启用 Redis", "启用 Asynq"} {
		if strings.Contains(prompts, unwanted) {
			t.Errorf("Redis 与 Asynq 已内建，问答不应再问 %q：\n%s", unwanted, prompts)
		}
	}
	if !strings.Contains(prompts, " | ") {
		t.Errorf("提示文案应为中英同步（含 \" | \"）: %q", prompts)
	}
}

// TestGeneratedGovernanceDocsStateConstraints 断言生成的治理文档覆盖硬性约束。
// TestGeneratedGovernanceDocsStateConstraints asserts the generated governance docs cover the hard constraints.
// TestGeneratedDocsCoverFrameworkGuides keeps the bundle complete when a new guide is added:
// every public guide except the framework release manual and the read-only requirement baseline
// must appear in requiredFiles (developer_guidelines.md is generated separately but still listed). |
// TestGeneratedDocsCoverFrameworkGuides 在新增指南时保持文档包完整：除框架发布手册与只读需求基线外，
// 每份公开指南都必须出现在 requiredFiles 中（developer_guidelines.md 由独立模板生成，但也在清单内）。
func TestGeneratedDocsCoverFrameworkGuides(t *testing.T) {
	entries, err := frameworkdocs.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded docs: %v", err)
	}
	excluded := map[string]bool{
		"releasing.md": true,
		"需求设计说明书.md":   true,
	}
	for _, entry := range entries {
		nameInDocs := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(nameInDocs, ".md") || excluded[nameInDocs] {
			continue
		}
		rel := "docs/" + nameInDocs
		if !slices.Contains(requiredFiles, rel) {
			t.Errorf("framework guide %s is not shipped to generated projects", rel)
		}
	}
}

// TestGeneratedDocsMatchFrameworkSources proves the generated project receives byte-identical
// copies of the framework guides rather than stale duplicates, and that the index links the new
// attachment documentation. | TestGeneratedDocsMatchFrameworkSources 证明生成项目拿到的是逐字节相同的
// 框架指南，而不是陈旧的副本，并证明索引链接到新的附件文档。
func TestGeneratedDocsMatchFrameworkSources(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("docs-app"))
	for _, rel := range requiredFiles {
		if !strings.HasPrefix(rel, "docs/") || !strings.HasSuffix(rel, ".md") ||
			rel == "docs/README.md" || rel == "docs/developer_guidelines.md" {
			continue
		}
		want, err := frameworkdocs.FS.ReadFile(strings.TrimPrefix(rel, "docs/"))
		if err != nil {
			t.Fatalf("framework doc %s is not embedded: %v", rel, err)
		}
		if got := pr.read(rel); got != string(want) {
			t.Errorf("generated %s drifted from the framework source", rel)
		}
	}
	if got := pr.read("docs/realtime-transports.md"); !strings.Contains(got, `{{ asset "/static/js/yd-realtime.js" }}`) {
		t.Error("generated doc lost the runtime template example")
	}
	index := pr.read("docs/README.md")
	for _, want := range []string{"attachments.md", "message-center.md", "cache.md", "redis-asynq.md"} {
		if !strings.Contains(index, want) {
			t.Errorf("docs/README.md is missing %q", want)
		}
	}
}

func TestGeneratedGovernanceDocsStateConstraints(t *testing.T) {
	o := scaffold.DefaultOptions("gov-app")
	pr, _ := generateMem(t, o)

	claude := pr.read("CLAUDE.md")
	for _, want := range []string{
		"gov-app",
		"github.com/yun-dev/gov-app",
		"Co-Authored-By: Eric Shi <shibingli@sina.com>",
		"中英文同步",
		"errs",
		"fmt.Errorf",
		"ydlog",
		"json",
		"base64",
		"YD_",
		// Stack versions (the template bolds them; the mandated version literals are checked verbatim). | 技术栈版本（模板以粗体书写，逐字校验需求规定的版本号）。
		"Go **1.27.3**",
		"Htmx **4.0.0**",
		"Tailwind CSS **v4**",
		"redis",
		"asynq",
		"cron",
		// 鉴权链路：多租户边界、能力守卫与会话顺序必须在助手的全局上下文里写明。
		// The authentication chain: the tenant boundary, the capability guards and the session order must be spelled out for the assistant.
		"auth.RequirePermission",
		"middleware.TenantID",
		"auth.ErrNotFound",
		"middleware.Promote",
		"auth.LoginEncryptedHeader",
		"SeedPasswordNotice",
		"web/i18n",
		"docs/attachments.md",
		"docs/node-identity.md",
		"docs/redis-asynq.md",
	} {
		if !strings.Contains(claude, want) {
			t.Errorf("CLAUDE.md 缺少 %q", want)
		}
	}
	lower := strings.ToLower(claude)
	if !strings.Contains(lower, "claude") || !strings.Contains(lower, "anthropic") {
		t.Error("CLAUDE.md must spell out that commit messages never mention Claude or Anthropic | CLAUDE.md 必须写明禁止提交信息出现 Claude/Anthropic 相关内容")
	}

	agents := pr.read("AGENTS.md")
	for _, want := range []string{
		"Test-and-Fix", "log", "json", "base64", "errs", "YD_", "Co-Authored-By: Eric Shi <shibingli@sina.com>",
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
	o.GRPC, o.I18n = true, true
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

// Time and size bounds of the live-server checks | 真实服务验证用到的时间与体积上限
const (
	// startupTimeout 是等待生成的应用就绪的上限：首次启动要建连、装配模板并绑定端口。
	// startupTimeout bounds the wait for the generated application to become ready: the first start has to connect,
	// assemble its templates and bind the port.
	startupTimeout = 60 * time.Second
	// httpTimeout bounds one ordinary request. | httpTimeout 是单条普通请求的上限。
	// httpTimeout is the probe timeout. The first HTML render in a generated project runs with
	// render.Dev=true and therefore compiles the whole template tree; on a cold Windows filesystem that
	// can take tens of seconds now that the gallery and the advanced components are part of the tree,
	// so the timeout leaves headroom for the compile instead of failing a healthy app. | httpTimeout
	// 是探针超时。生成项目首次 HTML 渲染运行在 render.Dev=true 下，需要编译整棵模板树；组件示例页与
	// 进阶组件加入后，冷启动的 Windows 文件系统上可能达到数十秒，因此留出编译余量，避免误判健康的应用。
	httpTimeout = 60 * time.Second
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

	projDir, base := startProbeApp(t, "yd-serve-probe")
	identityFile := filepath.Join(projDir, "data", "node-id")
	if raw, err := os.ReadFile(identityFile); err != nil {
		t.Errorf("生成的应用没有持久化节点身份: %v", err)
	} else if err := nodeid.Validate(strings.TrimSpace(string(raw))); err != nil {
		t.Errorf("生成的应用写入了非法节点身份 %q: %v", raw, err)
	}

	// /users 落在受保护组里，/ 在公开组按登录状态分流；探针先登录，以便后续受保护端点带着那枚 cookie
	// （中间件 Promote 会把它提升为 Authorization，浏览器与 API 客户端因此走同一条校验路径）。
	// /users lives in the protected group while / dispatches on the session in the public group; the probe signs in so
	// the protected requests that follow carry that cookie (middleware.Promote lifts it into Authorization, which is
	// how browsers and API clients end up sharing one verification path).
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
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/account" {
			t.Fatalf("GET / 状态码 = %d, Location = %q, 期望 303 /account（无数据库时无消息中心）\n%s",
				resp.StatusCode, resp.Header.Get("Location"), body)
		}
		resp, body = getText(t, client, base+"/account", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /account 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		for _, want := range []string{
			"<html", `id="yd-workspace"`, "/static/css/yd.css", `data-yd-page="account"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("GET /account 缺少 %q:\n%s", want, body)
			}
		}
		if strings.Contains(body, `hx-ws:connect="/mgr/ws/metrics"`) {
			t.Errorf("GET /account 不得建立管理指标连接:\n%s", body)
		}

		// 管理概览在 /mgr：指标卡片仍由同一条 WebSocket 供给，且不再走 HTTP 轮询。
		// The management overview lives at /mgr: its metric cards still share one WebSocket and no HTTP polling remains.
		resp, body = getText(t, client, base+"/mgr", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /mgr 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		for _, want := range []string{
			`data-yd-page="home"`,
			`id="yd-live-metrics"`, `hx-ws:connect="/mgr/ws/metrics"`, `id="yd-middleware-metrics"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("GET /mgr 缺少 %q:\n%s", want, body)
			}
		}
		if strings.Contains(body, `hx-trigger="every`) {
			t.Errorf("GET /mgr 仍使用定时轮询，指标必须由 WebSocket 推送:\n%s", body)
		}
		// 组件示例页是门户内的活文档：组件层新增控件后，这一页必须仍然能整页渲染。
		// The component gallery is living documentation inside the portal: after a new control joins the kit, this
		// page must still render as a whole page.
		resp, body = getText(t, client, base+"/ui-kit", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /ui-kit 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		for _, want := range []string{
			`data-yd-page="ui-kit"`,
			`id="yd-ui-kit-form"`,
			`id="yd-kit-chart"`,
			`id="yd-kit-modal"`,
			`class="yd-chart-card__plot"`,
			`id="yd-ui-kit-advanced-form"`,
			`id="yd-ui-kit-advanced-display"`,
			`id="yd-ui-kit-advanced-misc"`,
			`id="yd-kit-calendar"`,
			`id="yd-kit-viewer"`,
			`data-yd-editor-icons`,
			`id="yd-page-assets"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("GET /ui-kit 缺少 %q:\n%s", want, body)
			}
		}
	})

	t.Run("指标 WebSocket 推送", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), sseTimeout)
		defer cancel()

		conn, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/mgr/ws/metrics", websocket.DialOptions{
			Subprotocols: []string{"htmx"},
			Headers:      http.Header{"Authorization": []string{"Bearer " + token}},
		})
		if err != nil {
			t.Fatalf("指标 WebSocket 握手失败: %v\n指标 WebSocket handshake failed: %v", err, err)
		}
		defer func() { _ = conn.CloseNow() }()

		// 连接建立后服务端立即推一帧，两张卡片必须由同一条连接、同一个片段一起送达。
		// The server pushes one frame immediately after the handshake; one connection and one fragment must carry
		// both cards together.
		if err := conn.SetReadDeadline(time.Now().Add(sseTimeout)); err != nil {
			t.Fatalf("设置读超时失败: %v", err)
		}
		op, frame, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("读取指标推送失败: %v", err)
		}
		if op != websocket.TextMessage {
			t.Fatalf("指标推送帧类型 = %v, 期望文本帧", op)
		}
		for _, want := range []string{
			"<hx-partial",
			`hx-target="#yd-live-metrics"`,
			`hx-swap="innerMorph"`,
			`id="yd-metrics"`,
			`class="yd-metrics-grid"`,
			`class="yd-gauge`,
			`id="yd-middleware-metrics"`,
			`data-yd-middleware-name="recover"`,
		} {
			if !strings.Contains(string(frame), want) {
				t.Errorf("指标推送帧缺少 %q:\n%s", want, frame)
			}
		}

		// 第二帧证明推送不是一次性的：按 5 秒节拍持续送达，且仍然同时包含两张卡片。
		// A second frame proves the push is not one-shot: it keeps arriving on the five-second cadence and still
		// carries both cards.
		if err := conn.SetReadDeadline(time.Now().Add(7 * time.Second)); err != nil {
			t.Fatalf("设置第二帧读超时失败: %v", err)
		}
		op, frame, err = conn.ReadMessage()
		if err != nil {
			t.Fatalf("读取第二帧指标推送失败: %v", err)
		}
		if op != websocket.TextMessage {
			t.Fatalf("第二帧类型 = %v, 期望文本帧", op)
		}
		for _, want := range []string{`id="yd-metrics"`, `id="yd-middleware-metrics"`} {
			if !strings.Contains(string(frame), want) {
				t.Errorf("第二帧缺少 %q:\n%s", want, frame)
			}
		}
	})

	t.Run("系统资源片段", func(t *testing.T) {
		resp, body := getText(t, client, base+"/mgr/metrics", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /metrics 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		// 改为经统一指标抽象取数后，呈现必须逐字保持原样。 | After routing through the unified metrics
		// abstraction the rendering must stay byte-for-byte equivalent.
		for _, want := range []string{
			`class="yd-metrics-grid"`,
			`class="yd-gauge`,
			"CPU cores",
			"CPU usage",
			"Total memory",
			"Process CPU",
			"Process memory",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("GET /metrics 缺少 %q:\n%s", want, body)
			}
		}
	})

	t.Run("中间件运行时", func(t *testing.T) {
		resp, body := getText(t, client, base+"/mgr/metrics/middleware", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /metrics/middleware 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		for _, want := range []string{
			`data-yd-middleware`,
			`data-yd-middleware-name="recover"`,
			`data-yd-middleware-name="access-log"`,
			`data-yd-middleware-name="security"`,
			`data-yd-middleware-name="i18n"`,
			`data-yd-middleware-name="promote"`,
			`data-yd-middleware-name="auth"`,
			`data-yd-middleware-name="authz"`,
			`data-yd-middleware-name="tenant"`,
			">active<",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("GET /metrics/middleware 缺少 %q:\n%s", want, body)
			}
		}
	})

	t.Run("htmx 局部请求只返回片段", func(t *testing.T) {
		resp, body := getText(t, client, base+"/mgr", map[string]string{"HX-Request": "true"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("HX-Request GET /mgr 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		if strings.Contains(body, "<html") {
			t.Errorf("HX-Request 请求不应返回整页 HTML:\n%s", body)
		}
		if !strings.Contains(body, `data-yd-page="home"`) {
			t.Errorf("HX-Request 响应不是管理概览片段:\n%s", body)
		}
	})

	t.Run("OpenAPI 文档与自包含 /docs", func(t *testing.T) {
		resp, doc := getText(t, client, base+"/api/v1/openapi.json", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /openapi.json 状态码 = %d, 期望 200", resp.StatusCode)
		}
		for _, want := range []string{
			`"3.0`,
			`"/avatar/{id}.jpg"`,
			`"/report/{id}-xx.png"`,
			`"/mgr/users"`,
			`"/mgr/messages"`,
			`"/mgr/metrics"`,
			`"/mgr/dictionaries"`,
			`"/mgr/system-dictionaries"`,
			`"/api/portal/v1/dictionaries"`,
			`"/api/v1/dictionaries"`,
			`"/api/v1/system-dictionaries"`,
			`"/api/portal/v1/messages"`,
			`"/api/v1/message-types"`,
			`"/events"`,
			`"/ws"`,
			`"/login"`,
			`"X-Captcha-Token"`,
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
		// 注解里的路径必须与隔离后的路由一致：后台管理只剩 /mgr 与 /api/v1，成员 API 只在 /api/portal/v1。
		// The annotation paths must match the split routes: management only under /mgr and /api/v1, member APIs only
		// under /api/portal/v1.
		for _, unwanted := range []string{
			`"/users":`,
			`"/messages/admin":`,
			`"/message-settings":`,
			`"/api/v1/messages/conversations":`,
			`"/ws/metrics":`,
			`"/docs":`,
			`"/openapi.json":`,
		} {
			if strings.Contains(doc, unwanted) {
				t.Errorf("GET /openapi.json 仍含旧路径 %q:\n%s", unwanted, doc)
			}
		}

		assertAnnotatedOperations(t, doc)
		assertOpenAPIRefsResolve(t, doc)

		respDocs, docs := getText(t, client, base+"/mgr/docs", nil)
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
		resp, body := getText(t, client, base+"/static/js/vendored/htmx.min.js", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /static/js/vendored/htmx.min.js 状态码 = %d, 期望 200", resp.StatusCode)
		}
		if len(body) < 1000 || !strings.Contains(body, "htmx") {
			t.Errorf("htmx.min.js 内容异常（长度 %d 字节）", len(body))
		}
		// 生成项目的中间件链必须自带静态缓存策略；开发期是 no-cache，并始终声明 Accept-Encoding。 |
		// The generated middleware chain must carry the static cache policy; development revalidates and always
		// declares Accept-Encoding.
		if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("静态资源 Cache-Control = %q, 期望 no-cache", got)
		}
		if got := strings.Join(resp.Header.Values("Vary"), ","); !strings.Contains(got, "Accept-Encoding") {
			t.Errorf("静态资源 Vary = %q, 期望包含 Accept-Encoding", got)
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
			// htmx 4 swaps unnamed messages automatically; a named event would never reach the clock.
			// | htmx 4 自动交换无名消息；命名事件永远到不了时钟。
			if strings.Contains(frame, "event: yd.events") {
				t.Errorf("SSE 帧不应带事件名（htmx 4 只交换无名消息）:\n%s", frame)
			}
			for _, want := range []string{"data: ", "<hx-partial", `hx-target="#yd-clock"`, "<time"} {
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
		conn, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/ws", websocket.DialOptions{
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
		if op != websocket.TextMessage {
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
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"BODY":"ping"}`)); err != nil {
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
			t.Error("WebSocket did not echo the htmx envelope's BODY back | WebSocket 未把 htmx 信封的 BODY 回显回来")
		}
	})

	t.Run("表单校验与列表闭环", func(t *testing.T) {
		// 校验失败（htmx 请求）：422 + 错误片段，且片段必须只是错误槽位的内容，不能是整页。
		// 登录名与邮箱的校验发生在口令解密之前，因此这两条用例可以只发普通表单字段。
		// A validation failure on an htmx request: 422 plus the error fragment, which must be the error slot's content
		// only and never a whole page. The sign-in name and the address are checked before any password is decrypted,
		// which is why these two cases may post plain form fields.
		hx := map[string]string{"HX-Request": "true"}
		resp, body := postForm(t, client, base+"/mgr/users", map[string]string{"username": "", "email": "nobody@example.com"}, hx)
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
		respPlain, bodyPlain := postForm(t, client, base+"/mgr/users", map[string]string{"username": "", "email": "nobody@example.com"}, nil)
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
		respBad, bodyBad := postForm(t, client, base+"/mgr/users", map[string]string{"username": "Alice", "email": "not-an-email"}, hx)
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
		respOK, bodyOK := postFormValues(t, client, base+"/mgr/users", url.Values{
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
		respList, list := getText(t, client, base+"/mgr/users?q=alice", map[string]string{"HX-Request": "true"})
		if respList.StatusCode != http.StatusOK || !strings.Contains(list, "alice@example.com") {
			t.Errorf("GET /users?q=alice 未命中新用户（状态码 %d）:\n%s", respList.StatusCode, list)
		}
		respMiss, miss := getText(t, client, base+"/mgr/users?q=nobody-here", map[string]string{"HX-Request": "true"})
		if respMiss.StatusCode != http.StatusOK {
			t.Errorf("无匹配的筛选状态码 = %d, 期望 200（内容为空状态）", respMiss.StatusCode)
		}
		if !strings.Contains(miss, "no user matches") {
			t.Errorf("无匹配的筛选未返回空状态片段:\n%s", miss)
		}
	})

	t.Run("无数据库时消息中心显式不可用", func(t *testing.T) {
		// 本探针项目以 --db none 生成：导航里不应出现消息入口，两个端点都必须明确报告不可用，而不是假装成功。
		// This probe was generated with --db none: the navigation must carry no message entry and both endpoints must report
		// the centre as unavailable rather than pretending to work.
		resp, page := getText(t, client, base+"/account", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /account 状态码 = %d, 期望 200\n%s", resp.StatusCode, page)
		}
		for _, unwanted := range []string{`href="/messages"`, `href="/friends"`} {
			if strings.Contains(page, unwanted) {
				t.Errorf("无数据库的项目仍在导航里渲染 %q", unwanted)
			}
		}

		resp, page = getText(t, client, base+"/messages", nil)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("GET /messages 状态码 = %d, 期望 503\n%s", resp.StatusCode, page)
		}
		if !strings.Contains(page, "The message centre is unavailable") {
			t.Errorf("不可用页面缺少说明文案:\n%s", page)
		}

		resp, body := getText(t, client, base+"/api/portal/v1/messages", nil)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("GET /api/portal/v1/messages 状态码 = %d, 期望 503\n%s", resp.StatusCode, body)
		}
		if !strings.Contains(body, "message centre is unavailable") {
			t.Errorf("不可用 API 缺少说明文案:\n%s", body)
		}
	})
}

// 探针使用的种子凭据与契约常量：与生成项目的 internal/meta 同源，这里各留一份供断言使用，
// assertSeedCredentialsDocumented 会逐字回验一次，因此两边不会各自漂移。
// The seed credentials and contract constants the probes use: they share one source with the generated project's
// internal/meta and are kept here for the assertions, while assertSeedCredentialsDocumented verifies them verbatim so
// neither side can drift away on its own.
const (
	// probeTenant is the default tenant's key (the sign-in form's tenant field). | probeTenant 是默认租户的标识（登录表单的租户字段）。
	probeTenant = "default"
	// probeAdmin is the default tenant's administrator sign-in name. | probeAdmin 是默认租户的管理员登录名。
	probeAdmin = "admin"
	// probeAdminPassword 是默认租户管理员的初始口令：探针应用没有配置 seed.admin_password 也没有声明非 development 环境，
	// 因此种子写入走的是开发回退，用的正是那一枚文档化的开发口令（internal/meta.DevSeedAdminPassword）。
	// probeAdminPassword is the default administrator's initial password: the probe application configures no
	// seed.admin_password and declares no non-development environment, so the seed takes the development fallback and uses
	// exactly the documented development password (internal/meta.DevSeedAdminPassword).
	probeAdminPassword = "admin123"
	// probeOtherTenant 是种子**不会**创建的租户标识，跨租户用例用它模拟「另一个租户」。
	// probeOtherTenant is a tenant slug the seed does **not** create; the cross-tenant cases use it as "the other tenant".
	probeOtherTenant = "acme"
	// staleSeedPassword 是曾经硬编码在 internal/meta 里的第二枚口令：它必须彻底消失，任何文件里再出现即为回归。
	// staleSeedPassword is the second password that used to be hard-coded in internal/meta: it must be gone for good, and
	// its reappearance anywhere is a regression.
	staleSeedPassword = "Acme@123456"
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

// assertSeedCredentialsDocumented 逐字回验种子凭据与首登改密提示确实落在生成项目的源码、配置与 README 里，
// 并回验**没有任何口令被硬编码**：口令只有「配置 / 环境变量」与「那一枚文档化的开发回退」两条来源。
// assertSeedCredentialsDocumented verifies verbatim that the seed credentials and the first-sign-in notice land in the
// generated project's sources, configuration and README, and that **no password is hard-coded**: a password has exactly
// two sources — the configuration or the environment, and the one documented development fallback.
func assertSeedCredentialsDocumented(t *testing.T, projDir string) {
	t.Helper()

	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(projDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", rel, err)
		}
		return string(b)
	}

	metaSrc := read("internal/meta/meta.go")
	for _, want := range []string{
		`SeedTenantSlug = "` + probeTenant + `"`,
		`SeedTenantName = "默认租户 | Default tenant"`,
		`SeedAdminUsername = "` + probeAdmin + `"`,
		`SeedTenantConfigKey = "seed.tenant"`,
		`SeedUsernameConfigKey = "seed.username"`,
		`SeedPasswordConfigKey = "seed.admin_password"`,
		`SeedPasswordEnvKey = EnvPrefix + "SEED_ADMIN_PASSWORD"`,
		`DevSeedAdminPassword = "` + probeAdminPassword + `"`,
		`SeedPasswordNotice = "`,
		`TokenCookie = "` + tokenCookieName + `"`,
	} {
		if !strings.Contains(metaSrc, want) {
			t.Errorf("internal/meta/meta.go 缺少 %q", want)
		}
	}
	// 第二个种子租户必须彻底消失：它既不再被写入，也不该留下任何常量或文档。
	// The second seeded tenant must be gone for good: it is no longer written, and no constant or document may mention it.
	for _, banned := range []string{"SeedOpsSlug", "SeedOpsName", "SeedOpsAdminUsername", "SeedOpsAdminEmail"} {
		decl := regexpx.MustCompile(`(?m)(^|[^A-Za-z0-9_])` + banned + `\s*=`)
		if decl.MatchString(metaSrc) {
			t.Errorf("internal/meta/meta.go 仍含第二个种子租户常量 %s", banned)
		}
	}
	// 那两枚硬编码口令常量必须彻底消失：口令只经配置下发，开发回退也只有一个名字（DevSeedAdminPassword）。
	// 判定按标识符边界匹配一次赋值，因此 DevSeedAdminPassword 不会被误判，而 SeedAdminPassword = "..." 一定被抓到。
	// The two hard-coded password constants must be gone for good: a password arrives through the configuration only, and
	// the development fallback has exactly one name (DevSeedAdminPassword). The check matches an assignment at an
	// identifier boundary, so DevSeedAdminPassword is never mistaken for one while SeedAdminPassword = "..." always is.
	for _, banned := range []string{"SeedAdminPassword", "SeedOpsAdminPassword"} {
		decl := regexpx.MustCompile(`(?m)(^|[^A-Za-z0-9_])` + banned + `\s*=`)
		if decl.MatchString(metaSrc) {
			t.Errorf("internal/meta/meta.go 仍含硬编码口令常量 %s", banned)
		}
	}

	// 配置与 .env 模板是两个来源的落点：配置文件里的口令键必须是空串，env 模板必须是空的赋值。
	// The configuration file and the .env template are where the two sources land: the key in the file must be an empty
	// string and the template line must be an empty assignment.
	cfg := read("configs/config.yaml")
	for _, want := range []string{"seed:", "tenant:", "username:", "admin_password:"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("configs/config.yaml 缺少 %q", want)
		}
	}
	envTpl := read(".env.template")
	if !strings.Contains(envTpl, "YD_SEED_ADMIN_PASSWORD=") {
		t.Error("the .env.template lacks YD_SEED_ADMIN_PASSWORD= | .env.template 缺少 YD_SEED_ADMIN_PASSWORD=")
	}

	// README 是默认凭据与改密提醒唯一的对外落点：两处来源、开发回退与那句提示都必须在这里出现。
	// The README is the one outward landing place of the default credentials and the password notice: both sources, the
	// development fallback and that very sentence have to show up in it.
	readme := read("README.md")
	for _, want := range []string{
		"seed.admin_password",
		"YD_SEED_ADMIN_PASSWORD",
		"seed.tenant",
		"seed.username",
		probeAdminPassword,
		"SeedPasswordNotice",
		probeTenant,
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README.md 缺少 %q", want)
		}
	}

	// 全项目扫描：那枚已经作废的第二枚口令不得在任何文件里残留。
	// A project-wide scan: the retired second password must not survive in any file.
	assertNoStalePassword(t, projDir)
}

// assertNoStalePassword 断言全项目（含文档与模板）都不含那枚作废的硬编码口令。
// assertNoStalePassword asserts the whole project — docs and templates included — carries none of the retired hard-coded
// passwords.
func assertNoStalePassword(t *testing.T, projDir string) {
	t.Helper()

	err := filepath.WalkDir(projDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), staleSeedPassword) {
			rel, relErr := filepath.Rel(projDir, path)
			if relErr != nil {
				rel = path
			}
			t.Errorf("%s 残留了已作废的硬编码口令 %s", filepath.ToSlash(rel), staleSeedPassword)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s 失败: %v", projDir, err)
	}
}

// assertAuthzCheckPasses 在生成项目上跑一遍 `yd authz check` 的判定：零错误、确有引用、注册表规模相符。
// assertAuthzCheckPasses runs the verdict of `yd authz check` over a generated project: no errors, references actually
// found, and the registry of the expected size.
//
// 这是「角色名与权限名零字面量」这条硬约束的自动化证据：生成项目里的种子数据、路由守卫与模板都只能经常量名、
// 数据键或脚本键引用注册表，写错一个标识就会被它抓住。
// This is the automated evidence for the "no role or capability literals" constraint: the seed data, the route guards and
// the templates of a generated project may only reach the registry through constant names, data keys or script keys, and a
// single mistyped token is caught right here.
func assertAuthzCheckPasses(t *testing.T, fsys afero.Fs, dir, where string) authz.Report {
	t.Helper()

	rep, err := authz.Check(authz.CheckOptions{FS: fsys, Dir: dir})
	if err != nil {
		t.Fatalf("%s: authz.Check 失败: %v", where, err)
	}
	var out strings.Builder
	rep.Fprint(&out)
	if !rep.OK() {
		t.Fatalf("%s: yd authz check 有 %d 个错误:\n%s", where, rep.Errors(), out.String())
	}
	if rep.Refs == 0 {
		t.Errorf("%s: 扫描到 0 处能力/角色引用，说明守卫与模板根本没有接上注册表:\n%s", where, out.String())
	}
	if rep.Perms != len(authz.Permissions()) || rep.Roles != len(authz.Roles()) {
		t.Errorf("%s: 注册表规模 = %d 项能力 / %d 个角色, 期望 %d / %d",
			where, rep.Perms, rep.Roles, len(authz.Permissions()), len(authz.Roles()))
	}
	t.Logf("%s: yd authz check 通过（%d 个文件、%d 处引用、%d 项能力、%d 个角色）",
		where, rep.Files, rep.Refs, rep.Perms, rep.Roles)
	return rep
}

// assertNoUserData 断言响应里一行用户数据都没有：任何一条用户记录都会带上邮箱，因此域名出现即为泄漏。
// assertNoUserData asserts the response carries not a single user record: every one of them comes with an address, so the
// presence of the domain is the leak.
func assertNoUserData(t *testing.T, where, body string) {
	t.Helper()

	for _, leak := range []string{"@example.com", probeAdmin + "@"} {
		if strings.Contains(body, leak) {
			t.Errorf("%s 泄漏了用户数据 %q:\n%s", where, leak, body)
		}
	}
}

// authzEnvelopeOf 提取渲染页面下发给浏览器的内联权限信封。
// authzEnvelopeOf extracts the inline authority payload a rendered page hands to the browser.
//
// 它是「前端守卫真的接上了」的最终证据：模板与脚本都只是消费方，真正决定按钮显隐的是服务端写进这个元素的那份载荷。
// It is the final proof that the front-end guard is really wired: templates and scripts are merely consumers, and what
// decides whether a button appears is the payload the server writes into this very element.
func authzEnvelopeOf(t *testing.T, where, page string) authz.Envelope {
	t.Helper()

	open := `<script id="` + authz.AuthzScriptID + `" type="application/json">`
	start := strings.Index(page, open)
	if start < 0 {
		t.Fatalf("%s: the page carries no authority payload (%s), so the front end would deny every capability | %s：页面没有下发权限信封（%s），前端会因此拒绝一切能力",
			where, open, where, open)
	}
	rest := page[start+len(open):]
	end := strings.Index(rest, "</script>")
	if end < 0 {
		t.Fatalf("%s: the authority payload element is never closed | %s：权限信封元素没有闭合", where, where)
	}
	var env authz.Envelope
	if err := json.Unmarshal([]byte(rest[:end]), &env); err != nil {
		t.Fatalf("%s: the authority payload is not valid JSON (%v) | %s：权限信封不是合法 JSON（%v）", where, err, where, err)
	}
	return env
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
	// 真实落盘的产物同样要过 `yd authz check`：它在磁盘上、模板已渲染，与 `yd new` 交付给使用者的那份完全一致。
	// The artefact as it lands on disk passes `yd authz check` too: it is rendered, on disk, and identical to what `yd new`
	// hands to its user.
	assertAuthzCheckPasses(t, afero.NewOsFs(), projDir, "磁盘生成项目")

	t.Run("未登录被挡在受保护端点之外", func(t *testing.T) {
		anon := newProbeClient(t, httpTimeout)

		// 根路由按登录状态分流：匿名访客被送到登录页，页面里没有任何用户数据。
		// The root route branches on the session: an anonymous visitor is sent to sign-in and no user data is rendered.
		resp, body := getText(t, anon, base+"/", nil)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
			t.Fatalf("未登录 GET / 状态码 = %d, Location = %q, 期望 303 /login\n%s",
				resp.StatusCode, resp.Header.Get("Location"), body)
		}

		// htmx 请求得到 HX-Redirect 而不是公开片段。
		// An htmx request gets HX-Redirect instead of a public fragment.
		respHX, bodyHX := getText(t, anon, base+"/", map[string]string{"HX-Request": "true"})
		if respHX.StatusCode != http.StatusOK || respHX.Header.Get("HX-Redirect") != "/login" {
			t.Fatalf("未登录 HX-Request GET / 状态码 = %d, HX-Redirect = %q, 期望 200 /login\n%s",
				respHX.StatusCode, respHX.Header.Get("HX-Redirect"), bodyHX)
		}

		// 个人中心仍然是受保护端点：匿名请求被送到登录页。
		// The personal centre remains protected: an anonymous request is sent to sign-in.
		respAccount, bodyAccount := getText(t, anon, base+"/account", nil)
		if respAccount.StatusCode != http.StatusSeeOther || respAccount.Header.Get("Location") != "/login" {
			t.Errorf("未登录 GET /account 状态码 = %d, Location = %q, 期望 303 /login\n%s",
				respAccount.StatusCode, respAccount.Header.Get("Location"), bodyAccount)
		}

		// 用户列表对匿名请求同样只有 401，一行用户都不给。
		// The user list answers an anonymous request with the same 401 and not one row.
		respUsers, users := getText(t, anon, base+"/mgr/users", map[string]string{"HX-Request": "true"})
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

		// 验证码默认关闭：路由不挂载，登录页也不渲染组件与 SDK。 |
		// Captcha is off by default: the routes are not mounted and the sign-in page renders neither the widget
		// nor the SDK.
		respCaptcha, _ := postJSON(t, anon, base+"/captcha/challenge", `{"action":"login"}`)
		if respCaptcha.StatusCode != http.StatusNotFound {
			t.Errorf("默认关闭时 POST /captcha/challenge 状态码 = %d, 期望 404", respCaptcha.StatusCode)
		}
		if strings.Contains(login, "data-yd-captcha") {
			t.Errorf("默认关闭时登录页不应渲染验证码组件:\n%s", login)
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
			t.Error("a refused sign-in must not write a session cookie | 被拒绝的登录不应写下会话 cookie")
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
		if loc := resp.Header.Get("Location"); loc != "/account" {
			t.Errorf("登录成功后的 Location = %q, 期望 /account（无数据库时无消息中心）", loc)
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
			t.Error("the session cookie must be HttpOnly (a script must not be able to take the session) | 会话 cookie 必须是 HttpOnly（脚本拿不走会话）")
		}
		if session.Value == "" {
			t.Error("the session cookie carries an empty token | 会话 cookie 的令牌为空")
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
		if resp.StatusCode != http.StatusOK || resp.Header.Get("HX-Redirect") != "/account" {
			t.Fatalf("已登录 HX-Request GET / 状态码 = %d, HX-Redirect = %q, 期望 200 /account（无数据库时无消息中心）\n%s",
				resp.StatusCode, resp.Header.Get("HX-Redirect"), body)
		}
		resp, body = getText(t, browser, base+"/account", map[string]string{"HX-Request": "true"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("已登录 HX-Request GET /account 状态码 = %d, 期望 200\n%s", resp.StatusCode, body)
		}
		if strings.Contains(body, "<html") {
			t.Errorf("HX-Request 请求不应返回整页 HTML:\n%s", body)
		}
		if !strings.Contains(body, `data-yd-page="account"`) {
			t.Errorf("已登录的 HX-Request 响应不是账号页片段:\n%s", body)
		}

		// API 形态：显式 Authorization、没有 cookie，走的是同一条校验路径。
		// The API shape: an explicit Authorization with no cookie, verified along the very same path.
		api := newProbeClient(t, httpTimeout)
		respAPI, bodyAPI := getText(t, api, base+"/mgr/users", map[string]string{
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

		// 种子只创建 default 租户，因此这里先以管理员身份建出第二个租户，再拿它做提权目标。
		// The seed creates only the default tenant, so create a second one as the administrator and use it as the escalation target.
		respNew, newBody := postForm(t, browser, base+"/mgr/tenants", map[string]string{
			"slug": probeOtherTenant,
			"name": "Other tenant",
		}, map[string]string{"HX-Request": "true"})
		if respNew.StatusCode < http.StatusOK || respNew.StatusCode >= http.StatusMultipleChoices {
			t.Fatalf("创建第二个租户状态码 = %d, 期望 2xx\n%s", respNew.StatusCode, newBody)
		}

		// default 的令牌 + 另一个租户的请求头：令牌声明优先，请求头一旦指向别的租户就是提权，一律 403。
		// The default token with the other tenant's header: the token's claim wins and a header naming another tenant is
		// escalation, refused with 403.
		resp, body := getText(t, browser, base+"/mgr/users", map[string]string{
			"HX-Request": "true",
			"X-Tenant":   probeOtherTenant,
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
		respAPI, bodyAPI := getText(t, api, base+"/mgr/users", map[string]string{
			"HX-Request":    "true",
			"Authorization": "Bearer " + token,
			"X-Tenant":      probeOtherTenant,
		})
		if respAPI.StatusCode != http.StatusForbidden {
			t.Fatalf("API 形态跨租户 GET /users 状态码 = %d, 期望 403\n%s", respAPI.StatusCode, bodyAPI)
		}
		assertNoUserData(t, "API 形态跨租户 GET /users", bodyAPI)

		// 反向确认：同一枚令牌指向自己的租户时数据照常返回，因此上面的 403 说的是「租户对不上」而不是「令牌坏了」。
		// The reverse check: the same token naming its own tenant returns data as usual, so the 403 above says "the tenant
		// does not match" and not "the token is broken".
		respOwn, own := getText(t, browser, base+"/mgr/users", map[string]string{
			"HX-Request": "true",
			"X-Tenant":   probeTenant,
		})
		if respOwn.StatusCode != http.StatusOK || !strings.Contains(own, probeAdmin+"@example.com") {
			t.Errorf("令牌指向自己租户时未返回数据（状态码 %d）:\n%s", respOwn.StatusCode, own)
		}
	})

	t.Run("权限信封按主体下发且守卫值取自注册表", func(t *testing.T) {
		// 匿名访客：名字表完整、两个列表都为空——yd-authz.js 因此一个能力都不授予（失败关闭），
		// 这正是「没有会话就不显示任何按钮」在浏览器侧的实现方式。
		// The anonymous visitor: the name maps travel in full while both lists stay empty, so yd-authz.js grants nothing
		// (fail closed) — which is exactly how "no session, no button" is implemented in the browser.
		anon := newProbeClient(t, httpTimeout)
		respLogin, loginPage := getText(t, anon, base+"/login", nil)
		if respLogin.StatusCode != http.StatusOK {
			t.Fatalf("the anonymous GET /login answered %d, want 200 | 匿名 GET /login 状态码 = %d, 期望 200\n%s",
				respLogin.StatusCode, respLogin.StatusCode, loginPage)
		}
		anonEnv := authzEnvelopeOf(t, "the anonymous sign-in page | 匿名登录页", loginPage)
		if len(anonEnv.Granted) != 0 || len(anonEnv.Mine) != 0 {
			t.Errorf("the anonymous subject must hold nothing, got %d capabilities and %d roles | 匿名主体的授予列表应为空, 实得 %d 项能力 / %d 个角色",
				len(anonEnv.Granted), len(anonEnv.Mine), len(anonEnv.Granted), len(anonEnv.Mine))
		}
		// 名字表仍要完整下发：模板与脚本可能需要「称呼」一个自己并不持有的能力（例如渲染一条提示）。
		// The name maps still travel in full: a template or a script may need to *name* a capability it does not hold.
		if len(anonEnv.Perms) != len(authz.Permissions()) || len(anonEnv.Roles) != len(authz.Roles()) {
			t.Errorf("the anonymous name maps = %d capabilities and %d roles, want %d and %d | 匿名信封的名字表 = %d 项能力 / %d 个角色, 期望 %d / %d",
				len(anonEnv.Perms), len(anonEnv.Roles), len(authz.Permissions()), len(authz.Roles()),
				len(anonEnv.Perms), len(anonEnv.Roles), len(authz.Permissions()), len(authz.Roles()))
		}

		// owner 主体：信封带着 owner 的全部能力与角色，浏览器侧的 yd.can 与 data-yd-perm 才有内容可读。
		// The owner subject: the envelope carries every capability of an owner plus the role, so yd.can and
		// data-yd-perm have something to read in the browser.
		browser := newProbeClient(t, httpTimeout)
		signIn(t, browser, base, probeTenant, probeAdmin, probeAdminPassword)
		respHome, homePage := getText(t, browser, base+"/account", nil)
		if respHome.StatusCode != http.StatusOK || !strings.Contains(homePage, "<html") {
			t.Fatalf("the signed-in GET /account answered %d, want a full 200 page | 已登录 GET /account 状态码 = %d, 期望 200 的整页\n%s",
				respHome.StatusCode, respHome.StatusCode, homePage)
		}
		env := authzEnvelopeOf(t, "the signed-in account page | 已登录账号页", homePage)

		wantPerms := make([]string, 0, len(authz.Permissions()))
		for _, p := range authz.Permissions() {
			wantPerms = append(wantPerms, p.Name)
		}
		gotPerms := slices.Clone(env.Granted)
		slices.Sort(wantPerms)
		slices.Sort(gotPerms)
		if !slices.Equal(gotPerms, wantPerms) {
			t.Errorf("the owner grant list = %v, want every registry capability %v | owner 信封的授予列表 = %v, 期望注册表里的全部能力 %v",
				gotPerms, wantPerms, gotPerms, wantPerms)
		}
		if !slices.Equal(env.Mine, []string{authz.RoleOwner}) {
			t.Errorf("the owner role list = %v, want [%s] | owner 信封的角色列表 = %v, 期望 [%s]",
				env.Mine, authz.RoleOwner, env.Mine, authz.RoleOwner)
		}
		jsKey := authz.JSKey(authz.PermUserCreate)
		if token, ok := env.Perms[jsKey]; !ok || token != authz.PermUserCreate {
			t.Errorf("the JavaScript name map holds %s = %q (present = %v), want %q | 信封的 JavaScript 名字表里 %s = %q（存在 = %v）, 期望 %q",
				jsKey, token, ok, authz.PermUserCreate, jsKey, token, ok, authz.PermUserCreate)
		}

		// 用户列表片段：提交按钮的属性值是服务端解析出来的标识，而模板里写的是常量名或数据键，
		// 因此页面上不可能出现「属性写成常量名、守卫永远匹配不上」这种静默失效。
		// The user-list fragment: the submit button's attribute carries the token the server resolved while the template
		// spells a constant name or a data key, so the attribute can never silently hold the wrong spelling and match no
		// capability at all.
		respUsers, usersPage := getText(t, browser, base+"/mgr/users", map[string]string{"HX-Request": "true"})
		if respUsers.StatusCode != http.StatusOK {
			t.Fatalf("the signed-in HX-Request GET /users answered %d, want 200 | 已登录 HX-Request GET /users 状态码 = %d, 期望 200\n%s",
				respUsers.StatusCode, respUsers.StatusCode, usersPage)
		}
		if want := `data-yd-perm="` + authz.PermUserCreate + `"`; !strings.Contains(usersPage, want) {
			t.Errorf("the user-list fragment lacks the declarative guard %s on its create button | 用户列表片段缺少新建按钮的声明式守卫 %s",
				want, want)
		}
		if strings.Contains(usersPage, `data-yd-perm=""`) {
			t.Errorf("the user-list fragment carries an empty data-yd-perm, so a template referenced an unregistered capability | 用户列表片段出现空的 data-yd-perm, 说明模板引用了注册表里没有的能力")
		}
	})

	t.Run("缺少能力的令牌访问高权限端点被拒绝", func(t *testing.T) {
		// 用 demo 管理员建两个账号：member 没有任何能力，auditor 只有读能力。
		// Two accounts created by the demo administrator: member holds nothing and auditor holds read capabilities only.
		admin := newProbeClient(t, httpTimeout)
		adminToken := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
		createProbeUser(t, admin, base, adminToken, "probe-member", "member", "probe-member@example.com")
		createProbeUser(t, admin, base, adminToken, "probe-auditor", "auditor", "probe-auditor@example.com")

		hx := map[string]string{"HX-Request": "true"}

		// member：令牌本身有效（账号页给 200），但用户列表要 user:read → 403。
		// member: the token itself is valid (the account page answers 200) while the user list needs user:read → 403.
		member := newProbeClient(t, httpTimeout)
		signIn(t, member, base, probeTenant, "probe-member", probeUserPassword)
		respHome, home := getText(t, member, base+"/account", nil)
		if respHome.StatusCode != http.StatusOK {
			t.Fatalf("member 访问账号页状态码 = %d, 期望 200（令牌有效）\n%s", respHome.StatusCode, home)
		}
		// 门户默认只有普通用户功能：没有管理能力时连管理入口都不应渲染，管理概览本身也必须 403。
		// The portal defaults to ordinary-user features: without a management capability the entry is not rendered at all,
		// and the management overview itself must answer 403.
		if strings.Contains(home, `href="/mgr"`) || strings.Contains(home, `data-yd-nav="mgr-`) {
			t.Errorf("member 的账号页渲染了管理入口:\n%s", home)
		}
		respMgr, mgrBody := getText(t, member, base+"/mgr", hx)
		if respMgr.StatusCode != http.StatusForbidden {
			t.Fatalf("member GET /mgr 状态码 = %d, 期望 403\n%s", respMgr.StatusCode, mgrBody)
		}

		// 管理员的门户页只多出一个管理入口，页面本身仍是门户片段而不是后台外壳。
		// The administrator's portal page gains exactly one management entry while remaining a portal fragment, not the
		// back-office shell.
		respAdminHome, adminHome := getText(t, admin, base+"/account", nil)
		if respAdminHome.StatusCode != http.StatusOK {
			t.Fatalf("管理员访问账号页状态码 = %d, 期望 200\n%s", respAdminHome.StatusCode, adminHome)
		}
		if !strings.Contains(adminHome, `href="/mgr"`) {
			t.Errorf("管理员的账号页缺少管理入口:\n%s", adminHome)
		}
		if !strings.Contains(adminHome, `data-yd-page="account"`) {
			t.Errorf("管理员 GET /account 不是账号页:\n%s", adminHome)
		}
		respMember, memberBody := getText(t, member, base+"/mgr/users", hx)
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
		respAud, audBody := getText(t, auditor, base+"/mgr/users", hx)
		if respAud.StatusCode != http.StatusOK || !strings.Contains(audBody, "probe-auditor@example.com") {
			t.Fatalf("auditor GET /users 状态码 = %d, 期望 200 且能看到自己\n%s", respAud.StatusCode, audBody)
		}
		respWrite, writeBody := postForm(t, auditor, base+"/mgr/tenants", map[string]string{
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
		respTenants, tenants := getText(t, admin, base+"/mgr/tenants", hx)
		if respTenants.StatusCode != http.StatusOK {
			t.Fatalf("管理员 GET /tenants 状态码 = %d, 期望 200\n%s", respTenants.StatusCode, tenants)
		}
		if strings.Contains(tenants, "escalation-probe") {
			t.Errorf("被拒绝的越权写入居然生效了:\n%s", tenants)
		}

		// 默认租户名以中文开头，首字符必须按 rune 截取；按字节截断会产生非法 UTF-8，徽标因此空白。
		// The default tenant name starts with Chinese, so its initial must be cut by rune; a byte slice yields
		// invalid UTF-8 and leaves the badge blank.
		if !strings.Contains(tenants, ">默</span>") {
			t.Errorf("租户列表的标识徽标未显示首字符「默」:\n%s", tenants)
		}
		if strings.Contains(tenants, "�") {
			t.Errorf("租户列表出现替换字符，说明首字符按字节截断:\n%s", tenants)
		}
	})
}

// TestGeneratedI18nCatalogueCoversEveryTrKey 断言模板用到的 tr 键与两份语言目录的键集合严格一致。
// TestGeneratedI18nCatalogueCoversEveryTrKey asserts the tr keys used by the templates and the key sets of both
// catalogues agree exactly.
//
// 少一个键，页面上出现的就是键名本身；多一个键，则是一句永远不会被读到的文案。
// A missing key makes the page show the key itself, and a stale one is a sentence nobody ever reads.
// TestGeneratedAppRootRespectsMessageRead 断言登录落点不会把没有 message:read 的账号送到 403。
// TestGeneratedAppRootRespectsMessageRead asserts that the signed-in landing route never sends an account without
// message:read to a 403.
//
// 消息中心可用不等于当前账号能读它：自定义角色可能只持有 user:read。此时 `/messages` 由
// authz.Require(PermMessageRead) 守卫，登录与根路由都必须落到 `/account`。 |
// The centre being available does not mean the current account may read it: a custom role may hold only user:read.
// In that case /messages is guarded by authz.Require(PermMessageRead), so both sign-in and the root route must land
// on /account.
func TestGeneratedAppRootRespectsMessageRead(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实启动 | skipped in short mode")
	}

	_, base := startProbeAppDBWith(t, "yd-no-message-read", scaffold.DBSQLite, nil)

	admin := newProbeClient(t, httpTimeout)
	adminToken := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
	createProbeRole(t, admin, base, adminToken, "no-messages", authz.PermUserRead)
	createProbeUser(t, admin, base, adminToken, "probe-no-messages", "no-messages", "probe-no-messages@example.com")

	user := newProbeClient(t, httpTimeout)
	resp, body := postForm(t, user, base+"/login", map[string]string{
		"tenant":   probeTenant,
		"username": "probe-no-messages",
		"password": sm2Cipher(t, user, base, probeUserPassword),
	}, map[string]string{"X-YD-Encrypted": "sm2"})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/account" {
		t.Fatalf("没有 message:read 的账号登录后 Location = %q（状态码 %d），期望 303 /account\n%s",
			resp.Header.Get("Location"), resp.StatusCode, body)
	}

	resp, body = getText(t, user, base+"/", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/account" {
		t.Fatalf("没有 message:read 的账号 GET / 的 Location = %q（状态码 %d），期望 303 /account\n%s",
			resp.Header.Get("Location"), resp.StatusCode, body)
	}
}

// TestGeneratedAttachmentManagement proves the whole generic attachment lifecycle through real HTTP
// requests: a member uploads and downloads a file, deletes an unreferenced one, an administrator
// filters unreferenced rows, deletes with a reason, cleans up in bulk, and the uploader receives the
// notice in the private message-centre channel. | TestGeneratedAttachmentManagement 通过真实 HTTP 请求
// 证明通用附件的完整生命周期：成员上传与下载、删除未引用文件，管理员筛选未引用行、带原因删除与批量清理，
// 上传者在消息中心的私有频道收到通知。
func TestGeneratedAttachmentManagement(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实启动 | skipped in short mode")
	}

	_, base := startProbeAppDBWith(t, "yd-attachment-probe", scaffold.DBSQLite, nil)

	adminClient := newProbeClient(t, httpTimeout)
	adminToken := signIn(t, adminClient, base, probeTenant, probeAdmin, probeAdminPassword)
	createProbeUser(t, adminClient, base, adminToken, "probe-attach-user", "member", "probe-attach-user@example.com")
	createProbeRole(t, adminClient, base, adminToken, "attachment-auditor", authz.PermAttachmentRead)
	createProbeUser(t, adminClient, base, adminToken, "probe-attach-auditor", "attachment-auditor", "probe-attach-auditor@example.com")

	user := newProbeClient(t, httpTimeout)
	userToken := signIn(t, user, base, probeTenant, "probe-attach-user", probeUserPassword)
	auditor := newProbeClient(t, httpTimeout)
	auditorToken := signIn(t, auditor, base, probeTenant, "probe-attach-auditor", probeUserPassword)

	authHeaders := func(token string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + token, "HX-Request": "true"}
	}

	// Upload one pending file and verify the portal page, the API list and the download endpoint. |
	// 上传一个待引用文件，并核验门户页面、API 列表与下载端点。
	resp, body := uploadProbeAttachment(t, user, base, userToken, "first.txt", []byte("first attachment"))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "first.txt") {
		t.Fatalf("门户上传失败（状态码 %d）:\n%s", resp.StatusCode, body)
	}
	items := probeAttachmentItems(t, user, base, userToken, "/api/portal/v1/attachments")
	if len(items) != 1 || items[0].Name != "first.txt" || items[0].Status != "pending" {
		t.Fatalf("API 列表 = %+v", items)
	}
	firstID := items[0].ID
	resp, downloaded := getText(t, user, base+"/attachments/"+firstID+"/download", authHeaders(userToken))
	if resp.StatusCode != http.StatusOK || downloaded != "first attachment" {
		t.Fatalf("下载状态码 %d，正文 %q", resp.StatusCode, downloaded)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, "attachment") {
		t.Fatalf("下载 Content-Disposition = %q", disposition)
	}

	// The management page shows the row, and the member is refused there. | 管理页面能看到该行，成员访问被拒。
	resp, body = getText(t, adminClient, base+"/mgr/attachments", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "first.txt") {
		t.Fatalf("管理员附件页状态码 %d:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, adminClient, base+"/mgr/attachments?referenced=no", authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "first.txt") {
		t.Fatalf("未引用筛选状态码 %d:\n%s", resp.StatusCode, body)
	}
	resp, _ = getText(t, user, base+"/mgr/attachments", authHeaders(userToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("成员访问管理附件页状态码 = %d，期望 403", resp.StatusCode)
	}

	// The auditor holds attachment:read only: it can list and download every file but sees no write
	// control, and the deletion route still refuses it. | 审计员只持有 attachment:read：可以列出与下载
	// 全部文件，但看不到任何写控件，删除路由仍然拒绝。
	resp, body = getText(t, auditor, base+"/mgr/attachments", authHeaders(auditorToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "first.txt") {
		t.Fatalf("审计员附件页状态码 = %d", resp.StatusCode)
	}
	if strings.Contains(body, "Unreferenced only") || strings.Contains(body, "/delete") {
		t.Fatalf("审计员看到了写控件")
	}
	resp, downloaded = getText(t, auditor, base+"/mgr/attachments/"+firstID+"/download", authHeaders(auditorToken))
	if resp.StatusCode != http.StatusOK || downloaded != "first attachment" {
		t.Fatalf("审计员下载状态码 = %d，正文 %q", resp.StatusCode, downloaded)
	}
	resp, _ = postForm(t, auditor, base+"/mgr/attachments/"+firstID+"/delete", map[string]string{"reason": "auditor"}, authHeaders(auditorToken))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("审计员删除状态码 = %d，期望 403", resp.StatusCode)
	}

	// The administrator deletes with a mandatory reason; the uploader then sees the notice channel. |
	// 管理员带必填原因删除；上传者随后看到通知频道。
	resp, body = postForm(t, adminClient, base+"/mgr/attachments/"+firstID+"/delete", map[string]string{"reason": "policy violation"}, authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("管理员删除状态码 = %d:\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, user, base+"/messages", authHeaders(userToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Notices") {
		t.Fatalf("上传者消息页缺少通知频道（状态码 %d）:\n%s", resp.StatusCode, body)
	}
	noticeID := probeNoticeConversation(t, body)
	resp, body = getText(t, user, base+"/messages/"+noticeID, authHeaders(userToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "policy violation") {
		t.Fatalf("通知会话缺少删除原因（状态码 %d）:\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "private notice channel") {
		t.Fatalf("通知会话没有只读提示:\n%s", body)
	}
	items = probeAttachmentItems(t, adminClient, base, adminToken, "/api/v1/attachments?referenced=no")
	if len(items) != 0 {
		t.Fatalf("删除后仍有未引用记录: %+v", items)
	}

	// A member may delete a pending file (a referenced one is covered by the messaging tests); the
	// bulk cleanup removes every unreferenced row and notifies each uploader. |
	// 成员可以删除待引用文件（已引用由消息测试覆盖）；批量清理删除全部未引用记录并通知每个上传者。
	uploadProbeAttachment(t, user, base, userToken, "second.txt", []byte("second"))
	uploadProbeAttachment(t, user, base, userToken, "third.txt", []byte("third"))
	resp, body = postForm(t, adminClient, base+"/mgr/attachments/cleanup", map[string]string{"reason": "retention policy"}, authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("批量清理状态码 = %d:\n%s", resp.StatusCode, body)
	}
	items = probeAttachmentItems(t, adminClient, base, adminToken, "/api/v1/attachments")
	if len(items) != 0 {
		t.Fatalf("批量清理后仍有记录: %+v", items)
	}
	// The cleanup notification reuses the same private channel and carries its own reason. |
	// 清理通知复用同一个私有频道，并携带自己的原因。
	resp, body = getText(t, user, base+"/messages", authHeaders(userToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("清理后消息页状态码 = %d", resp.StatusCode)
	}
	noticeID = probeNoticeConversation(t, body)
	resp, body = getText(t, user, base+"/messages/"+noticeID, authHeaders(userToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "retention policy") {
		t.Fatalf("清理通知缺少原因（状态码 %d）:\n%s", resp.StatusCode, body)
	}
	uploadProbeAttachment(t, user, base, userToken, "fourth.txt", []byte("fourth"))
	items = probeAttachmentItems(t, user, base, userToken, "/api/portal/v1/attachments")
	if len(items) != 1 {
		t.Fatalf("成员重新上传后的列表 = %+v", items)
	}
	resp, body = postForm(t, user, base+"/attachments/"+items[0].ID+"/delete", nil, authHeaders(userToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("成员删除待引用附件状态码 = %d:\n%s", resp.StatusCode, body)
	}
	items = probeAttachmentItems(t, user, base, userToken, "/api/portal/v1/attachments")
	if len(items) != 0 {
		t.Fatalf("成员删除后仍有记录: %+v", items)
	}

	// The JSON surface is the same lifecycle: upload, self-delete and the management cleanup. |
	// JSON 面走同一条生命周期：上传、自助删除与管理端集中清理。
	resp, body = uploadProbeAttachmentTo(t, user, base, userToken, "/api/portal/v1/attachments", "api-upload.txt", []byte("api upload"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("API 上传状态码 = %d: %s", resp.StatusCode, body)
	}
	var uploaded probeAttachmentRecord
	if err := json.Unmarshal([]byte(body), &uploaded); err != nil || uploaded.ID == "" {
		t.Fatalf("解析 API 上传响应失败: %v: %s", err, body)
	}
	if !strings.Contains(uploaded.Download, "/api/portal/v1/attachments/") {
		t.Fatalf("API 上传的下载地址 = %q", uploaded.Download)
	}
	resp, body = postForm(t, user, base+"/api/portal/v1/attachments/"+uploaded.ID+"/delete", nil, authHeaders(userToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("API 自助删除状态码 = %d: %s", resp.StatusCode, body)
	}
	uploadProbeAttachmentTo(t, user, base, userToken, "/api/portal/v1/attachments", "api-cleanup.txt", []byte("api cleanup"))
	resp, body = postJSON(t, adminClient, base+"/api/v1/attachments/cleanup", `{"reason":"api cleanup"}`, authHeaders(adminToken))
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"deleted":1`) {
		t.Fatalf("API 集中清理状态码 = %d: %s", resp.StatusCode, body)
	}

	// The message composer's upload path goes through the same attachment domain; its inline image
	// preview is also authenticated. | 消息撰写区的上传同样经过附件域；其图片内联预览也需要鉴权。
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	resp, body = uploadProbeAttachmentTo(t, user, base, userToken, "/messages/attachments", "message-image.png", png)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `name="attachment_ids"`) {
		t.Fatalf("消息附件上传状态码 = %d: %s", resp.StatusCode, body)
	}
	messageAttachmentPattern := regexpx.MustCompile(`name="attachment_ids" value="([^"]+)"`)
	match := messageAttachmentPattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("消息附件片段没有 attachment_ids: %s", body)
	}
	resp, downloaded = getText(t, user, base+"/messages/attachments/"+match[1]+"?inline=1", authHeaders(userToken))
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("消息图片内联预览状态码 = %d，Content-Type = %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, body = postForm(t, user, base+"/api/portal/v1/attachments/"+match[1]+"/delete", nil, authHeaders(userToken))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("清理消息附件状态码 = %d: %s", resp.StatusCode, body)
	}
}

// probeAttachmentRecord is the subset of the attachment API the probe reads. | probeAttachmentRecord 是探针读取的
// 附件 API 子集。
type probeAttachmentRecord struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	Download string `json:"download_url"`
	SizeText string `json:"size_text"`
}

// uploadProbeAttachment posts one multipart file and returns the response and body. | uploadProbeAttachment 提交一个
// multipart 文件并返回响应与正文。
func uploadProbeAttachment(t *testing.T, client *http.Client, base, token, name string, content []byte) (*http.Response, string) {
	t.Helper()

	return uploadProbeAttachmentTo(t, client, base, token, "/attachments", name, content)
}

// uploadProbeAttachmentTo posts one multipart file to the given endpoint. | uploadProbeAttachmentTo 把 multipart
// 文件提交到指定端点。
func uploadProbeAttachmentTo(t *testing.T, client *http.Client, base, token, path, name string, content []byte) (*http.Response, string) {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("构造 multipart 文件失败: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 文件失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, base+path, &buf)
	if err != nil {
		t.Fatalf("构造上传请求失败: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("HX-Request", "true")
	return doText(t, client, req)
}

// probeAttachmentItems reads one attachment API page. | probeAttachmentItems 读取一页附件 API。
func probeAttachmentItems(t *testing.T, client *http.Client, base, token, path string) []probeAttachmentRecord {
	t.Helper()

	resp, body := getText(t, client, base+path, map[string]string{"Authorization": "Bearer " + token})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("附件 API %s 状态码 = %d:\n%s", path, resp.StatusCode, body)
	}
	var payload struct {
		Items []probeAttachmentRecord `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("解析附件 API %s 失败: %v\n%s", path, err, body)
	}
	return payload.Items
}

// probeNoticeConversation extracts the first conversation id of the message page. | probeNoticeConversation 提取消息页
// 的第一个会话 ID。
func probeNoticeConversation(t *testing.T, body string) string {
	t.Helper()

	pattern := regexpx.MustCompile(`data-yd-conversation-id="([^"]+)"`)
	match := pattern.FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("消息页没有会话链接:\n%s", body)
	}
	return match[1]
}

func TestGeneratedI18nCatalogueCoversEveryTrKey(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("i18n-app"))

	used := map[string]string{}
	// optionKeyPattern covers the select/choice contract: the visible label comes from tr, while a
	// data-driven option carries the same catalogue key in its Key field. The component dictionary
	// keys are equally real references: a filter passes PlaceholderKey/LabelKey instead of calling tr
	// inline. | optionKeyPattern 覆盖选择/选项契约：可见文案由 tr 给出，数据驱动的选项在 Key 字段里
	// 携带同一个目录键。组件字典键同样是真实引用：筛选行用 PlaceholderKey/LabelKey，而不是在模板里
	// 直接调用 tr。
	optionKeyPattern := regexpx.MustCompile(`"(?:Key|LabelKey|PlaceholderKey|HintKey|ErrorKey|SubmitKey|CancelKey|ClearKey|PanelLabelKey|ResetKey|TitleKey|TextKey|DescriptionKey)"\s+"([a-z0-9_.]+)"`)
	for _, rel := range requiredFiles {
		if !strings.HasPrefix(rel, "web/templates/") || !strings.HasSuffix(rel, ".html") {
			continue
		}
		body := pr.read(rel)
		for _, pattern := range []*regexpx.Regexp{trKeyPattern, optionKeyPattern} {
			for _, match := range pattern.FindAllStringSubmatch(body, -1) {
				if _, seen := used[match[1]]; !seen {
					used[match[1]] = rel
				}
			}
		}
	}
	// Dashboard gauge captions are data-driven: the template calls tr with .LabelKey, so the literal key
	// lives in Go. Controller validation messages are the same shape. Scan those references too;
	// otherwise a legitimately used key is reported as stale.
	// | 仪表盘标题是数据驱动的：模板用 .LabelKey 调 tr，字面键名留在 Go 里。控制器校验文案同理。
	// 这里一并扫描这些引用，否则真正被用到的键会被误报为陈旧键。
	goKeyPatterns := []*regexpx.Regexp{
		regexpx.MustCompile(`"(metrics\.[a-z0-9_.]+)"`),
		regexpx.MustCompile(`\btr\([^,\n]+,\s*"([a-z0-9_.]+)"`),
		regexpx.MustCompile(`"Key":\s*"([a-z0-9_.]+)"`),
	}
	for _, rel := range requiredFiles {
		if !strings.HasPrefix(rel, "internal/") || !strings.HasSuffix(rel, ".go") {
			continue
		}
		for _, pattern := range goKeyPatterns {
			for _, match := range pattern.FindAllStringSubmatch(pr.read(rel), -1) {
				if _, seen := used[match[1]]; !seen {
					used[match[1]] = rel
				}
			}
		}
	}
	if len(used) == 0 {
		t.Fatal("no tr key was found in the templates | 模板里没有找到任何 tr 键")
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
var trKeyPattern = regexpx.MustCompile(`\btr\s+"([^"]+)"`)

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

// TestGeneratedSeedConfigOverridesDefaults 验证 seed.tenant 与 seed.username 从配置文件读取并真正落到种子数据；
// 登录页不再显示租户字段，因此这里同时断言这一点，再用显式租户完成一次登录来证明种子租户确实叫 acme。
// TestGeneratedSeedConfigOverridesDefaults verifies that seed.tenant and seed.username are read from the configuration and
// really reach the seeded data. The sign-in page no longer carries a tenant field, which this also asserts; an explicit
// tenant then signs in to prove the seeded tenant is really named acme.
func TestGeneratedSeedConfigOverridesDefaults(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实鉴权验证 | skipped in short mode")
	}

	_, base := startProbeAppWith(t, "yd-seed-config-probe", func(projDir string) {
		path := filepath.Join(projDir, "configs", "config.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", path, err)
		}
		text := string(data)
		for _, repl := range [][2]string{
			{"tenant: default", "tenant: acme"},
			{"username: admin", "username: root"},
		} {
			if !strings.Contains(text, repl[0]) {
				t.Fatalf("configs/config.yaml 缺少 %q:\n%s", repl[0], text)
			}
			text = strings.Replace(text, repl[0], repl[1], 1)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", path, err)
		}
	})

	anon := newProbeClient(t, httpTimeout)
	resp, page := getText(t, anon, base+"/login", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login 状态码 = %d, 期望 200\n%s", resp.StatusCode, page)
	}
	if strings.Contains(page, `name="tenant"`) {
		t.Errorf("登录页不应再显示租户字段:\n%s", page)
	}
	if strings.Contains(page, `acme`) {
		t.Errorf("登录页不应泄露默认租户标识:\n%s", page)
	}

	browser := newProbeClient(t, httpTimeout)
	signIn(t, browser, base, "acme", "root", probeAdminPassword)
}

// startProbeApp 生成项目、编译它并在随机端口上启动，返回项目目录与基地址。
// startProbeApp generates the project, compiles it and starts it on a random free port, returning the project directory
// and the base URL.
func startProbeApp(t *testing.T, name string) (string, string) {
	return startProbeAppWith(t, name, nil)
}

// startProbeAppWith 与 startProbeApp 相同，但在编译前给调用方一次修改生成项目的机会（例如改写配置）。
// startProbeAppWith is startProbeApp with one chance for the caller to edit the generated project before it is compiled
// (rewriting the configuration, for instance).
func startProbeAppWith(t *testing.T, name string, edit func(projDir string)) (string, string) {
	t.Helper()

	return startProbeAppDBWith(t, name, scaffold.DBNone, edit)
}

// tempProbeDir creates the probe directory and removes it with retries. Windows keeps a
// just-exited executable image locked for a short while, and t.TempDir's single-shot
// RemoveAll turns that transient lock into a spurious test failure. The helper reports
// how long the release took, so a real leak stays visible instead of being hidden. |
// tempProbeDir 创建探针目录并在结束时带重试删除。Windows 会在进程刚退出后短暂锁定可执行映像，
// t.TempDir 的一次性 RemoveAll 会把这种短暂锁定误报为测试失败。辅助函数记录释放耗时，真正的
// 泄漏仍然可见而不是被掩盖。
func tempProbeDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "yd-probe-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() {
		start := time.Now()
		deadline := start.Add(15 * time.Second)
		for {
			removeErr := os.RemoveAll(dir)
			if removeErr == nil {
				if waited := time.Since(start); waited > 200*time.Millisecond {
					t.Logf("临时目录在 %s 后释放: %s", waited.Round(100*time.Millisecond), dir)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Logf("临时目录清理失败（可能是外部程序仍持有文件）: %v", removeErr)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return dir
}

// startProbeAppDBWith 与 startProbeAppWith 相同，但由调用方选择数据库驱动；消息中心的持久化与种子数据需要真实数据库。
// startProbeAppDBWith is startProbeAppWith with the database driver chosen by the caller; the message centre's
// persistence and seed data need a real database.
func startProbeAppDBWith(t *testing.T, name, db string, edit func(projDir string)) (string, string) {
	t.Helper()

	projDir, base, stop := startProbeAppDBWithStop(t, name, db, edit)
	t.Cleanup(stop)
	return projDir, base
}

// startProbeAppDBWithStop 与 startProbeAppDBWith 相同，但把停止函数交给调用方；升级用例需要先停止进程再改库。
// startProbeAppDBWithStop is startProbeAppDBWith with the stop function returned to the caller; upgrade cases stop the
// process before changing the database.
func startProbeAppDBWithStop(t *testing.T, name, db string, edit func(projDir string)) (string, string, func()) {
	t.Helper()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("未找到 go 工具链，跳过端到端验证: %v", err)
	}

	dir := tempProbeDir(t)
	o := scaffold.DefaultOptions(name)
	// 与端到端验证无关的依赖一律不引入：模板、路由与鉴权链路都不需要数据库。
	// Anything unrelated to the end-to-end checks is left out: templates, routes and the authentication chain need no database.
	o.DB = db
	o.LocalReplace = filepath.ToSlash(repoRoot(t))
	if _, err := scaffold.Generate(context.Background(), o, afs.NewOS(dir), nil); err != nil {
		t.Fatalf("真实生成失败: %v", err)
	}

	projDir := filepath.Join(dir, o.Name)
	if edit != nil {
		edit(projDir)
	}
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
	base, stop := startGeneratedAppWithStop(t, bin, projDir)
	return projDir, base, stop
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
	// Each client owns its connection pool. Sharing http.DefaultTransport lets an idle keep-alive
	// connection left over from an earlier test be reused after the server closed it, which shows up
	// as a request that never reaches the access log and times out. | 每个客户端独占连接池。共用
	// http.DefaultTransport 会复用先前测试留下、已被服务端关闭的空闲长连接，表现为请求从未到达
	// 访问日志并最终超时。
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Timeout:   timeout,
		Jar:       jar,
		Transport: langTransport{base: transport},
		// 不跟随重定向：303 与它的 Location 本身就是被测契约的一部分。
		// Redirects are never followed: the 303 and its Location are part of the contract under test.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// langTransport 给每条请求补上 Accept-Language: en（已有取值时不动），并转发到客户端自己的连接池。
// langTransport appends Accept-Language: en to every request that does not carry one and forwards
// to the client's own connection pool.
type langTransport struct {
	base *http.Transport
}

// RoundTrip implements http.RoundTripper. | RoundTrip 实现 http.RoundTripper。
func (t langTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "en")
	}
	if t.base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return t.base.RoundTrip(req)
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
	return base64.StdEncoding.EncodeToString(cipher)
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
		t.Fatal("the probe client must carry a cookie jar | 探针客户端必须带 cookie jar")
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
// createProbeRole 以管理员身份建一个自定义角色并授予给定能力。
// createProbeRole creates a custom role with the given capabilities as the administrator.
func createProbeRole(t *testing.T, client *http.Client, base, token, name string, perms ...string) {
	t.Helper()

	values := url.Values{"name": {name}, "description": {name}}
	for _, perm := range perms {
		values.Add("permission", perm)
	}
	resp, body := postFormValues(t, client, base+"/mgr/roles", values, map[string]string{
		"HX-Request":    "true",
		"Authorization": "Bearer " + token,
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, name) {
		t.Fatalf("建角色 %s 失败（状态码 %d）:\n%s", name, resp.StatusCode, body)
	}
}

func createProbeUser(t *testing.T, client *http.Client, base, token, username, role, email string) {
	t.Helper()

	resp, body := postFormValues(t, client, base+"/mgr/users", url.Values{
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
func startGeneratedApp(t *testing.T, bin, dir string) string {
	t.Helper()

	base, stop := startGeneratedAppWithStop(t, bin, dir)
	t.Cleanup(stop)
	return base
}

// startGeneratedAppWithStop 与 startGeneratedApp 相同，但把停止函数交给调用方，供「停止 → 修改数据库 → 重启」的
// 升级用例使用；调用方必须自己调用 stop。
// startGeneratedAppWithStop is startGeneratedApp with the stop function returned to the caller, for upgrade cases that
// stop the app, change the database and start it again; the caller must call stop itself.
//
// 端口经 YD_SERVER_PORT 注入（映射到 server.port），因此不与开发机上任何常驻服务抢端口；
// 启动输出被收集起来，就绪等待超时时一并打印，排障不必再手动复现。
// The port is injected through YD_SERVER_PORT (mapped onto server.port), so no resident service on the developer machine
// can collide; the startup output is collected and printed when the readiness wait times out, which keeps debugging free
// of manual reproduction.
// withoutEnv returns env without the named variables (case-insensitive). Probe
// applications must not inherit a developer's node identity or identity file. |
// withoutEnv 返回移除指定变量（大小写不敏感）后的环境。探针应用不得继承开发者的节点身份或
// 身份文件。
func withoutEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		skip := false
		if ok {
			for _, key := range keys {
				if strings.EqualFold(name, key) {
					skip = true
					break
				}
			}
		}
		if !skip {
			out = append(out, entry)
		}
	}
	return out
}

func startGeneratedAppWithStop(t *testing.T, bin, dir string) (string, func()) {
	t.Helper()

	return startGeneratedAppWithStopEnv(t, bin, dir, nil)
}

// startGeneratedAppWithStopEnv is startGeneratedAppWithStop with extra environment variables; the
// names of those variables are removed from the inherited environment first, so the explicit value
// always wins even when the developer shell exported the same key. |
// startGeneratedAppWithStopEnv 是带附加环境变量的 startGeneratedAppWithStop；附加变量会先从继承环境中移除，
// 因此显式值总是优先，即使开发者的 shell 导出了同名变量。
// lockedBuffer is a bytes.Buffer safe for one child process writing while the test reads it.
// | lockedBuffer 是一个并发安全的 bytes.Buffer，供子进程写入的同时测试读取。
type lockedBuffer struct {
	// mu guards buf. | mu 保护 buf。
	mu sync.Mutex
	// buf holds the captured output. | buf 保存捕获的输出。
	buf bytes.Buffer
}

// Write appends p under the lock. | Write 在锁内追加 p。
func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns a copy of the captured output. | String 返回已捕获输出的副本。
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startGeneratedAppWithStopEnv(t *testing.T, bin, dir string, extra map[string]string) (string, func()) {
	t.Helper()

	port := freePort(t)
	cmd := exec.Command(bin)
	cmd.Dir = dir
	// The probe sets GODEBUG itself so the generated app handles RFC 8441 without
	// re-executing; a re-exec would leave the server as a grandchild that survives
	// a test cleanup on Windows. | 探针自行设置 GODEBUG，使生成的应用无需重启进程即可
	// 处理 RFC 8441；重启会让服务器变成孙进程，在 Windows 上测试清理后仍会存活。
	removed := []string{"YD_NODE_ID", "YD_NODE_ID_FILE", "YD_NODE_REQUIRE_PERSISTENT",
		"YD_NODE_ALLOW_SHARED_SEED", "YD_NODE_SALT", "YD_MESSAGE_NODE_ID", "GODEBUG"}
	extraEnv := make([]string, 0, len(extra))
	for key, value := range extra {
		removed = append(removed, key)
		extraEnv = append(extraEnv, key+"="+value)
	}
	sort.Strings(extraEnv)
	cmd.Env = append(append(withoutEnv(os.Environ(), removed...),
		"GOWORK=off",
		"GODEBUG="+protocol.MergeGODEBUG(os.Getenv("GODEBUG"), "http2xconnect=1"),
		// 探针只从本机回环访问：绑定 127.0.0.1 既不依赖通配地址的绑定权限，也不会把端口暴露到局域网。
		// The probe only ever reaches the app over the loopback interface: binding 127.0.0.1 needs no wildcard-bind
		// permission and never exposes the port to the local network.
		"YD_SERVER_HOST=127.0.0.1",
		"YD_SERVER_PORT="+strconv.Itoa(port),
		"YD_LOG_FORMAT=console",
	), extraEnv...)
	logs := &lockedBuffer{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动生成的应用失败: %v", err)
	}
	stopped := false
	stop := func() {
		if stopped || cmd.Process == nil {
			return
		}
		stopped = true
		if runtime.GOOS == "windows" {
			// The app may re-exec itself to inject GODEBUG; kill the process tree,
			// not just the launcher, or the server child survives the test. |
			// 应用可能为注入 GODEBUG 重启自身；必须杀掉整棵进程树而不是只杀启动器，否则
			// 服务器子进程会在测试结束后继续存活。
			_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		} else {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("generated app log:\n%s", logs.String())
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
			stop()
			t.Fatalf("生成的应用未在 %s 内就绪:\n%s", startupTimeout, logs.String())
		}
		if !ready {
			time.Sleep(100 * time.Millisecond)
		}
	}

	t.Logf("生成的应用已就绪: %s", base)
	return base, stop
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
		Paths map[string]map[string]struct {
			Summary string   `json:"summary"`
			Tags    []string `json:"tags"`
		} `json:"paths"`
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

			op := raw
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

// assertOpenAPIRefsResolve proves every local $ref in the generated document points at an
// existing component. Unexported payload types need an @Name annotation; without it the document
// still renders, but the reference stays dangling and /mgr/docs cannot show the schema. |
// assertOpenAPIRefsResolve 证明生成文档中的每个本地 $ref 都指向已存在的组件。未导出的载荷类型
// 需要 @Name 注解；缺少它时文档仍会渲染，但引用悬空，/mgr/docs 无法展示该 Schema。
func assertOpenAPIRefsResolve(t *testing.T, doc string) {
	t.Helper()

	var root map[string]any
	if err := json.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatalf("解析 /openapi.json 失败: %v\n%s", err, doc)
	}
	seen := make(map[string]bool)
	var walk func(any)
	walk = func(node any) {
		switch value := node.(type) {
		case map[string]any:
			if ref, ok := value["$ref"].(string); ok {
				if seen[ref] {
					return
				}
				seen[ref] = true
				if !strings.HasPrefix(ref, "#/") {
					t.Errorf("OpenAPI $ref %q 不是本地引用", ref)
					return
				}
				if _, ok := resolveOpenAPIPointer(root, strings.TrimPrefix(ref, "#/")); !ok {
					t.Errorf("OpenAPI $ref %q 无法解析（缺少 @Name 注解？）", ref)
				}
				return
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(root)
}

// resolveOpenAPIPointer walks one JSON pointer inside the decoded document. | resolveOpenAPIPointer
// 在解码后的文档内按一个 JSON 指针遍历。
func resolveOpenAPIPointer(root map[string]any, pointer string) (any, bool) {
	var current any = root
	for _, token := range strings.Split(pointer, "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[token]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(node) {
				return nil, false
			}
			current = node[index]
		default:
			return nil, false
		}
	}
	return current, true
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

// TestGenerateSupportsTheFullHtmx4ExtensionSet 断言全部 htmx 4 扩展都能被选中、裁剪进清单并写入 meta 脚本表。
// TestGenerateSupportsTheFullHtmx4ExtensionSet asserts that every htmx 4 extension can be selected,
// survives the manifest trim and lands in the generated meta script list.
func TestGenerateSupportsTheFullHtmx4ExtensionSet(t *testing.T) {
	o := scaffold.DefaultOptions("ext-app")
	o.HtmxExts = scaffold.Extensions()
	pr, _ := generateMem(t, o)

	lock := pr.read("web/assets.lock.json")
	for _, name := range []string{"hx-live", "hx-alpine-compat", "hx-browser-indicator", "hx-csp", "hx-compat", "htmx-2-compat"} {
		if !strings.Contains(lock, `"`+name+`"`) {
			t.Errorf("assets.lock.json 缺少资产 %q", name)
		}
	}

	meta := pr.read("internal/meta/meta.go")
	for _, want := range []string{
		"/static/js/vendored/hx-ws.min.js",
		"/static/js/vendored/hx-sse.min.js",
		"/static/js/vendored/hx-preload.min.js",
		"/static/js/vendored/hx-alpine-compat.min.js",
		"/static/js/vendored/hx-targets.min.js",
		"/static/js/vendored/hx-head.min.js",
		"/static/js/vendored/hx-upsert.min.js",
		"/static/js/vendored/hx-pending.min.js",
		"/static/js/vendored/hx-multipart.min.js",
		"/static/js/vendored/hx-download.min.js",
		"/static/js/vendored/hx-browser-indicator.min.js",
		"/static/js/vendored/hx-history-cache.min.js",
		"/static/js/vendored/hx-live.min.js",
		"/static/js/vendored/hx-prompt.min.js",
		"/static/js/vendored/hx-ptag.min.js",
		"/static/js/vendored/hx-csp.min.js",
		"/static/js/vendored/hx-compat.min.js",
		"/static/js/vendored/htmx-2-compat.min.js",
	} {
		if !strings.Contains(meta, want) {
			t.Errorf("meta.go 缺少脚本 %q", want)
		}
	}
}

// TestGenerateRuntimeEnvUsesEnvx 断言生成代码复用 core/envx 归一化运行环境名，
// 使 dev/prod 短别名在 meta.Env 与种子判定上行为一致。
// TestGenerateRuntimeEnvUsesEnvx asserts the generated code reuses core/envx to normalize the runtime
// environment name, so the dev/prod aliases behave the same in meta.Env and in the seed decision.
func TestGenerateRuntimeEnvUsesEnvx(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("env-app"))

	for _, want := range []struct {
		file string
		text string
	}{
		{"internal/meta/meta.go", "envx.Normalize"},
		{"internal/repository/store.go", "envx.Normalize"},
	} {
		if !strings.Contains(pr.read(want.file), want.text) {
			t.Errorf("%s 缺少 %q，运行环境名必须经 core/envx 归一化", want.file, want.text)
		}
	}
}

// TestGenerateWiresStaticFSToRenderer 断言生成项目把 web.Static() 交给
// app.WithStaticFS，并让布局的静态引用统一走 asset。
// TestGenerateWiresStaticFSToRenderer asserts the generated project hands web.Static() to
// app.WithStaticFS and routes every layout static reference through asset.
func TestGenerateWiresStaticFSToRenderer(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("asset-app"))

	if !strings.Contains(pr.read("cmd/app/main.go"), "app.WithStaticFS(static)") {
		t.Error("the generated main.go must pass web.Static() to app.WithStaticFS | 生成的 main.go 必须把 web.Static() 交给 app.WithStaticFS")
	}
	if !strings.Contains(pr.read("web/embed.go"), "afs.HideShadowedMin") {
		t.Error("generated embed.go must hide shadowed framework .min assets | 生成的 embed.go 必须隐藏被项目源码遮蔽的框架 .min 资产")
	}
	for _, want := range []string{
		`{{ asset "/static/css/yd.css" }}`,
		`{{ asset "/static/js/yd-app.js" }}`,
	} {
		if !strings.Contains(pr.read("web/templates/partials/base.html"), want) {
			t.Errorf("生成的 base.html 未通过 asset 解析 %q", want)
		}
	}
}

// TestGeneratedShellsCarryTheGlobalProgressBar asserts both generated shells render the shared bar
// and load the framework controller ahead of their own app script. Without it the generated project
// would silently fall back to the browser's own spinner, which htmx partial swaps never trigger.
// TestGeneratedShellsCarryTheGlobalProgressBar 断言两套生成外壳都渲染共享进度条，并在自己的应用脚本之前
// 加载框架控制器。缺少它时，生成项目只能退回到浏览器自带的转圈，而 htmx 局部交换根本不会触发它。
func TestGeneratedShellsCarryTheGlobalProgressBar(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("progress-app"))

	for _, rel := range []string{
		"web/templates/partials/base.html",
	} {
		page := pr.read(rel)
		for _, want := range []string{
			`id="yd-progress"`,
			`class="yd-progress"`,
			`class="yd-progress__bar"`,
			`data-yd-progress`,
			`{{ asset "/static/js/yd-progress.js" }}`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("%s lacks %q | %s 缺少 %q", rel, want, rel, want)
			}
		}

		progressAt := strings.Index(page, `{{ asset "/static/js/yd-progress.js" }}`)
		appAt := strings.Index(page, `{{ asset "/static/js/yd-app.js" }}`)
		if progressAt < 0 || appAt < 0 || progressAt > appAt {
			t.Errorf("%s must load yd-progress.js before yd-app.js (progress=%d app=%d) | %s 必须在 yd-app.js 之前加载 yd-progress.js（progress=%d app=%d）",
				rel, progressAt, appAt, rel, progressAt, appAt)
		}
	}
}

// TestGeneratedUILoadsAlpineAndTheKitInOrder pins the one order the CSP build allows. Alpine's
// CSP distribution starts the moment its script executes, so a registry loaded after it would
// register into a started instance and every x-data on the page would fail. The test also proves
// the UI layer is copied from the framework rather than re-implemented per project.
// TestGeneratedUILoadsAlpineAndTheKitInOrder 固定 CSP 构建唯一允许的顺序。Alpine 的 CSP 发行版在
// 脚本执行的那一刻就启动，排在它之后的注册表会注册进一个已启动的实例，页面里每个 x-data 都会失败。
// 该测试同时证明 UI 层是从框架复制而来，而不是每个项目另写一份。
func TestGeneratedUILoadsAlpineAndTheKitInOrder(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("ui-app"))

	for _, rel := range []string{
		"web/templates/partials/base.html",
	} {
		page := pr.read(rel)
		kit := `{{ asset "/static/js/yd-ui.js" }}`
		alpine := `{{ asset "/static/js/vendored/alpine.min.js" }}`
		kitAt := strings.Index(page, kit)
		alpineAt := strings.Index(page, alpine)
		switch {
		case kitAt < 0:
			t.Errorf("%s does not load yd-ui.js | %s 未加载 yd-ui.js", rel, rel)
		case alpineAt < 0:
			t.Errorf("%s does not load alpine.min.js | %s 未加载 alpine.min.js", rel, rel)
		case kitAt > alpineAt:
			t.Errorf("%s loads Alpine before the UI kit (kit=%d alpine=%d); the CSP build starts immediately, so the kit must come first | %s 先加载 Alpine 后加载 UI 层（kit=%d alpine=%d）；CSP 构建立即启动，UI 层必须在前",
				rel, kitAt, alpineAt, rel, kitAt, alpineAt)
		}
	}

	app := pr.read("web/static/css/app.css")
	for _, want := range []string{`@import "./components.css"`, `@import "./ui.css"`} {
		if !strings.Contains(app, want) {
			t.Errorf("generated app.css lacks %q | 生成的 app.css 缺少 %q", want, want)
		}
	}

	// The generated component templates and UI stylesheet must be byte-identical to the framework's
	// copies: a second implementation would drift the moment either side is edited.
	// 生成的组件模板与 UI 样式表必须与框架副本逐字节一致：第二份实现会在任一侧被修改的那一刻开始漂移。
	for _, name := range []string{"chart.html", "cn.html", "data.html", "display.html", "display_advanced.html", "display_business.html", "display_interactive.html", "feedback.html", "feedback_advanced.html", "form.html", "form_advanced.html", "layout.html", "navigation.html", "navigation_advanced.html", "overlay.html"} {
		rel := "web/templates/components/" + name
		if !pr.exists(rel) {
			t.Errorf("generated project lacks %s | 生成项目缺少 %s", rel, rel)
			continue
		}
		want, err := afero.ReadFile(web.FS(), "templates/components/"+name)
		if err != nil {
			t.Fatalf("reading framework %s failed: %v | 读取框架 %s 失败：%v", name, err, name, err)
		}
		if got := pr.read(rel); got != string(want) {
			t.Errorf("%s differs from the framework copy | %s 与框架副本不一致", rel, rel)
		}
	}
	for _, name := range []string{"ui.css"} {
		rel := "web/static/css/" + name
		if !pr.exists(rel) {
			t.Errorf("generated project lacks %s | 生成项目缺少 %s", rel, rel)
			continue
		}
		want, err := afero.ReadFile(web.FS(), "static/css/"+name)
		if err != nil {
			t.Fatalf("reading framework %s failed: %v | 读取框架 %s 失败：%v", name, err, name, err)
		}
		if got := pr.read(rel); got != string(want) {
			t.Errorf("%s differs from the framework copy | %s 与框架副本不一致", rel, rel)
		}
	}
}

// TestGeneratePinsTheRunningYdVersion ensures a project generated by an installed
// yd pins that exact framework version, so go mod tidy cannot fall back to an older
// framework that lacks a newly imported package. | TestGeneratePinsTheRunningYdVersion
// 断言由已安装 yd 生成的项目锁定该框架版本，避免 go mod tidy 回落到缺少新导入包的旧框架。
func TestGeneratePinsTheRunningYdVersion(t *testing.T) {
	const pinned = "v0.0.0-20261019121535-d525ac6da9d3"

	o := scaffold.DefaultOptions("pinned-app")
	o.YdVersion = pinned
	pr, _ := generateMem(t, o)
	if got := pr.read("go.mod"); !strings.Contains(got, scaffold.YdModule+" "+pinned) {
		t.Errorf("go.mod 未锁定框架版本 %q:\n%s", pinned, got)
	}
	errsVersion := scaffold.YdxVersions[scaffold.YdxModule+"/errs"]
	if errsVersion == "" {
		t.Fatal("生成的 YdxVersions 缺少 errs 的版本 | generated YdxVersions is missing the errs version")
	}
	if got := pr.read("go.mod"); !strings.Contains(got, scaffold.YdxModule+"/errs "+errsVersion) {
		t.Errorf("go.mod 未锁定 ydx/errs 版本 %q:\n%s", errsVersion, got)
	}
	// The auth store imports cache for post-commit invalidation, so the framework now knows its
	// version and every generated project must pin it like any other framework dependency. |
	// 鉴权存储为提交后失效导入了 cache，框架因此知道它的版本，生成项目必须像其它框架依赖一样锁定。
	cacheVersion := scaffold.YdxVersions[scaffold.YdxModule+"/cache"]
	if cacheVersion == "" {
		t.Fatal("生成的 YdxVersions 缺少 cache 的版本 | generated YdxVersions is missing the cache version")
	}
	if got := pr.read("go.mod"); !strings.Contains(got, scaffold.YdxModule+"/cache "+cacheVersion) {
		t.Errorf("go.mod 未锁定 ydx/cache 版本 %q", cacheVersion)
	}

	unpinned := scaffold.DefaultOptions("unpinned-app")
	unpinned.YdVersion = ""
	pr, _ = generateMem(t, unpinned)
	if got := pr.read("go.mod"); strings.Contains(got, scaffold.YdModule+" v") {
		t.Errorf("版本未知时不应写入框架 require:\n%s", got)
	}
	if got := pr.read("go.mod"); strings.Contains(got, scaffold.YdxModule+"/errs v") {
		t.Errorf("版本未知时不应写入 ydx require:\n%s", got)
	}
}

// TestGeneratedGoModPinsEveryScaffoldYdxModule guards the non-local generation path: every module
// listed in YdxModules must carry an exact tag in go.mod. A missing tag makes `go build` resolve
// the package against the ydx root module, which does not contain submodules such as asynq and
// openapi, and the generated project fails on a clean machine even though --local-replace tests
// pass. | TestGeneratedGoModPinsEveryScaffoldYdxModule 守护非本地生成路径：YdxModules 中的每个模块
// 都必须在 go.mod 中带精确标签。缺少标签时 `go build` 会到 ydx 根模块中解析这些包，而根模块并不包含
// asynq、openapi 等子模块，生成项目在干净机器上会失败——即使 --local-replace 测试通过。
func TestGeneratedGoModPinsEveryScaffoldYdxModule(t *testing.T) {
	o := scaffold.DefaultOptions("pinned-modules-app")
	o.YdVersion = "v0.0.0-20261019121535-d525ac6da9d3"
	// Exercise the conditional template branches as well, so an import that only appears with gRPC or captcha
	// cannot bypass the module inventory. | 同时覆盖条件模板分支，使只在 gRPC 或验证码开启时出现的 import
	// 也不能绕过模块清单。
	o.GRPC = true
	o.Captcha = true
	o.DB = scaffold.DBMySQL
	pr, res := generateMem(t, o)
	gomod := pr.read("go.mod")

	for _, modulePath := range scaffold.YdxModules {
		version := scaffold.YdxVersions[modulePath]
		if version == "" {
			t.Errorf("YdxVersions 缺少模块 %q 的版本；生成项目会退回 ydx 根模块 | YdxVersions is missing %q; the generated project would fall back to the ydx root module", modulePath, modulePath)
			continue
		}
		if !strings.Contains(gomod, modulePath+" "+version) {
			t.Errorf("go.mod 未锁定 %q 的版本 %q:\n%s", modulePath, version, gomod)
		}
	}

	// The go.mod loop above can only see modules someone remembered to list. Scan the generated imports
	// instead, so a new {{ .YdxModule }}/foo import cannot silently bypass the version list just because
	// --local-replace keeps the local build green. | 上面的 go.mod 循环只能发现有人记得列出的模块；扫描生成
	// 代码的 import，避免新增的 {{ .YdxModule }}/foo 仅因 --local-replace 本地构建通过就悄悄绕过版本清单。
	fset := token.NewFileSet()
	for _, generated := range res.Files {
		if !strings.HasSuffix(generated, ".go") {
			continue
		}
		src, err := afero.ReadFile(pr.fs, generated)
		if err != nil {
			t.Fatalf("读取生成文件 %s 失败: %v", generated, err)
		}
		parsed, err := parser.ParseFile(fset, generated, src, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("解析生成文件 %s 失败: %v", generated, err)
		}
		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil || !strings.HasPrefix(path, scaffold.YdxModule+"/") {
				continue
			}
			owner := ""
			for _, modulePath := range scaffold.YdxModules {
				if (path == modulePath || strings.HasPrefix(path, modulePath+"/")) && len(modulePath) > len(owner) {
					owner = modulePath
				}
			}
			if owner == "" {
				t.Errorf("%s 导入了未登记的 ydx 模块路径 %q；请在 go.mod 或 tools.go 中补上 | %s imports unregistered ydx path %q; add it to go.mod or tools.go", generated, path, generated, path)
				continue
			}
			if scaffold.YdxVersions[owner] == "" {
				t.Errorf("%s 的 ydx 导入 %q 所属模块 %q 没有版本 | ydx import %q in %s belongs to unversioned module %q", generated, path, owner, path, generated, owner)
			}
		}
	}
}

// TestGenerateThemeBootAndAutoTokens pins the generated theme pipeline: the boot script runs before
// the stylesheet, the default is "auto", and the auto dark block carries every semantic token the
// shell uses. The last part is what keeps the workspace readable on a system-dark visitor. |
// TestGenerateThemeBootAndAutoTokens 固定生成项目的主题链路：首屏脚本先于样式表、默认 auto，
// 且 auto 深色块覆盖外壳使用的全部语义令牌——这正是系统深色下工作区保持可读的关键。
func TestGenerateThemeBootAndAutoTokens(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("theme-app"))

	base := pr.read("web/templates/partials/base.html")
	if !strings.Contains(base, `data-theme="auto"`) {
		t.Error("generated base.html must default to data-theme=auto | 生成的 base.html 必须默认 data-theme=auto")
	}
	boot, css := strings.Index(base, "yd-theme-boot.js"), strings.Index(base, `asset "/static/css/yd.css"`)
	if boot < 0 || css < 0 || boot > css {
		t.Errorf("yd-theme-boot.js must load before the stylesheet (boot=%d css=%d) | yd-theme-boot.js 必须先于样式表加载", boot, css)
	}

	app := pr.read("web/static/js/yd-app.js")
	for _, want := range []string{
		"function rememberTheme(theme)",
		"document.cookie = THEME_KEY",
		`applyTheme(readTheme() || "auto")`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("generated yd-app.js is missing %q | 生成的 yd-app.js 缺少 %q", want, want)
		}
	}

	cssBody := pr.read("web/static/css/app.css")
	for _, want := range []string{
		`@media (prefers-color-scheme: dark)`,
		":root[data-theme=\"auto\"]",
		"--color-surface-muted: #111c2f;",
		"--color-success: #4ade80;",
		"--color-accent: #60a5fa;",
	} {
		if !strings.Contains(cssBody, want) {
			t.Errorf("generated app.css auto block is missing %q | 生成的 app.css auto 深色块缺少 %q", want, want)
		}
	}
}

// TestGeneratedPageAssetsAnnounceCompletion pins the second initialisation point of page-level
// assets. A full page load delivers deferred page scripts before window.load, but an htmx partial
// navigation never fires it again; the editor component therefore has no trigger unless yd-app.js
// announces that the page scripts settled. The event name is a contract with yd-ui.js. |
// TestGeneratedPageAssetsAnnounceCompletion 固定页面级资产的第二个初始化时机。整页加载会在
// window.load 之前执行延迟的页面脚本，但 htmx 局部导航不会再次触发它；除非 yd-app.js 广播页面脚本
// 已结算，编辑器组件就没有任何触发点。事件名是与 yd-ui.js 的契约。
func TestGeneratedPageAssetsAnnounceCompletion(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("page-assets-app"))

	app := pr.read("web/static/js/yd-app.js")
	for _, want := range []string{
		"function loadPageAssets(root)",
		`"yd:page-assets:loaded"`,
		"document.dispatchEvent",
		"CustomEvent",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("generated yd-app.js is missing %q; an htmx navigation would never re-initialise page-level editors | 生成的 yd-app.js 缺少 %q；htmx 局部导航后将永远不会重新初始化页面级编辑器", want, want)
		}
	}
}

// TestGeneratedAppRepairsAlpineScopes pins the two shell repairs that regressions silently undo:
// hx-history-cache empties every Alpine data stack in the live document before an htmx morph, and
// EasyMDE's fullscreen toolbar ignores the sticky app bar. The glue must re-initialise the emptied
// scopes and publish the measured bar height. | TestGeneratedAppRepairsAlpineScopes 固定两项会被
// 回归悄悄撤销的外壳修复：hx-history-cache 在 htmx morph 前清空活动文档的 Alpine 数据栈，
// EasyMDE 全屏工具栏又不认识吸顶顶栏。胶水层必须重新初始化空栈并发布实测顶栏高度。
func TestGeneratedAppRepairsAlpineScopes(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("alpine-repair-app"))

	app := pr.read("web/static/js/yd-app.js")
	for _, want := range []string{
		"function reviveAlpineScopes(root)",
		`"[data-has-alpine-state]"`,
		"alpine.initTree(el)",
		"function syncTopbarOffset()",
		`"--yd-topbar-offset"`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("generated yd-app.js is missing %q | 生成的 yd-app.js 缺少 %q", want, want)
		}
	}
}

// TestGenerateIconsUseTheBundledSprite pins the generated icon contract: every requested button
// references the shared partial, and the partial resolves the framework sprite instead of copying
// icon bodies into templates. | TestGenerateIconsUseTheBundledSprite 固定生成项目的图标契约：
// 所有指定按钮都引用共享片段，片段解析框架 sprite，而不是把图标体复制进模板。
func TestGenerateIconsUseTheBundledSprite(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("icon-app"))

	partial := pr.read("web/templates/partials/icon.html")
	for _, want := range []string{
		`asset "/static/img/icons.svg"`,
		`data-yd-icon="{{ . }}"`,
		`focusable="false"`,
	} {
		if !strings.Contains(partial, want) {
			t.Errorf("partials/icon.html is missing %q | partials/icon.html 缺少 %q", want, want)
		}
	}

	for _, want := range []struct {
		file string
		icon string
	}{
		{"web/templates/portal/partials/login.html", "lucide-log-in"},
		{"web/templates/partials/topbar.html", "lucide-log-out"},
		{"web/templates/partials/shell-switches.html", "lucide-languages"},
		{"web/templates/partials/shell-switches.html", "lucide-sun"},
		{"web/templates/partials/shell-switches.html", "lucide-moon"},
		{"web/templates/partials/shell-switches.html", "lucide-monitor"},
	} {
		needle := `template "partials/icon" "` + want.icon + `"`
		if !strings.Contains(pr.read(want.file), needle) {
			t.Errorf("%s is missing %s | %s 缺少 %s", want.file, needle, want.file, needle)
		}
	}

	// The EasyMDE toolbar is built at runtime, so its icons come from a server-rendered palette
	// shipped with every editor. No Font Awesome asset or class may survive. | EasyMDE 工具栏在
	// 运行时构造，图标来自随每个编辑器渲染的服务端调色板；不得保留任何 Font Awesome 资源或类名。
	palette := pr.read("web/templates/partials/editor-icons.html")
	for _, want := range []string{"data-yd-editor-icons", `data-yd-editor-icon="lucide-bold"`, `template "partials/icon" "lucide-redo-2"`} {
		if !strings.Contains(palette, want) {
			t.Errorf("generated editor palette is missing %q | 生成的编辑器调色板缺少 %q", want, want)
		}
	}
	for _, file := range []string{
		"web/templates/components/form.html",
		"web/templates/portal/partials/messages/compose.html",
		"web/templates/portal/partials/messages/composer.html",
	} {
		if !strings.Contains(pr.read(file), `template "partials/editor-icons"`) {
			t.Errorf("%s does not render the editor palette | %s 未渲染编辑器调色板", file, file)
		}
	}
	lock := pr.read("web/assets.lock.json")
	for _, gone := range []string{"font-awesome", "fontawesome"} {
		if strings.Contains(strings.ToLower(lock), gone) {
			t.Errorf("generated assets.lock.json still carries %q | 生成的 assets.lock.json 仍包含 %q", gone, gone)
		}
	}
	if strings.Contains(pr.read("internal/controller/message_view.go"), "font-awesome") {
		t.Error("message_view.go still loads the Font Awesome stylesheet | message_view.go 仍加载 Font Awesome 样式表")
	}
}

// TestGenerateLanguageToggle pins the generated single language button: it shows the active
// language, carries the next language for the no-JS link and htmx shell swap, and no longer
// renders the old segmented control. | TestGenerateLanguageToggle 固定生成项目的单一语言按钮：
// 它显示当前语言，在无脚本链接与 htmx 外壳交换里携带目标语言，并且不再渲染旧的分段控件。
func TestGenerateLanguageToggle(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("lang-app"))

	switches := pr.read("web/templates/partials/shell-switches.html")
	if got := strings.Count(switches, "data-yd-lang-toggle"); got != 1 {
		t.Errorf("shell-switches.html has %d language toggles, want 1 | shell-switches.html 有 %d 个语言按钮，应为 1", got, got)
	}
	if strings.Contains(switches, "yd-switch__item") {
		t.Error("shell-switches.html still renders the old two-link switcher | shell-switches.html 仍渲染旧的双链接切换")
	}
	for _, want := range []string{`data-yd-lang-next="`, `hx-target="#yd-shell"`, `hx-swap="innerHTML"`, `href="{{ .Path }}?lang=`} {
		if !strings.Contains(switches, want) {
			t.Errorf("shell-switches.html is missing %q | shell-switches.html 缺少 %q", want, want)
		}
	}

	app := pr.read("web/static/js/yd-app.js")
	if !strings.Contains(app, "data-yd-lang-toggle") || !strings.Contains(app, "rememberLang(toggle.getAttribute(\"data-yd-lang-next\"))") {
		t.Error("generated yd-app.js does not remember the language chosen through the toggle in the capture phase | 生成的 yd-app.js 没有在捕获阶段记住按钮选择的语言")
	}
	if !strings.Contains(app, `setAttribute("lang", lang)`) {
		t.Error("generated yd-app.js does not sync <html lang> after a shell-only language swap | 生成的 yd-app.js 在外壳语言交换后没有同步 <html lang>")
	}

	for _, file := range []string{"web/i18n/zh-CN.yaml", "web/i18n/en.yaml"} {
		body := pr.read(file)
		for _, want := range []string{"zh-cn:", "en:"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s is missing the lang.%s label | %s 缺少 lang.%s 文案", file, strings.TrimSuffix(want, ":"), file, strings.TrimSuffix(want, ":"))
			}
		}
	}
}

// TestGenerateRealtimeRecoveryHooks pins the generated reconnect manager, its single banner and the
// declarative resync hook. | TestGenerateRealtimeRecoveryHooks 固定生成项目的重连管理器、唯一横幅与声明式重新同步钩子。
func TestGenerateRealtimeRecoveryHooks(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("realtime-app"))

	app := pr.read("web/static/js/yd-app.js")
	for _, want := range []string{
		"function realtimeElements()",
		"function refreshConnection()",
		"function retryRealtime()",
		"function resyncRealtime(",
		"yd:realtime:recovered",
		"data-yd-resync",
		"data-yd-resync-target",
		"data-yd-connection-attempt",
		"noticeItem",
		"readyState",
		"pagehide",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("generated yd-app.js is missing %q | 生成的 yd-app.js 缺少 %q", want, want)
		}
	}

	base := pr.read("web/templates/partials/base.html")
	for _, want := range []string{"data-yd-connecting", "data-yd-recovered", "data-yd-offline"} {
		if !strings.Contains(base, want) {
			t.Errorf("generated base.html is missing %q | 生成的 base.html 缺少 %q", want, want)
		}
	}

	flash := pr.read("web/templates/partials/flash.html")
	for _, want := range []string{"data-yd-connection-text", "data-yd-connection-attempt", "data-yd-reconnect"} {
		if !strings.Contains(flash, want) {
			t.Errorf("generated flash.html is missing %q | 生成的 flash.html 缺少 %q", want, want)
		}
	}

	home := pr.read("web/templates/mgr/pages/home.html")
	if !strings.Contains(home, `hx-ws:connect="/mgr/ws/metrics"`) {
		t.Error("the home metrics cards must share one WebSocket connection | 首页指标卡片必须共用一条 WebSocket 连接")
	}
	if strings.Contains(home, `data-yd-resync="/mgr/metrics"`) {
		t.Error("the home clock must not resync metrics over HTTP after a reconnect | 首页时钟不得在重连后用 HTTP 重同步指标")
	}

	for _, file := range []string{"web/i18n/zh-CN.yaml", "web/i18n/en.yaml"} {
		body := pr.read(file)
		for _, want := range []string{"recovered:", "offline:", "retry:"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s is missing %q | %s 缺少 %q", file, want, file, want)
			}
		}
	}
}

// TestSidebarOmitsHealthMenuItem pins the API group to the two links that need the chrome: the
// health probe still exists as a public endpoint, but it is an operator concern, not a navigation
// destination, so it must not occupy a sidebar row. | TestSidebarOmitsHealthMenuItem 固定接口组只保留
// 需要外壳的两个入口：健康检查仍是公开端点，但那是运维关注点而非导航目的地，不应占用侧栏一行。
func TestSidebarOmitsHealthMenuItem(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("nav-app"))

	sidebar := pr.read("web/templates/mgr/partials/sidebar.html")
	for _, unwanted := range []string{
		`href="/healthz"`, `data-yd-nav="health"`, `nav.health`,
		// The overview already renders system resources, so a separate metrics entry only duplicates it. |
		// 管理概览已经展示系统资源，单独的系统指标入口只会重复。
		`href="/mgr/metrics"`, `data-yd-nav="mgr-metrics"`, `nav.metrics`,
	} {
		if strings.Contains(sidebar, unwanted) {
			t.Errorf("sidebar still carries the utility menu item (%q) | 侧栏仍包含工具菜单项（%q）", unwanted, unwanted)
		}
	}
	for _, want := range []string{`href="/mgr/docs"`, `href="/api/v1/openapi.json"`} {
		if !strings.Contains(sidebar, want) {
			t.Errorf("sidebar must keep %q in the API group | 侧栏接口组必须保留 %q", want, want)
		}
	}

	for _, file := range []string{"web/i18n/zh-CN.yaml", "web/i18n/en.yaml"} {
		body := pr.read(file)
		for _, unwanted := range []string{"health:", "\n  metrics:"} {
			if strings.Contains(body, unwanted) {
				t.Errorf("%s still defines the unused nav key %q | %s 仍定义未使用的导航键 %q", file, unwanted, file, unwanted)
			}
		}
	}
}

// TestSeedPasswordNoticeAppearsOnceOnHome pins the notice placement: the shell must not repeat the
// first-sign-in password reminder above every page, because home and account already carry it in
// context. Two banners on the overview page push the actual content down and read as noise. |
// TestSeedPasswordNoticeAppearsOnceOnHome 固定提示位置：外壳不得在每个页面顶部重复首登改密提醒，
// 首页与账号页已经就地给出；概览页上下两条横幅既挤占内容，也只是噪音。
func TestSeedPasswordNoticeAppearsOnceOnHome(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("notice-app"))

	shell := pr.read("web/templates/partials/app_shell.html")
	for _, unwanted := range []string{"SeedNotice", "data-yd-seed-password"} {
		if strings.Contains(shell, unwanted) {
			t.Errorf("shell.html still renders the seed-password banner (%q) | shell.html 仍渲染种子口令横幅（%q）", unwanted, unwanted)
		}
	}

	home := pr.read("web/templates/mgr/pages/home.html")
	if got := strings.Count(home, "{{ .SeedNotice }}"); got != 1 {
		t.Errorf("home.html must carry exactly one notice, got %d | home.html 必须恰好包含一条提示，实际为 %d", got, got)
	}
	account := pr.read("web/templates/portal/pages/account.html")
	if !strings.Contains(account, "{{ .SeedNotice }}") {
		t.Error("account.html must keep its own password notice | account.html 必须保留自己的改密提示")
	}
}

// TestGenerateResponsiveShellHooks pins the generated mobile navigation hooks and the responsive
// component layer. | TestGenerateResponsiveShellHooks 固定生成项目的移动端导航钩子与响应式组件层。
// TestHomeShowsMiddlewareRuntime pins the middleware runtime card and the instrumentation wiring:
// every built-in middleware is observed through the same registry, and both metric cards share one
// WebSocket connection instead of two polling loops. | TestHomeShowsMiddlewareRuntime 固定中间件运行时
// 卡片与观测接线：每个内建中间件都经同一注册表观察，两张指标卡片共用一条 WebSocket 连接而不是两套轮询。
func TestHomeShowsMiddlewareRuntime(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("middleware-app"))

	home := pr.read("web/templates/mgr/pages/home.html")
	for _, want := range []string{
		`id="yd-live-metrics"`,
		`hx-ws:connect="/mgr/ws/metrics"`,
		`hx-target="this"`,
		`hx-swap="innerMorph"`,
		`partials/home/live-metrics`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("home.html 缺少 %q | home.html is missing %q", want, want)
		}
	}
	for _, unwanted := range []string{
		`hx-get="/mgr/metrics"`,
		`hx-get="/mgr/metrics/middleware"`,
		`hx-trigger="every 5s"`,
		`data-yd-resync="/mgr/metrics"`,
	} {
		if strings.Contains(home, unwanted) {
			t.Errorf("home.html 仍保留轮询/HTTP 重同步 %q | home.html still keeps polling or HTTP resync %q", unwanted, unwanted)
		}
	}
	live := pr.read("web/templates/mgr/partials/home/live_metrics.html")
	for _, want := range []string{
		`partials/home/live-metrics`,
		`id="yd-metrics"`,
		`id="yd-middleware-metrics"`,
		`partials/home/metrics-panel`,
		`partials/home/middleware-panel`,
	} {
		if !strings.Contains(live, want) {
			t.Errorf("live_metrics.html 缺少 %q | live_metrics.html is missing %q", want, want)
		}
	}
	fragment := pr.read("web/templates/mgr/partials/home/middleware_fragment.html")
	if !strings.Contains(fragment, `partials/home/middleware-panel`) {
		t.Error("middleware_fragment.html 未渲染共享面板 | middleware_fragment.html does not render the shared panel")
	}

	routerSrc := pr.read("internal/router/router.go")
	for _, want := range []string{`"/mgr/metrics/middleware"`, `"/mgr/ws/metrics"`, "ctrl.MetricsSocket()"} {
		if !strings.Contains(routerSrc, want) {
			t.Errorf("受保护路由缺少 %q | the protected routes lack %q", want, want)
		}
	}
	streamSrc := pr.read("internal/controller/metrics_stream.go")
	for _, want := range []string{"MetricsSocket", "WriteHtmxFragment", "renderLiveMetrics", "#yd-live-metrics"} {
		if !strings.Contains(streamSrc, want) {
			t.Errorf("metrics_stream.go 缺少 %q | metrics_stream.go is missing %q", want, want)
		}
	}
	for _, name := range []string{"promote", "auth", "authz", "tenant"} {
		if !strings.Contains(routerSrc, `MetricsDescriptor("`+name+`")`) {
			t.Errorf("router.go 未观察路由中间件 %q | router.go does not observe route middleware %q", name, name)
		}
	}

	mainSrc := pr.read("cmd/app/main.go")
	for _, name := range []string{"recover", "request-id", "access-log", "compress", "cors", "security", "i18n"} {
		if !strings.Contains(mainSrc, `MetricsDescriptor("`+name+`")`) {
			t.Errorf("main.go 未观察全局中间件 %q | main.go does not observe global middleware %q", name, name)
		}
	}
	if !strings.Contains(pr.read("internal/middleware/observability.go"), "func MetricsDescriptor") {
		t.Error("observability.go 缺少 MetricsDescriptor 入口 | observability.go lacks the MetricsDescriptor entry point")
	}
	controllerSrc := pr.read("internal/controller/controller.go")
	if !strings.Contains(controllerSrc, "metricsx.Provider") || !strings.Contains(controllerSrc, "metricsx.NewCollector") {
		t.Error("controller 未通过指标抽象组合来源 | the controller does not compose sources through the metrics abstraction")
	}
	if !strings.Contains(pr.read("internal/controller/middleware_metrics.go"), "metricsx.MiddlewareSnapshot") {
		t.Error("middleware controller 未消费中间件快照 | the middleware controller does not consume middleware snapshots")
	}

	// 系统资源同样只能经统一抽象取得：控制器不得再直连具体采样服务，否则两条数据路径会分叉。
	// System resources go through the same abstraction: the controller must not reach the concrete sampler
	// directly, or the two data paths would diverge.
	homeSrc := pr.read("internal/controller/home.go")
	if strings.Contains(homeSrc, "c.metrics.Snapshot()") {
		t.Error("home.go 仍直连 MetricsService | home.go still reaches MetricsService directly")
	}
	for _, want := range []string{
		"provider.Snapshot().System",
		"func (c *Controller) metricGauges(m metricsx.SystemSnapshot)",
	} {
		if !strings.Contains(homeSrc, want) {
			t.Errorf("home.go 缺少 %q | home.go is missing %q", want, want)
		}
	}
	if strings.Contains(pr.read("internal/controller/controller.go"), "metrics *service.MetricsService") {
		t.Error("controller 仍持有具体指标服务字段 | the controller still holds the concrete metrics service field")
	}
	if !strings.Contains(pr.read("internal/service/metrics.go"), "metricsx.SystemSource") {
		t.Error("metrics service 未实现系统来源接口 | the metrics service does not implement the system source")
	}
}

// TestGeneratedInitialsAreRuneSafe proves the tenant and user avatars derive their initial from a
// rune rather than a byte, so non-ASCII names never produce an unrenderable badge. | TestGeneratedInitialsAreRuneSafe
// 证明租户与用户徽标按 rune 而不是字节取首字符，非 ASCII 名称因此不会渲染出空徽标。
func TestGeneratedInitialsAreRuneSafe(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("initials-app"))
	for _, rel := range []string{"internal/model/tenant.go", "internal/model/user.go"} {
		src := pr.read(rel)
		if strings.Contains(src, "trimmed[:1]") {
			t.Errorf("%s still slices the initial by byte | %s 仍按字节截取首字符", rel, rel)
		}
		if !strings.Contains(src, "utf8.DecodeRuneInString") {
			t.Errorf("%s does not decode the first rune | %s 未按 rune 解码首字符", rel, rel)
		}
	}
}

func TestGenerateResponsiveShellHooks(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("responsive-app"))

	for _, want := range []struct {
		file string
		text string
	}{
		{"web/templates/partials/topbar.html", "data-yd-sidebar-open"},
		{"web/templates/partials/app_shell.html", "data-yd-sidebar-backdrop"},
		{"web/templates/portal/partials/sidebar.html", `id="yd-sidebar"`},
		{"web/templates/partials/base.html", "yd-shell--app"},
		{"web/static/js/yd-app.js", "function setRailOpen"},
		{"web/static/js/yd-app.js", "function swapReplacedRail(root)"},
		{"web/static/css/components.css", "max-width: 63.999rem"},
		{"web/static/css/components.css", "Responsive layout"},
		{"web/static/css/components.css", "padding: clamp(1rem, 0.5rem + 1vw, 4rem);"},
	} {
		if !strings.Contains(pr.read(want.file), want.text) {
			t.Errorf("%s 缺少 %q", want.file, want.text)
		}
	}
}

// TestGeneratedDrawerErrorsStayInsideDrawer 断言抽屉表单的校验错误落在抽屉内部的槽位，
// 而不是被模态遮罩压在下面的页面级 #yd-errors。
// TestGeneratedDrawerErrorsStayInsideDrawer asserts that drawer-form validation errors land in the in-drawer outlet
// instead of the page-level #yd-errors hidden behind the modal backdrop.
func TestGeneratedDrawerErrorsStayInsideDrawer(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("drawer-errors-app"))

	shell := pr.read("web/templates/partials/app_shell.html")
	for _, want := range []string{`id="yd-drawer-errors"`, "yd-errors--drawer"} {
		if !strings.Contains(shell, want) {
			t.Errorf("shell.html 缺少抽屉错误槽位 %q", want)
		}
	}

	drawerForms := []string{
		"web/templates/mgr/partials/users/form.html",
		"web/templates/mgr/partials/users/edit_drawer.html",
		"web/templates/mgr/partials/tenants/form.html",
		"web/templates/mgr/partials/roles/form.html",
		"web/templates/mgr/partials/roles/edit_drawer.html",
		"web/templates/portal/partials/friends/request.html",
		"web/templates/portal/partials/friends/remark.html",
		"web/templates/portal/partials/messages/compose.html",
		"web/templates/portal/partials/messages/group_settings.html",
		"web/templates/mgr/partials/messages/pin.html",
		"web/templates/mgr/partials/message_settings/type_form.html",
		"web/templates/mgr/partials/message_settings/tag_form.html",
	}
	for _, rel := range drawerForms {
		if strings.Contains(pr.read(rel), "target:#yd-errors") {
			t.Errorf("%s 的校验错误仍指向页面级 #yd-errors，会被抽屉遮罩盖住", rel)
		}
	}
	for _, rel := range []string{
		"web/templates/mgr/partials/users/form.html",
		"web/templates/mgr/partials/tenants/form.html",
		"web/templates/mgr/partials/roles/form.html",
	} {
		if !strings.Contains(pr.read(rel), "target:#yd-drawer-errors") {
			t.Errorf("%s 未把校验错误指向抽屉内槽位", rel)
		}
	}
}

// TestGeneratedMessageCentreUsesInlineNewConversationPanel 断言新建会话不再打开抽屉，
// 而是在会话栏内提供「会话 / 联系人」双页签：点好友直接开单聊，勾选多位好友创建群聊。
// TestGeneratedMessageCentreUsesInlineNewConversationPanel asserts that starting a conversation no longer opens a
// drawer: the conversation rail carries "conversations / contacts" tabs, where a friend row opens a direct chat and
// several checked friends create a group.
func TestGeneratedMessageCentreUsesInlineNewConversationPanel(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("message-contacts-app"))

	messages := pr.read("web/templates/portal/pages/messages.html")
	for _, want := range []string{
		`data-yd-msg-tab="conversations"`,
		`data-yd-msg-tab="contacts"`,
		`data-yd-msg-tab-open="contacts"`,
		`partials/messages/contacts`,
		"lucide-message-square",
		"lucide-users",
	} {
		if !strings.Contains(messages, want) {
			t.Errorf("messages.html 缺少 %q", want)
		}
	}
	if strings.Contains(messages, `hx-get="/messages/new"`) {
		t.Error("messages.html 仍从抽屉打开新建会话 | messages.html still opens the create drawer")
	}

	contacts := pr.read("web/templates/portal/partials/messages/contacts.html")
	for _, want := range []string{
		`action="/messages/groups"`,
		`name="group_members"`,
		`"Name" "group_title"`,
		`components.form.field`,
		`href="/messages/direct/`,
		`"ID" "yd-contact-q"`,
		`data-yd-contact-check`,
		`data-yd-contact-submit`,
	} {
		if !strings.Contains(contacts, want) {
			t.Errorf("contacts.html 缺少 %q", want)
		}
	}

	shell := pr.read("web/templates/partials/topbar.html")
	for _, want := range []string{
		`data-yd-user-menu`,
		`href="/messages?folder=unread"`,
		`href="/account"`,
		`partials/messages/unread_badge`,
		`partials/messages/unread_menu_badge`,
		`yd-user-menu__item--unread`,
		`class="yd-menu__item yd-user-menu__item"`,
		"lucide-mail",
		"lucide-user-round",
	} {
		if !strings.Contains(shell, want) {
			t.Errorf("shell.html 缺少 %q", want)
		}
	}
	if !strings.Contains(pr.read("web/templates/partials/messages/unread_menu_badge.html"), `id="yd-unread-menu-badge"`) {
		t.Error("unread_menu_badge.html lacks its own count id | unread_menu_badge.html 缺少独立计数 id")
	}
	// 未读角标必须排在用户名之后、菜单箭头之前，不能盖在头像角上。
	// The unread badge must follow the user name and precede the menu caret instead of covering the avatar corner.
	nameAt := strings.Index(shell, `yd-user__name`)
	badgeAt := strings.Index(shell, `partials/messages/unread_badge`)
	caretAt := strings.Index(shell, `yd-user-menu__caret`)
	if nameAt < 0 || badgeAt < 0 || caretAt < 0 || badgeAt < nameAt || caretAt < badgeAt {
		t.Errorf("用户菜单顺序应为 用户名 → 未读角标 → 箭头，实际 name=%d badge=%d caret=%d", nameAt, badgeAt, caretAt)
	}

	css := pr.read("web/static/css/components.css")
	for _, want := range []string{
		".yd-user .yd-user__badge",
		".yd-menu__item.yd-user-menu__item .yd-icon",
		".yd-user-menu__item--unread:has(.yd-user-menu__badge:not([hidden]))",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("components.css 缺少 %q", want)
		}
	}

	if sidebar := pr.read("web/templates/portal/partials/sidebar.html"); strings.Contains(sidebar, `partials/messages/unread_badge`) {
		t.Error("sidebar.html 仍渲染未读徽标，应与右上角用户名徽标重复 | sidebar.html still renders a second unread badge")
	}

	zh := readCatalogue(t, pr, "web/i18n/zh-CN.yaml")
	en := readCatalogue(t, pr, "web/i18n/en.yaml")
	for _, key := range []string{
		"message.contacts",
		"message.contact_search_label",
		"message.contact_search_placeholder",
		"message.contact_hint",
		"message.select_contact",
		"message.selected",
		"message.create_group",
		"nav.unread_messages",
		"nav.my_profile",
	} {
		if _, ok := zh[key]; !ok {
			t.Errorf("zh-CN.yaml 缺少 %q", key)
		}
		if _, ok := en[key]; !ok {
			t.Errorf("en.yaml 缺少 %q", key)
		}
	}
}

// TestGenerateDamengDSN asserts that the Dameng option emits the dm driver and a DSN that carries
// the framework's UTC preset, so the generated project connects with the same time semantics as the
// other dialects. | TestGenerateDamengDSN 断言达梦选项生成 dm 驱动与带框架 UTC 预置的连接串，
// 使生成项目与其它方言具备相同的时区语义。
func TestGenerateDamengDSN(t *testing.T) {
	o := scaffold.DefaultOptions("dm-app")
	o.DB = scaffold.DBDM
	pr, _ := generateMem(t, o)

	cfg := pr.read("configs/config.yaml")
	if !strings.Contains(cfg, "driver: dm") {
		t.Errorf("config.yaml 缺少 dm 驱动:\n%s", cfg)
	}
	wantDSN := `dm://SYSDBA:SYSDBA@127.0.0.1:5236?timeZone=0`
	if !strings.Contains(cfg, wantDSN) {
		t.Errorf("config.yaml 的达梦连接串缺少 %q:\n%s", wantDSN, cfg)
	}
}

// TestGeneratedUIKitShowsEveryComponent pins the component gallery: every component template the framework ships is
// referenced by the page, the page hand-writes no input or select, and the route plus sidebar entry exist. The only
// exception is the recursive tree helper, which is invoked by components.navigation.tree itself. |
// TestGeneratedUIKitShowsEveryComponent 固定组件示例页：框架提供的每一个组件模板都在页面里出现，页面不手写
// input/select，路由与侧栏入口都存在。唯一例外是递归的树节点片段，它由 components.navigation.tree 自己调用。
func TestGeneratedUIKitShowsEveryComponent(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("ui-kit-app"))

	page := pr.read("web/templates/portal/pages/ui_kit.html")
	if !strings.Contains(page, `data-yd-page="ui-kit"`) {
		t.Error("ui_kit.html lacks its page marker | ui_kit.html 缺少页面标记")
	}
	// The gallery loads EasyMDE as a page script. A shell swap never rewrites <head>, so the page must
	// also declare the node yd-app.js reads after a swap; without it the editor stays a bare textarea
	// until a full reload. | 组件示例页把 EasyMDE 作为页面脚本加载。外壳交换不会重写 <head>，因此
	// 页面还必须声明 yd-app.js 在交换后读取的节点；缺少它时编辑器要整页刷新才会升级。
	for _, want := range []string{`id="yd-page-assets"`, "data-yd-styles=", "data-yd-scripts="} {
		if !strings.Contains(page, want) {
			t.Errorf("ui_kit.html lacks %q; its editor never upgrades after an htmx navigation | ui_kit.html 缺少 %q；htmx 局部导航后编辑器不会升级", want, want)
		}
	}
	for _, unwanted := range []string{"<input", "<select", "<textarea"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("ui_kit.html hand-writes %q; every control must come from components/** | ui_kit.html 手写了 %q；所有控件必须来自 components/**", unwanted, unwanted)
		}
	}

	// form.search ships its own <form>; nesting it inside the demo form makes the browser drop or move nodes
	// (the magnifier is the visible symptom). | form.search 自带 <form>，嵌进演示表单会让浏览器丢弃或移动节点，
	// 放大镜错位就是可见症状。
	searchAt := strings.Index(page, `template "components.form.search"`)
	formClose := strings.Index(page, "</form>")
	if searchAt < 0 || formClose < 0 || searchAt < formClose {
		t.Error("ui_kit.html nests form.search inside the demo form | ui_kit.html 把 form.search 嵌进了演示表单")
	}

	entries, err := afero.ReadDir(web.FS(), "templates/components")
	if err != nil {
		t.Fatalf("reading framework components failed: %v | 读取框架组件失败：%v", err, err)
	}
	defPattern := regexpx.MustCompile(`\{\{-?\s*define\s+"(components\.[^"]+)"`)
	missing := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		body, readErr := afero.ReadFile(web.FS(), "templates/components/"+entry.Name())
		if readErr != nil {
			t.Fatalf("reading components/%s failed: %v | 读取 components/%s 失败：%v", entry.Name(), readErr, entry.Name(), readErr)
		}
		for _, match := range defPattern.FindAllStringSubmatch(string(body), -1) {
			name := match[1]
			if name == "components.navigation.tree.nodes" {
				continue
			}
			if !strings.Contains(page, `template "`+name+`"`) {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("ui_kit.html 未展示组件: %v | the component gallery does not show: %v", missing, missing)
	}

	router := pr.read("internal/router/router.go")
	if !strings.Contains(router, `g.GET("/ui-kit", ctrl.UIKit)`) {
		t.Error("router.go lacks the /ui-kit route | router.go 缺少 /ui-kit 路由")
	}
	sidebar := pr.read("web/templates/portal/partials/sidebar.html")
	if !strings.Contains(sidebar, `data-yd-nav="ui-kit"`) || !strings.Contains(sidebar, `tr "nav.ui_kit"`) {
		t.Error("portal sidebar lacks the component gallery entry | 门户侧栏缺少组件示例入口")
	}
	handler := pr.read("internal/controller/ui_kit.go")
	if !strings.Contains(handler, "http/chart") || !strings.Contains(handler, "ChartSVG") {
		t.Error("ui_kit.go must inject a server-rendered chart | ui_kit.go 必须注入服务端图表")
	}
}

// TestCaptchaOptionWritesConfiguration asserts the generation switch reaches the config, the env template and
// the project manifest. | TestCaptchaOptionWritesConfiguration 断言生成开关写入配置、环境变量模板与工程清单。
func TestCaptchaOptionWritesConfiguration(t *testing.T) {
	base := scaffold.DefaultOptions(demoName)

	disabled, _ := generateMem(t, base)
	disabledConfig := disabled.read("configs/config.yaml")
	for _, want := range []string{
		"captcha:", "login: false", "password_change: false", `store: auto`, `fallback: memory`,
		`types: [text, rotate]`, `trusted_proxies: []`,
	} {
		if !strings.Contains(disabledConfig, want) {
			t.Errorf("默认配置缺少 %q:\n%s", want, disabledConfig)
		}
	}
	if strings.Contains(disabled.read(".yd.json"), `"captcha": true`) {
		t.Error("default .yd.json must not mark captcha as enabled | 默认生成的 .yd.json 不应把 captcha 标记为开启")
	}

	enabledOptions := base
	enabledOptions.Captcha = true
	enabled, _ := generateMem(t, enabledOptions)

	enabledConfig := enabled.read("configs/config.yaml")
	for _, want := range []string{"login: true", "password_change: true"} {
		if !strings.Contains(enabledConfig, want) {
			t.Errorf("--captcha 配置缺少 %q:\n%s", want, enabledConfig)
		}
	}
	env := enabled.read(".env.template")
	for _, want := range []string{"# YD_CAPTCHA_LOGIN=true", "# YD_CAPTCHA_PASSWORD_CHANGE=true", "YD_CAPTCHA_SECRET", "YD_CAPTCHA_TRUSTED_PROXIES"} {
		if !strings.Contains(env, want) {
			t.Errorf("--captcha 环境模板缺少 %q:\n%s", want, env)
		}
	}
	for _, forbidden := range []string{"\nYD_CAPTCHA_LOGIN=", "\nYD_CAPTCHA_PASSWORD_CHANGE=", "\nYD_CAPTCHA_STORE=", "\nYD_CAPTCHA_FALLBACK="} {
		if strings.Contains(env, forbidden) {
			t.Errorf(".env.template must not actively override config.yaml with %q; a copied .env would silently win:\n%s", forbidden, env)
		}
	}
	if !strings.Contains(enabled.read(".yd.json"), `"captcha": true`) {
		t.Error("--captcha .yd.json must mark captcha as enabled | --captcha 生成的 .yd.json 应标记 captcha 开启")
	}
	if !strings.Contains(enabled.read("internal/meta/meta.go"), `"captcha"`) {
		t.Error("--captcha meta.Features must contain captcha | --captcha 生成的 meta.Features 应包含 captcha")
	}
	if !strings.Contains(enabled.read("README.md"), "YD_CAPTCHA_SECRET=<至少 16 字节的随机值>") {
		t.Error("--captcha README must put YD_CAPTCHA_SECRET into the systemd env block | --captcha 的 README 应把 YD_CAPTCHA_SECRET 写进 systemd 环境块")
	}
	if strings.Contains(disabled.read("README.md"), "YD_CAPTCHA_SECRET=<至少 16 字节的随机值>") {
		t.Error("a project generated without captcha must not require YD_CAPTCHA_SECRET in the systemd env block")
	}
}

// TestGeneratedProjectWiresCaptcha asserts the router, controller and both handlers carry the captcha chain.
// | TestGeneratedProjectWiresCaptcha 断言路由、控制器与两个处理器都带上验证码链路。
func TestGeneratedProjectWiresCaptcha(t *testing.T) {
	o := scaffold.DefaultOptions("captcha-app")
	o.Captcha = true
	pr, _ := generateMem(t, o)

	router := pr.read("internal/router/router.go")
	for _, want := range []string{
		`"github.com/shibingli/yd/security/captcha"`,
		"captcha.NewWithClient(a.Config(), a.Logger(), redisClient)",
		"captchaSvc.Register(a.Router())",
		"ErrCaptcha",
		"controller.New(a, store, a.Logger(), captchaSvc, redisClient)",
	} {
		if !strings.Contains(router, want) {
			t.Errorf("router.go 缺少 %q:\n%s", want, router)
		}
	}

	ctrl := pr.read("internal/controller/controller.go")
	for _, want := range []string{
		`"github.com/shibingli/yd/security/captcha"`,
		"captcha *captcha.Service",
		`"CaptchaLogin"`,
		`"CaptchaPasswordChange"`,
		`"CaptchaEndpoint"`,
		`"CaptchaTokenField"`,
	} {
		if !strings.Contains(ctrl, want) {
			t.Errorf("controller.go 缺少 %q:\n%s", want, ctrl)
		}
	}

	auth := pr.read("internal/controller/auth_controller.go")
	for _, want := range []string{
		"captcha.Token(ctx)",
		"captcha.Verify(ctx.Request.Context(), captcha.Token(ctx), captcha.ActionLogin)",
		"captcha.Verify(ctx.Request.Context(), captcha.Token(ctx), captcha.ActionPasswordChange)",
	} {
		if !strings.Contains(auth, want) {
			t.Errorf("auth_controller.go 缺少 %q", want)
		}
	}
	loginVerify := strings.Index(auth, "captcha.ActionLogin")
	loginCall := strings.Index(auth, "c.auth.Login(")
	if loginVerify < 0 || loginCall < 0 || loginVerify > loginCall {
		t.Errorf("登录处理器必须在 c.auth.Login 之前校验验证码（verify=%d call=%d）", loginVerify, loginCall)
	}
	changeVerify := strings.Index(auth, "captcha.ActionPasswordChange")
	changeCall := strings.Index(auth, "c.auth.ChangePassword(")
	if changeVerify < 0 || changeCall < 0 || changeVerify > changeCall {
		t.Errorf("改密处理器必须在 c.auth.ChangePassword 之前校验验证码（verify=%d call=%d）", changeVerify, changeCall)
	}
}

// TestGeneratedProjectRendersCaptchaWidget asserts the shared partial is wired into both pages, the shared script is
// loaded in the documented order and the two catalogues carry every captcha key. |
// TestGeneratedProjectRendersCaptchaWidget 断言共享片段接入两个页面、共享脚本按既定顺序加载，且两份目录都含有全部验证码键。
func TestGeneratedProjectRendersCaptchaWidget(t *testing.T) {
	o := scaffold.DefaultOptions("captcha-widget")
	o.Captcha = true
	pr, _ := generateMem(t, o)

	partial := pr.read("web/templates/partials/captcha.html")
	for _, want := range []string{
		"<dialog", "data-yd-captcha", "data-yd-captcha-dialog", "data-yd-captcha-host",
		`name="{{ .Field }}"`, `tr "captcha.required"`, `tr "captcha.noscript"`,
		"yd-banner--error", `aria-describedby=`, "hx-preserve", "data-yd-captcha-cancel",
	} {
		if !strings.Contains(partial, want) {
			t.Errorf("captcha.html 缺少 %q:\n%s", want, partial)
		}
	}
	if strings.Contains(partial, `aria-required="true"`) {
		t.Error("aria-required is not supported on role=group; the adapter marks the SDK answer control instead")
	}
	if strings.Contains(partial, "yd-field--required") {
		t.Error("the captcha dialog must not reserve a required field row in the form | 验证码对话框不得在表单里占用必填字段行")
	}
	hintAt := strings.Index(partial, `id="{{ .InputID }}-hint"`)
	hostAt := strings.Index(partial, `data-yd-captcha-host`)
	if hintAt < 0 || hostAt < 0 || hintAt > hostAt {
		t.Error("captcha.html must render the hint above the widget it points at | 提示必须位于它所指的组件上方")
	}

	login := pr.read("web/templates/portal/partials/login.html")
	for _, want := range []string{`template "partials/captcha"`, ".CaptchaLogin", ".CaptchaLoginAction", `hx "preserve" "true"`} {
		if !strings.Contains(login, want) {
			t.Errorf("login.html 缺少 %q:\n%s", want, login)
		}
	}
	account := pr.read("web/templates/portal/pages/account.html")
	for _, want := range []string{`template "partials/captcha"`, ".CaptchaPasswordChange", ".CaptchaPasswordChangeAction"} {
		if !strings.Contains(account, want) {
			t.Errorf("account.html 缺少 %q:\n%s", want, account)
		}
	}

	base := pr.read("web/templates/partials/base.html")
	captchaAt := strings.Index(base, `{{ asset "/static/js/yd-captcha.js" }}`)
	appAt := strings.Index(base, `{{ asset "/static/js/yd-app.js" }}`)
	if captchaAt < 0 || appAt < 0 || captchaAt > appAt {
		t.Errorf("base.html 必须在 yd-app.js 之前加载 yd-captcha.js（captcha=%d app=%d）", captchaAt, appAt)
	}

	for _, rel := range []string{"web/i18n/zh-CN.yaml", "web/i18n/en.yaml"} {
		catalogue := pr.read(rel)
		for _, key := range []string{"captcha:", "hint:", "required:", "failed:", "unavailable:", "noscript:"} {
			if !strings.Contains(catalogue, key) {
				t.Errorf("%s 缺少验证码键 %q", rel, key)
			}
		}
	}
}

// captchaProbeConfig enables exactly one action per probe so each subtest exercises one path.
// | captchaProbeConfig 每个探针只开启一个动作，使每个子测试只覆盖一条路径。
func captchaProbeConfig(login, passwordChange bool) string {
	return "captcha:\n" +
		"  login: " + strconv.FormatBool(login) + "\n" +
		"  password_change: " + strconv.FormatBool(passwordChange) + "\n" +
		"  store: memory\n" +
		"  secret: \"0123456789abcdef0123456789abcdef\"\n"
}

// writeProbeConfig writes configs/config.local.yaml, the documented local override layer.
// | writeProbeConfig 写入 configs/config.local.yaml，也就是文档化的本地覆盖层。
func writeProbeConfig(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, "configs", "config.local.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入本地配置失败: %v", err)
	}
}

// TestGeneratedWorkflowAPIUnavailableIsJSON proves a project generated without a database answers the workflow
// JSON API with a JSON 503 instead of an HTML error fragment. |
// TestGeneratedWorkflowAPIUnavailableIsJSON 证明没有数据库的生成项目对流程 JSON API 返回 JSON 503，
// 而不是 HTML 错误片段。
func TestGeneratedWorkflowAPIUnavailableIsJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过流程 API 不可用验证 | skipped in short mode")
	}
	_, base := startProbeApp(t, "yd-workflow-nodb-probe")
	admin := newProbeClient(t, httpTimeout)
	token := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
	resp, body := getText(t, admin, base+"/api/v1/workflows/definitions", map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/json",
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("无数据库项目的流程 API 状态码 = %d，期望 503\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		t.Fatalf("无数据库项目的流程 API Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil || payload["error"] == nil || payload["error"] == "" {
		t.Fatalf("流程 API 错误体不是 JSON: %v\n%s", err, body)
	}
}

// TestGeneratedWorkflowLifecycle proves the generated project wires the process engine end to end: definitions,
// versions, publication, the portal start form, task approval, the designer page, groups and a valid OpenAPI
// document. | TestGeneratedWorkflowLifecycle 证明生成项目完整接通流程引擎：定义、版本、发布、门户发起、
// 任务审批、设计器页面、流程组与合法的 OpenAPI 文档。
func TestGeneratedWorkflowLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过流程引擎端到端验证 | skipped in short mode")
	}

	_, base := startProbeAppDBWith(t, "yd-workflow-probe", scaffold.DBSQLite, nil)
	admin := newProbeClient(t, httpTimeout)
	token := signIn(t, admin, base, probeTenant, probeAdmin, probeAdminPassword)
	auth := map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"}

	resp, body := postJSON(t, admin, base+"/api/v1/workflows/definitions", `{
		"key": "leave", "name": "Leave request", "name_en": "Leave request", "category": "hr"
	}`, auth)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("创建流程定义状态码 = %d\n%s", resp.StatusCode, body)
	}
	var definition struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &definition); err != nil || definition.ID == "" {
		t.Fatalf("解析流程定义失败: %v\n%s", err, body)
	}

	design := `{
		"schema_version": 1,
		"nodes": [
			{"id": "start_1", "type": "start", "name": "Start", "position": {"x": 80, "y": 200}},
			{"id": "user_1", "type": "user", "name": "Manager approval", "position": {"x": 280, "y": 180},
			 "assignee": {"type": "initiator"}, "completion": {"mode": "any"}},
			{"id": "end_1", "type": "end", "name": "End", "position": {"x": 560, "y": 200}}
		],
		"edges": [
			{"id": "edge_1", "source": "start_1", "target": "user_1"},
			{"id": "edge_2", "source": "user_1", "target": "end_1"}
		],
		"forms": {"start": [{"key": "days", "label": "Days", "type": "number", "required": true}]}
	}`
	resp, body = postJSON(t, admin, base+"/api/v1/workflows/definitions/"+definition.ID+"/versions",
		`{"design": `+design+`, "note": "draft"}`, auth)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建流程版本状态码 = %d\n%s", resp.StatusCode, body)
	}
	var version struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &version); err != nil || version.ID == "" {
		t.Fatalf("解析流程版本失败: %v\n%s", err, body)
	}
	resp, body = postJSON(t, admin, base+"/api/v1/workflows/versions/"+version.ID+"/publish", `{"note": "release-note-xyz"}`, auth)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("发布流程版本状态码 = %d\n%s", resp.StatusCode, body)
	}

	// The version list is a lightweight summary; the design is read one version at a time. |
	// 版本列表只返回轻量摘要；设计按单个版本读取。
	resp, body = getText(t, admin, base+"/api/v1/workflows/definitions/"+definition.ID+"/versions", auth)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"status":"published"`) {
		t.Fatalf("API 版本摘要状态码 = %d\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, `"design"`) || strings.Contains(body, `"bpmn"`) {
		t.Fatalf("API 版本列表仍返回设计或 BPMN 文档:\n%s", body)
	}
	resp, body = getText(t, admin, base+"/api/v1/workflows/versions/"+version.ID, auth)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"design"`) {
		t.Fatalf("API 单版本读取缺少设计: 状态码 = %d\n%s", resp.StatusCode, body)
	}

	resp, body = getText(t, admin, base+"/mgr/workflows/"+definition.ID+"/versions", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "release-note-xyz") {
		t.Fatalf("版本抽屉状态码 = %d, 期望 200 且包含发布说明\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "data-yd-drawer-persist") {
		t.Fatalf("版本抽屉的写操作会在成功后关闭抽屉:\n%s", body)
	}

	resp, body = getText(t, admin, base+"/mgr/workflows", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("流程列表状态码 = %d\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, "Unpublished draft") || strings.Contains(body, "有未发布草稿") {
		t.Fatalf("没有草稿时列表仍显示草稿标记:\n%s", body)
	}

	resp, body = getText(t, admin, base+"/mgr/workflows?q=Leave", map[string]string{
		"HX-Request": "true", "HX-Target": "yd-workflow-definitions",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("流程列表片段状态码 = %d\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Fatalf("流程列表片段未返回 OOB 总数或误返回整页:\n%s", body)
	}

	// Category filters are shared by the HTML workbench and the JSON API. | 分类筛选在 HTML 工作台与 JSON API 之间共用。
	resp, body = getText(t, admin, base+"/mgr/workflows?category=hr", map[string]string{
		"HX-Request": "true", "HX-Target": "yd-workflow-definitions",
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Leave request") {
		t.Fatalf("按分类筛选流程列表状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/mgr/workflows?category=finance", map[string]string{
		"HX-Request": "true", "HX-Target": "yd-workflow-definitions",
	})
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "Leave request") {
		t.Fatalf("不匹配分类的流程列表状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/workflows/definitions?category=hr", auth)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"key":"leave"`) {
		t.Fatalf("API 分类筛选状态码 = %d\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"version_count":1`) {
		t.Fatalf("定义列表的版本计数未批量填充:\n%s", body)
	}
	resp, body = getText(t, admin, base+"/api/v1/workflows/definitions?category=finance", auth)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, `"key":"leave"`) {
		t.Fatalf("API 分类筛选未生效状态码 = %d\n%s", resp.StatusCode, body)
	}

	// A draft version can be deleted through the API, matching the version drawer. |
	// 草稿版本可以经 API 删除，与版本抽屉保持一致。
	resp, body = postJSON(t, admin, base+"/api/v1/workflows/definitions/"+definition.ID+"/versions",
		`{"design": `+design+`, "note": "disposable"}`, auth)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建待删除草稿状态码 = %d\n%s", resp.StatusCode, body)
	}
	var disposable struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &disposable); err != nil || disposable.ID == "" {
		t.Fatalf("解析待删除草稿失败: %v\n%s", err, body)
	}
	resp, body = deleteJSON(t, admin, base+"/api/v1/workflows/versions/"+disposable.ID, auth)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("删除草稿状态码 = %d, 期望 204\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/workflows/definitions/"+definition.ID+"/versions", auth)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, disposable.ID) {
		t.Fatalf("删除后版本列表状态码 = %d, 期望 200 且不含 %s\n%s", resp.StatusCode, disposable.ID, body)
	}

	resp, body = postJSON(t, admin, base+"/api/portal/v1/workflows/instances",
		`{"definition_key": "leave", "title": "Two days off", "form_data": {"days": 2}}`, auth)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("发起流程状态码 = %d\n%s", resp.StatusCode, body)
	}
	var instance struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &instance); err != nil || instance.ID == "" || instance.Status != "running" {
		t.Fatalf("发起后的实例 = %+v err=%v\n%s", instance, err, body)
	}

	// The instance category is inherited from the definition and filters both surfaces. |
	// 实例分类继承自定义，并同时作用于两个界面。
	resp, body = getText(t, admin, base+"/mgr/workflow-instances?category=hr", map[string]string{
		"HX-Request": "true", "HX-Target": "yd-workflow-instances",
	})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Two days off") {
		t.Fatalf("按分类筛选实例列表状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/mgr/workflow-instances?category=finance", map[string]string{
		"HX-Request": "true", "HX-Target": "yd-workflow-instances",
	})
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "Two days off") {
		t.Fatalf("不匹配分类的实例列表状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/workflow-instances?category=hr", auth)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Two days off") {
		t.Fatalf("API 实例分类筛选状态码 = %d\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, `"variables"`) || strings.Contains(body, `"current_nodes"`) {
		t.Fatalf("实例列表仍返回变量或节点快照载荷:\n%s", body)
	}
	resp, body = getText(t, admin, base+"/api/v1/workflow-instances?category=finance", auth)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "Two days off") {
		t.Fatalf("API 实例分类筛选未生效状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/workflows/definitions?category=hr", auth)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"instance_count":1`) {
		t.Fatalf("定义列表的实例计数未批量填充: 状态码 = %d\n%s", resp.StatusCode, body)
	}

	resp, body = getText(t, admin, base+"/api/portal/v1/workflows/tasks", auth)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("任务列表状态码 = %d\n%s", resp.StatusCode, body)
	}
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page.Items) == 0 {
		t.Fatalf("任务列表 = %s err=%v", body, err)
	}
	resp, body = getText(t, admin, base+"/mgr/workflow-tasks/"+page.Items[0].ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("管理端任务抽屉状态码 = %d\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `hx-post="/mgr/workflow-tasks/`+page.Items[0].ID+`/action"`) {
		t.Fatalf("the admin task drawer lacks its decision form: \n%s", body)
	}
	resp, body = getText(t, admin, base+"/workflow/tasks/"+page.Items[0].ID, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `hx-post="/workflow/tasks/`+page.Items[0].ID+`/action"`) {
		t.Fatalf("门户任务抽屉状态码 = %d, 期望 200 且含审批表单\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/mgr/workflow-instances/"+instance.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("管理端实例抽屉状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/workflow/instances/"+instance.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("门户实例抽屉状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = postJSON(t, admin, base+"/api/portal/v1/workflows/tasks/"+page.Items[0].ID+"/approve",
		`{"action": "approve", "comment": "ok", "form_data": {"days": 2}}`, auth)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("审批任务状态码 = %d\n%s", resp.StatusCode, body)
	}

	resp, body = getText(t, admin, base+"/api/portal/v1/workflows/instances/"+instance.ID, auth)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("实例详情状态码 = %d\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"variables"`) {
		t.Fatalf("实例详情缺少变量载荷:\n%s", body)
	}
	var detail struct {
		Instance struct {
			Status  string `json:"status"`
			Outcome string `json:"outcome"`
		} `json:"instance"`
	}
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatalf("解析实例详情失败: %v\n%s", err, body)
	}
	if detail.Instance.Status != "completed" || detail.Instance.Outcome != "approved" {
		t.Fatalf("审批后的实例状态 = %+v\n%s", detail.Instance, body)
	}

	resp, body = getText(t, admin, base+"/api/v1/workflows/export?format=json&keys=leave", auth)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"leave"`) {
		t.Fatalf("JSON 导出状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/workflows/export?format=bpmn&version="+version.ID, auth)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "<definitions") {
		t.Fatalf("BPMN 导出状态码 = %d\n%s", resp.StatusCode, body)
	}

	// The start drawer renders the published start form through the shared field partial. |
	// 发起抽屉经共享字段片段渲染已发布版本的发起表单。
	resp, body = getText(t, admin, base+"/workflow/start?definition=leave", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `name="field_days"`) {
		t.Fatalf("发起抽屉状态码 = %d, 期望 200 且包含发起表单字段\n%s", resp.StatusCode, body)
	}

	// The HTML start form and the HTML approval action share the same service. | HTML 发起表单与审批动作共用同一份服务。
	resp, body = postForm(t, admin, base+"/workflow/start", map[string]string{
		"definition": "leave", "title": "HTML instance", "field_days": "3",
	}, nil)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("HTML 发起流程状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/workflow/tasks", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "HTML instance") {
		t.Fatalf("门户任务页状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/portal/v1/workflows/tasks", auth)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTML 实例任务列表状态码 = %d\n%s", resp.StatusCode, body)
	}
	page.Items = nil
	if err := json.Unmarshal([]byte(body), &page); err != nil || len(page.Items) == 0 {
		t.Fatalf("HTML 实例任务列表 = %s err=%v", body, err)
	}
	resp, body = postForm(t, admin, base+"/workflow/tasks/"+page.Items[0].ID+"/action", map[string]string{
		"decision": "approve", "comment": "ok", "field_days": "3",
	}, nil)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("HTML 审批状态码 = %d\n%s", resp.StatusCode, body)
	}

	// A group with a linked role exercises the workflow-owned directory. | 带关联角色的流程组覆盖流程自有目录。
	resp, body = postJSON(t, admin, base+"/api/v1/workflows/groups",
		`{"key": "approvers", "name": "Approvers", "role": "member"}`, auth)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("创建流程组状态码 = %d\n%s", resp.StatusCode, body)
	}
	var workflowGroup struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &workflowGroup); err != nil || workflowGroup.ID == "" {
		t.Fatalf("解析流程组失败: %v\n%s", err, body)
	}
	resp, body = getText(t, admin, base+"/mgr/workflow-groups/"+workflowGroup.ID+"/members", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `hx-post="/mgr/workflow-groups/`+workflowGroup.ID+`/members"`) {
		t.Fatalf("管理员组成员抽屉状态码 = %d, 期望 200 且含保存表单\n%s", resp.StatusCode, body)
	}

	// The create form's status field must reach the service, not be forced to enabled. |
	// 新建表单的状态字段必须传给服务，而不是被强制为 enabled。
	resp, body = postForm(t, admin, base+"/mgr/workflow-groups", map[string]string{
		"key": "disabled-group", "name": "Disabled group", "status": "disabled",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("新建停用组状态码 = %d\n%s", resp.StatusCode, body)
	}
	resp, body = getText(t, admin, base+"/api/v1/workflows/groups?q=disabled-group", auth)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"status":"disabled"`) {
		t.Fatalf("新建组未按表单保存停用状态: 状态码 = %d\n%s", resp.StatusCode, body)
	}

	// Every create/edit/import drawer renders its shared components. | 每个新建/编辑/导入抽屉都经共享组件渲染。
	for _, path := range []string{
		"/mgr/workflows/new",
		"/mgr/workflows/" + definition.ID + "/edit",
		"/mgr/workflows/import",
		"/mgr/workflow-groups/new",
		"/mgr/workflow-groups/" + workflowGroup.ID + "/edit",
	} {
		resp, body = getText(t, admin, base+path, nil)
		if resp.StatusCode != http.StatusOK || strings.Contains(body, `data-yd-page="error"`) {
			t.Fatalf("抽屉 %s 状态码 = %d\n%s", path, resp.StatusCode, body)
		}
	}

	for _, page := range []string{
		"/mgr/workflows",
		"/mgr/workflows/" + definition.ID + "/design",
		"/mgr/workflows/" + definition.ID + "/versions",
		"/mgr/workflow-instances",
		"/mgr/workflow-groups",
		"/workflow/tasks",
	} {
		resp, body = getText(t, admin, base+page, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("页面 %s 状态码 = %d\n%s", page, resp.StatusCode, body)
		}
		if strings.Contains(body, `data-yd-page="error"`) {
			t.Fatalf("页面 %s 渲染了错误页:\n%s", page, body)
		}
	}

	// The designer script owns no copy: its label island must be valid JSON translated from the
	// catalogue, not a raw key. | 设计器脚本不自带文案：目录岛必须是经目录翻译的合法 JSON，
	// 而不是裸键。
	resp, body = getText(t, admin, base+"/mgr/workflows/"+definition.ID+"/design", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("设计器页面状态码 = %d\n%s", resp.StatusCode, body)
	}
	const labelMarker = `id="yd-workflow-labels">`
	labelStart := strings.Index(body, labelMarker)
	if labelStart < 0 {
		t.Fatalf("设计器页面缺少目录岛:\n%s", body)
	}
	labelStart += len(labelMarker)
	labelEnd := strings.Index(body[labelStart:], "</script>")
	if labelEnd < 0 {
		t.Fatalf("设计器目录岛没有结束标签:\n%s", body[labelStart:])
	}
	var designerLabels map[string]string
	if err := json.Unmarshal([]byte(body[labelStart:labelStart+labelEnd]), &designerLabels); err != nil {
		t.Fatalf("设计器目录岛不是合法 JSON: %v\n%s", err, body[labelStart:labelStart+labelEnd])
	}
	for _, key := range []string{"label_start", "label_zoom", "label_validation_node_no_name"} {
		if value := designerLabels[key]; value == "" || value == key {
			t.Fatalf("设计器目录 %s = %q，期望已翻译文案\n%s", key, value, body[labelStart:labelStart+labelEnd])
		}
	}

	// A workflow:read role may inspect every workflow surface but must not be offered manage-only
	// links or action forms that its endpoints would refuse. | workflow:read 角色可以查看全部流程面，
	// 但不应看到只读端点会拒绝的管理链接或动作表单。
	createProbeRole(t, admin, base, token, "workflow-auditor", authz.PermWorkflowRead)
	createProbeUser(t, admin, base, token, "probe-workflow-reader", "workflow-auditor", "probe-workflow-reader@example.com")
	reader := newProbeClient(t, httpTimeout)
	signIn(t, reader, base, probeTenant, "probe-workflow-reader", probeUserPassword)

	resp, body = getText(t, reader, base+"/mgr/workflows", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("只读角色访问流程列表状态码 = %d\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Unpublished draft") && !strings.Contains(body, "有未发布草稿") {
		t.Errorf("设计器创建草稿后列表未显示草稿标记:\n%s", body)
	}
	if !strings.Contains(body, `href="/mgr/workflows/export?format=json&keys=leave"`) {
		t.Errorf("只读角色看不到 JSON 导出链接:\n%s", body)
	}
	if strings.Contains(body, `href="/mgr/workflows/`+definition.ID+`/design"`) {
		t.Errorf("只读角色的定义列表仍渲染了设计器链接:\n%s", body)
	}
	resp, body = getText(t, reader, base+"/mgr/workflows/"+definition.ID+"/design", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("只读角色访问设计器状态码 = %d, 期望 403\n%s", resp.StatusCode, body)
	}

	resp, body = getText(t, reader, base+"/mgr/workflow-tasks/"+page.Items[0].ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("只读角色访问管理端任务抽屉状态码 = %d\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, `hx-post="/mgr/workflow-tasks/`) {
		t.Errorf("只读角色的任务抽屉渲染了管理动作表单:\n%s", body)
	}
	resp, body = postForm(t, reader, base+"/mgr/workflow-tasks/"+page.Items[0].ID+"/action",
		map[string]string{"decision": "approve"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("只读角色提交管理端任务动作状态码 = %d, 期望 403\n%s", resp.StatusCode, body)
	}

	resp, body = getText(t, reader, base+"/mgr/workflow-groups/"+workflowGroup.ID+"/members", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("只读角色访问组成员抽屉状态码 = %d\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, `hx-post="/mgr/workflow-groups/`) {
		t.Errorf("只读角色的组成员抽屉渲染了保存表单:\n%s", body)
	}

	resp, body = getText(t, admin, base+"/api/v1/openapi.json", auth)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OpenAPI 文档状态码 = %d\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, "#/components/schemas/workflow.") {
		t.Fatalf("OpenAPI 文档含无法解析的流程类型引用:\n%.2000s", body)
	}
	if !strings.Contains(body, "/api/v1/workflows/definitions") {
		t.Fatalf("OpenAPI 文档缺少流程定义端点:\n%.2000s", body)
	}
}

// postJSON submits one JSON body and returns the response with its body as text. | postJSON 提交一个 JSON 请求体，
// 返回响应与响应体文本。
// deleteJSON sends one DELETE with the given headers and returns the response and body. | deleteJSON
// 发送一次带指定请求头的 DELETE 并返回响应与正文。
func deleteJSON(t *testing.T, client *http.Client, target string, headers ...map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, target, nil)
	if err != nil {
		t.Fatalf("create DELETE %s: %v | 创建 DELETE 请求失败", target, err)
	}
	for _, set := range headers {
		for key, value := range set {
			req.Header.Set(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v | 发送 DELETE 失败", target, err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read DELETE body %s: %v | 读取 DELETE 响应失败", target, err)
	}
	return resp, string(data)
}

func postJSON(t *testing.T, client *http.Client, target, body string, headers ...map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("构造 JSON 请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, set := range headers {
		for name, value := range set {
			req.Header.Set(name, value)
		}
	}
	return doText(t, client, req)
}

// TestGeneratedAppSupportsCaptcha proves the generated project can enable sign-in verification and password-change
// verification at runtime, serves the same-origin SDK, and refuses both actions without a valid token. |
// TestGeneratedAppSupportsCaptcha 证明生成项目可以在运行期开启登录与改密验证、提供同源 SDK，并在没有有效令牌时拒绝两个动作。
func TestGeneratedAppSupportsCaptcha(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过真实验证码验证 | skipped in short mode")
	}

	t.Run("登录验证", func(t *testing.T) {
		_, base := startProbeAppWith(t, "yd-captcha-login-probe", func(dir string) {
			writeProbeConfig(t, dir, captchaProbeConfig(true, false))
		})
		client := newProbeClient(t, httpTimeout)

		respChallenge, challenge := postJSON(t, client, base+"/captcha/challenge", `{"action":"login"}`)
		if respChallenge.StatusCode != http.StatusOK {
			t.Fatalf("POST /captcha/challenge 状态码 = %d, 期望 200\n%s", respChallenge.StatusCode, challenge)
		}
		if !strings.Contains(challenge, `"id"`) {
			t.Errorf("challenge 响应缺少 id:\n%s", challenge)
		}

		respAsset, asset := getText(t, client, base+"/captcha/assets/yd-captcha.js", nil)
		if respAsset.StatusCode != http.StatusOK || !strings.Contains(asset, "YdCaptcha") {
			t.Fatalf("GET /captcha/assets/yd-captcha.js 状态码 = %d, 期望 200 且包含 YdCaptcha\n%s",
				respAsset.StatusCode, asset)
		}
		respAdapter, adapter := getText(t, client, base+"/static/js/yd-captcha.js", nil)
		if respAdapter.StatusCode != http.StatusOK || !strings.Contains(adapter, "YdCaptcha.mount") {
			t.Fatalf("GET /static/js/yd-captcha.js 状态码 = %d, 期望 200 且包含 YdCaptcha.mount；框架静态树未合并\n%s",
				respAdapter.StatusCode, adapter)
		}

		respLogin, login := getText(t, client, base+"/login", nil)
		if respLogin.StatusCode != http.StatusOK {
			t.Fatalf("GET /login 状态码 = %d, 期望 200\n%s", respLogin.StatusCode, login)
		}
		for _, want := range []string{
			"data-yd-captcha", "data-yd-captcha-dialog", "data-yd-captcha-cancel",
			"yd-login-captcha-token", "/static/js/yd-captcha.js", "<noscript",
		} {
			if !strings.Contains(login, want) {
				t.Errorf("登录页缺少 %q:\n%s", want, login)
			}
		}
		if strings.Contains(login, "yd-field--required yd-field--captcha") {
			t.Errorf("登录页仍渲染了常驻的验证码字段:\n%s", login)
		}

		for name, token := range map[string]string{
			"缺少令牌": "",
			"伪造令牌": "forged-token",
		} {
			form := map[string]string{"username": probeAdmin, "password": probeAdminPassword}
			if token != "" {
				form["captcha_token"] = token
			}
			resp, body := postForm(t, client, base+"/login", form, map[string]string{"X-YD-Encrypted": "sm2"})
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("%s POST /login 状态码 = %d, 期望 422\n%s", name, resp.StatusCode, body)
			}
			if !strings.Contains(body, "Verification failed") {
				t.Errorf("%s 的 422 未回显验证码失败文案:\n%s", name, body)
			}
			if !strings.Contains(body, "data-yd-captcha") {
				t.Errorf("%s 的 422 未重新渲染验证码组件:\n%s", name, body)
			}
			if cookie := sessionToken(t, client, base); cookie != "" {
				t.Errorf("%s 之后不应写下会话 cookie", name)
			}
		}
	})

	t.Run("改密验证", func(t *testing.T) {
		_, base := startProbeAppWith(t, "yd-captcha-password-probe", func(dir string) {
			writeProbeConfig(t, dir, captchaProbeConfig(false, true))
		})
		client := newProbeClient(t, httpTimeout)
		signIn(t, client, base, probeTenant, probeAdmin, probeAdminPassword)

		respAccount, account := getText(t, client, base+"/account", nil)
		if respAccount.StatusCode != http.StatusOK {
			t.Fatalf("GET /account 状态码 = %d, 期望 200\n%s", respAccount.StatusCode, account)
		}
		for _, want := range []string{
			"data-yd-captcha", "data-yd-captcha-dialog", "data-yd-captcha-cancel",
			"yd-account-captcha-token", "password_change", "<noscript",
		} {
			if !strings.Contains(account, want) {
				t.Errorf("账号页缺少 %q:\n%s", want, account)
			}
		}

		resp, body := postForm(t, client, base+"/account/password", map[string]string{
			"current": probeAdminPassword, "password": "Next@123456", "confirm": "Next@123456",
		}, map[string]string{"X-YD-Encrypted": "sm2"})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("无令牌 POST /account/password 状态码 = %d, 期望 422\n%s", resp.StatusCode, body)
		}
		if !strings.Contains(body, "Verification failed") {
			t.Errorf("改密 422 未回显验证码失败文案:\n%s", body)
		}
	})
}

// TestGeneratedProjectCreatesOneBackendClientPerProcess pins the process-wide singletons: one Redis client, one GORM
// handle, one cache, one message bus, one captcha manager and one controller. Duplicate opens are the usual source of
// pool leaks and double shutdown hooks. | TestGeneratedProjectCreatesOneBackendClientPerProcess 钉住进程级单例：
// 一个 Redis 客户端、一个 GORM 句柄、一个缓存、一个消息总线、一个验证码管理器与一个控制器。重复打开是连接池泄漏与
// 重复关停钩子的常见来源。
func TestGeneratedProjectCreatesOneBackendClientPerProcess(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("single-instance"))

	tokens := []string{
		"redis.Open(", "ydgorm.Open(", "goredis.NewClient(", "redis.NewClient(",
		"repository.Open(", "service.NewRedis(", "service.NewCache(", "service.NewMessageBus(",
		"service.NewCronService(", "captcha.NewWithClient(", "asynq.NewClient(", "asynq.NewServer(", "asynq.NewRegistry(",
		"sse.NewBroker(", "service.NewTicker(", "controller.New(", "service.NewMetricsService(",
		"web.InitBundles(", "i18n.New(", "openapi.ParseSource(", "gormcache.New(", "realtime.NewRedisBus(",
	}
	counts := make(map[string]int, len(tokens))
	err := afero.Walk(pr.fs, pr.name, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		content, readErr := afero.ReadFile(pr.fs, p)
		if readErr != nil {
			return readErr
		}
		for _, token := range tokens {
			counts[token] += strings.Count(string(content), token)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk generated project failed: %v", err)
	}

	want := map[string]int{
		"redis.Open(":                1,
		"ydgorm.Open(":               1,
		"goredis.NewClient(":         0,
		"redis.NewClient(":           0,
		"repository.Open(":           1,
		"service.NewRedis(":          1,
		"service.NewCache(":          1,
		"service.NewMessageBus(":     1,
		"service.NewCronService(":    1,
		"captcha.NewWithClient(":     1,
		"asynq.NewClient(":           1,
		"asynq.NewServer(":           1,
		"asynq.NewRegistry(":         1,
		"sse.NewBroker(":             1,
		"service.NewTicker(":         1,
		"controller.New(":            1,
		"service.NewMetricsService(": 1,
		"web.InitBundles(":           1,
		"i18n.New(":                  1,
		"openapi.ParseSource(":       1,
		"gormcache.New(":             1,
		"realtime.NewRedisBus(":      1,
	}
	for token, expected := range want {
		if got := counts[token]; got != expected {
			t.Errorf("%q occurs %d times, want %d", token, got, expected)
		}
	}

	router := pr.read("internal/router/router.go")
	if got := strings.Count(router, "a.Set(app.ServiceRedis, redisClient)"); got != 1 {
		t.Errorf("a.Set(app.ServiceRedis, redisClient) occurs %d times, want 1", got)
	}
	if got := strings.Count(router, "a.Set(app.ServiceCache, cacheMgr)"); got != 1 {
		t.Errorf("a.Set(app.ServiceCache, cacheMgr) occurs %d times, want 1", got)
	}
	if strings.Contains(router, "return closeCache()") {
		t.Error("the cache manager is closed by App.Set; a second closeCache hook duplicates shutdown")
	}
}

// TestGeneratedConfigDefaultsKoToLocal pins the generated ko.local default: a fresh project has no
// registry, so `yd ko` must be able to load into the local docker daemon without extra flags. |
// TestGeneratedConfigDefaultsKoToLocal 固定生成的 ko.local 默认值：新项目没有仓库，因此 `yd ko` 必须能
// 不带额外参数地载入本地 docker 守护进程。
func TestGeneratedConfigDefaultsKoToLocal(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("ko-local-app"))
	cfg := pr.read("configs/config.yaml")
	inKo := false
	found := false
	for _, line := range strings.Split(cfg, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "ko:" {
			inKo = true
			continue
		}
		if !inKo {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		if !strings.HasPrefix(trimmed, "local:") {
			continue
		}
		found = true
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "local:"))
		if i := strings.IndexByte(value, '#'); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if value != "true" {
			t.Errorf("ko.local = %q，期望 true | want true", value)
		}
		break
	}
	if !found {
		t.Fatal("生成的 config.yaml 缺少 ko.local | the generated config has no ko.local")
	}
}

// TestGeneratedListActionsUseIconMenus pins the generated list/table action convention: the right-hand
// actions are one icon button (lucide-more-horizontal, labelled by ui.actions) that expands a
// details.yd-menu, never a row of text buttons. | TestGeneratedListActionsUseIconMenus 固定生成项目
// 的列表/表格操作约定：右侧操作是一个图标按钮（lucide-more-horizontal，由 ui.actions 提供可访问名称）
// 展开 details.yd-menu，而不是一排文字按钮。
func TestGeneratedListActionsUseIconMenus(t *testing.T) {
	const name = "icon-actions-app"
	pr, res := generateMem(t, scaffold.DefaultOptions(name))
	prefix := name + "/web/templates/"
	checked := 0
	for _, path := range res.Files {
		if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, ".html") {
			continue
		}
		rel := strings.TrimPrefix(path, name+"/")
		content := pr.read(rel)
		if strings.Contains(content, `>{{ tr "ui.actions" }}</summary>`) {
			t.Errorf("%s 仍把 ui.actions 当作按钮文字 | %s still uses ui.actions as summary text", rel, rel)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("没有扫描到生成项目的模板 | no generated templates were scanned")
	}
	for _, rel := range []string{
		"web/templates/mgr/partials/users/row.html",
		"web/templates/mgr/partials/roles/list.html",
		"web/templates/mgr/partials/message_settings/body.html",
	} {
		content := pr.read(rel)
		if !strings.Contains(content, `aria-label="{{ tr "ui.actions" }}"`) {
			t.Errorf("%s 的操作图标缺少 aria-label | %s action icon lacks aria-label", rel, rel)
		}
		if !strings.Contains(content, `{{ template "partials/icon" "lucide-more-horizontal" }}`) {
			t.Errorf("%s 的操作按钮不是 lucide-more-horizontal 图标 | %s action trigger is not the lucide-more-horizontal icon", rel, rel)
		}
	}
	if sprite := pr.read("web/static/img/icons.svg"); !strings.Contains(sprite, `id="i-lucide-more-horizontal"`) {
		t.Error("生成的图标 sprite 缺少 lucide-more-horizontal | the generated icon sprite lacks lucide-more-horizontal")
	}
}

// TestGeneratedGoModPinsWAFWhenEnabled proves the generated go.mod pins both WAF-only modules only
// when the feature is on. | TestGeneratedGoModPinsWAFWhenEnabled 证明生成 go.mod 只在开启时锁定两个
// WAF 专用模块。
func TestGeneratedGoModPinsWAFWhenEnabled(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("waf-pins"))
	gomod := pr.read("go.mod")
	for _, want := range []string{
		"github.com/shibingli/ydx/middleware/waf " + scaffold.WafModuleVersion,
		"github.com/corazawaf/coraza-coreruleset " + scaffold.CRSRulesetVersion,
	} {
		if !strings.Contains(gomod, want) {
			t.Errorf("go.mod 缺少 %q:\n%s", want, gomod)
		}
	}

	o := scaffold.DefaultOptions("waf-off-pins")
	o.WAF = false
	pr, _ = generateMem(t, o)
	gomod = pr.read("go.mod")
	for _, unwanted := range []string{"ydx/middleware/waf", "coraza-coreruleset"} {
		if strings.Contains(gomod, unwanted) {
			t.Errorf("WAF 关闭时 go.mod 不应包含 %q:\n%s", unwanted, gomod)
		}
	}
}

// TestGeneratedWithoutWAFHasNoWAFFiles keeps the opt-out complete. | TestGeneratedWithoutWAFHasNoWAFFiles
// 保证关闭时完全不生成 WAF 包。
func TestGeneratedWithoutWAFHasNoWAFFiles(t *testing.T) {
	o := scaffold.DefaultOptions("waf-off-files")
	o.WAF = false
	_, res := generateMem(t, o)
	for _, f := range res.Files {
		if strings.Contains(f, "/internal/waf/") {
			t.Errorf("WAF 关闭时不应生成 %s", f)
		}
	}
}

// TestLocalReplaceIncludesWAFFromNestedModule keeps the development replace chain complete. |
// TestLocalReplaceIncludesWAFFromNestedModule 保证开发期 replace 链完整。
func TestLocalReplaceIncludesWAFFromNestedModule(t *testing.T) {
	o := scaffold.DefaultOptions("waf-local")
	o.LocalReplace = filepath.ToSlash(filepath.Join("..", "yd"))
	pr, _ := generateMem(t, o)
	gomod := pr.read("go.mod")
	if !strings.Contains(gomod, "replace github.com/shibingli/ydx/middleware/waf =>") {
		t.Errorf("本地替换缺少 WAF 模块:\n%s", gomod)
	}
}

// generateWAFProject lays down one real project with WAF enabled and returns its directory. |
// generateWAFProject 生成一个启用 WAF 的真实项目并返回其目录。
func generateWAFProject(t *testing.T, name string) (goBin, projDir string) {
	t.Helper()
	found, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("未找到 go 工具链，跳过: %v", err)
	}
	dir, err := os.MkdirTemp("", "yd-waf-unit-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	o := scaffold.DefaultOptions(name)
	o.DB = scaffold.DBSQLite
	o.LocalReplace = filepath.ToSlash(repoRoot(t))
	if _, err := scaffold.Generate(context.Background(), o, afs.NewOS(dir), nil); err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	return found, filepath.Join(dir, o.Name)
}

// runGeneratedWAFTests runs the generated package's own tests against the real engine. |
// runGeneratedWAFTests 用真实引擎运行生成包自身的测试。
func runGeneratedWAFTests(t *testing.T, name string) {
	t.Helper()
	goBin, projDir := generateWAFProject(t, name)
	output, err := runGo(goBin, projDir, true, "test", "-mod=mod", "./internal/waf")
	if err != nil {
		t.Logf("GOPROXY=off 测试失败，回退到环境代理重试:\n%s", output)
		output, err = runGo(goBin, projDir, false, "test", "-mod=mod", "./internal/waf")
	}
	if err != nil {
		t.Fatalf("生成的 WAF 包测试失败: %v\n%s", err, output)
	}
}

// TestGeneratedWAFSettingsAndRulesTests covers the generated settings and rule profile. |
// TestGeneratedWAFSettingsAndRulesTests 覆盖生成包的设置与规则画像。
func TestGeneratedWAFSettingsAndRulesTests(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过生成项目测试 | skipped in short mode")
	}
	runGeneratedWAFTests(t, "yd-waf-unit")
}

// TestGeneratedWAFManagerTests covers the generated manager, hot reload and shutdown. |
// TestGeneratedWAFManagerTests 覆盖生成包的管理器、热重载与关停。
func TestGeneratedWAFManagerTests(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过生成项目测试 | skipped in short mode")
	}
	runGeneratedWAFTests(t, "yd-waf-manager")
}

// TestGeneratedProjectWiresWAFInOrder pins the entry-point wiring and its position in the chain. |
// TestGeneratedProjectWiresWAFInOrder 钉住入口接线及其在链中的位置。
func TestGeneratedProjectWiresWAFInOrder(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("waf-wired"))
	main := pr.read("cmd/app/main.go")
	for _, want := range []string{
		`appwaf "github.com/yun-dev/waf-wired/internal/waf"`,
		"appwaf.New(application.Config(), log)",
		"application.OnShutdown(wafManager.Shutdown)",
		`middleware.MetricsDescriptor("waf")`,
		"ErrWAF",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.go 缺少 %q:\n%s", want, main)
		}
	}
	if strings.Index(main, `MetricsDescriptor("security")`) > strings.Index(main, `MetricsDescriptor("waf")`) {
		t.Error("WAF 必须在 security 之后注册")
	}
	if strings.Index(main, `MetricsDescriptor("waf")`) > strings.Index(main, "registerI18n(application, log)") {
		t.Error("WAF 必须在 i18n 之前注册")
	}
}

// TestGeneratedWAFConfigDefaults pins the generated waf.* section. | TestGeneratedWAFConfigDefaults
// 钉住生成的 waf.* 配置段。
func TestGeneratedWAFConfigDefaults(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("waf-config"))
	cfg := pr.read("configs/config.yaml")
	for _, want := range []string{
		"waf:", "enabled: true", "mode: block", "paranoia_level: 1",
		"anomaly_threshold: 5", "inspect_json: true",
		"request_body_limit: 8388608", "request_body_in_memory_limit: 131072",
		"/healthz", "/static", "body_markdown", "json.design",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config 缺少 %q:\n%s", want, cfg)
		}
	}
}

// TestGeneratedMiddlewareMetricsIncludesWAF keeps the WAF visible on the dashboard. |
// TestGeneratedMiddlewareMetricsIncludesWAF 让 WAF 出现在仪表盘上。
func TestGeneratedMiddlewareMetricsIncludesWAF(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("waf-metrics"))
	obs := pr.read("internal/middleware/observability.go")
	if !strings.Contains(obs, `"waf"`) {
		t.Error("observability 缺少 waf 描述符")
	}
	for _, path := range []string{"web/i18n/zh-CN.yaml", "web/i18n/en.yaml"} {
		if !strings.Contains(pr.read(path), "waf:") {
			t.Errorf("%s 缺少 metrics.middleware.name.waf", path)
		}
	}
}

// TestGeneratedDocsIncludeWAF publishes the guide and the project-level constraints. |
// TestGeneratedDocsIncludeWAF 发布指南与项目级约束。
func TestGeneratedDocsIncludeWAF(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("waf-docs"))
	if !strings.Contains(pr.read("docs/waf.md"), "waf.enabled") {
		t.Error("docs/waf.md 未随项目发布")
	}
	for _, path := range []string{"README.md", "CLAUDE.md", "AGENTS.md"} {
		if !strings.Contains(pr.read(path), "## WAF") {
			t.Errorf("%s 缺少 WAF 段", path)
		}
	}
}

// TestGeneratedCLAUDEKeepsSingleWatchSlot warns that WAF owns the config.Watch slot. |
// TestGeneratedCLAUDEKeepsSingleWatchSlot 警告 WAF 独占 config.Watch 槽。
func TestGeneratedCLAUDEKeepsSingleWatchSlot(t *testing.T) {
	pr, _ := generateMem(t, scaffold.DefaultOptions("waf-watch"))
	for _, path := range []string{"CLAUDE.md", "AGENTS.md"} {
		if !strings.Contains(pr.read(path), "cfg.Watch") {
			t.Errorf("%s 未记录单槽限制", path)
		}
	}
}

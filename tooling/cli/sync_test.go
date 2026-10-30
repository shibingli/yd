package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shibingli/yd/tooling/assets"
	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/spf13/afero"
)

// syncFixture generates a real project into memory so the sync tests exercise the same template
// plan as yd new. | syncFixture 在内存中生成真实项目，使同步测试使用与 yd new 相同的模板计划。
func syncFixture(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	opts := scaffold.DefaultOptions("app")
	opts.Module = "example.com/app"
	opts.DB = scaffold.DBSQLite
	opts.YdVersion = ""
	if _, err := scaffold.Generate(context.Background(), opts, fs, nil); err != nil {
		t.Fatalf("generate sync fixture: %v | 生成同步测试项目失败", err)
	}
	return fs, "app"
}

func syncRead(t *testing.T, fs afero.Fs, path string) string {
	t.Helper()
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("read %s: %v | 读取 %s 失败", path, err, path)
	}
	return string(data)
}

func syncWrite(t *testing.T, fs afero.Fs, path, content string) {
	t.Helper()
	if err := afero.WriteFile(fs, path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v | 写入 %s 失败", path, err, path)
	}
}

// TestSyncRestoresTemplatesAndBacksUpEdits proves the core safety contract: generated files are
// restored, local edits are backed up, and user-owned config/module files stay untouched. |
// TestSyncRestoresTemplatesAndBacksUpEdits 验证核心安全契约：生成文件恢复、本地修改备份、
// 用户自有配置与模块文件保持不变。
func TestSyncRestoresTemplatesAndBacksUpEdits(t *testing.T) {
	fs, dir := syncFixture(t)
	readmePath := dir + "/README.md"
	workflowPath := dir + "/web/templates/mgr/pages/workflows.html"
	configPath := dir + "/configs/config.yaml"
	goModPath := dir + "/go.mod"
	originalReadme := syncRead(t, fs, readmePath)
	originalWorkflow := syncRead(t, fs, workflowPath)
	syncWrite(t, fs, readmePath, originalReadme+"\nCUSTOM README\n")
	syncWrite(t, fs, configPath, syncRead(t, fs, configPath)+"\n# CUSTOM CONFIG\n")
	syncWrite(t, fs, goModPath, syncRead(t, fs, goModPath)+"\n// CUSTOM GO MOD\n")
	if err := fs.Remove(workflowPath); err != nil {
		t.Fatalf("remove workflow template: %v | 删除工作流模板失败", err)
	}

	te := newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--yes", "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("sync: %v\n%s", err, te.out.String())
	}
	if got := syncRead(t, fs, readmePath); got != originalReadme {
		t.Fatalf("README was not restored | README 未恢复")
	}
	if got := syncRead(t, fs, workflowPath); got != originalWorkflow {
		t.Fatalf("missing workflow template was not restored | 缺失的工作流模板未恢复")
	}
	if !strings.Contains(syncRead(t, fs, configPath), "# CUSTOM CONFIG") {
		t.Fatalf("configs/config.yaml was overwritten | configs/config.yaml 被覆盖")
	}
	if !strings.Contains(syncRead(t, fs, goModPath), "// CUSTOM GO MOD") {
		t.Fatalf("go.mod was overwritten | go.mod 被覆盖")
	}
	sidecar := syncRead(t, fs, configPath+".yd-new")
	if sidecar == "" || strings.Contains(sidecar, "# CUSTOM CONFIG") {
		t.Fatalf("config sidecar was not rendered from the template | 配置旁路文件不是模板渲染结果")
	}

	backupRoot := dir + "/.yd/backup"
	entries, err := afero.ReadDir(fs, backupRoot)
	if err != nil || len(entries) == 0 {
		t.Fatalf("backup directory missing: %v | 备份目录缺失", err)
	}
	backupReadme := backupRoot + "/" + entries[0].Name() + "/README.md"
	if !strings.Contains(syncRead(t, fs, backupReadme), "CUSTOM README") {
		t.Fatalf("backup does not contain the replaced README | 备份中没有被替换的 README")
	}
	if !strings.Contains(te.out.String(), "backup | 备份:") {
		t.Fatalf("sync did not report the backup path | 同步未报告备份路径")
	}
}

// TestSyncRequiresConfirmationAndSupportsDryRun covers both safety gates. | TestSyncRequiresConfirmationAndSupportsDryRun
// 覆盖两个安全闸门。
func TestSyncRequiresConfirmationAndSupportsDryRun(t *testing.T) {
	fs, dir := syncFixture(t)
	readmePath := dir + "/README.md"
	syncWrite(t, fs, readmePath, syncRead(t, fs, readmePath)+"\nCUSTOM\n")

	te := newTestEnv(t, fs, "", false)
	err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--go=false", "--assets=false",
	}, te.env)
	if !errors.Is(err, ErrSyncNeedsConfirmation) {
		t.Fatalf("non-interactive sync without --yes = %v, want ErrSyncNeedsConfirmation | 非交互未确认应为 ErrSyncNeedsConfirmation", err)
	}
	if !strings.Contains(syncRead(t, fs, readmePath), "CUSTOM") {
		t.Fatalf("non-interactive refusal changed the project | 非交互拒绝仍修改了项目")
	}

	te = newTestEnv(t, fs, "n\n", true)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("interactive cancel: %v | 交互取消失败", err)
	}
	if !strings.Contains(syncRead(t, fs, readmePath), "CUSTOM") {
		t.Fatalf("interactive cancel changed the project | 交互取消仍修改了项目")
	}

	te = newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--dry-run", "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("dry-run: %v | 干跑失败", err)
	}
	if !strings.Contains(syncRead(t, fs, readmePath), "CUSTOM") {
		t.Fatalf("dry-run changed the project | 干跑修改了项目")
	}
	if !strings.Contains(te.out.String(), "would upgrade | 将升级") && !strings.Contains(te.out.String(), "dry-run") {
		t.Fatalf("dry-run did not print its plan | 干跑未打印计划")
	}

	te = newTestEnv(t, fs, "y\n", true)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("interactive confirm: %v | 交互确认失败", err)
	}
	if strings.Contains(syncRead(t, fs, readmePath), "CUSTOM") {
		t.Fatalf("interactive confirmation did not apply the sync | 交互确认未执行同步")
	}
}

// TestSyncBackupCoversModuleAndAssetFiles proves the upgrade steps preserve the files they rewrite
// even when no scaffold file is overwritten. | TestSyncBackupCoversModuleAndAssetFiles 验证升级步骤
// 会保留它们改写的文件，即使没有任何脚手架文件被覆盖。
func TestSyncBackupCoversModuleAndAssetFiles(t *testing.T) {
	fs, dir := syncFixture(t)
	syncWrite(t, fs, dir+"/go.sum", "example.com/fixture v1.0.0 h1:fixture\n")
	backupPath := dir + "/.yd/backup/test"
	got, err := backupSyncFiles(fs, dir, &syncPlan{}, true, true, backupPath)
	if err != nil {
		t.Fatalf("backup: %v | 备份失败", err)
	}
	if got != backupPath {
		t.Fatalf("backup path = %q, want %q | 备份路径不符", got, backupPath)
	}
	for _, rel := range []string{"go.mod", "go.sum", "web/assets.lock.json"} {
		if _, err := afero.ReadFile(fs, backupPath+"/"+rel); err != nil {
			t.Fatalf("backup missing %s: %v | 备份缺少 %s", rel, err, rel)
		}
	}
	if got, err := backupSyncFiles(fs, dir, &syncPlan{}, false, false, dir+"/.yd/backup/none"); err != nil || got != "" {
		t.Fatalf("backup without upgrades = %q, %v; want empty | 未开启升级时不应备份", got, err)
	}
}

// TestSyncIgnoresAssetLockTimestamp proves the generated_at refresh from the asset updater does not
// make every sync look dirty, while a substantive lock change is still detected. |
// TestSyncIgnoresAssetLockTimestamp 验证资产更新器刷新的 generated_at 不会让每次同步都显得有变更，
// 同时仍能发现实质性的锁定变化。
func TestSyncIgnoresAssetLockTimestamp(t *testing.T) {
	fs, dir := syncFixture(t)
	if err := writeConfigBaseline(fs, dir); err != nil {
		t.Fatalf("write config baseline: %v | 写入配置基线失败", err)
	}
	lockPath := dir + "/" + assets.DefaultManifestPath
	m, err := assets.LoadManifest(fs, lockPath)
	if err != nil {
		t.Fatalf("load asset lock: %v | 读取资产锁失败", err)
	}
	m.GeneratedAt = time.Now().UTC()
	if err := m.Save(fs, lockPath); err != nil {
		t.Fatalf("save asset lock: %v | 保存资产锁失败", err)
	}

	te := newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--dry-run", "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("timestamp-only dry-run: %v | 仅时间戳变化的干跑失败", err)
	}
	if !strings.Contains(te.out.String(), "already current | 已是最新") {
		t.Fatalf("timestamp-only lock change scheduled an overwrite:\n%s | 仅时间戳变化不应安排覆盖", te.out.String())
	}

	m.Assets[0].SHA256 = strings.Repeat("0", 64)
	if err := m.Save(fs, lockPath); err != nil {
		t.Fatalf("save modified asset lock: %v | 保存修改后的资产锁失败", err)
	}
	te = newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--dry-run", "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("modified lock dry-run: %v | 修改资产锁后的干跑失败", err)
	}
	if !strings.Contains(te.out.String(), "overwrite | 覆盖 1") {
		t.Fatalf("substantive lock change was not scheduled:\n%s | 实质性的资产锁变化未被安排覆盖", te.out.String())
	}
}

// TestSyncConfigAutoMergeThroughCLI proves a generated baseline turns user edits into local
// overrides and restores the generated baseline through the real command. |
// TestSyncConfigAutoMergeThroughCLI 验证生成基线会把用户修改迁移为本地覆盖，并通过真实命令恢复
// 生成基线。
func TestSyncConfigAutoMergeThroughCLI(t *testing.T) {
	fs, dir := syncFixture(t)
	if err := writeConfigBaseline(fs, dir); err != nil {
		t.Fatalf("write config baseline: %v | 写入配置基线失败", err)
	}
	configPath := dir + "/" + syncConfigBaseRel
	original := syncRead(t, fs, configPath)
	modified := strings.Replace(original, "  port: 8080", "  port: 9099", 1)
	if modified == original {
		t.Fatalf("fixture has no server port to modify | 测试夹具没有可修改的服务端口")
	}
	syncWrite(t, fs, configPath, modified+"\ncustom:\n  enabled: true\n")

	te := newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--yes", "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("sync: %v\n%s", err, te.out.String())
	}
	if got := syncRead(t, fs, configPath); got != original {
		t.Fatalf("config.yaml was not restored to the new template | config.yaml 未恢复为新模板")
	}
	local := syncRead(t, fs, dir+"/"+syncConfigLocalRel)
	if !strings.Contains(local, "9099") || !strings.Contains(local, "custom:") {
		t.Fatalf("user overrides were not moved to config.local.yaml:\n%s | 用户覆盖未迁移到本地文件", local)
	}
	if got := syncRead(t, fs, dir+"/"+syncConfigBaselineRel); got != original {
		t.Fatalf("baseline snapshot was not refreshed | 基线快照未刷新")
	}
	if _, err := afero.ReadFile(fs, dir+"/"+syncConfigSidecarRel); err == nil {
		t.Fatalf("auto merge left a legacy sidecar | 自动合并遗留了旁路文件")
	}

	te = newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--dry-run", "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("second sync: %v | 第二次同步失败", err)
	}
	if !strings.Contains(te.out.String(), "already current | 已是最新") {
		t.Fatalf("second sync was not idempotent:\n%s | 第二次同步不幂等", te.out.String())
	}
}

// TestSyncConfigLegacyAdoptThroughCLI migrates an old project once and then records a baseline. |
// TestSyncConfigLegacyAdoptThroughCLI 一次性迁移旧项目并记录基线。
func TestSyncConfigLegacyAdoptThroughCLI(t *testing.T) {
	fs, dir := syncFixture(t)
	configPath := dir + "/" + syncConfigBaseRel
	original := syncRead(t, fs, configPath)
	modified := strings.Replace(original, "  port: 8080", "  port: 9099", 1)
	if modified == original {
		t.Fatalf("fixture has no server port to modify | 测试夹具没有可修改的服务端口")
	}
	syncWrite(t, fs, configPath, modified)

	te := newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--yes", "--config-mode", "adopt", "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("adopt sync: %v\n%s", err, te.out.String())
	}
	if got := syncRead(t, fs, configPath); got != original {
		t.Fatalf("adopt did not install the new template | 迁移未安装新模板")
	}
	if local := syncRead(t, fs, dir+"/"+syncConfigLocalRel); !strings.Contains(local, "9099") {
		t.Fatalf("adopt did not preserve the old port:\n%s | 迁移未保留旧端口", local)
	}
	if got := syncRead(t, fs, dir+"/"+syncConfigBaselineRel); got != original {
		t.Fatalf("adopt did not record a baseline | 迁移未记录基线")
	}
}

// TestSyncConfigLegacyCreatesBaselineWhenCurrent proves an unmodified legacy project adopts the
// snapshot without touching its configuration. | TestSyncConfigLegacyCreatesBaselineWhenCurrent
// 验证未修改的旧项目会记录快照且不改动配置。
func TestSyncConfigLegacyCreatesBaselineWhenCurrent(t *testing.T) {
	fs, dir := syncFixture(t)
	configPath := dir + "/" + syncConfigBaseRel
	original := syncRead(t, fs, configPath)

	te := newTestEnv(t, fs, "", false)
	// No --yes: recording only the derived baseline must keep the historical no-op exit status. |
	// 不传 --yes：只记录派生基线必须保持原有的无操作退出状态。
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("sync: %v\n%s", err, te.out.String())
	}
	if got := syncRead(t, fs, configPath); got != original {
		t.Fatalf("unmodified legacy config changed | 未修改的旧配置被改变")
	}
	if got := syncRead(t, fs, dir+"/"+syncConfigBaselineRel); got != original {
		t.Fatalf("baseline was not created | 基线未创建")
	}
	if _, err := afero.ReadFile(fs, dir+"/"+syncConfigSidecarRel); err == nil {
		t.Fatalf("unmodified legacy project got a sidecar | 未修改的旧项目生成了旁路文件")
	}
}

// TestSyncConfigInvalidMode rejects an unknown configuration mode before any write. |
// TestSyncConfigInvalidMode 在写入前拒绝未知配置模式。
func TestSyncConfigInvalidMode(t *testing.T) {
	fs, dir := syncFixture(t)
	configPath := dir + "/" + syncConfigBaseRel
	original := syncRead(t, fs, configPath)
	te := newTestEnv(t, fs, "", false)
	err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--config-mode", "merge", "--yes", "--go=false", "--assets=false",
	}, te.env)
	if !errors.Is(err, ErrSyncConfigMode) {
		t.Fatalf("invalid config mode = %v, want ErrSyncConfigMode | 非法配置模式应返回 ErrSyncConfigMode", err)
	}
	if got := syncRead(t, fs, configPath); got != original {
		t.Fatalf("invalid mode changed config | 非法模式修改了配置")
	}
}

// TestSyncRejectsNonProject keeps a random directory from being treated as a yd project. |
// TestSyncRejectsNonProject 防止任意目录被当成 yd 项目。
func TestSyncRejectsNonProject(t *testing.T) {
	te := newTestEnv(t, nil, "", false)
	err := Execute(context.Background(), []string{
		"sync", "--yes", "--go=false", "--assets=false",
	}, te.env)
	if !errors.Is(err, ErrSyncNotProject) {
		t.Fatalf("sync in a non-project = %v, want ErrSyncNotProject | 非项目目录应返回 ErrSyncNotProject", err)
	}
}

// TestSyncWarnsWhenWAFDisabledButPackageExists prevents a half-removed WAF tree from going
// unnoticed. | TestSyncWarnsWhenWAFDisabledButPackageExists 防止半移除的 WAF 目录被静默留下。
func TestSyncWarnsWhenWAFDisabledButPackageExists(t *testing.T) {
	fs, dir := syncFixture(t)
	metaPath := dir + "/.yd.json"
	meta := syncRead(t, fs, metaPath)
	meta = strings.Replace(meta, `"waf": true`, `"waf": false`, 1)
	syncWrite(t, fs, metaPath, meta)

	te := newTestEnv(t, fs, "", false)
	if err := Execute(context.Background(), []string{
		"sync", "--dir", dir, "--dry-run", "--go=false", "--assets=false",
	}, te.env); err != nil {
		t.Fatalf("sync dry-run: %v\n%s", err, te.errs.String())
	}
	if !strings.Contains(te.errs.String(), "internal/waf") {
		t.Fatalf("缺少半移除告警:\n%s", te.errs.String())
	}
}

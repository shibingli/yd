package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"go.yaml.in/yaml/v3"
)

// configFixture writes one baseline/current pair into an in-memory project. | configFixture 把一对
// 基线/当前配置写入内存项目。
func configFixture(t *testing.T, baseline, current string, withBaseline, withLocal bool) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	dir := "app"
	syncWrite(t, fs, dir+"/"+syncConfigBaseRel, current)
	if withBaseline {
		syncWrite(t, fs, dir+"/"+syncConfigBaselineRel, baseline)
	}
	if withLocal {
		syncWrite(t, fs, dir+"/"+syncConfigLocalRel, baseline)
	}
	return fs, dir
}

// configPlanFile returns the planned bytes for one path. | configPlanFile 返回某个路径的计划字节。
func configPlanFile(t *testing.T, plan *syncConfigPlan, rel string) []byte {
	t.Helper()
	for _, file := range plan.Files {
		if file.Rel == rel {
			return file.Content
		}
	}
	t.Fatalf("plan has no %s | 计划中没有 %s", rel, rel)
	return nil
}

// configValue decodes one dotted path from YAML bytes. | configValue 从 YAML 字节中解码一个点分路径。
func configValue(t *testing.T, data []byte, path string) any {
	t.Helper()
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("decode YAML: %v\n%s", err, data)
	}
	parts := strings.Split(path, ".")
	var current any = root
	for _, part := range parts {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = mapping[part]
	}
	return current
}

// TestPlanSyncConfigMergesUserOverrides covers the core three-way contract: new defaults move into
// the baseline, user values move into local, and report paths never expose values. |
// TestPlanSyncConfigMergesUserOverrides 覆盖核心三方契约：新默认值进入基线，用户值迁移到本地，
// 报告路径绝不暴露值。
func TestPlanSyncConfigMergesUserOverrides(t *testing.T) {
	baseline := "server:\n  port: 8080\n  read_timeout: 15s\nauth:\n  master_key: \"\"\n  token_ttl: 2h\n"
	current := "server:\n  port: 9099\nauth:\n  master_key: \"secret-value\"\n  token_ttl: 2h\ncustom:\n  enabled: true\n"
	newConfig := "server:\n  port: 8080\n  read_timeout: 30s\nauth:\n  master_key: \"\"\n  token_ttl: 4h\nfeature:\n  enabled: true\n"
	fs, dir := configFixture(t, baseline, current, true, false)

	plan, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigAuto)
	if err != nil {
		t.Fatalf("plan config: %v | 规划配置失败", err)
	}
	if !plan.Report.Merged || !plan.Report.Baseline || !plan.Report.BaselineWritten {
		t.Fatalf("unexpected report: %+v | 报告不符", plan.Report)
	}
	if got := configPlanFile(t, plan, syncConfigBaseRel); string(got) != newConfig {
		t.Fatalf("baseline write is not the new template:\n%s | 基线写入不是新模板", got)
	}
	local := configPlanFile(t, plan, syncConfigLocalRel)
	for _, file := range plan.Files {
		if file.Rel == syncConfigLocalRel && file.Mode != 0o600 {
			t.Fatalf("local override mode = %o, want 0600 | 本地覆盖文件权限应为 0600", file.Mode)
		}
	}
	if got := configValue(t, local, "server.port"); got != 9099 {
		t.Fatalf("server.port = %v, want 9099 | server.port 未迁移", got)
	}
	if got := configValue(t, local, "auth.master_key"); got != "secret-value" {
		t.Fatalf("auth.master_key was not moved | auth.master_key 未迁移")
	}
	if got := configValue(t, local, "custom.enabled"); got != true {
		t.Fatalf("custom.enabled was not moved | custom.enabled 未迁移")
	}
	if got := configValue(t, local, "server.read_timeout"); got != nil {
		t.Fatalf("deleted key leaked into local: %v | 已删除的键进入了本地文件", got)
	}
	if !containsString(plan.Report.NewKeys, "feature") {
		t.Fatalf("new keys = %v, want feature | 新增键不符", plan.Report.NewKeys)
	}
	if !containsString(plan.Report.MovedKeys, "server.port") {
		t.Fatalf("moved keys = %v, want server.port | 迁移键不符", plan.Report.MovedKeys)
	}
	if !containsString(plan.Report.DeletedKeys, "server.read_timeout") {
		t.Fatalf("deleted keys = %v, want server.read_timeout | 删除键不符", plan.Report.DeletedKeys)
	}
	if !containsString(plan.Report.ChangedDefaults, "server.read_timeout") {
		t.Fatalf("changed defaults = %v, want server.read_timeout | 模板默认变化不符", plan.Report.ChangedDefaults)
	}

	te := newTestEnv(t, fs, "", false)
	printSyncConfigReport(te.env, plan)
	if strings.Contains(te.out.String(), "secret-value") {
		t.Fatalf("config report exposed a secret value:\n%s | 配置报告泄露了密钥值", te.out.String())
	}
	if err := applySyncPlan(te.env, dir, &syncPlan{Files: plan.Files}); err != nil {
		t.Fatalf("apply config plan: %v | 应用配置计划失败", err)
	}
	if got := syncRead(t, fs, dir+"/"+syncConfigLocalRel); configValue(t, []byte(got), "server.port") != 9099 {
		t.Fatalf("local override was not written | 本地覆盖未写入")
	}

	second, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigAuto)
	if err != nil {
		t.Fatalf("second plan: %v | 第二次规划失败", err)
	}
	if len(second.Files) != 0 {
		t.Fatalf("config merge is not idempotent: %+v | 配置合并不幂等", second.Files)
	}
}

// TestPlanSyncConfigLegacyKeepAndAdopt covers the legacy safe mode and the explicit one-time
// adoption. | TestPlanSyncConfigLegacyKeepAndAdopt 覆盖旧项目安全模式与显式一次性迁移。
func TestPlanSyncConfigLegacyKeepAndAdopt(t *testing.T) {
	current := "server:\n  port: 9099\nlegacy:\n  keep: true\n"
	newConfig := "server:\n  port: 8080\nfeature:\n  enabled: true\n"
	fs, dir := configFixture(t, "", current, false, false)

	keep, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigAuto)
	if err != nil {
		t.Fatalf("legacy plan: %v | 旧项目规划失败", err)
	}
	if !keep.Report.Kept || keep.Report.Sidecar == "" || len(keep.Files) != 1 || keep.Files[0].Rel != syncConfigSidecarRel {
		t.Fatalf("legacy plan should keep config and write only the sidecar: %+v | 旧项目计划不符", keep)
	}
	if !containsString(keep.Report.NewKeys, "feature") || !containsString(keep.Report.RemovedKeys, "legacy") {
		t.Fatalf("legacy diff = %+v | 旧项目差异不符", keep.Report)
	}
	if got := syncRead(t, fs, dir+"/"+syncConfigBaseRel); got != current {
		t.Fatalf("legacy dry planning changed config.yaml | 旧项目规划修改了 config.yaml")
	}

	adopt, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigAdopt)
	if err != nil {
		t.Fatalf("adopt plan: %v | 迁移规划失败", err)
	}
	if !adopt.Report.Merged || !adopt.Report.BaselineWritten {
		t.Fatalf("adopt report = %+v | 迁移报告不符", adopt.Report)
	}
	local := configPlanFile(t, adopt, syncConfigLocalRel)
	if got := configValue(t, local, "server.port"); got != 9099 {
		t.Fatalf("adopted port = %v, want 9099 | 迁移后的端口不符", got)
	}
	if got := configValue(t, local, "legacy.keep"); got != true {
		t.Fatalf("legacy section was not adopted | 旧配置段未迁移")
	}
	if got := configPlanFile(t, adopt, syncConfigBaseRel); string(got) != newConfig {
		t.Fatalf("adopt did not install the new template | 迁移未安装新模板")
	}
	if !containsString(adopt.Report.NewKeys, "feature") {
		t.Fatalf("adopt new keys = %v | 迁移新增键不符", adopt.Report.NewKeys)
	}
}

// TestPlanSyncConfigLocalConflictAndOverwrite proves local values always win and overwrite is
// explicit. | TestPlanSyncConfigLocalConflictAndOverwrite 验证本地值始终优先，且覆盖必须显式
// 请求。
func TestPlanSyncConfigLocalConflictAndOverwrite(t *testing.T) {
	baseline := "server:\n  port: 8080\n"
	current := "server:\n  port: 9099\n"
	local := "server:\n  port: 9191\n"
	newConfig := "server:\n  port: 8080\n"
	fs := afero.NewMemMapFs()
	dir := "app"
	syncWrite(t, fs, dir+"/"+syncConfigBaselineRel, baseline)
	syncWrite(t, fs, dir+"/"+syncConfigBaseRel, current)
	syncWrite(t, fs, dir+"/"+syncConfigLocalRel, local)

	plan, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigAuto)
	if err != nil {
		t.Fatalf("merge plan: %v | 合并规划失败", err)
	}
	if !containsString(plan.Report.Conflicts, "server.port") {
		t.Fatalf("conflicts = %v, want server.port | 冲突未报告", plan.Report.Conflicts)
	}
	if len(plan.Report.MovedKeys) != 0 {
		t.Fatalf("conflicting override was reported as moved: %v | 冲突覆盖项被误报为已迁移", plan.Report.MovedKeys)
	}
	for _, file := range plan.Files {
		if file.Rel == syncConfigLocalRel {
			t.Fatalf("conflicting local file should not be rewritten | 冲突的本地文件不应被重写")
		}
	}
	if got := syncRead(t, fs, dir+"/"+syncConfigLocalRel); configValue(t, []byte(got), "server.port") != 9191 {
		t.Fatalf("local value did not win | 本地值未优先")
	}

	overwrite, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigOverwrite)
	if err != nil {
		t.Fatalf("overwrite plan: %v | 覆盖规划失败", err)
	}
	if !overwrite.Report.Overwritten || len(overwrite.Report.Conflicts) != 0 {
		t.Fatalf("overwrite report = %+v | 覆盖报告不符", overwrite.Report)
	}
	for _, file := range overwrite.Files {
		if file.Rel == syncConfigLocalRel {
			t.Fatalf("overwrite touched the local file | 覆盖模式修改了本地文件")
		}
	}
	if got := syncRead(t, fs, dir+"/"+syncConfigLocalRel); got != local {
		t.Fatalf("overwrite changed the local file | 覆盖模式修改了本地文件")
	}
}

// TestPlanSyncConfigKeepNeverTouchesSnapshot proves keep mode leaves both the baseline and the
// local file alone even when a snapshot exists. | TestPlanSyncConfigKeepNeverTouchesSnapshot 验证
// 即使存在快照，保留模式也不改动基线与本地文件。
func TestPlanSyncConfigKeepNeverTouchesSnapshot(t *testing.T) {
	baseline := "server:\n  port: 8080\n"
	current := "server:\n  port: 9099\n"
	newConfig := "server:\n  port: 8080\nfeature:\n  enabled: true\n"
	fs, dir := configFixture(t, baseline, current, true, true)

	plan, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigKeep)
	if err != nil {
		t.Fatalf("keep plan: %v | 保留规划失败", err)
	}
	if !plan.Report.Kept || plan.Report.Sidecar == "" || plan.Report.BaselineWritten {
		t.Fatalf("keep report = %+v | 保留报告不符", plan.Report)
	}
	for _, file := range plan.Files {
		if file.Rel == syncConfigBaseRel || file.Rel == syncConfigLocalRel || file.Rel == syncConfigBaselineRel {
			t.Fatalf("keep mode planned %s | 保留模式规划了 %s", file.Rel, file.Rel)
		}
	}
}

// TestMergeLocalPreservesExistingCommentsAndIndent proves appended overrides keep the local
// file's comments and two-space style. | TestMergeLocalPreservesExistingCommentsAndIndent 验证追加
// 覆盖项会保留本地文件的注释与两空格风格。
func TestMergeLocalPreservesExistingCommentsAndIndent(t *testing.T) {
	baseline := "server:\n  port: 8080\n"
	current := "server:\n  port: 9099\ncustom:\n  enabled: true\n"
	fs, dir := configFixture(t, baseline, current, true, false)
	syncWrite(t, fs, dir+"/"+syncConfigLocalRel, "# keep this comment\nserver:\n  port: 9099\n")

	plan, err := planSyncConfig(fs, dir, []byte(baseline), syncConfigAuto)
	if err != nil {
		t.Fatalf("merge plan: %v | 合并规划失败", err)
	}
	local := string(configPlanFile(t, plan, syncConfigLocalRel))
	if !strings.Contains(local, "# keep this comment") {
		t.Fatalf("local comment was lost:\n%s | 本地注释丢失", local)
	}
	if strings.Contains(local, "---") {
		t.Fatalf("local YAML gained a document marker:\n%s | 本地 YAML 出现文档标记", local)
	}
	if !strings.Contains(local, "server:\n  port: 9099") || !strings.Contains(local, "custom:\n  enabled: true") {
		t.Fatalf("local YAML is not two-space indented:\n%s | 本地 YAML 不是两空格缩进", local)
	}
}

// TestPlanSyncConfigKeepOnlyReportsWrittenSidecar proves an already-current sidecar is not reported
// as a write. | TestPlanSyncConfigKeepOnlyReportsWrittenSidecar 验证已是最新的旁路文件不会被报告为
// 写入。
func TestPlanSyncConfigKeepOnlyReportsWrittenSidecar(t *testing.T) {
	baseline := "server:\n  port: 8080\n"
	current := "server:\n  port: 9099\n"
	newConfig := "server:\n  port: 8081\n"
	fs, dir := configFixture(t, baseline, current, true, false)
	syncWrite(t, fs, dir+"/"+syncConfigSidecarRel, newConfig)

	plan, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigKeep)
	if err != nil {
		t.Fatalf("keep plan: %v | 保留规划失败", err)
	}
	if !plan.Report.Kept || plan.Report.Sidecar != "" || len(plan.Files) != 0 {
		t.Fatalf("unchanged sidecar was reported as written: %+v | 未变化的旁路文件被报告为写入", plan)
	}
}

// TestPlanSyncConfigRefreshesSnapshotWhenBaseMatchesNew proves a base that already equals the new
// template never has its new defaults misread as user overrides. | TestPlanSyncConfigRefreshesSnapshotWhenBaseMatchesNew
// 验证已与新模板一致的基线不会把新默认值误判为用户覆盖。
func TestPlanSyncConfigRefreshesSnapshotWhenBaseMatchesNew(t *testing.T) {
	baseline := "server:\n  port: 8080\n"
	current := "server:\n  port: 8081\n"
	newConfig := "server:\n  port: 8081\n"
	fs, dir := configFixture(t, baseline, current, true, false)

	plan, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigAuto)
	if err != nil {
		t.Fatalf("merge plan: %v | 合并规划失败", err)
	}
	if len(plan.Report.MovedKeys) != 0 {
		t.Fatalf("new defaults were misread as overrides: %v | 新默认值被误判为覆盖项", plan.Report.MovedKeys)
	}
	for _, file := range plan.Files {
		if file.Rel == syncConfigLocalRel {
			t.Fatalf("local file should not be written | 不应写入本地文件")
		}
	}
	if !plan.Report.BaselineWritten {
		t.Fatalf("baseline should be refreshed | 基线应被刷新")
	}
	if plan.Report.Merged {
		t.Fatalf("a base equal to the template must not claim a merge | 基线已与模板一致时不应声称执行了合并")
	}
	if !containsString(plan.Report.ChangedDefaults, "server.port") {
		t.Fatalf("template default change was not reported: %v | 未报告模板默认值变化", plan.Report.ChangedDefaults)
	}
}

// TestPlanSyncConfigInvalidBaselineFallsBack proves a corrupt derived snapshot cannot block the
// default sync: a differing config keeps its sidecar, and an already-current config repairs the
// snapshot. | TestPlanSyncConfigInvalidBaselineFallsBack 验证损坏的派生快照不会卡死默认同步：
// 内容不同的配置保留旁路文件，已与模板一致的配置修复快照。
func TestPlanSyncConfigInvalidBaselineFallsBack(t *testing.T) {
	invalid := "server: [broken\n"
	current := "server:\n  port: 9099\n"
	newConfig := "server:\n  port: 8080\nfeature:\n  enabled: true\n"
	fs, dir := configFixture(t, invalid, current, true, false)

	plan, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigAuto)
	if err != nil {
		t.Fatalf("invalid baseline should not block auto: %v | 非法基线不应阻断 auto", err)
	}
	if !plan.Report.BaselineInvalid || !plan.Report.Kept || plan.Report.Sidecar == "" {
		t.Fatalf("fallback report = %+v | 回退报告不符", plan.Report)
	}
	for _, file := range plan.Files {
		if file.Rel == syncConfigBaseRel || file.Rel == syncConfigBaselineRel {
			t.Fatalf("fallback must not rewrite %s | 回退不得改写 %s", file.Rel, file.Rel)
		}
	}

	fs, dir = configFixture(t, invalid, newConfig, true, false)
	repaired, err := planSyncConfig(fs, dir, []byte(newConfig), syncConfigAuto)
	if err != nil {
		t.Fatalf("repair should not fail: %v | 修复失败", err)
	}
	if !repaired.Report.BaselineWritten || repaired.Report.Sidecar != "" {
		t.Fatalf("repair report = %+v | 修复报告不符", repaired.Report)
	}
	for _, file := range repaired.Files {
		if file.Rel != syncConfigBaselineRel {
			t.Fatalf("repair should only refresh the snapshot: %+v | 修复应只刷新快照", repaired.Files)
		}
	}
}

// TestPlanSyncConfigRejectsInvalidLocal proves a broken local override blocks the merge instead of
// being silently replaced. | TestPlanSyncConfigRejectsInvalidLocal 验证损坏的本地覆盖会阻止合并，
// 而不是被静默替换。
func TestPlanSyncConfigRejectsInvalidLocal(t *testing.T) {
	baseline := "server:\n  port: 8080\n"
	current := "server:\n  port: 9099\n"
	fs, dir := configFixture(t, baseline, current, true, false)
	syncWrite(t, fs, dir+"/"+syncConfigLocalRel, "server: [unterminated\n")
	if _, err := planSyncConfig(fs, dir, []byte(baseline), syncConfigAuto); !errors.Is(err, ErrSyncConfig) {
		t.Fatalf("invalid local error = %v, want ErrSyncConfig | 损坏的本地文件应返回 ErrSyncConfig", err)
	}
}

// TestParseSyncConfigMode validates every accepted mode and rejects everything else. |
// TestParseSyncConfigMode 校验所有受支持模式并拒绝其它取值。
func TestParseSyncConfigMode(t *testing.T) {
	for _, raw := range []string{"auto", " AUTO ", "keep", "adopt", "overwrite"} {
		if _, err := parseSyncConfigMode(raw); err != nil {
			t.Fatalf("parse %q: %v | 解析 %q 失败", raw, err, raw)
		}
	}
	if _, err := parseSyncConfigMode("merge"); !errors.Is(err, ErrSyncConfigMode) {
		t.Fatalf("invalid mode error = %v | 非法模式错误不符", err)
	}
}

// TestWriteConfigBaseline verifies the snapshot written by yd new. | TestWriteConfigBaseline 验证
// yd new 写入的快照。
func TestWriteConfigBaseline(t *testing.T) {
	fs := afero.NewMemMapFs()
	dir := "app"
	syncWrite(t, fs, dir+"/"+syncConfigBaseRel, "server:\n  port: 8080\n")
	if err := writeConfigBaseline(fs, dir); err != nil {
		t.Fatalf("write baseline: %v | 写入基线失败", err)
	}
	if got := syncRead(t, fs, dir+"/"+syncConfigBaselineRel); got != "server:\n  port: 8080\n" {
		t.Fatalf("baseline = %q | 基线内容不符", got)
	}
}

// containsString reports whether values contains want. | containsString 报告 values 是否包含 want。
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// This file implements managed configuration for `yd sync` and `yd new`: a generated baseline, a
// user-owned local override file, and a three-way merge that keeps user values while replacing the
// baseline with the newest template.
//
// 本文件实现 `yd sync` 与 `yd new` 的配置管理：生成的基线、用户自有的本地覆盖文件，以及保留
// 用户值同时把基线更新到最新模板的三方合并。

package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shibingli/ydx/errs"
	"github.com/spf13/afero"
	"go.yaml.in/yaml/v3"
)

const (
	// syncConfigBaseRel is the project-relative baseline configuration file. | syncConfigBaseRel 是项目
	// 相对的基线配置文件。
	syncConfigBaseRel = "configs/config.yaml"
	// syncConfigLocalRel is the user-owned local override file; it is git-ignored. | syncConfigLocalRel
	// 是用户自有的本地覆盖文件，已被 git 忽略。
	syncConfigLocalRel = "configs/config.local.yaml"
	// syncConfigBaselineRel stores the template baseline for the next three-way merge. | syncConfigBaselineRel
	// 保存供下一次三方合并使用的模板基线。
	syncConfigBaselineRel = ".yd/config.baseline.yaml"
	// syncConfigSidecarRel is the legacy keep-mode sidecar. | syncConfigSidecarRel 是保留模式下的旁路文件。
	syncConfigSidecarRel = syncConfigBaseRel + ".yd-new"
)

var (
	// ErrSyncConfigMode reports an invalid --config-mode value. | ErrSyncConfigMode 表示非法的
	// --config-mode 取值。
	ErrSyncConfigMode = errs.New("sync: invalid config mode | sync：非法的配置模式")
	// ErrSyncConfig reports a configuration file that cannot be parsed or merged. | ErrSyncConfig 表示
	// 配置文件无法解析或合并。
	ErrSyncConfig = errs.New("sync: configuration merge failed | sync：配置合并失败")
)

// syncConfigMode selects how `yd sync` treats configs/config.yaml. | syncConfigMode 选择 `yd sync`
// 如何处理 configs/config.yaml。
type syncConfigMode string

const (
	// syncConfigAuto merges when a baseline exists and otherwise keeps the legacy sidecar mode. |
	// syncConfigAuto 在存在基线时合并，否则保持旧项目的旁路文件安全模式。
	syncConfigAuto syncConfigMode = "auto"
	// syncConfigKeep always preserves configs/config.yaml and writes a sidecar. | syncConfigKeep 始终
	// 保留 configs/config.yaml 并写入旁路文件。
	syncConfigKeep syncConfigMode = "keep"
	// syncConfigAdopt migrates a legacy project once and then writes a baseline. | syncConfigAdopt
	// 一次性迁移旧项目并写入基线。
	syncConfigAdopt syncConfigMode = "adopt"
	// syncConfigOverwrite replaces the baseline after a backup and leaves the local file untouched. |
	// syncConfigOverwrite 在备份后替换基线，并保持本地覆盖文件不变。
	syncConfigOverwrite syncConfigMode = "overwrite"
)

// parseSyncConfigMode normalises and validates a --config-mode value. | parseSyncConfigMode 规范化并
// 校验一个 --config-mode 取值。
func parseSyncConfigMode(raw string) (syncConfigMode, error) {
	value := syncConfigMode(strings.ToLower(strings.TrimSpace(raw)))
	switch value {
	case syncConfigAuto, syncConfigKeep, syncConfigAdopt, syncConfigOverwrite:
		return value, nil
	default:
		return "", errs.Detail(ErrSyncConfigMode,
			"%[1]q is not one of auto, keep, adopt, overwrite | %[1]q 不是 auto、keep、adopt、overwrite 之一", raw)
	}
}

// syncConfigReport is the human-readable outcome of one configuration plan. It contains key paths
// only, never values, so secrets cannot leak into the terminal. | syncConfigReport 是一次配置计划的
// 可读结果。它只包含键路径、不包含值，因此密钥不会出现在终端。
type syncConfigReport struct {
	// Mode is the requested configuration mode. | Mode 是请求的配置模式。
	Mode syncConfigMode
	// Baseline reports whether a baseline existed when planning started. | Baseline 表示规划开始时是否
	// 已有基线。
	Baseline bool
	// BaselineWritten reports whether the baseline will be written. | BaselineWritten 表示基线将被写入。
	BaselineWritten bool
	// BaselineInvalid reports an unreadable or malformed baseline snapshot. | BaselineInvalid 表示
	// 基线快照无法解析或格式非法。
	BaselineInvalid bool
	// Kept reports that the baseline file itself was preserved. | Kept 表示基线文件本体被保留。
	Kept bool
	// Merged reports a three-way or adoption merge. | Merged 表示执行了三方合并或迁移合并。
	Merged bool
	// Overwritten reports an explicit baseline replacement. | Overwritten 表示显式替换了基线。
	Overwritten bool
	// Sidecar is the legacy template sidecar path, when one is written. | Sidecar 是写入的旧式模板旁路
	// 文件路径。
	Sidecar string
	// NewKeys lists keys added by the new template. | NewKeys 列出新模板新增的键。
	NewKeys []string
	// RemovedKeys lists keys removed by the new template. | RemovedKeys 列出新模板移除的键。
	RemovedKeys []string
	// ChangedKeys lists differing keys in legacy mode; no values are stored. | ChangedKeys 列出旧项目
	// 模式下的差异键；不保存任何值。
	ChangedKeys []string
	// ChangedDefaults lists defaults changed by the new template without a user override. |
	// ChangedDefaults 列出新模板修改、且用户没有覆盖的默认值。
	ChangedDefaults []string
	// MovedKeys lists user overrides moved into the local file. | MovedKeys 列出迁移到本地文件的用户
	// 覆盖项。
	MovedKeys []string
	// Conflicts lists keys whose local value won over a new override. | Conflicts 列出本地值优先于新
	// 覆盖项的键。
	Conflicts []string
	// DeletedKeys lists keys deleted by the user and therefore not carried into the local file. |
	// DeletedKeys 列出用户删除、因此不会进入本地文件的键。
	DeletedKeys []string
	// ParseWarning reports that a legacy diff could not be produced. | ParseWarning 表示旧项目差异无法
	// 生成。
	ParseWarning bool
}

// syncConfigPlan is the configuration part of a sync plan. | syncConfigPlan 是同步计划中的配置部分。
type syncConfigPlan struct {
	// Files are the baseline, local and sidecar writes. | Files 是基线、本地覆盖与旁路文件的写入项。
	Files []syncFile
	// Unchanged counts configuration files already identical to the target content. | Unchanged 统计已
	// 与目标内容一致的配置文件。
	Unchanged int
	// Report explains what happened without exposing values. | Report 在不暴露值的前提下说明处理结果。
	Report syncConfigReport
}

// addFile plans one configuration file write and records whether it was already current. | addFile
// 规划一个配置文件写入，并记录它是否已是最新。
func (p *syncConfigPlan) addFile(fs afero.Fs, dir, rel string, content []byte, mode os.FileMode) (bool, error) {
	file, equal, err := planSyncConfigFile(fs, dir, rel, content, mode)
	if err != nil {
		return false, err
	}
	if equal {
		p.Unchanged++
		return false, nil
	}
	p.Files = append(p.Files, file)
	return true, nil
}

// planSyncConfigFile compares one planned configuration file with the project. | planSyncConfigFile
// 把一份计划的配置文件与项目比较。
func planSyncConfigFile(fs afero.Fs, dir, rel string, content []byte, mode os.FileMode) (syncFile, bool, error) {
	existing, exists, err := readProjectFile(fs, dir, rel)
	if err != nil {
		return syncFile{}, false, err
	}
	if !exists {
		return syncFile{Rel: rel, Content: content, Kind: syncAdd, Mode: mode}, false, nil
	}
	if bytes.Equal(existing, content) {
		return syncFile{}, true, nil
	}
	return syncFile{Rel: rel, Content: content, Existing: existing, Kind: syncOverwrite, Mode: mode}, false, nil
}

// planSyncConfig builds the configuration change set for one sync. | planSyncConfig 为一次同步构建
// 配置变更集。
func planSyncConfig(fs afero.Fs, dir string, newConfig []byte, mode syncConfigMode) (*syncConfigPlan, error) {
	if len(bytes.TrimSpace(newConfig)) == 0 {
		return nil, errs.Detail(ErrSyncConfig, "rendered %[1]s is empty | 渲染的 %[1]s 为空", syncConfigBaseRel)
	}
	base, baseExists, err := readProjectFile(fs, dir, syncConfigBaseRel)
	if err != nil {
		return nil, err
	}
	var baseline []byte
	var baselineExists bool
	if mode == syncConfigAuto || mode == syncConfigAdopt {
		baseline, baselineExists, err = readProjectFile(fs, dir, syncConfigBaselineRel)
		if err != nil {
			return nil, err
		}
	} else if info, statErr := fs.Stat(filepath.Join(dir, filepath.FromSlash(syncConfigBaselineRel))); statErr == nil && !info.IsDir() {
		// keep and overwrite do not consume the snapshot; report its presence without requiring it
		// to be readable. | keep 与 overwrite 不消费快照；只报告其存在性，不要求它可读。
		baselineExists = true
	}
	plan := &syncConfigPlan{Report: syncConfigReport{Mode: mode, Baseline: baselineExists}}
	if baselineExists && mode != syncConfigKeep && mode != syncConfigOverwrite {
		if _, parseErr := parseConfigDocument(baseline, syncConfigBaselineRel); parseErr != nil {
			if mode == syncConfigAuto {
				// A corrupt derived snapshot must not brick the default sync. Fall back to the safe
				// legacy reconciliation: refresh it when the baseline already equals the template,
				// otherwise keep config.yaml plus a sidecar and report the repair path. | 损坏的派生
				// 快照不能卡死默认同步。回退到安全旧项目路径：基线已与模板一致时直接刷新，否则保留
				// config.yaml 加旁路文件，并报告修复方式。
				plan.Report.BaselineInvalid = true
				return plan, planSyncConfigAutoLegacy(plan, fs, dir, base, baseExists, newConfig)
			}
			return nil, parseErr
		}
	}

	switch mode {
	case syncConfigKeep:
		return plan, planSyncConfigKeep(plan, fs, dir, base, baseExists, newConfig)
	case syncConfigOverwrite:
		return plan, planSyncConfigOverwrite(plan, fs, dir, newConfig)
	case syncConfigAdopt:
		if baselineExists {
			return plan, planSyncConfigMerge(plan, fs, dir, baseline, base, baseExists, newConfig)
		}
		return plan, planSyncConfigAdopt(plan, fs, dir, base, baseExists, newConfig)
	case syncConfigAuto:
		if baselineExists {
			return plan, planSyncConfigMerge(plan, fs, dir, baseline, base, baseExists, newConfig)
		}
		return plan, planSyncConfigAutoLegacy(plan, fs, dir, base, baseExists, newConfig)
	default:
		return nil, errs.Detail(ErrSyncConfigMode, "unsupported configuration mode %[1]q | 不支持的配置模式 %[1]q", string(mode))
	}
}

// planSyncConfigKeep preserves the baseline and writes a sidecar when it differs. It never touches
// the local file or the baseline snapshot. | planSyncConfigKeep 保留基线，并在内容不同时写入旁路
// 文件；它绝不触碰本地覆盖文件或基线快照。
func planSyncConfigKeep(plan *syncConfigPlan, fs afero.Fs, dir string, base []byte, baseExists bool, newConfig []byte) error {
	if !baseExists {
		_, err := plan.addFile(fs, dir, syncConfigBaseRel, newConfig, 0o644)
		return err
	}
	if bytes.Equal(base, newConfig) {
		plan.Unchanged++
		return nil
	}
	plan.Report.Kept = true
	wrote, err := plan.addFile(fs, dir, syncConfigSidecarRel, newConfig, 0o644)
	if err != nil {
		return err
	}
	if wrote {
		plan.Report.Sidecar = syncConfigSidecarRel
	}
	fillLegacyConfigReport(&plan.Report, base, newConfig)
	return nil
}

// planSyncConfigAutoLegacy keeps a legacy project safe: a baseline that already equals the new
// template adopts the snapshot, while a differing baseline is preserved together with a sidecar. |
// planSyncConfigAutoLegacy 保持旧项目安全：已经与新模板一致的基线会补写快照，内容不同的基线则
// 保留并写入旁路文件。
func planSyncConfigAutoLegacy(plan *syncConfigPlan, fs afero.Fs, dir string, base []byte, baseExists bool, newConfig []byte) error {
	if !baseExists || bytes.Equal(base, newConfig) {
		if !baseExists {
			if _, err := plan.addFile(fs, dir, syncConfigBaseRel, newConfig, 0o644); err != nil {
				return err
			}
		}
		wrote, err := plan.addFile(fs, dir, syncConfigBaselineRel, newConfig, 0o644)
		if err != nil {
			return err
		}
		plan.Report.BaselineWritten = wrote
		return nil
	}
	plan.Report.Kept = true
	wrote, err := plan.addFile(fs, dir, syncConfigSidecarRel, newConfig, 0o644)
	if err != nil {
		return err
	}
	if wrote {
		plan.Report.Sidecar = syncConfigSidecarRel
	}
	fillLegacyConfigReport(&plan.Report, base, newConfig)
	return nil
}

// planSyncConfigOverwrite replaces the baseline and refreshes the snapshot; the local file is left
// untouched because it is user-owned. | planSyncConfigOverwrite 替换基线并刷新快照；本地覆盖文件
// 属于用户，保持不动。
func planSyncConfigOverwrite(plan *syncConfigPlan, fs afero.Fs, dir string, newConfig []byte) error {
	if _, err := plan.addFile(fs, dir, syncConfigBaseRel, newConfig, 0o644); err != nil {
		return err
	}
	wrote, err := plan.addFile(fs, dir, syncConfigBaselineRel, newConfig, 0o644)
	if err != nil {
		return err
	}
	plan.Report.BaselineWritten = wrote
	plan.Report.Overwritten = true
	return nil
}

// planSyncConfigAdopt migrates a legacy project once: values in the current baseline that differ
// from the new template move into the local file, then the new template becomes the baseline. |
// planSyncConfigAdopt 一次性迁移旧项目：当前基线中与新模板不同的值迁移到本地文件，随后新模板成为
// 基线。
func planSyncConfigAdopt(plan *syncConfigPlan, fs afero.Fs, dir string, base []byte, baseExists bool, newConfig []byte) error {
	if !baseExists || bytes.Equal(base, newConfig) {
		if !baseExists {
			if _, err := plan.addFile(fs, dir, syncConfigBaseRel, newConfig, 0o644); err != nil {
				return err
			}
		}
		wrote, err := plan.addFile(fs, dir, syncConfigBaselineRel, newConfig, 0o644)
		if err != nil {
			return err
		}
		plan.Report.BaselineWritten = wrote
		return nil
	}
	currentDoc, err := parseConfigDocument(base, syncConfigBaseRel)
	if err != nil {
		return err
	}
	newDoc, err := parseConfigDocument(newConfig, syncConfigBaseRel)
	if err != nil {
		return err
	}
	// Treat the new template as the old side of the diff: every current value that differs from it
	// is a user (or legacy-default) value worth moving into config.local.yaml. | 把新模板作为差异的
	// 旧侧：当前基线中每个与它不同的值都是值得迁移到 config.local.yaml 的用户值（或旧默认值）。
	diff, err := diffConfig(newDoc, currentDoc)
	if err != nil {
		return err
	}
	moved, conflicts, _, err := planSyncConfigLocal(plan, fs, dir, diff.Overrides)
	if err != nil {
		return err
	}
	if _, err := plan.addFile(fs, dir, syncConfigBaseRel, newConfig, 0o644); err != nil {
		return err
	}
	wrote, err := plan.addFile(fs, dir, syncConfigBaselineRel, newConfig, 0o644)
	if err != nil {
		return err
	}
	plan.Report.BaselineWritten = wrote
	plan.Report.Merged = true
	plan.Report.MovedKeys = moved
	plan.Report.NewKeys = sortedConfigPaths(diff.Removed)
	plan.Report.Conflicts = conflicts
	return nil
}

// planSyncConfigMerge performs the three-way merge used by new projects and adopted legacy
// projects. | planSyncConfigMerge 执行新项目与已迁移旧项目使用的三方合并。
func planSyncConfigMerge(plan *syncConfigPlan, fs afero.Fs, dir string, baseline, base []byte, baseExists bool, newConfig []byte) error {
	// A base that already equals the new template has no user values to move. Refresh the snapshot
	// directly so template defaults can never be misread as user overrides. | 已与新模板完全一致的
	// 基线没有需要迁移的用户值；直接刷新快照，避免把模板默认值误判为用户覆盖。
	if baseExists && bytes.Equal(base, newConfig) {
		if _, err := plan.addFile(fs, dir, syncConfigBaseRel, newConfig, 0o644); err != nil {
			return err
		}
		wrote, err := plan.addFile(fs, dir, syncConfigBaselineRel, newConfig, 0o644)
		if err != nil {
			return err
		}
		plan.Report.BaselineWritten = wrote
		// The base already matches the template, so no user value moved; still report template
		// key changes when the snapshot is advanced. | 基线已与模板一致，没有用户值需要迁移；快照
		// 推进时仍报告模板的键变化。
		if baselineDoc, parseErr := parseConfigDocument(baseline, syncConfigBaselineRel); parseErr == nil {
			if newDoc, parseErr := parseConfigDocument(newConfig, syncConfigBaseRel); parseErr == nil {
				if templateDiff, diffErr := diffConfig(baselineDoc, newDoc); diffErr == nil {
					plan.Report.NewKeys = sortedConfigPaths(templateDiff.Added)
					plan.Report.RemovedKeys = sortedConfigPaths(templateDiff.Removed)
					plan.Report.ChangedDefaults = sortedConfigPaths(templateDiff.Changed)
				}
			}
		}
		return nil
	}
	baselineDoc, err := parseConfigDocument(baseline, syncConfigBaselineRel)
	if err != nil {
		return err
	}
	var baseDoc *yaml.Node
	if baseExists {
		baseDoc, err = parseConfigDocument(base, syncConfigBaseRel)
		if err != nil {
			return err
		}
	} else {
		baseDoc = newConfigDocument(false)
	}
	newDoc, err := parseConfigDocument(newConfig, syncConfigBaseRel)
	if err != nil {
		return err
	}
	userDiff, err := diffConfig(baselineDoc, baseDoc)
	if err != nil {
		return err
	}
	templateDiff, err := diffConfig(baselineDoc, newDoc)
	if err != nil {
		return err
	}
	moved, conflicts, _, err := planSyncConfigLocal(plan, fs, dir, userDiff.Overrides)
	if err != nil {
		return err
	}
	if _, err := plan.addFile(fs, dir, syncConfigBaseRel, newConfig, 0o644); err != nil {
		return err
	}
	wrote, err := plan.addFile(fs, dir, syncConfigBaselineRel, newConfig, 0o644)
	if err != nil {
		return err
	}
	plan.Report.BaselineWritten = wrote
	plan.Report.Merged = true
	plan.Report.MovedKeys = moved
	if baseExists {
		plan.Report.DeletedKeys = sortedConfigPaths(userDiff.Removed)
	}
	plan.Report.NewKeys = sortedConfigPaths(templateDiff.Added)
	plan.Report.RemovedKeys = sortedConfigPaths(templateDiff.Removed)
	plan.Report.ChangedDefaults = sortedConfigPaths(templateDiff.Changed)
	plan.Report.Conflicts = conflicts
	return nil
}

// planSyncConfigLocal merges overrides into configs/config.local.yaml. It parses the local file
// only when there is something to merge, so an unrelated sync never fails on a hand-edited local
// file. | planSyncConfigLocal 把覆盖项合并进 configs/config.local.yaml。只有确实需要合并时才解析
// 本地文件，因此无关的同步不会因为手工编辑过的本地文件而失败。
func planSyncConfigLocal(plan *syncConfigPlan, fs afero.Fs, dir string, overrides *yaml.Node) ([]string, []string, bool, error) {
	if overrides == nil || len(overrides.Content) == 0 {
		return nil, nil, false, nil
	}
	localData, localExists, err := readProjectFile(fs, dir, syncConfigLocalRel)
	if err != nil {
		return nil, nil, false, err
	}
	var localDoc *yaml.Node
	if localExists {
		localDoc, err = parseConfigDocument(localData, syncConfigLocalRel)
		if err != nil {
			return nil, nil, false, err
		}
	} else {
		localDoc = newConfigDocument(true)
	}
	merged, moved, conflicts, changed, err := mergeLocalConfig(localDoc, overrides)
	if err != nil {
		return nil, nil, false, err
	}
	if !changed {
		return moved, conflicts, false, nil
	}
	wrote, err := plan.addFile(fs, dir, syncConfigLocalRel, merged, 0o600)
	if err != nil {
		return nil, nil, false, err
	}
	return moved, conflicts, wrote, nil
}

// fillLegacyConfigReport computes the key-only diff shown when no baseline exists. | fillLegacyConfigReport
// 计算没有基线时展示的仅键差异。
func fillLegacyConfigReport(report *syncConfigReport, current, newConfig []byte) {
	currentDoc, err := parseConfigDocument(current, syncConfigBaseRel)
	if err != nil {
		report.ParseWarning = true
		return
	}
	newDoc, err := parseConfigDocument(newConfig, syncConfigBaseRel)
	if err != nil {
		report.ParseWarning = true
		return
	}
	diff, err := diffConfig(newDoc, currentDoc)
	if err != nil {
		report.ParseWarning = true
		return
	}
	report.NewKeys = sortedConfigPaths(diff.Removed)
	report.RemovedKeys = sortedConfigPaths(diff.Added)
	report.ChangedKeys = sortedConfigPaths(diff.Changed)
}

// printSyncConfigReport prints the configuration outcome without values. | printSyncConfigReport
// 在不输出值的情况下打印配置处理结果。
func printSyncConfigReport(env *Env, plan *syncConfigPlan) {
	if env == nil || plan == nil {
		return
	}
	report := plan.Report
	baseline := "absent | 不存在"
	if report.Baseline {
		baseline = "present | 存在"
	}
	fmt.Fprintf(env.Out, "config mode | 配置模式: %s (baseline | 基线: %s)\n", report.Mode, baseline)
	switch {
	case report.Merged:
		fmt.Fprintln(env.Out, "config state | 配置状态: merged | 已合并")
	case report.Overwritten:
		fmt.Fprintln(env.Out, "config state | 配置状态: overwritten | 已覆盖; local file kept | 本地覆盖文件保留")
	case report.Kept:
		fmt.Fprintln(env.Out, "config state | 配置状态: kept | 已保留")
	default:
		fmt.Fprintln(env.Out, "config state | 配置状态: current | 无变更")
	}
	if report.BaselineInvalid {
		fmt.Fprintln(env.Out, "config baseline | 配置基线: invalid | 非法; falling back to legacy safe mode | 回退到旧项目安全模式; use --config-mode=adopt or overwrite to repair | 可用 --config-mode=adopt 或 overwrite 修复")
	}
	if report.BaselineWritten {
		fmt.Fprintf(env.Out, "config baseline | 配置基线: written | 已写入: %s\n", syncConfigBaselineRel)
	}
	if report.Sidecar != "" {
		fmt.Fprintf(env.Out, "config sidecar | 配置旁路: %s\n", report.Sidecar)
	}
	printConfigPathList(env, "config new keys | 配置新增键", report.NewKeys, 12)
	printConfigPathList(env, "config removed keys | 配置移除键", report.RemovedKeys, 12)
	printConfigPathList(env, "config changed defaults | 模板默认变化", report.ChangedDefaults, 12)
	printConfigPathList(env, "config changed keys | 配置差异键", report.ChangedKeys, 12)
	printConfigPathList(env, "config moved overrides | 迁移覆盖项", report.MovedKeys, 12)
	printConfigPathList(env, "config conflicts | 配置冲突", report.Conflicts, 12)
	printConfigPathList(env, "config deleted keys | 配置中删除的键", report.DeletedKeys, 12)
	if report.ParseWarning {
		fmt.Fprintln(env.Out, "config warning | 配置告警: template diff unavailable; sidecar only | 无法生成模板差异，仅写入旁路文件")
	}
}

// printConfigPathList prints one bounded list of key paths. | printConfigPathList 打印一个有上限的
// 键路径列表。
func printConfigPathList(env *Env, label string, paths []string, limit int) {
	if len(paths) == 0 {
		return
	}
	shown := paths
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	fmt.Fprintf(env.Out, "%s: %s", label, strings.Join(shown, ", "))
	if len(paths) > len(shown) {
		fmt.Fprintf(env.Out, " ... %d more | 还有 %d 个", len(paths)-len(shown), len(paths)-len(shown))
	}
	fmt.Fprintln(env.Out)
}

// writeConfigBaseline writes the baseline snapshot right after `yd new` generated a project. |
// writeConfigBaseline 在 `yd new` 生成项目后立即写入基线快照。
func writeConfigBaseline(fs afero.Fs, project string) error {
	configPath := filepath.Join(project, filepath.FromSlash(syncConfigBaseRel))
	data, err := afero.ReadFile(fs, configPath)
	if err != nil {
		return errs.WrapDetail(err, ErrSyncApply, "read %[1]s | 读取 %[1]s", syncConfigBaseRel)
	}
	baselinePath := filepath.Join(project, filepath.FromSlash(syncConfigBaselineRel))
	if err := fs.MkdirAll(filepath.Dir(baselinePath), 0o755); err != nil {
		return errs.WrapDetail(err, ErrSyncApply, "create %[1]s | 创建 %[1]s", filepath.Dir(syncConfigBaselineRel))
	}
	if err := afero.WriteFile(fs, baselinePath, data, 0o644); err != nil {
		return errs.WrapDetail(err, ErrSyncApply, "write %[1]s | 写入 %[1]s", syncConfigBaselineRel)
	}
	return nil
}

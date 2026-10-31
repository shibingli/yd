// This file implements the full project sync command: it re-renders every scaffold file from the
// running yd binary, merges the generated configuration baseline with the user's local overrides,
// backs up conflicting generated files, and then upgrades Go modules and the locked front-end
// assets. go.mod and go.sum remain user-owned. | 本文件实现全量项目同步命令：用当前 yd 二进制重新
// 渲染全部脚手架文件，把生成的配置基线与用户本地覆盖合并，覆盖前备份冲突的生成文件，然后升级
// Go 模块与已锁定的前端资产。go.mod 与 go.sum 仍归用户所有。

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shibingli/yd/tooling/assets"
	"github.com/shibingli/yd/tooling/scaffold"
	"github.com/shibingli/ydx/errs"
	"github.com/shibingli/ydx/json"
	"github.com/spf13/afero"
	"github.com/spf13/pflag"
)

// syncCommandName is the command word of the full scaffold sync. | syncCommandName 是全量脚手架同步
// 的命令词。
const syncCommandName = "sync"

// The sync command's flag names. | sync 命令的标志名。
const (
	flagSyncDir        = "dir"
	flagSyncDryRun     = "dry-run"
	flagSyncYes        = "yes"
	flagSyncGo         = "go"
	flagSyncAssets     = "assets"
	flagSyncConfigMode = "config-mode"
)

const syncUsageLine = "yd sync [--dir DIR] [--yes] [--dry-run] [--go] [--assets] [--config-mode auto|keep|adopt|overwrite]"

var (
	// ErrSyncNotProject reports a directory without the .yd.json written by yd new. | ErrSyncNotProject
	// 表示目录缺少 yd new 写入的 .yd.json。
	ErrSyncNotProject = errs.New("sync: not a yd project (missing .yd.json) | sync：不是 yd 项目（缺少 .yd.json）")
	// ErrSyncNeedsConfirmation reports a non-interactive run without --yes. | ErrSyncNeedsConfirmation
	// 表示非交互运行没有传 --yes。
	ErrSyncNeedsConfirmation = errs.New("sync: confirmation required; pass --yes in non-interactive runs | sync：需要确认；非交互运行请传 --yes")
	// ErrSyncApply reports a failed backup or file write. | ErrSyncApply 表示备份或文件写入失败。
	ErrSyncApply = errs.New("sync: apply failed | sync：应用变更失败")
)

// syncFlags keeps the sync command's flag set, installed by registerSyncFlags. | syncFlags 保存 sync
// 命令的标志集，由 registerSyncFlags 安装。
var syncFlags *pflag.FlagSet

// syncMeta is the subset of .yd.json needed to render the same project shape. | syncMeta 是重新渲染
// 同一项目形态所需的 .yd.json 子集。
type syncMeta struct {
	Features struct {
		DB       string   `json:"db"`
		HtmxExts []string `json:"htmxExts"`
		GRPC     *bool    `json:"grpc"`
		Captcha  *bool    `json:"captcha"`
		WAF      *bool    `json:"waf"`
		I18n     *bool    `json:"i18n"`
		Tailwind *bool    `json:"tailwind"`
	} `json:"features"`
	Name   string `json:"name"`
	Module string `json:"module"`
}

// syncKind classifies one planned file. | syncKind 区分一个计划文件的动作。
type syncKind int

const (
	// syncAdd writes a file that does not exist yet. | syncAdd 写入尚不存在的文件。
	syncAdd syncKind = iota
	// syncOverwrite replaces a differing generated file after backup. | syncOverwrite 在备份后覆盖内容不同的生成文件。
	syncOverwrite
)

// syncFile is one scaffold file the sync will add or overwrite. | syncFile 是同步将新增或覆盖的一个
// 脚手架文件。
type syncFile struct {
	// Rel is the project-relative slash path. | Rel 是项目相对的斜杠路径。
	Rel string
	// Content is the newly rendered bytes. | Content 是新渲染的字节。
	Content []byte
	// Existing holds the bytes that will be overwritten; nil for an add. | Existing 是即将被覆盖的字节；
	// 新增文件为 nil。
	Existing []byte
	// Kind is add or overwrite. | Kind 取新增或覆盖。
	Kind syncKind
	// Mode is the file mode; zero means 0644. | Mode 是文件模式；零值表示 0644。
	Mode os.FileMode
}

// syncPlan is the complete local change set before any write. | syncPlan 是写入前的完整本地变更集。
type syncPlan struct {
	// Config is the managed configuration part of the plan. | Config 是计划中受管理的配置部分。
	Config *syncConfigPlan
	// Files are the adds and overwrites, sorted by path. | Files 是按路径排序的新增与覆盖。
	Files []syncFile
	// Unchanged counts rendered files already identical to the project. | Unchanged 统计已与项目一致的
	// 渲染文件。
	Unchanged int
}

// counts returns the add and overwrite counts. | counts 返回新增与覆盖数量。
func (p *syncPlan) counts() (adds, overwrites int) {
	for _, f := range p.Files {
		if f.Kind == syncAdd {
			adds++
		} else {
			overwrites++
		}
	}
	return adds, overwrites
}

// onlyConfigBaseline reports whether the only planned write is the derived baseline snapshot. |
// onlyConfigBaseline 报告计划中是否只有派生出来的配置基线快照写入。
func (p *syncPlan) onlyConfigBaseline() bool {
	if p == nil || len(p.Files) == 0 {
		return false
	}
	for _, file := range p.Files {
		if file.Rel != syncConfigBaselineRel {
			return false
		}
	}
	return true
}

func init() {
	Register(Command{
		Name:  syncCommandName,
		Short: "full-sync scaffold templates, Go modules and front-end assets | 全量同步脚手架模板、Go 模块与前端资源",
		Long: "Re-render every scaffold file with the running yd binary, then upgrade Go modules and locked npm front-end assets.\n" +
			"Conflicting generated files are backed up under .yd/backup/ before being overwritten; go.mod and go.sum are never overwritten. " +
			"Configuration follows --config-mode: auto merges a generated baseline with configs/config.local.yaml when a baseline exists, keep preserves the legacy sidecar mode, adopt migrates a legacy project once, and overwrite replaces the baseline explicitly. A legacy config identical to the new template only gets a baseline; a differing one keeps its sidecar until adopted. " +
			"An interactive run asks for confirmation; non-interactive runs require --yes, and --dry-run only prints the plan.\n" +
			"用当前 yd 二进制重新渲染全部脚手架文件，然后升级 Go 模块与锁定的 npm 前端资产。冲突的生成文件在覆盖前\n" +
			"备份到 .yd/backup/；go.mod 与 go.sum 永不覆盖。配置按 --config-mode 处理：auto 在存在基线时把生成\n" +
			"基线与 configs/config.local.yaml 合并，keep 保持旧式旁路文件，adopt 一次性迁移旧项目，overwrite 显式\n" +
			"替换基线；没有基线且内容与模板一致时只补写基线，内容不同时保持旁路文件等待 adopt。交互运行会要求确认，\n" +
			"非交互运行必须传 --yes，--dry-run 只打印计划。模板来自当前运行的\n" +
			"yd 二进制；如需最新脚手架，请先执行 `yd versions upgrade --self` 更新 CLI。",
		Usage: syncUsageLine,
		Flags: registerSyncFlags,
		Run:   runSync,
	})
}

// registerSyncFlags declares the sync command's flags. | registerSyncFlags 声明 sync 命令的标志。
func registerSyncFlags(fs *pflag.FlagSet) {
	fs.String(flagSyncDir, ".", "project directory | 项目目录")
	fs.Bool(flagSyncDryRun, false, "print the full plan without changing anything | 只打印完整计划，不做任何改动")
	fs.Bool(flagSyncYes, false, "confirm the sync without prompting | 不询问直接确认同步")
	fs.Bool(flagSyncGo, true, "upgrade Go module dependencies | 升级 Go 模块依赖")
	fs.Bool(flagSyncAssets, true, "upgrade and re-download locked front-end assets | 升级并重新下载锁定的前端资产")
	fs.String(flagSyncConfigMode, string(syncConfigAuto), "configuration mode: auto, keep, adopt or overwrite | 配置模式：auto、keep、adopt 或 overwrite")
	syncFlags = fs
}

// runSync executes the full project sync. | runSync 执行全量项目同步。
func runSync(ctx context.Context, env *Env) error {
	dir := syncDir(env)
	opts, err := syncOptions(env.FS, dir)
	if err != nil {
		return err
	}
	if !opts.WAF {
		if info, statErr := env.FS.Stat(filepath.Join(dir, "internal", "waf")); statErr == nil && info.IsDir() {
			fmt.Fprintln(env.Err, "waf: .yd.json marks WAF off but internal/waf exists; yd sync never deletes files — remove it manually | waf：清单标记关闭但 internal/waf 仍存在；yd sync 不删除文件，请手工移除")
		}
	}
	rendered, err := renderSyncScaffold(ctx, opts)
	if err != nil {
		return err
	}
	mode, err := parseSyncConfigMode(flagString(syncFlags, flagSyncConfigMode))
	if err != nil {
		return err
	}
	newConfig, ok := rendered[syncConfigBaseRel]
	if !ok {
		return errs.Detail(ErrSyncConfig, "missing rendered %[1]s | 缺少渲染的 %[1]s", syncConfigBaseRel)
	}
	configPlan, err := planSyncConfig(env.FS, dir, newConfig, mode)
	if err != nil {
		return err
	}
	plan, err := buildSyncPlan(env.FS, dir, rendered, configPlan)
	if err != nil {
		return err
	}

	dry := flagBool(syncFlags, flagSyncDryRun)
	yes := flagBool(syncFlags, flagSyncYes)
	upgradeGo := flagBool(syncFlags, flagSyncGo)
	upgradeAssets := flagBool(syncFlags, flagSyncAssets)
	adds, overwrites := plan.counts()
	if !upgradeGo && !upgradeAssets && plan.onlyConfigBaseline() {
		// Recording the derived baseline is internal bookkeeping, not a project update; keep the
		// historical no-op exit status for CI while still enabling future three-way merges. |
		// 记录派生基线属于内部簿记而不是项目更新；保持 CI 上原有的无操作退出状态，同时让后续三方
		// 合并可用。
		printSyncConfigReport(env, plan.Config)
		if dry {
			fmt.Fprintln(env.Out, "already current | 已是最新: config baseline would be recorded | 将记录配置基线")
			return nil
		}
		if err := applySyncFiles(env, dir, plan); err != nil {
			return err
		}
		fmt.Fprintln(env.Out, "already current | 已是最新: config baseline recorded | 已记录配置基线")
		return nil
	}
	if adds == 0 && overwrites == 0 && !upgradeGo && !upgradeAssets {
		printSyncConfigReport(env, plan.Config)
		fmt.Fprintln(env.Out, "already current | 已是最新: scaffold files match and dependency upgrades are disabled")
		return nil
	}

	backupPath := syncBackupPath(env.FS, dir)
	printSyncWarning(env, plan, upgradeGo, upgradeAssets, dry, backupPath)
	printSyncFiles(env, plan, dry)
	printSyncConfigReport(env, plan.Config)
	if dry {
		if upgradeGo {
			fmt.Fprintln(env.Out, "would upgrade | 将升级: Go modules -> go mod tidy && go get -u -tags=tools ./... && go mod tidy")
		}
		if upgradeAssets {
			fmt.Fprintln(env.Out, "would upgrade | 将升级: front-end assets -> latest same-major release and a forced re-download")
		}
		return nil
	}

	if !yes {
		if !env.IsTTY {
			return ErrSyncNeedsConfirmation
		}
		ok, confirmErr := Confirm(env, "确认执行全量同步？| Proceed with the full sync?", false)
		if confirmErr != nil {
			return confirmErr
		}
		if !ok {
			fmt.Fprintln(env.Out, "cancelled | 已取消: nothing was changed | 未做任何修改")
			return nil
		}
	}

	backup, err := backupSyncFiles(env.FS, dir, plan, upgradeGo, upgradeAssets, backupPath)
	if err != nil {
		return err
	}
	if backup != "" {
		fmt.Fprintf(env.Out, "backup | 备份: %s\n", backup)
	}
	if err := applySyncPlan(env, dir, plan); err != nil {
		return err
	}
	if upgradeGo || upgradeAssets {
		if err := runSyncVersionsUpgrade(ctx, env, dir, upgradeGo, upgradeAssets); err != nil {
			return err
		}
	}
	if upgradeAssets {
		if err := forceSyncAssets(ctx, env, dir); err != nil {
			return err
		}
	}
	fmt.Fprintln(env.Out, "next | 下一步: yd assets minify && go build ./... && go vet ./... && go test ./... -race")
	return nil
}

// syncDir resolves the project directory like the other project commands. | syncDir 像其它项目命令
// 一样解析项目目录。
func syncDir(env *Env) string {
	dir, _ := syncFlags.GetString(flagSyncDir)
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	return resolveUserPath(env.WorkDir, dir)
}

// syncOptions loads .yd.json and rebuilds the scaffold options that produced the project. |
// syncOptions 读取 .yd.json，并重建生成该项目时的脚手架选项。
func syncOptions(fs afero.Fs, dir string) (scaffold.Options, error) {
	data, err := afero.ReadFile(fs, filepath.Join(dir, ".yd.json"))
	if errors.Is(err, os.ErrNotExist) {
		return scaffold.Options{}, ErrSyncNotProject
	}
	if err != nil {
		return scaffold.Options{}, errs.WrapDetail(err, ErrSyncApply, "read .yd.json | 读取 .yd.json")
	}
	var meta syncMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return scaffold.Options{}, errs.WrapDetail(err, ErrSyncApply, "parse .yd.json | 解析 .yd.json")
	}
	meta.Name = strings.TrimSpace(meta.Name)
	meta.Module = strings.TrimSpace(meta.Module)
	if meta.Name == "" || meta.Module == "" {
		return scaffold.Options{}, errs.Detail(ErrSyncNotProject, ".yd.json needs name and module | .yd.json 缺少 name 或 module")
	}
	opts := scaffold.DefaultOptions(meta.Name)
	opts.Module = meta.Module
	if strings.TrimSpace(meta.Features.DB) != "" {
		opts.DB = strings.TrimSpace(meta.Features.DB)
	}
	if len(meta.Features.HtmxExts) > 0 {
		opts.HtmxExts = meta.Features.HtmxExts
	}
	if meta.Features.GRPC != nil {
		opts.GRPC = *meta.Features.GRPC
	}
	if meta.Features.Captcha != nil {
		opts.Captcha = *meta.Features.Captcha
	}
	// DefaultOptions enables WAF; a project whose manifest predates the switch must stay without it. |
	// DefaultOptions 默认开启 WAF；清单早于该开关的项目必须保持不生成。
	opts.WAF = meta.Features.WAF != nil && *meta.Features.WAF
	if meta.Features.I18n != nil {
		opts.I18n = *meta.Features.I18n
	}
	if meta.Features.Tailwind != nil {
		opts.Tailwind = *meta.Features.Tailwind
	}
	// go.mod is deliberately preserved; its version pins are maintained by the Go upgrade step. |
	// go.mod 刻意保留；其版本锁定由 Go 升级步骤维护。
	opts.YdVersion = ""
	opts.LocalReplace = ""
	opts.Force = true
	return opts, nil
}

// renderSyncScaffold renders the current scaffold into memory and returns project-relative files. |
// renderSyncScaffold 把当前脚手架渲染到内存，并返回项目相对文件。
func renderSyncScaffold(ctx context.Context, opts scaffold.Options) (map[string][]byte, error) {
	mem := afero.NewMemMapFs()
	if _, err := scaffold.Generate(ctx, opts, mem, nil); err != nil {
		return nil, err
	}
	prefix := strings.TrimSpace(opts.Name) + "/"
	out := make(map[string][]byte)
	err := afero.Walk(mem, strings.TrimSpace(opts.Name), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info == nil || info.IsDir() {
			return nil
		}
		slashed := filepath.ToSlash(path)
		rel := strings.TrimPrefix(slashed, prefix)
		if rel == slashed || rel == "" {
			return nil
		}
		data, err := afero.ReadFile(mem, path)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// buildSyncPlan compares rendered files with the project. configs/config.yaml and the module files
// are handled outside the overwrite set; the managed configuration plan supplies its own writes. |
// buildSyncPlan 把渲染文件与项目比较。configs/config.yaml 与模块文件不在普通覆盖集合内；受管理的
// 配置计划提供自己的写入项。
func buildSyncPlan(fs afero.Fs, dir string, rendered map[string][]byte, configPlan *syncConfigPlan) (*syncPlan, error) {
	rels := make([]string, 0, len(rendered))
	for rel := range rendered {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	plan := &syncPlan{Config: configPlan}
	for _, rel := range rels {
		switch rel {
		case "go.mod", "go.sum", syncConfigBaseRel:
			continue
		default:
			existing, exists, err := readProjectFile(fs, dir, rel)
			if err != nil {
				return nil, err
			}
			if !exists {
				plan.Files = append(plan.Files, syncFile{Rel: rel, Content: rendered[rel], Kind: syncAdd})
				continue
			}
			if bytes.Equal(existing, rendered[rel]) ||
				(rel == assets.DefaultManifestPath && syncManifestEqual(existing, rendered[rel])) {
				plan.Unchanged++
				continue
			}
			plan.Files = append(plan.Files, syncFile{Rel: rel, Content: rendered[rel], Existing: existing, Kind: syncOverwrite})
		}
	}
	if configPlan != nil {
		plan.Files = append(plan.Files, configPlan.Files...)
		plan.Unchanged += configPlan.Unchanged
	}
	sortSyncPlanFiles(plan.Files)
	return plan, nil
}

// sortSyncPlanFiles orders writes by path, keeping the configuration baseline last so it only
// advances after the baseline and local files have been written. | sortSyncPlanFiles 按路径排序
// 写入项，并把配置基线放到最后，使它在基线与本地文件写入成功后才推进。
func sortSyncPlanFiles(files []syncFile) {
	sort.SliceStable(files, func(i, j int) bool {
		leftBaseline := files[i].Rel == syncConfigBaselineRel
		rightBaseline := files[j].Rel == syncConfigBaselineRel
		if leftBaseline != rightBaseline {
			return !leftBaseline
		}
		return files[i].Rel < files[j].Rel
	})
}

// syncManifestEqual reports whether two asset locks have the same entries, ignoring generated_at.
// The asset updater refreshes generated_at on every save, so comparing raw bytes would schedule an
// overwrite on every sync even though nothing else changed. | syncManifestEqual 判断两份资产锁的条目
// 是否相同，并忽略 generated_at。资产更新器每次保存都会刷新 generated_at，按原始字节比较会让每次
// 同步都安排一次覆盖，即使其它内容完全没有变化。
func syncManifestEqual(existing, rendered []byte) bool {
	var a, b assets.Manifest
	if err := json.Unmarshal(existing, &a); err != nil {
		return false
	}
	if err := json.Unmarshal(rendered, &b); err != nil {
		return false
	}
	a.GeneratedAt = time.Time{}
	b.GeneratedAt = time.Time{}
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// readProjectFile reads one project-relative file. | readProjectFile 读取一个项目相对文件。
func readProjectFile(fs afero.Fs, dir, rel string) ([]byte, bool, error) {
	data, err := afero.ReadFile(fs, filepath.Join(dir, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errs.WrapDetail(err, ErrSyncApply, "read %[1]s | 读取 %[1]s", rel)
	}
	return data, true, nil
}

// printSyncWarning prints the safety notice before any mutation. | printSyncWarning 在任何修改前打印
// 安全提示。
func printSyncWarning(env *Env, plan *syncPlan, upgradeGo, upgradeAssets, dry bool, backupPath string) {
	adds, overwrites := plan.counts()
	mode := "apply"
	if dry {
		mode = "dry-run"
	}
	fmt.Fprintf(env.Out, "WARNING | 警告: full scaffold sync (%s) | 全量脚手架同步（%s）\n", mode, mode)
	fmt.Fprintf(env.Out, "templates come from the running yd binary (%s); update the CLI first with yd versions upgrade --self if needed. | 模板来自当前运行的 yd 二进制（%s）；如需最新脚手架，请先执行 yd versions upgrade --self。\n", env.Version, env.Version)
	fmt.Fprintf(env.Out, "files | 文件: add | 新增 %d, overwrite | 覆盖 %d, unchanged | 未变化 %d\n", adds, overwrites, plan.Unchanged)
	if overwrites > 0 {
		fmt.Fprintln(env.Out, "The overwrite set includes generated scaffold files; local edits to those files will be replaced. | 覆盖集合包含脚手架生成文件；这些文件中的本地修改会被替换。")
	}
	if overwrites > 0 || upgradeGo || upgradeAssets {
		fmt.Fprintf(env.Out, "A backup will be written to %s before any file is replaced or upgraded. | 覆盖或升级前会备份到 %s。\n", backupPath, backupPath)
	}
	if plan.Config != nil {
		fmt.Fprintf(env.Out, "configs/config.yaml follows --config-mode=%s; the detailed configuration report is printed below. | configs/config.yaml 按 --config-mode=%s 处理；详细配置报告见下方。\n", plan.Config.Report.Mode, plan.Config.Report.Mode)
	}
	fmt.Fprintln(env.Out, "go.mod and go.sum are never overwritten by the scaffold sync. | 脚手架同步永不覆盖 go.mod 与 go.sum。")
	if upgradeGo {
		fmt.Fprintln(env.Out, "Go modules will be upgraded by go mod tidy, go get -u -tags=tools ./... and a second go mod tidy (network access). | 将依次执行 go mod tidy、go get -u -tags=tools ./... 与第二次 go mod tidy 升级 Go 模块（需要网络）。")
	}
	if upgradeAssets {
		fmt.Fprintln(env.Out, "Front-end assets will be upgraded to the latest same-major release and re-downloaded (network access). | 前端资产将升级到同大版本的最新发布并重新下载（需要网络）。")
	}
	if !dry && !flagBool(syncFlags, flagSyncYes) {
		fmt.Fprintln(env.Out, "Use --dry-run to inspect the complete plan; --yes skips the prompt. | 可用 --dry-run 查看完整计划；--yes 跳过确认。")
	}
}

// printSyncFiles lists planned files; dry-run prints all, apply prints the first page. | printSyncFiles
// 列出计划文件；干跑打印全部，执行模式只打印第一页。
func printSyncFiles(env *Env, plan *syncPlan, all bool) {
	limit := 20
	if all {
		limit = len(plan.Files)
	}
	for i, file := range plan.Files {
		if i >= limit {
			fmt.Fprintf(env.Out, "  ... %d more | 还有 %d 个\n", len(plan.Files)-limit, len(plan.Files)-limit)
			break
		}
		action := "add | 新增"
		if file.Kind == syncOverwrite {
			action = "overwrite | 覆盖"
		}
		fmt.Fprintf(env.Out, "  %-10s %s\n", action, file.Rel)
	}
}

// runSyncVersionsUpgrade reuses the versions upgrade pipeline with a temporary flag set. The
// versions command is not concurrent by contract, so installing and restoring the set is safe. |
// runSyncVersionsUpgrade 用临时标志集复用 versions 升级管线。versions 命令按约定不并发，
// 因此安装与恢复标志集是安全的。
func runSyncVersionsUpgrade(ctx context.Context, env *Env, dir string, upgradeGo, upgradeAssets bool) error {
	old := versionsFlags
	fs := pflag.NewFlagSet("yd-sync", pflag.ContinueOnError)
	registerVersionsFlags(fs)
	defer func() { versionsFlags = old }()
	_ = fs.Set(flagVersionsDir, dir)
	_ = fs.Set(flagVersionsGo, strconv.FormatBool(upgradeGo))
	_ = fs.Set(flagVersionsAssets, strconv.FormatBool(upgradeAssets))
	_ = fs.Set(flagVersionsSelf, "false")
	return runVersionsUpgrade(ctx, env)
}

// forceSyncAssets re-downloads every locked asset after the version upgrade. It intentionally uses
// Force so a moved upstream tag is re-locked rather than reported as a failure. | forceSyncAssets
// 在版本升级后重新下载每个锁定资产。它刻意使用 Force，使上游被移动的标签重新锁定，而不是报错。
func forceSyncAssets(ctx context.Context, env *Env, dir string) error {
	client, err := versionsClient(env)
	if err != nil {
		return err
	}
	rep, updateErr := assets.Update(ctx, assets.UpdateOptions{
		FS:           env.FS,
		HTTPClient:   client,
		ManifestPath: filepath.Join(dir, assets.DefaultManifestPath),
		Root:         filepath.Join(dir, assets.DefaultRoot),
		Force:        true,
	})
	if rep != nil {
		fmt.Fprintf(env.Out, "assets | 资产: updated | 已更新 %d, unchanged | 未变化 %d, failed | 失败 %d\n",
			len(rep.Updated), len(rep.Unchanged), len(rep.Failed))
	}
	return updateErr
}

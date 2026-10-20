package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/shibingli/yd/core/errx"
	"github.com/shibingli/yd/tooling/assets"
	"github.com/shibingli/yd/tooling/versions"
)

// versionsCommandName is the command word of the version synchronization command. | versionsCommandName 是版本同步命令的命令词。
const versionsCommandName = "versions"

// The versions command's flag names. | versions 命令的标志名。
const (
	// flagVersionsDir is the project directory the command operates on. | flagVersionsDir 是命令操作的项目目录。
	flagVersionsDir = "dir"
	// flagVersionsDryRun reports the plan without running commands or writing files. | flagVersionsDryRun 只报告计划，不执行命令也不写文件。
	flagVersionsDryRun = "dry-run"
	// flagVersionsGo upgrades the Go module dependencies. | flagVersionsGo 升级 Go 模块依赖。
	flagVersionsGo = "go"
	// flagVersionsAssets upgrades the npm-pinned front-end assets. | flagVersionsAssets 升级 npm 锁定的前端资产。
	flagVersionsAssets = "assets"
)

// versionsUsageLine is the usage line of the versions command. | versionsUsageLine 是 versions 命令的用法行。
const versionsUsageLine = "yd versions [check|sync|upgrade] [--dir DIR] [--dry-run] [--go] [--assets]"

// The versions command's action words. | versions 命令的动作词。
const (
	// versionsActionCheck verifies the generated version file. | versionsActionCheck 校验生成的版本文件。
	versionsActionCheck = "check"
	// versionsActionSync regenerates the version file. | versionsActionSync 重新生成版本文件。
	versionsActionSync = "sync"
	// versionsActionUpgrade upgrades Go modules and front-end assets. | versionsActionUpgrade 升级 Go 模块与前端资产。
	versionsActionUpgrade = "upgrade"
)

var (
	// ErrVersionsCommand reports a failed external go command. | ErrVersionsCommand 表示外部 go 命令执行失败。
	ErrVersionsCommand = errx.New("run go command failed | 执行 go 命令失败")
	// ErrVersionsNothing reports an upgrade with both halves disabled. | ErrVersionsNothing 表示升级的两个部分都被关闭。
	ErrVersionsNothing = errx.New("nothing to upgrade | 没有可升级项")
	// ErrNoScaffold reports sync outside the framework repository. | ErrNoScaffold 表示在框架仓库之外运行 sync。
	ErrNoScaffold = errx.New("no tooling/scaffold directory; sync only applies to the framework repository | 没有 tooling/scaffold 目录；sync 仅适用于框架仓库")
)

// versionsFlags keeps the versions command's flag set. | versionsFlags 保存 versions 命令的标志集。
var versionsFlags *pflag.FlagSet

// versionsRunGo is the injection point for the external go command; tests replace it so no real
// module operation runs inside the test process. | versionsRunGo 是外部 go 命令的注入点；
// 测试替换它，使测试进程内不会真的执行模块操作。
var versionsRunGo = func(ctx context.Context, dir string, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return errx.WrapDetail(err, ErrVersionsCommand, "go %[1]s | go %[1]s", strings.Join(args, " "))
	}
	return nil
}

// versionsHTTPClient performs registry and asset downloads; tests replace it so no request leaves
// the process. | versionsHTTPClient 执行仓库查询与资产下载；测试替换它，使请求不会离开进程。
var (
	// defaultVersionsHTTPClient is the built-in client; tests replace versionsHTTPClient with a
	// different pointer, which versionsClient then uses verbatim. | defaultVersionsHTTPClient 是内置
	// 客户端；测试把 versionsHTTPClient 换成不同指针，versionsClient 便会原样使用它。
	defaultVersionsHTTPClient = &http.Client{Timeout: 2 * time.Minute}
	versionsHTTPClient        = defaultVersionsHTTPClient
)

// versionsClient returns the HTTP client of the upgrade run, applying http.proxy when the caller
// has not injected a client. | versionsClient 返回升级运行所用的 HTTP 客户端；调用方未注入客户端时
// 应用 http.proxy。
func versionsClient(env *Env) (*http.Client, error) {
	if versionsHTTPClient != defaultVersionsHTTPClient {
		return versionsHTTPClient, nil
	}
	proxy := configString(env, keyNetProxy)
	if proxy == "" {
		return versionsHTTPClient, nil
	}
	u, err := url.Parse(proxy)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errx.Detail(ErrConfigLoad, "invalid http.proxy | 非法的 http.proxy")
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errx.Detail(ErrConfigLoad, "default HTTP transport is not *http.Transport | 默认 HTTP 传输层不是 *http.Transport")
	}
	transport := base.Clone()
	transport.Proxy = http.ProxyURL(u)
	return &http.Client{Transport: transport, Timeout: 2 * time.Minute}, nil
}

func init() {
	Register(Command{
		Name:  versionsCommandName,
		Short: "keep scaffold versions in sync and upgrade dependencies | 保持脚手架版本同步并升级依赖",
		Long: "Derive the scaffold's Go and Htmx versions from go.mod and web/assets.lock.json, verify that the embedded templates\n" +
			"have not drifted, and upgrade Go modules and npm-pinned front-end assets in one repeatable step.\n" +
			"从 go.mod 与 web/assets.lock.json 推导脚手架的 Go 与 Htmx 版本，校验内嵌模板没有漂移，\n" +
			"并以一个可重复的步骤升级 Go 模块与 npm 锁定的前端资产。\n" +
			"\n" +
			"check (default) verifies the generated version file and prints the inventory; sync regenerates it;\n" +
			"upgrade runs go get -u ./... plus go mod tidy, resolves the latest npm release of every locked asset,\n" +
			"downloads and re-locks them, then regenerates the version file.\n" +
			"check（默认）校验生成文件并打印版本清单；sync 重新生成；upgrade 执行 go get -u ./... 与 go mod tidy，\n" +
			"解析并重新锁定每个资产的 npm 最新版本，最后重新生成版本文件。",
		Usage: versionsUsageLine,
		Flags: registerVersionsFlags,
		Run:   runVersions,
		Positionals: []Positional{{
			Name:        "action",
			Description: "action: check/sync/upgrade | 动作",
			Values:      []string{versionsActionCheck, versionsActionSync, versionsActionUpgrade},
		}},
	})
}

// registerVersionsFlags declares the versions command's flags. | registerVersionsFlags 声明 versions 命令的标志。
func registerVersionsFlags(fs *pflag.FlagSet) {
	fs.String(flagVersionsDir, ".", "project directory | 项目目录")
	fs.Bool(flagVersionsDryRun, false, "report the plan without changing anything | 只报告计划，不做任何改动")
	fs.Bool(flagVersionsGo, true, "upgrade Go module dependencies (upgrade) | 升级 Go 模块依赖（upgrade）")
	fs.Bool(flagVersionsAssets, true, "upgrade npm-pinned front-end assets (upgrade) | 升级 npm 锁定的前端资产（upgrade）")
	versionsFlags = fs
}

// runVersions dispatches the versions subcommands. | runVersions 派发 versions 的子命令。
func runVersions(ctx context.Context, env *Env) error {
	args := versionsFlags.Args()
	sub := versionsActionCheck
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case versionsActionCheck:
		return runVersionsCheck(env)
	case versionsActionSync:
		return runVersionsSync(env)
	case versionsActionUpgrade:
		return runVersionsUpgrade(ctx, env)
	default:
		fmt.Fprintf(env.Err, "yd versions: unknown subcommand %q | yd versions：未知子命令 %q\n", sub, sub)
		fmt.Fprintf(env.Err, "usage | 用法: %s\n", versionsUsageLine)
		return errx.Detail(ErrUnknownCommand, "yd versions %s", sub)
	}
}

// versionsDir resolves the project directory against the working directory. | versionsDir 把项目目录解析到工作目录之下。
func versionsDir(env *Env, fs *pflag.FlagSet) string {
	dir, _ := fs.GetString(flagVersionsDir)
	if dir == "" {
		dir = "."
	}
	return resolveUserPath(env.WorkDir, dir)
}

// runVersionsCheck prints the inventory and fails when the generated file drifted. | runVersionsCheck 打印版本清单，并在生成文件漂移时失败。
func runVersionsCheck(env *Env) error {
	dir := versionsDir(env, versionsFlags)
	inv, err := versions.Load(env.FS, dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "module | 模块: %s\n", inv.ModulePath)
	fmt.Fprintf(env.Out, "go | Go: %s\n", inv.GoDirective)
	fmt.Fprintf(env.Out, "htmx | Htmx 版本: %s\n", inv.HtmxVersion)
	fmt.Fprintf(env.Out, "modules | 直接依赖: %d\n", len(inv.Modules))
	if !versions.HasScaffold(env.FS, dir) {
		fmt.Fprintln(env.Out, "scaffold | 脚手架: not present, generated check skipped | 不存在，跳过生成文件校验")
		return nil
	}
	if err := versions.Check(env.FS, dir); err != nil {
		fmt.Fprintf(env.Err, "generated | 生成文件: out of date | 已过期\n")
		fmt.Fprintf(env.Err, "  run | 运行: yd versions sync\n")
		return err
	}
	fmt.Fprintf(env.Out, "generated | 生成文件: up to date | 已同步\n")
	return nil
}

// runVersionsSync regenerates the scaffold version file. | runVersionsSync 重新生成脚手架版本文件。
func runVersionsSync(env *Env) error {
	dir := versionsDir(env, versionsFlags)
	if !versions.HasScaffold(env.FS, dir) {
		return errx.Detail(ErrNoScaffold, "dir %[1]q | 目录 %[1]q", dir)
	}
	path, err := versions.Sync(env.FS, dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "synced | 已同步: %s\n", path)
	return nil
}

// runVersionsUpgrade upgrades Go modules and npm-pinned assets, then regenerates the version file. | runVersionsUpgrade 升级 Go 模块与 npm 资产，随后重新生成版本文件。
func runVersionsUpgrade(ctx context.Context, env *Env) error {
	fs := versionsFlags
	dir := versionsDir(env, fs)
	client, clientErr := versionsClient(env)
	if clientErr != nil {
		return clientErr
	}
	dry, _ := fs.GetBool(flagVersionsDryRun)
	upgradeGo, _ := fs.GetBool(flagVersionsGo)
	upgradeAssets, _ := fs.GetBool(flagVersionsAssets)

	if !upgradeGo && !upgradeAssets {
		return errx.Detail(ErrVersionsNothing, "pass --go or --assets | 请传 --go 或 --assets")
	}

	if upgradeGo {
		if dry {
			fmt.Fprintln(env.Out, "would run | 将执行: go get -u ./... && go mod tidy")
		} else {
			if err := versionsRunGo(ctx, dir, env.Out, "get", "-u", "./..."); err != nil {
				return err
			}
			if err := versionsRunGo(ctx, dir, env.Out, "mod", "tidy"); err != nil {
				return err
			}
			fmt.Fprintln(env.Out, "go modules upgraded | Go 模块已升级")
		}
	}

	if upgradeAssets {
		inv, err := versions.Load(env.FS, dir)
		if err != nil {
			return err
		}
		plan, err := versions.PlanAssetUpgrades(ctx, client, inv)
		printAssetPlan(env, plan)
		if err != nil {
			return err
		}
		if dry {
			fmt.Fprintln(env.Out, "assets not downloaded (dry run) | 资产未下载（干跑）")
		} else {
			changed := 0
			for _, up := range plan {
				if up.Changed {
					changed++
				}
			}
			if changed > 0 {
				manifestPath := filepath.Join(dir, filepath.FromSlash(assets.DefaultManifestPath))
				m, err := assets.LoadManifest(env.FS, manifestPath)
				if err != nil {
					return err
				}
				// Keep the pre-upgrade entries so a failed download can be rolled back. Persisting
				// only the version/URL bump before the bytes arrive leaves an unlocked entry that a
				// retry considers current and never repairs. | 保存升级前的条目，以便下载失败时回滚。
				// 在字节到达之前只落盘版本/URL 会让条目处于未锁定状态，重试又会认为它已是最新，
				// 从此再也不会修复。
				snapshot := make(map[string]assets.Asset, len(m.Assets))
				for _, a := range m.Assets {
					snapshot[a.Name] = a
				}
				if n := versions.ApplyAssetUpgrades(m, plan); n > 0 {
					if err := m.Save(env.FS, manifestPath); err != nil {
						return err
					}
				}
				rep, updateErr := assets.Update(ctx, assets.UpdateOptions{
					FS:           env.FS,
					HTTPClient:   client,
					ManifestPath: manifestPath,
					Root:         filepath.Join(dir, filepath.FromSlash(assets.DefaultRoot)),
				})
				if rep != nil {
					fmt.Fprintf(env.Out, "assets | 资产: updated | 已更新 %d, unchanged | 未变化 %d, failed | 失败 %d\n",
						len(rep.Updated), len(rep.Unchanged), len(rep.Failed))
				}
				var updateErrs []error
				if updateErr != nil {
					updateErrs = append(updateErrs, updateErr)
				}
				if rep == nil {
					// Update failed before it could report per-asset results: restore every entry so
					// the lockfile stays exactly as it was. | Update 在给出逐资产结果之前就失败了：
					// 恢复全部条目，使锁文件保持原样。
					for name, old := range snapshot {
						if cur, ok := m.Find(name); ok {
							*cur = old
						}
					}
					if saveErr := m.Save(env.FS, manifestPath); saveErr != nil {
						updateErrs = append(updateErrs, saveErr)
					}
				} else if len(rep.Failed) > 0 {
					// Reload so the checksums of successfully updated assets are kept, then put the
					// failed entries back to their previous pinned version/checksum. | 重新加载以保留
					// 成功资产的校验和，再把失败条目恢复到原先锁定的版本与校验和。
					current, loadErr := assets.LoadManifest(env.FS, manifestPath)
					if loadErr != nil {
						updateErrs = append(updateErrs, loadErr)
					} else {
						m = current
					}
					restored := false
					for _, name := range rep.Failed {
						old, ok := snapshot[name]
						if !ok {
							continue
						}
						if cur, ok := m.Find(name); ok {
							*cur = old
							restored = true
						}
					}
					if restored {
						if saveErr := m.Save(env.FS, manifestPath); saveErr != nil {
							updateErrs = append(updateErrs, saveErr)
						}
					}
				}
				if err := errors.Join(updateErrs...); err != nil {
					return err
				}
			} else {
				fmt.Fprintln(env.Out, "assets already current | 资产已是最新")
			}
		}
	}

	if !versions.HasScaffold(env.FS, dir) {
		fmt.Fprintln(env.Out, "scaffold | 脚手架: not present, sync skipped | 不存在，已跳过生成")
		return nil
	}
	if dry {
		fmt.Fprintln(env.Out, "would regenerate | 将重新生成: "+versions.GeneratedPath)
		return nil
	}
	path, err := versions.Sync(env.FS, dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "synced | 已同步: %s\n", path)
	fmt.Fprintln(env.Out, "next | 下一步: run go build ./... && go vet ./... && go test ./... -race")
	return nil
}

// printAssetPlan writes one line per locked asset. | printAssetPlan 为每个已锁定资产写一行。
func printAssetPlan(env *Env, plan []versions.AssetUpgrade) {
	for _, up := range plan {
		switch {
		case up.Changed:
			fmt.Fprintf(env.Out, "  %-18s %s -> %s\n", up.Name, up.From, up.To)
		case up.To != "":
			fmt.Fprintf(env.Out, "  %-18s up to date | 已是最新 (%s)\n", up.Name, up.To)
		default:
			fmt.Fprintf(env.Out, "  %-18s skipped | 跳过: %s\n", up.Name, up.Reason)
		}
	}
}

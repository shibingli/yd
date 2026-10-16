// This file implements the assets command: it is a thin shell around the assets package and owns nothing
// but flag parsing, path resolution and the human-readable report. | 本文件实现 assets 命令：它只是 assets
// 包的一层薄壳，只负责标志解析、路径解析与人类可读的报告。

package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"

	"github.com/spf13/pflag"
	"github.com/yun-dev/yd/assets"
	"github.com/yun-dev/yd/errx"
)

// The assets command's flag names. | assets 命令的标志名。
const (
	// flagAssetsCheck reports without writing. | flagAssetsCheck 只报告不写入。
	flagAssetsCheck = "check"
	// flagAssetsForce re-locks changed content. | flagAssetsForce 对已变化的内容重新锁定。
	flagAssetsForce = "force"
	// flagAssetsOffline forbids every network access. | flagAssetsOffline 禁止一切网络访问。
	flagAssetsOffline = "offline"
	// flagAssetsSource adds a repeatable download source template. | flagAssetsSource 追加可重复的下载源模板。
	flagAssetsSource = "source"
	// flagAssetsManifest overrides the manifest path. | flagAssetsManifest 覆盖清单路径。
	flagAssetsManifest = "manifest"
	// flagAssetsRoot overrides the asset root directory. | flagAssetsRoot 覆盖资产根目录。
	flagAssetsRoot = "root"
	// flagAssetsURL is the canonical download URL of an added asset. | flagAssetsURL 是新增资产的权威下载地址。
	flagAssetsURL = "url"
	// flagAssetsPath is the destination path of an added asset. | flagAssetsPath 是新增资产的落盘路径。
	flagAssetsPath = "path"
	// flagAssetsVersion is the version of an added asset; it shadows the global --version on purpose. | flagAssetsVersion
	// 是新增资产的版本；它有意遮蔽全局 --version。
	flagAssetsVersion = "version"
	// flagAssetsKind is the kind of an added asset. | flagAssetsKind 是新增资产的类型。
	flagAssetsKind = "kind"
	// flagAssetsLicense is the license of an added asset. | flagAssetsLicense 是新增资产的许可。
	flagAssetsLicense = "license"
)

// assetsUsageLine is the usage line of the assets command. | assetsUsageLine 是 assets 命令的用法行。
const assetsUsageLine = "yd assets <list|update|verify|add> [flags] [args]"

// assetsHTTPClient is the HTTP client the assets command downloads through. It is a variable so tests can
// substitute a client whose transport they can observe, which is how "offline never touches the network"
// is asserted from the CLI side. A nil value lets the assets package build its own client. | assetsHTTPClient
// 是 assets 命令下载所用的 HTTP 客户端。它被声明为变量，便于测试替换成传输层可观测的客户端——这正是
// 从 CLI 侧断言「离线绝不触网」的方式。取值为 nil 时由 assets 包自行构造客户端。
var assetsHTTPClient *http.Client

// assetsFlags keeps the assets command's flag set, installed by registerAssetsFlags. | assetsFlags 保存 assets
// 命令的标志集，由 registerAssetsFlags 安装。
var assetsFlags *pflag.FlagSet

func init() {
	Register(Command{
		Name:  "assets",
		Short: "manage front-end assets | 管理前端资产",
		Long: "List, download, lock and verify the front-end assets recorded in the lock file.\n" +
			"列出、下载、锁定并校验记录在锁文件中的前端资产。",
		Usage: assetsUsageLine,
		Flags: registerAssetsFlags,
		Run:   runAssets,
	})
}

// registerAssetsFlags declares the flags shared by every assets subcommand. | registerAssetsFlags 声明所有 assets 子命令共享的标志。
func registerAssetsFlags(fs *pflag.FlagSet) {
	fs.Bool(flagAssetsCheck, false, "report what would change without writing | 只报告将要变化的内容而不写入")
	fs.Bool(flagAssetsForce, false, "re-lock assets whose content changed | 对内容已变化的资产重新锁定")
	fs.Bool(flagAssetsOffline, false, "forbid all network access | 禁止一切网络访问")
	fs.StringArray(flagAssetsSource, nil, "download source template, repeatable | 下载源模板，可重复")
	fs.String(flagAssetsManifest, "", "manifest path | 资产清单路径")
	fs.String(flagAssetsRoot, "", "asset root directory | 资产根目录")
	fs.String(flagAssetsURL, "", "canonical download URL (add) | 权威下载地址（add）")
	fs.String(flagAssetsPath, "", "destination path relative to the root (add) | 相对资产根的落盘路径（add）")
	fs.String(flagAssetsVersion, "", "asset version (add) | 资产版本（add）")
	fs.String(flagAssetsKind, "", "asset kind: js/css/wasm/html/binary (add) | 资产类型（add）")
	fs.String(flagAssetsLicense, "", "SPDX license identifier (add) | SPDX 许可标识（add）")
	assetsFlags = fs
}

// runAssets dispatches the assets subcommands. | runAssets 派发 assets 的子命令。
func runAssets(ctx context.Context, env *Env) error {
	fs := assetsFlags
	args := fs.Args()
	if len(args) == 0 {
		printAssetsUsage(env.Out)
		return ErrNoCommand
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return runAssetsList(env, fs)
	case "update":
		return runAssetsUpdate(ctx, env, fs)
	case "verify":
		return runAssetsVerify(env, fs)
	case "add":
		return runAssetsAdd(ctx, env, fs, rest)
	default:
		fmt.Fprintf(env.Err, "yd assets: unknown subcommand %q | yd assets：未知子命令 %q\n", sub, sub)
		printAssetsUsage(env.Err)
		return errx.Detail(ErrUnknownCommand, "yd assets %s", sub)
	}
}

// runAssetsList prints the lock file as a table. | runAssetsList 以表格打印锁文件。
func runAssetsList(env *Env, fs *pflag.FlagSet) error {
	manifestPath, _ := assetsPaths(env, fs)
	m, err := assets.LoadManifest(env.FS, manifestPath)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(env.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tVERSION\tSIZE\tKIND\tSHA256\tPATH\n")
	for i := range m.Assets {
		a := m.Assets[i]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			a.Name, dash(a.Version), formatSize(a.Size), dash(a.Kind), shortDigest(a.SHA256), a.Path)
	}
	if err := w.Flush(); err != nil {
		return errx.Wrap(err, ErrOutputFailed)
	}
	fmt.Fprintf(env.Out, "\n%s: %d\n", manifestPath, len(m.Assets))
	return nil
}

// runAssetsUpdate downloads, verifies and locks every asset. | runAssetsUpdate 下载、校验并锁定每项资产。
func runAssetsUpdate(ctx context.Context, env *Env, fs *pflag.FlagSet) error {
	manifestPath, root := assetsPaths(env, fs)
	rep, err := assets.Update(ctx, updateOptions(env, fs, manifestPath, root))

	if rep != nil {
		if len(rep.Updated) > 0 {
			verb, verbZh := "updated", "已更新"
			if flagBool(fs, flagAssetsCheck) {
				verb, verbZh = "would update", "待更新"
			}
			fmt.Fprintf(env.Out, "%s | %s: %s\n", verb, verbZh, strings.Join(rep.Updated, ", "))
		}
		if len(rep.Unchanged) > 0 {
			fmt.Fprintf(env.Out, "unchanged | 未变化: %s\n", strings.Join(rep.Unchanged, ", "))
		}
		if len(rep.Failed) > 0 {
			fmt.Fprintf(env.Out, "%s | %s: %s\n",
				colorize(env, ansiRed, "failed"), colorize(env, ansiRed, "失败"), strings.Join(rep.Failed, ", "))
		}
		if rep.Offline {
			fmt.Fprintf(env.Out, "offline | 离线: %s\n", manifestPath)
		}
		fmt.Fprintf(env.Out, "checked | 已检查: %d\n", rep.Checked)
	}
	return err
}

// runAssetsVerify checks every locked asset against the working tree, without any network access. | runAssetsVerify 在不访问网络的前提下依据锁定值校验工作区。
func runAssetsVerify(env *Env, fs *pflag.FlagSet) error {
	manifestPath, root := assetsPaths(env, fs)
	m, err := assets.LoadManifest(env.FS, manifestPath)
	if err != nil {
		return err
	}

	rep, err := assets.Verify(env.FS, m, root)
	if rep == nil {
		return err
	}
	printStatus := func(status, statusZh string, names []string, code string) {
		if len(names) == 0 {
			return
		}
		fmt.Fprintf(env.Out, "%s | %s: %s\n",
			colorize(env, code, status), colorize(env, code, statusZh), strings.Join(names, ", "))
	}
	printStatus("ok", "正常", rep.OK, ansiGreen)
	printStatus("mismatch", "不匹配", rep.Mismatch, ansiRed)
	printStatus("missing", "缺失", rep.Missing, ansiRed)
	fmt.Fprintf(env.Out, "checked | 已检查: %d\n", len(m.Assets))
	return err
}

// runAssetsAdd downloads one new asset, locks its checksum and appends it to the manifest. | runAssetsAdd 下载单个新资产、锁定校验和并追加到清单。
func runAssetsAdd(ctx context.Context, env *Env, fs *pflag.FlagSet, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(env.Err, "yd assets add: missing asset name | yd assets add：缺少资产名\n")
		printAssetsUsage(env.Err)
		return ErrNoCommand
	}
	url, path := flagString(fs, flagAssetsURL), flagString(fs, flagAssetsPath)
	if url == "" || path == "" {
		fmt.Fprintf(env.Err, "yd assets add: --url and --path are required | yd assets add：--url 与 --path 为必填\n")
		printAssetsUsage(env.Err)
		return errx.Detail(ErrFlagParse, "yd assets add %s", args[0])
	}

	manifestPath, root := assetsPaths(env, fs)
	locked, err := assets.Add(ctx, updateOptions(env, fs, manifestPath, root), assets.Asset{
		Name:    args[0],
		Version: flagString(fs, flagAssetsVersion),
		URL:     url,
		Path:    path,
		Kind:    flagString(fs, flagAssetsKind),
		License: flagString(fs, flagAssetsLicense),
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(env.Out, "locked | 已锁定: %s %s %s %d\n",
		locked.Name, dash(locked.Version), locked.SHA256, locked.Size)
	fmt.Fprintf(env.Out, "written | 已写入: %s\n", locked.LocalPath(root))
	return nil
}

// updateOptions assembles the assets package options from the parsed flags. | updateOptions 依据已解析的标志组装 assets 包选项。
func updateOptions(env *Env, fs *pflag.FlagSet, manifestPath, root string) assets.UpdateOptions {
	return assets.UpdateOptions{
		FS:           env.FS,
		ManifestPath: manifestPath,
		Root:         root,
		Offline:      flagBool(fs, flagAssetsOffline),
		CheckOnly:    flagBool(fs, flagAssetsCheck),
		Force:        flagBool(fs, flagAssetsForce),
		Sources:      flagStrings(fs, flagAssetsSource),
		HTTPClient:   assetsHTTPClient,
	}
}

// assetsPaths resolves the manifest path and the asset root against the working directory. | assetsPaths 相对工作目录解析清单路径与资产根目录。
func assetsPaths(env *Env, fs *pflag.FlagSet) (manifestPath, root string) {
	manifestPath = flagString(fs, flagAssetsManifest)
	if manifestPath == "" {
		manifestPath = assets.DefaultManifestPath
	}
	root = flagString(fs, flagAssetsRoot)
	if root == "" {
		root = assets.DefaultRoot
	}
	return joinWorkDir(env.WorkDir, manifestPath), joinWorkDir(env.WorkDir, root)
}

// printAssetsUsage writes the assets command's subcommand list. | printAssetsUsage 写出 assets 命令的子命令列表。
func printAssetsUsage(w io.Writer) {
	fmt.Fprintf(w, "yd assets - front-end asset governance | yd assets - 前端资产治理\n\n")
	fmt.Fprintf(w, "Usage | 用法:\n  %s\n\n", assetsUsageLine)
	fmt.Fprintf(w, "Subcommands | 子命令:\n")
	fmt.Fprintf(w, "  list    list every locked asset | 列出全部已锁定资产\n")
	fmt.Fprintf(w, "  update  download, verify and lock assets | 下载、校验并锁定资产\n")
	fmt.Fprintf(w, "  verify  verify the working tree offline | 离线校验工作区\n")
	fmt.Fprintf(w, "  add     add and lock one asset | 新增并锁定单个资产\n")
	fmt.Fprintf(w, "\nGlobal flags | 全局标志:\n%s", globalFlagUsages())
	fmt.Fprintf(w, "  -h, --help                 print help | 打印帮助\n")
}

// formatSize renders a byte count for humans, "-" when the size is unknown. | formatSize 为人类渲染字节数，未知时输出 "-"。
func formatSize(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
}

// shortDigest abbreviates a checksum for the table view. | shortDigest 为表格视图缩写校验和。
func shortDigest(sum string) string {
	if sum == "" {
		return "-"
	}
	if len(sum) > 8 {
		return sum[:8]
	}
	return sum
}

// dash replaces an empty value with a placeholder so table cells never look broken. | dash 用占位符替换空值，使表格单元不会显得残缺。
func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

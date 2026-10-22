// This file holds the installer: `go install` with GOBIN pinned to the project-local tool
// directory, plus the verification that follows it. | 本文件保存安装器：以固定在项目内工具目录的 GOBIN 运行
// `go install`，以及其后的验证。

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	yderrs "github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

// goToolchainHint tells the user how to obtain Go itself, since nothing can be installed without
// it. | goToolchainHint 说明如何获取 Go 本身，因为没有它什么都装不了。
const goToolchainHint = "install Go from https://go.dev/dl/ so that `go` is on PATH | 从 https://go.dev/dl/ 安装 Go，使 `go` 位于 PATH 上"

// installOutputLimit caps how much of the captured child output a failure carries. | installOutputLimit 限制失败信息携带的子进程输出长度。
//
// A failed `go install` explains itself in its last lines, and the whole transcript would drown the
// report it belongs to. | 失败的 `go install` 用最后几行解释自己，而整份记录会淹没它所属的报告。
const installOutputLimit = 400

// versionLimit caps a queried version string. | versionLimit 限制查询到的版本字符串长度。
const versionLimit = 120

// Install installs the named tools so that they land in <dir>/.yd/tools. | Install 安装指定名称的工具，使它们落在 <dir>/.yd/tools。
//
// An empty names slice means every Go-installable registry entry, which is also what InstallAll
// asks for. A name outside the registry stops the run before anything is touched and returns
// ErrUnknownTool; a named tool that is neither Go-installable nor downloadable (docker) is recorded
// in Report.Failed with ErrNotInstallable instead, so the remaining names still get their chance.
// tailwindcss is downloaded on demand when named explicitly. | names 为空表示注册表中所有 Go 可安装条目，
// InstallAll 请求的也正是它。注册表之外的名字会在动任何东西之前中止并返回 ErrUnknownTool；既不能 go install
// 也不能下载的具名工具（docker）改记入 Report.Failed 并带上 ErrNotInstallable，使其余名字仍有机会。
// tailwindcss 在显式点名时按需下载。
//
// One tool failing never interrupts the others: every outcome is recorded in the returned Report,
// and the error returned alongside it joins the individual failures so errors.Is finds
// ErrInstallFailed and ErrNotInstallable on it. The Report is nil only when the run refused to
// start - an unknown name, a needed go toolchain that is missing, or a tool directory that cannot
// be created. A run that only downloads tailwindcss does not need Go. | 单个工具失败绝不打断其它工具：
// 每个结果都记入返回的 Report，与它一同返回的错误聚合了各个失败，因此 errors.Is 能在其上找到 ErrInstallFailed
// 与 ErrNotInstallable。只有当运行拒绝开始时 Report 才为 nil——未知名称、需要 Go 而 Go 工具链缺失，
// 或工具目录无法创建。只下载 tailwindcss 的运行不需要 Go。
//
// Installing is the only step here that touches the network; Check, Missing and Hints stay offline. | 安装是本包唯一触网的步骤；
// Check、Missing 与 Hints 始终保持离线。
func Install(ctx context.Context, dir string, names []string, log *ydlog.Logger) (*Report, error) {
	return InstallWith(ctx, Options{Dir: dir}, names, log)
}

// InstallWith installs the named tools using the executables configured in o. | InstallWith 使用 o 中
// 配置的可执行文件安装指定名称的工具。
//
// The probe that decides "already available" is the same one CheckWith runs, so a tool configured
// through align.bin, ko.binary or tailwind.bin is not installed a second time, and a machine whose
// tools are all configured needs no Go toolchain at all. | 决定「已就绪」的探测与 CheckWith 完全相同，
// 因此经 align.bin、ko.binary 或 tailwind.bin 配置的工具不会被重复安装，工具都已配置好的机器也完全不需要
// Go 工具链。
func InstallWith(ctx context.Context, o Options, names []string, log *ydlog.Logger) (*Report, error) {
	o = o.normalized()
	if len(names) == 0 {
		return installPlan(ctx, o, installableTools(), log)
	}
	plan := make([]Tool, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		t, err := Lookup(name)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[t.Name]; dup {
			continue
		}
		seen[t.Name] = struct{}{}
		plan = append(plan, t)
	}
	return installPlan(ctx, o, plan, log)
}

// InstallAll installs every installable tool into <dir>/.yd/tools. | InstallAll 把每个可安装的工具安装到 <dir>/.yd/tools。
//
// It is the entry point of `yd new` and of `yd tools install` without arguments. The plan holds the
// Go-installable tools only: docker has no automatic route, and tailwindcss downloads a large
// release binary, so it is fetched only when named explicitly (`yd tools install tailwindcss`). Both
// appear in Hints with their retrieval route. | 它是 `yd new` 与不带参数的 `yd tools install` 的入口。
// 计划只包含 Go 可安装工具：docker 没有自动安装路径，tailwindcss 要下载较大的发布二进制，因此仅在显式
// 点名时获取（`yd tools install tailwindcss`）。两者都会在 Hints 中给出获取途径。
func InstallAll(ctx context.Context, dir string, log *ydlog.Logger) (*Report, error) {
	return InstallWith(ctx, Options{Dir: dir}, nil, log)
}

// installableTools returns the registry entries that `go install` can provide. | installableTools 返回 `go install` 能提供的注册表条目。
func installableTools() []Tool {
	var out []Tool
	for _, t := range Registry() {
		if t.GoInstall != "" {
			out = append(out, t)
		}
	}
	return out
}

// installPlan probes the plan, installs what is missing, and verifies the result. | installPlan 探测计划、安装缺失项，并验证结果。
func installPlan(ctx context.Context, o Options, plan []Tool, log *ydlog.Logger) (*Report, error) {
	ctx = contextOr(ctx)
	log = loggerOr(log)
	root := o.Dir

	report := &Report{}
	pending := make([]Tool, 0, len(plan))
	for _, t := range plan {
		st := probe(t, o)
		switch {
		case st.Found:
			log.Debug("tool already available | 工具已就绪", ydlog.Str("tool", t.Name), ydlog.Str("path", st.Path))
			report.Skipped = append(report.Skipped, st)
		case st.Err != nil:
			report.Failed = append(report.Failed, Failure{
				Name: t.Name,
				Err: yderrs.WrapDetail(st.Err, ErrInstallFailed,
					"cannot resolve %[1]s before installing it | 安装前无法解析 %[1]s", t.Name),
			})
		case t.GoInstall == "" && t.Name != toolTailwindcss:
			report.Failed = append(report.Failed, Failure{
				Name: t.Name,
				Err: yderrs.Detail(ErrNotInstallable,
					"%[1]s must be installed by hand | %[1]s 需要手动安装: %[2]s", t.Name, t.InstallHint),
			})
		default:
			pending = append(pending, t)
		}
	}
	if len(pending) == 0 {
		return report, failedError(report)
	}

	// The Go toolchain is looked up only when at least one pending tool is installed through
	// `go install`. A tailwindcss-only run downloads a release binary and must work on a machine
	// without Go. | 只有当至少一个待装工具经 `go install` 安装时才查找 Go 工具链。只安装 tailwindcss
	// 的运行下载发布二进制，必须在没有 Go 的机器上也能工作。
	needsGo := false
	for _, t := range pending {
		if t.GoInstall != "" {
			needsGo = true
			break
		}
	}
	goBin := ""
	if needsGo {
		var goErr error
		goBin, goErr = PathLookup("go")
		if goErr == nil && strings.TrimSpace(goBin) == "" {
			goErr = errEmptyPath
		}
		if goErr != nil {
			// The hint already says both what is missing and where to get it, so the detail does not
			// restate it. | 提示本身已经说明了缺什么与去哪里获取，因此细节不再重复。
			return nil, yderrs.WrapDetail(goErr, ErrNoGoToolchain, "%[1]s", goToolchainHint)
		}
	}

	toolDirAbs, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(LocalToolDir)))
	if err != nil {
		return nil, yderrs.WrapDetail(err, ErrInstallFailed,
			"cannot resolve the tool directory under %[1]q | 无法解析 %[1]q 下的工具目录", root)
	}
	if err := os.MkdirAll(toolDirAbs, 0o755); err != nil {
		return nil, yderrs.WrapDetail(err, ErrInstallFailed, "cannot create %[1]q | 无法创建 %[1]q", toolDirAbs)
	}

	env := installEnv(toolDirAbs)
	for _, t := range pending {
		if t.GoInstall == "" {
			// tailwindcss is the one downloadable tool; everything else without a GoInstall target
			// was already recorded as ErrNotInstallable above. | tailwindcss 是唯一可下载的工具；其余没有
			// GoInstall 目标的条目已在上方记为 ErrNotInstallable。
			st, err := installTailwind(ctx, o, t, log)
			if err != nil {
				log.Error("tailwindcss download failed | tailwindcss 下载失败", ydlog.Str("error", err.Error()))
				report.Failed = append(report.Failed, Failure{Name: t.Name, Err: yderrs.Wrap(err, ErrInstallFailed)})
				continue
			}
			report.Installed = append(report.Installed, st)
			log.Info("tool installed | 工具已安装",
				ydlog.Str("tool", t.Name), ydlog.Str("path", st.Path), ydlog.Str("version", st.Version))
			continue
		}
		log.Info("installing tool | 正在安装工具", ydlog.Str("tool", t.Name), ydlog.Str("target", t.GoInstall))
		out, err := execCommand()(ctx, goBin, []string{"install", t.GoInstall}, root, env)
		if err != nil {
			log.Error("tool installation failed | 工具安装失败", ydlog.Str("tool", t.Name), ydlog.Str("error", err.Error()))
			report.Failed = append(report.Failed, Failure{Name: t.Name, Err: installFailure(t, err, out)})
			continue
		}
		st := probe(t, o)
		if !st.Found {
			log.Error("installed tool is still not resolvable | 已安装的工具仍无法解析", ydlog.Str("tool", t.Name))
			report.Failed = append(report.Failed, Failure{
				Name: t.Name,
				Err: yderrs.Detail(ErrInstallFailed,
					"go install %[1]s succeeded but %[2]s is still not resolvable | go install %[1]s 成功，但仍无法解析 %[2]s",
					t.GoInstall, t.Name),
			})
			continue
		}
		st.Version = queryVersion(ctx, log, st.Path, t)
		report.Installed = append(report.Installed, st)
		log.Info("tool installed | 工具已安装",
			ydlog.Str("tool", t.Name), ydlog.Str("path", st.Path), ydlog.Str("version", st.Version))
	}

	return report, failedError(report)
}

// failedError joins the failures of a report into one error. | failedError 把报告中的失败聚合成一个错误。
//
// It returns nil for a clean run, so a caller can tell "nothing was installed" from "something went
// wrong"; every joined branch still matches ErrInstallFailed or ErrNotInstallable through errors.Is. | 运行干净时返回 nil，
// 使调用方能分辨「没装什么」与「出了错」；聚合出的每条分支仍可用 errors.Is 匹配 ErrInstallFailed 或 ErrNotInstallable。
func failedError(report *Report) error {
	if len(report.Failed) == 0 {
		return nil
	}
	errs := make([]error, 0, len(report.Failed))
	for _, f := range report.Failed {
		errs = append(errs, f.Err)
	}
	return errors.Join(errs...)
}

// DefaultGOPROXY is the module proxy chain used when the caller has not configured one. It tries
// two China-friendly mirrors before falling back to direct module downloads. | DefaultGOPROXY 是调用方
// 未配置时使用的模块代理链，先走两个国内可用的镜像，最后回退到直接下载。
const DefaultGOPROXY = "https://goproxy.io,https://goproxy.cn,https://mirrors.aliyun.com/goproxy/,direct"

// DefaultGOSUMDB is the checksum database used when the caller has not configured one. It is off
// because the proxy chain above may serve modules the public sumdb cannot verify (private mirrors
// and forks), and a failed verification would abort an otherwise usable installation. | DefaultGOSUMDB
// 是调用方未配置时使用的校验数据库。默认关闭，因为上面的代理链可能提供公共 sumdb 无法验证的模块
// （私有镜像与分支），校验失败会中止一次本来可用的安装。
const DefaultGOSUMDB = "off"

// installEnv builds the child environment of `go install`. | installEnv 构建 `go install` 的子进程环境。
//
// GOBIN is pinned to the absolute project-local directory and every inherited GOBIN is dropped, so
// the fresh binary lands on the third segment of the resolution chain instead of ~/go/bin. GOWORK is
// turned off because workspace mode rejects the `package@version` form of go install, and a go.work
// above the project is none of our business. | GOBIN 被固定在项目内的绝对目录，继承来的 GOBIN 一律丢弃，
// 使新二进制落在解析链的第三段而不是 ~/go/bin。GOWORK 被关闭，因为工作区模式拒绝 go install 的
// `package@version` 形式，而项目上方的 go.work 不归我们管。
//
// GOPATH, GOMODCACHE and GOCACHE are moved under <project>/.yd/tools as well: the module cache, the
// build cache and the installed binaries then belong to this project, so two projects cannot poison
// each other's cache and deleting .yd/tools is a complete reset. A GOPROXY or GOSUMDB the caller
// explicitly exported wins; empty values fall back to the defaults above. | GOPATH、GOMODCACHE 与
// GOCACHE 也移到 <项目>/.yd/tools 之下：模块缓存、构建缓存与安装的二进制都归属本项目，两个项目不会互相
// 污染缓存，删除 .yd/tools 就是一次彻底重置。调用方显式导出的 GOPROXY 或 GOSUMDB 优先；空值回退到上面的默认值。
func installEnv(toolDirAbs string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+7)
	proxy, sumdb := "", ""
	for _, kv := range base {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		switch {
		case strings.EqualFold(key, "GOBIN"),
			strings.EqualFold(key, "GOWORK"),
			strings.EqualFold(key, "GOCACHE"),
			strings.EqualFold(key, "GOPATH"),
			strings.EqualFold(key, "GOMODCACHE"):
			continue
		case strings.EqualFold(key, "GOPROXY"):
			if i := strings.IndexByte(kv, '='); i >= 0 {
				proxy = strings.TrimSpace(kv[i+1:])
			}
			continue
		case strings.EqualFold(key, "GOSUMDB"):
			if i := strings.IndexByte(kv, '='); i >= 0 {
				sumdb = strings.TrimSpace(kv[i+1:])
			}
			continue
		}
		out = append(out, kv)
	}
	if proxy == "" {
		proxy = DefaultGOPROXY
	}
	if sumdb == "" {
		sumdb = DefaultGOSUMDB
	}
	return append(out,
		"GOBIN="+toolDirAbs,
		"GOWORK=off",
		"GOPATH="+toolDirAbs,
		"GOMODCACHE="+filepath.Join(toolDirAbs, "pkg", "mod"),
		"GOCACHE="+filepath.Join(toolDirAbs, "go_cache"),
		"GOPROXY="+proxy,
		"GOSUMDB="+sumdb,
	)
}

// installFailure turns a failed `go install` into a report entry. | installFailure 把失败的 `go install` 变成一条报告条目。
//
// The wrapped error keeps both the child's exit error and ErrInstallFailed reachable, and the tail of
// the captured output rides along as the detail. | 被包装的错误同时让子进程的退出错误与 ErrInstallFailed 可达，
// 捕获输出的末尾作为细节一同携带。
func installFailure(t Tool, err error, output []byte) error {
	detail := strings.TrimSpace(tailHead(string(output), installOutputLimit))
	if detail == "" {
		return yderrs.WrapDetail(err, ErrInstallFailed,
			"go install %[1]s failed | go install %[1]s 失败", t.GoInstall)
	}
	return yderrs.WrapDetail(err, ErrInstallFailed,
		"go install %[1]s failed | go install %[1]s 失败: %[2]s", t.GoInstall, detail)
}

// queryVersion asks a freshly installed tool for its version. | queryVersion 向刚安装的工具询问版本。
//
// It is best effort: a tool that fails, prints nothing or answers nonsense leaves Version empty and
// never turns a successful installation into a failure. | 它尽力而为：工具执行失败、没有输出或回答无法辨认时
// Version 留空，绝不把一次成功的安装变成失败。
func queryVersion(ctx context.Context, log *ydlog.Logger, path string, t Tool) string {
	if len(t.VersionArgs) == 0 || strings.TrimSpace(path) == "" {
		return ""
	}
	out, err := execCommand()(ctx, path, append([]string(nil), t.VersionArgs...), "", nil)
	if err != nil {
		log.Debug("tool version query failed | 工具版本查询失败", ydlog.Str("tool", t.Name), ydlog.Str("error", err.Error()))
		return ""
	}
	v := firstLine(string(out))
	if len(v) > versionLimit {
		v = v[:versionLimit]
	}
	return v
}

// firstLine returns the first non-blank line of s with ANSI escapes removed and blanks trimmed. | firstLine 返回 s 中第一行非空内容，去掉 ANSI 转义序列并修剪空白。
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(stripANSI(strings.TrimRight(line, "\r")))
		if line != "" {
			return line
		}
	}
	return ""
}

// stripANSI removes the CSI escape sequences tools decorate their version output with. | stripANSI 去掉工具用于装饰版本输出的 CSI 转义序列。
//
// `tailwindcss --version` prints the version behind an SGR colour prefix, and that prefix must not
// reach the status table. | `tailwindcss --version` 会在版本前打印 SGR 颜色前缀，该前缀不能出现在状态表格里。
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		if i >= len(s) || s[i] != '[' {
			continue // A lone ESC carries no sequence. | 单独的 ESC 不构成序列。
		}
		i++
		for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
			i++
		}
		if i < len(s) {
			i++ // Skip the final byte of the sequence. | 跳过序列的结束字节。
		}
	}
	return b.String()
}

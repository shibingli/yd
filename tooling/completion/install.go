// This file owns installation: where the script goes, which line loads it, and the one case in which a
// user's startup file is touched. | 本文件负责安装：脚本落到哪里、哪一行加载它，以及唯一会改动用户启动文件的情形。

package completion

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shibingli/yd/core/errx"
	"github.com/spf13/afero"
)

// Marker is the substring that identifies a line as written by this package. | Marker 是标识某一行由本包写入的子串。
//
// It is looked up before anything is appended, which is what makes --append-rc idempotent: running the
// installation twice adds the loading line once. | 追加之前会先查它，这正是 --append-rc 幂等的原因：
// 把安装跑两遍，加载行只被加一次。
const Marker = "# yd completion"

// markerLine is the complete comment line written above the loading line. | markerLine 是写在加载行之前的完整注释行。
const markerLine = Marker + ` (added by "yd completion install") | yd 补全（由 "yd completion install" 添加）`

// backupSuffix is appended to the startup file path to name its first backup. | backupSuffix 被追加到启动文件路径之后，
// 作为第一个备份的名字。
const backupSuffix = ".yd-backup"

// BackupLimit bounds the numbered backups of one startup file. | BackupLimit 限定单个启动文件的编号备份数量。
//
// The limit exists so that a pathological loop cannot fill a disk; reaching it is reported as an error
// rather than overwriting an older backup, because a backup that was silently replaced is not a backup. | 该上限
// 存在的意义是不让异常循环填满磁盘；触及上限会报错而不是覆盖更早的备份——被悄悄替换掉的备份不算备份。
const BackupLimit = 100

// Request is everything the caller hands to Plan and Install. | Request 是调用方交给 Plan 与 Install 的全部内容。
//
// It embeds Registry, so one value carries both the scripts to render and the environment to render them
// for. A zero field never means "guess": FS, GOOS, Dir and Home all have a documented default. | 它内嵌
// Registry，因此一个值同时承载要渲染的脚本与渲染所处的环境。零值字段从不表示「猜」：FS、GOOS、Dir 与 Home
// 都有明确写出的默认行为。
type Request struct {
	// FS is the file system the script and the startup file are read and written through; nil means
	// the operating system file system. Commands pass their Env.FS, so tests run on a memory file
	// system and never touch a real home directory. | FS 是读写脚本与启动文件所经的文件系统；为 nil 表示操作系统
	// 文件系统。命令传入自己的 Env.FS，因此测试跑在内存文件系统上，绝不触碰真实主目录。
	FS afero.Fs
	// Getenv reads environment variables for detection and for the PowerShell profile path; nil means
	// os.Getenv. | Getenv 为探测与 PowerShell profile 路径读取环境变量；为 nil 表示 os.Getenv。
	Getenv func(string) string
	// GOOS is the target platform, "windows" on Windows; empty means the platform this binary runs on
	// is not consulted at all, which keeps Windows-only rules off. | GOOS 是目标平台，Windows 上为 "windows"；
	// 为空表示完全不参考二进制所在的平台，从而使 Windows 专有规则不生效。
	GOOS string
	// Dir is the directory that holds the ".yd" segment; empty means Home. | Dir 是承载 ".yd" 段的目录；为空表示 Home。
	Dir string
	// Home is the user home directory; empty means it is resolved from the environment. | Home 是用户主目录；
	// 为空表示从环境解析。
	Home string
	// Shell names the shell to install for; empty means Detect. | Shell 指定为之安装的 shell；为空表示走探测。
	Shell Shell
	// Registry is the command tree the scripts are generated from. | Registry 是生成脚本所依据的命令树。
	Registry
	// AppendRC asks for the loading line to be appended to the shell startup file, with a backup taken
	// first. It is the only request that ever modifies a user's file. | AppendRC 要求把加载行追加到 shell
	// 启动文件，并在之前先备份。它是唯一会修改用户文件的请求。
	AppendRC bool
}

// Plan is the resolved outcome of a Request: which shell, which file, which startup file and which line
// loads the script. | Plan 是一次 Request 的解析结果：哪个 shell、哪个文件、哪个启动文件，以及哪一行加载脚本。
//
// It is computed before anything is written, so a report or a confirmation prompt can name the exact
// paths first - which is the whole point of a tool that never silently edits a dotfile. | 它在任何写入之前算出，
// 因此报告或确认提问可以先点明确切路径——正是「绝不悄悄改动 dotfile」的工具的全部意义所在。
type Plan struct {
	// Detection is the shell and the evidence for it. | Detection 是 shell 及其依据。
	Detection Detection
	// Dir is the base directory that holds the ".yd" segment. | Dir 是承载 ".yd" 段的基准目录。
	Dir string
	// ScriptPath is the file the script is written to. | ScriptPath 是脚本写入的文件。
	ScriptPath string
	// RCFile is the startup file --append-rc would touch; it is never touched otherwise. | RCFile
	// 是 --append-rc 会改动的启动文件；除此之外它绝不会被触碰。
	RCFile string
	// LoadLine is the line that loads the installed script. | LoadLine 是加载已安装脚本的那一行。
	LoadLine string
}

// Result reports what an installation actually did. | Result 报告一次安装实际做了什么。
type Result struct {
	// Plan is the resolution the installation followed. | Plan 是本次安装所依据的解析结果。
	Plan
	// BackupPath is the file the startup file was copied to before it was changed; it is empty when the
	// startup file was left alone, including when a backup turned out to be unnecessary. | BackupPath
	// 是启动文件被改动前复制到的文件；启动文件未被改动时为空，其中也包括「后来发现无需备份」的情形。
	BackupPath string
	// Bytes is the size of the written script. | Bytes 是写出脚本的字节数。
	Bytes int
	// RCAppended reports that the loading line was appended by this run. | RCAppended 报告本次运行追加了加载行。
	RCAppended bool
	// RCSkipped reports that the startup file already carried the marker, so nothing was appended and
	// the file was left byte for byte as it was. | RCSkipped 报告启动文件已带标记，因此没有追加，
	// 文件保持逐字节不变。
	RCSkipped bool
}

// Resolve turns a Request into a Plan without touching the file system. | Resolve 在不触碰文件系统的前提下把
// Request 解析为 Plan。
func Resolve(req Request) (Plan, error) {
	getenv := req.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	det, err := resolveShell(req, getenv)
	if err != nil {
		return Plan{}, err
	}
	home, err := resolveHome(req, getenv)
	if err != nil {
		return Plan{}, err
	}
	base := strings.TrimSpace(req.Dir)
	if base == "" {
		base = home
	}
	// A relative --dir would make the rc load line relative, and an rc file is later read from the
	// home directory, not from the project, so completion would silently never load. | 相对的
	// --dir 会让 rc 加载行也变成相对路径，而 rc 文件之后是在主目录而不是项目目录读取的，导致补全
	// 静默失效。
	base = absoluteForGOOS(base, req.GOOS)
	script := filepath.Join(base, ".yd", det.Shell.FileName())
	return Plan{
		Detection:  det,
		Dir:        base,
		ScriptPath: script,
		RCFile:     StartupFile(det.Shell, home, req.GOOS, getenv),
		LoadLine:   LoadLine(det.Shell, script),
	}, nil
}

// resolveShell uses the requested shell or detects one. | resolveShell 使用请求指定的 shell 或探测一个。
func resolveShell(req Request, getenv func(string) string) (Detection, error) {
	if strings.TrimSpace(string(req.Shell)) != "" {
		return override(string(req.Shell))
	}
	return Detect(req.GOOS, getenv)
}

// resolveHome returns the explicit home, else the one the environment reports. | resolveHome 返回显式指定的主目录，
// 否则返回环境上报的主目录。
//
// The environment is consulted rather than os.UserHomeDir so that a caller can inject it, which is what
// keeps a test from ever resolving the real home directory of the machine it runs on. os.UserHomeDir is
// only the last resort, for a caller that injected nothing. | 这里查环境而不是用 os.UserHomeDir，是为了让调用方
// 可以注入它，从而使测试永远不会解析到所跑机器上的真实主目录。os.UserHomeDir 只是最后一道兜底，供什么都没注入的
// 调用方使用。
//
// A home directory is required even when Dir overrides where the script is written, because the startup
// file is named relative to it and an installation that cannot name it could not honour --append-rc
// either; failing here is what keeps --append-rc from being silently ignored. | 即便 Dir 覆盖了脚本的写入位置，
// 主目录依然必需，因为启动文件是相对它命名的，而说不清它的安装也无法兑现 --append-rc；在这里失败，正是
// --append-rc 不会被悄悄忽略的原因。
func resolveHome(req Request, getenv func(string) string) (string, error) {
	if home := strings.TrimSpace(req.Home); home != "" {
		return absoluteForGOOS(home, req.GOOS), nil
	}
	// On Windows USERPROFILE is the native variable; HOME is usually set by Git Bash or Cygwin to
	// an MSYS path (/c/Users/...) that native Windows APIs do not understand. Prefer the native
	// variable there, and convert the POSIX form when it is the only one available. On POSIX the
	// order stays HOME first. | Windows 上 USERPROFILE 是原生变量；HOME 通常由 Git Bash 或 Cygwin
	// 设为 MSYS 路径（/c/Users/...），原生 Windows API 不认识。因此在 Windows 上优先原生变量，
	// 只有它缺失时才转换 POSIX 形式；POSIX 平台仍以 HOME 优先。
	names := []string{"HOME", "USERPROFILE"}
	if req.GOOS == "windows" {
		names = []string{"USERPROFILE", "HOME"}
	}
	for _, name := range names {
		if home := strings.TrimSpace(getenv(name)); home != "" {
			return absoluteForGOOS(home, req.GOOS), nil
		}
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return absoluteForGOOS(home, req.GOOS), nil
	}
	return "", errx.Detail(ErrNoHomeDir, "set HOME (or USERPROFILE on Windows) | 请设置 HOME（Windows 上为 USERPROFILE）")
}

// absoluteForGOOS turns a home or --dir value into an absolute path for the target platform. |
// absoluteForGOOS 把主目录或 --dir 取值转换为目标平台上的绝对路径。
//
// The conversion is platform-directed rather than host-directed: completion may be inspected for a
// platform other than the one running the test, and a POSIX absolute path must stay absolute on
// Windows so the reported load line matches the shell that will read it. MSYS and Cygwin drive
// forms are converted on Windows because native APIs reject them. | 转换按目标平台而不是宿主平台进行：
// 补全可能针对与测试宿主机不同的平台进行检查，而 POSIX 绝对路径在 Windows 上也必须保持绝对，报告出的加载行
// 才能被真正读取它的 shell 使用。MSYS 与 Cygwin 的盘符形式在 Windows 上会被转换，因为原生 API 拒绝它们。
func absoluteForGOOS(p, goos string) string {
	p = nativePathForGOOS(strings.TrimSpace(p), goos)
	if p == "" || isAbsoluteForGOOS(p, goos) {
		return p
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// nativePathForGOOS converts an MSYS/Cygwin drive path to Windows syntax when goos is windows. |
// nativePathForGOOS 在 goos 为 windows 时把 MSYS/Cygwin 盘符路径转换为 Windows 语法。
//
//	/c/Users/alice          -> C:/Users/alice
//	/cygdrive/d/work        -> D:/work
func nativePathForGOOS(p, goos string) string {
	if goos != "windows" || p == "" {
		return p
	}
	if len(p) >= 3 && p[0] == '/' && isASCIILetter(p[1]) && p[2] == '/' {
		return strings.ToUpper(p[1:2]) + ":/" + p[3:]
	}
	const cygdrive = "/cygdrive/"
	if len(p) > len(cygdrive)+1 && strings.EqualFold(p[:len(cygdrive)], cygdrive) &&
		isASCIILetter(p[len(cygdrive)]) && p[len(cygdrive)+1] == '/' {
		return strings.ToUpper(p[len(cygdrive):len(cygdrive)+1]) + ":/" + p[len(cygdrive)+2:]
	}
	return p
}

// isAbsoluteForGOOS reports whether p is absolute on the target platform. | isAbsoluteForGOOS 报告 p
// 在目标平台上是否为绝对路径。
func isAbsoluteForGOOS(p, goos string) bool {
	if goos == "windows" {
		return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) ||
			(len(p) >= 2 && p[1] == ':')
	}
	return strings.HasPrefix(p, "/")
}

// isASCIILetter reports whether b is an ASCII letter. | isASCIILetter 报告 b 是否为 ASCII 字母。
func isASCIILetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// Install writes the script and, only when asked, appends the loading line to the startup file. | Install
// 写入脚本，并且只在被要求时把加载行追加到启动文件。
//
// The script itself is always rewritten, so re-installing is how a user picks up the commands and flags
// added since the last run. The startup file is a different matter: it is read first, the marker is
// looked for, and when it is already there the file is left byte for byte untouched - no backup, no
// write, no new mtime. | 脚本每次都会被重写，因此重装就是用户获取上次运行以来新增命令与标志的方式。
// 启动文件则不同：先读取、先找标记，标记已存在时文件逐字节保持原样——不备份、不写入、mtime 不变。
func Install(req Request) (Result, error) {
	plan, err := Resolve(req)
	if err != nil {
		return Result{}, err
	}
	script, err := Generate(plan.Detection.Shell, req.Registry)
	if err != nil {
		return Result{}, err
	}
	fsys := req.FS
	if fsys == nil {
		fsys = afero.NewOsFs()
	}
	if dir := filepath.Dir(plan.ScriptPath); dir != "" && dir != "." {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			return Result{}, errx.WrapDetail(err, ErrWriteScript, "%s", dir)
		}
	}
	if err := afero.WriteFile(fsys, plan.ScriptPath, []byte(script), 0o644); err != nil {
		return Result{}, errx.WrapDetail(err, ErrWriteScript, "%s", plan.ScriptPath)
	}

	res := Result{Plan: plan, Bytes: len(script)}
	if !req.AppendRC || plan.RCFile == "" {
		return res, nil
	}
	backup, appended, err := appendStartup(fsys, plan)
	if err != nil {
		return res, err
	}
	res.BackupPath, res.RCAppended, res.RCSkipped = backup, appended, !appended
	return res, nil
}

// appendStartup adds the marker line and the loading line to the startup file, backing the file up
// first when it already exists. | appendStartup 把标记行与加载行加入启动文件，文件已存在时先做备份。
//
// The returned backup is empty when nothing was appended. | 未发生追加时返回的备份路径为空。
func appendStartup(fsys afero.Fs, plan Plan) (backup string, appended bool, err error) {
	original, err := readStartup(fsys, plan.RCFile)
	if err != nil {
		return "", false, err
	}
	if strings.Contains(string(original), Marker) {
		return "", false, nil
	}

	if original != nil {
		backup, err = writeBackup(fsys, plan.RCFile, original)
		if err != nil {
			return "", false, err
		}
	}

	block := buildBlock(original, plan.LoadLine)
	if dir := filepath.Dir(plan.RCFile); dir != "" && dir != "." {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			return "", false, errx.WrapDetail(err, ErrWriteStartup, "%s", dir)
		}
	}
	if err := afero.WriteFile(fsys, plan.RCFile, block, 0o644); err != nil {
		return "", false, errx.WrapDetail(err, ErrWriteStartup, "%s", plan.RCFile)
	}
	return backup, true, nil
}

// readStartup reads the startup file, distinguishing "absent" from "unreadable". | readStartup 读取启动文件，
// 并把「不存在」与「读不了」区分开。
//
// A missing file is normal - a fresh account has no .bashrc - and is reported as nil, while any other
// failure is an error, because appending to a file that could not be read would silently discard it. | 文件不存在
// 是正常的（新账号没有 .bashrc）并以 nil 报告；其它失败都是错误，因为向一个读不出来的文件追加会把它悄悄丢掉。
func readStartup(fsys afero.Fs, path string) ([]byte, error) {
	data, err := afero.ReadFile(fsys, path)
	switch {
	case err == nil:
		return data, nil
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, os.ErrNotExist):
		return nil, nil
	default:
		return nil, errx.WrapDetail(err, ErrReadStartup, "%s", path)
	}
}

// buildBlock renders the whole new content of a startup file: the original bytes, a separator newline
// when the original does not end with one, the marker line and the loading line. | buildBlock 渲染启动文件的
// 完整新内容：原始字节、原文件未以换行结尾时补上的分隔换行、标记行与加载行。
//
// The original text is carried through verbatim rather than merely appended to, so a file whose last
// line has no trailing newline does not end up gluing the marker onto that line. | 原文被逐字带上而不是简单追加，
// 因此最后一行没有换行的文件不会把标记粘到那一行上。
func buildBlock(original []byte, loadLine string) []byte {
	out := make([]byte, 0, len(original)+len(markerLine)+len(loadLine)+4)
	out = append(out, original...)
	if len(original) > 0 && original[len(original)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, markerLine...)
	out = append(out, '\n')
	out = append(out, loadLine...)
	out = append(out, '\n')
	return out
}

// writeBackup copies the original bytes to the first free numbered backup path. | writeBackup 把原始字节复制到
// 第一个空闲的编号备份路径。
func writeBackup(fsys afero.Fs, path string, original []byte) (string, error) {
	for i := 1; i <= BackupLimit; i++ {
		candidate := path + backupSuffix
		if i > 1 {
			candidate = candidate + "." + strconv.Itoa(i)
		}
		if exists, err := afero.Exists(fsys, candidate); err == nil && exists {
			continue
		}
		if err := afero.WriteFile(fsys, candidate, original, 0o644); err != nil {
			return "", errx.WrapDetail(err, ErrWriteBackup, "%s", candidate)
		}
		return candidate, nil
	}
	return "", errx.Detail(ErrWriteBackup, "%s (limit | 上限 %d)", path, BackupLimit)
}

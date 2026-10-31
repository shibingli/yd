// Package scaffold 生成 yd 全栈项目骨架（`yd new` 的实现基础）。
// Package scaffold generates yd full-stack project skeletons (the backing of `yd new`).
//
// Responsibility boundary： | 职责边界
//   - Only renders templates into a target filesystem and keeps writes idempotent; CLI flag parsing lives in the cli package. | 只负责「模板 → 目标文件系统」的渲染与幂等写入，不负责 CLI 参数解析（见 cli 包）；
//   - Templates are embedded from tooling/scaffold/templates via go:embed; `_name_` path placeholders become the project name, so every artefact lands under <name>/. | 模板以 go:embed 内嵌在 tooling/scaffold/templates 下，路径中的 `_name_` 占位符替换为项目名，因此全部产出落在 <name>/ 之下；
//   - web/assets.lock.json is taken from the framework's embedded web tree (web.FS) and trimmed to the selected Htmx extensions, keeping one source of truth for the manifest. | web/assets.lock.json 取自框架内嵌的 web 目录树（web.FS），按所选 Htmx 扩展裁剪后写入，保证清单只有一份真源；
//   - Identical existing files are recorded as Skipped; differing files without Force yield ErrConflict. | 目标文件已存在且内容相同 → 计入 Skipped；内容不同且未 Force → 返回 ErrConflict。
package scaffold

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
	"github.com/spf13/afero"
)

// Result describes the output of a single generation run. | Result 描述一次生成的产出清单。
//
// 路径一律是目标文件系统上的相对路径，已带项目目录前缀（形如 <name>/go.mod），以 "/" 分隔。
// Every path is relative to the target filesystem and already carries the project directory prefix
// (as in <name>/go.mod), separated by "/".
type Result struct {
	// ModulePath is the module path written into go.mod. | ModulePath 是写入 go.mod 的模块路径。
	ModulePath string
	// Files lists the paths really written (or overwritten), sorted. | Files 是本次真实写入（或覆盖）的路径，已排序。
	Files []string
	// Skipped lists the paths left untouched because their content already matched, sorted. | Skipped 是内容与模板一致、因而未改动的路径，已排序。
	Skipped []string
}

// Generate renders the templates described by o into fs and returns the produced file list. | Generate 依据 o 渲染模板并写入 fs，返回产出清单。
//
// Behaviour： | 行为
//   - Every file is rendered into memory and conflict-checked first, so no half-finished tree is left behind except on conflict. | 先渲染全部文件到内存并检查冲突，因此除冲突外不会留下半成品；
//   - Existing files whose bytes already match are recorded in Result.Skipped and never touched. | 已存在且内容逐字节相同的文件计入 Result.Skipped 且不被触碰；
//   - Existing but different files without o.Force return an error (detectable via errors.Is(ErrConflict)) listing every conflicting path. | 已存在但内容不同且未开启 o.Force 时返回错误（errors.Is 判定 ErrConflict），错误文案列出全部冲突路径；
//   - A cancelled ctx returns ErrCanceled wrapping ctx.Err(). | ctx 被取消时返回包装了 ctx.Err() 的 ErrCanceled；
//   - No logging happens when log is nil. | log 为 nil 时不输出日志。
func Generate(ctx context.Context, o Options, fs afero.Fs, log *ydlog.Logger) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrap(err, ErrCanceled)
	}

	normalized, err := normalize(o)
	if err != nil {
		return nil, err
	}
	if log != nil {
		log.Info("generating project | 正在生成项目",
			ydlog.Str("name", normalized.Name),
			ydlog.Str("module", normalized.Module),
			ydlog.Strs("features", featuresOf(normalized)),
		)
	}

	planned, err := plan(normalized)
	if err != nil {
		return nil, err
	}

	conflicts, err := detectConflicts(fs, planned)
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 && !normalized.Force {
		return nil, errs.Detail(ErrConflict,
			"rerun with --force to overwrite | 可加 --force 覆盖: %s", strings.Join(conflicts, ", "))
	}

	result := &Result{ModulePath: normalized.Module}
	for _, f := range planned {
		if err := ctx.Err(); err != nil {
			return nil, errs.Wrap(err, ErrCanceled)
		}

		written, err := afs.WriteFileIfChanged(fs, f.path, f.content)
		if err != nil {
			return nil, errs.WrapDetail(err, ErrWriteFile, "path=%[1]s | 路径=%[1]s", f.path)
		}
		if written {
			result.Files = append(result.Files, f.path)
			continue
		}
		result.Skipped = append(result.Skipped, f.path)
	}

	if log != nil {
		log.Info("project generated | 项目已生成",
			ydlog.Str("name", normalized.Name),
			ydlog.Int("files", len(result.Files)),
			ydlog.Int("skipped", len(result.Skipped)),
		)
	}
	return result, nil
}

// windowsDeviceName reports whether name is a reserved DOS device name on Windows. | windowsDeviceName 报告
// name 是否为 Windows 保留的 DOS 设备名。
//
// The check ignores case and the part after the first dot, because Win32 resolves CON.txt and
// com1.log to the device as well. | 检查忽略大小写与第一个点之后的部分，因为 Win32 同样会把 CON.txt 与
// com1.log 解析到设备。
func windowsDeviceName(name string) bool {
	base := strings.TrimRight(name, ". ")
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToUpper(base)
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

// normalize validates and completes the options and returns a copy ready for rendering. | normalize 校验并补齐选项，返回可直接用于渲染的副本。
func normalize(o Options) (Options, error) {
	o.Name = strings.TrimSpace(o.Name)
	if o.Name == "" {
		return Options{}, ErrNameRequired
	}
	if !namePattern.MatchString(o.Name) {
		return Options{}, errs.Detail(ErrNameInvalid, "%[1]q (want: a letter first, then letters, digits, `._-`) | %[1]q（要求：首字符为字母，其后仅限字母、数字、`._-`）", o.Name)
	}
	// The name becomes a directory, so it must also be a legal path segment on Windows: a trailing
	// dot is stripped by Win32 and a device name (CON, NUL, COM1, ...) addresses a device instead
	// of a directory. Rejecting them on every platform keeps a project created on Linux checkout-
	// able on Windows. | 项目名会成为目录名，因此也必须是 Windows 上合法的路径段：Win32 会去掉结尾的点，
	// 设备名（CON、NUL、COM1 等）指向设备而不是目录。在所有平台拒绝这些名称，使 Linux 上创建的项目
	// 能在 Windows 上检出。
	if strings.HasSuffix(o.Name, ".") {
		return Options{}, errs.Detail(ErrNameInvalid, "%[1]q must not end with a dot (Windows strips it) | %[1]q 不得以点结尾（Windows 会去掉它）", o.Name)
	}
	if windowsDeviceName(o.Name) {
		return Options{}, errs.Detail(ErrNameInvalid, "%[1]q is a reserved device name on Windows | %[1]q 是 Windows 的保留设备名", o.Name)
	}

	o.Module = strings.TrimSpace(o.Module)
	if o.Module == "" {
		o.Module = defaultModule(o.Name)
	}
	if !modulePattern.MatchString(o.Module) {
		return Options{}, errs.Detail(ErrModuleInvalid, "%[1]q (want: host/org/project) | %[1]q（期望: host/org/project）", o.Module)
	}

	o.DB = strings.TrimSpace(o.DB)
	if o.DB == "" {
		o.DB = DBDefault
	}
	if !containsString(dbValues(), o.DB) {
		return Options{}, errs.Detail(ErrDBUnsupported, "%[1]q (want: %[2]s) | %[1]q（期望: %[2]s）", o.DB, strings.Join(dbValues(), ", "))
	}

	o.License = strings.TrimSpace(o.License)
	if o.License == "" {
		o.License = LicenseApache2
	}
	if o.License != LicenseApache2 {
		return Options{}, errs.Detail(ErrLicenseUnsupported, "%[1]q (want: %[2]s) | %[1]q（期望: %[2]s）", o.License, LicenseApache2)
	}

	exts, err := ParseExtensions(strings.Join(o.HtmxExts, ","))
	if err != nil {
		return Options{}, err
	}
	o.HtmxExts = EffectiveExtensions(exts)

	o.LocalReplace = filepath.ToSlash(strings.TrimSpace(o.LocalReplace))
	for _, r := range o.LocalReplace {
		if r < 0x20 || r == 0x7f {
			return Options{}, errs.Detail(ErrNameInvalid, "local replace path contains a control character | 本地替换路径包含控制字符")
		}
	}

	o.YdVersion = strings.TrimSpace(o.YdVersion)
	// A dirty worktree build reports vX.Y.Z+dirty, which the module proxy cannot fetch. |
	// 脏工作区构建报告 vX.Y.Z+dirty，模块代理无法获取该版本。
	o.YdVersion = strings.TrimSuffix(o.YdVersion, "+dirty")
	if o.YdVersion == "dev" || o.YdVersion == "(devel)" {
		o.YdVersion = ""
	}
	return o, nil
}

// detectConflicts 返回内容与模板不同、且已存在于 fs 上的目标路径（按渲染顺序即字典序）。
// detectConflicts returns the existing target paths whose content differs from the templates (in rendered, i.e. lexicographic, order).
func detectConflicts(fs afero.Fs, planned []plannedFile) ([]string, error) {
	var conflicts []string
	for _, f := range planned {
		if !afs.Exists(fs, f.path) {
			continue
		}
		old, err := afero.ReadFile(fs, f.path)
		if err != nil {
			return nil, errs.WrapDetail(err, ErrTemplateRead, "path=%[1]s | 路径=%[1]s", f.path)
		}
		if !bytes.Equal(old, f.content) {
			conflicts = append(conflicts, f.path)
		}
	}
	return conflicts, nil
}

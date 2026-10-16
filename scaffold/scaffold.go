// Package scaffold 生成 yd 全栈项目骨架（`yd new` 的实现基础）。
// Package scaffold generates yd full-stack project skeletons (the backing of `yd new`).
//
// 职责边界 | Responsibility boundary：
//   - 只负责「模板 → 目标文件系统」的渲染与幂等写入，不负责 CLI 参数解析（见 cli 包）； | Only renders templates into a target filesystem and keeps writes idempotent; CLI flag parsing lives in the cli package.
//   - 模板以 go:embed 内嵌在 scaffold/templates 下，路径中的 `_name_` 占位符替换为项目名，因此全部产出落在 <name>/ 之下； | Templates are embedded from scaffold/templates via go:embed; `_name_` path placeholders become the project name, so every artefact lands under <name>/.
//   - web/assets.lock.json 取自框架内嵌的 web 目录树（web.FS），按所选 Htmx 扩展裁剪后写入，保证清单只有一份真源； | web/assets.lock.json is taken from the framework's embedded web tree (web.FS) and trimmed to the selected Htmx extensions, keeping one source of truth for the manifest.
//   - 目标文件已存在且内容相同 → 计入 Skipped；内容不同且未 Force → 返回 ErrConflict。 | Identical existing files are recorded as Skipped; differing files without Force yield ErrConflict.
package scaffold

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"github.com/shibingli/yd/afs"
	"github.com/shibingli/yd/errx"
	"github.com/shibingli/yd/logx"
	"github.com/spf13/afero"
)

// Result 描述一次生成的产出清单。 | Result describes the output of a single generation run.
//
// 路径一律是目标文件系统上的相对路径，已带项目目录前缀（形如 <name>/go.mod），以 "/" 分隔。
// Every path is relative to the target filesystem and already carries the project directory prefix
// (as in <name>/go.mod), separated by "/".
type Result struct {
	// ModulePath 是写入 go.mod 的模块路径。 | ModulePath is the module path written into go.mod.
	ModulePath string
	// Files 是本次真实写入（或覆盖）的路径，已排序。 | Files lists the paths really written (or overwritten), sorted.
	Files []string
	// Skipped 是内容与模板一致、因而未改动的路径，已排序。 | Skipped lists the paths left untouched because their content already matched, sorted.
	Skipped []string
}

// Generate 依据 o 渲染模板并写入 fs，返回产出清单。 | Generate renders the templates described by o into fs and returns the produced file list.
//
// 行为 | Behaviour：
//   - 先渲染全部文件到内存并检查冲突，因此除冲突外不会留下半成品； | Every file is rendered into memory and conflict-checked first, so no half-finished tree is left behind except on conflict.
//   - 已存在且内容逐字节相同的文件计入 Result.Skipped 且不被触碰； | Existing files whose bytes already match are recorded in Result.Skipped and never touched.
//   - 已存在但内容不同且未开启 o.Force 时返回错误（errors.Is 判定 ErrConflict），错误文案列出全部冲突路径； | Existing but different files without o.Force return an error (detectable via errors.Is(ErrConflict)) listing every conflicting path.
//   - ctx 被取消时返回包装了 ctx.Err() 的 ErrCanceled； | A cancelled ctx returns ErrCanceled wrapping ctx.Err().
//   - log 为 nil 时不输出日志。 | No logging happens when log is nil.
func Generate(ctx context.Context, o Options, fs afero.Fs, log *logx.Logger) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, errx.Wrap(err, ErrCanceled)
	}

	normalized, err := normalize(o)
	if err != nil {
		return nil, err
	}
	if log != nil {
		log.Info("generating project | 正在生成项目",
			logx.Str("name", normalized.Name),
			logx.Str("module", normalized.Module),
			logx.Strs("features", featuresOf(normalized)),
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
		return nil, errx.Detail(ErrConflict,
			"rerun with --force to overwrite | 可加 --force 覆盖: %s", strings.Join(conflicts, ", "))
	}

	result := &Result{ModulePath: normalized.Module}
	for _, f := range planned {
		if err := ctx.Err(); err != nil {
			return nil, errx.Wrap(err, ErrCanceled)
		}

		written, err := afs.WriteFileIfChanged(fs, f.path, f.content)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrWriteFile, "path=%s", f.path)
		}
		if written {
			result.Files = append(result.Files, f.path)
			continue
		}
		result.Skipped = append(result.Skipped, f.path)
	}

	if log != nil {
		log.Info("project generated | 项目已生成",
			logx.Str("name", normalized.Name),
			logx.Int("files", len(result.Files)),
			logx.Int("skipped", len(result.Skipped)),
		)
	}
	return result, nil
}

// normalize 校验并补齐选项，返回可直接用于渲染的副本。 | normalize validates and completes the options and returns a copy ready for rendering.
func normalize(o Options) (Options, error) {
	o.Name = strings.TrimSpace(o.Name)
	if o.Name == "" {
		return Options{}, ErrNameRequired
	}
	if !namePattern.MatchString(o.Name) {
		return Options{}, errx.Detail(ErrNameInvalid, "%q (want: letter first, then letters/digits/._-)", o.Name)
	}

	o.Module = strings.TrimSpace(o.Module)
	if o.Module == "" {
		o.Module = defaultModule(o.Name)
	}
	if !modulePattern.MatchString(o.Module) {
		return Options{}, errx.Detail(ErrModuleInvalid, "%q (want: host/org/project)", o.Module)
	}

	o.DB = strings.TrimSpace(o.DB)
	if o.DB == "" {
		o.DB = DBDefault
	}
	if !containsString(dbValues(), o.DB) {
		return Options{}, errx.Detail(ErrDBUnsupported, "%q (want: %s)", o.DB, strings.Join(dbValues(), ", "))
	}

	o.License = strings.TrimSpace(o.License)
	if o.License == "" {
		o.License = LicenseApache2
	}
	if o.License != LicenseApache2 {
		return Options{}, errx.Detail(ErrLicenseUnsupported, "%q (want: %s)", o.License, LicenseApache2)
	}

	exts, err := ParseExtensions(strings.Join(o.HtmxExts, ","))
	if err != nil {
		return Options{}, err
	}
	o.HtmxExts = exts

	o.LocalReplace = filepath.ToSlash(strings.TrimSpace(o.LocalReplace))
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
			return nil, errx.WrapDetail(err, ErrTemplateRead, "path=%s", f.path)
		}
		if !bytes.Equal(old, f.content) {
			conflicts = append(conflicts, f.path)
		}
	}
	return conflicts, nil
}

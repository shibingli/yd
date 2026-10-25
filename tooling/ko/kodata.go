// This file stages the baseline configuration into the kodata directory ko bundles into an image. | 本文件把基线配置
// 暂存到 ko 会打包进镜像的 kodata 目录。

package ko

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shibingli/ydx/errs"
	ydlog "github.com/shibingli/ydx/log"
)

// The layout of the staged configuration. | 暂存配置的目录布局。
const (
	// DefaultConfigDir is the configuration directory `yd new` scaffolds and the application reads by default. |
	// DefaultConfigDir 是 `yd new` 生成、应用默认读取的配置目录。
	DefaultConfigDir = "configs"
	// configFileName is the baseline file packaged into the image; config.local.yaml may hold secrets and is
	// deliberately never copied. | configFileName 是打包进镜像的基线文件；config.local.yaml 可能含密钥，刻意不复制。
	configFileName = "config.yaml"
	// kodataDir is the directory name ko treats as build-time image data; its contents land at KO_DATA_PATH. |
	// kodataDir 是 ko 视为构建期镜像数据的目录名；其内容会出现在 KO_DATA_PATH 下。
	kodataDir = "kodata"
)

// stagedKodata records every file and directory created for one build, so cleanup removes exactly those and
// never a kodata tree the project already owned. | stagedKodata 记录一次构建创建的文件与目录，使清理只删除这些条目，
// 绝不删除项目原本就有的 kodata 树。
type stagedKodata struct {
	// files lists the staged files in creation order. | files 按创建顺序列出暂存文件。
	files []string
	// dirs lists the created directories from outermost to innermost. | dirs 从最外层到最内层列出创建的目录。
	dirs []string
}

// stageKodataConfig copies configs/config.yaml into the kodata directory of every local package being built. |
// stageKodataConfig 把 configs/config.yaml 复制进每个待构建本地包的 kodata 目录。
//
// ko has no flag for adding files: it compiles the package and assembles the image from the binary plus the
// base image, so the documented route for project data is a kodata directory next to the package. The copy is
// temporary because it belongs to one build, not to the source tree; an existing kodata configuration always
// wins, and a missing baseline file is not an error because a project may be purely environment-driven. |
// ko 没有添加文件的标志：它编译包，并用二进制加基础镜像组装镜像，因此官方给出的项目数据通道就是包旁边的
// kodata 目录。副本是临时的，它属于一次构建而不是源码树；已有的 kodata 配置始终优先，缺少基线文件也不算错误，
// 因为项目可能完全由环境变量驱动。
func stageKodataConfig(o Options, log *ydlog.Logger) (*stagedKodata, error) {
	if o.SkipConfig {
		log.Debug("ko: configuration staging disabled | ko：已关闭配置文件暂存")
		return nil, nil
	}

	source := filepath.Join(o.WorkingDir, DefaultConfigDir, configFileName)
	data, err := os.ReadFile(source)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debug("ko: no baseline configuration to stage | ko：没有可暂存的基线配置", ydlog.Str("path", source))
			return nil, nil
		}
		return nil, errs.WrapDetail(err, ErrConfigStage, "read %s | 读取 %s", source, source)
	}

	staged := &stagedKodata{}
	ok := false
	defer func() {
		// A failure halfway through must not leave a partial kodata tree behind. | 中途失败不得留下残缺的 kodata 树。
		if !ok {
			staged.cleanup(log)
		}
	}()

	seen := make(map[string]struct{}, len(o.packages()))
	for _, pkg := range o.packages() {
		pkgDir := localPackageDir(o.WorkingDir, pkg)
		if pkgDir == "" {
			log.Debug("ko: package is not a local directory, configuration staging skipped | ko：包不是本地目录，跳过配置暂存",
				ydlog.Str("package", pkg))
			continue
		}
		if _, dup := seen[pkgDir]; dup {
			continue
		}
		seen[pkgDir] = struct{}{}

		target := filepath.Join(pkgDir, kodataDir, DefaultConfigDir, configFileName)
		if _, statErr := os.Stat(target); statErr == nil {
			log.Debug("ko: existing kodata configuration kept | ko：保留已有 kodata 配置", ydlog.Str("path", target))
			continue
		} else if !os.IsNotExist(statErr) {
			return nil, errs.WrapDetail(statErr, ErrConfigStage, "stat %s | 检查 %s", target, target)
		}

		dirs, dirErr := createKodataDirs(filepath.Dir(target))
		if dirErr != nil {
			return nil, errs.WrapDetail(dirErr, ErrConfigStage, "create %s | 创建 %s", filepath.Dir(target), filepath.Dir(target))
		}
		staged.dirs = append(staged.dirs, dirs...)

		if writeErr := os.WriteFile(target, data, 0o644); writeErr != nil {
			return nil, errs.WrapDetail(writeErr, ErrConfigStage, "write %s | 写入 %s", target, target)
		}
		staged.files = append(staged.files, target)
		log.Debug("ko: configuration staged | ko：配置文件已暂存", ydlog.Str("path", target), ydlog.Str("package", pkg))
	}

	if len(staged.files) == 0 {
		return nil, nil
	}
	ok = true
	return staged, nil
}

// localPackageDir resolves a package argument to a local directory, or returns "" when the argument is an
// import path or pattern that cannot be mapped without invoking the Go tool. | localPackageDir 把包参数解析为
// 本地目录；参数是导入路径或通配模式、不调用 Go 工具就无法映射时返回空串。
func localPackageDir(workDir, pkg string) string {
	if strings.TrimSpace(pkg) == "" {
		pkg = DefaultPackage
	}
	if strings.Contains(pkg, "...") {
		return ""
	}
	candidate := pkg
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(workDir, filepath.FromSlash(candidate))
	}
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return filepath.Clean(candidate)
	}
	return ""
}

// createKodataDirs creates dir and its missing ancestors, returning the directories it created from outermost
// to innermost so cleanup can remove them in reverse. | createKodataDirs 创建 dir 及其缺失的祖先目录，并从最外层
// 到最内层返回创建的目录，便于清理时逆序删除。
func createKodataDirs(dir string) ([]string, error) {
	var missing []string
	for current := dir; ; {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, errs.Detail(ErrConfigStage, "%s is not a directory | %s 不是目录", current, current)
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing, nil
}

// cleanup removes the staged files and then the directories created for them, ignoring entries a concurrent
// process already removed and reporting every other failure without masking the build's outcome. | cleanup 先删除
// 暂存文件，再删除为它们创建的目录；并发进程已删除的条目会被忽略，其余失败只报告而不覆盖构建结果。
func (s *stagedKodata) cleanup(log *ydlog.Logger) {
	if s == nil {
		return
	}
	for _, file := range s.files {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			log.Warn("ko: staged configuration could not be removed | ko：暂存的配置文件无法移除",
				ydlog.Err(err), ydlog.Str("path", file))
		}
	}
	for i := len(s.dirs) - 1; i >= 0; i-- {
		if err := os.Remove(s.dirs[i]); err != nil && !os.IsNotExist(err) {
			log.Warn("ko: staged configuration directory could not be removed | ko：暂存的配置目录无法移除",
				ydlog.Err(err), ydlog.Str("path", s.dirs[i]))
		}
	}
	s.files, s.dirs = nil, nil
}

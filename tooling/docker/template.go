package docker

import (
	"bytes"
	"embed"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/afero"

	"github.com/shibingli/ydx/afs"
	"github.com/shibingli/ydx/errs"
)

// Template sentinel errors. | 模板相关哨兵错误。
var (
	// ErrReadDockerfile indicates that neither the project file nor the builtin template could be read. | ErrReadDockerfile 表示项目文件与内置模板都无法读取。
	ErrReadDockerfile = errs.New("docker: failed to read Dockerfile | docker：读取 Dockerfile 失败")
	// ErrMaterializeDockerfile indicates that the builtin template could not be written to a temporary file for the docker CLI. | ErrMaterializeDockerfile 表示无法把内置模板落盘为临时文件供 docker CLI 读取。
	ErrMaterializeDockerfile = errs.New("docker: failed to materialize Dockerfile | docker：无法落盘 Dockerfile")
)

const (
	// DefaultDockerfile is the file name looked up in the project root. | DefaultDockerfile 是在项目根查找的文件名。
	DefaultDockerfile = "Dockerfile"
	// BuiltinDockerfilePath is the path of the embedded template inside its file system. | BuiltinDockerfilePath 是内置模板在其文件系统中的路径。
	BuiltinDockerfilePath = "templates/Dockerfile"
	// goMajorMinorPlaceholder is replaced with the project's Go major.minor so the builder
	// image tag is derived rather than hand-copied. | goMajorMinorPlaceholder 会被替换为项目的
	// Go 主次版本，使构建镜像标签是派生出来的而非手抄的。
	goMajorMinorPlaceholder = "{{GO_MAJOR_MINOR}}"
)

// templates holds the embedded Dockerfile template shipped with the framework. | templates 保存框架内置的 Dockerfile 模板。
//
//go:embed templates
var templates embed.FS

// BuiltinDockerfileFS returns the read-only file system holding the embedded
// template, already adapted to afero. | BuiltinDockerfileFS 返回承载内置模板的只读文件系统，并已适配 afero。
func BuiltinDockerfileFS() afero.Fs { return afs.NewEmbed(templates) }

// BuiltinDockerfile reads the embedded multi-stage scratch template. A nil file
// system means BuiltinDockerfileFS, which is what production code wants; tests may
// pass any afero file system. | BuiltinDockerfile 读取内置的多阶段 scratch 模板。文件系统为 nil
// 时表示使用 BuiltinDockerfileFS（生产代码即如此）；测试可以传入任意 afero 文件系统。
func BuiltinDockerfile(fs afero.Fs) ([]byte, error) {
	return builtinDockerfileFor(fs, runtimeGoMajorMinor())
}

// builtinDockerfileFor reads the embedded template and substitutes the builder image's Go
// major.minor. | builtinDockerfileFor 读取内嵌模板并替换构建镜像的 Go 主次版本。
func builtinDockerfileFor(fs afero.Fs, version string) ([]byte, error) {
	if fs == nil {
		fs = BuiltinDockerfileFS()
	}
	data, err := afero.ReadFile(fs, BuiltinDockerfilePath)
	if err != nil {
		return nil, errs.WrapDetail(err, ErrReadDockerfile, "%s | %s", BuiltinDockerfilePath, BuiltinDockerfilePath)
	}
	return bytes.ReplaceAll(data, []byte(goMajorMinorPlaceholder), []byte(version)), nil
}

// runtimeGoMajorMinor returns the major.minor of the toolchain that built yd. | runtimeGoMajorMinor
// 返回构建 yd 所用工具链的主次版本。
func runtimeGoMajorMinor() string {
	return majorMinor(strings.TrimPrefix(runtime.Version(), "go"))
}

// majorMinor reduces a Go version to its major.minor pair. | majorMinor 把 Go 版本归约为主次版本。
func majorMinor(v string) string {
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) < 2 {
		return strings.TrimSpace(v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return strings.TrimSpace(v)
	}
	minor := parts[1]
	for i, r := range minor {
		if r < '0' || r > '9' {
			minor = minor[:i]
			break
		}
	}
	return strconv.Itoa(major) + "." + minor
}

// projectGoMajorMinor reads the go directive of dir/go.mod through fs; an absent or malformed
// file falls back to the toolchain version. | projectGoMajorMinor 经 fs 读取 dir/go.mod 的 go 指令；
// 文件缺失或畸形时回退到工具链版本。
func projectGoMajorMinor(fs afero.Fs, dir string) string {
	if dir == "" {
		dir = "."
	}
	data, err := afero.ReadFile(fs, filepath.Join(dir, "go.mod"))
	if err != nil {
		return runtimeGoMajorMinor()
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "go "); ok {
			if v := strings.TrimSpace(rest); v != "" {
				return majorMinor(v)
			}
		}
	}
	return runtimeGoMajorMinor()
}

// Dockerfile is the resolved build recipe handed to the docker CLI. | Dockerfile 是交给 docker CLI 的已解析构建配方。
type Dockerfile struct {
	// Path is a real file system path that `docker build -f` can open. | Path 是 `docker build -f` 能打开的真实文件系统路径。
	Path string
	// Content is the resolved recipe. | Content 是解析后的配方内容。
	Content []byte
	// FromProject reports whether a project file shadowed the builtin template. | FromProject 报告项目文件是否覆盖了内置模板。
	FromProject bool
	// Temporary reports whether Path is a temporary file owned by the caller. | Temporary 报告 Path 是否为调用方拥有的临时文件。
	Temporary bool
}

// Cleanup removes the temporary file backing the recipe, if there is one. | Cleanup 删除支撑该配方的临时文件（若存在）。
//
// It is safe to call on a nil receiver and to call more than once. | 允许在 nil 接收者上调用，也允许重复调用。
func (d *Dockerfile) Cleanup() {
	if d == nil || !d.Temporary || d.Path == "" {
		return
	}
	_ = os.Remove(d.Path)
}

// ResolveDockerfile resolves the recipe of one image build. A Dockerfile inside the
// project (dir/file, with file defaulting to Dockerfile) always shadows the builtin
// template; content that docker cannot read through a plain path is materialized to
// a temporary file, because the docker CLI receives the recipe as a path. | ResolveDockerfile
// 解析一次镜像构建的配方。项目内的 Dockerfile（dir/file，file 默认 Dockerfile）始终覆盖内置模板；
// 无法以普通路径被 docker 读取的内容会落盘为临时文件，因为 docker CLI 是以路径接收配方的。
func ResolveDockerfile(fs afero.Fs, dir, file string) (*Dockerfile, error) {
	if fs == nil {
		fs = afs.NewOS("")
	}
	if file == "" {
		file = DefaultDockerfile
	}
	project := file
	if !filepath.IsAbs(project) {
		// A drive-relative ("C:Dockerfile") or rooted ("/Dockerfile") Windows value is not
		// filepath.IsAbs but must not be joined as a child of dir: the first would contain a colon
		// in a path component, the second is relative to the current drive. | Windows 的盘符相对
		// （"C:Dockerfile"）或根相对（"/Dockerfile"）取值不是 filepath.IsAbs，但绝不能拼成 dir 的子路径：
		// 前者会让路径分量含冒号，后者相对于当前驱动器。
		rooted := runtime.GOOS == "windows" && (strings.HasPrefix(project, "/") || strings.HasPrefix(project, `\`))
		if filepath.VolumeName(project) != "" || rooted {
			if abs, err := filepath.Abs(project); err == nil {
				project = abs
			}
		} else {
			if dir == "" {
				dir = "."
			}
			project = filepath.Join(dir, project)
		}
	}

	data, err := afero.ReadFile(fs, project)
	switch {
	case err == nil:
		return materialize(project, data, true)
	case errors.Is(err, os.ErrNotExist):
		// No project Dockerfile: fall through to the builtin template. | 项目内没有 Dockerfile：回退到内置模板。
	default:
		return nil, errs.WrapDetail(err, ErrReadDockerfile, "%s | %s", project, project)
	}

	// Use the project's own go directive when it exists; the embedded template alone cannot know
	// which toolchain the project requires. | 项目存在 go 指令时使用项目自己的版本；仅凭内嵌模板
	// 无法知道项目要求哪个工具链。
	builtin, err := builtinDockerfileFor(nil, projectGoMajorMinor(fs, dir))
	if err != nil {
		return nil, err
	}
	return materializeTemp(builtin, false)
}

// materialize keeps the project path when the file is readable from disk with
// exactly the resolved content, and copies it to a temporary file otherwise (an
// in-memory file system, for instance). | materialize 在文件能从磁盘按解析内容原样读取时沿用项目路径，
// 否则（例如内存文件系统）复制到临时文件。
func materialize(project string, data []byte, fromProject bool) (*Dockerfile, error) {
	if onDisk, err := os.ReadFile(project); err == nil && bytes.Equal(onDisk, data) {
		return &Dockerfile{Path: project, Content: data, FromProject: fromProject}, nil
	}
	return materializeTemp(data, fromProject)
}

// materializeTemp writes the recipe to a temporary file that the docker CLI can
// open by path. | materializeTemp 把配方写入临时文件，供 docker CLI 按路径打开。
func materializeTemp(data []byte, fromProject bool) (*Dockerfile, error) {
	f, err := os.CreateTemp("", "yd-dockerfile-*")
	if err != nil {
		return nil, errs.WrapDetail(err, ErrMaterializeDockerfile, "cannot create temp file | 无法创建临时文件")
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return nil, errs.WrapDetail(err, ErrMaterializeDockerfile, "%s | %s", name, name)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return nil, errs.WrapDetail(err, ErrMaterializeDockerfile, "%s | %s", name, name)
	}
	return &Dockerfile{Path: name, Content: data, FromProject: fromProject, Temporary: true}, nil
}

package afs

import (
	"os"
	"strings"

	"github.com/spf13/afero"
)

// HideShadowedMin hides a base-layer ".min.js"/".min.css" entry when the upper
// layer provides the corresponding source but not its minified sibling. | HideShadowedMin
// 在上层提供对应源码但没有其压缩同族文件时，隐藏下层的 ".min.js"/".min.css" 条目。
//
// The merged filesystem returned by Overlay falls through to the base for every path the
// upper does not contain. Without this guard a framework-provided yd-app.min.js would be
// served for a project's own yd-app.js, and a framework yd.min.css would shadow a project's
// yd.css. The upper layer remains authoritative whenever it ships its own minified sibling. |
// Overlay 返回的合并文件系统会在上层缺失的每个路径上回落到下层。没有这层保护时，框架提供的
// yd-app.min.js 会顶替项目自己的 yd-app.js，框架的 yd.min.css 也会遮蔽项目的 yd.css；
// 上层自带压缩同族文件时，上层仍然优先。
func HideShadowedMin(merged, upper FS) FS {
	if merged == nil || upper == nil {
		return merged
	}
	return &shadowedMinFS{FS: merged, upper: upper}
}

// shadowedMinFS delegates to the merged filesystem and hides the shadowed
// minified entries on the read paths. | shadowedMinFS 把读取委托给合并文件系统，
// 并在读取路径上隐藏被遮蔽的压缩条目。
type shadowedMinFS struct {
	FS
	upper FS
}

// hidden reports whether name is a base-layer .min entry shadowed by an upper
// source. | hidden 报告 name 是否是被上层源码遮蔽的下层 .min 条目。
func (f *shadowedMinFS) hidden(name string) bool {
	p := strings.ReplaceAll(name, "\\", "/")
	source := ""
	switch {
	case strings.HasSuffix(p, ".min.js"):
		source = strings.TrimSuffix(p, ".min.js") + ".js"
	case strings.HasSuffix(p, ".min.css"):
		source = strings.TrimSuffix(p, ".min.css") + ".css"
	default:
		return false
	}
	return Exists(f.upper, source) && !Exists(f.upper, p)
}

// notExist returns the error a read of a hidden path reports. | notExist 返回读取被隐藏路径时的错误。
func notExist(op, name string) error {
	return &os.PathError{Op: op, Path: name, Err: os.ErrNotExist}
}

// Open opens name unless the entry is shadowed. | Open 打开 name，被遮蔽时返回不存在。
func (f *shadowedMinFS) Open(name string) (afero.File, error) {
	if f.hidden(name) {
		return nil, notExist("open", name)
	}
	return f.FS.Open(name)
}

// OpenFile opens name unless the entry is shadowed. | OpenFile 打开 name，被遮蔽时返回不存在。
func (f *shadowedMinFS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if f.hidden(name) {
		return nil, notExist("openfile", name)
	}
	return f.FS.OpenFile(name, flag, perm)
}

// Stat stats name unless the entry is shadowed. | Stat 读取 name 的状态，被遮蔽时返回不存在。
func (f *shadowedMinFS) Stat(name string) (os.FileInfo, error) {
	if f.hidden(name) {
		return nil, notExist("stat", name)
	}
	return f.FS.Stat(name)
}

// LstatIfPossible preserves the underlying Lstater capability when present and
// applies the same hiding rule. | LstatIfPossible 在底层支持时保留 Lstater 能力，
// 并应用同样的隐藏规则。
func (f *shadowedMinFS) LstatIfPossible(name string) (os.FileInfo, bool, error) {
	if f.hidden(name) {
		return nil, true, notExist("lstat", name)
	}
	if l, ok := f.FS.(afero.Lstater); ok {
		return l.LstatIfPossible(name)
	}
	info, err := f.Stat(name)
	return info, false, err
}

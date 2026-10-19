// This file implements the confined operating-system filesystem returned by NewOS. | 本文件实现 NewOS
// 返回的、受根目录约束的操作系统文件系统。

package afs

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/afero"
)

// confinedOS confines every operation to one absolute root. | confinedOS 把每个操作都约束在一个绝对根之下。
//
// It exists because afero.BasePathFs checks containment with a plain string prefix: joining a
// sibling such as "../app2/secret" onto "/srv/app" yields "/srv/app2/secret", which still starts with
// "/srv/app" and is therefore accepted. This wrapper rejects lexical escapes before BasePathFs ever
// sees the name, so a caller cannot reach a sibling whose path happens to share the prefix. Symlinks
// inside the root are still followed, exactly like the operating system itself does. | 它存在的原因是
// afero.BasePathFs 只用字符串前缀判断包含关系：把 "../app2/secret" 拼到 "/srv/app" 上得到
// "/srv/app2/secret"，它仍以 "/srv/app" 开头，因此被接受。本包装在 BasePathFs 看到名字之前就拒绝
// 词法逃逸，调用方无法到达恰好共享前缀的兄弟目录。根内的符号链接仍会被跟随，与操作系统自身的行为一致。
type confinedOS struct {
	// Fs is the BasePathFs the validated relative names are handed to. | Fs 接收已校验相对名的 BasePathFs。
	afero.Fs
	// root is the absolute, cleaned confinement root. | root 是绝对且已清理的约束根。
	root string
}

// newConfinedOS builds a confined filesystem rooted at root. | newConfinedOS 构造以 root 为根的受限文件系统。
func newConfinedOS(root string) afero.Fs {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = filepath.Clean(root)
	}
	return &confinedOS{Fs: afero.NewBasePathFs(afero.NewOsFs(), abs), root: abs}
}

// local validates name and returns the relative name BasePathFs resolves under the root. | local 校验 name，
// 并返回 BasePathFs 在根下解析所用的相对名。
//
// Absolute names are accepted only when they already point inside the root; they are converted to a
// relative name so Windows sees no colon-bearing path component. | 绝对名只有在确实指向根内时才被接受；
// 它们会被转换为相对名，避免 Windows 收到带冒号的路径分量。
func (c *confinedOS) local(name string) (string, error) {
	if name == "" {
		name = "."
	}
	if filepath.IsAbs(name) {
		rel, err := filepath.Rel(c.root, filepath.Clean(name))
		if err != nil || escapesRoot(rel) {
			return "", os.ErrNotExist
		}
		return rel, nil
	}
	// A drive-relative name ("C:foo") has a volume but is not absolute; joining it under the root
	// would produce an invalid component, so it is refused rather than misinterpreted. | 盘符相对名
	// （"C:foo"）带卷名但不是绝对路径；把它拼到根下会产生非法分量，因此直接拒绝而不是曲解。
	if filepath.VolumeName(name) != "" {
		return "", os.ErrNotExist
	}
	clean := filepath.Clean(name)
	if escapesRoot(clean) {
		return "", os.ErrNotExist
	}
	return clean, nil
}

// escapesRoot reports whether rel climbs out of the root. | escapesRoot 报告 rel 是否爬出根目录。
func escapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Create implements afero.Fs. | Create 实现 afero.Fs。
func (c *confinedOS) Create(name string) (afero.File, error) {
	n, err := c.local(name)
	if err != nil {
		return nil, err
	}
	return c.Fs.Create(n)
}

// Mkdir implements afero.Fs. | Mkdir 实现 afero.Fs。
func (c *confinedOS) Mkdir(name string, perm os.FileMode) error {
	n, err := c.local(name)
	if err != nil {
		return err
	}
	return c.Fs.Mkdir(n, perm)
}

// MkdirAll implements afero.Fs. | MkdirAll 实现 afero.Fs。
func (c *confinedOS) MkdirAll(path string, perm os.FileMode) error {
	n, err := c.local(path)
	if err != nil {
		return err
	}
	return c.Fs.MkdirAll(n, perm)
}

// Open implements afero.Fs. | Open 实现 afero.Fs。
func (c *confinedOS) Open(name string) (afero.File, error) {
	n, err := c.local(name)
	if err != nil {
		return nil, err
	}
	return c.Fs.Open(n)
}

// OpenFile implements afero.Fs. | OpenFile 实现 afero.Fs。
func (c *confinedOS) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	n, err := c.local(name)
	if err != nil {
		return nil, err
	}
	return c.Fs.OpenFile(n, flag, perm)
}

// Remove implements afero.Fs. | Remove 实现 afero.Fs。
func (c *confinedOS) Remove(name string) error {
	n, err := c.local(name)
	if err != nil {
		return err
	}
	return c.Fs.Remove(n)
}

// RemoveAll implements afero.Fs. | RemoveAll 实现 afero.Fs。
func (c *confinedOS) RemoveAll(path string) error {
	n, err := c.local(path)
	if err != nil {
		return err
	}
	return c.Fs.RemoveAll(n)
}

// Rename implements afero.Fs. | Rename 实现 afero.Fs。
func (c *confinedOS) Rename(oldname, newname string) error {
	oldName, err := c.local(oldname)
	if err != nil {
		return err
	}
	newName, err := c.local(newname)
	if err != nil {
		return err
	}
	return c.Fs.Rename(oldName, newName)
}

// Stat implements afero.Fs. | Stat 实现 afero.Fs。
func (c *confinedOS) Stat(name string) (os.FileInfo, error) {
	n, err := c.local(name)
	if err != nil {
		return nil, err
	}
	return c.Fs.Stat(n)
}

// Chmod implements afero.Fs. | Chmod 实现 afero.Fs。
func (c *confinedOS) Chmod(name string, mode os.FileMode) error {
	n, err := c.local(name)
	if err != nil {
		return err
	}
	return c.Fs.Chmod(n, mode)
}

// Chown implements afero.Fs. | Chown 实现 afero.Fs。
func (c *confinedOS) Chown(name string, uid, gid int) error {
	n, err := c.local(name)
	if err != nil {
		return err
	}
	return c.Fs.Chown(n, uid, gid)
}

// Chtimes implements afero.Fs. | Chtimes 实现 afero.Fs。
func (c *confinedOS) Chtimes(name string, atime, mtime time.Time) error {
	n, err := c.local(name)
	if err != nil {
		return err
	}
	return c.Fs.Chtimes(n, atime, mtime)
}

// LstatIfPossible implements afero.Lstater without following a final symbolic link, and validates
// the name like every other operation. | LstatIfPossible 实现 afero.Lstater，不跟随最后一段符号链接，
// 并像其它操作一样校验路径。
func (c *confinedOS) LstatIfPossible(name string) (os.FileInfo, bool, error) {
	n, err := c.local(name)
	if err != nil {
		return nil, false, err
	}
	if lfs, ok := c.Fs.(afero.Lstater); ok {
		return lfs.LstatIfPossible(n)
	}
	info, err := c.Fs.Stat(n)
	return info, false, err
}

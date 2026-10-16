// Package afs（abstraction filesystem）在 afero 之上提供统一文件系统门面：
// 同一套 API 可在操作系统目录（OsFs / BasePathFs）、内存（MemMapFs）与
// 只读内嵌资产（io/fs，如 go:embed）之间无缝切换，便于测试与虚拟化。
//
// 约定：路径一律交给底层 afero.Fs 解释——操作系统后端使用 OS 分隔符，
// io/fs 语义的内嵌后端使用 "/" 且不接受前导斜杠。
package afs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/afero"
	"github.com/shibingli/yd/errx"
)

// File operation errors, declared once at package level. | 文件操作错误，在包级声明一次。
var (
	// ErrRelPath indicates the relative path from the source directory could not be computed. | ErrRelPath 表示无法计算源路径与源目录之间的相对路径。
	ErrRelPath = errx.New("afs: failed to compute relative path | afs：计算相对路径失败")
	// ErrMkdir indicates the target directory could not be created. | ErrMkdir 表示无法创建目标目录。
	ErrMkdir = errx.New("afs: failed to create directory | afs：创建目录失败")
	// ErrReadFile indicates the file content could not be read. | ErrReadFile 表示无法读取文件内容。
	ErrReadFile = errx.New("afs: failed to read file | afs：读取文件失败")
	// ErrWriteFile indicates the file content could not be written. | ErrWriteFile 表示无法写入文件内容。
	ErrWriteFile = errx.New("afs: failed to write file | afs：写入文件失败")
	// ErrOpenFile indicates the file could not be opened. | ErrOpenFile 表示无法打开文件。
	ErrOpenFile = errx.New("afs: failed to open file | afs：打开文件失败")
	// ErrHashFile indicates the file hash could not be computed while streaming. | ErrHashFile 表示无法流式计算文件哈希。
	ErrHashFile = errx.New("afs: failed to hash file | afs：计算文件哈希失败")
)

// FS 是 afero.Fs 的类型别名：afero 生态中的任意实现（OsFs、MemMapFs、
// BasePathFs、CopyOnWriteFs 等）都可直接传入本包 API，无需适配层。
type FS = afero.Fs

// NewOS 返回以 root 为根的操作系统文件系统。
// root 为空时等价 afero.NewOsFs（相对当前工作目录）；root 非空时返回
// afero.NewBasePathFs，后续所有路径都被约束在该目录之下。
func NewOS(root string) FS {
	if root == "" {
		return afero.NewOsFs()
	}
	return afero.NewBasePathFs(afero.NewOsFs(), root)
}

// NewMem 返回内存文件系统（afero.NewMemMapFs），用于测试与虚拟化场景；
// 每次调用返回相互独立的实例。
func NewMem() FS {
	return afero.NewMemMapFs()
}

// NewEmbed 把 io/fs 语义的只读文件系统（典型来源是 go:embed 的 embed.FS）
// 适配为 FS。返回的 FS 只读：所有写操作返回包装了 fs.ErrPermission 的
// *fs.PathError；路径必须满足 fs.ValidPath（"/" 分隔、无前导斜杠）。
func NewEmbed(f fs.FS) FS {
	return afero.FromIOFS{FS: f}
}

// CopyDir 把 src 上以 srcDir 为根的目录树递归复制到 dst 的 dstDir 之下，
// 保留相对结构：srcDir 自身不产生额外层级（其内容直接落在 dstDir 内），
// 缺失的父目录与空目录都会被创建。目录权限统一为 0o755，文件权限统一为
// 0o644；源端不做任何改动。
func CopyDir(src, dst FS, srcDir, dstDir string) error {
	return Walk(src, srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return errx.WrapDetail(err, ErrRelPath, "%s relative to %s", path, srcDir)
		}
		target := filepath.Join(dstDir, rel)

		if info.IsDir() {
			if err := dst.MkdirAll(target, 0o755); err != nil {
				return errx.WrapDetail(err, ErrMkdir, "%s", target)
			}
			return nil
		}

		data, err := afero.ReadFile(src, path)
		if err != nil {
			return errx.WrapDetail(err, ErrReadFile, "%s", path)
		}
		if err := dst.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return errx.WrapDetail(err, ErrMkdir, "%s", target)
		}
		if err := afero.WriteFile(dst, target, data, 0o644); err != nil {
			return errx.WrapDetail(err, ErrWriteFile, "%s", target)
		}
		return nil
	})
}

// WriteFileIfChanged 仅在内容变化时写入 path，返回是否真正发生了写入。
// 目标已存在且内容与 data 逐字节相同时返回 (false, nil) 且不触碰文件
// （ModTime 保持不变）；否则以 0o644 权限写入（父目录缺失时自动创建）并
// 返回 (true, nil)。供 dev 热重载与前端资产更新复用，避免无谓写入触发
// 下游的文件监听与全量重载。
func WriteFileIfChanged(fs FS, path string, data []byte) (bool, error) {
	if Exists(fs, path) {
		old, err := afero.ReadFile(fs, path)
		if err != nil {
			return false, errx.WrapDetail(err, ErrReadFile, "%s", path)
		}
		if bytes.Equal(old, data) {
			return false, nil
		}
	}

	if dir := filepath.Dir(path); dir != "." {
		if err := fs.MkdirAll(dir, 0o755); err != nil {
			return false, errx.WrapDetail(err, ErrMkdir, "%s", dir)
		}
	}
	if err := afero.WriteFile(fs, path, data, 0o644); err != nil {
		return false, errx.WrapDetail(err, ErrWriteFile, "%s", path)
	}
	return true, nil
}

// Exists 报告 path 在 fs 上是否存在，目录同样返回 true；
// 任何 Stat 错误（不存在、路径非法、权限不足等）都视为不存在。
func Exists(fs FS, path string) bool {
	_, err := fs.Stat(path)
	return err == nil
}

// Sha256File 计算 fs 上 path 内容的 SHA-256，返回 64 位小写十六进制字符串；
// 内容流式读取，适用于大文件。
func Sha256File(fs FS, path string) (string, error) {
	f, err := fs.Open(path)
	if err != nil {
		return "", errx.WrapDetail(err, ErrOpenFile, "%s", path)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", errx.WrapDetail(err, ErrHashFile, "%s", path)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Walk 以 afero.Walk 的语义遍历 fs 上以 root 为根的目录树：
// fn 会先收到 root 自身，随后按目录名字典序深度优先访问每个条目；
// 遍历过程中产生的错误（如 root 不存在时 info 为 nil 的错误）经 err 参数
// 传给 fn，fn 返回非 nil 即终止遍历并把该错误返回给调用方；对目录返回
// filepath.SkipDir 可跳过整棵子树。
//
// 内嵌（io/fs 语义）后端由 fs.WalkDir 驱动，因此 path 始终以 "/" 分隔，
// 与 fs.ValidPath 一致；其余后端完全复用 afero.Walk。
func Walk(fs FS, root string, fn func(path string, info os.FileInfo, err error) error) error {
	if embedFS, ok := fs.(afero.FromIOFS); ok {
		return walkIOFS(embedFS.FS, root, fn)
	}
	return afero.Walk(fs, root, fn)
}

// walkIOFS 遍历 io/fs 语义的只读后端。
// afero.Walk 用 filepath.Join 拼接子路径，在 Windows 上会得到含反斜杠的
// 路径，而 fs.ValidPath 只接受 "/"，嵌套目录遍历会直接失败；这里改用
// fs.WalkDir 并以 "/" 拼接，回调契约（含 root、err 经 fn 传递、SkipDir
// 跳过子树）与 afero.Walk 保持一致。
func walkIOFS(fsys fs.FS, root string, fn func(path string, info os.FileInfo, err error) error) error {
	return fs.WalkDir(fsys, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fn(path, nil, err)
		}
		info, err := entry.Info()
		if err != nil {
			return fn(path, nil, err)
		}
		err = fn(path, info, nil)
		// filepath.SkipDir 与 fs.SkipDir 是两个不同的哨兵值，需按语义归一化。
		if info.IsDir() && errors.Is(err, filepath.SkipDir) {
			return fs.SkipDir
		}
		return err
	})
}

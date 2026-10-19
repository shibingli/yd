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
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shibingli/yd/core/errx"
	"github.com/spf13/afero"
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

// NewOS returns an operating-system filesystem rooted at root. | NewOS 返回以 root 为根的操作系统文件系统。
//
// An empty root is the plain operating-system filesystem, interpreted relative to the current
// directory. A non-empty root — including "." — is resolved to an absolute directory and every path
// is confined beneath it, with lexical ".." escapes rejected before the OS sees them. Symbolic links
// inside the root are still followed, exactly as the operating system follows them. | root 为空表示普通
// 操作系统文件系统，路径相对当前目录解释。非空 root（包括 "."）会解析为绝对目录，所有路径都被约束在其下，
// 词法 ".." 逃逸在到达操作系统之前即被拒绝。根内的符号链接仍会被跟随，与操作系统自身行为一致。
func NewOS(root string) FS {
	if root == "" {
		return afero.NewOsFs()
	}
	return newConfinedOS(root)
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

// Overlay returns a copy-on-write filesystem where upper shadows base. | Overlay 返回写时复制文件系统，upper 覆盖 base。
//
// Reads fall through to base when upper has no entry, and every write lands in upper, so a read-only
// embedded tree can sit underneath a writable disk tree. | 读取在 upper 缺失时回落到 base，所有写入都落在
// upper，因此只读的内嵌树可以垫在可写的磁盘树下面。
func Overlay(base, upper FS) FS {
	return afero.NewCopyOnWriteFs(base, upper)
}

// IOFSTree returns the io/fs view behind an embedded filesystem. | IOFSTree 返回内嵌文件系统背后的 io/fs 视图。
//
// The second result is false when fsys did not come from NewEmbed; callers then decide whether to
// degrade or fail. | 当 fsys 并非来自 NewEmbed 时第二返回值为 false，调用方据此决定降级还是失败。
func IOFSTree(fsys FS) (fs.FS, bool) {
	from, ok := fsys.(afero.FromIOFS)
	if !ok {
		return nil, false
	}
	return from.FS, true
}

// Sub returns the subtree rooted at dir as a read-only embedded filesystem. | Sub 返回以 dir 为根的只读内嵌文件系统。
//
// fs.Sub is used rather than afero's BasePathFs because the embedded backend requires "/"-separated
// fs.ValidPath names while BasePathFs joins with the OS separator. | 这里用 fs.Sub 而不是 afero 的
// BasePathFs：内嵌后端要求 "/" 分隔的 fs.ValidPath 名称，而 BasePathFs 会用操作系统分隔符拼接。
func Sub(parent fs.FS, dir string) (FS, error) {
	sub, err := fs.Sub(parent, dir)
	if err != nil {
		return nil, err
	}
	return NewEmbed(sub), nil
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
			return errx.WrapDetail(err, ErrRelPath, "%[1]s relative to %[2]s | 相对于 %[2]s 的 %[1]s", path, srcDir)
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

// WriteFileIfChanged writes path only when its content changed and reports whether it wrote.
// | WriteFileIfChanged 仅在内容变化时写入 path，并返回是否真正发生了写入。
//
// When the target exists and equals data byte for byte it returns (false, nil) without touching the
// file (ModTime is preserved). Otherwise the bytes go through a temporary file in the same directory
// and are renamed into place, so an interrupted write never leaves a truncated target and a symbolic
// link at the destination is replaced rather than followed; a new file gets 0o644 while an existing
// file keeps its permission bits. Missing parent directories are created. It backs dev hot reload and
// front-end asset updates, where a needless write would trigger file watchers and a full reload.
// | 目标已存在且与 data 逐字节相同时返回 (false, nil)，且不触碰文件（ModTime 保持不变）。否则先写
// 同目录临时文件再改名覆盖，因此中断的写入不会留下截断的目标，目标位置的符号链接会被替换而不是被跟随；
// 新文件权限为 0o644，既有文件保留原有权限位。父目录缺失时自动创建。它服务于 dev 热重载与前端资产更新，
// 避免无谓写入触发文件监听与全量重载。
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

	dir := filepath.Dir(path)
	if dir != "." {
		if err := fs.MkdirAll(dir, 0o755); err != nil {
			return false, errx.WrapDetail(err, ErrMkdir, "%s", dir)
		}
	}

	// Write through a temporary file in the same directory and rename it over the target:
	// a crash mid-write can then never leave a truncated asset or manifest, and a symbolic
	// link at the destination is replaced rather than followed. | 先写同目录临时文件再改名覆盖：
	// 写到一半崩溃也不会留下截断的资产或清单，目标位置的符号链接会被替换而不是被跟随。
	mode := os.FileMode(0o644)
	if info, statErr := fs.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	tmp, tmpName, err := createTempFile(fs, dir)
	if err != nil {
		return false, errx.WrapDetail(err, ErrWriteFile, "%s", path)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = fs.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return false, errx.WrapDetail(err, ErrWriteFile, "%s", path)
	}
	if err := tmp.Close(); err != nil {
		return false, errx.WrapDetail(err, ErrWriteFile, "%s", path)
	}
	if err := fs.Chmod(tmpName, mode); err != nil {
		return false, errx.WrapDetail(err, ErrWriteFile, "%s", path)
	}
	if err := fs.Rename(tmpName, path); err != nil {
		return false, errx.WrapDetail(err, ErrWriteFile, "%s", path)
	}
	cleanup = false
	return true, nil
}

// tempSeq disambiguates temporary names created within the same nanosecond. | tempSeq 区分同一纳秒内
// 创建的临时文件名。
var tempSeq atomic.Uint64

// createTempFile creates an exclusive temporary file inside dir. | createTempFile 在 dir 内创建一个
// 独占的临时文件。
//
// It builds the name itself instead of using afero.TempFile and then reading File.Name(): on Linux a
// wrapper such as BasePathFs returns a stripped name with a leading separator, which this package's
// confinement then rightly rejects as absolute. Keeping the name that was passed in avoids that
// mismatch and works on every backend. | 它自行构造文件名，而不是使用 afero.TempFile 再读取
// File.Name()：在 Linux 上，BasePathFs 这类包装器返回去掉根前缀却仍带前导分隔符的名字，本包的
// 根目录约束会正确地把该名字当作绝对路径拒绝。保留传入时使用的名字即可避开这一不一致，并在所有后端成立。
func createTempFile(fsys FS, dir string) (afero.File, string, error) {
	for i := 0; i < 100; i++ {
		name := filepath.Join(dir, ".yd-write-"+strconv.FormatInt(time.Now().UnixNano(), 36)+"-"+
			strconv.FormatUint(tempSeq.Add(1), 36))
		f, err := fsys.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", os.ErrExist
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

// Walk traverses the tree rooted at root with the semantics of afero.Walk. | Walk 以 afero.Walk 的语义
// 遍历 fs 上以 root 为根的目录树。
//
// fn receives root first, then every entry depth-first in lexical order. An error (a missing root,
// for example) reaches fn with a nil info, and a non-nil return aborts the walk; returning
// filepath.SkipDir for a directory skips its subtree. | fn 先收到 root，随后按目录名字典序深度优先访问
// 每个条目。遍历错误（例如 root 不存在）会带着 nil info 传给 fn，返回非 nil 即终止并把该错误返回给调用方；
// 对目录返回 filepath.SkipDir 可跳过整棵子树。
//
// Callback paths are always slash-separated, for io/fs backends and for OS/Mem/BasePath backends
// alike, so callers can compare them with fs.ValidPath-style names on every platform. The io/fs
// backend is driven by fs.WalkDir; the others use the same traversal with "/" joins instead of
// filepath.Join, which would inject a backslash on Windows. | 回调路径一律以 "/" 分隔——无论 io/fs
// 后端还是 OS/Mem/BasePath 后端——因此调用方在所有平台上都能用 fs.ValidPath 风格的名称比较。io/fs 后端
// 由 fs.WalkDir 驱动；其余后端使用同样的遍历，但以 "/" 拼接而不是 filepath.Join，后者在 Windows 上会注入反斜杠。
func Walk(fs FS, root string, fn func(path string, info os.FileInfo, err error) error) error {
	if embedFS, ok := fs.(afero.FromIOFS); ok {
		return walkIOFS(embedFS.FS, root, fn)
	}
	return walkSlash(fs, filepath.ToSlash(root), fn)
}

// walkSlash is afero.Walk with slash-separated child names. | walkSlash 是使用斜杠分隔子名的 afero.Walk。
//
// afero.Walk joins children with filepath.Join, so on Windows it hands backslash names to wrappers
// whose base is an io/fs backend (CopyOnWriteFs over an embedded tree, for instance); fs.ValidPath
// rejects those and entries that exist only in the embedded layer become invisible. A slash name is
// accepted by OsFs, MemMapFs, BasePathFs and io/fs alike, so one traversal covers every combination.
// | afero.Walk 用 filepath.Join 拼接子名，因此在 Windows 上会把反斜杠名交给「底层是 io/fs 后端」的包装
// （例如叠在内嵌树上的 CopyOnWriteFs）；fs.ValidPath 拒绝这种名字，只存在于内嵌层的条目会看不见。
// 斜杠名可被 OsFs、MemMapFs、BasePathFs 与 io/fs 共同接受，因此一次遍历覆盖所有组合。
func walkSlash(fsys FS, root string, fn func(path string, info os.FileInfo, err error) error) error {
	info, err := lstatSlash(fsys, root)
	if err != nil {
		return fn(root, nil, err)
	}
	return walkSlashDir(fsys, root, info, fn)
}

// walkSlashDir is the recursive half of walkSlash. | walkSlashDir 是 walkSlash 的递归部分。
func walkSlashDir(fsys FS, p string, info os.FileInfo, fn func(string, os.FileInfo, error) error) error {
	if err := fn(p, info, nil); err != nil {
		if info.IsDir() && errors.Is(err, filepath.SkipDir) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	names, err := readDirNamesSlash(fsys, p)
	if err != nil {
		return fn(p, info, err)
	}
	for _, name := range names {
		child := joinSlash(p, name)
		childInfo, err := lstatSlash(fsys, child)
		if err != nil {
			if fnErr := fn(child, childInfo, err); fnErr != nil && !errors.Is(fnErr, filepath.SkipDir) {
				return fnErr
			}
			continue
		}
		if err := walkSlashDir(fsys, child, childInfo, fn); err != nil {
			if !childInfo.IsDir() || !errors.Is(err, filepath.SkipDir) {
				return err
			}
		}
	}
	return nil
}

// joinSlash joins a directory and a child name without collapsing a leading "//" (UNC) or "C:/". |
// joinSlash 拼接目录与子名，且不会折叠开头的 "//"（UNC）或 "C:/"。
func joinSlash(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// lstatSlash prefers Lstat so symbolic links are not followed, exactly like afero.Walk. | lstatSlash
// 优先使用 Lstat 以免跟随符号链接，与 afero.Walk 一致。
func lstatSlash(fsys FS, p string) (os.FileInfo, error) {
	if lfs, ok := fsys.(afero.Lstater); ok {
		info, _, err := lfs.LstatIfPossible(p)
		return info, err
	}
	return fsys.Stat(p)
}

// readDirNamesSlash returns the sorted entries of dir. | readDirNamesSlash 返回 dir 排好序的条目名。
func readDirNamesSlash(fsys FS, dir string) ([]string, error) {
	f, err := fsys.Open(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
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
		// filepath.SkipDir and fs.SkipDir are two distinct sentinels and must be normalized by meaning. | filepath.SkipDir 与 fs.SkipDir 是两个不同的哨兵值，需按语义归一化。
		if info.IsDir() && errors.Is(err, filepath.SkipDir) {
			return fs.SkipDir
		}
		return err
	})
}

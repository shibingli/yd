// External tests for package afs: the facade is exercised only through its exported API, never through implementation details. | afs 包的外部测试：仅通过导出 API 验证门面行为，不依赖内部实现细节。
package afs_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/spf13/afero"

	"github.com/shibingli/yd/core/afs"
)

// testTree 是各后端复用的小目录树：相对路径 -> 内容。
// 同时覆盖嵌套目录、二进制内容（含 0x00）与空目录，用于遍历与复制用例。
var testTree = map[string][]byte{
	"a.txt":          []byte("alpha"),
	"sub/b.txt":      []byte("beta"),
	"sub/deep/c.bin": {0x00, 0x01, 0xfe, 0xff},
}

// backend describes one FS backend reusable by every table-driven case. | backend 描述一个可被所有表驱动用例复用的 FS 后端。
type backend struct {
	name string
	root string // The legal root spelling of this backend: MemMapFs uses "/srv", io/fs semantics use "srv". | 该后端合法的根路径写法：MemMapFs 用 "/srv"，io/fs 语义用 "srv"
	new  func(t *testing.T) afs.FS
}

// path joins a relative path under root in the backend's separator style. | path 按后端的分隔符风格拼接 root 下的相对路径。
func (b backend) path(rel string) string {
	return strings.TrimSuffix(b.root, "/") + "/" + rel
}

// backends 返回跨后端测试矩阵：内存 FS 与 embed FS。
// 两者的 root 写法不同（"/srv" 与 "srv"），以验证门面不泄漏任一后端的路径假设。
func backends(t *testing.T) []backend {
	t.Helper()
	return []backend{
		{
			name: "MemMapFs",
			root: "/srv",
			new: func(t *testing.T) afs.FS {
				t.Helper()
				fsys := afs.NewMem()
				for rel, data := range testTree {
					writeFile(t, fsys, "/srv/"+rel, data)
				}
				if err := fsys.MkdirAll("/srv/empty", 0o755); err != nil {
					t.Fatalf("准备空目录失败: %v", err)
				}
				return fsys
			},
		},
		{
			name: "EmbedFstestMapFS",
			root: "srv",
			new: func(t *testing.T) afs.FS {
				t.Helper()
				mapFS := fstest.MapFS{
					"srv/empty": &fstest.MapFile{Mode: fs.ModeDir | 0o755},
				}
				for rel, data := range testTree {
					mapFS["srv/"+rel] = &fstest.MapFile{Data: data}
				}
				return afs.NewEmbed(mapFS)
			},
		},
	}
}

// writeFile creates path on fsys (missing parent directories are filled in) and writes data to it. | writeFile 在 fsys 上创建 path（缺失的父目录自动补齐）并写入 data。
func writeFile(t *testing.T, fsys afs.FS, path string, data []byte) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "." {
		if err := fsys.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("准备目录 %s 失败: %v", dir, err)
		}
	}
	if err := afero.WriteFile(fsys, path, data, 0o644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// readFile reads the whole content at path on fsys; a failure ends the current test. | readFile 读取 fsys 上 path 的全部内容，失败即终止当前测试。
func readFile(t *testing.T, fsys afs.FS, path string) []byte {
	t.Helper()
	data, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return data
}

// TestFSAliasIsAferoFs 断言 FS 是 afero.Fs 的类型别名而非新建定义类型：
// 两个方向的赋值都必须能通过编译，否则本测试无法构建。
func TestFSAliasIsAferoFs(t *testing.T) {
	var fromYd afero.Fs = afs.NewMem() // FS -> afero.Fs
	writeFile(t, fromYd, "/a.txt", []byte("1"))

	var fromAfero afs.FS = afero.NewMemMapFs() // afero.Fs -> FS
	writeFile(t, fromAfero, "/b.txt", []byte("2"))

	if got := readFile(t, fromAfero, "/b.txt"); string(got) != "2" {
		t.Fatalf("FS 应可直接承载 afero.Fs，读到 %q", got)
	}
}

// TestMemFSRoundTrip 验证 NewMem 返回内存 FS：写入后可逐字节读回，
// 且不同实例之间互不共享数据。
func TestMemFSRoundTrip(t *testing.T) {
	fsys := afs.NewMem()
	if _, ok := fsys.(*afero.MemMapFs); !ok {
		t.Fatalf("NewMem() 应返回 *afero.MemMapFs，实际为 %T", fsys)
	}

	const content = "server:\n  port: 8080\n"
	writeFile(t, fsys, "/conf/app.yaml", []byte(content))

	if got := string(readFile(t, fsys, "/conf/app.yaml")); got != content {
		t.Fatalf("MemFS 往返内容 = %q, want %q", got, content)
	}
	if !afs.Exists(fsys, "/conf") {
		t.Fatal("the /conf directory should exist in MemFS | MemFS 中 /conf 目录应存在")
	}

	other := afs.NewMem()
	if afs.Exists(other, "/conf/app.yaml") {
		t.Fatal("distinct NewMem instances must not share data | NewMem 的不同实例不应共享数据")
	}
}

// TestNewOS 表驱动验证 NewOS 的 root 语义：
// root 为空等价 afero.NewOsFs，root 非空则以该目录为根并真实落盘。
func TestNewOS(t *testing.T) {
	osDir := t.TempDir()

	cases := []struct {
		name     string
		root     string
		fsPath   string // Path handed to the FS. | 传给 FS 的路径
		diskPath string // Real path read directly through the operating system. | 用操作系统直接读取的真实路径
		content  string
		wantOS   bool
	}{
		{
			name:     "root 为空时等价 afero.NewOsFs",
			root:     "",
			fsPath:   filepath.Join(osDir, "osfs-plain.txt"),
			diskPath: filepath.Join(osDir, "osfs-plain.txt"),
			content:  "plain-os",
			wantOS:   true,
		},
		{
			name:     "root 非空时以该目录为根",
			root:     osDir,
			fsPath:   filepath.Join("nested", "deep", "based.txt"),
			diskPath: filepath.Join(osDir, "nested", "deep", "based.txt"),
			content:  "based-root",
			wantOS:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := afs.NewOS(tc.root)

			if tc.wantOS {
				if _, ok := fsys.(*afero.OsFs); !ok {
					t.Fatalf("NewOS(%q) 应返回 *afero.OsFs，实际为 %T", tc.root, fsys)
				}
			}

			writeFile(t, fsys, tc.fsPath, []byte(tc.content))

			raw, err := os.ReadFile(tc.diskPath)
			if err != nil {
				t.Fatalf("通过操作系统读取 %s 失败: %v", tc.diskPath, err)
			}
			if string(raw) != tc.content {
				t.Fatalf("%s 真实内容 = %q, want %q", tc.diskPath, raw, tc.content)
			}
			if got := string(readFile(t, fsys, tc.fsPath)); got != tc.content {
				t.Fatalf("经 afs 读回 %s = %q, want %q", tc.fsPath, got, tc.content)
			}
		})
	}
}

// TestNewOSConfinesSiblingPrefixEscape proves the confinement is separator-aware: a sibling whose
// name shares the root's prefix ("app" versus "app2") is not reachable through "..", while an
// absolute path inside the root still works. | TestNewOSConfinesSiblingPrefixEscape 证明约束是
// 分隔符感知的：与根共享前缀的兄弟目录（"app" 与 "app2"）无法经 ".." 到达，而根内的绝对路径仍可用。
func TestNewOSConfinesSiblingPrefixEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	sibling := filepath.Join(parent, "app2")
	for _, dir := range []string{root, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) 失败 | failed: %v", dir, err)
		}
	}
	secret := filepath.Join(sibling, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) 失败 | failed: %v", secret, err)
	}
	fsys := afs.NewOS(root)

	for _, name := range []string{
		filepath.Join("..", "app2", "secret.txt"),
		"a/../../app2/secret.txt",
	} {
		if _, err := afero.ReadFile(fsys, name); err == nil {
			t.Errorf("NewOS(%q) 经 %q 读到了根外文件 | escaped the root through %q", root, name, name)
		}
	}
	if _, err := afero.ReadFile(fsys, secret); err == nil {
		t.Errorf("NewOS(%q) 通过绝对路径读到了根外文件 | read outside the root by absolute path", root)
	}

	inside := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(inside, []byte("inside"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) 失败 | failed: %v", inside, err)
	}
	if _, err := afero.ReadFile(fsys, inside); err != nil {
		t.Errorf("根内绝对路径应可读 | an absolute path inside the root must work: %v", err)
	}
}

// TestNewOSDotRootServesCurrentDirectory is the regression for afero.BasePathFs treating "." as a
// prefix: every relative lookup failed, so NewOS(".") could not serve the working directory. |
// TestNewOSDotRootServesCurrentDirectory 是 afero.BasePathFs 把 "." 当字符串前缀的回归：所有相对
// 查找都失败，导致 NewOS(".") 无法服务当前工作目录。
func TestNewOSDotRootServesCurrentDirectory(t *testing.T) {
	fsys := afs.NewOS(".")
	if _, err := fsys.Stat("."); err != nil {
		t.Fatalf("NewOS(\".\") 无法 Stat 当前目录 | cannot stat the current directory: %v", err)
	}
}

// TestNewEmbed 表驱动验证基于 fstest.MapFS 的 embed 后端：
// 路径遵循 io/fs 语义（fs.ValidPath，无前导斜杠），且整体只读。
func TestNewEmbed(t *testing.T) {
	fsys := afs.NewEmbed(fstest.MapFS{
		"a.txt":          &fstest.MapFile{Data: []byte("alpha")},
		"dir/b.txt":      &fstest.MapFile{Data: []byte("beta")},
		"dir/deep/c.bin": &fstest.MapFile{Data: []byte{0x00, 0x01, 0xff}},
	})

	cases := []struct {
		name    string
		path    string
		want    []byte
		wantErr bool
	}{
		{name: "根级文件可读", path: "a.txt", want: []byte("alpha")},
		{name: "嵌套文件可读", path: "dir/deep/c.bin", want: []byte{0x00, 0x01, 0xff}},
		{name: "子目录中的文件可读", path: "dir/b.txt", want: []byte("beta")},
		{name: "目录本身可 Stat", path: "dir", wantErr: false},
		{name: "读取不存在的文件返回错误", path: "missing.txt", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := afero.ReadFile(fsys, tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("读取 %s 应返回错误", tc.path)
				}
				return
			}
			if tc.want == nil { // Readability only (directory case). | 仅验证可读（目录用例）
				if _, statErr := fsys.Stat(tc.path); statErr != nil {
					t.Fatalf("Stat(%s) 失败: %v", tc.path, statErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("读取 %s 失败: %v", tc.path, err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("读取 %s = %v, want %v", tc.path, got, tc.want)
			}
		})
	}

	t.Run("embed 后端只读", func(t *testing.T) {
		if err := fsys.MkdirAll("newdir", 0o755); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("MkdirAll 应返回 fs.ErrPermission，实际为 %v", err)
		}
		if _, err := fsys.Create("new.txt"); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("Create 应返回 fs.ErrPermission，实际为 %v", err)
		}
	})
}

// TestExists table-drives the Exists verdict on every backend: a directory counts as existing too. | TestExists 表驱动验证 Exists 在各后端上的判定：目录同样视为存在。
func TestExists(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{name: "普通文件存在", path: "a.txt", want: true},
		{name: "目录同样视为存在", path: "sub", want: true},
		{name: "嵌套目录存在", path: "sub/deep", want: true},
		{name: "空目录存在", path: "empty", want: true},
		{name: "嵌套文件存在", path: "sub/deep/c.bin", want: true},
		{name: "文件不存在", path: "nope.txt", want: false},
		{name: "已存在目录下的文件不存在", path: "sub/nope.txt", want: false},
		{name: "前缀不存在", path: "other/x.txt", want: false},
	}

	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			fsys := b.new(t)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					target := b.path(tc.path)
					if got := afs.Exists(fsys, target); got != tc.want {
						t.Fatalf("Exists(%q) = %v, want %v", target, got, tc.want)
					}
				})
			}
		})
	}
}

// TestWalk 表驱动验证 Walk 遵循 afero.Walk 语义：
// 包含 root 自身、按字典序深度优先、fn 通过 err 参数收到遍历错误。
func TestWalk(t *testing.T) {
	wantAll := []string{".", "a.txt", "empty", "sub", "sub/b.txt", "sub/deep", "sub/deep/c.bin"}

	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			fsys := b.new(t)

			t.Run("包含 root 且按字典序深度优先", func(t *testing.T) {
				var visits []string
				err := afs.Walk(fsys, b.root, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if info == nil {
						return errors.New("info 不应为 nil: " + path)
					}
					rel, relErr := filepath.Rel(b.root, path)
					if relErr != nil {
						return relErr
					}
					visits = append(visits, filepath.ToSlash(rel))
					return nil
				})
				if err != nil {
					t.Fatalf("Walk 返回错误: %v", err)
				}
				if !slices.Equal(visits, wantAll) {
					t.Fatalf("访问序列 = %v, want %v", visits, wantAll)
				}
			})

			t.Run("fn 返回 filepath.SkipDir 时跳过整棵子树", func(t *testing.T) {
				var visits []string
				err := afs.Walk(fsys, b.root, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					rel, relErr := filepath.Rel(b.root, path)
					if relErr != nil {
						return relErr
					}
					rel = filepath.ToSlash(rel)
					visits = append(visits, rel)
					if rel == "sub" && info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				})
				if err != nil {
					t.Fatalf("SkipDir 不应产生错误，实际: %v", err)
				}
				want := []string{".", "a.txt", "empty", "sub"}
				if !slices.Equal(visits, want) {
					t.Fatalf("访问序列 = %v, want %v", visits, want)
				}
			})

			t.Run("fn 返回的错误原样冒泡", func(t *testing.T) {
				sentinel := errors.New("walk 中断")
				got := afs.Walk(fsys, b.root, func(path string, info os.FileInfo, err error) error {
					return sentinel
				})
				if !errors.Is(got, sentinel) {
					t.Fatalf("Walk 返回 %v, want %v", got, sentinel)
				}
			})

			t.Run("root 不存在时错误经 fn 传递", func(t *testing.T) {
				var (
					calls  int
					gotErr error
				)
				got := afs.Walk(fsys, b.path("missing-dir"), func(path string, info os.FileInfo, err error) error {
					calls++
					if info != nil {
						t.Errorf("root 不存在时 info 应为 nil，实际 %v", info)
					}
					gotErr = err
					return err
				})
				if calls != 1 {
					t.Fatalf("fn 调用次数 = %d, want 1", calls)
				}
				if !errors.Is(gotErr, fs.ErrNotExist) {
					t.Fatalf("fn 收到的错误 = %v, want fs.ErrNotExist", gotErr)
				}
				if !errors.Is(got, fs.ErrNotExist) {
					t.Fatalf("Walk 返回值 = %v, want fs.ErrNotExist", got)
				}
			})
		})
	}
}

// TestCopyDirPreservesNestedFiles 验证 CopyDir 保留相对结构、二进制内容、
// 空目录，并采用 0o755/0o644 权限；srcDir 自身不产生额外层级。
func TestCopyDirPreservesNestedFiles(t *testing.T) {
	const dstRoot = "/dst"

	for _, b := range backends(t) {
		t.Run(b.name, func(t *testing.T) {
			src := b.new(t)
			dst := afs.NewMem()

			if err := afs.CopyDir(src, dst, b.root, dstRoot); err != nil {
				t.Fatalf("CopyDir 失败: %v", err)
			}

			for rel, want := range testTree {
				got := readFile(t, dst, dstRoot+"/"+rel)
				if !bytes.Equal(got, want) {
					t.Fatalf("复制后的 %s = %v, want %v", rel, got, want)
				}
			}

			if !afs.Exists(dst, dstRoot+"/empty") {
				t.Fatal("the empty directory was not copied | 空目录未被复制")
			}
			if afs.Exists(dst, dstRoot+"/srv") {
				t.Fatalf("srcDir 自身不应产生额外层级，%s/srv 不应存在", dstRoot)
			}

			for _, dir := range []string{dstRoot, dstRoot + "/sub", dstRoot + "/sub/deep", dstRoot + "/empty"} {
				info, err := dst.Stat(dir)
				if err != nil {
					t.Fatalf("Stat(%s) 失败: %v", dir, err)
				}
				if !info.IsDir() {
					t.Fatalf("%s 应为目录", dir)
				}
				if perm := info.Mode().Perm(); perm != 0o755 {
					t.Fatalf("%s 目录权限 = %o, want 755", dir, perm)
				}
			}

			for rel := range testTree {
				target := dstRoot + "/" + rel
				info, err := dst.Stat(target)
				if err != nil {
					t.Fatalf("Stat(%s) 失败: %v", target, err)
				}
				if perm := info.Mode().Perm(); perm != 0o644 {
					t.Fatalf("%s 文件权限 = %o, want 644", target, perm)
				}
			}

			// The source side must stay untouched. | 源端不应被改动
			for rel, want := range testTree {
				if got := readFile(t, src, b.path(rel)); !bytes.Equal(got, want) {
					t.Fatalf("源文件 %s 被改动: %v", rel, got)
				}
			}
		})
	}
}

// TestWriteFileIfChangedSkipsIdentical 表驱动验证内容相同不写入、
// 内容不同才写入、父目录缺失自动创建、覆盖时正确截断。
func TestWriteFileIfChangedSkipsIdentical(t *testing.T) {
	type step struct {
		data        []byte
		wantChanged bool
	}

	cases := []struct {
		name  string
		path  string
		steps []step
	}{
		{
			name: "内容变化时才写入",
			path: "/out/app.txt",
			steps: []step{
				{data: []byte("v1"), wantChanged: true},
				{data: []byte("v1"), wantChanged: false},
				{data: []byte("v2"), wantChanged: true},
				{data: []byte("v2"), wantChanged: false},
				{data: []byte("a much longer content"), wantChanged: true},
				{data: []byte("x"), wantChanged: true}, // Overwriting with less content must truncate. | 覆盖变短必须截断
			},
		},
		{
			name: "父目录不存在时自动创建",
			path: "/deep/nested/dir/file.txt",
			steps: []step{
				{data: []byte("first"), wantChanged: true},
				{data: []byte("first"), wantChanged: false},
			},
		},
		{
			name: "空内容同样参与比较",
			path: "/empty.bin",
			steps: []step{
				{data: []byte{}, wantChanged: true},
				{data: []byte{}, wantChanged: false},
				{data: []byte{0x00}, wantChanged: true},
				{data: []byte{}, wantChanged: true},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := afs.NewMem()

			// Precondition: the parent directory (excluding the filesystem root, such as "/" or Windows "\") does not exist yet. | 前置断言：父目录（不含文件系统根，如 "/" 或 Windows 的 "\"）尚未存在
			parent := filepath.Dir(tc.path)
			if filepath.Dir(parent) != parent && afs.Exists(fsys, parent) {
				t.Fatalf("前置条件失败：父目录 %s 不应预先存在", parent)
			}

			for i, st := range tc.steps {
				var before time.Time
				if afs.Exists(fsys, tc.path) {
					info, err := fsys.Stat(tc.path)
					if err != nil {
						t.Fatalf("第 %d 步 Stat 失败: %v", i+1, err)
					}
					before = info.ModTime()
				}

				changed, err := afs.WriteFileIfChanged(fsys, tc.path, st.data)
				if err != nil {
					t.Fatalf("第 %d 步 WriteFileIfChanged 失败: %v", i+1, err)
				}
				if changed != st.wantChanged {
					t.Fatalf("第 %d 步 changed = %v, want %v", i+1, changed, st.wantChanged)
				}
				if got := readFile(t, fsys, tc.path); !bytes.Equal(got, st.data) {
					t.Fatalf("第 %d 步内容 = %v, want %v", i+1, got, st.data)
				}

				info, err := fsys.Stat(tc.path)
				if err != nil {
					t.Fatalf("第 %d 步 Stat 失败: %v", i+1, err)
				}
				if !st.wantChanged && !info.ModTime().Equal(before) {
					t.Fatalf("第 %d 步内容相同却发生了写入（ModTime 由 %v 变为 %v）", i+1, before, info.ModTime())
				}
				if perm := info.Mode().Perm(); perm != 0o644 {
					t.Fatalf("第 %d 步文件权限 = %o, want 644", i+1, perm)
				}
			}
		})
	}

	t.Run("父目录确实被创建", func(t *testing.T) {
		fsys := afs.NewMem()
		if _, err := afs.WriteFileIfChanged(fsys, "/a/b/c/d.txt", []byte("x")); err != nil {
			t.Fatalf("WriteFileIfChanged 失败: %v", err)
		}
		for _, dir := range []string{"/a", "/a/b", "/a/b/c"} {
			if !afs.Exists(fsys, dir) {
				t.Fatalf("父目录 %s 应被自动创建", dir)
			}
		}
	})

	t.Run("写入后不残留临时文件", func(t *testing.T) {
		fsys := afs.NewMem()
		for _, p := range []string{"/x/y.txt", "/x/z.txt"} {
			if _, err := afs.WriteFileIfChanged(fsys, p, []byte("v")); err != nil {
				t.Fatalf("WriteFileIfChanged(%q) 失败: %v", p, err)
			}
		}
		entries, err := afero.ReadDir(fsys, "/x")
		if err != nil {
			t.Fatalf("ReadDir 失败: %v", err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".yd-write-") {
				t.Fatalf("临时文件 %q 未被清理 | temporary file %q was left behind", e.Name(), e.Name())
			}
		}
	})
}

// TestSha256FileMatchesCrypto table-drives a cross-check against crypto/sha256 and verifies the case and the error path. | TestSha256FileMatchesCrypto 表驱动对拍 crypto/sha256，并校验大小写与错误路径。
func TestSha256FileMatchesCrypto(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{name: "空文件", data: []byte{}},
		{name: "单字节", data: []byte("a")},
		{name: "标准向量 abc", data: []byte("abc")},
		{name: "二进制内容", data: []byte{0x00, 0x01, 0xfe, 0xff, 0x7f}},
		{name: "跨读取块的大文件", data: bytes.Repeat([]byte("yd-"), 200_000)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sum := sha256.Sum256(tc.data)
			want := hex.EncodeToString(sum[:])

			memFS := afs.NewMem()
			writeFile(t, memFS, "/blob.bin", tc.data)
			assertSha256(t, memFS, "/blob.bin", want)

			embedFS := afs.NewEmbed(fstest.MapFS{
				"blob.bin": &fstest.MapFile{Data: tc.data},
			})
			assertSha256(t, embedFS, "blob.bin", want)
		})
	}

	t.Run("符合 SHA-256 已知向量", func(t *testing.T) {
		fsys := afs.NewMem()
		writeFile(t, fsys, "/abc.txt", []byte("abc"))
		const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
		assertSha256(t, fsys, "/abc.txt", want)
	})

	t.Run("文件不存在返回错误", func(t *testing.T) {
		fsys := afs.NewMem()
		if _, err := afs.Sha256File(fsys, "/missing.bin"); err == nil {
			t.Fatal("Sha256File must return an error for a missing file | Sha256File 对不存在的文件应返回错误")
		}
	})
}

// assertSha256 asserts that the Sha256File output equals want and is 64 lowercase hexadecimal digits. | assertSha256 断言 Sha256File 输出等于 want，且为 64 位小写十六进制。
func assertSha256(t *testing.T, fsys afs.FS, path, want string) {
	t.Helper()
	got, err := afs.Sha256File(fsys, path)
	if err != nil {
		t.Fatalf("Sha256File(%s) 失败: %v", path, err)
	}
	if got != want {
		t.Fatalf("Sha256File(%s) = %s, want %s", path, got, want)
	}
	if len(got) != sha256.Size*2 {
		t.Fatalf("摘要长度 = %d, want %d", len(got), sha256.Size*2)
	}
	if strings.ToLower(got) != got {
		t.Fatalf("摘要应为小写十六进制，实际 %s", got)
	}
}

// TestWalkOverCopyOnWriteEmbed is the regression for afero.Walk over a wrapper whose base is an
// io/fs backend: nested names must stay slash-separated or the embedded layer becomes invisible on
// Windows. | TestWalkOverCopyOnWriteEmbed 是「afero.Walk 遍历底层为 io/fs 的包装器」的回归：嵌套名
// 必须保持斜杠分隔，否则内嵌层在 Windows 上会不可见。
func TestWalkOverCopyOnWriteEmbed(t *testing.T) {
	base := afs.NewEmbed(fstest.MapFS{
		"templates/base.html": &fstest.MapFile{Data: []byte("base")},
	})
	fsys := afero.NewCopyOnWriteFs(base, afs.NewMem())

	var files []string
	err := afs.Walk(fsys, "templates", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Walk 失败 | failed: %v", err)
	}
	if !slices.Equal(files, []string{"templates/base.html"}) {
		t.Fatalf("文件 = %v，期望 [templates/base.html] | files = %v", files, files)
	}
}

// TestWriteFileIfChangedOverBasePath is the regression for deriving the temporary name from
// File.Name(): BasePathFs strips the root but leaves a leading separator, which an absolute-name
// check then mistakes for a path outside the root on Linux. | TestWriteFileIfChangedOverBasePath 是
// 「从 File.Name() 取临时文件名」的回归：BasePathFs 去掉根前缀却留下前导分隔符，绝对路径检查会把它
// 误判为根外路径（Linux 上尤为明显）。
func TestWriteFileIfChangedOverBasePath(t *testing.T) {
	base := afero.NewMemMapFs()
	fsys := afero.NewBasePathFs(base, "/root")
	if err := fsys.MkdirAll("pkg", 0o755); err != nil {
		t.Fatalf("MkdirAll 失败 | failed: %v", err)
	}
	changed, err := afs.WriteFileIfChanged(fsys, "pkg/x.txt", []byte("content"))
	if err != nil {
		t.Fatalf("WriteFileIfChanged 失败 | failed: %v", err)
	}
	if !changed {
		t.Fatal("首次写入应返回 changed=true | the first write must report a change")
	}
	got, err := afero.ReadFile(base, "/root/pkg/x.txt")
	if err != nil || string(got) != "content" {
		t.Fatalf("内容 = %q, err = %v | content/error", got, err)
	}
}

// TestOverlayPrefersUpperAndFallsBackToBase proves the copy-on-write facade keeps the embedded tree as a
// read-only floor: upper wins on conflicts, base fills the gaps and writes never reach base.
// | TestOverlayPrefersUpperAndFallsBackToBase 证明写时复制门面把内嵌树当作只读底板：
// upper 在冲突时胜出，base 补缺，写入永远不落回 base。
func TestOverlayPrefersUpperAndFallsBackToBase(t *testing.T) {
	base := afs.NewEmbed(fstest.MapFS{
		"a.txt": &fstest.MapFile{Data: []byte("base-a")},
		"b.txt": &fstest.MapFile{Data: []byte("base-b")},
	})
	upper := afs.NewMem()
	if err := afero.WriteFile(upper, "a.txt", []byte("upper-a"), 0o644); err != nil {
		t.Fatalf("写入 upper 失败 | failed: %v", err)
	}

	overlay := afs.Overlay(base, upper)
	got, err := afero.ReadFile(overlay, "a.txt")
	if err != nil || string(got) != "upper-a" {
		t.Fatalf("冲突文件 = %q, err = %v，期望 upper-a | conflict = %q, err = %v", got, err, got, err)
	}
	got, err = afero.ReadFile(overlay, "b.txt")
	if err != nil || string(got) != "base-b" {
		t.Fatalf("回落文件 = %q, err = %v，期望 base-b | fallback = %q, err = %v", got, err, got, err)
	}
	if err := afero.WriteFile(overlay, "c.txt", []byte("new"), 0o644); err != nil {
		t.Fatalf("写入 overlay 失败 | failed: %v", err)
	}
	if _, err := afero.ReadFile(base, "c.txt"); err == nil {
		t.Fatal("写入不得落到只读 base | a write must not land in read-only base")
	}
	if got, err := afero.ReadFile(upper, "c.txt"); err != nil || string(got) != "new" {
		t.Fatalf("upper 中的新文件 = %q, err = %v | new upper file = %q, err = %v", got, err, got, err)
	}
}

// TestIOFSTreeAndSub proves the embedded view and subtree helpers expose exactly the io/fs tree that
// NewEmbed wrapped, and reject a non-embedded filesystem. | TestIOFSTreeAndSub 证明内嵌视图与子树助手
// 暴露的正是 NewEmbed 包装的那棵 io/fs 树，并拒绝非内嵌文件系统。
func TestIOFSTreeAndSub(t *testing.T) {
	embedded := afs.NewEmbed(fstest.MapFS{
		"static/css/x.css": &fstest.MapFile{Data: []byte("x")},
		"other.txt":        &fstest.MapFile{Data: []byte("o")},
	})
	tree, ok := afs.IOFSTree(embedded)
	if !ok {
		t.Fatal("NewEmbed 返回的 FS 必须可还原为 io/fs 树 | a NewEmbed FS must expose its io/fs tree")
	}
	sub, err := afs.Sub(tree, "static")
	if err != nil {
		t.Fatalf("Sub 失败 | failed: %v", err)
	}
	got, err := afero.ReadFile(sub, "css/x.css")
	if err != nil || string(got) != "x" {
		t.Fatalf("子树文件 = %q, err = %v | subtree file = %q, err = %v", got, err, got, err)
	}
	if _, err := afero.ReadFile(sub, "other.txt"); err == nil {
		t.Fatal("子树不得包含父树中的兄弟文件 | the subtree must not expose a sibling of the parent")
	}
	if _, ok := afs.IOFSTree(afs.NewMem()); ok {
		t.Fatal("内存 FS 不是内嵌 FS | a memory FS is not an embedded FS")
	}
	// fs.Sub validates lazily: a missing directory may only fail on access. Either failure mode is
	// acceptable as long as the subtree never serves content. | fs.Sub 惰性校验：目录不存在时可能到访问
	// 才报错；两种失败方式都可接受，只要子树绝不提供内容。
	if missing, err := afs.Sub(tree, "missing"); err == nil {
		if _, readErr := afero.ReadFile(missing, "anything"); readErr == nil {
			t.Fatal("不存在的子树不得提供任何内容 | a missing subtree must serve nothing")
		}
	}
}

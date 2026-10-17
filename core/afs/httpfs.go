package afs

import (
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/spf13/afero"
)

// HTTPFileSystem 把 afero.Fs 适配为 http.FileSystem，供 http.FileServer 使用。
//
// 相比 afero.NewHttpFs，本适配器额外解决两个真实存在的跨平台缺陷：
//
//  1. net/http 传给 Open 的路径带前导 "/"。afero.NewHttpFs 会把该路径原样透传，
//     而 MemMapFs 在 Windows 上把 "/a/b.css" 规范化为 "\a\b.css"，
//     与写入时 "a/b.css" 规范化得到的 "a\b.css" 不是同一个键 —— 静态资源全部 404。
//  2. go:embed / io/fs 后端要求路径满足 fs.ValidPath（斜杠分隔且**不含前导斜杠**），
//     直接透传会返回 ErrInvalid。
//
// 适配器统一把路径规范化为「斜杠分隔、无前导斜杠」形式后交给底层文件系统：
// 各 afero 后端会自行完成操作系统相关的分隔符规范化，因此该形式在所有后端
// （OsFs / BasePathFs / MemMapFs / FromIOFS）与所有平台上都成立。
type HTTPFileSystem struct {
	fs afero.Fs
}

// NewHTTPFileSystem creates an http.FileSystem adapter. | NewHTTPFileSystem 创建一个 http.FileSystem 适配器。
func NewHTTPFileSystem(fs afero.Fs) http.FileSystem {
	return &HTTPFileSystem{fs: fs}
}

// Open implements http.FileSystem. | Open 实现 http.FileSystem。
func (h *HTTPFileSystem) Open(name string) (http.File, error) {
	if strings.ContainsRune(name, '\x00') {
		return nil, os.ErrInvalid
	}
	// path.Clean removes upward segments such as ".." and prevents directory traversal. | path.Clean 会消除 ".." 等上跳片段，避免目录穿越
	cleaned := strings.TrimPrefix(path.Clean("/"+name), "/")
	if cleaned == "" {
		cleaned = "."
	}
	f, err := h.fs.Open(cleaned)
	if err != nil {
		return nil, err
	}
	hf, ok := f.(http.File)
	if !ok {
		// 底层不是 http.File（缺少 Seek/Readdir），按不存在处理，
		// 避免向 http.FileServer 返回不可用对象。
		_ = f.Close()
		return nil, os.ErrNotExist
	}
	return hf, nil
}

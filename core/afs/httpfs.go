package afs

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
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
	// path.Clean only understands "/" separators. On Windows a request such as
	// "..\\static2\\secret" survives it and reaches the OS backend as a traversal. Localize
	// rejects backslashes, drive/volume names and other names that are not local to the platform;
	// net/http's Dir applies the same check. | path.Clean 只理解 "/" 分隔符。在 Windows 上，
	// "..\\static2\\secret" 这类请求能穿过它并以穿越路径落到 OS 后端。Localize 会拒绝反斜杠、
	// 盘符/卷名等非本平台本地名称；net/http 的 Dir 施加的是同一道检查。
	// Validate platform-local naming, but keep opening the slash-separated form: io/fs and
	// go:embed backends require "/" even on Windows, and afero's OS backend localizes on its own.
	// | 只做平台本地命名校验，打开时仍使用斜杠形式：io/fs 与 go:embed 后端在 Windows 上也要求
	// "/"，而 afero 的 OS 后端会自行完成本地化。
	if _, err := filepath.Localize(cleaned); err != nil {
		return nil, os.ErrInvalid
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

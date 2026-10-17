package afs_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/fstest"

	"github.com/shibingli/yd/core/afs"
	"github.com/spf13/afero"
)

// serve drives one real request path through http.FileServer. | serve 用 http.FileServer 走一遍真实请求路径。
func serve(fs interface {
	Open(string) (http.File, error)
}, target string) (int, string) {
	srv := http.FileServer(fs)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	return rec.Code, string(body)
}

func TestHTTPFileSystemMemFSServesFile(t *testing.T) {
	mem := afs.NewMem()
	if err := afero.WriteFile(mem, "css/app.css", []byte("body{color:red}"), 0o644); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}
	code, body := serve(afs.NewHTTPFileSystem(mem), "/css/app.css")
	if code != http.StatusOK || body != "body{color:red}" {
		t.Fatalf("code=%d body=%q（MemFS 静态资源必须可服务，Windows 上尤其不能因分隔符而 404）", code, body)
	}
}

func TestHTTPFileSystemEmbedFSServesFile(t *testing.T) {
	embedFS := afs.NewEmbed(fstest.MapFS{
		"css/app.css": &fstest.MapFile{Data: []byte("h1{}")},
		"js/htmx.js":  &fstest.MapFile{Data: []byte("//htmx")},
		"templates/a": &fstest.MapFile{Data: []byte("tpl")},
	})
	code, body := serve(afs.NewHTTPFileSystem(embedFS), "/css/app.css")
	if code != http.StatusOK || body != "h1{}" {
		t.Fatalf("embed FS code=%d body=%q（io/fs 后端不接受带前导斜杠的路径）", code, body)
	}
}

func TestHTTPFileSystemRejectsTraversalAndMissing(t *testing.T) {
	embedFS := afs.NewEmbed(fstest.MapFS{"a.txt": &fstest.MapFile{Data: []byte("a")}})
	hfs := afs.NewHTTPFileSystem(embedFS)
	if _, err := hfs.Open("/../secret.txt"); err == nil {
		t.Error("a traversal path must be rejected or absent | 目录穿越路径必须被拒绝或不存在")
	}
	if _, err := hfs.Open("/missing.txt"); err == nil {
		t.Error("a missing file must return an error | 缺失文件必须返回错误")
	} else if !os.IsNotExist(err) {
		t.Errorf("缺失文件错误类型 = %v, 期望 not-exist", err)
	}
	if _, err := hfs.Open("/a\x00b"); err == nil {
		t.Error("a path containing NUL must be rejected | 含 NUL 的路径必须被拒绝")
	}
}

func TestHTTPFileSystemRootDirectory(t *testing.T) {
	mem := afs.NewMem()
	if err := mem.MkdirAll("sub", 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	code, _ := serve(afs.NewHTTPFileSystem(mem), "/")
	if code != http.StatusOK {
		t.Fatalf("根目录 code=%d, 期望 200", code)
	}
}

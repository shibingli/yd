package router

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// uploadFile is one file part of a test request. | uploadFile 是测试请求中的一个文件分段。
type uploadFile struct {
	field    string
	filename string
	content  string
}

// uploadField is one plain form field of a test request. | uploadField 是测试请求中的一个普通表单字段。
type uploadField struct {
	name  string
	value string
}

// multipartRequest builds a POST with the given fields and files. | multipartRequest 用给定的字段与文件构建 POST 请求。
func multipartRequest(t *testing.T, target string, fields []uploadField, files []uploadFile) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := mw.WriteField(f.name, f.value); err != nil {
			t.Fatalf("写入字段 %q 失败: %v", f.name, err)
		}
	}
	for _, f := range files {
		w, err := mw.CreateFormFile(f.field, f.filename)
		if err != nil {
			t.Fatalf("创建分段 %q 失败: %v", f.filename, err)
		}
		if _, err := io.WriteString(w, f.content); err != nil {
			t.Fatalf("写入分段 %q 失败: %v", f.filename, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart 写入器失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestStreamUploadsSkipsPlainFields(t *testing.T) {
	r := New()
	var seen []string
	r.POST("/upload", func(c *Ctx) {
		err := c.StreamUploads(func(p UploadPart) error {
			data, readErr := io.ReadAll(p.Reader)
			if readErr != nil {
				return readErr
			}
			seen = append(seen, p.Name+"|"+p.Filename+"|"+string(data)+"|"+p.ContentType)
			return nil
		})
		if err != nil {
			t.Errorf("StreamUploads 返回错误: %v", err)
		}
		_ = c.NoContent()
	})

	req := multipartRequest(t, "/upload",
		[]uploadField{{name: "title", value: "ignored"}},
		[]uploadFile{
			{field: "file", filename: "a.txt", content: "alpha"},
			{field: "file", filename: "b.bin", content: "beta"},
		})
	r.ServeHTTP(httptest.NewRecorder(), req)

	if len(seen) != 2 {
		t.Fatalf("分段数 = %d, 期望 2: %v", len(seen), seen)
	}
	if seen[0] != "file|a.txt|alpha|application/octet-stream" {
		t.Fatalf("第一段 = %q", seen[0])
	}
	if seen[1] != "file|b.bin|beta|application/octet-stream" {
		t.Fatalf("第二段 = %q", seen[1])
	}
}

func TestStreamUploadsNotMultipart(t *testing.T) {
	r := New()
	var err error
	r.POST("/upload", func(c *Ctx) {
		err = c.StreamUploads(func(UploadPart) error { return nil })
		_ = c.NoContent()
	})

	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if !errors.Is(err, ErrNotMultipart) {
		t.Fatalf("err = %v, 期望 ErrNotMultipart", err)
	}
}

func TestStreamUploadsEarlyStop(t *testing.T) {
	r := New()
	count := 0
	var err error
	r.POST("/upload", func(c *Ctx) {
		err = c.StreamUploads(func(UploadPart) error {
			count++
			return ErrUploadDone
		})
		_ = c.NoContent()
	})

	req := multipartRequest(t, "/upload", nil, []uploadFile{
		{field: "file", filename: "a.txt", content: "alpha"},
		{field: "file", filename: "b.txt", content: "beta"},
	})
	r.ServeHTTP(httptest.NewRecorder(), req)

	if err != nil {
		t.Fatalf("提前结束不应报错: %v", err)
	}
	if count != 1 {
		t.Fatalf("回调次数 = %d, 期望 1", count)
	}
}

func TestCopyUploadSavesFile(t *testing.T) {
	dir := t.TempDir()
	var info UploadInfo
	var err error
	r := New()
	r.POST("/upload", func(c *Ctx) {
		info, err = c.CopyUpload("file", dir, 0)
		_ = c.NoContent()
	})

	req := multipartRequest(t, "/upload", nil, []uploadFile{
		{field: "file", filename: "报表 2026.txt", content: "hello upload"},
	})
	r.ServeHTTP(httptest.NewRecorder(), req)

	if err != nil {
		t.Fatalf("CopyUpload 失败: %v", err)
	}
	if info.Filename != "报表 2026.txt" || info.Size != int64(len("hello upload")) {
		t.Fatalf("info = %+v", info)
	}
	if filepath.Dir(info.SavedPath) != filepath.Clean(dir) {
		t.Fatalf("SavedPath = %q, 不在 %q 内", info.SavedPath, dir)
	}
	data, readErr := os.ReadFile(info.SavedPath)
	if readErr != nil {
		t.Fatalf("读取已保存文件失败: %v", readErr)
	}
	if string(data) != "hello upload" {
		t.Fatalf("内容 = %q", data)
	}
}

func TestCopyUploadsUniqueNames(t *testing.T) {
	dir := t.TempDir()
	var infos []UploadInfo
	var err error
	r := New()
	r.POST("/upload", func(c *Ctx) {
		infos, err = c.CopyUploads("file", dir, 0)
		_ = c.NoContent()
	})

	req := multipartRequest(t, "/upload", nil, []uploadFile{
		{field: "file", filename: "a.txt", content: "first"},
		{field: "file", filename: "a.txt", content: "second"},
	})
	r.ServeHTTP(httptest.NewRecorder(), req)

	if err != nil {
		t.Fatalf("CopyUploads 失败: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("保存数 = %d, 期望 2", len(infos))
	}
	if infos[0].Filename != "a.txt" || infos[1].Filename != "a-1.txt" {
		t.Fatalf("文件名 = %q, %q", infos[0].Filename, infos[1].Filename)
	}
	for i, want := range []string{"first", "second"} {
		data, readErr := os.ReadFile(infos[i].SavedPath)
		if readErr != nil || string(data) != want {
			t.Fatalf("第 %d 个文件 = %q, err = %v", i, data, readErr)
		}
	}
}

func TestCopyUploadStreamsLargeFile(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("0123456789abcdef", 1<<18) // 4 MiB | 4 MiB
	var info UploadInfo
	var err error
	r := New()
	r.POST("/upload", func(c *Ctx) {
		info, err = c.CopyUpload("file", dir, 0)
		_ = c.NoContent()
	})

	r.ServeHTTP(httptest.NewRecorder(), multipartRequest(t, "/upload", nil, []uploadFile{
		{field: "file", filename: "big.bin", content: content},
	}))
	if err != nil {
		t.Fatalf("CopyUpload 失败: %v", err)
	}
	if info.Size != int64(len(content)) {
		t.Fatalf("Size = %d, 期望 %d", info.Size, len(content))
	}
	st, statErr := os.Stat(info.SavedPath)
	if statErr != nil || st.Size() != int64(len(content)) {
		t.Fatalf("磁盘大小不符: st=%v err=%v", st, statErr)
	}
}

func TestCopyUploadPerFileLimit(t *testing.T) {
	dir := t.TempDir()
	var err error
	r := New()
	r.POST("/upload", func(c *Ctx) {
		_, err = c.CopyUpload("file", dir, 16)
		_ = c.NoContent()
	})

	r.ServeHTTP(httptest.NewRecorder(), multipartRequest(t, "/upload", nil, []uploadFile{
		{field: "file", filename: "big.txt", content: strings.Repeat("x", 100)},
	}))
	if !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("err = %v, 期望 ErrUploadTooLarge", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("读取目录失败: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("超限的上传不应留下文件: %v", entries)
	}
}

func TestCopyUploadRouterBodyLimit(t *testing.T) {
	dir := t.TempDir()
	var err error
	r := New(WithMaxBodySize(64))
	r.POST("/upload", func(c *Ctx) {
		_, err = c.CopyUpload("file", dir, 0)
		_ = c.NoContent()
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, multipartRequest(t, "/upload", nil, []uploadFile{
		{field: "file", filename: "big.txt", content: strings.Repeat("x", 4096)},
	}))
	if !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("err = %v, 期望 ErrUploadTooLarge", err)
	}
	// The 413 response itself is written by http.MaxBytesReader against a real server writer;
	// httptest.ResponseRecorder deliberately cannot implement its unexported hook. | 413 由
	// http.MaxBytesReader 针对真实服务器写入器写出；httptest.ResponseRecorder 无法实现其未导出钩子。
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("读取目录失败: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("超限的上传不应留下文件: %v", entries)
	}
}

func TestStreamUploadsNilCallback(t *testing.T) {
	r := New()
	var err error
	r.POST("/upload", func(c *Ctx) {
		err = c.StreamUploads(nil)
		_ = c.NoContent()
	})

	r.ServeHTTP(httptest.NewRecorder(), multipartRequest(t, "/upload", nil, []uploadFile{
		{field: "file", filename: "a.txt", content: "x"},
	}))
	if !errors.Is(err, ErrUploadCallback) {
		t.Fatalf("err = %v, 期望 ErrUploadCallback", err)
	}
}

func TestCopyUploadMissingField(t *testing.T) {
	dir := t.TempDir()
	var err error
	r := New()
	r.POST("/upload", func(c *Ctx) {
		_, err = c.CopyUpload("file", dir, 0)
		_ = c.NoContent()
	})

	r.ServeHTTP(httptest.NewRecorder(), multipartRequest(t, "/upload", nil, []uploadFile{
		{field: "other", filename: "a.txt", content: "x"},
	}))
	if !errors.Is(err, ErrNoUpload) {
		t.Fatalf("err = %v, 期望 ErrNoUpload", err)
	}
}

func TestCopyUploadTraversalStaysInsideDir(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "uploads")
	var info UploadInfo
	var err error
	r := New()
	r.POST("/upload", func(c *Ctx) {
		info, err = c.CopyUpload("file", dir, 0)
		_ = c.NoContent()
	})

	r.ServeHTTP(httptest.NewRecorder(), multipartRequest(t, "/upload", nil, []uploadFile{
		{field: "file", filename: "../../evil.txt", content: "pwn"},
	}))
	if err != nil {
		t.Fatalf("CopyUpload 失败: %v", err)
	}
	if info.Filename != "evil.txt" {
		t.Fatalf("Filename = %q, 期望 evil.txt", info.Filename)
	}
	if filepath.Dir(filepath.Clean(info.SavedPath)) != filepath.Clean(dir) {
		t.Fatalf("SavedPath = %q, 越出了 %q", info.SavedPath, dir)
	}
	if _, statErr := os.Stat(filepath.Join(parent, "evil.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("父目录不应出现文件: %v", statErr)
	}
}

func TestSafeUploadName(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "report.pdf", want: "report.pdf"},
		{in: "../../etc/passwd", want: "passwd"},
		{in: `..\..\windows\win.ini`, want: "win.ini"},
		{in: "C:\\tmp\\x.tar.gz", want: "x.tar.gz"},
		{in: "  spaced.txt  ", want: "spaced.txt"},
		{in: "a\x00b.txt", want: "ab.txt"},
		{in: "trailing. . . ", want: "trailing"},
		{in: "CON.txt", want: "_CON.txt"},
		{in: "CON.foo.bar", want: "_CON.foo.bar"},
		{in: "nul", want: "_nul"},
		{in: "safe" + string(rune(0x202E)) + "gnp.exe", want: "safegnp.exe"},
		{in: "COM7.log", want: "_COM7.log"},
		{in: "COM¹.log", want: "_COM¹.log"},
		{in: "CONIN$.txt", want: "_CONIN$.txt"},
		{in: "CONOUT$.txt", want: "_CONOUT$.txt"},
		{in: "a:b?c*.txt", want: "a_b_c_.txt"},
		{in: "C:evil.txt", want: "C_evil.txt"},
		{in: `a<b>c"d|e.txt`, want: "a_b_c_d_e.txt"},
		{in: "CONSOLE.txt", want: "CONSOLE.txt"},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "..", wantErr: true},
		{in: "a/..", wantErr: true},
		{in: "/", wantErr: true},
	}
	for _, tt := range tests {
		got, err := SafeUploadName(tt.in)
		if tt.wantErr {
			if !errors.Is(err, ErrUploadName) {
				t.Errorf("SafeUploadName(%q) err = %v, 期望 ErrUploadName", tt.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("SafeUploadName(%q) 返回错误: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("SafeUploadName(%q) = %q, 期望 %q", tt.in, got, tt.want)
		}
	}
}

func TestSafeUploadNameBoundsLength(t *testing.T) {
	got, err := SafeUploadName(strings.Repeat("a", 300) + ".txt")
	if err != nil {
		t.Fatalf("返回错误: %v", err)
	}
	if len(got) != 200 || !strings.HasSuffix(got, ".txt") {
		t.Fatalf("长度 = %d, 后缀 = %q", len(got), filepath.Ext(got))
	}

	got, err = SafeUploadName(strings.Repeat("报", 100))
	if err != nil {
		t.Fatalf("返回错误: %v", err)
	}
	if len(got) > 200 || !utf8.ValidString(got) {
		t.Fatalf("截断结果非法: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
}

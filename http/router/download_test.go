package router

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// downloadTime is the fixed modification time of download tests. | downloadTime 是下载测试的固定修改时间。
var downloadTime = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func TestDownloadBytesHeadersAndBody(t *testing.T) {
	content := []byte("%PDF-1.7 fake content")
	var handlerErr error
	r := New()
	r.GET("/file", func(c *Ctx) {
		handlerErr = c.DownloadBytes(content, DownloadOptions{
			Filename: "报表 2026.pdf",
			ModTime:  downloadTime,
		})
	})

	rec := do(r, http.MethodGet, "/file", nil, "")
	if handlerErr != nil {
		t.Fatalf("DownloadBytes 失败: %v", handlerErr)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != string(content) {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(content)) {
		t.Fatalf("Content-Length = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := rec.Header().Get("Last-Modified"); got != downloadTime.Format(http.TimeFormat) {
		t.Fatalf("Last-Modified = %q", got)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, `filename*=UTF-8''%E6%8A%A5%E8%A1%A8%202026.pdf`) {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if etag := rec.Header().Get("ETag"); !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Fatalf("ETag = %q", etag)
	}
}

func TestDownloadRangeAndConditional(t *testing.T) {
	content := []byte("0123456789")
	r := New()
	r.GET("/file", func(c *Ctx) {
		_ = c.DownloadBytes(content, DownloadOptions{
			Filename:    "numbers.txt",
			ContentType: "text/plain",
			ETag:        "v1",
			ModTime:     downloadTime,
		})
	})

	rec := do(r, http.MethodGet, "/file", map[string]string{"Range": "bytes=2-5"}, "")
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "2345" {
		t.Fatalf("Range: code=%d body=%q", rec.Code, rec.Body.String())
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q", cr)
	}

	rec = do(r, http.MethodGet, "/file", map[string]string{"If-None-Match": `"v1"`}, "")
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("If-None-Match: code=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = do(r, http.MethodGet, "/file", map[string]string{"If-Modified-Since": downloadTime.Format(http.TimeFormat)}, "")
	if rec.Code != http.StatusNotModified {
		t.Fatalf("If-Modified-Since: code=%d", rec.Code)
	}
}

func TestDownloadAutoETagMatchesConditional(t *testing.T) {
	content := []byte("auto etag")
	r := New()
	r.GET("/file", func(c *Ctx) {
		_ = c.DownloadBytes(content, DownloadOptions{Filename: "a.txt", ModTime: downloadTime})
	})

	first := do(r, http.MethodGet, "/file", nil, "")
	tag := first.Header().Get("ETag")
	if tag == "" {
		t.Fatal("missing auto ETag | 缺少自动 ETag")
	}
	rec := do(r, http.MethodGet, "/file", map[string]string{"If-None-Match": tag}, "")
	if rec.Code != http.StatusNotModified {
		t.Fatalf("code = %d, 期望 304", rec.Code)
	}
}

func TestDownloadSniffsUnknownExtension(t *testing.T) {
	content := []byte("<!doctype html><title>sniffed</title>")
	r := New()
	r.GET("/file", func(c *Ctx) {
		_ = c.DownloadBytes(content, DownloadOptions{Filename: "no-extension"})
	})

	rec := do(r, http.MethodGet, "/file", nil, "")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, 期望 text/html", ct)
	}
	if rec.Body.String() != string(content) {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestDownloadHEADFallsBackToGET(t *testing.T) {
	content := []byte("head body")
	var handlerErr error
	r := New()
	r.GET("/file", func(c *Ctx) {
		handlerErr = c.DownloadBytes(content, DownloadOptions{Filename: "a.bin"})
	})

	rec := do(r, http.MethodHead, "/file", nil, "")
	if handlerErr != nil {
		t.Fatalf("HEAD 处理失败: %v", handlerErr)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, 期望 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(content)) {
		t.Fatalf("HEAD Content-Length = %q", got)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD 不应有正文: %q", rec.Body.String())
	}
}

func TestDownloadReaderSizes(t *testing.T) {
	r := New()
	r.GET("/known", func(c *Ctx) {
		_ = c.DownloadReader(strings.NewReader("hello"), 5, DownloadOptions{Filename: "a.txt"})
	})
	r.GET("/unknown", func(c *Ctx) {
		_ = c.DownloadReader(strings.NewReader("hello"), -1, DownloadOptions{Filename: "a.txt"})
	})

	rec := do(r, http.MethodGet, "/known", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("known: code=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Length"); got != "5" {
		t.Fatalf("Content-Length = %q", got)
	}

	rec = do(r, http.MethodGet, "/unknown", nil, "")
	if rec.Body.String() != "hello" {
		t.Fatalf("unknown: body=%q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Fatalf("未知大小的 Content-Length = %q, 期望为空", got)
	}
}

func TestDownloadFileFromFS(t *testing.T) {
	fsys := fstest.MapFS{
		"static/hello.txt": &fstest.MapFile{Data: []byte("from fs"), ModTime: downloadTime},
	}
	r := New()
	r.GET("/hello", func(c *Ctx) {
		if err := c.DownloadFile(fsys, "static/hello.txt", DownloadOptions{}); err != nil {
			t.Errorf("DownloadFile 失败: %v", err)
		}
	})

	rec := do(r, http.MethodGet, "/hello", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "from fs" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Last-Modified"); got != downloadTime.Format(http.TimeFormat) {
		t.Fatalf("Last-Modified = %q", got)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="hello.txt"`) {
		t.Fatalf("Content-Disposition = %q", cd)
	}
}

func TestDownloadNilReader(t *testing.T) {
	r := New()
	var err error
	r.GET("/file", func(c *Ctx) {
		err = c.Download(nil, DownloadOptions{})
		_ = c.NoContent()
	})

	do(r, http.MethodGet, "/file", nil, "")
	if !errors.Is(err, ErrDownloadReader) {
		t.Fatalf("err = %v, 期望 ErrDownloadReader", err)
	}
}

func TestDownloadEmptyBody(t *testing.T) {
	r := New()
	r.GET("/empty", func(c *Ctx) {
		_ = c.DownloadBytes(nil, DownloadOptions{ContentType: "application/octet-stream"})
	})

	rec := do(r, http.MethodGet, "/empty", nil, "")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Length"); got != "0" {
		t.Fatalf("Content-Length = %q", got)
	}
}

func TestContentDisposition(t *testing.T) {
	if got := contentDisposition("report.pdf", false); got != `attachment; filename="report.pdf"; filename*=UTF-8''report.pdf` {
		t.Fatalf("普通文件名 = %q", got)
	}
	got := contentDisposition("报表 2026.pdf", true)
	if !strings.HasPrefix(got, "inline; ") {
		t.Fatalf("inline = %q", got)
	}
	if !strings.Contains(got, `filename="`+"_ 2026.pdf"+`"`) {
		t.Fatalf("ASCII 兜底 = %q", got)
	}
	if !strings.Contains(got, `filename*=UTF-8''%E6%8A%A5%E8%A1%A8%202026.pdf`) {
		t.Fatalf("RFC 5987 = %q", got)
	}

	injected := contentDisposition("a\r\nX-Evil: 1.txt", false)
	if strings.ContainsAny(injected, "\r\n") {
		t.Fatalf("头部注入未被清除: %q", injected)
	}
	if got := contentDisposition("", false); got != "attachment" {
		t.Fatalf("空文件名 = %q", got)
	}
}

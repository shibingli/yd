package router

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shibingli/yd/core/errx"
)

// downloadCopyBuffer is the fallback copy buffer size when neither side implements WriterTo/
// ReaderFrom. | downloadCopyBuffer 是两侧都未实现 WriterTo/ReaderFrom 时的兜底拷贝缓冲大小。
const downloadCopyBuffer = 32 << 10

// downloadBufPool reuses the fallback copy buffers. | downloadBufPool 复用兜底拷贝缓冲。
var downloadBufPool = sync.Pool{New: func() any {
	b := make([]byte, downloadCopyBuffer)
	return &b
}}

// Download errors, declared once at package level. | 下载错误，在包级声明一次。
var (
	// ErrDownload indicates the response could not be produced from the source. | ErrDownload 表示无法从数据源产出响应。
	ErrDownload = errx.New("router: download failed | router：下载失败")
	// ErrDownloadReader indicates a nil download source. | ErrDownloadReader 表示下载数据源为 nil。
	ErrDownloadReader = errx.New("router: download source is nil | router：下载数据源为 nil")
	// ErrDownloadOpen indicates the file could not be opened or inspected. | ErrDownloadOpen 表示无法打开或读取文件信息。
	ErrDownloadOpen = errx.New("router: cannot open the download file | router：无法打开下载文件")
)

// DownloadOptions controls a download response. | DownloadOptions 控制下载响应。
//
// The zero value is valid: the file name is taken from the path, the MIME type is inferred, and the
// disposition is attachment. | 零值可用：文件名取自路径，MIME 类型自动推断，处置方式为附件。
type DownloadOptions struct {
	// ModTime is the modification time, sent as Last-Modified and used for conditional requests. | ModTime 是修改时间，作为 Last-Modified 发送并用于条件请求。
	ModTime time.Time
	// Filename is the suggested file name for the client. | Filename 是建议客户端保存的文件名。
	Filename string
	// ContentType overrides the inferred MIME type. | ContentType 覆盖推断出的 MIME 类型。
	ContentType string
	// ETag is the entity tag; when empty and ModTime is set, one is derived from size and mtime. | ETag 是实体标签；为空且设置了 ModTime 时，会由大小与修改时间派生一个。
	ETag string
	// CacheControl sets the Cache-Control header when non-empty. | CacheControl 非空时设置 Cache-Control 头。
	CacheControl string
	// Inline selects inline instead of attachment disposition. | Inline 为真时使用 inline 而非 attachment 处置方式。
	Inline bool
}

// Download writes a seekable source with Range and conditional-request support. | Download 输出可定位的数据源，支持 Range 与条件请求。
//
// When the request carries Range/If-* headers it delegates to http.ServeContent, which implements
// RFC 7233 byte ranges and RFC 7232 preconditions correctly. Otherwise it takes a fast path: the
// length comes from one seek, the handler writes exactly one body copy, and a *os.File source with a
// sendfile-capable ResponseWriter (and a bytes.Reader source) is served without any intermediate
// buffer. | 携带 Range/If-* 请求头时交给 http.ServeContent，由它正确实现 RFC 7233 字节区间与
// RFC 7232 前置条件。否则走快路径：长度只经一次 seek 获得，正文只拷贝一次；*os.File 配合支持
// sendfile 的响应写入器（以及 bytes.Reader）无需任何中间缓冲即可输出。
func (c *Ctx) Download(r io.ReadSeeker, o DownloadOptions) error {
	if r == nil {
		return ErrDownloadReader
	}
	c.prepareDownload(o)

	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return errx.Wrap(err, ErrDownload)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return errx.Wrap(err, ErrDownload)
	}
	if err := c.finishDownloadHeaders(o, size, r); err != nil {
		return err
	}

	if downloadNeedsCondense(c.Request) {
		http.ServeContent(c.Writer, c.Request, o.Filename, o.ModTime, r)
		c.flags |= ctxWritten
		return nil
	}

	c.SetHeader("Content-Length", strconv.FormatInt(size, 10))
	c.WriteHeader(http.StatusOK)
	if c.Request.Method == http.MethodHead || size == 0 {
		return nil
	}
	if _, err := copyResponse(c.Writer, r); err != nil {
		return errx.Wrap(err, ErrDownload)
	}
	return nil
}

// DownloadBytes writes b as a download. | DownloadBytes 把 b 作为下载内容输出。
//
// bytes.Reader implements io.WriterTo, so the body is written in one call without an intermediate
// buffer. | bytes.Reader 实现了 io.WriterTo，因此正文一次写出，无需中间缓冲。
func (c *Ctx) DownloadBytes(b []byte, o DownloadOptions) error {
	return c.Download(bytes.NewReader(b), o)
}

// DownloadReader writes a non-seekable source of a known or unknown size. | DownloadReader 输出大小已知或未知的不可定位数据源。
//
// Range and conditional requests are not available because the source cannot be rewound; use
// Download (or DownloadFile) whenever the content is seekable. size < 0 omits Content-Length. | 由于
// 数据源不可回退，Range 与条件请求不可用；内容可定位时应使用 Download（或 DownloadFile）。
// size < 0 表示不发送 Content-Length。
func (c *Ctx) DownloadReader(r io.Reader, size int64, o DownloadOptions) error {
	if r == nil {
		return ErrDownloadReader
	}
	c.prepareDownload(o)
	if c.Writer.Header().Get("Content-Type") == "" {
		c.SetHeader("Content-Type", inferredContentType(o.Filename))
	}
	if size >= 0 {
		c.SetHeader("Content-Length", strconv.FormatInt(size, 10))
	}
	c.autoETag(o, size)
	c.WriteHeader(http.StatusOK)
	if c.Request.Method == http.MethodHead {
		return nil
	}
	if _, err := copyResponse(c.Writer, r); err != nil {
		return errx.Wrap(err, ErrDownload)
	}
	return nil
}

// DownloadFile opens name from fsys and streams it as a download. | DownloadFile 从 fsys 打开 name 并作为下载内容流式输出。
//
// Embedded files (embed.FS) and OS files are seekable, so they get Range and If-* support; the file
// is closed when the response finishes. An afero file system can be passed via afero.NewIOFS. | 嵌入文件
// （embed.FS）与操作系统文件是可定位的，因此支持 Range 与 If-*；响应结束时文件会被关闭。
// afero 文件系统可经 afero.NewIOFS 传入。
func (c *Ctx) DownloadFile(fsys fs.FS, name string, o DownloadOptions) error {
	if fsys == nil {
		return ErrDownloadOpen
	}
	f, err := fsys.Open(name)
	if err != nil {
		return errx.Wrap(err, ErrDownloadOpen)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return errx.Wrap(err, ErrDownloadOpen)
	}
	if o.Filename == "" {
		o.Filename = info.Name()
		if o.Filename == "" || o.Filename == "." {
			o.Filename = path.Base(name)
		}
	}
	if o.ModTime.IsZero() {
		o.ModTime = info.ModTime()
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		return c.Download(rs, o)
	}
	return c.DownloadReader(f, info.Size(), o)
}

// prepareDownload sets the headers that do not depend on the content size. | prepareDownload 设置不依赖内容大小的响应头。
func (c *Ctx) prepareDownload(o DownloadOptions) {
	h := c.Writer.Header()
	if o.ContentType != "" {
		h.Set("Content-Type", o.ContentType)
	}
	if o.CacheControl != "" {
		h.Set("Cache-Control", o.CacheControl)
	}
	if !o.ModTime.IsZero() {
		h.Set("Last-Modified", o.ModTime.UTC().Format(http.TimeFormat))
	}
	if o.Filename != "" {
		h.Set("Content-Disposition", contentDisposition(o.Filename, o.Inline))
	}
	// The type may be inferred from content, so never let a browser second-guess it. | 类型可能由内容推断，因此绝不让浏览器再猜一次。
	h.Set("X-Content-Type-Options", "nosniff")
	if o.ETag != "" {
		h.Set("ETag", quoteETag(o.ETag))
	}
}

// finishDownloadHeaders fills the size-dependent headers of the seekable path. | finishDownloadHeaders 补齐可定位路径中依赖大小的响应头。
//
// An unknown extension is sniffed from the first 512 bytes and the source is rewound, exactly as
// http.ServeContent does; a failure to rewind aborts the download instead of serving a body that
// starts at offset 512. | 未知扩展名会从首 512 字节嗅探并把数据源倒回，与 http.ServeContent 一致；
// 倒回失败会中止下载，而不是输出从偏移 512 开始的正文。
func (c *Ctx) finishDownloadHeaders(o DownloadOptions, size int64, r io.ReadSeeker) error {
	if c.Writer.Header().Get("Content-Type") == "" {
		ct, err := sniffContentType(r, o.Filename)
		if err != nil {
			return err
		}
		c.SetHeader("Content-Type", ct)
	}
	c.autoETag(o, size)
	return nil
}

// sniffContentType resolves the MIME type from the extension, then from the first 512 bytes. | sniffContentType 先由扩展名、再由首 512 字节解析 MIME 类型。
func sniffContentType(r io.ReadSeeker, filename string) (string, error) {
	if ct := mime.TypeByExtension(path.Ext(filename)); ct != "" {
		return ct, nil
	}
	var buf [512]byte
	n, err := io.ReadFull(r, buf[:])
	if _, seekErr := r.Seek(0, io.SeekStart); seekErr != nil {
		return "", errx.Wrap(seekErr, ErrDownload)
	}
	if n > 0 && (err == nil || errors.Is(err, io.ErrUnexpectedEOF)) {
		return http.DetectContentType(buf[:n]), nil
	}
	return "application/octet-stream", nil
}

// autoETag derives a strong validator from size and mtime when none was supplied, so conditional
// GETs work out of the box. | autoETag 在未提供时由大小与修改时间派生强验证器，使条件 GET 开箱即用。
//
// The format matches nginx's "hex(mtime)-hex(size)". It is only set when ModTime is known: a tag
// built from size alone would go stale when content changes without changing length. | 格式与 nginx 的
// "hex(mtime)-hex(size)" 一致。仅在 ModTime 已知时设置：只由大小构成的标签会在内容变化但长度不变时失效。
func (c *Ctx) autoETag(o DownloadOptions, size int64) {
	if o.ETag != "" || o.ModTime.IsZero() || size < 0 || c.Writer.Header().Get("ETag") != "" {
		return
	}
	tag := strconv.FormatInt(o.ModTime.Unix(), 16) + "-" + strconv.FormatInt(size, 16)
	c.SetHeader("ETag", quoteETag(tag))
}

// quoteETag wraps a bare validator in quotes; an already quoted or weak tag is kept as-is. | quoteETag 给裸验证器加引号；已带引号或弱标签的原样保留。
func quoteETag(tag string) string {
	if tag == "" || strings.HasPrefix(tag, `"`) || strings.HasPrefix(tag, `W/"`) {
		return tag
	}
	return `"` + tag + `"`
}

// downloadNeedsCondense reports whether the request needs Range or precondition handling. | downloadNeedsCondense 判断请求是否需要 Range 或前置条件处理。
func downloadNeedsCondense(r *http.Request) bool {
	return r.Header.Get("Range") != "" ||
		r.Header.Get("If-Range") != "" ||
		r.Header.Get("If-None-Match") != "" ||
		r.Header.Get("If-Modified-Since") != "" ||
		r.Header.Get("If-Match") != "" ||
		r.Header.Get("If-Unmodified-Since") != ""
}

// copyResponse copies src to dst, preferring WriterTo/ReaderFrom (sendfile or a single write) and
// falling back to a pooled buffer. | copyResponse 把 src 拷贝到 dst，优先 WriterTo/ReaderFrom
// （sendfile 或一次写出），否则回退到池化缓冲。
func copyResponse(dst io.Writer, src io.Reader) (int64, error) {
	buf := downloadBufPool.Get().(*[]byte)
	defer downloadBufPool.Put(buf)
	return io.CopyBuffer(dst, src, *buf)
}

// inferredContentType guesses the MIME type from the file extension, falling back to binary. | inferredContentType 由扩展名推断 MIME 类型，兜底为二进制流。
func inferredContentType(filename string) string {
	if ct := mime.TypeByExtension(path.Ext(filename)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// contentDisposition builds an RFC 6266 header with an RFC 5987 UTF-8 filename. | contentDisposition 构建符合 RFC 6266 的头部，并附 RFC 5987 的 UTF-8 文件名。
//
// Header values cannot contain CR/LF - a client-supplied filename with them would split the
// response - so control characters are removed before anything is encoded. The ASCII filename is
// only a fallback for clients without RFC 5987 support. | 头部值不能包含 CR/LF——客户端提供的文件名
// 若含它们就会造成响应拆分——因此在编码之前先移除控制字符。ASCII 文件名只是给不支持 RFC 5987 的
// 客户端兜底。
func contentDisposition(filename string, inline bool) string {
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	filename = sanitizeHeaderText(filename)
	if filename == "" {
		return disposition
	}
	fallback := asciiFallback(filename)
	return disposition + `; filename="` + fallback + `"; filename*=UTF-8''` + encodeRFC5987(filename)
}

// sanitizeHeaderText removes control characters that would terminate or split a header value. | sanitizeHeaderText 移除会截断或拆分头部值的控制字符。
func sanitizeHeaderText(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// asciiFallback reduces a UTF-8 filename to printable ASCII for the legacy filename parameter. | asciiFallback 把 UTF-8 文件名收敛为可打印 ASCII，供旧式 filename 参数使用。
func asciiFallback(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	replaced := false
	for _, r := range name {
		switch {
		case r < 0x20 || r >= 0x7f, r == '"', r == '\\', r == ';':
			if !replaced {
				b.WriteByte('_')
				replaced = true
			}
		default:
			b.WriteRune(r)
			replaced = false
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" || out == "_" {
		return "download"
	}
	return out
}

// encodeRFC5987 percent-encodes every byte outside the RFC 5987 attr-char set. | encodeRFC5987 对 RFC 5987 attr-char 集合之外的每个字节做百分号编码。
func encodeRFC5987(s string) string {
	const attrChar = "!#$&+-.^_`|~"
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= '0' && ch <= '9', ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z',
			strings.IndexByte(attrChar, ch) >= 0:
			b.WriteByte(ch)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[ch>>4])
			b.WriteByte(hex[ch&0x0f])
		}
	}
	return b.String()
}

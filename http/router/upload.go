package router

import (
	"errors"
	"io"
	"net/http"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/shibingli/yd/core/errx"
)

// uploadCopyBuffer is the size of the pooled buffer used to stream a part to disk. | uploadCopyBuffer
// 是把分段流式写入磁盘时所用池化缓冲区的大小。
const uploadCopyBuffer = 32 << 10

// uploadBufPool reuses the copy buffers of streaming uploads. | uploadBufPool 复用流式上传的拷贝缓冲。
var uploadBufPool = sync.Pool{New: func() any {
	b := make([]byte, uploadCopyBuffer)
	return &b
}}

// Upload errors, declared once at package level. | 上传错误，在包级声明一次。
var (
	// ErrNotMultipart indicates the request is not multipart/form-data. | ErrNotMultipart 表示请求不是 multipart/form-data。
	ErrNotMultipart = errx.New("router: request is not multipart/form-data | router：请求不是 multipart/form-data")
	// ErrUploadName indicates a client-supplied filename cannot be used safely. | ErrUploadName 表示客户端提供的文件名无法安全使用。
	ErrUploadName = errx.New("router: invalid upload filename | router：非法的上传文件名")
	// ErrUploadTooLarge indicates one file or the whole body exceeded its limit. | ErrUploadTooLarge 表示单个文件或整个请求体超过限制。
	ErrUploadTooLarge = errx.New("router: upload exceeds the size limit | router：上传超过大小限制")
	// ErrUploadCreate indicates the destination file or directory could not be created. | ErrUploadCreate 表示无法创建目标文件或目录。
	ErrUploadCreate = errx.New("router: cannot create the upload destination | router：无法创建上传目标")
	// ErrUploadWrite indicates receiving or writing the part failed. | ErrUploadWrite 表示接收或写入分段失败。
	ErrUploadWrite = errx.New("router: failed to receive or write the upload | router：接收或写入上传失败")
	// ErrNoUpload indicates no part matched the requested field. | ErrNoUpload 表示没有匹配请求字段的分段。
	ErrNoUpload = errx.New("router: no matching uploaded file | router：没有匹配的上传文件")
	// ErrUploadCallback indicates a nil StreamUploads callback. | ErrUploadCallback 表示 StreamUploads 回调为 nil。
	ErrUploadCallback = errx.New("router: nil upload callback | router：上传回调为 nil")
	// ErrUploadDone may be returned by a StreamUploads callback to stop early without an error. | ErrUploadDone 可由 StreamUploads 回调返回来提前结束且不报错。
	ErrUploadDone = errx.New("router: stop streaming uploads | router：停止流式上传")
)

// UploadPart is one file part of a multipart request. | UploadPart 是 multipart 请求中的一个文件分段。
//
// Reader streams the part directly; it is only valid until the callback returns. Copy it somewhere
// (or hand it to a storage client) instead of retaining it. | Reader 直接流式读取该分段；它只在回调
// 返回之前有效。请把它拷贝到目的地（或交给存储客户端），不要保留。
type UploadPart struct {
	// Reader streams the part content. | Reader 流式读取分段内容。
	Reader io.Reader
	// Header is the raw part header. | Header 是分段的原始头部。
	Header textproto.MIMEHeader
	// Name is the form field name. | Name 是表单字段名。
	Name string
	// Filename is the client-supplied file name, unsanitized. | Filename 是客户端提供的文件名，尚未清洗。
	Filename string
	// ContentType is the part's Content-Type header. | ContentType 是分段的 Content-Type 头。
	ContentType string
}

// UploadInfo describes one saved upload. | UploadInfo 描述一个已保存的上传。
type UploadInfo struct {
	// Name is the form field name. | Name 是表单字段名。
	Name string
	// Filename is the sanitized destination file name. | Filename 是清洗后的目标文件名。
	Filename string
	// SavedPath is the full destination path. | SavedPath 是完整的目标路径。
	SavedPath string
	// ContentType is the part's Content-Type header. | ContentType 是分段的 Content-Type 头。
	ContentType string
	// Size is the number of bytes written. | Size 是写入的字节数。
	Size int64
}

// StreamUploads iterates the file parts of a multipart request and hands each one to fn. | StreamUploads 遍历 multipart 请求的文件分段，逐个交给 fn。
//
// Plain form fields are skipped. The request body is bounded by the router's MaxBodySize (and a
// read past it reports ErrUploadTooLarge); fn should copy the part to its destination and must not
// retain Reader. Returning ErrUploadDone from fn stops the iteration without an error. | 普通表单字段
// 被跳过。请求体受路由器 MaxBodySize 限制（越界读取返回 ErrUploadTooLarge）；fn 应把分段拷贝到
// 目的地，且不得保留 Reader。fn 返回 ErrUploadDone 可提前结束且不报错。
func (c *Ctx) StreamUploads(fn func(UploadPart) error) error {
	if fn == nil {
		return ErrUploadCallback
	}
	c.limitBody()
	reader, err := c.Request.MultipartReader()
	if err != nil {
		return errx.Wrap(err, ErrNotMultipart)
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return uploadReadError(err)
		}
		if part.FileName() == "" {
			continue // plain form field | 普通表单字段
		}
		up := UploadPart{
			Name:        part.FormName(),
			Filename:    part.FileName(),
			ContentType: part.Header.Get("Content-Type"),
			Header:      part.Header,
			Reader:      part,
		}
		if err := fn(up); err != nil {
			if errors.Is(err, ErrUploadDone) {
				return nil
			}
			return err
		}
	}
}

// uploadReadError maps a body read error to an upload sentinel. | uploadReadError 把请求体读取错误映射为上传哨兵。
func uploadReadError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return errx.Wrap(err, ErrUploadTooLarge)
	}
	return errx.Wrap(err, ErrUploadWrite)
}

// CopyUpload streams the first file of field into dir and returns what it saved. | CopyUpload 把 field 的第一个文件流式写入 dir 并返回保存信息。
//
// An empty field matches the first file of any field. The destination name is sanitized (see
// SafeUploadName) and made unique instead of overwriting an existing file. maxFileSize <= 0 means
// only the router's MaxBodySize applies. Reading stops after the first match; the remaining body is
// left for the server to drain. | field 为空时匹配任意字段的第一个文件。目标名会被清洗
// （见 SafeUploadName）并在冲突时生成唯一名，而不是覆盖已有文件。maxFileSize <= 0 表示只受路由器
// 的 MaxBodySize 约束。命中第一个文件后即停止读取，剩余请求体交由服务器排空。
func (c *Ctx) CopyUpload(field, dir string, maxFileSize int64) (UploadInfo, error) {
	var info UploadInfo
	found := false
	err := c.StreamUploads(func(p UploadPart) error {
		if field != "" && p.Name != field {
			return nil
		}
		saved, size, err := c.saveUpload(p, dir, maxFileSize)
		if err != nil {
			return err
		}
		info = UploadInfo{
			Name:        p.Name,
			Filename:    saved,
			SavedPath:   filepath.Join(dir, saved),
			ContentType: p.ContentType,
			Size:        size,
		}
		found = true
		return ErrUploadDone
	})
	if err != nil {
		return UploadInfo{}, err
	}
	if !found {
		return UploadInfo{}, ErrNoUpload
	}
	return info, nil
}

// CopyUploads streams every file of field into dir. | CopyUploads 把 field 的所有文件流式写入 dir。
//
// It returns the files saved before any error, so a partial batch can still be cleaned up by the
// caller. | 它返回出错前已保存的文件，便于调用方清理不完整的一批。
func (c *Ctx) CopyUploads(field, dir string, maxFileSize int64) ([]UploadInfo, error) {
	var out []UploadInfo
	err := c.StreamUploads(func(p UploadPart) error {
		if field != "" && p.Name != field {
			return nil
		}
		saved, size, err := c.saveUpload(p, dir, maxFileSize)
		if err != nil {
			return err
		}
		out = append(out, UploadInfo{
			Name:        p.Name,
			Filename:    saved,
			SavedPath:   filepath.Join(dir, saved),
			ContentType: p.ContentType,
			Size:        size,
		})
		return nil
	})
	if err != nil {
		return out, err
	}
	if len(out) == 0 {
		return nil, ErrNoUpload
	}
	return out, nil
}

// saveUpload streams one part to a uniquely named file inside dir. | saveUpload 把一个分段流式写入 dir 内唯一命名的文件。
func (c *Ctx) saveUpload(p UploadPart, dir string, maxFileSize int64) (string, int64, error) {
	name, err := SafeUploadName(p.Filename)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, errx.WrapDetail(err, ErrUploadCreate, "dir %[1]q | 目录 %[1]q", dir)
	}
	file, saved, err := createUniqueFile(dir, name)
	if err != nil {
		return "", 0, err
	}
	size, copyErr := copyUploadPart(file, p.Reader, maxFileSize)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(filepath.Join(dir, saved))
		if copyErr != nil {
			return "", 0, copyErr
		}
		return "", 0, errx.WrapDetail(closeErr, ErrUploadWrite, "file %[1]q | 文件 %[1]q", saved)
	}
	return saved, size, nil
}

// copyUploadPart streams src into dst without buffering the part in memory. | copyUploadPart 把 src 流式写入 dst，不在内存中缓冲整个分段。
//
// dst is wrapped to hide its ReadFrom method: *os.File implements io.ReaderFrom, and io.CopyBuffer
// would then let the file allocate its own 32 KiB buffer instead of using the pooled one. The
// source is a network part, never a file, so there is no copy_file_range fast path to lose. | dst 被
// 包装以隐藏其 ReadFrom 方法：*os.File 实现了 io.ReaderFrom，否则 io.CopyBuffer 会让文件自行分配
// 32 KiB 缓冲而不用池中的缓冲。数据源是网络分段、绝不可能是文件，因此不会损失 copy_file_range 快路径。
func copyUploadPart(dst io.Writer, src io.Reader, max int64) (int64, error) {
	buf := uploadBufPool.Get().(*[]byte)
	defer uploadBufPool.Put(buf)
	reader := src
	if max > 0 {
		reader = io.LimitReader(src, max+1)
	}
	n, err := io.CopyBuffer(bufferedOnly{dst}, reader, *buf)
	if err != nil {
		return n, uploadReadError(err)
	}
	if max > 0 && n > max {
		return n, errx.Detail(ErrUploadTooLarge, "limit %[1]d bytes | 上限 %[1]d 字节", max)
	}
	return n, nil
}

// bufferedOnly hides every optional copy fast path, leaving only Write. | bufferedOnly 隐藏所有可选的拷贝快路径，只保留 Write。
type bufferedOnly struct{ w io.Writer }

// Write implements io.Writer. | Write 实现 io.Writer。
func (b bufferedOnly) Write(p []byte) (int, error) { return b.w.Write(p) }

// createUniqueFile creates dir/name with O_EXCL, adding a numeric suffix on a collision. | createUniqueFile 以 O_EXCL 创建 dir/name，冲突时追加数字后缀。
func createUniqueFile(dir, name string) (*os.File, string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		candidate := name
		if i > 0 {
			candidate = stem + "-" + strconv.Itoa(i) + ext
		}
		file, err := os.OpenFile(filepath.Join(dir, candidate), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return file, candidate, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", errx.WrapDetail(err, ErrUploadCreate, "file %[1]q | 文件 %[1]q", candidate)
		}
	}
	return nil, "", errx.Detail(ErrUploadCreate, "too many name collisions for %[1]q | %[1]q 的文件名冲突过多", name)
}

// SafeUploadName reduces a client-supplied filename to a safe base name. | SafeUploadName 把客户端提供的文件名收敛为安全的基础名。
//
// Both slash styles are stripped to a base name, NUL/control characters and bidi overrides are
// removed, trailing dots and spaces (illegal on Windows) are trimmed, the length is bounded while
// keeping the extension, and Windows device names are prefixed. The result is a single path segment
// that cannot escape the destination directory. The caller still owns the storage location: do not
// save uploads into a directory whose contents are executed as scripts. | 两种斜杠都会被收敛为基础名，
// NUL/控制字符与方向覆盖字符被移除，Windows 上非法的尾随点与空格被裁剪，长度在保留扩展名的前提下
// 受限，Windows 设备名会被加前缀。结果是无法逃出目标目录的单个路径段。存储位置仍由调用方负责：
// 不要把上传保存到会把其中内容当作脚本执行的目录。
func SafeUploadName(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.TrimSpace(name)
	name = strings.TrimRight(name, ". ")
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || isBidiControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return "", errx.Detail(ErrUploadName, "%q | %q", name, name)
	}
	if len(name) > 200 {
		ext := path.Ext(name)
		if len(ext) > 32 {
			ext = "" // a "dot" 100 bytes long is not an extension | 长达 100 字节的点段不是扩展名
		}
		keep := 200 - len(ext)
		// Cutting at a byte offset may split a UTF-8 rune; drop the partial tail. | 按字节截断可能切断 UTF-8 码点，丢弃残缺尾字节。
		name = strings.ToValidUTF8(name[:keep], "") + ext
	}
	if reservedName(name) {
		name = "_" + name
	}
	return name, nil
}

// reservedName reports a Windows device name (CON, NUL, COM1, ...), with or without an extension.
// Windows reserves the name before the *first* dot, so CON.foo.bar is reserved too. | reservedName
// 报告 Windows 设备名（CON、NUL、COM1 等），可带扩展名。Windows 判定的是**第一个点之前**的名字，
// 因此 CON.foo.bar 同样被保留。
func reservedName(name string) bool {
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.ToUpper(base)
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$":
		return true
	}
	if len(base) == 4 {
		switch base[:3] {
		case "COM", "LPT":
			return base[3] >= '1' && base[3] <= '9'
		}
	}
	return false
}

// isBidiControl reports invisible direction overrides that could disguise a file extension, e.g.
// "photo<U+202E>gnp.exe" displayed as "photoexe.png". | isBidiControl 报告可用于伪装扩展名的
// 不可见方向覆盖字符，例如把 "photo<U+202E>gnp.exe" 显示成 "photoexe.png"。
func isBidiControl(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

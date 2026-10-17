package middleware

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	"github.com/shibingli/yd/http/router"
)

// Algorithm identifies a supported content-encoding. | Algorithm 标识一种受支持的内容编码。
type Algorithm int

const (
	// Gzip is the gzip encoding of RFC 1952. | Gzip 是 RFC 1952 定义的 gzip 编码。
	Gzip Algorithm = iota
	// Brotli is the brotli encoding of RFC 7932. | Brotli 是 RFC 7932 定义的 brotli 编码。
	Brotli
	// Zstd is the zstd encoding of RFC 8878. | Zstd 是 RFC 8878 定义的 zstd 编码。
	Zstd
)

// defaultAlgorithms is the priority order used when CompressOptions.Algorithms is empty.
// | defaultAlgorithms 是 CompressOptions.Algorithms 为空时使用的优先级顺序。
var defaultAlgorithms = []Algorithm{Brotli, Zstd, Gzip}

// defaultExcludePatterns is the prefix list used when CompressOptions.ExcludePatterns is empty.
// | defaultExcludePatterns 是 CompressOptions.ExcludePatterns 为空时使用的前缀排除表。
var defaultExcludePatterns = []string{"/ws", "/sse", "/events"}

// defaultMinSize is the smallest response body worth compressing, in bytes.
// | defaultMinSize 是值得压缩的最小响应体字节数。
const defaultMinSize = 1024

// Name returns the display name of the algorithm. | Name 返回算法的展示名称。
func (a Algorithm) Name() string {
	switch a {
	case Gzip:
		return "gzip"
	case Brotli:
		return "brotli"
	case Zstd:
		return "zstd"
	default:
		return "unknown"
	}
}

// Encoding returns the HTTP content-coding token, or "" for an unknown algorithm.
// | Encoding 返回 HTTP 内容编码标识；未知算法返回空串。
func (a Algorithm) Encoding() string {
	switch a {
	case Gzip:
		return "gzip"
	case Brotli:
		return "br"
	case Zstd:
		return "zstd"
	default:
		return ""
	}
}

// ParseAcceptEncoding parses an Accept-Encoding header into encoding → q-value pairs. | ParseAcceptEncoding 把 Accept-Encoding 头解析为「编码 → q 值」映射。
// Names are lowercased and a missing or malformed q defaults to 1. | 编码名统一小写；缺失或非法的 q 按 1 处理。
// q=0 is preserved so callers can treat that encoding as explicitly disabled. | q=0 原样保留，便于调用方把该编码视为显式禁用。
// The wildcard entry is kept under the key "*". | 通配项以键 "*" 保存。
func ParseAcceptEncoding(h string) map[string]float64 {
	h = strings.TrimSpace(h)
	if h == "" {
		return nil
	}
	out := make(map[string]float64, 4)
	for _, part := range strings.Split(h, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name := part
		q := 1.0
		if i := strings.IndexByte(part, ';'); i >= 0 {
			name = strings.TrimSpace(part[:i])
			q = parseQValue(part[i+1:])
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		out[name] = q
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseQValue extracts the q parameter from the parameters part of one entry.
// | parseQValue 从一条编码项的参数部分中提取 q 参数。
func parseQValue(params string) float64 {
	for _, p := range strings.Split(params, ";") {
		p = strings.TrimSpace(p)
		if len(p) < 1 || (p[0] != 'q' && p[0] != 'Q') {
			continue
		}
		rest := strings.TrimSpace(p[1:])
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(rest[1:]), 64)
		if err != nil {
			return 1
		}
		switch {
		case v < 0:
			return 0
		case v > 1:
			return 1
		default:
			return v
		}
	}
	return 1
}

// Negotiate picks the best algorithm the client accepts. | Negotiate 选出客户端可接受的最优算法。
// Higher q-values win and ties are broken by the order of algos, which is therefore the priority list. | q 值高者优先，q 值相同时按 algos 顺序决定，因此该顺序即优先级表。
// Encodings with q=0 are disabled and a "*" entry stands for every algorithm not listed explicitly. | q=0 表示该编码被禁用；"*" 代表所有未显式列出的算法。
// An empty Accept-Encoding reports no usable algorithm, and an empty algos slice falls back to the
// default priority list. | Accept-Encoding 为空时报告没有可用算法；algos 为空时退回默认优先级表。
func Negotiate(acceptEncoding string, algos []Algorithm) (Algorithm, bool) {
	if len(algos) == 0 {
		algos = defaultAlgorithms
	}
	q := ParseAcceptEncoding(acceptEncoding)
	if len(q) == 0 {
		return 0, false
	}
	star, hasStar := q["*"]
	best := Algorithm(0)
	bestQ := 0.0
	found := false
	for _, a := range algos {
		v, ok := q[a.Encoding()]
		if !ok {
			if !hasStar {
				continue
			}
			v = star
		}
		if v <= 0 {
			continue
		}
		if !found || v > bestQ {
			best, bestQ, found = a, v, true
		}
	}
	if !found {
		return 0, false
	}
	return best, true
}

// CompressOptions configures the compression middleware.
// | CompressOptions 配置压缩中间件。
type CompressOptions struct {
	// Algorithms is the priority list; the array order is the weight order. | Algorithms 是优先级列表，数组顺序即权重顺序。
	// Defaults to []Algorithm{Brotli, Zstd, Gzip}. | 默认为 []Algorithm{Brotli, Zstd, Gzip}。
	Algorithms []Algorithm
	// ExcludePatterns is a list of path prefixes that are never wrapped. | ExcludePatterns 是永不包装的路径前缀列表。
	// Defaults to {"/ws", "/sse", "/events"} and replaces those defaults when set. | 默认为 {"/ws", "/sse", "/events"}，一旦配置即替换默认值。
	ExcludePatterns []string
	// MinSize is the smallest body that is worth compressing; 0 means 1024 bytes.
	// | MinSize 是值得压缩的最小正文长度；0 表示 1024 字节。
	MinSize int
	// Level is the codec compression level; 0 keeps each codec's own default.
	// | Level 是编解码器的压缩级别；0 表示沿用各自的默认级别。
	Level int
}

// compressConfig is the normalized, immutable configuration of one Compress instance.
// | compressConfig 是单个 Compress 实例归一化后的不可变配置。
type compressConfig struct {
	pools   [3]*encoderPool
	algos   []Algorithm
	exclude []string
	minSize int
	level   int
}

// newCompressConfig normalizes o and prepares one encoder pool per algorithm.
// | newCompressConfig 归一化 o，并为每种算法准备一个编码器池。
func newCompressConfig(o CompressOptions) *compressConfig {
	cfg := &compressConfig{
		algos:   o.Algorithms,
		minSize: o.MinSize,
		level:   o.Level,
		exclude: o.ExcludePatterns,
	}
	if len(cfg.algos) == 0 {
		cfg.algos = defaultAlgorithms
	}
	if cfg.minSize <= 0 {
		cfg.minSize = defaultMinSize
	}
	if len(cfg.exclude) == 0 {
		cfg.exclude = defaultExcludePatterns
	}
	for i := range cfg.pools {
		cfg.pools[i] = &encoderPool{algo: Algorithm(i), level: cfg.level}
	}
	return cfg
}

// skip reports whether the response for c must not be wrapped at all.
// | skip 报告 c 的响应是否完全不应被包装。
func (cfg *compressConfig) skip(c *router.Ctx) bool {
	r := c.Request
	if r.Method == http.MethodHead {
		return true
	}
	if isUpgradeRequest(r) {
		return true
	}
	path := r.URL.Path
	for _, prefix := range cfg.exclude {
		if prefix != "" && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// Compress returns a middleware that compresses responses with brotli, zstd or gzip. | Compress 返回用 brotli、zstd 或 gzip 压缩响应的中间件。
// The encoding is chosen from the request's Accept-Encoding by Negotiate. | 编码由 Negotiate 依据请求的 Accept-Encoding 选出。
// HEAD requests, protocol upgrades, excluded path prefixes, SSE responses, bodiless or already
// encoded responses, bodies below MinSize and responses carrying Cache-Control: no-transform are
// never wrapped. | HEAD 请求、协议升级、命中排除前缀、SSE 响应、无正文或已编码的响应、小于 MinSize 的正文，以及带 Cache-Control: no-transform 的响应一律不包装。
// A compressed response gains Content-Encoding and Vary: Accept-Encoding while Content-Length is
// dropped, and its encoder comes from a sync.Pool that it returns to when the response finishes. | 被压缩的响应会带上 Content-Encoding 与 Vary: Accept-Encoding 并删除 Content-Length；编码器取自 sync.Pool，并在响应结束时归还。
func Compress(o CompressOptions) router.Middleware {
	cfg := newCompressConfig(o)
	return func(c *router.Ctx) {
		if cfg.skip(c) {
			c.Next()
			return
		}
		algo, ok := Negotiate(c.Request.Header.Get("Accept-Encoding"), cfg.algos)
		if !ok {
			c.Next()
			return
		}
		cw := &compressWriter{
			ctx:     c,
			rw:      c.Writer,
			algo:    algo,
			pool:    cfg.pools[algo],
			minSize: cfg.minSize,
		}
		c.Writer = cw
		// finish also runs while a panic unwinds, so buffers never leak. | panic 展开时 finish 同样会执行，缓冲不会泄漏。
		defer cw.finish()
		c.Next()
	}
}

// compressWriter defers the response header until the body size is known. | compressWriter 延迟写出响应头，直到正文长度可知。
// Writes are buffered until they reach MinSize. | 写入先被缓冲，直到累计长度达到 MinSize。
// At that point the middleware either starts the encoder or, when compression is impossible,
// switches to a plain pass-through and hands the context back to the original writer. | 达到阈值时要么启动编码器，要么在无法压缩时切换为直通，并把上下文交还给原始写入器。
type compressWriter struct {
	rw      http.ResponseWriter
	enc     encoder
	err     error
	ctx     *router.Ctx
	pool    *encoderPool
	buf     []byte
	algo    Algorithm
	minSize int

	code    int
	decided bool
	closed  bool
}

// statusAllowsCompression reports whether a body may follow the given status code.
// | statusAllowsCompression 报告给定状态码之后是否还可能有正文。
func statusAllowsCompression(code int) bool {
	switch {
	case code < 200:
		return false
	case code == http.StatusNoContent, code == http.StatusResetContent, code == http.StatusNotModified:
		return false
	}
	return true
}

// contentLength returns the declared Content-Length, if any. | contentLength 返回已声明的 Content-Length（若有）。
func contentLength(h http.Header) (int, bool) {
	v := strings.TrimSpace(h.Get("Content-Length"))
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// Header returns the wrapped header map. | Header 返回被包装的响应头映射。
func (w *compressWriter) Header() http.Header { return w.rw.Header() }

// WriteHeader records the status and decides whether the body can be compressed.
// | WriteHeader 记录状态码并判定正文是否可压缩。
func (w *compressWriter) WriteHeader(code int) {
	if w.decided || w.code != 0 {
		return
	}
	w.code = code
	if !statusAllowsCompression(code) || !w.headersAllowCompression() {
		w.passThrough()
		return
	}
	// A declared length that already reaches MinSize lets us compress immediately. | 已声明且达到 MinSize 的长度允许立即压缩。
	if n, ok := contentLength(w.rw.Header()); ok && n >= w.minSize {
		w.beginCompress()
	}
}

// Write buffers the body until the compression decision is taken. | Write 缓冲正文，直到压缩判定完成。
func (w *compressWriter) Write(p []byte) (int, error) {
	if w.decided {
		if w.enc != nil {
			return w.enc.Write(p)
		}
		return w.rw.Write(p)
	}
	w.buf = append(w.buf, p...)
	if len(w.buf) >= w.minSize {
		w.beginCompress()
	}
	if w.err != nil {
		return 0, w.err
	}
	return len(p), nil
}

// Flush pushes buffered data out; streaming before the threshold stays uncompressed.
// | Flush 把缓冲数据推送给客户端；阈值之前开始的流式响应保持不压缩。
func (w *compressWriter) Flush() {
	if !w.decided {
		w.passThrough()
	}
	if w.enc != nil {
		if f, ok := w.enc.(interface{ Flush() error }); ok {
			_ = f.Flush()
		}
	}
	if f, ok := w.rw.(http.Flusher); ok {
		f.Flush()
	}
}

// headersAllowCompression reports whether the current response headers forbid compression.
// | headersAllowCompression 报告当前响应头是否禁止压缩。
func (w *compressWriter) headersAllowCompression() bool {
	h := w.rw.Header()
	if h.Get("Content-Encoding") != "" {
		return false
	}
	if ct := strings.ToLower(strings.TrimSpace(h.Get("Content-Type"))); strings.HasPrefix(ct, "text/event-stream") {
		return false
	}
	if cc := strings.ToLower(h.Get("Cache-Control")); strings.Contains(cc, "no-transform") {
		return false
	}
	if n, ok := contentLength(h); ok && n < w.minSize {
		return false
	}
	return true
}

// beginCompress writes the header and starts the encoder, flushing the buffered body into it.
// | beginCompress 写出响应头并启动编码器，把已缓冲的正文冲入其中。
func (w *compressWriter) beginCompress() {
	if w.decided {
		return
	}
	if !w.headersAllowCompression() {
		w.passThrough()
		return
	}
	enc := w.pool.get(w.rw)
	if enc == nil {
		w.passThrough()
		return
	}
	w.enc = enc
	w.decided = true

	h := w.rw.Header()
	h.Set("Content-Encoding", w.algo.Encoding())
	h.Del("Content-Length")
	appendVary(h, "Accept-Encoding")

	code := w.code
	if code == 0 {
		code = http.StatusOK
	}
	w.rw.WriteHeader(code)
	if len(w.buf) > 0 {
		if _, err := w.enc.Write(w.buf); err != nil {
			w.err = err
		}
		w.buf = nil
	}
}

// passThrough writes the buffered body unchanged and stops wrapping.
// | passThrough 原样写出已缓冲的正文，并停止包装。
func (w *compressWriter) passThrough() {
	if w.decided {
		return
	}
	w.decided = true
	code := w.code
	if code == 0 {
		code = http.StatusOK
	}
	w.rw.WriteHeader(code)
	if len(w.buf) > 0 {
		n, err := w.rw.Write(w.buf)
		if err == nil && n < len(w.buf) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.err = err
		}
		w.buf = nil
	}
	w.detach()
}

// detach gives the context back to the original writer when it still points at this wrapper.
// | detach 在上下文仍指向本包装器时，把上下文交还给原始写入器。
func (w *compressWriter) detach() {
	if w.ctx != nil && w.ctx.Writer == http.ResponseWriter(w) {
		w.ctx.Writer = w.rw
	}
}

// finish closes the encoder and returns it to the pool. | finish 关闭编码器并把它归还到池中。
func (w *compressWriter) finish() {
	if w.closed {
		return
	}
	w.closed = true
	if !w.decided {
		// The whole body stayed below MinSize, so it goes out untouched. | 整个正文都没达到 MinSize，原样输出。
		w.passThrough()
	}
	if w.enc != nil {
		_ = w.enc.Close()
		w.pool.put(w.enc)
		w.enc = nil
	}
	w.detach()
}

// encoder is a reusable compression stream. | encoder 是可复用的压缩流。
type encoder interface {
	io.WriteCloser
	// Reset retargets the encoder at w for a new stream. | Reset 把编码器重新指向 w，开始新的压缩流。
	Reset(w io.Writer)
}

// encoderPool pools encoders of a single algorithm and level. | encoderPool 池化同一算法与级别的编码器。
type encoderPool struct {
	pool  sync.Pool
	level int
	algo  Algorithm
}

// get returns a reset encoder writing to w, or nil when the codec cannot be built.
// | get 返回已重置、写入 w 的编码器；无法构建时返回 nil。
func (p *encoderPool) get(w io.Writer) encoder {
	if v := p.pool.Get(); v != nil {
		enc := v.(encoder)
		enc.Reset(w)
		return enc
	}
	return p.newEncoder(w)
}

// put returns an encoder to the pool. | put 把编码器归还到池中。
func (p *encoderPool) put(enc encoder) {
	if enc != nil {
		p.pool.Put(enc)
	}
}

// newEncoder builds the codec backing the pool. | newEncoder 构建池所使用的编码器实例。
func (p *encoderPool) newEncoder(w io.Writer) encoder {
	switch p.algo {
	case Gzip:
		if p.level == 0 {
			return gzip.NewWriter(w)
		}
		zw, err := gzip.NewWriterLevel(w, clampLevel(p.level, gzip.BestSpeed, gzip.BestCompression))
		if err != nil {
			return gzip.NewWriter(w)
		}
		return zw
	case Brotli:
		if p.level == 0 {
			return brotli.NewWriter(w)
		}
		return brotli.NewWriterLevel(w, clampLevel(p.level, brotli.BestSpeed, brotli.BestCompression))
	case Zstd:
		// One concurrency per stream keeps every pooled encoder goroutine-free. | 每个流一个并发，使池化编码器不占用 goroutine。
		opts := []zstd.EOption{zstd.WithEncoderConcurrency(1)}
		if p.level != 0 {
			opts = append(opts, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(p.level)))
		}
		enc, err := zstd.NewWriter(w, opts...)
		if err != nil {
			return nil
		}
		return enc
	default:
		return nil
	}
}

// clampLevel constrains a codec level to the range it supports. | clampLevel 把压缩级别限制在编解码器支持的区间内。
func clampLevel(level, minLevel, maxLevel int) int {
	if level < minLevel {
		return minLevel
	}
	if level > maxLevel {
		return maxLevel
	}
	return level
}

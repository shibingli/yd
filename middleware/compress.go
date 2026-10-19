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
	"github.com/shibingli/yd/core/bufpool"
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
//
// Zstd leads because on dynamic payloads it matches or beats brotli's ratio at a fraction of the
// CPU cost (see docs/verification.md §3.19); brotli stays second for clients that do not accept
// zstd, and gzip is the universal fallback. | Zstd 居首是因为在动态载荷上它的压缩率与 brotli
// 持平或更好，而 CPU 成本低得多（见 docs/verification.md §3.19）；brotli 留给不接受 zstd 的
// 客户端，gzip 作为通用兜底。
var defaultAlgorithms = []Algorithm{Zstd, Brotli, Gzip}

// defaultBrotliQuality is the brotli quality used when CompressOptions.Level is zero. Quality 4
// keeps brotli's ratio while cutting its CPU cost substantially compared with the library default
// of 6. | defaultBrotliQuality 是 CompressOptions.Level 为零时使用的 brotli 质量。质量 4 保持
// brotli 的压缩率，同时比库默认的 6 显著降低 CPU 成本。
const defaultBrotliQuality = 4

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
// The scan performs no allocations, which keeps it off the per-request heap. | 该扫描不产生任何分配，因而不会进入每请求的堆分配路径。
func Negotiate(acceptEncoding string, algos []Algorithm) (Algorithm, bool) {
	if len(algos) == 0 {
		algos = defaultAlgorithms
	}
	// The common three-algorithm setup stays on the stack; longer lists fall back to the heap.
	// | 常见的三算法配置完全留在栈上；更长的列表才退回堆分配。
	var smallQ [8]float64
	var smallSeen [8]bool
	q, seen := smallQ[:], smallSeen[:]
	if len(algos) > len(smallQ) {
		q = make([]float64, len(algos))
		seen = make([]bool, len(algos))
	}
	var wildcard float64
	hasWildcard := false

	for rest := acceptEncoding; len(rest) > 0; {
		entry := rest
		if i := strings.IndexByte(rest, ','); i >= 0 {
			entry, rest = rest[:i], rest[i+1:]
		} else {
			rest = ""
		}
		name, qv, ok := parseAcceptEntry(entry)
		if !ok {
			continue
		}
		if name == "*" {
			wildcard, hasWildcard = qv, true
			continue
		}
		for i, a := range algos {
			if i < len(q) && strings.EqualFold(name, a.Encoding()) {
				q[i], seen[i] = qv, true
			}
		}
	}

	best := Algorithm(0)
	bestQ := 0.0
	found := false
	for i, a := range algos {
		v, ok := q[i], seen[i]
		if !ok {
			if !hasWildcard {
				continue
			}
			v = wildcard
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

// parseAcceptEntry splits one Accept-Encoding list entry into its case-insensitive name
// and q-value. | parseAcceptEntry 把一条 Accept-Encoding 列表项拆成名称与 q 值。
// The name is returned as written; callers compare it with strings.EqualFold so no lowercase copy
// is allocated. | 名称按原样返回；调用方用 strings.EqualFold 比较，因此不会分配小写副本。
func parseAcceptEntry(entry string) (string, float64, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", 0, false
	}
	name, params := entry, ""
	if i := strings.IndexByte(entry, ';'); i >= 0 {
		name, params = strings.TrimSpace(entry[:i]), entry[i+1:]
	}
	if name == "" {
		return "", 0, false
	}
	return name, parseQValueNoAlloc(params), true
}

// parseQValueNoAlloc is the allocation-free equivalent of parseQValue. | parseQValueNoAlloc 是 parseQValue 的无分配等价实现。
func parseQValueNoAlloc(params string) float64 {
	for len(params) > 0 {
		part := params
		if i := strings.IndexByte(params, ';'); i >= 0 {
			part, params = params[:i], params[i+1:]
		} else {
			params = ""
		}
		part = strings.TrimSpace(part)
		if len(part) < 1 || (part[0] != 'q' && part[0] != 'Q') {
			continue
		}
		rest := strings.TrimSpace(part[1:])
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

// CompressOptions configures the compression middleware.
// | CompressOptions 配置压缩中间件。
type CompressOptions struct {
	// Algorithms is the priority list; the array order is the weight order. | Algorithms 是优先级列表，数组顺序即权重顺序。
	// Defaults to []Algorithm{Zstd, Brotli, Gzip}. | 默认为 []Algorithm{Zstd, Brotli, Gzip}。
	Algorithms []Algorithm
	// ExcludePatterns is a list of path prefixes that are never wrapped. | ExcludePatterns 是永不包装的路径前缀列表。
	// Defaults to {"/ws", "/sse", "/events"} and replaces those defaults when set. | 默认为 {"/ws", "/sse", "/events"}，一旦配置即替换默认值。
	ExcludePatterns []string
	// MinSize is the smallest body that is worth compressing; 0 means 1024 bytes.
	// | MinSize 是值得压缩的最小正文长度；0 表示 1024 字节。
	MinSize int
	// Level is the codec compression level; 0 uses the middleware defaults (brotli quality 4,
	// zstd and gzip at their codec defaults). | Level 是编解码器的压缩级别；0 表示使用中间件默认值
	// （brotli 质量 4，zstd 与 gzip 沿用各自默认级别）。
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
// The encoding is chosen from the request's Accept-Encoding by Negotiate, and only once a response
// is actually large enough to compress. | 编码由 Negotiate 依据请求的 Accept-Encoding 选出，且只在响应确实大到值得压缩时才协商。
// HEAD requests, protocol upgrades, excluded path prefixes, SSE responses, bodiless or already
// encoded responses, bodies below MinSize and responses carrying Cache-Control: no-transform are
// never wrapped. | HEAD 请求、协议升级、命中排除前缀、SSE 响应、无正文或已编码的响应、小于 MinSize 的正文，以及带 Cache-Control: no-transform 的响应一律不包装。
// A compressed response gains Content-Encoding and Vary: Accept-Encoding while Content-Length is
// dropped, and its encoder and pending body buffer come from pools that are returned when the
// response finishes. | 被压缩的响应会带上 Content-Encoding 与 Vary: Accept-Encoding 并删除 Content-Length；编码器与待压缩正文缓冲均取自池，并在响应结束时归还。
func Compress(o CompressOptions) router.Middleware {
	cfg := newCompressConfig(o)
	return func(c *router.Ctx) {
		if cfg.skip(c) {
			c.Next()
			return
		}
		// A missing header can never negotiate, so skip the wrapper altogether. | 请求头缺失时必然协商失败，直接跳过包装。
		accept := headerValue(c.Request.Header, "Accept-Encoding")
		if accept == "" {
			c.Next()
			return
		}
		cw := acquireCompressWriter(c, accept, cfg)
		c.Writer = cw
		// finish also runs while a panic unwinds, so buffers never leak. | panic 展开时 finish 同样会执行，缓冲不会泄漏。
		defer cw.finish()
		c.Next()
	}
}

// compressWriterPool recycles the per-request wrapper so the skip path does not allocate one.
// | compressWriterPool 复用每请求的包装器，使跳过路径不再分配它。
var compressWriterPool = sync.Pool{New: func() any { return new(compressWriter) }}

// acquireCompressWriter takes a wrapper from the pool and points it at c. | acquireCompressWriter 从池中取出包装器并指向 c。
func acquireCompressWriter(c *router.Ctx, accept string, cfg *compressConfig) *compressWriter {
	w := compressWriterPool.Get().(*compressWriter)
	w.ctx = c
	w.rw = c.Writer
	w.cfg = cfg
	w.accept = accept
	w.minSize = cfg.minSize
	return w
}

// releaseCompressWriter clears every request-scoped reference before pooling the wrapper.
// | releaseCompressWriter 在归还包装器前清除所有与请求相关的引用。
func releaseCompressWriter(w *compressWriter) {
	w.ctx = nil
	w.rw = nil
	w.cfg = nil
	w.enc = nil
	w.sink = encoderSink{}
	w.pool = nil
	w.err = nil
	w.buf = nil
	w.accept = ""
	w.algo = 0
	w.code = 0
	w.minSize = 0
	w.decided = false
	w.closed = false
	compressWriterPool.Put(w)
}

// appendBuffer appends p to dst, taking a pooled block when dst lacks capacity.
// | appendBuffer 把 p 追加到 dst；容量不足时从池中取新块。
func appendBuffer(dst, p []byte) []byte {
	need := len(dst) + len(p)
	if need <= cap(dst) {
		return append(dst, p...)
	}
	grown := bufpool.Get(need)
	grown = append(grown, dst...)
	bufpool.Put(dst)
	return append(grown, p...)
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
	cfg     *compressConfig
	pool    *encoderPool
	accept  string
	sink    encoderSink
	buf     []byte
	algo    Algorithm
	minSize int

	code    int
	decided bool
	closed  bool
}

// encoderSinkSize bounds the pooled buffer that coalesces encoder output. | encoderSinkSize 限制合并编码器输出的池化缓冲大小。
const encoderSinkSize = 4 << 10

// encoderSink coalesces encoder writes into one pooled buffer before they reach the response
// writer. | encoderSink 在编码器输出到达响应写入器之前，用一个池化缓冲合并这些小写入。
type encoderSink struct {
	dst io.Writer
	buf []byte
}

// reset points the sink at dst and takes a pooled buffer. | reset 把槽指向 dst 并取得池化缓冲。
func (s *encoderSink) reset(dst io.Writer) {
	s.dst = dst
	s.buf = bufpool.Get(encoderSinkSize)
}

// Write buffers p, or writes it straight through when it is already large. | Write 缓冲 p；
// 若 p 本身已足够大则直接写出。
func (s *encoderSink) Write(p []byte) (int, error) {
	if len(p) >= encoderSinkSize && len(s.buf) == 0 {
		return s.dst.Write(p)
	}
	if len(s.buf)+len(p) > encoderSinkSize {
		// Flush first so the pooled buffer never has to grow; large writes then bypass it.
		// | 先冲刷，池化缓冲因此永不扩容；随后的大块写入直接绕过它。
		if err := s.flush(); err != nil {
			return 0, err
		}
		if len(p) >= encoderSinkSize {
			return s.dst.Write(p)
		}
	}
	s.buf = append(s.buf, p...)
	return len(p), nil
}

// flush pushes buffered output to the destination. | flush 把缓冲输出推送到目标写入器。
func (s *encoderSink) flush() error {
	if len(s.buf) == 0 {
		return nil
	}
	n, err := s.dst.Write(s.buf)
	if err == nil && n < len(s.buf) {
		err = io.ErrShortWrite
	}
	s.buf = s.buf[:0]
	return err
}

// release returns the buffer to the pool. | release 把缓冲归还到池中。
func (s *encoderSink) release() {
	bufpool.Put(s.buf)
	s.buf = nil
	s.dst = nil
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
	v := strings.TrimSpace(headerValue(h, "Content-Length"))
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
	w.buf = appendBuffer(w.buf, p)
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
		_ = w.sink.flush()
	}
	if f, ok := w.rw.(http.Flusher); ok {
		f.Flush()
	}
}

// headersAllowCompression reports whether the current response headers forbid compression.
// | headersAllowCompression 报告当前响应头是否禁止压缩。
func (w *compressWriter) headersAllowCompression() bool {
	h := w.rw.Header()
	// Range responses must never be compressed: compressing a 206 leaves Content-Range describing
	// offsets into the original bytes while the body is a gzip stream, so clients and caches splice
	// garbage into resumed downloads. | Range 响应绝不能压缩：压缩 206 后 Content-Range 仍描述
	// 原始字节的区间，而正文已是 gzip 流，客户端与缓存会把垃圾拼进断点续传结果。
	if w.code == http.StatusPartialContent || headerValue(h, "Content-Range") != "" {
		return false
	}
	if headerValue(h, "Content-Encoding") != "" {
		return false
	}
	if ct := strings.ToLower(strings.TrimSpace(headerValue(h, "Content-Type"))); strings.HasPrefix(ct, "text/event-stream") {
		return false
	}
	if cc := strings.ToLower(headerValue(h, "Cache-Control")); strings.Contains(cc, "no-transform") {
		return false
	}
	if n, ok := contentLength(h); ok && n < w.minSize {
		return false
	}
	return true
}

// beginCompress negotiates the encoding, writes the header and starts the encoder, flushing the
// buffered body into it. | beginCompress 协商编码、写出响应头并启动编码器，把已缓冲的正文冲入其中。
func (w *compressWriter) beginCompress() {
	if w.decided {
		return
	}
	if !w.headersAllowCompression() {
		w.passThrough()
		return
	}
	algo, ok := Negotiate(w.accept, w.cfg.algos)
	if !ok {
		w.passThrough()
		return
	}
	w.algo = algo
	w.pool = w.cfg.pools[algo]
	w.sink.reset(w.rw)
	enc := w.pool.get(&w.sink)
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
		w.releaseBuf()
	}
}

// releaseBuf returns the pending body buffer to the pool. | releaseBuf 把待压缩正文缓冲归还到池中。
func (w *compressWriter) releaseBuf() {
	bufpool.Put(w.buf)
	w.buf = nil
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
		w.releaseBuf()
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

// finish closes the encoder, returns the pending buffer and the wrapper to their pools.
// | finish 关闭编码器，并把待压缩缓冲与包装器归还到各自的池中。
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
		_ = w.sink.flush()
		w.pool.put(w.enc)
		w.enc = nil
	}
	w.sink.release()
	w.releaseBuf()
	w.detach()
	releaseCompressWriter(w)
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
			return brotli.NewWriterLevel(w, defaultBrotliQuality)
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

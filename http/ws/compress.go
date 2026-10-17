package ws

import (
	"bytes"
	"io"

	"github.com/klauspost/compress/flate"

	"github.com/shibingli/yd/core/errx"
)

// deflateWindowSize is the 32 KiB sliding window used for context takeover. | deflateWindowSize 是上下文接管所用的 32 KiB 滑动窗口。
const deflateWindowSize = 32 << 10

// deflateTail is the empty stored-block trailer of RFC 7692 §7.2.1: the sender strips it and the receiver appends it back. | deflateTail 是 RFC 7692 §7.2.1 的空存储块尾部：发送方剥离、接收方补回。
var deflateTail = [4]byte{0x00, 0x00, 0xff, 0xff}

// messageCompressor is the per-connection outbound DEFLATE state. | messageCompressor 是每条连接出方向的 DEFLATE 状态。
//
// With context takeover the same flate.Writer is reused for every message and only flushed between | 启用上下文接管时，同一 flate.Writer 会跨消息复用，消息之间只做 flush；
// messages, so the LZ77 window survives (RFC 7692 §7.2.3.1). With no-context-takeover the writer is | LZ77 窗口因此得以保留（RFC 7692 §7.2.3.1）。禁用上下文接管时，
// reset before every message, which discards that window. | 每条消息前都会重置写出器，从而丢弃窗口。
type messageCompressor struct {
	w *flate.Writer
	// buf receives the compressed bytes of the current message. | buf 接收当前消息的压缩字节。
	buf bytes.Buffer
	// windowBits is the LZ77 window size (log2). | windowBits 是 LZ77 窗口大小（以 2 为底的对数）。
	windowBits int
	// noContextTakeover resets the writer for every message. | noContextTakeover 表示每条消息都重置写出器。
	noContextTakeover bool
}

// newMessageCompressor returns an uninitialized per-connection compressor. | newMessageCompressor 返回一个尚未初始化的按连接压缩器。
func newMessageCompressor(windowBits int, noContextTakeover bool) *messageCompressor {
	if windowBits < minWindowBits || windowBits > maxWindowBits {
		windowBits = defaultWindowBits
	}
	return &messageCompressor{windowBits: windowBits, noContextTakeover: noContextTakeover}
}

// compress compresses one message and returns the payload with the four-octet tail already stripped. | compress 压缩一条消息，返回已剥离 4 字节尾部的载荷。
//
// The returned slice is only valid until the next call, because it points into the reused buffer. | 返回的切片指向复用缓冲，仅在下一次调用前有效。
// Callers must invoke it under the connection write lock: the writer state is shared by the whole connection. | 调用方必须持有连接写锁：写出器状态由整条连接共享。
func (mc *messageCompressor) compress(p []byte) ([]byte, error) {
	if mc.w == nil {
		w, err := flate.NewWriterWindow(&mc.buf, 1<<mc.windowBits)
		if err != nil {
			return nil, errx.WrapDetail(err, ErrCompression,
				"failed to create the deflate writer | 创建 deflate 写出器失败")
		}
		mc.w = w
	}
	mc.buf.Reset()
	if mc.noContextTakeover {
		// Reset drops the LZ77 window and the hash tables, which is exactly what | Reset 丢弃 LZ77 窗口与哈希表，这正是
		// "no context takeover" means for the encoder (RFC 7692 §7.2.3.2). | 编码端的「不接管上下文」语义（RFC 7692 §7.2.3.2）。
		mc.w.Reset(&mc.buf)
	}
	if _, err := mc.w.Write(p); err != nil {
		return nil, errx.WrapDetail(err, ErrCompression,
			"failed to write compressed data | 写入压缩数据失败")
	}
	if err := mc.w.Flush(); err != nil {
		return nil, errx.WrapDetail(err, ErrCompression,
			"failed to flush compressed data | 刷出压缩数据失败")
	}
	out := mc.buf.Bytes()
	if len(out) < len(deflateTail) || !bytes.HasSuffix(out, deflateTail[:]) {
		// Flush always ends with the empty stored block; a mismatch means the codec state is corrupt. | Flush 必定以空存储块结尾；不匹配说明编解码状态已损坏。
		return nil, errx.Detail(ErrCompression,
			"deflate output does not end with the RFC 7692 tail | deflate 输出未以 RFC 7692 尾部结束")
	}
	return out[:len(out)-len(deflateTail)], nil
}

// framePayloadReader streams the raw payload of the current message across frames. | framePayloadReader 跨帧流式读取当前消息的原始载荷。
//
// It hides frame boundaries and interleaved control frames from the DEFLATE decoder, while | 它向 DEFLATE 解码器隐藏帧边界与穿插的控制帧，同时
// keeping the masking offset and c.msgLen bookkeeping of the normal read path. | 保持普通读路径的掩码偏移与 c.msgLen 记账。
type framePayloadReader struct {
	c *Conn
}

// Read implements io.Reader over the message's raw payload. | Read 在消息原始载荷上实现 io.Reader。
//
// It returns io.EOF only after the FIN frame has been drained; control frames encountered between | 只有在 FIN 帧被读尽后才返回 io.EOF；分片之间遇到的控制帧
// fragments are handled transparently. | 会被透明处理。
func (f *framePayloadReader) Read(p []byte) (int, error) {
	c := f.c
	for c.frameRemain == 0 {
		if c.frameFin {
			return 0, io.EOF
		}
		if err := c.nextDataFrame(&c.frameRemain); err != nil {
			return 0, err
		}
	}
	if len(p) == 0 {
		return 0, nil
	}
	n := int64(len(p))
	if n > c.frameRemain {
		n = c.frameRemain
	}
	read, err := io.ReadFull(c.reader(), p[:n])
	if read > 0 {
		if c.frameMasked {
			maskBytes(c.maskKey, int(c.frameOffset()), p[:read])
		}
		c.frameRemain -= int64(read)
		c.msgLen += int64(read)
	}
	if err != nil {
		final := err
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			final = ErrUnexpectedEOF
		}
		return read, c.setReadErr(final)
	}
	return read, nil
}

// deflateInput feeds the frame payload stream plus the RFC 7692 tail to the flate reader. | deflateInput 把帧载荷流与 RFC 7692 尾部一起喂给 flate 读取器。
//
// It implements flate.Reader (Read plus ReadByte), so the decoder uses it directly instead of | 它实现了 flate.Reader（Read 与 ReadByte），解码器因此直接使用它，
// wrapping it in a fresh bufio.Reader for every message. | 而不是为每条消息再包一层新的 bufio.Reader。
type deflateInput struct {
	raw *framePayloadReader
	// tailPos is the number of tail octets already delivered. | tailPos 是已交付的尾部字节数。
	tailPos int
}

// Read implements io.Reader. | Read 实现 io.Reader。
func (d *deflateInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n, err := d.raw.Read(p)
	if n > 0 {
		return n, nil
	}
	if err != nil && err != io.EOF {
		return 0, err
	}
	if err == nil {
		return 0, nil
	}
	if d.tailPos >= len(deflateTail) {
		return 0, io.EOF
	}
	n = copy(p, deflateTail[d.tailPos:])
	d.tailPos += n
	return n, nil
}

// ReadByte implements flate.Reader. | ReadByte 实现 flate.Reader。
func (d *deflateInput) ReadByte() (byte, error) {
	var b [1]byte
	for {
		n, err := d.Read(b[:])
		if n == 1 {
			return b[0], nil
		}
		if err != nil {
			return 0, err
		}
	}
}

// messageDecompressor is the per-connection inbound DEFLATE state. | messageDecompressor 是每条连接入方向的 DEFLATE 状态。
//
// For context takeover it keeps the last 32 KiB of decompressed output and installs it as the preset | 上下文接管时，它保留最近 32 KiB 解压输出，并在下一条消息开始时
// dictionary of the next message (RFC 7692 §7.2.3.1); the flate reader itself is reset per message but | 作为预置字典装入（RFC 7692 §7.2.3.1）；flate 读取器虽按消息重置，
// never rebuilt. | 但从不重建。
type messageDecompressor struct {
	r  io.ReadCloser
	in deflateInput
	// window holds the last deflateWindowSize decompressed bytes. | window 保存最近 deflateWindowSize 字节的解压输出。
	window []byte
	// noContextTakeover resets the dictionary for every message. | noContextTakeover 表示每条消息都重置字典。
	noContextTakeover bool
}

// newMessageDecompressor returns an uninitialized per-connection decompressor. | newMessageDecompressor 返回一个尚未初始化的按连接解压器。
func newMessageDecompressor(noContextTakeover bool) *messageDecompressor {
	return &messageDecompressor{noContextTakeover: noContextTakeover}
}

// begin prepares the decoder for one message and returns the stream to read decompressed bytes from. | begin 为一条消息准备解码器，返回读取解压字节的流。
func (md *messageDecompressor) begin(raw *framePayloadReader) (io.Reader, error) {
	md.in.raw = raw
	md.in.tailPos = 0
	var dict []byte
	if !md.noContextTakeover {
		dict = md.window
	}
	if md.r == nil {
		md.r = flate.NewReaderDict(&md.in, dict)
		return md.r, nil
	}
	if err := md.r.(flate.Resetter).Reset(&md.in, dict); err != nil {
		return nil, errx.WrapDetail(err, ErrCompression,
			"failed to reset the decompressor | 重置解压器失败")
	}
	return md.r, nil
}

// record keeps the trailing deflateWindowSize bytes of decompressed output. | record 保留最近 deflateWindowSize 字节的解压输出。
func (md *messageDecompressor) record(p []byte) {
	if md.noContextTakeover || len(p) == 0 {
		return
	}
	if md.window == nil {
		md.window = make([]byte, 0, deflateWindowSize)
	}
	if len(p) >= deflateWindowSize {
		md.window = append(md.window[:0], p[len(p)-deflateWindowSize:]...)
		return
	}
	if overflow := len(md.window) + len(p) - deflateWindowSize; overflow > 0 {
		md.window = md.window[:copy(md.window, md.window[overflow:])]
	}
	md.window = append(md.window, p...)
}

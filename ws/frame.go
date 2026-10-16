package ws

import (
	"encoding/binary"
	"io"
)

// frameHeader is a parsed frame header. | frameHeader 是解析后的帧头。
type frameHeader struct {
	fin     bool
	rsv1    bool
	rsv2    bool
	rsv3    bool
	opcode  OpCode
	masked  bool
	maskKey [4]byte
	length  int64
}

// readFrameHeader reads and parses a frame header from r. | readFrameHeader 从 r 读取并解析帧头。
//
// The supplied buf is used to hold the extended length and the mask key; it must be at least 8 bytes long so that no allocation is needed. | 传入的 buf 用于暂存扩展长度与掩码键，长度至少为 8，避免分配。
func readFrameHeader(r io.Reader, buf []byte) (frameHeader, error) {
	var h frameHeader
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return h, err
	}
	h.fin = head[0]&0x80 != 0
	h.rsv1 = head[0]&0x40 != 0
	h.rsv2 = head[0]&0x20 != 0
	h.rsv3 = head[0]&0x10 != 0
	h.opcode = OpCode(head[0] & 0x0f)
	h.masked = head[1]&0x80 != 0

	length := int64(head[1] & 0x7f)
	switch length {
	case 126:
		if _, err := io.ReadFull(r, buf[:2]); err != nil {
			return h, err
		}
		length = int64(binary.BigEndian.Uint16(buf[:2]))
	case 127:
		if _, err := io.ReadFull(r, buf[:8]); err != nil {
			return h, err
		}
		v := binary.BigEndian.Uint64(buf[:8])
		if v > 1<<62 {
			// RFC 6455 §5.2: the most significant bit must be 0, and the length must not exceed the implementation limit | RFC 6455 §5.2：最高位必须为 0，且长度不得超过实现上限
			return h, ErrMessageTooBig
		}
		length = int64(v)
	}
	h.length = length

	if h.masked {
		if _, err := io.ReadFull(r, h.maskKey[:]); err != nil {
			return h, err
		}
	}
	return h, nil
}

// writeFrameHeader writes a frame header into dst and returns the number of bytes written. | writeFrameHeader 把帧头写入 dst，返回写入字节数。
//
// dst must be at least maxHeaderSize bytes long; it usually comes from a stack array. | dst 需至少 maxHeaderSize 字节，通常来自栈上数组。
func writeFrameHeader(dst []byte, fin bool, opcode OpCode, masked bool, payloadLen int64) int {
	b0 := byte(opcode)
	if fin {
		b0 |= 0x80
	}
	dst[0] = b0

	switch {
	case payloadLen < 126:
		dst[1] = byte(payloadLen)
		if masked {
			dst[1] |= 0x80
		}
		return 2
	case payloadLen < 1<<16:
		dst[1] = 126
		if masked {
			dst[1] |= 0x80
		}
		binary.BigEndian.PutUint16(dst[2:4], uint16(payloadLen))
		return 4
	default:
		dst[1] = 127
		if masked {
			dst[1] |= 0x80
		}
		binary.BigEndian.PutUint64(dst[2:10], uint64(payloadLen))
		return 10
	}
}

// maskBytes applies the XOR mask transform with key to b, where pos is the offset of b[0] within the whole message. | maskBytes 以 key 对 b 做 XOR 掩码变换，pos 为 b[0] 在整条消息中的偏移。
//
// XOR is self-inverse: the same function both applies and removes the mask. | XOR 是自反的：同一函数既用于加掩码也用于解掩码。
// Chunks of length >= 8 are processed in 8-byte words (uint64) to reduce per-byte overhead. | 对长度 >= 8 的片段按 8 字节字（uint64）批量处理，减少逐字节开销。
func maskBytes(key [4]byte, pos int, b []byte) int {
	if len(b) == 0 {
		return 0
	}
	// first align to the 4-byte boundary | 先对齐到 4 字节边界
	if head := (4 - pos%4) % 4; head > 0 && head <= len(b) {
		for i := 0; i < head; i++ {
			b[i] ^= key[(pos+i)%4]
		}
		pos += head
		b = b[head:]
	} else if head > len(b) {
		for i := range b {
			b[i] ^= key[(pos+i)%4]
		}
		return len(b)
	}

	// process in 8-byte words | 批量处理 8 字节
	var kb [8]byte
	kb[0], kb[1], kb[2], kb[3] = key[0], key[1], key[2], key[3]
	kb[4], kb[5], kb[6], kb[7] = key[0], key[1], key[2], key[3]
	k := binary.LittleEndian.Uint64(kb[:])
	i := 0
	for ; i+8 <= len(b); i += 8 {
		v := binary.LittleEndian.Uint64(b[i : i+8])
		binary.LittleEndian.PutUint64(b[i:i+8], v^k)
	}
	for ; i < len(b); i++ {
		b[i] ^= key[(pos+i)%4]
	}
	return len(b)
}

// closePayload builds a close frame payload: a 2-byte status code followed by the reason text. | closePayload 构造关闭帧载荷：2 字节状态码 + 原因文本。
func closePayload(code CloseCode, text string) []byte {
	if code == CloseNoStatusReceived {
		return nil
	}
	buf := make([]byte, 2+len(text))
	binary.BigEndian.PutUint16(buf[:2], uint16(code))
	copy(buf[2:], text)
	return buf
}

// parseClosePayload parses a close frame payload. | parseClosePayload 解析关闭帧载荷。
func parseClosePayload(p []byte) (CloseCode, string, error) {
	switch {
	case len(p) == 0:
		return CloseNoStatusReceived, "", nil
	case len(p) == 1:
		return CloseProtocolError, "", ErrProtocol
	default:
		code := CloseCode(binary.BigEndian.Uint16(p[:2]))
		if !validCloseCode(code) {
			return CloseProtocolError, "", ErrProtocol
		}
		text := string(p[2:])
		if !utf8Valid([]byte(text)) {
			return CloseInvalidFramePayloadData, "", ErrInvalidUTF8
		}
		return code, text, nil
	}
}

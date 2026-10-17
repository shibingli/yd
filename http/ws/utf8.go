package ws

import (
	"encoding/binary"
	"unicode/utf8"
)

// utf8State is the cross-frame state of incremental UTF-8 validation. | utf8State 是增量 UTF-8 校验的跨帧状态。
//
// A text message may be fragmented and a multi-byte character may straddle a frame boundary, so utf8.Valid cannot simply be called | 文本消息可能被分片，一个多字节字符也可能跨帧边界，因此不能对每一帧
// on each frame on its own — the part of an unfinished sequence left at the end of the previous frame must be carried over. | 单独调用 utf8.Valid —— 必须携带上一帧末尾未完成序列的部分。
type utf8State struct {
	// partial holds the prefix of a multi-byte sequence that is not complete yet (at most 3 bytes). | partial 保存尚未凑齐的多字节序列前缀（最多 3 字节）。
	partial [4]byte
	// n is the number of bytes accumulated in partial. | n 为 partial 中已累积的字节数。
	n int
	// need is the total number of bytes the current sequence should reach. | need 为当前序列应达到的总字节数。
	need int
}

// reset clears the state and starts a new message. | reset 清空状态，开始一条新消息。
func (s *utf8State) reset() { s.n, s.need = 0, 0 }

// write validates a chunk of bytes incrementally and reports whether it is valid UTF-8. | write 增量校验一段字节，返回是否为合法 UTF-8。
//
// The validation strictly follows RFC 3629: overlong encodings are rejected, surrogate code points (U+D800–U+DFFF) are rejected, | 该校验严格遵循 RFC 3629：拒绝过长编码、拒绝代理区码点（U+D800–U+DFFF）、
// and code points beyond U+10FFFF are rejected. | 拒绝超过 U+10FFFF 的码点。
func (s *utf8State) write(p []byte) bool {
	if s.n == 0 {
		// Skip a leading pure-ASCII run in bulk: text messages are mostly ASCII,
		// and eight bytes per check avoid a branch per byte. The remainder keeps
		// the original per-byte state machine, so non-ASCII text pays no extra
		// check at every sequence boundary. | 批量跳过开头的纯 ASCII 段：文本消息以
		// ASCII 为主，一次检查八个字节可省去逐字节分支。剩余部分仍走原来的逐字节状态机，
		// 非 ASCII 文本因此不必在每个序列边界多做一次检查。
		i := 0
		for i+8 <= len(p) {
			if binary.LittleEndian.Uint64(p[i:])&0x8080808080808080 != 0 {
				break
			}
			i += 8
		}
		for i < len(p) && p[i] < 0x80 {
			i++
		}
		if i == len(p) {
			return true
		}
		p = p[i:]
	}
	for _, b := range p {
		if s.n == 0 {
			switch {
			case b < 0x80:
				continue
			case b&0xE0 == 0xC0:
				if b < 0xC2 { // overlong encoding (C0/C1) | 过长编码（C0/C1）
					return false
				}
				s.need = 2
			case b&0xF0 == 0xE0:
				s.need = 3
			case b&0xF8 == 0xF0:
				if b > 0xF4 { // beyond U+10FFFF | 超出 U+10FFFF
					return false
				}
				s.need = 4
			default:
				return false
			}
			s.partial[0] = b
			s.n = 1
			continue
		}
		if b&0xC0 != 0x80 { // a continuation byte must be 10xxxxxx | 续字节必须是 10xxxxxx
			return false
		}
		s.partial[s.n] = b
		s.n++
		if s.n < s.need {
			continue
		}
		// the sequence is complete; let the standard library make the final call (covering details such as surrogates) | 序列凑齐，用标准库做最终判定（覆盖代理区等细节）
		if !utf8.Valid(s.partial[:s.n]) {
			return false
		}
		s.n = 0
	}
	return true
}

// complete reports whether no half character is left over (used when a message ends). | complete 报告当前是否没有残留的半个字符（消息结束时使用）。
func (s *utf8State) complete() bool { return s.n == 0 }

// utf8Valid reports whether an entire byte slice is valid UTF-8. | utf8Valid 报告整段字节是否为合法 UTF-8。
func utf8Valid(p []byte) bool {
	var s utf8State
	return s.write(p) && s.complete()
}

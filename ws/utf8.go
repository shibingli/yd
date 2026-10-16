package ws

import "unicode/utf8"

// utf8State 是增量 UTF-8 校验的跨帧状态。
//
// 文本消息可能被分片，一个多字节字符也可能跨帧边界，因此不能对每一帧
// 单独调用 utf8.Valid —— 必须携带上一帧末尾未完成序列的部分。
type utf8State struct {
	// partial 保存尚未凑齐的多字节序列前缀（最多 3 字节）。
	partial [4]byte
	// n 为 partial 中已累积的字节数。
	n int
	// need 为当前序列应达到的总字节数。
	need int
}

// reset 清空状态，开始一条新消息。
func (s *utf8State) reset() { s.n, s.need = 0, 0 }

// write 增量校验一段字节，返回是否为合法 UTF-8。
//
// 该校验严格遵循 RFC 3629：拒绝过长编码、拒绝代理区码点（U+D800–U+DFFF）、
// 拒绝超过 U+10FFFF 的码点。
func (s *utf8State) write(p []byte) bool {
	for _, b := range p {
		if s.n == 0 {
			switch {
			case b < 0x80:
				continue
			case b&0xE0 == 0xC0:
				if b < 0xC2 { // 过长编码（C0/C1）
					return false
				}
				s.need = 2
			case b&0xF0 == 0xE0:
				s.need = 3
			case b&0xF8 == 0xF0:
				if b > 0xF4 { // 超出 U+10FFFF
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
		if b&0xC0 != 0x80 { // 续字节必须是 10xxxxxx
			return false
		}
		s.partial[s.n] = b
		s.n++
		if s.n < s.need {
			continue
		}
		// 序列凑齐，用标准库做最终判定（覆盖代理区等细节）
		if !utf8.Valid(s.partial[:s.n]) {
			return false
		}
		s.n = 0
	}
	return true
}

// complete 报告当前是否没有残留的半个字符（消息结束时使用）。
func (s *utf8State) complete() bool { return s.n == 0 }

// utf8Valid 报告整段字节是否为合法 UTF-8。
func utf8Valid(p []byte) bool {
	var s utf8State
	return s.write(p) && s.complete()
}

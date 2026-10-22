package cachex

import "encoding/binary"

// Wire envelope constants. | 线路信封常量。
const (
	// WireMagic marks a cache envelope; it lets a reader reject foreign payloads. | WireMagic 标记缓存信封，使读取方能拒绝外来载荷。
	WireMagic = "YD"
	// WireHeaderSize is the fixed envelope header length in bytes. | WireHeaderSize 是信封固定头的字节长度。
	WireHeaderSize = 11
	// FlagNone marks an ordinary value. | FlagNone 标记普通值。
	FlagNone uint8 = 0
	// FlagTombstone marks an explicit delete; readers treat it as a plain miss. | FlagTombstone 标记显式删除，读取方按普通未命中处理。
	FlagTombstone uint8 = 1 << 0
	// FlagNegative marks a cached "not found" answer; it distinguishes a negative entry from a delete. | FlagNegative 标记缓存的「不存在」答复，用于与删除区分。
	FlagNegative uint8 = 1 << 1
)

// WireEncode returns a new envelope containing version, flags and data. | WireEncode 返回包含 version、flags 与 data 的新信封。
//
// The envelope layout is fixed and mirrored by the Redis Lua scripts: | 信封布局固定，并与 Redis Lua 脚本保持一致：
//
//	[0:2]   "YD"
//	[2]     flags
//	[3:11]  version, big endian uint64
//	[11:]   data
func WireEncode(version uint64, flags uint8, data []byte) []byte {
	return WireAppend(make([]byte, 0, WireHeaderSize+len(data)), version, flags, data)
}

// WireAppend appends an envelope to dst and returns the extended slice. | WireAppend 把信封追加到 dst 并返回扩展后的切片。
//
// It is the allocation-free form used by the local write path. | 它是本地写路径使用的零分配形式。
func WireAppend(dst []byte, version uint64, flags uint8, data []byte) []byte {
	dst = append(dst, WireMagic[0], WireMagic[1], flags)
	var ver [8]byte
	binary.BigEndian.PutUint64(ver[:], version)
	dst = append(dst, ver[:]...)
	return append(dst, data...)
}

// WireDecode splits an envelope into its parts. | WireDecode 把信封拆分为各组成部分。
//
// data aliases raw: the caller must not keep it beyond the lifetime of raw and must not | data 与 raw 共享底层数组：调用方不得在其生命周期之外保留它，也不得
// mutate it. ok is false for a short, foreign or truncated payload. | 修改它。载荷过短、非本框架或截断时 ok 为 false。
func WireDecode(raw []byte) (version uint64, flags uint8, data []byte, ok bool) {
	if len(raw) < WireHeaderSize || raw[0] != WireMagic[0] || raw[1] != WireMagic[1] {
		return 0, 0, nil, false
	}
	return binary.BigEndian.Uint64(raw[3:WireHeaderSize]), raw[2], raw[WireHeaderSize:], true
}

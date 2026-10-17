//go:build b64x_native && goexperiment.simd && amd64

package b64x

import "simd/archsimd"

// init installs the widest available kernel: AVX-512 with VBMI/VBMI2 first, then AVX-512, then
// AVX2. | init 安装当前 CPU 支持的最宽内核：优先 AVX-512 + VBMI/VBMI2，其次 AVX-512，最后 AVX2。
func init() {
	avx512 := archsimd.X86.AVX512()
	switch {
	case avx512 && archsimd.X86.AVX512VBMI() && archsimd.X86.AVX512VBMI2():
		simdEncode = encodeSIMD512VBMI
		simdDecode = decodeSIMD512VBMI2
	case avx512:
		simdEncode = encodeSIMD512
		simdDecode = decodeSIMD512
	case archsimd.X86.AVX2():
		simdEncode = encodeSIMD
		simdDecode = decodeSIMD
	}
}

// encodeSIMD encodes complete 12-byte groups into 16 characters and returns the bytes consumed.
// It prefers 256-bit vectors and falls back to 128-bit vectors for the tail.
// | encodeSIMD 把完整的 12 字节组编码为 16 个字符并返回消耗的字节数；优先使用 256 位向量，
// 尾部回退到 128 位向量。
func encodeSIMD(dst, src []byte, url bool) int {
	var shufArr = [16]int8{1, 0, 2, 1, 4, 3, 5, 4, 7, 6, 8, 7, 10, 9, 11, 10}
	var maskLoArr = [16]uint16{0xfc00, 0x0fc0, 0xfc00, 0x0fc0, 0xfc00, 0x0fc0, 0xfc00, 0x0fc0, 0xfc00, 0x0fc0, 0xfc00, 0x0fc0, 0xfc00, 0x0fc0, 0xfc00, 0x0fc0}
	var maskHiArr = [16]uint16{0x03f0, 0x003f, 0x03f0, 0x003f, 0x03f0, 0x003f, 0x03f0, 0x003f, 0x03f0, 0x003f, 0x03f0, 0x003f, 0x03f0, 0x003f, 0x03f0, 0x003f}
	var mulHiArr = [16]uint16{0x0040, 0x0400, 0x0040, 0x0400, 0x0040, 0x0400, 0x0040, 0x0400, 0x0040, 0x0400, 0x0040, 0x0400, 0x0040, 0x0400, 0x0040, 0x0400}
	var mulLoArr = [16]uint16{0x0010, 0x0100, 0x0010, 0x0100, 0x0010, 0x0100, 0x0010, 0x0100, 0x0010, 0x0100, 0x0010, 0x0100, 0x0010, 0x0100, 0x0010, 0x0100}
	var lutArr = [32]int8{
		71, -4, -4, -4, -4, -4, -4, -4, -4, -4, -4, -19, -16, 65, 0, 0,
		71, -4, -4, -4, -4, -4, -4, -4, -4, -4, -4, -19, -16, 65, 0, 0,
	}

	shuffle := archsimd.LoadInt8x16Array(&shufArr)
	lut := archsimd.LoadInt8x16(lutArr[:16])
	maskLo8 := archsimd.LoadUint16x8(maskLoArr[:8])
	maskHi8 := archsimd.LoadUint16x8(maskHiArr[:8])
	mulHi8 := archsimd.LoadUint16x8(mulHiArr[:8])
	mulLo8 := archsimd.LoadUint16x8(mulLoArr[:8])

	lut32 := archsimd.LoadInt8x32(lutArr[:])
	maskLo16 := archsimd.LoadUint16x16(maskLoArr[:])
	maskHi16 := archsimd.LoadUint16x16(maskHiArr[:])
	mulHi16 := archsimd.LoadUint16x16(mulHiArr[:])
	mulLo16 := archsimd.LoadUint16x16(mulLoArr[:])
	zero32 := archsimd.BroadcastUint8x32(0)

	i, o := 0, 0
	for i+28 <= len(src) && o+32 <= len(dst) {
		g0 := archsimd.LoadUint8x16(src[i:]).PermuteOrZero(shuffle)
		g1 := archsimd.LoadUint8x16(src[i+12:]).PermuteOrZero(shuffle)
		in := zero32.SetLo(g0).SetHi(g1).AsUint16x16()
		idx := in.And(maskLo16).MulHigh(mulHi16).Or(in.And(maskHi16).Mul(mulLo16)).AsUint8x32()
		res := idx.SubSaturated(archsimd.BroadcastUint8x32(51))
		less := archsimd.BroadcastUint8x32(26).Greater(idx)
		res = archsimd.BroadcastUint8x32(13).IfElse(less, res)
		res = idx.Add(lut32.PermuteOrZeroGrouped(res.AsInt8x32()).AsUint8x32())
		if url {
			res = archsimd.BroadcastUint8x32('-').IfElse(idx.Equal(archsimd.BroadcastUint8x32(62)), res)
			res = archsimd.BroadcastUint8x32('_').IfElse(idx.Equal(archsimd.BroadcastUint8x32(63)), res)
		}
		res.Store(dst[o:])
		i += 24
		o += 32
	}
	for i+16 <= len(src) && o+16 <= len(dst) {
		in := archsimd.LoadUint8x16(src[i:]).PermuteOrZero(shuffle).AsUint16x8()
		idx := in.And(maskLo8).MulHigh(mulHi8).Or(in.And(maskHi8).Mul(mulLo8)).AsUint8x16()
		res := idx.SubSaturated(archsimd.BroadcastUint8x16(51))
		less := archsimd.BroadcastUint8x16(26).Greater(idx)
		res = archsimd.BroadcastUint8x16(13).IfElse(less, res)
		res = idx.Add(lut.PermuteOrZero(res.AsInt8x16()).AsUint8x16())
		if url {
			res = archsimd.BroadcastUint8x16('-').IfElse(idx.Equal(archsimd.BroadcastUint8x16(62)), res)
			res = archsimd.BroadcastUint8x16('_').IfElse(idx.Equal(archsimd.BroadcastUint8x16(63)), res)
		}
		res.Store(dst[o:])
		i += 12
		o += 16
	}
	return i
}

// decodeSIMD decodes complete 16-character groups into 12 bytes and returns the consumed prefix.
// It prefers 256-bit vectors, stops before padding, whitespace, or invalid input, and lets the
// scalar path finish. | decodeSIMD 把完整的 16 字符组解码为 12 字节并返回已处理的前缀；优先使用
// 256 位向量，遇到填充、空白或非法输入时停止，交给标量路径收尾。
func decodeSIMD(dst, src []byte, url bool) (int, int) {
	plus, slash := byte('+'), byte('/')
	if url {
		plus, slash = '-', '_'
	}
	var mulArr = [32]int8{64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1, 64, 1}
	var packArr = [16]int8{2, 1, 0, 6, 5, 4, 10, 9, 8, 14, 13, 12, 15, 15, 15, 15}
	mul32 := archsimd.LoadInt8x32(mulArr[:])
	mul16 := archsimd.LoadInt8x16(mulArr[:16])
	pack16 := archsimd.LoadInt8x16(packArr[:])

	upperA32 := archsimd.BroadcastUint8x32('A')
	upperZ32 := archsimd.BroadcastUint8x32('Z')
	lowerA32 := archsimd.BroadcastUint8x32('a')
	lowerZ32 := archsimd.BroadcastUint8x32('z')
	digit032 := archsimd.BroadcastUint8x32('0')
	digit932 := archsimd.BroadcastUint8x32('9')
	plus32 := archsimd.BroadcastUint8x32(plus)
	slash32 := archsimd.BroadcastUint8x32(slash)
	zero32 := archsimd.BroadcastUint8x32(0)

	upperA16 := archsimd.BroadcastUint8x16('A')
	upperZ16 := archsimd.BroadcastUint8x16('Z')
	lowerA16 := archsimd.BroadcastUint8x16('a')
	lowerZ16 := archsimd.BroadcastUint8x16('z')
	digit016 := archsimd.BroadcastUint8x16('0')
	digit916 := archsimd.BroadcastUint8x16('9')
	plus16 := archsimd.BroadcastUint8x16(plus)
	slash16 := archsimd.BroadcastUint8x16(slash)
	zero16 := archsimd.BroadcastUint8x16(0)

	i, o := 0, 0
	for i+32 <= len(src) && o+32 <= len(dst) {
		c := archsimd.LoadUint8x32(src[i:])
		upper := c.GreaterEqual(upperA32).And(c.LessEqual(upperZ32))
		lower := c.GreaterEqual(lowerA32).And(c.LessEqual(lowerZ32))
		digit := c.GreaterEqual(digit032).And(c.LessEqual(digit932))
		plusMask := c.Equal(plus32)
		slashMask := c.Equal(slash32)
		if upper.Or(lower).Or(digit).Or(plusMask).Or(slashMask).ToBits() != 0xFFFFFFFF {
			break
		}
		v := zero32
		v = c.Sub(upperA32).IfElse(upper, v)
		v = c.Sub(lowerA32).Add(archsimd.BroadcastUint8x32(26)).IfElse(lower, v)
		v = c.Sub(digit032).Add(archsimd.BroadcastUint8x32(52)).IfElse(digit, v)
		v = archsimd.BroadcastUint8x32(62).IfElse(plusMask, v)
		v = archsimd.BroadcastUint8x32(63).IfElse(slashMask, v)
		pairs := v.DotProductPairsSaturated(mul32).AsUint32x8()
		groups := pairs.And(archsimd.BroadcastUint32x8(0xFFFF)).ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x32()
		groups.GetLo().PermuteOrZero(pack16).Store(dst[o:])
		groups.GetHi().PermuteOrZero(pack16).Store(dst[o+12:])
		i += 32
		o += 24
	}
	for i+16 <= len(src) && o+16 <= len(dst) {
		c := archsimd.LoadUint8x16(src[i:])
		upper := c.GreaterEqual(upperA16).And(c.LessEqual(upperZ16))
		lower := c.GreaterEqual(lowerA16).And(c.LessEqual(lowerZ16))
		digit := c.GreaterEqual(digit016).And(c.LessEqual(digit916))
		plusMask := c.Equal(plus16)
		slashMask := c.Equal(slash16)
		if upper.Or(lower).Or(digit).Or(plusMask).Or(slashMask).ToBits() != 0xFFFF {
			break
		}
		v := zero16
		v = c.Sub(upperA16).IfElse(upper, v)
		v = c.Sub(lowerA16).Add(archsimd.BroadcastUint8x16(26)).IfElse(lower, v)
		v = c.Sub(digit016).Add(archsimd.BroadcastUint8x16(52)).IfElse(digit, v)
		v = archsimd.BroadcastUint8x16(62).IfElse(plusMask, v)
		v = archsimd.BroadcastUint8x16(63).IfElse(slashMask, v)
		pairs := v.DotProductPairsSaturated(mul16).AsUint32x4()
		groups := pairs.And(archsimd.BroadcastUint32x4(0xFFFF)).ShiftAllLeft(12).Or(pairs.ShiftAllRight(16))
		groups.AsUint8x16().PermuteOrZero(pack16).Store(dst[o:])
		i += 16
		o += 12
	}
	return i, o
}

// encodeSIMD512 encodes four 12-byte groups per 512-bit iteration, then hands the tail to the
// AVX2/128-bit kernel. | encodeSIMD512 每次 512 位迭代编码四个 12 字节组，尾部交给 AVX2/128 位内核。
func encodeSIMD512(dst, src []byte, url bool) int {
	if len(src) < 52 || len(dst) < 64 {
		return 0
	}
	var shufArr = [16]int8{1, 0, 2, 1, 4, 3, 5, 4, 7, 6, 8, 7, 10, 9, 11, 10}
	var maskLoArr [32]uint16
	var maskHiArr [32]uint16
	var mulHiArr [32]uint16
	var mulLoArr [32]uint16
	for j := range maskLoArr {
		if j&1 == 0 {
			maskLoArr[j], maskHiArr[j] = 0xfc00, 0x03f0
			mulHiArr[j], mulLoArr[j] = 0x0040, 0x0010
		} else {
			maskLoArr[j], maskHiArr[j] = 0x0fc0, 0x003f
			mulHiArr[j], mulLoArr[j] = 0x0400, 0x0100
		}
	}
	lutBase := [16]int8{71, -4, -4, -4, -4, -4, -4, -4, -4, -4, -4, -19, -16, 65, 0, 0}
	var lutArr [64]int8
	for j := range lutArr {
		lutArr[j] = lutBase[j&15]
	}

	shuffle := archsimd.LoadInt8x16(shufArr[:])
	maskLo := archsimd.LoadUint16x32(maskLoArr[:])
	maskHi := archsimd.LoadUint16x32(maskHiArr[:])
	mulHi := archsimd.LoadUint16x32(mulHiArr[:])
	mulLo := archsimd.LoadUint16x32(mulLoArr[:])
	lut := archsimd.LoadInt8x64(lutArr[:])
	zero32 := archsimd.BroadcastUint8x32(0)
	zero64 := archsimd.BroadcastUint8x64(0)
	i, o := 0, 0
	for i+52 <= len(src) && o+64 <= len(dst) {
		g0 := archsimd.LoadUint8x16(src[i:]).PermuteOrZero(shuffle)
		g1 := archsimd.LoadUint8x16(src[i+12:]).PermuteOrZero(shuffle)
		g2 := archsimd.LoadUint8x16(src[i+24:]).PermuteOrZero(shuffle)
		g3 := archsimd.LoadUint8x16(src[i+36:]).PermuteOrZero(shuffle)
		lo := zero32.SetLo(g0).SetHi(g1)
		hi := zero32.SetLo(g2).SetHi(g3)
		in := zero64.SetLo(lo).SetHi(hi).AsUint16x32()
		idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x64()
		res := idx.SubSaturated(archsimd.BroadcastUint8x64(51))
		less := archsimd.BroadcastUint8x64(26).Greater(idx)
		res = archsimd.BroadcastUint8x64(13).IfElse(less, res)
		res = idx.Add(lut.PermuteOrZeroGrouped(res.AsInt8x64()).AsUint8x64())
		if url {
			res = archsimd.BroadcastUint8x64('-').IfElse(idx.Equal(archsimd.BroadcastUint8x64(62)), res)
			res = archsimd.BroadcastUint8x64('_').IfElse(idx.Equal(archsimd.BroadcastUint8x64(63)), res)
		}
		res.Store(dst[o:])
		i += 48
		o += 64
	}
	if i == 0 {
		return 0
	}
	return i + encodeSIMD(dst[o:], src[i:], url)
}

// decodeSIMD512 decodes four 16-character groups per 512-bit iteration, then hands the tail to
// the AVX2/128-bit kernel. | decodeSIMD512 每次 512 位迭代解码四个 16 字符组，尾部交给
// AVX2/128 位内核。
func decodeSIMD512(dst, src []byte, url bool) (int, int) {
	if len(src) < 64 || len(dst) < 64 {
		return 0, 0
	}
	plus, slash := byte('+'), byte('/')
	if url {
		plus, slash = '-', '_'
	}
	var mulArr [64]int8
	for j := range mulArr {
		if j&1 == 0 {
			mulArr[j] = 64
		} else {
			mulArr[j] = 1
		}
	}
	var packArr = [16]int8{2, 1, 0, 6, 5, 4, 10, 9, 8, 14, 13, 12, 15, 15, 15, 15}
	mul := archsimd.LoadInt8x64(mulArr[:])
	pack := archsimd.LoadInt8x16(packArr[:])

	upperA := archsimd.BroadcastUint8x64('A')
	upperZ := archsimd.BroadcastUint8x64('Z')
	lowerA := archsimd.BroadcastUint8x64('a')
	lowerZ := archsimd.BroadcastUint8x64('z')
	digit0 := archsimd.BroadcastUint8x64('0')
	digit9 := archsimd.BroadcastUint8x64('9')
	plusB := archsimd.BroadcastUint8x64(plus)
	slashB := archsimd.BroadcastUint8x64(slash)
	zero := archsimd.BroadcastUint8x64(0)
	i, o := 0, 0
	for i+64 <= len(src) && o+64 <= len(dst) {
		c := archsimd.LoadUint8x64(src[i:])
		upper := c.GreaterEqual(upperA).And(c.LessEqual(upperZ))
		lower := c.GreaterEqual(lowerA).And(c.LessEqual(lowerZ))
		digit := c.GreaterEqual(digit0).And(c.LessEqual(digit9))
		plusMask := c.Equal(plusB)
		slashMask := c.Equal(slashB)
		if upper.Or(lower).Or(digit).Or(plusMask).Or(slashMask).ToBits() != ^uint64(0) {
			break
		}
		v := zero
		v = c.Sub(upperA).IfElse(upper, v)
		v = c.Sub(lowerA).Add(archsimd.BroadcastUint8x64(26)).IfElse(lower, v)
		v = c.Sub(digit0).Add(archsimd.BroadcastUint8x64(52)).IfElse(digit, v)
		v = archsimd.BroadcastUint8x64(62).IfElse(plusMask, v)
		v = archsimd.BroadcastUint8x64(63).IfElse(slashMask, v)
		pairs := v.DotProductPairsSaturated(mul).AsUint32x16()
		groups := pairs.And(archsimd.BroadcastUint32x16(0xFFFF)).ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x64()
		groups.GetLo().GetLo().PermuteOrZero(pack).Store(dst[o:])
		groups.GetLo().GetHi().PermuteOrZero(pack).Store(dst[o+12:])
		groups.GetHi().GetLo().PermuteOrZero(pack).Store(dst[o+24:])
		groups.GetHi().GetHi().PermuteOrZero(pack).Store(dst[o+36:])
		i += 64
		o += 48
	}
	if i == 0 {
		return 0, 0
	}
	ni, no := decodeSIMD(dst[o:], src[i:], url)
	return i + ni, o + no
}

// encodeSIMD512VBMI gathers four 12-byte groups with one VPERMB, then encodes them in 512-bit
// lanes. | encodeSIMD512VBMI 用一条 VPERMB 汇集四个 12 字节组，再在 512 位通道中编码。
func encodeSIMD512VBMI(dst, src []byte, url bool) int {
	if len(src) < 64 || len(dst) < 64 {
		return 0
	}
	pattern := [16]uint8{1, 0, 2, 1, 4, 3, 5, 4, 7, 6, 8, 7, 10, 9, 11, 10}
	var inArr [64]uint8
	for g := 0; g < 4; g++ {
		for j := 0; j < 16; j++ {
			inArr[g*16+j] = uint8(g*12) + pattern[j]
		}
	}
	var maskLoArr [32]uint16
	var maskHiArr [32]uint16
	var mulHiArr [32]uint16
	var mulLoArr [32]uint16
	for j := range maskLoArr {
		if j&1 == 0 {
			maskLoArr[j], maskHiArr[j] = 0xfc00, 0x03f0
			mulHiArr[j], mulLoArr[j] = 0x0040, 0x0010
		} else {
			maskLoArr[j], maskHiArr[j] = 0x0fc0, 0x003f
			mulHiArr[j], mulLoArr[j] = 0x0400, 0x0100
		}
	}
	lutBase := [16]int8{71, -4, -4, -4, -4, -4, -4, -4, -4, -4, -4, -19, -16, 65, 0, 0}
	var lutArr [64]int8
	for j := range lutArr {
		lutArr[j] = lutBase[j&15]
	}

	inShuf := archsimd.LoadUint8x64(inArr[:])
	maskLo := archsimd.LoadUint16x32(maskLoArr[:])
	maskHi := archsimd.LoadUint16x32(maskHiArr[:])
	mulHi := archsimd.LoadUint16x32(mulHiArr[:])
	mulLo := archsimd.LoadUint16x32(mulLoArr[:])
	lut := archsimd.LoadInt8x64(lutArr[:])
	i, o := 0, 0
	for i+64 <= len(src) && o+64 <= len(dst) {
		in := archsimd.LoadUint8x64(src[i:]).Permute(inShuf).AsUint16x32()
		idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x64()
		res := idx.SubSaturated(archsimd.BroadcastUint8x64(51))
		less := archsimd.BroadcastUint8x64(26).Greater(idx)
		res = archsimd.BroadcastUint8x64(13).IfElse(less, res)
		res = idx.Add(lut.PermuteOrZeroGrouped(res.AsInt8x64()).AsUint8x64())
		if url {
			res = archsimd.BroadcastUint8x64('-').IfElse(idx.Equal(archsimd.BroadcastUint8x64(62)), res)
			res = archsimd.BroadcastUint8x64('_').IfElse(idx.Equal(archsimd.BroadcastUint8x64(63)), res)
		}
		res.Store(dst[o:])
		i += 48
		o += 64
	}
	if i == 0 {
		return 0
	}
	return i + encodeSIMD(dst[o:], src[i:], url)
}

// decodeSIMD512VBMI2 validates and maps 64 characters at a time, then compacts the 48 valid
// bytes with VPCOMPRESSB. | decodeSIMD512VBMI2 每次校验并映射 64 个字符，再用 VPCOMPRESSB
// 压缩出 48 个有效字节。
func decodeSIMD512VBMI2(dst, src []byte, url bool) (int, int) {
	if len(src) < 64 || len(dst) < 64 {
		return 0, 0
	}
	plus, slash := byte('+'), byte('/')
	if url {
		plus, slash = '-', '_'
	}
	var mulArr [64]int8
	for j := range mulArr {
		if j&1 == 0 {
			mulArr[j] = 64
		} else {
			mulArr[j] = 1
		}
	}
	mul := archsimd.LoadInt8x64(mulArr[:])
	packMask := archsimd.Mask8x64FromBits(0x7777777777777777)

	upperA := archsimd.BroadcastUint8x64('A')
	upperZ := archsimd.BroadcastUint8x64('Z')
	lowerA := archsimd.BroadcastUint8x64('a')
	lowerZ := archsimd.BroadcastUint8x64('z')
	digit0 := archsimd.BroadcastUint8x64('0')
	digit9 := archsimd.BroadcastUint8x64('9')
	plusB := archsimd.BroadcastUint8x64(plus)
	slashB := archsimd.BroadcastUint8x64(slash)
	zero := archsimd.BroadcastUint8x64(0)
	i, o := 0, 0
	for i+64 <= len(src) && o+64 <= len(dst) {
		c := archsimd.LoadUint8x64(src[i:])
		upper := c.GreaterEqual(upperA).And(c.LessEqual(upperZ))
		lower := c.GreaterEqual(lowerA).And(c.LessEqual(lowerZ))
		digit := c.GreaterEqual(digit0).And(c.LessEqual(digit9))
		plusMask := c.Equal(plusB)
		slashMask := c.Equal(slashB)
		if upper.Or(lower).Or(digit).Or(plusMask).Or(slashMask).ToBits() != ^uint64(0) {
			break
		}
		v := zero
		v = c.Sub(upperA).IfElse(upper, v)
		v = c.Sub(lowerA).Add(archsimd.BroadcastUint8x64(26)).IfElse(lower, v)
		v = c.Sub(digit0).Add(archsimd.BroadcastUint8x64(52)).IfElse(digit, v)
		v = archsimd.BroadcastUint8x64(62).IfElse(plusMask, v)
		v = archsimd.BroadcastUint8x64(63).IfElse(slashMask, v)
		pairs := v.DotProductPairsSaturated(mul).AsUint32x16()
		groups := pairs.And(archsimd.BroadcastUint32x16(0xFFFF)).ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x64()
		groups.Compress(packMask).Store(dst[o:])
		i += 64
		o += 48
	}
	if i == 0 {
		return 0, 0
	}
	ni, no := decodeSIMD(dst[o:], src[i:], url)
	return i + ni, o + no
}

//go:build b64x_native && goexperiment.simd && amd64

package b64x

import (
	"simd/archsimd"
	"unsafe"
)

// SIMD nibble lookup tables for the standard alphabet. | 标准字母表的 SIMD 半字节查找表。
var (
	simdLoLUT   [64]int8
	simdHiLUT   [64]int8
	simdRollLUT [64]int8
)

// init installs the widest available kernel: AVX-512 with VBMI/VBMI2 first, then AVX-512, then
// AVX2. | init 安装当前 CPU 支持的最宽内核：优先 AVX-512 + VBMI/VBMI2，其次 AVX-512，最后 AVX2。
func init() {
	var loBase, hiBase, rollBase [16]int8
	for lo := 0; lo < 16; lo++ {
		v := int8(-128)
		if lo != 11 && lo != 15 {
			v |= 0x01
		}
		if lo >= 10 {
			v |= 0x02
		}
		if lo == 0 {
			v |= 0x04
		}
		if lo >= 11 {
			v |= 0x08
		}
		if lo == 0 {
			v |= 0x10
		}
		if lo >= 11 {
			v |= 0x20
		}
		loBase[lo] = v
	}
	for hi := 0; hi < 16; hi++ {
		hiBase[hi] = -128
	}
	for hi := 2; hi <= 7; hi++ {
		hiBase[hi] = int8(1 << uint(hi-2))
	}
	rollBase[3], rollBase[4], rollBase[5] = 4, -65, -65
	rollBase[6], rollBase[7] = -71, -71
	for j := 0; j < 64; j++ {
		simdLoLUT[j] = loBase[j&15]
		simdHiLUT[j] = hiBase[j&15]
		simdRollLUT[j] = rollBase[j&15]
	}
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
func encodeSIMD(dst, src []byte, url, padded bool, pad byte) (int, int) {
	if len(src) < 28 || len(dst) < 32 {
		return 0, 0
	}
	lut32 := archsimd.LoadInt8x32(simdLut32[:])
	maskLo16 := archsimd.LoadUint16x16(simdMaskLo16[:])
	maskHi16 := archsimd.LoadUint16x16(simdMaskHi16[:])
	mulHi16 := archsimd.LoadUint16x16(simdMulHi16[:])
	mulLo16 := archsimd.LoadUint16x16(simdMulLo16[:])
	b51x32, b13x32 := archsimd.BroadcastUint8x32(51), archsimd.BroadcastUint8x32(13)
	b26i32 := archsimd.BroadcastInt8x32(26)
	i, o := 0, 0
	// The first block uses two 16-byte loads; subsequent blocks load 32 bytes at i-4 and gather
	// both 12-byte groups with one lane-local shuffle. | 首块用两次 16 字节加载；后续块在 i-4
	// 处加载 32 字节，用一次通道内重排汇集两个 12 字节组。
	if i+28 <= len(src) && o+32 <= len(dst) {
		shuffle := archsimd.LoadInt8x16(simdShuf16[:])
		zero32 := archsimd.BroadcastUint8x32(0)
		g0 := archsimd.LoadUint8x16(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 16)).PermuteOrZero(shuffle)
		g1 := archsimd.LoadUint8x16(unsafe.Slice((*byte)(unsafe.Pointer(&src[i+12])), 16)).PermuteOrZero(shuffle)
		in := zero32.SetLo(g0).SetHi(g1).AsUint16x16()
		idx := in.And(maskLo16).MulHigh(mulHi16).Or(in.And(maskHi16).Mul(mulLo16)).AsUint8x32()
		res := idx.SubSaturated(b51x32)
		less := b26i32.Greater(idx.AsInt8x32())
		res = b13x32.IfElse(less, res)
		res = idx.Add(lut32.PermuteOrZeroGrouped(res.AsInt8x32()).AsUint8x32())
		if url {
			res = archsimd.BroadcastUint8x32('-').IfElse(idx.Equal(archsimd.BroadcastUint8x32(62)), res)
			res = archsimd.BroadcastUint8x32('_').IfElse(idx.Equal(archsimd.BroadcastUint8x32(63)), res)
		}
		res.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		i += 24
		o += 32
	}
	shuf32 := archsimd.LoadInt8x32(simdShuf32Gather[:])
	for i+52 <= len(src) && o+64 <= len(dst) {
		in0 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i-4])), 32)).PermuteOrZeroGrouped(shuf32).AsUint16x16()
		in1 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i+20])), 32)).PermuteOrZeroGrouped(shuf32).AsUint16x16()
		idx0 := in0.And(maskLo16).MulHigh(mulHi16).Or(in0.And(maskHi16).Mul(mulLo16)).AsUint8x32()
		idx1 := in1.And(maskLo16).MulHigh(mulHi16).Or(in1.And(maskHi16).Mul(mulLo16)).AsUint8x32()
		res0 := idx0.SubSaturated(b51x32)
		res1 := idx1.SubSaturated(b51x32)
		res0 = b13x32.IfElse(b26i32.Greater(idx0.AsInt8x32()), res0)
		res1 = b13x32.IfElse(b26i32.Greater(idx1.AsInt8x32()), res1)
		res0 = idx0.Add(lut32.PermuteOrZeroGrouped(res0.AsInt8x32()).AsUint8x32())
		res1 = idx1.Add(lut32.PermuteOrZeroGrouped(res1.AsInt8x32()).AsUint8x32())
		if url {
			res0 = archsimd.BroadcastUint8x32('-').IfElse(idx0.Equal(archsimd.BroadcastUint8x32(62)), res0)
			res0 = archsimd.BroadcastUint8x32('_').IfElse(idx0.Equal(archsimd.BroadcastUint8x32(63)), res0)
			res1 = archsimd.BroadcastUint8x32('-').IfElse(idx1.Equal(archsimd.BroadcastUint8x32(62)), res1)
			res1 = archsimd.BroadcastUint8x32('_').IfElse(idx1.Equal(archsimd.BroadcastUint8x32(63)), res1)
		}
		res0.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		res1.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o+32])), 32))
		i += 48
		o += 64
	}
	for i+28 <= len(src) && o+32 <= len(dst) {
		in := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i-4])), 32)).PermuteOrZeroGrouped(shuf32).AsUint16x16()
		idx := in.And(maskLo16).MulHigh(mulHi16).Or(in.And(maskHi16).Mul(mulLo16)).AsUint8x32()
		res := idx.SubSaturated(b51x32)
		less := b26i32.Greater(idx.AsInt8x32())
		res = b13x32.IfElse(less, res)
		res = idx.Add(lut32.PermuteOrZeroGrouped(res.AsInt8x32()).AsUint8x32())
		if url {
			res = archsimd.BroadcastUint8x32('-').IfElse(idx.Equal(archsimd.BroadcastUint8x32(62)), res)
			res = archsimd.BroadcastUint8x32('_').IfElse(idx.Equal(archsimd.BroadcastUint8x32(63)), res)
		}
		res.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		i += 24
		o += 32
	}
	shuffle := archsimd.LoadInt8x16(simdShuf16[:])
	zero32 := archsimd.BroadcastUint8x32(0)
	for rem := len(src) - i; rem > 0; {
		take := rem
		if take > 24 {
			take = 24
		}
		outLen := int(simdOutPadded[take])
		if !padded {
			outLen = int(simdOutRaw[take])
		}
		if o+outLen > len(dst) {
			break
		}
		g0, _ := archsimd.LoadUint8x16Part(src[i:])
		var g1 archsimd.Uint8x16
		if rem > 12 {
			g1, _ = archsimd.LoadUint8x16Part(src[i+12:])
		}
		g0 = g0.PermuteOrZero(shuffle)
		g1 = g1.PermuteOrZero(shuffle)
		in := zero32.SetLo(g0).SetHi(g1).AsUint16x16()
		idx := in.And(maskLo16).MulHigh(mulHi16).Or(in.And(maskHi16).Mul(mulLo16)).AsUint8x32()
		res := idx.SubSaturated(b51x32)
		less := b26i32.Greater(idx.AsInt8x32())
		res = b13x32.IfElse(less, res)
		res = idx.Add(lut32.PermuteOrZeroGrouped(res.AsInt8x32()).AsUint8x32())
		if url {
			res = archsimd.BroadcastUint8x32('-').IfElse(idx.Equal(archsimd.BroadcastUint8x32(62)), res)
			res = archsimd.BroadcastUint8x32('_').IfElse(idx.Equal(archsimd.BroadcastUint8x32(63)), res)
		}
		res.StorePart(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), outLen))
		if pads := int(simdPads[take]); padded && pads > 0 {
			dst[o+outLen-1] = pad
			if pads == 2 {
				dst[o+outLen-2] = pad
			}
		}
		i += take
		o += outLen
		rem -= take
	}
	return i, o
}

func decodeSIMD(dst, src []byte, url bool) (int, int) {
	if url {
		return decodeSIMDCompare(dst, src, true)
	}
	return decodeSIMDStd(dst, src)
}

// decodeSIMDStd decodes 32-character blocks with the nibble lookup tables. | decodeSIMDStd 使用
// 半字节查找表解码 32 字符块。
// decodeSIMDTail decodes a short padded tail with the nibble lookup tables. | decodeSIMDTail
// 使用半字节查找表解码较短的带填充尾部。
func decodeSIMDTail(dst, src []byte) (int, int) {
	loLUT := archsimd.LoadInt8x32(simdLoLUT[:32])
	hiLUT := archsimd.LoadInt8x32(simdHiLUT[:32])
	rollLUT := archsimd.LoadInt8x32(simdRollLUT[:32])
	mul32 := archsimd.LoadInt8x32(simdMul32[:])
	pack32 := archsimd.LoadInt8x32(simdPack32[:])
	zero := archsimd.BroadcastUint8x32(0)
	plus := archsimd.BroadcastUint8x32('+')
	slash := archsimd.BroadcastUint8x32('/')
	b62 := archsimd.BroadcastUint8x32(62)
	b63 := archsimd.BroadcastUint8x32(63)
	hiMask := archsimd.BroadcastUint16x16(0x0F0F)
	loMask := archsimd.BroadcastUint8x32(0x0F)
	eqChar := archsimd.BroadcastUint8x32('=')
	aChar := archsimd.BroadcastUint8x32('A')
	i, o := 0, 0
	for rem := len(src) - i; rem >= 4 && o+3 <= len(dst); {
		take := rem
		if take > 32 {
			take = 32
		}
		take &^= 3
		if take < 4 {
			break
		}
		c, _ := archsimd.LoadUint8x32Part(src[i:])
		eq := c.Equal(eqChar)
		c = aChar.IfElse(eq, c)
		hi := c.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		lo := c.And(loMask)
		loV := loLUT.PermuteOrZeroGrouped(lo.AsInt8x32()).AsUint8x32()
		hiV := hiLUT.PermuteOrZeroGrouped(hi.AsInt8x32()).AsUint8x32()
		need := ^uint32(0)
		if take < 32 {
			need = uint32(1)<<uint(take) - 1
		}
		if loV.And(hiV).Equal(zero).ToBits()&need != need {
			break
		}
		eqBits := eq.ToBits() & need
		padCount := 0
		switch eqBits {
		case 0:
		case uint32(1) << uint(take-1):
			padCount = 1
		case uint32(3) << uint(take-2):
			padCount = 2
		default:
			break
		}
		if eqBits != 0 && padCount == 0 {
			break
		}
		outLen := take/4*3 - padCount
		if o+outLen > len(dst) {
			break
		}
		v := c.Add(rollLUT.PermuteOrZeroGrouped(hi.AsInt8x32()).AsUint8x32())
		v = b62.IfElse(c.Equal(plus), v)
		v = b63.IfElse(c.Equal(slash), v)
		pairs := v.DotProductPairsSaturated(mul32).AsUint32x8()
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x32()
		packed := groups.PermuteOrZeroGrouped(pack32)
		out := unsafe.Pointer(&dst[o])
		if outLen <= 12 {
			packed.GetLo().StorePart(unsafe.Slice((*byte)(out), outLen))
		} else {
			packed.GetLo().StorePart(unsafe.Slice((*byte)(out), 12))
			packed.GetHi().StorePart(unsafe.Slice((*byte)(unsafe.Add(out, 12)), outLen-12))
		}
		i += take
		o += outLen
		rem -= take
	}
	return i, o
}

func decodeSIMDStd(dst, src []byte) (int, int) {
	if len(src) < 32 || len(dst) < 32 {
		return 0, 0
	}
	loLUT := archsimd.LoadInt8x32(simdLoLUT[:32])
	hiLUT := archsimd.LoadInt8x32(simdHiLUT[:32])
	rollLUT := archsimd.LoadInt8x32(simdRollLUT[:32])
	mul32 := archsimd.LoadInt8x32(simdMul32[:])
	pack32 := archsimd.LoadInt8x32(simdPack32[:])
	dwordIdx := archsimd.LoadUint32x8(simdPackDword[:])
	zero := archsimd.BroadcastUint8x32(0)
	plus := archsimd.BroadcastUint8x32('+')
	slash := archsimd.BroadcastUint8x32('/')
	b62 := archsimd.BroadcastUint8x32(62)
	b63 := archsimd.BroadcastUint8x32(63)
	hiMask := archsimd.BroadcastUint16x16(0x0F0F)
	loMask := archsimd.BroadcastUint8x32(0x0F)
	i, o := 0, 0
	for i+64 <= len(src) && o+64 <= len(dst) {
		c0 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 32))
		c1 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i+32])), 32))
		hi0 := c0.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		hi1 := c1.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		lo0 := c0.And(loMask)
		lo1 := c1.And(loMask)
		loV0 := loLUT.PermuteOrZeroGrouped(lo0.AsInt8x32()).AsUint8x32()
		loV1 := loLUT.PermuteOrZeroGrouped(lo1.AsInt8x32()).AsUint8x32()
		hiV0 := hiLUT.PermuteOrZeroGrouped(hi0.AsInt8x32()).AsUint8x32()
		hiV1 := hiLUT.PermuteOrZeroGrouped(hi1.AsInt8x32()).AsUint8x32()
		valid0 := loV0.And(hiV0).Equal(zero).ToBits()
		valid1 := loV1.And(hiV1).Equal(zero).ToBits()
		if valid0&valid1 != 0xFFFFFFFF {
			break
		}
		v0 := c0.Add(rollLUT.PermuteOrZeroGrouped(hi0.AsInt8x32()).AsUint8x32())
		v1 := c1.Add(rollLUT.PermuteOrZeroGrouped(hi1.AsInt8x32()).AsUint8x32())
		v0 = b62.IfElse(c0.Equal(plus), v0)
		v0 = b63.IfElse(c0.Equal(slash), v0)
		v1 = b62.IfElse(c1.Equal(plus), v1)
		v1 = b63.IfElse(c1.Equal(slash), v1)
		p0 := v0.DotProductPairsSaturated(mul32).AsUint32x8()
		p1 := v1.DotProductPairsSaturated(mul32).AsUint32x8()
		g0 := p0.ShiftAllLeft(12).Or(p0.ShiftAllRight(16)).AsUint8x32()
		g1 := p1.ShiftAllLeft(12).Or(p1.ShiftAllRight(16)).AsUint8x32()
		pk0 := g0.PermuteOrZeroGrouped(pack32).AsUint32x8().Permute(dwordIdx).AsUint8x32()
		pk1 := g1.PermuteOrZeroGrouped(pack32).AsUint32x8().Permute(dwordIdx).AsUint8x32()
		pk0.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		pk1.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o+24])), 32))
		i += 64
		o += 48
	}
	for i+32 <= len(src) && o+32 <= len(dst) {
		c := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 32))
		hi := c.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		lo := c.And(loMask)
		loV := loLUT.PermuteOrZeroGrouped(lo.AsInt8x32()).AsUint8x32()
		hiV := hiLUT.PermuteOrZeroGrouped(hi.AsInt8x32()).AsUint8x32()
		if loV.And(hiV).Equal(zero).ToBits() != 0xFFFFFFFF {
			break
		}
		v := c.Add(rollLUT.PermuteOrZeroGrouped(hi.AsInt8x32()).AsUint8x32())
		v = b62.IfElse(c.Equal(plus), v)
		v = b63.IfElse(c.Equal(slash), v)
		pairs := v.DotProductPairsSaturated(mul32).AsUint32x8()
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x32()
		packed := groups.PermuteOrZeroGrouped(pack32).AsUint32x8().Permute(dwordIdx).AsUint8x32()
		packed.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		i += 32
		o += 24
	}
	ti, to := decodeSIMDTail(dst[o:], src[i:])
	i += ti
	o += to
	if i == 0 {
		return 0, 0
	}
	ni, no := decodeSIMDCompare(dst[o:], src[i:], false)
	return i + ni, o + no
}

func decodeSIMDCompare(dst, src []byte, url bool) (int, int) {
	if len(src) < 32 || len(dst) < 32 {
		return 0, 0
	}
	plus, slash := byte('+'), byte('/')
	if url {
		plus, slash = '-', '_'
	}
	mul32 := archsimd.LoadInt8x32(simdMul32[:])
	mul16 := archsimd.LoadInt8x16(simdMul16[:])
	pack16 := archsimd.LoadInt8x16(simdPack16[:])

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
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x32()
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
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16))
		groups.AsUint8x16().PermuteOrZero(pack16).Store(dst[o:])
		i += 16
		o += 12
	}
	return i, o
}

// encodeSIMD512 encodes four 12-byte groups per 512-bit iteration, then hands the tail to the
// AVX2/128-bit kernel. | encodeSIMD512 每次 512 位迭代编码四个 12 字节组，尾部交给 AVX2/128 位内核。
func encodeSIMD512(dst, src []byte, url, padded bool, pad byte) (int, int) {
	if len(src) < 52 || len(dst) < 64 {
		return 0, 0
	}
	shuffle := archsimd.LoadInt8x16(simdShuf16[:])
	maskLo := archsimd.LoadUint16x32(simdMaskLo32[:])
	maskHi := archsimd.LoadUint16x32(simdMaskHi32[:])
	mulHi := archsimd.LoadUint16x32(simdMulHi32[:])
	mulLo := archsimd.LoadUint16x32(simdMulLo32[:])
	lut := archsimd.LoadInt8x64(simdLut64[:])
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
		return 0, 0
	}
	n, out := encodeSIMD(dst[o:], src[i:], url, padded, pad)
	return i + n, i/3*4 + out
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
	mul := archsimd.LoadInt8x64(simdMul64[:])
	pack := archsimd.LoadInt8x16(simdPack16[:])

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
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x64()
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
func encodeSIMD512VBMI(dst, src []byte, url, padded bool, pad byte) (int, int) {
	if len(src) < 1 || len(dst) < 2 {
		return 0, 0
	}
	inShuf := archsimd.LoadUint8x64(simdInShuf64[:])
	maskLo := archsimd.LoadUint16x32(simdMaskLo32[:])
	maskHi := archsimd.LoadUint16x32(simdMaskHi32[:])
	mulHi := archsimd.LoadUint16x32(simdMulHi32[:])
	mulLo := archsimd.LoadUint16x32(simdMulLo32[:])
	lut := archsimd.LoadInt8x64(simdLut64[:])
	b51, b26, b13 := simdB51x64, simdB26x64, simdB13x64
	b62, b63, dash, under := simdB62x64, simdB63x64, simdDashx64, simdUnderx64
	i, o := 0, 0
	for i+64 <= len(src) && o+64 <= len(dst) {
		in := archsimd.LoadUint8x64(src[i:]).Permute(inShuf).AsUint16x32()
		idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x64()
		res := idx.SubSaturated(b51)
		less := b26.Greater(idx)
		res = b13.IfElse(less, res)
		res = idx.Add(lut.PermuteOrZeroGrouped(res.AsInt8x64()).AsUint8x64())
		if url {
			res = dash.IfElse(idx.Equal(b62), res)
			res = under.IfElse(idx.Equal(b63), res)
		}
		res.Store(dst[o:])
		i += 48
		o += 64
	}
	for rem := len(src) - i; rem > 0; {
		take := rem
		if take > 48 {
			take = 48
		}
		outLen := int(simdOutPadded[take])
		if !padded {
			outLen = int(simdOutRaw[take])
		}
		if o+outLen > len(dst) {
			break
		}
		in, _ := archsimd.LoadUint8x64Part(src[i:])
		v := in.Permute(inShuf).AsUint16x32()
		idx := v.And(maskLo).MulHigh(mulHi).Or(v.And(maskHi).Mul(mulLo)).AsUint8x64()
		res := idx.SubSaturated(b51)
		less := b26.Greater(idx)
		res = b13.IfElse(less, res)
		res = idx.Add(lut.PermuteOrZeroGrouped(res.AsInt8x64()).AsUint8x64())
		if url {
			res = dash.IfElse(idx.Equal(b62), res)
			res = under.IfElse(idx.Equal(b63), res)
		}
		res.StorePart(dst[o : o+outLen])
		if pads := int(simdPads[take]); padded && pads > 0 {
			dst[o+outLen-1] = pad
			if pads == 2 {
				dst[o+outLen-2] = pad
			}
		}
		i += take
		o += outLen
		rem -= take
	}
	return i, o
}

func decodeSIMD512VBMI2(dst, src []byte, url bool) (int, int) {
	if url {
		return decodeSIMD512(dst, src, true)
	}
	if len(src) < 16 || len(dst) < 16 {
		return 0, 0
	}
	mul := archsimd.LoadInt8x64(simdMul64[:])
	pack := archsimd.LoadInt8x64(simdPack64[:])
	loLUT := archsimd.LoadInt8x64(simdLoLUT[:])
	hiLUT := archsimd.LoadInt8x64(simdHiLUT[:])
	rollLUT := archsimd.LoadInt8x64(simdRollLUT[:])
	packMask := archsimd.Mask8x64FromBits(0x0FFF0FFF0FFF0FFF)
	zero, plus, slash := simdZeroX64, simdPlusX64, simdSlashX64
	eqChar, aChar := simdEqX64, simdAX64
	b62, b63 := simdB62x64, simdB63x64
	hiMask, loMask := simdHiMaskX32, simdLoMaskX64
	i, o := 0, 0
	for i+64 <= len(src) && o+64 <= len(dst) {
		c := archsimd.LoadUint8x64(src[i:])
		hi := c.AsUint16x32().ShiftAllRight(4).And(hiMask).AsUint8x64()
		lo := c.And(loMask)
		loV := loLUT.PermuteOrZeroGrouped(lo.AsInt8x64()).AsUint8x64()
		hiV := hiLUT.PermuteOrZeroGrouped(hi.AsInt8x64()).AsUint8x64()
		if loV.And(hiV).Equal(zero).ToBits() != ^uint64(0) {
			break
		}
		v := c.Add(rollLUT.PermuteOrZeroGrouped(hi.AsInt8x64()).AsUint8x64())
		v = b62.IfElse(c.Equal(plus), v)
		v = b63.IfElse(c.Equal(slash), v)
		pairs := v.DotProductPairsSaturated(mul).AsUint32x16()
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x64()
		groups.PermuteOrZeroGrouped(pack).Compress(packMask).Store(dst[o:])
		i += 64
		o += 48
	}
	for rem := len(src) - i; rem >= 16; {
		take := rem
		if take > 64 {
			take = 64
		}
		take &^= 3
		if take < 16 {
			break
		}
		c, _ := archsimd.LoadUint8x64Part(src[i:])
		eq := c.Equal(eqChar)
		c = aChar.IfElse(eq, c)
		hi := c.AsUint16x32().ShiftAllRight(4).And(hiMask).AsUint8x64()
		lo := c.And(loMask)
		loV := loLUT.PermuteOrZeroGrouped(lo.AsInt8x64()).AsUint8x64()
		hiV := hiLUT.PermuteOrZeroGrouped(hi.AsInt8x64()).AsUint8x64()
		need := ^uint64(0)
		if take < 64 {
			need = uint64(1)<<uint(take) - 1
		}
		if loV.And(hiV).Equal(zero).ToBits()&need != need {
			break
		}
		eqBits := eq.ToBits() & need
		padCount := 0
		switch eqBits {
		case 0:
		case uint64(1) << uint(take-1):
			padCount = 1
		case uint64(3) << uint(take-2):
			padCount = 2
		default:
			break
		}
		if eqBits != 0 && padCount == 0 {
			break
		}
		outLen := take/4*3 - padCount
		if o+outLen > len(dst) {
			break
		}
		v := c.Add(rollLUT.PermuteOrZeroGrouped(hi.AsInt8x64()).AsUint8x64())
		v = b62.IfElse(c.Equal(plus), v)
		v = b63.IfElse(c.Equal(slash), v)
		pairs := v.DotProductPairsSaturated(mul).AsUint32x16()
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x64()
		groups.PermuteOrZeroGrouped(pack).Compress(packMask).StorePart(dst[o : o+outLen])
		i += take
		o += outLen
		rem -= take
	}
	if i == 0 {
		return 0, 0
	}
	ni, no := decodeSIMD(dst[o:], src[i:], false)
	return i + ni, o + no
}

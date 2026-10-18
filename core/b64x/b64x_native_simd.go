//go:build b64x_native && goexperiment.simd && amd64

package b64x

import (
	"simd/archsimd"
	"unsafe"
)

// SIMD nibble lookup tables for the standard alphabet. | 标准字母表的 SIMD 半字节查找表。
var (
	simdLoLUT, simdLoLUTURL     [64]int8
	simdHiLUT, simdHiLUTURL     [64]int8
	simdRollLUT, simdRollLUTURL [64]int8
)

const (
	stdAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	urlAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

// init installs the widest available kernel: AVX-512 with VBMI/VBMI2 first, then AVX-512, then
// AVX2. | init 安装当前 CPU 支持的最宽内核：优先 AVX-512 + VBMI/VBMI2，其次 AVX-512，最后 AVX2。
func init() {
	buildDecodeLUTs(&simdLoLUT, &simdHiLUT, &simdRollLUT, stdAlphabet,
		[16]int8{2: 16, 3: 4, 4: -65, 5: -65, 6: -71, 7: -71})
	buildDecodeLUTs(&simdLoLUTURL, &simdHiLUTURL, &simdRollLUTURL, urlAlphabet,
		[16]int8{2: 17, 3: 4, 4: -65, 5: -65, 6: -71, 7: -71})
	selectKernels()
}

// selectKernels installs the widest kernel allowed by the CPU and the optional force tag.
// | selectKernels 安装 CPU 与可选强制标签允许的最宽内核。
func selectKernels() {
	avx512 := archsimd.X86.AVX512()
	vbmi := avx512 && archsimd.X86.AVX512VBMI() && archsimd.X86.AVX512VBMI2()
	switch forceLevel {
	case 1:
		if archsimd.X86.AVX2() {
			simdEncode, simdDecode = encodeSIMD, decodeSIMD
			return
		}
	case 2:
		if avx512 {
			simdEncode, simdDecode = encodeSIMD512, decodeSIMD512
			return
		}
	case 3:
		if vbmi {
			simdEncode, simdDecode = encodeSIMD512VBMI, decodeSIMD512VBMI2
			return
		}
	}
	switch {
	case vbmi:
		simdEncode = encodeSIMD512VBMI
		simdDecode = decodeSIMD512VBMI2
	case avx512:
		// The baseline 512-bit decoder loses to the AVX2 kernel on Xeons without VBMI2,
		// so keep the 512-bit encoder and the AVX2 decoder.
		// 无 VBMI2 的 Xeon 上基础 512 位解码慢于 AVX2 内核，因此编码保留 512 位、解码用 AVX2。
		simdEncode = encodeSIMD512
		simdDecode = decodeSIMD
	case archsimd.X86.AVX2():
		simdEncode = encodeSIMD
		simdDecode = decodeSIMD
	}
}

// alphabetHas reports whether alphabet contains c. | alphabetHas 报告 alphabet 是否包含 c。
func alphabetHas(alphabet string, c byte) bool {
	for i := 0; i < len(alphabet); i++ {
		if alphabet[i] == c {
			return true
		}
	}
	return false
}

// buildDecodeLUTs fills the nibble validation and roll tables for one alphabet.
// | buildDecodeLUTs 为一种字母表填充半字节校验表与偏移表。
func buildDecodeLUTs(loLUT, hiLUT, rollLUT *[64]int8, alphabet string, rollBase [16]int8) {
	var loBase, hiBase [16]int8
	for hi := 0; hi < 16; hi++ {
		hiBase[hi] = -128
		if hi >= 2 && hi <= 7 {
			hiBase[hi] = int8(1 << uint(hi-2))
		}
	}
	for lo := 0; lo < 16; lo++ {
		v := int8(-128)
		for hi := 2; hi <= 7; hi++ {
			if !alphabetHas(alphabet, byte(hi<<4|lo)) {
				v |= int8(1 << uint(hi-2))
			}
		}
		loBase[lo] = v
	}
	for j := 0; j < 64; j++ {
		loLUT[j] = loBase[j&15]
		hiLUT[j] = hiBase[j&15]
		rollLUT[j] = rollBase[j&15]
	}
}

// encodeSIMD encodes complete 12-byte groups into 16 characters and returns the bytes consumed.
// It prefers 256-bit vectors and falls back to 128-bit vectors for the tail.
// | encodeSIMD 把完整的 12 字节组编码为 16 个字符并返回消耗的字节数；优先使用 256 位向量，
// 尾部回退到 128 位向量。
// encodeBlocksAVX2 encodes complete 24-byte blocks. src must include the four-byte prefix
// before the first block. It returns the logical input bytes consumed and output bytes written.
// | encodeBlocksAVX2 编码完整的 24 字节块；src 必须包含首块之前的 4 字节前缀。返回消耗的逻辑
// 输入字节数与写入的输出字节数。
func encodeBlocksAVX2(dst, src []byte, url bool) (int, int) {
	lut := &simdLut32
	if url {
		lut = &simdLutURL32
	}
	lut32 := archsimd.LoadInt8x32(lut[:])
	maskLo16 := archsimd.LoadUint16x16(simdMaskLo16[:])
	maskHi16 := archsimd.LoadUint16x16(simdMaskHi16[:])
	mulHi16 := archsimd.LoadUint16x16(simdMulHi16[:])
	mulLo16 := archsimd.LoadUint16x16(simdMulLo16[:])
	b51x32, b13x32 := archsimd.BroadcastUint8x32(51), archsimd.BroadcastUint8x32(13)
	b26i32 := archsimd.BroadcastInt8x32(26)
	shuf32 := archsimd.LoadInt8x32(simdShuf32Gather[:])
	i, o := 0, 0
	for i+56 <= len(src) && o+64 <= len(dst) {
		in0 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 32)).PermuteOrZeroGrouped(shuf32).AsUint16x16()
		in1 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i+24])), 32)).PermuteOrZeroGrouped(shuf32).AsUint16x16()
		idx0 := in0.And(maskLo16).MulHigh(mulHi16).Or(in0.And(maskHi16).Mul(mulLo16)).AsUint8x32()
		idx1 := in1.And(maskLo16).MulHigh(mulHi16).Or(in1.And(maskHi16).Mul(mulLo16)).AsUint8x32()
		res0 := idx0.SubSaturated(b51x32)
		res1 := idx1.SubSaturated(b51x32)
		res0 = b13x32.IfElse(b26i32.Greater(idx0.AsInt8x32()), res0)
		res1 = b13x32.IfElse(b26i32.Greater(idx1.AsInt8x32()), res1)
		res0 = idx0.Add(lut32.PermuteOrZeroGrouped(res0.AsInt8x32()).AsUint8x32())
		res1 = idx1.Add(lut32.PermuteOrZeroGrouped(res1.AsInt8x32()).AsUint8x32())
		res0.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		res1.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o+32])), 32))
		i += 48
		o += 64
	}
	for i+32 <= len(src) && o+32 <= len(dst) {
		in := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 32)).PermuteOrZeroGrouped(shuf32).AsUint16x16()
		idx := in.And(maskLo16).MulHigh(mulHi16).Or(in.And(maskHi16).Mul(mulLo16)).AsUint8x32()
		res := idx.SubSaturated(b51x32)
		res = b13x32.IfElse(b26i32.Greater(idx.AsInt8x32()), res)
		res = idx.Add(lut32.PermuteOrZeroGrouped(res.AsInt8x32()).AsUint8x32())
		res.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		i += 24
		o += 32
	}
	return i, o
}

func encodeSIMD(dst, src []byte, url, padded bool, pad byte) (int, int) {
	if len(src) < 28 || len(dst) < 32 {
		return 0, 0
	}
	i, o := encodeFirstAVX2(dst, src, url)
	if len(src)-i >= 28 {
		n, out := encodeBlocksAVX2(dst[o:], src[i-4:], url)
		i += n
		o += out
	}
	n, out := encodeTailAVX2(dst[o:], src[i:], url, padded, pad)
	return i + n, o + out
}

// encodeFirstAVX2 encodes the first 12-byte group with 128-bit vectors and returns 12 input
// bytes and 16 output characters. | encodeFirstAVX2 用 128 位向量编码首组 12 字节，返回 12 字节
// 输入与 16 个输出字符。
func encodeFirstAVX2(dst, src []byte, url bool) (int, int) {
	lut := &simdLut16
	if url {
		lut = &simdLutURL16
	}
	lut16 := archsimd.LoadInt8x16(lut[:])
	maskLo := archsimd.LoadUint16x8(simdMaskLo8[:])
	maskHi := archsimd.LoadUint16x8(simdMaskHi8[:])
	mulHi := archsimd.LoadUint16x8(simdMulHi8[:])
	mulLo := archsimd.LoadUint16x8(simdMulLo8[:])
	b51x16, b13x16 := archsimd.BroadcastUint8x16(51), archsimd.BroadcastUint8x16(13)
	b26i16 := archsimd.BroadcastInt8x16(26)
	shuffle := archsimd.LoadInt8x16(simdShuf16[:])
	in := archsimd.LoadUint8x16(unsafe.Slice((*byte)(unsafe.Pointer(&src[0])), 16)).PermuteOrZero(shuffle).AsUint16x8()
	idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x16()
	res := idx.SubSaturated(b51x16)
	less := b26i16.Greater(idx.AsInt8x16())
	res = b13x16.IfElse(less, res)
	res = idx.Add(lut16.PermuteOrZero(res.AsInt8x16()).AsUint8x16())
	res.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[0])), 16))
	return 12, 16
}

// encodeTailAVX2 encodes the remaining groups with 128-bit masked loads and stores.
// | encodeTailAVX2 用 128 位掩码加载与存储编码剩余分组。
func encodeTailAVX2(dst, src []byte, url, padded bool, pad byte) (int, int) {
	lut := &simdLut16
	if url {
		lut = &simdLutURL16
	}
	lut16 := archsimd.LoadInt8x16(lut[:])
	maskLo := archsimd.LoadUint16x8(simdMaskLo8[:])
	maskHi := archsimd.LoadUint16x8(simdMaskHi8[:])
	mulHi := archsimd.LoadUint16x8(simdMulHi8[:])
	mulLo := archsimd.LoadUint16x8(simdMulLo8[:])
	b51x16, b13x16 := archsimd.BroadcastUint8x16(51), archsimd.BroadcastUint8x16(13)
	b26i16 := archsimd.BroadcastInt8x16(26)
	shuffle := archsimd.LoadInt8x16(simdShuf16[:])
	i, o := 0, 0
	for rem := len(src); rem > 0; {
		take := rem
		if take > 12 {
			take = 12
		}
		outLen := int(simdOutPadded[take])
		if !padded {
			outLen = int(simdOutRaw[take])
		}
		if o+outLen > len(dst) {
			break
		}
		g, _ := archsimd.LoadUint8x16Part(src[i:])
		in := g.PermuteOrZero(shuffle).AsUint16x8()
		idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x16()
		res := idx.SubSaturated(b51x16)
		less := b26i16.Greater(idx.AsInt8x16())
		res = b13x16.IfElse(less, res)
		res = idx.Add(lut16.PermuteOrZero(res.AsInt8x16()).AsUint8x16())
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

func decodeSIMD(dst, src []byte, url bool) (int, int) {
	if len(src) < 32 || len(dst) < 32 {
		return 0, 0
	}
	valid := validateSIMD(src, url)
	return convertSIMD(dst, src[:valid], url)
}

// validateSIMD returns the length of the leading run of full 32-character blocks that are valid
// for the chosen alphabet. | validateSIMD 返回开头连续、由完整 32 字符块构成且符合所选字母表的长度。
func validateSIMD(src []byte, url bool) int {
	loTable, hiTable := &simdLoLUT, &simdHiLUT
	if url {
		loTable, hiTable = &simdLoLUTURL, &simdHiLUTURL
	}
	loLUT := archsimd.LoadInt8x32(loTable[:32])
	hiLUT := archsimd.LoadInt8x32(hiTable[:32])
	hiMask := archsimd.BroadcastUint16x16(0x0F0F)
	loMask := archsimd.BroadcastUint8x32(0x0F)
	zero := archsimd.BroadcastUint8x32(0)
	i := 0
	for i+64 <= len(src) {
		c0 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 32))
		c1 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i+32])), 32))
		hi0 := c0.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		hi1 := c1.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		loV0 := loLUT.PermuteOrZeroGrouped(c0.And(loMask).AsInt8x32()).AsUint8x32()
		loV1 := loLUT.PermuteOrZeroGrouped(c1.And(loMask).AsInt8x32()).AsUint8x32()
		hiV0 := hiLUT.PermuteOrZeroGrouped(hi0.AsInt8x32()).AsUint8x32()
		hiV1 := hiLUT.PermuteOrZeroGrouped(hi1.AsInt8x32()).AsUint8x32()
		if loV0.And(hiV0).Equal(zero).ToBits()&loV1.And(hiV1).Equal(zero).ToBits() != 0xFFFFFFFF {
			break
		}
		i += 64
	}
	for i+32 <= len(src) {
		c := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 32))
		hi := c.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		loV := loLUT.PermuteOrZeroGrouped(c.And(loMask).AsInt8x32()).AsUint8x32()
		hiV := hiLUT.PermuteOrZeroGrouped(hi.AsInt8x32()).AsUint8x32()
		if loV.And(hiV).Equal(zero).ToBits() != 0xFFFFFFFF {
			break
		}
		i += 32
	}
	return i
}

// convertSIMD decodes a prefix that validateSIMD has accepted. | convertSIMD 解码 validateSIMD
// 已确认有效的前缀。
func convertSIMD(dst, src []byte, url bool) (int, int) {
	rollTable := &simdRollLUT
	eqChar, eqVal := byte('+'), byte(62)
	if url {
		rollTable = &simdRollLUTURL
		eqChar, eqVal = '_', 63
	}
	rollLUT := archsimd.LoadInt8x32(rollTable[:32])
	mul32 := archsimd.LoadInt8x32(simdMul32[:])
	pack32 := archsimd.LoadInt8x32(simdPack32[:])
	dwordIdx := archsimd.LoadUint32x8(simdPackDword[:])
	eqCharV := archsimd.BroadcastUint8x32(eqChar)
	eqValV := archsimd.BroadcastUint8x32(eqVal)
	hiMask := archsimd.BroadcastUint16x16(0x0F0F)
	i, o := 0, 0
	for i+64 <= len(src) && o+64 <= len(dst) {
		c0 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 32))
		hi0 := c0.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		v0 := c0.Add(rollLUT.PermuteOrZeroGrouped(hi0.AsInt8x32()).AsUint8x32())
		v0 = eqValV.IfElse(c0.Equal(eqCharV), v0)
		p0 := v0.DotProductPairsSaturated(mul32).AsUint32x8()
		g0 := p0.ShiftAllLeft(12).Or(p0.ShiftAllRight(16)).AsUint8x32()
		pk0 := g0.PermuteOrZeroGrouped(pack32).AsUint32x8().Permute(dwordIdx).AsUint8x32()
		pk0.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		c1 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i+32])), 32))
		hi1 := c1.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		v1 := c1.Add(rollLUT.PermuteOrZeroGrouped(hi1.AsInt8x32()).AsUint8x32())
		v1 = eqValV.IfElse(c1.Equal(eqCharV), v1)
		p1 := v1.DotProductPairsSaturated(mul32).AsUint32x8()
		g1 := p1.ShiftAllLeft(12).Or(p1.ShiftAllRight(16)).AsUint8x32()
		pk1 := g1.PermuteOrZeroGrouped(pack32).AsUint32x8().Permute(dwordIdx).AsUint8x32()
		pk1.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o+24])), 32))
		i += 64
		o += 48
	}
	for i+32 <= len(src) && o+32 <= len(dst) {
		c := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 32))
		hi := c.AsUint16x16().ShiftAllRight(4).And(hiMask).AsUint8x32()
		v := c.Add(rollLUT.PermuteOrZeroGrouped(hi.AsInt8x32()).AsUint8x32())
		v = eqValV.IfElse(c.Equal(eqCharV), v)
		pairs := v.DotProductPairsSaturated(mul32).AsUint32x8()
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x32()
		packed := groups.PermuteOrZeroGrouped(pack32).AsUint32x8().Permute(dwordIdx).AsUint8x32()
		packed.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		i += 32
		o += 24
	}
	return i, o
}

// encodeSIMD512 encodes four 12-byte groups per 512-bit iteration, then hands the tail to the
// AVX2/128-bit kernel. | encodeSIMD512 每次 512 位迭代编码四个 12 字节组，尾部交给 AVX2/128 位内核。
func encodeSIMD512(dst, src []byte, url, padded bool, pad byte) (int, int) {
	if len(src) < 52 || len(dst) < 64 {
		return encodeSIMD(dst, src, url, padded, pad)
	}
	i, o := encodeFirstAVX2(dst, src, url)
	n, out := encodeSIMD512Blocks(dst[o:], src[i-4:], url)
	i += n
	o += out
	n, out = encodeSIMD(dst[o:], src[i:], url, padded, pad)
	return i + n, o + out
}

// encodeSIMD512Blocks encodes complete 48-byte blocks with the baseline AVX-512 pipeline.
// src starts four bytes before the first logical block. | encodeSIMD512Blocks 用基础 AVX-512
// 流水线编码完整的 48 字节块；src 从首个逻辑块前四字节开始。
func encodeSIMD512Blocks(dst, src []byte, url bool) (int, int) {
	lutTable := &simdLut64
	if url {
		lutTable = &simdLutURL64
	}
	lut := archsimd.LoadInt8x64(lutTable[:])
	shuf32 := archsimd.LoadInt8x32(simdShuf32Gather[:])
	maskLo := archsimd.LoadUint16x32(simdMaskLo32[:])
	maskHi := archsimd.LoadUint16x32(simdMaskHi32[:])
	mulHi := archsimd.LoadUint16x32(simdMulHi32[:])
	mulLo := archsimd.LoadUint16x32(simdMulLo32[:])
	b51, b26, b13 := archsimd.BroadcastUint8x64(51), archsimd.BroadcastUint8x64(26), archsimd.BroadcastUint8x64(13)
	base := lut.AsUint8x64()
	i, o := 0, 0
	for i+56 <= len(src) && o+64 <= len(dst) {
		p0 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i])), 32)).PermuteOrZeroGrouped(shuf32)
		p1 := archsimd.LoadUint8x32(unsafe.Slice((*byte)(unsafe.Pointer(&src[i+24])), 32)).PermuteOrZeroGrouped(shuf32)
		in := base.SetLo(p0).SetHi(p1).AsUint16x32()
		idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x64()
		res := idx.SubSaturated(b51)
		less := b26.Greater(idx)
		res = b13.IfElse(less, res)
		res = idx.Add(lut.PermuteOrZeroGrouped(res.AsInt8x64()).AsUint8x64())
		res.Store(dst[o:])
		i += 48
		o += 64
	}
	return i, o
}

// decodeSIMD512 decodes four 16-character groups per 512-bit iteration, then hands the tail to
// the AVX2/128-bit kernel. | decodeSIMD512 每次 512 位迭代解码四个 16 字符组，尾部交给
// AVX2/128 位内核。
func decodeSIMD512(dst, src []byte, url bool) (int, int) {
	if len(src) < 64 || len(dst) < 64 {
		return decodeSIMD(dst, src, url)
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
	lutTable := &simdLut64
	if url {
		lutTable = &simdLutURL64
	}
	lut := archsimd.LoadInt8x64(lutTable[:])
	b51, b26, b13 := simdB51x64, simdB26x64, simdB13x64
	zero64 := archsimd.BroadcastUint8x64(0)
	if len(src) == 32 {
		outLen := int(simdOutPadded[32])
		if !padded {
			outLen = int(simdOutRaw[32])
		}
		if outLen > len(dst) {
			return 0, 0
		}
		in := zero64.SetLo(archsimd.LoadUint8x32(src)).Permute(inShuf).AsUint16x32()
		idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x64()
		res := idx.SubSaturated(b51)
		less := b26.Greater(idx)
		res = b13.IfElse(less, res)
		res = idx.Add(lut.PermuteOrZeroGrouped(res.AsInt8x64()).AsUint8x64())
		res.StorePart(dst[:outLen])
		if pads := int(simdPads[32]); padded && pads > 0 {
			dst[outLen-1] = pad
		}
		return 32, outLen
	}
	i, o := 0, 0
	for i+64 <= len(src) && o+64 <= len(dst) {
		in := archsimd.LoadUint8x64(src[i:]).Permute(inShuf).AsUint16x32()
		idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x64()
		res := idx.SubSaturated(b51)
		less := b26.Greater(idx)
		res = b13.IfElse(less, res)
		res = idx.Add(lut.PermuteOrZeroGrouped(res.AsInt8x64()).AsUint8x64())
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

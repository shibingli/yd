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
		// Large inputs use the 512-bit LUT decoder on Xeons without VBMI2; small inputs stay
		// on the AVX2 fused kernel, which wins at 32 B and 1 KiB.
		// | 无 VBMI2 的 Xeon 上，大输入用 512 位 LUT 解码；小输入仍走 AVX2 融合内核，
		// 它在 32 B 与 1 KiB 更快。
		simdEncode = encodeSIMD512
		simdDecode = decodeSIMD512
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
	b51x32, b13x32 := archsimd.LoadUint8x32(simdB51x32[:]), archsimd.LoadUint8x32(simdB13x32[:])
	b26i32 := archsimd.LoadInt8x32(simdB26i32[:])
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

// encodeSIMD encodes Base64 into dst with the AVX2 kernels and returns the input bytes consumed
// and the output bytes written. Exact 32-byte sources use the dedicated kernel; 28-51 byte
// sources use one 24-byte head plus the 128-bit tail; larger sources keep the 12-byte head so
// the block loop starts at the alignment that leaves the smallest tail. | encodeSIMD 用 AVX2
// 内核把 Base64 编码写入 dst，返回消耗的输入字节数与写入的输出字节数。精确 32 字节走专用
// 内核；28-51 字节用一个 24 字节首块加 128 位尾部；更大的输入保留 12 字节首块，使块循环
// 从尾部最小的对齐位置开始。
func encodeSIMD(dst, src []byte, url, padded bool, pad byte) (int, int) {
	if len(src) == 32 && len(dst) >= encoded32Len(padded) {
		return encode32AVX2(dst, src, url, padded, pad)
	}
	if len(src) < 28 || len(dst) < 32 {
		return 0, 0
	}
	if len(src) < 52 {
		i, o := encodeFirst24AVX2(dst, src, url)
		n, out := encodeTailAVX2(dst[o:], src[i:], url, padded, pad)
		return i + n, o + out
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

// encodeFirst24AVX2 encodes the first 24 bytes with one 256-bit block fed by two 16-byte
// loads, one for each 128-bit lane, and returns 24 input bytes and 32 characters. The first
// block is deliberately twice the width of the 12-byte 128-bit variant so that small inputs
// need fewer tail iterations. | encodeFirst24AVX2 用两次 16 字节加载（每个 128 位通道一次）
// 组成一个 256 位块编码前 24 字节，返回 24 字节输入与 32 个字符。首块刻意取 12 字节
// 128 位变体的两倍宽，使小输入需要更少的尾部迭代。
func encodeFirst24AVX2(dst, src []byte, url bool) (int, int) {
	lut := &simdLut32
	if url {
		lut = &simdLutURL32
	}
	lut32 := archsimd.LoadInt8x32(lut[:])
	maskLo := archsimd.LoadUint16x16(simdMaskLo16[:])
	maskHi := archsimd.LoadUint16x16(simdMaskHi16[:])
	mulHi := archsimd.LoadUint16x16(simdMulHi16[:])
	mulLo := archsimd.LoadUint16x16(simdMulLo16[:])
	b51 := archsimd.LoadUint8x32(simdB51x32[:])
	b13 := archsimd.LoadUint8x32(simdB13x32[:])
	b26 := archsimd.LoadInt8x32(simdB26i32[:])
	shuf := archsimd.LoadInt8x16(simdShuf16[:])
	g0 := archsimd.LoadUint8x16(unsafe.Slice((*byte)(unsafe.Pointer(&src[0])), 16))
	g1 := archsimd.LoadUint8x16(unsafe.Slice((*byte)(unsafe.Pointer(&src[12])), 16))
	in := archsimd.Uint8x32{}.SetLo(g0.PermuteOrZero(shuf)).SetHi(g1.PermuteOrZero(shuf)).AsUint16x16()
	idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x32()
	res := idx.SubSaturated(b51)
	res = b13.IfElse(b26.Greater(idx.AsInt8x32()), res)
	res = idx.Add(lut32.PermuteOrZeroGrouped(res.AsInt8x32()).AsUint8x32())
	res.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[0])), 32))
	return 24, 32
}

// encoded32Len returns the number of characters produced by encoding 32 bytes: 44 with
// padding and 43 without. | encoded32Len 返回编码 32 字节产生的字符数：带填充为 44，无填充为 43。
func encoded32Len(padded bool) int {
	if padded {
		return 44
	}
	return 43
}

// encode32AVX2 encodes exactly 32 bytes. The first 24 bytes go through one 256-bit block fed by
// two 16-byte loads; the last 8 bytes use a separate 128-bit constant set so the 256-bit set
// dies with the first block and no vector register spills. It writes 44 padded or 43 raw
// characters. | encode32AVX2 精确编码 32 字节：前 24 字节由两次 16 字节加载喂给一个 256 位块；
// 末尾 8 字节使用独立的 128 位常量集，使 256 位常量随首块结束而失效，任何向量寄存器都不会
// 溢出。它写入 44 个填充字符或 43 个原始字符。
func encode32AVX2(dst, src []byte, url, padded bool, pad byte) (int, int) {
	lut := &simdLut32
	if url {
		lut = &simdLutURL32
	}
	lut32 := archsimd.LoadInt8x32(lut[:])
	maskLo := archsimd.LoadUint16x16(simdMaskLo16[:])
	maskHi := archsimd.LoadUint16x16(simdMaskHi16[:])
	mulHi := archsimd.LoadUint16x16(simdMulHi16[:])
	mulLo := archsimd.LoadUint16x16(simdMulLo16[:])
	b51 := archsimd.LoadUint8x32(simdB51x32[:])
	b13 := archsimd.LoadUint8x32(simdB13x32[:])
	b26 := archsimd.LoadInt8x32(simdB26i32[:])
	shuf := archsimd.LoadInt8x16(simdShuf16[:])

	// First 24 bytes: lane 0 holds bytes 0-11 and lane 1 holds bytes 12-23. | 前 24 字节：
	// 低半 128 位放 0-11 字节，高半 128 位放 12-23 字节。
	g0 := archsimd.LoadUint8x16(unsafe.Slice((*byte)(unsafe.Pointer(&src[0])), 16))
	g1 := archsimd.LoadUint8x16(unsafe.Slice((*byte)(unsafe.Pointer(&src[12])), 16))
	in := archsimd.Uint8x32{}.SetLo(g0.PermuteOrZero(shuf)).SetHi(g1.PermuteOrZero(shuf)).AsUint16x16()
	idx := in.And(maskLo).MulHigh(mulHi).Or(in.And(maskHi).Mul(mulLo)).AsUint8x32()
	res := idx.SubSaturated(b51)
	res = b13.IfElse(b26.Greater(idx.AsInt8x32()), res)
	res = idx.Add(lut32.PermuteOrZeroGrouped(res.AsInt8x32()).AsUint8x32())
	res.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[0])), 32))

	// Last 8 bytes: a 12-byte group with zero-filled upper bytes; the fourth character is
	// overwritten by the padding character when needed. The 128-bit constants are loaded
	// separately so the 256-bit set above dies with the first block and nothing spills.
	// | 末尾 8 字节：按 12 字节组处理，高位补零；需要时把第四个字符覆盖为填充符。
	// 128 位常量单独加载，使上面的 256 位常量随首块结束而失效，避免任何栈溢出。
	tailLut := &simdLut16
	if url {
		tailLut = &simdLutURL16
	}
	lut16 := archsimd.LoadInt8x16(tailLut[:])
	tmaskLo := archsimd.LoadUint16x8(simdMaskLo8[:])
	tmaskHi := archsimd.LoadUint16x8(simdMaskHi8[:])
	tmulHi := archsimd.LoadUint16x8(simdMulHi8[:])
	tmulLo := archsimd.LoadUint16x8(simdMulLo8[:])
	t51, t13 := archsimd.LoadUint8x16(simdB51x16[:]), archsimd.LoadUint8x16(simdB13x16[:])
	t26 := archsimd.LoadInt8x16(simdB26i16[:])
	tshuf := archsimd.LoadInt8x16(simdShuf16[:])
	// Load the last 16 source bytes (always in bounds for 32-byte input) and byte-align the
	// final 8 to the low lane. LoadUint8x16Part would compile to a call and force every live
	// constant to spill around it. | 加载最后 16 个源字节（32 字节输入下必定在界内），
	// 并把末尾 8 字节按字节对齐到低半；LoadUint8x16Part 会编译成一次调用，迫使所有
	// 存活常量围绕它溢出到栈。
	tg := archsimd.LoadUint8x16(unsafe.Slice((*byte)(unsafe.Pointer(&src[16])), 16))
	tail := archsimd.BroadcastUint8x16(0).ConcatShiftBytesRight(tg, 8)
	tin := tail.PermuteOrZero(tshuf).AsUint16x8()
	tidx := tin.And(tmaskLo).MulHigh(tmulHi).Or(tin.And(tmaskHi).Mul(tmulLo)).AsUint8x16()
	tres := tidx.SubSaturated(t51)
	tres = t13.IfElse(t26.Greater(tidx.AsInt8x16()), tres)
	tres = tidx.Add(lut16.PermuteOrZero(tres.AsInt8x16()).AsUint8x16())
	outLen := 11
	if padded {
		outLen = 12
	}
	tres.StorePart(dst[32 : 32+outLen])
	if padded {
		dst[32+outLen-1] = pad
	}
	return 32, 32 + outLen
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
	b51x16, b13x16 := archsimd.LoadUint8x16(simdB51x16[:]), archsimd.LoadUint8x16(simdB13x16[:])
	b26i16 := archsimd.LoadInt8x16(simdB26i16[:])
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
	b51x16, b13x16 := archsimd.LoadUint8x16(simdB51x16[:]), archsimd.LoadUint8x16(simdB13x16[:])
	b26i16 := archsimd.LoadInt8x16(simdB26i16[:])
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

// decodeSIMD validates and converts 32-character blocks in a single pass, then hands the tail
// to decodeTailAVX2. Validation reuses the high-nibble vector that conversion needs, so the
// input is loaded once instead of once per pass. | decodeSIMD 在单遍中完成 32 字符块的校验与
// 转换，尾部交给 decodeTailAVX2。校验复用转换所需的高半字节向量，因此输入只加载一次，而
// 不是每遍各加载一次。
func decodeSIMD(dst, src []byte, url, raw bool) (int, int) {
	if len(src) < 32 || len(dst) < 32 {
		return 0, 0
	}
	loTable, hiTable, rollTable := &simdLoLUT, &simdHiLUT, &simdRollLUT
	eqChar, eqVal := byte('+'), byte(62)
	if url {
		loTable, hiTable, rollTable = &simdLoLUTURL, &simdHiLUTURL, &simdRollLUTURL
		eqChar, eqVal = '_', 63
	}
	loLUT := archsimd.LoadInt8x32(loTable[:32])
	hiLUT := archsimd.LoadInt8x32(hiTable[:32])
	rollLUT := archsimd.LoadInt8x32(rollTable[:32])
	loMask := archsimd.LoadUint8x32(simdLoMaskX32[:])
	hiMask := archsimd.LoadUint16x16(simdHiMaskX16[:])
	mul := archsimd.LoadInt8x32(simdMul32[:])
	pack := archsimd.LoadInt8x32(simdPack32[:])
	dwordIdx := archsimd.LoadUint32x8(simdPackDword[:])
	eqCharV := archsimd.BroadcastUint8x32(eqChar)
	eqValV := archsimd.BroadcastUint8x32(eqVal)
	zero := archsimd.BroadcastUint8x32(0)
	i, o := 0, 0
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
		v = eqValV.IfElse(c.Equal(eqCharV), v)
		pairs := v.DotProductPairsSaturated(mul).AsUint32x8()
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x32()
		packed := groups.PermuteOrZeroGrouped(pack).AsUint32x8().Permute(dwordIdx).AsUint8x32()
		packed.Store(unsafe.Slice((*byte)(unsafe.Pointer(&dst[o])), 32))
		i += 32
		o += 24
	}
	n, out := decodeTailAVX2(dst, src, i, o, url, raw)
	return i + n, o + out
}

// decodeTailAVX2 decodes the characters that follow the complete 32-character blocks, in
// chunks of at most 16 characters. It returns the characters consumed and bytes written; a
// chunk that must go to the scalar path stops the loop without being counted: invalid
// characters, padding in a non-final chunk, malformed padding, or fewer than four characters.
// Padding is only accepted in the final chunk and only when the decoding alphabet is padded.
// Constants are loaded once for all chunks, so the common 24-character remainder needs only
// one constant set instead of one per chunk.
//
// The conversion window is the 16 characters ending at this chunk's last complete quartet, so
// the chunk always starts on a 128-bit group boundary and the load never leaves src. Characters
// of the window that precede the chunk were decoded already and are rewritten with the same
// bytes; only the chunk's own bytes are counted.
// | decodeTailAVX2 按每块最多 16 字符解码完整 32 字符块之后的字符。返回消耗的字符数与写入
// 字节数；必须交给标量路径的块不会计入并终止循环：非法字符、非末尾块出现填充、填充格式
// 错误或不足四个字符。只有当该块位于末尾且解码字母表带填充时才接受填充符。常量对所有块
// 只加载一次，因此常见的 24 字符剩余量只需一套常量而不是每块一套。
//
// 转换窗口是本块最后一个完整四元组之前的 16 个字符，因此块总是从 128 位组边界开始，
// 加载永远不会越出 src。窗口中位于块之前的字符已经解码过，重写得到相同字节；只有块
// 自身的字节计入返回值。
func decodeTailAVX2(dst, src []byte, off, out int, url, raw bool) (int, int) {
	loTable, hiTable, rollTable := &simdLoLUT, &simdHiLUT, &simdRollLUT
	eqChar, eqVal := byte('+'), byte(62)
	if url {
		loTable, hiTable, rollTable = &simdLoLUTURL, &simdHiLUTURL, &simdRollLUTURL
		eqChar, eqVal = '_', 63
	}
	hiMask := archsimd.LoadUint16x8(simdHiMaskX8[:])
	loMask := archsimd.LoadUint8x16(simdLoMaskX16[:])
	loLUT := archsimd.LoadInt8x16(loTable[:16])
	hiLUT := archsimd.LoadInt8x16(hiTable[:16])
	roll := archsimd.LoadInt8x16(rollTable[:16])
	mul := archsimd.LoadInt8x16(simdMul16[:])
	pack := archsimd.LoadInt8x16(simdPack16[:])
	eqCharV := archsimd.BroadcastUint8x16(eqChar)
	eqValV := archsimd.BroadcastUint8x16(eqVal)
	zero := archsimd.BroadcastUint8x16(0)
	totalI, totalO := 0, 0
	for len(src)-off >= 4 && out < len(dst) {
		rem := len(src) - off
		take := rem
		final := true
		if take > 16 {
			take = 16
			final = false
		}
		quads := take &^ 3
		if quads == 0 {
			break
		}
		pads := 0
		if final && !raw && take&3 == 0 && src[off+take-1] == '=' {
			pads = 1
			if take >= 2 && src[off+take-2] == '=' {
				pads = 2
			}
		}

		end := off + quads
		start := end - 16
		if start < 0 {
			start = 0
		}
		ws := end - start - quads
		c := archsimd.LoadUint8x16(src[start : start+16])

		hi := c.AsUint16x8().ShiftAllRight(4).And(hiMask).AsUint8x16()
		lo := c.And(loMask)
		loV := loLUT.PermuteOrZero(lo.AsInt8x16()).AsUint8x16()
		hiV := hiLUT.PermuteOrZero(hi.AsInt8x16()).AsUint8x16()
		consumeBits := uint16(1)<<uint(quads) - 1
		dataBits := consumeBits << uint(ws)
		if pads > 0 {
			dataBits &^= (uint16(1)<<uint(pads) - 1) << uint(ws+quads-pads)
		}
		if loV.And(hiV).Equal(zero).ToBits()&dataBits != dataBits {
			break
		}

		v := c.Add(roll.PermuteOrZero(hi.AsInt8x16()).AsUint8x16())
		v = eqValV.IfElse(c.Equal(eqCharV), v)
		pairs := v.DotProductPairsSaturated(mul).AsUint32x4()
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16)).AsUint8x16()
		packed := groups.PermuteOrZero(pack)
		outLen := (quads>>2)*3 - pads
		po := (ws >> 2) * 3
		if outLen <= 0 || po > out || out-po+12 > len(dst) {
			break
		}
		packed.StorePart(dst[out-po : out-po+12])
		off += quads
		out += outLen
		totalI += quads
		totalO += outLen
		if quads < 16 {
			break
		}
	}
	return totalI, totalO
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

// decodeLUTThreshold is the character count above which the 512-bit LUT decoder wins over
// the AVX2 fused kernel on AVX-512 hosts without VBMI2. | decodeLUTThreshold 是无 VBMI2 的
// AVX-512 主机上 512 位 LUT 解码开始快于 AVX2 融合内核的字符数阈值。
const decodeLUTThreshold = 1 << 12

// decodeSIMD512 decodes 64 characters per iteration with 512-bit nibble lookups. The
// alphabet check reuses the same high-nibble vector as the value mapping, so input is read
// once; a cross-lane VPERMD gathers the twelve valid dwords of the sixteen 24-bit groups,
// which lets two exact stores write 48 bytes without any write past the decoded length.
// The tail is handed to the AVX2/128-bit kernel. | decodeSIMD512 每次迭代用 512 位半字节
// 查表解码 64 个字符。字母表校验复用取值映射所需的高半字节向量，因此输入只读取一次；
// 跨通道 VPERMD 汇集十六个 24 位组中的十二个有效双字，两次精确存储即可写入 48 字节，
// 不会写到解码长度之外。尾部交给 AVX2/128 位内核。
func decodeSIMD512(dst, src []byte, url, raw bool) (int, int) {
	if len(src) < 64 || len(dst) < 48 || len(src) < decodeLUTThreshold {
		return decodeSIMD(dst, src, url, raw)
	}
	loTable, hiTable, rollTable := &simdLoLUT, &simdHiLUT, &simdRollLUT
	eqChar, eqVal := byte('+'), byte(62)
	if url {
		loTable, hiTable, rollTable = &simdLoLUTURL, &simdHiLUTURL, &simdRollLUTURL
		eqChar, eqVal = '_', 63
	}
	loLUT := archsimd.LoadInt8x64(loTable[:])
	hiLUT := archsimd.LoadInt8x64(hiTable[:])
	rollLUT := archsimd.LoadInt8x64(rollTable[:])
	pack := archsimd.LoadInt8x64(simdPack64[:])
	mul := archsimd.LoadInt8x64(simdMul64[:])
	dwordIdx := archsimd.LoadUint32x16(simdPackDword64[:])
	eqCharV := archsimd.BroadcastUint8x64(eqChar)
	eqValV := archsimd.BroadcastUint8x64(eqVal)
	i, o := 0, 0
	for i+64 <= len(src) && o+48 <= len(dst) {
		c := archsimd.LoadUint8x64(src[i:])
		hi := c.AsUint16x32().ShiftAllRight(4).And(simdHiMaskX32).AsUint8x64()
		lo := c.And(simdLoMaskX64)
		loV := loLUT.PermuteOrZeroGrouped(lo.AsInt8x64()).AsUint8x64()
		hiV := hiLUT.PermuteOrZeroGrouped(hi.AsInt8x64()).AsUint8x64()
		if loV.And(hiV).Equal(simdZeroX64).ToBits() != ^uint64(0) {
			break
		}
		v := c.Add(rollLUT.PermuteOrZeroGrouped(hi.AsInt8x64()).AsUint8x64())
		v = eqValV.IfElse(c.Equal(eqCharV), v)
		pairs := v.DotProductPairsSaturated(mul).AsUint32x16()
		groups := pairs.ShiftAllLeft(12).Or(pairs.ShiftAllRight(16))
		// The 24-bit groups sit in the low three bytes of each dword; the lane-local pack
		// removes the fourth byte and VPERMD concatenates the twelve groups across lanes.
		// | 24 位组位于每个双字的低三字节；通道内 pack 去掉第四字节，VPERMD 再跨通道
		// 拼接十二个组。
		packed := groups.AsUint8x64().PermuteOrZeroGrouped(pack).AsUint32x16().Permute(dwordIdx).AsUint8x64()
		packed.GetLo().Store(dst[o : o+32])
		packed.GetHi().GetLo().Store(dst[o+32 : o+48])
		i += 64
		o += 48
	}
	if i == 0 {
		return 0, 0
	}
	ni, no := decodeSIMD(dst[o:], src[i:], url, raw)
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

func decodeSIMD512VBMI2(dst, src []byte, url, raw bool) (int, int) {
	if url {
		// The size-adaptive decoder keeps small URL payloads on the AVX2 fused kernel and
		// uses the validated 512-bit LUT kernel for large ones. | 尺寸自适应解码器让小 URL
		// 载荷走 AVX2 融合内核，大载荷走已验证的 512 位 LUT 内核。
		return decodeSIMD512(dst, src, true, raw)
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
	for i+64 <= len(src) && o+48 <= len(dst) {
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
		// Store exactly the 48 decoded bytes; a full 64-byte store would overhang
		// DecodedLen by 16 bytes when the caller provides spare capacity.
		// | 精确写入 48 个解码字节；调用方提供富余容量时，64 字节全宽存储会越过
		// DecodedLen 16 字节。
		groups.PermuteOrZeroGrouped(pack).Compress(packMask).StorePart(dst[o : o+48])
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
		need := ^uint64(0)
		if take < 64 {
			need = uint64(1)<<uint(take) - 1
		}
		eq := c.Equal(eqChar)
		eqBits := eq.ToBits() & need
		padCount := 0
		if eqBits != 0 {
			// Padding is only legal at the very end of a padded alphabet. Raw input, and
			// any chunk with data after the padding, belongs to the scalar path so it can
			// reject the input instead of decoding the '=' as a data character.
			// | 填充符只允许出现在带填充字母表的输入末尾。raw 输入以及填充之后仍有数据的
			// 块必须交给标量路径报错，而不是把 '=' 当成数据字符解码。
			if raw || i+take != len(src) {
				break
			}
			switch eqBits {
			case uint64(1) << uint(take-1):
				padCount = 1
			case uint64(3) << uint(take-2):
				padCount = 2
			}
			if padCount == 0 {
				break
			}
		}
		c = aChar.IfElse(eq, c)
		hi := c.AsUint16x32().ShiftAllRight(4).And(hiMask).AsUint8x64()
		lo := c.And(loMask)
		loV := loLUT.PermuteOrZeroGrouped(lo.AsInt8x64()).AsUint8x64()
		hiV := hiLUT.PermuteOrZeroGrouped(hi.AsInt8x64()).AsUint8x64()
		if loV.And(hiV).Equal(zero).ToBits()&need != need {
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
	ni, no := decodeSIMD(dst[o:], src[i:], false, raw)
	return i + ni, o + no
}

//go:build b64x_native && goexperiment.simd && amd64

package b64x

import "simd/archsimd"

// SIMD constant tables are built once at startup instead of on every call.
// SIMD 常量表在启动时构建一次，避免每次调用重复初始化。
var (
	simdShuf16 = [16]int8{1, 0, 2, 1, 4, 3, 5, 4, 7, 6, 8, 7, 10, 9, 11, 10}
	simdLut16  = [16]int8{71, -4, -4, -4, -4, -4, -4, -4, -4, -4, -4, -19, -16, 65, 0, 0}
	simdPack16 = [16]int8{2, 1, 0, 6, 5, 4, 10, 9, 8, 14, 13, 12, 15, 15, 15, 15}
	simdPack32 [32]int8

	simdMaskLo8, simdMaskHi8, simdMulHi8, simdMulLo8     [8]uint16
	simdMaskLo16, simdMaskHi16, simdMulHi16, simdMulLo16 [16]uint16
	simdMaskLo32, simdMaskHi32, simdMulHi32, simdMulLo32 [32]uint16

	simdLut32 [32]int8
	simdLut64 [64]int8

	simdMul16  [16]int8
	simdMul32  [32]int8
	simdMul64  [64]int8
	simdPack64 [64]int8

	simdInShuf64 [64]uint8

	simdOutPadded, simdOutRaw, simdPads [49]uint8

	simdB51x64, simdB26x64, simdB13x64, simdB62x64, simdB63x64   archsimd.Uint8x64
	simdDashx64, simdUnderx64, simdZeroX64                       archsimd.Uint8x64
	simdPlusX64, simdSlashX64, simdEqX64, simdAX64, simdThreeX64 archsimd.Uint8x64
	simdHiMaskX32                                                archsimd.Uint16x32
	simdLoMaskX64                                                archsimd.Uint8x64
)

func init() {
	for j := 0; j < 32; j++ {
		lo, hi, mh, ml := uint16(0x0fc0), uint16(0x003f), uint16(0x0400), uint16(0x0100)
		if j&1 == 0 {
			lo, hi, mh, ml = 0xfc00, 0x03f0, 0x0040, 0x0010
		}
		simdMaskLo32[j], simdMaskHi32[j], simdMulHi32[j], simdMulLo32[j] = lo, hi, mh, ml
		if j < 16 {
			simdMaskLo16[j], simdMaskHi16[j], simdMulHi16[j], simdMulLo16[j] = lo, hi, mh, ml
		}
		if j < 8 {
			simdMaskLo8[j], simdMaskHi8[j], simdMulHi8[j], simdMulLo8[j] = lo, hi, mh, ml
		}
		if j&1 == 0 {
			simdMul16[j&15], simdMul32[j] = 64, 64
		} else {
			simdMul16[j&15], simdMul32[j] = 1, 1
		}
		simdLut32[j] = simdLut16[j&15]
	}
	for j := 0; j < 64; j++ {
		if j&1 == 0 {
			simdMul64[j] = 64
		} else {
			simdMul64[j] = 1
		}
		simdLut64[j] = simdLut16[j&15]
		simdPack64[j] = simdPack16[j&15]
		if j < 32 {
			simdPack32[j] = simdPack16[j&15]
		}
	}
	if archsimd.X86.AVX512() {
		simdB51x64, simdB26x64, simdB13x64 = archsimd.BroadcastUint8x64(51), archsimd.BroadcastUint8x64(26), archsimd.BroadcastUint8x64(13)
		simdB62x64, simdB63x64 = archsimd.BroadcastUint8x64(62), archsimd.BroadcastUint8x64(63)
		simdDashx64, simdUnderx64 = archsimd.BroadcastUint8x64('-'), archsimd.BroadcastUint8x64('_')
		simdZeroX64 = archsimd.BroadcastUint8x64(0)
		simdPlusX64, simdSlashX64 = archsimd.BroadcastUint8x64('+'), archsimd.BroadcastUint8x64('/')
		simdEqX64, simdAX64 = archsimd.BroadcastUint8x64('='), archsimd.BroadcastUint8x64('A')
		simdThreeX64 = archsimd.BroadcastUint8x64(3)
		simdHiMaskX32 = archsimd.BroadcastUint16x32(0x0F0F)
		simdLoMaskX64 = archsimd.BroadcastUint8x64(0x0F)
	}
	for t := 1; t <= 48; t++ {
		simdOutPadded[t] = uint8((t + 2) / 3 * 4)
		simdOutRaw[t] = uint8((t*8 + 5) / 6)
		simdPads[t] = uint8((3 - t%3) % 3)
	}
	pattern := simdShuf16
	for g := 0; g < 4; g++ {
		for j := 0; j < 16; j++ {
			simdInShuf64[g*16+j] = uint8(g*12) + uint8(pattern[j])
		}
	}
}

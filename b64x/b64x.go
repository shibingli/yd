// Package b64x provides an adaptive Base64 codec abstraction layer that hides the differences between underlying implementations. | Package b64x 提供自适应的 Base64 编解码抽象层，隐藏底层实现的差异。
//
// Backend routing (determined by build tags, fully transparent to callers): | 后端分流（由构建标签决定，对调用方完全透明）：
//
//   - amd64 without the b64x_std tag: the SIMD-accelerated github.com/cristalhq/base64, | amd64 且未指定 b64x_std 标签：使用 SIMD 加速的 github.com/cristalhq/base64，
//     Backend() returns "base64x"; | Backend() 返回 "base64x"；
//   - other architectures (arm64/riscv64/...) or with the b64x_std tag set explicitly: the standard library | 其他架构（arm64/riscv64/...）或显式指定 b64x_std 标签：使用标准库
//     encoding/base64, Backend() returns "encoding/base64". | encoding/base64，Backend() 返回 "encoding/base64"。
//
// Both backends expose exactly the same symbols and semantics: the four encoding variants | 两个后端暴露完全相同的符号与语义：StdEncoding、RawStdEncoding、URLEncoding、
// StdEncoding, RawStdEncoding, URLEncoding and RawURLEncoding, plus the Encoding methods | RawURLEncoding 四个编码变体，以及 Encoding 的 Encode、Decode、EncodeToString、
// Encode, Decode, EncodeToString, DecodeString, EncodedLen, DecodedLen, AppendEncode and AppendDecode, | DecodeString、EncodedLen、DecodedLen、AppendEncode、AppendDecode 方法，
// with length calculations and encoding results byte-for-byte identical to encoding/base64. | 长度计算与编码结果均与 encoding/base64 逐字节一致。
//
// Zero copy: AppendEncode and AppendDecode reuse the underlying array of the destination buffer when its capacity is sufficient, | 零拷贝：AppendEncode、AppendDecode 在目标缓冲区容量充足时复用其底层数组，
// producing no new allocation; DecodeString produces results directly in an internal buffer. The base64x backend's | 不产生新的分配；DecodeString 只在内部缓冲区中直接产出结果。base64x 后端的
// Decode, needing to accommodate the SIMD decoder writing out whole blocks, goes through a pooled buffer (capacity limit | Decode 因为需要兼容 SIMD 解码器按整块写出的特性，会借助池化缓冲区（容量上限
// 64 KiB, not recycled beyond that) and copies once; the fallback backend writes directly into dst. Hot paths pursuing | 64 KiB，超出不回收）中转并拷贝一次；降级后端直接写入 dst。追求极致零拷贝的
// ultimate zero copy should prefer AppendDecode. | 热路径应优先使用 AppendDecode。
//
// Differences from encoding/base64 (both backends behave identically): | 与 encoding/base64 的差异（两个后端行为一致）：
//
//   - on decode failure Decode returns (0, err), DecodeString returns (nil, err) and | 解码失败时 Decode 返回 (0, err)、DecodeString 返回 (nil, err)、
//     AppendDecode returns dst unchanged; none of them hands a partial decode result to the caller; | AppendDecode 原样返回 dst，都不会把部分解码结果交给调用方；
//   - when the destination buffer is shorter than DecodedLen(len(src)) it returns ErrShortBuffer instead of panicking out of bounds; | 目标缓冲区长度小于 DecodedLen(len(src)) 时返回 ErrShortBuffer，而不是越界 panic；
//   - every illegal-data error can be matched with errors.Is(err, ErrInvalidData), | 一切非法数据的错误都可用 errors.Is(err, ErrInvalidData) 判定，
//     while the underlying error (cristalhq's ErrWrongData or the standard library's CorruptInputError) is kept for | 同时保留底层错误（cristalhq 的 ErrWrongData 或标准库的 CorruptInputError）供
//     further matching with errors.Is. | errors.Is 进一步匹配。
package b64x

import "github.com/yun-dev/yd/errx"

var (
	// ErrShortBuffer indicates the destination buffer is shorter than DecodedLen(len(src)) and cannot hold the decode result. | ErrShortBuffer 表示目标缓冲区长度小于 DecodedLen(len(src))，无法容纳解码结果。
	ErrShortBuffer = errx.New("b64x: destination buffer is too short | b64x：目标缓冲区过短")

	// ErrInvalidData indicates the input is not valid Base64 data; the underlying backend's original error is also annotated | ErrInvalidData 表示输入不是合法的 Base64 数据；底层后端的原始错误通过
	// with errx.Wrap, so it can be matched with errors.Is. | errx.Wrap 一并注解，可用 errors.Is 匹配。
	ErrInvalidData = errx.New("b64x: invalid Base64 data | b64x：非法的 Base64 数据")
)
